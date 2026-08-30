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
PREDECESSOR = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v3-package-identity.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair/e1_native/live-launch-definition.json"
OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-offline-qualification/offline-qualification.json"
SOURCE_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v3-all-scenario-canary/execution-receipt.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4-acceptance.json"
LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4-live-acceptance.json"
AUTHORITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4-continuation-authorization.json"
V4 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4"
SOURCES = (
    V4 / "e1_native_v4/__init__.py", V4 / "e1_native_v4/runner.py", V4 / "e1_native_v4/live_entrypoint.py",
    ROOT / "scripts/qualify_sma_e1_native_tool_repair_v4.py", ROOT / "scripts/execute_sma_e1_native_tool_repair_v4_continuation.py", ROOT / "scripts/build_sma_e1_native_tool_repair_v4.py",
)
PRINCIPAL_STATEMENT = "No, I should not. So prove to me that you can do this work. I don't think it is particularly hard work, so let's get it done."


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(value: Any) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def identity() -> None:
    predecessor = json.loads(PREDECESSOR.read_text(encoding="utf-8"))
    offline = json.loads(OFFLINE.read_text(encoding="utf-8"))
    source_rows = [{"path": str(path.relative_to(ROOT)), "sha256": sha(path)} for path in SOURCES]
    if digest(source_rows) != offline["packageSourceIdentity"]:
        raise RuntimeError("v4 source identity does not match offline qualification")
    subject = {
        "lineage": "SMA-E1-DDALCU-NATIVE-TOOL-REPAIR-V4", "predecessorPackageIdentity": predecessor["packageIdentity"],
        "changeScope": "RETAIN_ACTUAL_WORKSPACE_ROLE_AND_PROVIDER_REASONING_EVIDENCE",
        "packageSourceIdentity": offline["packageSourceIdentity"], "sources": source_rows,
        "launchDefinition": {"path": str(LAUNCH.relative_to(ROOT)), "sha256": sha(LAUNCH)},
        "offlineQualification": {"path": str(OFFLINE.relative_to(ROOT)), "sha256": sha(OFFLINE), "receiptSha256": offline["receiptSha256"]},
        "sourceCheckpoint": {"path": str(SOURCE_RECEIPT.relative_to(ROOT)), "sha256": sha(SOURCE_RECEIPT)},
        "preservedScenarios": 12, "continuationScenarios": 6, "scienceChanged": False, "modelProfileChanged": False, "measuredExecution": False,
    }
    value = {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V4_PACKAGE_IDENTITY", "status": "PREPARED_NOT_MEASURED", "identitySubject": subject, "packageIdentity": digest(subject)}
    write(IDENTITY, value)
    print(json.dumps({"packageIdentity": value["packageIdentity"], "recordSha256": sha(IDENTITY)}, sort_keys=True))


def acceptances() -> None:
    package = json.loads(IDENTITY.read_text(encoding="utf-8"))
    common = {"schemaVersion": "1.0.0", "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "packageIdentity": package["packageIdentity"], "packageIdentityRecordSha256": sha(IDENTITY), "principalStatement": PRINCIPAL_STATEMENT, "zeroCreditOnly": True, "measuredExecutionAuthorized": False}
    write(ACCEPTANCE, dict(common, recordType="SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V4_ACCEPTANCE", status="ACCEPTED_FROZEN_FOR_ZERO_CREDIT_VALIDATION"))
    write(LIVE_ACCEPTANCE, dict(common, recordType="SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V4_LIVE_ACCEPTANCE", status="ACCEPTED_FROZEN"))
    print(json.dumps({"acceptanceSha256": sha(ACCEPTANCE), "liveAcceptanceSha256": sha(LIVE_ACCEPTANCE)}, sort_keys=True))


def authority() -> None:
    package = json.loads(IDENTITY.read_text(encoding="utf-8"))
    value = {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V4_PRINCIPAL_AUTHORIZATION", "status": "AUTHORIZED_BY_PRINCIPAL", "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "packageIdentity": package["packageIdentity"], "sourceCheckpointSha256": sha(SOURCE_RECEIPT), "scope": "SCENARIOS_13_THROUGH_18_ZERO_CREDIT_CONTINUATION", "mode": "ZERO_CREDIT_DRESS", "singleUse": True, "maximumScenarios": 6, "maximumRepetitions": 6, "automaticReruns": 0, "measuredExecution": False, "principalStatement": PRINCIPAL_STATEMENT}
    write(AUTHORITY, value)
    print(json.dumps({"scope": value["scope"], "recordSha256": sha(AUTHORITY)}, sort_keys=True))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("phase", choices=("identity", "acceptances", "authority"))
    args = parser.parse_args()
    if args.phase == "identity":
        identity()
    elif args.phase == "acceptances":
        acceptances()
    else:
        authority()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
