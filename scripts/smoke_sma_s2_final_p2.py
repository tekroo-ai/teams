#!/usr/bin/env python3
"""One disposable end-to-end SMA/OpenHands smoke test using the deterministic stub."""

from __future__ import annotations

import json
import os
from pathlib import Path
import sys
import time
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
sys.path.insert(0, str(PACKAGE))

from t1.live_actions import Engine, _load  # noqa: E402


DEFINITION = PACKAGE / "t1/live-launch-definition.json"


def request_json(
    key: str,
    method: str,
    path: str,
    body: dict | None = None,
    timeout: float = 10,
) -> tuple[int, dict]:
    payload = None if body is None else json.dumps(body, separators=(",", ":")).encode()
    headers = {"X-Session-API-Key": key}
    if payload is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(
        "http://127.0.0.1:8000" + path,
        data=payload,
        headers=headers,
        method=method,
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        raw = response.read()
        return response.status, json.loads(raw) if raw else {}


def main() -> int:
    definition = _load(DEFINITION)
    live_python = Path(definition["environment"]["livePython"])
    if Path(sys.executable) != live_python:
        os.execv(str(live_python), [str(live_python), str(Path(__file__).resolve())])
    engine = Engine(definition)
    workspace = engine.root / "SMA-S2-SMOKE-001" / "alpha"
    conversation_id: str | None = None
    outcome: dict[str, object] = {}
    primary_error: BaseException | None = None
    cleanup_error: BaseException | None = None
    key = Path(definition["environment"]["sessionKeyFile"]).read_text().strip()

    try:
        engine.run("cleanup_owned", [])
        engine.run("prepare_workspaces", [str(workspace)])
        configured = engine.run(
            "set_startup_configuration",
            ["CAPTURE_AND_RETRIEVAL", str(workspace)],
        )
        if configured.get("configuredWhileStopped") is not True:
            raise RuntimeError("startup configuration was not applied while stopped")
        engine.run("start_intake_proxy", [])
        engine.run("start_transport_guard", [])
        engine.run("start_stub", [])
        engine.run("start_sma", [])

        status, settings = request_json(key, "GET", "/api/settings")
        if status != 200 or not settings.get("agent_settings"):
            raise RuntimeError("OpenHands settings are unavailable")
        agent_settings = dict(settings["agent_settings"])
        llm = dict(agent_settings.get("llm") or {})
        llm.update({
            "model": "openai/sma-s2-deterministic-stub-final-p2",
            "model_canonical_name": "openai/gpt-4o",
            "base_url": definition["loopback"]["stub"],
            "api_mode": "chat",
            "api_key": "sma-s2-synthetic-local",
            "native_tool_calling": True,
            "force_string_serializer": False,
            "stream": False,
            "temperature": 0,
            "max_output_tokens": 64,
            "num_retries": 0,
            "retry_multiplier": 0,
            "retry_min_wait": 0,
            "retry_max_wait": 0,
            "timeout": 5,
            "log_completions": False,
        })
        agent_settings["llm"] = llm
        agent_settings["tools"] = []
        agent_settings["mcp_config"] = {"mcpServers": {}}
        hook = Path(definition["environment"]["smaRoot"]) / ".openhands/hooks/sma_context_hook.py"
        create_body = {
            "conversation_id": None,
            "parent_conversation_id": None,
            "workspace": {"kind": "LocalWorkspace", "working_dir": str(workspace)},
            "agent_settings": agent_settings,
            "hook_config": {
                "user_prompt_submit": [{
                    "matcher": "*",
                    "hooks": [{"type": "command", "command": str(hook), "timeout": 1}],
                }]
            },
            "max_iterations": 10,
            "autotitle": False,
        }
        status, conversation = request_json(key, "POST", "/api/conversations", create_body)
        if status not in {200, 201} or not conversation.get("id"):
            raise RuntimeError("OpenHands conversation creation failed")
        conversation_id = str(conversation["id"])
        prompt = (
            "[SMA-S2-STUB case=SMA-S2-002-SAME-PARTITION-DELIVERY "
            "repetition=1 mode=SUCCESS] Return the exact text SMOKE_OK."
        )
        submit = {
            "content": [{"type": "text", "text": prompt, "cache_prompt": False}],
            "role": "user",
            "run": True,
        }
        status, _ = request_json(
            key,
            "POST",
            f"/api/conversations/{conversation_id}/events",
            submit,
            timeout=30,
        )
        if status not in {200, 202}:
            raise RuntimeError("OpenHands prompt submission failed")

        terminal = None
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            _, current = request_json(key, "GET", f"/api/conversations/{conversation_id}")
            terminal = current.get("execution_status")
            if terminal in {"finished", "error", "stuck"}:
                break
            time.sleep(0.1)
        if terminal != "finished":
            raise RuntimeError(f"conversation did not finish successfully: {terminal}")

        _, event_page = request_json(
            key,
            "GET",
            f"/api/conversations/{conversation_id}/events/search?limit=100"
            "&kind=openhands.sdk.event.hook_execution.HookExecutionEvent",
        )
        hooks = [
            event for event in event_page.get("items", [])
            if event.get("kind") == "HookExecutionEvent"
            and event.get("hook_event_type") == "UserPromptSubmit"
            and event.get("hook_command") == str(hook)
        ]
        if len(hooks) != 1 or hooks[0].get("success") is not True:
            statuses = [
                {
                    "kind": event.get("kind"),
                    "eventType": event.get("hook_event_type"),
                    "success": event.get("success"),
                    "exitCode": event.get("exit_code"),
                    "error": event.get("error"),
                }
                for event in event_page.get("items", [])
            ]
            raise RuntimeError(f"OpenHands SMA hook result mismatch: {statuses}")

        from pymongo import MongoClient

        client = MongoClient(definition["environment"]["mongoUri"], serverSelectionTimeoutMS=5000)
        collection = client[definition["disposable"]["mongoDatabase"]]["memories"]
        selector = {"origin.openhands_provenance.conversation_id": conversation_id}
        captured = []
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            captured = list(collection.find(selector, {"_id": 1, "origin.openhands_provenance.workspace": 1}))
            if len(captured) >= 2:
                break
            time.sleep(0.25)
        total = collection.count_documents({})
        client.close()
        if len(captured) < 2:
            raise RuntimeError("SMA did not capture the smoke conversation")
        if total != len(captured):
            raise RuntimeError("SMA captured a conversation outside the smoke workspace")
        if any(row.get("origin", {}).get("openhands_provenance", {}).get("workspace") != str(workspace) for row in captured):
            raise RuntimeError("captured memory has the wrong workspace provenance")

        raw_rows = [
            json.loads(line)
            for line in Path(definition["evidenceFiles"]["stubRaw"]).read_text().splitlines()
            if line
        ]
        terminal_rows = [
            json.loads(line)
            for line in Path(definition["evidenceFiles"]["stubTerminal"]).read_text().splitlines()
            if line
        ]
        if len(raw_rows) != 1 or len(terminal_rows) != 1:
            raise RuntimeError("deterministic stub did not retain one request and one terminal")

        outcome = {
            "status": "PASS",
            "conversationFinished": True,
            "hookExecutions": 1,
            "capturedMemories": len(captured),
            "outsideWorkspaceCaptures": 0,
            "stubRequests": 1,
            "stubTerminals": 1,
            "realModelCalls": 0,
        }
    except BaseException as exc:  # Cleanup must run for every smoke-test failure.
        primary_error = exc
    finally:
        if conversation_id is not None:
            try:
                request_json(key, "DELETE", f"/api/conversations/{conversation_id}")
            except (OSError, urllib.error.HTTPError, ValueError):
                pass
        try:
            engine.run("cleanup_owned", [])
        except BaseException as exc:
            cleanup_error = exc

    if cleanup_error is not None:
        preceding = (
            "none" if primary_error is None
            else f"{type(primary_error).__name__}: {primary_error}"
        )
        raise RuntimeError(
            f"smoke cleanup failed after primary result [{preceding}]: "
            f"{type(cleanup_error).__name__}: {cleanup_error}"
        ) from cleanup_error
    if primary_error is not None:
        raise RuntimeError(f"smoke test failed: {type(primary_error).__name__}: {primary_error}") from primary_error
    print(json.dumps(outcome, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
