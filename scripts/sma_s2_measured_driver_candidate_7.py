#!/usr/bin/env python3
"""Candidate-9 SMA-S2 driver with an explicit Maven project directory.

Candidate 8 remains immutable. This successor preserves its workspace
isolation and scientific plan while binding corpus-promotion Maven subprocesses
to the frozen SMA project. Live modes remain default-deny.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import time
from pathlib import Path
from typing import Any

import sma_s2_measured_driver_candidate_6 as c8


ROOT = Path(__file__).resolve().parents[1]
DRIVER = Path(__file__).resolve()
CONFIG = ROOT / "investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-9.json"
MATRIX = ROOT / "investigations/sma-q1/layered/sma-s2-predicate-oracle-matrix-candidate-4.json"
CORPUS = ROOT / "investigations/sma-q1/layered/sma-s2-measured-corpus-candidate-1.json"
STUB = ROOT / "scripts/sma_s2_deterministic_model_stub_candidate_3.py"
MAVEN_PROJECT = Path("/Users/paul/work/tekroo-ai/sma-s1-p2m")
MAVEN_POM = MAVEN_PROJECT / "pom.xml"
MEASURED_ROOT = Path("/tmp/tekroo-sma-s2-successor-candidate-9")
EMPTY = MEASURED_ROOT / "actor-empty"
ALPHA = MEASURED_ROOT / "actor-alpha"
BETA = MEASURED_ROOT / "actor-beta"
DATABASE = "sma_s2_successor_candidate_9_measured_20260814"
SEMANTIC = "sma_s2_successor_candidate_9_semantic_20260814"
EPISODIC = "sma_s2_successor_candidate_9_episodic_20260814"
LABEL = "com.tekroo.sma-service-s2-successor-candidate-9"
DRESS_OPERATION_COUNT = 34

for module in (c8, c8.c7, c8.c7.c6, c8.c7.c6.c5, c8.c7.c6.c5.c4,
               c8.c7.c6.c5.c4.base):
    module.CONFIG = CONFIG
    module.MATRIX = MATRIX
    module.CORPUS = CORPUS
    module.STUB = STUB
    module.MEASURED_ROOT = MEASURED_ROOT
    module.DATABASE = DATABASE
    module.SEMANTIC = SEMANTIC
    module.EPISODIC = EPISODIC
    module.LABEL = LABEL

c8.MEASURED_ROOT = MEASURED_ROOT
c8.EMPTY = EMPTY
c8.ALPHA = ALPHA
c8.BETA = BETA
c8.DATABASE = DATABASE
c8.SEMANTIC = SEMANTIC
c8.EPISODIC = EPISODIC
c8.LABEL = LABEL

PredicateFailure = c8.PredicateFailure
require = c8.require
sha_file = c8.sha_file
sha_bytes = c8.sha_bytes
canonical_bytes = c8.canonical_bytes
base = c8.base


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


def expected_maven_promotion() -> dict[str, Any]:
    return {
        "projectDirectory": str(MAVEN_PROJECT),
        "pomPath": str(MAVEN_POM),
        "subprocessWorkingDirectoryMustBeExplicit": True,
        "parentWorkingDirectoryMayChange": False,
        "failureEvidence": {
            "exitStatusRequired": True,
            "stdoutLengthRequired": True,
            "stdoutSha256Required": True,
            "stderrLengthRequired": True,
            "stderrSha256Required": True,
            "rawStdoutOrStderrProhibited": True,
        },
    }


def validate_workspace_isolation(value: Any) -> dict[str, Any]:
    expected = expected_workspace_isolation()
    require(value == expected, "candidate-9 workspace-isolation contract mismatch")
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
    require(all(checks.values()), f"candidate-9 workspace isolation rejected: {checks}")
    return checks


def validate_maven_promotion(value: Any, *, require_files: bool = True) -> dict[str, Any]:
    expected = expected_maven_promotion()
    checks = {
        "contractExact": value == expected,
        "projectAbsolute": MAVEN_PROJECT.is_absolute(),
        "pomUnderProject": MAVEN_POM.parent == MAVEN_PROJECT,
        "projectExists": MAVEN_PROJECT.is_dir() if require_files else True,
        "pomExists": MAVEN_POM.is_file() if require_files else True,
        "explicitSubprocessCwd": value.get("subprocessWorkingDirectoryMustBeExplicit") is True
                                 if isinstance(value, dict) else False,
        "parentCwdImmutable": value.get("parentWorkingDirectoryMayChange") is False
                              if isinstance(value, dict) else False,
        "sanitizedFailureEvidenceExact": value.get("failureEvidence")
                                         == expected["failureEvidence"]
                                         if isinstance(value, dict) else False,
    }
    require(all(checks.values()), f"candidate-9 Maven promotion contract rejected: {checks}")
    return checks


def _bytes(value: str | bytes | None) -> bytes:
    if value is None:
        return b""
    return value if isinstance(value, bytes) else value.encode()


class Candidate9Runtime(c8.Candidate8Runtime):
    def __init__(self, output: Path, journal: Any, runtime_ports: dict[str, Any]) -> None:
        super().__init__(output, journal, runtime_ports)
        self.parent_working_directory = Path.cwd()
        self._original_promote_memory = self.support.promote_memory
        self.support.promote_memory = self.promote_memory_bound

    def _failure_receipt(self, *, error_type: str, exit_status: int | None,
                         stdout: str | bytes | None, stderr: str | bytes | None,
                         command: list[str], timed_out: bool) -> dict[str, Any]:
        stdout_bytes = _bytes(stdout)
        stderr_bytes = _bytes(stderr)
        return {
            "errorType": error_type,
            "exitStatus": exit_status,
            "timedOut": timed_out,
            "projectDirectory": str(MAVEN_PROJECT),
            "pomPath": str(MAVEN_POM),
            "subprocessCwdExplicit": True,
            "parentWorkingDirectoryUnchanged": Path.cwd() == self.parent_working_directory,
            "commandSha256": sha_bytes(canonical_bytes(command)),
            "stdoutLength": len(stdout_bytes),
            "stdoutSha256": sha_bytes(stdout_bytes),
            "stderrLength": len(stderr_bytes),
            "stderrSha256": sha_bytes(stderr_bytes),
            "rawStdoutOrStderrRetained": False,
        }

    def promote_memory_bound(self, database: str, semantic_collection: str,
                             episodic_collection: str, memory_id: str,
                             code: str, checksum: str) -> dict[str, Any]:
        config = json.loads(CONFIG.read_text())
        validate_maven_promotion(config.get("mavenPromotion"))
        command = [
            "mvn", "-q", "-Dunit.excludedGroups=",
            "-Dtest=Wp5eReplayPromotionIntegrationTest",
            f"-Dwp5e.database={database}",
            f"-Dwp5e.semanticCollection={semantic_collection}",
            f"-Dwp5e.episodicCollection={episodic_collection}",
            f"-Dwp5e.memoryId={memory_id}", f"-Dwp5e.code={code}",
            f"-Dwp5e.checksum={checksum}", "test",
        ]
        started = time.monotonic()
        try:
            completed = subprocess.run(
                command, cwd=MAVEN_PROJECT, check=True, capture_output=True,
                text=True, timeout=900,
            )
        except subprocess.CalledProcessError as error:
            receipt = self._failure_receipt(
                error_type=type(error).__name__, exit_status=error.returncode,
                stdout=error.stdout, stderr=error.stderr, command=command, timed_out=False,
            )
            self.journal.append("MAVEN_PROMOTION_SUBPROCESS_FAILURE", receipt)
            raise
        except subprocess.TimeoutExpired as error:
            receipt = self._failure_receipt(
                error_type=type(error).__name__, exit_status=None,
                stdout=error.stdout, stderr=error.stderr, command=command, timed_out=True,
            )
            self.journal.append("MAVEN_PROMOTION_SUBPROCESS_FAILURE", receipt)
            raise
        except OSError as error:
            receipt = self._failure_receipt(
                error_type=type(error).__name__, exit_status=None,
                stdout=None, stderr=None, command=command, timed_out=False,
            )
            self.journal.append("MAVEN_PROMOTION_SUBPROCESS_FAILURE", receipt)
            raise
        require(Path.cwd() == self.parent_working_directory,
                "Maven promotion changed parent working directory")
        stdout_bytes = _bytes(completed.stdout)
        stderr_bytes = _bytes(completed.stderr)
        return {
            "elapsed_ms": (time.monotonic() - started) * 1000.0,
            "exit_code": completed.returncode,
            "stdout_length": len(stdout_bytes),
            "stdout_sha256": sha_bytes(stdout_bytes),
            "stderr_length": len(stderr_bytes),
            "stderr_sha256": sha_bytes(stderr_bytes),
            "project_directory": str(MAVEN_PROJECT),
            "pom_sha256": sha_file(MAVEN_POM),
            "subprocess_cwd_explicit": True,
            "parent_working_directory_unchanged": True,
        }

    def cleanup(self) -> dict[str, Any]:
        try:
            result = super().cleanup()
            unchanged = Path.cwd() == self.parent_working_directory
            result["verified"]["parentWorkingDirectoryUnchanged"] = unchanged
            result["oracleValues"]["C9-PARENT-CWD-UNCHANGED"] = unchanged
            self.journal.append("PREASSERTION_CANDIDATE_9_CWD_CLEANUP", {
                "parentWorkingDirectoryUnchanged": unchanged,
                "mavenProjectSha256": sha_bytes(str(MAVEN_PROJECT).encode()),
            })
            return result
        finally:
            self.support.promote_memory = self._original_promote_memory


for module in (c8, c8.c7, c8.c7.c6, c8.c7.c6.c5, c8.c7.c6.c5.c4):
    for name in ("Candidate8Runtime", "Candidate7Runtime", "Candidate6Runtime",
                 "Candidate5Runtime", "Candidate4Runtime"):
        if hasattr(module, name):
            setattr(module, name, Candidate9Runtime)
c8.c7.c6.c5.c4.base.LiveRuntime = Candidate9Runtime


def load_object(path: Path, label: str) -> dict[str, Any]:
    return c8.load_object(path, label)


def validate_static_contract() -> dict[str, Any]:
    inherited = c8.c7.c6.c5.c4.base.validate_static_contract()
    config = json.loads(CONFIG.read_text())
    corpus = json.loads(CORPUS.read_text())
    isolation = validate_workspace_isolation(config.get("workspaceIsolation"))
    maven = validate_maven_promotion(config.get("mavenPromotion"))
    namespaces = config["disposableNamespaces"]
    checks = {
        "candidateExact": config.get("candidate") == 9,
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
    require(all(checks.values()), f"candidate-9 static contract rejected: {checks}")
    return {**inherited, "candidate9ConfigurationMatchesDriver": True,
            "candidate9WorkspaceIsolation": isolation,
            "candidate9MavenPromotion": maven, "candidate9StaticChecks": checks}


def validate_execution_identity(identity_path: Path) -> dict[str, Any]:
    identity = load_object(identity_path, "execution identity")
    ports = c8.c7.c6.validate_runtime_ports(identity.get("runtimePorts"))
    prereg_ref = identity.get("preregistration", {})
    acceptance_ref = identity.get("acceptance", {})
    prereg_path = Path(str(prereg_ref.get("path", "")))
    acceptance_path = Path(str(acceptance_ref.get("path", "")))
    prereg = load_object(prereg_path, "candidate-9 preregistration") if prereg_path.is_file() else {}
    acceptance = load_object(acceptance_path, "candidate-9 acceptance") if acceptance_path.is_file() else {}
    config = json.loads(CONFIG.read_text())
    checks = {
        "identityRecordType": identity.get("recordType") == "SMA_S2_CANDIDATE_9_EXECUTION_IDENTITY",
        "identityCandidateExact": identity.get("candidate") == 9,
        "identityAcceptedFrozen": identity.get("status") == "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
        "identityNonAuthorizing": identity.get("measuredExecutionAuthorized") is False,
        "driverMatches": c8.c7.c6.referenced_file_matches(identity.get("driver"), DRIVER),
        "corpusMatches": c8.c7.c6.referenced_file_matches(identity.get("corpus"), CORPUS),
        "configurationMatches": c8.c7.c6.referenced_file_matches(identity.get("configuration"), CONFIG),
        "matrixMatches": c8.c7.c6.referenced_file_matches(identity.get("oracleMatrix"), MATRIX),
        "stubMatches": c8.c7.c6.referenced_file_matches(identity.get("stub"), STUB),
        "workspaceIsolationExact": identity.get("workspaceIsolation")
                                   == config.get("workspaceIsolation"),
        "mavenPromotionExact": identity.get("mavenPromotion") == config.get("mavenPromotion"),
        "preregistrationMatches": c8.c7.c6.referenced_file_matches(prereg_ref)
                                  and prereg.get("candidate") == 9,
        "acceptanceMatches": c8.c7.c6.referenced_file_matches(acceptance_ref)
                             and acceptance.get("status") == "ACCEPTED_FROZEN"
                             and acceptance.get("candidate") == 9
                             and acceptance.get("candidatePreregistrationSha256")
                             == prereg_ref.get("sha256")
                             and acceptance.get("measuredExecutionAuthorized") is False,
        "dependenciesExactAndMatch": c8.c7.c6.dependency_closure_matches(identity, prereg),
        "stubPortAvailable": c8.c7.c6.port_is_available(ports["stubHost"], ports["stubPort"]),
        "bridgePortAvailable": c8.c7.c6.port_is_available(ports["bridgeHost"], ports["bridgePort"]),
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-9 execution identity rejected: {checks}")
    return {"identity": identity, "identitySha256": sha_file(identity_path),
            "runtimePorts": ports, "preregistration": prereg_ref,
            "acceptance": acceptance_ref, "checks": checks}


def validate_dress_rehearsal_fence(identity_path: Path,
                                    authorization_path: Path) -> dict[str, Any]:
    bound = validate_execution_identity(identity_path)
    authorization = load_object(authorization_path, "dress-rehearsal authorization")
    checks = {
        "recordType": authorization.get("recordType")
                      == "SMA_S2_CANDIDATE_9_DRESS_REHEARSAL_AUTHORIZATION",
        "live": authorization.get("status") == "AUTHORIZED_NOT_CONSUMED",
        "identity": authorization.get("executionIdentitySha256") == bound["identitySha256"],
        "ports": authorization.get("runtimePorts") == bound["runtimePorts"],
        "workspaceIsolation": authorization.get("workspaceIsolation")
                              == bound["identity"].get("workspaceIsolation"),
        "mavenPromotion": authorization.get("mavenPromotion")
                          == bound["identity"].get("mavenPromotion"),
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
        raise PermissionError(f"candidate-9 dress-rehearsal fence rejected: {checks}")
    return {**bound, "authorizationChecks": checks}


def validate_execution_fence(identity_path: Path, authorization_path: Path) -> dict[str, Any]:
    bound = validate_execution_identity(identity_path)
    authorization = load_object(authorization_path, "measured authorization")
    dress_ref = authorization.get("dressRehearsalReceipt", {})
    dress_path = Path(str(dress_ref.get("path", "")))
    dress = load_object(dress_path, "dress-rehearsal receipt") if dress_path.is_file() else {}
    checks = {
        "recordType": authorization.get("recordType")
                      == "SMA_S2_CANDIDATE_9_SINGLE_USE_MEASURED_AUTHORIZATION",
        "live": authorization.get("status") == "AUTHORIZED_NOT_CONSUMED",
        "singleUse": authorization.get("maximumMeasuredAttempts") == 1,
        "identity": authorization.get("executionIdentitySha256") == bound["identitySha256"],
        "ports": authorization.get("runtimePorts") == bound["runtimePorts"],
        "workspaceIsolation": authorization.get("workspaceIsolation")
                              == bound["identity"].get("workspaceIsolation"),
        "mavenPromotion": authorization.get("mavenPromotion")
                          == bound["identity"].get("mavenPromotion"),
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
        "dressReference": c8.c7.c6.referenced_file_matches(dress_ref),
        "dressRecordType": dress.get("recordType")
                           == "SMA_S2_CANDIDATE_9_DRESS_REHEARSAL_RECEIPT",
        "dressPass": dress.get("status") == "PASS",
        "dressIdentity": dress.get("executionIdentitySha256") == bound["identitySha256"],
        "dressPorts": dress.get("runtimePorts") == bound["runtimePorts"],
        "dressWorkspaceIsolation": dress.get("workspaceIsolation")
                                   == bound["identity"].get("workspaceIsolation"),
        "dressMavenPromotion": dress.get("mavenPromotion")
                               == bound["identity"].get("mavenPromotion"),
        "dressOperations": dress.get("completedOperations") == DRESS_OPERATION_COUNT,
        "dressCleanup": dress.get("cleanupStatus") == "PASS",
        "dressNoClaim": dress.get("claim") is None,
        "dressNoMeasuredCredit": dress.get("measuredCaseExecutions") == 0
                                 and dress.get("measuredRepetitionExecutions") == 0,
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-9 measured fence rejected: {checks}")
    return {**bound, "authorizationChecks": checks,
            "dressReceiptSha256": dress_ref["sha256"]}


def run_plan(args: argparse.Namespace, plan: list[dict[str, Any]], fence: dict[str, Any],
             *, dress: bool) -> int:
    args.output.mkdir(parents=True, exist_ok=False)
    journal = base.DurableJournal(args.output / "raw-evidence.jsonl")
    workspace_isolation = fence["identity"].get("workspaceIsolation")
    maven_promotion = fence["identity"].get("mavenPromotion")
    journal.append("DRESS_REHEARSAL_START" if dress else "EXECUTION_START", {
        "driverSha256": sha_file(DRIVER), "matrixSha256": sha_file(MATRIX),
        "corpusSha256": sha_file(CORPUS), "runtimePorts": fence["runtimePorts"],
        "workspaceIsolation": workspace_isolation, "mavenPromotion": maven_promotion,
        "plannedOperations": len(plan), "measuredCredit": 0 if dress else len(plan),
        "dressReceiptSha256": None if dress else fence["dressReceiptSha256"],
    })
    runtime = Candidate9Runtime(args.output, journal, fence["runtimePorts"])
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
                           **c8.c7.sanitized_failure(error, "RUN_PLAN")}
                journal.append("REPETITION_FAILURE", failure)
                break
    finally:
        cleanup = runtime.cleanup()
    complete = len(results) == len(plan) and all(item["status"] == "PASS" for item in results) \
        and all(cleanup["oracleValues"].values())
    receipt = {
        "recordType": "SMA_S2_CANDIDATE_9_DRESS_REHEARSAL_RECEIPT" if dress
                      else "SMA_S2_MEASURED_EXECUTION_RECEIPT",
        "status": "PASS" if complete else "FAIL" if failure and failure["class"] == "SUBSTANTIVE"
                  else "INCONCLUSIVE",
        "claim": None if dress or not complete else "OPENHANDS_SMA_BOUNDARY_QUALIFIED",
        "executionIdentitySha256": fence["identitySha256"],
        "runtimePorts": fence["runtimePorts"], "workspaceIsolation": workspace_isolation,
        "mavenPromotion": maven_promotion, "completedOperations": len(results),
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
        return run_plan(args, c8.c7.dress_rehearsal_plan(json.loads(CORPUS.read_text())),
                        fence, dress=True)
    fence = validate_execution_fence(args.execution_identity, args.authorization)
    return run_plan(args, base.exact_plan(json.loads(CORPUS.read_text())), fence, dress=False)


if __name__ == "__main__":
    raise SystemExit(main())
