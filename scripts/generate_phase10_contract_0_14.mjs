#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.13.0");
const target = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.14.0");
if (fs.existsSync(target)) {
  if (process.argv[2] !== "--refresh-candidate" || JSON.parse(fs.readFileSync(path.join(target, "manifest.json"), "utf8")).contract.status !== "IMPLEMENTED_CANDIDATE") {
    throw new Error("0.14.0 exists; only an unaccepted candidate may be refreshed");
  }
  fs.rmSync(target, { recursive: true });
}
const sha = (value) => crypto.createHash("sha256").update(value).digest("hex");
const sort = (value) => Array.isArray(value) ? value.map(sort) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map((key) => [key, sort(value[key])])) : value;
const canonical = (value) => JSON.stringify(sort(value));
const read = (relative) => JSON.parse(fs.readFileSync(path.join(target, relative), "utf8"));
const write = (relative, value) => {
  const absolute = path.join(target, relative);
  fs.mkdirSync(path.dirname(absolute), { recursive: true });
  fs.writeFileSync(absolute, `${JSON.stringify(sort(value), null, 2)}\n`);
};
const walk = (directory, prefix = "") => fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
  const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
  return entry.isDirectory() ? walk(path.join(directory, entry.name), relative) : [relative];
}).sort();

fs.cpSync(source, target, { recursive: true });
for (const relative of walk(target)) {
  if (relative === "manifest.json" || relative === "manifest.sha256") continue;
  const absolute = path.join(target, relative);
  fs.writeFileSync(absolute, fs.readFileSync(absolute, "utf8").replaceAll("tekroo.kernel.contracts/0.13.0", "tekroo.kernel.contracts/0.14.0"));
}
const definitionSchema = read("schemas/workflow-definition.schema.json");
definitionSchema.properties.stages.items.properties.optional = { type: "boolean" };
write("schemas/workflow-definition.schema.json", definitionSchema);
const instanceSchema = read("schemas/workflow-instance.schema.json");
instanceSchema.properties.nodes.items.properties.state.enum.push("SKIPPED");
instanceSchema.properties.nodes.items.allOf = [{
  if: { properties: { state: { const: "SKIPPED" } }, required: ["state"] },
  then: {
    properties: { attempt: { const: 0 }, failure_classification: { const: "NONE" }, output_evidence_ids: { maxItems: 0 } },
    not: { anyOf: ["actor_fqn", "invocation_id", "progress_digest", "checkpoint_id", "continuation_node_id", "blocked_from"].map((field) => ({ required: [field] })) }
  }
}];
write("schemas/workflow-instance.schema.json", instanceSchema);
write("schemas/feature-design-revision.schema.json", {
  $id: "tekroo.kernel.contracts/0.14.0/schemas/feature-design-revision.schema.json",
  $schema: "https://json-schema.org/draft/2020-12/schema", title: "Single-use PM design revision receipt",
  type: "object", additionalProperties: false,
  required: ["prior_design_digest", "decision_digest", "reason", "requested_at"],
  properties: {
    prior_design_digest: { type: "string", pattern: "^[0-9a-f]{64}$" },
    decision_digest: { type: "string", pattern: "^[0-9a-f]{64}$" },
    reason: { type: "string", minLength: 1, maxLength: 4096 },
    requested_at: { type: "string", format: "date-time" }
  }
});

const workflowFixtures = read("fixtures/workflow-runtime.json");
workflowFixtures.fixtures.push(...[
  { fixtureId: "P14-REV-001", scenario: "FIRST_PM_PLAN_SKIPS_REVISION", given: { firstPmOutcome: "completed", revisionCount: 0 }, when: {}, then: { expected: { revisionAdmitted: false, skippedNodes: 2, blocked: false } } },
  { fixtureId: "P14-REV-002", scenario: "FIRST_PM_DECISION_FORWARDS_ONCE", given: { firstPmOutcome: "needs_decision", revisionCount: 0 }, when: {}, then: { expected: { revisionAdmitted: true, skippedNodes: 0, blocked: false } } },
  { fixtureId: "P14-REV-003", scenario: "SECOND_PM_DECISION_STOPS", given: { firstPmOutcome: "needs_decision", revisionCount: 1 }, when: {}, then: { expected: { revisionAdmitted: false, skippedNodes: 0, blocked: true } } },
  { fixtureId: "P14-REV-004", scenario: "INSUFFICIENT_HOPS_STOPS", given: { firstPmOutcome: "needs_decision", revisionCount: 0, remainingHops: 1 }, when: {}, then: { expected: { revisionAdmitted: false, skippedNodes: 0, blocked: true } } }
].map((item) => ({ classification: "NORMATIVE_EXAMPLE", kind: "WORKFLOW_MODEL", model: "DESIGN_REVISION_BRANCH", ...item })));
write("fixtures/workflow-runtime.json", workflowFixtures);
const workflowInvariants = read("invariants/workflow-invariants.json");
workflowInvariants.invariants.push({ invariantId: "P14-REV-INV-001", statement: "One PM decision may create exactly one forward architect revision when the hop budget permits; a second decision or insufficient budget blocks.", testIds: ["P14-REV-002", "P14-REV-003", "P14-REV-004"] });
workflowInvariants.invariants.push({ invariantId: "P14-REV-INV-002", statement: "A successful first PM plan skips untouched optional revision nodes and completes the workflow.", testIds: ["P14-REV-001"] });
write("invariants/workflow-invariants.json", workflowInvariants);
const workflowTrace = read("traceability/workflow-traceability.json");
const revisionRequirements = workflowInvariants.invariants.filter((item) => item.invariantId.startsWith("P14-")).map((item) => ({ requirementId: item.invariantId.replace("INV", "REQ"), kind: "DECISION", statement: item.statement, testIds: item.testIds }));
workflowTrace.requirements.push(...revisionRequirements);
write("traceability/workflow-traceability.json", workflowTrace);
const traceability = read("traceability/traceability.json");
traceability.requirements.push(...revisionRequirements);
write("traceability/traceability.json", traceability);
write("compatibility/from-0.13.0.json", {
  schemaVersion: "1.0.0", contractName: "tekroo.kernel.contracts", contractVersion: "0.14.0",
  predecessor: { contractName: "tekroo.kernel.contracts", contractVersion: "0.13.0", manifestSha256: sha(fs.readFileSync(path.join(source, "manifest.json"))) },
  compatibility: "ADDITIVE_OPT_IN", directions: { reader: "COMPATIBLE", writer: "WORKFLOW_2_1_0_REQUIRED", adapter: "WORKFLOW_2_0_0_DECISION_BLOCK_PRESERVED" },
  additions: ["optional workflow nodes and durable SKIP transition", "one PM-requested architect revision with immutable decision receipt", "terminal block on a second PM decision"],
  preserved: ["0.13.0 workflow and role-handler semantics for already-admitted instances", "kernel event and command identities", "SMA boundary"]
});

const validatorPath = path.join(target, "runner/validate-package.mjs");
let validator = fs.readFileSync(validatorPath, "utf8");
validator = validator.replace('manifest.contract.version === "0.13.0"', 'manifest.contract.version === "0.14.0"');
validator = validator.replace('const compatibility = readJson("compatibility/from-0.12.0.json");', 'const compatibility = readJson("compatibility/from-0.13.0.json");');
validator = validator.replace('compatibility.predecessor?.contractVersion === "0.12.0" && compatibility.contractVersion === "0.13.0"', 'compatibility.predecessor?.contractVersion === "0.13.0" && compatibility.contractVersion === "0.14.0"');
validator = validator.replace('decisions.length === 63', 'decisions.length === 65');
validator = validator.replace('workflowFixtures.fixtures.length === 8', 'workflowFixtures.fixtures.length === 12');
validator = validator.replace('workflowInvariants.invariants.length === 8', 'workflowInvariants.invariants.length === 10');
validator = validator.replace('const workflowFixtures = readJson("fixtures/workflow-runtime.json");', `const revisionSchema = readJson("schemas/feature-design-revision.schema.json");
const workflowDefinitionSchema = readJson("schemas/workflow-definition.schema.json");
const workflowInstanceSchema = readJson("schemas/workflow-instance.schema.json");
check("phase14-revision-schema", revisionSchema.required.includes("decision_digest") && revisionSchema.properties.reason.maxLength === 4096);
check("phase14-optional-node-schema", workflowDefinitionSchema.properties.stages.items.properties.optional.type === "boolean" && workflowInstanceSchema.properties.nodes.items.properties.state.enum.includes("SKIPPED"));
const workflowFixtures = readJson("fixtures/workflow-runtime.json");`);
fs.writeFileSync(validatorPath, validator);
const runnerPath = path.join(target, "runner/reference-runner.mjs");
let runner = fs.readFileSync(runnerPath, "utf8");
runner = runner.replace('if (fixture.model === "PARALLEL_READY") {', `if (fixture.model === "DESIGN_REVISION_BRANCH") {
    const decision = current.firstPmOutcome === "needs_decision";
    const admitted = decision && current.revisionCount === 0 && (current.remainingHops ?? 2) >= 2;
    return { revisionAdmitted: admitted, skippedNodes: decision ? 0 : 2, blocked: decision && !admitted };
  }
  if (fixture.model === "PARALLEL_READY") {`);
fs.writeFileSync(runnerPath, runner);

const sourceManifest = JSON.parse(fs.readFileSync(path.join(source, "manifest.json"), "utf8"));
const oldFiles = new Map(sourceManifest.files.map((item) => [item.path, item]));
const roles = new Map([["schemas/feature-design-revision.schema.json", "schema"], ["compatibility/from-0.13.0.json", "compatibility"]]);
const paths = walk(target).filter((item) => item !== "manifest.json" && item !== "manifest.sha256");
const files = paths.map((relative) => {
  const bytes = fs.readFileSync(path.join(target, relative));
  const json = relative.endsWith(".json") ? JSON.parse(bytes.toString("utf8")) : null;
  const old = oldFiles.get(relative);
  return { path: relative, role: roles.get(relative) ?? old?.role ?? "contract-asset", mediaType: json ? "application/json" : old?.mediaType ?? "text/javascript", bytes: bytes.length, sha256: sha(bytes), canonicalJsonSha256: json ? sha(canonical(json)) : null, dependencies: old?.dependencies ?? [] };
});
const count = (directory, field) => paths.filter((item) => item.startsWith(`${directory}/`) && item.endsWith(".json")).reduce((sum, item) => sum + (read(item)[field]?.length ?? 0), 0);
const catalogue = read("catalogue/kernel-catalogue.json");
write("manifest.json", {
  ...sourceManifest, contract: { name: "tekroo.kernel.contracts", version: "0.14.0", status: "IMPLEMENTED_CANDIDATE" },
  authority: { decisionAuthority: "Principal", implementationAuthority: "AUTHORIZED", productionAuthority: "NONE" },
  generatedAt: "2026-10-02T00:00:00Z", files,
  counts: { catalogueEntries: catalogue.entries.length, commandTypes: catalogue.entries.filter((item) => item.kind === "COMMAND").length, eventTypes: catalogue.entries.filter((item) => item.kind === "EVENT").length, fixtures: count("fixtures", "fixtures"), invariants: count("invariants", "invariants"), schemas: paths.filter((item) => item.startsWith("schemas/") && item.endsWith(".json")).length, traceabilityRequirements: count("traceability", "requirements") },
  knownLimitations: ["Package structure does not qualify a production deployment.", "Switching an active feature between workflow trigger versions is unsupported; isolate or drain before activation.", "At most one PM-requested architect revision is allowed; a second decision blocks for operator resolution."],
  sourceLineage: [
    { path: "CONTRACTS/tekroo.kernel.contracts/0.13.0/manifest.json", sha256: sha(fs.readFileSync(path.join(source, "manifest.json"))) },
    { path: "docs/architecture/140-single-use-pm-design-revision-014.md", sha256: sha(fs.readFileSync(path.join(root, "docs/architecture/140-single-use-pm-design-revision-014.md"))) },
    { path: "config/workflows/software-development.v2.1.json", sha256: sha(fs.readFileSync(path.join(root, "config/workflows/software-development.v2.1.json"))) },
    { path: "scripts/generate_phase10_contract_0_14.mjs", sha256: sha(fs.readFileSync(fileURLToPath(import.meta.url))) }
  ]
});
const manifestBytes = fs.readFileSync(path.join(target, "manifest.json"));
fs.writeFileSync(path.join(target, "manifest.sha256"), `${sha(manifestBytes)}  manifest.json\n`);
process.stdout.write(`tekroo.kernel.contracts/0.14.0 ${sha(manifestBytes)} ${files.length}\n`);
