#!/usr/bin/env python3
from __future__ import annotations

import base64
from dataclasses import asdict, is_dataclass
from enum import Enum
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
from typing import Any


RUN_DIR = Path(__file__).resolve().parent
REPO = RUN_DIR.parents[2]
PACKAGE = REPO / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
sys.path.insert(0, str(PACKAGE))

from t1.live_entrypoint import compose  # noqa: E402
from t1.model import LiveAuthority, RunMode, canonical_bytes, file_sha256  # noqa: E402


EXECUTION_IDENTITY = "0336eb1753b67fd1c6c93868e3f48e77f6f3a5d0cef7969b751038a014085ed4"
PACKAGE_IDENTITY = "5b56a52511a315c7418e1476fe912b9d1f25232eda5e9b1f902ef3de361a3467"
PLAN_SHA256 = "6c37c90e875828744f1144398ba4181ef5b21385a9c4c98160a9f845bba709a7"


def _default(value: Any) -> Any:
    if isinstance(value, bytes):
        return {"base64": base64.b64encode(value).decode("ascii")}
    if isinstance(value, Enum):
        return value.value
    if is_dataclass(value):
        return asdict(value)
    raise TypeError(f"unsupported evidence value: {type(value).__name__}")


def _exclusive_write(path: Path, payload: bytes) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(payload)
        stream.flush()
        os.fsync(stream.fileno())


def _inventory() -> dict[str, Any]:
    definition = PACKAGE / "t1/live-launch-definition.json"
    action_script = PACKAGE / "t1/live_actions.py"
    live_python = json.loads(definition.read_text(encoding="utf-8"))["environment"]["livePython"]
    process = subprocess.run(
        [live_python, str(action_script), "--definition", str(definition), "inventory_owned"],
        cwd=REPO, capture_output=True, text=True, check=False, timeout=30)
    if process.returncode != 0:
        return {"inventoryError": process.stderr[-2000:], "returnCode": process.returncode}
    return json.loads(process.stdout)


def _plan_digest(values: list[str]) -> str:
    encoded = json.dumps(values, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def main() -> int:
    package = RUN_DIR / "package.json"
    authorization = RUN_DIR / "authorization.json"
    execution = RUN_DIR / "execution.json"
    assert json.loads(package.read_text(encoding="utf-8"))["packageIdentity"] == PACKAGE_IDENTITY
    assert json.loads(execution.read_text(encoding="utf-8"))["executionIdentity"] == EXECUTION_IDENTITY
    authority = LiveAuthority(
        package_manifest_path=str(package),
        package_manifest_sha256=file_sha256(package),
        authorization_path=str(authorization),
        authorization_sha256=file_sha256(authorization),
        execution_identity_path=str(execution),
        execution_identity_sha256=file_sha256(execution),
        mode=RunMode.ZERO_CREDIT_DRESS,
        consumed_marker_path=str(RUN_DIR / "consumed.json"),
    )
    runner, plan = compose(REPO, RunMode.ZERO_CREDIT_DRESS, authority)
    plan_values = [key.value for key in plan]
    assert len(plan_values) == 34
    assert len({value.split("#", 1)[0] for value in plan_values}) == 20
    assert _plan_digest(plan_values) == PLAN_SHA256

    results = runner.run(plan)
    evidence_path = RUN_DIR / "operation-evidence.jsonl"
    descriptor = os.open(evidence_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    rows: list[dict[str, Any]] = []
    with os.fdopen(descriptor, "w", encoding="utf-8") as evidence_stream:
        for result in results:
            observation = result.observation
            scientific = [asdict(item) for item in result.scientific]
            evidence = [asdict(item) for item in result.evidence]
            detail = {
                "caseId": observation.key.case_id,
                "repetition": observation.key.repetition,
                "passed": result.passed,
                "failure": observation.failure.value if observation.failure else None,
                "failureDetail": observation.failure_detail,
                "scientificOracles": scientific,
                "evidenceOracles": evidence,
                "rawReceipts": observation.raw_receipts,
                "cleanupReceipts": observation.cleanup_receipts,
                "timings": observation.timings,
                "counts": {
                    "rawEvents": len(observation.raw_events),
                    "conversations": len(observation.conversations),
                    "hooks": len(observation.hooks),
                    "modelRequests": len(observation.model_requests),
                    "modelTerminals": len(observation.model_terminals),
                    "memories": len(observation.memories),
                    "retrievals": len(observation.retrievals),
                    "semanticPoints": len(observation.semantic_points),
                    "episodicPoints": len(observation.episodic_points),
                },
            }
            evidence_stream.write(json.dumps(detail, sort_keys=True, separators=(",", ":"), ensure_ascii=False, default=_default))
            evidence_stream.write("\n")
            rows.append({
                "caseId": observation.key.case_id,
                "repetition": observation.key.repetition,
                "passed": result.passed,
                "failure": observation.failure.value if observation.failure else None,
                "failureDetail": observation.failure_detail,
                "failedScientificOracles": [item.oracle_id for item in result.scientific if not item.passed],
                "failedEvidenceOracles": [item.oracle_id for item in result.evidence if not item.passed],
            })
        evidence_stream.flush()
        os.fsync(evidence_stream.fileno())

    _exclusive_write(RUN_DIR / "evidence-ledger.json", canonical_bytes({
        "recordType": "SMA_S2_FINAL_P2_REPAIRED_DRESS_EVIDENCE_LEDGER",
        "executionIdentity": EXECUTION_IDENTITY,
        "verified": runner.ledger.verify(),
        "records": [asdict(record) for record in runner.ledger.records],
    }) + b"\n")
    inventory = _inventory()
    clean_inventory = (
        inventory.get("activeRequests") == []
        and inventory.get("workspaces") == []
        and inventory.get("mongoDatabasePresent") is False
        and inventory.get("semanticCollectionPresent") is False
        and inventory.get("episodicCollectionPresent") is False
        and all(state == "STOPPED" for state in inventory.get("processState", {}).values())
    )
    passed = len(rows) == 34 and all(row["passed"] for row in rows) and runner.ledger.verify() and clean_inventory
    receipt = {
        "recordType": "SMA_S2_FINAL_P2_REPAIRED_ZERO_CREDIT_RESULT",
        "executionIdentity": EXECUTION_IDENTITY,
        "packageIdentity": PACKAGE_IDENTITY,
        "planSha256": PLAN_SHA256,
        "status": "PASS" if passed else "FAIL",
        "casesExpected": 20,
        "operationsExpected": 34,
        "operationsCompleted": len(rows),
        "operationsPassed": sum(1 for row in rows if row["passed"]),
        "evidenceLedgerVerified": runner.ledger.verify(),
        "operationEvidenceSha256": file_sha256(evidence_path),
        "evidenceLedgerSha256": file_sha256(RUN_DIR / "evidence-ledger.json"),
        "results": rows,
        "finalInventory": inventory,
        "measuredCredit": False,
        "realModelCalls": False,
        "productionOrHistoricalData": False,
    }
    _exclusive_write(RUN_DIR / "zero-credit-result.json", canonical_bytes(receipt) + b"\n")
    print(json.dumps({
        "status": receipt["status"],
        "operationsCompleted": len(rows),
        "operationsPassed": receipt["operationsPassed"],
        "evidenceLedgerVerified": receipt["evidenceLedgerVerified"],
        "finalInventory": inventory,
    }, sort_keys=True))
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
