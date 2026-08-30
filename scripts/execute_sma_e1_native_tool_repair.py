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
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}" for number in (1, 3, 4, 5, 6)]
ADJUDICATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-interaction-adjudication"
NATIVE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair"
for package in (S2_PACKAGE, *PACKAGES, ADJUDICATION, NATIVE):
    sys.path.insert(0, str(package))
sys.path.insert(0, str(ROOT / "scripts"))

import execute_sma_e1_ddalcu_candidate_6 as base  # noqa: E402
from e1.runner import E1_CASES  # noqa: E402
from e1_native.live_entrypoint import compose, load_definition  # noqa: E402
from e1_native.runner import verdict_vector  # noqa: E402
from t1.model import LiveAuthority, OperationKey, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402


PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-acceptance.json"
LAUNCH = NATIVE / "e1_native/live-launch-definition.json"
OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-offline-qualification/offline-qualification.json"
DIAGNOSTIC_OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-case16-diagnostic"
CANARY_OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-all-scenario-canary"
CASE16 = "SMA-S2-016-EMPTY-RESULT"


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
        raise RuntimeError(f"native-tool action {name} failed: " + completed.stderr.decode(errors="replace"))
    return json.loads(completed.stdout)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--mode", choices=("case16-diagnostic", "all-scenario-canary"), required=True)
    parser.add_argument("--authorization", required=True)
    args = parser.parse_args()
    diagnostic = args.mode == "case16-diagnostic"
    output = DIAGNOSTIC_OUTPUT if diagnostic else CANARY_OUTPUT
    if output.exists():
        raise RuntimeError(f"native-tool {args.mode} output already exists")
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    acceptance = json.loads(ACCEPTANCE.read_text(encoding="utf-8"))
    offline = json.loads(OFFLINE.read_text(encoding="utf-8"))
    launch = load_definition(LAUNCH)
    authorization_path = Path(args.authorization).resolve()
    authorization = json.loads(authorization_path.read_text(encoding="utf-8"))
    if (
        digest(package["identitySubject"]) != package.get("packageIdentity")
        or acceptance.get("status") != "ACCEPTED_FROZEN_FOR_ZERO_CREDIT_VALIDATION"
        or acceptance.get("packageIdentity") != package.get("packageIdentity")
        or acceptance.get("packageIdentityRecordSha256") != file_sha256(PACKAGE)
        or offline.get("status") != "PASS_NATIVE_TOOL_OFFLINE_QUALIFICATION"
        or offline.get("packageSourceIdentity") != package["identitySubject"]["packageSourceIdentity"]
    ):
        raise RuntimeError("native-tool frozen package mismatch")
    expected_scope = "CASE16_NATIVE_TOOL_DIAGNOSTIC" if diagnostic else "ALL_18_SCENARIO_NATIVE_TOOL_CANARY"
    expected_count = 1 if diagnostic else 18
    if (
        authorization.get("status") != "AUTHORIZED_BY_PRINCIPAL"
        or authorization.get("packageIdentity") != package["packageIdentity"]
        or authorization.get("scope") != expected_scope
        or authorization.get("mode") != RunMode.ZERO_CREDIT_DRESS.value
        or authorization.get("singleUse") is not True
        or authorization.get("maximumScenarios") != expected_count
        or authorization.get("maximumRepetitions") != expected_count
        or authorization.get("automaticReruns") != 0
        or authorization.get("measuredExecution") is not False
        or not str(authorization.get("principalStatement") or "").strip()
    ):
        raise RuntimeError("native-tool principal authorization mismatch")
    if not diagnostic:
        prior = json.loads((DIAGNOSTIC_OUTPUT / "execution-receipt.json").read_text(encoding="utf-8"))
        if (
            prior.get("status") != "PASS_EXACT_CASE16_NATIVE_TOOL_DIAGNOSTIC"
            or prior.get("packageIdentity") != package["packageIdentity"]
            or prior.get("overallVerdicts") != {"PASS": 1}
            or prior.get("finalInventoryClean") is not True
        ):
            raise RuntimeError("exact case16 diagnostic prerequisite is not green")
    plan = (
        (OperationKey(CASE16, 1),)
        if diagnostic
        else tuple(OperationKey(str(row["s2Id"]), 1) for row in E1_CASES)
    )
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    preflight = base.preflight(launch)
    preflight.update({
        "recordType": f"SMA_E1_NATIVE_TOOL_{args.mode.upper().replace('-', '_')}_PREFLIGHT",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "nativeToolCalling": True,
    })
    preflight["receiptSha256"] = digest(preflight)
    output.mkdir(parents=True, exist_ok=False)
    preflight_path = output / "read-only-preflight.json"
    write_exclusive(preflight_path, preflight)
    identity_subject = {
        "packageIdentity": package["packageIdentity"],
        "packageAcceptanceSha256": file_sha256(ACCEPTANCE),
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "offlineQualificationSha256": file_sha256(OFFLINE),
        "principalAuthorizationSha256": file_sha256(authorization_path),
        "preflightSha256": file_sha256(preflight_path),
        "driverSha256": file_sha256(Path(__file__).resolve()),
        "mode": RunMode.ZERO_CREDIT_DRESS.value,
        "scope": expected_scope,
        "planSha256": digest([key.value for key in plan]),
        "scenarios": expected_count,
        "repetitions": expected_count,
        "automaticReruns": 0,
    }
    execution_identity = digest(identity_subject)
    identity_path = output / "execution-identity.json"
    identity = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_NATIVE_TOOL_EXECUTION_IDENTITY",
        "status": "SEALED_SINGLE_USE_EXECUTION_ONLY",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "identitySubject": identity_subject,
        "executionIdentity": execution_identity,
    }
    write_exclusive(identity_path, identity)
    sealed_path = output / "single-use-authorization.json"
    sealed = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_NATIVE_TOOL_SEALED_AUTHORIZATION",
        "status": "SEALED_FROM_PRINCIPAL_GRANT",
        "packageIdentity": package["packageIdentity"],
        "executionIdentity": execution_identity,
        "mode": RunMode.ZERO_CREDIT_DRESS.value,
        "singleUse": True,
        "maximumScenarios": expected_count,
        "maximumRepetitions": expected_count,
        "automaticReruns": 0,
        "principalGrantSha256": file_sha256(authorization_path),
        "principalStatement": authorization["principalStatement"],
    }
    write_exclusive(sealed_path, sealed)
    authority = LiveAuthority(
        package_manifest_path=str(ACCEPTANCE),
        package_manifest_sha256=file_sha256(ACCEPTANCE),
        authorization_path=str(sealed_path),
        authorization_sha256=file_sha256(sealed_path),
        execution_identity_path=str(identity_path),
        execution_identity_sha256=file_sha256(identity_path),
        mode=RunMode.ZERO_CREDIT_DRESS,
        consumed_marker_path=str(output / "single-use-authority-consumed.json"),
    )
    runner, full_plan = compose(ROOT, RunMode.ZERO_CREDIT_DRESS, authority, LAUNCH)
    if (diagnostic and plan[0] not in full_plan) or (not diagnostic and tuple(full_plan) != plan):
        raise RuntimeError("native-tool plan changed after sealing")
    walk = output / "execution-walk.jsonl"
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
                "planned": expected_count,
                "operationKey": key.value,
                "overall": record["vector"]["overallRepetitionVerdict"],
                "modelRequests": len(observation.get("model_requests", [])),
                "selectedAnswers": record["vector"]["selectedInteractionAnswers"],
                "nativeTools": [row.get("nativeToolName") for row in observation.get("model_terminals", [])],
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
            terminal_exception = terminal_exception or {"class": type(exc).__name__, "message": str(exc)}
        try:
            final_inventory = action(launch, "inventory_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {"class": type(exc).__name__, "message": str(exc)}
    vectors = [record["vector"] for record in records]
    prompted_statuses = [
        conversation.get("execution_status")
        for record in records
        for conversation in record["observation"].get("conversations", [])
        if any(receipt.get("kind") == "prompt_submission" and receipt.get("conversationId") == conversation.get("id") for receipt in record["observation"].get("raw_receipts", []))
    ]
    interaction_counts_match = all(
        record["vector"]["selectedInteractionAnswers"]
        == sum(receipt.get("kind") == "prompt_submission" for receipt in record["observation"].get("raw_receipts", []))
        for record in records
    )
    inventory_clean = final_inventory is not None and base.clean(final_inventory)
    passed = bool(
        len(records) == expected_count
        and all(vector["overallRepetitionVerdict"] == "PASS" for vector in vectors)
        and set(prompted_statuses) == {"finished"}
        and interaction_counts_match
        and terminal_exception is None
        and runner.ledger.verify()
        and inventory_clean
    )
    success_status = "PASS_EXACT_CASE16_NATIVE_TOOL_DIAGNOSTIC" if diagnostic else "PASS_ALL_18_SCENARIO_NATIVE_TOOL_CANARY_NOT_MEASURED"
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_NATIVE_TOOL_EXECUTION_RECEIPT",
        "status": success_status if passed else "NO_GO_NATIVE_TOOL_EXECUTION_STOPPED",
        "recordedAt": recorded,
        "durationMilliseconds": (time.monotonic_ns() - started_ns) / 1_000_000,
        "packageIdentity": package["packageIdentity"],
        "executionIdentity": execution_identity,
        "scope": expected_scope,
        "workload": {"planned": expected_count, "executed": len(records), "automaticReruns": 0},
        "walk": {"path": str(walk.relative_to(ROOT)), "sha256": file_sha256(walk), "records": len(records)},
        "overallVerdicts": dict(Counter(vector["overallRepetitionVerdict"] for vector in vectors)),
        "conversationStatuses": dict(Counter(prompted_statuses)),
        "interactionCountsMatch": interaction_counts_match,
        "nativeToolCalls": sum(bool(row.get("nativeToolName")) for record in records for row in record["observation"].get("model_terminals", [])),
        "authorityConsumedBeforeFirstAction": Path(authority.consumed_marker_path).is_file(),
        "ledgerValid": runner.ledger.verify(),
        "terminalException": terminal_exception,
        "finalCleanup": final_cleanup,
        "finalInventory": final_inventory,
        "finalInventoryClean": inventory_clean,
        "measuredExecution": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    receipt_path = output / "execution-receipt.json"
    write_exclusive(receipt_path, receipt)
    print(json.dumps({
        "status": receipt["status"],
        "executed": len(records),
        "planned": expected_count,
        "overallVerdicts": receipt["overallVerdicts"],
        "nativeToolCalls": receipt["nativeToolCalls"],
        "ledgerValid": receipt["ledgerValid"],
        "finalInventoryClean": inventory_clean,
        "receiptFileSha256": file_sha256(receipt_path),
        "embeddedReceiptSha256": receipt["receiptSha256"],
    }, sort_keys=True), flush=True)
    return 0 if passed else 2


if __name__ == "__main__":
    raise SystemExit(main())
