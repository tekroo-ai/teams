#!/usr/bin/env python3
from __future__ import annotations

import argparse
from collections import Counter
from dataclasses import asdict
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys
import time
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
sys.path.insert(0, str(ROOT / "scripts"))

import execute_sma_e1_ddalcu_candidate_6 as base  # noqa: E402
from e1_candidate6.live_entrypoint import compose  # noqa: E402
from e1_candidate6_adjudication.verdict import (  # noqa: E402
    case13_continuation_basis,
    verdict_vector,
)
from e1.runner import E1_CASES  # noqa: E402
from t1.model import LiveAuthority, OperationKey, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402


PACKAGE = base.PACKAGE
ACCEPTANCE = base.ACCEPTANCE
LAUNCH = base.LAUNCH
SOURCE_CANARY = base.CANARY_OUTPUT
SOURCE_WALK = SOURCE_CANARY / "execution-walk.jsonl"
SOURCE_RECEIPT = SOURCE_CANARY / "execution-receipt.json"
OFFLINE_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-6-interaction-adjudication-offline-qualification-v2/offline-qualification.json"
VERDICT_SOURCE = ADJUDICATION / "e1_candidate6_adjudication/verdict.py"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-6-all-scenario-canary-continuation"
TAIL_CASES = tuple(str(row["s2Id"]) for row in E1_CASES[13:])


def source_basis() -> dict[str, Any]:
    rows = [
        json.loads(line)
        for line in SOURCE_WALK.read_text(encoding="utf-8").splitlines()
        if line
    ]
    receipt = json.loads(SOURCE_RECEIPT.read_text(encoding="utf-8"))
    if (
        len(rows) != 13
        or any(row["vector"]["overallRepetitionVerdict"] != "PASS" for row in rows[:12])
        or receipt.get("status") != "NO_GO_EXECUTION_STOPPED"
        or receipt.get("workload", {}).get("executedRepetitions") != 13
        or receipt.get("finalInventoryClean") is not True
        or receipt.get("automaticRerun") != "NOT_RUN"
    ):
        raise RuntimeError("frozen source canary is not the exact clean 13-scenario checkpoint")
    case13 = case13_continuation_basis(rows[-1])
    if case13["correctedOverallVerdict"] != "PASS":
        raise RuntimeError("scenario 13 does not pass the qualified interaction adjudication")
    return {
        "sourceWalk": str(SOURCE_WALK.relative_to(ROOT)),
        "sourceWalkSha256": file_sha256(SOURCE_WALK),
        "sourceReceipt": str(SOURCE_RECEIPT.relative_to(ROOT)),
        "sourceReceiptSha256": file_sha256(SOURCE_RECEIPT),
        "preservedRecords": 13,
        "preservedPasses": 12,
        "correctedScenario13": case13,
        "finalInventoryWasClean": True,
    }


def write_exclusive(path: Path, value: Any) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical_bytes(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def jsonable(value: Any) -> Any:
    if hasattr(value, "value") and isinstance(value.value, str):
        return value.value
    if isinstance(value, Mapping):
        return {str(key): jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [jsonable(item) for item in value]
    return value


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--authorization", required=True)
    args = parser.parse_args()
    if OUTPUT.exists():
        raise RuntimeError("candidate-6 continuation output already exists")
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    acceptance = json.loads(ACCEPTANCE.read_text(encoding="utf-8"))
    offline = json.loads(OFFLINE_RECEIPT.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    authorization_path = Path(args.authorization).resolve()
    authorization = json.loads(authorization_path.read_text(encoding="utf-8"))
    if (
        digest(package["identitySubject"]) != package.get("packageIdentity")
        or acceptance.get("status") != "ACCEPTED_FROZEN"
        or acceptance.get("packageIdentity") != package.get("packageIdentity")
    ):
        raise RuntimeError("candidate-6 frozen package identity mismatch")
    if (
        offline.get("status") != "PASS_OFFLINE_INTERACTION_ADJUDICATION_QUALIFIED"
        or offline.get("implementation", {}).get("verdictSourceSha256") != file_sha256(VERDICT_SOURCE)
        or offline.get("summary") != {"passed": 16, "failed": 0}
    ):
        raise RuntimeError("interaction adjudication qualification mismatch")
    basis = source_basis()
    if (
        authorization.get("status") != "AUTHORIZED_BY_PRINCIPAL"
        or authorization.get("packageIdentity") != package["packageIdentity"]
        or authorization.get("sourceCanaryReceiptSha256") != file_sha256(SOURCE_RECEIPT)
        or authorization.get("offlineAdjudicationReceiptSha256") != file_sha256(OFFLINE_RECEIPT)
        or authorization.get("scope") != "SCENARIOS_14_THROUGH_18_ZERO_CREDIT_CONTINUATION"
        or authorization.get("mode") != RunMode.ZERO_CREDIT_DRESS.value
        or authorization.get("singleUse") is not True
        or authorization.get("maximumScenarios") != 5
        or authorization.get("maximumRepetitions") != 5
        or authorization.get("automaticReruns") != 0
        or not str(authorization.get("principalStatement") or "").strip()
    ):
        raise RuntimeError("candidate-6 continuation principal authorization mismatch")
    plan = tuple(OperationKey(case_id, 1) for case_id in TAIL_CASES)
    full_canary_plan = tuple(
        OperationKey(str(row["s2Id"]), 1)
        for row in E1_CASES
    )
    if len(plan) != 5 or full_canary_plan[13:] != plan:
        raise RuntimeError("candidate-6 continuation plan cardinality drift")
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    preflight = base.preflight(launch)
    preflight.update({
        "recordType": "SMA_E1_DDALCU_CANDIDATE_6_CONTINUATION_READ_ONLY_PREFLIGHT",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
    })
    preflight["receiptSha256"] = digest(preflight)
    OUTPUT.mkdir(parents=True, exist_ok=False)
    preflight_path = OUTPUT / "read-only-preflight.json"
    write_exclusive(preflight_path, preflight)
    basis_path = OUTPUT / "continuation-basis.json"
    basis_record = dict(basis)
    basis_record.update({
        "recordType": "SMA_E1_DDALCU_CANDIDATE_6_CONTINUATION_BASIS",
        "offlineAdjudicationReceiptSha256": file_sha256(OFFLINE_RECEIPT),
        "verdictSourceSha256": file_sha256(VERDICT_SOURCE),
    })
    basis_record["receiptSha256"] = digest(basis_record)
    write_exclusive(basis_path, basis_record)
    identity_subject = {
        "packageIdentity": package["packageIdentity"],
        "packageAcceptanceSha256": file_sha256(ACCEPTANCE),
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "sourceCanaryReceiptSha256": file_sha256(SOURCE_RECEIPT),
        "sourceCanaryWalkSha256": file_sha256(SOURCE_WALK),
        "continuationBasisSha256": file_sha256(basis_path),
        "offlineAdjudicationReceiptSha256": file_sha256(OFFLINE_RECEIPT),
        "verdictSourceSha256": file_sha256(VERDICT_SOURCE),
        "principalGrantSha256": file_sha256(authorization_path),
        "preflightSha256": file_sha256(preflight_path),
        "driverSha256": file_sha256(Path(__file__).resolve()),
        "mode": RunMode.ZERO_CREDIT_DRESS.value,
        "planSha256": digest([key.value for key in plan]),
        "scenarios": 5,
        "repetitions": 5,
        "automaticReruns": 0,
    }
    execution_identity = digest(identity_subject)
    identity_path = OUTPUT / "execution-identity.json"
    identity = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_6_CONTINUATION_EXECUTION_IDENTITY",
        "status": "SEALED_SINGLE_USE_EXECUTION_ONLY",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "identitySubject": identity_subject,
        "executionIdentity": execution_identity,
    }
    write_exclusive(identity_path, identity)
    sealed_authorization_path = OUTPUT / "single-use-authorization.json"
    sealed_authorization = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_6_CONTINUATION_SEALED_AUTHORIZATION",
        "status": "SEALED_FROM_PRINCIPAL_GRANT",
        "packageIdentity": package["packageIdentity"],
        "executionIdentity": execution_identity,
        "mode": RunMode.ZERO_CREDIT_DRESS.value,
        "singleUse": True,
        "maximumScenarios": 5,
        "maximumRepetitions": 5,
        "automaticReruns": 0,
        "principalGrantSha256": file_sha256(authorization_path),
        "principalStatement": authorization["principalStatement"],
    }
    write_exclusive(sealed_authorization_path, sealed_authorization)
    authority = LiveAuthority(
        package_manifest_path=str(ACCEPTANCE),
        package_manifest_sha256=file_sha256(ACCEPTANCE),
        authorization_path=str(sealed_authorization_path),
        authorization_sha256=file_sha256(sealed_authorization_path),
        execution_identity_path=str(identity_path),
        execution_identity_sha256=file_sha256(identity_path),
        mode=RunMode.ZERO_CREDIT_DRESS,
        consumed_marker_path=str(OUTPUT / "single-use-authority-consumed.json"),
    )
    runner, composed_plan = compose(ROOT, RunMode.ZERO_CREDIT_DRESS, authority, LAUNCH)
    if tuple(composed_plan[13:]) != plan:
        raise RuntimeError("candidate-6 continuation plan changed after identity sealing")
    walk = OUTPUT / "continuation-walk.jsonl"
    records: list[dict[str, Any]] = []
    terminal_exception: dict[str, str] | None = None
    final_cleanup: Mapping[str, Any] | None = None
    final_inventory: Mapping[str, Any] | None = None
    descriptor = os.open(walk, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    started_ns = time.monotonic_ns()
    try:
        for index, key in enumerate(plan, start=1):
            result = runner.run_operation(key)
            observation = jsonable(asdict(result.observation))
            record = {
                "vector": verdict_vector(result),
                "observation": observation,
                "scientific": [jsonable(asdict(row)) for row in result.scientific],
                "evidence": [jsonable(asdict(row)) for row in result.evidence],
                "observationSha256": digest(observation),
            }
            records.append(record)
            os.write(descriptor, canonical_bytes(record) + b"\n")
            os.fsync(descriptor)
            print(json.dumps({
                "completedTail": index,
                "plannedTail": 5,
                "operationKey": key.value,
                "overall": record["vector"]["overallRepetitionVerdict"],
                "modelRequests": len(observation.get("model_requests", [])),
                "selectedAnswers": record["vector"]["selectedInteractionAnswers"],
            }, sort_keys=True), flush=True)
            if record["vector"]["overallRepetitionVerdict"] != "PASS":
                break
    except BaseException as exc:
        terminal_exception = {"class": type(exc).__name__, "message": str(exc)}
    finally:
        os.fsync(descriptor)
        os.close(descriptor)
        try:
            final_cleanup = base.action(launch, "cleanup_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {"class": type(exc).__name__, "message": str(exc)}
        try:
            final_inventory = base.action(launch, "inventory_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {"class": type(exc).__name__, "message": str(exc)}
    vectors = [record["vector"] for record in records]
    prompted_statuses = [
        conversation.get("execution_status")
        for record in records
        for conversation in record["observation"].get("conversations", [])
        if any(
            receipt.get("kind") == "prompt_submission"
            and receipt.get("conversationId") == conversation.get("id")
            for receipt in record["observation"].get("raw_receipts", [])
        )
    ]
    finish_reasons = [
        terminal.get("finishReason")
        for record in records
        for terminal in record["observation"].get("model_terminals", [])
    ]
    interaction_counts_match = all(
        record["vector"]["selectedInteractionAnswers"]
        == sum(
            receipt.get("kind") == "prompt_submission"
            for receipt in record["observation"].get("raw_receipts", [])
        )
        for record in records
    )
    inventory_clean = final_inventory is not None and base.clean(final_inventory)
    tail_pass = bool(
        len(records) == 5
        and all(vector["overallRepetitionVerdict"] == "PASS" for vector in vectors)
        and set(prompted_statuses) == {"finished"}
        and bool(finish_reasons)
        and set(finish_reasons) == {"stop"}
        and interaction_counts_match
        and terminal_exception is None
        and runner.ledger.verify()
        and inventory_clean
    )
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_6_ALL_SCENARIO_CANARY_COMPOSITE_RECEIPT",
        "status": "PASS_ALL_18_SCENARIO_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION" if tail_pass else "NO_GO_CONTINUATION_STOPPED",
        "recordedAt": recorded,
        "durationMilliseconds": (time.monotonic_ns() - started_ns) / 1_000_000,
        "packageIdentity": package["packageIdentity"],
        "executionIdentity": execution_identity,
        "sourceBasis": basis_record,
        "continuation": {
            "plannedScenarios": 5,
            "executedScenarios": len(records),
            "overallVerdicts": dict(Counter(vector["overallRepetitionVerdict"] for vector in vectors)),
            "conversationStatuses": dict(Counter(prompted_statuses)),
            "finishReasons": dict(Counter(finish_reasons)),
            "interactionCountsMatch": interaction_counts_match,
            "walk": str(walk.relative_to(ROOT)),
            "walkSha256": file_sha256(walk),
        },
        "composite": {
            "scenarios": 18,
            "passes": 18 if tail_pass else 13 + sum(vector["overallRepetitionVerdict"] == "PASS" for vector in vectors),
            "automaticReruns": 0,
        },
        "authorityConsumedBeforeFirstAction": Path(authority.consumed_marker_path).is_file(),
        "ledgerValid": runner.ledger.verify(),
        "terminalException": terminal_exception,
        "finalCleanup": final_cleanup,
        "finalInventory": final_inventory,
        "finalInventoryClean": inventory_clean,
        "measuredExecution": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    receipt_path = OUTPUT / "composite-canary-receipt.json"
    write_exclusive(receipt_path, receipt)
    print(json.dumps({
        "status": receipt["status"],
        "executedTail": len(records),
        "compositePasses": receipt["composite"]["passes"],
        "ledgerValid": receipt["ledgerValid"],
        "finalInventoryClean": receipt["finalInventoryClean"],
        "receiptFileSha256": file_sha256(receipt_path),
        "embeddedReceiptSha256": receipt["receiptSha256"],
    }, sort_keys=True), flush=True)
    return 0 if tail_pass else 2


if __name__ == "__main__":
    raise SystemExit(main())
