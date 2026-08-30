#!/usr/bin/env python3
from __future__ import annotations

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
CANDIDATE_2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2"
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
CANDIDATE_1_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
sys.path.insert(0, str(CANDIDATE_2_PACKAGE))
sys.path.insert(0, str(S2_PACKAGE))
sys.path.insert(0, str(CANDIDATE_1_PACKAGE))

from e1.runner import verdict_vector  # noqa: E402
from e1_candidate2.live_entrypoint import compose  # noqa: E402
from t1.model import FailureClass, LiveAuthority, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402


EXECUTION_IDENTITY = "8aec5b1b6923264cb552edf7d9b666bc2dc304891a3b2e73dbafcbdad40a521d"
PACKAGE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2-acceptance.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2-execution-identity.json"
IDENTITY_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2-execution-identity-acceptance.json"
PREFLIGHT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-2-live-state-preflight/preflight.json"
AUTHORIZATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2-measured-execution-authorization.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-2-measured-execution"
WALK = OUTPUT / "measured-walk.jsonl"
RECEIPT = OUTPUT / "measured-execution-receipt.json"
CONSUMED = OUTPUT / "single-use-authority-consumed.json"


def jsonable(value: Any) -> Any:
    if hasattr(value, "value") and isinstance(value.value, str):
        return value.value
    if isinstance(value, Mapping):
        return {str(key): jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [jsonable(item) for item in value]
    return value


def result_record(result: Any) -> dict[str, Any]:
    observation = jsonable(asdict(result.observation))
    return {
        "vector": verdict_vector(result),
        "observation": observation,
        "s2ScientificSupport": [jsonable(asdict(row)) for row in result.scientific],
        "evidence": [jsonable(asdict(row)) for row in result.evidence],
        "observationSha256": digest(observation),
    }


def load(path: Path, record_type: str) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if value.get("recordType") != record_type:
        raise RuntimeError(f"wrong record type: {path}")
    return value


def write_exclusive(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(content)
        stream.flush()
        os.fsync(stream.fileno())


def main() -> int:
    if OUTPUT.exists():
        raise RuntimeError("candidate-2 single-use measured output already exists")
    authorization = load(AUTHORIZATION, "SMA_E1_DDALCU_CANDIDATE_2_MEASURED_EXECUTION_AUTHORIZATION")
    package_acceptance = load(PACKAGE_ACCEPTANCE, "SMA_E1_DDALCU_CANDIDATE_2_ACCEPTANCE")
    identity = load(IDENTITY, "SMA_E1_DDALCU_CANDIDATE_2_EXECUTION_IDENTITY")
    identity_acceptance = load(IDENTITY_ACCEPTANCE, "SMA_E1_DDALCU_CANDIDATE_2_EXECUTION_IDENTITY_ACCEPTANCE")
    preflight = load(PREFLIGHT, "SMA_E1_DDALCU_CANDIDATE_2_READ_ONLY_LIVE_STATE_PREFLIGHT")
    if authorization.get("executionIdentity") != EXECUTION_IDENTITY or authorization.get("mode") != "MEASURED" or authorization.get("singleUse") is not True:
        raise RuntimeError("candidate-2 measured authorization scope mismatch")
    if authorization.get("maximumScenarios") != 18 or authorization.get("maximumRepetitions") != 96 or authorization.get("automaticReruns") != 0:
        raise RuntimeError("candidate-2 measured cardinality mismatch")
    if authorization.get("preflightReceiptSha256") != file_sha256(PREFLIGHT) or preflight.get("status") != "PASS_READ_ONLY_PREFLIGHT":
        raise RuntimeError("candidate-2 preflight binding mismatch")
    if package_acceptance.get("status") != "ACCEPTED_FROZEN" or package_acceptance.get("packageIdentity") != identity.get("packageIdentity"):
        raise RuntimeError("candidate-2 package is not accepted/frozen")
    if identity.get("executionIdentity") != EXECUTION_IDENTITY or identity_acceptance.get("status") != "ACCEPTED_FROZEN_EXECUTION_IDENTITY":
        raise RuntimeError("candidate-2 execution identity is not accepted/frozen")
    if authorization.get("executionDriverSha256") != file_sha256(Path(__file__).resolve()):
        raise RuntimeError("candidate-2 execution driver drift")

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
            raise RuntimeError(f"candidate-2 frozen plan has {len(plan)} operations, expected 96")
        for index, key in enumerate(plan, start=1):
            result = runner.run_operation(key)
            record = result_record(result)
            records.append(record)
            os.write(walk_descriptor, canonical_bytes(record) + b"\n")
            os.fsync(walk_descriptor)
            print(json.dumps({"completed": index, "planned": 96, "operationKey": key.value, "overall": record["vector"]["overallRepetitionVerdict"], "failureClass": jsonable(result.observation.failure), "failureDetail": result.observation.failure_detail}, sort_keys=True), flush=True)
            if result.observation.failure in {FailureClass.HARNESS, FailureClass.ENVIRONMENT, FailureClass.SAFETY}:
                break
    except Exception as exc:
        terminal_exception = {"class": type(exc).__name__, "message": str(exc)}
    finally:
        os.fsync(walk_descriptor)
        os.close(walk_descriptor)

    vectors = [row["vector"] for row in records]
    fields = ("smaSemanticsVerdict", "openHandsBoundaryVerdict", "modelBehaviorVerdict", "environmentVerdict", "harnessVerdict", "cleanupVerdict", "safetyVerdict", "overallRepetitionVerdict")
    counts = {field: dict(sorted(Counter(row[field] for row in vectors).items())) for field in fields}
    failures = [row["operationKey"] for row in vectors if row["overallRepetitionVerdict"] != "PASS"]
    early_failure = records[-1]["observation"].get("failure") if records else None
    ledger_valid = bool(runner is not None and runner.ledger.verify())
    complete = len(records) == 96 and len(plan) == 96
    all_pass = complete and not failures and terminal_exception is None and ledger_valid
    if all_pass:
        status = "PASS_MEASURED_PENDING_SCIENTIFIC_ADJUDICATION"
    elif terminal_exception is not None or early_failure in {FailureClass.HARNESS.value, FailureClass.ENVIRONMENT.value}:
        status = "INCONCLUSIVE_MEASURED_EXECUTION"
    elif early_failure == FailureClass.SAFETY.value:
        status = "FAIL_SAFETY_STOPPED"
    else:
        status = "FAIL_MEASURED_COUNTEREXAMPLE_RETAINED"
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_2_MEASURED_EXECUTION_RECEIPT",
        "status": status,
        "startedAt": started_at,
        "finishedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "durationMilliseconds": (time.monotonic_ns() - started_ns) / 1_000_000,
        "packageIdentity": package_acceptance["packageIdentity"],
        "executionIdentity": EXECUTION_IDENTITY,
        "authorization": {"path": str(AUTHORIZATION.relative_to(ROOT)), "sha256": file_sha256(AUTHORIZATION), "mode": "MEASURED", "singleUse": True},
        "preflight": {"path": str(PREFLIGHT.relative_to(ROOT)), "sha256": file_sha256(PREFLIGHT), "status": preflight["status"]},
        "authorityConsumption": {"path": str(CONSUMED.relative_to(ROOT)), "sha256": file_sha256(CONSUMED) if CONSUMED.is_file() else None, "consumedBeforeFirstRawAction": CONSUMED.is_file()},
        "workload": {"scenarios": 18, "plannedRepetitions": 96, "executedRepetitions": len(records), "completed": complete, "automaticReruns": 0},
        "measuredWalk": {"path": str(WALK.relative_to(ROOT)), "sha256": file_sha256(WALK), "records": len(records)},
        "vectorCounts": counts,
        "failedOperationKeys": failures,
        "ledgerValid": ledger_valid,
        "terminalException": terminal_exception,
        "claimOnAcceptedPass": "INTEGRATED_RUNTIME_TUPLE_QUALIFIED",
        "step15ClosureOnAcceptedScientificAdjudication": all_pass,
        "productionOrHistoricalDataAccess": "NOT_RUN",
        "automaticRerun": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(RECEIPT, canonical_bytes(receipt) + b"\n")
    print(json.dumps({"status": status, "executed": len(records), "planned": 96, "failed": len(failures), "ledgerValid": ledger_valid, "receiptFileSha256": file_sha256(RECEIPT), "embeddedReceiptSha256": receipt["receiptSha256"]}, sort_keys=True), flush=True)
    return 0 if all_pass else 2


if __name__ == "__main__":
    raise SystemExit(main())
