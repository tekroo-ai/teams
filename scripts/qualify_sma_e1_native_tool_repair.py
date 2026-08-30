#!/usr/bin/env python3
from __future__ import annotations

import argparse
from hashlib import sha256
import json
import os
from pathlib import Path
import subprocess
import sys
from types import SimpleNamespace
from typing import Any, Callable


ROOT = Path(__file__).resolve().parents[1]
S2 = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}" for number in (1, 3, 4, 5, 6)]
ADJUDICATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-interaction-adjudication"
NATIVE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair"
for package in (S2, *PACKAGES, ADJUDICATION, NATIVE):
    sys.path.insert(0, str(package))

from e1_candidate4.runner import CONDENSATION_PROMPT  # noqa: E402
from e1_native.live_entrypoint import load_definition  # noqa: E402
from e1_native.runner import (  # noqa: E402
    NATIVE_TOOL_NAMES,
    _native_tool,
    _semantic_content,
    expected_native_tools,
    interaction_answers,
    model_behavior,
)
from t1.model import HarnessFailure, canonical_bytes, digest, file_sha256  # noqa: E402


OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-offline-qualification/offline-qualification.json"
LAUNCH = NATIVE / "e1_native/live-launch-definition.json"
CASE16_FAILURE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-6-all-scenario-canary-continuation/continuation-walk.jsonl"
SOURCES = (
    NATIVE / "e1_native/__init__.py",
    NATIVE / "e1_native/runner.py",
    NATIVE / "e1_native/live_entrypoint.py",
    ROOT / "scripts/qualify_sma_e1_native_tool_repair.py",
    ROOT / "scripts/build_sma_e1_native_tool_repair.py",
    ROOT / "scripts/execute_sma_e1_native_tool_repair.py",
)


def expect_failure(fn: Callable[[], Any]) -> bool:
    try:
        fn()
    except HarnessFailure:
        return True
    return False


def observation(case_id: str, roles: list[str], calls: list[dict[str, Any]]) -> SimpleNamespace:
    conversations = [{"id": f"conversation-{role}", "role": role} for role in dict.fromkeys(roles)]
    receipts = [{"kind": "prompt_submission", "conversationId": f"conversation-{role}"} for role in roles]
    requests = []
    terminals = []
    for index, call in enumerate(calls, start=1):
        request_id = f"request-{index}"
        requests.append({
            "requestId": request_id,
            "receivedNs": index,
            "interactionRole": call["role"],
            "prompt": call.get("prompt", "task"),
        })
        terminals.append({
            "requestId": request_id,
            "content": call.get("content", ""),
            "nativeToolName": call.get("tool"),
            "nativeToolArguments": call.get("arguments", {}),
            "finishReason": call.get("finish", "tool_calls" if call.get("tool") else "stop"),
        })
    return SimpleNamespace(
        key=SimpleNamespace(case_id=case_id),
        conversations=conversations,
        raw_receipts=receipts,
        model_requests=requests,
        model_terminals=terminals,
    )


def known_case16_failure() -> str:
    if not CASE16_FAILURE.is_file():
        raise RuntimeError("retained candidate-6 case-16 failure walk is missing")
    for line in CASE16_FAILURE.read_text(encoding="utf-8").splitlines():
        row = json.loads(line)
        if row.get("observation", {}).get("key", {}).get("case_id") != "SMA-S2-016-EMPTY-RESULT":
            continue
        outputs = [str(item.get("content") or "") for item in row["observation"].get("model_terminals", [])]
        if outputs:
            return outputs[-1]
    raise RuntimeError("retained candidate-6 case-16 output is absent")


def run_checks() -> list[dict[str, Any]]:
    checks: list[tuple[str, bool]] = []
    add = lambda name, passed: checks.append((name, bool(passed)))

    name, args = _native_tool({"tool_calls": [{"function": {"name": "finish", "arguments": '{"message":"UNKNOWN"}'}}]})
    add("native_finish_decodes", name == "finish" and args == {"message": "UNKNOWN"})
    add("native_finish_semantic_content", "UNKNOWN" in _semantic_content(None, name, args))
    add("native_think_semantic_content", "inspect" in _semantic_content(None, "think", {"thought": "inspect"}))
    add("plain_response_preserved", _semantic_content("plain", None, {}) == "plain")
    add("multiple_tool_calls_rejected", expect_failure(lambda: _native_tool({"tool_calls": [{"function": {"name": "finish", "arguments": "{}"}}, {"function": {"name": "think", "arguments": "{}"}}]})))
    add("unknown_tool_rejected", expect_failure(lambda: _native_tool({"tool_calls": [{"function": {"name": "shell", "arguments": "{}"}}]})))
    add("malformed_arguments_rejected", expect_failure(lambda: _native_tool({"tool_calls": [{"function": {"name": "finish", "arguments": "{"}}]})))
    add("empty_finish_rejected", expect_failure(lambda: _semantic_content(None, "finish", {})))
    add("ordinary_exact_tool_set", expected_native_tools("SMA-S2-016-EMPTY-RESULT", "task") == NATIVE_TOOL_NAMES)
    add("case14_client_tool_added", expected_native_tools("SMA-S2-014-FEEDBACK-LOOP-PREVENTION", "task") == NATIVE_TOOL_NAMES | {"launch_child_conversation"})
    add("condensation_has_no_tools", expected_native_tools("SMA-S2-018-CONDENSATION-REANCHOR", CONDENSATION_PROMPT) == set())

    case16 = observation("SMA-S2-016-EMPTY-RESULT", ["primary"], [
        {"role": "primary", "tool": "think", "arguments": {"thought": "check memory"}},
        {"role": "primary", "tool": "finish", "arguments": {"message": "UNKNOWN"}},
    ])
    answers, outputs, selected, detail = interaction_answers(case16)
    add("think_then_finish_selects_one_answer", selected and answers == ["UNKNOWN"] and "NATIVE_FINISH_TOOL" in detail)
    add("case16_native_finish_passes", model_behavior(case16.key.case_id, answers, outputs, selected, detail)[0])
    bad = known_case16_failure()
    add("known_case16_planning_answer_rejected", not model_behavior(case16.key.case_id, [bad], [bad], True, "retained failure")[0])

    concurrent = observation("SMA-S2-013-FOUR-CHANNEL-CONCURRENCY", ["alpha-1", "alpha-2", "beta-1", "beta-2"], [
        {"role": role, "tool": "finish", "arguments": {"message": role}}
        for role in ("alpha-1", "alpha-2", "beta-1", "beta-2")
    ])
    answers, outputs, selected, detail = interaction_answers(concurrent)
    add("four_channel_answers_preserved", selected and len(answers) == 4 and model_behavior(concurrent.key.case_id, answers, outputs, selected, detail)[0])

    condensation = observation("SMA-S2-018-CONDENSATION-REANCHOR", ["primary", "primary"], [
        {"role": "primary", "tool": "finish", "arguments": {"message": "17 seconds"}},
        {"role": "primary", "prompt": CONDENSATION_PROMPT, "content": "summary", "finish": "stop"},
        {"role": "primary", "tool": "finish", "arguments": {"message": "17 seconds"}},
    ])
    answers, outputs, selected, detail = interaction_answers(condensation)
    add("condensation_excluded_from_two_answers", selected and answers == ["17 seconds", "17 seconds"])
    add("condensation_behavior_passes", model_behavior(condensation.key.case_id, answers, outputs, selected, detail)[0])

    duplicate_finish = observation("SMA-S2-016-EMPTY-RESULT", ["primary"], [
        {"role": "primary", "tool": "finish", "arguments": {"message": "UNKNOWN"}},
        {"role": "primary", "tool": "finish", "arguments": {"message": "UNKNOWN"}},
    ])
    add("multiple_finish_responses_rejected", interaction_answers(duplicate_finish)[2] is False)

    missing_terminal = observation("SMA-S2-016-EMPTY-RESULT", ["primary"], [{"role": "primary", "tool": "finish", "arguments": {"message": "UNKNOWN"}}])
    missing_terminal.model_terminals = []
    add("request_terminal_mismatch_rejected", interaction_answers(missing_terminal)[2] is False)

    return [{"control": name, "verdict": "PASS" if passed else "FAIL"} for name, passed in checks]


def source_identity() -> tuple[list[dict[str, str]], str]:
    files = [{"path": str(path.relative_to(ROOT)), "sha256": file_sha256(path)} for path in SOURCES]
    return files, digest(files)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--child", action="store_true")
    args = parser.parse_args()
    load_definition(LAUNCH)
    checks = run_checks()
    if args.child:
        print(json.dumps(checks, sort_keys=True, separators=(",", ":")))
        return 0 if all(row["verdict"] == "PASS" for row in checks) else 2
    completed = subprocess.run([sys.executable, "-O", str(Path(__file__).resolve()), "--child"], cwd=ROOT, capture_output=True, text=True, check=False)
    optimized = json.loads(completed.stdout) if completed.returncode == 0 else []
    checks.append({"control": "optimized_runtime_equivalence", "verdict": "PASS" if optimized == checks else "FAIL"})
    files, package_source_identity = source_identity()
    value = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_OFFLINE_QUALIFICATION",
        "status": "PASS_NATIVE_TOOL_OFFLINE_QUALIFICATION" if all(row["verdict"] == "PASS" for row in checks) else "NO_GO_NATIVE_TOOL_OFFLINE_QUALIFICATION",
        "packageSourceIdentity": package_source_identity,
        "sources": files,
        "controls": checks,
        "summary": {"passed": sum(row["verdict"] == "PASS" for row in checks), "total": len(checks)},
        "knownFailureEvidence": {"path": str(CASE16_FAILURE.relative_to(ROOT)), "sha256": file_sha256(CASE16_FAILURE)},
        "liveActivity": "NOT_RUN",
        "modelCalls": 0,
        "measuredExecution": "NOT_RUN",
    }
    value["receiptSha256"] = digest(value)
    if OUTPUT.exists():
        raise RuntimeError("native-tool offline qualification output already exists")
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(OUTPUT, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical_bytes(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())
    print(json.dumps({"status": value["status"], "summary": value["summary"], "packageSourceIdentity": package_source_identity, "fileSha256": file_sha256(OUTPUT), "receiptSha256": value["receiptSha256"]}, sort_keys=True))
    return 0 if value["status"].startswith("PASS_") else 2


if __name__ == "__main__":
    raise SystemExit(main())
