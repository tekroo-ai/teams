#!/usr/bin/env node
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.14.0");
const target = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.15.0");
const mode = process.argv[2];
if (process.argv.length > 3 || mode && !["--refresh-candidate", "--freeze-candidate"].includes(mode)) throw new Error("Expected --refresh-candidate or --freeze-candidate");
const frozen = mode === "--freeze-candidate";
if (fs.existsSync(target)) {
  const existing = JSON.parse(fs.readFileSync(path.join(target, "manifest.json"), "utf8"));
  if (!["--refresh-candidate", "--freeze-candidate"].includes(mode) || existing.contract.status !== "IMPLEMENTATION_IN_PROGRESS" || existing.authority.productionAuthority !== "NONE") throw new Error("Refusing to modify a frozen or activated contract package");
}
const sha = (bytes) => crypto.createHash("sha256").update(bytes).digest("hex");
const sorted = (value) => Array.isArray(value) ? value.map(sorted) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map((key) => [key, sorted(value[key])])) : value;
const canonical = (value) => JSON.stringify(sorted(value));
const walk = (directory, prefix = "") => fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
  const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
  return entry.isDirectory() ? walk(path.join(directory, entry.name), relative) : [relative];
}).sort();
const write = (relative, value) => { const absolute = path.join(target, relative); fs.mkdirSync(path.dirname(absolute), { recursive: true }); fs.writeFileSync(absolute, `${JSON.stringify(sorted(value), null, 2)}\n`); };
// Only the unaccepted successor is written. No predecessor file is edited.
fs.cpSync(source, target, { recursive: true });
for (const relative of walk(target)) {
  if (relative === "manifest.json" || relative === "manifest.sha256") continue;
  const absolute = path.join(target, relative);
  fs.writeFileSync(absolute, fs.readFileSync(absolute, "utf8").replaceAll("tekroo.kernel.contracts/0.14.0", "tekroo.kernel.contracts/0.15.0"));
}
const schema = JSON.parse(execFileSync("go", ["run", "./scripts/export_plan_amendment_schema"], { cwd: root, encoding: "utf8" }));
write("schemas/feature-plan-amendment-proposal.schema.json", schema);
fs.copyFileSync(path.join(root, "docs/architecture/bounded-inflight-plan-amendment-design.md"), path.join(target, "plan-amendment-design.md"));
write("qualification/plan-amendment.json", {
  schemaVersion: "1.0.0", contractIdentity: "tekroo.kernel.contracts/0.15.0",
  boundary: "Component and isolated automatic coordinator/frontier verification with a canned HTTP model, accepted-context inputs and minimal signed role fixtures. No current production-role or live-model qualification is claimed.",
  cases: [
    { id: "P15-DOMAIN", requirement: "One independent-policy-approved amendment preserves product/design and retained work; no unplanned/wrong-purpose source or task, attempt, purpose, hop or deadline capacity reset", testSource: "organization/plan_amendment_test.go", testNames: ["TestPlanAmendmentReservationRejectsUnplannedAndWrongPurposeSources", "TestPlanAmendmentSourceRepairUsesExistingFiniteAllowance", "TestPlanAmendmentPreservesCompletedWorkAndRequiresIndependentPolicy", "TestPlanAmendmentRejectsUnsafePlans"] },
    { id: "P15-SCHEMA", requirement: "Runtime and packaged closed Draft 2020-12 object schema agree; encoded objects and ambiguous fields rejected", testSource: "organization/plan_amendment_test.go", testNames: ["TestPlanAmendmentGeneratedSchemaMatchesRuntime", "TestPlanAmendmentSchemaRejectsEncodedObjectsAndAmbiguousFields"] },
    { id: "P15-TRANSACTION", requirement: "Full graph, invocation, available evidence and complete budget value fenced; no generic-CAS bypass; failed assessment rejection retained", testSource: "adapters/mongo/plan_amendment_store_integration_test.go", testNames: ["TestPlanAmendmentTransactionFencesBudgetDebitsAndPreservesRejection", "TestPlanAmendmentTransactionPublishesOnlyPinnedAssessmentAndFreshSuccessors", "TestPlanAmendmentFailedAssessmentWithoutOutputIsDurablyRejected"] },
    { id: "P15-PREPARATION", requirement: "Kernel/Mongo native service preparation and restart create no prepublication invocation/message and do not rewrite retained state or budget", testSource: "adapters/operationalruntime/plan_amendment_integration_test.go", testNames: ["TestAmendmentPrepublicationCreatesOnlyFreshUnassignedNodesAcrossRestart"] },
    { id: "P15-NATIVE", requirement: "Retained reasoning, admitted raw nested input and formal schema reach native wire; journal replay repeats no model call", testSource: "adapters/nativeagent/plan_amendment_test.go", testNames: ["TestPreparedAmendmentRetainsHistoryAndUsesRawInputAndFormalGrammar"] },
    { id: "P15-CHECKPOINT", requirement: "Private checkpoint preserves dirty bytes/deletions/executable mode without editing original Git; isolated successor lineage, immutable replay and tamper/path rejection", testSource: "adapters/operationalruntime/plan_amendment_checkpoint_test.go", testNames: ["TestAmendmentCheckpointPreservesDirtyWorkWithoutChangingSourceGit", "TestAmendmentCheckpointPreservesDeletionAndRejectsSymlinks"] },
    { id: "P15-ACTIVATION", requirement: "No activation without exact frozen schema/manifest, independent HUMAN acceptance and pinned policy", testSource: "adapters/operationalruntime/plan_amendment_config_test.go", testNames: ["TestAmendmentActivationRequiresFrozenContractAndBoundHumanAcceptance"] },
    { id: "P15-EVIDENCE", requirement: "Successor consumes source/consultation/assessment/input evidence with output bindings; retained evidence unchanged", testSource: "adapters/operationalruntime/plan_amendment_evidence_test.go", testNames: ["TestAmendmentSuccessorEvidencePreservesAndBindsDecisionTrail"] },
    { id: "P15-REJECTION", requirement: "Real coordinator persists malformed/failed/expired assessment as an operator-readable decision; restart freezes dispatch without a repeated assessment or ledger rewrite", testSource: "adapters/operationalruntime/plan_amendment_rejection_integration_test.go", testNames: ["TestAmendmentCoordinatorRetainsOperatorDecisionWithoutAnotherAssessment"] },
    { id: "P15-FAULT-ISOLATION", requirement: "Receipt-backed consultation failure preserves unrelated terminal ingestion without repair/retry admission; four-reference exact binding and restart preserve original rejection", testSource: "adapters/operationalruntime/task_consultation_integration_test.go", testNames: ["TestConsultationProfileRecoversRejectedEvidenceBindingAcrossRestart"] },
    { id: "P15-FORWARD", requirement: "Real kernel/native-worker blocked output through architect consultation, one retained-context PM assessment, independent policy, checkpoint, publication and normal successor frontier; restart, immutable completed/source Git and budget preservation without seeded terminal/APPROVED/APPLIED projections; exact completed journal inspection after publication, changed invocation rejection and used-allowance no-op", testSource: "adapters/operationalruntime/plan_amendment_forward_integration_test.go", testNames: ["TestAmendmentForwardCoordinatorNativeAssessmentAndPublicationAcrossRestart", "TestAmendmentInScopeConsultationContinuesWithoutPMAssessment"] },
    { id: "P15-PROTOCOL-INSPECTION", requirement: "Protocol wrapper construction and inspection share exact server-resolved binding; changed prompt, schema contract, input, invocation and sequence rejected", testSource: "adapters/nativeagent/protocol_prompt_binding_test.go", testNames: ["TestBoundaryRecognizesOnlyExactServerResolvedProtocolPrompt"] }
  ],
  pending: frozen ? ["exact independent principal policy acceptance and activation", "current production-role live-model qualification"] : ["normative coverage review and explicit contract freeze/acceptance", "current production-role live-model qualification"],
  implementationStatus: frozen ? "COMPLETE" : "IN_PROGRESS", productionAuthority: "NONE"
});
fs.writeFileSync(path.join(target, "README.md"), `# 0.15.0 bounded plan-amendment candidate\n\n${frozen ? "Implementation is reviewed and frozen; activation still requires independent principal acceptance of the exact manifest and administrator-owned policy." : "Implementation is staged, disabled and not frozen."} See plan-amendment-design.md for implemented boundaries and qualification gaps, and qualification/plan-amendment.json for the Go-test coverage index. Inherited predecessor fixtures are not amendment execution evidence. Package validator PASS checks structure/reference integrity only, not the automatic coordinator or live model. No accepted predecessor or live authority is modified.\n`);
write("compatibility/from-0.14.0.json", {
  schemaVersion: "1.0.0", contractName: "tekroo.kernel.contracts", contractVersion: "0.15.0",
  predecessor: { contractName: "tekroo.kernel.contracts", contractVersion: "0.14.0", manifestSha256: sha(fs.readFileSync(path.join(source, "manifest.json"))) },
  compatibility: "ADDITIVE_OPT_IN", directions: { reader: "COMPATIBLE", writer: "EXPLICIT_AMENDMENT_POLICY_REQUIRED", adapter: "UNRESOLVED_ADVISORY_REMAINS_BLOCKED_WITHOUT_ACTIVATION" },
  additions: ["closed raw-object PM amendment proposal", "single-use reservation and independent policy decision ledger", "transaction fences on tasks, invocations, evidence availability and complete budget snapshot"],
  preserved: ["accepted product specification and completed work", "all immutable predecessor events, outputs and task definitions", "existing finite invocation/purpose budgets and deadlines", "kernel command identities and SMA boundary"],
  qualification: frozen ? "Reviewed frozen candidate. Isolated component and automatic coordinator/frontier tests with a canned model pass. Current production-role/live-model behavior remains unqualified. Disabled until exact independent principal policy acceptance and separate deployment authority." : "Isolated component and automatic coordinator/frontier tests with a canned model pass. Current production-role/live-model behavior remains unqualified. Disabled pending normative review, freeze, exact principal acceptance and separate deployment authority."
});
const validatorPath = path.join(target, "runner/validate-package.mjs");
let validator = fs.readFileSync(validatorPath, "utf8");
validator = validator.replace('manifest.contract.version === "0.14.0"', 'manifest.contract.version === "0.15.0"');
validator = validator.replace('readJson("compatibility/from-0.13.0.json")', 'readJson("compatibility/from-0.14.0.json")');
validator = validator.replace('compatibility.predecessor?.contractVersion === "0.13.0" && compatibility.contractVersion === "0.14.0"', 'compatibility.predecessor?.contractVersion === "0.14.0" && compatibility.contractVersion === "0.15.0"');
validator = validator.replace('const revisionSchema = readJson("schemas/feature-design-revision.schema.json");', `const amendmentSchema = readJson("schemas/feature-plan-amendment-proposal.schema.json");
check("phase15-amendment-object-schema", amendmentSchema.$schema === "https://json-schema.org/draft/2020-12/schema" && amendmentSchema.additionalProperties === false && amendmentSchema.properties.successor_plan.type === "object" && amendmentSchema.properties.schema_version.const === "0.15.0");
check("phase15-not-activated", ["IMPLEMENTATION_IN_PROGRESS", "FROZEN_CANDIDATE"].includes(manifest.contract.status) && manifest.authority.productionAuthority === "NONE");
const amendmentQualification = readJson("qualification/plan-amendment.json");
check("phase15-qualification-boundary", amendmentQualification.implementationStatus === (manifest.contract.status === "FROZEN_CANDIDATE" ? "COMPLETE" : "IN_PROGRESS") && amendmentQualification.productionAuthority === "NONE" && amendmentQualification.pending.length > 0);
check("phase15-qualification-case-identities", new Set(amendmentQualification.cases.map(item => item.id)).size === amendmentQualification.cases.length && amendmentQualification.cases.every(item => item.testNames.length > 0 && manifest.sourceLineage.some(source => source.sourcePath === item.testSource)));
const revisionSchema = readJson("schemas/feature-design-revision.schema.json");`);
fs.writeFileSync(validatorPath, validator);
const manifest = JSON.parse(fs.readFileSync(path.join(source, "manifest.json"), "utf8"));
manifest.contract.version = "0.15.0";
manifest.contract.status = frozen ? "FROZEN_CANDIDATE" : "IMPLEMENTATION_IN_PROGRESS";
manifest.authority.productionAuthority = "NONE";
manifest.authority.implementationAuthority = "AUTHORIZED";
manifest.sourceLineage = [
  { sourcePath: "CONTRACTS/tekroo.kernel.contracts/0.14.0/manifest.json", sha256: sha(fs.readFileSync(path.join(source, "manifest.json"))) },
  ...[
    "organization/feature.go", "organization/plan_amendment.go", "organization/plan_amendment_schema.go", "organization/plan_amendment_lifecycle.go", "organization/plan_amendment_test.go",
    "adapters/mongo/aggregate_head.go", "adapters/mongo/feature_store.go", "adapters/mongo/plan_amendment_store.go", "adapters/mongo/plan_amendment_store_integration_test.go",
    "adapters/nativeagent/session.go", "adapters/nativeagent/boundary.go", "adapters/nativeagent/prompt_binding.go", "adapters/nativeagent/protocol_prompt_binding_test.go", "adapters/nativeagent/result_contract_test.go", "adapters/nativeagent/plan_amendment_test.go",
    "adapters/operationalruntime/plan_amendment.go", "adapters/operationalruntime/plan_amendment_config.go", "adapters/operationalruntime/plan_amendment_publication.go", "adapters/operationalruntime/plan_amendment_checkpoint.go",
    "adapters/operationalruntime/production.go", "adapters/operationalruntime/production_native.go", "adapters/operationalruntime/task_admission.go", "adapters/operationalruntime/task_completion.go", "adapters/operationalruntime/task_consultation.go", "adapters/operationalruntime/task_workspace.go", "adapters/operationalruntime/candidate_lineage.go", "adapters/operationalruntime/feature_work_recovery.go", "agenttools/worktree_snapshot.go",
    "adapters/operationalruntime/task_consultation_integration_test.go", "adapters/operationalruntime/reconciliation_fault_isolation_test.go",
    "adapters/operationalruntime/plan_amendment_config_test.go", "adapters/operationalruntime/plan_amendment_checkpoint_test.go", "adapters/operationalruntime/plan_amendment_integration_test.go", "adapters/operationalruntime/plan_amendment_evidence_test.go", "adapters/operationalruntime/plan_amendment_rejection_integration_test.go", "adapters/operationalruntime/plan_amendment_forward_integration_test.go",
    "docs/architecture/bounded-inflight-plan-amendment-design.md", "scripts/export_plan_amendment_schema/main.go", "scripts/generate_phase10_contract_0_15.mjs"
  ].map((sourcePath) => ({ sourcePath, sha256: sha(fs.readFileSync(path.join(root, sourcePath))) }))
];
manifest.files = walk(target).filter((relative) => relative !== "manifest.json" && relative !== "manifest.sha256").map((relative) => {
  const bytes = fs.readFileSync(path.join(target, relative));
  const previous = manifest.files.find((entry) => entry.path === relative);
  const entry = { ...previous, path: relative, role: previous?.role ?? (relative.startsWith("schemas/") ? "schema" : relative.startsWith("compatibility/") ? "compatibility" : "specification"), dependencies: previous?.dependencies ?? [], bytes: bytes.length, mediaType: relative.endsWith(".json") ? "application/json" : relative.endsWith(".mjs") ? "text/javascript" : "text/markdown", sha256: sha(bytes) };
  if (relative.endsWith(".json")) entry.canonicalJsonSha256 = sha(canonical(JSON.parse(bytes)));
  return entry;
});
manifest.counts.schemas = manifest.files.filter((entry) => entry.role === "schema").length;
write("manifest.json", manifest);
const identity = sha(fs.readFileSync(path.join(target, "manifest.json")));
fs.writeFileSync(path.join(target, "manifest.sha256"), `${identity}\n`);
process.stdout.write(`0.15.0 ${manifest.contract.status}: ${identity}\n`);
