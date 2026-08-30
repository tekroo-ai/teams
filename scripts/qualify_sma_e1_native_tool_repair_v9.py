#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
from types import MethodType, SimpleNamespace
import sys


ROOT = Path(__file__).resolve().parents[1]
S2 = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}" for number in (1, 3, 4, 5, 6)]
ADJUDICATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-interaction-adjudication"
NATIVE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair"
V4 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4"
V5 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v5"
V6 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v6"
V9 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9"
for package in (S2, *PACKAGES, ADJUDICATION, NATIVE, V4, V5, V6, V9):
    sys.path.insert(0, str(package))

from e1_native_v9.runner import NativeToolRunnerV9  # noqa: E402


SOURCE_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v8-case18/case18-walk.jsonl"
SOURCE_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v8-case18/composite-canary-receipt.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v9-offline-qualification/offline-qualification.json"
SOURCES = (
    V9 / "e1_native_v9/__init__.py",
    V9 / "e1_native_v9/runner.py",
    V9 / "e1_native_v9/live_entrypoint.py",
    ROOT / "scripts/execute_sma_e1_native_tool_repair_v9_case18.py",
    ROOT / "scripts/qualify_sma_e1_native_tool_repair_v9.py",
    ROOT / "scripts/build_sma_e1_native_tool_repair_v9.py",
)


def canonical(value: object) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(value: object) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_exclusive(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


class FakePorts:
    def now_ns(self) -> int:
        return 1

    def sleep_ms(self, _milliseconds: int) -> None:
        return None


def executable_timestamp_control() -> bool:
    runner = object.__new__(NativeToolRunnerV9)
    runner.ports = FakePorts()
    runner.config = SimpleNamespace(operation_deadline_ms=120_000, session_header_value="test")
    completions = iter((101, 202))

    def fake_http(self, observation, service, request):
        completed = next(completions)
        return SimpleNamespace(
            status=200,
            error_kind=None,
            completed_ns=completed,
            json_body=lambda: {
                "execution_status": "finished",
                "last_user_message_id": "turn-2",
                "leaf_event_id": "leaf-2",
            },
        )

    runner._http = MethodType(fake_http, runner)
    observation = SimpleNamespace(timings=[])
    conversation = {"id": "conversation-18"}
    runner._poll_new_turn_terminal(observation, conversation, "turn-1")
    return observation.timings == [
        {
            "kind": "conversation_terminal",
            "atNs": 202,
            "conversationId": "conversation-18",
            "status": "finished",
            "durationMs": 0,
            "deadlineMs": 120_000,
            "lastUserMessageId": "turn-2",
            "leafEventId": "leaf-2",
        }
    ]


def main() -> int:
    walk = json.loads(SOURCE_WALK.read_text(encoding="utf-8"))
    receipt = json.loads(SOURCE_RECEIPT.read_text(encoding="utf-8"))
    evidence = {row["oracle_id"]: row["passed"] for row in walk["evidence"]}
    missing_timestamps = [row for row in walk["observation"]["timings"] if row.get("atNs") is None]
    controls = [
        ("source_is_only_scenario_18", walk["vector"]["operationKey"] == "SMA-S2-018-CONDENSATION-REANCHOR#001"),
        ("source_preserves_seventeen_passes", receipt["sourceBasis"]["preservedPasses"] == 17),
        ("source_science_passed", all(row["passed"] for row in walk["scientific"])),
        ("source_model_behavior_passed", walk["vector"]["modelBehaviorVerdict"] == "PASS"),
        ("source_sma_semantics_passed", walk["vector"]["smaSemanticsVerdict"] == "PASS"),
        ("source_openhands_boundary_passed", walk["vector"]["openHandsBoundaryVerdict"] == "PASS"),
        ("source_only_e008_failed", evidence.get("E-008") is False and all(value for key, value in evidence.items() if key != "E-008")),
        ("source_has_exactly_one_missing_timing", len(missing_timestamps) == 1 and missing_timestamps[0].get("kind") == "conversation_terminal"),
        ("successor_retains_raw_completed_timestamp", executable_timestamp_control()),
        ("source_cleanup_was_clean", receipt["finalInventoryClean"] is True and receipt["ledgerValid"] is True),
    ]
    if not all(passed for _, passed in controls):
        raise RuntimeError("v9 offline qualification failed")
    sources = [{"path": str(path.relative_to(ROOT)), "sha256": sha(path)} for path in SOURCES]
    value = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_NATIVE_TOOL_V9_OFFLINE_QUALIFICATION",
        "status": "PASS_NATIVE_TOOL_V5_OFFLINE_QUALIFICATION",
        "nativeStatus": "PASS_NATIVE_TOOL_V9_OFFLINE_QUALIFICATION",
        "packageSourceIdentity": digest(sources),
        "sources": sources,
        "sourceScenario18": {
            "walk": str(SOURCE_WALK.relative_to(ROOT)),
            "walkSha256": sha(SOURCE_WALK),
            "receipt": str(SOURCE_RECEIPT.relative_to(ROOT)),
            "receiptSha256": sha(SOURCE_RECEIPT),
        },
        "controls": [{"control": name, "verdict": "PASS"} for name, _ in controls],
        "summary": {"passed": len(controls), "total": len(controls)},
        "liveActivity": "NOT_RUN",
        "modelCalls": 0,
        "measuredExecution": "NOT_RUN",
    }
    value["receiptSha256"] = digest(value)
    write_exclusive(OUTPUT, value)
    print(json.dumps({"status": value["nativeStatus"], "passed": len(controls), "fileSha256": sha(OUTPUT)}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
