#!/usr/bin/env python3
"""Qualify the deterministic OpenHands response lane with an exact tool set."""

from __future__ import annotations

import copy
import datetime as dt
import hashlib
import json
import os
import shutil
import time
from pathlib import Path
from typing import Any

import openhands_response_lane_qualification_v1 as lane


ROOT = Path("/Users/paul/work/tekroo-ai/teams")
OUTPUT = ROOT / "OUTPUT/phase-3/openhands-exact-capability-v2"
JOURNAL = OUTPUT / "raw-receipts.jsonl"
SUMMARY = OUTPUT / "execution-receipt.json"
WORKSPACE = ROOT / "target/openhands-exact-capability-v2"
ATTEMPTS = 128
TEMPERATURE = 0.0
SEED = 20260813
EXPECTED_BUILTINS = ["FinishTool"]


def now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat()


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def append(record: dict[str, Any]) -> None:
    with JOURNAL.open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n")
        stream.flush()
        os.fsync(stream.fileno())


def delete(conversation: str, key: str) -> None:
    cleanup = lane.delete_conversation(conversation, key)
    if not cleanup["verified"]:
        raise RuntimeError(f"conversation cleanup failed: {cleanup}")


def run_once(ordinal: int, key: str, settings: dict[str, Any]) -> dict[str, Any]:
    conversation, creation_ms = lane.create_conversation(key, settings)
    started = time.monotonic()
    try:
        detail_status, detail = lane.http(
            "GET", lane.INGRESS, f"/api/conversations/{conversation}", key, timeout=15
        )
        if detail_status != 200:
            raise RuntimeError(f"conversation detail returned HTTP {detail_status}")
        agent = detail.get("agent") or {}
        capability = {
            "include_default_tools": agent.get("include_default_tools"),
            "auto_attach_vision_inspect_tool": agent.get(
                "auto_attach_vision_inspect_tool"
            ),
            "configured_tool_specs": len(agent.get("tools") or []),
            "configured_mcp_servers": len(agent.get("mcp_config") or {}),
        }
        if capability != {
            "include_default_tools": EXPECTED_BUILTINS,
            "auto_attach_vision_inspect_tool": False,
            "configured_tool_specs": 0,
            "configured_mcp_servers": 0,
        }:
            raise RuntimeError(f"effective capability mismatch: {capability}")

        status, _ = lane.http(
            "POST",
            lane.INGRESS,
            f"/api/conversations/{conversation}/events",
            key,
            {
                "role": "user",
                "run": True,
                "content": [{"type": "text", "text": lane.PROMPT}],
            },
            30,
        )
        if status != 200:
            raise RuntimeError(f"prompt submission returned HTTP {status}")

        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            events = lane.fetch_events(conversation, key)
            errors = [event for event in events if event.get("kind") == "ConversationErrorEvent"]
            if errors:
                return {
                    "ordinal": ordinal,
                    "status": "FAIL",
                    "terminal": "CONVERSATION_ERROR_EVENT",
                    "capability": capability,
                    "error": errors[-1],
                }
            terminal = lane.agent_final_response(events)
            if terminal is not None:
                event, response, shape = terminal
                status, final_detail = lane.http(
                    "GET",
                    lane.INGRESS,
                    f"/api/conversations/{conversation}",
                    key,
                    timeout=15,
                )
                if status != 200:
                    raise RuntimeError(f"final detail returned HTTP {status}")
                if final_detail.get("execution_status") != "finished":
                    time.sleep(0.05)
                    continue
                actions = [
                    {
                        "kind": item.get("kind"),
                        "tool_name": item.get("tool_name"),
                        "action_kind": (item.get("action") or {}).get("kind"),
                    }
                    for item in events
                    if item.get("source") == "agent"
                    and item.get("kind") in {"ActionEvent", "MessageEvent"}
                ]
                return {
                    "ordinal": ordinal,
                    "status": "PASS" if response == lane.EXPECTED else "FAIL",
                    "terminal": shape,
                    "response": response,
                    "response_exact": response == lane.EXPECTED,
                    "wall_ms": (time.monotonic() - started) * 1000,
                    "creation_ms": creation_ms,
                    "capability": capability,
                    "agent_events": actions,
                    "terminal_event_id": event.get("id"),
                }
            time.sleep(0.05)
        return {
            "ordinal": ordinal,
            "status": "FAIL",
            "terminal": "TIMEOUT",
            "capability": capability,
        }
    finally:
        delete(conversation, key)


def main() -> int:
    if OUTPUT.exists() or WORKSPACE.exists():
        raise RuntimeError("qualification output or workspace already exists")
    OUTPUT.mkdir(parents=True)
    lane.WORKSPACE = WORKSPACE
    key = lane.session_key()
    status, payload = lane.http(
        "GET", lane.INGRESS, "/api/settings", key, timeout=15, expose_encrypted=True
    )
    if status != 200:
        raise RuntimeError(f"settings returned HTTP {status}")
    settings = copy.deepcopy(payload["agent_settings"])
    settings["tools"] = []
    settings["enable_sub_agents"] = False
    settings["enable_switch_llm_tool"] = False
    settings["include_default_tools"] = EXPECTED_BUILTINS
    settings["auto_attach_vision_inspect_tool"] = False
    settings["mcp_config"] = {}
    settings["llm"]["temperature"] = TEMPERATURE
    settings["llm"]["seed"] = SEED

    results: list[dict[str, Any]] = []
    try:
        for ordinal in range(1, ATTEMPTS + 1):
            result = run_once(ordinal, key, settings)
            append({"recorded_at": now(), **result})
            results.append(result)
            if result["status"] != "PASS":
                break
    finally:
        if WORKSPACE.exists():
            shutil.rmtree(WORKSPACE)

    failures = [result for result in results if result["status"] != "PASS"]
    summary = {
        "schemaVersion": "1.0.0",
        "recordType": "OPENHANDS_EXACT_CAPABILITY_QUALIFICATION_RECEIPT",
        "classification": "COMPUTED",
        "status": "PASS" if len(results) == ATTEMPTS and not failures else "FAIL",
        "completedAttempts": len(results),
        "requiredAttempts": ATTEMPTS,
        "failedAttempts": len(failures),
        "exactResponseCount": sum(bool(item.get("response_exact")) for item in results),
        "effectiveCapabilities": {
            "configuredTools": [],
            "includeDefaultTools": EXPECTED_BUILTINS,
            "autoAttachVisionInspectTool": False,
            "mcpConfig": {},
            "temperature": TEMPERATURE,
            "seed": SEED,
        },
        "terminalCounts": {
            shape: sum(item.get("terminal") == shape for item in results)
            for shape in sorted({str(item.get("terminal")) for item in results})
        },
        "unexpectedAgentToolNames": sorted(
            {
                str(event.get("tool_name"))
                for item in results
                for event in item.get("agent_events", [])
                if event.get("kind") == "ActionEvent"
                and event.get("tool_name") != "finish"
            }
        ),
        "runnerSHA256": sha_file(Path(__file__)),
        "openhandsSources": {
            "agentBaseSHA256": sha_file(
                Path("/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel/openhands-sdk/openhands/sdk/agent/base.py")
            ),
            "settingsModelSHA256": sha_file(
                Path("/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel/openhands-sdk/openhands/sdk/settings/model.py")
            ),
        },
        "rawJournalSHA256": sha_file(JOURNAL),
        "workspaceRemoved": not WORKSPACE.exists(),
        "completedAt": now(),
    }
    SUMMARY.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n")
    print(json.dumps(summary, indent=2, sort_keys=True))
    return 0 if summary["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
