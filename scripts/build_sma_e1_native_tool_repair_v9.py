#!/usr/bin/env python3
from __future__ import annotations

from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v9-offline-qualification/offline-qualification.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair/e1_native/live-launch-definition.json"
V4_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-continuation-13-18/composite-canary-receipt.json"
V8_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v8-case18/composite-canary-receipt.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9-acceptance.json"
LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9-live-acceptance.json"
AUTHORITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9-case18-authorization.json"
PRINCIPAL_STATEMENT = "So work only on 18 and do not repeat 1-17."


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


def main() -> int:
    offline = json.loads(OFFLINE.read_text(encoding="utf-8"))
    if offline.get("nativeStatus") != "PASS_NATIVE_TOOL_V9_OFFLINE_QUALIFICATION" or offline.get("summary") != {"passed": 10, "total": 10}:
        raise RuntimeError("v9 offline qualification mismatch")
    subject = {
        "lineage": "SMA-E1-DDALCU-NATIVE-TOOL-REPAIR-V9",
        "changeScope": "RETAIN_SECOND_TURN_RAW_COMPLETION_TIMESTAMP",
        "packageSourceIdentity": offline["packageSourceIdentity"],
        "sources": offline["sources"],
        "launchDefinitionSha256": sha(LAUNCH),
        "offlineQualification": {
            "path": str(OFFLINE.relative_to(ROOT)),
            "sha256": sha(OFFLINE),
            "receiptSha256": offline["receiptSha256"],
        },
        "preservedPasses": 17,
        "continuationScenarios": 1,
        "scienceChanged": False,
        "modelProfileChanged": False,
        "measuredExecution": False,
    }
    package_identity = digest(subject)
    write(IDENTITY, {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V9_PACKAGE_IDENTITY", "status": "PREPARED_NOT_MEASURED", "identitySubject": subject, "packageIdentity": package_identity})
    common = {
        "schemaVersion": "1.0.0",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "packageIdentity": package_identity,
        "packageIdentityRecordSha256": sha(IDENTITY),
        "principalStatement": PRINCIPAL_STATEMENT,
        "zeroCreditOnly": True,
        "measuredExecutionAuthorized": False,
    }
    write(ACCEPTANCE, dict(common, recordType="SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V9_ACCEPTANCE", status="ACCEPTED_FROZEN_FOR_ZERO_CREDIT_VALIDATION"))
    write(LIVE_ACCEPTANCE, dict(common, recordType="SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V9_LIVE_ACCEPTANCE", status="ACCEPTED_FROZEN"))
    write(
        AUTHORITY,
        {
            "schemaVersion": "1.0.0",
            "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V9_PRINCIPAL_AUTHORIZATION",
            "status": "AUTHORIZED_BY_PRINCIPAL",
            "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "packageIdentity": package_identity,
            "sourceV4ReceiptSha256": sha(V4_RECEIPT),
            "sourceV8ReceiptSha256": sha(V8_RECEIPT),
            "scope": "SCENARIO_18_ZERO_CREDIT_CONTINUATION",
            "mode": "ZERO_CREDIT_DRESS",
            "singleUse": True,
            "maximumScenarios": 1,
            "maximumRepetitions": 1,
            "automaticReruns": 0,
            "measuredExecution": False,
            "principalStatement": PRINCIPAL_STATEMENT,
        },
    )
    print(json.dumps({"packageIdentity": package_identity, "authorizationSha256": sha(AUTHORITY)}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
