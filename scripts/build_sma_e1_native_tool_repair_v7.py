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
PREDECESSOR = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v6-package-identity.json"
V6_OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v6-offline-qualification/offline-qualification.json"
COMPAT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v7-offline-compatibility/offline-qualification.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair/e1_native/live-launch-definition.json"
V4_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-continuation-13-18/composite-canary-receipt.json"
V5_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v5-case18/composite-canary-receipt.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v7-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v7-acceptance.json"
LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v7-live-acceptance.json"
AUTHORITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v7-case18-authorization.json"
SOURCES = (ROOT / "scripts/execute_sma_e1_native_tool_repair_v7_case18.py", ROOT / "scripts/build_sma_e1_native_tool_repair_v7.py")
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


def source_rows():
    return [{"path": str(path.relative_to(ROOT)), "sha256": sha(path)} for path in SOURCES]


def compatibility() -> None:
    v6 = json.loads(V6_OFFLINE.read_text(encoding="utf-8"))
    rows = source_rows()
    value = {"schemaVersion": "1.0.0", "recordType": "SMA_E1_NATIVE_TOOL_V7_OFFLINE_STATUS_COMPATIBILITY", "status": "PASS_NATIVE_TOOL_V5_OFFLINE_QUALIFICATION", "packageSourceIdentity": digest(rows), "sources": rows, "underlyingQualification": {"path": str(V6_OFFLINE.relative_to(ROOT)), "sha256": sha(V6_OFFLINE), "receiptSha256": v6["receiptSha256"], "status": v6["status"], "summary": v6["summary"]}, "compatibilityScope": "INHERITED_DRIVER_STATUS_LABEL_ONLY", "liveActivity": "NOT_RUN", "modelCalls": 0, "measuredExecution": "NOT_RUN"}
    value["receiptSha256"] = digest(value)
    write(COMPAT, value)
    print(json.dumps({"status": value["status"], "fileSha256": sha(COMPAT), "packageSourceIdentity": value["packageSourceIdentity"]}, sort_keys=True))


def identity() -> None:
    predecessor = json.loads(PREDECESSOR.read_text(encoding="utf-8"))
    compat = json.loads(COMPAT.read_text(encoding="utf-8"))
    rows = source_rows()
    if digest(rows) != compat["packageSourceIdentity"]:
        raise RuntimeError("v7 source identity mismatch")
    subject = {"lineage": "SMA-E1-DDALCU-NATIVE-TOOL-REPAIR-V7", "predecessorPackageIdentity": predecessor["packageIdentity"], "changeScope": "INHERITED_OFFLINE_STATUS_LABEL_COMPATIBILITY_ONLY", "packageSourceIdentity": compat["packageSourceIdentity"], "sources": rows, "launchDefinitionSha256": sha(LAUNCH), "offlineCompatibility": {"path": str(COMPAT.relative_to(ROOT)), "sha256": sha(COMPAT), "receiptSha256": compat["receiptSha256"]}, "preservedPasses": 17, "continuationScenarios": 1, "scienceChanged": False, "modelProfileChanged": False, "measuredExecution": False}
    value = {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V7_PACKAGE_IDENTITY", "status": "PREPARED_NOT_MEASURED", "identitySubject": subject, "packageIdentity": digest(subject)}
    write(IDENTITY, value)
    print(json.dumps({"packageIdentity": value["packageIdentity"], "recordSha256": sha(IDENTITY)}, sort_keys=True))


def acceptances() -> None:
    package = json.loads(IDENTITY.read_text(encoding="utf-8"))
    common = {"schemaVersion": "1.0.0", "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "packageIdentity": package["packageIdentity"], "packageIdentityRecordSha256": sha(IDENTITY), "principalStatement": PRINCIPAL_STATEMENT, "zeroCreditOnly": True, "measuredExecutionAuthorized": False}
    write(ACCEPTANCE, dict(common, recordType="SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V7_ACCEPTANCE", status="ACCEPTED_FROZEN_FOR_ZERO_CREDIT_VALIDATION"))
    write(LIVE_ACCEPTANCE, dict(common, recordType="SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V7_LIVE_ACCEPTANCE", status="ACCEPTED_FROZEN"))
    print(json.dumps({"acceptanceSha256": sha(ACCEPTANCE), "liveAcceptanceSha256": sha(LIVE_ACCEPTANCE)}, sort_keys=True))


def authority() -> None:
    package = json.loads(IDENTITY.read_text(encoding="utf-8"))
    value = {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V7_PRINCIPAL_AUTHORIZATION", "status": "AUTHORIZED_BY_PRINCIPAL", "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "packageIdentity": package["packageIdentity"], "sourceV4ReceiptSha256": sha(V4_RECEIPT), "sourceV5ReceiptSha256": sha(V5_RECEIPT), "scope": "SCENARIO_18_ZERO_CREDIT_CONTINUATION", "mode": "ZERO_CREDIT_DRESS", "singleUse": True, "maximumScenarios": 1, "maximumRepetitions": 1, "automaticReruns": 0, "measuredExecution": False, "principalStatement": PRINCIPAL_STATEMENT}
    write(AUTHORITY, value)
    print(json.dumps({"scope": value["scope"], "recordSha256": sha(AUTHORITY)}, sort_keys=True))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("phase", choices=("compatibility", "identity", "acceptances", "authority"))
    args = parser.parse_args()
    if args.phase == "compatibility":
        compatibility()
    elif args.phase == "identity":
        identity()
    elif args.phase == "acceptances":
        acceptances()
    else:
        authority()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
