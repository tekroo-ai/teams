#!/usr/bin/env python3
from __future__ import annotations

from collections import Counter
from dataclasses import asdict
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import sys
import time
from typing import Any, Mapping


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"))
sys.path.insert(0, str(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"))

from e1.live_entrypoint import compose  # noqa: E402
from e1.runner import verdict_vector  # noqa: E402
from t1.model import FailureClass, LiveAuthority, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402


EXECUTION_IDENTITY = "b8a0fcd0ea9ff067bfcbb57b8d257bfe2977ce71a47e0831bd8fd94deed70198"
PACKAGE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-acceptance.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-execution-identity.json"
IDENTITY_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-execution-identity-acceptance.json"
PREFLIGHT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-1-live-state-preflight/preflight.json"
AUTHORIZATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-measured-execution-authorization.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-1-measured-execution"
WALK = OUTPUT / "measured-walk.jsonl"
RECEIPT = OUTPUT / "measured-execution-receipt.json"
CONSUMED = OUTPUT / "single-use-authority-consumed.json"


def _jsonable(value: Any) -> Any:
    if hasattr(value, "value") and isinstance(value.value, str):
        return value.value
    if isinstance(value, Mapping):
        return {str(key): _jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_jsonable(item) for item in value]
    return value


def _record(result: Any) -> dict[str, Any]:
    observation = _jsonable(asdict(result.observation))
    return {
        "vector": verdict_vector(result),
        "observation": observation,
        "s2ScientificSupport": [_jsonable(asdict(row)) for row in result.scientific],
        "evidence": [_jsonable(asdict(row)) for row in result.evidence],
        "observationSha256": digest(observation),
    }


def _write_exclusive(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(content)
        stream.flush()
        os.fsync(stream.fileno())


def _append_sync(descriptor: int, content: bytes) -> None:
    os.write(descriptor, content)
    os.fsync(descriptor)


def _load_checked(path: Path, expected_type: str | None = None) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if expected_type is not None and value.get("recordType") != expected_type:
        raise RuntimeError(f"wrong record type: {path}")
    return value


def main() -> int:
    if OUTPUT.exists():
        raise RuntimeError("single-use E1 measured output already exists")
    authorization = _load_checked(AUTHORIZATION, "SMA_E1_DDALCU_CANDIDATE_1_MEASURED_EXECUTION_AUTHORIZATION")
    preflight = _load_checked(PREFLIGHT, "SMA_E1_DDALCU_CANDIDATE_1_READ_ONLY_LIVE_STATE_PREFLIGHT")
    identity = _load_checked(IDENTITY, "SMA_E1_DDALCU_CANDIDATE_1_EXECUTION_IDENTITY")
    identity_acceptance = _load_checked(IDENTITY_ACCEPTANCE, "SMA_E1_DDALCU_CANDIDATE_1_EXECUTION_IDENTITY_ACCEPTANCE")
    if authorization.get("executionIdentity") != EXECUTION_IDENTITY or authorization.get("mode") != "MEASURED" or authorization.get("singleUse") is not True:
        raise RuntimeError("measured authorization scope mismatch")
    if authorization.get("maximumScenarios") != 18 or authorization.get("maximumRepetitions") != 96 or authorization.get("automaticReruns") != 0:
        raise RuntimeError("measured authorization cardinality mismatch")
    if authorization.get("preflightReceiptSha256") != file_sha256(PREFLIGHT) or preflight.get("status") != "PASS_READ_ONLY_PREFLIGHT":
        raise RuntimeError("accepted preflight binding mismatch")
    if identity.get("executionIdentity") != EXECUTION_IDENTITY or identity_acceptance.get("status") != "ACCEPTED_FROZEN_EXECUTION_IDENTITY":
        raise RuntimeError("execution identity is not accepted/frozen")
    if authorization.get("executionDriverSha256") != file_sha256(Path(__file__).resolve()):
        raise RuntimeError("execution driver drift")

    started_at = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    started_ns = time.monotonic_ns()
    OUTPUT.mkdir(parents=True, exist_ok=False)
    authority = LiveAuthority(
        package_manifest_path=str(PACKAGE_ACCEPTANCE),
        package_manifest_sha256=file_sha256(PACKAGE_ACCEPTANCE),
        authorization_path=str(AUTHORIZATION),
        authorization_sha256=file_sha256(AUTHORIZATION),
        execution_identity_path=str(IDENTITY),
        execution_identity_sha256=file_sha256(IDENTITY),
        mode=RunMode.MEASURED,
        consumed_marker_path=str(CONSUMED),
    )

    records: list[dict[str, Any]] = []
    runner = None
    plan = ()
    terminal_exception: dict[str, str] | None = None
    walk_descriptor = os.open(WALK, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        runner, plan = compose(ROOT, RunMode.MEASURED, authority)
        if len(plan) != 96:
            raise RuntimeError(f"frozen E1 plan has {len(plan)} operations, expected 96")
        for index, key in enumerate(plan, start=1):
            result = runner.run_operation(key)
            record = _record(result)
            records.append(record)
            _append_sync(walk_descriptor, canonical_bytes(record) + b"\n")
            print(json.dumps({
                "completed": index,
                "planned": len(plan),
                "operationKey": key.value,
                "overall": record["vector"]["overallRepetitionVerdict"],
                "failureClass": _jsonable(result.observation.failure),
                "failureDetail": result.observation.failure_detail,
            }, sort_keys=True), flush=True)
            if result.observation.failure in {FailureClass.HARNESS, FailureClass.ENVIRONMENT, FailureClass.SAFETY}:
                break
    except Exception as exc:
        terminal_exception = {"class": type(exc).__name__, "message": str(exc)}
    finally:
        os.fsync(walk_descriptor)
        os.close(walk_descriptor)

    vectors = [row["vector"] for row in records]
    vector_counts = {
        field: dict(sorted(Counter(row[field] for row in vectors).items()))
        for field in (
            "smaSemanticsVerdict", "openHandsBoundaryVerdict", "modelBehaviorVerdict",
            "environmentVerdict", "harnessVerdict", "cleanupVerdict", "safetyVerdict",
            "overallRepetitionVerdict",
        )
    }
    failures = [row["operationKey"] for row in vectors if row["overallRepetitionVerdict"] != "PASS"]
    early_failure = records[-1]["observation"].get("failure") if records else None
    ledger_valid = bool(runner is not None and runner.ledger.verify())
    complete = len(records) == 96 and len(plan) == 96
    all_pass = complete and not failures and terminal_exception is None and ledger_valid
    if all_pass:
        status = "PASS_MEASURED_PENDING_INDEPENDENT_ADJUDICATION"
    elif terminal_exception is not None or early_failure in {FailureClass.HARNESS.value, FailureClass.ENVIRONMENT.value}:
        status = "INCONCLUSIVE_MEASURED_EXECUTION"
    elif early_failure == FailureClass.SAFETY.value:
        status = "FAIL_SAFETY_STOPPED"
    else:
        status = "FAIL_MEASURED_COUNTEREXAMPLE_RETAINED"
    finished_at = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_1_MEASURED_EXECUTION_RECEIPT",
        "status": status,
        "startedAt": started_at,
        "finishedAt": finished_at,
        "durationMilliseconds": (time.monotonic_ns() - started_ns) / 1_000_000,
        "executionIdentity": EXECUTION_IDENTITY,
        "authorization": {"path": str(AUTHORIZATION.relative_to(ROOT)), "sha256": file_sha256(AUTHORIZATION), "mode": "MEASURED", "singleUse": True},
        "preflight": {"path": str(PREFLIGHT.relative_to(ROOT)), "sha256": file_sha256(PREFLIGHT), "status": preflight["status"]},
        "authorityConsumption": {"path": str(CONSUMED.relative_to(ROOT)), "sha256": file_sha256(CONSUMED) if CONSUMED.is_file() else None, "consumedBeforeFirstRawAction": CONSUMED.is_file()},
        "workload": {"scenarios": 18, "plannedRepetitions": 96, "executedRepetitions": len(records), "completed": complete, "automaticReruns": 0},
        "measuredWalk": {"path": str(WALK.relative_to(ROOT)), "sha256": file_sha256(WALK), "records": len(records)},
        "vectorCounts": vector_counts,
        "failedOperationKeys": failures,
        "ledgerValid": ledger_valid,
        "terminalException": terminal_exception,
        "claimOnAcceptedPass": "INTEGRATED_RUNTIME_TUPLE_QUALIFIED",
        "step15ClosureOnAcceptedScientificAdjudication": all_pass,
        "productionOrHistoricalDataAccess": "NOT_RUN",
        "automaticRerun": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    _write_exclusive(RECEIPT, canonical_bytes(receipt) + b"\n")
    print(json.dumps({
        "status": status,
        "executed": len(records),
        "planned": 96,
        "failed": len(failures),
        "ledgerValid": ledger_valid,
        "receiptFileSha256": file_sha256(RECEIPT),
        "embeddedReceiptSha256": receipt["receiptSha256"],
    }, sort_keys=True), flush=True)
    return 0 if all_pass else 2


if __name__ == "__main__":
    raise SystemExit(main())
