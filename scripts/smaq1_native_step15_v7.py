#!/usr/bin/env python3
"""Final SMA-Q1 evaluator with a deterministic, evidence-preserving model lane.

V7 preserves the V6 scientific corpus and SMA behavior. It changes only the
OpenHands evaluation control: deterministic sampling, minimum exposed tools,
the SDK's two supported terminal response forms, and retained content-free
terminal evidence on failure.
"""

from __future__ import annotations

import copy
import hashlib
import importlib.util
import json
import sys
import time
from pathlib import Path
from typing import Any


TEAMS = Path("/Users/paul/work/tekroo-ai/teams")
SMA = Path("/Users/paul/work/tekroo-ai/sma-step15")
PARENT = TEAMS / "scripts/smaq1_native_step15_v6.py"
MANIFEST = TEAMS / "investigations/sma-q1/preregistration-native-v7.json"
IDENTITY = TEAMS / "investigations/sma-q1/step-15-execution-identity-v7.json"
AUTHORIZATION = TEAMS / "investigations/sma-q1/step-15-execution-authorization-v7.json"
OUTPUT = TEAMS / "OUTPUT/phase-3/sma-q1n-step15-v7"
RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v7"
QUAL_RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v7-fixture"
MANIFEST_SHA = "3099916ff55c600b718995c7519bc595ea5a945ddbbc017767819f2897210e34"
TEMPERATURE = 0.0
MAX_ITERATIONS = 3
EXPECTED_PARTITIONS = {
    "qualification_alpha": "openhands:5d0f999538b5d938159b44aa:default",
    "qualification_beta": "openhands:5aa5a0f50e637f3230940e96:default",
    "measured_alpha": "openhands:86287853e7ebf1f56bb31ec0:default",
    "measured_beta": "openhands:7116655d5dc55abd8d940893:default",
}


def load_parent():
    spec = importlib.util.spec_from_file_location("smaq1_v7_parent", PARENT)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load sealed V6 evaluator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


inherited = load_parent()
v5 = inherited.v5
v4 = inherited.v4
support = v4.support


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def sha_text(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


def workspace_partition(path: Path) -> str:
    return "openhands:" + hashlib.sha256(str(path).encode()).hexdigest()[:24] + ":default"


def configure_v7() -> None:
    v5.__file__ = str(Path(__file__))
    v5.MANIFEST = MANIFEST
    v5.MANIFEST_SHA = MANIFEST_SHA
    v5.IDENTITY = IDENTITY
    v5.AUTHORIZATION = AUTHORIZATION
    v5.OUTPUT = OUTPUT
    v5.RUN_ROOT = RUN_ROOT
    v5.QUAL_RUN_ROOT = QUAL_RUN_ROOT
    v5.configure_inherited_runner()

    v4.__file__ = str(Path(__file__))
    v4.MANIFEST = MANIFEST
    v4.MANIFEST_SHA = MANIFEST_SHA
    v4.IDENTITY = IDENTITY
    v4.AUTHORIZATION = AUTHORIZATION
    v4.OUTPUT = OUTPUT
    v4.JOURNAL = OUTPUT / "raw-receipts.jsonl"
    v4.SUMMARY = OUTPUT / "execution-receipt.json"
    v4.RUN_ROOT = RUN_ROOT
    v4.ALPHA = RUN_ROOT / "actor-alpha"
    v4.BETA = RUN_ROOT / "actor-beta"
    v4.RUNTIME = RUN_ROOT / "runtime"
    v4.LABEL = "com.tekroo.sma-service-smaq1n-step15-v7"
    v4.DATABASE = "sma_q1n_step15_20260813_v7"
    v4.SEMANTIC = "sma_q1n_step15_v7_semantic_20260813"
    v4.EPISODIC = "sma_q1n_step15_v7_episodic_20260813"
    v4.QUAL_RUN_ROOT = QUAL_RUN_ROOT
    v4.QUAL_DATABASE = "sma_q1n_step15_v7_fixture_20260813"
    v4.QUAL_SEMANTIC = "sma_q1n_step15_v7_fixture_semantic_20260813"
    v4.QUAL_EPISODIC = "sma_q1n_step15_v7_fixture_episodic_20260813"
    v4.QUAL_LABEL = "com.tekroo.sma-service-smaq1n-step15-v7-fixture"
    v4.ALPHA_PARTITION = workspace_partition(v4.ALPHA)
    v4.BETA_PARTITION = workspace_partition(v4.BETA)
    support.SMA_LABEL = v4.LABEL
    v4.qualify_fixture = inherited.parent.qualify_fixture


configure_v7()


def terminal_response(
    events: list[dict[str, Any]],
) -> tuple[dict[str, Any], str, str] | None:
    """Mirror openhands.sdk.conversation.get_agent_final_response()."""
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
            return event, v4.event_text(event), "AGENT_MESSAGE"
    return None


def terminal_projection(events: list[dict[str, Any]]) -> dict[str, Any]:
    projected = []
    for event in events:
        action = event.get("action")
        response = ""
        if event.get("kind") == "MessageEvent" and event.get("source") == "agent":
            response = v4.event_text(event)
        elif isinstance(action, dict) and isinstance(action.get("message"), str):
            response = action["message"]
        projected.append({
            "id": event.get("id"),
            "kind": event.get("kind"),
            "source": event.get("source"),
            "tool_name": event.get("tool_name"),
            "action_kind": action.get("kind") if isinstance(action, dict) else None,
            "response_length": len(response),
            "response_sha256": sha_text(response),
        })
    return {"events": projected}


def retain_terminal_evidence(conversation: str, events: list[dict[str, Any]],
                             reason: str) -> None:
    path = OUTPUT / "openhands-terminal-evidence.json"
    if path.exists():
        return
    payload = {
        "schemaVersion": "2.0.0",
        "recordType": "OPENHANDS_TERMINAL_EVIDENCE",
        "classification": "OBSERVED",
        "conversationId": conversation,
        "reason": reason,
        "contentPolicy": "Bodies omitted; lengths and SHA-256 retained.",
        **terminal_projection(events),
    }
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")


def create_conversation(workspace: Path, parent: str | None = None,
                        client_tools: list[dict[str, Any]] | None = None,
                        initial_prompt: str | None = None) -> str:
    session_key = v4.key()
    settings = copy.deepcopy(support.settings(session_key))
    settings["tools"] = []
    settings["enable_sub_agents"] = False
    settings["enable_switch_llm_tool"] = False
    settings["mcp_config"] = {}
    settings["llm"]["temperature"] = TEMPERATURE
    payload: dict[str, Any] = {
        "agent_settings": settings,
        "secrets_encrypted": True,
        "workspace": {"kind": "LocalWorkspace", "working_dir": str(workspace)},
        "worktree": False,
        "max_iterations": MAX_ITERATIONS,
        "autotitle": False,
        "hook_config": support.hooks(session_key, str(workspace)),
    }
    if client_tools is not None:
        payload["client_tools"] = client_tools
    if initial_prompt is not None:
        payload["initial_message"] = {
            "role": "user", "content": [{"type": "text", "text": initial_prompt}],
        }
    if parent is not None:
        payload["parent_conversation_id"] = parent
    status, created = v4.http(
        "POST", support.INGRESS, "/api/conversations", session_key, payload, 90
    )
    if status not in (200, 201):
        raise RuntimeError(f"conversation creation failed: HTTP {status}")
    conversation = str(created["id"])
    status, detail = v4.http(
        "GET", support.INGRESS, f"/api/conversations/{conversation}",
        session_key, timeout=15,
    )
    if status != 200 or detail["workspace"]["working_dir"] != str(workspace):
        raise RuntimeError("created conversation identity mismatch")
    if parent is not None and str(detail.get("parent_conversation_id")) != parent:
        raise RuntimeError("child conversation parent mismatch")
    return conversation


def submit_and_wait(conversation: str, prompt: str,
                    timeout: float = v4.SCENARIO_LIMIT_SECONDS) -> dict[str, Any]:
    started_ns = time.monotonic_ns()
    status, _ = v4.http(
        "POST", support.INGRESS, f"/api/conversations/{conversation}/events", v4.key(),
        {"role": "user", "run": True, "content": [{"type": "text", "text": prompt}]}, 30,
    )
    submitted_ns = time.monotonic_ns()
    if status != 200:
        raise RuntimeError(f"prompt submission failed: HTTP {status}")
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        observed_ns = time.monotonic_ns()
        events = v4.fetch_events(conversation)
        user = next((item for item in events if item.get("kind") == "MessageEvent"
                     and item.get("source") == "user" and v4.event_text(item) == prompt), None)
        hook = next((item for item in events if item.get("kind") == "HookExecutionEvent"
                     and (item.get("hook_input") or {}).get("message") == prompt), None)
        if user is not None and hook is not None:
            later = events[events.index(user) + 1:]
            error = next((item for item in later if item.get("kind") == "ConversationErrorEvent"), None)
            if error is not None:
                retain_terminal_evidence(conversation, later, "CONVERSATION_ERROR_EVENT")
                raise RuntimeError("conversation emitted a terminal error")
            terminal = terminal_response(later)
            if terminal is not None:
                agent, answer, terminal_shape = terminal
                detail_status, detail = v4.http(
                    "GET", support.INGRESS, f"/api/conversations/{conversation}",
                    v4.key(), timeout=15,
                )
                if detail_status != 200:
                    raise RuntimeError(f"conversation detail failed: HTTP {detail_status}")
                if detail.get("execution_status") != "finished":
                    time.sleep(0.05)
                    continue
                context = hook.get("additional_context") or ""
                actions = [item for item in later[:later.index(agent) + 1]
                           if item.get("kind") == "ActionEvent"]
                user_events = [item for item in events
                               if item.get("kind") == "MessageEvent"
                               and item.get("source") == "user"]
                llm_message = agent.get("llm_message") or {}
                return {
                    "submission_ms": (submitted_ns - started_ns) / 1_000_000.0,
                    "wall_ms": (observed_ns - started_ns) / 1_000_000.0,
                    "conversation_id": conversation,
                    "user_event_id": user.get("id"),
                    "user_ordinal": user_events.index(user) + 1,
                    "hook_event_id": hook.get("id"),
                    "agent_event_id": agent.get("id"),
                    "hook_before_user": events.index(hook) < events.index(user),
                    "hook_success": bool(hook.get("success")),
                    "hook_exit_code": hook.get("exit_code"),
                    "hook_stderr_length": len(hook.get("stderr") or ""),
                    "prompt_unchanged": v4.event_text(user) == prompt,
                    "prompt_length": len(prompt),
                    "prompt_sha256": sha_text(prompt),
                    "context_length": len(context),
                    "context_sha256": sha_text(context),
                    "context": context,
                    "response_length": len(answer),
                    "response_sha256": sha_text(answer),
                    "response": answer,
                    "terminal_shape": terminal_shape,
                    "execution_status": detail.get("execution_status"),
                    "action_count": len(actions),
                    "usage": agent.get("usage"),
                    "model": llm_message.get("model"),
                    "event_parent_ids": {
                        "user": user.get("parent_id"),
                        "hook": hook.get("parent_id"),
                        "agent": agent.get("parent_id"),
                    },
                }
        time.sleep(0.1)
    events = v4.fetch_events(conversation)
    retain_terminal_evidence(conversation, events, "SCENARIO_TIMEOUT")
    raise TimeoutError(f"scenario exceeded {timeout} seconds")


v4.create_conversation = create_conversation
v4.submit_and_wait = submit_and_wait


def validate_artifacts() -> dict[str, Any]:
    manifest = json.loads(MANIFEST.read_text())
    identity = json.loads(IDENTITY.read_text())
    authorization = json.loads(AUTHORIZATION.read_text())
    runner_hash = sha_file(Path(__file__))
    checks = {
        "manifest_hash_matches": sha_file(MANIFEST) == MANIFEST_SHA,
        "manifest_corpus_unchanged": (
            manifest["unchangedBase"]["scenarioCount"] == 18
            and manifest["unchangedBase"]["repetitionCount"] == 96
        ),
        "manifest_temperature_zero": manifest["v7Amendments"]["modelLane"]["temperature"] == 0,
        "manifest_tools_empty": manifest["v7Amendments"]["modelLane"]["tools"] == [],
        "identity_manifest_matches": identity["frozenExperiment"]["manifestSHA256"] == MANIFEST_SHA,
        "identity_runner_matches": identity["harness"]["sha256"] == runner_hash,
        "authorization_manifest_matches": authorization["frozenManifestSHA256"] == MANIFEST_SHA,
        "authorization_identity_matches": authorization["executionIdentitySHA256"] == sha_file(IDENTITY),
        "authorization_runner_matches": authorization["harnessSHA256"] == runner_hash,
        "authorization_is_live": authorization["executionAuthorized"] is True,
    }
    if not all(checks.values()):
        raise RuntimeError(f"V7 artifact contract failed: {checks}")
    return {"status": "PASS", "checks": checks, "liveServiceCalls": 0}


def static_expectation_checks() -> dict[str, Any]:
    computed = {
        "qualification_alpha": workspace_partition(QUAL_RUN_ROOT / "actor-alpha"),
        "qualification_beta": workspace_partition(QUAL_RUN_ROOT / "actor-beta"),
        "measured_alpha": workspace_partition(RUN_ROOT / "actor-alpha"),
        "measured_beta": workspace_partition(RUN_ROOT / "actor-beta"),
    }
    if computed != EXPECTED_PARTITIONS:
        raise RuntimeError(f"V7 workspace partition mismatch: {computed}")
    return {
        "artifact_contract": validate_artifacts(),
        "v7_partitions": computed,
        "deterministic_temperature": TEMPERATURE,
        "nonessential_tools_removed": True,
        "terminal_forms": ["FINISH_ACTION", "AGENT_MESSAGE"],
    }


v4.static_expectation_checks = static_expectation_checks


def main() -> None:
    if "--authorization-contract-test" in sys.argv or "--static-self-test" in sys.argv:
        print(json.dumps(static_expectation_checks(), indent=2, sort_keys=True))
        return
    validate_artifacts()
    v4.main()


if __name__ == "__main__":
    main()
