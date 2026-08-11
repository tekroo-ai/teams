#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const predecessorVersion = "0.2.0";
const contractVersion = "0.3.0";
const schemaVersion = "1.2.0";
const contractIdentity = `tekroo.kernel.contracts/${contractVersion}`;
const predecessorRoot = path.join(repoRoot, `CONTRACTS/tekroo.kernel.contracts/${predecessorVersion}`);
const packageRoot = path.join(repoRoot, `CONTRACTS/${contractIdentity}`);
const manifestPath = path.join(packageRoot, "manifest.json");
const checksumPath = path.join(packageRoot, "manifest.sha256");

if (fs.existsSync(packageRoot)) throw new Error(`contract ${contractVersion} already exists; refusing in-place regeneration`);

const uuid7Pattern = "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$";
const shaPattern = "^[0-9a-f]{64}$";
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
    .replaceAll("tekroo.kernel.contracts/0.2.0", contractIdentity)
    .replaceAll("_1_1_0", "_1_2_0")
    .replaceAll("1.1.0", schemaVersion);
}

function advance(value) {
  if (typeof value === "string") return advanceString(value);
  if (Array.isArray(value)) return value.map(advance);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, child]) => [advanceString(key), advance(child)]));
  }
  return value;
}

const string = (options = {}) => ({ type: "string", ...options });
const integer = (options = {}) => ({ type: "integer", ...options });
const array = (items, options = {}) => ({ type: "array", items, ...options });
const object = (properties, required = Object.keys(properties)) => ({ type: "object", additionalProperties: false, properties, required });
const uuid = () => string({ pattern: uuid7Pattern });
const digest = () => string({ pattern: shaPattern });
const actor = () => string({ pattern: actorPattern });
const nonempty = () => string({ minLength: 1, maxLength: 4096 });
const uuidList = (options = {}) => array(uuid(), { maxItems: 64, uniqueItems: true, ...options });
const stringList = (options = {}) => array(nonempty(), { maxItems: 64, uniqueItems: true, ...options });
const principal = () => object({ kind: string({ enum: ["ACTOR", "HUMAN", "SERVICE", "POLICY"] }), id: string({ minLength: 1, maxLength: 256 }) });

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
  "compatibility/from-0.1.0.json",
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

const branchSpec = object({
  branch_id: nonempty(),
  validator: principal(),
  resolution_owner_fqn: actor(),
  acceptance_criteria: stringList({ minItems: 1 }),
  input_evidence_ids: uuidList({ minItems: 1 }),
  deadline_at: string({ format: "date-time" }),
  round_limit: integer({ minimum: 1, maximum: 1000 }),
});
const adjudicationSpec = object({
  adjudicator: principal(),
  deadline_at: string({ format: "date-time" }),
  round_limit: integer({ minimum: 1, maximum: 1000 }),
});
const findingSpec = object({
  finding_id: uuid(),
  finding_key: digest(),
  classification: string({ enum: ["DEFECT", "RISK", "POLICY_VIOLATION", "MISSING_EVIDENCE", "OTHER"] }),
  summary: nonempty(),
  evidence_ids: uuidList({ minItems: 1 }),
});

const openProperties = {
  subject_kind: string({ enum: ["story", "task"] }),
  subject_id: uuid(),
  lifecycle_epoch: integer({ minimum: 1 }),
  criteria_revision: integer({ minimum: 1 }),
  evidence_set_digest: digest(),
  branch_policy_revision: integer({ minimum: 1 }),
  branches: array(branchSpec, { minItems: 1, maxItems: 32, uniqueItems: true }),
  join_rule: string({ enum: ["ALL_PASS"] }),
  partial_result_policy: string({ enum: ["WAIT_ALL", "FAIL_FAST"] }),
  adjudication: adjudicationSpec,
};
const resultProperties = {
  review_id: uuid(),
  branch_id: nonempty(),
  branch_policy_revision: integer({ minimum: 1 }),
  source_role: string({ enum: ["VALIDATOR", "ADJUDICATOR", "POLICY_TIMEOUT"] }),
  round: integer({ minimum: 1, maximum: 1000 }),
  result: string({ enum: ["PASS", "FAIL", "BLOCKED", "INCONCLUSIVE", "SUPERSEDED", "CANCELLED"] }),
  reasons: stringList(),
  evidence_ids: uuidList({ minItems: 1 }),
  findings: array(findingSpec, { maxItems: 64, uniqueItems: true }),
  supersedes_result_event_ids: uuidList(),
  changed_condition_evidence_ids: uuidList(),
};
const finalizeProperties = {
  review_id: uuid(),
  subject_kind: string({ enum: ["story", "task"] }),
  subject_id: uuid(),
  lifecycle_epoch: integer({ minimum: 1 }),
  branch_policy_revision: integer({ minimum: 1 }),
  expected_review_revision: integer({ minimum: 1 }),
  terminal_status: string({ enum: ["PASS", "FAIL", "BLOCKED", "INCONCLUSIVE", "SUPERSEDED", "CANCELLED"] }),
  result_event_ids: uuidList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
};
const completionReviewFields = {
  completion_review_id: uuid(),
  completion_review_revision: integer({ minimum: 1 }),
  branch_policy_revision: integer({ minimum: 1 }),
  validation_finalized_event_id: uuid(),
};

const payloadPath = "schemas/payloads.schema.json";
const payloads = readJson(`CONTRACTS/${contractIdentity}/${payloadPath}`);
payloads.$defs.tekroo_command_completion_review_open_1_2_0 = object(openProperties);
payloads.$defs.tekroo_event_completion_review_opened_1_2_0 = object(openProperties);
payloads.$defs.tekroo_command_completion_review_record_result_1_2_0 = object(resultProperties);
payloads.$defs.tekroo_event_completion_review_result_recorded_1_2_0 = object(resultProperties);
payloads.$defs.tekroo_command_completion_review_finalize_1_2_0 = object(finalizeProperties);
payloads.$defs.tekroo_event_completion_review_finalized_1_2_0 = object(finalizeProperties);
for (const name of [
	"tekroo_command_task_request_completion_1_2_0",
	"tekroo_event_task_completed_1_2_0",
	"tekroo_command_story_request_completion_1_2_0",
	"tekroo_event_story_completed_1_2_0",
]) {
	const prior = payloads.$defs[name];
	prior.properties = { ...prior.properties, ...completionReviewFields };
	prior.required = [...new Set([...prior.required.filter((field) => field !== "validation_event_ids"), ...Object.keys(completionReviewFields)])];
	delete prior.properties.validation_event_ids;
}
overwriteJson(payloadPath, payloads);

const cataloguePath = "catalogue/kernel-catalogue.json";
const catalogue = readJson(`CONTRACTS/${contractIdentity}/${cataloguePath}`);
catalogue.revision = 3;
const changedTypes = new Set([
  "tekroo.command.completion-review.open", "tekroo.event.completion-review.opened",
  "tekroo.command.completion-review.record-result", "tekroo.event.completion-review.result-recorded",
  "tekroo.command.task.request-completion", "tekroo.event.task.completed",
  "tekroo.command.story.request-completion", "tekroo.event.story.completed",
]);
for (const entry of catalogue.entries) {
  if (changedTypes.has(entry.typeId)) entry.compatibility = { acceptedSourceVersions: [schemaVersion], transforms: [] };
  else entry.compatibility = { acceptedSourceVersions: ["1.1.0", schemaVersion], transforms: [{ sourceVersion: "1.1.0", targetVersion: schemaVersion, mode: "IDENTITY" }] };
	if (entry.typeId === "tekroo.command.completion-review.open") entry.authorityKinds = ["POLICY"];
}
const commonEntry = {
  version: schemaVersion,
  lifecycle: "ACTIVE",
  owner: "tekroo-kernel",
  targetKinds: ["completion-review"],
  executionRequired: false,
  allowedParentEdges: ["CAUSAL", "RESPONSE", "RETRY", "DERIVATION", "SUPERSESSION"],
  rootAllowed: false,
  aliases: [],
  compatibility: { acceptedSourceVersions: [schemaVersion], transforms: [] },
};
catalogue.entries.push({
  ...commonEntry,
  typeId: "tekroo.command.completion-review.finalize", kind: "COMMAND", authorityKinds: ["POLICY"], routingMode: "KERNEL_DIRECT",
  payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_command_completion_review_finalize_1_2_0",
  emits: ["tekroo.event.completion-review.finalized"],
});
catalogue.entries.push({
  ...commonEntry,
  typeId: "tekroo.event.completion-review.finalized", kind: "EVENT", authorityKinds: [], routingMode: "COMMITTED_EVENT",
  payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_event_completion_review_finalized_1_2_0",
  acceptedCommandTypes: ["tekroo.command.completion-review.finalize"],
});
catalogue.entries.sort((left, right) => left.typeId.localeCompare(right.typeId));
overwriteJson(cataloguePath, catalogue);

const testsEvidence = "00000000-0000-7000-8000-000000000207";
const reviewEvidence = "00000000-0000-7000-8000-000000000208";
const reviewID = "00000000-0000-7000-8000-000000000501";
const resultEventID = "00000000-0000-7000-8000-000000000601";
const finalizedEventID = "00000000-0000-7000-8000-000000000602";
const branches = [
  { branch_id: "tests", validator: { kind: "SERVICE", id: "validator-tests" }, resolution_owner_fqn: "teams::coder-1", acceptance_criteria: ["all required tests pass"], input_evidence_ids: [testsEvidence], deadline_at: "2026-08-12T00:00:00Z", round_limit: 2 },
  { branch_id: "review", validator: { kind: "ACTOR", id: "teams::reviewer-1" }, resolution_owner_fqn: "teams::coder-1", acceptance_criteria: ["review findings resolved"], input_evidence_ids: [reviewEvidence], deadline_at: "2026-08-12T00:00:00Z", round_limit: 2 },
];
const adjudication = { adjudicator: { kind: "POLICY", id: "validation-adjudicator" }, deadline_at: "2026-08-13T00:00:00Z", round_limit: 1 };

const catalogueFixturesPath = "fixtures/catalogue-coverage.json";
const catalogueFixtures = readJson(`CONTRACTS/${contractIdentity}/${catalogueFixturesPath}`);
for (const fixture of catalogueFixtures.fixtures) {
  if (fixture.classification !== "NORMATIVE_EXAMPLE") continue;
  const payload = fixture.when.payload;
  switch (fixture.when.commandType) {
    case "tekroo.command.completion-review.open":
      delete payload.required_branch_ids;
      payload.branches = branches;
      payload.adjudication = adjudication;
      break;
    case "tekroo.command.completion-review.record-result":
      Object.assign(payload, { source_role: "VALIDATOR", round: 1, findings: [], supersedes_result_event_ids: [], changed_condition_evidence_ids: [] });
      break;
    case "tekroo.command.task.request-completion":
    case "tekroo.command.story.request-completion":
      delete payload.validation_event_ids;
      Object.assign(payload, { completion_review_id: reviewID, completion_review_revision: 3, branch_policy_revision: 1, validation_finalized_event_id: finalizedEventID });
      break;
  }
}
catalogueFixtures.fixtures.push(
  {
    classification: "NORMATIVE_EXAMPLE", fixtureId: "CAT-027-COMPLETION-REVIEW-FINALIZE-VALID", kind: "CATALOGUE_COMMAND", sourceDecisionIds: ["P2-KCF-008"],
    given: { contractManifest: contractIdentity },
    when: { commandType: "tekroo.command.completion-review.finalize", payload: { review_id: reviewID, subject_kind: "task", subject_id: "00000000-0000-7000-8000-000000000101", lifecycle_epoch: 1, branch_policy_revision: 1, expected_review_revision: 3, terminal_status: "PASS", result_event_ids: [resultEventID], evidence_ids: [testsEvidence] } },
    then: { expected: { outcomeCode: "APPLIED", eventTypes: ["tekroo.event.completion-review.finalized"] } },
  },
  {
    classification: "BOUNDARY_NEGATIVE", fixtureId: "CAT-027-COMPLETION-REVIEW-FINALIZE-INVALID", kind: "CATALOGUE_COMMAND", sourceDecisionIds: ["P2-KCF-008"],
    given: { contractManifest: contractIdentity },
    when: { commandType: "tekroo.command.completion-review.finalize", payload: { review_id: reviewID, subject_kind: "task", subject_id: "00000000-0000-7000-8000-000000000101", lifecycle_epoch: 1, branch_policy_revision: 1, expected_review_revision: 3, terminal_status: "PASS", result_event_ids: [resultEventID] } },
    then: { expected: { outcomeCode: "REJECTED_INVALID", eventTypes: [] } },
  },
);
catalogueFixtures.fixtures.sort((left, right) => left.fixtureId.localeCompare(right.fixtureId));
overwriteJson(catalogueFixturesPath, catalogueFixtures);

const modelFixturesPath = "fixtures/model-and-invariant-scenarios.json";
const modelFixtures = readJson(`CONTRACTS/${contractIdentity}/${modelFixturesPath}`);
const boundedBase = {
  branch: branches[0],
  adjudication,
};
modelFixtures.fixtures.push(
  boundedFixture("REVIEW-BOUNDED-VALIDATOR-PASS", "NORMATIVE_EXAMPLE", boundedBase, { sourceRole: "VALIDATOR", authority: branches[0].validator, round: 1, result: "PASS", decidedAt: "2026-08-11T12:00:00Z", supersedesResultEventIds: [], changedConditionEvidenceIds: [] }, true, "ACCEPTED"),
  boundedFixture("REVIEW-BOUNDED-WRONG-VALIDATOR", "BOUNDARY_NEGATIVE", boundedBase, { sourceRole: "VALIDATOR", authority: { kind: "SERVICE", id: "different-validator" }, round: 1, result: "PASS", decidedAt: "2026-08-11T12:00:00Z", supersedesResultEventIds: [], changedConditionEvidenceIds: [] }, false, "VALIDATOR_MISMATCH"),
  boundedFixture("REVIEW-BOUNDED-DEADLINE", "BOUNDARY_NEGATIVE", boundedBase, { sourceRole: "VALIDATOR", authority: branches[0].validator, round: 1, result: "PASS", decidedAt: "2026-08-12T00:00:01Z", supersedesResultEventIds: [], changedConditionEvidenceIds: [] }, false, "BRANCH_DEADLINE_EXCEEDED"),
  boundedFixture("REVIEW-BOUNDED-ROUND-LIMIT", "BOUNDARY_NEGATIVE", boundedBase, { sourceRole: "VALIDATOR", authority: branches[0].validator, round: 3, result: "PASS", decidedAt: "2026-08-11T12:00:00Z", supersedesResultEventIds: [], changedConditionEvidenceIds: [] }, false, "BRANCH_ROUND_EXHAUSTED"),
  boundedFixture("REVIEW-BOUNDED-CHANGED-CONDITION", "BOUNDARY_NEGATIVE", boundedBase, { sourceRole: "VALIDATOR", authority: branches[0].validator, round: 2, result: "PASS", decidedAt: "2026-08-11T12:00:00Z", supersedesResultEventIds: [resultEventID], changedConditionEvidenceIds: [] }, false, "CHANGED_CONDITION_REQUIRED"),
  boundedFixture("REVIEW-BOUNDED-ADJUDICATOR", "NORMATIVE_EXAMPLE", boundedBase, { sourceRole: "ADJUDICATOR", authority: adjudication.adjudicator, round: 1, result: "FAIL", decidedAt: "2026-08-12T12:00:00Z", supersedesResultEventIds: [resultEventID], changedConditionEvidenceIds: [testsEvidence] }, true, "ACCEPTED"),
  boundedFixture("REVIEW-BOUNDED-POLICY-TIMEOUT", "NORMATIVE_EXAMPLE", boundedBase, { sourceRole: "POLICY_TIMEOUT", authority: { kind: "POLICY", id: "validation-policy" }, round: 1, result: "BLOCKED", decidedAt: "2026-08-12T00:00:01Z", supersedesResultEventIds: [], changedConditionEvidenceIds: [] }, true, "ACCEPTED"),
	boundedFixture("REVIEW-BOUNDED-POST-FINALIZATION", "BOUNDARY_NEGATIVE", { ...boundedBase, finalized: true }, { sourceRole: "VALIDATOR", authority: branches[0].validator, round: 1, result: "PASS", decidedAt: "2026-08-11T12:00:00Z", supersedesResultEventIds: [], changedConditionEvidenceIds: [] }, false, "REVIEW_ALREADY_FINALIZED"),
);
modelFixtures.fixtures.sort((left, right) => left.fixtureId.localeCompare(right.fixtureId));
overwriteJson(modelFixturesPath, modelFixtures);

function boundedFixture(fixtureId, classification, given, when, accepted, reason) {
  return { classification, fixtureId, kind: "BOUNDED_REVIEW_MODEL", sourceDecisionIds: ["P2-KCF-008"], given, when, then: { expected: { accepted, reason } } };
}

const fixtureSchemaPath = "schemas/conformance-fixture.schema.json";
const fixtureSchema = readJson(`CONTRACTS/${contractIdentity}/${fixtureSchemaPath}`);
fixtureSchema.properties.kind.enum.push("BOUNDED_REVIEW_MODEL");
overwriteJson(fixtureSchemaPath, fixtureSchema);

const invariantsPath = "invariants/invariants.json";
const invariants = readJson(`CONTRACTS/${contractIdentity}/${invariantsPath}`);
invariants.invariants.push(
  { invariantId: "INV-015-BOUNDED-VALIDATION-BRANCH", description: "Every validation branch has exact validator and resolution-owner identity, explicit inputs and criteria, a finite round limit, and a deadline.", decisionIds: ["P2-KCF-008"], negativeRequirementIds: ["NEG-002"], fixtureIds: ["REVIEW-BOUNDED-VALIDATOR-PASS", "REVIEW-BOUNDED-WRONG-VALIDATOR", "REVIEW-BOUNDED-DEADLINE", "REVIEW-BOUNDED-ROUND-LIMIT"] },
  { invariantId: "INV-016-VALIDATION-SUPERSESSION-ADJUDICATION", description: "Finding keys deduplicate exact logical findings; revalidation cites superseded results and changed-condition evidence; inconclusive or conflicting review is resolved only by the exact bounded adjudicator.", decisionIds: ["P2-KCF-008"], negativeRequirementIds: ["NEG-002"], fixtureIds: ["REVIEW-BOUNDED-CHANGED-CONDITION", "REVIEW-BOUNDED-ADJUDICATOR", "REVIEW-BOUNDED-POLICY-TIMEOUT"] },
  { invariantId: "INV-017-POLICY-FINALIZED-COMPLETION", description: "Authoritative completion references one current policy-finalized bounded review, cannot treat an individual validator result as organizational completion, and rejects post-finalization results.", decisionIds: ["P2-KCF-008"], negativeRequirementIds: ["NEG-002", "NEG-007"], fixtureIds: ["CAT-027-COMPLETION-REVIEW-FINALIZE-VALID", "CAT-027-COMPLETION-REVIEW-FINALIZE-INVALID", "REVIEW-BOUNDED-POST-FINALIZATION"] },
);
invariants.invariants.sort((left, right) => left.invariantId.localeCompare(right.invariantId));
overwriteJson(invariantsPath, invariants);

const traceabilityPath = "traceability/traceability.json";
const traceability = readJson(`CONTRACTS/${contractIdentity}/${traceabilityPath}`);
const boundedReviewTestIds = [
  "INV-015-BOUNDED-VALIDATION-BRANCH",
  "INV-016-VALIDATION-SUPERSESSION-ADJUDICATION",
  "INV-017-POLICY-FINALIZED-COMPLETION",
  "CAT-027-COMPLETION-REVIEW-FINALIZE-INVALID",
  "CAT-027-COMPLETION-REVIEW-FINALIZE-VALID",
  "REVIEW-BOUNDED-ADJUDICATOR",
  "REVIEW-BOUNDED-CHANGED-CONDITION",
  "REVIEW-BOUNDED-DEADLINE",
  "REVIEW-BOUNDED-POLICY-TIMEOUT",
	"REVIEW-BOUNDED-POST-FINALIZATION",
  "REVIEW-BOUNDED-ROUND-LIMIT",
  "REVIEW-BOUNDED-VALIDATOR-PASS",
  "REVIEW-BOUNDED-WRONG-VALIDATOR",
];
for (const requirement of traceability.requirements) {
  if (requirement.requirementId === "NEG-002") requirement.testIds = [...new Set([...requirement.testIds, ...boundedReviewTestIds])].sort();
  if (requirement.requirementId === "P2-KCF-008") requirement.testIds = [...new Set([...requirement.testIds, ...boundedReviewTestIds])].sort();
}
overwriteJson(traceabilityPath, traceability);

const predecessorManifestBytes = fs.readFileSync(path.join(predecessorRoot, "manifest.json"));
const unchangedCommands = catalogue.entries.filter((entry) => entry.kind === "COMMAND" && !changedTypes.has(entry.typeId) && entry.typeId !== "tekroo.command.completion-review.finalize").map((entry) => entry.typeId).sort();
writeJson("compatibility/from-0.2.0.json", {
  schemaVersion,
  contractName: "tekroo.kernel.contracts",
  contractVersion,
  predecessor: { contractVersion: predecessorVersion, contractIdentity: `tekroo.kernel.contracts/${predecessorVersion}`, manifestSha256: sha256(predecessorManifestBytes) },
  compatibilityClaim: "BREAKING_BOUNDED_VALIDATION_REVISION_WITH_EXPLICIT_ADAPTER_MIGRATION",
  directions: { backward: "PARTIAL_ADAPTER_REQUIRED", forward: "NOT_COMPATIBLE", replay: "VERSION_GATED", adapter: "REQUIRED" },
  unchangedSemanticTypes: unchangedCommands,
  breakingChanges: [
    "CompletionReviewOpen 1.2.0 replaces bare branch IDs with exact bounded branch and adjudication specifications.",
    "CompletionReviewRecordResult 1.2.0 adds source role, round, complete terminal outcomes, typed findings, supersession, and changed-condition evidence.",
    "TaskCompletion and StoryCompletion 1.2.0 require an exact current policy-finalized completion review.",
    "CompletionReviewFinalize 1.2.0 is a new policy-only organizational aggregation command.",
  ],
  migrationRules: [
    { source: "1.1.0", target: schemaVersion, mode: "IDENTITY", appliesTo: unchangedCommands },
    { source: "1.1.0", target: schemaVersion, mode: "REJECT_WITHOUT_EXACT_CONTEXT", appliesTo: ["tekroo.command.completion-review.open", "tekroo.command.completion-review.record-result", "tekroo.command.task.request-completion", "tekroo.command.story.request-completion"], reason: "Validator, owner, deadline, per-branch budget, adjudication, supersession, and finalization context cannot be inferred." },
    { source: schemaVersion, target: schemaVersion, mode: "IDENTITY" },
  ],
  rollback: "Readers retain 0.2.0 for historical replay. New bounded-review and finalization commands are never down-converted to 1.1.0.",
});

let referenceRunner = fs.readFileSync(path.join(predecessorRoot, "runner/reference-runner.mjs"), "utf8");
referenceRunner = referenceRunner.replace(
  /function runReviewJoinModel\(fixture\) \{[\s\S]*?\n\}\n\nfunction runSuccessorSetModel/,
  `function runReviewJoinModel(fixture) {
  const required = new Set(fixture.given.requiredBranchIds);
  const results = new Map();
  const precedence = ["FAIL", "BLOCKED", "INCONCLUSIVE", "CANCELLED", "SUPERSEDED"];
  for (const item of fixture.when.results) {
    if (!required.has(item.branchId) || (results.has(item.branchId) && results.get(item.branchId) !== item.result)) return { status: "CONFLICT", complete: false };
    results.set(item.branchId, item.result);
    if (item.result === "FAIL" && fixture.given.partialResultPolicy === "FAIL_FAST") return { status: "FAIL", complete: true };
  }
  if (results.size !== required.size) return { status: "PENDING", complete: false };
  const values = [...results.values()];
  const status = precedence.find((candidate) => values.includes(candidate)) ?? "PASS";
  return { status, complete: true };
}

function runBoundedReviewModel(fixture) {
  const { branch, adjudication } = fixture.given;
  const submission = fixture.when;
  const samePrincipal = (left, right) => left?.kind === right?.kind && left?.id === right?.id;
	if (fixture.given.finalized === true) return { accepted: false, reason: "REVIEW_ALREADY_FINALIZED" };
  if (submission.supersedesResultEventIds.length > 0 && submission.changedConditionEvidenceIds.length === 0) return { accepted: false, reason: "CHANGED_CONDITION_REQUIRED" };
  if (submission.sourceRole === "VALIDATOR") {
    if (!samePrincipal(submission.authority, branch.validator)) return { accepted: false, reason: "VALIDATOR_MISMATCH" };
    if (submission.round > branch.round_limit) return { accepted: false, reason: "BRANCH_ROUND_EXHAUSTED" };
    if (Date.parse(submission.decidedAt) > Date.parse(branch.deadline_at)) return { accepted: false, reason: "BRANCH_DEADLINE_EXCEEDED" };
  } else if (submission.sourceRole === "ADJUDICATOR") {
    if (!samePrincipal(submission.authority, adjudication.adjudicator)) return { accepted: false, reason: "ADJUDICATOR_MISMATCH" };
    if (submission.round > adjudication.round_limit) return { accepted: false, reason: "ADJUDICATION_EXHAUSTED" };
    if (Date.parse(submission.decidedAt) > Date.parse(adjudication.deadline_at)) return { accepted: false, reason: "ADJUDICATION_DEADLINE_EXCEEDED" };
  } else if (submission.sourceRole === "POLICY_TIMEOUT") {
    if (submission.authority.kind !== "POLICY") return { accepted: false, reason: "POLICY_AUTHORITY_REQUIRED" };
    if (Date.parse(submission.decidedAt) <= Date.parse(branch.deadline_at)) return { accepted: false, reason: "DEADLINE_NOT_REACHED" };
    if (!["BLOCKED", "CANCELLED"].includes(submission.result)) return { accepted: false, reason: "INVALID_TIMEOUT_RESULT" };
  } else return { accepted: false, reason: "INVALID_SOURCE_ROLE" };
  return { accepted: true, reason: "ACCEPTED" };
}

function runSuccessorSetModel`,
);
referenceRunner = referenceRunner.replace(
  '  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {',
  '  } else if (fixture.kind === "BOUNDED_REVIEW_MODEL") {\n    actual = runBoundedReviewModel(fixture);\n  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {',
);
fs.mkdirSync(path.join(packageRoot, "runner"), { recursive: true });
fs.writeFileSync(path.join(packageRoot, "runner/reference-runner.mjs"), referenceRunner, { flag: "wx" });

let validator = fs.readFileSync(path.join(predecessorRoot, "runner/validate-package.mjs"), "utf8")
  .replace('manifest.contract.version === "0.2.0"', 'manifest.contract.version === "0.3.0"')
  .replaceAll("compatibility/from-0.1.0.json", "compatibility/from-0.2.0.json")
  .replace(
    'compatibility.predecessor?.contractVersion === "0.1.0" && compatibility.contractVersion === "0.2.0" && compatibility.directions?.adapter === "REQUIRED"',
    'compatibility.predecessor?.contractVersion === "0.2.0" && compatibility.contractVersion === "0.3.0" && compatibility.directions?.adapter === "REQUIRED"',
  );
fs.writeFileSync(path.join(packageRoot, "runner/validate-package.mjs"), validator, { flag: "wx" });

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
  if (relativePath === "runner/validate-package.mjs") return ["catalogue/kernel-catalogue.json", "schemas/payloads.schema.json", "invariants/invariants.json", "traceability/traceability.json", "compatibility/from-0.2.0.json", "runner/protocol.json"];
  if (relativePath.startsWith("schemas/") && relativePath !== "schemas/core.schema.json") return ["schemas/core.schema.json"];
  return [];
}

const payloadPaths = walk(packageRoot).filter((relativePath) => !["manifest.json", "manifest.sha256"].includes(relativePath));
const fileInventory = payloadPaths.map((relativePath) => {
  const bytes = fs.readFileSync(path.join(packageRoot, relativePath));
  const isJson = relativePath.endsWith(".json");
  return { path: relativePath, role: roleFor(relativePath), mediaType: isJson ? "application/json" : "text/javascript", bytes: bytes.length, sha256: sha256(bytes), canonicalJsonSha256: isJson ? sha256(canonical(JSON.parse(bytes.toString("utf8")))) : null, dependencies: dependenciesFor(relativePath) };
});
const fixtureCount = catalogueFixtures.fixtures.length + modelFixtures.fixtures.length;
const requirementCount = traceability.requirements.length;
const commandCount = catalogue.entries.filter((entry) => entry.kind === "COMMAND").length;
const eventCount = catalogue.entries.filter((entry) => entry.kind === "EVENT").length;
const generatorPath = "scripts/generate_phase3_contract_0_3.mjs";
const manifest = {
  schemaVersion,
  contract: { name: "tekroo.kernel.contracts", version: contractVersion, status: "FROZEN_CANDIDATE" },
  generatedAt: "2026-08-11T00:00:00Z",
  authority: { decisionAuthority: "Principal", implementationAuthority: "NONE", productionAuthority: "NONE" },
  sourceLineage: [
    { path: "OUTPUT/adjudication/negative-requirement-decisions.json", sha256: sha256(read("OUTPUT/adjudication/negative-requirement-decisions.json")) },
    { path: "PHASE-1B/008-final-architecture-handoff.md", sha256: sha256(read("PHASE-1B/008-final-architecture-handoff.md")) },
    { path: "OUTPUT/phase-3/step-7-contract-revision-authorization.json", sha256: sha256(read("OUTPUT/phase-3/step-7-contract-revision-authorization.json")) },
    { path: "OUTPUT/phase-3/step-7-contract-encoding-gaps.md", sha256: sha256(read("OUTPUT/phase-3/step-7-contract-encoding-gaps.md")) },
    { path: `CONTRACTS/tekroo.kernel.contracts/${predecessorVersion}/manifest.json`, sha256: sha256(predecessorManifestBytes) },
    { path: generatorPath, sha256: sha256(read(generatorPath)) },
  ],
  canonicalization: { textEncoding: "UTF-8", byteOrderMark: false, lineEnding: "LF", jsonSemanticCanonicalization: "RFC-8785-compatible sorted-key canonical JSON", byteDigest: "SHA-256", manifestDigest: "detached manifest.sha256" },
  inventoryBoundary: "files lists every payload asset; manifest.json is identified by detached manifest.sha256 and manifest.sha256 is not self-listed.",
  counts: { catalogueEntries: catalogue.entries.length, commandTypes: commandCount, eventTypes: eventCount, schemas: fileInventory.filter((item) => item.role === "schemas").length, fixtures: fixtureCount, invariants: invariants.invariants.length, traceabilityRequirements: requirementCount },
  profiles: [
    { name: "contract-structure", authorization: "PHASE_3_STEP_7", required: true, currentStatus: "READY_TO_RUN" },
    { name: "core-hermetic", authorization: "PHASE_3_STEP_7_IMPLEMENTATION_EXTENSION", required: true, currentStatus: "REFERENCE_MODEL_ONLY" },
    { name: "mongo-integration", authorization: "FUTURE_IMPLEMENTATION_GATE", required: true, currentStatus: "NOT_RUN" },
    { name: "synthesized-merge", authorization: "FUTURE_MERGE_GATE", required: true, currentStatus: "NOT_RUN" },
    { name: "provider-e2e", authorization: "SEPARATE_PRINCIPAL_AUTHORIZATION", required: false, currentStatus: "NOT_RUN" },
  ],
  files: fileInventory,
  knownLimitations: [
    "No Tekroo v4 runtime implementation is qualified by this contract package alone.",
    "The Step 6 Go implementation requires a separately gated 1.2.0 extension before authoritative completion may consume bounded-review finalization.",
    "Mongo integration, synthesized merge, OpenHands, SMA, providers, deployment, migration execution, and production profiles are not run by the contract gate.",
    "Performance and resource budgets require later measured workload evidence.",
    "Version 1.1.0 review and completion commands lacking exact bounded-review context cannot be inferred or silently upgraded.",
  ],
};
fs.writeFileSync(manifestPath, pretty(manifest), { flag: "wx" });
const manifestDigest = sha256(fs.readFileSync(manifestPath));
fs.writeFileSync(checksumPath, `${manifestDigest}  manifest.json\n`, { flag: "wx" });
process.stdout.write(`GENERATED ${contractIdentity} ${manifestDigest} ${fileInventory.length}\n`);
