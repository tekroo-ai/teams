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
PREDECESSOR = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v2-package-identity.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair/e1_native/live-launch-definition.json"
OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-offline-qualification/offline-qualification.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v3-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v3-acceptance.json"
LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v3-live-acceptance.json"
DIAGNOSTIC_AUTH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v3-case16-diagnostic-authorization.json"
CANARY_AUTH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v3-all-scenario-canary-authorization.json"
DRIVER = ROOT / "scripts/execute_sma_e1_native_tool_repair_v3.py"
BUILDER = ROOT / "scripts/build_sma_e1_native_tool_repair_v3.py"
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
    sources = [
        {"path": str(DRIVER.relative_to(ROOT)), "sha256": sha(DRIVER)},
        {"path": str(BUILDER.relative_to(ROOT)), "sha256": sha(BUILDER)},
    ]
    subject = {
        "lineage": "SMA-E1-DDALCU-NATIVE-TOOL-REPAIR-V3",
        "predecessorPackageIdentity": predecessor["packageIdentity"],
        "changeScope": "PACKAGE_SOURCE_IDENTITY_BINDING_CORRECTION_ONLY",
        "packageSourceIdentity": offline["packageSourceIdentity"],
        "sources": sources,
        "launchDefinition": {"path": str(LAUNCH.relative_to(ROOT)), "sha256": sha(LAUNCH)},
        "offlineQualification": {"path": str(OFFLINE.relative_to(ROOT)), "sha256": sha(OFFLINE), "receiptSha256": offline["receiptSha256"]},
        "scienceChanged": False,
        "modelProfileChanged": False,
        "measuredExecution": False,
    }
    value = {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V3_PACKAGE_IDENTITY", "status": "PREPARED_NOT_MEASURED", "identitySubject": subject, "packageIdentity": digest(subject)}
    write(IDENTITY, value)
    print(json.dumps({"packageIdentity": value["packageIdentity"], "recordSha256": sha(IDENTITY)}, sort_keys=True))


def acceptances() -> None:
    package = json.loads(IDENTITY.read_text(encoding="utf-8"))
    common = {"schemaVersion": "1.0.0", "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "packageIdentity": package["packageIdentity"], "packageIdentityRecordSha256": sha(IDENTITY), "principalStatement": PRINCIPAL_STATEMENT, "zeroCreditOnly": True, "measuredExecutionAuthorized": False}
    write(ACCEPTANCE, dict(common, recordType="SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V3_ACCEPTANCE", status="ACCEPTED_FROZEN_FOR_ZERO_CREDIT_VALIDATION"))
    write(LIVE_ACCEPTANCE, dict(common, recordType="SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V3_LIVE_ACCEPTANCE", status="ACCEPTED_FROZEN"))
    print(json.dumps({"acceptanceSha256": sha(ACCEPTANCE), "liveAcceptanceSha256": sha(LIVE_ACCEPTANCE)}, sort_keys=True))


def authority(path: Path, scope: str, count: int) -> None:
    package = json.loads(IDENTITY.read_text(encoding="utf-8"))
    value = {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V3_PRINCIPAL_AUTHORIZATION", "status": "AUTHORIZED_BY_PRINCIPAL", "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "packageIdentity": package["packageIdentity"], "scope": scope, "mode": "ZERO_CREDIT_DRESS", "singleUse": True, "maximumScenarios": count, "maximumRepetitions": count, "automaticReruns": 0, "measuredExecution": False, "principalStatement": PRINCIPAL_STATEMENT}
    write(path, value)
    print(json.dumps({"scope": scope, "recordSha256": sha(path)}, sort_keys=True))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("phase", choices=("identity", "acceptances", "diagnostic-authority", "canary-authority"))
    args = parser.parse_args()
    if args.phase == "identity":
        identity()
    elif args.phase == "acceptances":
        acceptances()
    elif args.phase == "diagnostic-authority":
        authority(DIAGNOSTIC_AUTH, "CASE16_NATIVE_TOOL_DIAGNOSTIC", 1)
    else:
        authority(CANARY_AUTH, "ALL_18_SCENARIO_NATIVE_TOOL_CANARY", 18)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
