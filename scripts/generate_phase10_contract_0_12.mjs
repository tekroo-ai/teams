#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.11.0");
const target = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.12.0");
const oldIdentity = "tekroo.kernel.contracts/0.11.0";
const newIdentity = "tekroo.kernel.contracts/0.12.0";
const sha256 = (value) => crypto.createHash("sha256").update(value).digest("hex");
const sortValue = (value) => Array.isArray(value) ? value.map(sortValue) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortValue(value[key])])) : value;
const canonical = (value) => JSON.stringify(sortValue(value));
const digest = { type: "string", pattern: "^[0-9a-f]{64}$" };
const messageType = { type: "string", pattern: "^tekroo\\.message\\.[a-z0-9]+(?:[.-][a-z0-9]+)*$", maxLength: 256 };
const handlerDispatchBinding = {
  type: "object", additionalProperties: false,
  required: ["message_id", "message_type", "message_purpose", "message_body_digest", "subscription_purpose", "role_bundle_digest", "charter_digest", "handler_digest", "input_schema_digest", "result_schema_digest", "allowed_results", "allowed_message_proposals"],
  properties: {
    message_id: { type: "string", format: "uuid" },
    message_type: messageType,
    message_purpose: { enum: ["REQUEST", "HANDOFF", "EVIDENCE", "RESPONSE", "NOTIFICATION"] },
    message_body_digest: digest,
    subscription_purpose: { type: "string", minLength: 1, maxLength: 128 },
    role_bundle_digest: digest,
    charter_digest: digest,
    handler_digest: digest,
    input_schema_digest: digest,
    result_schema_digest: digest,
    allowed_results: { type: "array", minItems: 1, maxItems: 16, uniqueItems: true, items: { enum: ["blocked", "completed", "failed", "needs_decision"] } },
    allowed_message_proposals: { type: "array", maxItems: 64, uniqueItems: true, items: messageType }
  }
};

function writeJson(relative, value) {
  const absolute = path.join(target, relative);
  fs.mkdirSync(path.dirname(absolute), { recursive: true });
  fs.writeFileSync(absolute, `${JSON.stringify(sortValue(value), null, 2)}\n`);
}

function readJson(relative) {
  return JSON.parse(fs.readFileSync(path.join(target, relative), "utf8"));
}

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

fs.rmSync(target, { recursive: true, force: true });
fs.cpSync(source, target, { recursive: true });
for (const relative of walk(target)) {
  if (["manifest.json", "manifest.sha256"].includes(relative)) continue;
  const absolute = path.join(target, relative);
  const raw = fs.readFileSync(absolute, "utf8");
  fs.writeFileSync(absolute, raw.split(oldIdentity).join(newIdentity));
}
fs.rmSync(path.join(target, "manifest.json"), { force: true });
fs.rmSync(path.join(target, "manifest.sha256"), { force: true });

const resource = {
  type: "object", additionalProperties: false, required: ["path", "sha256", "media_type"],
  properties: {
    path: { type: "string", minLength: 1, maxLength: 1024, pattern: "^(?!/)(?!.*(?:^|/)\\.\\.(?:/|$))(?!\\.$).+$" },
    sha256: digest,
    media_type: { enum: ["text/markdown", "application/schema+json"] }
  }
};
const handler = {
  type: "object", additionalProperties: false,
  required: ["subscription_purpose", "message_purpose", "disposition", "allowed_results", "allowed_message_proposals"],
  properties: {
    subscription_purpose: { type: "string", minLength: 1, maxLength: 128 },
    message_purpose: { enum: ["REQUEST", "HANDOFF", "EVIDENCE", "RESPONSE", "NOTIFICATION"] },
    disposition: { enum: ["MODEL_HANDLER", "DETERMINISTIC_HANDLER", "OBSERVE_ONLY"] },
    resource: { $ref: "#/$defs/roleResource" },
    input_schema: { $ref: "#/$defs/roleResource" },
    result_schema: { $ref: "#/$defs/roleResource" },
    deterministic_operation: { type: "string", minLength: 1, maxLength: 256 },
    allowed_results: { type: "array", minItems: 1, maxItems: 16, uniqueItems: true, items: { enum: ["blocked", "completed", "failed", "needs_decision"] } },
    allowed_message_proposals: { type: "array", maxItems: 64, uniqueItems: true, items: messageType }
  },
  allOf: [
    { if: { properties: { disposition: { const: "MODEL_HANDLER" } }, required: ["disposition"] }, then: { required: ["resource", "input_schema", "result_schema"], not: { required: ["deterministic_operation"] } } },
    { if: { properties: { disposition: { const: "DETERMINISTIC_HANDLER" } }, required: ["disposition"] }, then: { required: ["input_schema", "result_schema", "deterministic_operation"], not: { required: ["resource"] } } },
    { if: { properties: { disposition: { const: "OBSERVE_ONLY" } }, required: ["disposition"] }, then: { not: { anyOf: [{ required: ["resource"] }, { required: ["input_schema"] }, { required: ["result_schema"] }, { required: ["deterministic_operation"] }] }, properties: { allowed_message_proposals: { maxItems: 0 } } } }
  ]
};
const rolePackage = {
  type: "object", additionalProperties: false,
  required: ["schema_version", "role", "version", "capabilities", "subscriptions", "permissions", "charter", "handler_bindings", "publisher_key_id", "signature"],
  properties: {
    schema_version: { const: "2.0.0" },
    role: { type: "string", minLength: 1, maxLength: 64, pattern: "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])$" },
    version: { type: "string", pattern: "^[0-9]+\\.[0-9]+\\.[0-9]+$" },
    capabilities: { type: "array", minItems: 1, maxItems: 128, uniqueItems: true, items: { type: "string", minLength: 1, maxLength: 256 } },
    subscriptions: { type: "array", maxItems: 256, items: { $ref: "#/$defs/subscription" } },
    permissions: { type: "array", minItems: 1, maxItems: 128, uniqueItems: true, items: { type: "string", minLength: 1, maxLength: 256 } },
    charter: { $ref: "#/$defs/roleResource" },
    handler_bindings: { type: "object", minProperties: 1, maxProperties: 256, propertyNames: messageType, additionalProperties: { $ref: "#/$defs/roleHandler" } },
    publisher_key_id: { type: "string", minLength: 1, maxLength: 256 },
    signature: { type: "string", minLength: 1, maxLength: 4096 }
  }
};

const organizationSchema = readJson("schemas/organization-runtime.schema.json");
organizationSchema.$defs.roleBundleV1 = organizationSchema.$defs.roleBundle;
organizationSchema.$defs.roleResource = resource;
organizationSchema.$defs.roleHandler = handler;
organizationSchema.$defs.roleBundleV2 = rolePackage;
organizationSchema.$defs.roleBundle = { oneOf: [{ $ref: "#/$defs/roleBundleV1" }, { $ref: "#/$defs/roleBundleV2" }] };
writeJson("schemas/organization-runtime.schema.json", organizationSchema);

const workInvocationSchema = readJson("schemas/work-invocation.schema.json");
workInvocationSchema.$defs = { ...(workInvocationSchema.$defs ?? {}), handlerDispatchBinding };
workInvocationSchema.properties.handler_dispatch = { $ref: "#/$defs/handlerDispatchBinding" };
writeJson("schemas/work-invocation.schema.json", workInvocationSchema);

const payloadsSchema = readJson("schemas/payloads.schema.json");
payloadsSchema.$defs.handler_dispatch_binding_2_0_0 = handlerDispatchBinding;
for (const name of ["tekroo_command_work_invocation_authorize_1_7_0", "tekroo_event_work_invocation_authorized_1_7_0"]) {
  payloadsSchema.$defs[name].properties.handler_dispatch = { $ref: "#/$defs/handler_dispatch_binding_2_0_0" };
}
writeJson("schemas/payloads.schema.json", payloadsSchema);

const catalogueCoverage = readJson("fixtures/catalogue-coverage.json");
const authorizationFixture = catalogueCoverage.fixtures.find((item) => item.when?.commandType === "tekroo.command.work-invocation.authorize" && item.classification === "NORMATIVE_EXAMPLE");
if (!authorizationFixture) throw new Error("work-invocation authorization fixture is missing");
authorizationFixture.when.payload.handler_dispatch = {
  message_id: "00000000-0000-7000-8000-000000000920",
  message_type: "tekroo.message.task.assigned",
  message_purpose: "HANDOFF",
  message_body_digest: "5555555555555555555555555555555555555555555555555555555555555555",
  subscription_purpose: "implementation",
  role_bundle_digest: "6666666666666666666666666666666666666666666666666666666666666666",
  charter_digest: "7777777777777777777777777777777777777777777777777777777777777777",
  handler_digest: "8888888888888888888888888888888888888888888888888888888888888888",
  input_schema_digest: "9999999999999999999999999999999999999999999999999999999999999999",
  result_schema_digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  allowed_results: ["completed", "failed", "blocked", "needs_decision"],
  allowed_message_proposals: []
};
writeJson("fixtures/catalogue-coverage.json", catalogueCoverage);

const fixtures = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  fixtures: [
    { fixtureId: "P10-RH-001", kind: "ROLE_HANDLER_MODEL", model: "PER_ROLE_DISPATCH", given: { messageType: "tekroo.message.review.requested", bindings: [{ role: "architect", handler: "architecture-review" }, { role: "coder", handler: "implementation-review" }] }, then: { expected: { distinctHandlers: true } } },
    { fixtureId: "P10-RH-002", kind: "ROLE_HANDLER_MODEL", model: "UNMAPPED_MESSAGE", given: { mapped: false, modelCalls: 0 }, then: { expected: { admitted: false, modelCalls: 0 } } },
    { fixtureId: "P10-RH-003", kind: "ROLE_HANDLER_MODEL", model: "MUTATED_RESOURCE", given: { declaredDigest: "a", observedDigest: "b", modelCalls: 0 }, then: { expected: { eligible: false, modelCalls: 0 } } },
    { fixtureId: "P10-RH-004", kind: "ROLE_HANDLER_MODEL", model: "IDLE_TIMEOUT", given: { messages: 0, timeout: true }, then: { expected: { messagesWritten: 0, handlersRun: 0, modelCalls: 0 } } },
    { fixtureId: "P10-RH-005", kind: "ROLE_HANDLER_MODEL", model: "MESSAGE_SUBSTITUTION", given: { currentNode: "implementation", declaredSuccessors: ["validation"], proposedNode: "design", changedMessageType: true }, then: { expected: { admitted: false, reason: "UNDECLARED_TRANSITION" } } },
    { fixtureId: "P10-RH-006", kind: "ROLE_HANDLER_MODEL", model: "COMPACTION_REBIND", given: { charterDigest: "a", handlerDigest: "b" }, then: { expected: { resumedCharterDigest: "a", resumedHandlerDigest: "b" } } },
    { fixtureId: "P10-RH-007", kind: "ROLE_HANDLER_MODEL", model: "AMBIGUOUS_HANDLER", given: { matchingHandlers: 2, modelCalls: 0 }, then: { expected: { eligible: false, modelCalls: 0 } } },
    { fixtureId: "P10-RH-008", kind: "ROLE_HANDLER_MODEL", model: "PROHIBITED_MESSAGE_PROPOSAL", given: { allowed: ["tekroo.message.task.completed"], proposed: "tekroo.message.story.design-requested" }, then: { expected: { accepted: false, reason: "UNAUTHORIZED_MESSAGE_PROPOSAL" } } },
    { fixtureId: "P10-RH-009", kind: "ROLE_HANDLER_MODEL", model: "DUPLICATE_DELIVERY", given: { deliveries: 2, messageId: "same" }, then: { expected: { admissions: 1, invocations: 1 } } },
    { fixtureId: "P10-RH-010", kind: "ROLE_HANDLER_MODEL", model: "LEGACY_STRING_HANDLERS", given: { schemaVersion: "1.0.0", handler: "implement-task" }, then: { expected: { acceptedThroughCompatibilityAdapter: true } } }
  ]
};
const invariants = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  invariants: [
    { invariantId: "P10-RH-INV-001", statement: "Each subscribed message resolves to exactly one content-addressed per-role disposition before model execution.", testIds: ["P10-RH-001", "P10-RH-002", "P10-RH-003", "P10-RH-007"] },
    { invariantId: "P10-RH-INV-002", statement: "Role and handler resources remain bound across execution and compaction.", testIds: ["P10-RH-003", "P10-RH-006"] },
    { invariantId: "P10-RH-INV-003", statement: "Handler output proposes only transitions declared by the current acyclic workflow node.", testIds: ["P10-RH-005"] },
    { invariantId: "P10-RH-INV-004", statement: "An idle change-stream timeout creates no organizational work and invokes no model.", testIds: ["P10-RH-004"] },
    { invariantId: "P10-RH-INV-005", statement: "Changing message type, role, actor, alias, or thread cannot bypass node identity or root authority.", testIds: ["P10-RH-005", "P10-RH-008"] },
    { invariantId: "P10-RH-INV-006", statement: "Duplicate delivery creates at most one admission and one invocation, and legacy string handlers remain readable only through the bounded compatibility path.", testIds: ["P10-RH-009", "P10-RH-010"] }
  ]
};
const traceability = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  requirements: invariants.invariants.map((item) => ({ requirementId: item.invariantId.replace("INV", "REQ"), kind: "DECISION", statement: item.statement, testIds: item.testIds }))
};
writeJson("fixtures/role-handler-runtime.json", fixtures);
writeJson("invariants/role-handler-invariants.json", invariants);
writeJson("traceability/role-handler-traceability.json", traceability);
const rootTraceability = readJson("traceability/traceability.json");
rootTraceability.requirements.push(...traceability.requirements);
writeJson("traceability/traceability.json", rootTraceability);
writeJson("compatibility/from-0.11.0.json", {
  schemaVersion: "1.0.0", contractName: "tekroo.kernel.contracts", contractVersion: "0.12.0",
  predecessor: { contractName: "tekroo.kernel.contracts", contractVersion: "0.11.0", manifestSha256: sha256(fs.readFileSync(path.join(source, "manifest.json"))) },
  compatibility: "ADDITIVE",
  directions: { reader: "COMPATIBLE", writer: "SUCCESSOR_IDENTITY_REQUIRED", adapter: "ROLE_BUNDLE_V1_COMPATIBILITY_ADAPTER_REQUIRED" },
  additions: ["content-addressed role charters", "per-role message handler bindings", "handler result authority", "idle timeout without organizational work"],
  preserved: ["all 0.11.0 workflow, kernel, organization, federation, task, story, budget, execution, OpenHands, and SMA boundaries"]
});

const validatorPath = path.join(target, "runner/validate-package.mjs");
let validator = fs.readFileSync(validatorPath, "utf8");
validator = validator.replace('manifest.contract.version === "0.11.0"', 'manifest.contract.version === "0.12.0"');
validator = validator.replace('const compatibility = readJson("compatibility/from-0.10.0.json");', 'const compatibility = readJson("compatibility/from-0.11.0.json");');
validator = validator.replace('compatibility.predecessor?.contractVersion === "0.10.0" && compatibility.contractVersion === "0.11.0"', 'compatibility.predecessor?.contractVersion === "0.11.0" && compatibility.contractVersion === "0.12.0"');
validator = validator.replace('check("decision-trace-count", decisions.length === 57, decisions.length);', 'check("decision-trace-count", decisions.length === 63, decisions.length);');
validator = validator.replace('check("phase10-workflow-schemas"', `
const roleHandlerFixtures = readJson("fixtures/role-handler-runtime.json");
const roleHandlerInvariants = readJson("invariants/role-handler-invariants.json");
const roleHandlerTraceability = readJson("traceability/role-handler-traceability.json");
const roleHandlerTestIds = new Set([...roleHandlerFixtures.fixtures.map((item) => item.fixtureId), ...roleHandlerInvariants.invariants.map((item) => item.invariantId)]);
check("phase10-role-handler-schema", Boolean(readJson("schemas/organization-runtime.schema.json").$defs.roleBundleV2));
check("phase10-role-handler-fixtures", roleHandlerFixtures.fixtures.length === 10);
check("phase10-role-handler-invariants", roleHandlerInvariants.invariants.length === 6);
check("phase10-role-handler-traceability", roleHandlerTraceability.requirements.every((item) => item.testIds.length > 0 && item.testIds.every((id) => roleHandlerTestIds.has(id))));
check("phase10-workflow-schemas"`);
fs.writeFileSync(validatorPath, validator);

const referenceRunnerPath = path.join(target, "runner/reference-runner.mjs");
let referenceRunner = fs.readFileSync(referenceRunnerPath, "utf8");
referenceRunner = referenceRunner.replace('function validateType(value, rule) {', `let payloadDefinitions = {};

function validateType(value, rule) {
  if (rule.$ref !== undefined) {
    const prefix = "#/$defs/";
    if (!rule.$ref.startsWith(prefix)) return false;
    const resolved = payloadDefinitions[rule.$ref.slice(prefix.length)];
    return resolved !== undefined && validateType(value, resolved);
  }
  if (rule.enum !== undefined) return rule.enum.includes(value);`);
referenceRunner = referenceRunner.replace('const payloadSchemas = readJson("schemas/payloads.schema.json");', 'const payloadSchemas = readJson("schemas/payloads.schema.json");\npayloadDefinitions = payloadSchemas.$defs;');
referenceRunner = referenceRunner.replace('const manifest = readJson("manifest.json");', `function runRoleHandlerModel(fixture) {
  const current = fixture.given;
  if (fixture.model === "PER_ROLE_DISPATCH") return { distinctHandlers: new Set(current.bindings.map((item) => item.handler)).size === current.bindings.length };
  if (fixture.model === "UNMAPPED_MESSAGE") return { admitted: current.mapped, modelCalls: current.mapped ? current.modelCalls + 1 : current.modelCalls };
  if (fixture.model === "MUTATED_RESOURCE") return { eligible: current.declaredDigest === current.observedDigest, modelCalls: current.modelCalls };
  if (fixture.model === "IDLE_TIMEOUT") return { messagesWritten: current.messages, handlersRun: 0, modelCalls: 0 };
  if (fixture.model === "MESSAGE_SUBSTITUTION") return current.declaredSuccessors.includes(current.proposedNode) ? { admitted: true, reason: null } : { admitted: false, reason: "UNDECLARED_TRANSITION" };
  if (fixture.model === "COMPACTION_REBIND") return { resumedCharterDigest: current.charterDigest, resumedHandlerDigest: current.handlerDigest };
  if (fixture.model === "AMBIGUOUS_HANDLER") return { eligible: current.matchingHandlers === 1, modelCalls: current.modelCalls };
  if (fixture.model === "PROHIBITED_MESSAGE_PROPOSAL") return current.allowed.includes(current.proposed) ? { accepted: true, reason: null } : { accepted: false, reason: "UNAUTHORIZED_MESSAGE_PROPOSAL" };
  if (fixture.model === "DUPLICATE_DELIVERY") return { admissions: current.messageId === "same" ? 1 : current.deliveries, invocations: current.messageId === "same" ? 1 : current.deliveries };
  if (fixture.model === "LEGACY_STRING_HANDLERS") return { acceptedThroughCompatibilityAdapter: current.schemaVersion === "1.0.0" && typeof current.handler === "string" && current.handler.length > 0 };
  return { unsupportedRoleHandlerModel: fixture.model };
}

const manifest = readJson("manifest.json");`);
referenceRunner = referenceRunner.replace('} else if (fixture.kind === "WORKFLOW_MODEL") {', `} else if (fixture.kind === "ROLE_HANDLER_MODEL") {
    actual = runRoleHandlerModel(fixture);
  } else if (fixture.kind === "WORKFLOW_MODEL") {`);
fs.writeFileSync(referenceRunnerPath, referenceRunner);

const sourceManifest = JSON.parse(fs.readFileSync(path.join(source, "manifest.json"), "utf8"));
const sourceByPath = new Map(sourceManifest.files.map((entry) => [entry.path, entry]));
const roles = new Map([
  ["fixtures/role-handler-runtime.json", "fixtures"],
  ["invariants/role-handler-invariants.json", "role-handler-invariants"],
  ["traceability/role-handler-traceability.json", "role-handler-traceability"],
  ["compatibility/from-0.11.0.json", "compatibility"]
]);
const dependencies = new Map([
  ["fixtures/role-handler-runtime.json", ["schemas/organization-runtime.schema.json"]],
  ["invariants/role-handler-invariants.json", ["fixtures/role-handler-runtime.json"]],
  ["traceability/role-handler-traceability.json", ["fixtures/role-handler-runtime.json", "invariants/role-handler-invariants.json"]],
  ["compatibility/from-0.11.0.json", ["CONTRACTS/tekroo.kernel.contracts/0.11.0/manifest.json"]]
]);
const paths = walk(target).filter((value) => !["manifest.json", "manifest.sha256"].includes(value));
const files = paths.map((relative) => {
  const bytes = fs.readFileSync(path.join(target, relative));
  const json = relative.endsWith(".json") ? JSON.parse(bytes.toString("utf8")) : null;
  const sourceEntry = sourceByPath.get(relative);
  return { path: relative, role: roles.get(relative) ?? sourceEntry?.role ?? "contract-asset", mediaType: json ? "application/json" : sourceEntry?.mediaType ?? "text/javascript", bytes: bytes.length, sha256: sha256(bytes), canonicalJsonSha256: json ? sha256(canonical(json)) : null, dependencies: dependencies.get(relative) ?? sourceEntry?.dependencies ?? [] };
});
const count = (directory, property) => paths.filter((value) => value.startsWith(`${directory}/`) && value.endsWith(".json")).reduce((sum, value) => sum + (readJson(value)[property]?.length ?? 0), 0);
const catalogue = readJson("catalogue/kernel-catalogue.json");
const manifest = {
  ...sourceManifest,
  contract: { name: "tekroo.kernel.contracts", version: "0.12.0", status: "FROZEN_CANDIDATE" },
  generatedAt: "2026-09-13T00:00:00Z",
  files,
  counts: {
    catalogueEntries: catalogue.entries.length,
    commandTypes: catalogue.entries.filter((entry) => entry.kind === "COMMAND").length,
    eventTypes: catalogue.entries.filter((entry) => entry.kind === "EVENT").length,
    fixtures: count("fixtures", "fixtures"), invariants: count("invariants", "invariants"),
    schemas: paths.filter((value) => value.startsWith("schemas/") && value.endsWith(".json")).length,
    traceabilityRequirements: count("traceability", "requirements")
  },
  knownLimitations: ["Contract structure does not by itself qualify the Go implementation or a deployment.", "V3 data migration and unrestricted agent-to-agent conversation are not authorized."],
  sourceLineage: [
    { path: "CONTRACTS/tekroo.kernel.contracts/0.11.0/manifest.json", sha256: sha256(fs.readFileSync(path.join(source, "manifest.json"))) },
    { path: "docs/architecture/131-phase-10-message-handler-runtime-plan.md", sha256: sha256(fs.readFileSync(path.join(root, "docs/architecture/131-phase-10-message-handler-runtime-plan.md"))) },
    { path: "scripts/generate_phase10_contract_0_12.mjs", sha256: sha256(fs.readFileSync(fileURLToPath(import.meta.url))) }
  ]
};
writeJson("manifest.json", manifest);
const manifestBytes = fs.readFileSync(path.join(target, "manifest.json"));
fs.writeFileSync(path.join(target, "manifest.sha256"), `${sha256(manifestBytes)}  manifest.json\n`);
process.stdout.write(`${newIdentity} ${sha256(manifestBytes)} ${files.length}\n`);
