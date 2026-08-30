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

from e1.runner import e1_plan  # noqa: E402


PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final-package-identity.json"
PREREG = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-preregistration-candidate-1.json"
EXECUTION = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-final-measured-execution/execution-receipt.json"
WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-final-measured-execution/execution-walk.jsonl"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-final-scientific-adjudication/scientific-adjudication.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-final-scientific-adjudication-acceptance.json"
ARCHITECTURE = ROOT / "docs/architecture/106-step-15-final-e1-acceptance.md"


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(value: Any) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def write_text(path: Path, text: str) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        stream.write(text)
        stream.flush()
        os.fsync(stream.fileno())


def main() -> int:
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    prereg = json.loads(PREREG.read_text(encoding="utf-8"))
    execution = json.loads(EXECUTION.read_text(encoding="utf-8"))
    records = [json.loads(line) for line in WALK.read_text(encoding="utf-8").splitlines() if line]
    plan = e1_plan()
    operation_keys = [record["vector"]["operationKey"] for record in records]
    expected_keys = [key.value for key in plan]
    component_fields = ("smaSemanticsVerdict", "openHandsBoundaryVerdict", "modelBehaviorVerdict", "environmentVerdict", "harnessVerdict", "safetyVerdict", "cleanupVerdict", "executionVerdict")
    all_components_pass = all(all(record["vector"].get(field) == "PASS" for field in component_fields) for record in records)
    all_oracles_pass = all(all(row["passed"] for row in record["scientific"]) and all(row["passed"] for row in record["evidence"]) for record in records)
    per_scenario = Counter(key.split("#", 1)[0] for key in operation_keys)
    checks = {
        "executionReceiptPass": execution["status"] == "PASS_MEASURED_PENDING_SCIENTIFIC_ADJUDICATION",
        "executionReceiptHashMatches": execution["walk"]["sha256"] == sha(WALK),
        "completeExactPlan": len(records) == 96 and operation_keys == expected_keys,
        "allOverallVerdictsPass": all(record["vector"]["overallRepetitionVerdict"] == "PASS" for record in records),
        "allComponentVerdictsPass": all_components_pass,
        "allScientificAndEvidenceOraclesPass": all_oracles_pass,
        "interactionCountsMatch": execution["interactionCountsMatch"] is True,
        "ledgerValid": execution["ledgerValid"] is True,
        "finalInventoryClean": execution["finalInventoryClean"] is True,
        "noTerminalException": execution["terminalException"] is None,
        "noAutomaticRerun": execution["workload"]["automaticReruns"] == 0 and execution["automaticRerun"] == "NOT_RUN",
        "upstreamClaimsBound": set(prereg["upstreamClaims"]) == {"S1", "S2", "M1"},
    }
    if not all(checks.values()):
        raise RuntimeError("final E1 scientific adjudication failed")
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    adjudication = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_FINAL_SCIENTIFIC_ADJUDICATION",
        "status": "PASS_ACCEPTED",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "executionIdentity": execution["executionIdentity"],
        "executionReceipt": {"path": str(EXECUTION.relative_to(ROOT)), "sha256": sha(EXECUTION), "embeddedReceiptSha256": execution["receiptSha256"]},
        "walk": {"path": str(WALK.relative_to(ROOT)), "sha256": sha(WALK), "records": len(records)},
        "checks": checks,
        "workload": {"scenarios": 18, "repetitions": 96, "perScenario": dict(sorted(per_scenario.items()))},
        "acceptedClaim": "INTEGRATED_RUNTIME_TUPLE_QUALIFIED",
        "step15Status": "COMPLETE_ACCEPTED",
        "excludedClaims": prereg["excludedClaims"],
    }
    adjudication["receiptSha256"] = digest(adjudication)
    write_json(OUTPUT, adjudication)
    acceptance = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_FINAL_SCIENTIFIC_ADJUDICATION_ACCEPTANCE",
        "status": "ACCEPTED_FROZEN",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "executionIdentity": execution["executionIdentity"],
        "adjudicationPath": str(OUTPUT.relative_to(ROOT)),
        "adjudicationSha256": sha(OUTPUT),
        "adjudicationReceiptSha256": adjudication["receiptSha256"],
        "acceptedClaim": "INTEGRATED_RUNTIME_TUPLE_QUALIFIED",
        "step15Status": "COMPLETE_ACCEPTED",
        "principalStatement": "complete it, please.",
        "additionalExecutionAuthorized": False,
    }
    write_json(ACCEPTANCE, acceptance)
    architecture = f"""# Step 15 final E1 acceptance\n\n## Outcome\n\nStep 15 is **COMPLETE / ACCEPTED**. The final E1 measured execution passed all 18 scenarios and all 96 preregistered repetitions with no automatic rerun.\n\nAccepted claim: `INTEGRATED_RUNTIME_TUPLE_QUALIFIED`.\n\n## Evidence\n\n- Package identity: `{package['packageIdentity']}`\n- Execution identity: `{execution['executionIdentity']}`\n- Execution receipt SHA-256: `{sha(EXECUTION)}`\n- Execution walk SHA-256: `{sha(WALK)}`\n- Scientific adjudication SHA-256: `{sha(OUTPUT)}`\n- Executed repetitions: 96 / 96\n- Overall verdicts: PASS 96\n- Ledger: PASS\n- Final disposable inventory: clean\n- Automatic reruns: 0\n\n## Scope\n\nThis acceptance qualifies the exact integrated SMA, OpenHands boundary, and ddalcu Qwen model-profile tuple bound by the package. It does not claim production readiness, model routing, another runtime tuple, Teams event-export qualification, or SMA live-shadow qualification.\n"""
    write_text(ARCHITECTURE, architecture)
    print(json.dumps({"status": adjudication["status"], "acceptedClaim": adjudication["acceptedClaim"], "step15Status": adjudication["step15Status"], "adjudicationFileSha256": sha(OUTPUT), "acceptanceFileSha256": sha(ACCEPTANCE), "architectureFileSha256": sha(ARCHITECTURE)}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
