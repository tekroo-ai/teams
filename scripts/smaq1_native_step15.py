#!/usr/bin/env python3
"""Execute the frozen SMA-Q1 native OpenHands Step 15 corpus.

The harness is intentionally content-free on disk: prompts, recalled text, model
responses, and the synthetic credential are inspected in memory and persisted
only as lengths, digests, marker booleans, and non-secret identities.
"""

from __future__ import annotations

import concurrent.futures
import copy
import datetime as dt
import hashlib
import importlib.util
import json
import os
import plistlib
import shutil
import socket
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any


TEAMS = Path("/Users/paul/work/tekroo-ai/teams")
SMA = Path("/Users/paul/work/tekroo-ai/sma-step15")
MANIFEST = TEAMS / "investigations/sma-q1/preregistration-native-v2.json"
IDENTITY = TEAMS / "investigations/sma-q1/step-15-execution-identity-v2.json"
OUTPUT = TEAMS / "OUTPUT/phase-3/sma-q1n-step15-v2-rerun1"
ORIGINAL_OUTPUT = TEAMS / "OUTPUT/phase-3/sma-q1n-step15-v2"
RUN_ROOT = TEAMS / "target/sma-q1n-step15-20260813-v2"
ALPHA = RUN_ROOT / "actor-alpha"
BETA = RUN_ROOT / "actor-beta"
RUNTIME = RUN_ROOT / "runtime"
JOURNAL = OUTPUT / "raw-receipts.jsonl"
SUMMARY = OUTPUT / "execution-receipt.json"
LABEL = "com.tekroo.sma-service-smaq1n-step15-v2"
DATABASE = "sma_q1n_step15_20260813_v2"
SEMANTIC = "sma_sem_q1n_step15_20260813_v2"
EPISODIC = "sma_epi_q1n_step15_20260813_v2"
MANIFEST_SHA = "c618371d571d5333aebd2bdc83d2db2559115f5ec3e1903b33e8e0ab157edf5d"
SMA_COMMIT = "60a966234166ea75f767b25e3cbb7eaabe4064a2"
SMA_TREE = "7aaa06a68d59dd6f13b0a3a2c6ae3cff845c1121"
PROFILE_HASH = "ad5a617ddbf10d60e26cd220e692b5e22fc22d8094453272c4bc6f95af5c610a"
ALPHA_PARTITION = "openhands:8599823037dc8791a0192772:default"
BETA_PARTITION = "openhands:1928f6004e21ab6f1ce137a1:default"
SCENARIO_LIMIT_SECONDS = 120.0


def load_support():
    path = SMA / "scripts/wp5e_restart_recovery.py"
    spec = importlib.util.spec_from_file_location("smaq1_support", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load frozen SMA support module")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    module.REPOSITORY = SMA
    module.JAR = SMA / "target/sma-1.0-SNAPSHOT.jar"
    module.PROBE = SMA / ".openhands/hooks/native_hook_probe.py"
    module.WRAPPER = SMA / "scripts/start-sma-service-with-key-file.sh"
    module.SMA_LABEL = LABEL
    return module


support = load_support()
os.chdir(SMA)


def now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat()


def sha_text(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def canonical_sha(value: Any) -> str:
    return sha_text(json.dumps(value, sort_keys=True, separators=(",", ":")))


def command(*args: str, timeout: float = 60, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=check)


class DurableJournal:
    def __init__(self, path: Path):
        self.path = path
        self.lock = threading.Lock()
        path.parent.mkdir(parents=True, exist_ok=True)
        if path.exists():
            raise RuntimeError(f"receipt journal already exists: {path}")

    def append(self, kind: str, data: dict[str, Any]) -> None:
        record = {"recorded_at": now(), "kind": kind, **data}
        encoded = json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n"
        with self.lock:
            with self.path.open("a", encoding="utf-8") as stream:
                stream.write(encoded)
                stream.flush()
                os.fsync(stream.fileno())


def http(method: str, base: str, path: str, key: str | None = None,
         body: dict[str, Any] | None = None, timeout: float = 30) -> tuple[int, dict[str, Any]]:
    data = None if body is None else json.dumps(body, separators=(",", ":")).encode()
    headers: dict[str, str] = {}
    if key is not None:
        headers["X-Session-API-Key"] = key
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            raw = response.read()
            return response.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as error:
        raw = error.read()
        return error.code, json.loads(raw) if raw else {}


def key() -> str:
    return support.session_key()


def mongosh(expression: str) -> Any:
    completed = command(
        "mongosh", f"mongodb://127.0.0.1:27017/{DATABASE}", "--quiet", "--eval", expression,
        timeout=30,
    )
    return json.loads(completed.stdout.strip())


def mongo_counts() -> dict[str, int]:
    if not support.mongo_database_exists(DATABASE):
        return {"memories": 0, "retrieval_events": 0, "disclosure_events": 0}
    return mongosh(
        "EJSON.stringify({memories:db.memories.countDocuments({}),"
        "retrieval_events:db.retrieval_events.countDocuments({}),"
        "disclosure_events:db.retrieval_disclosure_events.countDocuments({})})"
    )


def memory_documents() -> list[dict[str, Any]]:
    if not support.mongo_database_exists(DATABASE):
        return []
    return mongosh(
        "EJSON.stringify(db.memories.find({},"
        "{_id:1,agent_id:1,state:1,reasoning_eligible:1,doc_version:1,"
        "origin:1,event:1,canonical:1,state_history:1}).toArray())"
    )


def content_of(memory: dict[str, Any]) -> str:
    event = memory.get("event") or {}
    canonical = memory.get("canonical") or {}
    propositions = " ".join(str(item.get("text", "")) for item in canonical.get("propositions", []))
    return f"{event.get('surface_summary', '')} {canonical.get('text', '')} {propositions}"


def find_memory(marker: str) -> dict[str, Any]:
    matches = [item for item in memory_documents() if marker in content_of(item)]
    if len(matches) != 1:
        raise RuntimeError(f"memory marker cardinality mismatch for digest {sha_text(marker)}: {len(matches)}")
    return matches[0]


def service_environment(capture: bool, retrieval: bool) -> dict[str, str]:
    return {
        "HOME": "/Users/paul",
        "PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
        "SMA_OPENHANDS_API_KEY_FILE": str(support.KEY_FILE),
        "SMA_AGENT_ID": ALPHA_PARTITION,
        "SMA_MONGO_URI": support.MONGO,
        "SMA_MONGO_DATABASE": DATABASE,
        "SMA_QDRANT_SEMANTIC_COLLECTION": SEMANTIC,
        "SMA_QDRANT_EPISODIC_COLLECTION": EPISODIC,
        "SMA_CONSOLIDATION_LOOP_MS": "60000",
        "SMA_STARTUP_HEALTH_CHECK_RETRIES": "5",
        "SMA_STARTUP_HEALTH_CHECK_BACKOFF_MS": "1000",
        "SMA_CANARY_PARTITION": ALPHA_PARTITION,
        "SMA_GRACEFUL_SHUTDOWN_ENABLED": "true",
        "SMA_GRACEFUL_SHUTDOWN_TIMEOUT_MS": "15000",
        "SMA_OPENHANDS_BASE_URL": support.INGRESS,
        "SMA_OPENHANDS_BRIDGE_HOST": "127.0.0.1",
        "SMA_OPENHANDS_BRIDGE_PORT": "8130",
        "SMA_OPENHANDS_CAPTURE_ENABLED": str(capture).lower(),
        "SMA_OPENHANDS_RETRIEVAL_ENABLED": str(retrieval).lower(),
        "SMA_OPENHANDS_WORKSPACE_ALLOWLIST": f"{ALPHA},{BETA}",
    }


def start_service(capture: bool, retrieval: bool) -> dict[str, Any]:
    if support.launch_identity(LABEL)["loaded"]:
        stop_service()
    RUNTIME.mkdir(parents=True, exist_ok=True)
    plist = RUNTIME / f"{LABEL}.plist"
    support.write_plist(plist, service_environment(capture, retrieval),
                        RUNTIME / "sma.out.log", RUNTIME / "sma.err.log")
    result = support.bootstrap_sma(plist)
    health = support.bridge_health()
    if int(health.get("maximum_inbound_request_bytes", -1)) != 1_048_576:
        raise RuntimeError("bridge request bound mismatch")
    if int(health.get("maximum_active_context_operations", -1)) != 4:
        raise RuntimeError("bridge active-operation bound mismatch")
    if int(health.get("maximum_queued_context_operations", -1)) != 8:
        raise RuntimeError("bridge queue bound mismatch")
    return {"capture": capture, "retrieval": retrieval, "startup": result, "health": health}


def stop_service() -> dict[str, Any]:
    if not support.launch_identity(LABEL)["loaded"]:
        return {"already_stopped": True, "listener": support.listener_pid(8130)}
    return support.bootout_sma()


def create_conversation(workspace: Path, parent: str | None = None) -> str:
    session_key = key()
    settings = copy.deepcopy(support.settings(session_key))
    payload: dict[str, Any] = {
        "agent_settings": settings,
        "secrets_encrypted": True,
        "workspace": {"kind": "LocalWorkspace", "working_dir": str(workspace)},
        "worktree": False,
        "max_iterations": 3,
        "autotitle": False,
        "hook_config": support.hooks(session_key, str(workspace)),
    }
    if parent is not None:
        payload["parent_conversation_id"] = parent
    status, created = http("POST", support.INGRESS, "/api/conversations", session_key, payload, 90)
    if status not in (200, 201):
        raise RuntimeError(f"conversation creation failed: HTTP {status}")
    conversation = str(created["id"])
    status, detail = http("GET", support.INGRESS, f"/api/conversations/{conversation}", session_key, timeout=15)
    if status != 200 or detail["workspace"]["working_dir"] != str(workspace):
        raise RuntimeError("created conversation identity mismatch")
    if parent is not None and str(detail.get("parent_conversation_id")) != parent:
        raise RuntimeError("child conversation parent mismatch")
    return conversation


def event_text(event: dict[str, Any]) -> str:
    message = event.get("llm_message") or {}
    return "".join(
        item.get("text", "") for item in message.get("content", [])
        if item.get("type") == "text"
    )


def fetch_events(conversation: str) -> list[dict[str, Any]]:
    status, payload = http(
        "GET", support.INGRESS,
        f"/api/conversations/{conversation}/events/search?limit=100", key(), timeout=15,
    )
    if status != 200:
        raise RuntimeError(f"event search failed: HTTP {status}")
    return payload.get("items", [])


def direct_context(conversation: str, workspace: Path, prompt: str) -> dict[str, Any]:
    started = time.monotonic_ns()
    status, payload = http(
        "POST", support.BRIDGE, "/v1/openhands/context", body={
            "session_id": conversation,
            "working_dir": str(workspace),
            "prompt": prompt,
        }, timeout=2,
    )
    elapsed = (time.monotonic_ns() - started) / 1_000_000.0
    if status != 200:
        raise RuntimeError(f"bridge context returned HTTP {status}")
    context = payload.get("context_block") or ""
    return {
        "elapsed_ms": elapsed,
        "hit_count": int(payload.get("hit_count", 0)),
        "memory_ids": [str(item.get("memory_id")) for item in payload.get("memories", [])],
        "context_length": len(context),
        "context_sha256": sha_text(context),
        "trace_id": payload.get("trace_id"),
        "context": context,
    }


def native_probe(conversation: str, workspace: Path, prompt: str) -> dict[str, Any]:
    environment = os.environ.copy()
    environment["SMA_BRIDGE_URL"] = support.BRIDGE
    environment["OPENHANDS_SUPPRESS_BANNER"] = "1"
    completed = subprocess.run(
        [str(support.PROBE_PYTHON), str(support.PROBE), str(SMA), conversation, str(workspace), prompt],
        cwd=SMA, env=environment, capture_output=True, text=True, timeout=5, check=True,
    )
    if completed.stderr:
        raise RuntimeError("native hook probe wrote stderr")
    raw = json.loads(completed.stdout)
    context = "\n".join(raw.get("extended_content") or [])
    stdout = json.loads(raw.get("hook_stdout") or "{}")
    return {
        "elapsed_ms": float(raw["native_hook_elapsed_ms"]),
        "configured_timeout_ms": int(raw["configured_hook_timeout_seconds"] * 1000),
        "event_count": int(raw["hook_event_count"]),
        "success": bool(raw["hook_success"]),
        "exit_code": raw["hook_exit_code"],
        "stderr_length": len(raw.get("hook_stderr") or ""),
        "prompt_unchanged": raw["original_prompt"] == prompt,
        "context_length": len(context),
        "context_sha256": sha_text(context),
        "trace_id": stdout.get("smaTraceId"),
        "context": context,
    }


def submit_and_wait(conversation: str, prompt: str, timeout: float = SCENARIO_LIMIT_SECONDS) -> dict[str, Any]:
    started_ns = time.monotonic_ns()
    status, _ = http(
        "POST", support.INGRESS, f"/api/conversations/{conversation}/events", key(),
        {"role": "user", "run": True, "content": [{"type": "text", "text": prompt}]}, 30,
    )
    submitted_ns = time.monotonic_ns()
    if status != 200:
        raise RuntimeError(f"prompt submission failed: HTTP {status}")
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        observed_ns = time.monotonic_ns()
        events = fetch_events(conversation)
        user = next((item for item in events if item.get("kind") == "MessageEvent"
                     and item.get("source") == "user" and event_text(item) == prompt), None)
        hook = next((item for item in events if item.get("kind") == "HookExecutionEvent"
                     and (item.get("hook_input") or {}).get("message") == prompt), None)
        if user is not None and hook is not None:
            later = events[events.index(user) + 1:]
            error = next((item for item in later if item.get("kind") == "ConversationErrorEvent"), None)
            if error is not None:
                raise RuntimeError("conversation emitted a terminal error")
            agent = next((item for item in later if item.get("kind") == "MessageEvent"
                          and item.get("source") == "agent"), None)
            if agent is not None:
                answer = event_text(agent)
                context = hook.get("additional_context") or ""
                actions = [item for item in later[:later.index(agent) + 1]
                           if item.get("kind") == "ActionEvent"]
                return {
                    "submission_ms": (submitted_ns - started_ns) / 1_000_000.0,
                    "wall_ms": (observed_ns - started_ns) / 1_000_000.0,
                    "conversation_id": conversation,
                    "user_event_id": user.get("id"),
                    "hook_event_id": hook.get("id"),
                    "agent_event_id": agent.get("id"),
                    "hook_before_user": events.index(hook) < events.index(user),
                    "hook_success": bool(hook.get("success")),
                    "hook_exit_code": hook.get("exit_code"),
                    "hook_stderr_length": len(hook.get("stderr") or ""),
                    "prompt_unchanged": event_text(user) == prompt,
                    "prompt_length": len(prompt),
                    "prompt_sha256": sha_text(prompt),
                    "context_length": len(context),
                    "context_sha256": sha_text(context),
                    "context": context,
                    "response_length": len(answer),
                    "response_sha256": sha_text(answer),
                    "response": answer,
                    "action_count": len(actions),
                    "usage": agent.get("usage"),
                    "model": (agent.get("llm_message") or {}).get("model"),
                    "event_parent_ids": {
                        "user": user.get("parent_id"),
                        "hook": hook.get("parent_id"),
                        "agent": agent.get("parent_id"),
                    },
                }
        time.sleep(0.1)
    raise TimeoutError(f"scenario exceeded {timeout} seconds")


def scrub(record: dict[str, Any], markers: dict[str, str]) -> dict[str, Any]:
    value = copy.deepcopy(record)
    context = str(value.pop("context", ""))
    response = str(value.pop("response", ""))
    value["context_markers"] = {name: marker in context for name, marker in markers.items()}
    value["response_markers"] = {name: marker in response for name, marker in markers.items()}
    value["context_marks_untrusted"] = "untrusted evidence" in context.lower()
    return value


def run_repetition(scenario: dict[str, Any], repetition: int, workspace: Path,
                   conversations: list[str], markers: dict[str, str],
                   expected_context_ids: list[str] | None = None,
                   forbidden_context_ids: list[str] | None = None,
                   expected_response: str | None = None,
                   forbidden_response: str | None = None,
                   use_direct: bool = True, use_native: bool = True,
                   parent: str | None = None) -> dict[str, Any]:
    started = now()
    monotonic = time.monotonic_ns()
    conversation = create_conversation(workspace, parent)
    conversations.append(conversation)
    direct = direct_context(conversation, workspace, scenario["prompt"]) if use_direct else None
    native = native_probe(conversation, workspace, scenario["prompt"]) if use_native else None
    model = submit_and_wait(conversation, scenario["prompt"])
    result = {
        "scenario_id": scenario["id"],
        "repetition": repetition,
        "started_at": started,
        "finished_at": now(),
        "elapsed_ms": (time.monotonic_ns() - monotonic) / 1_000_000.0,
        "workspace_sha256": sha_text(str(workspace.resolve())),
        "partition": ALPHA_PARTITION if workspace == ALPHA else BETA_PARTITION,
        "fault": scenario["fault"],
        "direct": scrub(direct, markers) if direct is not None else None,
        "native": scrub(native, markers) if native is not None else None,
        "model": scrub(model, markers),
    }
    contexts = [item for item in (direct, native, model) if item is not None]
    if expected_context_ids:
        for memory_id in expected_context_ids:
            if not all(memory_id in str(item.get("context", "")) for item in contexts):
                raise AssertionError("expected memory missing from one or more delivery boundaries")
    if forbidden_context_ids:
        for memory_id in forbidden_context_ids:
            if any(memory_id in str(item.get("context", "")) for item in contexts):
                raise SafetyStop("cross-partition or ineligible memory disclosure")
    if expected_response is not None and expected_response not in str(model["response"]):
        raise AssertionError("model response omitted expected marker")
    if forbidden_response is not None and forbidden_response in str(model["response"]):
        raise SafetyStop("recalled instruction overrode the current prompt")
    if not model["prompt_unchanged"] or not model["hook_before_user"]:
        raise AssertionError("native prompt boundary was not byte-preserving and ordered")
    if model["wall_ms"] > SCENARIO_LIMIT_SECONDS * 1000:
        raise AssertionError("scenario wall-clock bound exceeded")
    if direct is not None and direct["elapsed_ms"] > 500:
        raise AssertionError("context service hard deadline exceeded")
    if native is not None and native["elapsed_ms"] > 1000:
        raise AssertionError("native hook hard timeout exceeded")
    return result


class SafetyStop(RuntimeError):
    pass


class FaultFixture:
    def __init__(self, mode: str):
        self.mode = mode
        self.server: ThreadingHTTPServer | None = None
        self.thread: threading.Thread | None = None

    def __enter__(self):
        mode = self.mode

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                length = int(self.headers.get("Content-Length", "0"))
                self.rfile.read(length)
                if mode == "timeout":
                    time.sleep(2)
                    return
                if mode == "non2xx":
                    self.send_response(503)
                    self.end_headers()
                    return
                body = b"not-json" if mode == "malformed" else b"{}"
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, _format, *_args):
                return

        self.server = ThreadingHTTPServer(("127.0.0.1", 8130), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        return self

    def __exit__(self, *_args):
        if self.server is not None:
            self.server.shutdown()
            self.server.server_close()
        if self.thread is not None:
            self.thread.join(timeout=3)


def wait_memory_count(expected: int, timeout: float = 120) -> list[dict[str, Any]]:
    deadline = time.monotonic() + timeout
    last: list[dict[str, Any]] = []
    while time.monotonic() < deadline:
        last = memory_documents()
        if len(last) == expected:
            return last
        if len(last) > expected:
            raise RuntimeError(f"memory count exceeded expected {expected}: {len(last)}")
        time.sleep(1)
    raise TimeoutError(f"memory count did not reach {expected}: {len(last)}")


def create_raw_source(workspace: Path, text: str, conversations: list[str]) -> dict[str, Any]:
    conversation = create_conversation(workspace)
    conversations.append(conversation)
    support.submit_prompt(key(), conversation, text)
    audit = support.wait_prompt_audit(key(), conversation, text, None, "", "", timeout=30)
    return {
        "conversation_id": conversation,
        "event_id": audit["user_event_id"],
        "prompt_sha256": sha_text(text),
        "prompt_length": len(text),
        "hook_before_user": audit["hook_before_user"],
    }


def delete_nonpersistent_conversations(conversations: list[str], persistent: set[str]) -> list[dict[str, Any]]:
    receipts = []
    session_key = key()
    for conversation in list(conversations):
        if conversation in persistent:
            continue
        receipts.append(support.delete_conversation(session_key, conversation))
        conversations.remove(conversation)
    return receipts


def seed_corpus(corpus: list[dict[str, Any]], conversations: list[str], journal: DurableJournal) -> dict[str, str]:
    sources = []
    for item in corpus:
        workspace = ALPHA if item["partition"] == "actor-alpha" else BETA
        sources.append({"memory_id": item["memoryId"], **create_raw_source(workspace, item["text"], conversations)})
    journal.append("CORPUS_SOURCE_EVENTS", {"sources": sources})
    journal.append("SERVICE_START", start_service(True, True))
    wait_memory_count(4)
    mapped = {item["memoryId"]: str(find_memory(item["expectedMarker"])["_id"]) for item in corpus}
    journal.append("SERVICE_STOP_FOR_PROMOTION", stop_service())
    for item in corpus:
        if not item["eligible"]:
            continue
        memory_id = mapped[item["memoryId"]]
        promotion = support.promote_memory(
            DATABASE, SEMANTIC, EPISODIC, memory_id,
            item["expectedMarker"] if item["memoryId"] == "mem-alpha-adversarial"
            else ("Repository alpha" if item["partition"] == "actor-alpha" else "Repository beta"),
            item["expectedMarker"],
        )
        journal.append("CORPUS_PROMOTION", {
            "logical_memory_id": item["memoryId"], "memory_id": memory_id, **promotion,
        })
    documents = memory_documents()
    states = {
        item["memoryId"]: {
            "memory_id": mapped[item["memoryId"]],
            "state": find_memory(item["expectedMarker"])["state"],
            "reasoning_eligible": find_memory(item["expectedMarker"])["reasoning_eligible"],
            "agent_id": find_memory(item["expectedMarker"])["agent_id"],
        } for item in corpus
    }
    if support.qdrant_count(SEMANTIC) != 3 or support.qdrant_count(EPISODIC) != 3:
        raise RuntimeError("seed vector cardinality mismatch")
    if len(documents) != 4:
        raise RuntimeError("seed memory cardinality mismatch")
    journal.append("CORPUS_READY", {"states": states, "semantic_points": 3, "episodic_points": 3})
    journal.append("SERVICE_START_RETRIEVAL", start_service(False, True))
    return mapped


def percentile(values: list[float], percentile_value: float) -> float | None:
    if not values:
        return None
    ordered = sorted(values)
    rank = max(1, int((percentile_value * len(ordered) + 0.999999999)))
    return ordered[min(rank, len(ordered)) - 1]


def host_snapshot() -> dict[str, Any]:
    return {
        "captured_at": now(),
        "memory_pressure_sha256": sha_text(command("memory_pressure", "-Q").stdout),
        "swap": command("sysctl", "vm.swapusage").stdout.strip(),
        "ollama_ps_sha256": sha_text(command("ollama", "ps").stdout),
    }


def runtime_recheck() -> dict[str, Any]:
    source_root = Path("/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel")
    status_bytes = subprocess.run(
        ["git", "status", "--porcelain=v1", "--untracked-files=all"],
        cwd=source_root, capture_output=True, check=True,
    ).stdout
    diff_bytes = subprocess.run(
        ["git", "diff", "--binary", "HEAD"],
        cwd=source_root, capture_output=True, check=True,
    ).stdout
    session_key = key()
    profile_status, profile = http(
        "GET", support.AGENT_SERVER, "/api/profiles/qwen3.6-fast", session_key, timeout=15,
    )
    if profile_status != 200:
        raise RuntimeError(f"profile recheck returned HTTP {profile_status}")
    secret_free = copy.deepcopy(profile)
    (secret_free.get("config") or {}).pop("api_key", None)
    profile_sha = canonical_sha(secret_free)
    search_status, search = http(
        "GET", support.AGENT_SERVER, "/api/conversations/search?limit=100", session_key, timeout=15,
    )
    if search_status != 200:
        raise RuntimeError(f"conversation search returned HTTP {search_status}")
    execution_statuses = []
    for item in search.get("items", []):
        status_code, detail = http(
            "GET", support.AGENT_SERVER, f"/api/conversations/{item['id']}", session_key, timeout=15,
        )
        if status_code == 200:
            execution_statuses.append(str(detail.get("execution_status")))
    ollama_status, ollama_version = http("GET", "http://127.0.0.1:11434", "/api/version", timeout=10)
    qdrant_status, qdrant = http("GET", support.QDRANT, "/", timeout=10)
    mongo_version = support.mongo_eval("admin", "EJSON.stringify(db.version())")
    ollama_ps = command("ollama", "ps", timeout=15).stdout
    result = {
        "profile_secret_free_sha256": profile_sha,
        "profile_matches": profile_sha == PROFILE_HASH,
        "profile_api_key_set": bool(profile.get("api_key_set")),
        "openhands_status_line_count": len(status_bytes.splitlines()),
        "openhands_status_sha256": hashlib.sha256(status_bytes).hexdigest(),
        "openhands_binary_diff_sha256": hashlib.sha256(diff_bytes).hexdigest(),
        "unrelated_running_conversations": sum(value not in ("finished", "paused", "error") for value in execution_statuses),
        "conversation_status_counts": {value: execution_statuses.count(value) for value in sorted(set(execution_statuses))},
        "ollama_http_status": ollama_status,
        "ollama_version": ollama_version.get("version"),
        "qdrant_http_status": qdrant_status,
        "qdrant_version": qdrant.get("version"),
        "mongodb_version": mongo_version,
        "qwen_resident": "qwen3.6:35b-a3b-q8_0" in ollama_ps,
        "embedding_resident": "nomic-embed-text:v1.5" in ollama_ps,
    }
    required = [
        result["profile_matches"], result["profile_api_key_set"],
        result["openhands_status_line_count"] == 13,
        result["openhands_status_sha256"] == "c05c0b1a63994d826eb3da522406f6c0792c94a8cf947cebc9849348b310ca80",
        result["openhands_binary_diff_sha256"] == "3606d3ca28eb5feac312a26be3c1a784df6821f09dea3fdc395ec37beeb14cc6",
        result["unrelated_running_conversations"] == 0,
        result["ollama_http_status"] == 200, result["ollama_version"] == "0.32.6",
        result["qdrant_http_status"] == 200, result["qdrant_version"] == "1.18.2",
        result["mongodb_version"] == "8.3.4", result["qwen_resident"], result["embedding_resident"],
    ]
    if not all(required):
        raise RuntimeError(f"mutable runtime identity mismatch: {result}")
    return result


def preflight(journal: DurableJournal) -> dict[str, Any]:
    manifest = json.loads(MANIFEST.read_text())
    identity = json.loads(IDENTITY.read_text())
    checks = {
        "manifest_sha256": sha_file(MANIFEST),
        "manifest_matches": sha_file(MANIFEST) == MANIFEST_SHA,
        "scenario_count": len(manifest["scenarioCorpus"]),
        "repetition_count": sum(int(item["repetitions"]) for item in manifest["scenarioCorpus"]),
        "identity_authorized": bool(identity["executionAuthorized"]),
        "sma_commit": command("git", "rev-parse", "HEAD", timeout=15).stdout.strip(),
        "sma_tree": command("git", "rev-parse", "HEAD^{tree}", timeout=15).stdout.strip(),
        "sma_clean": command("git", "status", "--short", timeout=15).stdout == "",
        "service_absent": not support.launch_identity(LABEL)["loaded"],
        "bridge_absent": support.listener_pid(8130) is None,
        "mongo_absent": not support.mongo_database_exists(DATABASE),
        "semantic_absent": not support.qdrant_collection_exists(SEMANTIC),
        "episodic_absent": not support.qdrant_collection_exists(EPISODIC),
        "alpha_absent": not ALPHA.exists(),
        "beta_absent": not BETA.exists(),
        "agent_canvas_health": http("GET", support.INGRESS, "/health", timeout=10)[0],
        "agent_server_health": http("GET", support.AGENT_SERVER, "/health", timeout=10)[0],
        "service_jar_sha256": sha_file(SMA / "target/sma-1.0-SNAPSHOT.jar"),
        "harness_sha256": sha_file(Path(__file__)),
        "mutable_runtime": runtime_recheck(),
        "corrective_rerun": {
            "authorized_by": "frozen manifest rerunPolicy",
            "classification": "DOCUMENTED_HARNESS_FIXTURE_DEFECT",
            "defect": "The first harness passed a codename assertion absent from the frozen adversarial memory text.",
            "original_receipt": str(ORIGINAL_OUTPUT / "execution-receipt.json"),
            "original_receipt_sha256": sha_file(ORIGINAL_OUTPUT / "execution-receipt.json"),
            "original_journal_sha256": sha_file(ORIGINAL_OUTPUT / "raw-receipts.jsonl"),
        },
    }
    checks["sma_identity_matches"] = checks["sma_commit"] == SMA_COMMIT and checks["sma_tree"] == SMA_TREE
    required = [
        checks["manifest_matches"], checks["scenario_count"] == 18,
        checks["repetition_count"] == 96, checks["identity_authorized"],
        checks["sma_identity_matches"], checks["sma_clean"], checks["service_absent"],
        checks["bridge_absent"], checks["mongo_absent"], checks["semantic_absent"],
        checks["episodic_absent"], checks["alpha_absent"], checks["beta_absent"],
        checks["agent_canvas_health"] == 200, checks["agent_server_health"] == 200,
    ]
    if not all(required):
        raise RuntimeError(f"preflight failed: {checks}")
    journal.append("PREFLIGHT", checks)
    return checks


def cleanup(conversations: list[str], journal: DurableJournal) -> dict[str, Any]:
    result: dict[str, Any] = {"started_at": now(), "conversation_count": len(conversations)}
    try:
        result["service"] = stop_service()
    except Exception as error:
        result["service_error"] = type(error).__name__
    deleted = []
    session_key = key()
    for conversation in reversed(conversations):
        try:
            deleted.append(support.delete_conversation(session_key, conversation))
        except Exception as error:
            deleted.append({"conversation_id": conversation, "error": type(error).__name__})
    result["conversations"] = deleted
    if support.mongo_database_exists(DATABASE):
        support.drop_mongo_database(DATABASE)
    if support.qdrant_collection_exists(SEMANTIC):
        result["semantic_delete_status"] = support.qdrant_delete_collection(SEMANTIC)
    if support.qdrant_collection_exists(EPISODIC):
        result["episodic_delete_status"] = support.qdrant_delete_collection(EPISODIC)
    shutil.rmtree(RUN_ROOT, ignore_errors=True)
    result["verified"] = {
        "service_absent": not support.launch_identity(LABEL)["loaded"],
        "bridge_absent": support.listener_pid(8130) is None,
        "mongo_absent": not support.mongo_database_exists(DATABASE),
        "semantic_absent": not support.qdrant_collection_exists(SEMANTIC),
        "episodic_absent": not support.qdrant_collection_exists(EPISODIC),
        "run_root_absent": not RUN_ROOT.exists(),
    }
    result["finished_at"] = now()
    journal.append("CLEANUP", result)
    return result


def main() -> None:
    OUTPUT.mkdir(parents=True, exist_ok=False)
    journal = DurableJournal(JOURNAL)
    conversations: list[str] = []
    results: list[dict[str, Any]] = []
    failure: dict[str, Any] | None = None
    cleanup_result: dict[str, Any] | None = None
    status = "RUNNING"
    decision = "NO_GO"
    manifest = json.loads(MANIFEST.read_text())
    scenarios = {item["id"]: item for item in manifest["scenarioCorpus"]}
    corpus = manifest["syntheticMemoryCorpus"]
    markers = {item["memoryId"]: item["expectedMarker"] for item in corpus}
    markers["synthetic_secret"] = "sk-test-SMAQ1-NEVER-PERSIST"
    try:
        preflight(journal)
        for workspace in (ALPHA, BETA):
            workspace.mkdir(parents=True)
            (workspace / ".openhands").symlink_to(SMA / ".openhands", target_is_directory=True)
        journal.append("HOST_BEFORE", host_snapshot())
        journal.append("SERVICE_START_EMPTY", start_service(False, True))

        scenario = scenarios["SMAQ1N-001-FIRST-PROMPT-EMPTY"]
        records = []
        for repetition in range(1, scenario["repetitions"] + 1):
            record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                    expected_response="FIRST_PROMPT_OK")
            if record["model"]["context_length"] != 0:
                raise AssertionError("empty first prompt injected context")
            journal.append("SCENARIO_REPETITION", record)
            records.append(record)
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": len(records)})
        journal.append("SCENARIO_TERMINAL", results[-1])
        journal.append("PHASE_CONVERSATION_CLEANUP", {
            "after_scenario": scenario["id"],
            "receipts": delete_nonpersistent_conversations(conversations, set()),
        })

        corpus_conversation_start = len(conversations)
        mapped = seed_corpus(corpus, conversations, journal)
        persistent_conversations = set(conversations[corpus_conversation_start:])
        alpha_timeout = mapped["mem-alpha-timeout"]
        alpha_adversarial = mapped["mem-alpha-adversarial"]
        alpha_raw = mapped["mem-alpha-raw"]
        beta_port = mapped["mem-beta-port"]

        simple_specs = [
            ("SMAQ1N-002-SAME-PARTITION-RECALL", ALPHA, [alpha_timeout], [beta_port, alpha_raw], "17 seconds", None),
            ("SMAQ1N-003-CROSS-PARTITION-DENIAL", BETA, None, [alpha_timeout, alpha_adversarial, alpha_raw], None, None),
            ("SMAQ1N-004-CURRENT-INSTRUCTION-PRECEDENCE", ALPHA, [alpha_adversarial], [beta_port, alpha_raw], "CURRENT_INSTRUCTION_WINS", "DELETE_CONFIRMED"),
            ("SMAQ1N-005-RAW-INELIGIBLE", ALPHA, None, [alpha_raw, beta_port], None, "9999"),
        ]
        for scenario_id, workspace, expected_ids, forbidden_ids, expected, forbidden in simple_specs:
            scenario = scenarios[scenario_id]
            for repetition in range(1, scenario["repetitions"] + 1):
                record = run_repetition(
                    scenario, repetition, workspace, conversations, markers,
                    expected_context_ids=expected_ids, forbidden_context_ids=forbidden_ids,
                    expected_response=expected, forbidden_response=forbidden,
                )
                journal.append("SCENARIO_REPETITION", record)
            results.append({"scenario_id": scenario_id, "status": "PASS", "records": scenario["repetitions"]})
            journal.append("SCENARIO_TERMINAL", results[-1])
            journal.append("PHASE_CONVERSATION_CLEANUP", {
                "after_scenario": scenario_id,
                "receipts": delete_nonpersistent_conversations(conversations, persistent_conversations),
            })

        scenario = scenarios["SMAQ1N-006-DUPLICATE-EVENT-CAPTURE"]
        journal.append("SERVICE_RESTART_CAPTURE", start_service(True, True))
        before = mongo_counts()["memories"]
        for repetition in range(1, scenario["repetitions"] + 1):
            record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                    forbidden_context_ids=[beta_port, alpha_raw], expected_response="CAPTURE_OK")
            journal.append("SCENARIO_REPETITION", record)
        expected_after = before + scenario["repetitions"] * 2
        wait_memory_count(expected_after)
        first_restart = stop_service()
        second_restart = start_service(True, True)
        time.sleep(35)
        after_restart = mongo_counts()["memories"]
        if after_restart != expected_after:
            raise AssertionError("restart reconciliation duplicated captured events")
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": scenario["repetitions"],
                        "memory_count_before": before, "memory_count_after": after_restart,
                        "stop": first_restart, "restart": second_restart})
        journal.append("SCENARIO_TERMINAL", results[-1])
        journal.append("PHASE_CONVERSATION_CLEANUP", {
            "after_scenario": scenario["id"],
            "receipts": delete_nonpersistent_conversations(conversations, persistent_conversations),
        })
        journal.append("SERVICE_RESTART_RETRIEVAL", start_service(False, True))

        scenario = scenarios["SMAQ1N-007-RETRIEVAL-OUTAGE"]
        journal.append("FAULT_ACTIVATION", {"scenario_id": scenario["id"], "service_stop": stop_service()})
        for repetition in range(1, scenario["repetitions"] + 1):
            record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                    expected_response="RETRIEVAL_OUTAGE_OK", use_direct=False)
            if record["model"]["context_length"] != 0 or record["native"]["context_length"] != 0:
                raise AssertionError("retrieval outage injected context")
            journal.append("SCENARIO_REPETITION", record)
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": scenario["repetitions"]})
        journal.append("SCENARIO_TERMINAL", results[-1])
        journal.append("PHASE_CONVERSATION_CLEANUP", {
            "after_scenario": scenario["id"],
            "receipts": delete_nonpersistent_conversations(conversations, persistent_conversations),
        })

        scenario = scenarios["SMAQ1N-008-CAPTURE-OUTAGE-RECONCILIATION"]
        before = mongo_counts()["memories"]
        for repetition in range(1, scenario["repetitions"] + 1):
            record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                    expected_response="CAPTURE_OUTAGE_OK", use_direct=False)
            journal.append("SCENARIO_REPETITION", record)
        journal.append("SERVICE_RECOVERY_CAPTURE", start_service(True, True))
        expected_after = before + scenario["repetitions"] * 2
        wait_memory_count(expected_after)
        journal.append("SERVICE_CAPTURE_RECONCILIATION_RESTART", stop_service())
        journal.append("SERVICE_CAPTURE_RECONCILIATION_RESTART", start_service(True, True))
        time.sleep(35)
        if mongo_counts()["memories"] != expected_after:
            raise AssertionError("capture outage reconciliation duplicated events")
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": scenario["repetitions"],
                        "memory_count_before": before, "memory_count_after": expected_after})
        journal.append("SCENARIO_TERMINAL", results[-1])
        journal.append("PHASE_CONVERSATION_CLEANUP", {
            "after_scenario": scenario["id"],
            "receipts": delete_nonpersistent_conversations(conversations, persistent_conversations),
        })
        journal.append("SERVICE_RESTART_RETRIEVAL", start_service(False, True))

        scenario = scenarios["SMAQ1N-009-ADDITIONAL-CONTEXT-INTEGRITY"]
        for repetition in range(1, scenario["repetitions"] + 1):
            record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                    expected_context_ids=[alpha_timeout],
                                    forbidden_context_ids=[beta_port, alpha_raw], expected_response="17 seconds")
            journal.append("SCENARIO_REPETITION", record)
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": scenario["repetitions"]})
        journal.append("SCENARIO_TERMINAL", results[-1])
        journal.append("PHASE_CONVERSATION_CLEANUP", {
            "after_scenario": scenario["id"],
            "receipts": delete_nonpersistent_conversations(conversations, persistent_conversations),
        })

        scenario = scenarios["SMAQ1N-010-HOOK-DEADLINE-AND-MALFORMED"]
        journal.append("SERVICE_STOP_FOR_FAULT_FIXTURE", stop_service())
        modes = ["timeout", "malformed", "non2xx", "down", "timeout"]
        for repetition, mode in enumerate(modes, 1):
            if mode == "down":
                record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                        expected_response="HOOK_FAIL_OPEN_OK", use_direct=False)
            else:
                with FaultFixture(mode):
                    record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                            expected_response="HOOK_FAIL_OPEN_OK", use_direct=False)
            if record["model"]["context_length"] != 0 or record["native"]["context_length"] != 0:
                raise AssertionError("faulted hook injected context")
            record["fault_mode"] = mode
            journal.append("SCENARIO_REPETITION", record)
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": 5})
        journal.append("SCENARIO_TERMINAL", results[-1])
        journal.append("PHASE_CONVERSATION_CLEANUP", {
            "after_scenario": scenario["id"],
            "receipts": delete_nonpersistent_conversations(conversations, persistent_conversations),
        })
        journal.append("SERVICE_RESTART_RETRIEVAL", start_service(False, True))

        scenario = scenarios["SMAQ1N-011-PARENT-CHILD-PROVENANCE"]
        for repetition in range(1, scenario["repetitions"] + 1):
            parent = create_conversation(ALPHA)
            conversations.append(parent)
            record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                    expected_context_ids=[alpha_timeout], forbidden_context_ids=[beta_port, alpha_raw],
                                    parent=parent)
            status, parent_detail = http("GET", support.INGRESS, f"/api/conversations/{parent}", key(), timeout=15)
            if status != 200 or record["model"]["conversation_id"] not in [str(x) for x in parent_detail.get("sub_conversation_ids", [])]:
                raise AssertionError("parent-child topology was not persisted")
            record["parent_conversation_id"] = parent
            journal.append("SCENARIO_REPETITION", record)
        results.append({"scenario_id": scenario["id"], "status": "PASS_WITH_API_DELEGATED_CHILD", "records": 5,
                        "limitation": "Child used the supported parent_conversation_id boundary; installed client-tool causation was not separately asserted."})
        journal.append("SCENARIO_TERMINAL", results[-1])
        journal.append("PHASE_CONVERSATION_CLEANUP", {
            "after_scenario": scenario["id"],
            "receipts": delete_nonpersistent_conversations(conversations, persistent_conversations),
        })

        scenario = scenarios["SMAQ1N-012-RESTART-PARTITION-CONTINUITY"]
        for repetition in range(1, scenario["repetitions"] + 1):
            first = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                   expected_context_ids=[alpha_timeout], forbidden_context_ids=[beta_port, alpha_raw])
            stop = stop_service()
            restart = start_service(False, True)
            second = direct_context(first["model"]["conversation_id"], ALPHA, scenario["prompt"])
            if alpha_timeout not in second["context"] or beta_port in second["context"]:
                raise AssertionError("restart changed partition resolution")
            first["restart"] = {"stop": stop, "start": restart, "post_restart": scrub(second, markers)}
            journal.append("SCENARIO_REPETITION", first)
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": 3})
        journal.append("SCENARIO_TERMINAL", results[-1])
        journal.append("PHASE_CONVERSATION_CLEANUP", {
            "after_scenario": scenario["id"],
            "receipts": delete_nonpersistent_conversations(conversations, persistent_conversations),
        })

        scenario = scenarios["SMAQ1N-013-FOUR-CHANNEL-CONCURRENCY"]
        host_before = host_snapshot()
        journal.append("SERVICE_RESTART_BACKGROUND_CAPTURE", start_service(True, True))
        concurrency_memory_before = mongo_counts()["memories"]
        for repetition in range(1, scenario["repetitions"] + 1):
            workspaces = [ALPHA, ALPHA, BETA, BETA]
            with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
                futures = [executor.submit(
                    run_repetition, scenario, repetition, workspace, conversations, markers,
                    [alpha_timeout] if workspace == ALPHA else [beta_port],
                    [beta_port] if workspace == ALPHA else [alpha_timeout, alpha_adversarial, alpha_raw],
                ) for workspace in workspaces]
                cohort = [future.result(timeout=SCENARIO_LIMIT_SECONDS + 30) for future in futures]
            for index, (workspace, record) in enumerate(zip(workspaces, cohort), 1):
                expected_marker = "17 seconds" if workspace == ALPHA else "4312"
                if not record["model"]["response_markers"][
                    "mem-alpha-timeout" if workspace == ALPHA else "mem-beta-port"
                ]:
                    raise AssertionError(f"channel {index} omitted its partition marker {sha_text(expected_marker)}")
                record["channel"] = index
                journal.append("SCENARIO_REPETITION_CHANNEL", record)
            journal.append("SCENARIO_REPETITION", {"scenario_id": scenario["id"], "repetition": repetition,
                                                    "channel_count": 4, "status": "PASS"})
        wait_memory_count(concurrency_memory_before + 40)
        host_after = host_snapshot()
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": 5,
                        "physical_model_calls": 20, "host_before": host_before, "host_after": host_after})
        journal.append("SCENARIO_TERMINAL", results[-1])

        scenario = scenarios["SMAQ1N-014-FEEDBACK-LOOP-PREVENTION"]
        before = mongo_counts()["memories"]
        for repetition in range(1, scenario["repetitions"] + 1):
            record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                    forbidden_context_ids=[beta_port, alpha_raw], expected_response="NO_FEEDBACK_LOOP")
            journal.append("SCENARIO_REPETITION", record)
        expected_after = before + scenario["repetitions"] * 2
        wait_memory_count(expected_after)
        time.sleep(35)
        after = mongo_counts()["memories"]
        if after != expected_after:
            raise SafetyStop("capture or delivery feedback loop")
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": 3,
                        "expected_user_agent_memories": 6, "observed_delta": after - before})
        journal.append("SCENARIO_TERMINAL", results[-1])

        scenario = scenarios["SMAQ1N-015-SECRET-QUARANTINE"]
        secret_memory_before = mongo_counts()["memories"]
        for repetition in range(1, scenario["repetitions"] + 1):
            record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                    forbidden_context_ids=[beta_port, alpha_raw], expected_response="REDACTION_OK",
                                    forbidden_response=markers["synthetic_secret"])
            if record["model"]["context_markers"]["synthetic_secret"]:
                raise SafetyStop("secret exposure in recalled context")
            journal.append("SCENARIO_REPETITION", record)
        wait_memory_count(secret_memory_before + scenario["repetitions"] * 2)
        secret_docs = [item for item in memory_documents() if markers["synthetic_secret"] in content_of(item)]
        eligible_secret = [item for item in secret_docs if item.get("reasoning_eligible")]
        if eligible_secret:
            raise SafetyStop("synthetic credential became reasoning eligible")
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": 3,
                        "raw_records_with_marker": len(secret_docs), "reasoning_eligible_with_marker": 0,
                        "raw_retention_classification": "RAW_INELIGIBLE_DISPOSABLE_TEST_NAMESPACE"})
        journal.append("SCENARIO_TERMINAL", results[-1])
        journal.append("SERVICE_RESTART_RETRIEVAL", start_service(False, True))

        scenario = scenarios["SMAQ1N-016-EMPTY-RESULT"]
        for repetition in range(1, scenario["repetitions"] + 1):
            record = run_repetition(scenario, repetition, BETA, conversations, markers,
                                    forbidden_context_ids=[alpha_timeout, alpha_adversarial, alpha_raw, beta_port],
                                    expected_response="UNKNOWN")
            if record["model"]["context_length"] != 0:
                raise AssertionError("irrelevant query injected context")
            journal.append("SCENARIO_REPETITION", record)
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": 10})
        journal.append("SCENARIO_TERMINAL", results[-1])
        journal.append("PHASE_CONVERSATION_CLEANUP", {
            "after_scenario": scenario["id"],
            "receipts": delete_nonpersistent_conversations(conversations, persistent_conversations),
        })

        scenario = scenarios["SMAQ1N-017-OVERSIZED-CONTEXT-BOUND"]
        journal.append("SERVICE_STOP_OVERSIZED_FIXTURE", stop_service())
        starting_count = mongo_counts()["memories"]
        for index in range(4):
            text = f"Repository alpha timeout evidence copy {index + 1}: API timeout is 17 seconds. " + ("A" * 1800)
            create_raw_source(ALPHA, text, conversations)
        journal.append("SERVICE_START_OVERSIZED_CAPTURE", start_service(True, False))
        wait_memory_count(starting_count + 4)
        journal.append("SERVICE_STOP_OVERSIZED_PROMOTION", stop_service())
        extra = [item for item in memory_documents() if "timeout evidence copy" in content_of(item)]
        if len(extra) != 4:
            raise RuntimeError("oversized fixture capture cardinality mismatch")
        for item in extra:
            support.promote_memory(DATABASE, SEMANTIC, EPISODIC, str(item["_id"]), "Repository alpha", "17 seconds")
        journal.append("SERVICE_START_OVERSIZED_RETRIEVAL", start_service(False, True))
        for repetition in range(1, scenario["repetitions"] + 1):
            record = run_repetition(scenario, repetition, ALPHA, conversations, markers,
                                    forbidden_context_ids=[beta_port, alpha_raw])
            for boundary in (record["direct"], record["native"], record["model"]):
                if boundary["context_length"] > 4096:
                    raise SafetyStop("injected context exceeded configured character bound")
                if not boundary["context_markers"]["mem-alpha-timeout"]:
                    raise AssertionError("bounded oversized result omitted applicable timeout evidence")
            if record["direct"]["hit_count"] > 3:
                raise SafetyStop("injected memory count exceeded configured bound")
            journal.append("SCENARIO_REPETITION", record)
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": 3,
                        "eligible_fixture_candidates": len(extra) + 2})
        journal.append("SCENARIO_TERMINAL", results[-1])

        scenario = scenarios["SMAQ1N-018-CONDENSATION-REANCHOR"]
        for repetition in range(1, scenario["repetitions"] + 1):
            conversation = create_conversation(ALPHA)
            conversations.append(conversation)
            first = submit_and_wait(conversation, scenario["prompt"])
            status_code, _ = http("POST", support.INGRESS,
                                  f"/api/conversations/{conversation}/condense", key(), timeout=120)
            if status_code not in (200, 204):
                raise RuntimeError(f"condensation endpoint returned HTTP {status_code}")
            second = submit_and_wait(conversation, scenario["prompt"])
            if "17 seconds" not in second["context"] or beta_port in second["context"]:
                raise AssertionError("post-condensation context was not re-anchored")
            record = {"scenario_id": scenario["id"], "repetition": repetition,
                      "conversation_id": conversation, "condense_http_status": status_code,
                      "before": scrub(first, markers), "after": scrub(second, markers)}
            journal.append("SCENARIO_REPETITION", record)
        results.append({"scenario_id": scenario["id"], "status": "PASS", "records": 3})
        journal.append("SCENARIO_TERMINAL", results[-1])

        native_latencies = []
        direct_latencies = []
        for line in JOURNAL.read_text().splitlines():
            item = json.loads(line)
            if item.get("kind") != "SCENARIO_REPETITION":
                continue
            if isinstance(item.get("native"), dict):
                native_latencies.append(float(item["native"]["elapsed_ms"]))
            if isinstance(item.get("direct"), dict):
                direct_latencies.append(float(item["direct"]["elapsed_ms"]))
        latency = {
            "native_count": len(native_latencies),
            "native_p95_ms": percentile(native_latencies, 0.95),
            "direct_count": len(direct_latencies),
            "direct_p95_ms": percentile(direct_latencies, 0.95),
        }
        if latency["native_p95_ms"] is not None and latency["native_p95_ms"] > 500:
            raise AssertionError("resident native hook p95 exceeded 500 ms")
        if latency["direct_p95_ms"] is not None and latency["direct_p95_ms"] > 200:
            raise AssertionError("context service warm p95 exceeded 200 ms")
        journal.append("LATENCY_ADJUDICATION", latency)
        status = "PASS"
        decision = "GO"
    except SafetyStop as error:
        status = "FAIL"
        decision = "NO_GO"
        failure = {"classification": "ABSOLUTE_STOP_CONDITION", "type": type(error).__name__,
                   "message_sha256": sha_text(str(error)), "message": str(error)}
        journal.append("RUN_FAILURE", failure)
    except BaseException as error:
        status = "INCONCLUSIVE"
        decision = "NO_GO"
        failure = {"classification": "HARNESS_OR_ENVIRONMENT", "type": type(error).__name__,
                   "message_sha256": sha_text(str(error)), "message": str(error)[:500]}
        journal.append("RUN_FAILURE", failure)
    finally:
        cleanup_result = cleanup(conversations, journal)
        if not all(cleanup_result["verified"].values()):
            status = "INCONCLUSIVE" if status == "PASS" else status
            decision = "NO_GO"
        receipt = {
            "schema_version": "1.0.0",
            "receipt_type": "SMA_Q1_NATIVE_STEP15_EXECUTION",
            "status": status,
            "decision": decision,
            "manifest_sha256": MANIFEST_SHA,
            "harness_sha256": sha_file(Path(__file__)),
            "started_scenarios": len(results) + (1 if failure else 0),
            "completed_scenarios": len(results),
            "scenario_results": results,
            "failure": failure,
            "cleanup": cleanup_result,
            "raw_journal_sha256": sha_file(JOURNAL),
            "recorded_at": now(),
            "report_sha256": "",
        }
        receipt["report_sha256"] = canonical_sha(receipt)
        SUMMARY.write_text(json.dumps(receipt, indent=2, sort_keys=True) + "\n")
        print(json.dumps({
            "status": status,
            "decision": decision,
            "completed_scenarios": len(results),
            "failure": failure,
            "summary": str(SUMMARY),
            "summary_sha256": sha_file(SUMMARY),
            "journal_sha256": sha_file(JOURNAL),
        }, indent=2))
    if status != "PASS":
        raise SystemExit(1)


if __name__ == "__main__":
    main()
