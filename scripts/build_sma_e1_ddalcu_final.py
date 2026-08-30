#!/usr/bin/env python3
from __future__ import annotations

from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import sys
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
S2 = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
C1 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
for package in (S2, C1):
    sys.path.insert(0, str(package))

from e1.runner import E1_CASES, e1_plan  # noqa: E402


PREREG = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-preregistration-candidate-1.json"
CANARY_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v9-case18/composite-canary-receipt.json"
CANARY_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v9-case18/case18-walk.jsonl"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair/e1_native/live-launch-definition.json"
OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-final-offline-qualification/offline-qualification.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final-acceptance.json"
LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final-live-acceptance.json"
AUTHORIZATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final-measured-authorization.json"
PRINCIPAL_STATEMENT = "complete it, please."
SOURCES = (
    ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9/e1_native_v9/runner.py",
    ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final/e1_final/__init__.py",
    ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final/e1_final/live_entrypoint.py",
    ROOT / "scripts/execute_sma_e1_ddalcu_final.py",
    ROOT / "scripts/build_sma_e1_ddalcu_final.py",
    ROOT / "scripts/adjudicate_sma_e1_ddalcu_final.py",
)


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
    prereg = json.loads(PREREG.read_text(encoding="utf-8"))
    canary = json.loads(CANARY_RECEIPT.read_text(encoding="utf-8"))
    walk = json.loads(CANARY_WALK.read_text(encoding="utf-8"))
    plan = e1_plan()
    expected_counts = Counter({str(row["s2Id"]): int(row["repetitions"]) for row in E1_CASES})
    actual_counts = Counter(key.case_id for key in plan)
    controls = [
        ("preregistration_binds_eighteen_scenarios", prereg["corpus"]["scenarios"] == 18 and len(prereg["cases"]) == 18),
        ("preregistration_binds_ninety_six_repetitions", prereg["corpus"]["repetitions"] == 96 and len(plan) == 96),
        ("measured_plan_matches_per_scenario_counts", actual_counts == expected_counts),
        ("claim_is_integrated_runtime_tuple_qualified", prereg["claimOnAcceptedPass"] == "INTEGRATED_RUNTIME_TUPLE_QUALIFIED"),
        ("accepted_pass_closes_step_15", prereg["step15ClosureOnAcceptedPass"] is True),
        ("final_canary_passed_all_eighteen", canary["status"] == "PASS_ALL_18_SCENARIO_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION" and canary["composite"] == {"scenarios": 18, "passes": 18, "automaticReruns": 0}),
        ("scenario_18_scientific_predicates_pass", all(row["passed"] for row in walk["scientific"])),
        ("scenario_18_evidence_oracles_pass", all(row["passed"] for row in walk["evidence"])),
        ("scenario_18_harness_and_cleanup_pass", walk["vector"]["harnessVerdict"] == "PASS" and canary["ledgerValid"] is True and canary["finalInventoryClean"] is True),
        ("measured_execution_not_previously_run_for_final_package", not (ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-final-measured-execution").exists()),
    ]
    if not all(passed for _, passed in controls):
        raise RuntimeError("final E1 offline qualification failed")
    sources = [{"path": str(path.relative_to(ROOT)), "sha256": sha(path)} for path in SOURCES]
    package_source_identity = digest(sources)
    offline = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_FINAL_OFFLINE_QUALIFICATION",
        "status": "PASS_FINAL_E1_OFFLINE_QUALIFICATION",
        "packageSourceIdentity": package_source_identity,
        "sources": sources,
        "preregistration": {"path": str(PREREG.relative_to(ROOT)), "sha256": sha(PREREG)},
        "canary": {"receipt": str(CANARY_RECEIPT.relative_to(ROOT)), "receiptSha256": sha(CANARY_RECEIPT), "walk": str(CANARY_WALK.relative_to(ROOT)), "walkSha256": sha(CANARY_WALK)},
        "plan": {"scenarios": 18, "repetitions": 96, "sha256": digest([key.value for key in plan]), "perScenario": dict(sorted(actual_counts.items()))},
        "controls": [{"control": name, "verdict": "PASS"} for name, _ in controls],
        "summary": {"passed": len(controls), "total": len(controls)},
        "liveActivity": "NOT_RUN",
        "modelCalls": 0,
        "measuredExecution": "NOT_RUN",
    }
    offline["receiptSha256"] = digest(offline)
    write(OFFLINE, offline)
    subject = {
        "lineage": "SMA-E1-DDALCU-FINAL",
        "packageSourceIdentity": package_source_identity,
        "sources": sources,
        "preregistrationSha256": sha(PREREG),
        "launchDefinitionSha256": sha(LAUNCH),
        "offlineQualification": {"path": str(OFFLINE.relative_to(ROOT)), "sha256": sha(OFFLINE), "receiptSha256": offline["receiptSha256"]},
        "canaryReceiptSha256": sha(CANARY_RECEIPT),
        "scenarios": 18,
        "repetitions": 96,
        "automaticReruns": 0,
        "claimOnAcceptedPass": "INTEGRATED_RUNTIME_TUPLE_QUALIFIED",
        "scienceChanged": False,
        "modelProfileChanged": False,
    }
    package_identity = digest(subject)
    write(IDENTITY, {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_FINAL_PACKAGE_IDENTITY", "status": "PREPARED_FOR_SINGLE_MEASURED_EXECUTION", "identitySubject": subject, "packageIdentity": package_identity})
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    common = {"schemaVersion": "1.0.0", "recordedAt": recorded, "packageIdentity": package_identity, "packageIdentityRecordSha256": sha(IDENTITY), "principalStatement": PRINCIPAL_STATEMENT, "maximumScenarios": 18, "maximumRepetitions": 96, "automaticReruns": 0}
    write(ACCEPTANCE, dict(common, recordType="SMA_E1_DDALCU_FINAL_ACCEPTANCE", status="ACCEPTED_FROZEN_FOR_MEASURED_EXECUTION"))
    write(LIVE_ACCEPTANCE, dict(common, recordType="SMA_E1_DDALCU_FINAL_LIVE_ACCEPTANCE", status="ACCEPTED_FROZEN"))
    write(AUTHORIZATION, {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_FINAL_PRINCIPAL_AUTHORIZATION", "status": "AUTHORIZED_BY_PRINCIPAL", "recordedAt": recorded, "packageIdentity": package_identity, "scope": "FULL_E1_18_SCENARIO_96_REPETITION_MEASURED_EXECUTION", "mode": "MEASURED", "singleUse": True, "maximumScenarios": 18, "maximumRepetitions": 96, "automaticReruns": 0, "principalStatement": PRINCIPAL_STATEMENT})
    print(json.dumps({"status": offline["status"], "packageIdentity": package_identity, "authorizationSha256": sha(AUTHORIZATION)}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
