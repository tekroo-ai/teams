#!/usr/bin/env python3
"""Exercise the bound hook through the frozen OpenHands hook product path."""

from __future__ import annotations

import argparse
import copy
import json
import shlex
import sys
from pathlib import Path
from typing import Any

from openhands.agent_server.models import EventPage
from openhands.sdk.event import (
    AgentResponseFinality,
    AuthorshipOrigin,
    HookExecutionEvent,
    MessageEvent,
    SemanticPurpose,
)
from openhands.sdk.hooks.config import HookConfig, HookDefinition, HookMatcher
from openhands.sdk.hooks.conversation_hooks import HookEventProcessor
from openhands.sdk.hooks.manager import HookManager
from openhands.sdk.llm import Message, TextContent


FIXED_TIMESTAMP = "2026-08-28T12:00:00+00:00"
PROMPT = "Implement the product-bound fixture."


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--adapter", required=True, type=Path)
    parser.add_argument("--hook", required=True, type=Path)
    parser.add_argument("--sma-truth", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--command-receipt", required=True, type=Path)
    args = parser.parse_args()
    workspace = Path("/tmp/sma-s2-product-truth")
    workspace.mkdir(parents=True, exist_ok=True)

    command = shlex.join(
        [
            sys.executable,
            str(args.adapter),
            "--command-mode",
            "--hook",
            str(args.hook),
            "--sma-truth",
            str(args.sma_truth),
            "--command-receipt",
            str(args.command_receipt),
        ]
    )
    config = HookConfig(
        user_prompt_submit=[
            HookMatcher(
                matcher="*",
                hooks=[HookDefinition(command=command, timeout=10)],
            )
        ]
    )
    manager = HookManager(
        config=config,
        working_dir="/tmp/sma-s2-product-truth",
        session_id="conv-product-truth",
    )
    emitted: list[Any] = []
    processor = HookEventProcessor(manager, original_callback=emitted.append)
    message = MessageEvent(
        id="evt-user-task-hook-path",
        timestamp=FIXED_TIMESTAMP,
        source="user",
        authorship_origin=AuthorshipOrigin.CONVERSATION_INPUT,
        semantic_purpose=SemanticPurpose.TASK_INPUT,
        agent_response_finality=AgentResponseFinality.NOT_APPLICABLE,
        llm_message=Message(
            role="user",
            content=[TextContent(text=PROMPT)],
        ),
    )
    try:
        processor.on_event(message)
    finally:
        try:
            workspace.rmdir()
        except OSError:
            pass

    hook_events = [event for event in emitted if isinstance(event, HookExecutionEvent)]
    if len(hook_events) != 1:
        raise RuntimeError(f"expected one HookExecutionEvent, got {len(hook_events)}")
    event = hook_events[0]
    page = EventPage(items=[event])
    page_payload = page.model_dump(mode="json", by_alias=True)
    round_trip = EventPage.model_validate(copy.deepcopy(page_payload)).model_dump(
        mode="json", by_alias=True
    )
    if round_trip != page_payload:
        raise RuntimeError("HookExecutionEvent EventPage round-trip drift")
    serialized = page_payload["items"][0]
    if not serialized["stdout"].strip():
        raise RuntimeError(
            "bound hook command produced no stdout: "
            + json.dumps(
                {
                    "success": serialized.get("success"),
                    "exit_code": serialized.get("exit_code"),
                    "stderr": serialized.get("stderr"),
                    "error": serialized.get("error"),
                },
                sort_keys=True,
            )
        )
    parsed_stdout = json.loads(serialized["stdout"])
    bridge_truth = json.loads(args.sma_truth.read_text(encoding="utf-8"))[
        "bridgeResponse"
    ]
    command_receipt = json.loads(args.command_receipt.read_text(encoding="utf-8"))

    if serialized["hook_input"] != {"message": PROMPT}:
        raise RuntimeError("product hook producer persisted unexpected hook_input")
    if parsed_stdout.get("smaTraceId") != bridge_truth.get("trace_id"):
        raise RuntimeError("product hook event did not retain handler trace")
    if serialized.get("additional_context") != bridge_truth.get("context_block"):
        raise RuntimeError("product hook producer altered additional context")

    receipt = {
        "schemaVersion": "2.0.0-dev",
        "recordType": "BOUND_SMA_HOOK_OPENHANDS_PRODUCT_PATH",
        "authoritativePositiveSource": (
            "OPENHANDS_HOOK_MANAGER_EXECUTOR_AND_EVENT_PROCESSOR"
        ),
        "transport": "IN_PROCESS_BRIDGE_RESPONSE_NO_NETWORK",
        "configuredProbeCommand": command,
        "persistedHookExecutionEvent": serialized,
        "parsedHookStdout": parsed_stdout,
        "emittedEventKinds": [item.__class__.__name__ for item in emitted],
        "boundHookCommandReceipt": command_receipt,
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(receipt, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()
