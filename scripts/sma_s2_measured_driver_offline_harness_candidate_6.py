#!/usr/bin/env python3
"""Offline controls for candidate-8 SMA-S2 workspace isolation."""

from __future__ import annotations

import argparse
import ast
import copy
import inspect
import json
import os
import tempfile
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Callable

import sma_s2_measured_driver_candidate_6 as driver


ROOT = Path(__file__).resolve().parents[1]
PREREGISTRATION = ROOT / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-8.json"
CANDIDATE7_RECEIPT = ROOT / "OUTPUT/phase-3/sma-s2-candidate-7-offline-harness-qualification-2/offline-harness-qualification-receipt.json"
CANDIDATE7_ADJUDICATION = ROOT / "OUTPUT/phase-3/sma-s2-candidate-7-dress-rehearsal-adjudication.json"


class Journal:
    def __init__(self) -> None:
        self.rows: list[tuple[str, dict[str, Any]]] = []

    def append(self, kind: str, value: dict[str, Any]) -> None:
        self.rows.append((kind, value))


def require_true(value: bool, message: str) -> None:
    if value is not True:
        raise RuntimeError(message)


def expect_error(error_type: type[BaseException], action: Callable[[], Any], message: str) -> None:
    try:
        action()
    except error_type:
        return
    raise RuntimeError(message)


def write_json(path: Path, value: dict[str, Any]) -> None:
    path.write_bytes(driver.canonical_bytes(value) + b"\n")


def reference(path: Path) -> dict[str, str]:
    return {"path": str(path), "sha256": driver.sha_file(path)}


def run_test(name: str, action: Callable[[], None]) -> dict[str, Any]:
    try:
        action()
        return {"name": name, "status": "PASS"}
    except Exception as error:
        return {"name": name, "status": "FAIL", "errorType": type(error).__name__,
                "errorSha256": driver.sha_bytes(str(error).encode())}


def make_bundle(root: Path) -> dict[str, Any]:
    prereg = json.loads(PREREGISTRATION.read_text())
    isolation = driver.expected_workspace_isolation()
    acceptance_path = root / "acceptance.json"
    write_json(acceptance_path, {
        "recordType": "SMA_S2_CANDIDATE_8_ACCEPTANCE", "candidate": 8,
        "status": "ACCEPTED_FROZEN",
        "candidatePreregistrationSha256": driver.sha_file(PREREGISTRATION),
        "measuredExecutionAuthorized": False,
    })
    ports = {"stubHost": "127.0.0.1", "stubPort": 19128,
             "bridgeHost": "127.0.0.1", "bridgePort": 8130}
    identity_path = root / "identity.json"
    write_json(identity_path, {
        "recordType": "SMA_S2_CANDIDATE_8_EXECUTION_IDENTITY", "candidate": 8,
        "status": "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
        "measuredExecutionAuthorized": False,
        "driver": reference(driver.DRIVER), "corpus": reference(driver.CORPUS),
        "configuration": reference(driver.CONFIG), "oracleMatrix": reference(driver.MATRIX),
        "stub": reference(driver.STUB), "preregistration": reference(PREREGISTRATION),
        "acceptance": reference(acceptance_path), "dependencies": prereg["driverDependencyClosure"],
        "runtimePorts": ports, "workspaceIsolation": isolation,
    })
    dress_path = root / "dress-receipt.json"
    write_json(dress_path, {
        "recordType": "SMA_S2_CANDIDATE_8_DRESS_REHEARSAL_RECEIPT", "status": "PASS",
        "executionIdentitySha256": driver.sha_file(identity_path), "runtimePorts": ports,
        "workspaceIsolation": isolation, "completedOperations": 34,
        "cleanupStatus": "PASS", "claim": None,
        "measuredCaseExecutions": 0, "measuredRepetitionExecutions": 0,
    })
    dress_authorization_path = root / "dress-authorization.json"
    dress_authorization = {
        "recordType": "SMA_S2_CANDIDATE_8_DRESS_REHEARSAL_AUTHORIZATION",
        "status": "AUTHORIZED_NOT_CONSUMED",
        "executionIdentitySha256": driver.sha_file(identity_path), "runtimePorts": ports,
        "workspaceIsolation": isolation, "driverSha256": driver.sha_file(driver.DRIVER),
        "configurationSha256": driver.sha_file(driver.CONFIG),
        "preregistrationSha256": driver.sha_file(PREREGISTRATION),
        "acceptanceSha256": driver.sha_file(acceptance_path),
        "maximumDressRehearsalAttempts": 1, "operationCount": 34,
        "measuredCaseExecutions": 0, "measuredRepetitionExecutions": 0,
        "realModelCallsAllowed": False,
    }
    write_json(dress_authorization_path, dress_authorization)
    measured_authorization_path = root / "measured-authorization.json"
    measured_authorization = {
        "recordType": "SMA_S2_CANDIDATE_8_SINGLE_USE_MEASURED_AUTHORIZATION",
        "status": "AUTHORIZED_NOT_CONSUMED", "maximumMeasuredAttempts": 1,
        "executionIdentitySha256": driver.sha_file(identity_path), "runtimePorts": ports,
        "workspaceIsolation": isolation, "driverSha256": driver.sha_file(driver.DRIVER),
        "oracleMatrixSha256": driver.sha_file(driver.MATRIX),
        "corpusSha256": driver.sha_file(driver.CORPUS),
        "configurationSha256": driver.sha_file(driver.CONFIG),
        "stubSha256": driver.sha_file(driver.STUB),
        "preregistrationSha256": driver.sha_file(PREREGISTRATION),
        "acceptanceSha256": driver.sha_file(acceptance_path),
        "caseCount": 20, "repetitionCount": 103, "realModelCallsAllowed": False,
        "dressRehearsalReceipt": reference(dress_path),
    }
    write_json(measured_authorization_path, measured_authorization)
    return {
        "identity": identity_path, "acceptance": acceptance_path, "dress": dress_path,
        "dressAuthorization": dress_authorization_path,
        "measuredAuthorization": measured_authorization_path,
        "dressAuthorizationValue": dress_authorization,
        "measuredAuthorizationValue": measured_authorization,
    }


def main() -> int:
    arguments = argparse.ArgumentParser()
    arguments.add_argument("--output", type=Path, required=True)
    args = arguments.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    tests: list[dict[str, Any]] = []

    def static_contract() -> None:
        value = driver.validate_static_contract()
        require_true(value["caseCount"] == 20 and value["repetitionCount"] == 103,
                     "accepted SMA-S2 science changed")
        require_true(value["candidate8ConfigurationMatchesDriver"] is True,
                     "candidate-8 configuration mismatch")
        require_true(all(value["candidate8WorkspaceIsolation"].values()),
                     "workspace-isolation static check failed")
    tests.append(run_test("static-science-and-workspace-isolation-contract", static_contract))

    def predecessor_evidence() -> None:
        require_true(driver.sha_file(CANDIDATE7_RECEIPT)
                     == "f1061561784e2fd6730e2370365e90e2057afa1a779afa056ca8f5fe87f942b0",
                     "candidate-7 offline receipt changed")
        require_true(driver.sha_file(CANDIDATE7_ADJUDICATION)
                     == "3f499ec0bd3f60d036d2c274444b0da94a6ae029de1f64572f9f6ac6f3ebb6da",
                     "candidate-7 adjudication changed")
        literal = "memory count exceeded expected 4: 10"
        require_true(driver.sha_bytes(literal.encode())
                     == "42aceb4995e5adee6ccccb7f4abf223522506f5f21ad06b88d909d0c57241844",
                     "candidate-7 root-cause digest no longer reconstructs")
    tests.append(run_test("frozen-predecessor-and-root-cause-correspondence", predecessor_evidence))

    def workspace_contract_mutations() -> None:
        mutations = []
        shared = driver.expected_workspace_isolation()
        shared["case1Workspace"] = str(driver.ALPHA)
        mutations.append(shared)
        allowlisted = driver.expected_workspace_isolation()
        allowlisted["corpusCaptureAllowlist"].append(str(driver.EMPTY))
        mutations.append(allowlisted)
        no_alpha = driver.expected_workspace_isolation()
        no_alpha["corpusCaptureAllowlist"] = [str(driver.BETA)]
        mutations.append(no_alpha)
        outside = driver.expected_workspace_isolation()
        outside["case1Workspace"] = "/tmp/outside-candidate-8"
        mutations.append(outside)
        wrong_count = driver.expected_workspace_isolation()
        wrong_count["syntheticCorpusSourceCount"] = 5
        mutations.append(wrong_count)
        for mutation in mutations:
            expect_error(Exception, lambda value=mutation: driver.validate_workspace_isolation(value),
                         "mutated workspace isolation was accepted")
    tests.append(run_test("mutated-workspace-contracts-rejected", workspace_contract_mutations))

    def runtime_allowlist_positive_negative() -> None:
        runtime = driver.Candidate8Runtime.__new__(driver.Candidate8Runtime)
        runtime.empty, runtime.alpha, runtime.beta = driver.EMPTY, driver.ALPHA, driver.BETA
        runtime.p = SimpleNamespace(service_environment=lambda *_args: {
            "SMA_OPENHANDS_WORKSPACE_ALLOWLIST": f"{driver.ALPHA},{driver.BETA}"
        })
        receipt = runtime.workspace_isolation_receipt()
        require_true(receipt["emptyCaptureAllowed"] is False
                     and receipt["alphaCaptureAllowed"] is True
                     and receipt["betaCaptureAllowed"] is True,
                     "positive runtime allowlist rejected")
        runtime.p = SimpleNamespace(service_environment=lambda *_args: {
            "SMA_OPENHANDS_WORKSPACE_ALLOWLIST":
                f"{driver.EMPTY},{driver.ALPHA},{driver.BETA}"
        })
        expect_error(Exception, runtime.workspace_isolation_receipt,
                     "empty workspace in runtime allowlist was accepted")
    tests.append(run_test("runtime-capture-allowlist-positive-negative", runtime_allowlist_positive_negative))

    def case1_routes_to_empty_and_retains_receipt() -> None:
        runtime = driver.Candidate8Runtime.__new__(driver.Candidate8Runtime)
        runtime.empty, runtime.alpha, runtime.beta = driver.EMPTY, driver.ALPHA, driver.BETA
        runtime.journal = Journal()
        runtime.p = SimpleNamespace(service_environment=lambda *_args: {
            "SMA_OPENHANDS_WORKSPACE_ALLOWLIST": f"{driver.ALPHA},{driver.BETA}"
        })
        runtime.support = SimpleNamespace(
            listener_pid=lambda _port: None,
            launch_identity=lambda _label: {"loaded": False},
        )
        seen: list[Path] = []
        runtime.one_prompt = lambda _item, workspace: (
            seen.append(workspace) or {"contextLength": 0, "conversationId": "retained-c1"}
        )
        item = {"caseId": "SMA-S2-001-FIRST-PROMPT-EMPTY", "repetition": 1}
        records = runtime.case_first_prompt_empty(item)
        require_true(seen == [driver.EMPTY] and records[0]["conversationId"] == "retained-c1",
                     "case 1 did not use the dedicated empty workspace")
        kind, evidence = runtime.journal.rows[-1]
        require_true(kind == "PREASSERTION_CASE1_WORKSPACE_ISOLATION"
                     and evidence["emptyCaptureAllowed"] is False,
                     "case-1 isolation evidence absent")
    tests.append(run_test("case1-empty-routing-and-evidence", case1_routes_to_empty_and_retains_receipt))

    def case1_rejects_active_service() -> None:
        runtime = driver.Candidate8Runtime.__new__(driver.Candidate8Runtime)
        runtime.empty, runtime.alpha, runtime.beta = driver.EMPTY, driver.ALPHA, driver.BETA
        runtime.p = SimpleNamespace(service_environment=lambda *_args: {
            "SMA_OPENHANDS_WORKSPACE_ALLOWLIST": f"{driver.ALPHA},{driver.BETA}"
        })
        runtime.support = SimpleNamespace(
            listener_pid=lambda _port: 99,
            launch_identity=lambda _label: {"loaded": True},
        )
        runtime.one_prompt = lambda *_args: {"contextLength": 0, "conversationId": "bad"}
        expect_error(Exception, lambda: runtime.case_first_prompt_empty({
            "caseId": "SMA-S2-001-FIRST-PROMPT-EMPTY", "repetition": 1,
        }), "case 1 ran with capture/retrieval service active")
    tests.append(run_test("case1-active-service-rejected", case1_rejects_active_service))

    def historical_contamination_simulation() -> None:
        isolation = driver.expected_workspace_isolation()
        allowed = set(isolation["corpusCaptureAllowlist"])
        isolated_events = ([{"workspace": str(driver.EMPTY), "role": "case1"}] * 10
                           + [{"workspace": str(driver.ALPHA), "role": "seed"}] * 3
                           + [{"workspace": str(driver.BETA), "role": "seed"}])
        isolated_capture = [item for item in isolated_events if item["workspace"] in allowed]
        require_true(len(isolated_capture) == 4
                     and all(item["role"] == "seed" for item in isolated_capture),
                     "isolated workspace population did not preserve exact four-source seed")
        shared_events = ([{"workspace": str(driver.ALPHA), "role": "case1"}] * 10
                         + isolated_events[10:])
        shared_capture = [item for item in shared_events if item["workspace"] in allowed]
        require_true(len(shared_capture) == 14,
                     "historical shared-workspace contamination was not reconstructed")
        expect_error(RuntimeError, lambda: require_true(len(shared_capture) == 4,
                     "shared workspace exceeded exact four-source seed"),
                     "shared-workspace mutation did not fail exact seed cardinality")
    tests.append(run_test("historical-contamination-isolated-and-mutated",
                          historical_contamination_simulation))

    def source_handler_structure() -> None:
        source = inspect.getsource(driver.Candidate8Runtime.case_first_prompt_empty)
        cleanup_source = inspect.getsource(driver.Candidate8Runtime.cleanup)
        require_true("self.empty" in source and "self.alpha" not in source
                     and "self.beta" not in source,
                     "case-1 handler source is not dedicated to empty workspace")
        require_true("emptyWorkspaceAbsent" in cleanup_source
                     and "C8-WORKSPACE-CLEANUP" in cleanup_source,
                     "empty-workspace cleanup oracle absent")
    tests.append(run_test("handler-routing-and-cleanup-source-structure", source_handler_structure))

    def terminal_and_dress_controls_retained() -> None:
        item = {"caseId": "SMA-S2-001-FIRST-PROMPT-EMPTY", "repetition": 1,
                "mode": "SUCCESS", "stubMode": "SUCCESS"}
        user = {"kind": "MessageEvent", "source": "user", "id": "u", "text": "prompt"}
        agent = {"kind": "MessageEvent", "source": "agent", "id": "a",
                 "text": "STUB_OK:SMA-S2-001-FIRST-PROMPT-EMPTY:1"}
        text = lambda value: value.get("text", "")
        require_true(driver.c7.expected_terminal_event(item, [user], user, text) is None,
                     "terminal event omission was accepted")
        require_true(driver.c7.expected_terminal_event(item, [user, agent], user, text) == agent,
                     "exact terminal event was rejected")
        plan = driver.c7.dress_rehearsal_plan(json.loads(driver.CORPUS.read_text()))
        counts: dict[str, int] = {}
        for operation in plan:
            counts[operation["caseId"]] = counts.get(operation["caseId"], 0) + 1
        require_true(len(plan) == 34 and len(counts) == 20
                     and counts["SMA-S2-001-FIRST-PROMPT-EMPTY"] == 10
                     and counts["SMA-S2-019-MODEL-STUB-FAULT-MATRIX"] == 4
                     and counts["SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN"] == 3,
                     "candidate-7 terminal or dress controls changed")
    tests.append(run_test("terminal-event-and-dress-plan-controls-retained",
                          terminal_and_dress_controls_retained))

    def fence_controls() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c8-fences-") as temporary:
            bundle = make_bundle(Path(temporary))
            original = driver.c7.c6.port_is_available
            driver.c7.c6.port_is_available = lambda _host, _port: True
            try:
                dress = driver.validate_dress_rehearsal_fence(
                    bundle["identity"], bundle["dressAuthorization"]
                )
                measured = driver.validate_execution_fence(
                    bundle["identity"], bundle["measuredAuthorization"]
                )
                require_true(all(dress["authorizationChecks"].values()),
                             "positive dress fence failed")
                require_true(all(measured["authorizationChecks"].values()),
                             "positive measured fence failed")
                mutated = copy.deepcopy(bundle["dressAuthorizationValue"])
                mutated["workspaceIsolation"]["case1Workspace"] = str(driver.ALPHA)
                write_json(bundle["dressAuthorization"], mutated)
                expect_error(PermissionError, lambda: driver.validate_dress_rehearsal_fence(
                    bundle["identity"], bundle["dressAuthorization"]
                ), "dress workspace mismatch was accepted")
            finally:
                driver.c7.c6.port_is_available = original
    tests.append(run_test("identity-dress-measured-workspace-fences", fence_controls))

    def dress_receipt_workspace_mutation() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c8-dress-") as temporary:
            bundle = make_bundle(Path(temporary))
            receipt = json.loads(bundle["dress"].read_text())
            receipt["workspaceIsolation"]["case1Workspace"] = str(driver.ALPHA)
            write_json(bundle["dress"], receipt)
            measured = copy.deepcopy(bundle["measuredAuthorizationValue"])
            measured["dressRehearsalReceipt"] = reference(bundle["dress"])
            write_json(bundle["measuredAuthorization"], measured)
            original = driver.c7.c6.port_is_available
            driver.c7.c6.port_is_available = lambda _host, _port: True
            try:
                expect_error(PermissionError, lambda: driver.validate_execution_fence(
                    bundle["identity"], bundle["measuredAuthorization"]
                ), "workspace-mutated dress receipt opened measured fence")
            finally:
                driver.c7.c6.port_is_available = original
    tests.append(run_test("workspace-mutated-dress-receipt-rejected",
                          dress_receipt_workspace_mutation))

    def identity_workspace_mutation() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c8-identity-") as temporary:
            bundle = make_bundle(Path(temporary))
            identity = json.loads(bundle["identity"].read_text())
            identity["workspaceIsolation"]["case1Workspace"] = str(driver.ALPHA)
            write_json(bundle["identity"], identity)
            original = driver.c7.c6.port_is_available
            driver.c7.c6.port_is_available = lambda _host, _port: True
            try:
                expect_error(PermissionError,
                             lambda: driver.validate_execution_identity(bundle["identity"]),
                             "workspace-mutated identity was accepted")
            finally:
                driver.c7.c6.port_is_available = original
    tests.append(run_test("workspace-mutated-identity-rejected", identity_workspace_mutation))

    def optimization_and_default_deny() -> None:
        sources = [driver.DRIVER.read_text(), Path(__file__).read_text()]
        nodes = [node for source in sources for node in ast.walk(ast.parse(source))
                 if isinstance(node, ast.Assert)]
        require_true(not nodes, "Python assertion statement used in qualification code")
        parsed = driver.parser().parse_args([])
        require_true(not parsed.execute and not parsed.dress_rehearsal,
                     "candidate-8 live mode was not default deny")
    tests.append(run_test("python-O-safe-and-default-deny", optimization_and_default_deny))

    passed = sum(test["status"] == "PASS" for test in tests)
    receipt = {
        "recordType": "SMA_S2_CANDIDATE_8_OFFLINE_WORKSPACE_ISOLATION_QUALIFICATION_RECEIPT",
        "candidate": 8, "status": "PASS" if passed == len(tests) else "FAIL",
        "testCount": len(tests), "passCount": passed, "failCount": len(tests) - passed,
        "pythonOptimizationMode": not __debug__, "networkUse": "NONE",
        "openhandsConversations": 0, "smaServiceInvocations": 0,
        "deterministicStubNetworkInvocations": 0, "dressRehearsalAttempts": 0,
        "measuredExecutionAttempts": 0, "tests": tests,
        "boundArtifacts": {
            "driverSha256": driver.sha_file(driver.DRIVER),
            "configurationSha256": driver.sha_file(driver.CONFIG),
            "offlineHarnessSha256": driver.sha_file(Path(__file__)),
            "candidate7OfflineReceiptSha256": driver.sha_file(CANDIDATE7_RECEIPT),
            "candidate7AdjudicationSha256": driver.sha_file(CANDIDATE7_ADJUDICATION),
        },
    }
    path = args.output / "offline-workspace-isolation-qualification-receipt.json"
    descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, driver.canonical_bytes(receipt) + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    print(json.dumps({"status": receipt["status"], "testCount": len(tests),
                      "passCount": passed, "receipt": str(path)}, sort_keys=True))
    return 0 if receipt["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
