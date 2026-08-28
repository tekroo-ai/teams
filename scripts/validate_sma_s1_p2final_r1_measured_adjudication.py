#!/usr/bin/env python3
"""Read-only validator for final-P2 SMA-S1 measured adjudication."""

from __future__ import annotations

import hashlib
import json
from collections import Counter
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
RUN = ROOT / "OUTPUT/phase-3/sma-s1-measured-p2final-r1-sealed-v1-attempt-1"
ADJUDICATION = RUN / "scientific-adjudication.json"


def load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"object required: {path}")
    return value


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def main() -> int:
    adjudication = load(ADJUDICATION)
    bindings = adjudication["bindings"]
    receipt = load(RUN / "execution-receipt.json")
    cleanup = load(RUN / "post-cleanup-verification.json")
    consumption = load(
        ROOT
        / "investigations/sma-q1/layered/"
        "sma-s1-execution-authorization-p2final-r1-sealed-v1-attempt-1-consumption.json"
    )
    manifest = load(ROOT / "investigations/sma-q1/layered/sma-s1-preregistration.json")
    records = [
        json.loads(line)
        for line in (RUN / "raw-receipts.jsonl").read_text(encoding="utf-8").splitlines()
        if line.strip()
    ]
    checks: list[tuple[str, bool]] = []

    def check(name: str, condition: bool) -> None:
        checks.append((name, bool(condition)))

    bound_files = {
        "manifestSHA256": ROOT / "investigations/sma-q1/layered/sma-s1-preregistration.json",
        "identitySHA256": ROOT / "investigations/sma-q1/layered/sma-s1-execution-identity-p2final-r1-sealed-v1.json",
        "offlineHarnessReceiptSHA256": ROOT / "OUTPUT/phase-3/sma-s1-harness-p2final-r1-sealed-v1/self-test-receipt.json",
        "preflightReceiptSHA256": ROOT / "OUTPUT/phase-3/sma-s1-p2final-r1-sealed-v1-preflight-receipt.json",
        "authorizationSHA256": ROOT / "investigations/sma-q1/layered/sma-s1-execution-authorization-p2final-r1-sealed-v1-attempt-1.json",
        "authorizationConsumptionStartSHA256": ROOT / "investigations/sma-q1/layered/sma-s1-execution-authorization-p2final-r1-sealed-v1-attempt-1-consumption-start.json",
        "harnessSHA256": ROOT / "scripts/sma_s1_harness_evidence_v2_candidate_4.py",
        "driverSHA256": ROOT / "scripts/sma_s1_product_driver_p2final_r1_candidate_1.py",
        "executionReceiptSHA256": RUN / "execution-receipt.json",
        "journalSHA256": RUN / "raw-receipts.jsonl",
        "postCleanupVerificationSHA256": RUN / "post-cleanup-verification.json",
    }
    for name, path in bound_files.items():
        check(f"binding_{name}", sha256_file(path) == bindings[name])

    check("receipt_pass", receipt["status"] == "PASS")
    check("receipt_completed_71", receipt["completedRepetitions"] == 71)
    check("receipt_failed_0", receipt["failedRepetitions"] == 0)
    check("receipt_no_harness_failure", receipt["harnessFailure"] is False)
    check("receipt_no_safety_stop", receipt["safetyStop"] is False)
    check("receipt_cleanup_pass", receipt["cleanupPassed"] is True)
    check("receipt_minimum_evidence", receipt["minimumPassEvidenceComplete"] is True)
    check("receipt_record_count", receipt["journalRecordCount"] == len(records) == 169)
    check("receipt_journal_hash", receipt["journalSHA256"] == sha256_file(RUN / "raw-receipts.jsonl"))

    sequences = [item.get("journalSequence") for item in records]
    check("journal_sequence", sequences == list(range(169)))

    assertions = [item for item in records if item.get("recordType") == "SMA_S1_ASSERTION_RESULT"]
    check("assertion_count", len(assertions) == 71)
    check("assertions_all_pass", all(item.get("status") == "PASS" for item in assertions))

    expected_cases = {item["id"]: item["repetitions"] for item in manifest["cases"]}
    actual_cases = Counter(item["caseId"] for item in assertions)
    check("case_distribution_14_71", actual_cases == expected_cases)

    evidence = [item for item in records if item.get("recordType") == "SMA_S1_PREASSERTION_EVIDENCE"]
    predicate_results = [
        predicate
        for item in evidence
        for predicate in item["validatedResponse"]["predicateResults"]
    ]
    check("preassertion_count", len(evidence) == 71)
    check("predicate_count", len(predicate_results) == 160)
    check("predicates_all_pass", all(item.get("passed") is True for item in predicate_results))
    check("distinct_predicates", len({item["predicate"] for item in predicate_results}) == 32)

    restart = [
        item
        for item in records
        if item.get("recordType") == "SMA_S1_RESTART_BEFORE_ASSERTION_RESULT"
    ]
    check("restart_count", len(restart) == 9)
    check("restart_all_pass", all(item.get("status") == "PASS" for item in restart))

    threshold = [
        item
        for item in records
        if item.get("recordType") == "SMA_S1_THRESHOLD_ASSERTION_RESULT"
    ]
    cleanup_assertion = [
        item
        for item in records
        if item.get("recordType") == "SMA_S1_CLEANUP_ASSERTION_RESULT"
    ]
    summaries = [item for item in records if item.get("recordType") == "SMA_S1_RUN_SUMMARY"]
    check("threshold_pass", len(threshold) == 1 and threshold[0].get("status") == "PASS")
    check(
        "cleanup_assertion_pass",
        len(cleanup_assertion) == 1 and cleanup_assertion[0].get("status") == "PASS",
    )
    check(
        "summary_pass",
        len(summaries) == 1
        and summaries[0].get("status") == "PASS"
        and summaries[0].get("completedRepetitions") == 71,
    )

    check("cleanup_receipt_pass", cleanup["status"] == "PASS")
    check("cleanup_mongo_absent", cleanup["mongodb"]["databaseAbsent"] is True)
    check("cleanup_semantic_absent", cleanup["qdrant"]["semanticCollectionAbsent"] is True)
    check("cleanup_episodic_absent", cleanup["qdrant"]["episodicCollectionAbsent"] is True)

    check("authorization_consumed", consumption["status"] == "CONSUMED_NO_REUSE")
    check("one_attempt_consumed", consumption["consumption"]["attemptsConsumed"] == 1)
    check("authorization_not_reusable", consumption["authorityBoundary"]["authorizationReusable"] is False)
    check("no_rerun", consumption["authorityBoundary"]["correctiveRerunAuthorized"] is False)
    check("no_downstream_authority", all(consumption["authorityBoundary"][key] is False for key in ("s2Authorized", "m1Authorized", "e1Authorized")))

    check("adjudication_pass", adjudication["status"] == "PASS")
    check("scientific_claim", adjudication["claimUnderInvestigation"] == "SMA_SEMANTICS_QUALIFIED")
    check("no_minimum_evidence_gaps", adjudication["minimumPassEvidenceGaps"] == [])
    check("attempt_count_frozen", adjudication["authority"]["attemptsConsumed"] == 1)
    check("no_additional_attempt", adjudication["authority"]["additionalAttemptsAuthorized"] == 0)

    failures = [name for name, passed in checks if not passed]
    if failures:
        print(
            f"SMA-S1 final-P2 measured adjudication FAIL: "
            f"{len(checks) - len(failures)}/{len(checks)}; failures={','.join(failures)}"
        )
        return 1
    print(
        f"SMA-S1 final-P2 measured adjudication PASS: "
        f"{len(checks)}/{len(checks)}; repetitions=71/71; predicates=160/160"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
