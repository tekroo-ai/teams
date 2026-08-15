#!/usr/bin/env python3
"""Offline controls for candidate-9 Maven working-directory remediation."""

from __future__ import annotations

import argparse
import ast
import copy
import inspect
import json
import os
import subprocess
import tempfile
from pathlib import Path
from typing import Any, Callable

import sma_s2_measured_driver_candidate_7 as driver


ROOT = Path(__file__).resolve().parents[1]
CANDIDATE8_CONFIG = ROOT / "investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-8.json"
CANDIDATE8_DRIVER = ROOT / "scripts/sma_s2_measured_driver_candidate_6.py"
CANDIDATE8_OFFLINE = ROOT / "OUTPUT/phase-3/sma-s2-candidate-8-offline-workspace-isolation-qualification-2/offline-workspace-isolation-qualification-receipt.json"
CANDIDATE8_DRESS = ROOT / "OUTPUT/phase-3/sma-s2-candidate-8-dress-rehearsal-attempt-1/dress-rehearsal-receipt.json"
CANDIDATE8_ADJUDICATION = ROOT / "OUTPUT/phase-3/sma-s2-candidate-8-dress-rehearsal-adjudication.json"


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


def runtime_for_promotion() -> driver.Candidate9Runtime:
    runtime = driver.Candidate9Runtime.__new__(driver.Candidate9Runtime)
    runtime.parent_working_directory = Path.cwd()
    runtime.journal = Journal()
    return runtime


def make_bundle(root: Path) -> dict[str, Any]:
    dependencies = [{
        "role": "CANDIDATE_8_DRIVER_WORKSPACE_ISOLATION_LIBRARY_NO_RESULT_CREDIT",
        "path": str(CANDIDATE8_DRIVER),
        "sha256": driver.sha_file(CANDIDATE8_DRIVER),
    }]
    preregistration = root / "preregistration.json"
    write_json(preregistration, {"candidate": 9,
                                 "driverDependencyClosure": dependencies})
    acceptance = root / "acceptance.json"
    write_json(acceptance, {
        "recordType": "SMA_S2_CANDIDATE_9_ACCEPTANCE", "candidate": 9,
        "status": "ACCEPTED_FROZEN",
        "candidatePreregistrationSha256": driver.sha_file(preregistration),
        "measuredExecutionAuthorized": False,
    })
    ports = {"stubHost": "127.0.0.1", "stubPort": 19129,
             "bridgeHost": "127.0.0.1", "bridgePort": 8130}
    isolation = driver.expected_workspace_isolation()
    maven = driver.expected_maven_promotion()
    identity = root / "identity.json"
    write_json(identity, {
        "recordType": "SMA_S2_CANDIDATE_9_EXECUTION_IDENTITY", "candidate": 9,
        "status": "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
        "measuredExecutionAuthorized": False,
        "driver": reference(driver.DRIVER), "corpus": reference(driver.CORPUS),
        "configuration": reference(driver.CONFIG), "oracleMatrix": reference(driver.MATRIX),
        "stub": reference(driver.STUB), "preregistration": reference(preregistration),
        "acceptance": reference(acceptance), "dependencies": dependencies,
        "runtimePorts": ports, "workspaceIsolation": isolation, "mavenPromotion": maven,
    })
    dress = root / "dress.json"
    write_json(dress, {
        "recordType": "SMA_S2_CANDIDATE_9_DRESS_REHEARSAL_RECEIPT", "status": "PASS",
        "executionIdentitySha256": driver.sha_file(identity), "runtimePorts": ports,
        "workspaceIsolation": isolation, "mavenPromotion": maven,
        "completedOperations": 34, "cleanupStatus": "PASS", "claim": None,
        "measuredCaseExecutions": 0, "measuredRepetitionExecutions": 0,
    })
    dress_authorization = root / "dress-authorization.json"
    dress_value = {
        "recordType": "SMA_S2_CANDIDATE_9_DRESS_REHEARSAL_AUTHORIZATION",
        "status": "AUTHORIZED_NOT_CONSUMED",
        "executionIdentitySha256": driver.sha_file(identity), "runtimePorts": ports,
        "workspaceIsolation": isolation, "mavenPromotion": maven,
        "driverSha256": driver.sha_file(driver.DRIVER),
        "configurationSha256": driver.sha_file(driver.CONFIG),
        "preregistrationSha256": driver.sha_file(preregistration),
        "acceptanceSha256": driver.sha_file(acceptance),
        "maximumDressRehearsalAttempts": 1, "operationCount": 34,
        "measuredCaseExecutions": 0, "measuredRepetitionExecutions": 0,
        "realModelCallsAllowed": False,
    }
    write_json(dress_authorization, dress_value)
    measured_authorization = root / "measured-authorization.json"
    measured_value = {
        "recordType": "SMA_S2_CANDIDATE_9_SINGLE_USE_MEASURED_AUTHORIZATION",
        "status": "AUTHORIZED_NOT_CONSUMED", "maximumMeasuredAttempts": 1,
        "executionIdentitySha256": driver.sha_file(identity), "runtimePorts": ports,
        "workspaceIsolation": isolation, "mavenPromotion": maven,
        "driverSha256": driver.sha_file(driver.DRIVER),
        "oracleMatrixSha256": driver.sha_file(driver.MATRIX),
        "corpusSha256": driver.sha_file(driver.CORPUS),
        "configurationSha256": driver.sha_file(driver.CONFIG),
        "stubSha256": driver.sha_file(driver.STUB),
        "preregistrationSha256": driver.sha_file(preregistration),
        "acceptanceSha256": driver.sha_file(acceptance),
        "caseCount": 20, "repetitionCount": 103, "realModelCallsAllowed": False,
        "dressRehearsalReceipt": reference(dress),
    }
    write_json(measured_authorization, measured_value)
    return {"identity": identity, "dress": dress,
            "dressAuthorization": dress_authorization,
            "measuredAuthorization": measured_authorization,
            "dressValue": dress_value, "measuredValue": measured_value}


def main() -> int:
    arguments = argparse.ArgumentParser()
    arguments.add_argument("--output", type=Path, required=True)
    args = arguments.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    tests: list[dict[str, Any]] = []

    def static_contract() -> None:
        value = driver.validate_static_contract()
        require_true(value["caseCount"] == 20 and value["repetitionCount"] == 103,
                     "accepted science changed")
        require_true(all(value["candidate9WorkspaceIsolation"].values()),
                     "workspace isolation changed")
        require_true(all(value["candidate9MavenPromotion"].values()),
                     "Maven promotion contract invalid")
    tests.append(run_test("static-science-workspace-and-maven-contract", static_contract))

    def predecessor_correspondence() -> None:
        require_true(driver.sha_file(CANDIDATE8_CONFIG)
                     == "3cc553c6a618b2e4ca6151906c700805303012ef0cb49d270c33291edd517f44",
                     "candidate-8 configuration changed")
        require_true(driver.sha_file(CANDIDATE8_DRIVER)
                     == "717d29b96107c7cc86c1aeceeeb9b32ab9aa8776bf6b3ec18496dd86a0305d38",
                     "candidate-8 driver changed")
        require_true(driver.sha_file(CANDIDATE8_OFFLINE)
                     == "c2ae00d8e23944f9e7fb62ba706bc3b2afe9c24929021904b5d466e8bb990175",
                     "candidate-8 offline receipt changed")
        require_true(driver.sha_file(CANDIDATE8_DRESS)
                     == "f91a7f8c7c02cbb70be302dba78bdc1f8ca6ff2ed6b8ec1cb6df882d08a29cca",
                     "candidate-8 dress receipt changed")
        require_true(driver.sha_file(CANDIDATE8_ADJUDICATION)
                     == "c0e1dd3cef5f33864630b61ea9a20f900c844f5eef29f9ed003b977fd3aa63b8",
                     "candidate-8 adjudication changed")
    tests.append(run_test("frozen-candidate8-correspondence", predecessor_correspondence))

    def maven_contract_mutations() -> None:
        mutations: list[Any] = [None]
        for field, value in (("projectDirectory", "/tmp/wrong"),
                             ("pomPath", "/tmp/wrong/pom.xml"),
                             ("subprocessWorkingDirectoryMustBeExplicit", False),
                             ("parentWorkingDirectoryMayChange", True)):
            mutation = driver.expected_maven_promotion()
            mutation[field] = value
            mutations.append(mutation)
        evidence = driver.expected_maven_promotion()
        evidence["failureEvidence"]["stderrSha256Required"] = False
        mutations.append(evidence)
        for mutation in mutations:
            expect_error(Exception,
                         lambda value=mutation: driver.validate_maven_promotion(
                             value, require_files=False),
                         "mutated Maven contract accepted")
    tests.append(run_test("missing-and-mutated-maven-contracts-rejected",
                          maven_contract_mutations))

    def missing_and_non_maven_directories() -> None:
        original_project, original_pom = driver.MAVEN_PROJECT, driver.MAVEN_POM
        try:
            with tempfile.TemporaryDirectory(prefix="sma-s2-c9-nonmaven-") as temporary:
                root = Path(temporary)
                driver.MAVEN_PROJECT = root / "missing"
                driver.MAVEN_POM = driver.MAVEN_PROJECT / "pom.xml"
                expect_error(Exception, lambda: driver.validate_maven_promotion(
                    driver.expected_maven_promotion()), "missing project accepted")
                driver.MAVEN_PROJECT = root
                driver.MAVEN_POM = root / "pom.xml"
                expect_error(Exception, lambda: driver.validate_maven_promotion(
                    driver.expected_maven_promotion()), "non-Maven directory accepted")
        finally:
            driver.MAVEN_PROJECT, driver.MAVEN_POM = original_project, original_pom
    tests.append(run_test("missing-and-non-maven-directories-rejected",
                          missing_and_non_maven_directories))

    def explicit_cwd_success_without_maven() -> None:
        runtime = runtime_for_promotion()
        observed: dict[str, Any] = {}
        original = driver.subprocess.run
        def fake_run(command: list[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
            observed.update(kwargs)
            return subprocess.CompletedProcess(command, 0, stdout="ok", stderr="")
        driver.subprocess.run = fake_run
        before = Path.cwd()
        try:
            receipt = runtime.promote_memory_bound("db", "semantic", "episodic",
                                                   "memory", "code", "checksum")
        finally:
            driver.subprocess.run = original
        require_true(observed.get("cwd") == driver.MAVEN_PROJECT,
                     "subprocess cwd was not explicit")
        require_true(Path.cwd() == before and receipt["parent_working_directory_unchanged"] is True,
                     "parent cwd changed")
        require_true(receipt["exit_code"] == 0 and receipt["stdout_sha256"]
                     == driver.sha_bytes(b"ok"), "success receipt incomplete")
    tests.append(run_test("explicit-subprocess-cwd-success-and-parent-restoration",
                          explicit_cwd_success_without_maven))

    def sanitized_called_process_failure() -> None:
        runtime = runtime_for_promotion()
        original = driver.subprocess.run
        driver.subprocess.run = lambda command, **_kwargs: (_ for _ in ()).throw(
            subprocess.CalledProcessError(7, command, output="SECRET-OUT", stderr="SECRET-ERR"))
        before = Path.cwd()
        try:
            expect_error(subprocess.CalledProcessError,
                         lambda: runtime.promote_memory_bound(
                             "db", "semantic", "episodic", "memory", "code", "checksum"),
                         "CalledProcessError was swallowed")
        finally:
            driver.subprocess.run = original
        kind, evidence = runtime.journal.rows[-1]
        serialized = json.dumps(evidence, sort_keys=True)
        require_true(kind == "MAVEN_PROMOTION_SUBPROCESS_FAILURE"
                     and evidence["exitStatus"] == 7
                     and evidence["stdoutLength"] == len("SECRET-OUT")
                     and evidence["stderrLength"] == len("SECRET-ERR")
                     and evidence["stdoutSha256"] == driver.sha_bytes(b"SECRET-OUT")
                     and evidence["stderrSha256"] == driver.sha_bytes(b"SECRET-ERR")
                     and "SECRET-OUT" not in serialized and "SECRET-ERR" not in serialized
                     and evidence["rawStdoutOrStderrRetained"] is False
                     and Path.cwd() == before,
                     "sanitized failure evidence incomplete or leaked raw output")
    tests.append(run_test("called-process-failure-sanitized-and-retained",
                          sanitized_called_process_failure))

    def sanitized_timeout_failure() -> None:
        runtime = runtime_for_promotion()
        original = driver.subprocess.run
        driver.subprocess.run = lambda command, **_kwargs: (_ for _ in ()).throw(
            subprocess.TimeoutExpired(command, 900, output=b"OUT", stderr=b"ERR"))
        try:
            expect_error(subprocess.TimeoutExpired,
                         lambda: runtime.promote_memory_bound(
                             "db", "semantic", "episodic", "memory", "code", "checksum"),
                         "TimeoutExpired was swallowed")
        finally:
            driver.subprocess.run = original
        kind, evidence = runtime.journal.rows[-1]
        require_true(kind == "MAVEN_PROMOTION_SUBPROCESS_FAILURE"
                     and evidence["timedOut"] is True and evidence["exitStatus"] is None
                     and evidence["stdoutSha256"] == driver.sha_bytes(b"OUT")
                     and evidence["stderrSha256"] == driver.sha_bytes(b"ERR"),
                     "timeout failure evidence incomplete")
    tests.append(run_test("timeout-failure-sanitized-and-retained",
                          sanitized_timeout_failure))

    def source_structure() -> None:
        source = inspect.getsource(driver.Candidate9Runtime.promote_memory_bound)
        require_true("cwd=MAVEN_PROJECT" in source and "os.chdir" not in source,
                     "explicit subprocess cwd source structure absent")
        require_true("MAVEN_PROMOTION_SUBPROCESS_FAILURE" in source,
                     "failure evidence path absent")
    tests.append(run_test("explicit-cwd-and-no-parent-chdir-source-structure", source_structure))

    def candidate8_workspace_isolation_preserved() -> None:
        value = driver.validate_workspace_isolation(driver.expected_workspace_isolation())
        require_true(all(value.values()), "candidate-8 workspace correction not preserved")
        runtime_source = inspect.getsource(driver.c8.Candidate8Runtime.case_first_prompt_empty)
        require_true("self.empty" in runtime_source and "self.alpha" not in runtime_source,
                     "case-1 empty routing changed")
    tests.append(run_test("candidate8-workspace-isolation-preserved",
                          candidate8_workspace_isolation_preserved))

    def identity_and_authorization_fences() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c9-fences-") as temporary:
            bundle = make_bundle(Path(temporary))
            original = driver.c8.c7.c6.port_is_available
            driver.c8.c7.c6.port_is_available = lambda _host, _port: True
            try:
                require_true(all(driver.validate_dress_rehearsal_fence(
                    bundle["identity"], bundle["dressAuthorization"]
                )["authorizationChecks"].values()), "positive dress fence failed")
                require_true(all(driver.validate_execution_fence(
                    bundle["identity"], bundle["measuredAuthorization"]
                )["authorizationChecks"].values()), "positive measured fence failed")
                mutated = copy.deepcopy(bundle["dressValue"])
                mutated["mavenPromotion"]["projectDirectory"] = "/tmp/wrong"
                write_json(bundle["dressAuthorization"], mutated)
                expect_error(PermissionError, lambda: driver.validate_dress_rehearsal_fence(
                    bundle["identity"], bundle["dressAuthorization"]),
                    "Maven-mutated dress authorization accepted")
            finally:
                driver.c8.c7.c6.port_is_available = original
    tests.append(run_test("identity-dress-measured-maven-fences",
                          identity_and_authorization_fences))

    def mutated_identity_and_receipt_rejected() -> None:
        with tempfile.TemporaryDirectory(prefix="sma-s2-c9-mutated-") as temporary:
            bundle = make_bundle(Path(temporary))
            original = driver.c8.c7.c6.port_is_available
            driver.c8.c7.c6.port_is_available = lambda _host, _port: True
            try:
                identity = json.loads(bundle["identity"].read_text())
                identity["mavenPromotion"]["pomPath"] = "/tmp/wrong/pom.xml"
                write_json(bundle["identity"], identity)
                expect_error(PermissionError,
                             lambda: driver.validate_execution_identity(bundle["identity"]),
                             "Maven-mutated identity accepted")
            finally:
                driver.c8.c7.c6.port_is_available = original
    tests.append(run_test("maven-mutated-identity-rejected",
                          mutated_identity_and_receipt_rejected))

    def exact_plan_retained() -> None:
        plan = driver.c8.c7.dress_rehearsal_plan(json.loads(driver.CORPUS.read_text()))
        require_true(len(plan) == 34
                     and sum(x["caseId"] == "SMA-S2-001-FIRST-PROMPT-EMPTY" for x in plan) == 10
                     and len(driver.base.exact_plan(json.loads(driver.CORPUS.read_text()))) == 103,
                     "dress or measured plan changed")
    tests.append(run_test("exact-34-operation-and-103-repetition-plans-retained",
                          exact_plan_retained))

    def optimization_and_default_deny() -> None:
        sources = [driver.DRIVER.read_text(), Path(__file__).read_text()]
        nodes = [node for source in sources for node in ast.walk(ast.parse(source))
                 if isinstance(node, ast.Assert)]
        require_true(not nodes, "Python assertion used in qualification code")
        parsed = driver.parser().parse_args([])
        require_true(not parsed.execute and not parsed.dress_rehearsal,
                     "live mode not default deny")
    tests.append(run_test("python-O-safe-and-default-deny", optimization_and_default_deny))

    passed = sum(test["status"] == "PASS" for test in tests)
    receipt = {
        "recordType": "SMA_S2_CANDIDATE_9_OFFLINE_WORKING_DIRECTORY_QUALIFICATION_RECEIPT",
        "candidate": 9, "status": "PASS" if passed == len(tests) else "FAIL",
        "testCount": len(tests), "passCount": passed, "failCount": len(tests) - passed,
        "pythonOptimizationMode": not __debug__, "networkUse": "NONE",
        "mavenInvocations": 0, "openhandsConversations": 0, "smaServiceInvocations": 0,
        "deterministicStubNetworkInvocations": 0, "dressRehearsalAttempts": 0,
        "measuredExecutionAttempts": 0, "tests": tests,
        "boundArtifacts": {
            "driverSha256": driver.sha_file(driver.DRIVER),
            "configurationSha256": driver.sha_file(driver.CONFIG),
            "offlineHarnessSha256": driver.sha_file(Path(__file__)),
            "candidate8DriverSha256": driver.sha_file(CANDIDATE8_DRIVER),
            "candidate8DressReceiptSha256": driver.sha_file(CANDIDATE8_DRESS),
            "candidate8AdjudicationSha256": driver.sha_file(CANDIDATE8_ADJUDICATION),
        },
    }
    path = args.output / "offline-working-directory-qualification-receipt.json"
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
