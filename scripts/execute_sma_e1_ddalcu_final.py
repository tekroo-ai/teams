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
V5 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v5"
V6 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v6"
V9 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9"
FINAL = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final"
for package in (S2, *PACKAGES, ADJUDICATION, NATIVE, V4, V5, V6, V9, FINAL):
    sys.path.insert(0, str(package))
sys.path.insert(0, str(ROOT / "scripts"))

import execute_sma_e1_ddalcu_candidate_6 as base  # noqa: E402
from e1.runner import E1_CASES, e1_plan  # noqa: E402
from e1_candidate6.authority import sealed_execution_authorization  # noqa: E402
from e1_final.live_entrypoint import compose  # noqa: E402
from e1_native.runner import verdict_vector  # noqa: E402
from t1.model import LiveAuthority, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402


PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final-acceptance.json"
LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final-live-acceptance.json"
AUTHORIZATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final-measured-authorization.json"
OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-final-offline-qualification/offline-qualification.json"
CANARY = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v9-case18/composite-canary-receipt.json"
LAUNCH = NATIVE / "e1_native/live-launch-definition.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-final-measured-execution"


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


def action(launch: Mapping[str, Any], name: str) -> Mapping[str, Any]:
    command = [
        launch["environment"]["livePython"],
        launch["nativeToolRepair"]["actionScript"],
        "--definition",
        str(LAUNCH),
        name,
    ]
    completed = subprocess.run(command, cwd=ROOT, capture_output=True, timeout=180, check=False)
    if completed.returncode != 0:
        raise RuntimeError(f"final E1 action {name} failed: " + completed.stderr.decode(errors="replace"))
    return json.loads(completed.stdout)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--authorization", default=str(AUTHORIZATION))
    args = parser.parse_args()
    if OUTPUT.exists():
        raise RuntimeError("final E1 measured output already exists")
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    acceptance = json.loads(ACCEPTANCE.read_text(encoding="utf-8"))
    live_acceptance = json.loads(LIVE_ACCEPTANCE.read_text(encoding="utf-8"))
    authorization_path = Path(args.authorization).resolve()
    authorization = json.loads(authorization_path.read_text(encoding="utf-8"))
    offline = json.loads(OFFLINE.read_text(encoding="utf-8"))
    canary = json.loads(CANARY.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    package_identity = package["packageIdentity"]
    if digest(package["identitySubject"]) != package_identity:
        raise RuntimeError("final E1 package identity mismatch")
    if acceptance.get("status") != "ACCEPTED_FROZEN_FOR_MEASURED_EXECUTION" or live_acceptance.get("status") != "ACCEPTED_FROZEN" or len({package_identity, acceptance.get("packageIdentity"), live_acceptance.get("packageIdentity"), authorization.get("packageIdentity")}) != 1:
        raise RuntimeError("final E1 acceptance mismatch")
    if offline.get("status") != "PASS_FINAL_E1_OFFLINE_QUALIFICATION" or offline.get("packageSourceIdentity") != package["identitySubject"]["packageSourceIdentity"]:
        raise RuntimeError("final E1 offline qualification mismatch")
    if canary.get("status") != "PASS_ALL_18_SCENARIO_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION" or canary.get("composite") != {"scenarios": 18, "passes": 18, "automaticReruns": 0} or canary.get("finalInventoryClean") is not True:
        raise RuntimeError("final E1 canary prerequisite mismatch")
    if authorization.get("status") != "AUTHORIZED_BY_PRINCIPAL" or authorization.get("mode") != "MEASURED" or authorization.get("singleUse") is not True or authorization.get("maximumScenarios") != 18 or authorization.get("maximumRepetitions") != 96 or authorization.get("automaticReruns") != 0:
        raise RuntimeError("final E1 measured authorization mismatch")
    plan = e1_plan()
    expected_counts = {str(row["s2Id"]): int(row["repetitions"]) for row in E1_CASES}
    if len(plan) != 96 or Counter(key.case_id for key in plan) != Counter(expected_counts):
        raise RuntimeError("final E1 measured plan mismatch")
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    base.LAUNCH = LAUNCH
    preflight = base.preflight(launch)
    preflight.update({"recordType": "SMA_E1_DDALCU_FINAL_MEASURED_PREFLIGHT", "recordedAt": recorded, "packageIdentity": package_identity})
    preflight["receiptSha256"] = digest(preflight)
    OUTPUT.mkdir(parents=True, exist_ok=False)
    preflight_path = OUTPUT / "read-only-preflight.json"
    write_exclusive(preflight_path, preflight)
    identity_subject = {
        "packageIdentity": package_identity,
        "acceptanceSha256": file_sha256(ACCEPTANCE),
        "liveAcceptanceSha256": file_sha256(LIVE_ACCEPTANCE),
        "offlineSha256": file_sha256(OFFLINE),
        "canarySha256": file_sha256(CANARY),
        "launchSha256": file_sha256(LAUNCH),
        "authorizationSha256": file_sha256(authorization_path),
        "preflightSha256": file_sha256(preflight_path),
        "driverSha256": file_sha256(Path(__file__).resolve()),
        "mode": "MEASURED",
        "planSha256": digest([key.value for key in plan]),
        "scenarios": 18,
        "repetitions": 96,
        "automaticReruns": 0,
    }
    execution_identity = digest(identity_subject)
    identity_path = OUTPUT / "execution-identity.json"
    write_exclusive(identity_path, {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_FINAL_MEASURED_EXECUTION_IDENTITY", "status": "SEALED_SINGLE_USE_EXECUTION_ONLY", "recordedAt": recorded, "packageIdentity": package_identity, "identitySubject": identity_subject, "executionIdentity": execution_identity})
    sealed_path = OUTPUT / "single-use-authorization.json"
    write_exclusive(sealed_path, sealed_execution_authorization(principal_grant_sha256=file_sha256(authorization_path), principal_statement=str(authorization["principalStatement"]), package_identity=package_identity, execution_identity=execution_identity, mode="MEASURED", maximum_scenarios=18, maximum_repetitions=96))
    authority = LiveAuthority(package_manifest_path=str(LIVE_ACCEPTANCE), package_manifest_sha256=file_sha256(LIVE_ACCEPTANCE), authorization_path=str(sealed_path), authorization_sha256=file_sha256(sealed_path), execution_identity_path=str(identity_path), execution_identity_sha256=file_sha256(identity_path), mode=RunMode.MEASURED, consumed_marker_path=str(OUTPUT / "single-use-authority-consumed.json"))
    runner, composed_plan = compose(ROOT, RunMode.MEASURED, authority, LAUNCH)
    if tuple(composed_plan) != tuple(plan):
        raise RuntimeError("final E1 plan changed after sealing")
    walk = OUTPUT / "execution-walk.jsonl"
    records: list[dict[str, Any]] = []
    terminal_exception: dict[str, str] | None = None
    final_cleanup = None
    final_inventory = None
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
            print(json.dumps({"completed": index, "planned": 96, "operationKey": key.value, "overall": record["vector"]["overallRepetitionVerdict"], "failure": observation.get("failure"), "modelRequests": len(observation.get("model_requests", [])), "selectedAnswers": record["vector"]["selectedInteractionAnswers"]}, sort_keys=True), flush=True)
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
    executed_counts = Counter(record["vector"]["operationKey"].split("#", 1)[0] for record in records)
    prompted_statuses = [conversation.get("execution_status") for record in records for conversation in record["observation"].get("conversations", []) if any(receipt.get("kind") == "prompt_submission" and receipt.get("conversationId") == conversation.get("id") for receipt in record["observation"].get("raw_receipts", []))]
    finish_reasons = [terminal.get("finishReason") for record in records for terminal in record["observation"].get("model_terminals", [])]
    interaction_counts_match = all(record["vector"]["selectedInteractionAnswers"] == sum(receipt.get("kind") == "prompt_submission" for receipt in record["observation"].get("raw_receipts", [])) for record in records)
    all_oracles_pass = all(all(row["passed"] for row in record["scientific"]) and all(row["passed"] for row in record["evidence"]) for record in records)
    inventory_clean = final_inventory is not None and base.clean(final_inventory)
    all_pass = bool(len(records) == 96 and executed_counts == Counter(expected_counts) and all(vector["overallRepetitionVerdict"] == "PASS" for vector in vectors) and all_oracles_pass and set(prompted_statuses) == {"finished"} and finish_reasons and set(finish_reasons).issubset({"stop", "tool_calls"}) and interaction_counts_match and terminal_exception is None and runner.ledger.verify() and inventory_clean)
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_FINAL_MEASURED_EXECUTION_RECEIPT",
        "status": "PASS_MEASURED_PENDING_SCIENTIFIC_ADJUDICATION" if all_pass else "NO_GO_EXECUTION_STOPPED",
        "recordedAt": recorded,
        "durationMilliseconds": (time.monotonic_ns() - started_ns) / 1_000_000,
        "packageIdentity": package_identity,
        "executionIdentity": execution_identity,
        "authorityConsumedBeforeFirstAction": Path(authority.consumed_marker_path).is_file(),
        "workload": {"scenarios": 18, "plannedRepetitions": 96, "executedRepetitions": len(records), "completed": len(records) == 96, "automaticReruns": 0, "perScenario": dict(sorted(executed_counts.items()))},
        "walk": {"path": str(walk.relative_to(ROOT)), "sha256": file_sha256(walk), "records": len(records)},
        "overallVerdicts": dict(Counter(vector["overallRepetitionVerdict"] for vector in vectors)),
        "conversationStatuses": dict(Counter(prompted_statuses)),
        "finishReasons": dict(Counter(finish_reasons)),
        "allScientificAndEvidenceOraclesPass": all_oracles_pass,
        "interactionCountsMatch": interaction_counts_match,
        "modelCalls": sum(len(record["observation"].get("model_requests", [])) for record in records),
        "ledgerValid": runner.ledger.verify(),
        "terminalException": terminal_exception,
        "finalCleanup": final_cleanup,
        "finalInventory": final_inventory,
        "finalInventoryClean": inventory_clean,
        "scientificAdjudication": "NOT_RUN",
        "automaticRerun": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    receipt_path = OUTPUT / "execution-receipt.json"
    write_exclusive(receipt_path, receipt)
    print(json.dumps({"status": receipt["status"], "executed": len(records), "planned": 96, "overallVerdicts": receipt["overallVerdicts"], "modelCalls": receipt["modelCalls"], "ledgerValid": receipt["ledgerValid"], "finalInventoryClean": inventory_clean, "receiptFileSha256": file_sha256(receipt_path), "embeddedReceiptSha256": receipt["receiptSha256"]}, sort_keys=True), flush=True)
    return 0 if all_pass else 2


if __name__ == "__main__":
    raise SystemExit(main())
