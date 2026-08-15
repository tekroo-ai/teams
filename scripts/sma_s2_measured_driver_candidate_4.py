#!/usr/bin/env python3
"""Candidate-6 SMA-S2 driver with a closed runtime-endpoint identity fence.

Candidate 5 remains immutable and supplies the accepted scientific operations.
This successor changes only harness identity and authorization controls.  It is
default-deny and cannot run without a separately accepted identity, a passing
identity-bound live-preflight receipt, and a single-use measured authorization.
"""

from __future__ import annotations

import argparse
import json
import os
import socket
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

import sma_s2_measured_driver_candidate_3 as c5


ROOT = Path(__file__).resolve().parents[1]
DRIVER = Path(__file__).resolve()
CONFIG = ROOT / "investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-6.json"
MATRIX = ROOT / "investigations/sma-q1/layered/sma-s2-predicate-oracle-matrix-candidate-4.json"
CORPUS = ROOT / "investigations/sma-q1/layered/sma-s2-measured-corpus-candidate-1.json"
SEMANTIC_SOURCE = ROOT / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-2.json"
STUB = ROOT / "scripts/sma_s2_deterministic_model_stub_candidate_3.py"
MEASURED_ROOT = Path("/tmp/tekroo-sma-s2-successor-candidate-6")
DATABASE = "sma_s2_successor_candidate_6_measured_20260814"
SEMANTIC = "sma_s2_successor_candidate_6_semantic_20260814"
EPISODIC = "sma_s2_successor_candidate_6_episodic_20260814"
LABEL = "com.tekroo.sma-service-s2-successor-candidate-6"
LOOPBACK = "127.0.0.1"
BRIDGE_PORT = 8130
MIN_PORT = 1024
MAX_PORT = 65535

for module in (c5, c5.c4, c5.c4.base):
    module.CONFIG = CONFIG
    module.MATRIX = MATRIX
    module.CORPUS = CORPUS
    module.STUB = STUB
    module.MEASURED_ROOT = MEASURED_ROOT
    module.DATABASE = DATABASE
    module.SEMANTIC = SEMANTIC
    module.EPISODIC = EPISODIC
    module.LABEL = LABEL

PredicateFailure = c5.PredicateFailure
require = c5.require
sha_file = c5.sha_file
sha_bytes = c5.sha_bytes
canonical_bytes = c5.canonical_bytes
base = c5.base


def load_object(path: Path, label: str) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as error:
        raise PermissionError(f"candidate-6 fence could not read {label}") from error
    if not isinstance(value, dict):
        raise PermissionError(f"candidate-6 fence requires object-shaped {label}")
    return value


def is_numeric_port(value: Any) -> bool:
    return type(value) is int and MIN_PORT <= value <= MAX_PORT


def validate_runtime_ports(value: Any) -> dict[str, Any]:
    ports = value if isinstance(value, dict) else {}
    checks = {
        "stubHostLoopback": ports.get("stubHost") == LOOPBACK,
        "bridgeHostLoopback": ports.get("bridgeHost") == LOOPBACK,
        "stubPortNumeric": is_numeric_port(ports.get("stubPort")),
        "bridgePortNumeric": is_numeric_port(ports.get("bridgePort")),
        "bridgePortContract": ports.get("bridgePort") == BRIDGE_PORT,
        "portsDistinct": ports.get("stubPort") != ports.get("bridgePort"),
        "fieldSetExact": set(ports) == {"stubHost", "stubPort", "bridgeHost", "bridgePort"},
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-6 runtime endpoint identity rejected: {checks}")
    return dict(ports)


def port_is_available(host: str, port: int) -> bool:
    probe = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 0)
        probe.bind((host, port))
        return True
    except OSError:
        return False
    finally:
        probe.close()


def stub_ready_matches(value: Any, ports: dict[str, Any]) -> bool:
    return isinstance(value, dict) \
        and value.get("host") == ports["stubHost"] \
        and type(value.get("port")) is int \
        and value.get("port") == ports["stubPort"]


def referenced_file_matches(reference: Any, expected: Path | None = None) -> bool:
    if not isinstance(reference, dict):
        return False
    path_value = reference.get("path")
    digest = reference.get("sha256")
    if not isinstance(path_value, str) or not isinstance(digest, str):
        return False
    path = Path(path_value)
    if not path.is_file() or sha_file(path) != digest:
        return False
    return expected is None or path.resolve() == expected.resolve() and digest == sha_file(expected)


def dependency_closure_matches(identity: dict[str, Any], preregistration: dict[str, Any]) -> bool:
    actual = identity.get("dependencies")
    expected = preregistration.get("driverDependencyClosure")
    if not isinstance(actual, list) or not isinstance(expected, list) or actual != expected:
        return False
    return bool(actual) and all(referenced_file_matches(item) for item in actual)


class Candidate6Runtime(c5.Candidate5Runtime):
    def __init__(self, output: Path, journal: Any, runtime_ports: dict[str, Any]) -> None:
        self.runtime_ports = validate_runtime_ports(runtime_ports)
        super().__init__(output, journal)
        self.stub_port = self.runtime_ports["stubPort"]

    def preflight_absence(self) -> dict[str, bool]:
        checks = super().preflight_absence()
        checks["identityStubPortAvailable"] = port_is_available(
            self.runtime_ports["stubHost"], self.runtime_ports["stubPort"]
        )
        checks["identityBridgePortAvailable"] = port_is_available(
            self.runtime_ports["bridgeHost"], self.runtime_ports["bridgePort"]
        )
        self.journal.append("PREASSERTION_BOUND_ENDPOINT_ABSENCE", checks)
        require(all(checks.values()), "one or more identity-bound endpoints were occupied before execution")
        return checks

    def start_stub(self) -> None:
        require(
            port_is_available(self.runtime_ports["stubHost"], self.runtime_ports["stubPort"]),
            "identity-bound stub port became occupied before launch",
        )
        stdout = (self.output / "stub-stdout.log").open("w")
        stderr = (self.output / "stub-stderr.log").open("w")
        self.stub_process = subprocess.Popen(
            [
                sys.executable,
                str(STUB),
                "--host",
                self.runtime_ports["stubHost"],
                "--port",
                str(self.runtime_ports["stubPort"]),
                "--raw-journal",
                str(self.raw),
                "--operational-journal",
                str(self.operational),
                "--terminal-journal",
                str(self.terminal),
                "--timeout-delay-ms",
                "5000",
                "--ready-file",
                str(self.ready),
            ],
            stdin=subprocess.DEVNULL,
            stdout=stdout,
            stderr=stderr,
            text=True,
        )
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if self.ready.exists():
                ready = load_object(self.ready, "deterministic-stub ready receipt")
                if not stub_ready_matches(ready, self.runtime_ports):
                    self.stub_process.terminate()
                    self.stub_process.wait(timeout=5)
                    raise RuntimeError("deterministic-stub ready endpoint did not match execution identity")
                self.stub_port = self.runtime_ports["stubPort"]
                return
            if self.stub_process.poll() is not None:
                raise RuntimeError("deterministic stub exited before ready")
            time.sleep(0.02)
        self.stub_process.terminate()
        self.stub_process.wait(timeout=5)
        raise TimeoutError("deterministic stub readiness exceeded five seconds")


def validate_static_contract() -> dict[str, Any]:
    value = c5.validate_static_contract()
    configuration = load_object(CONFIG, "candidate-6 configuration")
    namespaces = configuration["disposableNamespaces"]
    endpoint = configuration["runtimeEndpointIdentity"]
    value["candidate6ConfigurationMatchesDriver"] = (
        namespaces["mongodbDatabase"] == DATABASE
        and namespaces["qdrantSemanticCollection"] == SEMANTIC
        and namespaces["qdrantEpisodicCollection"] == EPISODIC
        and namespaces["workspaceRoot"] == str(MEASURED_ROOT)
    )
    value["candidate6EndpointContract"] = {
        "loopbackExact": endpoint["requiredHost"] == LOOPBACK,
        "bridgePortExact": endpoint["requiredBridgePort"] == BRIDGE_PORT,
        "numericBoundsExact": endpoint["numericPortMinimum"] == MIN_PORT
                              and endpoint["numericPortMaximum"] == MAX_PORT,
        "identityFieldsExact": endpoint["executionIdentityMustBind"]
                               == ["stubHost", "stubPort", "bridgeHost", "bridgePort"],
        "preflightReceiptRequired": configuration["executionFence"]
                                    ["passingBoundLivePreflightReceiptRequired"] is True,
    }
    require(value["candidate6ConfigurationMatchesDriver"], "candidate-6 namespace mismatch")
    require(all(value["candidate6EndpointContract"].values()), "candidate-6 endpoint contract mismatch")
    return value


def validate_execution_identity(identity_path: Path) -> dict[str, Any]:
    identity = load_object(identity_path, "execution identity")
    ports = validate_runtime_ports(identity.get("runtimePorts"))
    prereg_ref = identity.get("preregistration", {})
    acceptance_ref = identity.get("acceptance", {})
    prereg_path = Path(str(prereg_ref.get("path", "")))
    acceptance_path = Path(str(acceptance_ref.get("path", "")))
    preregistration = load_object(prereg_path, "candidate-6 preregistration") if prereg_path.is_file() else {}
    acceptance = load_object(acceptance_path, "candidate-6 acceptance") if acceptance_path.is_file() else {}
    checks = {
        "identityRecordType": identity.get("recordType") == "SMA_S2_CANDIDATE_6_EXECUTION_IDENTITY",
        "identityCandidateExact": identity.get("candidate") == 6,
        "identityAcceptedFrozen": identity.get("status") == "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
        "identityNonAuthorizing": identity.get("measuredExecutionAuthorized") is False,
        "driverMatches": referenced_file_matches(identity.get("driver"), DRIVER),
        "corpusMatches": referenced_file_matches(identity.get("corpus"), CORPUS),
        "configurationMatches": referenced_file_matches(identity.get("configuration"), CONFIG),
        "matrixMatches": referenced_file_matches(identity.get("oracleMatrix"), MATRIX),
        "stubMatches": referenced_file_matches(identity.get("stub"), STUB),
        "preregistrationMatches": referenced_file_matches(prereg_ref)
                                  and preregistration.get("status") == "SUCCESSOR_CANDIDATE_NOT_ACCEPTED_NOT_AUTHORIZED",
        "acceptanceMatches": referenced_file_matches(acceptance_ref)
                             and acceptance.get("status") == "ACCEPTED_FROZEN"
                             and acceptance.get("candidate") == 6
                             and acceptance.get("candidatePreregistrationSha256") == prereg_ref.get("sha256")
                             and acceptance.get("measuredExecutionAuthorized") is False,
        "dependenciesExactAndMatch": dependency_closure_matches(identity, preregistration),
        "stubPortAvailable": port_is_available(ports["stubHost"], ports["stubPort"]),
        "bridgePortAvailable": port_is_available(ports["bridgeHost"], ports["bridgePort"]),
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-6 execution identity rejected: {checks}")
    return {
        "identity": identity,
        "identitySha256": sha_file(identity_path),
        "runtimePorts": ports,
        "preregistration": prereg_ref,
        "acceptance": acceptance_ref,
        "checks": checks,
    }


def validate_live_preflight_fence(identity_path: Path, authorization_path: Path) -> dict[str, Any]:
    bound = validate_execution_identity(identity_path)
    authorization = load_object(authorization_path, "live-preflight authorization")
    checks = {
        "authorizationRecordType": authorization.get("recordType")
                                   == "SMA_S2_CANDIDATE_6_LIVE_PREFLIGHT_AUTHORIZATION",
        "authorizationLive": authorization.get("status") == "AUTHORIZED_NOT_CONSUMED",
        "authorizationIdentityMatches": authorization.get("executionIdentitySha256")
                                        == bound["identitySha256"],
        "authorizationPortsExact": authorization.get("runtimePorts") == bound["runtimePorts"],
        "authorizationDriverMatches": authorization.get("driverSha256") == sha_file(DRIVER),
        "authorizationConfigurationMatches": authorization.get("configurationSha256") == sha_file(CONFIG),
        "authorizationPreregistrationMatches": authorization.get("preregistrationSha256")
                                                == bound["preregistration"].get("sha256"),
        "authorizationAcceptanceMatches": authorization.get("acceptanceSha256")
                                           == bound["acceptance"].get("sha256"),
        "singleAttempt": authorization.get("maximumLivePreflightAttempts") == 1,
        "singleConversation": authorization.get("maximumOpenHandsConversations") == 1,
        "singleStubRequest": authorization.get("maximumDeterministicStubRequests") == 1,
        "noMeasuredCases": authorization.get("measuredCaseExecutions") == 0,
        "noMeasuredRepetitions": authorization.get("measuredRepetitionExecutions") == 0,
        "realModelProhibited": authorization.get("realModelCallsAllowed") is False,
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-6 live-preflight fence rejected: {checks}")
    return {**bound, "preflightAuthorizationChecks": checks}


def validate_execution_fence(identity_path: Path, authorization_path: Path) -> dict[str, Any]:
    bound = validate_execution_identity(identity_path)
    identity = bound["identity"]
    authorization = load_object(authorization_path, "measured authorization")
    ports = bound["runtimePorts"]
    prereg_ref = identity.get("preregistration", {})
    acceptance_ref = identity.get("acceptance", {})
    prereg_path = Path(str(prereg_ref.get("path", "")))
    acceptance_path = Path(str(acceptance_ref.get("path", "")))
    preregistration = load_object(prereg_path, "candidate-6 preregistration") if prereg_path.is_file() else {}
    acceptance = load_object(acceptance_path, "candidate-6 acceptance") if acceptance_path.is_file() else {}
    preflight_ref = authorization.get("livePreflightReceipt", {})
    preflight_path = Path(str(preflight_ref.get("path", "")))
    preflight = load_object(preflight_path, "live-preflight receipt") if preflight_path.is_file() else {}
    preflight_ports = preflight.get("runtimePorts")
    authorization_ports = authorization.get("runtimePorts")
    identity_sha = bound["identitySha256"]
    checks = {
        "authorizationRecordType": authorization.get("recordType")
                                   == "SMA_S2_CANDIDATE_6_SINGLE_USE_MEASURED_AUTHORIZATION",
        "authorizationLive": authorization.get("status") == "AUTHORIZED_NOT_CONSUMED",
        "authorizationSingleUse": authorization.get("maximumMeasuredAttempts") == 1,
        "authorizationIdentityMatches": authorization.get("executionIdentitySha256") == identity_sha,
        "authorizationPortsExact": authorization_ports == ports,
        "authorizationDriverMatches": authorization.get("driverSha256") == sha_file(DRIVER),
        "authorizationMatrixMatches": authorization.get("oracleMatrixSha256") == sha_file(MATRIX),
        "authorizationCorpusMatches": authorization.get("corpusSha256") == sha_file(CORPUS),
        "authorizationConfigurationMatches": authorization.get("configurationSha256") == sha_file(CONFIG),
        "authorizationStubMatches": authorization.get("stubSha256") == sha_file(STUB),
        "authorizationPreregistrationMatches": authorization.get("preregistrationSha256")
                                                == prereg_ref.get("sha256"),
        "authorizationAcceptanceMatches": authorization.get("acceptanceSha256")
                                           == acceptance_ref.get("sha256"),
        "scopeExact": authorization.get("caseCount") == 20
                      and authorization.get("repetitionCount") == 103,
        "realModelProhibited": authorization.get("realModelCallsAllowed") is False,
        "preflightReferenceMatches": referenced_file_matches(preflight_ref),
        "preflightRecordType": preflight.get("recordType") == "SMA_S2_CANDIDATE_6_LIVE_PREFLIGHT_RECEIPT",
        "preflightPass": preflight.get("status") == "PASS",
        "preflightIdentityMatches": preflight.get("executionIdentitySha256") == identity_sha,
        "preflightPortsExact": preflight_ports == ports,
        "preflightAttemptExact": preflight.get("livePreflightAttempts") == 1,
        "preflightStubRequestExact": preflight.get("deterministicStubRequests") == 1,
        "preflightConversationExact": preflight.get("openhandsConversations") == 1,
        "preflightCleanupPass": preflight.get("cleanupStatus") == "PASS",
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-6 execution fence rejected: {checks}")
    return {"identityChecks": bound["checks"], "checks": checks, "runtimePorts": ports,
            "livePreflightReceiptSha256": preflight_ref["sha256"]}


def execute_live_preflight(args: argparse.Namespace) -> int:
    fence = validate_live_preflight_fence(args.execution_identity, args.authorization)
    corpus = json.loads(CORPUS.read_text())
    item = base.exact_plan(corpus)[0]
    require(item["caseId"] == "SMA-S2-001-FIRST-PROMPT-EMPTY", "preflight probe identity changed")
    args.output.mkdir(parents=True, exist_ok=False)
    journal = base.DurableJournal(args.output / "raw-evidence.jsonl")
    journal.append("LIVE_PREFLIGHT_START", {
        "driverSha256": sha_file(DRIVER),
        "executionIdentitySha256": fence["identitySha256"],
        "runtimePorts": fence["runtimePorts"],
    })
    runtime = Candidate6Runtime(args.output, journal, fence["runtimePorts"])
    failure: dict[str, Any] | None = None
    records: list[dict[str, Any]] = []
    try:
        runtime.preflight_absence()
        runtime.start_stub()
        runtime.setup_workspaces()
        records = runtime.run_operation(item)
    except Exception as error:
        failure = {"type": type(error).__name__, "messageSha256": sha_bytes(str(error).encode())}
        journal.append("LIVE_PREFLIGHT_FAILURE", failure)
    finally:
        cleanup = runtime.cleanup()
    request_count = sum(int(record.get("rawStubRequestCount", 0)) for record in records)
    passed = failure is None and len(records) == 1 and request_count == 1 \
        and all(cleanup["oracleValues"].values())
    receipt = {
        "recordType": "SMA_S2_CANDIDATE_6_LIVE_PREFLIGHT_RECEIPT",
        "status": "PASS" if passed else "INCONCLUSIVE",
        "executionIdentitySha256": fence["identitySha256"],
        "runtimePorts": fence["runtimePorts"],
        "livePreflightAttempts": 1,
        "deterministicStubRequests": request_count,
        "openhandsConversations": len(records),
        "cleanupStatus": "PASS" if all(cleanup["oracleValues"].values()) else "FAIL",
        "failure": failure,
        "probeCaseId": item["caseId"],
        "measuredCaseExecutions": 0,
        "measuredRepetitionExecutions": 0,
        "claim": None,
        "rawJournalSha256": sha_file(args.output / "raw-evidence.jsonl"),
    }
    receipt_path = args.output / "live-preflight-receipt.json"
    descriptor = os.open(receipt_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, canonical_bytes(receipt) + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    print(json.dumps({"status": receipt["status"], "receipt": str(receipt_path)}, sort_keys=True))
    return 0 if passed else 1


def execute(args: argparse.Namespace) -> int:
    fence = validate_execution_fence(args.execution_identity, args.authorization)
    corpus = json.loads(CORPUS.read_text())
    plan = base.exact_plan(corpus)
    args.output.mkdir(parents=True, exist_ok=False)
    journal = base.DurableJournal(args.output / "raw-evidence.jsonl")
    journal.append("EXECUTION_START", {
        "driverSha256": sha_file(DRIVER),
        "matrixSha256": sha_file(MATRIX),
        "corpusSha256": sha_file(CORPUS),
        "runtimePorts": fence["runtimePorts"],
        "livePreflightReceiptSha256": fence["livePreflightReceiptSha256"],
        "caseCount": 20,
        "repetitionCount": 103,
    })
    runtime = Candidate6Runtime(args.output, journal, fence["runtimePorts"])
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
                           "repetition": item["repetition"], "type": type(error).__name__,
                           "messageSha256": sha_bytes(str(error).encode())}
                journal.append("REPETITION_FAILURE", failure)
                break
    finally:
        cleanup = runtime.cleanup()
    complete = len(results) == 103 \
        and all(item["status"] == "PASS" for item in results) \
        and all(cleanup["oracleValues"].values())
    receipt = {
        "recordType": "SMA_S2_MEASURED_EXECUTION_RECEIPT",
        "status": "PASS" if complete else "FAIL" if failure and failure["class"] == "SUBSTANTIVE"
                  else "INCONCLUSIVE",
        "claim": "OPENHANDS_SMA_BOUNDARY_QUALIFIED" if complete else None,
        "runtimePorts": fence["runtimePorts"],
        "livePreflightReceiptSha256": fence["livePreflightReceiptSha256"],
        "completedRepetitions": len(results),
        "results": results,
        "failure": failure,
        "cleanup": cleanup,
        "rawJournalSha256": sha_file(args.output / "raw-evidence.jsonl"),
    }
    descriptor = os.open(args.output / "execution-receipt.json", os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, canonical_bytes(receipt) + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    print(json.dumps({"status": receipt["status"],
                      "receipt": str(args.output / "execution-receipt.json")}, sort_keys=True))
    return 0 if complete else 1


def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser()
    value.add_argument("--offline-contract", action="store_true")
    value.add_argument("--live-preflight", action="store_true")
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
    if args.live_preflight:
        if args.execute:
            raise PermissionError("live preflight and measured execution are mutually exclusive")
        if args.execution_identity is None or args.authorization is None or args.output is None:
            raise PermissionError("live preflight requires identity, authorization, and output")
        return execute_live_preflight(args)
    if not args.execute:
        raise PermissionError("default deny: use --offline-contract or separately authorized --execute")
    if args.execution_identity is None or args.authorization is None or args.output is None:
        raise PermissionError("measured execution requires identity, authorization, and output")
    return execute(args)


if __name__ == "__main__":
    raise SystemExit(main())
