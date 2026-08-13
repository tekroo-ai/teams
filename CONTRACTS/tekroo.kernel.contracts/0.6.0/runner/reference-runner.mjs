#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const reportPath = process.argv[2];

if (!reportPath) {
  throw new Error("usage: node reference-runner.mjs <report-path>");
}

function readJson(relativePath) {
  return JSON.parse(fs.readFileSync(path.join(packageRoot, relativePath), "utf8"));
}

function sortValue(value) {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.keys(value).sort().map((key) => [key, sortValue(value[key])]),
    );
  }
  return value;
}

function canonical(value) {
  return JSON.stringify(sortValue(value));
}

function sha256(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
}

function validateType(value, rule) {
  if (rule.oneOf !== undefined) return rule.oneOf.filter((candidate) => validateType(value, candidate)).length === 1;
  if (rule.anyOf !== undefined) return rule.anyOf.some((candidate) => validateType(value, candidate));
  if (rule.const !== undefined) return value === rule.const;
  if (rule.type === "null") return value === null;
  if (rule.type === "string") {
    if (typeof value !== "string") return false;
    if (rule.minLength !== undefined && value.length < rule.minLength) return false;
    if (rule.maxLength !== undefined && value.length > rule.maxLength) return false;
    if (rule.pattern !== undefined && !(new RegExp(rule.pattern)).test(value)) return false;
    if (rule.enum !== undefined && !rule.enum.includes(value)) return false;
    return true;
  }
  if (rule.type === "integer") {
    if (!Number.isInteger(value)) return false;
    if (rule.minimum !== undefined && value < rule.minimum) return false;
    if (rule.maximum !== undefined && value > rule.maximum) return false;
    return true;
  }
  if (rule.type === "boolean") return typeof value === "boolean";
  if (rule.type === "array") {
    if (!Array.isArray(value)) return false;
    if (rule.minItems !== undefined && value.length < rule.minItems) return false;
    if (rule.maxItems !== undefined && value.length > rule.maxItems) return false;
    if (rule.uniqueItems && new Set(value.map(canonical)).size !== value.length) return false;
    return rule.items === undefined || value.every((item) => validateType(item, rule.items));
  }
  if (rule.type === "object") return validatePayload(value, rule);
  return false;
}

function validatePayload(payload, schema) {
  if (schema.oneOf !== undefined) return schema.oneOf.filter((candidate) => validateType(payload, candidate)).length === 1;
  if (schema.anyOf !== undefined) return schema.anyOf.some((candidate) => validateType(payload, candidate));
  if (payload === null || typeof payload !== "object" || Array.isArray(payload)) return false;
  const required = new Set(schema.required ?? []);
  const allowed = new Set(Object.keys(schema.properties ?? {}));
  for (const name of required) if (!(name in payload)) return false;
  if (schema.additionalProperties === false) {
    for (const name of Object.keys(payload)) if (!allowed.has(name)) return false;
  }
  if (schema.minProperties !== undefined && Object.keys(payload).length < schema.minProperties) return false;
  for (const [name, value] of Object.entries(payload)) {
    const rule = schema.properties?.[name];
    if (rule && !validateType(value, rule)) return false;
    if (!rule && schema.additionalProperties === false) return false;
  }
  return true;
}

const storyTransitions = {
  DRAFT: { authorize: "READY", close: "CLOSED" },
  READY: { begin_planning: "PLANNING", close: "CLOSED" },
  PLANNING: { activate: "ACTIVE", close: "CLOSED" },
  ACTIVE: { complete: "COMPLETED", close: "CLOSED" },
  COMPLETED: { accept: "ACCEPTED", reopen: "ACTIVE", close: "CLOSED" },
  ACCEPTED: { reopen: "ACTIVE" },
  CLOSED: {},
};

const taskTransitions = {
  PLANNED: { mark_ready: "READY", close: "CLOSED" },
  READY: { activate: "ACTIVE", close: "CLOSED" },
  ACTIVE: { complete: "COMPLETED", close: "CLOSED" },
  COMPLETED: { reopen: "ACTIVE" },
  CLOSED: {},
};

function runStateModel(fixture) {
  const transitions = fixture.model === "task" ? taskTransitions : storyTransitions;
  let phase = fixture.given.phase;
  let epoch = fixture.given.lifecycleEpoch;
  let condition = fixture.given.condition ?? "RUNNABLE";
  let rejected = null;
  for (const action of fixture.when.actions) {
    if (action === "block") {
      if (["COMPLETED", "ACCEPTED", "CLOSED"].includes(phase)) rejected = "REJECTED_CLOSED";
      else condition = "BLOCKED";
      continue;
    }
    if (action === "unblock") {
      if (condition !== "BLOCKED") rejected = "REJECTED_POLICY";
      else condition = "RUNNABLE";
      continue;
    }
    const next = transitions[phase]?.[action];
    if (!next) {
      rejected = ["COMPLETED", "ACCEPTED", "CLOSED"].includes(phase)
        ? "REJECTED_CLOSED"
        : "REJECTED_POLICY";
      break;
    }
    if (action === "reopen") epoch += 1;
    phase = next;
  }
  return { phase, lifecycleEpoch: epoch, condition, rejected };
}

function runDagModel(fixture) {
  const graph = new Map();
  for (const node of fixture.given.nodes) graph.set(node, []);
  for (const edge of fixture.when.edges) {
    if (!graph.has(edge.parent) || !graph.has(edge.child) || edge.parent === edge.child) {
      return { valid: false, reason: "INVALID_PARENT" };
    }
    graph.get(edge.parent).push(edge.child);
  }
  const visiting = new Set();
  const visited = new Set();
  function cycle(node) {
    if (visiting.has(node)) return true;
    if (visited.has(node)) return false;
    visiting.add(node);
    for (const child of graph.get(node)) if (cycle(child)) return true;
    visiting.delete(node);
    visited.add(node);
    return false;
  }
  for (const node of graph.keys()) if (cycle(node)) return { valid: false, reason: "CYCLE" };
  return { valid: true, reason: null };
}

function runIdentityModel(fixture) {
  const segment = "(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])";
  const actorFqn = new RegExp(`^${segment}::${segment}-[1-9][0-9]{0,19}$`);
  return { valid: actorFqn.test(fixture.when.value) };
}

function runPreconditionModel(fixture) {
  const keys = fixture.when.preconditions.map((item) => item.aggregate);
  if (new Set(keys).size !== keys.length || canonical([...keys].sort()) !== canonical(keys)) return { valid: false };
  return { valid: fixture.when.preconditions.every((item) => fixture.given.revisions[item.aggregate] === item.expectedRevision) };
}

function runReviewJoinModel(fixture) {
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

function runEscalationModel(fixture) {
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

function runReleaseModel(fixture) {
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

function runModelCapabilityPolicyModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  const rank = { DETERMINISTIC: 0, BOUNDED_EXECUTION: 1, COMPLEX_REASONING: 2, NOVEL_REASONING: 3, HUMAN_REQUIRED: 4 };
  if (action.action === "BIND_PROFILE") {
    if (current.scopeRevision !== current.profileScopeRevision) return { accepted: false, reason: "STALE_WORK_PROFILE" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "AUTHORIZE_ASSIGNMENT") {
    if (current.revoked) return { accepted: false, reason: "QUALIFICATION_REVOKED" };
    if (current.qualificationStatus !== "PASS") return { accepted: false, reason: "QUALIFICATION_REQUIRED" };
    if (!current.hardConstraintsPass) return { accepted: false, reason: "HARD_CONSTRAINT_FAILED" };
    if (rank[current.selectedRoute] < rank[current.requiredRoute]) return { accepted: false, reason: "CAPABILITY_MISMATCH" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "FINALIZE_REVIEW") {
    const proven = new Set(current.provenDimensions);
    if (!current.requiredDimensions.every((dimension) => proven.has(dimension))) return { accepted: false, reason: "INDEPENDENCE_NOT_PROVEN" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "ESCALATE" && current.classification === "MATERIAL_DISAGREEMENT") return { accepted: true, reason: "ESCALATION_REQUIRED" };
  if (action.action === "SELECT") {
    if (current.classification === "MATERIAL_DISAGREEMENT") return { accepted: false, reason: "MATERIAL_DISAGREEMENT" };
    if (!current.submittedCandidateIds.includes(action.selectedCandidateId)) return { accepted: false, reason: "CANDIDATE_NOT_SUBMITTED" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  return { accepted: false, reason: "INVALID_ACTION" };
}

function runSuccessorSetModel(fixture) {
  const values = fixture.when.successorIds;
  return { valid: values.length > 0 && new Set(values).size === values.length && canonical([...values].sort()) === canonical(values) };
}

function runCompatibilityModel(fixture) {
  return { outcome: fixture.given.sourceVersion === "1.0.0" && !fixture.when.exactContextAvailable ? "MIGRATION_REQUIRED" : "ACCEPTED" };
}

function runIdempotencyModel(fixture) {
  const decisions = new Map();
  const receipts = [];
  for (const command of fixture.when.commands) {
    const key = canonical(command.scope);
    const fingerprint = sha256(canonical(command.semanticRequest));
    const prior = decisions.get(key);
    if (!prior) {
      const receipt = { outcome: "APPLIED", fingerprint, eventCount: 1 };
      decisions.set(key, receipt);
      receipts.push(receipt);
    } else if (prior.fingerprint === fingerprint) {
      receipts.push(prior);
    } else {
      receipts.push({ outcome: "REJECTED_CONFLICT", reason: "IDEMPOTENCY_KEY_REUSE", eventCount: 0 });
    }
  }
  return { receipts, durableDecisionCount: decisions.size };
}

const manifest = readJson("manifest.json");
const catalogue = readJson("catalogue/kernel-catalogue.json");
const payloadSchemas = readJson("schemas/payloads.schema.json");
const fixtureFiles = manifest.files
  .filter((file) => file.role === "fixtures")
  .map((file) => file.path);
const fixtures = fixtureFiles.flatMap((file) => readJson(file).fixtures);
const commands = new Map(catalogue.entries.filter((entry) => entry.kind === "COMMAND").map((entry) => [entry.typeId, entry]));

const cases = [];
for (const fixture of fixtures) {
  let actual;
  if (fixture.kind === "CATALOGUE_COMMAND") {
    const entry = commands.get(fixture.when.commandType);
    if (!entry) {
      actual = { outcomeCode: "REJECTED_INVALID", eventTypes: [] };
    } else {
      const schemaName = entry.payloadSchema.split("#/$defs/")[1];
      const valid = validatePayload(fixture.when.payload, payloadSchemas.$defs[schemaName]);
      actual = valid
        ? { outcomeCode: "APPLIED", eventTypes: entry.emits }
        : { outcomeCode: "REJECTED_INVALID", eventTypes: [] };
    }
  } else if (fixture.kind === "STATE_MODEL") {
    actual = runStateModel(fixture);
  } else if (fixture.kind === "DAG_MODEL") {
    actual = runDagModel(fixture);
  } else if (fixture.kind === "IDENTITY_MODEL") {
    actual = runIdentityModel(fixture);
  } else if (fixture.kind === "IDEMPOTENCY_MODEL") {
    actual = runIdempotencyModel(fixture);
  } else if (fixture.kind === "PRECONDITION_MODEL") {
    actual = runPreconditionModel(fixture);
  } else if (fixture.kind === "REVIEW_JOIN_MODEL") {
    actual = runReviewJoinModel(fixture);
  } else if (fixture.kind === "BOUNDED_REVIEW_MODEL") {
    actual = runBoundedReviewModel(fixture);
  } else if (fixture.kind === "ESCALATION_MODEL") {
    actual = runEscalationModel(fixture);
  } else if (fixture.kind === "RELEASE_MODEL") {
    actual = runReleaseModel(fixture);
  } else if (fixture.kind === "MODEL_CAPABILITY_POLICY_MODEL") {
    actual = runModelCapabilityPolicyModel(fixture);
  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {
    actual = runSuccessorSetModel(fixture);
  } else if (fixture.kind === "COMPATIBILITY_MODEL") {
    actual = runCompatibilityModel(fixture);
  } else {
    actual = { unsupported: fixture.kind };
  }
  const passed = canonical(actual) === canonical(fixture.then.expected);
  cases.push({
    fixtureId: fixture.fixtureId,
    status: passed ? "PASS" : "FAIL",
    actual,
    expected: fixture.then.expected,
  });
}

const report = {
  schemaVersion: "1.0.0",
  reportType: "REFERENCE_CORPUS",
  contractIdentity: `${manifest.contract.name}/${manifest.contract.version}`,
  manifestSha256: sha256(fs.readFileSync(path.join(packageRoot, "manifest.json"))),
  generatedAt: "2026-08-11T00:00:00Z",
  profile: "core-hermetic-reference-model",
  status: cases.every((item) => item.status === "PASS") ? "PASS" : "FAIL",
  counts: {
    total: cases.length,
    passed: cases.filter((item) => item.status === "PASS").length,
    failed: cases.filter((item) => item.status === "FAIL").length,
  },
  cases,
  qualificationBoundary: "This report validates the reference corpus and model only; it does not qualify a Tekroo implementation or production system.",
};

report.reportSha256 = sha256(canonical(report));
fs.mkdirSync(path.dirname(path.resolve(reportPath)), { recursive: true });
fs.writeFileSync(path.resolve(reportPath), `${JSON.stringify(sortValue(report), null, 2)}\n`);
process.stdout.write(`${report.status} ${report.counts.total} ${report.reportSha256}\n`);
if (report.status !== "PASS") process.exitCode = 1;
