#!/usr/bin/env python3
"""Candidate-8 SMA-S2 driver with isolated case-1 workspace.

Candidate 7 remains immutable. This successor preserves its science and
runtime fences while excluding the first-prompt-empty workspace from the
alpha/beta corpus capture allowlist. Live modes remain default-deny.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
from typing import Any

import sma_s2_measured_driver_candidate_5 as c7


ROOT = Path(__file__).resolve().parents[1]
DRIVER = Path(__file__).resolve()
CONFIG = ROOT / "investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-8.json"
MATRIX = ROOT / "investigations/sma-q1/layered/sma-s2-predicate-oracle-matrix-candidate-4.json"
CORPUS = ROOT / "investigations/sma-q1/layered/sma-s2-measured-corpus-candidate-1.json"
SEMANTIC_SOURCE = ROOT / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-2.json"
STUB = ROOT / "scripts/sma_s2_deterministic_model_stub_candidate_3.py"
MEASURED_ROOT = Path("/tmp/tekroo-sma-s2-successor-candidate-8")
EMPTY = MEASURED_ROOT / "actor-empty"
ALPHA = MEASURED_ROOT / "actor-alpha"
BETA = MEASURED_ROOT / "actor-beta"
DATABASE = "sma_s2_successor_candidate_8_measured_20260814"
SEMANTIC = "sma_s2_successor_candidate_8_semantic_20260814"
EPISODIC = "sma_s2_successor_candidate_8_episodic_20260814"
LABEL = "com.tekroo.sma-service-s2-successor-candidate-8"
DRESS_OPERATION_COUNT = 34

for module in (c7, c7.c6, c7.c6.c5, c7.c6.c5.c4, c7.c6.c5.c4.base):
    module.CONFIG = CONFIG
    module.MATRIX = MATRIX
    module.CORPUS = CORPUS
    module.STUB = STUB
    module.MEASURED_ROOT = MEASURED_ROOT
    module.DATABASE = DATABASE
    module.SEMANTIC = SEMANTIC
    module.EPISODIC = EPISODIC
    module.LABEL = LABEL

PredicateFailure = c7.PredicateFailure
require = c7.require
sha_file = c7.sha_file
sha_bytes = c7.sha_bytes
canonical_bytes = c7.canonical_bytes
base = c7.base


def expected_workspace_isolation() -> dict[str, Any]:
    return {
        "case1WorkspaceRole": "EMPTY_UNCAPTURED",
        "case1Workspace": str(EMPTY),
        "corpusCaptureAllowlist": [str(ALPHA), str(BETA)],
        "emptyWorkspaceMustNotBeCaptureAllowlisted": True,
        "case1ConversationReceiptsRetainedUntilCleanup": True,
        "syntheticCorpusSourceCount": 4,
        "allWorkspacesUnderCandidateRoot": True,
        "cleanupMustRemoveEmptyWorkspace": True,
    }


def validate_workspace_isolation(value: Any) -> dict[str, Any]:
    expected = expected_workspace_isolation()
    require(value == expected, "candidate-8 workspace-isolation contract mismatch")
    allowlist = [Path(item) for item in value["corpusCaptureAllowlist"]]
    empty = Path(value["case1Workspace"])
    checks = {
        "workspaceContractExact": value == expected,
        "emptyDistinctFromAlpha": empty != ALPHA,
        "emptyDistinctFromBeta": empty != BETA,
        "emptyExcludedFromCapture": empty not in allowlist,
        "alphaCaptureAllowed": ALPHA in allowlist,
        "betaCaptureAllowed": BETA in allowlist,
        "captureAllowlistCardinality": len(allowlist) == 2,
        "allUnderCandidateRoot": all(path.parent == MEASURED_ROOT
                                     for path in [empty, *allowlist]),
    }
    require(all(checks.values()), f"candidate-8 workspace isolation rejected: {checks}")
    return checks


class Candidate8Runtime(c7.Candidate7Runtime):
    def __init__(self, output: Path, journal: Any, runtime_ports: dict[str, Any]) -> None:
        super().__init__(output, journal, runtime_ports)
        self.empty = EMPTY

    def setup_workspaces(self) -> None:
        super().setup_workspaces()
        self.empty.mkdir(parents=True, exist_ok=False)
        (self.empty / ".openhands").symlink_to(
            c7.c6.c5.c4.base.SMA / ".openhands", target_is_directory=True
        )
        self.journal.append("PREASSERTION_CANDIDATE_8_WORKSPACE_LAYOUT", {
            "rootSha256": sha_bytes(str(MEASURED_ROOT).encode()),
            "emptySha256": sha_bytes(str(self.empty).encode()),
            "alphaSha256": sha_bytes(str(self.alpha).encode()),
            "betaSha256": sha_bytes(str(self.beta).encode()),
            "allUnderCandidateRoot": all(
                path.parent == MEASURED_ROOT for path in (self.empty, self.alpha, self.beta)
            ),
        })

    def workspace_isolation_receipt(self) -> dict[str, Any]:
        environment = self.p.service_environment(True, True)
        raw = environment.get("SMA_OPENHANDS_WORKSPACE_ALLOWLIST", "")
        allowlist = raw.split(",") if raw else []
        receipt = {
            "case1WorkspaceRole": "EMPTY_UNCAPTURED",
            "emptyWorkspaceSha256": sha_bytes(str(self.empty).encode()),
            "alphaWorkspaceSha256": sha_bytes(str(self.alpha).encode()),
            "betaWorkspaceSha256": sha_bytes(str(self.beta).encode()),
            "captureAllowlistSha256": sha_bytes(raw.encode()),
            "captureAllowlistCardinality": len(allowlist),
            "emptyCaptureAllowed": str(self.empty) in allowlist,
            "alphaCaptureAllowed": str(self.alpha) in allowlist,
            "betaCaptureAllowed": str(self.beta) in allowlist,
        }
        require(receipt["captureAllowlistCardinality"] == 2,
                "candidate-8 capture allowlist cardinality changed")
        require(receipt["emptyCaptureAllowed"] is False,
                "candidate-8 empty workspace entered capture allowlist")
        require(receipt["alphaCaptureAllowed"] and receipt["betaCaptureAllowed"],
                "candidate-8 corpus workspace missing from capture allowlist")
        return receipt

    def case_first_prompt_empty(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        isolation = self.workspace_isolation_receipt()
        require(self.support.listener_pid(c7.c6.BRIDGE_PORT) is None,
                "case-1 empty workspace ran with the SMA bridge active")
        require(not self.support.launch_identity(LABEL)["loaded"],
                "case-1 empty workspace ran with the candidate service active")
        record = self.one_prompt(item, self.empty)
        require(record["contextLength"] == 0,
                "first prompt in dedicated empty partition injected context")
        self.journal.append("PREASSERTION_CASE1_WORKSPACE_ISOLATION", {
            "caseId": item["caseId"],
            "repetition": item["repetition"],
            "conversationId": record["conversationId"],
            "workspaceRole": "EMPTY_UNCAPTURED",
            **isolation,
        })
        return [record]

    def ensure_seeded(self) -> None:
        if not self.seeded:
            isolation = self.workspace_isolation_receipt()
            self.journal.append("PREASSERTION_CORPUS_CAPTURE_WORKSPACE_ISOLATION", isolation)
        super().ensure_seeded()

    def cleanup(self) -> dict[str, Any]:
        result = super().cleanup()
        empty_absent = not self.empty.exists()
        result["verified"]["emptyWorkspaceAbsent"] = empty_absent
        result["oracleValues"]["C8-WORKSPACE-CLEANUP"] = empty_absent
        self.journal.append("PREASSERTION_CANDIDATE_8_WORKSPACE_CLEANUP", {
            "emptyWorkspaceAbsent": empty_absent,
            "candidateRootAbsent": not MEASURED_ROOT.exists(),
        })
        return result


def load_object(path: Path, label: str) -> dict[str, Any]:
    return c7.load_object(path, label)


def validate_static_contract() -> dict[str, Any]:
    inherited = c7.validate_static_contract()
    config = json.loads(CONFIG.read_text())
    corpus = json.loads(CORPUS.read_text())
    isolation = validate_workspace_isolation(config.get("workspaceIsolation"))
    namespaces = config["disposableNamespaces"]
    checks = {
        "candidateExact": config.get("candidate") == 8,
        "rootExact": namespaces.get("workspaceRoot") == str(MEASURED_ROOT),
        "emptyExact": namespaces.get("emptyWorkspace") == str(EMPTY),
        "alphaExact": namespaces.get("alphaWorkspace") == str(ALPHA),
        "betaExact": namespaces.get("betaWorkspace") == str(BETA),
        "databaseExact": namespaces.get("mongodbDatabase") == DATABASE,
        "semanticExact": namespaces.get("qdrantSemanticCollection") == SEMANTIC,
        "episodicExact": namespaces.get("qdrantEpisodicCollection") == EPISODIC,
        "dressOperationCount": config["dressRehearsal"].get("operationCount")
                               == DRESS_OPERATION_COUNT,
        "syntheticCorpusSourceCount": len(corpus["syntheticMemoryCorpus"])
                                      == config["workspaceIsolation"]["syntheticCorpusSourceCount"],
    }
    require(all(checks.values()), f"candidate-8 static contract rejected: {checks}")
    return {**inherited, "candidate8ConfigurationMatchesDriver": True,
            "candidate8WorkspaceIsolation": isolation,
            "candidate8StaticChecks": checks}


def validate_execution_identity(identity_path: Path) -> dict[str, Any]:
    identity = load_object(identity_path, "execution identity")
    ports = c7.c6.validate_runtime_ports(identity.get("runtimePorts"))
    prereg_ref = identity.get("preregistration", {})
    acceptance_ref = identity.get("acceptance", {})
    prereg_path = Path(str(prereg_ref.get("path", "")))
    acceptance_path = Path(str(acceptance_ref.get("path", "")))
    prereg = load_object(prereg_path, "candidate-8 preregistration") if prereg_path.is_file() else {}
    acceptance = load_object(acceptance_path, "candidate-8 acceptance") if acceptance_path.is_file() else {}
    config = json.loads(CONFIG.read_text())
    checks = {
        "identityRecordType": identity.get("recordType") == "SMA_S2_CANDIDATE_8_EXECUTION_IDENTITY",
        "identityCandidateExact": identity.get("candidate") == 8,
        "identityAcceptedFrozen": identity.get("status") == "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
        "identityNonAuthorizing": identity.get("measuredExecutionAuthorized") is False,
        "driverMatches": c7.c6.referenced_file_matches(identity.get("driver"), DRIVER),
        "corpusMatches": c7.c6.referenced_file_matches(identity.get("corpus"), CORPUS),
        "configurationMatches": c7.c6.referenced_file_matches(identity.get("configuration"), CONFIG),
        "matrixMatches": c7.c6.referenced_file_matches(identity.get("oracleMatrix"), MATRIX),
        "stubMatches": c7.c6.referenced_file_matches(identity.get("stub"), STUB),
        "workspaceIsolationExact": identity.get("workspaceIsolation")
                                   == config.get("workspaceIsolation"),
        "preregistrationMatches": c7.c6.referenced_file_matches(prereg_ref)
                                  and prereg.get("candidate") == 8,
        "acceptanceMatches": c7.c6.referenced_file_matches(acceptance_ref)
                             and acceptance.get("status") == "ACCEPTED_FROZEN"
                             and acceptance.get("candidate") == 8
                             and acceptance.get("candidatePreregistrationSha256")
                             == prereg_ref.get("sha256")
                             and acceptance.get("measuredExecutionAuthorized") is False,
        "dependenciesExactAndMatch": c7.c6.dependency_closure_matches(identity, prereg),
        "stubPortAvailable": c7.c6.port_is_available(ports["stubHost"], ports["stubPort"]),
        "bridgePortAvailable": c7.c6.port_is_available(ports["bridgeHost"], ports["bridgePort"]),
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-8 execution identity rejected: {checks}")
    return {"identity": identity, "identitySha256": sha_file(identity_path),
            "runtimePorts": ports, "preregistration": prereg_ref,
            "acceptance": acceptance_ref, "checks": checks}


def validate_dress_rehearsal_fence(identity_path: Path, authorization_path: Path) -> dict[str, Any]:
    bound = validate_execution_identity(identity_path)
    authorization = load_object(authorization_path, "dress-rehearsal authorization")
    checks = {
        "recordType": authorization.get("recordType")
                      == "SMA_S2_CANDIDATE_8_DRESS_REHEARSAL_AUTHORIZATION",
        "live": authorization.get("status") == "AUTHORIZED_NOT_CONSUMED",
        "identity": authorization.get("executionIdentitySha256") == bound["identitySha256"],
        "ports": authorization.get("runtimePorts") == bound["runtimePorts"],
        "workspaceIsolation": authorization.get("workspaceIsolation")
                              == bound["identity"].get("workspaceIsolation"),
        "driver": authorization.get("driverSha256") == sha_file(DRIVER),
        "configuration": authorization.get("configurationSha256") == sha_file(CONFIG),
        "preregistration": authorization.get("preregistrationSha256")
                           == bound["preregistration"].get("sha256"),
        "acceptance": authorization.get("acceptanceSha256")
                      == bound["acceptance"].get("sha256"),
        "singleAttempt": authorization.get("maximumDressRehearsalAttempts") == 1,
        "operationCount": authorization.get("operationCount") == DRESS_OPERATION_COUNT,
        "noMeasuredCases": authorization.get("measuredCaseExecutions") == 0,
        "noMeasuredRepetitions": authorization.get("measuredRepetitionExecutions") == 0,
        "realModelProhibited": authorization.get("realModelCallsAllowed") is False,
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-8 dress-rehearsal fence rejected: {checks}")
    return {**bound, "authorizationChecks": checks}


def validate_execution_fence(identity_path: Path, authorization_path: Path) -> dict[str, Any]:
    bound = validate_execution_identity(identity_path)
    authorization = load_object(authorization_path, "measured authorization")
    dress_ref = authorization.get("dressRehearsalReceipt", {})
    dress_path = Path(str(dress_ref.get("path", "")))
    dress = load_object(dress_path, "dress-rehearsal receipt") if dress_path.is_file() else {}
    checks = {
        "recordType": authorization.get("recordType")
                      == "SMA_S2_CANDIDATE_8_SINGLE_USE_MEASURED_AUTHORIZATION",
        "live": authorization.get("status") == "AUTHORIZED_NOT_CONSUMED",
        "singleUse": authorization.get("maximumMeasuredAttempts") == 1,
        "identity": authorization.get("executionIdentitySha256") == bound["identitySha256"],
        "ports": authorization.get("runtimePorts") == bound["runtimePorts"],
        "workspaceIsolation": authorization.get("workspaceIsolation")
                              == bound["identity"].get("workspaceIsolation"),
        "driver": authorization.get("driverSha256") == sha_file(DRIVER),
        "matrix": authorization.get("oracleMatrixSha256") == sha_file(MATRIX),
        "corpus": authorization.get("corpusSha256") == sha_file(CORPUS),
        "configuration": authorization.get("configurationSha256") == sha_file(CONFIG),
        "stub": authorization.get("stubSha256") == sha_file(STUB),
        "preregistration": authorization.get("preregistrationSha256")
                           == bound["preregistration"].get("sha256"),
        "acceptance": authorization.get("acceptanceSha256")
                      == bound["acceptance"].get("sha256"),
        "scope": authorization.get("caseCount") == 20
                 and authorization.get("repetitionCount") == 103,
        "realModelProhibited": authorization.get("realModelCallsAllowed") is False,
        "dressReference": c7.c6.referenced_file_matches(dress_ref),
        "dressRecordType": dress.get("recordType")
                           == "SMA_S2_CANDIDATE_8_DRESS_REHEARSAL_RECEIPT",
        "dressPass": dress.get("status") == "PASS",
        "dressIdentity": dress.get("executionIdentitySha256") == bound["identitySha256"],
        "dressPorts": dress.get("runtimePorts") == bound["runtimePorts"],
        "dressWorkspaceIsolation": dress.get("workspaceIsolation")
                                   == bound["identity"].get("workspaceIsolation"),
        "dressOperations": dress.get("completedOperations") == DRESS_OPERATION_COUNT,
        "dressCleanup": dress.get("cleanupStatus") == "PASS",
        "dressNoClaim": dress.get("claim") is None,
        "dressNoMeasuredCredit": dress.get("measuredCaseExecutions") == 0
                                 and dress.get("measuredRepetitionExecutions") == 0,
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-8 measured fence rejected: {checks}")
    return {**bound, "authorizationChecks": checks,
            "dressReceiptSha256": dress_ref["sha256"]}


for module in (c7, c7.c6, c7.c6.c5, c7.c6.c5.c4):
    if hasattr(module, "Candidate7Runtime"):
        module.Candidate7Runtime = Candidate8Runtime
    if hasattr(module, "Candidate6Runtime"):
        module.Candidate6Runtime = Candidate8Runtime
    if hasattr(module, "Candidate5Runtime"):
        module.Candidate5Runtime = Candidate8Runtime
    if hasattr(module, "Candidate4Runtime"):
        module.Candidate4Runtime = Candidate8Runtime
c7.c6.c5.c4.base.LiveRuntime = Candidate8Runtime


def run_plan(args: argparse.Namespace, plan: list[dict[str, Any]], fence: dict[str, Any],
             *, dress: bool) -> int:
    args.output.mkdir(parents=True, exist_ok=False)
    journal = base.DurableJournal(args.output / "raw-evidence.jsonl")
    workspace_isolation = fence["identity"].get("workspaceIsolation")
    journal.append("DRESS_REHEARSAL_START" if dress else "EXECUTION_START", {
        "driverSha256": sha_file(DRIVER), "matrixSha256": sha_file(MATRIX),
        "corpusSha256": sha_file(CORPUS), "runtimePorts": fence["runtimePorts"],
        "workspaceIsolation": workspace_isolation,
        "plannedOperations": len(plan), "measuredCredit": 0 if dress else len(plan),
        "dressReceiptSha256": None if dress else fence["dressReceiptSha256"],
    })
    runtime = Candidate8Runtime(args.output, journal, fence["runtimePorts"])
    results: list[dict[str, Any]] = []
    failure: dict[str, Any] | None = None
    try:
        runtime.preflight_absence()
        runtime.start_stub()
        runtime.setup_workspaces()
        for item in plan:
            try:
                records = runtime.run_operation(item)
                results.append({"caseId": item["caseId"], "repetition": item["repetition"],
                                "status": "PASS", "recordCount": len(records)})
            except PredicateFailure as error:
                failure = {"class": "SUBSTANTIVE", "caseId": item["caseId"],
                           "repetition": item["repetition"],
                           "messageSha256": sha_bytes(str(error).encode())}
                journal.append("REPETITION_FAILURE", failure)
                results.append({"caseId": item["caseId"], "repetition": item["repetition"],
                                "status": "FAIL"})
            except Exception as error:
                failure = {"class": "HARNESS", "caseId": item["caseId"],
                           "repetition": item["repetition"],
                           **c7.sanitized_failure(error, "RUN_PLAN")}
                journal.append("REPETITION_FAILURE", failure)
                break
    finally:
        cleanup = runtime.cleanup()
    complete = len(results) == len(plan) and all(item["status"] == "PASS" for item in results) \
        and all(cleanup["oracleValues"].values())
    receipt = {
        "recordType": "SMA_S2_CANDIDATE_8_DRESS_REHEARSAL_RECEIPT" if dress
                      else "SMA_S2_MEASURED_EXECUTION_RECEIPT",
        "status": "PASS" if complete else "FAIL" if failure and failure["class"] == "SUBSTANTIVE"
                  else "INCONCLUSIVE",
        "claim": None if dress or not complete else "OPENHANDS_SMA_BOUNDARY_QUALIFIED",
        "executionIdentitySha256": fence["identitySha256"],
        "runtimePorts": fence["runtimePorts"],
        "workspaceIsolation": workspace_isolation,
        "completedOperations": len(results),
        "completedRepetitions": 0 if dress else len(results),
        "measuredCaseExecutions": 0 if dress else 20,
        "measuredRepetitionExecutions": 0 if dress else len(results),
        "results": results, "failure": failure, "cleanup": cleanup,
        "cleanupStatus": "PASS" if all(cleanup["oracleValues"].values()) else "FAIL",
        "rawJournalSha256": sha_file(args.output / "raw-evidence.jsonl"),
    }
    name = "dress-rehearsal-receipt.json" if dress else "execution-receipt.json"
    descriptor = os.open(args.output / name, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, canonical_bytes(receipt) + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    print(json.dumps({"status": receipt["status"],
                      "receipt": str(args.output / name)}, sort_keys=True))
    return 0 if complete else 1


def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser()
    value.add_argument("--offline-contract", action="store_true")
    value.add_argument("--dress-rehearsal", action="store_true")
    value.add_argument("--execute", action="store_true")
    value.add_argument("--execution-identity", type=Path)
    value.add_argument("--authorization", type=Path)
    value.add_argument("--output", type=Path)
    return value


def main() -> int:
    args = parser().parse_args()
    if args.offline_contract:
        print(json.dumps(validate_static_contract(), indent=2, sort_keys=True))
        return 0
    if args.dress_rehearsal and args.execute:
        raise PermissionError("dress rehearsal and measured execution are mutually exclusive")
    if not args.dress_rehearsal and not args.execute:
        raise PermissionError("default deny: use --offline-contract or separately authorized live mode")
    if args.execution_identity is None or args.authorization is None or args.output is None:
        raise PermissionError("live mode requires identity, authorization, and output")
    if args.dress_rehearsal:
        fence = validate_dress_rehearsal_fence(args.execution_identity, args.authorization)
        return run_plan(args, c7.dress_rehearsal_plan(json.loads(CORPUS.read_text())),
                        fence, dress=True)
    fence = validate_execution_fence(args.execution_identity, args.authorization)
    return run_plan(args, base.exact_plan(json.loads(CORPUS.read_text())), fence, dress=False)


if __name__ == "__main__":
    raise SystemExit(main())
