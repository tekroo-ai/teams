#!/usr/bin/env python3
"""Offline candidate-6 harness-identity and endpoint negative controls.

This harness makes no OpenHands, SMA, model, or stub-network calls.  Its only
socket activity is binding loopback ports to prove occupied-port rejection.
"""

from __future__ import annotations

import argparse
import ast
import copy
import json
import os
import socket
import tempfile
from pathlib import Path
from typing import Any, Callable

import sma_s2_measured_driver_candidate_4 as driver


ROOT = Path(__file__).resolve().parents[1]
PREREGISTRATION = ROOT / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-6.json"


def write_json(path: Path, value: dict[str, Any]) -> None:
    path.write_bytes(driver.canonical_bytes(value) + b"\n")


def unused_port() -> int:
    probe = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        probe.bind((driver.LOOPBACK, 0))
        return int(probe.getsockname()[1])
    finally:
        probe.close()


def require_true(value: bool, message: str) -> None:
    if value is not True:
        raise RuntimeError(message)


def expect_rejection(action: Callable[[], Any], message: str) -> None:
    try:
        action()
    except PermissionError:
        return
    raise RuntimeError(message)


def reference(path: Path) -> dict[str, str]:
    return {"path": str(path), "sha256": driver.sha_file(path)}


def make_bundle(root: Path, *, stub_port: int | None = None) -> dict[str, Any]:
    preregistration = json.loads(PREREGISTRATION.read_text())
    acceptance_path = root / "acceptance.json"
    acceptance = {
        "recordType": "SMA_S2_CANDIDATE_6_ACCEPTANCE",
        "candidate": 6,
        "status": "ACCEPTED_FROZEN",
        "candidatePreregistrationSha256": driver.sha_file(PREREGISTRATION),
        "measuredExecutionAuthorized": False,
    }
    write_json(acceptance_path, acceptance)
    ports = {
        "stubHost": driver.LOOPBACK,
        "stubPort": stub_port if stub_port is not None else unused_port(),
        "bridgeHost": driver.LOOPBACK,
        "bridgePort": driver.BRIDGE_PORT,
    }
    identity_path = root / "identity.json"
    identity = {
        "recordType": "SMA_S2_CANDIDATE_6_EXECUTION_IDENTITY",
        "candidate": 6,
        "status": "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
        "measuredExecutionAuthorized": False,
        "driver": reference(driver.DRIVER),
        "corpus": reference(driver.CORPUS),
        "configuration": reference(driver.CONFIG),
        "oracleMatrix": reference(driver.MATRIX),
        "stub": reference(driver.STUB),
        "preregistration": reference(PREREGISTRATION),
        "acceptance": reference(acceptance_path),
        "dependencies": preregistration["driverDependencyClosure"],
        "runtimePorts": ports,
    }
    write_json(identity_path, identity)
    identity_sha = driver.sha_file(identity_path)
    preflight_path = root / "preflight.json"
    preflight = {
        "recordType": "SMA_S2_CANDIDATE_6_LIVE_PREFLIGHT_RECEIPT",
        "status": "PASS",
        "executionIdentitySha256": identity_sha,
        "runtimePorts": copy.deepcopy(ports),
        "livePreflightAttempts": 1,
        "deterministicStubRequests": 1,
        "openhandsConversations": 1,
        "cleanupStatus": "PASS",
    }
    write_json(preflight_path, preflight)
    authorization_path = root / "authorization.json"
    authorization = {
        "recordType": "SMA_S2_CANDIDATE_6_SINGLE_USE_MEASURED_AUTHORIZATION",
        "status": "AUTHORIZED_NOT_CONSUMED",
        "maximumMeasuredAttempts": 1,
        "executionIdentitySha256": identity_sha,
        "runtimePorts": copy.deepcopy(ports),
        "driverSha256": driver.sha_file(driver.DRIVER),
        "oracleMatrixSha256": driver.sha_file(driver.MATRIX),
        "corpusSha256": driver.sha_file(driver.CORPUS),
        "configurationSha256": driver.sha_file(driver.CONFIG),
        "stubSha256": driver.sha_file(driver.STUB),
        "preregistrationSha256": driver.sha_file(PREREGISTRATION),
        "acceptanceSha256": driver.sha_file(acceptance_path),
        "livePreflightReceipt": reference(preflight_path),
        "caseCount": 20,
        "repetitionCount": 103,
        "realModelCallsAllowed": False,
    }
    write_json(authorization_path, authorization)
    return {
        "identityPath": identity_path,
        "authorizationPath": authorization_path,
        "acceptancePath": acceptance_path,
        "preflightPath": preflight_path,
        "identity": identity,
        "authorization": authorization,
        "preflight": preflight,
        "ports": ports,
    }


def write_live_preflight_authorization(root: Path, bundle: dict[str, Any]) -> Path:
    path = root / "live-preflight-authorization.json"
    value = {
        "recordType": "SMA_S2_CANDIDATE_6_LIVE_PREFLIGHT_AUTHORIZATION",
        "status": "AUTHORIZED_NOT_CONSUMED",
        "executionIdentitySha256": driver.sha_file(bundle["identityPath"]),
        "runtimePorts": copy.deepcopy(bundle["ports"]),
        "driverSha256": driver.sha_file(driver.DRIVER),
        "configurationSha256": driver.sha_file(driver.CONFIG),
        "preregistrationSha256": driver.sha_file(PREREGISTRATION),
        "acceptanceSha256": driver.sha_file(bundle["acceptancePath"]),
        "maximumLivePreflightAttempts": 1,
        "maximumOpenHandsConversations": 1,
        "maximumDeterministicStubRequests": 1,
        "measuredCaseExecutions": 0,
        "measuredRepetitionExecutions": 0,
        "realModelCallsAllowed": False,
    }
    write_json(path, value)
    return path


def rewrite_identity(bundle: dict[str, Any], mutate: Callable[[dict[str, Any]], None]) -> None:
    value = copy.deepcopy(bundle["identity"])
    mutate(value)
    write_json(bundle["identityPath"], value)


def rewrite_authorization(bundle: dict[str, Any], mutate: Callable[[dict[str, Any]], None]) -> None:
    value = copy.deepcopy(bundle["authorization"])
    mutate(value)
    write_json(bundle["authorizationPath"], value)


def rewrite_preflight(bundle: dict[str, Any], mutate: Callable[[dict[str, Any]], None],
                      refresh_reference: bool = True) -> None:
    value = copy.deepcopy(bundle["preflight"])
    mutate(value)
    write_json(bundle["preflightPath"], value)
    if refresh_reference:
        rewrite_authorization(bundle, lambda authorization: authorization.update({
            "livePreflightReceipt": reference(bundle["preflightPath"]),
        }))


def run_test(name: str, action: Callable[[], None]) -> dict[str, Any]:
    try:
        action()
        return {"name": name, "status": "PASS"}
    except Exception as error:
        return {
            "name": name,
            "status": "FAIL",
            "errorType": type(error).__name__,
            "errorSha256": driver.sha_bytes(str(error).encode()),
        }


def main() -> int:
    arguments = argparse.ArgumentParser()
    arguments.add_argument("--output", type=Path, required=True)
    args = arguments.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    tests: list[dict[str, Any]] = []

    tests.append(run_test("static-contract-and-candidate5-semantics-preserved", lambda: require_true(
        driver.validate_static_contract()["repetitionCount"] == 103,
        "static contract did not preserve 103 repetitions",
    )))

    def positive_fence() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c6-positive-") as temporary:
            bundle = make_bundle(Path(temporary))
            original = driver.port_is_available
            driver.port_is_available = lambda _host, _port: True
            try:
                result = driver.validate_execution_fence(
                    bundle["identityPath"], bundle["authorizationPath"]
                )
            finally:
                driver.port_is_available = original
            require_true(all(result["checks"].values()), "positive fence did not pass every check")
    tests.append(run_test("complete-bound-identity-fence-positive", positive_fence))

    def live_preflight_fence_controls() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c6-live-preflight-") as temporary:
            root = Path(temporary)
            bundle = make_bundle(root)
            authorization_path = write_live_preflight_authorization(root, bundle)
            original = driver.port_is_available
            driver.port_is_available = lambda _host, _port: True
            try:
                result = driver.validate_live_preflight_fence(bundle["identityPath"], authorization_path)
                require_true(all(result["preflightAuthorizationChecks"].values()),
                             "complete live-preflight authorization did not pass")
                value = json.loads(authorization_path.read_text())
                value["runtimePorts"]["stubPort"] += 1
                write_json(authorization_path, value)
                expect_rejection(lambda: driver.validate_live_preflight_fence(
                    bundle["identityPath"], authorization_path
                ), "mismatched live-preflight port identity was accepted")
            finally:
                driver.port_is_available = original
    tests.append(run_test("live-preflight-authorization-port-binding-enforced",
                          live_preflight_fence_controls))

    def missing_endpoint_fields() -> None:
        for field in ("stubHost", "stubPort", "bridgeHost", "bridgePort"):
            value = {"stubHost": driver.LOOPBACK, "stubPort": 19091,
                     "bridgeHost": driver.LOOPBACK, "bridgePort": driver.BRIDGE_PORT}
            del value[field]
            expect_rejection(lambda candidate=value: driver.validate_runtime_ports(candidate),
                             f"missing {field} was accepted")
    tests.append(run_test("missing-endpoint-fields-rejected", missing_endpoint_fields))

    def mutated_endpoint_values() -> None:
        invalid = [
            {"stubHost": "0.0.0.0", "stubPort": 19091,
             "bridgeHost": driver.LOOPBACK, "bridgePort": driver.BRIDGE_PORT},
            {"stubHost": driver.LOOPBACK, "stubPort": "19091",
             "bridgeHost": driver.LOOPBACK, "bridgePort": driver.BRIDGE_PORT},
            {"stubHost": driver.LOOPBACK, "stubPort": 0,
             "bridgeHost": driver.LOOPBACK, "bridgePort": driver.BRIDGE_PORT},
            {"stubHost": driver.LOOPBACK, "stubPort": 19091,
             "bridgeHost": "localhost", "bridgePort": driver.BRIDGE_PORT},
            {"stubHost": driver.LOOPBACK, "stubPort": 19091,
             "bridgeHost": driver.LOOPBACK, "bridgePort": 8131},
            {"stubHost": driver.LOOPBACK, "stubPort": driver.BRIDGE_PORT,
             "bridgeHost": driver.LOOPBACK, "bridgePort": driver.BRIDGE_PORT},
        ]
        for value in invalid:
            expect_rejection(lambda candidate=value: driver.validate_runtime_ports(candidate),
                             "mutated endpoint identity was accepted")
    tests.append(run_test("mutated-and-reused-endpoint-values-rejected", mutated_endpoint_values))

    def authorization_port_mismatch() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c6-auth-port-") as temporary:
            bundle = make_bundle(Path(temporary))
            rewrite_authorization(bundle, lambda value: value["runtimePorts"].update({
                "stubPort": value["runtimePorts"]["stubPort"] + 1,
            }))
            original = driver.port_is_available
            driver.port_is_available = lambda _host, _port: True
            try:
                expect_rejection(lambda: driver.validate_execution_fence(
                    bundle["identityPath"], bundle["authorizationPath"]
                ), "authorization/identity port mismatch was accepted")
            finally:
                driver.port_is_available = original
    tests.append(run_test("authorization-identity-port-mismatch-rejected", authorization_port_mismatch))

    def preflight_port_mismatch() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c6-preflight-port-") as temporary:
            bundle = make_bundle(Path(temporary))
            rewrite_preflight(bundle, lambda value: value["runtimePorts"].update({
                "stubPort": value["runtimePorts"]["stubPort"] + 1,
            }))
            original = driver.port_is_available
            driver.port_is_available = lambda _host, _port: True
            try:
                expect_rejection(lambda: driver.validate_execution_fence(
                    bundle["identityPath"], bundle["authorizationPath"]
                ), "preflight/identity port mismatch was accepted")
            finally:
                driver.port_is_available = original
    tests.append(run_test("preflight-identity-port-mismatch-rejected", preflight_port_mismatch))

    def missing_and_mutated_preflight() -> None:
        mutations = [
            lambda value: value.update({"status": "FAIL"}),
            lambda value: value.update({"executionIdentitySha256": "0" * 64}),
            lambda value: value.update({"livePreflightAttempts": 0}),
            lambda value: value.update({"deterministicStubRequests": 2}),
            lambda value: value.update({"openhandsConversations": 0}),
            lambda value: value.update({"cleanupStatus": "FAIL"}),
        ]
        for index, mutation in enumerate(mutations):
            with tempfile.TemporaryDirectory(prefix=f"sma-s2-c6-preflight-{index}-") as temporary:
                bundle = make_bundle(Path(temporary))
                rewrite_preflight(bundle, mutation)
                original = driver.port_is_available
                driver.port_is_available = lambda _host, _port: True
                try:
                    expect_rejection(lambda: driver.validate_execution_fence(
                        bundle["identityPath"], bundle["authorizationPath"]
                    ), "invalid preflight receipt was accepted")
                finally:
                    driver.port_is_available = original
        with tempfile.TemporaryDirectory(prefix="sma-s2-c6-missing-preflight-") as temporary:
            bundle = make_bundle(Path(temporary))
            bundle["preflightPath"].unlink()
            original = driver.port_is_available
            driver.port_is_available = lambda _host, _port: True
            try:
                expect_rejection(lambda: driver.validate_execution_fence(
                    bundle["identityPath"], bundle["authorizationPath"]
                ), "missing preflight receipt was accepted")
            finally:
                driver.port_is_available = original
    tests.append(run_test("missing-and-mutated-preflight-receipts-rejected", missing_and_mutated_preflight))

    def occupied_stub_port() -> None:
        blocker = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        blocker.bind((driver.LOOPBACK, 0))
        try:
            port = int(blocker.getsockname()[1])
            require_true(driver.port_is_available(driver.LOOPBACK, port) is False,
                         "occupied stub port was reported available")
            with tempfile.TemporaryDirectory(prefix="sma-s2-c6-occupied-stub-") as temporary:
                bundle = make_bundle(Path(temporary), stub_port=port)
                expect_rejection(lambda: driver.validate_execution_fence(
                    bundle["identityPath"], bundle["authorizationPath"]
                ), "occupied identity-bound stub port opened the fence")
        finally:
            blocker.close()
    tests.append(run_test("occupied-stub-port-rejected", occupied_stub_port))

    def occupied_bridge_port() -> None:
        blocker: socket.socket | None = None
        if driver.port_is_available(driver.LOOPBACK, driver.BRIDGE_PORT):
            blocker = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            blocker.bind((driver.LOOPBACK, driver.BRIDGE_PORT))
        try:
            require_true(driver.port_is_available(driver.LOOPBACK, driver.BRIDGE_PORT) is False,
                         "occupied bridge port was reported available")
            with tempfile.TemporaryDirectory(prefix="sma-s2-c6-occupied-bridge-") as temporary:
                bundle = make_bundle(Path(temporary))
                expect_rejection(lambda: driver.validate_execution_fence(
                    bundle["identityPath"], bundle["authorizationPath"]
                ), "occupied identity-bound bridge port opened the fence")
        finally:
            if blocker is not None:
                blocker.close()
    tests.append(run_test("occupied-bridge-port-rejected", occupied_bridge_port))

    def ready_receipt_controls() -> None:
        ports = {"stubHost": driver.LOOPBACK, "stubPort": 19091,
                 "bridgeHost": driver.LOOPBACK, "bridgePort": driver.BRIDGE_PORT}
        require_true(driver.stub_ready_matches({"host": driver.LOOPBACK, "port": 19091}, ports),
                     "matching ready receipt was rejected")
        require_true(not driver.stub_ready_matches({"host": driver.LOOPBACK, "port": 19092}, ports),
                     "mismatched ready port was accepted")
        require_true(not driver.stub_ready_matches({"host": "localhost", "port": 19091}, ports),
                     "mismatched ready host was accepted")
        require_true(not driver.stub_ready_matches({"host": driver.LOOPBACK}, ports),
                     "missing ready port was accepted")
    tests.append(run_test("stub-ready-endpoint-match-enforced", ready_receipt_controls))

    def acceptance_and_hash_controls() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c6-acceptance-") as temporary:
            bundle = make_bundle(Path(temporary))
            acceptance = json.loads(bundle["acceptancePath"].read_text())
            acceptance["status"] = "NOT_ACCEPTED"
            write_json(bundle["acceptancePath"], acceptance)
            original = driver.port_is_available
            driver.port_is_available = lambda _host, _port: True
            try:
                expect_rejection(lambda: driver.validate_execution_fence(
                    bundle["identityPath"], bundle["authorizationPath"]
                ), "mutated acceptance opened the fence")
            finally:
                driver.port_is_available = original
        with tempfile.TemporaryDirectory(prefix="sma-s2-c6-hash-") as temporary:
            bundle = make_bundle(Path(temporary))
            rewrite_authorization(bundle, lambda value: value.update({"driverSha256": "0" * 64}))
            original = driver.port_is_available
            driver.port_is_available = lambda _host, _port: True
            try:
                expect_rejection(lambda: driver.validate_execution_fence(
                    bundle["identityPath"], bundle["authorizationPath"]
                ), "mutated driver hash opened the fence")
            finally:
                driver.port_is_available = original
    tests.append(run_test("acceptance-and-bound-hash-mutations-rejected", acceptance_and_hash_controls))

    def default_deny_and_no_assert() -> None:
        source = driver.DRIVER.read_text()
        harness_source = Path(__file__).read_text()
        assertion_nodes = [
            node
            for text in (source, harness_source)
            for node in ast.walk(ast.parse(text))
            if isinstance(node, ast.Assert)
        ]
        require_true(not assertion_nodes, "Python assertion statement was used in qualification logic")
        parsed = driver.parser().parse_args([])
        require_true(parsed.execute is False, "driver execution was not default deny")
    tests.append(run_test("python-O-safe-and-default-deny", default_deny_and_no_assert))

    passed = sum(test["status"] == "PASS" for test in tests)
    receipt = {
        "recordType": "SMA_S2_CANDIDATE_6_OFFLINE_HARNESS_IDENTITY_QUALIFICATION_RECEIPT",
        "status": "PASS" if passed == len(tests) else "FAIL",
        "candidate": 6,
        "testCount": len(tests),
        "passCount": passed,
        "failCount": len(tests) - passed,
        "pythonOptimizationMode": not __debug__,
        "networkUse": "NO_CONNECT_OR_SERVICE_CALLS; LOOPBACK_BIND_ONLY_FOR_OCCUPIED_PORT_CONTROLS",
        "livePreflightAttempts": 0,
        "measuredExecutionAttempts": 0,
        "openhandsConversations": 0,
        "smaServiceInvocations": 0,
        "deterministicStubNetworkInvocations": 0,
        "tests": tests,
        "boundArtifacts": {
            "driverSha256": driver.sha_file(driver.DRIVER),
            "configurationSha256": driver.sha_file(driver.CONFIG),
            "offlineHarnessSha256": driver.sha_file(Path(__file__)),
            "candidate5SemanticQualificationSha256":
                "6437c515fca3742a784d90ba7c2eba4d8d675016a2e92ad07266492fb8890e90",
        },
    }
    receipt_path = args.output / "offline-harness-identity-qualification-receipt.json"
    descriptor = os.open(receipt_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, driver.canonical_bytes(receipt) + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    print(json.dumps({"status": receipt["status"], "testCount": len(tests),
                      "passCount": passed, "receipt": str(receipt_path)}, sort_keys=True))
    return 0 if receipt["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
