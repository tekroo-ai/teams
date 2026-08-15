#!/usr/bin/env python3
"""Run one disposable bare OpenHands turn and retain its terminal event shape."""

from __future__ import annotations

import copy
import json
import time
from typing import Any

import openhands_response_lane_qualification_v1 as lane


TERMINAL_STATUSES = {"finished", "paused", "error"}


def terminal_projection(event: dict[str, Any]) -> dict[str, Any]:
    action = event.get("action") or {}
    return {
        "id": event.get("id"),
        "kind": event.get("kind"),
        "source": event.get("source"),
        "tool_name": event.get("tool_name"),
        "message_text": lane.event_text(event),
        "action_kind": action.get("kind") if isinstance(action, dict) else None,
        "action_message": action.get("message") if isinstance(action, dict) else None,
    }


def main() -> int:
    key = lane.session_key()
    status, settings_payload = lane.http(
        "GET", lane.INGRESS, "/api/settings", key, timeout=15, expose_encrypted=True
    )
    if status != 200:
        raise RuntimeError(f"settings returned HTTP {status}")
    settings = copy.deepcopy(settings_payload["agent_settings"])
    settings["tools"] = []
    settings["enable_sub_agents"] = False
    settings["enable_switch_llm_tool"] = False
    settings["mcp_config"] = {}

    conversation, creation_ms = lane.create_conversation(key, settings)
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
        detail: dict[str, Any] = {}
        events: list[dict[str, Any]] = []
        while time.monotonic() < deadline:
            events = lane.fetch_events(conversation, key)
            status, detail = lane.http(
                "GET",
                lane.INGRESS,
                f"/api/conversations/{conversation}",
                key,
                timeout=15,
            )
            if status != 200:
                raise RuntimeError(f"conversation detail returned HTTP {status}")
            if detail.get("execution_status") in TERMINAL_STATUSES:
                break
            time.sleep(0.05)

        print(
            json.dumps(
                {
                    "conversation_id": conversation,
                    "creation_ms": creation_ms,
                    "execution_status": detail.get("execution_status"),
                    "events": [terminal_projection(event) for event in events],
                },
                indent=2,
                sort_keys=True,
            )
        )
        return 0
    finally:
        cleanup = lane.delete_conversation(conversation, key)
        if not cleanup["verified"]:
            raise RuntimeError(f"cleanup failed: {cleanup}")


if __name__ == "__main__":
    raise SystemExit(main())
