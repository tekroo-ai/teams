#!/usr/bin/env python3
"""Qualify a bare, response-only local OpenHands conversation lane.

This is independent of SMA-Q1. It never starts SMA, supplies no hooks, exposes
no environment tools, and stops at the first failed fresh conversation.
"""

from __future__ import annotations

import copy
import datetime as dt
import hashlib
import json
import math
import os
import shutil
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


ROOT = Path("/Users/paul/work/tekroo-ai/teams")
RUNNER = Path(__file__)
MANIFEST = ROOT / "investigations/openhands-lane/qualification-v1.json"
AUTHORIZATION = ROOT / "investigations/openhands-lane/qualification-v1-authorization.json"
OUTPUT = ROOT / "OUTPUT/phase-3/openhands-response-lane-v1"
JOURNAL = OUTPUT / "raw-receipts.jsonl"
SUMMARY = OUTPUT / "execution-receipt.json"
WORKSPACE = ROOT / "target/openhands-response-lane-v1"
KEY_FILE = Path("/Users/paul/.openhands/agent-canvas/api-key.txt")
INGRESS = "http://127.0.0.1:8000"
AGENT_SERVER = "http://127.0.0.1:18000"
PROFILE = "qwen3.6-fast"
MODEL = "ollama_chat/qwen3.6:35b-a3b-q8_0"
PROFILE_SHA = "ad5a617ddbf10d60e26cd220e692b5e22fc22d8094453272c4bc6f95af5c610a"
MANIFEST_SHA = "9704c0b0e78023fefdeed67848bff233632cbb33a4683a7fa7c794d7199c8e7d"
PROMPT = "Respond with exactly OPENHANDS_LANE_OK. Do not inspect files. Use only the required finish mechanism."
EXPECTED = "OPENHANDS_LANE_OK"
CONVERSATIONS = 128
MAX_ITERATIONS = 8
TIMEOUT_SECONDS = 120.0
TERMINAL_STATUSES = {"finished", "paused", "error"}


def now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat()


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def sha_text(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


def canonical_sha(value: Any) -> str:
    raw = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(raw).hexdigest()


def session_key() -> str:
    value = KEY_FILE.read_text().strip()
    if not value:
        raise RuntimeError("OpenHands session key file is empty")
    return value


def http(method: str, base: str, path: str, key: str | None = None,
         body: dict[str, Any] | None = None, timeout: float = 30.0,
         expose_encrypted: bool = False) -> tuple[int, dict[str, Any]]:
    data = None if body is None else json.dumps(body).encode()
    headers = {"Content-Type": "application/json"}
    if key is not None:
        headers["X-Session-API-Key"] = key
    if expose_encrypted:
        headers["X-Expose-Secrets"] = "encrypted"
    request = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            raw = response.read()
            return response.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as error:
        raw = error.read()
        return error.code, json.loads(raw) if raw else {}


class Journal:
    def __init__(self, path: Path):
        self.path = path

    def append(self, kind: str, data: dict[str, Any]) -> None:
        record = {"kind": kind, "recorded_at": now(), **data}
        with self.path.open("a", encoding="utf-8") as stream:
            stream.write(json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n")
            stream.flush()
            os.fsync(stream.fileno())


def event_text(event: dict[str, Any]) -> str:
    message = event.get("llm_message") or {}
    return "".join(
        item.get("text", "") for item in message.get("content", [])
        if item.get("type") == "text"
    )


def action_name(event: dict[str, Any]) -> str:
    for key in ("tool_name", "action", "name"):
        value = event.get(key)
        if isinstance(value, str) and value:
            return value
    tool = event.get("tool") or event.get("tool_call") or {}
    if isinstance(tool, dict):
        for key in ("name", "tool_name"):
            value = tool.get(key)
            if isinstance(value, str) and value:
                return value
    return "UNRESOLVED_BUILTIN_ACTION"


def agent_final_response(
    events: list[dict[str, Any]],
) -> tuple[dict[str, Any], str, str] | None:
    """Mirror OpenHands get_agent_final_response() for serialized events.

    OpenHands has two valid terminal response shapes: an agent FinishAction or
    an agent MessageEvent. The finish action takes precedence when it is the
    later terminal event.
    """
    for event in reversed(events):
        if (
            event.get("kind") == "ActionEvent"
            and event.get("source") == "agent"
            and event.get("tool_name") == "finish"
        ):
            action = event.get("action")
            if isinstance(action, dict) and action.get("kind") == "FinishAction":
                message = action.get("message")
                if isinstance(message, str):
                    return event, message, "FINISH_ACTION"
            return event, "", "MALFORMED_FINISH_ACTION"
        if event.get("kind") == "MessageEvent" and event.get("source") == "agent":
            return event, event_text(event), "AGENT_MESSAGE"
    return None


def fetch_events(conversation: str, key: str) -> list[dict[str, Any]]:
    status, payload = http(
        "GET", INGRESS, f"/api/conversations/{conversation}/events/search?limit=100",
        key, timeout=15,
    )
    if status != 200:
        raise RuntimeError(f"event search failed: HTTP {status}")
    return payload.get("items", [])


def artifact_contract(require_authorized: bool) -> dict[str, Any]:
    manifest = json.loads(MANIFEST.read_text())
    authorization = json.loads(AUTHORIZATION.read_text())
    checks = {
        "manifest_hash_matches": sha_file(MANIFEST) == MANIFEST_SHA,
        "runner_hash_matches": authorization["runnerSHA256"] == sha_file(RUNNER),
        "authorization_manifest_matches": authorization["manifestSHA256"] == MANIFEST_SHA,
        "authorization_state_matches": bool(authorization["executionAuthorized"]) == require_authorized,
        "conversation_count_matches": int(manifest["fixedProbe"]["conversationCount"]) == CONVERSATIONS,
        "max_iterations_matches": int(manifest["pinnedLane"]["maxIterations"]) == MAX_ITERATIONS,
        "prompt_matches": manifest["fixedProbe"]["prompt"] == PROMPT,
        "expected_response_matches": manifest["fixedProbe"]["expectedResponse"] == EXPECTED,
        "tools_are_explicitly_empty": manifest["pinnedLane"]["agentSettingsOverrides"]["tools"] == [],
        "hooks_are_absent": manifest["pinnedLane"]["hookConfigSupplied"] is False,
        "sma_is_forbidden": (
            manifest["pinnedLane"]["smaServiceAllowed"] is False
            and manifest["pinnedLane"]["smaBridgeAllowed"] is False
        ),
    }
    if "implementationSHA256" in authorization:
        checks["implementation_hash_matches"] = (
            authorization["implementationSHA256"] == sha_file(Path(__file__))
        )
    if not all(checks.values()):
        raise RuntimeError(f"offline artifact contract failed: {checks}")
    return {"status": "PASS", "checks": checks, "live_calls": 0}


def port_open(port: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as client:
        client.settimeout(0.2)
        return client.connect_ex(("127.0.0.1", port)) == 0


def live_preflight(key: str) -> tuple[dict[str, Any], dict[str, Any]]:
    ingress_status, _ = http("GET", INGRESS, "/health", timeout=10)
    agent_status, _ = http("GET", AGENT_SERVER, "/health", timeout=10)
    profile_status, profile = http(
        "GET", AGENT_SERVER, f"/api/profiles/{PROFILE}", key, timeout=15,
    )
    if profile_status != 200:
        raise RuntimeError(f"profile returned HTTP {profile_status}")
    secret_free = copy.deepcopy(profile)
    (secret_free.get("config") or {}).pop("api_key", None)

    settings_status, settings_payload = http(
        "GET", INGRESS, "/api/settings", key, timeout=15, expose_encrypted=True,
    )
    if settings_status != 200:
        raise RuntimeError(f"settings returned HTTP {settings_status}")
    settings = copy.deepcopy(settings_payload["agent_settings"])
    original_model = settings["llm"]["model"]
    settings["tools"] = []
    settings["enable_sub_agents"] = False
    settings["enable_switch_llm_tool"] = False
    settings["mcp_config"] = {}

    search_status, search = http(
        "GET", AGENT_SERVER, "/api/conversations/search?limit=100", key, timeout=15,
    )
    if search_status != 200:
        raise RuntimeError(f"conversation search returned HTTP {search_status}")
    statuses: list[str] = []
    for item in search.get("items", []):
        status, detail = http(
            "GET", AGENT_SERVER, f"/api/conversations/{item['id']}", key, timeout=15,
        )
        if status == 200:
            statuses.append(str(detail.get("execution_status")))

    launchctl = subprocess.run(
        ["launchctl", "list"], capture_output=True, text=True, check=True, timeout=15,
    )
    sma_labels = sorted(
        line.split()[-1] for line in launchctl.stdout.splitlines()
        if "com.tekroo.sma-service-" in line
    )
    observed = {
        "ingress_health": ingress_status,
        "agent_server_health": agent_status,
        "profile_secret_free_sha256": canonical_sha(secret_free),
        "profile_matches": canonical_sha(secret_free) == PROFILE_SHA,
        "profile_api_key_set": bool(profile.get("api_key_set")),
        "model": original_model,
        "model_matches": original_model == MODEL,
        "unrelated_running_conversations": sum(value not in TERMINAL_STATUSES for value in statuses),
        "conversation_status_counts": {value: statuses.count(value) for value in sorted(set(statuses))},
        "sma_launchd_labels": sma_labels,
        "sma_bridge_port_open": port_open(8130),
        "workspace_absent": not WORKSPACE.exists(),
        "output_absent": not OUTPUT.exists(),
        "effective_overrides": {
            "tools": settings["tools"],
            "enable_sub_agents": settings["enable_sub_agents"],
            "enable_switch_llm_tool": settings["enable_switch_llm_tool"],
            "mcp_config": settings["mcp_config"],
        },
    }
    required = [
        observed["ingress_health"] == 200,
        observed["agent_server_health"] == 200,
        observed["profile_matches"],
        observed["profile_api_key_set"],
        observed["model_matches"],
        observed["unrelated_running_conversations"] == 0,
        not observed["sma_launchd_labels"],
        not observed["sma_bridge_port_open"],
        observed["workspace_absent"],
        observed["output_absent"],
    ]
    if not all(required):
        raise RuntimeError(f"live preflight identity mismatch: {observed}")
    return observed, settings


def create_conversation(key: str, settings: dict[str, Any]) -> tuple[str, float]:
    started = time.monotonic_ns()
    payload = {
        "agent_settings": copy.deepcopy(settings),
        "secrets_encrypted": True,
        "workspace": {"kind": "LocalWorkspace", "working_dir": str(WORKSPACE)},
        "worktree": False,
        "max_iterations": MAX_ITERATIONS,
        "autotitle": False,
    }
    status, created = http("POST", INGRESS, "/api/conversations", key, payload, 90)
    elapsed = (time.monotonic_ns() - started) / 1_000_000.0
    if status not in (200, 201):
        raise RuntimeError(f"conversation creation failed: HTTP {status}")
    return str(created["id"]), elapsed


def delete_conversation(conversation: str, key: str) -> dict[str, Any]:
    delete_status, _ = http(
        "DELETE", INGRESS, f"/api/conversations/{conversation}", key, timeout=30,
    )
    get_status, _ = http(
        "GET", INGRESS, f"/api/conversations/{conversation}", key, timeout=15,
    )
    return {
        "conversation_id": conversation,
        "delete_status": delete_status,
        "get_after_delete": get_status,
        "verified": delete_status == 200 and get_status == 404,
    }


def run_probe(ordinal: int, key: str, settings: dict[str, Any]) -> tuple[dict[str, Any], dict[str, Any]]:
    conversation, creation_ms = create_conversation(key, settings)
    result: dict[str, Any] | None = None
    cleanup: dict[str, Any]
    started_ns = time.monotonic_ns()
    try:
        status, _ = http(
            "POST", INGRESS, f"/api/conversations/{conversation}/events", key,
            {"role": "user", "run": True, "content": [{"type": "text", "text": PROMPT}]}, 30,
        )
        submitted_ns = time.monotonic_ns()
        if status != 200:
            raise RuntimeError(f"prompt submission failed: HTTP {status}")
        deadline = time.monotonic() + TIMEOUT_SECONDS
        while time.monotonic() < deadline:
            observed_ns = time.monotonic_ns()
            events = fetch_events(conversation, key)
            user = next((item for item in events if item.get("kind") == "MessageEvent"
                         and item.get("source") == "user" and event_text(item) == PROMPT), None)
            if user is not None:
                later = events[events.index(user) + 1:]
                hooks = [item for item in events if item.get("kind") == "HookExecutionEvent"]
                errors = [item for item in later if item.get("kind") == "ConversationErrorEvent"]
                if errors:
                    error = errors[0]
                    result = {
                        "ordinal": ordinal,
                        "conversation_id": conversation,
                        "status": "FAIL",
                        "terminal": "CONVERSATION_ERROR_EVENT",
                        "creation_ms": creation_ms,
                        "submission_ms": (submitted_ns - started_ns) / 1_000_000.0,
                        "wall_ms": (observed_ns - started_ns) / 1_000_000.0,
                        "error_event_id": error.get("id"),
                        "error_event_sha256": canonical_sha(error),
                        "hook_event_count": len(hooks),
                    }
                    break
                terminal = agent_final_response(later)
                if terminal is not None:
                    agent, response, terminal_shape = terminal
                    detail_status, detail = http(
                        "GET", INGRESS, f"/api/conversations/{conversation}", key,
                        timeout=15,
                    )
                    if detail_status != 200:
                        raise RuntimeError(
                            f"conversation detail failed: HTTP {detail_status}"
                        )
                    if detail.get("execution_status") != "finished":
                        time.sleep(0.05)
                        continue
                    actions = [item for item in later[:later.index(agent) + 1]
                               if item.get("kind") == "ActionEvent"]
                    allowed_actions = all(
                        action_name(item) in {"finish", "think"} for item in actions
                    )
                    result = {
                        "ordinal": ordinal,
                        "conversation_id": conversation,
                        "status": (
                            "PASS"
                            if response == EXPECTED and not hooks and allowed_actions
                            else "FAIL"
                        ),
                        "terminal": terminal_shape,
                        "creation_ms": creation_ms,
                        "submission_ms": (submitted_ns - started_ns) / 1_000_000.0,
                        "wall_ms": (observed_ns - started_ns) / 1_000_000.0,
                        "user_event_id": user.get("id"),
                        "agent_event_id": agent.get("id"),
                        "execution_status": detail.get("execution_status"),
                        "prompt_sha256": sha_text(PROMPT),
                        "prompt_unchanged": event_text(user) == PROMPT,
                        "response_length": len(response),
                        "response_sha256": sha_text(response),
                        "response_exact": response == EXPECTED,
                        "action_count": len(actions),
                        "action_names": [action_name(item) for item in actions],
                        "actions_are_allowed_builtins": allowed_actions,
                        "hook_event_count": len(hooks),
                    }
                    break
            time.sleep(0.1)
        if result is None:
            result = {
                "ordinal": ordinal,
                "conversation_id": conversation,
                "status": "FAIL",
                "terminal": "TIMEOUT",
                "creation_ms": creation_ms,
                "wall_ms": (time.monotonic_ns() - started_ns) / 1_000_000.0,
            }
    except Exception as error:
        result = {
            "ordinal": ordinal,
            "conversation_id": conversation,
            "status": "FAIL",
            "terminal": "HARNESS_OR_API_EXCEPTION",
            "error_type": type(error).__name__,
            "error_message_sha256": sha_text(str(error)),
        }
    finally:
        cleanup = delete_conversation(conversation, key)
    if not cleanup["verified"]:
        result["status"] = "FAIL"
        result["terminal"] = "CONVERSATION_CLEANUP_FAILURE"
    return result, cleanup


def percentile(values: list[float], percentage: float) -> float | None:
    if not values:
        return None
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * percentage) - 1)]


def execute() -> int:
    contract = artifact_contract(True)
    key = session_key()
    try:
        preflight, settings = live_preflight(key)
    except Exception as error:
        OUTPUT.mkdir(parents=True, exist_ok=False)
        summary = {
            "schema_version": "1.0.0",
            "receipt_type": "OPENHANDS_RESPONSE_LANE_QUALIFICATION",
            "recorded_at": now(),
            "status": "INCONCLUSIVE",
            "decision": "NO_GO",
            "completed_conversations": 0,
            "failure": {
                "type": type(error).__name__,
                "message_sha256": sha_text(str(error)),
                "message": str(error),
            },
            "offline_contract": contract,
        }
        SUMMARY.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n")
        print(json.dumps(summary, indent=2, sort_keys=True))
        return 2

    OUTPUT.mkdir(parents=True, exist_ok=False)
    WORKSPACE.mkdir(parents=True, exist_ok=False)
    journal = Journal(JOURNAL)
    journal.append("OFFLINE_CONTRACT", contract)
    journal.append("LIVE_PREFLIGHT", preflight)
    results: list[dict[str, Any]] = []
    cleanups: list[dict[str, Any]] = []
    try:
        for ordinal in range(1, CONVERSATIONS + 1):
            result, cleanup = run_probe(ordinal, key, settings)
            results.append(result)
            cleanups.append(cleanup)
            journal.append("CONVERSATION_RESULT", result)
            journal.append("CONVERSATION_CLEANUP", cleanup)
            if result["status"] != "PASS":
                break
    finally:
        if WORKSPACE.exists():
            shutil.rmtree(WORKSPACE)

    passed = len(results) == CONVERSATIONS and all(item["status"] == "PASS" for item in results)
    cleanup_passed = all(item["verified"] for item in cleanups) and not WORKSPACE.exists()
    if not cleanup_passed:
        passed = False
    latencies = [float(item["wall_ms"]) for item in results if "wall_ms" in item]
    summary = {
        "schema_version": "1.0.0",
        "receipt_type": "OPENHANDS_RESPONSE_LANE_QUALIFICATION",
        "recorded_at": now(),
        "manifest_sha256": MANIFEST_SHA,
        "runner_sha256": sha_file(Path(__file__)),
        "raw_journal_sha256": sha_file(JOURNAL),
        "status": "PASS" if passed else "FAIL",
        "decision": "GO_FINAL_SMA_Q1_DESIGN" if passed else "NO_GO",
        "required_conversations": CONVERSATIONS,
        "completed_conversations": len(results),
        "passed_conversations": sum(item["status"] == "PASS" for item in results),
        "failed_conversations": sum(item["status"] != "PASS" for item in results),
        "first_failure": next((item for item in results if item["status"] != "PASS"), None),
        "latency_ms": {
            "minimum": min(latencies) if latencies else None,
            "p50": percentile(latencies, 0.50),
            "p95": percentile(latencies, 0.95),
            "p99": percentile(latencies, 0.99),
            "maximum": max(latencies) if latencies else None,
        },
        "cleanup": {
            "all_conversations_deleted_and_404_verified": all(item["verified"] for item in cleanups),
            "verified_conversation_count": sum(item["verified"] for item in cleanups),
            "workspace_absent": not WORKSPACE.exists(),
        },
        "sma_q1_claim": "NONE",
    }
    SUMMARY.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n")
    journal.append("TERMINAL", {
        "status": summary["status"],
        "decision": summary["decision"],
        "summary_sha256": sha_file(SUMMARY),
    })
    print(json.dumps(summary, indent=2, sort_keys=True))
    return 0 if passed else 1


def main() -> int:
    if len(sys.argv) == 2 and sys.argv[1] == "--contract-test":
        print(json.dumps(artifact_contract(True), indent=2, sort_keys=True))
        return 0
    if len(sys.argv) != 1:
        raise SystemExit("usage: openhands_response_lane_qualification_v1.py [--contract-test]")
    return execute()


if __name__ == "__main__":
    raise SystemExit(main())
