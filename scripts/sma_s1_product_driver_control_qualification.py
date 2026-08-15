#!/usr/bin/env python3
"""Qualify only the SMA-S1 product driver's disposable lifecycle controls."""

from __future__ import annotations

import argparse
import json
import time
import uuid
from pathlib import Path
from typing import Any

from sma_s1_harness import (
    DurableJournal,
    canonical_bytes,
    capture_driver,
    create_once_directory,
    read_journal,
    sha256_bytes,
    sha256_file,
    utc_now,
    write_new_file,
)


SEQUENCE = ("INVENTORY", "PREPARE", "INVENTORY", "CLEANUP", "CLEANUP", "INVENTORY")


def request(
    operation: str,
    ordinal: int,
    run_id: str,
    manifest_sha256: str,
    identity_sha256: str,
    namespaces: dict[str, Any],
) -> dict[str, Any]:
    return {
        "protocolVersion": "1.0.0",
        "operation": operation,
        "runId": run_id,
        "caseId": f"__CONTROL_{ordinal}_{operation}__",
        "repetition": 0,
        "manifestSHA256": manifest_sha256,
        "identitySHA256": identity_sha256,
        "fault": "NONE",
        "fixture": {"predicates": []},
        "namespaces": namespaces,
    }


def inventory_state(response: dict[str, Any]) -> dict[str, Any] | None:
    evidence = response.get("evidence")
    if not isinstance(evidence, dict):
        return None
    if "anyPresent" in evidence:
        return evidence
    after = evidence.get("after")
    return after if isinstance(after, dict) else None


def expected_step(ordinal: int, operation: str, response: dict[str, Any]) -> bool:
    if response.get("terminalState") != "SUCCEEDED":
        return False
    if response.get("predicateResults") != [] or response.get("safetyStop") is not False:
        return False
    inventory = inventory_state(response)
    if inventory is None:
        return False
    if ordinal in (1, 6):
        return inventory.get("anyPresent") is False
    if ordinal in (2, 3):
        return (
            inventory.get("mongoDatabasePresent") is True
            and inventory.get("mongoMemoryCount") == 4
            and inventory.get("semanticCollectionPresent") is True
            and inventory.get("semanticPointCount") == 4
            and inventory.get("episodicCollectionPresent") is True
            and inventory.get("episodicPointCount") == 4
        )
    if ordinal in (4, 5):
        return inventory.get("anyPresent") is False
    return False


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-root", required=True)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--identity", required=True)
    parser.add_argument("--protocol", required=True)
    parser.add_argument("--driver", required=True)
    parser.add_argument("--timeout", type=float, default=45.0)
    args = parser.parse_args()

    output_root = Path(args.output_root).resolve()
    manifest = Path(args.manifest).resolve()
    identity = Path(args.identity).resolve()
    protocol = Path(args.protocol).resolve()
    driver = Path(args.driver).resolve()
    create_once_directory(output_root)

    run_id = "sma-s1-product-driver-controls-p2m-candidate-7"
    namespaces = {
        "mongodbDatabase": "sma_s1_driver_fixture_controls_p2m_candidate_7",
        "semanticCollection": "sma_s1_driver_fixture_controls_semantic_p2m_candidate_7",
        "episodicCollection": "sma_s1_driver_fixture_controls_episodic_p2m_candidate_7",
        "actorAlphaPartition": "sma-s1-fixture-actor-alpha-controls-p2m-candidate-7",
        "actorBetaPartition": "sma-s1-fixture-actor-beta-controls-p2m-candidate-7",
        "productionOrHistoricalDataAllowed": False,
    }
    manifest_hash = sha256_file(manifest)
    identity_hash = sha256_file(identity)
    journal_path = output_root / "control-journal.jsonl"
    outcomes: list[dict[str, Any]] = []

    with DurableJournal(journal_path) as journal:
        journal.append({
            "recordType": "SMA_S1_PRODUCT_DRIVER_CONTROL_BOUNDARY",
            "runId": run_id,
            "authorizedOperations": list(SEQUENCE),
            "executeCaseAuthorized": False,
            "measuredCasesAuthorized": 0,
            "namespaceIdentitySHA256": sha256_bytes(canonical_bytes(namespaces)),
        })
        for ordinal, operation in enumerate(SEQUENCE, start=1):
            control_request = request(
                operation, ordinal, run_id, manifest_hash, identity_hash, namespaces)
            evidence_id = f"control:{ordinal}:{uuid.uuid4()}"
            started = time.monotonic_ns()
            response, process, error_class = capture_driver(driver, control_request, args.timeout)
            journal.append({
                "recordType": "SMA_S1_PRODUCT_DRIVER_CONTROL_PREASSERTION_EVIDENCE",
                "evidenceId": evidence_id,
                "ordinal": ordinal,
                "operation": operation,
                "requestSHA256": sha256_bytes(canonical_bytes(control_request)),
                "validatedResponse": response,
                "driverErrorClass": error_class,
                "driverElapsedNanoseconds": process.elapsed_ns,
                "controlElapsedNanoseconds": time.monotonic_ns() - started,
                "driverTimedOut": process.timed_out,
                "driverReturnCode": process.return_code,
                "driverStdoutSHA256": sha256_bytes(process.stdout),
                "driverStdoutLength": len(process.stdout),
                "driverStderrSHA256": sha256_bytes(process.stderr),
                "driverStderrLength": len(process.stderr),
            })
            passed = error_class is None and response is not None and expected_step(
                ordinal, operation, response)
            outcome = {
                "ordinal": ordinal,
                "operation": operation,
                "status": "PASS" if passed else "FAIL",
                "evidenceId": evidence_id,
                "failureClass": None if passed else "HARNESS_OR_ENVIRONMENT",
            }
            journal.append({
                "recordType": "SMA_S1_PRODUCT_DRIVER_CONTROL_ASSERTION_RESULT",
                **outcome,
            })
            outcomes.append(outcome)

        status = "PASS" if all(item["status"] == "PASS" for item in outcomes) else "FAIL"
        journal.append({
            "recordType": "SMA_S1_PRODUCT_DRIVER_CONTROL_SUMMARY",
            "status": status,
            "operationCount": len(outcomes),
            "passCount": sum(item["status"] == "PASS" for item in outcomes),
            "failCount": sum(item["status"] == "FAIL" for item in outcomes),
            "executeCaseOperations": 0,
            "measuredCasesExecuted": 0,
        })

    records = read_journal(journal_path)
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_S1_PRODUCT_DRIVER_CONTROL_QUALIFICATION_RECEIPT",
        "status": status,
        "scope": "PRODUCT_DRIVER_LIFECYCLE_CONTROLS_ONLY_NO_S1_PRODUCT_CREDIT",
        "recordedAt": utc_now(),
        "runId": run_id,
        "manifestSHA256": manifest_hash,
        "identityDraftSHA256": identity_hash,
        "protocolSHA256": sha256_file(protocol),
        "driverLauncherSHA256": sha256_file(driver),
        "journalSHA256": sha256_file(journal_path),
        "journalRecordCount": len(records),
        "sequence": list(SEQUENCE),
        "outcomes": outcomes,
        "executeCaseOperations": 0,
        "measuredCasesExecuted": 0,
        "openhandsOrModelUsed": False,
        "productionOrHistoricalNamespaceAllowed": False,
        "cleanupAttemptCount": 2,
        "finalInventoryAttempted": True,
    }
    write_new_file(output_root / "control-receipt.json", canonical_bytes(receipt) + b"\n")
    print(
        f"SMA-S1 product-driver controls {status}: "
        f"{len(outcomes)} operations, 0 measured cases"
    )
    return 0 if status == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
