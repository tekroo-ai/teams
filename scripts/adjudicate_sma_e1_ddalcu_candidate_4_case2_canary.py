#!/usr/bin/env python3
from __future__ import annotations

from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
CANARY = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-case2-live-canary"
SOURCE_RECEIPT = CANARY / "case2-live-canary-receipt.json"
SOURCE_RESULT = CANARY / "case2-result.json"
SOURCE_IDENTITY = CANARY / "engineering-canary-execution-identity.json"
SOURCE_AUTHORIZATION = CANARY / "engineering-canary-single-use-authorization.json"
SOURCE_CONSUMED = CANARY / "single-use-authority-consumed.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live-launch-definition.json"
ACTION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live_actions.py"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-case2-live-canary-adjudication"
RECEIPT = OUTPUT / "adjudication.json"
EXPECTED_SOURCE_RECEIPT_SHA256 = "1567d684aaf47c4b49f95f43ceee79a8dd13146ee21c1c15a92c56283909ae4f"
FROZEN_TASK = "What API timeout applies to repository alpha? Answer with the number and unit only."


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def sha(path: Path) -> str:
    import hashlib
    return hashlib.sha256(path.read_bytes()).hexdigest()


def digest(value: Any) -> str:
    import hashlib
    return hashlib.sha256(canonical(value)).hexdigest()


def write_exclusive(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=False)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def inventory() -> dict[str, Any]:
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    command = [launch["environment"]["livePython"], str(ACTION), "--definition", str(LAUNCH), "inventory_owned"]
    completed = subprocess.run(command, cwd=ROOT, capture_output=True, timeout=60, check=False)
    if completed.returncode != 0:
        raise RuntimeError(completed.stderr.decode(errors="replace"))
    return json.loads(completed.stdout)


def clean(value: dict[str, Any]) -> bool:
    return bool(
        value.get("mongoDatabasePresent") is False
        and value.get("semanticCollectionPresent") is False
        and value.get("episodicCollectionPresent") is False
        and value.get("processState")
        and all(state == "STOPPED" for state in value["processState"].values())
        and not value.get("activeRequests")
        and not value.get("workspaces")
    )


def main() -> int:
    if OUTPUT.exists():
        raise RuntimeError("candidate-4 canary adjudication already exists")
    if sha(SOURCE_RECEIPT) != EXPECTED_SOURCE_RECEIPT_SHA256:
        raise RuntimeError("source canary receipt drift")
    source = json.loads(SOURCE_RECEIPT.read_text(encoding="utf-8"))
    result = json.loads(SOURCE_RESULT.read_text(encoding="utf-8"))
    identity = json.loads(SOURCE_IDENTITY.read_text(encoding="utf-8"))
    authorization = json.loads(SOURCE_AUTHORIZATION.read_text(encoding="utf-8"))
    consumed = json.loads(SOURCE_CONSUMED.read_text(encoding="utf-8"))
    if source["result"]["sha256"] != sha(SOURCE_RESULT):
        raise RuntimeError("source result drift")
    if authorization.get("executionIdentity") != identity.get("executionIdentity") or consumed.get("executionIdentity") != identity.get("executionIdentity"):
        raise RuntimeError("canary authority lineage mismatch")
    observation = result["observation"]
    vector = result["vector"]
    requests = observation["model_requests"]
    terminals = observation["model_terminals"]
    request_ids = {str(row.get("requestId")) for row in requests}
    terminal_ids = {str(row.get("requestId")) for row in terminals}
    cardinality = [row for row in observation["raw_receipts"] if row.get("kind") == "model_call_cardinality"]
    retained_cleanup = source["cleanup"]["finalInventory"]
    current_inventory = inventory()
    checks = {
        "sourceReceiptImmutable": sha(SOURCE_RECEIPT) == EXPECTED_SOURCE_RECEIPT_SHA256,
        "sourceResultImmutable": source["result"]["sha256"] == sha(SOURCE_RESULT),
        "authorityConsumedBeforeAction": source.get("authorityConsumedBeforeFirstAction") is True,
        "singleAuthorizedOperation": authorization.get("maximumOperations") == 1 and authorization.get("operationKey") == vector.get("operationKey"),
        "overallVerdictPass": vector.get("overallRepetitionVerdict") == "PASS",
        "allVerdictDimensionsPass": all(vector.get(key) == "PASS" for key in ("smaSemanticsVerdict", "openHandsBoundaryVerdict", "modelBehaviorVerdict", "environmentVerdict", "harnessVerdict", "cleanupVerdict", "safetyVerdict")),
        "threeRealModelRequestsRetained": len(requests) == 3,
        "threeRealModelTerminalsRetained": len(terminals) == 3,
        "requestTerminalIdentitiesPaired": request_ids == terminal_ids and len(request_ids) == 3,
        "frozenTaskRetainedAcrossEveryCall": all(row.get("prompt") == FROZEN_TASK for row in requests),
        "followupAgentLoopObserved": any(row.get("agentLoopRequest") is True for row in requests),
        "boundedCardinalityReceipts": len(cardinality) == 2 and all(row.get("observedCalls") == 3 and row.get("withinBound") is True and row.get("maximumCalls") == 12 for row in cardinality),
        "retainedFinalInventoryClean": clean(retained_cleanup),
        "independentPostRunInventoryClean": clean(current_inventory),
        "noTerminalException": source.get("terminalException") is None,
        "measuredExecutionNotRun": source.get("measuredExecution") == "NOT_RUN",
        "automaticRerunNotRun": source.get("automaticRerun") == "NOT_RUN",
    }
    failed = [key for key, passed in checks.items() if not passed]
    adjudication = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_4_CASE2_REAL_PATH_CANARY_ADJUDICATION",
        "status": "PASS_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION" if not failed else "NO_GO_REAL_PATH_CANARY_ADJUDICATION",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "sourceCanaryReceipt": {"path": str(SOURCE_RECEIPT.relative_to(ROOT)), "sha256": sha(SOURCE_RECEIPT), "reportedStatus": source["status"]},
        "sourceResult": {"path": str(SOURCE_RESULT.relative_to(ROOT)), "sha256": sha(SOURCE_RESULT)},
        "executionIdentity": identity["executionIdentity"],
        "checks": checks,
        "failedChecks": failed,
        "modelCalls": {"requests": len(requests), "terminals": len(terminals), "paired": request_ids == terminal_ids, "boundedMaximum": 12},
        "cleanup": {"retained": retained_cleanup, "independentReadOnlyRecheck": current_inventory},
        "originalNoGoCause": "AUDIT_CHECKER_EXPECTED_OBSOLETE_NESTED_INVENTORY_FIELD_NAMES",
        "engineeringConclusion": "THE_REAL_PRODUCT_PATH_PASSED; THE_ORIGINAL_NO_GO WAS CAUSED_ONLY_BY_THE_CANARY_CHECKER_SCHEMA_MISMATCH",
        "modelOperationRerun": "NOT_RUN",
        "measuredExecution": "NOT_RUN",
    }
    adjudication["receiptSha256"] = digest(adjudication)
    write_exclusive(RECEIPT, adjudication)
    print(json.dumps({"status": adjudication["status"], "failedChecks": failed, "modelCalls": adjudication["modelCalls"], "receiptFileSha256": sha(RECEIPT), "embeddedReceiptSha256": adjudication["receiptSha256"]}, sort_keys=True))
    return 0 if not failed else 2


if __name__ == "__main__":
    raise SystemExit(main())
