#!/usr/bin/env python3
"""Non-creditable implementation checks for all frozen SMA-S1 driver handlers."""

from __future__ import annotations

import argparse
import json
import time
import uuid
from pathlib import Path
from typing import Any

from sma_s1_harness_evidence_v2 import (
    DurableJournal,
    RESTART_CASES,
    canonical_bytes,
    capture_driver,
    create_once_directory,
    minimum_evidence_gap,
    read_journal,
    sha256_bytes,
    sha256_file,
    utc_now,
    write_new_file,
)


def make_request(
    operation: str,
    case_id: str,
    fault: str,
    fixture: dict[str, Any],
    run_id: str,
    manifest_sha256: str,
    identity_sha256: str,
    namespaces: dict[str, Any],
) -> dict[str, Any]:
    return {
        "protocolVersion": "1.0.0",
        "operation": operation,
        "runId": run_id,
        "caseId": case_id,
        "repetition": 0,
        "manifestSHA256": manifest_sha256,
        "identitySHA256": identity_sha256,
        "fault": fault,
        "fixture": fixture,
        "namespaces": namespaces,
    }


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
    manifest_path = Path(args.manifest).resolve()
    identity_path = Path(args.identity).resolve()
    protocol_path = Path(args.protocol).resolve()
    driver_path = Path(args.driver).resolve()
    create_once_directory(output_root)
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    run_id = "sma-s1-handler-implementation-check-p2m-evidence-v2-candidate-1"
    namespaces = {
        "mongodbDatabase": "sma_s1_driver_fixture_handler_p2m_evidence_v2_candidate_1",
        "semanticCollection": "sma_s1_driver_fixture_handler_semantic_p2m_evidence_v2_candidate_1",
        "episodicCollection": "sma_s1_driver_fixture_handler_episodic_p2m_evidence_v2_candidate_1",
        "actorAlphaPartition": "sma-s1-fixture-actor-alpha-handler-p2m-evidence-v2-candidate-1",
        "actorBetaPartition": "sma-s1-fixture-actor-beta-handler-p2m-evidence-v2-candidate-1",
        "productionOrHistoricalDataAllowed": False,
    }
    manifest_hash = sha256_file(manifest_path)
    identity_hash = sha256_file(identity_path)
    journal_path = output_root / "handler-check-journal.jsonl"
    handler_results: list[dict[str, Any]] = []
    controls: list[dict[str, Any]] = []

    def invoke_and_record(
        journal: DurableJournal,
        request: dict[str, Any],
        record_type: str,
    ) -> tuple[dict[str, Any] | None, str | None]:
        evidence_id = f"handler-check:{uuid.uuid4()}"
        started = time.monotonic_ns()
        request_bytes = canonical_bytes(request)
        response, process, error_class = capture_driver(driver_path, request, args.timeout)
        journal.append({
            "recordType": record_type,
            "evidenceId": evidence_id,
            "operation": request["operation"],
            "caseId": request["caseId"],
            "requestSHA256": sha256_bytes(request_bytes),
            "requestByteLength": len(request_bytes),
            "validatedResponse": response,
            "driverErrorClass": error_class,
            "driverElapsedNanoseconds": process.elapsed_ns,
            "checkElapsedNanoseconds": time.monotonic_ns() - started,
            "driverTimedOut": process.timed_out,
            "driverReturnCode": process.return_code,
            "driverStdoutSHA256": sha256_bytes(process.stdout),
            "driverStdoutLength": len(process.stdout),
            "responseByteLength": len(process.stdout),
            "driverStderrSHA256": sha256_bytes(process.stderr),
            "driverStderrLength": len(process.stderr),
        })
        return response, error_class

    with DurableJournal(journal_path) as journal:
        journal.append({
            "recordType": "SMA_S1_HANDLER_IMPLEMENTATION_CHECK_BOUNDARY",
            "runId": run_id,
            "scope": "NON_CREDITABLE_SINGLE_INVOCATION_PER_HANDLER",
            "measuredS1Execution": False,
            "scientificRepetitionsExecuted": 0,
            "singleUseAuthorizationConsumed": False,
            "namespaceIdentitySHA256": sha256_bytes(canonical_bytes(namespaces)),
        })

        initial = make_request("INVENTORY", "__INITIAL_INVENTORY__", "NONE",
                {"predicates": []}, run_id, manifest_hash, identity_hash, namespaces)
        response, error = invoke_and_record(journal, initial,
                "SMA_S1_HANDLER_CHECK_CONTROL_PREASSERTION")
        initial_pass = error is None and response is not None \
                and response["terminalState"] == "SUCCEEDED" \
                and response["evidence"].get("anyPresent") is False
        controls.append({"operation": "INITIAL_INVENTORY", "status": "PASS" if initial_pass else "FAIL"})

        prepare = make_request("PREPARE", "__PREPARE__", "NONE", {"predicates": []},
                run_id, manifest_hash, identity_hash, namespaces)
        response, error = invoke_and_record(journal, prepare,
                "SMA_S1_HANDLER_CHECK_CONTROL_PREASSERTION")
        prepare_pass = error is None and response is not None \
                and response["terminalState"] == "SUCCEEDED" and response["safetyStop"] is False
        controls.append({"operation": "PREPARE", "status": "PASS" if prepare_pass else "FAIL"})

        if prepare_pass:
            for case in manifest["cases"]:
                fixture = {
                    "predicates": case["predicates"],
                    "syntheticCorpus": manifest["syntheticCorpus"],
                    "executionPhase": "SINGLE",
                }
                restart_before_pass = True
                if case["id"] in RESTART_CASES:
                    before_fixture = dict(fixture)
                    before_fixture["executionPhase"] = "BEFORE_PROCESS_RESTART"
                    before_request = make_request(
                        "EXECUTE_CASE", case["id"], case["fault"],
                        before_fixture, run_id, manifest_hash, identity_hash, namespaces)
                    before_response, before_error = invoke_and_record(
                        journal, before_request,
                        "SMA_S1_HANDLER_CHECK_RESTART_BEFORE_PREASSERTION")
                    restart_before_pass = before_error is None and before_response is not None \
                            and before_response["terminalState"] == "SUCCEEDED" \
                            and before_response["safetyStop"] is False \
                            and before_response["evidence"].get("executionPhase") \
                            == "BEFORE_PROCESS_RESTART" \
                            and isinstance(before_response["evidence"].get("restartReceipt"), dict)
                    fixture["executionPhase"] = "AFTER_PROCESS_RESTART"
                case_request = make_request(
                    "EXECUTE_CASE", case["id"], case["fault"],
                    fixture,
                    run_id, manifest_hash, identity_hash, namespaces)
                response, error = invoke_and_record(journal, case_request,
                        "SMA_S1_HANDLER_CHECK_PREASSERTION")
                evidence_gap = None if response is None else minimum_evidence_gap(
                        case["id"], response.get("evidence", {}))
                mechanics_pass = restart_before_pass and error is None and response is not None \
                        and response["terminalState"] == "SUCCEEDED" \
                        and response["safetyStop"] is False \
                        and [item["predicate"] for item in response["predicateResults"]] == case["predicates"] \
                        and response["faultActivation"].get("activated") is True \
                        and response["faultObservation"].get("observed") is True \
                        and evidence_gap is None
                predicate_passes = [] if response is None else [
                    item["passed"] for item in response["predicateResults"]]
                result = {
                    "caseId": case["id"],
                    "handlerMechanicsStatus": "PASS" if mechanics_pass else "FAIL",
                    "predicatePassCount": sum(predicate_passes),
                    "predicateFailCount": len(predicate_passes) - sum(predicate_passes),
                    "minimumEvidenceGap": evidence_gap,
                    "restartBeforePhaseStatus": "PASS" if restart_before_pass else "FAIL",
                    "substantiveDisposition": "OBSERVED_ALL_TRUE"
                    if predicate_passes and all(predicate_passes) else "OBSERVED_COUNTEREXAMPLE_OR_GAP",
                }
                handler_results.append(result)
                journal.append({"recordType": "SMA_S1_HANDLER_CHECK_RESULT", **result})

        for ordinal in (1, 2):
            cleanup = make_request("CLEANUP", f"__CLEANUP_{ordinal}__", "NONE",
                    {"predicates": []}, run_id, manifest_hash, identity_hash, namespaces)
            response, error = invoke_and_record(journal, cleanup,
                    "SMA_S1_HANDLER_CHECK_CONTROL_PREASSERTION")
            passed = error is None and response is not None \
                    and response["terminalState"] == "SUCCEEDED" \
                    and response["evidence"].get("after", {}).get("anyPresent") is False
            controls.append({"operation": f"CLEANUP_{ordinal}", "status": "PASS" if passed else "FAIL"})

        final = make_request("INVENTORY", "__FINAL_INVENTORY__", "NONE",
                {"predicates": []}, run_id, manifest_hash, identity_hash, namespaces)
        response, error = invoke_and_record(journal, final,
                "SMA_S1_HANDLER_CHECK_CONTROL_PREASSERTION")
        final_pass = error is None and response is not None \
                and response["terminalState"] == "SUCCEEDED" \
                and response["evidence"].get("anyPresent") is False
        controls.append({"operation": "FINAL_INVENTORY", "status": "PASS" if final_pass else "FAIL"})

        mechanics_status = "PASS" if len(handler_results) == 14 \
                and all(item["handlerMechanicsStatus"] == "PASS" for item in handler_results) \
                and all(item["status"] == "PASS" for item in controls) else "FAIL"
        journal.append({
            "recordType": "SMA_S1_HANDLER_IMPLEMENTATION_CHECK_SUMMARY",
            "status": mechanics_status,
            "handlerCount": len(handler_results),
            "handlerMechanicsPassCount": sum(
                item["handlerMechanicsStatus"] == "PASS" for item in handler_results),
            "substantivePredicatePassCount": sum(item["predicatePassCount"] for item in handler_results),
            "substantivePredicateFailCount": sum(item["predicateFailCount"] for item in handler_results),
            "measuredS1Execution": False,
            "scientificRepetitionsExecuted": 0,
        })

    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_S1_HANDLER_IMPLEMENTATION_CHECK_RECEIPT",
        "status": mechanics_status,
        "scope": "NON_CREDITABLE_IMPLEMENTATION_CHECK_NO_S1_PRODUCT_CLAIM",
        "recordedAt": utc_now(),
        "runId": run_id,
        "manifestSHA256": manifest_hash,
        "identityDraftSHA256": identity_hash,
        "protocolSHA256": sha256_file(protocol_path),
        "driverLauncherSHA256": sha256_file(driver_path),
        "journalSHA256": sha256_file(journal_path),
        "journalRecordCount": len(read_journal(journal_path)),
        "controls": controls,
        "handlers": handler_results,
        "measuredS1Execution": False,
        "scientificRepetitionsExecuted": 0,
        "singleUseAuthorizationConsumed": False,
        "openhandsProcessOrModelUsed": False,
        "productionOrHistoricalNamespaceAllowed": False,
    }
    write_new_file(output_root / "handler-check-receipt.json", canonical_bytes(receipt) + b"\n")
    print(
        f"SMA-S1 handler implementation checks {mechanics_status}: "
        f"handlers={len(handler_results)} substantivePredicateFailures="
        f"{sum(item['predicateFailCount'] for item in handler_results)}"
    )
    return 0 if mechanics_status == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
