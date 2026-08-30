#!/usr/bin/env python3
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys

RUN_DIR = Path(__file__).resolve().parent
REPO = RUN_DIR.parents[2]
PACKAGE = REPO / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
sys.path.insert(0, str(PACKAGE))

from t1.live_entrypoint import compose  # noqa: E402
from t1.model import LiveAuthority, RunMode, canonical_bytes, file_sha256  # noqa: E402

SELECTED = {
    ("SMA-S2-014-FEEDBACK-LOOP-PREVENTION", 1),
    ("SMA-S2-018-CONDENSATION-REANCHOR", 1),
    ("SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN", 1),
    ("SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN", 2),
    ("SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN", 3),
}


def main() -> int:
    package = RUN_DIR / "package.json"
    authorization = RUN_DIR / "authorization.json"
    execution = RUN_DIR / "execution.json"
    execution_document = json.loads(execution.read_text())
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
    runner, complete_plan = compose(REPO, RunMode.ZERO_CREDIT_DRESS, authority)
    plan = tuple(
        item for item in complete_plan
        if (item.case_id, item.repetition) in SELECTED
    )
    if len(plan) != 5:
        raise RuntimeError(f"expected five focused operations, found {len(plan)}")
    results = runner.run(plan)
    rows = [{
        "caseId": result.observation.key.case_id,
        "repetition": result.observation.key.repetition,
        "passed": result.passed,
        "failure": result.observation.failure.value if result.observation.failure else None,
        "failureDetail": result.observation.failure_detail,
        "modelRequests": len(result.observation.model_requests),
        "modelTerminals": len(result.observation.model_terminals),
        "failedScientificOracles": [
            item.oracle_id for item in result.scientific if not item.passed
        ],
        "failedEvidenceOracles": [
            item.oracle_id for item in result.evidence if not item.passed
        ],
    } for result in results]
    definition = PACKAGE / "t1/live-launch-definition.json"
    action_script = PACKAGE / "t1/live_actions.py"
    live_python = json.loads(definition.read_text())["environment"]["livePython"]
    inventory_process = subprocess.run(
        [
            live_python, str(action_script), "--definition", str(definition),
            "inventory_owned",
        ],
        cwd=REPO, capture_output=True, text=True, check=False, timeout=30,
    )
    inventory = (
        json.loads(inventory_process.stdout)
        if inventory_process.returncode == 0
        else {"inventoryError": inventory_process.stderr[-1000:]}
    )
    passed = len(rows) == 5 and all(row["passed"] for row in rows)
    receipt = {
        "recordType": "SMA_S2_FINAL_P2_FOCUSED_RESUME_RESULT",
        "executionIdentity": execution_document["executionIdentity"],
        "status": "PASS" if passed else "FAIL",
        "checkpoint": "POST_RUN_7_REMAINING_FIVE",
        "operationsExpected": 5,
        "operationsCompleted": len(rows),
        "operationsPassed": sum(1 for row in rows if row["passed"]),
        "results": rows,
        "finalInventory": inventory,
        "measuredCredit": False,
    }
    path = RUN_DIR / "focused-result.json"
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical_bytes(receipt) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())
    print(json.dumps({
        "status": receipt["status"],
        "operationsCompleted": receipt["operationsCompleted"],
        "operationsPassed": receipt["operationsPassed"],
        "finalInventory": inventory,
    }, sort_keys=True))
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
