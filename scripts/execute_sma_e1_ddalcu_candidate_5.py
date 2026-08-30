#!/usr/bin/env python3
from __future__ import annotations

from collections import Counter
from dataclasses import asdict
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess
import sys
import time
from typing import Any, Mapping


ROOT = Path(__file__).resolve().parents[1]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
CANDIDATE_1_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
CANDIDATE_3_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-3"
CANDIDATE_4_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4"
CANDIDATE_5_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-5"
for package in (
    S2_PACKAGE,
    CANDIDATE_1_PACKAGE,
    CANDIDATE_3_PACKAGE,
    CANDIDATE_4_PACKAGE,
    CANDIDATE_5_PACKAGE,
):
    sys.path.insert(0, str(package))

from e1.runner import e1_plan  # noqa: E402
from e1_candidate5.live_entrypoint import compose  # noqa: E402
from e1_candidate5.runner import verdict_vector  # noqa: E402
from execute_sma_e1_ddalcu_candidate_5_restart_canary import (  # noqa: E402
    clean,
    jsonable,
    preflight,
    write_exclusive,
)
from t1.model import (  # noqa: E402
    LiveAuthority,
    RunMode,
    canonical_bytes,
    digest,
    file_sha256,
)


PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-5-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-5-acceptance.json"
LAUNCH = CANDIDATE_5_PACKAGE / "e1_candidate5/live-launch-definition.json"
CANARY_DRIVER = ROOT / "scripts/execute_sma_e1_ddalcu_candidate_5_restart_canary.py"
CANARY_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-5-restart-continuity-canary/canary-receipt.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-5-measured-execution"
PREFLIGHT = OUTPUT / "read-only-preflight.json"
IDENTITY = OUTPUT / "execution-identity.json"
AUTHORIZATION = OUTPUT / "single-use-authorization.json"
CONSUMED = OUTPUT / "single-use-authority-consumed.json"
WALK = OUTPUT / "measured-walk.jsonl"
RECEIPT = OUTPUT / "measured-execution-receipt.json"


def action(launch: Mapping[str, Any], name: str) -> Mapping[str, Any]:
    command = [
        launch["environment"]["livePython"],
        str(CANDIDATE_5_PACKAGE / "e1_candidate5/live_actions.py"),
        "--definition",
        str(LAUNCH),
        name,
    ]
    completed = subprocess.run(
        command,
        cwd=ROOT,
        capture_output=True,
        timeout=180,
        check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError(
            f"candidate-5 action {name} failed: "
            + completed.stderr.decode(errors="replace")
        )
    return json.loads(completed.stdout)


def main() -> int:
    if OUTPUT.exists():
        raise RuntimeError("candidate-5 measured output already exists")
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    acceptance = json.loads(ACCEPTANCE.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    canary = json.loads(CANARY_RECEIPT.read_text(encoding="utf-8"))
    if digest(package["identitySubject"]) != package.get("packageIdentity"):
        raise RuntimeError("candidate-5 package identity does not recompute")
    if (
        acceptance.get("status") != "ACCEPTED_FROZEN"
        or acceptance.get("packageIdentity") != package["packageIdentity"]
        or acceptance.get("packageIdentityRecordSha256") != file_sha256(PACKAGE)
    ):
        raise RuntimeError("candidate-5 package acceptance mismatch")
    if (
        canary.get("status")
        != "PASS_RESTART_CONTINUITY_CANARY_NOT_MEASURED_EXECUTION"
        or canary.get("packageIdentity") != package["packageIdentity"]
        or canary.get("workload", {}).get("executed") != 3
        or canary.get("conversationStatuses") != {"finished": 3}
        or canary.get("finishReasons") != {"stop": 12}
        or canary.get("finalInventoryClean") is not True
    ):
        raise RuntimeError("candidate-5 restart canary is not an accepted green prerequisite")
    plan = e1_plan()
    if len(plan) != 96:
        raise RuntimeError(f"candidate-5 measured plan has {len(plan)} operations")
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    OUTPUT.mkdir(parents=True, exist_ok=False)
    preflight_receipt = preflight(launch)
    preflight_receipt.update({
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_MEASURED_READ_ONLY_PREFLIGHT",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "restartCanaryReceiptSha256": file_sha256(CANARY_RECEIPT),
    })
    preflight_receipt["receiptSha256"] = digest(preflight_receipt)
    write_exclusive(PREFLIGHT, preflight_receipt)
    identity_subject = {
        "packageIdentity": package["packageIdentity"],
        "packageAcceptanceSha256": file_sha256(ACCEPTANCE),
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "offlineQualification": package["identitySubject"]["offlineQualification"],
        "restartCanary": {
            "path": str(CANARY_RECEIPT.relative_to(ROOT)),
            "sha256": file_sha256(CANARY_RECEIPT),
        },
        "preflightSha256": file_sha256(PREFLIGHT),
        "canaryDriverSha256": file_sha256(CANARY_DRIVER),
        "measuredDriverSha256": file_sha256(Path(__file__).resolve()),
        "mode": "MEASURED",
        "planSha256": digest([key.value for key in plan]),
        "scenarios": 18,
        "repetitions": 96,
        "automaticReruns": 0,
    }
    execution_identity = digest(identity_subject)
    identity = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_MEASURED_EXECUTION_IDENTITY",
        "status": "SEALED_SINGLE_USE_MEASURED_EXECUTION_ONLY",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "identitySubject": identity_subject,
        "executionIdentity": execution_identity,
    }
    authorization = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_MEASURED_EXECUTION_AUTHORIZATION",
        "status": "AUTHORIZED_BY_PRINCIPAL_ACCEPTED_RECOMMENDATION",
        "recordedAt": recorded,
        "executionIdentity": execution_identity,
        "mode": "MEASURED",
        "singleUse": True,
        "maximumScenarios": 18,
        "maximumRepetitions": 96,
        "automaticReruns": 0,
        "principalStatement": "Accepted. Please proceed.",
    }
    write_exclusive(IDENTITY, identity)
    write_exclusive(AUTHORIZATION, authorization)
    authority = LiveAuthority(
        package_manifest_path=str(ACCEPTANCE),
        package_manifest_sha256=file_sha256(ACCEPTANCE),
        authorization_path=str(AUTHORIZATION),
        authorization_sha256=file_sha256(AUTHORIZATION),
        execution_identity_path=str(IDENTITY),
        execution_identity_sha256=file_sha256(IDENTITY),
        mode=RunMode.MEASURED,
        consumed_marker_path=str(CONSUMED),
    )
    records: list[dict[str, Any]] = []
    runner = None
    terminal_exception: dict[str, str] | None = None
    final_cleanup: Mapping[str, Any] | None = None
    final_inventory: Mapping[str, Any] | None = None
    descriptor = os.open(WALK, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    started_ns = time.monotonic_ns()
    try:
        runner, composed_plan = compose(ROOT, RunMode.MEASURED, authority, LAUNCH)
        if tuple(composed_plan) != tuple(plan):
            raise RuntimeError("candidate-5 composed measured plan drift")
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
                "completed": index,
                "planned": 96,
                "operationKey": key.value,
                "overall": record["vector"]["overallRepetitionVerdict"],
                "failure": observation.get("failure"),
                "modelRequests": len(observation.get("model_requests", [])),
            }, sort_keys=True), flush=True)
            if record["vector"]["overallRepetitionVerdict"] != "PASS":
                break
    except BaseException as exc:
        terminal_exception = {"class": type(exc).__name__, "message": str(exc)}
    finally:
        os.fsync(descriptor)
        os.close(descriptor)
        try:
            final_cleanup = action(launch, "cleanup_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {
                "class": type(exc).__name__,
                "message": str(exc),
            }
        try:
            final_inventory = action(launch, "inventory_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {
                "class": type(exc).__name__,
                "message": str(exc),
            }
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
    inventory_clean = final_inventory is not None and clean(final_inventory)
    complete = len(records) == 96
    ledger_valid = bool(runner is not None and runner.ledger.verify())
    all_pass = bool(
        complete
        and all(vector["overallRepetitionVerdict"] == "PASS" for vector in vectors)
        and set(prompted_statuses) == {"finished"}
        and finish_reasons
        and set(finish_reasons) == {"stop"}
        and terminal_exception is None
        and ledger_valid
        and inventory_clean
    )
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_MEASURED_EXECUTION_RECEIPT",
        "status": "PASS_MEASURED_PENDING_SCIENTIFIC_ADJUDICATION" if all_pass else "NO_GO_MEASURED_EXECUTION_STOPPED",
        "recordedAt": recorded,
        "durationMilliseconds": (time.monotonic_ns() - started_ns) / 1_000_000,
        "packageIdentity": package["packageIdentity"],
        "executionIdentity": execution_identity,
        "authorityConsumedBeforeFirstAction": CONSUMED.is_file(),
        "workload": {
            "scenarios": 18,
            "plannedRepetitions": 96,
            "executedRepetitions": len(records),
            "completed": complete,
            "automaticReruns": 0,
        },
        "walk": {"path": str(WALK.relative_to(ROOT)), "sha256": file_sha256(WALK), "records": len(records)},
        "overallVerdicts": Counter(vector["overallRepetitionVerdict"] for vector in vectors),
        "conversationStatuses": Counter(prompted_statuses),
        "finishReasons": Counter(finish_reasons),
        "modelCalls": sum(
            len(record["observation"].get("model_requests", []))
            for record in records
        ),
        "ledgerValid": ledger_valid,
        "terminalException": terminal_exception,
        "finalCleanup": final_cleanup,
        "finalInventory": final_inventory,
        "finalInventoryClean": inventory_clean,
        "scientificAdjudication": "NOT_RUN",
        "automaticRerun": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(RECEIPT, receipt)
    print(json.dumps({
        "status": receipt["status"],
        "executed": len(records),
        "planned": 96,
        "overallVerdicts": dict(receipt["overallVerdicts"]),
        "conversationStatuses": dict(receipt["conversationStatuses"]),
        "finishReasons": dict(receipt["finishReasons"]),
        "modelCalls": receipt["modelCalls"],
        "ledgerValid": ledger_valid,
        "finalInventoryClean": inventory_clean,
        "receiptFileSha256": file_sha256(RECEIPT),
        "embeddedReceiptSha256": receipt["receiptSha256"],
    }, sort_keys=True), flush=True)
    return 0 if all_pass else 2


if __name__ == "__main__":
    raise SystemExit(main())
