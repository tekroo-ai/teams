#!/usr/bin/env python3
from __future__ import annotations

import base64
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
V6 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v6"
for package in (S2, *PACKAGES, ADJUDICATION, NATIVE, V4, V5, V6):
    sys.path.insert(0, str(package))

from e1_native_v6.runner import CONDENSATION_PREFIX, CONDENSATION_SUFFIX, is_real_condensation_prompt  # noqa: E402
from t1.model import canonical_bytes, digest, file_sha256  # noqa: E402


SOURCE_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v5-case18/case18-walk.jsonl"
SOURCE_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v5-case18/composite-canary-receipt.json"
BASE_OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v5-offline-qualification/offline-qualification.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v6-offline-qualification/offline-qualification.json"
SOURCES = (V6 / "e1_native_v6/__init__.py", V6 / "e1_native_v6/runner.py", V6 / "e1_native_v6/live_entrypoint.py", ROOT / "scripts/execute_sma_e1_native_tool_repair_v6_case18.py", ROOT / "scripts/qualify_sma_e1_native_tool_repair_v6.py", ROOT / "scripts/build_sma_e1_native_tool_repair_v6.py")
FROZEN = "What API timeout applies to repository alpha?"


def controls():
    row = json.loads(SOURCE_WALK.read_text(encoding="utf-8").strip())
    observation = row["observation"]
    request_rows = [receipt["row"] for receipt in observation["raw_receipts"] if receipt.get("kind") == "e1_model_audit_request_raw"]
    payloads = [json.loads(base64.b64decode(item["requestBodyBase64"])) for item in request_rows]
    condensation_segments = []
    for payload in payloads:
        if payload.get("tools"):
            continue
        for message in payload.get("messages", []):
            if message.get("role") != "user":
                continue
            content = message.get("content")
            if isinstance(content, list):
                condensation_segments.extend(str(segment.get("text") or "") for segment in content if isinstance(segment, dict))
            else:
                condensation_segments.append(str(content or ""))
    matches = [value for value in condensation_segments if is_real_condensation_prompt(value, FROZEN)]
    receipt = json.loads(SOURCE_RECEIPT.read_text(encoding="utf-8"))
    base = json.loads(BASE_OFFLINE.read_text(encoding="utf-8"))
    values = {
        "source_failure_is_decoder_only": observation.get("failure") == "HARNESS" and observation.get("failure_detail") == "E1 request lost the frozen task across the OpenHands agent loop",
        "sequencing_repair_reached_condensation": sum(item.get("kind") == "prompt_submission" for item in observation["raw_receipts"]) == 2 and sum(item.get("kind") == "condensation_completed" for item in observation["raw_receipts"]) == 1,
        "two_distinct_turn_terminals_retained": len([item for item in observation["timings"] if item.get("kind") == "conversation_terminal"]) == 2,
        "one_real_tool_free_condensation_prompt": len(matches) == 1,
        "real_prompt_fixed_prefix": matches[0].strip().startswith(CONDENSATION_PREFIX),
        "real_prompt_fixed_suffix": matches[0].strip().endswith(CONDENSATION_SUFFIX),
        "real_prompt_embeds_exact_frozen_task": FROZEN in matches[0],
        "source_cleanup_clean": receipt.get("finalInventoryClean") is True and receipt.get("ledgerValid") is True,
        "base_v5_controls_green": base.get("status") == "PASS_NATIVE_TOOL_V5_OFFLINE_QUALIFICATION" and base.get("summary") == {"passed": 9, "total": 9},
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
    value = {"schemaVersion": "1.0.0", "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V6_OFFLINE_QUALIFICATION", "status": "PASS_NATIVE_TOOL_V6_OFFLINE_QUALIFICATION" if all(row["verdict"] == "PASS" for row in result) else "NO_GO_NATIVE_TOOL_V6_OFFLINE_QUALIFICATION", "packageSourceIdentity": identity, "sources": sources, "controls": result, "summary": {"passed": sum(row["verdict"] == "PASS" for row in result), "total": len(result)}, "sourceCheckpoint": {"walkSha256": file_sha256(SOURCE_WALK), "receiptSha256": file_sha256(SOURCE_RECEIPT)}, "liveActivity": "NOT_RUN", "modelCalls": 0, "measuredExecution": "NOT_RUN"}
    value["receiptSha256"] = digest(value)
    if OUTPUT.exists():
        raise RuntimeError("v6 offline output already exists")
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
