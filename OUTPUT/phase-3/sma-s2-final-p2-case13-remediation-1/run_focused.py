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
PREDECESSOR = REPO / "OUTPUT/phase-3/sma-s2-final-p2-repaired-measured-1/measured-result.json"
sys.path.insert(0, str(PACKAGE))

from t1.live_entrypoint import compose  # noqa: E402
from t1.model import LiveAuthority, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402


CASE_ID = "SMA-S2-013-FOUR-CHANNEL-CONCURRENCY"
EXECUTION_IDENTITY = "9632ffaf0eefad987e8eab81297ac1705b720265c3cd89513c88cbee9c77560c"
PACKAGE_IDENTITY = "540388a53d243397aa3562a07952100334c20b3d05818aafe144f9c848f653d9"
PLAN_SHA256 = "8702e1c73818967121930e9bbabce73a67982e7505333a497b5e541a6aacddaa"
PREDECESSOR_SHA256 = "f095151fe5ad0a20ee8748d04977377c4f53ff1bb6f565f82aa559c755204aba"


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


def _plan_digest(values: list[str]) -> str:
    encoded = json.dumps(
        values, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode()
    return hashlib.sha256(encoded).hexdigest()


def _inventory() -> dict[str, Any]:
    definition = PACKAGE / "t1/live-launch-definition.json"
    action_script = PACKAGE / "t1/live_actions.py"
    live_python = json.loads(definition.read_text(encoding="utf-8"))["environment"]["livePython"]
    process = subprocess.run(
        [live_python, str(action_script), "--definition", str(definition), "inventory_owned"],
        cwd=REPO,
        capture_output=True,
        text=True,
        check=False,
        timeout=30,
    )
    if process.returncode != 0:
        return {"inventoryError": process.stderr[-2000:], "returnCode": process.returncode}
    return json.loads(process.stdout)


def _clean_inventory(inventory: dict[str, Any]) -> bool:
    return (
        inventory.get("activeRequests") == []
        and inventory.get("workspaces") == []
        and inventory.get("mongoDatabasePresent") is False
        and inventory.get("semanticCollectionPresent") is False
        and inventory.get("episodicCollectionPresent") is False
        and all(
            state == "STOPPED"
            for state in inventory.get("processState", {}).values()
        )
    )


def main() -> int:
    package_path = RUN_DIR / "package.json"
    authorization_path = RUN_DIR / "authorization.json"
    execution_path = RUN_DIR / "execution.json"
    package = json.loads(package_path.read_text(encoding="utf-8"))
    authorization = json.loads(authorization_path.read_text(encoding="utf-8"))
    execution = json.loads(execution_path.read_text(encoding="utf-8"))
    assert package["status"] == "ACCEPTED_FROZEN"
    assert package["packageIdentity"] == PACKAGE_IDENTITY
    assert digest(package["packageSubject"]) == PACKAGE_IDENTITY
    assert execution["executionIdentity"] == EXECUTION_IDENTITY
    assert digest(execution["identitySubject"]) == EXECUTION_IDENTITY
    assert authorization["executionIdentity"] == EXECUTION_IDENTITY
    assert authorization["singleUse"] is True
    assert authorization["operations"] == 5
    assert file_sha256(PREDECESSOR) == PREDECESSOR_SHA256

    authority = LiveAuthority(
        package_manifest_path=str(package_path),
        package_manifest_sha256=file_sha256(package_path),
        authorization_path=str(authorization_path),
        authorization_sha256=file_sha256(authorization_path),
        execution_identity_path=str(execution_path),
        execution_identity_sha256=file_sha256(execution_path),
        mode=RunMode.MEASURED,
        consumed_marker_path=str(RUN_DIR / "consumed.json"),
    )
    runner, full_plan = compose(REPO, RunMode.MEASURED, authority)
    plan = [key for key in full_plan if key.case_id == CASE_ID]
    plan_values = [key.value for key in plan]
    assert len(plan_values) == 5
    assert _plan_digest(plan_values) == PLAN_SHA256

    results = runner.run(plan)
    evidence_path = RUN_DIR / "focused-operation-evidence.jsonl"
    evidence_descriptor = os.open(
        evidence_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600
    )
    rows: list[dict[str, Any]] = []
    with os.fdopen(evidence_descriptor, "w", encoding="utf-8") as evidence_stream:
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
            evidence_stream.write(json.dumps(
                detail,
                sort_keys=True,
                separators=(",", ":"),
                ensure_ascii=False,
                default=_default,
            ))
            evidence_stream.write("\n")
            rows.append({
                "caseId": observation.key.case_id,
                "repetition": observation.key.repetition,
                "passed": result.passed,
                "failure": observation.failure.value if observation.failure else None,
                "failureDetail": observation.failure_detail,
                "failedScientificOracles": [
                    item.oracle_id for item in result.scientific if not item.passed
                ],
                "failedEvidenceOracles": [
                    item.oracle_id for item in result.evidence if not item.passed
                ],
                "peakInFlight": next(
                    (
                        timing.get("peakInFlight")
                        for timing in observation.timings
                        if timing.get("kind") == "concurrency"
                    ),
                    None,
                ),
            })
        evidence_stream.flush()
        os.fsync(evidence_stream.fileno())

    ledger_path = RUN_DIR / "focused-evidence-ledger.json"
    _exclusive_write(ledger_path, canonical_bytes({
        "recordType": "SMA_S2_FINAL_P2_CASE13_REMEDIATION_EVIDENCE_LEDGER",
        "executionIdentity": EXECUTION_IDENTITY,
        "verified": runner.ledger.verify(),
        "records": [asdict(record) for record in runner.ledger.records],
    }) + b"\n")

    inventory = _inventory()
    focused_pass = (
        len(rows) == 5
        and all(row["passed"] for row in rows)
        and runner.ledger.verify()
        and _clean_inventory(inventory)
    )
    focused_receipt = {
        "recordType": "SMA_S2_FINAL_P2_CASE13_REMEDIATION_RESULT",
        "executionIdentity": EXECUTION_IDENTITY,
        "packageIdentity": PACKAGE_IDENTITY,
        "planSha256": PLAN_SHA256,
        "status": "PASS" if focused_pass else "FAIL",
        "caseId": CASE_ID,
        "operationsExpected": 5,
        "operationsCompleted": len(rows),
        "operationsPassed": sum(1 for row in rows if row["passed"]),
        "evidenceLedgerVerified": runner.ledger.verify(),
        "operationEvidenceSha256": file_sha256(evidence_path),
        "evidenceLedgerSha256": file_sha256(ledger_path),
        "results": rows,
        "finalInventory": inventory,
        "realModelCalls": False,
        "productionOrHistoricalData": False,
    }
    focused_path = RUN_DIR / "focused-result.json"
    _exclusive_write(focused_path, canonical_bytes(focused_receipt) + b"\n")

    predecessor = json.loads(PREDECESSOR.read_text(encoding="utf-8"))
    unaffected = [
        row for row in predecessor["results"]
        if row["caseId"] != CASE_ID
    ]
    composite_pass = (
        len(unaffected) == 98
        and all(row["passed"] for row in unaffected)
        and focused_pass
    )
    composite = {
        "recordType": "SMA_S2_FINAL_P2_COMPOSITE_MEASURED_RESULT",
        "status": "PASS" if composite_pass else "FAIL",
        "operationsExpected": 103,
        "operationsPassed": 103 if composite_pass else sum(
            1 for row in unaffected if row["passed"]
        ) + sum(1 for row in rows if row["passed"]),
        "unaffectedPredecessorOperations": len(unaffected),
        "supersededPredecessorCase13Operations": 5,
        "replacementCase13Operations": len(rows),
        "predecessorMeasuredResultSha256": PREDECESSOR_SHA256,
        "focusedResultSha256": file_sha256(focused_path),
        "focusedOperationEvidenceSha256": file_sha256(evidence_path),
        "focusedEvidenceLedgerSha256": file_sha256(ledger_path),
        "finalInventory": inventory,
        "evidenceLedgerVerified": runner.ledger.verify(),
        "realModelCalls": False,
        "productionOrHistoricalData": False,
    }
    _exclusive_write(
        RUN_DIR / "composite-result.json",
        canonical_bytes(composite) + b"\n",
    )
    print(json.dumps({
        "focusedStatus": focused_receipt["status"],
        "compositeStatus": composite["status"],
        "operationsCompleted": len(rows),
        "operationsPassed": focused_receipt["operationsPassed"],
        "peaks": [row["peakInFlight"] for row in rows],
        "evidenceLedgerVerified": focused_receipt["evidenceLedgerVerified"],
        "finalInventory": inventory,
    }, sort_keys=True))
    return 0 if composite_pass else 1


if __name__ == "__main__":
    raise SystemExit(main())
