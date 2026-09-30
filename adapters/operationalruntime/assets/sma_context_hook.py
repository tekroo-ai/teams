#!/usr/bin/env python3
"""Fail-open OpenHands UserPromptSubmit adapter for the local SMA bridge."""

import json
import os
import sys
import urllib.request


ALLOW = {"decision": "allow", "continue": True}


def main() -> None:
    result = dict(ALLOW)
    try:
        event = json.load(sys.stdin)
        message = event.get("message")
        session_id = event.get("session_id")
        working_dir = event.get("working_dir")
        if not all(
            isinstance(value, str) and value
            for value in (message, session_id, working_dir)
        ):
            raise ValueError("missing hook event field")

        request = urllib.request.Request(
            os.environ.get("SMA_BRIDGE_URL", "http://127.0.0.1:8130")
            + "/v1/openhands/context",
            data=json.dumps(
                {
                    "session_id": session_id,
                    "working_dir": working_dir,
                    "prompt": message,
                }
            ).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        deadline = int(os.environ.get("SMA_INTERNAL_DEADLINE_MS", "750")) / 1000.0
        with urllib.request.urlopen(request, timeout=deadline) as response:
            bridge_response = json.loads(response.read().decode("utf-8"))

        hit_count = bridge_response.get("hit_count")
        context_block = bridge_response.get("context_block")
        trace_id = bridge_response.get("trace_id")
        if (
            isinstance(trace_id, str)
            and trace_id.startswith("ctx_")
            and len(trace_id) <= 64
        ):
            result["smaTraceId"] = trace_id
        if (
            isinstance(hit_count, (int, float))
            and not isinstance(hit_count, bool)
            and hit_count > 0
            and isinstance(context_block, str)
            and context_block.strip()
            and len(context_block) <= 4096
        ):
            result["additionalContext"] = context_block
    except Exception:
        pass

    print(json.dumps(result, separators=(",", ":")))


if __name__ == "__main__":
    main()
