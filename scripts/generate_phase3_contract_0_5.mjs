#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const predecessorVersion = "0.4.0";
const contractVersion = "0.5.0";
const predecessorSchemaVersion = "1.3.0";
const schemaVersion = "1.4.0";
const contractIdentity = `tekroo.kernel.contracts/${contractVersion}`;
const predecessorRoot = path.join(repoRoot, `CONTRACTS/tekroo.kernel.contracts/${predecessorVersion}`);
const packageRoot = path.join(repoRoot, `CONTRACTS/${contractIdentity}`);
const manifestPath = path.join(packageRoot, "manifest.json");
const checksumPath = path.join(packageRoot, "manifest.sha256");

if (fs.existsSync(packageRoot)) throw new Error(`contract ${contractVersion} already exists; refusing in-place regeneration`);

const uuid7Pattern = "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$";
const shaPattern = "^[0-9a-f]{64}$";
const gitObjectPattern = "^[0-9a-f]{40,64}$";
const actorPattern = "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])-[1-9][0-9]{0,19}$";

function sortValue(value) {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortValue(value[key])]));
  return value;
}

const canonical = (value) => JSON.stringify(sortValue(value));
const pretty = (value) => `${JSON.stringify(sortValue(value), null, 2)}\n`;
const sha256 = (value) => crypto.createHash("sha256").update(value).digest("hex");
const read = (relativePath) => fs.readFileSync(path.join(repoRoot, relativePath));
const readJson = (relativePath) => JSON.parse(read(relativePath).toString("utf8"));

function writeJson(relativePath, value) {
  const absolute = path.join(packageRoot, relativePath);
  fs.mkdirSync(path.dirname(absolute), { recursive: true });
  fs.writeFileSync(absolute, pretty(value), { flag: "wx" });
}

function overwriteJson(relativePath, value) {
  fs.writeFileSync(path.join(packageRoot, relativePath), pretty(value));
}

function advanceString(value) {
  return value
    .replaceAll("tekroo.kernel.contracts/0.4.0", contractIdentity)
    .replaceAll("_1_3_0", "_1_4_0")
    .replaceAll("1.3.0", schemaVersion);
}

function advance(value) {
  if (typeof value === "string") return advanceString(value);
  if (Array.isArray(value)) return value.map(advance);
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([key, child]) => [advanceString(key), advance(child)]));
  return value;
}

const string = (options = {}) => ({ type: "string", ...options });
const integer = (options = {}) => ({ type: "integer", ...options });
const array = (items, options = {}) => ({ type: "array", items, ...options });
const object = (properties, required = Object.keys(properties)) => ({ type: "object", additionalProperties: false, properties, required });
const uuid = () => string({ pattern: uuid7Pattern });
const digest = () => string({ pattern: shaPattern });
const gitObject = () => string({ pattern: gitObjectPattern });
const actor = () => string({ pattern: actorPattern });
const nonempty = () => string({ minLength: 1, maxLength: 4096 });
const uuidList = (options = {}) => array(uuid(), { maxItems: 64, uniqueItems: true, ...options });
const stringList = (options = {}) => array(nonempty(), { maxItems: 64, uniqueItems: true, ...options });
const principal = () => object({ kind: string({ enum: ["ACTOR", "HUMAN", "SERVICE", "POLICY"] }), id: string({ minLength: 1, maxLength: 256 }) });
const policyPrincipal = () => object({ kind: string({ enum: ["POLICY"] }), id: string({ minLength: 1, maxLength: 256 }) });

function walk(directory, prefix = "") {
  const result = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) result.push(...walk(absolute, relative));
    else result.push(relative);
  }
  return result.sort();
}

const skipped = new Set([
  "manifest.json",
  "manifest.sha256",
  "compatibility/from-0.3.0.json",
  "runner/reference-runner.mjs",
  "runner/validate-package.mjs",
]);

for (const relativePath of walk(predecessorRoot)) {
  if (skipped.has(relativePath)) continue;
  if (relativePath.endsWith(".json")) writeJson(relativePath, advance(JSON.parse(fs.readFileSync(path.join(predecessorRoot, relativePath), "utf8"))));
  else {
    const target = path.join(packageRoot, relativePath);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, advanceString(fs.readFileSync(path.join(predecessorRoot, relativePath), "utf8")), { flag: "wx" });
  }
}

const corePath = "schemas/core.schema.json";
const core = readJson(`CONTRACTS/${contractIdentity}/${corePath}`);
core.$defs.AggregateRef.properties.kind.enum.push("release-plan");
core.$defs.AggregateRef.properties.kind.enum.sort();
overwriteJson(corePath, core);

const mergeItem = () => object({
  merge_id: uuid(),
  change_ref: string({ minLength: 1, maxLength: 512 }),
  head_commit: gitObject(),
  role: nonempty(),
});
const authorPrincipal = () => object({ kind: string({ enum: ["ACTOR", "HUMAN"] }), id: string({ minLength: 1, maxLength: 256 }) });
const releaseCommon = {
  release_plan_id: uuid(),
  story_id: uuid(),
  story_lifecycle_epoch: integer({ minimum: 1 }),
  expected_story_revision: integer({ minimum: 1 }),
  author: principal(),
  author_approval_event_id: uuid(),
  author_approval_revision: integer({ minimum: 1 }),
  release_policy_revision: integer({ minimum: 1 }),
  plan_digest: digest(),
  evidence_ids: uuidList({ minItems: 1 }),
};
const codeReleaseCreate = object({
  ...releaseCommon,
  release_mode: { const: "CODE" },
  repository_url: string({ minLength: 1, maxLength: 2048 }),
  base_ref: string({ minLength: 1, maxLength: 512 }),
  base_commit: gitObject(),
  ordered_merges: array(mergeItem(), { minItems: 1, maxItems: 64 }),
  merge_strategy: string({ enum: ["FF_ONLY_ORDERED"] }),
  git_version: string({ minLength: 1, maxLength: 256 }),
  conflict_policy: { const: "FAIL_NO_IMPROVISATION" },
  contract_manifest: { const: contractIdentity },
  manifest_sha256: digest(),
  required_profiles: array(string({ enum: ["contract-structure", "core-hermetic", "mongo-integration", "provider-e2e", "synthesized-merge"] }), { minItems: 1, maxItems: 8, uniqueItems: true }),
  expected_qualified_tree: gitObject(),
  execution_round_limit: integer({ minimum: 1, maximum: 1000 }),
});
const noCodeReleaseCreate = object({
  ...releaseCommon,
  release_mode: { const: "NO_RELEASE_REQUIRED" },
  no_release_reason: nonempty(),
});
const qualificationProperties = {
  release_plan_id: uuid(),
  expected_release_revision: integer({ minimum: 1 }),
  plan_digest: digest(),
  qualification_id: uuid(),
  qualified_base_commit: gitObject(),
  ordered_head_commits: array(gitObject(), { minItems: 1, maxItems: 64, uniqueItems: true }),
  qualified_tree_digest: gitObject(),
  contract_manifest: { const: contractIdentity },
  manifest_sha256: digest(),
  required_profiles: array(string({ enum: ["contract-structure", "core-hermetic", "mongo-integration", "provider-e2e", "synthesized-merge"] }), { minItems: 1, maxItems: 8, uniqueItems: true }),
  gate_definition_digest: digest(),
  toolchain_digest: digest(),
  dependency_lock_digest: digest(),
  artifact_digests: array(digest(), { minItems: 1, maxItems: 64, uniqueItems: true }),
  evidence_ids: uuidList({ minItems: 1 }),
};
const executionRequestProperties = {
  release_plan_id: uuid(),
  expected_release_revision: integer({ minimum: 1 }),
  plan_digest: digest(),
  merge_id: uuid(),
  attempt_id: uuid(),
  round: integer({ minimum: 1, maximum: 1000 }),
  provider_idempotency_key: string({ minLength: 1, maxLength: 256 }),
  evidence_ids: uuidList({ minItems: 1 }),
};
const resultCommon = {
  release_plan_id: uuid(),
  expected_release_revision: integer({ minimum: 1 }),
  plan_digest: digest(),
  merge_id: uuid(),
  attempt_id: uuid(),
  reasons: stringList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
  observed_at: string({ format: "date-time" }),
};
const definitiveResult = object({
  ...resultCommon,
  outcome: string({ enum: ["ALREADY_MERGED", "MERGED"] }),
  observed_base_commit: gitObject(),
  observed_head_commit: gitObject(),
  observed_tree_digest: gitObject(),
});
const failedResult = object({ ...resultCommon, outcome: { const: "FAILED" } });
const unknownResult = object({ ...resultCommon, outcome: { const: "UNKNOWN" } });
const reconciliationCommon = {
  release_plan_id: uuid(),
  expected_release_revision: integer({ minimum: 1 }),
  plan_digest: digest(),
  merge_id: uuid(),
  attempt_id: uuid(),
  reconciliation_id: uuid(),
  supersedes_result_event_id: uuid(),
  reasons: stringList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
  observed_at: string({ format: "date-time" }),
};
const mergedReconciliation = object({
  ...reconciliationCommon,
  provider_state: { const: "MERGED" },
  outcome: string({ enum: ["ALREADY_MERGED", "MERGED"] }),
  observed_base_commit: gitObject(),
  observed_head_commit: gitObject(),
  observed_tree_digest: gitObject(),
});
const failedReconciliation = object({
  ...reconciliationCommon,
  provider_state: string({ enum: ["CLOSED_UNMERGED", "MISSING", "OPEN"] }),
  outcome: { const: "FAILED" },
});
const unknownReconciliation = object({
  ...reconciliationCommon,
  provider_state: { const: "UNAVAILABLE" },
  outcome: { const: "UNKNOWN" },
});
const codeFinalize = object({
  release_plan_id: uuid(),
  expected_release_revision: integer({ minimum: 1 }),
  plan_digest: digest(),
  release_mode: { const: "CODE" },
  terminal_status: { const: "READY_FOR_ACCEPTANCE" },
  qualification_event_id: uuid(),
  result_event_ids: uuidList({ minItems: 1 }),
  qualified_tree_digest: gitObject(),
  provider_tree_digest: gitObject(),
  reasons: stringList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
});
const noCodeFinalize = object({
  release_plan_id: uuid(),
  expected_release_revision: integer({ minimum: 1 }),
  plan_digest: digest(),
  release_mode: { const: "NO_RELEASE_REQUIRED" },
  terminal_status: { const: "NO_RELEASE_REQUIRED" },
  reasons: stringList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
});
const codeAcceptance = object({
  lifecycle_epoch: integer({ minimum: 1 }),
  acceptance_policy_revision: integer({ minimum: 1 }),
  release_mode: { const: "CODE" },
  release_plan_id: uuid(),
  release_plan_revision: integer({ minimum: 1 }),
  release_finalized_event_id: uuid(),
  qualified_tree_digest: gitObject(),
  evidence_ids: uuidList({ minItems: 1 }),
});
const noCodeAcceptance = object({
  lifecycle_epoch: integer({ minimum: 1 }),
  acceptance_policy_revision: integer({ minimum: 1 }),
  release_mode: { const: "NO_RELEASE_REQUIRED" },
  release_plan_id: uuid(),
  release_plan_revision: integer({ minimum: 1 }),
  release_finalized_event_id: uuid(),
  evidence_ids: uuidList({ minItems: 1 }),
});
const releaseApproval = object({
  story_id: uuid(),
  lifecycle_epoch: integer({ minimum: 1 }),
  expected_story_revision: integer({ minimum: 1 }),
  author: authorPrincipal(),
  approval_revision: integer({ minimum: 1 }),
  release_policy_revision: integer({ minimum: 1 }),
  reasons: stringList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
});

const payloadPath = "schemas/payloads.schema.json";
const payloads = readJson(`CONTRACTS/${contractIdentity}/${payloadPath}`);
payloads.$defs.tekroo_command_release_plan_create_1_4_0 = { oneOf: [codeReleaseCreate, noCodeReleaseCreate] };
payloads.$defs.tekroo_event_release_plan_created_1_4_0 = { oneOf: [codeReleaseCreate, noCodeReleaseCreate] };
payloads.$defs.tekroo_command_release_plan_record_qualification_1_4_0 = object(qualificationProperties);
payloads.$defs.tekroo_event_release_plan_qualification_recorded_1_4_0 = object(qualificationProperties);
payloads.$defs.tekroo_command_release_plan_request_execution_1_4_0 = object(executionRequestProperties);
payloads.$defs.tekroo_event_release_plan_execution_requested_1_4_0 = object(executionRequestProperties);
payloads.$defs.tekroo_command_release_plan_record_result_1_4_0 = { oneOf: [definitiveResult, failedResult, unknownResult] };
payloads.$defs.tekroo_event_release_plan_result_recorded_1_4_0 = { oneOf: [definitiveResult, failedResult, unknownResult] };
payloads.$defs.tekroo_command_release_plan_record_reconciliation_1_4_0 = { oneOf: [mergedReconciliation, failedReconciliation, unknownReconciliation] };
payloads.$defs.tekroo_event_release_plan_reconciliation_recorded_1_4_0 = { oneOf: [mergedReconciliation, failedReconciliation, unknownReconciliation] };
payloads.$defs.tekroo_command_release_plan_finalize_1_4_0 = { oneOf: [codeFinalize, noCodeFinalize] };
payloads.$defs.tekroo_event_release_plan_finalized_1_4_0 = { oneOf: [codeFinalize, noCodeFinalize] };
payloads.$defs.tekroo_command_story_request_acceptance_1_4_0 = { oneOf: [codeAcceptance, noCodeAcceptance] };
payloads.$defs.tekroo_event_story_accepted_1_4_0 = { oneOf: [codeAcceptance, noCodeAcceptance] };
payloads.$defs.tekroo_command_story_approve_release_1_4_0 = releaseApproval;
payloads.$defs.tekroo_event_story_release_approved_1_4_0 = releaseApproval;
overwriteJson(payloadPath, payloads);

const cataloguePath = "catalogue/kernel-catalogue.json";
const catalogue = readJson(`CONTRACTS/${contractIdentity}/${cataloguePath}`);
catalogue.revision = 5;
const predecessorTypeIds = catalogue.entries.map((entry) => entry.typeId).sort();
for (const entry of catalogue.entries) {
  entry.compatibility = {
    acceptedSourceVersions: [predecessorSchemaVersion, schemaVersion],
    transforms: [{ sourceVersion: predecessorSchemaVersion, targetVersion: schemaVersion, mode: "IDENTITY" }],
  };
}
for (const entry of catalogue.entries) {
  if (["tekroo.command.story.request-acceptance", "tekroo.event.story.accepted"].includes(entry.typeId)) {
    entry.compatibility = { acceptedSourceVersions: [schemaVersion], transforms: [] };
  }
}
const commonEntry = {
  version: schemaVersion,
  lifecycle: "ACTIVE",
  owner: "tekroo-kernel",
  targetKinds: ["release-plan"],
  executionRequired: false,
  allowedParentEdges: ["CAUSAL", "RESPONSE", "RETRY", "DERIVATION", "SUPERSESSION"],
  rootAllowed: false,
  aliases: [],
  compatibility: { acceptedSourceVersions: [schemaVersion], transforms: [] },
};
catalogue.entries.push(
  {
    ...commonEntry,
    typeId: "tekroo.command.release-plan.create",
    kind: "COMMAND",
    authorityKinds: ["POLICY"],
    routingMode: "KERNEL_DIRECT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_command_release_plan_create_1_4_0",
    emits: ["tekroo.event.release-plan.created"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.command.release-plan.record-qualification",
    kind: "COMMAND",
    authorityKinds: ["POLICY", "SERVICE"],
    routingMode: "KERNEL_DIRECT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_command_release_plan_record_qualification_1_4_0",
    emits: ["tekroo.event.release-plan.qualification-recorded"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.command.release-plan.request-execution",
    kind: "COMMAND",
    authorityKinds: ["POLICY"],
    routingMode: "KERNEL_DIRECT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_command_release_plan_request_execution_1_4_0",
    emits: ["tekroo.event.release-plan.execution-requested"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.command.release-plan.record-result",
    kind: "COMMAND",
    authorityKinds: ["SERVICE"],
    routingMode: "KERNEL_DIRECT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_command_release_plan_record_result_1_4_0",
    emits: ["tekroo.event.release-plan.result-recorded"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.command.release-plan.record-reconciliation",
    kind: "COMMAND",
    authorityKinds: ["POLICY", "SERVICE"],
    routingMode: "KERNEL_DIRECT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_command_release_plan_record_reconciliation_1_4_0",
    emits: ["tekroo.event.release-plan.reconciliation-recorded"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.command.release-plan.finalize",
    kind: "COMMAND",
    authorityKinds: ["POLICY"],
    routingMode: "KERNEL_DIRECT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_command_release_plan_finalize_1_4_0",
    emits: ["tekroo.event.release-plan.finalized"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.event.release-plan.created",
    kind: "EVENT",
    authorityKinds: [],
    routingMode: "COMMITTED_EVENT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_event_release_plan_created_1_4_0",
    acceptedCommandTypes: ["tekroo.command.release-plan.create"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.event.release-plan.qualification-recorded",
    kind: "EVENT",
    authorityKinds: [],
    routingMode: "COMMITTED_EVENT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_event_release_plan_qualification_recorded_1_4_0",
    acceptedCommandTypes: ["tekroo.command.release-plan.record-qualification"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.event.release-plan.execution-requested",
    kind: "EVENT",
    authorityKinds: [],
    routingMode: "COMMITTED_EVENT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_event_release_plan_execution_requested_1_4_0",
    acceptedCommandTypes: ["tekroo.command.release-plan.request-execution"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.event.release-plan.result-recorded",
    kind: "EVENT",
    authorityKinds: [],
    routingMode: "COMMITTED_EVENT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_event_release_plan_result_recorded_1_4_0",
    acceptedCommandTypes: ["tekroo.command.release-plan.record-result"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.event.release-plan.reconciliation-recorded",
    kind: "EVENT",
    authorityKinds: [],
    routingMode: "COMMITTED_EVENT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_event_release_plan_reconciliation_recorded_1_4_0",
    acceptedCommandTypes: ["tekroo.command.release-plan.record-reconciliation"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.event.release-plan.finalized",
    kind: "EVENT",
    authorityKinds: [],
    routingMode: "COMMITTED_EVENT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_event_release_plan_finalized_1_4_0",
    acceptedCommandTypes: ["tekroo.command.release-plan.finalize"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.command.story.approve-release",
    kind: "COMMAND",
    targetKinds: ["story"],
    authorityKinds: ["ACTOR", "HUMAN"],
    routingMode: "KERNEL_DIRECT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_command_story_approve_release_1_4_0",
    emits: ["tekroo.event.story.release-approved"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.event.story.release-approved",
    kind: "EVENT",
    targetKinds: ["story"],
    authorityKinds: [],
    routingMode: "COMMITTED_EVENT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_event_story_release_approved_1_4_0",
    acceptedCommandTypes: ["tekroo.command.story.approve-release"],
  },
);
catalogue.entries.sort((left, right) => left.typeId.localeCompare(right.typeId));
overwriteJson(cataloguePath, catalogue);

const catalogueFixturesPath = "fixtures/catalogue-coverage.json";
const catalogueFixtures = readJson(`CONTRACTS/${contractIdentity}/${catalogueFixturesPath}`);
const storyAcceptanceFixture = catalogueFixtures.fixtures.find((fixture) => fixture.fixtureId === "CAT-020-STORY-REQUEST-ACCEPTANCE-VALID");
storyAcceptanceFixture.sourceDecisionIds = ["P1B-V3-INTERNAL-RELEASE", "P2-KCF-012"];
storyAcceptanceFixture.when.payload = {
  lifecycle_epoch: 1,
  acceptance_policy_revision: 1,
  release_mode: "CODE",
  release_plan_id: "00000000-0000-7000-8000-000000000751",
  release_plan_revision: 8,
  release_finalized_event_id: "00000000-0000-7000-8000-000000000760",
  qualified_tree_digest: "3333333333333333333333333333333333333333",
  evidence_ids: ["00000000-0000-7000-8000-000000000750"],
};
const releaseCreatePayload = {
  release_plan_id: "00000000-0000-7000-8000-000000000751",
  story_id: "00000000-0000-7000-8000-000000000101",
  story_lifecycle_epoch: 1,
  expected_story_revision: 8,
  author: { kind: "HUMAN", id: "principal-author" },
  author_approval_event_id: "00000000-0000-7000-8000-000000000749",
  author_approval_revision: 1,
  release_policy_revision: 1,
  plan_digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  evidence_ids: ["00000000-0000-7000-8000-000000000750"],
  release_mode: "CODE",
  repository_url: "https://example.invalid/tekroo/teams.git",
  base_ref: "main",
  base_commit: "1111111111111111111111111111111111111111",
  ordered_merges: [
    { merge_id: "00000000-0000-7000-8000-000000000752", change_ref: "refs/heads/story-1", head_commit: "2222222222222222222222222222222222222222", role: "story" },
  ],
  merge_strategy: "FF_ONLY_ORDERED",
  git_version: "git version 2.51.0",
  conflict_policy: "FAIL_NO_IMPROVISATION",
  contract_manifest: contractIdentity,
  manifest_sha256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  required_profiles: ["contract-structure", "core-hermetic", "mongo-integration", "synthesized-merge"],
  expected_qualified_tree: "3333333333333333333333333333333333333333",
  execution_round_limit: 2,
};
const qualificationPayload = {
  release_plan_id: releaseCreatePayload.release_plan_id,
  expected_release_revision: 1,
  plan_digest: releaseCreatePayload.plan_digest,
  qualification_id: "00000000-0000-7000-8000-000000000754",
  qualified_base_commit: releaseCreatePayload.base_commit,
  ordered_head_commits: [releaseCreatePayload.ordered_merges[0].head_commit],
  qualified_tree_digest: releaseCreatePayload.expected_qualified_tree,
  contract_manifest: contractIdentity,
  manifest_sha256: releaseCreatePayload.manifest_sha256,
  required_profiles: releaseCreatePayload.required_profiles,
  gate_definition_digest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
  toolchain_digest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
  dependency_lock_digest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  artifact_digests: ["ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"],
  evidence_ids: releaseCreatePayload.evidence_ids,
};
const executionPayload = {
  release_plan_id: releaseCreatePayload.release_plan_id,
  expected_release_revision: 2,
  plan_digest: releaseCreatePayload.plan_digest,
  merge_id: releaseCreatePayload.ordered_merges[0].merge_id,
  attempt_id: "00000000-0000-7000-8000-000000000755",
  round: 1,
  provider_idempotency_key: "release-751-merge-752-round-1",
  evidence_ids: releaseCreatePayload.evidence_ids,
};
const resultPayload = {
  release_plan_id: releaseCreatePayload.release_plan_id,
  expected_release_revision: 3,
  plan_digest: releaseCreatePayload.plan_digest,
  merge_id: releaseCreatePayload.ordered_merges[0].merge_id,
  attempt_id: executionPayload.attempt_id,
  outcome: "UNKNOWN",
  reasons: ["provider response was not terminal"],
  evidence_ids: ["00000000-0000-7000-8000-000000000756"],
  observed_at: "2026-08-11T12:00:00Z",
};
const reconciliationPayload = {
  release_plan_id: releaseCreatePayload.release_plan_id,
  expected_release_revision: 4,
  plan_digest: releaseCreatePayload.plan_digest,
  merge_id: releaseCreatePayload.ordered_merges[0].merge_id,
  attempt_id: executionPayload.attempt_id,
  reconciliation_id: "00000000-0000-7000-8000-000000000759",
  supersedes_result_event_id: "00000000-0000-7000-8000-000000000757",
  provider_state: "MERGED",
  outcome: "MERGED",
  observed_base_commit: releaseCreatePayload.base_commit,
  observed_head_commit: releaseCreatePayload.ordered_merges[0].head_commit,
  observed_tree_digest: releaseCreatePayload.expected_qualified_tree,
  reasons: ["provider reports exact planned head merged"],
  evidence_ids: ["00000000-0000-7000-8000-000000000756"],
  observed_at: "2026-08-11T12:01:00Z",
};
const finalizePayload = {
  release_plan_id: releaseCreatePayload.release_plan_id,
  expected_release_revision: 5,
  plan_digest: releaseCreatePayload.plan_digest,
  release_mode: "CODE",
  terminal_status: "READY_FOR_ACCEPTANCE",
  qualification_event_id: "00000000-0000-7000-8000-000000000758",
  result_event_ids: ["00000000-0000-7000-8000-000000000759"],
  qualified_tree_digest: releaseCreatePayload.expected_qualified_tree,
  provider_tree_digest: releaseCreatePayload.expected_qualified_tree,
  reasons: ["all planned merges and the provider tree are verified"],
  evidence_ids: releaseCreatePayload.evidence_ids,
};
const releaseApprovalPayload = {
  story_id: releaseCreatePayload.story_id,
  lifecycle_epoch: releaseCreatePayload.story_lifecycle_epoch,
  expected_story_revision: releaseCreatePayload.expected_story_revision,
  author: releaseCreatePayload.author,
  approval_revision: releaseCreatePayload.author_approval_revision,
  release_policy_revision: releaseCreatePayload.release_policy_revision,
  reasons: ["author approves the exact release scope"],
  evidence_ids: releaseCreatePayload.evidence_ids,
};
function catalogueFixture(fixtureId, classification, commandType, payload, outcomeCode, eventTypes) {
  return {
    classification,
    fixtureId,
    kind: "CATALOGUE_COMMAND",
    sourceDecisionIds: ["P1B-V3-INTERNAL-RELEASE", "P2-KCF-012"],
    given: { contractManifest: contractIdentity },
    when: { commandType, payload },
    then: { expected: { outcomeCode, eventTypes } },
  };
}
catalogueFixtures.fixtures.push(
  catalogueFixture("CAT-030-RELEASE-CREATE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.release-plan.create", releaseCreatePayload, "APPLIED", ["tekroo.event.release-plan.created"]),
  catalogueFixture("CAT-030-RELEASE-CREATE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.release-plan.create", { ...releaseCreatePayload, author_approval_event_id: undefined }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-031-RELEASE-QUALIFY-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.release-plan.record-qualification", qualificationPayload, "APPLIED", ["tekroo.event.release-plan.qualification-recorded"]),
  catalogueFixture("CAT-031-RELEASE-QUALIFY-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.release-plan.record-qualification", { ...qualificationPayload, artifact_digests: [] }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-032-RELEASE-EXECUTE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.release-plan.request-execution", executionPayload, "APPLIED", ["tekroo.event.release-plan.execution-requested"]),
  catalogueFixture("CAT-032-RELEASE-EXECUTE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.release-plan.request-execution", { ...executionPayload, round: 0 }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-033-RELEASE-RESULT-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.release-plan.record-result", resultPayload, "APPLIED", ["tekroo.event.release-plan.result-recorded"]),
  catalogueFixture("CAT-033-RELEASE-RESULT-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.release-plan.record-result", { ...resultPayload, reasons: [] }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-034-RELEASE-RECONCILE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.release-plan.record-reconciliation", reconciliationPayload, "APPLIED", ["tekroo.event.release-plan.reconciliation-recorded"]),
  catalogueFixture("CAT-034-RELEASE-RECONCILE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.release-plan.record-reconciliation", { ...reconciliationPayload, supersedes_result_event_id: undefined }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-035-RELEASE-FINALIZE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.release-plan.finalize", finalizePayload, "APPLIED", ["tekroo.event.release-plan.finalized"]),
  catalogueFixture("CAT-035-RELEASE-FINALIZE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.release-plan.finalize", { ...finalizePayload, reasons: [] }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-036-RELEASE-APPROVAL-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.story.approve-release", releaseApprovalPayload, "APPLIED", ["tekroo.event.story.release-approved"]),
  catalogueFixture("CAT-036-RELEASE-APPROVAL-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.story.approve-release", { ...releaseApprovalPayload, reasons: [] }, "REJECTED_INVALID", []),
);
catalogueFixtures.fixtures.sort((left, right) => left.fixtureId.localeCompare(right.fixtureId));
overwriteJson(catalogueFixturesPath, catalogueFixtures);

function releaseFixture(fixtureId, classification, given, when, accepted, reason, state) {
  return {
    classification,
    fixtureId,
    kind: "RELEASE_MODEL",
    sourceDecisionIds: ["P1B-V3-INTERNAL-RELEASE", "P2-KCF-012", "NEG-012"],
    given,
    when,
    then: { expected: { accepted, reason, state } },
  };
}
const mergeOne = releaseCreatePayload.ordered_merges[0].merge_id;
const mergeTwo = "00000000-0000-7000-8000-000000000753";
const attemptOne = executionPayload.attempt_id;
const resultEvent = "00000000-0000-7000-8000-000000000757";
const authorApprovalBase = { state: "ABSENT", releaseMode: "CODE", author: releaseCreatePayload.author, authorApprovalEventId: releaseCreatePayload.author_approval_event_id };
const secondHead = "5555555555555555555555555555555555555555";
const releaseBase = {
  state: "PLANNED", releaseMode: "CODE", planDigest: releaseCreatePayload.plan_digest,
  baseCommit: releaseCreatePayload.base_commit, headCommits: [releaseCreatePayload.ordered_merges[0].head_commit, secondHead],
  mergeOrder: [mergeOne, mergeTwo], nextMergeIndex: 0, executionRoundLimit: 2, nextRound: 1, activeRound: 0,
  unresolvedAttemptId: null, latestResultEventId: null, qualifiedTreeDigest: null, providerTreeDigest: null,
};
const qualifiedBase = { ...releaseBase, state: "QUALIFIED", qualifiedTreeDigest: releaseCreatePayload.expected_qualified_tree };
const executingBase = { ...qualifiedBase, state: "EXECUTING", activeMergeId: mergeOne, activeAttemptId: attemptOne, activeRound: 1 };
const reconcilingBase = { ...executingBase, state: "RECONCILING", unresolvedAttemptId: attemptOne, latestResultEventId: resultEvent };
const fullyMergedBase = { ...qualifiedBase, nextMergeIndex: 2, providerTreeDigest: releaseCreatePayload.expected_qualified_tree };
const retryBase = { ...qualifiedBase, nextRound: 2 };
const exhaustedExecutingBase = { ...executingBase, activeRound: 2, nextRound: 2 };
const modelFixturesPath = "fixtures/model-and-invariant-scenarios.json";
const modelFixtures = readJson(`CONTRACTS/${contractIdentity}/${modelFixturesPath}`);
modelFixtures.fixtures.push(
  releaseFixture("RELEASE-EXACT-AUTHOR-APPROVAL", "NORMATIVE_EXAMPLE", authorApprovalBase, { action: "CREATE", author: releaseCreatePayload.author, authorApprovalEventId: releaseCreatePayload.author_approval_event_id }, true, "ACCEPTED", "PLANNED"),
  releaseFixture("RELEASE-AUTHOR-APPROVAL-MISMATCH", "COUNTEREXAMPLE", authorApprovalBase, { action: "CREATE", author: releaseCreatePayload.author, authorApprovalEventId: "00000000-0000-7000-8000-000000000748" }, false, "AUTHOR_APPROVAL_MISMATCH", "ABSENT"),
  releaseFixture("RELEASE-EXACT-QUALIFICATION", "NORMATIVE_EXAMPLE", releaseBase, { action: "QUALIFY", planDigest: releaseBase.planDigest, baseCommit: releaseBase.baseCommit, headCommits: releaseBase.headCommits, qualifiedTreeDigest: releaseCreatePayload.expected_qualified_tree }, true, "ACCEPTED", "QUALIFIED"),
  releaseFixture("RELEASE-CHANGED-PLAN", "COUNTEREXAMPLE", releaseBase, { action: "QUALIFY", planDigest: "9999999999999999999999999999999999999999999999999999999999999999", baseCommit: releaseBase.baseCommit, headCommits: releaseBase.headCommits, qualifiedTreeDigest: releaseCreatePayload.expected_qualified_tree }, false, "PLAN_DIGEST_MISMATCH", "PLANNED"),
  releaseFixture("RELEASE-CHANGED-BASE", "COUNTEREXAMPLE", releaseBase, { action: "QUALIFY", planDigest: releaseBase.planDigest, baseCommit: "6666666666666666666666666666666666666666", headCommits: releaseBase.headCommits, qualifiedTreeDigest: releaseCreatePayload.expected_qualified_tree }, false, "QUALIFICATION_INPUT_MISMATCH", "PLANNED"),
  releaseFixture("RELEASE-CHANGED-HEAD", "COUNTEREXAMPLE", releaseBase, { action: "QUALIFY", planDigest: releaseBase.planDigest, baseCommit: releaseBase.baseCommit, headCommits: [releaseBase.headCommits[0], "7777777777777777777777777777777777777777"], qualifiedTreeDigest: releaseCreatePayload.expected_qualified_tree }, false, "QUALIFICATION_INPUT_MISMATCH", "PLANNED"),
  releaseFixture("RELEASE-EXECUTE-BEFORE-QUALIFICATION", "BOUNDARY_NEGATIVE", releaseBase, { action: "REQUEST_EXECUTION", mergeId: mergeOne, attemptId: attemptOne, round: 1 }, false, "QUALIFICATION_REQUIRED", "PLANNED"),
  releaseFixture("RELEASE-FIRST-ORDERED-REQUEST", "NORMATIVE_EXAMPLE", qualifiedBase, { action: "REQUEST_EXECUTION", mergeId: mergeOne, attemptId: attemptOne, round: 1 }, true, "ACCEPTED", "EXECUTING"),
  releaseFixture("RELEASE-WRONG-ORDER", "COUNTEREXAMPLE", qualifiedBase, { action: "REQUEST_EXECUTION", mergeId: mergeTwo, attemptId: attemptOne, round: 1 }, false, "MERGE_ORDER_CONFLICT", "QUALIFIED"),
  releaseFixture("RELEASE-CONCURRENT-WORKER", "COUNTEREXAMPLE", executingBase, { action: "REQUEST_EXECUTION", mergeId: mergeOne, attemptId: "00000000-0000-7000-8000-000000000761", round: 1 }, false, "EXECUTION_IN_PROGRESS", "EXECUTING"),
  releaseFixture("RELEASE-BOUNDED-RETRY", "NORMATIVE_EXAMPLE", retryBase, { action: "REQUEST_EXECUTION", mergeId: mergeOne, attemptId: "00000000-0000-7000-8000-000000000761", round: 2 }, true, "ACCEPTED", "EXECUTING"),
  releaseFixture("RELEASE-RETRY-BUDGET-EXHAUSTED", "COUNTEREXAMPLE", { ...retryBase, nextRound: 3 }, { action: "REQUEST_EXECUTION", mergeId: mergeOne, attemptId: "00000000-0000-7000-8000-000000000762", round: 3 }, false, "RETRY_BUDGET_EXHAUSTED", "QUALIFIED"),
  releaseFixture("RELEASE-UNKNOWN-RESULT", "NORMATIVE_EXAMPLE", executingBase, { action: "RECORD_RESULT", attemptId: attemptOne, resultEventId: resultEvent, outcome: "UNKNOWN" }, true, "ACCEPTED", "RECONCILING"),
  releaseFixture("RELEASE-CLEAN-MERGE", "NORMATIVE_EXAMPLE", executingBase, { action: "RECORD_RESULT", attemptId: attemptOne, resultEventId: resultEvent, outcome: "MERGED" }, true, "ACCEPTED", "QUALIFIED"),
  releaseFixture("RELEASE-ALREADY-MERGED", "NORMATIVE_EXAMPLE", executingBase, { action: "RECORD_RESULT", attemptId: attemptOne, resultEventId: resultEvent, outcome: "ALREADY_MERGED" }, true, "ACCEPTED", "QUALIFIED"),
  releaseFixture("RELEASE-GENUINE-FAILURE-RETRYABLE", "NORMATIVE_EXAMPLE", executingBase, { action: "RECORD_RESULT", attemptId: attemptOne, resultEventId: resultEvent, outcome: "FAILED" }, true, "ACCEPTED", "QUALIFIED"),
  releaseFixture("RELEASE-GENUINE-FAILURE-EXHAUSTED", "NORMATIVE_EXAMPLE", exhaustedExecutingBase, { action: "RECORD_RESULT", attemptId: attemptOne, resultEventId: resultEvent, outcome: "FAILED" }, true, "ACCEPTED", "FAILED"),
  releaseFixture("RELEASE-NO-RETRY-WHILE-UNKNOWN", "COUNTEREXAMPLE", reconcilingBase, { action: "REQUEST_EXECUTION", mergeId: mergeOne, attemptId: "00000000-0000-7000-8000-000000000761", round: 2 }, false, "RECONCILIATION_REQUIRED", "RECONCILING"),
  releaseFixture("RELEASE-EXACT-RECONCILIATION", "NORMATIVE_EXAMPLE", reconcilingBase, { action: "RECONCILE", attemptId: attemptOne, supersedesResultEventId: resultEvent, outcome: "MERGED", providerTreeDigest: releaseCreatePayload.expected_qualified_tree }, true, "ACCEPTED", "QUALIFIED"),
  releaseFixture("RELEASE-PROVIDER-UNAVAILABLE", "NORMATIVE_EXAMPLE", reconcilingBase, { action: "RECONCILE", attemptId: attemptOne, supersedesResultEventId: resultEvent, outcome: "UNKNOWN" }, true, "ACCEPTED", "RECONCILING"),
  releaseFixture("RELEASE-WRONG-SUPERSESSION", "COUNTEREXAMPLE", reconcilingBase, { action: "RECONCILE", attemptId: attemptOne, supersedesResultEventId: "00000000-0000-7000-8000-000000000762", outcome: "MERGED", providerTreeDigest: releaseCreatePayload.expected_qualified_tree }, false, "RESULT_SUPERSESSION_MISMATCH", "RECONCILING"),
  releaseFixture("RELEASE-PARTIAL-FINALIZE", "COUNTEREXAMPLE", { ...fullyMergedBase, nextMergeIndex: 1 }, { action: "FINALIZE", terminalStatus: "READY_FOR_ACCEPTANCE", providerTreeDigest: releaseCreatePayload.expected_qualified_tree }, false, "RELEASE_INCOMPLETE", "QUALIFIED"),
  releaseFixture("RELEASE-TREE-MISMATCH", "COUNTEREXAMPLE", fullyMergedBase, { action: "FINALIZE", terminalStatus: "READY_FOR_ACCEPTANCE", providerTreeDigest: "4444444444444444444444444444444444444444" }, false, "TREE_MISMATCH", "QUALIFIED"),
  releaseFixture("RELEASE-VERIFIED-FINALIZE", "NORMATIVE_EXAMPLE", fullyMergedBase, { action: "FINALIZE", terminalStatus: "READY_FOR_ACCEPTANCE", providerTreeDigest: releaseCreatePayload.expected_qualified_tree }, true, "ACCEPTED", "READY_FOR_ACCEPTANCE"),
  releaseFixture("RELEASE-TERMINAL-IMMUTABLE", "BOUNDARY_NEGATIVE", { ...fullyMergedBase, state: "READY_FOR_ACCEPTANCE" }, { action: "REQUEST_EXECUTION", mergeId: mergeOne, attemptId: attemptOne }, false, "RELEASE_TERMINAL", "READY_FOR_ACCEPTANCE"),
  releaseFixture("RELEASE-NO-CODE-FINALIZE", "NORMATIVE_EXAMPLE", { ...releaseBase, state: "PLANNED", releaseMode: "NO_RELEASE_REQUIRED", mergeOrder: [] }, { action: "FINALIZE", terminalStatus: "NO_RELEASE_REQUIRED" }, true, "ACCEPTED", "NO_RELEASE_REQUIRED"),
);
const releaseIdempotencyScope = { commandType: "tekroo.command.release-plan.request-execution", contract: contractIdentity, key: "release-751-merge-752-round-1", principal: "release-policy", target: releaseCreatePayload.release_plan_id };
const releaseSemanticRequest = { attemptId: attemptOne, expectedRevision: 2, mergeId: mergeOne, planDigest: releaseCreatePayload.plan_digest, round: 1 };
const releaseSemanticConflict = { ...releaseSemanticRequest, attemptId: "00000000-0000-7000-8000-000000000761" };
const releaseFingerprint = sha256(canonical(releaseSemanticRequest));
modelFixtures.fixtures.push({
  classification: "NORMATIVE_EXAMPLE",
  fixtureId: "RELEASE-IDEMPOTENCY-DUPLICATE-AND-CONFLICT",
  kind: "IDEMPOTENCY_MODEL",
  sourceDecisionIds: ["P1B-V3-INTERNAL-RELEASE", "NEG-012"],
  given: {},
  when: { commands: [
    { scope: releaseIdempotencyScope, semanticRequest: releaseSemanticRequest },
    { scope: releaseIdempotencyScope, semanticRequest: releaseSemanticRequest },
    { scope: releaseIdempotencyScope, semanticRequest: releaseSemanticConflict },
  ] },
  then: { expected: { durableDecisionCount: 1, receipts: [
    { outcome: "APPLIED", fingerprint: releaseFingerprint, eventCount: 1 },
    { outcome: "APPLIED", fingerprint: releaseFingerprint, eventCount: 1 },
    { outcome: "REJECTED_CONFLICT", reason: "IDEMPOTENCY_KEY_REUSE", eventCount: 0 },
  ] } },
});
modelFixtures.fixtures.sort((left, right) => left.fixtureId.localeCompare(right.fixtureId));
overwriteJson(modelFixturesPath, modelFixtures);

const fixtureSchemaPath = "schemas/conformance-fixture.schema.json";
const fixtureSchema = readJson(`CONTRACTS/${contractIdentity}/${fixtureSchemaPath}`);
fixtureSchema.properties.kind.enum.push("RELEASE_MODEL");
fixtureSchema.properties.kind.enum.sort();
overwriteJson(fixtureSchemaPath, fixtureSchema);

const releaseTestIds = [
  ...catalogueFixtures.fixtures.filter((fixture) => /^CAT-03[0-6]-RELEASE-/.test(fixture.fixtureId)).map((fixture) => fixture.fixtureId),
  ...modelFixtures.fixtures.filter((fixture) => fixture.fixtureId.startsWith("RELEASE-")).map((fixture) => fixture.fixtureId),
  "CAT-020-STORY-REQUEST-ACCEPTANCE-VALID",
  "INV-020-FROZEN-RELEASE-PLAN",
  "INV-021-DURABLE-RELEASE-INTENT-RECONCILIATION",
  "INV-022-VERIFIED-RELEASE-ACCEPTANCE",
].sort();
const invariantsPath = "invariants/invariants.json";
const invariants = readJson(`CONTRACTS/${contractIdentity}/${invariantsPath}`);
invariants.invariants.push(
  {
    invariantId: "INV-020-FROZEN-RELEASE-PLAN",
    description: "External release execution is authorized only from an exact, durably persisted, author-approved plan whose repository, base, ordered changes, heads, policy, toolchain, contract, profiles, and synthesized tree match the qualified plan.",
    decisionIds: ["P1B-V3-INTERNAL-RELEASE", "P2-KCF-012"],
    negativeRequirementIds: ["NEG-012"],
    fixtureIds: ["RELEASE-EXACT-AUTHOR-APPROVAL", "RELEASE-AUTHOR-APPROVAL-MISMATCH", "RELEASE-EXACT-QUALIFICATION", "RELEASE-CHANGED-PLAN", "RELEASE-CHANGED-BASE", "RELEASE-CHANGED-HEAD", "RELEASE-FIRST-ORDERED-REQUEST", "RELEASE-WRONG-ORDER", "RELEASE-EXECUTE-BEFORE-QUALIFICATION"],
  },
  {
    invariantId: "INV-021-DURABLE-RELEASE-INTENT-RECONCILIATION",
    description: "Every external attempt and outcome is durably recorded; UNKNOWN remains unresolved, prohibits retry, and can advance only through exact provider reconciliation that supersedes the recorded uncertain result.",
    decisionIds: ["P1B-V3-INTERNAL-RELEASE", "P2-KCF-012"],
    negativeRequirementIds: ["NEG-012"],
    fixtureIds: ["RELEASE-IDEMPOTENCY-DUPLICATE-AND-CONFLICT", "RELEASE-CONCURRENT-WORKER", "RELEASE-BOUNDED-RETRY", "RELEASE-RETRY-BUDGET-EXHAUSTED", "RELEASE-UNKNOWN-RESULT", "RELEASE-CLEAN-MERGE", "RELEASE-ALREADY-MERGED", "RELEASE-GENUINE-FAILURE-RETRYABLE", "RELEASE-GENUINE-FAILURE-EXHAUSTED", "RELEASE-NO-RETRY-WHILE-UNKNOWN", "RELEASE-EXACT-RECONCILIATION", "RELEASE-PROVIDER-UNAVAILABLE", "RELEASE-WRONG-SUPERSESSION"],
  },
  {
    invariantId: "INV-022-VERIFIED-RELEASE-ACCEPTANCE",
    description: "A code story is eligible for acceptance only after every ordered merge is verified and the provider tree equals the qualified tree; partial, ambiguous, mismatched, and terminal release states cannot be silently accepted or reopened.",
    decisionIds: ["P1B-V3-INTERNAL-RELEASE", "P2-KCF-012"],
    negativeRequirementIds: ["NEG-012"],
    fixtureIds: ["RELEASE-PARTIAL-FINALIZE", "RELEASE-TREE-MISMATCH", "RELEASE-VERIFIED-FINALIZE", "RELEASE-TERMINAL-IMMUTABLE", "RELEASE-NO-CODE-FINALIZE", "CAT-020-STORY-REQUEST-ACCEPTANCE-VALID"],
  },
);
invariants.invariants.sort((left, right) => left.invariantId.localeCompare(right.invariantId));
overwriteJson(invariantsPath, invariants);

const traceabilityPath = "traceability/traceability.json";
const traceability = readJson(`CONTRACTS/${contractIdentity}/${traceabilityPath}`);
for (const requirement of traceability.requirements) {
  if (["P2-KCF-012", "NEG-012"].includes(requirement.requirementId)) {
    requirement.testIds = [...new Set([...requirement.testIds, ...releaseTestIds])].sort();
  }
}
traceability.requirements.push(
  { disposition: "BINDING", kind: "DECISION", requirementId: "P1B-V3-INTERNAL-RELEASE", testIds: releaseTestIds, text: "Bind author approval; persist, qualify, and execute the exact ordered release plan; make duplicate and concurrent workers converge; bound retries; reconcile every ambiguous external outcome; verify the provider tree before story acceptance; and explicitly represent non-code stories." },
  { disposition: "BINDING", kind: "PROHIBITED_INTERPRETATION", parentDecisionId: "P1B-V3-INTERNAL-RELEASE", requirementId: "P1B-V3-INTERNAL-RELEASE-PROHIBIT-01", testIds: ["RELEASE-UNKNOWN-RESULT", "RELEASE-NO-RETRY-WHILE-UNKNOWN", "INV-021-DURABLE-RELEASE-INTENT-RECONCILIATION"], text: "A provider process exit, timeout, or missing response proves that a merge succeeded or failed." },
  { disposition: "BINDING", kind: "PROHIBITED_INTERPRETATION", parentDecisionId: "P1B-V3-INTERNAL-RELEASE", requirementId: "P1B-V3-INTERNAL-RELEASE-PROHIBIT-02", testIds: ["RELEASE-NO-RETRY-WHILE-UNKNOWN", "RELEASE-EXACT-RECONCILIATION", "INV-021-DURABLE-RELEASE-INTENT-RECONCILIATION"], text: "An UNKNOWN external outcome may be retried before exact provider reconciliation." },
  { disposition: "BINDING", kind: "PROHIBITED_INTERPRETATION", parentDecisionId: "P1B-V3-INTERNAL-RELEASE", requirementId: "P1B-V3-INTERNAL-RELEASE-PROHIBIT-03", testIds: ["RELEASE-PARTIAL-FINALIZE", "INV-022-VERIFIED-RELEASE-ACCEPTANCE"], text: "A partial release implies either rollback or acceptance." },
  { disposition: "BINDING", kind: "PROHIBITED_INTERPRETATION", parentDecisionId: "P1B-V3-INTERNAL-RELEASE", requirementId: "P1B-V3-INTERNAL-RELEASE-PROHIBIT-04", testIds: ["RELEASE-TREE-MISMATCH", "INV-022-VERIFIED-RELEASE-ACCEPTANCE"], text: "A code story is accepted when the provider tree differs from the qualified synthesized tree." },
  { disposition: "BINDING", kind: "PROHIBITED_INTERPRETATION", parentDecisionId: "P1B-V3-INTERNAL-RELEASE", requirementId: "P1B-V3-INTERNAL-RELEASE-PROHIBIT-05", testIds: ["RELEASE-EXACT-QUALIFICATION", "INV-020-FROZEN-RELEASE-PLAN"], text: "Evidence identifiers substitute for the durable release-plan aggregate and its ordered state transitions." },
);
traceability.requirements.sort((left, right) => left.requirementId.localeCompare(right.requirementId));
overwriteJson(traceabilityPath, traceability);

const predecessorManifestBytes = fs.readFileSync(path.join(predecessorRoot, "manifest.json"));
const acceptanceTypes = ["tekroo.command.story.request-acceptance", "tekroo.event.story.accepted"];
const unchangedSemanticTypes = predecessorTypeIds.filter((typeId) => !acceptanceTypes.includes(typeId));
const releaseTypes = catalogue.entries.map((entry) => entry.typeId).filter((typeId) => !predecessorTypeIds.includes(typeId)).sort();
writeJson("compatibility/from-0.4.0.json", {
  schemaVersion,
  contractName: "tekroo.kernel.contracts",
  contractVersion,
  predecessor: {
    contractVersion: predecessorVersion,
    contractIdentity: `tekroo.kernel.contracts/${predecessorVersion}`,
    manifestSha256: sha256(predecessorManifestBytes),
  },
  compatibilityClaim: "ADDITIVE_RELEASE_EXTENSION_WITH_CONTEXT_REQUIRED_ACCEPTANCE_REVISION",
  directions: { backward: "COMPATIBLE_ADAPTER_REQUIRED", forward: "NOT_COMPATIBLE", replay: "VERSION_GATED", adapter: "REQUIRED" },
  unchangedSemanticTypes,
  changedSemanticTypes: acceptanceTypes,
  additiveTypes: releaseTypes,
  migrationRules: [
    { source: predecessorSchemaVersion, target: schemaVersion, mode: "IDENTITY", appliesTo: unchangedSemanticTypes },
    { source: predecessorSchemaVersion, target: schemaVersion, mode: "CONTEXT_REQUIRED", appliesTo: acceptanceTypes, requiredContext: ["release_mode", "release_plan_id", "release_plan_revision", "release_finalized_event_id"] },
    { source: schemaVersion, target: schemaVersion, mode: "IDENTITY" },
  ],
  prohibitedInferences: [
    "Historical story acceptance records do not imply that a release plan existed or that external merges were verified.",
    "No release mode, release-plan identity, finalization event, or qualified/provider tree equality may be invented for 0.4.0 acceptance records.",
  ],
  rollback: "Readers retain 0.4.0 for historical replay. Release-plan records are never down-converted, and 1.4.0 story acceptance is not coerced into 1.3.0 without explicit loss handling.",
});

let referenceRunner = advanceString(fs.readFileSync(path.join(predecessorRoot, "runner/reference-runner.mjs"), "utf8"));
referenceRunner = referenceRunner
  .replace(
    "function validateType(value, rule) {\n  if (rule.anyOf !== undefined)",
    "function validateType(value, rule) {\n  if (rule.oneOf !== undefined) return rule.oneOf.filter((candidate) => validateType(value, candidate)).length === 1;\n  if (rule.anyOf !== undefined)",
  )
  .replace(
    "function validatePayload(payload, schema) {\n  if (payload === null",
    "function validatePayload(payload, schema) {\n  if (schema.oneOf !== undefined) return schema.oneOf.filter((candidate) => validateType(payload, candidate)).length === 1;\n  if (schema.anyOf !== undefined) return schema.anyOf.some((candidate) => validateType(payload, candidate));\n  if (payload === null",
  );
referenceRunner = referenceRunner.replace(
  "function runSuccessorSetModel(fixture) {",
  `function runReleaseModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  if (["READY_FOR_ACCEPTANCE", "FAILED", "BLOCKED", "NO_RELEASE_REQUIRED"].includes(current.state)) return { accepted: false, reason: "RELEASE_TERMINAL", state: current.state };
  if (action.planDigest && action.planDigest !== current.planDigest) return { accepted: false, reason: "PLAN_DIGEST_MISMATCH", state: current.state };
  if (action.action === "CREATE") {
    if (current.state !== "ABSENT") return { accepted: false, reason: "INVALID_RELEASE_STATE", state: current.state };
    if (canonical(action.author) !== canonical(current.author) || action.authorApprovalEventId !== current.authorApprovalEventId) return { accepted: false, reason: "AUTHOR_APPROVAL_MISMATCH", state: current.state };
    return { accepted: true, reason: "ACCEPTED", state: "PLANNED" };
  }
  if (action.action === "QUALIFY") {
    if (current.releaseMode !== "CODE" || current.state !== "PLANNED") return { accepted: false, reason: "INVALID_RELEASE_STATE", state: current.state };
    if (action.baseCommit !== current.baseCommit || canonical(action.headCommits) !== canonical(current.headCommits)) return { accepted: false, reason: "QUALIFICATION_INPUT_MISMATCH", state: current.state };
    return { accepted: true, reason: "ACCEPTED", state: "QUALIFIED" };
  }
  if (action.action === "REQUEST_EXECUTION") {
    if (current.state === "RECONCILING") return { accepted: false, reason: "RECONCILIATION_REQUIRED", state: current.state };
    if (current.state === "EXECUTING") return { accepted: false, reason: "EXECUTION_IN_PROGRESS", state: current.state };
    if (current.state !== "QUALIFIED") return { accepted: false, reason: "QUALIFICATION_REQUIRED", state: current.state };
    if (current.mergeOrder[current.nextMergeIndex] !== action.mergeId) return { accepted: false, reason: "MERGE_ORDER_CONFLICT", state: current.state };
    if (current.nextRound > current.executionRoundLimit || action.round > current.executionRoundLimit) return { accepted: false, reason: "RETRY_BUDGET_EXHAUSTED", state: current.state };
    if (action.round !== current.nextRound) return { accepted: false, reason: "ROUND_CONFLICT", state: current.state };
    return { accepted: true, reason: "ACCEPTED", state: "EXECUTING" };
  }
  if (action.action === "RECORD_RESULT") {
    if (current.state !== "EXECUTING" || current.activeAttemptId !== action.attemptId) return { accepted: false, reason: "ATTEMPT_MISMATCH", state: current.state };
    if (action.outcome === "UNKNOWN") return { accepted: true, reason: "ACCEPTED", state: "RECONCILING" };
    if (["MERGED", "ALREADY_MERGED"].includes(action.outcome)) return { accepted: true, reason: "ACCEPTED", state: "QUALIFIED" };
    if (action.outcome === "FAILED") return { accepted: true, reason: "ACCEPTED", state: current.activeRound < current.executionRoundLimit ? "QUALIFIED" : "FAILED" };
    return { accepted: false, reason: "INVALID_OUTCOME", state: current.state };
  }
  if (action.action === "RECONCILE") {
    if (current.state !== "RECONCILING" || current.unresolvedAttemptId !== action.attemptId) return { accepted: false, reason: "ATTEMPT_MISMATCH", state: current.state };
    if (current.latestResultEventId !== action.supersedesResultEventId) return { accepted: false, reason: "RESULT_SUPERSESSION_MISMATCH", state: current.state };
    if (action.outcome === "UNKNOWN") return { accepted: true, reason: "ACCEPTED", state: "RECONCILING" };
    if (["MERGED", "ALREADY_MERGED"].includes(action.outcome)) return { accepted: true, reason: "ACCEPTED", state: "QUALIFIED" };
    if (action.outcome === "FAILED") return { accepted: true, reason: "ACCEPTED", state: current.activeRound < current.executionRoundLimit ? "QUALIFIED" : "FAILED" };
    return { accepted: false, reason: "INVALID_OUTCOME", state: current.state };
  }
  if (action.action === "FINALIZE") {
    if (current.releaseMode === "NO_RELEASE_REQUIRED") {
      if (current.state === "PLANNED" && action.terminalStatus === "NO_RELEASE_REQUIRED") return { accepted: true, reason: "ACCEPTED", state: "NO_RELEASE_REQUIRED" };
      return { accepted: false, reason: "INVALID_RELEASE_STATE", state: current.state };
    }
    if (current.state !== "QUALIFIED" || current.nextMergeIndex !== current.mergeOrder.length) return { accepted: false, reason: "RELEASE_INCOMPLETE", state: current.state };
    if (action.providerTreeDigest !== current.qualifiedTreeDigest) return { accepted: false, reason: "TREE_MISMATCH", state: current.state };
    return { accepted: true, reason: "ACCEPTED", state: action.terminalStatus };
  }
  return { accepted: false, reason: "INVALID_ACTION", state: current.state };
}

function runSuccessorSetModel(fixture) {`,
);
referenceRunner = referenceRunner.replace(
  '  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {',
  '  } else if (fixture.kind === "RELEASE_MODEL") {\n    actual = runReleaseModel(fixture);\n  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {',
);
fs.mkdirSync(path.join(packageRoot, "runner"), { recursive: true });
fs.writeFileSync(path.join(packageRoot, "runner/reference-runner.mjs"), referenceRunner, { flag: "wx" });

let validator = advanceString(fs.readFileSync(path.join(predecessorRoot, "runner/validate-package.mjs"), "utf8"))
  .replace('manifest.contract.version === "0.4.0"', 'manifest.contract.version === "0.5.0"')
  .replace("decisions.length === 12", "decisions.length === 13")
  .replaceAll("compatibility/from-0.3.0.json", "compatibility/from-0.4.0.json")
  .replace(
    'compatibility.predecessor?.contractVersion === "0.3.0" && compatibility.contractVersion === "0.4.0" && compatibility.directions?.adapter === "REQUIRED"',
    'compatibility.predecessor?.contractVersion === "0.4.0" && compatibility.contractVersion === "0.5.0" && compatibility.directions?.adapter === "REQUIRED"',
  );
fs.writeFileSync(path.join(packageRoot, "runner/validate-package.mjs"), `${validator.trimEnd()}\n`, { flag: "wx" });

function roleFor(relativePath) {
  if (relativePath.startsWith("catalogue/")) return "catalogue";
  if (relativePath.startsWith("schemas/")) return "schemas";
  if (relativePath.startsWith("fixtures/")) return "fixtures";
  if (relativePath.startsWith("invariants/")) return "invariants";
  if (relativePath.startsWith("traceability/")) return "traceability";
  if (relativePath.startsWith("compatibility/")) return "compatibility";
  if (relativePath.startsWith("runner/")) return relativePath.endsWith(".json") ? "runner-protocol" : "runner-tool";
  throw new Error(`unknown package role for ${relativePath}`);
}

function dependenciesFor(relativePath) {
  if (relativePath === "catalogue/kernel-catalogue.json") return ["schemas/payloads.schema.json"];
  if (relativePath.startsWith("fixtures/")) return ["catalogue/kernel-catalogue.json", "schemas/conformance-fixture.schema.json", "schemas/payloads.schema.json"];
  if (relativePath.startsWith("invariants/")) return ["fixtures/catalogue-coverage.json", "fixtures/model-and-invariant-scenarios.json"];
  if (relativePath.startsWith("traceability/")) return ["invariants/invariants.json", "fixtures/catalogue-coverage.json", "fixtures/model-and-invariant-scenarios.json"];
  if (relativePath === "runner/reference-runner.mjs") return ["catalogue/kernel-catalogue.json", "schemas/payloads.schema.json", "fixtures/catalogue-coverage.json", "fixtures/model-and-invariant-scenarios.json"];
  if (relativePath === "runner/validate-package.mjs") return ["catalogue/kernel-catalogue.json", "schemas/payloads.schema.json", "invariants/invariants.json", "traceability/traceability.json", "compatibility/from-0.4.0.json", "runner/protocol.json"];
  if (relativePath.startsWith("schemas/") && relativePath !== "schemas/core.schema.json") return ["schemas/core.schema.json"];
  return [];
}

const payloadPaths = walk(packageRoot).filter((relativePath) => !["manifest.json", "manifest.sha256"].includes(relativePath));
const fileInventory = payloadPaths.map((relativePath) => {
  const bytes = fs.readFileSync(path.join(packageRoot, relativePath));
  const isJson = relativePath.endsWith(".json");
  return {
    path: relativePath,
    role: roleFor(relativePath),
    mediaType: isJson ? "application/json" : "text/javascript",
    bytes: bytes.length,
    sha256: sha256(bytes),
    canonicalJsonSha256: isJson ? sha256(canonical(JSON.parse(bytes.toString("utf8")))) : null,
    dependencies: dependenciesFor(relativePath),
  };
});
const fixtureCount = catalogueFixtures.fixtures.length + modelFixtures.fixtures.length;
const requirementCount = traceability.requirements.length;
const commandCount = catalogue.entries.filter((entry) => entry.kind === "COMMAND").length;
const eventCount = catalogue.entries.filter((entry) => entry.kind === "EVENT").length;
const generatorPath = "scripts/generate_phase3_contract_0_5.mjs";
const manifest = {
  schemaVersion,
  contract: { name: "tekroo.kernel.contracts", version: contractVersion, status: "FROZEN_CANDIDATE" },
  generatedAt: "2026-08-11T00:00:00Z",
  authority: { decisionAuthority: "Principal", implementationAuthority: "NONE", productionAuthority: "NONE" },
  sourceLineage: [
    { path: "OUTPUT/adjudication/negative-requirement-decisions.json", sha256: sha256(read("OUTPUT/adjudication/negative-requirement-decisions.json")) },
    { path: "PHASE-1B/008-final-architecture-handoff.md", sha256: sha256(read("PHASE-1B/008-final-architecture-handoff.md")) },
    { path: "OUTPUT/phase-3/step-10-contract-revision-authorization.json", sha256: sha256(read("OUTPUT/phase-3/step-10-contract-revision-authorization.json")) },
    { path: "OUTPUT/phase-3/step-10-release-contract-sufficiency.json", sha256: sha256(read("OUTPUT/phase-3/step-10-release-contract-sufficiency.json")) },
    { path: "OUTPUT/phase-3/step-10-release-contract-encoding-gaps.md", sha256: sha256(read("OUTPUT/phase-3/step-10-release-contract-encoding-gaps.md")) },
    { path: "OUTPUT/phase-3/step-9-acceptance.json", sha256: sha256(read("OUTPUT/phase-3/step-9-acceptance.json")) },
    { path: "OUTPUT/phase-3/step-9-release-receipt.json", sha256: sha256(read("OUTPUT/phase-3/step-9-release-receipt.json")) },
    { path: `CONTRACTS/tekroo.kernel.contracts/${predecessorVersion}/manifest.json`, sha256: sha256(predecessorManifestBytes) },
    { path: generatorPath, sha256: sha256(read(generatorPath)) },
  ],
  canonicalization: { textEncoding: "UTF-8", byteOrderMark: false, lineEnding: "LF", jsonSemanticCanonicalization: "RFC-8785-compatible sorted-key canonical JSON", byteDigest: "SHA-256", manifestDigest: "detached manifest.sha256" },
  inventoryBoundary: "files lists every payload asset; manifest.json is identified by detached manifest.sha256 and manifest.sha256 is not self-listed.",
  counts: {
    catalogueEntries: catalogue.entries.length,
    commandTypes: commandCount,
    eventTypes: eventCount,
    schemas: fileInventory.filter((item) => item.role === "schemas").length,
    fixtures: fixtureCount,
    invariants: invariants.invariants.length,
    traceabilityRequirements: requirementCount,
  },
  profiles: [
    { name: "contract-structure", authorization: "PHASE_3_STEP_10_CONTRACT_REVISION", required: true, currentStatus: "READY_TO_RUN" },
    { name: "core-hermetic", authorization: "PHASE_3_STEP_9_FORWARD_REQUALIFICATION", required: true, currentStatus: "REFERENCE_MODEL_ONLY" },
    { name: "mongo-integration", authorization: "FUTURE_IMPLEMENTATION_GATE", required: true, currentStatus: "NOT_RUN" },
    { name: "synthesized-merge", authorization: "FUTURE_MERGE_GATE", required: true, currentStatus: "NOT_RUN" },
    { name: "provider-e2e", authorization: "SEPARATE_PRINCIPAL_AUTHORIZATION", required: false, currentStatus: "NOT_RUN" },
  ],
  files: fileInventory,
  knownLimitations: [
    "No Git or provider release coordinator is implemented or qualified by this contract package alone.",
    "The accepted Step 9 Go implementation requires forward requalification under 1.4.0 before a later implementation may consume the successor contract.",
    "Legacy story acceptance records cannot be inferred or silently upgraded into verified release records.",
    "Mongo integration, synthesized merge, OpenHands, SMA, providers, deployment, migration execution, and production profiles are not run by the contract gate.",
    "Performance and resource budgets require later measured workload evidence.",
  ],
};
fs.writeFileSync(manifestPath, pretty(manifest), { flag: "wx" });
const manifestDigest = sha256(fs.readFileSync(manifestPath));
fs.writeFileSync(checksumPath, `${manifestDigest}  manifest.json\n`, { flag: "wx" });
process.stdout.write(`GENERATED ${contractIdentity} ${manifestDigest} ${fileInventory.length}\n`);
