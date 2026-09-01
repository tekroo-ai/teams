#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.8.0");
const target = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.9.0");
const oldIdentity = "tekroo.kernel.contracts/0.8.0";
const newIdentity = "tekroo.kernel.contracts/0.9.0";

function sha256(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
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

function writeJson(relative, value) {
  const absolute = path.join(target, relative);
  fs.mkdirSync(path.dirname(absolute), { recursive: true });
  fs.writeFileSync(absolute, `${JSON.stringify(sortValue(value), null, 2)}\n`);
}

function walk(directory, prefix = "") {
  const values = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) values.push(...walk(absolute, relative));
    else values.push(relative);
  }
  return values.sort();
}

function replaceIdentityInCopiedFiles() {
  for (const relative of walk(target)) {
    if (relative === "manifest.json" || relative === "manifest.sha256") continue;
    const absolute = path.join(target, relative);
    const raw = fs.readFileSync(absolute, "utf8");
    fs.writeFileSync(absolute, raw.split(oldIdentity).join(newIdentity).split('version === "0.8.0"').join('version === "0.9.0"'));
  }
}

function stringSchema(maxLength = 4096) {
  return { type: "string", minLength: 1, maxLength };
}

const uuid = { type: "string", pattern: "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$" };
const digest = { type: "string", pattern: "^[0-9a-f]{64}$" };
const actor = { type: "string", pattern: "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])-[1-9][0-9]{0,19}$" };
const principal = {
  type: "object", additionalProperties: false, required: ["kind", "id"],
  properties: { kind: { enum: ["HUMAN", "ACTOR", "SERVICE", "POLICY"] }, id: stringSchema(256) },
};

const organizationSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `${newIdentity}/schemas/organization-runtime.schema.json`,
  title: "Tekroo Teams organizational runtime contract",
  $defs: {
    subscription: {
      type: "object", additionalProperties: false, required: ["type", "purpose"],
      properties: { type: stringSchema(256), purpose: stringSchema(128) },
    },
    roleBundle: {
      type: "object", additionalProperties: false,
      required: ["schema_version", "role", "version", "capabilities", "subscriptions", "permissions", "instructions", "handlers", "publisher_key_id", "signature"],
      properties: {
        schema_version: { const: "1.0.0" }, role: stringSchema(64), version: { type: "string", pattern: "^[0-9]+\\.[0-9]+\\.[0-9]+$" },
        capabilities: { type: "array", minItems: 1, maxItems: 128, uniqueItems: true, items: stringSchema(256) },
        subscriptions: { type: "array", maxItems: 256, items: { $ref: "#/$defs/subscription" } },
        permissions: { type: "array", minItems: 1, maxItems: 128, uniqueItems: true, items: stringSchema(256) },
        instructions: stringSchema(1048576), handlers: { type: "object", maxProperties: 256, additionalProperties: stringSchema(65536) },
        publisher_key_id: stringSchema(256), signature: stringSchema(4096),
      },
    },
    roleBinding: {
      type: "object", additionalProperties: false,
      required: ["role", "bundle_path", "bundle_digest", "publisher_key_id", "initial_instances", "maximum_instances", "launch_mode", "model_profile_digest", "workspace_ids"],
      properties: {
        role: stringSchema(64), bundle_path: stringSchema(4096), bundle_digest: digest, publisher_key_id: stringSchema(256),
        initial_instances: { type: "integer", minimum: 1, maximum: 64 }, maximum_instances: { type: "integer", minimum: 1, maximum: 64 },
        launch_mode: { enum: ["EAGER", "ON_DEMAND", "MANUAL"] }, model_profile_digest: digest,
        workspace_ids: { type: "array", minItems: 1, maxItems: 64, uniqueItems: true, items: stringSchema(1024) },
      },
    },
    teamManifest: {
      type: "object", additionalProperties: false, required: ["schema_version", "team", "version", "roles"],
      properties: { schema_version: { const: "1.0.0" }, team: stringSchema(64), version: { type: "string", pattern: "^[0-9]+\\.[0-9]+\\.[0-9]+$" }, roles: { type: "array", minItems: 1, maxItems: 128, items: { $ref: "#/$defs/roleBinding" } } },
    },
    executionTuple: {
      type: "object", additionalProperties: false, required: ["execution_id", "fencing_epoch"],
      properties: { execution_id: uuid, fencing_epoch: { type: "integer", minimum: 1 } },
    },
    messageFlow: {
      type: "object", additionalProperties: false,
      required: ["thread_id", "step_id", "hop", "maximum_hops", "budget_account_id", "lifecycle_epoch", "scope_revision", "progress_digest"],
      properties: { thread_id: uuid, step_id: uuid, parent_step_id: { anyOf: [uuid, { type: "null" }] }, hop: { type: "integer", minimum: 1, maximum: 64 }, maximum_hops: { type: "integer", minimum: 1, maximum: 64 }, budget_account_id: uuid, lifecycle_epoch: { type: "integer", minimum: 1 }, scope_revision: { type: "integer", minimum: 1 }, progress_digest: digest },
    },
    organizationalMessage: {
      type: "object", additionalProperties: false,
      required: ["schema_version", "id", "type", "purpose", "sender", "sender_execution", "recipient", "correlation_id", "work", "flow", "body", "created_at", "expires_at"],
      properties: {
        schema_version: { const: "1.0.0" }, id: uuid, type: { type: "string", pattern: "^tekroo\\.message\\.[a-z0-9]+(?:[.-][a-z0-9]+)*$" }, purpose: { enum: ["REQUEST", "HANDOFF", "EVIDENCE", "RESPONSE", "NOTIFICATION"] }, sender: actor, sender_execution: { $ref: "#/$defs/executionTuple" }, recipient: actor,
        causation_id: { anyOf: [uuid, { type: "null" }] }, correlation_id: uuid, fanout_id: { anyOf: [uuid, { type: "null" }] },
        work: { type: "object", additionalProperties: false, required: ["dag_node_id"], properties: { feature_id: { anyOf: [uuid, { type: "null" }] }, story_id: { anyOf: [uuid, { type: "null" }] }, task_id: { anyOf: [uuid, { type: "null" }] }, dag_node_id: uuid } },
        flow: { $ref: "#/$defs/messageFlow" }, body: { type: "object", minProperties: 1 }, created_at: { type: "string", format: "date-time" }, expires_at: { type: "string", format: "date-time" },
        readdress_history: { type: "array", maxItems: 8, items: { type: "object" } },
      },
    },
    featureRequestInput: {
      type: "object", required: ["idempotency_key", "team", "title", "description", "acceptance_criteria", "priority", "repository", "workspace_id", "maximum_stories", "maximum_tasks", "maximum_hops"],
      properties: { idempotency_key: stringSchema(256), team: stringSchema(64), title: stringSchema(512), description: stringSchema(65536), acceptance_criteria: { type: "array", minItems: 1, maxItems: 64, uniqueItems: true, items: stringSchema(4096) }, priority: { enum: ["LOW", "NORMAL", "HIGH", "CRITICAL"] }, constraints: { type: "array", maxItems: 64, uniqueItems: true, items: stringSchema(4096) }, repository: stringSchema(4096), workspace_id: stringSchema(1024), maximum_stories: { type: "integer", minimum: 1, maximum: 64 }, maximum_tasks: { type: "integer", minimum: 1, maximum: 256 }, maximum_hops: { type: "integer", minimum: 1, maximum: 64 } },
    },
    humanNotification: {
      type: "object", additionalProperties: false,
      required: ["schema_version", "interaction_id", "subject_task_id", "recipient", "question", "response_specification", "delivery_id", "delivery_event_id", "state", "created_at", "updated_at"],
      properties: { schema_version: { const: "1.0.0" }, interaction_id: uuid, subject_task_id: uuid, recipient: principal, question: stringSchema(65536), response_specification: stringSchema(16384), delivery_id: uuid, delivery_event_id: uuid, state: { enum: ["COLLECTING", "CLOSED", "TIMED_OUT"] }, response: { type: "string", maxLength: 65536 }, response_event_id: uuid, created_at: { type: "string", format: "date-time" }, updated_at: { type: "string", format: "date-time" } },
    },
  },
};

const organizationFixtures = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  fixtures: [
    { fixtureId: "P6-ORG-001", kind: "TEAM_MANIFEST", classification: "NORMATIVE_EXAMPLE", assertion: "a signed content-addressed manifest resolves an exact finite roster" },
    { fixtureId: "P6-ORG-002", kind: "TEAM_MANIFEST", classification: "BOUNDARY_NEGATIVE", assertion: "a missing, changed, or untrusted bundle fails activation" },
    { fixtureId: "P6-ORG-003", kind: "MESSAGE_DELIVERY", classification: "NORMATIVE_EXAMPLE", assertion: "one exact directed message produces one durable recipient claim" },
    { fixtureId: "P6-ORG-004", kind: "MESSAGE_DELIVERY", classification: "BOUNDARY_NEGATIVE", assertion: "message delivery has no path to model invocation or role launch" },
    { fixtureId: "P6-ORG-005", kind: "MESSAGE_FANOUT", classification: "NORMATIVE_EXAMPLE", assertion: "fanout materializes one exact delivery per recipient" },
    { fixtureId: "P6-ORG-006", kind: "MESSAGE_LOOP", classification: "BOUNDARY_NEGATIVE", assertion: "renaming a message or role cannot reset hop, progress, or work budgets" },
    { fixtureId: "P6-ORG-007", kind: "MESSAGE_RECOVERY", classification: "NORMATIVE_EXAMPLE", assertion: "stream-before-backlog recovery is lossless and duplicate-safe" },
    { fixtureId: "P6-ORG-008", kind: "MESSAGE_RECOVERY", classification: "BOUNDARY_NEGATIVE", assertion: "poison delivery reaches dead letter within the attempt limit" },
    { fixtureId: "P6-ORG-009", kind: "FEATURE_INTAKE", classification: "NORMATIVE_EXAMPLE", assertion: "one feature request binds one product owner and a finite story/task DAG" },
    { fixtureId: "P6-ORG-010", kind: "HUMAN_PARTICIPATION", classification: "NORMATIVE_EXAMPLE", assertion: "an authenticated exact recipient response resumes only the linked blocked task" },
    { fixtureId: "P6-ORG-011", kind: "HUMAN_PARTICIPATION", classification: "BOUNDARY_NEGATIVE", assertion: "waiting for a human consumes no model invocation" },
    { fixtureId: "P6-ORG-012", kind: "AUTHORITY_BOUNDARY", classification: "BOUNDARY_NEGATIVE", assertion: "SMA, prompts, tools, providers, and channels cannot create organizational authority" },
  ],
};

const organizationInvariants = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  invariants: [
    { invariantId: "P6-INV-001", statement: "Stable actor FQN is independent of replaceable process and execution identity.", testIds: ["P6-ORG-001", "P6-ORG-002"] },
    { invariantId: "P6-INV-002", statement: "Organizational messages carry information but never execution authority.", testIds: ["P6-ORG-003", "P6-ORG-004"] },
    { invariantId: "P6-INV-003", statement: "Every fanout recipient owns a distinct durable delivery.", testIds: ["P6-ORG-005"] },
    { invariantId: "P6-INV-004", statement: "Hop, progress, and root-work budgets survive role and message changes.", testIds: ["P6-ORG-006"] },
    { invariantId: "P6-INV-005", statement: "Recovery is bounded, duplicate-safe, and terminal for poison work.", testIds: ["P6-ORG-007", "P6-ORG-008"] },
    { invariantId: "P6-INV-006", statement: "Feature decomposition is finite and Teams-authoritative.", testIds: ["P6-ORG-009"] },
    { invariantId: "P6-INV-007", statement: "Human identity, response, and task linkage are exact and durable.", testIds: ["P6-ORG-010", "P6-ORG-011"] },
    { invariantId: "P6-INV-008", statement: "External cognition and transport remain non-authoritative.", testIds: ["P6-ORG-012"] },
  ],
};

const organizationTraceability = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  requirements: organizationInvariants.invariants.map((item) => ({ requirementId: item.invariantId.replace("INV", "REQ"), kind: "DECISION", statement: item.statement, testIds: item.testIds })),
};

fs.rmSync(target, { recursive: true, force: true });
fs.cpSync(source, target, { recursive: true });
replaceIdentityInCopiedFiles();
fs.rmSync(path.join(target, "manifest.json"), { force: true });
fs.rmSync(path.join(target, "manifest.sha256"), { force: true });
writeJson("schemas/organization-runtime.schema.json", organizationSchema);
writeJson("fixtures/organization-runtime.json", organizationFixtures);
writeJson("invariants/organization-invariants.json", organizationInvariants);
writeJson("traceability/organization-traceability.json", organizationTraceability);
writeJson("compatibility/from-0.8.0.json", {
  schemaVersion: "1.0.0", contractName: "tekroo.kernel.contracts", contractVersion: "0.9.0",
  predecessor: { contractName: "tekroo.kernel.contracts", contractVersion: "0.8.0", manifestSha256: sha256(fs.readFileSync(path.join(source, "manifest.json"))) },
  compatibility: "ADDITIVE", directions: { reader: "COMPATIBLE", writer: "SUCCESSOR_IDENTITY_REQUIRED", adapter: "NOT_REQUIRED" },
  additions: ["signed team and role manifests", "role host and liveness state", "exact-directed organizational messages and delivery", "feature intake and planning", "focused operator and human participant surfaces"],
  preserved: ["all 0.8.0 kernel commands, events, invariants, negative fixtures, and authority boundaries"],
});

const validatorPath = path.join(target, "runner/validate-package.mjs");
let validator = fs.readFileSync(validatorPath, "utf8");
validator = validator.replace('const compatibility = readJson("compatibility/from-0.7.0.json");', 'const compatibility = readJson("compatibility/from-0.8.0.json");');
validator = validator.replace('compatibility.predecessor?.contractVersion === "0.7.0" && compatibility.contractVersion === "0.8.0" && compatibility.directions?.adapter === "REQUIRED"', 'compatibility.predecessor?.contractVersion === "0.8.0" && compatibility.contractVersion === "0.9.0" && compatibility.compatibility === "ADDITIVE"');
validator = validator.replace('check("phase4-state-schemas"', 'check("phase6-organization-schema", listedPayloadFiles.includes("schemas/organization-runtime.schema.json"));\nconst organizationFixtures = readJson("fixtures/organization-runtime.json");\nconst organizationInvariants = readJson("invariants/organization-invariants.json");\nconst organizationTraceability = readJson("traceability/organization-traceability.json");\nconst organizationTestIds = new Set([...organizationFixtures.fixtures.map((item) => item.fixtureId), ...organizationInvariants.invariants.map((item) => item.invariantId)]);\ncheck("phase6-organization-fixture-cardinality", organizationFixtures.fixtures.length === 12);\ncheck("phase6-organization-invariant-cardinality", organizationInvariants.invariants.length === 8);\ncheck("phase6-organization-traceability", organizationTraceability.requirements.every((item) => item.testIds.length > 0 && item.testIds.every((id) => organizationTestIds.has(id))));\ncheck("phase4-state-schemas"');
fs.writeFileSync(validatorPath, validator);

const sourceManifest = JSON.parse(fs.readFileSync(path.join(source, "manifest.json"), "utf8"));
const paths = walk(target).filter((value) => value !== "manifest.json" && value !== "manifest.sha256");
const sourceByPath = new Map(sourceManifest.files.map((entry) => [entry.path, entry]));
const specialRoles = new Map([
  ["schemas/organization-runtime.schema.json", "organization-schemas"],
  ["fixtures/organization-runtime.json", "organization-fixtures"],
  ["invariants/organization-invariants.json", "organization-invariants"],
  ["traceability/organization-traceability.json", "organization-traceability"],
  ["compatibility/from-0.8.0.json", "compatibility"],
]);
const files = paths.map((relative) => {
  const bytes = fs.readFileSync(path.join(target, relative));
  const json = relative.endsWith(".json") ? JSON.parse(bytes.toString("utf8")) : null;
  const sourceEntry = sourceByPath.get(relative);
  return {
    path: relative,
    role: specialRoles.get(relative) ?? sourceEntry?.role ?? "contract-asset",
    mediaType: json ? "application/json" : sourceEntry?.mediaType ?? "text/javascript",
    bytes: bytes.length,
    sha256: sha256(bytes),
    canonicalJsonSha256: json ? sha256(canonical(json)) : null,
    dependencies: sourceEntry?.dependencies?.map((value) => value.replace("0.7.0", "0.8.0")) ?? [],
  };
});
const manifest = {
  ...sourceManifest,
  contract: { name: "tekroo.kernel.contracts", version: "0.9.0", status: "FROZEN_CANDIDATE" },
  generatedAt: "2026-08-31T00:00:00Z",
  files,
  counts: { ...sourceManifest.counts, schemas: sourceManifest.counts.schemas + 1, fixtures: sourceManifest.counts.fixtures + organizationFixtures.fixtures.length, invariants: sourceManifest.counts.invariants + organizationInvariants.invariants.length, traceabilityRequirements: sourceManifest.counts.traceabilityRequirements + organizationTraceability.requirements.length },
  knownLimitations: [
    "Contract structure does not qualify the Go implementation or live operation.",
    "Aliases, signed cross-host ingress, and cross-team routing remain explicitly deferred to the dependency recorded by the Phase 6 preservation ledger.",
    "No v3 data migration, deployment, live service activity, OpenHands conversation, or SMA modification is authorized by this package.",
  ],
  sourceLineage: [
    { path: "CONTRACTS/tekroo.kernel.contracts/0.8.0/manifest.json", sha256: sha256(fs.readFileSync(path.join(source, "manifest.json"))) },
    { path: "docs/architecture/119-phase-6-organizational-runtime-recovery-plan.md", sha256: sha256(fs.readFileSync(path.join(root, "docs/architecture/119-phase-6-organizational-runtime-recovery-plan.md"))) },
    { path: "docs/architecture/120-phase-6-feature-preservation-baseline.md", sha256: sha256(fs.readFileSync(path.join(root, "docs/architecture/120-phase-6-feature-preservation-baseline.md"))) },
    { path: "OUTPUT/phase-6/step-1/feature-preservation-ledger.json", sha256: sha256(fs.readFileSync(path.join(root, "OUTPUT/phase-6/step-1/feature-preservation-ledger.json"))) },
    { path: "scripts/generate_phase6_contract_0_9.mjs", sha256: sha256(fs.readFileSync(fileURLToPath(import.meta.url))) },
  ],
};
writeJson("manifest.json", manifest);
const manifestBytes = fs.readFileSync(path.join(target, "manifest.json"));
fs.writeFileSync(path.join(target, "manifest.sha256"), `${sha256(manifestBytes)}  manifest.json\n`);
process.stdout.write(`${newIdentity} ${sha256(manifestBytes)} ${files.length}\n`);
