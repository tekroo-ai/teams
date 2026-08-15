#!/usr/bin/env python3
"""Offline negative controls for candidate-7 SMA-S2 harness remediation."""

from __future__ import annotations

import argparse
import ast
import copy
import json
import os
import tempfile
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Callable

import sma_s2_measured_driver_candidate_5 as driver


ROOT = Path(__file__).resolve().parents[1]
PREREGISTRATION = ROOT / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-7.json"


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


def event(kind: str, source: str | None, identity: str, text: str = "") -> dict[str, Any]:
    value: dict[str, Any] = {"kind": kind, "id": identity}
    if source is not None:
        value["source"] = source
    if text:
        value["text"] = text
    return value


def make_bundle(root: Path) -> dict[str, Any]:
    prereg = json.loads(PREREGISTRATION.read_text())
    acceptance_path = root / "acceptance.json"
    write_json(acceptance_path, {
        "recordType": "SMA_S2_CANDIDATE_7_ACCEPTANCE", "candidate": 7,
        "status": "ACCEPTED_FROZEN", "candidatePreregistrationSha256": driver.sha_file(PREREGISTRATION),
        "measuredExecutionAuthorized": False,
    })
    ports = {"stubHost": "127.0.0.1", "stubPort": 19127,
             "bridgeHost": "127.0.0.1", "bridgePort": 8130}
    identity_path = root / "identity.json"
    identity = {
        "recordType": "SMA_S2_CANDIDATE_7_EXECUTION_IDENTITY", "candidate": 7,
        "status": "ACCEPTED_FROZEN_EXECUTION_IDENTITY", "measuredExecutionAuthorized": False,
        "driver": reference(driver.DRIVER), "corpus": reference(driver.CORPUS),
        "configuration": reference(driver.CONFIG), "oracleMatrix": reference(driver.MATRIX),
        "stub": reference(driver.STUB), "preregistration": reference(PREREGISTRATION),
        "acceptance": reference(acceptance_path), "dependencies": prereg["driverDependencyClosure"],
        "runtimePorts": ports,
    }
    write_json(identity_path, identity)
    dress_path = root / "dress-receipt.json"
    write_json(dress_path, {
        "recordType": "SMA_S2_CANDIDATE_7_DRESS_REHEARSAL_RECEIPT", "status": "PASS",
        "executionIdentitySha256": driver.sha_file(identity_path), "runtimePorts": ports,
        "completedOperations": 34, "cleanupStatus": "PASS", "claim": None,
        "measuredCaseExecutions": 0, "measuredRepetitionExecutions": 0,
    })
    dress_auth_path = root / "dress-authorization.json"
    dress_auth = {
        "recordType": "SMA_S2_CANDIDATE_7_DRESS_REHEARSAL_AUTHORIZATION",
        "status": "AUTHORIZED_NOT_CONSUMED", "executionIdentitySha256": driver.sha_file(identity_path),
        "runtimePorts": ports, "driverSha256": driver.sha_file(driver.DRIVER),
        "configurationSha256": driver.sha_file(driver.CONFIG),
        "preregistrationSha256": driver.sha_file(PREREGISTRATION),
        "acceptanceSha256": driver.sha_file(acceptance_path),
        "maximumDressRehearsalAttempts": 1, "operationCount": 34,
        "measuredCaseExecutions": 0, "measuredRepetitionExecutions": 0,
        "realModelCallsAllowed": False,
    }
    write_json(dress_auth_path, dress_auth)
    measured_path = root / "measured-authorization.json"
    measured = {
        "recordType": "SMA_S2_CANDIDATE_7_SINGLE_USE_MEASURED_AUTHORIZATION",
        "status": "AUTHORIZED_NOT_CONSUMED", "maximumMeasuredAttempts": 1,
        "executionIdentitySha256": driver.sha_file(identity_path), "runtimePorts": ports,
        "driverSha256": driver.sha_file(driver.DRIVER), "oracleMatrixSha256": driver.sha_file(driver.MATRIX),
        "corpusSha256": driver.sha_file(driver.CORPUS), "configurationSha256": driver.sha_file(driver.CONFIG),
        "stubSha256": driver.sha_file(driver.STUB), "preregistrationSha256": driver.sha_file(PREREGISTRATION),
        "acceptanceSha256": driver.sha_file(acceptance_path), "caseCount": 20, "repetitionCount": 103,
        "realModelCallsAllowed": False, "dressRehearsalReceipt": reference(dress_path),
    }
    write_json(measured_path, measured)
    return {"identity": identity_path, "acceptance": acceptance_path, "dress": dress_path,
            "dressAuthorization": dress_auth_path, "measuredAuthorization": measured_path,
            "ports": ports, "dressAuthValue": dress_auth, "measuredValue": measured}


def main() -> int:
    arguments = argparse.ArgumentParser()
    arguments.add_argument("--output", type=Path, required=True)
    args = arguments.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    tests: list[dict[str, Any]] = []

    tests.append(run_test("static-science-and-34-operation-contract", lambda: require_true(
        driver.validate_static_contract()["candidate7HarnessCorrections"]["dressOperationCount"] == 34,
        "candidate-7 static contract failed",
    )))

    def terminal_helper_controls() -> None:
        item = {"caseId": "SMA-S2-001-FIRST-PROMPT-EMPTY", "repetition": 1,
                "mode": "SUCCESS", "stubMode": "SUCCESS", "literalPrompt": "prompt"}
        user = event("MessageEvent", "user", "u", "prompt")
        hook = event("HookExecutionEvent", None, "h")
        agent = event("MessageEvent", "agent", "a", "STUB_OK:SMA-S2-001-FIRST-PROMPT-EMPTY:1")
        text = lambda value: value.get("text", "")
        require_true(driver.expected_terminal_event(item, [hook, user], user, text) is None,
                     "terminal status without agent event was accepted")
        require_true(driver.expected_terminal_event(item, [hook, user, agent], user, text) == agent,
                     "exact agent terminal event was rejected")
        wrong = event("MessageEvent", "agent", "w", "wrong")
        require_true(driver.expected_terminal_event(item, [hook, user, wrong], user, text) is None,
                     "wrong agent terminal text was accepted")
        fault = dict(item, mode="TIMEOUT", stubMode="TIMEOUT")
        error = event("ConversationErrorEvent", None, "e")
        require_true(driver.expected_terminal_event(fault, [hook, user, error], user, text) == error,
                     "fault terminal error was rejected")
    tests.append(run_test("required-terminal-event-controls", terminal_helper_controls))

    def stable_wait_regression() -> None:
        item = {"caseId": "SMA-S2-001-FIRST-PROMPT-EMPTY", "repetition": 1,
                "mode": "SUCCESS", "stubMode": "SUCCESS", "literalPrompt": "prompt"}
        prompt = driver.base.control_prompt(item)
        user = event("MessageEvent", "user", "u", prompt)
        hook = event("HookExecutionEvent", None, "h")
        hook["hook_input"] = {"message": prompt}
        agent = event("MessageEvent", "agent", "a", "STUB_OK:SMA-S2-001-FIRST-PROMPT-EMPTY:1")
        snapshots = [[hook, user], [hook, user, agent], [hook, user, agent]]
        calls = {"count": 0}
        runtime = driver.Candidate7Runtime.__new__(driver.Candidate7Runtime)
        runtime.events = lambda _conversation: snapshots[min(calls.update(count=calls["count"] + 1)
                                                              or calls["count"] - 1, 2)]
        runtime.raw_for = lambda *_args: [{"requestId": "r"}]
        runtime.terminal = Path("unused")
        runtime.journal = Journal()
        runtime.support = SimpleNamespace(INGRESS="http://unused", session_key=lambda: "secret")
        runtime.p = SimpleNamespace(
            event_text=lambda value: value.get("text", ""),
            http=lambda *_args, **_kwargs: (200, {"execution_status": "finished"}),
        )
        original = driver.base.read_jsonl
        driver.base.read_jsonl = lambda _path: [{"requestId": "r"}]
        try:
            observed = runtime.wait_observation(item, "conversation", timeout=1)
        finally:
            driver.base.read_jsonl = original
        require_true(observed["terminalEventId"] == "a" and observed["stableFetchCount"] == 2,
                     "stable event visibility was not required")
        require_true(calls["count"] >= 3, "wait exited on the pre-agent terminal snapshot")
    tests.append(run_test("fast-terminal-event-visibility-race-regression", stable_wait_regression))

    def bound_source_positive() -> None:
        runtime = driver.Candidate7Runtime.__new__(driver.Candidate7Runtime)
        runtime.stub_port = 19127
        runtime.conversations = []
        runtime.journal = Journal()
        submitted: list[dict[str, Any]] = []
        runtime.create_conversation = lambda _workspace, _port, max_iterations=1: (
            runtime.conversations.append("c") or "c"
        )
        runtime.p = SimpleNamespace(http=lambda *_args, **_kwargs: (
            submitted.append(_args[4]) or (200, {})
        ))
        runtime.support = SimpleNamespace(
            INGRESS="http://unused", session_key=lambda: "secret",
            wait_prompt_audit=lambda *_args, **_kwargs: {"user_event_id": "u", "hook_before_user": True},
        )
        value = runtime._bound_raw_source(Path("/tmp/workspace"), "source", runtime.conversations)
        require_true(value["constructor"] == "CANDIDATE_7_BOUND_DETERMINISTIC",
                     "bound source constructor identity absent")
        require_true(submitted[0]["run"] is False and submitted[0]["content"][0]["text"] == "source",
                     "bound source submission changed")
    tests.append(run_test("bound-corpus-source-positive", bound_source_positive))

    def seed_factory_rebound_and_restored() -> None:
        runtime = driver.Candidate7Runtime.__new__(driver.Candidate7Runtime)
        runtime.seeded = False
        runtime.conversations = []
        runtime.journal = Journal()
        runtime.mapped = {}
        sentinel = lambda *_args: {"constructor": "UNBOUND"}
        fake = SimpleNamespace(create_raw_source=sentinel)
        def seed(corpus: list[dict[str, Any]], conversations: list[str], _journal: Journal) -> dict[str, str]:
            value = fake.create_raw_source(Path("/tmp/workspace"), "text", conversations)
            require_true(value["constructor"] == "CANDIDATE_7_BOUND_DETERMINISTIC",
                         "seed corpus did not use bound factory")
            return {item["memoryId"]: item["memoryId"] for item in corpus}
        fake.seed_corpus = seed
        runtime.p = fake
        runtime._bound_raw_source = lambda *_args: {"constructor": "CANDIDATE_7_BOUND_DETERMINISTIC"}
        runtime.ensure_seeded()
        require_true(fake.create_raw_source is sentinel, "inherited seed factory was not restored")
        require_true(runtime.seeded is True and len(runtime.mapped) == 4, "seed result was not retained")
    tests.append(run_test("seed-factory-bound-and-restored", seed_factory_rebound_and_restored))

    def sanitized_http_failure() -> None:
        runtime = driver.Candidate7Runtime.__new__(driver.Candidate7Runtime)
        runtime.stub_port = 19127
        runtime.conversations = []
        runtime.journal = Journal()
        runtime.create_conversation = lambda *_args, **_kwargs: (
            runtime.conversations.append("c") or "c"
        )
        runtime.p = SimpleNamespace(http=lambda *_args, **_kwargs: (500, {"error_id": "server-secret-id"}))
        runtime.support = SimpleNamespace(INGRESS="http://unused", session_key=lambda: "secret")
        expect_error(RuntimeError, lambda: runtime._bound_raw_source(
            Path("/tmp/workspace"), "source-body", runtime.conversations
        ), "HTTP 500 did not fail closed")
        kind, value = runtime.journal.rows[-1]
        require_true(kind == "HARNESS_BOUNDARY_FAILURE" and value["httpStatus"] == 500,
                     "HTTP failure boundary was not retained")
        require_true("source-body" not in json.dumps(value) and "server-secret-id" not in json.dumps(value),
                     "failure receipt persisted a body or raw error identifier")
    tests.append(run_test("sanitized-http-boundary-failure-evidence", sanitized_http_failure))

    def dress_plan_coverage() -> None:
        plan = driver.dress_rehearsal_plan(json.loads(driver.CORPUS.read_text()))
        counts: dict[str, int] = {}
        for item in plan:
            counts[item["caseId"]] = counts.get(item["caseId"], 0) + 1
        require_true(len(plan) == 34 and len(counts) == 20, "dress plan did not cover every case")
        require_true(counts["SMA-S2-001-FIRST-PROMPT-EMPTY"] == 10,
                     "dress plan lacks ten fast-terminal repetitions")
        require_true(counts["SMA-S2-019-MODEL-STUB-FAULT-MATRIX"] == 4
                     and counts["SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN"] == 3,
                     "dress plan lacks complete scheduled fault coverage")
    tests.append(run_test("dress-rehearsal-plan-complete", dress_plan_coverage))

    def fence_controls() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c7-fences-") as temporary:
            bundle = make_bundle(Path(temporary))
            original = driver.c6.port_is_available
            driver.c6.port_is_available = lambda _host, _port: True
            try:
                dress = driver.validate_dress_rehearsal_fence(bundle["identity"], bundle["dressAuthorization"])
                measured = driver.validate_execution_fence(bundle["identity"], bundle["measuredAuthorization"])
                require_true(all(dress["authorizationChecks"].values()), "positive dress fence failed")
                require_true(all(measured["authorizationChecks"].values()), "positive measured fence failed")
                mutated = copy.deepcopy(bundle["dressAuthValue"])
                mutated["runtimePorts"]["stubPort"] += 1
                write_json(bundle["dressAuthorization"], mutated)
                expect_error(PermissionError, lambda: driver.validate_dress_rehearsal_fence(
                    bundle["identity"], bundle["dressAuthorization"]
                ), "dress endpoint mismatch was accepted")
            finally:
                driver.c6.port_is_available = original
    tests.append(run_test("identity-dress-and-measured-fences-positive-negative", fence_controls))

    def dress_receipt_negative_controls() -> None:
        mutations = [
            lambda value: value.update({"status": "FAIL"}),
            lambda value: value.update({"completedOperations": 33}),
            lambda value: value.update({"cleanupStatus": "FAIL"}),
            lambda value: value.update({"claim": "UNAUTHORIZED"}),
            lambda value: value["runtimePorts"].update({"stubPort": 19128}),
            lambda value: value.update({"measuredCaseExecutions": 1}),
        ]
        for index, mutation in enumerate(mutations):
            with tempfile.TemporaryDirectory(prefix=f"sma-s2-c7-dress-{index}-") as temporary:
                bundle = make_bundle(Path(temporary))
                receipt = json.loads(bundle["dress"].read_text())
                mutation(receipt)
                write_json(bundle["dress"], receipt)
                measured = copy.deepcopy(bundle["measuredValue"])
                measured["dressRehearsalReceipt"] = reference(bundle["dress"])
                write_json(bundle["measuredAuthorization"], measured)
                original = driver.c6.port_is_available
                driver.c6.port_is_available = lambda _host, _port: True
                try:
                    expect_error(PermissionError, lambda: driver.validate_execution_fence(
                        bundle["identity"], bundle["measuredAuthorization"]
                    ), "invalid dress receipt opened measured fence")
                finally:
                    driver.c6.port_is_available = original
    tests.append(run_test("mutated-dress-receipts-rejected", dress_receipt_negative_controls))

    def dependency_and_acceptance_controls() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c7-acceptance-") as temporary:
            bundle = make_bundle(Path(temporary))
            acceptance = json.loads(bundle["acceptance"].read_text())
            acceptance["status"] = "NOT_ACCEPTED"
            write_json(bundle["acceptance"], acceptance)
            original = driver.c6.port_is_available
            driver.c6.port_is_available = lambda _host, _port: True
            try:
                expect_error(PermissionError, lambda: driver.validate_execution_identity(bundle["identity"]),
                             "mutated acceptance opened identity fence")
            finally:
                driver.c6.port_is_available = original
    tests.append(run_test("acceptance-and-dependency-closure-enforced", dependency_and_acceptance_controls))

    def optimization_and_default_deny() -> None:
        sources = [driver.DRIVER.read_text(), Path(__file__).read_text()]
        nodes = [node for source in sources for node in ast.walk(ast.parse(source))
                 if isinstance(node, ast.Assert)]
        require_true(not nodes, "Python assertion statement used in qualification code")
        parsed = driver.parser().parse_args([])
        require_true(not parsed.execute and not parsed.dress_rehearsal,
                     "candidate-7 live mode was not default deny")
    tests.append(run_test("python-O-safe-and-default-deny", optimization_and_default_deny))

    passed = sum(test["status"] == "PASS" for test in tests)
    receipt = {
        "recordType": "SMA_S2_CANDIDATE_7_OFFLINE_HARNESS_QUALIFICATION_RECEIPT",
        "candidate": 7, "status": "PASS" if passed == len(tests) else "FAIL",
        "testCount": len(tests), "passCount": passed, "failCount": len(tests) - passed,
        "pythonOptimizationMode": not __debug__,
        "networkUse": "NONE", "openhandsConversations": 0, "smaServiceInvocations": 0,
        "deterministicStubNetworkInvocations": 0, "dressRehearsalAttempts": 0,
        "measuredExecutionAttempts": 0, "tests": tests,
        "boundArtifacts": {
            "driverSha256": driver.sha_file(driver.DRIVER),
            "configurationSha256": driver.sha_file(driver.CONFIG),
            "offlineHarnessSha256": driver.sha_file(Path(__file__)),
            "candidate6InconclusiveReceiptSha256":
                "62226fd8658dece27e282400dc311813565aca334a1957f31e9b128b55575933",
        },
    }
    path = args.output / "offline-harness-qualification-receipt.json"
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
