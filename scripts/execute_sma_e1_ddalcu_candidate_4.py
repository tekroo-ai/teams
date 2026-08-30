#!/usr/bin/env python3
from __future__ import annotations

from collections import Counter
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess
import sys
import time
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
CANDIDATE_4_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4"
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
CANDIDATE_1_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
for package in (CANDIDATE_4_PACKAGE, S2_PACKAGE, CANDIDATE_1_PACKAGE):
    sys.path.insert(0, str(package))

from e1.runner import verdict_vector  # noqa: E402
from e1_candidate4.live_entrypoint import compose  # noqa: E402
from t1.model import LiveAuthority, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402
from execute_sma_e1_ddalcu_candidate_2 import jsonable, load, result_record, write_exclusive  # noqa: E402


EXECUTION_IDENTITY = "e1c7974dbb051714ed7f287bd3debc7d3660fd9444a9fe3eeeb67a272a4e53e7"
PACKAGE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4-acceptance.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4-execution-identity.json"
PREFLIGHT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-live-state-preflight-v2/preflight.json"
CANARY = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-case2-live-canary-adjudication/adjudication.json"
AUTHORIZATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4-measured-execution-authorization.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live-launch-definition.json"
ACTION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live_actions.py"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-measured-execution"
WALK = OUTPUT / "measured-walk.jsonl"
RECEIPT = OUTPUT / "measured-execution-receipt.json"
CONSUMED = OUTPUT / "single-use-authority-consumed.json"


def action(name: str) -> dict[str, Any]:
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    command = [launch["environment"]["livePython"], str(ACTION), "--definition", str(LAUNCH), name]
    completed = subprocess.run(command, cwd=ROOT, capture_output=True, timeout=180, check=False)
    if completed.returncode != 0:
        raise RuntimeError(f"candidate-4 action {name} failed: {completed.stderr.decode(errors='replace')}")
    return json.loads(completed.stdout)


def clean(inventory: dict[str, Any]) -> bool:
    return bool(
        inventory.get("mongoDatabasePresent") is False
        and inventory.get("semanticCollectionPresent") is False
        and inventory.get("episodicCollectionPresent") is False
        and inventory.get("processState")
        and all(state == "STOPPED" for state in inventory["processState"].values())
        and not inventory.get("activeRequests")
        and not inventory.get("workspaces")
    )


def main() -> int:
    if OUTPUT.exists():
        raise RuntimeError("candidate-4 single-use measured output already exists")
    authorization = load(AUTHORIZATION, "SMA_E1_DDALCU_CANDIDATE_4_MEASURED_EXECUTION_AUTHORIZATION")
    acceptance = load(PACKAGE_ACCEPTANCE, "SMA_E1_DDALCU_CANDIDATE_4_ACCEPTANCE")
    identity = load(IDENTITY, "SMA_E1_DDALCU_CANDIDATE_4_EXECUTION_IDENTITY")
    preflight = load(PREFLIGHT, "SMA_E1_DDALCU_CANDIDATE_4_READ_ONLY_LIVE_STATE_PREFLIGHT_V2")
    canary = load(CANARY, "SMA_E1_DDALCU_CANDIDATE_4_CASE2_REAL_PATH_CANARY_ADJUDICATION")
    if authorization.get("executionIdentity") != EXECUTION_IDENTITY or authorization.get("mode") != "MEASURED" or authorization.get("singleUse") is not True:
        raise RuntimeError("candidate-4 measured authorization scope mismatch")
    if authorization.get("maximumAttempts") != 1 or authorization.get("maximumScenarios") != 18 or authorization.get("maximumRepetitions") != 96 or authorization.get("automaticReruns") != 0:
        raise RuntimeError("candidate-4 measured cardinality mismatch")
    if authorization.get("preflightReceiptSha256") != file_sha256(PREFLIGHT) or preflight.get("status") != "PASS_READ_ONLY_PREFLIGHT":
        raise RuntimeError("candidate-4 preflight binding mismatch")
    if authorization.get("canaryAdjudicationSha256") != file_sha256(CANARY) or canary.get("status") != "PASS_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION":
        raise RuntimeError("candidate-4 real-path canary binding mismatch")
    if acceptance.get("status") != "ACCEPTED_FROZEN" or acceptance.get("packageIdentity") != identity.get("packageIdentity"):
        raise RuntimeError("candidate-4 package is not accepted/frozen")
    if identity.get("executionIdentity") != EXECUTION_IDENTITY or identity.get("status") != "ACCEPTED_FROZEN_EXECUTION_IDENTITY_BY_PRINCIPAL_PROCEED_AUTHORITY":
        raise RuntimeError("candidate-4 execution identity is not accepted/frozen")
    if authorization.get("executionDriverSha256") != file_sha256(Path(__file__).resolve()):
        raise RuntimeError("candidate-4 execution driver drift")
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
    final_cleanup: dict[str, Any] | None = None
    final_inventory: dict[str, Any] | None = None
    walk_descriptor = os.open(WALK, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        runner, plan = compose(ROOT, RunMode.MEASURED, authority, LAUNCH)
        if len(plan) != 96:
            raise RuntimeError(f"candidate-4 frozen plan has {len(plan)} operations, expected 96")
        for index, key in enumerate(plan, start=1):
            result = runner.run_operation(key)
            record = result_record(result)
            records.append(record)
            os.write(walk_descriptor, canonical_bytes(record) + b"\n")
            os.fsync(walk_descriptor)
            print(json.dumps({"completed": index, "planned": 96, "operationKey": key.value, "overall": record["vector"]["overallRepetitionVerdict"], "failureClass": jsonable(result.observation.failure), "failureDetail": result.observation.failure_detail, "modelRequests": len(result.observation.model_requests)}, sort_keys=True), flush=True)
            if record["vector"]["overallRepetitionVerdict"] != "PASS":
                break
    except BaseException as exc:
        terminal_exception = {"class": type(exc).__name__, "message": str(exc)}
    finally:
        os.fsync(walk_descriptor)
        os.close(walk_descriptor)
        try:
            final_cleanup = action("cleanup_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {"class": type(exc).__name__, "message": str(exc)}
        try:
            final_inventory = action("inventory_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {"class": type(exc).__name__, "message": str(exc)}
    vectors = [row["vector"] for row in records]
    fields = ("smaSemanticsVerdict", "openHandsBoundaryVerdict", "modelBehaviorVerdict", "environmentVerdict", "harnessVerdict", "cleanupVerdict", "safetyVerdict", "overallRepetitionVerdict")
    counts = {field: dict(sorted(Counter(row[field] for row in vectors).items())) for field in fields}
    failures = [row["operationKey"] for row in vectors if row["overallRepetitionVerdict"] != "PASS"]
    ledger_valid = bool(runner is not None and runner.ledger.verify())
    complete = len(records) == 96 and len(plan) == 96
    inventory_clean = final_inventory is not None and clean(final_inventory)
    all_pass = complete and not failures and terminal_exception is None and ledger_valid and inventory_clean
    status = "PASS_MEASURED_PENDING_SCIENTIFIC_ADJUDICATION" if all_pass else "NO_GO_MEASURED_EXECUTION_STOPPED"
    model_counts = [len(row["observation"].get("model_requests", [])) for row in records]
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_4_MEASURED_EXECUTION_RECEIPT",
        "status": status,
        "startedAt": started_at,
        "finishedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "durationMilliseconds": (time.monotonic_ns() - started_ns) / 1_000_000,
        "packageIdentity": acceptance["packageIdentity"],
        "executionIdentity": EXECUTION_IDENTITY,
        "authorization": {"path": str(AUTHORIZATION.relative_to(ROOT)), "sha256": file_sha256(AUTHORIZATION), "mode": "MEASURED", "singleUse": True},
        "preflight": {"path": str(PREFLIGHT.relative_to(ROOT)), "sha256": file_sha256(PREFLIGHT), "status": preflight["status"]},
        "realPathCanary": {"path": str(CANARY.relative_to(ROOT)), "sha256": file_sha256(CANARY), "status": canary["status"]},
        "authorityConsumption": {"path": str(CONSUMED.relative_to(ROOT)), "sha256": file_sha256(CONSUMED) if CONSUMED.is_file() else None, "consumedBeforeFirstRawAction": CONSUMED.is_file()},
        "workload": {"scenarios": 18, "plannedRepetitions": 96, "executedRepetitions": len(records), "completed": complete, "automaticReruns": 0},
        "measuredWalk": {"path": str(WALK.relative_to(ROOT)), "sha256": file_sha256(WALK), "records": len(records)},
        "vectorCounts": counts,
        "modelCallCounts": {"minimum": min(model_counts) if model_counts else None, "maximum": max(model_counts) if model_counts else None, "total": sum(model_counts), "perLogicalInteractionBound": 12},
        "failedOperationKeys": failures,
        "ledgerValid": ledger_valid,
        "terminalException": terminal_exception,
        "finalCleanup": final_cleanup,
        "finalInventory": final_inventory,
        "finalInventoryClean": inventory_clean,
        "claimOnAcceptedPass": "INTEGRATED_RUNTIME_TUPLE_QUALIFIED",
        "step15ClosureOnAcceptedScientificAdjudication": all_pass,
        "productionOrHistoricalDataAccess": "NOT_RUN",
        "automaticRerun": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(RECEIPT, canonical_bytes(receipt) + b"\n")
    print(json.dumps({"status": status, "executed": len(records), "planned": 96, "failed": len(failures), "ledgerValid": ledger_valid, "finalInventoryClean": inventory_clean, "receiptFileSha256": file_sha256(RECEIPT), "embeddedReceiptSha256": receipt["receiptSha256"]}, sort_keys=True), flush=True)
    return 0 if all_pass else 2


if __name__ == "__main__":
    raise SystemExit(main())
