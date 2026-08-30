#!/usr/bin/env python3
from __future__ import annotations

import argparse
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
for package in (S2, *PACKAGES, ADJUDICATION, NATIVE, V4, V5):
    sys.path.insert(0, str(package))
sys.path.insert(0, str(ROOT / "scripts"))

import execute_sma_e1_ddalcu_candidate_6 as base  # noqa: E402
from e1_native.runner import verdict_vector  # noqa: E402
from e1_native_v5.live_entrypoint import compose  # noqa: E402
from t1.model import LiveAuthority, OperationKey, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402


PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v5-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v5-acceptance.json"
LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v5-live-acceptance.json"
AUTHORITY_RECORD = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v5-case18-authorization.json"
LAUNCH = NATIVE / "e1_native/live-launch-definition.json"
OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v5-offline-qualification/offline-qualification.json"
V3_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v3-all-scenario-canary/execution-walk.jsonl"
V3_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v3-all-scenario-canary/execution-receipt.json"
V4_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-continuation-13-18/continuation-walk.jsonl"
V4_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-continuation-13-18/composite-canary-receipt.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v5-case18"
KEY = OperationKey("SMA-S2-018-CONDENSATION-REANCHOR", 1)


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
    v3 = [json.loads(line) for line in V3_WALK.read_text(encoding="utf-8").splitlines() if line]
    v4 = [json.loads(line) for line in V4_WALK.read_text(encoding="utf-8").splitlines() if line]
    if len(v3) != 13 or any(row["vector"]["overallRepetitionVerdict"] != "PASS" for row in v3[:12]) or len(v4) != 6 or any(row["vector"]["overallRepetitionVerdict"] != "PASS" for row in v4[:5]) or v4[-1]["observation"].get("failure_detail") != "condensation failed" or json.loads(V3_RECEIPT.read_text())["finalInventoryClean"] is not True or json.loads(V4_RECEIPT.read_text())["finalInventoryClean"] is not True:
        raise RuntimeError("v5 source checkpoints do not preserve exactly seventeen passes")
    return {"preservedPasses": 17, "v3WalkSha256": file_sha256(V3_WALK), "v3ReceiptSha256": file_sha256(V3_RECEIPT), "v4WalkSha256": file_sha256(V4_WALK), "v4ReceiptSha256": file_sha256(V4_RECEIPT), "supersededFailure": "SMA-S2-018-CONDENSATION-REANCHOR#001", "finalInventoriesWereClean": True}


def action(launch: Mapping[str, Any], name: str) -> Mapping[str, Any]:
    command = [launch["environment"]["livePython"], launch["nativeToolRepair"]["actionScript"], "--definition", str(LAUNCH), name]
    completed = subprocess.run(command, cwd=ROOT, capture_output=True, timeout=180, check=False)
    if completed.returncode != 0:
        raise RuntimeError(f"v5 action {name} failed: " + completed.stderr.decode(errors="replace"))
    return json.loads(completed.stdout)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--authorization", default=str(AUTHORITY_RECORD))
    args = parser.parse_args()
    if OUTPUT.exists():
        raise RuntimeError("v5 case18 output already exists")
    package, acceptance, live_acceptance, offline = (json.loads(path.read_text(encoding="utf-8")) for path in (PACKAGE, ACCEPTANCE, LIVE_ACCEPTANCE, OFFLINE))
    authorization_path = Path(args.authorization).resolve()
    authorization = json.loads(authorization_path.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    if digest(package["identitySubject"]) != package["packageIdentity"] or acceptance.get("status") != "ACCEPTED_FROZEN_FOR_ZERO_CREDIT_VALIDATION" or live_acceptance.get("status") != "ACCEPTED_FROZEN" or not (package["packageIdentity"] == acceptance.get("packageIdentity") == live_acceptance.get("packageIdentity") == authorization.get("packageIdentity")) or offline.get("status") != "PASS_NATIVE_TOOL_V5_OFFLINE_QUALIFICATION" or offline.get("packageSourceIdentity") != package["identitySubject"]["packageSourceIdentity"]:
        raise RuntimeError("v5 frozen package mismatch")
    basis = source_basis()
    if authorization.get("status") != "AUTHORIZED_BY_PRINCIPAL" or authorization.get("scope") != "SCENARIO_18_ZERO_CREDIT_CONTINUATION" or authorization.get("mode") != RunMode.ZERO_CREDIT_DRESS.value or authorization.get("singleUse") is not True or authorization.get("maximumScenarios") != 1 or authorization.get("maximumRepetitions") != 1 or authorization.get("automaticReruns") != 0 or authorization.get("measuredExecution") is not False or authorization.get("sourceV4ReceiptSha256") != file_sha256(V4_RECEIPT):
        raise RuntimeError("v5 authority mismatch")
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    preflight = base.preflight(launch)
    preflight.update({"recordType": "SMA_E1_NATIVE_TOOL_V5_CASE18_PREFLIGHT", "recordedAt": recorded, "packageIdentity": package["packageIdentity"]})
    preflight["receiptSha256"] = digest(preflight)
    OUTPUT.mkdir(parents=True, exist_ok=False)
    preflight_path = OUTPUT / "read-only-preflight.json"
    write_exclusive(preflight_path, preflight)
    basis["receiptSha256"] = digest(basis)
    basis_path = OUTPUT / "continuation-basis.json"
    write_exclusive(basis_path, basis)
    identity_subject = {"packageIdentity": package["packageIdentity"], "acceptanceSha256": file_sha256(ACCEPTANCE), "liveAcceptanceSha256": file_sha256(LIVE_ACCEPTANCE), "offlineSha256": file_sha256(OFFLINE), "launchSha256": file_sha256(LAUNCH), "authorizationSha256": file_sha256(authorization_path), "preflightSha256": file_sha256(preflight_path), "basisSha256": file_sha256(basis_path), "driverSha256": file_sha256(Path(__file__).resolve()), "operationKey": KEY.value, "mode": RunMode.ZERO_CREDIT_DRESS.value, "automaticReruns": 0}
    execution_identity = digest(identity_subject)
    identity_path = OUTPUT / "execution-identity.json"
    write_exclusive(identity_path, {"schemaVersion": "1.0.0", "recordType": "SMA_E1_NATIVE_TOOL_V5_EXECUTION_IDENTITY", "status": "SEALED_SINGLE_USE_EXECUTION_ONLY", "packageIdentity": package["packageIdentity"], "identitySubject": identity_subject, "executionIdentity": execution_identity})
    sealed_path = OUTPUT / "single-use-authorization.json"
    write_exclusive(sealed_path, {"schemaVersion": "1.0.0", "recordType": "SMA_E1_NATIVE_TOOL_V5_SEALED_AUTHORIZATION", "status": "SEALED_FROM_PRINCIPAL_GRANT", "packageIdentity": package["packageIdentity"], "executionIdentity": execution_identity, "mode": RunMode.ZERO_CREDIT_DRESS.value, "singleUse": True, "maximumScenarios": 1, "maximumRepetitions": 1, "automaticReruns": 0, "principalGrantSha256": file_sha256(authorization_path), "principalStatement": authorization["principalStatement"]})
    authority = LiveAuthority(package_manifest_path=str(LIVE_ACCEPTANCE), package_manifest_sha256=file_sha256(LIVE_ACCEPTANCE), authorization_path=str(sealed_path), authorization_sha256=file_sha256(sealed_path), execution_identity_path=str(identity_path), execution_identity_sha256=file_sha256(identity_path), mode=RunMode.ZERO_CREDIT_DRESS, consumed_marker_path=str(OUTPUT / "single-use-authority-consumed.json"))
    runner, plan = compose(ROOT, RunMode.ZERO_CREDIT_DRESS, authority, LAUNCH)
    if KEY not in plan:
        raise RuntimeError("v5 case18 missing from sealed plan")
    started = time.monotonic_ns()
    terminal_exception = None
    record = None
    final_cleanup = None
    final_inventory = None
    try:
        result = runner.run_operation(KEY)
        observation = jsonable(asdict(result.observation))
        record = {"vector": verdict_vector(result), "observation": observation, "scientific": [jsonable(asdict(row)) for row in result.scientific], "evidence": [jsonable(asdict(row)) for row in result.evidence], "observationSha256": digest(observation)}
    except BaseException as exc:
        terminal_exception = {"class": type(exc).__name__, "message": str(exc)}
    finally:
        try:
            final_cleanup = action(launch, "cleanup_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {"class": type(exc).__name__, "message": str(exc)}
        try:
            final_inventory = action(launch, "inventory_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {"class": type(exc).__name__, "message": str(exc)}
    walk = OUTPUT / "case18-walk.jsonl"
    with os.fdopen(os.open(walk, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as stream:
        if record is not None:
            stream.write(canonical_bytes(record) + b"\n")
            stream.flush()
            os.fsync(stream.fileno())
    prompted = [] if record is None else [conversation.get("execution_status") for conversation in record["observation"].get("conversations", []) if any(receipt.get("kind") == "prompt_submission" and receipt.get("conversationId") == conversation.get("id") for receipt in record["observation"].get("raw_receipts", []))]
    interaction_match = bool(record and record["vector"]["selectedInteractionAnswers"] == sum(receipt.get("kind") == "prompt_submission" for receipt in record["observation"].get("raw_receipts", [])))
    inventory_clean = final_inventory is not None and base.clean(final_inventory)
    passed = bool(record and record["vector"]["overallRepetitionVerdict"] == "PASS" and set(prompted) == {"finished"} and interaction_match and terminal_exception is None and runner.ledger.verify() and inventory_clean)
    receipt = {"schemaVersion": "1.0.0", "recordType": "SMA_E1_NATIVE_TOOL_V5_ALL_SCENARIO_COMPOSITE_RECEIPT", "status": "PASS_ALL_18_SCENARIO_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION" if passed else "NO_GO_V5_CASE18_STOPPED", "recordedAt": recorded, "durationMilliseconds": (time.monotonic_ns() - started) / 1_000_000, "packageIdentity": package["packageIdentity"], "executionIdentity": execution_identity, "sourceBasis": basis, "case18": {"verdict": None if record is None else record["vector"], "walk": str(walk.relative_to(ROOT)), "walkSha256": file_sha256(walk), "conversationStatuses": prompted, "interactionCountsMatch": interaction_match}, "composite": {"scenarios": 18, "passes": 18 if passed else 17, "automaticReruns": 0}, "authorityConsumedBeforeFirstAction": Path(authority.consumed_marker_path).is_file(), "ledgerValid": runner.ledger.verify(), "terminalException": terminal_exception, "finalCleanup": final_cleanup, "finalInventory": final_inventory, "finalInventoryClean": inventory_clean, "measuredExecution": "NOT_RUN"}
    receipt["receiptSha256"] = digest(receipt)
    receipt_path = OUTPUT / "composite-canary-receipt.json"
    write_exclusive(receipt_path, receipt)
    print(json.dumps({"status": receipt["status"], "compositePasses": receipt["composite"]["passes"], "ledgerValid": receipt["ledgerValid"], "finalInventoryClean": receipt["finalInventoryClean"], "receiptFileSha256": file_sha256(receipt_path), "embeddedReceiptSha256": receipt["receiptSha256"]}, sort_keys=True), flush=True)
    return 0 if passed else 2


if __name__ == "__main__":
    raise SystemExit(main())
