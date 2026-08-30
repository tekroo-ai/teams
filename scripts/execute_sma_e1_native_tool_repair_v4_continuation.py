#!/usr/bin/env python3
from __future__ import annotations

import argparse
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
S2 = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}" for number in (1, 3, 4, 5, 6)]
ADJUDICATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-interaction-adjudication"
NATIVE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair"
V4 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4"
for package in (S2, *PACKAGES, ADJUDICATION, NATIVE, V4):
    sys.path.insert(0, str(package))
sys.path.insert(0, str(ROOT / "scripts"))

import execute_sma_e1_ddalcu_candidate_6 as base  # noqa: E402
from e1.runner import E1_CASES  # noqa: E402
from e1_native.runner import verdict_vector  # noqa: E402
from e1_native_v4.live_entrypoint import compose  # noqa: E402
from t1.model import LiveAuthority, OperationKey, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402


PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4-acceptance.json"
LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4-live-acceptance.json"
LAUNCH = NATIVE / "e1_native/live-launch-definition.json"
OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-offline-qualification/offline-qualification.json"
SOURCE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v3-all-scenario-canary"
SOURCE_WALK = SOURCE / "execution-walk.jsonl"
SOURCE_RECEIPT = SOURCE / "execution-receipt.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-continuation-13-18"
TAIL = tuple(str(row["s2Id"]) for row in E1_CASES[12:])


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


def source_basis() -> dict[str, Any]:
    rows = [json.loads(line) for line in SOURCE_WALK.read_text(encoding="utf-8").splitlines() if line]
    receipt = json.loads(SOURCE_RECEIPT.read_text(encoding="utf-8"))
    if (
        len(rows) != 13
        or any(row["vector"]["overallRepetitionVerdict"] != "PASS" for row in rows[:12])
        or rows[12]["vector"]["operationKey"] != "SMA-S2-013-FOUR-CHANNEL-CONCURRENCY#001"
        or rows[12]["observation"].get("failure_detail") != "E1 model emitted thinking/reasoning content"
        or receipt.get("status") != "NO_GO_NATIVE_TOOL_EXECUTION_STOPPED"
        or receipt.get("workload") != {"automaticReruns": 0, "executed": 13, "planned": 18}
        or receipt.get("finalInventoryClean") is not True
        or receipt.get("measuredExecution") != "NOT_RUN"
    ):
        raise RuntimeError("v3 source is not the exact clean twelve-pass checkpoint")
    return {
        "sourceWalk": str(SOURCE_WALK.relative_to(ROOT)),
        "sourceWalkSha256": file_sha256(SOURCE_WALK),
        "sourceReceipt": str(SOURCE_RECEIPT.relative_to(ROOT)),
        "sourceReceiptSha256": file_sha256(SOURCE_RECEIPT),
        "preservedRecords": 12,
        "preservedPasses": 12,
        "supersededFailedRecord": rows[12]["vector"]["operationKey"],
        "finalInventoryWasClean": True,
    }


def action(launch: Mapping[str, Any], name: str) -> Mapping[str, Any]:
    command = [launch["environment"]["livePython"], launch["nativeToolRepair"]["actionScript"], "--definition", str(LAUNCH), name]
    completed = subprocess.run(command, cwd=ROOT, capture_output=True, timeout=180, check=False)
    if completed.returncode != 0:
        raise RuntimeError(f"v4 action {name} failed: " + completed.stderr.decode(errors="replace"))
    return json.loads(completed.stdout)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--authorization", required=True)
    args = parser.parse_args()
    if OUTPUT.exists():
        raise RuntimeError("v4 continuation output already exists")
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    acceptance = json.loads(ACCEPTANCE.read_text(encoding="utf-8"))
    live_acceptance = json.loads(LIVE_ACCEPTANCE.read_text(encoding="utf-8"))
    offline = json.loads(OFFLINE.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    authorization_path = Path(args.authorization).resolve()
    authorization = json.loads(authorization_path.read_text(encoding="utf-8"))
    if (
        digest(package["identitySubject"]) != package.get("packageIdentity")
        or acceptance.get("status") != "ACCEPTED_FROZEN_FOR_ZERO_CREDIT_VALIDATION"
        or live_acceptance.get("status") != "ACCEPTED_FROZEN"
        or acceptance.get("packageIdentity") != package["packageIdentity"]
        or live_acceptance.get("packageIdentity") != package["packageIdentity"]
        or offline.get("status") != "PASS_NATIVE_TOOL_V4_OFFLINE_QUALIFICATION"
        or offline.get("packageSourceIdentity") != package["identitySubject"]["packageSourceIdentity"]
    ):
        raise RuntimeError("v4 frozen package mismatch")
    basis = source_basis()
    if (
        authorization.get("status") != "AUTHORIZED_BY_PRINCIPAL"
        or authorization.get("packageIdentity") != package["packageIdentity"]
        or authorization.get("sourceCheckpointSha256") != file_sha256(SOURCE_RECEIPT)
        or authorization.get("scope") != "SCENARIOS_13_THROUGH_18_ZERO_CREDIT_CONTINUATION"
        or authorization.get("mode") != RunMode.ZERO_CREDIT_DRESS.value
        or authorization.get("singleUse") is not True
        or authorization.get("maximumScenarios") != 6
        or authorization.get("maximumRepetitions") != 6
        or authorization.get("automaticReruns") != 0
        or authorization.get("measuredExecution") is not False
    ):
        raise RuntimeError("v4 principal authorization mismatch")
    plan = tuple(OperationKey(case_id, 1) for case_id in TAIL)
    if len(plan) != 6:
        raise RuntimeError("v4 tail plan cardinality drift")
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    preflight = base.preflight(launch)
    preflight.update({"recordType": "SMA_E1_NATIVE_TOOL_V4_CONTINUATION_PREFLIGHT", "recordedAt": recorded, "packageIdentity": package["packageIdentity"]})
    preflight["receiptSha256"] = digest(preflight)
    OUTPUT.mkdir(parents=True, exist_ok=False)
    preflight_path = OUTPUT / "read-only-preflight.json"
    write_exclusive(preflight_path, preflight)
    basis_path = OUTPUT / "continuation-basis.json"
    basis["receiptSha256"] = digest(basis)
    write_exclusive(basis_path, basis)
    identity_subject = {
        "packageIdentity": package["packageIdentity"], "packageAcceptanceSha256": file_sha256(ACCEPTANCE), "liveAcceptanceSha256": file_sha256(LIVE_ACCEPTANCE),
        "launchDefinitionSha256": file_sha256(LAUNCH), "offlineQualificationSha256": file_sha256(OFFLINE), "sourceCheckpointSha256": file_sha256(SOURCE_RECEIPT),
        "sourceWalkSha256": file_sha256(SOURCE_WALK), "continuationBasisSha256": file_sha256(basis_path), "principalGrantSha256": file_sha256(authorization_path),
        "preflightSha256": file_sha256(preflight_path), "driverSha256": file_sha256(Path(__file__).resolve()), "mode": RunMode.ZERO_CREDIT_DRESS.value,
        "planSha256": digest([key.value for key in plan]), "scenarios": 6, "repetitions": 6, "automaticReruns": 0,
    }
    execution_identity = digest(identity_subject)
    identity_path = OUTPUT / "execution-identity.json"
    write_exclusive(identity_path, {"schemaVersion": "1.0.0", "recordType": "SMA_E1_NATIVE_TOOL_V4_CONTINUATION_EXECUTION_IDENTITY", "status": "SEALED_SINGLE_USE_EXECUTION_ONLY", "recordedAt": recorded, "packageIdentity": package["packageIdentity"], "identitySubject": identity_subject, "executionIdentity": execution_identity})
    sealed_path = OUTPUT / "single-use-authorization.json"
    write_exclusive(sealed_path, {"schemaVersion": "1.0.0", "recordType": "SMA_E1_NATIVE_TOOL_V4_CONTINUATION_SEALED_AUTHORIZATION", "status": "SEALED_FROM_PRINCIPAL_GRANT", "packageIdentity": package["packageIdentity"], "executionIdentity": execution_identity, "mode": RunMode.ZERO_CREDIT_DRESS.value, "singleUse": True, "maximumScenarios": 6, "maximumRepetitions": 6, "automaticReruns": 0, "principalGrantSha256": file_sha256(authorization_path), "principalStatement": authorization["principalStatement"]})
    authority = LiveAuthority(package_manifest_path=str(LIVE_ACCEPTANCE), package_manifest_sha256=file_sha256(LIVE_ACCEPTANCE), authorization_path=str(sealed_path), authorization_sha256=file_sha256(sealed_path), execution_identity_path=str(identity_path), execution_identity_sha256=file_sha256(identity_path), mode=RunMode.ZERO_CREDIT_DRESS, consumed_marker_path=str(OUTPUT / "single-use-authority-consumed.json"))
    runner, full_plan = compose(ROOT, RunMode.ZERO_CREDIT_DRESS, authority, LAUNCH)
    if tuple(full_plan[12:]) != plan:
        raise RuntimeError("v4 continuation plan changed after sealing")
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
            record = {"vector": verdict_vector(result), "observation": observation, "scientific": [jsonable(asdict(row)) for row in result.scientific], "evidence": [jsonable(asdict(row)) for row in result.evidence], "observationSha256": digest(observation)}
            records.append(record)
            os.write(descriptor, canonical_bytes(record) + b"\n")
            os.fsync(descriptor)
            print(json.dumps({"completedTail": index, "plannedTail": 6, "operationKey": key.value, "overall": record["vector"]["overallRepetitionVerdict"], "modelRequests": len(observation.get("model_requests", [])), "selectedAnswers": record["vector"]["selectedInteractionAnswers"], "nativeTools": [row.get("nativeToolName") for row in observation.get("model_terminals", [])]}, sort_keys=True), flush=True)
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
            terminal_exception = terminal_exception or {"class": type(exc).__name__, "message": str(exc)}
        try:
            final_inventory = action(launch, "inventory_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {"class": type(exc).__name__, "message": str(exc)}
    vectors = [record["vector"] for record in records]
    prompted_statuses = [conversation.get("execution_status") for record in records for conversation in record["observation"].get("conversations", []) if any(receipt.get("kind") == "prompt_submission" and receipt.get("conversationId") == conversation.get("id") for receipt in record["observation"].get("raw_receipts", []))]
    interaction_counts_match = all(record["vector"]["selectedInteractionAnswers"] == sum(receipt.get("kind") == "prompt_submission" for receipt in record["observation"].get("raw_receipts", [])) for record in records)
    inventory_clean = final_inventory is not None and base.clean(final_inventory)
    tail_pass = bool(len(records) == 6 and all(vector["overallRepetitionVerdict"] == "PASS" for vector in vectors) and set(prompted_statuses) == {"finished"} and interaction_counts_match and terminal_exception is None and runner.ledger.verify() and inventory_clean)
    receipt = {
        "schemaVersion": "1.0.0", "recordType": "SMA_E1_NATIVE_TOOL_V4_ALL_SCENARIO_COMPOSITE_RECEIPT",
        "status": "PASS_ALL_18_SCENARIO_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION" if tail_pass else "NO_GO_V4_CONTINUATION_STOPPED",
        "recordedAt": recorded, "durationMilliseconds": (time.monotonic_ns() - started_ns) / 1_000_000, "packageIdentity": package["packageIdentity"], "executionIdentity": execution_identity,
        "sourceBasis": basis, "continuation": {"plannedScenarios": 6, "executedScenarios": len(records), "overallVerdicts": dict(Counter(vector["overallRepetitionVerdict"] for vector in vectors)), "conversationStatuses": dict(Counter(prompted_statuses)), "interactionCountsMatch": interaction_counts_match, "walk": str(walk.relative_to(ROOT)), "walkSha256": file_sha256(walk)},
        "composite": {"scenarios": 18, "passes": 12 + sum(vector["overallRepetitionVerdict"] == "PASS" for vector in vectors), "automaticReruns": 0},
        "authorityConsumedBeforeFirstAction": Path(authority.consumed_marker_path).is_file(), "ledgerValid": runner.ledger.verify(), "terminalException": terminal_exception,
        "finalCleanup": final_cleanup, "finalInventory": final_inventory, "finalInventoryClean": inventory_clean, "measuredExecution": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    receipt_path = OUTPUT / "composite-canary-receipt.json"
    write_exclusive(receipt_path, receipt)
    print(json.dumps({"status": receipt["status"], "executedTail": len(records), "compositePasses": receipt["composite"]["passes"], "ledgerValid": receipt["ledgerValid"], "finalInventoryClean": receipt["finalInventoryClean"], "receiptFileSha256": file_sha256(receipt_path), "embeddedReceiptSha256": receipt["receiptSha256"]}, sort_keys=True), flush=True)
    return 0 if tail_pass else 2


if __name__ == "__main__":
    raise SystemExit(main())
