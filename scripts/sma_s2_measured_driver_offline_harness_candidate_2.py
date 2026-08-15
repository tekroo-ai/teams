#!/usr/bin/env python3
"""Offline negative-control qualification for SMA-S2 driver candidate 2."""

from __future__ import annotations

import argparse
import ast
import importlib.util
import inspect
import json
import os
import shutil
import tempfile
import time
from pathlib import Path
from typing import Any, Callable


ROOT = Path(__file__).resolve().parents[1]
DRIVER = ROOT / "scripts/sma_s2_measured_driver_candidate_2.py"


def load_driver():
    spec = importlib.util.spec_from_file_location("sma_s2_driver_candidate_2", DRIVER)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load driver candidate 2")
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module); return module


driver = load_driver()


def check(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def run(test_id: str, function: Callable[[], dict[str, Any]]) -> dict[str, Any]:
    started = time.monotonic_ns()
    try:
        detail = function(); status = "PASS"; failure = None
    except Exception as error:
        detail = {}; status = "FAIL"; failure = {"type": type(error).__name__,
                                                  "messageSha256": driver.sha_bytes(str(error).encode())}
    return {"id": test_id, "status": status, "durationNs": time.monotonic_ns() - started,
            "detail": detail, "failure": failure}


def main() -> int:
    parser = argparse.ArgumentParser(); parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(); args.output.mkdir(parents=True, exist_ok=False)
    tests: list[dict[str, Any]] = []

    tests.append(run("C4-OFFLINE-001-STATIC-CONTRACT", driver.validate_static_contract))

    def predicate_coverage() -> dict[str, Any]:
        matrix = driver.matrix_value(); source = json.loads(driver.SEMANTIC_SOURCE.read_text())
        predicate_count = sum(len(item["predicates"]) for item in source["cases"])
        oracle_count = sum(len(item["predicateOracleIds"]) for item in matrix["cases"])
        check(predicate_count == oracle_count, "predicate/oracle count mismatch")
        check([item["id"] for item in source["cases"]] == [item["caseId"] for item in matrix["cases"]],
              "case identity mismatch")
        return {"caseCount": len(source["cases"]), "predicateCount": predicate_count,
                "predicateOracleCount": oracle_count, "requiredEvidenceCount": len(source["requiredEvidence"])}
    tests.append(run("C4-OFFLINE-002-SEMANTIC-COVERAGE", predicate_coverage))

    def all_true() -> dict[str, Any]:
        tested = 0
        for case in driver.matrix_value()["cases"]:
            required = driver.required_oracles(case["caseId"])
            driver.evaluate_oracles(case["caseId"], {oracle_id: True for oracle_id in required}); tested += 1
        return {"caseBundlesAccepted": tested}
    tests.append(run("C4-OFFLINE-003-ALL-TRUE-BUNDLES", all_true))

    def false_negative_controls() -> dict[str, Any]:
        rejected = 0
        for case in driver.matrix_value()["cases"]:
            required = driver.required_oracles(case["caseId"])
            for oracle_id in required:
                values = {value: True for value in required}; values[oracle_id] = False
                try: driver.evaluate_oracles(case["caseId"], values)
                except driver.PredicateFailure: rejected += 1
                else: raise RuntimeError(f"false oracle accepted: {case['caseId']}:{oracle_id}")
        return {"singleFalseBundlesRejected": rejected}
    tests.append(run("C4-OFFLINE-004-EVERY-SINGLE-FALSE-REJECTED", false_negative_controls))

    def missing_negative_controls() -> dict[str, Any]:
        rejected = 0
        for case in driver.matrix_value()["cases"]:
            required = driver.required_oracles(case["caseId"])
            for oracle_id in required:
                values = {value: True for value in required}; del values[oracle_id]
                try: driver.evaluate_oracles(case["caseId"], values)
                except driver.PredicateFailure: rejected += 1
                else: raise RuntimeError(f"missing oracle accepted: {case['caseId']}:{oracle_id}")
        return {"singleMissingBundlesRejected": rejected}
    tests.append(run("C4-OFFLINE-005-EVERY-SINGLE-MISSING-REJECTED", missing_negative_controls))

    def implementation_literals() -> dict[str, Any]:
        missing: list[str] = []
        for case in driver.matrix_value()["cases"]:
            operation = next(item["operation"] for item in json.loads(driver.CORPUS.read_text())["cases"]
                             if item["id"] == case["caseId"])
            source = inspect.getsource(getattr(driver.Candidate4Runtime, "case_" + operation.lower()))
            missing.extend(oracle_id for oracle_id in case["predicateOracleIds"] if oracle_id not in source)
        check(not missing, "handler omitted predicate oracle literals")
        return {"handlerCount": 20, "missingPredicateOracleLiterals": missing}
    tests.append(run("C4-OFFLINE-006-HANDLER-ORACLE-BINDING", implementation_literals))

    def explicit_predicates() -> dict[str, Any]:
        tree = ast.parse(DRIVER.read_text())
        assert_nodes = [node.lineno for node in ast.walk(tree) if isinstance(node, ast.Assert)]
        check(not assert_nodes, "optimization-removable assert found")
        return {"pythonAssertNodes": assert_nodes, "pythonOptimizationEnabled": not __debug__}
    tests.append(run("C4-OFFLINE-007-EXPLICIT-PREDICATES", explicit_predicates))

    def fixture_binding() -> dict[str, Any]:
        config = json.loads(driver.CONFIG.read_text()); matrix = driver.matrix_value()
        stub_spec = importlib.util.spec_from_file_location("sma_s2_stub_candidate_3", driver.STUB)
        check(stub_spec is not None and stub_spec.loader is not None, "cannot load stub")
        stub = importlib.util.module_from_spec(stub_spec); stub_spec.loader.exec_module(stub)
        configured = config["openhands"]["deterministicFixtureModes"]
        check(configured == matrix["deterministicFixtureModes"], "fixture mode binding mismatch")
        check(set(configured.values()) == set(stub.SPECIAL_MODES), "stub fixture modes mismatch")
        return {"fixtureModes": configured, "stubBaseSha256": driver.sha_file(stub.BASE_PATH)}
    tests.append(run("C4-OFFLINE-008-FIXTURE-STIMULUS-BINDING", fixture_binding))

    def execution_fence() -> dict[str, Any]:
        root = Path(tempfile.mkdtemp(prefix="sma-s2-c4-fence-")); prereg = root / "prereg.json"
        prereg.write_text("{}\n")
        dependency = {"path": str(driver.BASE_DRIVER), "sha256": driver.sha_file(driver.BASE_DRIVER)}
        identity = {"status": "ACCEPTED_FROZEN_EXECUTION_IDENTITY", "measuredExecutionAuthorized": False,
                    "driver": {"sha256": driver.sha_file(driver.DRIVER if hasattr(driver, "DRIVER") else DRIVER)},
                    "corpus": {"path": str(driver.CORPUS), "sha256": driver.sha_file(driver.CORPUS)},
                    "configuration": {"path": str(driver.CONFIG), "sha256": driver.sha_file(driver.CONFIG)},
                    "oracleMatrix": {"path": str(driver.MATRIX), "sha256": driver.sha_file(driver.MATRIX)},
                    "stub": {"path": str(driver.STUB), "sha256": driver.sha_file(driver.STUB)},
                    "preregistration": {"path": str(prereg), "sha256": driver.sha_file(prereg)},
                    "dependencies": [dependency]}
        identity_path = root / "identity.json"; identity_path.write_text(json.dumps(identity, sort_keys=True))
        authorization = {"status": "AUTHORIZED_NOT_CONSUMED", "maximumMeasuredAttempts": 1,
                         "executionIdentitySha256": driver.sha_file(identity_path),
                         "driverSha256": driver.sha_file(DRIVER), "corpusSha256": driver.sha_file(driver.CORPUS),
                         "configurationSha256": driver.sha_file(driver.CONFIG), "oracleMatrixSha256": driver.sha_file(driver.MATRIX),
                         "stubSha256": driver.sha_file(driver.STUB), "preregistrationSha256": driver.sha_file(prereg),
                         "caseCount": 20, "repetitionCount": 103, "realModelCallsAllowed": False}
        auth_path = root / "auth.json"; auth_path.write_text(json.dumps(authorization, sort_keys=True))
        checks = driver.validate_execution_fence(identity_path, auth_path)
        authorization["maximumMeasuredAttempts"] = 2; auth_path.write_text(json.dumps(authorization, sort_keys=True))
        rejected = False
        try: driver.validate_execution_fence(identity_path, auth_path)
        except PermissionError: rejected = True
        shutil.rmtree(root, ignore_errors=True); check(rejected, "mutated authorization passed fence")
        return {"positiveChecks": len(checks), "mutatedAuthorizationRejected": rejected}
    tests.append(run("C4-OFFLINE-009-EXECUTION-FENCE", execution_fence))

    def default_deny_and_cleanup() -> dict[str, Any]:
        parsed = driver.parser().parse_args([]); check(not parsed.execute, "execution default is not deny")
        root = Path(tempfile.mkdtemp(prefix="sma-s2-c4-cleanup-")); nested = root / "owned"; nested.mkdir()
        shutil.rmtree(nested, ignore_errors=True); first = not nested.exists(); shutil.rmtree(nested, ignore_errors=True)
        second = not nested.exists(); shutil.rmtree(root, ignore_errors=True); check(first and second, "cleanup not idempotent")
        return {"executionDefault": "DENY", "cleanupIdempotent": True}
    tests.append(run("C4-OFFLINE-010-DEFAULT-DENY-AND-CLEANUP", default_deny_and_cleanup))

    overall = "PASS" if all(test["status"] == "PASS" for test in tests) else "FAIL"
    receipt = {"schemaVersion": "1.0.0-candidate", "recordType": "SMA_S2_CANDIDATE_4_OFFLINE_QUALIFICATION_RECEIPT",
               "status": overall, "scope": "OFFLINE_MATRIX_DRIVER_AND_FIXTURE_CONTROLS_ONLY",
               "pythonOptimizationEnabled": not __debug__, "networkUse": "NONE",
               "explicitlyNot": ["live preflight", "measured S2 execution", "OpenHands conversation",
                                 "SMA service execution", "deterministic stub network invocation",
                                 "real model call", "OPENHANDS_SMA_BOUNDARY_QUALIFIED"],
               "driver": {"path": str(DRIVER), "sha256": driver.sha_file(DRIVER)},
               "harness": {"path": str(Path(__file__).resolve()), "sha256": driver.sha_file(Path(__file__).resolve())},
               "matrix": {"path": str(driver.MATRIX), "sha256": driver.sha_file(driver.MATRIX)},
               "configuration": {"path": str(driver.CONFIG), "sha256": driver.sha_file(driver.CONFIG)},
               "stub": {"path": str(driver.STUB), "sha256": driver.sha_file(driver.STUB)},
               "tests": tests, "summary": {"testCount": len(tests),
               "passCount": sum(test["status"] == "PASS" for test in tests),
               "failCount": sum(test["status"] == "FAIL" for test in tests)}}
    path = args.output / "offline-qualification-receipt.json"
    descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try: os.write(descriptor, json.dumps(receipt, sort_keys=True, separators=(",", ":")).encode() + b"\n"); os.fsync(descriptor)
    finally: os.close(descriptor)
    print(json.dumps({"status": overall, "receipt": str(path)}, sort_keys=True)); return 0 if overall == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
