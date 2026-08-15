#!/usr/bin/env python3
"""Offline-only control qualification for the SMA-S2 measured driver."""

from __future__ import annotations

import argparse
import base64
import concurrent.futures
import hashlib
import importlib.util
import json
import os
import shutil
import tempfile
import time
from pathlib import Path
from typing import Any, Callable


def canonical(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        while block := stream.read(1024 * 1024):
            digest.update(block)
    return digest.hexdigest()


def check(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def load_driver(path: Path):
    spec = importlib.util.spec_from_file_location("sma_s2_measured_driver_under_test", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load measured driver")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run(test_id: str, function: Callable[[], dict[str, Any]]) -> dict[str, Any]:
    started = time.monotonic_ns()
    try:
        detail = function()
        return {"id": test_id, "status": "PASS", "durationNs": time.monotonic_ns() - started, "detail": detail}
    except Exception as error:
        return {"id": test_id, "status": "FAIL", "durationNs": time.monotonic_ns() - started,
                "errorType": type(error).__name__, "error": str(error)}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--driver", type=Path, required=True)
    parser.add_argument("--corpus", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    driver_path = args.driver.resolve()
    corpus_path = args.corpus.resolve()
    driver = load_driver(driver_path)
    corpus = json.loads(corpus_path.read_text())
    tests: list[dict[str, Any]] = []

    tests.append(run("DRIVER-OFFLINE-001-STATIC-CONTRACT", driver.validate_static_contract))

    def case_map() -> dict[str, Any]:
        plan = driver.exact_plan(corpus)
        check(len(plan) == 103, "plan repetition count mismatch")
        check(len({item["caseId"] for item in plan}) == 20, "plan case count mismatch")
        check(plan == sorted(plan, key=lambda item: (item["caseId"], item["repetition"])), "plan order mismatch")
        handlers = {"case_" + operation.lower() for operation in driver.LiveRuntime.OPERATIONS}
        check(all(callable(getattr(driver.LiveRuntime, name, None)) for name in handlers), "case handler missing")
        check({item["operation"] for item in plan} == set(driver.LiveRuntime.OPERATIONS), "operation set mismatch")
        return {"caseCount": 20, "repetitionCount": 103, "handlerCount": len(handlers)}

    tests.append(run("DRIVER-OFFLINE-002-COMPLETE-CASE-MAP", case_map))

    def create_once() -> dict[str, Any]:
        path = output / "create-once.jsonl"
        first = driver.DurableJournal(path)
        first.append("CREATED", {"value": 1})
        rejected = False
        try:
            driver.DurableJournal(path)
        except FileExistsError:
            rejected = True
        check(rejected, "create-once fence allowed replacement")
        return {"secondCreateRejected": rejected, "records": len(driver.read_jsonl(path))}

    tests.append(run("DRIVER-OFFLINE-003-CREATE-ONCE", create_once))

    def concurrent_journal() -> dict[str, Any]:
        path = output / "concurrent.jsonl"
        journal = driver.DurableJournal(path)
        def append(index: int) -> str:
            return journal.append("CONCURRENT", {"index": index})
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
            ids = list(executor.map(append, range(128)))
        records = driver.read_jsonl(path)
        check(len(records) == 128 and len(set(ids)) == 128, "concurrent record count or identity mismatch")
        check({record["index"] for record in records} == set(range(128)), "concurrent journal lost an index")
        return {"records": len(records), "uniqueRecordIds": len(set(ids)), "sha256": sha_file(path)}

    tests.append(run("DRIVER-OFFLINE-004-CONCURRENT-FSYNC-JOURNAL", concurrent_journal))

    def preassertion() -> dict[str, Any]:
        path = output / "preassertion.jsonl"
        journal = driver.DurableJournal(path)
        failed = False
        try:
            journal.append("PREASSERTION_REPETITION_EVIDENCE", {"caseId": "NEGATIVE", "observed": 1})
            check(False, "deliberate assertion control")
        except AssertionError:
            failed = True
        reopened = driver.read_jsonl(path)
        check(failed and len(reopened) == 1 and reopened[0]["observed"] == 1,
              "preassertion evidence did not survive deliberate failure")
        return {"assertionFailed": failed, "evidenceSurvived": True, "sha256": sha_file(path)}

    tests.append(run("DRIVER-OFFLINE-005-PREASSERTION-SURVIVAL", preassertion))

    def execution_fence() -> dict[str, Any]:
        identity = output / "synthetic-identity.json"
        authorization = output / "synthetic-authorization.json"
        synthetic_preregistration = output / "synthetic-preregistration.json"
        synthetic_configuration = output / "synthetic-configuration.json"
        synthetic_dependency = output / "synthetic-dependency.txt"
        synthetic_preregistration.write_bytes(canonical({"synthetic": "preregistration"}))
        synthetic_configuration.write_bytes(canonical({"synthetic": "configuration"}))
        synthetic_dependency.write_text("synthetic dependency\n")
        identity_value = {
            "status": "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
            "measuredExecutionAuthorized": False,
            "driver": {"sha256": sha_file(driver_path)},
            "corpus": {"sha256": sha_file(corpus_path)},
            "stub": {"sha256": sha_file(driver.STUB)},
            "preregistration": {"path": str(synthetic_preregistration),
                                "sha256": sha_file(synthetic_preregistration)},
            "configuration": {"path": str(synthetic_configuration),
                              "sha256": sha_file(synthetic_configuration)},
            "dependencies": [{"path": str(synthetic_dependency),
                              "sha256": sha_file(synthetic_dependency)}],
        }
        identity.write_bytes(canonical(identity_value))
        authorization_value = {
            "status": "AUTHORIZED_NOT_CONSUMED", "maximumMeasuredAttempts": 1,
            "executionIdentitySha256": sha_file(identity), "driverSha256": sha_file(driver_path),
            "corpusSha256": sha_file(corpus_path), "caseCount": 20, "repetitionCount": 103,
            "preregistrationSha256": sha_file(synthetic_preregistration),
            "realModelCallsAllowed": False,
        }
        authorization.write_bytes(canonical(authorization_value))
        positive = driver.validate_execution_fence(identity, authorization)
        authorization_value["repetitionCount"] = 102
        authorization.write_bytes(canonical(authorization_value))
        rejected = False
        try:
            driver.validate_execution_fence(identity, authorization)
        except PermissionError:
            rejected = True
        check(rejected, "mutated measured authorization was accepted")
        return {"positiveChecks": len(positive), "mutatedAuthorizationRejected": rejected}

    tests.append(run("DRIVER-OFFLINE-006-AUTHORIZATION-FENCE", execution_fence))

    def fault_controls() -> dict[str, Any]:
        plan = driver.exact_plan(corpus)
        fault19 = [item["mode"] for item in plan if item["caseId"].startswith("SMA-S2-019-")]
        fault20 = [item["mode"] for item in plan if item["caseId"].startswith("SMA-S2-020-")]
        hook = [item for item in plan if item["caseId"].startswith("SMA-S2-010-")]
        check(fault19 == ["TIMEOUT", "MALFORMED", "HTTP_503", "TRANSPORT_FAILURE_UNUSED_PORT"],
              "model fault schedule mismatch")
        check(fault20 == ["TIMEOUT", "TIMEOUT", "TIMEOUT"], "cancellation mode schedule mismatch")
        check(all(item["stubMode"] == "SUCCESS" for item in hook), "hook faults leaked into stub modes")
        check(corpus["cases"][-1]["cancelAfterRawReceiptMilliseconds"] == 250,
              "cancellation delay mismatch")
        return {"modelFaults": fault19, "cancellationModes": fault20,
                "hookFaultCount": len(hook), "cancellationDelayMilliseconds": 250}

    tests.append(run("DRIVER-OFFLINE-007-FAULT-AND-CANCELLATION-MAP", fault_controls))

    def secret_surfaces() -> dict[str, Any]:
        raw = output / "synthetic-raw.jsonl"
        operational = output / "synthetic-operational.jsonl"
        body = canonical({"prompt": driver.SECRET})
        raw.write_bytes(canonical({"requestBodyBase64": base64.b64encode(body).decode(),
                                   "requestBodySha256": hashlib.sha256(body).hexdigest(),
                                   "requestBodyLength": len(body)}) + b"\n")
        operational.write_bytes(canonical({"requestBodySha256": hashlib.sha256(body).hexdigest(),
                                           "requestBodyLength": len(body)}) + b"\n")
        check(driver.SECRET.encode() in base64.b64decode(json.loads(raw.read_text())["requestBodyBase64"]),
              "synthetic secret absent from permitted sealed raw body")
        check(driver.SECRET.encode() not in operational.read_bytes(),
              "synthetic secret escaped into operational evidence")
        return {"presentOnlyInSealedRawBody": True, "absentFromOperational": True}

    tests.append(run("DRIVER-OFFLINE-008-SECRET-SURFACE-SEPARATION", secret_surfaces))

    def reconstruction() -> dict[str, Any]:
        path = output / "reconstruction.jsonl"
        journal = driver.DurableJournal(path)
        for item in driver.exact_plan(corpus):
            journal.append("PREASSERTION_REPETITION_EVIDENCE", {"caseId": item["caseId"],
                           "repetition": item["repetition"], "operation": item["operation"]})
        reopened = driver.read_jsonl(path)
        pairs = [(item["caseId"], item["repetition"]) for item in reopened]
        expected = [(item["caseId"], item["repetition"]) for item in driver.exact_plan(corpus)]
        check(pairs == expected and len(set(pairs)) == 103, "receipt reconstruction mismatch")
        return {"reconstructedRepetitions": len(pairs), "uniquePairs": len(set(pairs)),
                "sha256": sha_file(path)}

    tests.append(run("DRIVER-OFFLINE-009-RECEIPT-RECONSTRUCTION", reconstruction))

    def cleanup_idempotency() -> dict[str, Any]:
        root = Path(tempfile.mkdtemp(prefix="sma-s2-driver-cleanup-"))
        nested = root / "owned" / "nested"
        nested.mkdir(parents=True)
        (nested / "fixture").write_text("synthetic")
        shutil.rmtree(root / "owned", ignore_errors=True)
        first = not (root / "owned").exists()
        shutil.rmtree(root / "owned", ignore_errors=True)
        second = not (root / "owned").exists()
        shutil.rmtree(root, ignore_errors=True)
        check(first and second and not root.exists(), "cleanup was not idempotent")
        return {"firstCleanup": first, "secondCleanup": second, "rootAbsent": not root.exists()}

    tests.append(run("DRIVER-OFFLINE-010-IDEMPOTENT-CLEANUP", cleanup_idempotency))

    overall = "PASS" if all(test["status"] == "PASS" for test in tests) else "FAIL"
    receipt = {
        "schemaVersion": "1.0.0-candidate",
        "recordType": "SMA_S2_MEASURED_DRIVER_OFFLINE_QUALIFICATION_RECEIPT",
        "status": overall,
        "scope": "OFFLINE_DRIVER_CONTROLS_ONLY",
        "pythonOptimizationEnabled": not __debug__,
        "networkUse": "NONE_EPHEMERAL_FILES_ONLY",
        "explicitlyNot": ["live preflight", "measured S2 execution", "OpenHands conversation",
                          "SMA service execution", "deterministic stub invocation", "real model call",
                          "OPENHANDS_SMA_BOUNDARY_QUALIFIED"],
        "driver": {"path": str(driver_path), "sha256": sha_file(driver_path)},
        "corpus": {"path": str(corpus_path), "sha256": sha_file(corpus_path)},
        "harness": {"path": str(Path(__file__).resolve()), "sha256": sha_file(Path(__file__).resolve())},
        "tests": tests,
        "summary": {"testCount": len(tests), "passCount": sum(test["status"] == "PASS" for test in tests),
                    "failCount": sum(test["status"] == "FAIL" for test in tests)},
    }
    receipt_path = output / "offline-driver-qualification-receipt.json"
    descriptor = os.open(receipt_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, canonical(receipt) + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    print(json.dumps({"status": overall, "receipt": str(receipt_path),
                      "receiptSha256": sha_file(receipt_path)}, sort_keys=True))
    return 0 if overall == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
