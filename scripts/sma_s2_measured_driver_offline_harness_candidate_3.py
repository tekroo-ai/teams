#!/usr/bin/env python3
"""Offline semantic-mutation qualification for SMA-S2 candidate 5."""

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
DRIVER = ROOT / "scripts/sma_s2_measured_driver_candidate_3.py"


def load_driver():
    spec = importlib.util.spec_from_file_location("sma_s2_driver_candidate_3", DRIVER)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load candidate-5 driver")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


driver = load_driver()


def check(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def run(test_id: str, function: Callable[[], dict[str, Any]]) -> dict[str, Any]:
    started = time.monotonic_ns()
    try:
        detail = function()
        status = "PASS"
        failure = None
    except Exception as error:
        detail = {}
        status = "FAIL"
        failure = {"type": type(error).__name__, "messageSha256": driver.sha_bytes(str(error).encode())}
    return {"id": test_id, "status": status, "durationNs": time.monotonic_ns() - started,
            "detail": detail, "failure": failure}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    tests: list[dict[str, Any]] = []

    tests.append(run("C5-OFFLINE-001-STATIC-CONTRACT", driver.validate_static_contract))

    def oracle_bundle_controls() -> dict[str, Any]:
        accepted = 0
        false_rejected = 0
        missing_rejected = 0
        for case in driver.matrix_value()["cases"]:
            required = driver.required_oracles(case["caseId"])
            driver.evaluate_oracles(case["caseId"], {oracle: True for oracle in required})
            accepted += 1
            for oracle in required:
                false_values = {value: True for value in required}
                false_values[oracle] = False
                try:
                    driver.evaluate_oracles(case["caseId"], false_values)
                except driver.PredicateFailure:
                    false_rejected += 1
                else:
                    raise RuntimeError(f"false oracle accepted: {case['caseId']}:{oracle}")
                missing_values = {value: True for value in required}
                del missing_values[oracle]
                try:
                    driver.evaluate_oracles(case["caseId"], missing_values)
                except driver.PredicateFailure:
                    missing_rejected += 1
                else:
                    raise RuntimeError(f"missing oracle accepted: {case['caseId']}:{oracle}")
        return {"allTrueCaseBundlesAccepted": accepted,
                "singleFalseBundlesRejected": false_rejected,
                "singleMissingBundlesRejected": missing_rejected}
    tests.append(run("C5-OFFLINE-002-ORACLE-BUNDLE-CONTROLS", oracle_bundle_controls))

    def provenance_mutations() -> dict[str, Any]:
        check(driver.classify_context_provenance("memory", "trace") == "TRACE", "trace not classified")
        check(driver.classify_context_provenance("", None) == "EXPLICIT_FAIL_OPEN_EMPTY", "empty fail-open not classified")
        check(driver.classify_context_provenance("memory", None) == "MISSING", "non-empty missing trace accepted")
        return {"semanticMutations": 3, "nonEmptyWithoutTraceRejected": True}
    tests.append(run("C5-OFFLINE-003-PROVENANCE-EVIDENCE-MUTATIONS", provenance_mutations))

    def body_surface_mutations() -> dict[str, Any]:
        bodies = (b"current prompt", b"memory body")
        baseline = {"stubOperational": b"digest only", "serviceLogs": b"status only",
                    "retrievalEvents": b"ids only"}
        check(driver.bodies_absent_from_surfaces(baseline, bodies), "clean body surfaces rejected")
        rejected = 0
        for surface_name in baseline:
            for body in bodies:
                mutated = dict(baseline)
                mutated[surface_name] += body
                check(not driver.bodies_absent_from_surfaces(mutated, bodies),
                      f"body disclosure accepted on {surface_name}")
                rejected += 1
        return {"surfaceMutationCount": rejected, "allDisclosuresRejected": True}
    tests.append(run("C5-OFFLINE-004-CASE7-BODY-SURFACE-MUTATIONS", body_surface_mutations))

    def sequence_mutations() -> dict[str, Any]:
        events = [{"id": "u", "sequence": 7}, {"id": "a", "sequence": 9}]
        docs = [
            {"openhands_provenance": {"event_id": "u", "sequence": 7}},
            {"openhands_provenance": {"event_id": "a", "sequence": 9}},
        ]
        check(driver.sequence_provenance_matches(events, docs), "valid sequence provenance rejected")
        mutations = [
            [docs[0], {"openhands_provenance": {"event_id": "a", "sequence": 8}}],
            [docs[0]],
            [{"openhands_provenance": {"event_id": "other", "sequence": 7}}, docs[1]],
            [{"openhands_provenance": {"event_id": "u", "sequence": None}}, docs[1]],
        ]
        for mutated in mutations:
            check(not driver.sequence_provenance_matches(events, mutated), "corrupt sequence provenance accepted")
        source = inspect.getsource(driver.Candidate5Runtime.case_parent_child_provenance)
        check("before + 3" in source and "sequence_provenance_matches" in source,
              "case 11 is not bound to corrected count and sequence oracle")
        return {"semanticMutations": len(mutations), "captureCardinality": 3}
    tests.append(run("C5-OFFLINE-005-CASE11-PROVENANCE-MUTATIONS", sequence_mutations))

    def summary_mutations() -> dict[str, Any]:
        values = {
            "marker": driver.CONDENSATION_MARKER,
            "context": "bounded recalled memory",
            "captured_event_ids": {"user-event", "agent-event"},
            "summary_event_ids": {"summary-event"},
            "retrieval_bytes": b"memory-id-1",
            "selected_memory_ids": {"memory-id-1"},
            "allowed_memory_ids": {"memory-id-1", "memory-id-2"},
        }
        check(driver.summary_not_delivery_state(**values), "valid summary exclusion rejected")
        mutations = []
        for key, value in (
            ("context", f"bounded {driver.CONDENSATION_MARKER}"),
            ("captured_event_ids", {"summary-event"}),
            ("retrieval_bytes", driver.CONDENSATION_MARKER.encode()),
            ("selected_memory_ids", {"summary-derived-memory"}),
            ("summary_event_ids", set()),
        ):
            mutated = dict(values)
            mutated[key] = value
            mutations.append(mutated)
        for mutated in mutations:
            check(not driver.summary_not_delivery_state(**mutated), "summary-as-state mutation accepted")
        return {"semanticMutations": len(mutations), "marker": driver.CONDENSATION_MARKER}
    tests.append(run("C5-OFFLINE-006-CASE18-SUMMARY-MUTATIONS", summary_mutations))

    def cancellation_mutations() -> dict[str, Any]:
        baseline = {
            "interrupt_ns": 1_000_000_000,
            "future_observed_ns": 2_000_000_000,
            "stub_completed_ns": 2_500_000_000,
            "shutdown_ns": 500_000_000,
            "no_owned_work": True,
            "deletion_complete": True,
            "repetition_workspace_absent": True,
            "repetition_namespace_absent": True,
        }
        valid = driver.cancellation_oracles(**baseline)
        check(all(valid.values()), "valid cancellation evidence rejected")
        mutations = [
            ("future_observed_ns", 11_000_000_001, "futureBound"),
            ("stub_completed_ns", 11_000_000_001, "disconnectBound"),
            ("shutdown_ns", 15_000_000_001, "ownedShutdownBound"),
            ("no_owned_work", False, "ownedShutdownBound"),
            ("deletion_complete", False, "repetitionCleanup"),
            ("repetition_workspace_absent", False, "repetitionCleanup"),
            ("repetition_namespace_absent", False, "repetitionCleanup"),
        ]
        for key, value, expected_false in mutations:
            mutated = dict(baseline)
            mutated[key] = value
            result = driver.cancellation_oracles(**mutated)
            check(result[expected_false] is False, f"cancellation mutation accepted: {key}")
        return {"semanticMutations": len(mutations), "separateFutureAndStubClocks": True}
    tests.append(run("C5-OFFLINE-007-CASE20-CANCELLATION-MUTATIONS", cancellation_mutations))

    def implementation_bindings() -> dict[str, Any]:
        source = DRIVER.read_text()
        required = [
            "classify_context_provenance",
            "bodies_absent_from_surfaces",
            "sequence_provenance_matches",
            "summary_not_delivery_state",
            "cancellation_oracles",
            "futureObservedMonotonicNs",
            "repetitionWorkspaceAbsent",
            "repetitionNamespaceAbsent",
        ]
        missing = [name for name in required if name not in source]
        check(not missing, "live handlers omit semantic helper bindings")
        check('"bodyFree": True' not in source, "case 7 retained literal body-free assertion")
        return {"requiredBindings": len(required), "missing": missing}
    tests.append(run("C5-OFFLINE-008-LIVE-HANDLER-SEMANTIC-BINDINGS", implementation_bindings))

    def explicit_predicates() -> dict[str, Any]:
        tree = ast.parse(DRIVER.read_text())
        assert_nodes = [node.lineno for node in ast.walk(tree) if isinstance(node, ast.Assert)]
        check(not assert_nodes, "optimization-removable assert found")
        return {"pythonAssertNodes": assert_nodes, "pythonOptimizationEnabled": not __debug__}
    tests.append(run("C5-OFFLINE-009-EXPLICIT-PREDICATES", explicit_predicates))

    def execution_fence() -> dict[str, Any]:
        root = Path(tempfile.mkdtemp(prefix="sma-s2-c5-fence-"))
        prereg = root / "prereg.json"
        prereg.write_text("{}\n")
        dependency = {"path": str(driver.BASE_DRIVER), "sha256": driver.sha_file(driver.BASE_DRIVER)}
        identity = {
            "status": "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
            "measuredExecutionAuthorized": False,
            "driver": {"sha256": driver.sha_file(DRIVER)},
            "corpus": {"path": str(driver.CORPUS), "sha256": driver.sha_file(driver.CORPUS)},
            "configuration": {"path": str(driver.CONFIG), "sha256": driver.sha_file(driver.CONFIG)},
            "oracleMatrix": {"path": str(driver.MATRIX), "sha256": driver.sha_file(driver.MATRIX)},
            "stub": {"path": str(driver.STUB), "sha256": driver.sha_file(driver.STUB)},
            "preregistration": {"path": str(prereg), "sha256": driver.sha_file(prereg)},
            "dependencies": [dependency],
        }
        identity_path = root / "identity.json"
        identity_path.write_text(json.dumps(identity, sort_keys=True))
        authorization = {
            "status": "AUTHORIZED_NOT_CONSUMED", "maximumMeasuredAttempts": 1,
            "executionIdentitySha256": driver.sha_file(identity_path),
            "driverSha256": driver.sha_file(DRIVER), "corpusSha256": driver.sha_file(driver.CORPUS),
            "configurationSha256": driver.sha_file(driver.CONFIG),
            "oracleMatrixSha256": driver.sha_file(driver.MATRIX), "stubSha256": driver.sha_file(driver.STUB),
            "preregistrationSha256": driver.sha_file(prereg), "caseCount": 20, "repetitionCount": 103,
            "realModelCallsAllowed": False,
        }
        auth_path = root / "auth.json"
        auth_path.write_text(json.dumps(authorization, sort_keys=True))
        checks = driver.validate_execution_fence(identity_path, auth_path)
        authorization["maximumMeasuredAttempts"] = 2
        auth_path.write_text(json.dumps(authorization, sort_keys=True))
        rejected = False
        try:
            driver.validate_execution_fence(identity_path, auth_path)
        except PermissionError:
            rejected = True
        shutil.rmtree(root, ignore_errors=True)
        check(rejected, "mutated authorization passed fence")
        return {"positiveChecks": len(checks), "mutatedAuthorizationRejected": rejected}
    tests.append(run("C5-OFFLINE-010-EXECUTION-FENCE", execution_fence))

    def default_deny() -> dict[str, Any]:
        parsed = driver.parser().parse_args([])
        check(not parsed.execute, "execution default is not deny")
        return {"executionDefault": "DENY", "networkUse": "NONE"}
    tests.append(run("C5-OFFLINE-011-DEFAULT-DENY", default_deny))

    overall = "PASS" if all(test["status"] == "PASS" for test in tests) else "FAIL"
    receipt = {
        "schemaVersion": "1.0.0-candidate",
        "recordType": "SMA_S2_CANDIDATE_5_OFFLINE_SEMANTIC_QUALIFICATION_RECEIPT",
        "status": overall,
        "scope": "OFFLINE_SEMANTIC_EVIDENCE_MUTATION_AND_DRIVER_CONTROLS_ONLY",
        "pythonOptimizationEnabled": not __debug__,
        "networkUse": "NONE",
        "explicitlyNot": [
            "live preflight", "measured S2 execution", "OpenHands conversation", "SMA service execution",
            "deterministic stub network invocation", "real model call", "OPENHANDS_SMA_BOUNDARY_QUALIFIED",
        ],
        "driver": {"path": str(DRIVER), "sha256": driver.sha_file(DRIVER)},
        "harness": {"path": str(Path(__file__).resolve()), "sha256": driver.sha_file(Path(__file__).resolve())},
        "configuration": {"path": str(driver.CONFIG), "sha256": driver.sha_file(driver.CONFIG)},
        "matrix": {"path": str(driver.MATRIX), "sha256": driver.sha_file(driver.MATRIX)},
        "stub": {"path": str(driver.STUB), "sha256": driver.sha_file(driver.STUB)},
        "tests": tests,
        "summary": {
            "testCount": len(tests),
            "passCount": sum(test["status"] == "PASS" for test in tests),
            "failCount": sum(test["status"] == "FAIL" for test in tests),
            "semanticEvidenceMutations": sum(
                int(test.get("detail", {}).get("semanticMutations", 0))
                + int(test.get("detail", {}).get("surfaceMutationCount", 0))
                for test in tests
            ),
        },
    }
    path = args.output / "offline-semantic-qualification-receipt.json"
    descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, json.dumps(receipt, sort_keys=True, separators=(",", ":")).encode() + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    print(json.dumps({"status": overall, "receipt": str(path)}, sort_keys=True))
    return 0 if overall == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
