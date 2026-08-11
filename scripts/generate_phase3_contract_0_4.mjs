#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const predecessorVersion = "0.3.0";
const contractVersion = "0.4.0";
const predecessorSchemaVersion = "1.2.0";
const schemaVersion = "1.3.0";
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
    .replaceAll("tekroo.kernel.contracts/0.3.0", contractIdentity)
    .replaceAll("_1_2_0", "_1_3_0")
    .replaceAll("1.2.0", schemaVersion);
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
  "compatibility/from-0.2.0.json",
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
core.$defs.AggregateRef.properties.kind.enum.push("escalation");
core.$defs.AggregateRef.properties.kind.enum.sort();
overwriteJson(corePath, core);

const triggerKinds = [
  "HANDOFF_BUDGET_EXHAUSTED",
  "HANDOFF_CYCLE_DETECTED",
  "RETRY_EXHAUSTED",
  "VALIDATION_BUDGET_EXHAUSTED",
  "VALIDATION_CONFLICT",
  "VALIDATION_DEADLINE_EXPIRED",
  "VALIDATION_INCONCLUSIVE",
];
const terminalOutcomes = ["BLOCKED", "HUMAN_REQUIRED", "REJECTED", "RESOLVED", "SPLIT"];
const openProperties = {
  escalation_id: uuid(),
  subject_kind: string({ enum: ["story", "task"] }),
  subject_id: uuid(),
  subject_lifecycle_epoch: integer({ minimum: 1 }),
  expected_subject_revision: integer({ minimum: 1 }),
  trigger: string({ enum: triggerKinds }),
  triggering_condition_digest: digest(),
  adjudicator: principal(),
  timeout_policy: policyPrincipal(),
  resolution_owner_fqn: actor(),
  deadline_at: string({ format: "date-time" }),
  resolution_round_limit: integer({ minimum: 1, maximum: 1000 }),
  route_limit: { const: 1 },
  escalation_policy_revision: integer({ minimum: 1 }),
  causal_path_event_ids: uuidList({ minItems: 1 }),
  unresolved_question: nonempty(),
  evidence_ids: uuidList({ minItems: 1 }),
};
const resolveProperties = {
  escalation_id: uuid(),
  subject_kind: string({ enum: ["story", "task"] }),
  subject_id: uuid(),
  subject_lifecycle_epoch: integer({ minimum: 1 }),
  expected_escalation_revision: integer({ minimum: 1 }),
  source_role: string({ enum: ["ADJUDICATOR", "POLICY_TIMEOUT"] }),
  round: integer({ minimum: 1, maximum: 1000 }),
  outcome: string({ enum: terminalOutcomes }),
  reasons: stringList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
  decided_at: string({ format: "date-time" }),
};

const payloadPath = "schemas/payloads.schema.json";
const payloads = readJson(`CONTRACTS/${contractIdentity}/${payloadPath}`);
payloads.$defs.tekroo_command_escalation_open_1_3_0 = object(openProperties);
payloads.$defs.tekroo_event_escalation_opened_1_3_0 = object(openProperties);
payloads.$defs.tekroo_command_escalation_resolve_1_3_0 = object(resolveProperties);
payloads.$defs.tekroo_event_escalation_resolved_1_3_0 = object(resolveProperties);
overwriteJson(payloadPath, payloads);

const cataloguePath = "catalogue/kernel-catalogue.json";
const catalogue = readJson(`CONTRACTS/${contractIdentity}/${cataloguePath}`);
catalogue.revision = 4;
const predecessorTypeIds = catalogue.entries.map((entry) => entry.typeId).sort();
for (const entry of catalogue.entries) {
  entry.compatibility = {
    acceptedSourceVersions: [predecessorSchemaVersion, schemaVersion],
    transforms: [{ sourceVersion: predecessorSchemaVersion, targetVersion: schemaVersion, mode: "IDENTITY" }],
  };
}
const commonEntry = {
  version: schemaVersion,
  lifecycle: "ACTIVE",
  owner: "tekroo-kernel",
  targetKinds: ["escalation"],
  executionRequired: false,
  allowedParentEdges: ["CAUSAL", "RESPONSE", "RETRY", "DERIVATION", "SUPERSESSION"],
  rootAllowed: false,
  aliases: [],
  compatibility: { acceptedSourceVersions: [schemaVersion], transforms: [] },
};
catalogue.entries.push(
  {
    ...commonEntry,
    typeId: "tekroo.command.escalation.open",
    kind: "COMMAND",
    authorityKinds: ["POLICY"],
    routingMode: "KERNEL_DIRECT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_command_escalation_open_1_3_0",
    emits: ["tekroo.event.escalation.opened"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.command.escalation.resolve",
    kind: "COMMAND",
    authorityKinds: ["ACTOR", "HUMAN", "SERVICE", "POLICY"],
    routingMode: "KERNEL_DIRECT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_command_escalation_resolve_1_3_0",
    emits: ["tekroo.event.escalation.resolved"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.event.escalation.opened",
    kind: "EVENT",
    authorityKinds: [],
    routingMode: "COMMITTED_EVENT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_event_escalation_opened_1_3_0",
    acceptedCommandTypes: ["tekroo.command.escalation.open"],
  },
  {
    ...commonEntry,
    typeId: "tekroo.event.escalation.resolved",
    kind: "EVENT",
    authorityKinds: [],
    routingMode: "COMMITTED_EVENT",
    payloadSchema: "schemas/payloads.schema.json#/$defs/tekroo_event_escalation_resolved_1_3_0",
    acceptedCommandTypes: ["tekroo.command.escalation.resolve"],
  },
);
catalogue.entries.sort((left, right) => left.typeId.localeCompare(right.typeId));
overwriteJson(cataloguePath, catalogue);

const escalationID = "00000000-0000-7000-8000-000000000701";
const subjectID = "00000000-0000-7000-8000-000000000101";
const causalEventID = "00000000-0000-7000-8000-000000000702";
const evidenceID = "00000000-0000-7000-8000-000000000703";
const adjudicator = { kind: "HUMAN", id: "principal-adjudicator" };
const timeoutPolicy = { kind: "POLICY", id: "escalation-timeout-policy" };
const openPayload = {
  escalation_id: escalationID,
  subject_kind: "task",
  subject_id: subjectID,
  subject_lifecycle_epoch: 1,
  expected_subject_revision: 7,
  trigger: "HANDOFF_CYCLE_DETECTED",
  triggering_condition_digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  adjudicator,
  timeout_policy: timeoutPolicy,
  resolution_owner_fqn: "teams::coder-1",
  deadline_at: "2026-08-12T00:00:00Z",
  resolution_round_limit: 1,
  route_limit: 1,
  escalation_policy_revision: 1,
  causal_path_event_ids: [causalEventID],
  unresolved_question: "Which directed successor resolves the detected handoff cycle?",
  evidence_ids: [evidenceID],
};
const resolvePayload = {
  escalation_id: escalationID,
  subject_kind: "task",
  subject_id: subjectID,
  subject_lifecycle_epoch: 1,
  expected_escalation_revision: 1,
  source_role: "ADJUDICATOR",
  round: 1,
  outcome: "RESOLVED",
  reasons: ["A directed successor was selected."],
  evidence_ids: [evidenceID],
  decided_at: "2026-08-11T12:00:00Z",
};

const catalogueFixturesPath = "fixtures/catalogue-coverage.json";
const catalogueFixtures = readJson(`CONTRACTS/${contractIdentity}/${catalogueFixturesPath}`);
catalogueFixtures.fixtures.push(
  {
    classification: "NORMATIVE_EXAMPLE",
    fixtureId: "CAT-028-ESCALATION-OPEN-VALID",
    kind: "CATALOGUE_COMMAND",
    sourceDecisionIds: ["P2-KCF-005", "P2-KCF-007"],
    given: { contractManifest: contractIdentity },
    when: { commandType: "tekroo.command.escalation.open", payload: openPayload },
    then: { expected: { outcomeCode: "APPLIED", eventTypes: ["tekroo.event.escalation.opened"] } },
  },
  {
    classification: "BOUNDARY_NEGATIVE",
    fixtureId: "CAT-028-ESCALATION-OPEN-INVALID",
    kind: "CATALOGUE_COMMAND",
    sourceDecisionIds: ["P2-KCF-005", "P2-KCF-007"],
    given: { contractManifest: contractIdentity },
    when: { commandType: "tekroo.command.escalation.open", payload: { ...openPayload, route_limit: 2 } },
    then: { expected: { outcomeCode: "REJECTED_INVALID", eventTypes: [] } },
  },
  {
    classification: "NORMATIVE_EXAMPLE",
    fixtureId: "CAT-029-ESCALATION-RESOLVE-VALID",
    kind: "CATALOGUE_COMMAND",
    sourceDecisionIds: ["P2-KCF-007"],
    given: { contractManifest: contractIdentity },
    when: { commandType: "tekroo.command.escalation.resolve", payload: resolvePayload },
    then: { expected: { outcomeCode: "APPLIED", eventTypes: ["tekroo.event.escalation.resolved"] } },
  },
  {
    classification: "BOUNDARY_NEGATIVE",
    fixtureId: "CAT-029-ESCALATION-RESOLVE-INVALID",
    kind: "CATALOGUE_COMMAND",
    sourceDecisionIds: ["P2-KCF-007"],
    given: { contractManifest: contractIdentity },
    when: { commandType: "tekroo.command.escalation.resolve", payload: { ...resolvePayload, reasons: [] } },
    then: { expected: { outcomeCode: "REJECTED_INVALID", eventTypes: [] } },
  },
);
catalogueFixtures.fixtures.sort((left, right) => left.fixtureId.localeCompare(right.fixtureId));
overwriteJson(catalogueFixturesPath, catalogueFixtures);

function escalationFixture(fixtureId, classification, given, when, accepted, reason, state) {
  return {
    classification,
    fixtureId,
    kind: "ESCALATION_MODEL",
    sourceDecisionIds: ["P2-KCF-005", "P2-KCF-007"],
    given,
    when,
    then: { expected: { accepted, reason, state } },
  };
}

const escalationBase = {
  state: "OPEN",
  adjudicator,
  timeoutPolicy,
  subjectLifecycleEpoch: 1,
  deadlineAt: "2026-08-12T00:00:00Z",
  resolutionRoundLimit: 1,
};
const adjudicatorResolution = {
  action: "RESOLVE",
  authority: adjudicator,
  sourceRole: "ADJUDICATOR",
  subjectLifecycleEpoch: 1,
  decidedAt: "2026-08-11T12:00:00Z",
  round: 1,
  outcome: "RESOLVED",
};
const modelFixturesPath = "fixtures/model-and-invariant-scenarios.json";
const modelFixtures = readJson(`CONTRACTS/${contractIdentity}/${modelFixturesPath}`);
modelFixtures.fixtures.push(
  escalationFixture("ESCALATION-EXACT-ADJUDICATOR", "NORMATIVE_EXAMPLE", escalationBase, adjudicatorResolution, true, "ACCEPTED", "TERMINAL"),
  escalationFixture("ESCALATION-WRONG-ADJUDICATOR", "BOUNDARY_NEGATIVE", escalationBase, { ...adjudicatorResolution, authority: { kind: "HUMAN", id: "wrong-adjudicator" } }, false, "ADJUDICATOR_MISMATCH", "OPEN"),
  escalationFixture("ESCALATION-STALE-LIFECYCLE", "BOUNDARY_NEGATIVE", escalationBase, { ...adjudicatorResolution, subjectLifecycleEpoch: 2 }, false, "LIFECYCLE_EPOCH_MISMATCH", "OPEN"),
  escalationFixture("ESCALATION-ROUND-EXHAUSTED", "BOUNDARY_NEGATIVE", escalationBase, { ...adjudicatorResolution, round: 2 }, false, "RESOLUTION_BUDGET_EXHAUSTED", "OPEN"),
  escalationFixture("ESCALATION-ADJUDICATOR-DEADLINE", "BOUNDARY_NEGATIVE", escalationBase, { ...adjudicatorResolution, decidedAt: "2026-08-12T00:00:01Z" }, false, "ADJUDICATOR_DEADLINE_EXCEEDED", "OPEN"),
  escalationFixture("ESCALATION-POLICY-TIMEOUT", "NORMATIVE_EXAMPLE", escalationBase, { ...adjudicatorResolution, authority: timeoutPolicy, sourceRole: "POLICY_TIMEOUT", decidedAt: "2026-08-12T00:00:01Z", outcome: "HUMAN_REQUIRED" }, true, "ACCEPTED", "TERMINAL"),
  escalationFixture("ESCALATION-TIMEOUT-BEFORE-DEADLINE", "BOUNDARY_NEGATIVE", escalationBase, { ...adjudicatorResolution, authority: timeoutPolicy, sourceRole: "POLICY_TIMEOUT", outcome: "BLOCKED" }, false, "DEADLINE_NOT_REACHED", "OPEN"),
  escalationFixture("ESCALATION-TIMEOUT-OUTCOME", "BOUNDARY_NEGATIVE", escalationBase, { ...adjudicatorResolution, authority: timeoutPolicy, sourceRole: "POLICY_TIMEOUT", decidedAt: "2026-08-12T00:00:01Z", outcome: "RESOLVED" }, false, "INVALID_TIMEOUT_OUTCOME", "OPEN"),
  escalationFixture("ESCALATION-ROUTE-ONCE", "BOUNDARY_NEGATIVE", escalationBase, { action: "OPEN" }, false, "ESCALATION_ALREADY_OPEN", "OPEN"),
  escalationFixture("ESCALATION-NO-REROUTE", "BOUNDARY_NEGATIVE", escalationBase, { action: "REROUTE" }, false, "REROUTE_PROHIBITED", "OPEN"),
  escalationFixture("ESCALATION-TERMINAL", "BOUNDARY_NEGATIVE", { ...escalationBase, state: "TERMINAL" }, adjudicatorResolution, false, "ESCALATION_TERMINAL", "TERMINAL"),
);
modelFixtures.fixtures.sort((left, right) => left.fixtureId.localeCompare(right.fixtureId));
overwriteJson(modelFixturesPath, modelFixtures);

const fixtureSchemaPath = "schemas/conformance-fixture.schema.json";
const fixtureSchema = readJson(`CONTRACTS/${contractIdentity}/${fixtureSchemaPath}`);
fixtureSchema.properties.kind.enum.push("ESCALATION_MODEL");
fixtureSchema.properties.kind.enum.sort();
overwriteJson(fixtureSchemaPath, fixtureSchema);

const escalationTestIds = [
  "CAT-028-ESCALATION-OPEN-INVALID",
  "CAT-028-ESCALATION-OPEN-VALID",
  "CAT-029-ESCALATION-RESOLVE-INVALID",
  "CAT-029-ESCALATION-RESOLVE-VALID",
  "ESCALATION-ADJUDICATOR-DEADLINE",
  "ESCALATION-EXACT-ADJUDICATOR",
  "ESCALATION-NO-REROUTE",
  "ESCALATION-POLICY-TIMEOUT",
  "ESCALATION-ROUTE-ONCE",
  "ESCALATION-ROUND-EXHAUSTED",
  "ESCALATION-STALE-LIFECYCLE",
  "ESCALATION-TERMINAL",
  "ESCALATION-TIMEOUT-BEFORE-DEADLINE",
  "ESCALATION-TIMEOUT-OUTCOME",
  "ESCALATION-WRONG-ADJUDICATOR",
  "INV-018-EXACT-BOUNDED-ESCALATION",
  "INV-019-ONE-WAY-TERMINAL-ESCALATION",
];
const invariantsPath = "invariants/invariants.json";
const invariants = readJson(`CONTRACTS/${contractIdentity}/${invariantsPath}`);
invariants.invariants.push(
  {
    invariantId: "INV-018-EXACT-BOUNDED-ESCALATION",
    description: "Each escalation binds one exact work lifecycle epoch, accountable resolution owner, designated adjudicator, timeout policy, finite resolution budget, deadline, causal path, unresolved question, and evidence.",
    decisionIds: ["P2-KCF-005", "P2-KCF-007", "P2-KCF-008"],
    negativeRequirementIds: ["NEG-001", "NEG-002", "NEG-004"],
    fixtureIds: ["ESCALATION-EXACT-ADJUDICATOR", "ESCALATION-WRONG-ADJUDICATOR", "ESCALATION-STALE-LIFECYCLE", "ESCALATION-ROUND-EXHAUSTED", "ESCALATION-ADJUDICATOR-DEADLINE", "ESCALATION-POLICY-TIMEOUT"],
  },
  {
    invariantId: "INV-019-ONE-WAY-TERMINAL-ESCALATION",
    description: "An escalation routes once, cannot bounce or reopen, and terminates through the exact adjudicator or exact timeout policy with one stable accepted outcome.",
    decisionIds: ["P2-KCF-005", "P2-KCF-007"],
    negativeRequirementIds: ["NEG-001", "NEG-002", "NEG-004"],
    fixtureIds: ["ESCALATION-ROUTE-ONCE", "ESCALATION-NO-REROUTE", "ESCALATION-TERMINAL", "ESCALATION-TIMEOUT-BEFORE-DEADLINE", "ESCALATION-TIMEOUT-OUTCOME"],
  },
);
invariants.invariants.sort((left, right) => left.invariantId.localeCompare(right.invariantId));
overwriteJson(invariantsPath, invariants);

const traceabilityPath = "traceability/traceability.json";
const traceability = readJson(`CONTRACTS/${contractIdentity}/${traceabilityPath}`);
for (const requirement of traceability.requirements) {
  if (["P2-KCF-005", "P2-KCF-007", "P2-KCF-008", "NEG-001", "NEG-002", "NEG-004"].includes(requirement.requirementId)) {
    requirement.testIds = [...new Set([...requirement.testIds, ...escalationTestIds])].sort();
  }
}
overwriteJson(traceabilityPath, traceability);

const predecessorManifestBytes = fs.readFileSync(path.join(predecessorRoot, "manifest.json"));
writeJson("compatibility/from-0.3.0.json", {
  schemaVersion,
  contractName: "tekroo.kernel.contracts",
  contractVersion,
  predecessor: {
    contractVersion: predecessorVersion,
    contractIdentity: `tekroo.kernel.contracts/${predecessorVersion}`,
    manifestSha256: sha256(predecessorManifestBytes),
  },
  compatibilityClaim: "ADDITIVE_ESCALATION_EXTENSION_WITH_IDENTITY_ADAPTER",
  directions: { backward: "COMPATIBLE_ADAPTER_REQUIRED", forward: "NOT_COMPATIBLE", replay: "VERSION_GATED", adapter: "REQUIRED" },
  unchangedSemanticTypes: predecessorTypeIds,
  additiveTypes: [
    "tekroo.command.escalation.open",
    "tekroo.command.escalation.resolve",
    "tekroo.event.escalation.opened",
    "tekroo.event.escalation.resolved",
  ],
  migrationRules: [
    { source: predecessorSchemaVersion, target: schemaVersion, mode: "IDENTITY", appliesTo: predecessorTypeIds },
    { source: schemaVersion, target: schemaVersion, mode: "IDENTITY" },
  ],
  prohibitedInferences: [
    "Historical work.block and task.handoff records are not escalation records.",
    "No exact adjudicator, timeout policy, resolution owner, budget, deadline, causal path, unresolved question, or terminal outcome may be inferred for legacy history.",
  ],
  rollback: "Readers retain 0.3.0 for historical replay. Escalation records are never down-converted or coerced into work.block or task.handoff records.",
});

let referenceRunner = advanceString(fs.readFileSync(path.join(predecessorRoot, "runner/reference-runner.mjs"), "utf8"));
referenceRunner = referenceRunner.replace(
  "function runSuccessorSetModel(fixture) {",
  `function runEscalationModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  const samePrincipal = (left, right) => left?.kind === right?.kind && left?.id === right?.id;
  if (current.state === "TERMINAL") return { accepted: false, reason: "ESCALATION_TERMINAL", state: "TERMINAL" };
  if (action.action === "OPEN") return { accepted: false, reason: "ESCALATION_ALREADY_OPEN", state: "OPEN" };
  if (action.action === "REROUTE") return { accepted: false, reason: "REROUTE_PROHIBITED", state: "OPEN" };
  if (action.action !== "RESOLVE") return { accepted: false, reason: "INVALID_ACTION", state: "OPEN" };
  if (action.subjectLifecycleEpoch !== current.subjectLifecycleEpoch) return { accepted: false, reason: "LIFECYCLE_EPOCH_MISMATCH", state: "OPEN" };
  if (action.sourceRole === "ADJUDICATOR") {
    if (!samePrincipal(action.authority, current.adjudicator)) return { accepted: false, reason: "ADJUDICATOR_MISMATCH", state: "OPEN" };
    if (action.round > current.resolutionRoundLimit) return { accepted: false, reason: "RESOLUTION_BUDGET_EXHAUSTED", state: "OPEN" };
    if (Date.parse(action.decidedAt) > Date.parse(current.deadlineAt)) return { accepted: false, reason: "ADJUDICATOR_DEADLINE_EXCEEDED", state: "OPEN" };
  } else if (action.sourceRole === "POLICY_TIMEOUT") {
    if (!samePrincipal(action.authority, current.timeoutPolicy)) return { accepted: false, reason: "TIMEOUT_POLICY_MISMATCH", state: "OPEN" };
    if (Date.parse(action.decidedAt) <= Date.parse(current.deadlineAt)) return { accepted: false, reason: "DEADLINE_NOT_REACHED", state: "OPEN" };
    if (!["BLOCKED", "HUMAN_REQUIRED"].includes(action.outcome)) return { accepted: false, reason: "INVALID_TIMEOUT_OUTCOME", state: "OPEN" };
  } else return { accepted: false, reason: "INVALID_SOURCE_ROLE", state: "OPEN" };
  return { accepted: true, reason: "ACCEPTED", state: "TERMINAL" };
}

function runSuccessorSetModel(fixture) {`,
);
referenceRunner = referenceRunner.replace(
  '  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {',
  '  } else if (fixture.kind === "ESCALATION_MODEL") {\n    actual = runEscalationModel(fixture);\n  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {',
);
fs.mkdirSync(path.join(packageRoot, "runner"), { recursive: true });
fs.writeFileSync(path.join(packageRoot, "runner/reference-runner.mjs"), referenceRunner, { flag: "wx" });

let validator = advanceString(fs.readFileSync(path.join(predecessorRoot, "runner/validate-package.mjs"), "utf8"))
  .replace('manifest.contract.version === "0.3.0"', 'manifest.contract.version === "0.4.0"')
  .replaceAll("compatibility/from-0.2.0.json", "compatibility/from-0.3.0.json")
  .replace(
    'compatibility.predecessor?.contractVersion === "0.2.0" && compatibility.contractVersion === "0.3.0" && compatibility.directions?.adapter === "REQUIRED"',
    'compatibility.predecessor?.contractVersion === "0.3.0" && compatibility.contractVersion === "0.4.0" && compatibility.directions?.adapter === "REQUIRED"',
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
  if (relativePath === "runner/validate-package.mjs") return ["catalogue/kernel-catalogue.json", "schemas/payloads.schema.json", "invariants/invariants.json", "traceability/traceability.json", "compatibility/from-0.3.0.json", "runner/protocol.json"];
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
const generatorPath = "scripts/generate_phase3_contract_0_4.mjs";
const manifest = {
  schemaVersion,
  contract: { name: "tekroo.kernel.contracts", version: contractVersion, status: "FROZEN_CANDIDATE" },
  generatedAt: "2026-08-11T00:00:00Z",
  authority: { decisionAuthority: "Principal", implementationAuthority: "NONE", productionAuthority: "NONE" },
  sourceLineage: [
    { path: "OUTPUT/adjudication/negative-requirement-decisions.json", sha256: sha256(read("OUTPUT/adjudication/negative-requirement-decisions.json")) },
    { path: "PHASE-1B/008-final-architecture-handoff.md", sha256: sha256(read("PHASE-1B/008-final-architecture-handoff.md")) },
    { path: "OUTPUT/phase-3/step-9-contract-revision-authorization.json", sha256: sha256(read("OUTPUT/phase-3/step-9-contract-revision-authorization.json")) },
    { path: "OUTPUT/phase-3/step-9-contract-encoding-gaps.md", sha256: sha256(read("OUTPUT/phase-3/step-9-contract-encoding-gaps.md")) },
    { path: "OUTPUT/phase-3/step-8-acceptance.json", sha256: sha256(read("OUTPUT/phase-3/step-8-acceptance.json")) },
    { path: "OUTPUT/phase-3/step-8-release-receipt.json", sha256: sha256(read("OUTPUT/phase-3/step-8-release-receipt.json")) },
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
    { name: "contract-structure", authorization: "PHASE_3_STEP_9_CONTRACT_REVISION", required: true, currentStatus: "READY_TO_RUN" },
    { name: "core-hermetic", authorization: "PHASE_3_STEP_8_FORWARD_REQUALIFICATION", required: true, currentStatus: "REFERENCE_MODEL_ONLY" },
    { name: "mongo-integration", authorization: "FUTURE_IMPLEMENTATION_GATE", required: true, currentStatus: "NOT_RUN" },
    { name: "synthesized-merge", authorization: "FUTURE_MERGE_GATE", required: true, currentStatus: "NOT_RUN" },
    { name: "provider-e2e", authorization: "SEPARATE_PRINCIPAL_AUTHORIZATION", required: false, currentStatus: "NOT_RUN" },
  ],
  files: fileInventory,
  knownLimitations: [
    "No deterministic escalation Go implementation is qualified by this contract package alone.",
    "The accepted Step 8 Go implementation requires forward requalification under 1.3.0 before Step 9 may consume the successor contract.",
    "Legacy work.block and task.handoff records cannot be inferred or silently upgraded into escalation records.",
    "Mongo integration, synthesized merge, OpenHands, SMA, providers, deployment, migration execution, and production profiles are not run by the contract gate.",
    "Performance and resource budgets require later measured workload evidence.",
  ],
};
fs.writeFileSync(manifestPath, pretty(manifest), { flag: "wx" });
const manifestDigest = sha256(fs.readFileSync(manifestPath));
fs.writeFileSync(checksumPath, `${manifestDigest}  manifest.json\n`, { flag: "wx" });
process.stdout.write(`GENERATED ${contractIdentity} ${manifestDigest} ${fileInventory.length}\n`);
