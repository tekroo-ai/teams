#!/usr/bin/env python3
"""Zero-credit handler checks for the final-P2 SMA-S1 R1 candidate."""

from __future__ import annotations

import argparse
import json
import time
import uuid
from pathlib import Path
from typing import Any

from sma_s1_harness_evidence_v2_candidate_4 import (
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


RUN_ID = "sma-s1-handler-check-p2final-r1-candidate-2"
NAMESPACES = {
    "mongodbDatabase": "sma_s1_driver_fixture_p2final_r1_candidate_2",
    "semanticCollection": "sma_s1_driver_fixture_semantic_p2final_r1_candidate_2",
    "episodicCollection": "sma_s1_driver_fixture_episodic_p2final_r1_candidate_2",
    "actorAlphaPartition": "sma-s1-fixture-actor-alpha-p2final-r1-candidate-2",
    "actorBetaPartition": "sma-s1-fixture-actor-beta-p2final-r1-candidate-2",
    "productionOrHistoricalDataAllowed": False,
}


def request_for(
    operation: str,
    case_id: str,
    fault: str,
    fixture: dict[str, Any],
    manifest_sha256: str,
    identity_sha256: str,
) -> dict[str, Any]:
    return {
        "protocolVersion": "1.0.0",
        "operation": operation,
        "runId": RUN_ID,
        "caseId": case_id,
        "repetition": 0,
        "manifestSHA256": manifest_sha256,
        "identitySHA256": identity_sha256,
        "fault": fault,
        "fixture": fixture,
        "namespaces": NAMESPACES,
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
    manifest_hash = sha256_file(manifest_path)
    identity_hash = sha256_file(identity_path)
    journal_path = output_root / "handler-check-journal.jsonl"
    controls: list[dict[str, Any]] = []
    handlers: list[dict[str, Any]] = []

    def invoke(
        journal: DurableJournal,
        request: dict[str, Any],
        record_type: str,
    ) -> tuple[dict[str, Any] | None, str | None]:
        evidence_id = f"handler:{uuid.uuid4()}"
        started = time.monotonic_ns()
        response, process, error_class = capture_driver(
            driver_path, request, args.timeout
        )
        request_bytes = canonical_bytes(request)
        journal.append(
            {
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
                "driverStderrSHA256": sha256_bytes(process.stderr),
                "driverStderrLength": len(process.stderr),
            }
        )
        return response, error_class

    def lifecycle(
        journal: DurableJournal,
        operation: str,
        ordinal: int,
    ) -> tuple[dict[str, Any] | None, str | None]:
        return invoke(
            journal,
            request_for(
                operation,
                f"__{operation}_{ordinal}__",
                "NONE",
                {"predicates": []},
                manifest_hash,
                identity_hash,
            ),
            "SMA_S1_R1_LIFECYCLE_PREASSERTION",
        )

    with DurableJournal(journal_path) as journal:
        journal.append(
            {
                "recordType": "SMA_S1_R1_HANDLER_CHECK_BOUNDARY",
                "runId": RUN_ID,
                "scope": "ZERO_CREDIT_ONE_INVOCATION_PER_HANDLER",
                "namespaceIdentitySHA256": sha256_bytes(canonical_bytes(NAMESPACES)),
                "measuredExecution": False,
                "scientificRepetitionsExecuted": 0,
                "authorizationConsumed": False,
            }
        )

        response, error = lifecycle(journal, "INVENTORY", 0)
        passed = (
            error is None
            and response is not None
            and response["terminalState"] == "SUCCEEDED"
            and response["evidence"].get("anyPresent") is False
        )
        controls.append({"operation": "INITIAL_INVENTORY", "status": "PASS" if passed else "FAIL"})

        response, error = lifecycle(journal, "PREPARE", 0)
        prepared = (
            error is None
            and response is not None
            and response["terminalState"] == "SUCCEEDED"
            and response["safetyStop"] is False
        )
        controls.append({"operation": "PREPARE", "status": "PASS" if prepared else "FAIL"})

        if prepared:
            for case in manifest["cases"]:
                fixture = {
                    "predicates": case["predicates"],
                    "syntheticCorpus": manifest["syntheticCorpus"],
                    "executionPhase": "SINGLE",
                }
                restart_before_passed = True
                if case["id"] in RESTART_CASES:
                    before_fixture = dict(fixture)
                    before_fixture["executionPhase"] = "BEFORE_PROCESS_RESTART"
                    before_response, before_error = invoke(
                        journal,
                        request_for(
                            "EXECUTE_CASE",
                            case["id"],
                            case["fault"],
                            before_fixture,
                            manifest_hash,
                            identity_hash,
                        ),
                        "SMA_S1_R1_RESTART_BEFORE_PREASSERTION",
                    )
                    restart_before_passed = (
                        before_error is None
                        and before_response is not None
                        and before_response["terminalState"] == "SUCCEEDED"
                        and before_response["safetyStop"] is False
                        and before_response["evidence"].get("executionPhase")
                        == "BEFORE_PROCESS_RESTART"
                        and isinstance(
                            before_response["evidence"].get("restartReceipt"), dict
                        )
                    )
                    fixture["executionPhase"] = "AFTER_PROCESS_RESTART"

                response, error = invoke(
                    journal,
                    request_for(
                        "EXECUTE_CASE",
                        case["id"],
                        case["fault"],
                        fixture,
                        manifest_hash,
                        identity_hash,
                    ),
                    "SMA_S1_R1_HANDLER_PREASSERTION",
                )
                gap = None if response is None else minimum_evidence_gap(
                    case["id"], response.get("evidence", {})
                )
                predicate_results = [] if response is None else response["predicateResults"]
                mechanics_passed = (
                    restart_before_passed
                    and error is None
                    and response is not None
                    and response["terminalState"] == "SUCCEEDED"
                    and response["safetyStop"] is False
                    and [item["predicate"] for item in predicate_results]
                    == case["predicates"]
                    and response["faultActivation"].get("activated") is True
                    and response["faultObservation"].get("observed") is True
                    and gap is None
                )
                passes = [item["passed"] for item in predicate_results]
                result = {
                    "caseId": case["id"],
                    "handlerMechanicsStatus": "PASS" if mechanics_passed else "FAIL",
                    "predicatePassCount": sum(passes),
                    "predicateFailCount": len(passes) - sum(passes),
                    "minimumEvidenceGap": gap,
                    "restartBeforePhaseStatus": "PASS" if restart_before_passed else "FAIL",
                    "scientificDisposition": (
                        "OBSERVED_ALL_TRUE"
                        if passes and all(passes)
                        else "OBSERVED_COUNTEREXAMPLE_OR_GAP"
                    ),
                }
                handlers.append(result)
                journal.append({"recordType": "SMA_S1_R1_HANDLER_RESULT", **result})

        for ordinal in (1, 2):
            response, error = lifecycle(journal, "CLEANUP", ordinal)
            passed = (
                error is None
                and response is not None
                and response["terminalState"] == "SUCCEEDED"
                and response["evidence"].get("after", {}).get("anyPresent") is False
            )
            controls.append({"operation": f"CLEANUP_{ordinal}", "status": "PASS" if passed else "FAIL"})

        response, error = lifecycle(journal, "INVENTORY", 1)
        passed = (
            error is None
            and response is not None
            and response["terminalState"] == "SUCCEEDED"
            and response["evidence"].get("anyPresent") is False
        )
        controls.append({"operation": "FINAL_INVENTORY", "status": "PASS" if passed else "FAIL"})

        status = (
            "PASS"
            if len(handlers) == 14
            and all(item["handlerMechanicsStatus"] == "PASS" for item in handlers)
            and all(item["status"] == "PASS" for item in controls)
            else "FAIL"
        )
        journal.append(
            {
                "recordType": "SMA_S1_R1_HANDLER_CHECK_SUMMARY",
                "status": status,
                "handlerCount": len(handlers),
                "handlerPassCount": sum(
                    item["handlerMechanicsStatus"] == "PASS" for item in handlers
                ),
                "predicatePassCount": sum(item["predicatePassCount"] for item in handlers),
                "predicateFailCount": sum(item["predicateFailCount"] for item in handlers),
                "measuredExecution": False,
                "scientificRepetitionsExecuted": 0,
            }
        )

    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_S1_FINAL_P2_R1_HANDLER_CHECK_RECEIPT",
        "status": status,
        "scope": "ZERO_CREDIT_IMPLEMENTATION_CHECK_NO_S1_CLAIM",
        "recordedAt": utc_now(),
        "runId": RUN_ID,
        "manifestSHA256": manifest_hash,
        "identitySHA256": identity_hash,
        "protocolSHA256": sha256_file(protocol_path),
        "driverLauncherSHA256": sha256_file(driver_path),
        "handlerCheckSHA256": sha256_file(Path(__file__).resolve()),
        "namespaceIdentitySHA256": sha256_bytes(canonical_bytes(NAMESPACES)),
        "journalSHA256": sha256_file(journal_path),
        "journalRecordCount": len(read_journal(journal_path)),
        "controls": controls,
        "handlers": handlers,
        "measuredExecution": False,
        "scientificRepetitionsExecuted": 0,
        "authorizationConsumed": False,
        "openhandsOrModelUsed": False,
        "productionOrHistoricalDataAllowed": False,
    }
    write_new_file(
        output_root / "handler-check-receipt.json",
        canonical_bytes(receipt) + b"\n",
    )
    print(
        f"SMA-S1 final-P2 R1 handler checks {status}: "
        f"handlers={len(handlers)} predicateFailures="
        f"{sum(item['predicateFailCount'] for item in handlers)}"
    )
    return 0 if status == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
