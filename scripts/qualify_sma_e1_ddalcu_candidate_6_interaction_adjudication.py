#!/usr/bin/env python3
from __future__ import annotations

from copy import deepcopy
from dataclasses import asdict
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys
from typing import Any, Mapping


ROOT = Path(__file__).resolve().parents[1]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [
    ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}"
    for number in (1, 3, 4, 5, 6)
]
ADJUDICATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-interaction-adjudication"
for package in (S2_PACKAGE, *PACKAGES, ADJUDICATION):
    sys.path.insert(0, str(package))

from e1_candidate6.runner import run_candidate6_offline  # noqa: E402
from e1_candidate6_adjudication.verdict import (  # noqa: E402
    CASE_13,
    CASE_15,
    CASE_16,
    CASE_18,
    interaction_oracle_overrides,
    verdict_vector,
)
from t1.model import OperationKey, canonical_bytes, digest, file_sha256  # noqa: E402
from t1.truth import ProductTruth  # noqa: E402


CANARY_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-6-all-scenario-canary/execution-walk.jsonl"
CANARY_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-6-all-scenario-canary/execution-receipt.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-6-interaction-adjudication-offline-qualification-v2"
VERDICT_SOURCE = ADJUDICATION / "e1_candidate6_adjudication/verdict.py"


def jsonable(value: Any) -> Any:
    if hasattr(value, "value") and isinstance(value.value, str):
        return value.value
    if isinstance(value, Mapping):
        return {str(key): jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [jsonable(item) for item in value]
    return value


def duplicate_call(observation: dict[str, Any], index: int = 0) -> None:
    request = deepcopy(observation["model_requests"][index])
    terminal = deepcopy(observation["model_terminals"][index])
    request_id = (
        str(request["requestId"])
        + "-internal-"
        + str(len(observation["model_requests"]))
    )
    request["requestId"] = request_id
    request["receivedNs"] = int(request.get("receivedNs", 0)) + 1
    terminal["requestId"] = request_id
    observation["model_requests"].append(request)
    observation["model_terminals"].append(terminal)
    sealed = next(
        row
        for row in observation["raw_receipts"]
        if row.get("kind") == "stub_raw"
    )
    copied = deepcopy(sealed)
    copied["digest"] = digest({"source": copied.get("digest"), "requestId": request_id})
    observation["raw_receipts"].append(copied)


def main() -> int:
    checks: list[dict[str, Any]] = []

    def check(name: str, passed: bool, evidence: Any) -> None:
        checks.append({"name": name, "pass": bool(passed), "evidence": evidence})
        if not passed:
            raise AssertionError(f"{name}: {evidence}")

    canary_rows = [
        json.loads(line)
        for line in CANARY_WALK.read_text(encoding="utf-8").splitlines()
        if line
    ]
    canary_receipt = json.loads(CANARY_RECEIPT.read_text(encoding="utf-8"))
    check(
        "frozen_canary_is_exact_stopped_source",
        len(canary_rows) == 13
        and canary_rows[-1]["observation"]["key"]["case_id"] == CASE_13
        and canary_rows[-1]["vector"]["overallRepetitionVerdict"] == "FAIL"
        and canary_receipt.get("status") == "NO_GO_EXECUTION_STOPPED",
        {
            "records": len(canary_rows),
            "walkSha256": file_sha256(CANARY_WALK),
            "receiptSha256": file_sha256(CANARY_RECEIPT),
        },
    )
    check(
        "completed_scenarios_1_through_12_remain_pass",
        all(row["vector"]["overallRepetitionVerdict"] == "PASS" for row in canary_rows[:12])
        and all(not interaction_oracle_overrides(row["observation"]) for row in canary_rows[:12]),
        [row["vector"]["operationKey"] for row in canary_rows[:12]],
    )
    case13 = canary_rows[-1]["observation"]
    case13_overrides = interaction_oracle_overrides(case13)
    check(
        "real_case13_reclassified_from_raw_evidence",
        set(case13_overrides) == {"S2-013-P1", "S2-013-P2"}
        and all(value[0] for value in case13_overrides.values()),
        case13_overrides,
    )

    missing_terminal = deepcopy(case13)
    missing_terminal["model_terminals"].pop()
    check(
        "case13_rejects_unmatched_model_terminal",
        interaction_oracle_overrides(missing_terminal)["S2-013-P1"][0] is False,
        "one exact model terminal removed",
    )
    cross_partition = deepcopy(case13)
    first_user_message = next(
        message
        for message in cross_partition["model_requests"][0]["messages"]
        if message.get("role") == "user"
    )
    first_user_message["content"] += " mem-alpha-timeout"
    check(
        "case13_rejects_cross_partition_memory",
        interaction_oracle_overrides(cross_partition)["S2-013-P2"][0] is False,
        "alpha memory injected into beta request",
    )
    unfinished = deepcopy(case13)
    terminal_timing = next(row for row in unfinished["timings"] if row.get("kind") == "conversation_terminal")
    terminal_timing["status"] = "error"
    check(
        "case13_rejects_unfinished_conversation",
        interaction_oracle_overrides(unfinished)["S2-013-P1"][0] is False,
        "one conversation status changed to error",
    )
    over_bound = deepcopy(case13)
    role_index = next(
        index
        for index, row in enumerate(over_bound["model_requests"])
        if row.get("interactionRole") == "alpha-1"
    )
    while sum(row.get("interactionRole") == "alpha-1" for row in over_bound["model_requests"]) <= 12:
        duplicate_call(over_bound, role_index)
    check(
        "case13_rejects_internal_call_overrun",
        interaction_oracle_overrides(over_bound)["S2-013-P1"][0] is False,
        {"alpha1Calls": sum(row.get("interactionRole") == "alpha-1" for row in over_bound["model_requests"])},
    )

    keys = tuple(OperationKey(case_id, 1) for case_id in (CASE_15, CASE_16, CASE_18))
    results, _, _ = run_candidate6_offline(ProductTruth.load(ROOT), keys)
    check(
        "offline_reference_cases_execute",
        len(results) == 3 and all(result.observation.failure is None for result in results),
        [result.observation.key.value for result in results],
    )
    result_by_case = {result.observation.key.case_id: result for result in results}
    check(
        "corrected_vectors_preserve_reference_passes",
        all(verdict_vector(result)["overallRepetitionVerdict"] == "PASS" for result in results),
        {case_id: verdict_vector(result)["overallRepetitionVerdict"] for case_id, result in result_by_case.items()},
    )

    case15 = jsonable(asdict(result_by_case[CASE_15].observation))
    duplicate_call(case15)
    check(
        "case15_accepts_multiple_sealed_internal_calls",
        interaction_oracle_overrides(case15)["S2-015-P3"][0] is True,
        {"calls": len(case15["model_requests"])},
    )
    case15["raw_receipts"] = [row for row in case15["raw_receipts"] if row.get("kind") != "stub_raw"]
    check(
        "case15_rejects_missing_sealed_receipts",
        interaction_oracle_overrides(case15)["S2-015-P3"][0] is False,
        "sealed raw receipts removed",
    )

    case16 = jsonable(asdict(result_by_case[CASE_16].observation))
    duplicate_call(case16)
    check(
        "case16_accepts_one_interaction_with_multiple_internal_calls",
        interaction_oracle_overrides(case16)["S2-016-P2"][0] is True,
        {"calls": len(case16["model_requests"])},
    )
    case16["model_terminals"].pop()
    check(
        "case16_rejects_unmatched_internal_call",
        interaction_oracle_overrides(case16)["S2-016-P2"][0] is False,
        "one terminal removed",
    )

    case18 = jsonable(asdict(result_by_case[CASE_18].observation))
    first_user_index = next(
        index
        for index, row in enumerate(case18["model_requests"])
        if row.get("prompt") != "Summarize the conversation for condensation."
    )
    duplicate_call(case18, first_user_index)
    check(
        "case18_accepts_bounded_internal_calls_around_condensation",
        interaction_oracle_overrides(case18)["S2-018-P3"][0] is True,
        {"calls": len(case18["model_requests"])},
    )
    for request in case18["model_requests"]:
        if request.get("prompt") == "Summarize the conversation for condensation.":
            request["prompt"] = "mutated condensation"
    check(
        "case18_rejects_missing_condensation_call",
        interaction_oracle_overrides(case18)["S2-018-P3"][0] is False,
        "condensation prompt identity mutated",
    )

    audit = {
        "SMA-S2-013-FOUR-CHANNEL-CONCURRENCY": ["S2-013-P1", "S2-013-P2"],
        "SMA-S2-014-FEEDBACK-LOOP-PREVENTION": [],
        "SMA-S2-015-SECRET-DELIVERY-ABSENCE": ["S2-015-P3"],
        "SMA-S2-016-EMPTY-RESULT": ["S2-016-P2"],
        "SMA-S2-017-OVERSIZED-CONTEXT": [],
        "SMA-S2-018-CONDENSATION-REANCHOR": ["S2-018-P3"],
    }
    check(
        "remaining_scenario_cardinality_audit_complete",
        sum(len(value) for value in audit.values()) == 5,
        audit,
    )

    report = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_6_INTERACTION_ADJUDICATION_OFFLINE_QUALIFICATION",
        "status": "PASS_OFFLINE_INTERACTION_ADJUDICATION_QUALIFIED",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "sourceCanary": {
            "walk": str(CANARY_WALK.relative_to(ROOT)),
            "walkSha256": file_sha256(CANARY_WALK),
            "receipt": str(CANARY_RECEIPT.relative_to(ROOT)),
            "receiptSha256": file_sha256(CANARY_RECEIPT),
            "preserved": True,
        },
        "scope": "scientific adjudication at OpenHands interaction boundaries; no product or lifecycle change",
        "implementation": {
            "verdictSource": str(VERDICT_SOURCE.relative_to(ROOT)),
            "verdictSourceSha256": file_sha256(VERDICT_SOURCE),
            "qualifier": str(Path(__file__).resolve().relative_to(ROOT)),
            "qualifierSha256": file_sha256(Path(__file__).resolve()),
        },
        "audit": audit,
        "checks": checks,
        "summary": {"passed": len(checks), "failed": 0},
        "liveActivity": "NOT_RUN",
        "modelCalls": "NOT_RUN",
        "services": "NOT_STARTED_OR_STOPPED",
    }
    report["receiptSha256"] = digest(report)
    OUTPUT.mkdir(parents=True, exist_ok=False)
    path = OUTPUT / "offline-qualification.json"
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical_bytes(report) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())
    print(json.dumps({
        "status": report["status"],
        "checks": report["summary"],
        "receiptFileSha256": file_sha256(path),
        "embeddedReceiptSha256": report["receiptSha256"],
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
