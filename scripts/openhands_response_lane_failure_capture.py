#!/usr/bin/env python3
"""Capture the first non-exact terminal response from the bare OpenHands lane.

This diagnostic prints the response body to the operator terminal but does not
persist it. Every disposable conversation is deleted and verified absent.
"""

from __future__ import annotations

import copy
import json
import time
from typing import Any

import openhands_response_lane_qualification_v1 as lane


MAX_ATTEMPTS = 128
DETERMINISTIC_TEMPERATURE = 0.0


def run_once(ordinal: int, key: str, settings: dict[str, Any]) -> dict[str, Any]:
    conversation, creation_ms = lane.create_conversation(key, settings)
    started = time.monotonic()
    try:
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
            terminal = lane.agent_final_response(events)
            if terminal is not None:
                event, response, shape = terminal
                detail_status, detail = lane.http(
                    "GET",
                    lane.INGRESS,
                    f"/api/conversations/{conversation}",
                    key,
                    timeout=15,
                )
                if detail_status != 200:
                    raise RuntimeError(
                        f"conversation detail returned HTTP {detail_status}"
                    )
                if detail.get("execution_status") != "finished":
                    time.sleep(0.05)
                    continue
                return {
                    "ordinal": ordinal,
                    "conversation_id": conversation,
                    "creation_ms": creation_ms,
                    "wall_ms": (time.monotonic() - started) * 1000,
                    "terminal": shape,
                    "response": response,
                    "response_exact": response == lane.EXPECTED,
                    "event": {
                        "id": event.get("id"),
                        "kind": event.get("kind"),
                        "source": event.get("source"),
                        "tool_name": event.get("tool_name"),
                        "action": event.get("action"),
                    },
                }
            time.sleep(0.05)
        raise TimeoutError("terminal response not observed within 30 seconds")
    finally:
        cleanup = lane.delete_conversation(conversation, key)
        if not cleanup["verified"]:
            raise RuntimeError(f"cleanup failed: {cleanup}")


def main() -> int:
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
    settings["mcp_config"] = {}
    settings["llm"]["temperature"] = DETERMINISTIC_TEMPERATURE

    for ordinal in range(1, MAX_ATTEMPTS + 1):
        result = run_once(ordinal, key, settings)
        if not result["response_exact"]:
            print(json.dumps(result, indent=2, sort_keys=True))
            return 0
        if ordinal % 10 == 0:
            print(f"{ordinal} exact responses observed", flush=True)
    print(json.dumps({"attempts": MAX_ATTEMPTS, "non_exact_observed": False}))
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
