#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.12.0");
const target = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.13.0");
if (fs.existsSync(target)) {
  if (process.argv[2] !== "--refresh-candidate" || readCandidateStatus(target) !== "IMPLEMENTED_CANDIDATE") {
    throw new Error("0.13.0 exists; only an unaccepted candidate may be deliberately refreshed");
  }
  fs.rmSync(target, { recursive: true });
}
function readCandidateStatus(directory) {
  return JSON.parse(fs.readFileSync(path.join(directory, "manifest.json"), "utf8")).contract.status;
}
const sha = (value) => crypto.createHash("sha256").update(value).digest("hex");
const sort = (value) => Array.isArray(value) ? value.map(sort) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map((key) => [key, sort(value[key])])) : value;
const canonical = (value) => JSON.stringify(sort(value));
const read = (base, relative) => JSON.parse(fs.readFileSync(path.join(base, relative), "utf8"));
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
  const content = fs.readFileSync(absolute, "utf8");
  fs.writeFileSync(absolute, content.replaceAll("tekroo.kernel.contracts/0.12.0", "tekroo.kernel.contracts/0.13.0"));
}

const architectSchema = read(root, "config/starter-team/transport-neutral-20260929/roles-v4/architect-2.1.1/handlers/story.design-requested/result.schema.json");
const planSchema = structuredClone(architectSchema.properties.work_product);
planSchema.$id = "tekroo.kernel.contracts/0.13.0/schemas/feature-execution-plan.schema.json";
planSchema.$schema = "https://json-schema.org/draft/2020-12/schema";
planSchema.title = "PM-finalized executable feature plan";
planSchema.properties.result_type.const = "FEATURE_EXECUTION_PLAN";
planSchema.properties.source_design_digest = { type: "string", pattern: "^[0-9a-f]{64}$" };
planSchema.properties.handoffs = { type: "array", maxItems: 16384, items: {
  type: "object", additionalProperties: false,
  required: ["provider_task_index", "consumer_task_index", "capability", "contract"],
  properties: {
    provider_task_index: { type: "integer", minimum: 0, maximum: 127 },
    consumer_task_index: { type: "integer", minimum: 0, maximum: 127 },
    capability: { type: "string", pattern: "^[a-z0-9](?:[a-z0-9.-]{0,126}[a-z0-9])?$" },
    contract: { type: "string", minLength: 1, maxLength: 4096 }
  }
} };
planSchema.required = ["schema_version", "result_type", "source_design_digest", "architecture", "tasks", "handoffs"];
write("schemas/feature-execution-plan.schema.json", planSchema);
write("schemas/feature-design-candidate.schema.json", {
  $id: "tekroo.kernel.contracts/0.13.0/schemas/feature-design-candidate.schema.json",
  $schema: "https://json-schema.org/draft/2020-12/schema", title: "Immutable architect proposal reference",
  type: "object", additionalProperties: false,
  required: ["prepared_by", "prepared_execution", "output_digest", "prepared_at"],
  properties: {
    prepared_by: { type: "string", minLength: 1 }, prepared_execution: { type: "object" },
    output_digest: { type: "string", pattern: "^[0-9a-f]{64}$" }, prepared_at: { type: "string", format: "date-time" }
  }
});

const designDigest = "a".repeat(64);
const baseTasks = [
  { title: "Provide alias lookup", description: "Implement lookup", dependsOn: [] },
  { title: "Use alias lookup", description: "Integrate lookup", dependsOn: [0] }
];
const handoff = { providerTaskIndex: 0, consumerTaskIndex: 1, capability: "alias.lookup", contract: "Lookup returns a resolved actor." };
const planCase = (fixtureId, scenario, finalTasks, handoffs, admitted, reason, sourceDesignDigest = designDigest) => ({
  classification: "NORMATIVE_EXAMPLE", fixtureId, kind: "FEATURE_PLAN_MODEL", model: "PM_FINALIZATION",
  given: { designDigest, sourceTasks: baseTasks },
  when: { authorRole: "project-manager", sourceDesignDigest, finalTasks, handoffs },
  then: { expected: { admitted, reason } }, scenario
});
const fixtures = [
  planCase("P13-PLAN-001", "PM_FINALIZES_ARCHITECT_PROPOSAL", baseTasks, [handoff], true, null),
  planCase("P13-PLAN-002", "TECHNICAL_MUTATION_WITHOUT_ARCHITECT", [{ ...baseTasks[0], description: "Different design" }, baseTasks[1]], [handoff], false, "TECHNICAL_MUTATION"),
  planCase("P13-PLAN-003", "PROVIDER_NOT_UPSTREAM", baseTasks, [{ ...handoff, providerTaskIndex: 1, consumerTaskIndex: 0 }], false, "PROVIDER_NOT_UPSTREAM"),
  planCase("P13-PLAN-004", "MISSING_PROVIDER", baseTasks, [{ ...handoff, providerTaskIndex: 2 }], false, "MISSING_ENDPOINT"),
  planCase("P13-PLAN-005", "DUPLICATE_CONSUMER_CAPABILITY", baseTasks, [handoff, handoff], false, "DUPLICATE_CAPABILITY"),
  planCase("P13-PLAN-006", "ORDERING_EDGE_WITHOUT_CAPABILITY", baseTasks, [], true, null),
  planCase("P13-PLAN-007", "WRONG_DESIGN_DIGEST", baseTasks, [handoff], false, "DESIGN_DIGEST_MISMATCH", "b".repeat(64))
];
write("fixtures/feature-plan-runtime.json", { schemaVersion: "1.0.0", contractIdentity: "tekroo.kernel.contracts/0.13.0", fixtures });
const invariants = [
  { invariantId: "P13-PLAN-INV-001", statement: "The architect proposes technical design; the PM alone prepares the executable plan bound to that immutable proposal.", testIds: ["P13-PLAN-001", "P13-PLAN-002"] },
  { invariantId: "P13-PLAN-INV-002", statement: "Every declared cross-task capability has one provider on an upstream dependency path.", testIds: ["P13-PLAN-003", "P13-PLAN-004", "P13-PLAN-005", "P13-PLAN-006"] },
  { invariantId: "P13-PLAN-INV-003", statement: "The PM final plan is bound to the exact immutable architect output digest.", testIds: ["P13-PLAN-001", "P13-PLAN-007"] }
];
write("invariants/feature-plan-invariants.json", { schemaVersion: "1.0.0", contractIdentity: "tekroo.kernel.contracts/0.13.0", invariants });
write("traceability/feature-plan-traceability.json", { schemaVersion: "1.0.0", contractIdentity: "tekroo.kernel.contracts/0.13.0", requirements: invariants.map((item) => ({ requirementId: item.invariantId.replace("INV", "REQ"), kind: "DECISION", statement: item.statement, testIds: item.testIds })) });
write("compatibility/from-0.12.0.json", {
  schemaVersion: "1.0.0", contractName: "tekroo.kernel.contracts", contractVersion: "0.13.0",
  predecessor: { contractName: "tekroo.kernel.contracts", contractVersion: "0.12.0", manifestSha256: sha(fs.readFileSync(path.join(source, "manifest.json"))) },
  compatibility: "ADDITIVE_OPT_IN", directions: { reader: "COMPATIBLE", writer: "WORKFLOW_2_0_0_REQUIRED", adapter: "LEGACY_ARCHITECT_PLAN_PATH_PRESERVED" },
  additions: ["immutable architect design candidate", "PM-owned executable plan", "explicit cross-task capability handoffs", "explicit PM decision-request block"],
  preserved: ["0.12.0 workflow and role-handler semantics for already-admitted instances", "kernel event and command identities", "SMA boundary"]
});

const validatorPath = path.join(target, "runner/validate-package.mjs");
let validator = fs.readFileSync(validatorPath, "utf8");
validator = validator.replace('manifest.contract.version === "0.12.0"', 'manifest.contract.version === "0.13.0"');
validator = validator.replace('const compatibility = readJson("compatibility/from-0.11.0.json");', 'const compatibility = readJson("compatibility/from-0.12.0.json");');
validator = validator.replace('compatibility.predecessor?.contractVersion === "0.11.0" && compatibility.contractVersion === "0.12.0" && compatibility.compatibility === "ADDITIVE"', 'compatibility.predecessor?.contractVersion === "0.12.0" && compatibility.contractVersion === "0.13.0" && compatibility.compatibility === "ADDITIVE_OPT_IN"');
validator = validator.replace('const workflowFixtures = readJson("fixtures/workflow-runtime.json");', `const featurePlanSchema = readJson("schemas/feature-execution-plan.schema.json");
const featurePlanFixtures = readJson("fixtures/feature-plan-runtime.json").fixtures;
const featurePlanInvariants = readJson("invariants/feature-plan-invariants.json").invariants;
const featurePlanRequirements = readJson("traceability/feature-plan-traceability.json").requirements;
const featurePlanIds = new Set(featurePlanFixtures.map((item) => item.fixtureId));
check("phase13-plan-schema", featurePlanSchema.properties.result_type.const === "FEATURE_EXECUTION_PLAN" && featurePlanSchema.required.includes("handoffs"));
check("phase13-plan-fixtures", featurePlanFixtures.length === 7 && featurePlanFixtures.every((item) => item.kind === "FEATURE_PLAN_MODEL" && item.given && item.when && item.then) && featurePlanFixtures.some((item) => item.scenario === "PROVIDER_NOT_UPSTREAM" && item.then.expected.admitted === false));
check("phase13-plan-traceability", featurePlanRequirements.length === 3 && featurePlanInvariants.length === 3 && featurePlanRequirements.every((item) => item.testIds.every((id) => featurePlanIds.has(id))));
const workflowFixtures = readJson("fixtures/workflow-runtime.json");`);
fs.writeFileSync(validatorPath, validator);

const runnerPath = path.join(target, "runner/reference-runner.mjs");
let runner = fs.readFileSync(runnerPath, "utf8");
runner = runner.replace('const fixtures = fixtureFiles.flatMap((file) => readJson(file).fixtures);', `const fixtures = fixtureFiles.flatMap((file) => readJson(file).fixtures).concat(readJson("fixtures/feature-plan-runtime.json").fixtures);`);
runner = runner.replace('  } else if (fixture.kind === "WORKFLOW_MODEL") {', `  } else if (fixture.kind === "FEATURE_PLAN_MODEL") {
    actual = runFeaturePlanModel(fixture);
  } else if (fixture.kind === "WORKFLOW_MODEL") {`);
runner = runner.replace('const manifest = readJson("manifest.json");', `function runFeaturePlanModel(fixture) {
  const { designDigest, sourceTasks } = fixture.given;
  const { authorRole, sourceDesignDigest, finalTasks, handoffs } = fixture.when;
  const reject = (reason) => ({ admitted: false, reason });
  if (authorRole !== "project-manager") return reject("WRONG_AUTHOR");
  if (sourceDesignDigest !== designDigest) return reject("DESIGN_DIGEST_MISMATCH");
  if (finalTasks.length !== sourceTasks.length) return reject("TECHNICAL_MUTATION");
  for (let index = 0; index < finalTasks.length; index += 1) {
    const source = sourceTasks[index], final = finalTasks[index];
    if (final.title !== source.title || final.description !== source.description) return reject("TECHNICAL_MUTATION");
    if (!source.dependsOn.every((dependency) => final.dependsOn.includes(dependency))) return reject("DROPPED_DEPENDENCY");
  }
  const seen = new Set();
  const upstream = (provider, consumer, visited = new Set()) => {
    if (visited.has(consumer)) return false;
    visited.add(consumer);
    return finalTasks[consumer].dependsOn.some((dependency) => dependency === provider || upstream(provider, dependency, visited));
  };
  for (const handoff of handoffs) {
    const { providerTaskIndex: provider, consumerTaskIndex: consumer, capability } = handoff;
    if (!finalTasks[provider] || !finalTasks[consumer] || provider === consumer) return reject("MISSING_ENDPOINT");
    const key = consumer + ":" + capability;
    if (seen.has(key)) return reject("DUPLICATE_CAPABILITY");
    seen.add(key);
    if (!upstream(provider, consumer)) return reject("PROVIDER_NOT_UPSTREAM");
  }
  return { admitted: true, reason: null };
}

const manifest = readJson("manifest.json");`);
fs.writeFileSync(runnerPath, runner);

const sourceManifest = read(source, "manifest.json");
const oldFiles = new Map(sourceManifest.files.map((item) => [item.path, item]));
const roles = new Map([
  ["schemas/feature-execution-plan.schema.json", "schema"], ["schemas/feature-design-candidate.schema.json", "schema"],
  ["fixtures/feature-plan-runtime.json", "feature-plan-fixtures"], ["invariants/feature-plan-invariants.json", "feature-plan-invariants"],
  ["traceability/feature-plan-traceability.json", "feature-plan-traceability"], ["compatibility/from-0.12.0.json", "compatibility"]
]);
const paths = walk(target).filter((item) => item !== "manifest.json" && item !== "manifest.sha256");
const files = paths.map((relative) => {
  const bytes = fs.readFileSync(path.join(target, relative));
  const json = relative.endsWith(".json") ? JSON.parse(bytes.toString("utf8")) : null;
  const old = oldFiles.get(relative);
  return { path: relative, role: roles.get(relative) ?? old?.role ?? "contract-asset", mediaType: json ? "application/json" : old?.mediaType ?? "text/javascript", bytes: bytes.length, sha256: sha(bytes), canonicalJsonSha256: json ? sha(canonical(json)) : null, dependencies: old?.dependencies ?? [] };
});
const count = (directory, field) => paths.filter((item) => item.startsWith(`${directory}/`) && item.endsWith(".json")).reduce((sum, item) => sum + (read(target, item)[field]?.length ?? 0), 0);
const catalogue = read(target, "catalogue/kernel-catalogue.json");
write("manifest.json", {
  ...sourceManifest, contract: { name: "tekroo.kernel.contracts", version: "0.13.0", status: "IMPLEMENTED_CANDIDATE" },
  authority: { decisionAuthority: "Principal", implementationAuthority: "AUTHORIZED", productionAuthority: "NONE" },
  generatedAt: "2026-09-30T00:00:00Z", files,
  counts: { catalogueEntries: catalogue.entries.length, commandTypes: catalogue.entries.filter((item) => item.kind === "COMMAND").length, eventTypes: catalogue.entries.filter((item) => item.kind === "EVENT").length, fixtures: count("fixtures", "fixtures"), invariants: count("invariants", "invariants"), schemas: paths.filter((item) => item.startsWith("schemas/") && item.endsWith(".json")).length, traceabilityRequirements: count("traceability", "requirements") },
  knownLimitations: ["Package structure does not qualify a production deployment.", "Workflow 1.x instances must drain or be isolated before loading workflow 2.0.0; concurrent trigger versions are unsupported.", "Automatic architect clarification successor is not implemented; PM needs_decision blocks for operator resolution."],
  sourceLineage: [
    { path: "CONTRACTS/tekroo.kernel.contracts/0.12.0/manifest.json", sha256: sha(fs.readFileSync(path.join(source, "manifest.json"))) },
    { path: "docs/architecture/139-pm-owned-feature-plan-013.md", sha256: sha(fs.readFileSync(path.join(root, "docs/architecture/139-pm-owned-feature-plan-013.md"))) },
    { path: "config/workflows/software-development.v2.json", sha256: sha(fs.readFileSync(path.join(root, "config/workflows/software-development.v2.json"))) },
    { path: "config/starter-team/roles-v4/project-manager-2.2.0/role.json", sha256: sha(fs.readFileSync(path.join(root, "config/starter-team/roles-v4/project-manager-2.2.0/role.json"))) },
    { path: "config/starter-team/plan-013-publisher.pub", sha256: sha(fs.readFileSync(path.join(root, "config/starter-team/plan-013-publisher.pub"))) },
    { path: "scripts/generate_phase10_contract_0_13.mjs", sha256: sha(fs.readFileSync(fileURLToPath(import.meta.url))) }
  ]
});
const manifestBytes = fs.readFileSync(path.join(target, "manifest.json"));
fs.writeFileSync(path.join(target, "manifest.sha256"), `${sha(manifestBytes)}  manifest.json\n`);
process.stdout.write(`tekroo.kernel.contracts/0.13.0 ${sha(manifestBytes)} ${files.length}\n`);
