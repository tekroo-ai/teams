#!/usr/bin/env python3
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
BASE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6/e1_candidate6/live-launch-definition.json"
NATIVE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair"
LAUNCH = NATIVE / "e1_native/live-launch-definition.json"
OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-offline-qualification/offline-qualification.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-acceptance.json"
DIAGNOSTIC_AUTH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-case16-diagnostic-authorization.json"
CANARY_AUTH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-all-scenario-canary-authorization.json"
PRINCIPAL_STATEMENT = "No, I should not. So prove to me that you can do this work. I don't think it is particularly hard work, so let's get it done."


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(value: Any) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_exclusive(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def additions() -> tuple[Path, ...]:
    return (
        NATIVE / "e1_native/__init__.py",
        NATIVE / "e1_native/runner.py",
        NATIVE / "e1_native/live_entrypoint.py",
        ROOT / "scripts/qualify_sma_e1_native_tool_repair.py",
        ROOT / "scripts/build_sma_e1_native_tool_repair.py",
        ROOT / "scripts/execute_sma_e1_native_tool_repair.py",
        ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-interaction-adjudication/e1_candidate6_adjudication/verdict.py",
    )


def build_launch() -> None:
    if LAUNCH.exists():
        raise RuntimeError("native-tool launch definition already exists")
    value = json.loads(BASE.read_text(encoding="utf-8"))
    target = "/Users/paul/work/tekroo-ai/sma-step15-s1-p2final/target/sma-e1-ddalcu-native-tool-repair"
    value["disposable"].update({
        "mongoDatabase": "sma_e1_ddalcu_native_tool_repair",
        "semanticCollection": "sma_e1_ddalcu_native_tool_repair_semantic",
        "episodicCollection": "sma_e1_ddalcu_native_tool_repair_episodic",
        "workspaceRoot": target,
        "launchLabel": "com.tekroo.sma-service-e1-ddalcu-native-tool-repair",
        "stubLaunchLabel": "com.tekroo.sma-e1-model-audit-native-tool-repair",
    })
    runtime = target + "/runtime"
    value["evidenceFiles"] = {
        "captureAudit": runtime + "/capture-audit.jsonl",
        "operationalLog": runtime + "/operational-log.jsonl",
        "stubRaw": runtime + "/stub-raw.jsonl",
        "stubTerminal": runtime + "/stub-terminal.jsonl",
    }
    value["nativeToolRepair"] = {
        "enabled": True,
        "protocol": "OPENAI_NATIVE_TOOLS",
        "actionScript": str(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6/e1_candidate6/live_actions.py"),
    }
    value["agentLoopAudit"].update({
        "acceptedModelFinishReasons": ["stop", "tool_calls"],
        "nativeToolCallingRequired": True,
        "nativeToolSchemaExact": True,
        "finalFinishToolMessageRequiredPerUserPrompt": True,
        "plainFinalResponsePermittedByAcceptedScience": False,
    })
    known = {row["path"] for row in value["boundFiles"]}
    for path in additions():
        if not path.is_file():
            raise RuntimeError(f"native-tool source is missing: {path}")
        if str(path) not in known:
            value["boundFiles"].append({"path": str(path), "sha256": sha(path)})
    write_exclusive(LAUNCH, value)
    print(json.dumps({"launchDefinition": str(LAUNCH), "sha256": sha(LAUNCH), "boundFiles": len(value["boundFiles"])}, sort_keys=True))


def verify_launch(value: dict[str, Any]) -> None:
    for row in value["boundFiles"]:
        path = Path(row["path"])
        if not path.is_file() or sha(path) != row["sha256"]:
            raise RuntimeError(f"native-tool bound file drift: {path}")


def build_identity() -> None:
    if IDENTITY.exists():
        raise RuntimeError("native-tool package identity already exists")
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    offline = json.loads(OFFLINE.read_text(encoding="utf-8"))
    verify_launch(launch)
    if offline.get("status") != "PASS_NATIVE_TOOL_OFFLINE_QUALIFICATION":
        raise RuntimeError("native-tool offline qualification is not green")
    subject = {
        "lineage": "SMA-E1-DDALCU-NATIVE-TOOL-REPAIR",
        "predecessorPackageIdentity": "1717983362120d429412a5f63c9e89174b5b31220a9753b5f34df04e4e4fa4e5",
        "changeScope": "OPENHANDS_NATIVE_TOOL_PROTOCOL_AND_INTERACTION_LEVEL_ADJUDICATION_ONLY",
        "packageSourceIdentity": offline["packageSourceIdentity"],
        "launchDefinition": {"path": str(LAUNCH.relative_to(ROOT)), "sha256": sha(LAUNCH)},
        "offlineQualification": {"path": str(OFFLINE.relative_to(ROOT)), "sha256": sha(OFFLINE), "receiptSha256": offline["receiptSha256"]},
        "science": {
            "corpusSha256": "69ad47c6b73f3d3d5b326e3c1817f22f1d5aa6e6ebe91970a448ec21dd6778c2",
            "preregistrationSha256": "de9f90bdbbf5a422c2d77dc1e958ac27a09c6a6e2977a198d2cc6ca07c5fd045",
            "scenarios": 18,
            "repetitions": 96,
            "changed": False,
        },
        "model": launch["modelProfile"],
        "nativeToolRepair": launch["nativeToolRepair"],
    }
    value = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_PACKAGE_IDENTITY",
        "status": "PREPARED_NOT_MEASURED",
        "identityAlgorithm": "SHA256_CANONICAL_JSON_OF_IDENTITY_SUBJECT",
        "identitySubject": subject,
        "packageIdentity": digest(subject),
        "authority": {"zeroCreditValidation": False, "measuredExecution": False},
    }
    write_exclusive(IDENTITY, value)
    print(json.dumps({"packageIdentity": value["packageIdentity"], "recordSha256": sha(IDENTITY)}, sort_keys=True))


def build_acceptance() -> None:
    if ACCEPTANCE.exists():
        raise RuntimeError("native-tool package acceptance already exists")
    identity = json.loads(IDENTITY.read_text(encoding="utf-8"))
    offline = json.loads(OFFLINE.read_text(encoding="utf-8"))
    value = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_PACKAGE_ACCEPTANCE",
        "status": "ACCEPTED_FROZEN_FOR_ZERO_CREDIT_VALIDATION",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "packageIdentity": identity["packageIdentity"],
        "packageIdentityRecordSha256": sha(IDENTITY),
        "offlineQualification": {"path": str(OFFLINE.relative_to(ROOT)), "sha256": sha(OFFLINE), "receiptSha256": offline["receiptSha256"]},
        "principalStatement": PRINCIPAL_STATEMENT,
        "scope": "NATIVE_TOOL_SUCCESSOR_PACKAGE_AND_ZERO_CREDIT_VALIDATION_ONLY",
        "measuredExecutionAuthorized": False,
    }
    write_exclusive(ACCEPTANCE, value)
    print(json.dumps({"status": value["status"], "recordSha256": sha(ACCEPTANCE)}, sort_keys=True))


def build_authority(path: Path, scope: str, count: int) -> None:
    if path.exists():
        raise RuntimeError(f"native-tool authorization already exists: {path}")
    identity = json.loads(IDENTITY.read_text(encoding="utf-8"))
    value = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_PRINCIPAL_AUTHORIZATION",
        "status": "AUTHORIZED_BY_PRINCIPAL",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "packageIdentity": identity["packageIdentity"],
        "scope": scope,
        "mode": "ZERO_CREDIT_DRESS",
        "singleUse": True,
        "maximumScenarios": count,
        "maximumRepetitions": count,
        "automaticReruns": 0,
        "measuredExecution": False,
        "principalStatement": PRINCIPAL_STATEMENT,
    }
    write_exclusive(path, value)
    print(json.dumps({"scope": scope, "recordSha256": sha(path)}, sort_keys=True))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("phase", choices=("launch", "identity", "acceptance", "diagnostic-authority", "canary-authority"))
    args = parser.parse_args()
    if args.phase == "launch":
        build_launch()
    elif args.phase == "identity":
        build_identity()
    elif args.phase == "acceptance":
        build_acceptance()
    elif args.phase == "diagnostic-authority":
        build_authority(DIAGNOSTIC_AUTH, "CASE16_NATIVE_TOOL_DIAGNOSTIC", 1)
    else:
        build_authority(CANARY_AUTH, "ALL_18_SCENARIO_NATIVE_TOOL_CANARY", 18)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
