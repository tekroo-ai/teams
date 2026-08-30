#!/usr/bin/env python3
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[1]
S2 = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}" for number in (1, 3, 4, 5, 6)]
ADJUDICATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-interaction-adjudication"
NATIVE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair"
V4 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4"
V5 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v5"
for package in (S2, *PACKAGES, ADJUDICATION, NATIVE, V4, V5):
    sys.path.insert(0, str(package))

from t1.model import canonical_bytes, digest, file_sha256  # noqa: E402


RUNNER = V5 / "e1_native_v5/runner.py"
SOURCE_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-continuation-13-18/continuation-walk.jsonl"
SOURCE_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-continuation-13-18/composite-canary-receipt.json"
BASE_OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-offline-qualification/offline-qualification.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v5-offline-qualification/offline-qualification.json"
SOURCES = (
    V5 / "e1_native_v5/__init__.py", V5 / "e1_native_v5/runner.py", V5 / "e1_native_v5/live_entrypoint.py",
    ROOT / "scripts/qualify_sma_e1_native_tool_repair_v5.py", ROOT / "scripts/execute_sma_e1_native_tool_repair_v5_case18.py", ROOT / "scripts/build_sma_e1_native_tool_repair_v5.py",
)


def controls() -> list[dict[str, str]]:
    rows = [json.loads(line) for line in SOURCE_WALK.read_text(encoding="utf-8").splitlines() if line]
    receipt = json.loads(SOURCE_RECEIPT.read_text(encoding="utf-8"))
    base = json.loads(BASE_OFFLINE.read_text(encoding="utf-8"))
    source = RUNNER.read_text(encoding="utf-8")
    method = source[source.index("    def _case_condensation"):]
    sequence = [method.find("self._submit(observation, conversation, prompt)"), method.find("self._poll_conversation_terminal(observation, conversation)"), method.find("/condense"), method.find("self._submit(observation, conversation, prompt)", method.find("/condense")), method.find("self._poll_new_turn_terminal(observation, conversation, first_user_message_id)")]
    values = {
        "source_case18_pre_model_timeout_exact": len(rows) == 6 and rows[-1]["vector"]["operationKey"] == "SMA-S2-018-CONDENSATION-REANCHOR#001" and rows[-1]["observation"].get("failure_detail") == "condensation failed" and len(rows[-1]["observation"].get("model_requests", [])) == 0,
        "source_cases13_through17_green": all(row["vector"]["overallRepetitionVerdict"] == "PASS" for row in rows[:5]),
        "source_cleanup_clean": receipt.get("finalInventoryClean") is True and receipt.get("ledgerValid") is True,
        "turn_sequence_is_submit_wait_condense_submit_wait": all(value >= 0 for value in sequence) and sequence == sorted(sequence),
        "condensation_timeout_is_120_seconds": "b\"\", 120_000" in method,
        "second_turn_requires_new_user_identity": "user_message_id != previous_user_message_id" in source,
        "second_turn_requires_two_stable_reads": "stable_reads >= 2" in source,
        "base_v4_controls_green": base.get("status") == "PASS_NATIVE_TOOL_V4_OFFLINE_QUALIFICATION" and base.get("summary") == {"passed": 9, "total": 9},
    }
    return [{"control": key, "verdict": "PASS" if value else "FAIL"} for key, value in values.items()]


def source_identity():
    rows = [{"path": str(path.relative_to(ROOT)), "sha256": file_sha256(path)} for path in SOURCES]
    return rows, digest(rows)


def main() -> int:
    child = "--child" in sys.argv
    result = controls()
    if child:
        print(json.dumps(result, sort_keys=True, separators=(",", ":")))
        return 0 if all(row["verdict"] == "PASS" for row in result) else 2
    completed = subprocess.run([sys.executable, "-O", str(Path(__file__).resolve()), "--child"], cwd=ROOT, capture_output=True, text=True, check=False)
    optimized = json.loads(completed.stdout) if completed.returncode == 0 else []
    result.append({"control": "optimized_runtime_equivalence", "verdict": "PASS" if optimized == result else "FAIL"})
    sources, identity = source_identity()
    value = {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V5_OFFLINE_QUALIFICATION", "status": "PASS_NATIVE_TOOL_V5_OFFLINE_QUALIFICATION" if all(row["verdict"] == "PASS" for row in result) else "NO_GO_NATIVE_TOOL_V5_OFFLINE_QUALIFICATION", "packageSourceIdentity": identity, "sources": sources, "controls": result, "summary": {"passed": sum(row["verdict"] == "PASS" for row in result), "total": len(result)}, "sourceCheckpoint": {"walkSha256": file_sha256(SOURCE_WALK), "receiptSha256": file_sha256(SOURCE_RECEIPT)}, "liveActivity": "NOT_RUN", "modelCalls": 0, "measuredExecution": "NOT_RUN"}
    value["receiptSha256"] = digest(value)
    if OUTPUT.exists():
        raise RuntimeError("v5 offline output already exists")
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(OUTPUT, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical_bytes(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())
    print(json.dumps({"status": value["status"], "summary": value["summary"], "packageSourceIdentity": identity, "fileSha256": file_sha256(OUTPUT), "receiptSha256": value["receiptSha256"]}, sort_keys=True))
    return 0 if value["status"].startswith("PASS_") else 2


if __name__ == "__main__":
    raise SystemExit(main())
