#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const contractVersion = "0.2.0";
const contractIdentity = `tekroo.kernel.contracts/${contractVersion}`;
const schemaVersion = "1.1.0";
const predecessorVersion = "0.1.0";
const predecessorRoot = path.join(repoRoot, `CONTRACTS/tekroo.kernel.contracts/${predecessorVersion}`);
const packageRoot = path.join(repoRoot, `CONTRACTS/${contractIdentity}`);
const manifestPath = path.join(packageRoot, "manifest.json");
const checksumPath = path.join(packageRoot, "manifest.sha256");

if (fs.existsSync(manifestPath) || fs.existsSync(checksumPath)) {
  throw new Error(`contract ${contractVersion} is already sealed; refusing in-place regeneration`);
}

function sortValue(value) {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortValue(value[key])]));
  }
  return value;
}

function canonical(value) {
  return JSON.stringify(sortValue(value));
}

function pretty(value) {
  return `${JSON.stringify(sortValue(value), null, 2)}\n`;
}

function sha256(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
}

function read(relativePath) {
  return fs.readFileSync(path.join(repoRoot, relativePath));
}

function readJson(relativePath) {
  return JSON.parse(read(relativePath).toString("utf8"));
}

function writeJson(relativePath, value) {
  const absolute = path.join(packageRoot, relativePath);
  fs.mkdirSync(path.dirname(absolute), { recursive: true });
  fs.writeFileSync(absolute, pretty(value), { flag: "wx" });
}

const actorPattern = "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])-[1-9][0-9]{0,19}$";
const uuid7Pattern = "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$";
const shaPattern = "^[0-9a-f]{64}$";
const typePattern = "^tekroo\\.(command|event)\\.[a-z0-9]+(?:[.-][a-z0-9]+)*$";
const schemaDialect = "https://json-schema.org/draft/2020-12/schema";
const contractBase = `https://contracts.tekroo.ai/${contractIdentity}`;

const string = (options = {}) => ({ type: "string", ...options });
const integer = (options = {}) => ({ type: "integer", ...options });
const boolean = () => ({ type: "boolean" });
const nullable = (schema) => ({ anyOf: [schema, { type: "null" }] });
const array = (items, options = {}) => ({ type: "array", items, ...options });
const object = (options = {}) => ({ type: "object", ...options });
const uuid = () => string({ pattern: uuid7Pattern });
const digest = () => string({ pattern: shaPattern });
const actor = () => string({ pattern: actorPattern });
const nonempty = () => string({ minLength: 1, maxLength: 4096 });
const stringList = (options = {}) => array(nonempty(), { maxItems: 64, uniqueItems: true, ...options });
const uuidList = (options = {}) => array(uuid(), { maxItems: 64, uniqueItems: true, ...options });
const digestList = (options = {}) => array(digest(), { maxItems: 64, uniqueItems: true, ...options });

function operation({
  command,
  event,
  targetKinds,
  authorityKinds,
  executionRequired = false,
  fields,
  optional = [],
  example,
  rootAllowed = false,
  owner = "tekroo-kernel",
}) {
  return { command, event, targetKinds, authorityKinds, executionRequired, fields, optional, example, rootAllowed, owner };
}

const operations = [
  operation({
    command: "tekroo.command.execution.register",
    event: "tekroo.event.execution.registered",
    targetKinds: ["execution"], authorityKinds: ["SERVICE", "POLICY"], rootAllowed: true,
    fields: { actor_fqn: actor(), execution_id: uuid(), fencing_epoch: integer({ minimum: 1 }), runtime_identity: digest() },
    example: { actor_fqn: "teams::coder-1", execution_id: "00000000-0000-7000-8000-000000000001", fencing_epoch: 1, runtime_identity: "a".repeat(64) },
  }),
  operation({
    command: "tekroo.command.execution.replace",
    event: "tekroo.event.execution.replaced",
    targetKinds: ["execution"], authorityKinds: ["SERVICE", "POLICY"],
    fields: { actor_fqn: actor(), prior_execution_id: uuid(), new_execution_id: uuid(), new_fencing_epoch: integer({ minimum: 2 }), reason: nonempty() },
    example: { actor_fqn: "teams::coder-1", prior_execution_id: "00000000-0000-7000-8000-000000000001", new_execution_id: "00000000-0000-7000-8000-000000000002", new_fencing_epoch: 2, reason: "planned restart" },
  }),
  operation({
    command: "tekroo.command.evidence.register",
    event: "tekroo.event.evidence.registered",
    targetKinds: ["evidence"], authorityKinds: ["ACTOR", "HUMAN", "SERVICE", "POLICY"], rootAllowed: true,
    fields: {
      evidence_kind: string({ enum: ["SOURCE_SNAPSHOT", "SOURCE_DIFF", "TEST_RESULT", "TOOL_RESULT", "ARTIFACT", "PROVIDER_RECEIPT", "MODEL_OUTPUT", "HUMAN_ATTESTATION", "DECISION_RECORD", "EXTERNAL_OBSERVATION"] }),
      sha256: digest(), byte_length: integer({ minimum: 0 }), media_type: string({ minLength: 1, maxLength: 255 }),
      canonical_digest: nullable(digest()), locator: nonempty(), locator_immutable: { const: true },
      availability: string({ enum: ["AVAILABLE", "UNAVAILABLE", "DELETED_WITH_TOMBSTONE"] }),
      producing_component: nonempty(), producing_version: nonempty(), source_timestamp: nullable(string({ format: "date-time" })),
      transport_provenance: nonempty(), sensitivity: string({ enum: ["PUBLIC", "INTERNAL", "CONFIDENTIAL", "RESTRICTED"] }),
      access_partition: nonempty(), retention_policy: nonempty(), integrity_state: string({ enum: ["UNVERIFIED", "DIGEST_VERIFIED", "MISSING", "MISMATCH"] }),
      source_evidence_ids: uuidList(),
      computation: nullable(object({
        additionalProperties: false,
        properties: { method: nonempty(), build_digest: digest(), configuration_digest: digest(), deterministic: boolean() },
        required: ["method", "build_digest", "configuration_digest", "deterministic"],
      })),
      redacts: nullable(uuid()),
      deletion_tombstone: nullable(object({
        additionalProperties: false,
        properties: { basis: nonempty(), authority: object({ additionalProperties: false, properties: { kind: string({ enum: ["ACTOR", "HUMAN", "SERVICE", "POLICY"] }), id: string({ minLength: 1, maxLength: 256 }) }, required: ["kind", "id"] }), deleted_at: string({ format: "date-time" }) },
        required: ["basis", "authority", "deleted_at"],
      })),
    },
    example: {
      evidence_kind: "TEST_RESULT", sha256: "b".repeat(64), byte_length: 42, media_type: "application/json",
      canonical_digest: null, locator: "artifact://tests/result-1", locator_immutable: true, availability: "AVAILABLE",
      producing_component: "go-test", producing_version: "go1.26.4", source_timestamp: null,
      transport_provenance: "kernel-adapter://test-runner", sensitivity: "INTERNAL", access_partition: "engineering",
      retention_policy: "phase-2", integrity_state: "DIGEST_VERIFIED", source_evidence_ids: [], computation: null,
      redacts: null, deletion_tombstone: null,
    },
  }),
  operation({
    command: "tekroo.command.story.create",
    event: "tekroo.event.story.created",
    targetKinds: ["story"], authorityKinds: ["ACTOR", "HUMAN", "SERVICE"], rootAllowed: true,
    fields: { title: string({ minLength: 1, maxLength: 256 }), description: string({ maxLength: 65536 }), acceptance_criteria: stringList({ minItems: 1 }) },
    example: { title: "Implement deterministic ownership", description: "Define and verify exact-FQN ownership.", acceptance_criteria: ["one owner wins", "restart preserves ownership"] },
  }),
  operation({
    command: "tekroo.command.story.authorize",
    event: "tekroo.event.story.authorized",
    targetKinds: ["story"], authorityKinds: ["HUMAN", "POLICY"],
    fields: { scope_revision: integer({ minimum: 1 }), reason: nonempty() },
    example: { scope_revision: 1, reason: "scope approved" },
  }),
  operation({
    command: "tekroo.command.story.begin-planning",
    event: "tekroo.event.story.planning-started",
    targetKinds: ["story"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"],
    fields: { planning_budget: integer({ minimum: 1, maximum: 1000 }), accountable_owner_fqn: actor() },
    example: { planning_budget: 4, accountable_owner_fqn: "teams::pm-1" },
  }),
  operation({
    command: "tekroo.command.story.activate",
    event: "tekroo.event.story.activated",
    targetKinds: ["story"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"],
    fields: { plan_digest: digest(), required_task_ids: uuidList() },
    example: { plan_digest: "c".repeat(64), required_task_ids: ["00000000-0000-7000-8000-000000000101"] },
  }),
  operation({
    command: "tekroo.command.work.block",
    event: "tekroo.event.work.blocked",
    targetKinds: ["story", "task"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"], executionRequired: true,
    fields: { blocker_refs: stringList({ minItems: 1 }), reason: nonempty(), review_policy: nonempty() },
    example: { blocker_refs: ["external://dependency/42"], reason: "dependency unavailable", review_policy: "review-after-evidence-change" },
  }),
  operation({
    command: "tekroo.command.work.unblock",
    event: "tekroo.event.work.unblocked",
    targetKinds: ["story", "task"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"], executionRequired: true,
    fields: { resolved_blocker_refs: stringList({ minItems: 1 }), evidence_ids: uuidList({ minItems: 1 }) },
    example: { resolved_blocker_refs: ["external://dependency/42"], evidence_ids: ["00000000-0000-7000-8000-000000000201"] },
  }),
  operation({
    command: "tekroo.command.task.create",
    event: "tekroo.event.task.created",
    targetKinds: ["task"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"], rootAllowed: true,
    fields: { story_id: uuid(), title: string({ minLength: 1, maxLength: 256 }), description: string({ maxLength: 65536 }), acceptance_criteria: stringList({ minItems: 1 }), depends_on: uuidList() },
    example: { story_id: "00000000-0000-7000-8000-000000000100", title: "Write ownership model", description: "Specify CAS ownership.", acceptance_criteria: ["concurrent acquisition has one winner"], depends_on: [] },
  }),
  operation({
    command: "tekroo.command.task.mark-ready",
    event: "tekroo.event.task.readied",
    targetKinds: ["task"], authorityKinds: ["POLICY"],
    fields: { dependency_event_ids: uuidList(), readiness_policy_revision: integer({ minimum: 1 }) },
    example: { dependency_event_ids: [], readiness_policy_revision: 1 },
  }),
  operation({
    command: "tekroo.command.task.dispatch",
    event: "tekroo.event.task.dispatched",
    targetKinds: ["task"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"],
    fields: { destination: nonempty(), routing_mode: string({ enum: ["EXACT", "ROLE_CLASS", "WILDCARD"] }) },
    example: { destination: "teams::coder-1", routing_mode: "EXACT" },
  }),
  operation({
    command: "tekroo.command.task.acquire-ownership",
    event: "tekroo.event.task.ownership-acquired",
    targetKinds: ["task"], authorityKinds: ["ACTOR"], executionRequired: true,
    fields: { owner_fqn: actor(), expected_ownership_version: integer({ minimum: 0 }) },
    example: { owner_fqn: "teams::coder-1", expected_ownership_version: 0 },
  }),
  operation({
    command: "tekroo.command.task.release-ownership",
    event: "tekroo.event.task.ownership-released",
    targetKinds: ["task"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"], executionRequired: true,
    fields: { owner_fqn: actor(), expected_ownership_version: integer({ minimum: 1 }), reason: nonempty() },
    example: { owner_fqn: "teams::coder-1", expected_ownership_version: 1, reason: "authorized release" },
  }),
  operation({
    command: "tekroo.command.task.handoff",
    event: "tekroo.event.task.handed-off",
    targetKinds: ["task"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"], executionRequired: true,
    fields: { prior_owner_fqn: actor(), new_owner_fqn: actor(), expected_ownership_version: integer({ minimum: 1 }), reason: nonempty() },
    example: { prior_owner_fqn: "teams::coder-1", new_owner_fqn: "teams::coder-2", expected_ownership_version: 1, reason: "bounded handoff" },
  }),
  operation({
    command: "tekroo.command.task.force-reassign",
    event: "tekroo.event.task.force-reassigned",
    targetKinds: ["task"], authorityKinds: ["HUMAN", "POLICY"],
    fields: { prior_owner_fqn: actor(), new_owner_fqn: actor(), expected_ownership_version: integer({ minimum: 1 }), reason: nonempty(), evidence_ids: uuidList({ minItems: 1 }) },
    example: { prior_owner_fqn: "teams::coder-1", new_owner_fqn: "teams::coder-2", expected_ownership_version: 1, reason: "owner unavailable after verified fencing", evidence_ids: ["00000000-0000-7000-8000-000000000202"] },
  }),
  operation({
    command: "tekroo.command.task.activate",
    event: "tekroo.event.task.activated",
    targetKinds: ["task"], authorityKinds: ["ACTOR"], executionRequired: true,
    fields: { owner_fqn: actor(), ownership_version: integer({ minimum: 1 }) },
    example: { owner_fqn: "teams::coder-1", ownership_version: 1 },
  }),
  operation({
    command: "tekroo.command.task.request-completion",
    event: "tekroo.event.task.completed",
    targetKinds: ["task"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"], executionRequired: true,
    fields: { lifecycle_epoch: integer({ minimum: 1 }), owner_fqn: actor(), criteria_revision: integer({ minimum: 1 }), evidence_ids: uuidList({ minItems: 1 }), artifact_digests: digestList(), unresolved_exceptions: stringList() },
    example: { lifecycle_epoch: 1, owner_fqn: "teams::coder-1", criteria_revision: 1, evidence_ids: ["00000000-0000-7000-8000-000000000203"], artifact_digests: ["d".repeat(64)], unresolved_exceptions: [] },
  }),
  operation({
    command: "tekroo.command.story.request-completion",
    event: "tekroo.event.story.completed",
    targetKinds: ["story"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"],
    fields: { lifecycle_epoch: integer({ minimum: 1 }), criteria_revision: integer({ minimum: 1 }), evidence_ids: uuidList({ minItems: 1 }), artifact_digests: digestList(), validation_event_ids: uuidList({ minItems: 1 }), unresolved_exceptions: stringList() },
    example: { lifecycle_epoch: 1, criteria_revision: 1, evidence_ids: ["00000000-0000-7000-8000-000000000204"], artifact_digests: ["d".repeat(64)], validation_event_ids: ["00000000-0000-7000-8000-000000000304"], unresolved_exceptions: [] },
  }),
  operation({
    command: "tekroo.command.story.request-acceptance",
    event: "tekroo.event.story.accepted",
    targetKinds: ["story"], authorityKinds: ["HUMAN", "POLICY"],
    fields: { lifecycle_epoch: integer({ minimum: 1 }), acceptance_policy_revision: integer({ minimum: 1 }), evidence_ids: uuidList({ minItems: 1 }), qualified_tree_digest: digest() },
    optional: ["qualified_tree_digest"],
    example: { lifecycle_epoch: 1, acceptance_policy_revision: 1, evidence_ids: ["00000000-0000-7000-8000-000000000205"], qualified_tree_digest: "e".repeat(64) },
  }),
  operation({
    command: "tekroo.command.work.reopen",
    event: "tekroo.event.work.reopened",
    targetKinds: ["story", "task"], authorityKinds: ["HUMAN", "POLICY"],
    fields: { prior_epoch: integer({ minimum: 1 }), new_scope_revision: integer({ minimum: 1 }), reason: nonempty(), owner_carry_forward: boolean(), evidence_ids: uuidList({ minItems: 1 }) },
    example: { prior_epoch: 1, new_scope_revision: 2, reason: "verified remedial work", owner_carry_forward: false, evidence_ids: ["00000000-0000-7000-8000-000000000206"] },
  }),
  operation({
    command: "tekroo.command.work.close",
    event: "tekroo.event.work.closed",
    targetKinds: ["story", "task"], authorityKinds: ["HUMAN", "POLICY"],
    fields: { disposition: string({ enum: ["CANCELLED", "SUPERSEDED", "INVALIDATED"] }), reason: nonempty(), successor_ids: uuidList() },
    example: { disposition: "SUPERSEDED", reason: "replaced by narrower successor", successor_ids: ["00000000-0000-7000-8000-000000000401"] },
  }),
  operation({
    command: "tekroo.command.work.create-successor",
    event: "tekroo.event.work.successor-created",
    targetKinds: ["story", "task"], authorityKinds: ["HUMAN", "POLICY"],
    fields: { successor_ids: uuidList({ minItems: 1 }), relation: string({ enum: ["DERIVATION", "SUPERSESSION", "SPLIT", "MERGE", "CORRECTION"] }), ownership_rule: string({ enum: ["UNOWNED", "CARRY_FORWARD", "EXPLICIT_POLICY"] }), dependency_rule: string({ enum: ["COPY", "REPLACE", "EXPLICIT"] }), reason: nonempty() },
    example: { successor_ids: ["00000000-0000-7000-8000-000000000401"], relation: "SUPERSESSION", ownership_rule: "UNOWNED", dependency_rule: "EXPLICIT", reason: "scope replacement" },
  }),
  operation({
    command: "tekroo.command.completion-review.open",
    event: "tekroo.event.completion-review.opened",
    targetKinds: ["completion-review"], authorityKinds: ["ACTOR", "HUMAN", "POLICY"], rootAllowed: true,
    fields: {
      subject_kind: string({ enum: ["story", "task"] }), subject_id: uuid(), lifecycle_epoch: integer({ minimum: 1 }), criteria_revision: integer({ minimum: 1 }), evidence_set_digest: digest(),
      branch_policy_revision: integer({ minimum: 1 }), required_branch_ids: stringList({ minItems: 1, maxItems: 32 }),
      join_rule: string({ enum: ["ALL_PASS"] }), partial_result_policy: string({ enum: ["WAIT_ALL", "FAIL_FAST"] }),
    },
    example: { subject_kind: "task", subject_id: "00000000-0000-7000-8000-000000000101", lifecycle_epoch: 1, criteria_revision: 1, evidence_set_digest: "f".repeat(64), branch_policy_revision: 1, required_branch_ids: ["tests", "review"], join_rule: "ALL_PASS", partial_result_policy: "WAIT_ALL" },
  }),
  operation({
    command: "tekroo.command.completion-review.record-result",
    event: "tekroo.event.completion-review.result-recorded",
    targetKinds: ["completion-review"], authorityKinds: ["ACTOR", "HUMAN", "SERVICE", "POLICY"],
    fields: { review_id: uuid(), branch_id: nonempty(), branch_policy_revision: integer({ minimum: 1 }), result: string({ enum: ["PASS", "FAIL", "INCONCLUSIVE"] }), reasons: stringList(), evidence_ids: uuidList({ minItems: 1 }) },
    example: { review_id: "00000000-0000-7000-8000-000000000501", branch_id: "tests", branch_policy_revision: 1, result: "PASS", reasons: [], evidence_ids: ["00000000-0000-7000-8000-000000000207"] },
  }),
  operation({
    command: "tekroo.command.record.correct",
    event: "tekroo.event.record.corrected",
    targetKinds: ["story", "task", "evidence", "system"], authorityKinds: ["HUMAN", "POLICY"],
    fields: { target_event_id: uuid(), corrected_fields: object({ minProperties: 1 }), reason: nonempty(), evidence_ids: uuidList({ minItems: 1 }) },
    example: { target_event_id: "00000000-0000-7000-8000-000000000601", corrected_fields: { title: "Corrected title" }, reason: "verified source correction", evidence_ids: ["00000000-0000-7000-8000-000000000208"] },
  }),
];

const payloadDefs = {};
const catalogueEntries = [];
for (const op of operations) {
  const commandSchemaName = op.command.replaceAll(".", "_").replaceAll("-", "_").concat("_1_1_0");
  const eventSchemaName = op.event.replaceAll(".", "_").replaceAll("-", "_").concat("_1_1_0");
  const required = Object.keys(op.fields).filter((name) => !op.optional.includes(name));
  const payloadSchema = {
    type: "object",
    additionalProperties: false,
    properties: op.fields,
    required,
  };
  payloadDefs[commandSchemaName] = payloadSchema;
  payloadDefs[eventSchemaName] = payloadSchema;
  catalogueEntries.push({
    typeId: op.command,
    kind: "COMMAND",
    version: schemaVersion,
    lifecycle: "ACTIVE",
    owner: op.owner,
    targetKinds: op.targetKinds,
    authorityKinds: op.authorityKinds,
    executionRequired: op.executionRequired,
    routingMode: "KERNEL_DIRECT",
    allowedParentEdges: ["CAUSAL", "RESPONSE", "RETRY", "DERIVATION", "SUPERSESSION"],
    rootAllowed: op.rootAllowed,
    payloadSchema: `schemas/payloads.schema.json#/$defs/${commandSchemaName}`,
    emits: [op.event],
    aliases: [],
    compatibility: { acceptedSourceVersions: [schemaVersion], transforms: [] },
  });
  catalogueEntries.push({
    typeId: op.event,
    kind: "EVENT",
    version: schemaVersion,
    lifecycle: "ACTIVE",
    owner: op.owner,
    targetKinds: op.targetKinds,
    authorityKinds: [],
    executionRequired: false,
    routingMode: "COMMITTED_EVENT",
    allowedParentEdges: ["CAUSAL", "RESPONSE", "RETRY", "DERIVATION", "SUPERSESSION"],
    rootAllowed: op.rootAllowed,
    payloadSchema: `schemas/payloads.schema.json#/$defs/${eventSchemaName}`,
    acceptedCommandTypes: [op.command],
    aliases: [],
    compatibility: { acceptedSourceVersions: [schemaVersion], transforms: [] },
  });
}

const catalogue = {
  schemaVersion,
  revision: 2,
  contractIdentity,
  status: "FROZEN_CANDIDATE",
  defaultRoutingMode: "KERNEL_DIRECT",
  unknownCommandBehavior: "PRESERVE_AND_REJECT_WITHOUT_TARGET_EFFECT",
  unknownEventReplayBehavior: "PRESERVE_QUARANTINE_AND_STOP",
  adoptedHistoricalAliases: [],
  explicitlyUnadoptedHistoricalTypes: ["tekroo-agent-chat", "tekroo-agent-ping", "tekroo-agent-pong"],
  entries: catalogueEntries.sort((a, b) => a.typeId.localeCompare(b.typeId)),
};
writeJson("catalogue/kernel-catalogue.json", catalogue);

const coreSchema = {
  $schema: schemaDialect,
  $id: `${contractBase}/schemas/core.schema.json`,
  title: "Tekroo kernel core values",
  $defs: {
    ActorFQN: { type: "string", pattern: actorPattern },
    UUIDv7: { type: "string", pattern: uuid7Pattern },
    Sha256: { type: "string", pattern: shaPattern },
    ContractManifestIdentity: { const: contractIdentity },
    AggregateRef: {
      type: "object", additionalProperties: false,
      properties: { kind: { enum: ["story", "task", "completion-review", "evidence", "execution", "system"] }, id: { $ref: "#/$defs/UUIDv7" } },
      required: ["kind", "id"],
    },
    PrincipalRef: {
      type: "object", additionalProperties: false,
      properties: { kind: { enum: ["ACTOR", "HUMAN", "SERVICE", "POLICY"] }, id: { type: "string", minLength: 1, maxLength: 256 } },
      required: ["kind", "id"],
    },
    ExecutionTuple: {
      type: "object", additionalProperties: false,
      properties: { execution_id: { $ref: "#/$defs/UUIDv7" }, fencing_epoch: { type: "integer", minimum: 1 } },
      required: ["execution_id", "fencing_epoch"],
    },
    DagParent: {
      type: "object", additionalProperties: false,
      properties: { parent_event_id: { $ref: "#/$defs/UUIDv7" }, edge_kind: { enum: ["CAUSAL", "RESPONSE", "RETRY", "DERIVATION", "SUPERSESSION"] } },
      required: ["parent_event_id", "edge_kind"],
    },
    EvidenceRef: { type: "object", additionalProperties: false, properties: { evidence_id: { $ref: "#/$defs/UUIDv7" }, sha256: { $ref: "#/$defs/Sha256" } }, required: ["evidence_id", "sha256"] },
    ExpectedRevision: { anyOf: [{ type: "integer", minimum: 1 }, { const: "MUST_NOT_EXIST" }] },
    AggregatePrecondition: {
      type: "object", additionalProperties: false,
      properties: { aggregate: { $ref: "#/$defs/AggregateRef" }, expected_revision: { $ref: "#/$defs/ExpectedRevision" } },
      required: ["aggregate", "expected_revision"],
    },
  },
};
writeJson("schemas/core.schema.json", coreSchema);

writeJson("schemas/payloads.schema.json", {
  $schema: schemaDialect,
  $id: `${contractBase}/schemas/payloads.schema.json`,
  title: "Tekroo kernel command and event payloads",
  $defs: payloadDefs,
});

writeJson("schemas/kernel-command.schema.json", {
  $schema: schemaDialect,
  $id: `${contractBase}/schemas/kernel-command.schema.json`,
  title: "KernelCommand",
  type: "object",
  additionalProperties: false,
  properties: {
    contract_manifest: { $ref: "./core.schema.json#/$defs/ContractManifestIdentity" },
    command_id: { $ref: "./core.schema.json#/$defs/UUIDv7" },
    command_type: { type: "string", pattern: "^tekroo\\.command\\.[a-z0-9]+(?:[.-][a-z0-9]+)*$" },
    command_version: { const: schemaVersion },
    target: { $ref: "./core.schema.json#/$defs/AggregateRef" },
    authority: { $ref: "./core.schema.json#/$defs/PrincipalRef" },
    actor_fqn: { anyOf: [{ $ref: "./core.schema.json#/$defs/ActorFQN" }, { type: "null" }] },
    execution: { anyOf: [{ $ref: "./core.schema.json#/$defs/ExecutionTuple" }, { type: "null" }] },
    expected_revision: { $ref: "./core.schema.json#/$defs/ExpectedRevision" },
    preconditions: { type: "array", maxItems: 64, uniqueItems: true, items: { $ref: "./core.schema.json#/$defs/AggregatePrecondition" } },
    expected_lifecycle_epoch: { anyOf: [{ type: "integer", minimum: 1 }, { type: "null" }] },
    expected_policy_revision: { type: "integer", minimum: 1 },
    expected_catalogue_revision: { type: "integer", minimum: 1 },
    idempotency_key: { type: "string", minLength: 1, maxLength: 256 },
    correlation_id: { $ref: "./core.schema.json#/$defs/UUIDv7" },
    causation: { type: "array", maxItems: 64, uniqueItems: true, items: { $ref: "./core.schema.json#/$defs/DagParent" } },
    issued_at: { type: ["string", "null"], format: "date-time" },
    payload: { type: "object" },
    evidence_refs: { type: "array", maxItems: 64, uniqueItems: true, items: { $ref: "./core.schema.json#/$defs/EvidenceRef" } },
  },
  required: ["contract_manifest", "command_id", "command_type", "command_version", "target", "authority", "actor_fqn", "execution", "expected_revision", "preconditions", "expected_lifecycle_epoch", "expected_policy_revision", "expected_catalogue_revision", "idempotency_key", "correlation_id", "causation", "issued_at", "payload", "evidence_refs"],
});

writeJson("schemas/domain-event.schema.json", {
  $schema: schemaDialect,
  $id: `${contractBase}/schemas/domain-event.schema.json`,
  title: "DomainEvent",
  type: "object", additionalProperties: false,
  properties: {
    contract_manifest: { $ref: "./core.schema.json#/$defs/ContractManifestIdentity" },
    event_id: { $ref: "./core.schema.json#/$defs/UUIDv7" },
    event_type: { type: "string", pattern: "^tekroo\\.event\\.[a-z0-9]+(?:[.-][a-z0-9]+)*$" },
    event_version: { const: schemaVersion },
    aggregate: { $ref: "./core.schema.json#/$defs/AggregateRef" },
    aggregate_revision: { type: "integer", minimum: 1 },
    lifecycle_epoch: { type: "integer", minimum: 1 },
    command_id: { $ref: "./core.schema.json#/$defs/UUIDv7" },
    authority: { $ref: "./core.schema.json#/$defs/PrincipalRef" },
    actor_fqn: { anyOf: [{ $ref: "./core.schema.json#/$defs/ActorFQN" }, { type: "null" }] },
    execution: { anyOf: [{ $ref: "./core.schema.json#/$defs/ExecutionTuple" }, { type: "null" }] },
    parents: { type: "array", maxItems: 64, uniqueItems: true, items: { $ref: "./core.schema.json#/$defs/DagParent" } },
    committed_at: { type: "string", format: "date-time" },
    payload: { type: "object" },
    provenance_digest: { $ref: "./core.schema.json#/$defs/Sha256" },
  },
  required: ["contract_manifest", "event_id", "event_type", "event_version", "aggregate", "aggregate_revision", "lifecycle_epoch", "command_id", "authority", "actor_fqn", "execution", "parents", "committed_at", "payload", "provenance_digest"],
});

writeJson("schemas/command-receipt.schema.json", {
  $schema: schemaDialect,
  $id: `${contractBase}/schemas/command-receipt.schema.json`,
  title: "CommandReceipt",
  type: "object", additionalProperties: false,
  properties: {
    contract_manifest: { $ref: "./core.schema.json#/$defs/ContractManifestIdentity" },
    command_id: { $ref: "./core.schema.json#/$defs/UUIDv7" },
    command_type: { type: "string", pattern: "^tekroo\\.command\\." },
    target: { $ref: "./core.schema.json#/$defs/AggregateRef" },
    outcome_code: { enum: ["APPLIED", "NO_CHANGE", "REJECTED_INVALID", "REJECTED_UNAUTHORIZED", "REJECTED_NOT_FOUND", "REJECTED_CONFLICT", "REJECTED_STALE_EXECUTION", "REJECTED_CLOSED", "REJECTED_POLICY"] },
    reason_code: { type: "string", pattern: "^[A-Z][A-Z0-9_]{0,127}$" },
    state_changed: { type: "boolean" },
    resulting_revision: { type: ["integer", "null"], minimum: 1 },
    event_ids: { type: "array", maxItems: 64, uniqueItems: true, items: { $ref: "./core.schema.json#/$defs/UUIDv7" } },
    received_at: { type: "string", format: "date-time" },
    decided_at: { type: "string", format: "date-time" },
    provenance_digest: { $ref: "./core.schema.json#/$defs/Sha256" },
  },
  required: ["contract_manifest", "command_id", "command_type", "target", "outcome_code", "reason_code", "state_changed", "resulting_revision", "event_ids", "received_at", "decided_at", "provenance_digest"],
});

writeJson("schemas/evidence-record.schema.json", {
  $schema: schemaDialect,
  $id: `${contractBase}/schemas/evidence-record.schema.json`,
  title: "EvidenceRecord",
  type: "object", additionalProperties: false,
  properties: {
    evidence_id: { $ref: "./core.schema.json#/$defs/UUIDv7" },
    evidence_kind: { type: "string", minLength: 1 },
    raw_sha256: { $ref: "./core.schema.json#/$defs/Sha256" },
    byte_length: { type: "integer", minimum: 0 },
    media_type: { type: "string", minLength: 1 },
    canonical_digest: { anyOf: [{ $ref: "./core.schema.json#/$defs/Sha256" }, { type: "null" }] },
    locator: { type: "string", minLength: 1 },
    locator_immutable: { const: true },
    availability: { enum: ["AVAILABLE", "UNAVAILABLE", "DELETED_WITH_TOMBSTONE"] },
    registered_by: { $ref: "./core.schema.json#/$defs/PrincipalRef" },
    producing_component: { type: "string", minLength: 1 },
    producing_version: { type: "string", minLength: 1 },
    source_timestamp: { type: ["string", "null"], format: "date-time" },
    ingested_at: { type: "string", format: "date-time" },
    transport_provenance: { type: "string", minLength: 1 },
    source_evidence_ids: { type: "array", maxItems: 64, uniqueItems: true, items: { $ref: "./core.schema.json#/$defs/UUIDv7" } },
    sensitivity: { type: "string", minLength: 1 },
    access_partition: { type: "string", minLength: 1 },
    retention_policy: { type: "string", minLength: 1 },
    integrity_state: { enum: ["UNVERIFIED", "DIGEST_VERIFIED", "MISSING", "MISMATCH"] },
    computation: { anyOf: [{
      type: "object", additionalProperties: false,
      properties: { method: { type: "string", minLength: 1 }, build_digest: { $ref: "./core.schema.json#/$defs/Sha256" }, configuration_digest: { $ref: "./core.schema.json#/$defs/Sha256" }, deterministic: { type: "boolean" } },
      required: ["method", "build_digest", "configuration_digest", "deterministic"],
    }, { type: "null" }] },
    redacts: { anyOf: [{ $ref: "./core.schema.json#/$defs/UUIDv7" }, { type: "null" }] },
    deletion_tombstone: { anyOf: [{
      type: "object", additionalProperties: false,
      properties: { basis: { type: "string", minLength: 1 }, authority: { $ref: "./core.schema.json#/$defs/PrincipalRef" }, deleted_at: { type: "string", format: "date-time" } },
      required: ["basis", "authority", "deleted_at"],
    }, { type: "null" }] },
  },
  required: ["evidence_id", "evidence_kind", "raw_sha256", "byte_length", "media_type", "canonical_digest", "locator", "locator_immutable", "availability", "registered_by", "producing_component", "producing_version", "source_timestamp", "ingested_at", "transport_provenance", "source_evidence_ids", "sensitivity", "access_partition", "retention_policy", "integrity_state", "computation", "redacts", "deletion_tombstone"],
});

writeJson("schemas/aggregate-state.schema.json", {
  $schema: schemaDialect,
  $id: `${contractBase}/schemas/aggregate-state.schema.json`,
  title: "Story and task aggregate state",
  $defs: {
    Ownership: {
      type: "object", additionalProperties: false,
      properties: { owner_fqn: { anyOf: [{ $ref: "./core.schema.json#/$defs/ActorFQN" }, { type: "null" }] }, ownership_version: { type: "integer", minimum: 0 }, assigned_event_id: { anyOf: [{ $ref: "./core.schema.json#/$defs/UUIDv7" }, { type: "null" }] } },
      required: ["owner_fqn", "ownership_version", "assigned_event_id"],
    },
  },
  oneOf: [
    { type: "object", additionalProperties: false, properties: { kind: { const: "story" }, id: { $ref: "./core.schema.json#/$defs/UUIDv7" }, revision: { type: "integer", minimum: 1 }, lifecycle_epoch: { type: "integer", minimum: 1 }, phase: { enum: ["DRAFT", "READY", "PLANNING", "ACTIVE", "COMPLETED", "ACCEPTED", "CLOSED"] }, condition: { enum: ["RUNNABLE", "BLOCKED"] }, ownership: { $ref: "#/$defs/Ownership" } }, required: ["kind", "id", "revision", "lifecycle_epoch", "phase", "condition", "ownership"] },
    { type: "object", additionalProperties: false, properties: { kind: { const: "task" }, id: { $ref: "./core.schema.json#/$defs/UUIDv7" }, revision: { type: "integer", minimum: 1 }, lifecycle_epoch: { type: "integer", minimum: 1 }, phase: { enum: ["PLANNED", "READY", "ACTIVE", "COMPLETED", "CLOSED"] }, condition: { enum: ["RUNNABLE", "BLOCKED"] }, ownership: { $ref: "#/$defs/Ownership" } }, required: ["kind", "id", "revision", "lifecycle_epoch", "phase", "condition", "ownership"] },
  ],
});

writeJson("schemas/conformance-fixture.schema.json", {
  $schema: schemaDialect,
  $id: `${contractBase}/schemas/conformance-fixture.schema.json`,
  title: "Conformance fixture",
  type: "object", additionalProperties: false,
  properties: {
    fixtureId: { type: "string", pattern: "^[A-Z0-9][A-Z0-9._-]+$" },
    kind: { enum: ["CATALOGUE_COMMAND", "STATE_MODEL", "DAG_MODEL", "IDENTITY_MODEL", "IDEMPOTENCY_MODEL", "PRECONDITION_MODEL", "REVIEW_JOIN_MODEL", "SUCCESSOR_SET_MODEL", "COMPATIBILITY_MODEL"] },
    classification: { enum: ["NORMATIVE_EXAMPLE", "BOUNDARY_NEGATIVE", "REGRESSION_RECEIPT", "GENERATED_COUNTEREXAMPLE", "FAULT_INJECTION"] },
    sourceDecisionIds: { type: "array", minItems: 1, uniqueItems: true, items: { type: "string", pattern: "^P2-KCF-[0-9]{3}$" } },
    given: { type: "object" }, when: { type: "object" }, then: { type: "object" },
  },
  required: ["fixtureId", "kind", "classification", "sourceDecisionIds", "given", "when", "then"],
});

writeJson("schemas/conformance-report.schema.json", {
  $schema: schemaDialect,
  $id: `${contractBase}/schemas/conformance-report.schema.json`,
  title: "Conformance report",
  type: "object", additionalProperties: true,
  properties: {
    schemaVersion: { const: schemaVersion }, reportType: { type: "string", minLength: 1 }, contractIdentity: { const: contractIdentity },
    manifestSha256: { type: "string", pattern: shaPattern }, profile: { type: "string", minLength: 1 }, status: { enum: ["PASS", "FAIL", "NOT_RUN", "INCONCLUSIVE"] }, reportSha256: { type: "string", pattern: shaPattern },
  },
  required: ["schemaVersion", "reportType", "contractIdentity", "manifestSha256", "profile", "status", "reportSha256"],
});

const fixtures = [];
let fixtureOrdinal = 1;
for (const op of operations) {
  const short = op.command.replace("tekroo.command.", "").replaceAll(".", "-");
  fixtures.push({
    fixtureId: `CAT-${String(fixtureOrdinal).padStart(3, "0")}-${short.toUpperCase()}-VALID`,
    kind: "CATALOGUE_COMMAND", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-003", "P2-KCF-004"],
    given: { contractManifest: contractIdentity },
    when: { commandType: op.command, payload: op.example },
    then: { expected: { outcomeCode: "APPLIED", eventTypes: [op.event] } },
  });
  const invalid = { ...op.example };
  delete invalid[Object.keys(op.fields).find((name) => !op.optional.includes(name))];
  fixtures.push({
    fixtureId: `CAT-${String(fixtureOrdinal).padStart(3, "0")}-${short.toUpperCase()}-INVALID`,
    kind: "CATALOGUE_COMMAND", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: ["P2-KCF-003", "P2-KCF-004"],
    given: { contractManifest: contractIdentity },
    when: { commandType: op.command, payload: invalid },
    then: { expected: { outcomeCode: "REJECTED_INVALID", eventTypes: [] } },
  });
  fixtureOrdinal += 1;
}

fixtures.push(
  { fixtureId: "MODEL-STORY-FORWARD", kind: "STATE_MODEL", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-006", "P2-KCF-008"], given: { phase: "DRAFT", lifecycleEpoch: 1, condition: "RUNNABLE" }, when: { actions: ["authorize", "begin_planning", "activate", "complete", "accept"] }, then: { expected: { phase: "ACCEPTED", lifecycleEpoch: 1, condition: "RUNNABLE", rejected: null } } },
  { fixtureId: "MODEL-TASK-FORWARD", kind: "STATE_MODEL", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-006", "P2-KCF-008"], model: "task", given: { phase: "PLANNED", lifecycleEpoch: 1, condition: "RUNNABLE" }, when: { actions: ["mark_ready", "activate", "complete"] }, then: { expected: { phase: "COMPLETED", lifecycleEpoch: 1, condition: "RUNNABLE", rejected: null } } },
  { fixtureId: "MODEL-BLOCK-ORTHOGONAL", kind: "STATE_MODEL", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-006"], model: "task", given: { phase: "ACTIVE", lifecycleEpoch: 1, condition: "RUNNABLE" }, when: { actions: ["block", "unblock"] }, then: { expected: { phase: "ACTIVE", lifecycleEpoch: 1, condition: "RUNNABLE", rejected: null } } },
  { fixtureId: "MODEL-COMPLETION-NO-REQUESTED-PHASE", kind: "STATE_MODEL", classification: "REGRESSION_RECEIPT", sourceDecisionIds: ["P2-KCF-006", "P2-KCF-008"], model: "task", given: { phase: "ACTIVE", lifecycleEpoch: 1, condition: "RUNNABLE" }, when: { actions: ["complete"] }, then: { expected: { phase: "COMPLETED", lifecycleEpoch: 1, condition: "RUNNABLE", rejected: null } } },
  { fixtureId: "MODEL-ILLEGAL-COMPLETION", kind: "STATE_MODEL", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: ["P2-KCF-006", "P2-KCF-008"], model: "task", given: { phase: "READY", lifecycleEpoch: 1, condition: "RUNNABLE" }, when: { actions: ["complete"] }, then: { expected: { phase: "READY", lifecycleEpoch: 1, condition: "RUNNABLE", rejected: "REJECTED_POLICY" } } },
  { fixtureId: "MODEL-REOPEN-INCREMENTS-EPOCH", kind: "STATE_MODEL", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-008"], model: "story", given: { phase: "COMPLETED", lifecycleEpoch: 1, condition: "RUNNABLE" }, when: { actions: ["reopen"] }, then: { expected: { phase: "ACTIVE", lifecycleEpoch: 2, condition: "RUNNABLE", rejected: null } } },
  { fixtureId: "DAG-VALID-BRANCH-JOIN", kind: "DAG_MODEL", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-005"], given: { nodes: ["root", "left", "right", "join"] }, when: { edges: [{ parent: "root", child: "left" }, { parent: "root", child: "right" }, { parent: "left", child: "join" }, { parent: "right", child: "join" }] }, then: { expected: { valid: true, reason: null } } },
  { fixtureId: "DAG-REJECT-CYCLE", kind: "DAG_MODEL", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: ["P2-KCF-005"], given: { nodes: ["a", "b"] }, when: { edges: [{ parent: "a", child: "b" }, { parent: "b", child: "a" }] }, then: { expected: { valid: false, reason: "CYCLE" } } },
  { fixtureId: "IDENTITY-CONCRETE-FQN", kind: "IDENTITY_MODEL", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-002"], given: {}, when: { value: "teams::senior-coder-1" }, then: { expected: { valid: true } } },
  { fixtureId: "IDENTITY-REJECT-WILDCARD", kind: "IDENTITY_MODEL", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: ["P2-KCF-002"], given: {}, when: { value: "teams::coder-*" }, then: { expected: { valid: false } } },
  { fixtureId: "IDENTITY-REJECT-LEADING-ZERO", kind: "IDENTITY_MODEL", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: ["P2-KCF-002"], given: {}, when: { value: "teams::coder-01" }, then: { expected: { valid: false } } },
  { fixtureId: "PRECONDITION-EXACT-ALL", kind: "PRECONDITION_MODEL", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-007"], given: { revisions: { "story:00000000-0000-7000-8000-000000000701": 3, "task:00000000-0000-7000-8000-000000000702": 5 } }, when: { preconditions: [{ aggregate: "story:00000000-0000-7000-8000-000000000701", expectedRevision: 3 }, { aggregate: "task:00000000-0000-7000-8000-000000000702", expectedRevision: 5 }] }, then: { expected: { valid: true } } },
  { fixtureId: "PRECONDITION-STALE-NONE", kind: "PRECONDITION_MODEL", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: ["P2-KCF-007"], given: { revisions: { "story:00000000-0000-7000-8000-000000000701": 4, "task:00000000-0000-7000-8000-000000000702": 5 } }, when: { preconditions: [{ aggregate: "story:00000000-0000-7000-8000-000000000701", expectedRevision: 3 }, { aggregate: "task:00000000-0000-7000-8000-000000000702", expectedRevision: 5 }] }, then: { expected: { valid: false } } },
  { fixtureId: "REVIEW-JOIN-ORDER-INDEPENDENT", kind: "REVIEW_JOIN_MODEL", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-008"], given: { requiredBranchIds: ["review", "tests"], joinRule: "ALL_PASS", partialResultPolicy: "WAIT_ALL" }, when: { results: [{ branchId: "tests", result: "PASS" }, { branchId: "review", result: "PASS" }] }, then: { expected: { status: "PASS", complete: true } } },
  { fixtureId: "REVIEW-JOIN-DUPLICATE-CONFLICT", kind: "REVIEW_JOIN_MODEL", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: ["P2-KCF-008"], given: { requiredBranchIds: ["review", "tests"], joinRule: "ALL_PASS", partialResultPolicy: "WAIT_ALL" }, when: { results: [{ branchId: "tests", result: "PASS" }, { branchId: "tests", result: "FAIL" }] }, then: { expected: { status: "CONFLICT", complete: false } } },
  { fixtureId: "SUCCESSOR-SET-CANONICAL", kind: "SUCCESSOR_SET_MODEL", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-008"], given: {}, when: { successorIds: ["00000000-0000-7000-8000-000000000801", "00000000-0000-7000-8000-000000000802"] }, then: { expected: { valid: true } } },
  { fixtureId: "SUCCESSOR-SET-REJECT-UNORDERED", kind: "SUCCESSOR_SET_MODEL", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: ["P2-KCF-008"], given: {}, when: { successorIds: ["00000000-0000-7000-8000-000000000802", "00000000-0000-7000-8000-000000000801"] }, then: { expected: { valid: false } } },
  { fixtureId: "COMPATIBILITY-1.0-MISSING-CONTEXT", kind: "COMPATIBILITY_MODEL", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: ["P2-KCF-012"], given: { sourceVersion: "1.0.0" }, when: { exactContextAvailable: false }, then: { expected: { outcome: "MIGRATION_REQUIRED" } } },
);

const idemScope = { contract: contractIdentity, principal: "teams::coder-1", commandType: "tekroo.command.task.activate", target: "task-1", key: "activate-1" };
const requestA = { owner: "teams::coder-1", expectedRevision: 2 };
const requestB = { owner: "teams::coder-1", expectedRevision: 3 };
const fingerprintA = sha256(canonical(requestA));
fixtures.push({
  fixtureId: "IDEMPOTENCY-DUPLICATE-AND-CONFLICT", kind: "IDEMPOTENCY_MODEL", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: ["P2-KCF-007"], given: {},
  when: { commands: [{ scope: idemScope, semanticRequest: requestA }, { scope: idemScope, semanticRequest: requestA }, { scope: idemScope, semanticRequest: requestB }] },
  then: { expected: { durableDecisionCount: 1, receipts: [{ outcome: "APPLIED", fingerprint: fingerprintA, eventCount: 1 }, { outcome: "APPLIED", fingerprint: fingerprintA, eventCount: 1 }, { outcome: "REJECTED_CONFLICT", reason: "IDEMPOTENCY_KEY_REUSE", eventCount: 0 }] } },
});
fixtures.push({
  fixtureId: "CAT-UNKNOWN-COMMAND", kind: "CATALOGUE_COMMAND", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: ["P2-KCF-004"], given: {}, when: { commandType: "tekroo.command.unknown.operation", payload: { raw: "preserved" } }, then: { expected: { outcomeCode: "REJECTED_INVALID", eventTypes: [] } },
});

const catalogueFixtures = fixtures.filter((fixture) => fixture.kind === "CATALOGUE_COMMAND");
const modelFixtures = fixtures.filter((fixture) => fixture.kind !== "CATALOGUE_COMMAND");
writeJson("fixtures/catalogue-coverage.json", { schemaVersion, contractIdentity, fixtures: catalogueFixtures });
writeJson("fixtures/model-and-invariant-scenarios.json", { schemaVersion, contractIdentity, fixtures: modelFixtures });

const invariants = [
  { invariantId: "INV-001-REVISION-CONTIGUITY", description: "Accepted transitions advance exact contiguous revisions; rejections do not.", decisionIds: ["P2-KCF-003", "P2-KCF-007"], negativeRequirementIds: [], fixtureIds: ["MODEL-TASK-FORWARD", "PRECONDITION-EXACT-ALL", "PRECONDITION-STALE-NONE"] },
  { invariantId: "INV-002-IDEMPOTENT-DECISION", description: "One semantic command produces at most one durable decision and canonical receipt.", decisionIds: ["P2-KCF-003", "P2-KCF-007", "P2-KCF-010"], negativeRequirementIds: ["NEG-012"], fixtureIds: ["IDEMPOTENCY-DUPLICATE-AND-CONFLICT"] },
  { invariantId: "INV-003-DAG-ACYCLIC", description: "Accepted event lineage remains an immutable acyclic graph with explicit parents.", decisionIds: ["P2-KCF-005"], negativeRequirementIds: ["NEG-004", "NEG-014"], fixtureIds: ["DAG-VALID-BRANCH-JOIN", "DAG-REJECT-CYCLE"] },
  { invariantId: "INV-004-EXACT-OWNERSHIP", description: "At most one concrete FQN owns work; routing and claims do not.", decisionIds: ["P2-KCF-002", "P2-KCF-006"], negativeRequirementIds: ["NEG-007"], fixtureIds: ["IDENTITY-CONCRETE-FQN", "IDENTITY-REJECT-WILDCARD"] },
  { invariantId: "INV-005-EXECUTION-FENCING", description: "Only the current execution tuple may commit actor-attributed commands.", decisionIds: ["P2-KCF-002", "P2-KCF-007"], negativeRequirementIds: ["NEG-011"], fixtureIds: ["CAT-001-EXECUTION-REGISTER-VALID", "CAT-002-EXECUTION-REPLACE-VALID"] },
  { invariantId: "INV-006-LIFECYCLE-EPOCHS", description: "Completion, acceptance, reopening, and correction preserve immutable lifecycle epochs.", decisionIds: ["P2-KCF-006", "P2-KCF-008"], negativeRequirementIds: ["NEG-003", "NEG-008"], fixtureIds: ["MODEL-COMPLETION-NO-REQUESTED-PHASE", "MODEL-REOPEN-INCREMENTS-EPOCH", "MODEL-ILLEGAL-COMPLETION", "REVIEW-JOIN-ORDER-INDEPENDENT", "REVIEW-JOIN-DUPLICATE-CONFLICT", "SUCCESSOR-SET-CANONICAL", "SUCCESSOR-SET-REJECT-UNORDERED"] },
  { invariantId: "INV-007-BOUNDED-ITERATION", description: "Retry, validation, planning, handoff, and escalation are finite and durable across restart.", decisionIds: ["P2-KCF-005", "P2-KCF-007"], negativeRequirementIds: ["NEG-001", "NEG-002", "NEG-005", "NEG-006"], fixtureIds: ["IDEMPOTENCY-DUPLICATE-AND-CONFLICT"] },
  { invariantId: "INV-008-UNKNOWN-NO-EFFECT", description: "Unknown types are preserved and cannot produce canonical organizational effects.", decisionIds: ["P2-KCF-004"], negativeRequirementIds: ["NEG-009"], fixtureIds: ["CAT-UNKNOWN-COMMAND"] },
  { invariantId: "INV-009-PROVENANCE-COMPLETE", description: "Every event and receipt carries exact authority, evidence, contract, source, and runtime provenance.", decisionIds: ["P2-KCF-003", "P2-KCF-009", "P2-KCF-010"], negativeRequirementIds: ["NEG-014"], fixtureIds: ["CAT-003-EVIDENCE-REGISTER-VALID"] },
  { invariantId: "INV-010-NONAUTHORITATIVE-EVIDENCE", description: "Provider, model, tool, and SMA output remains evidence until accepted by policy.", decisionIds: ["P2-KCF-009"], negativeRequirementIds: ["NEG-008", "NEG-013"], fixtureIds: ["CAT-003-EVIDENCE-REGISTER-VALID"] },
  { invariantId: "INV-011-ATOMIC-MONGO-DECISION", description: "State, events, receipt, audit, and outbox commit atomically or remain explicitly uncertain.", decisionIds: ["P2-KCF-010"], negativeRequirementIds: ["NEG-012"], fixtureIds: ["IDEMPOTENCY-DUPLICATE-AND-CONFLICT"] },
  { invariantId: "INV-012-CONFORMANCE-REPRODUCIBLE", description: "Canonical fixtures and reports reproduce under pinned deterministic inputs.", decisionIds: ["P2-KCF-011", "P2-KCF-012"], negativeRequirementIds: [], fixtureIds: ["MODEL-STORY-FORWARD", "DAG-VALID-BRANCH-JOIN", "COMPATIBILITY-1.0-MISSING-CONTEXT"] },
  { invariantId: "INV-013-MERGE-TREE-QUALIFIED", description: "Qualification binds exact base, heads, ordered merge plan, contract, and resulting tree.", decisionIds: ["P2-KCF-012"], negativeRequirementIds: [], fixtureIds: ["IDEMPOTENCY-DUPLICATE-AND-CONFLICT"] },
  { invariantId: "INV-014-PROVIDER-NEUTRAL-KERNEL", description: "Provider, persistence, transport, credential, and process fields cannot redefine kernel semantics.", decisionIds: ["P2-KCF-001", "P2-KCF-004", "P2-KCF-009"], negativeRequirementIds: ["NEG-010"], fixtureIds: ["CAT-UNKNOWN-COMMAND"] },
];
writeJson("invariants/invariants.json", { schemaVersion, contractIdentity, invariants });

const decisionRegister = readJson("OUTPUT/phase-2/kernel-contract-freeze-decisions.json");
if (decisionRegister.counts.principalDecisionsRecorded !== 12 || decisionRegister.counts.awaitingPrincipalDecision !== 0) {
  throw new Error("all twelve principal decisions must be recorded before contract generation");
}
const negativeRegister = readJson("OUTPUT/adjudication/negative-requirement-decisions.json");
if (negativeRegister.counts.principalDecisionsRecorded !== 14 || negativeRegister.requirements.some((item) => item.finalDisposition !== "ADOPT")) {
  throw new Error("all fourteen negative requirements must be adopted before contract generation");
}
const docketText = read("PHASE-2/001-kernel-contract-freeze.md").toString("utf8");

function prohibitedForDecision(order) {
  const marker = `## Decision ${order} `;
  const start = docketText.indexOf(marker);
  const end = order === 12 ? docketText.indexOf("## Step 1 gate", start) : docketText.indexOf(`## Decision ${order + 1} `, start);
  const block = docketText.slice(start, end);
  const heading = block.indexOf("### Prohibited interpretations");
  if (heading < 0) return [];
  const tail = block.slice(heading + "### Prohibited interpretations".length);
  const nextHeading = tail.indexOf("\n### ");
  const section = nextHeading >= 0 ? tail.slice(0, nextHeading) : tail;
  const results = [];
  let current = null;
  for (const line of section.split("\n")) {
    if (line.startsWith("- ")) {
      if (current) results.push(current);
      current = line.slice(2).trim();
    } else if (current && /^\s{2,}\S/.test(line)) {
      current += ` ${line.trim()}`;
    }
  }
  if (current) results.push(current);
  return results;
}

const allFixtureIds = fixtures.map((fixture) => fixture.fixtureId);
const invariantIds = invariants.map((item) => item.invariantId);
const decisionTests = {
  "P2-KCF-001": ["INV-014-PROVIDER-NEUTRAL-KERNEL", ...catalogueFixtures.slice(0, 2).map((item) => item.fixtureId)],
  "P2-KCF-002": ["INV-004-EXACT-OWNERSHIP", "INV-005-EXECUTION-FENCING", "IDENTITY-CONCRETE-FQN", "IDENTITY-REJECT-WILDCARD", "IDENTITY-REJECT-LEADING-ZERO"],
  "P2-KCF-003": ["INV-001-REVISION-CONTIGUITY", "INV-002-IDEMPOTENT-DECISION", ...catalogueFixtures.map((item) => item.fixtureId)],
  "P2-KCF-004": ["INV-008-UNKNOWN-NO-EFFECT", "CAT-UNKNOWN-COMMAND", ...catalogueFixtures.map((item) => item.fixtureId)],
  "P2-KCF-005": ["INV-003-DAG-ACYCLIC", "DAG-VALID-BRANCH-JOIN", "DAG-REJECT-CYCLE"],
  "P2-KCF-006": ["INV-004-EXACT-OWNERSHIP", "INV-006-LIFECYCLE-EPOCHS", "MODEL-STORY-FORWARD", "MODEL-TASK-FORWARD", "MODEL-BLOCK-ORTHOGONAL", "MODEL-ILLEGAL-COMPLETION"],
  "P2-KCF-007": ["INV-001-REVISION-CONTIGUITY", "INV-002-IDEMPOTENT-DECISION", "INV-007-BOUNDED-ITERATION", "IDEMPOTENCY-DUPLICATE-AND-CONFLICT", "PRECONDITION-EXACT-ALL", "PRECONDITION-STALE-NONE"],
  "P2-KCF-008": ["INV-006-LIFECYCLE-EPOCHS", "MODEL-COMPLETION-NO-REQUESTED-PHASE", "MODEL-REOPEN-INCREMENTS-EPOCH", "REVIEW-JOIN-ORDER-INDEPENDENT", "REVIEW-JOIN-DUPLICATE-CONFLICT", "SUCCESSOR-SET-CANONICAL", "SUCCESSOR-SET-REJECT-UNORDERED"],
  "P2-KCF-009": ["INV-009-PROVENANCE-COMPLETE", "INV-010-NONAUTHORITATIVE-EVIDENCE", "CAT-003-EVIDENCE-REGISTER-VALID"],
  "P2-KCF-010": ["INV-011-ATOMIC-MONGO-DECISION", "IDEMPOTENCY-DUPLICATE-AND-CONFLICT"],
  "P2-KCF-011": [...invariantIds, ...allFixtureIds],
  "P2-KCF-012": ["INV-012-CONFORMANCE-REPRODUCIBLE", "INV-013-MERGE-TREE-QUALIFIED", "COMPATIBILITY-1.0-MISSING-CONTEXT"],
};

const requirements = [];
for (const decision of decisionRegister.decisions) {
  requirements.push({
    requirementId: decision.id,
    kind: "DECISION",
    text: decision.title,
    disposition: decision.principalDisposition,
    testIds: decisionTests[decision.id],
  });
  const prohibitions = prohibitedForDecision(decision.order);
  prohibitions.forEach((text, index) => requirements.push({
    requirementId: `${decision.id}-PROHIBIT-${String(index + 1).padStart(2, "0")}`,
    kind: "PROHIBITED_INTERPRETATION",
    parentDecisionId: decision.id,
    text,
    disposition: "BINDING",
    testIds: decisionTests[decision.id],
  }));
}
for (const requirement of negativeRegister.requirements) {
  const linked = invariants.filter((item) => item.negativeRequirementIds.includes(requirement.id)).map((item) => item.invariantId);
  requirements.push({
    requirementId: requirement.id,
    kind: "NEGATIVE_REQUIREMENT",
    text: requirement.adoptedRequirement,
    disposition: requirement.finalDisposition,
    testIds: linked.length > 0 ? linked : ["INV-012-CONFORMANCE-REPRODUCIBLE"],
  });
}
writeJson("traceability/traceability.json", {
  schemaVersion,
  contractIdentity,
  sourceDecisionRegister: { path: "OUTPUT/phase-2/kernel-contract-freeze-decisions.json", sha256: sha256(read("OUTPUT/phase-2/kernel-contract-freeze-decisions.json")) },
  sourceNegativeRequirementRegister: { path: "OUTPUT/adjudication/negative-requirement-decisions.json", sha256: sha256(read("OUTPUT/adjudication/negative-requirement-decisions.json")) },
  requirements,
});

const predecessorManifestBytes = fs.readFileSync(path.join(predecessorRoot, "manifest.json"));
writeJson("compatibility/from-0.1.0.json", {
  schemaVersion,
  contractName: "tekroo.kernel.contracts",
  contractVersion,
  predecessor: { contractVersion: predecessorVersion, contractIdentity: `tekroo.kernel.contracts/${predecessorVersion}`, manifestSha256: sha256(predecessorManifestBytes) },
  compatibilityClaim: "BREAKING_WIRE_REVISION_WITH_EXPLICIT_ADAPTER_MIGRATION",
  directions: { backward: "PARTIAL_ADAPTER_REQUIRED", forward: "NOT_COMPATIBLE", replay: "VERSION_GATED", adapter: "REQUIRED" },
  unchangedSemanticTypes: operations.map((op) => op.command),
  breakingChanges: [
    "KernelCommand 1.1.0 requires exact related-aggregate, lifecycle, policy, and catalogue preconditions.",
    "Completion-review commands 1.1.0 require named branch and join-policy fields.",
    "CreateSuccessor 1.1.0 replaces successor_id with a deterministic successor_ids set and explicit ownership/dependency rules.",
    "EvidenceRegister 1.1.0 requires the complete Decision 9 evidence metadata and lifecycle fields.",
  ],
  migrationRules: [
    { source: "1.0.0", target: "1.1.0", mode: "REJECT_WITHOUT_EXACT_CONTEXT", reason: "Missing exact preconditions or mandatory metadata cannot be inferred." },
    { source: "1.1.0", target: "1.1.0", mode: "IDENTITY" },
  ],
  rollback: "Readers retain 0.1.0 for historical replay. New 1.1.0 commands are never down-converted to 1.0.0.",
});

writeJson("runner/protocol.json", {
  schemaVersion,
  protocolVersion: "1.0.0",
  contractIdentity,
  resultClasses: ["PASS", "FAIL", "NOT_RUN", "INCONCLUSIVE"],
  requiredInputs: ["manifest_sha256", "profile", "source_identity", "build_identity", "runtime_identity", "fixture_ids", "fake_clock", "id_source", "fault_schedule"],
  requiredOutputs: ["status", "case_results", "executed_count", "skipped_count", "seeds", "scheduler_traces", "artifacts", "report_sha256"],
  rules: [
    "A skipped, unsupported, unavailable, or retried-away required case cannot be PASS.",
    "All variable outputs must be declared by the manifest; undeclared variation is failure.",
    "Every property failure records seed, bounds, trace, and minimized counterexample.",
    "Reports state their qualification boundary and cannot claim production readiness from semantic conformance.",
  ],
});

fs.mkdirSync(path.join(packageRoot, "runner"), { recursive: true });
let successorReferenceRunner = fs.readFileSync(path.join(predecessorRoot, "runner/reference-runner.mjs"), "utf8")
  .replace(
    'function validateType(value, rule) {\n',
    'function validateType(value, rule) {\n  if (rule.anyOf !== undefined) return rule.anyOf.some((candidate) => validateType(value, candidate));\n  if (rule.const !== undefined) return value === rule.const;\n  if (rule.type === "null") return value === null;\n',
  )
  .replace(
    '    return rule.items === undefined || value.every((item) => validateType(item, rule.items));',
    '    if (rule.uniqueItems && new Set(value.map(canonical)).size !== value.length) return false;\n    return rule.items === undefined || value.every((item) => validateType(item, rule.items));',
  )
  .replace(
    '  if (rule.type === "object") return value !== null && typeof value === "object" && !Array.isArray(value);',
    '  if (rule.type === "object") return validatePayload(value, rule);',
  )
  .replace(
    '  for (const [name, value] of Object.entries(payload)) {\n    const rule = schema.properties?.[name];\n    if (!rule || !validateType(value, rule)) return false;\n  }',
    '  if (schema.minProperties !== undefined && Object.keys(payload).length < schema.minProperties) return false;\n  for (const [name, value] of Object.entries(payload)) {\n    const rule = schema.properties?.[name];\n    if (rule && !validateType(value, rule)) return false;\n    if (!rule && schema.additionalProperties === false) return false;\n  }',
  )
  .replace(
    'function runIdempotencyModel(fixture) {',
    `function runPreconditionModel(fixture) {
  const keys = fixture.when.preconditions.map((item) => item.aggregate);
  if (new Set(keys).size !== keys.length || canonical([...keys].sort()) !== canonical(keys)) return { valid: false };
  return { valid: fixture.when.preconditions.every((item) => fixture.given.revisions[item.aggregate] === item.expectedRevision) };
}

function runReviewJoinModel(fixture) {
  const required = new Set(fixture.given.requiredBranchIds);
  const results = new Map();
  for (const item of fixture.when.results) {
    if (!required.has(item.branchId) || (results.has(item.branchId) && results.get(item.branchId) !== item.result)) return { status: "CONFLICT", complete: false };
    results.set(item.branchId, item.result);
    if (item.result === "FAIL" && fixture.given.partialResultPolicy === "FAIL_FAST") return { status: "FAIL", complete: true };
  }
  if (results.size !== required.size) return { status: "PENDING", complete: false };
  return { status: [...results.values()].every((value) => value === "PASS") ? "PASS" : "FAIL", complete: true };
}

function runSuccessorSetModel(fixture) {
  const values = fixture.when.successorIds;
  return { valid: values.length > 0 && new Set(values).size === values.length && canonical([...values].sort()) === canonical(values) };
}

function runCompatibilityModel(fixture) {
  return { outcome: fixture.given.sourceVersion === "1.0.0" && !fixture.when.exactContextAvailable ? "MIGRATION_REQUIRED" : "ACCEPTED" };
}

function runIdempotencyModel(fixture) {`,
  )
  .replace(
    '  } else if (fixture.kind === "IDEMPOTENCY_MODEL") {\n    actual = runIdempotencyModel(fixture);',
    '  } else if (fixture.kind === "IDEMPOTENCY_MODEL") {\n    actual = runIdempotencyModel(fixture);\n  } else if (fixture.kind === "PRECONDITION_MODEL") {\n    actual = runPreconditionModel(fixture);\n  } else if (fixture.kind === "REVIEW_JOIN_MODEL") {\n    actual = runReviewJoinModel(fixture);\n  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {\n    actual = runSuccessorSetModel(fixture);\n  } else if (fixture.kind === "COMPATIBILITY_MODEL") {\n    actual = runCompatibilityModel(fixture);',
  );
fs.writeFileSync(path.join(packageRoot, "runner/reference-runner.mjs"), successorReferenceRunner, { flag: "wx" });
let successorValidator = fs.readFileSync(path.join(predecessorRoot, "runner/validate-package.mjs"), "utf8")
  .replace('manifest.contract.version === "0.1.0"', 'manifest.contract.version === "0.2.0"')
  .replaceAll('compatibility/no-predecessor.json', 'compatibility/from-0.1.0.json')
  .replace(
    'compatibility.predecessor === null && compatibility.contractVersion === "0.1.0"',
    'compatibility.predecessor?.contractVersion === "0.1.0" && compatibility.contractVersion === "0.2.0" && compatibility.directions?.adapter === "REQUIRED"',
  )
  .replace('initial-compatibility-record', 'successor-compatibility-record');
fs.writeFileSync(path.join(packageRoot, "runner/validate-package.mjs"), successorValidator, { flag: "wx" });

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
  if (relativePath === "runner/validate-package.mjs") return ["catalogue/kernel-catalogue.json", "schemas/payloads.schema.json", "invariants/invariants.json", "traceability/traceability.json", "compatibility/from-0.1.0.json", "runner/protocol.json"];
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

const manifest = {
  schemaVersion,
  contract: { name: "tekroo.kernel.contracts", version: contractVersion, status: "FROZEN_CANDIDATE" },
  generatedAt: "2026-08-11T00:00:00Z",
  authority: { decisionAuthority: "Principal", implementationAuthority: "NONE", productionAuthority: "NONE" },
  sourceLineage: [
    { path: "OUTPUT/adjudication/step-8-final-gate.json", sha256: sha256(read("OUTPUT/adjudication/step-8-final-gate.json")) },
    { path: "PHASE-2/001-kernel-contract-freeze.md", sha256: sha256(read("PHASE-2/001-kernel-contract-freeze.md")) },
    { path: "OUTPUT/phase-2/kernel-contract-freeze-decisions.json", sha256: sha256(read("OUTPUT/phase-2/kernel-contract-freeze-decisions.json")) },
    { path: "OUTPUT/adjudication/negative-requirement-decisions.json", sha256: sha256(read("OUTPUT/adjudication/negative-requirement-decisions.json")) },
    { path: "OUTPUT/phase-2/step-2-contract-revision-authorization.json", sha256: sha256(read("OUTPUT/phase-2/step-2-contract-revision-authorization.json")) },
    { path: "OUTPUT/phase-2/step-2-contract-encoding-gaps.md", sha256: sha256(read("OUTPUT/phase-2/step-2-contract-encoding-gaps.md")) },
    { path: `CONTRACTS/tekroo.kernel.contracts/${predecessorVersion}/manifest.json`, sha256: sha256(predecessorManifestBytes) },
    { path: "scripts/generate_phase2_contract_0_2.mjs", sha256: sha256(read("scripts/generate_phase2_contract_0_2.mjs")) },
  ],
  canonicalization: { textEncoding: "UTF-8", byteOrderMark: false, lineEnding: "LF", jsonSemanticCanonicalization: "RFC-8785-compatible sorted-key canonical JSON", byteDigest: "SHA-256", manifestDigest: "detached manifest.sha256" },
  inventoryBoundary: "files lists every payload asset; manifest.json is identified by detached manifest.sha256 and manifest.sha256 is not self-listed.",
  counts: { catalogueEntries: catalogueEntries.length, commandTypes: operations.length, eventTypes: operations.length, schemas: fileInventory.filter((item) => item.role === "schemas").length, fixtures: fixtures.length, invariants: invariants.length, traceabilityRequirements: requirements.length },
  profiles: [
    { name: "contract-structure", authorization: "STEP_1", required: true, currentStatus: "READY_TO_RUN" },
    { name: "core-hermetic", authorization: "FUTURE_IMPLEMENTATION_GATE", required: true, currentStatus: "REFERENCE_MODEL_ONLY" },
    { name: "mongo-integration", authorization: "FUTURE_IMPLEMENTATION_GATE", required: true, currentStatus: "NOT_RUN" },
    { name: "synthesized-merge", authorization: "FUTURE_MERGE_GATE", required: true, currentStatus: "NOT_RUN" },
    { name: "provider-e2e", authorization: "SEPARATE_PRINCIPAL_AUTHORIZATION", required: false, currentStatus: "NOT_RUN" },
  ],
  files: fileInventory,
  knownLimitations: [
    "No Tekroo v4 runtime implementation is qualified by this package.",
    "Mongo integration, synthesized merge, OpenHands, SMA, Git provider, model provider, migration, and production profiles are not run in Step 1.",
    "Performance and resource budgets require later measured workload evidence.",
    "Version 1.0.0 commands that lack exact 1.1.0 preconditions or metadata require an authorized adapter and cannot be inferred by the kernel.",
  ],
};

fs.writeFileSync(manifestPath, pretty(manifest), { flag: "wx" });
const manifestDigest = sha256(fs.readFileSync(manifestPath));
fs.writeFileSync(checksumPath, `${manifestDigest}  manifest.json\n`, { flag: "wx" });
process.stdout.write(`GENERATED ${contractIdentity} ${manifestDigest} ${fileInventory.length}\n`);
