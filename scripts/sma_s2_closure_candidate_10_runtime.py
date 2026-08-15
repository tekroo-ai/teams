#!/usr/bin/env python3
"""Raw-port runtime for SMA-S2 closure candidate 10.

The orchestration in this module is frozen above six raw boundaries: HTTP,
process, store, filesystem, Maven, and monotonic clock.  Raw ports return no
scientific predicate, context-validity, provenance-completeness, or capture-
cardinality decisions.  H0 replaces only these raw ports.
"""

from __future__ import annotations

import base64
import concurrent.futures
import json
import os
import re
import shutil
import subprocess
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Protocol

import sma_s2_closure_candidate_10 as c


def raw_receipt_value(value: Any) -> Any:
    if value is None or isinstance(value, (str, int, float, bool)):
        return value
    if isinstance(value, bytes):
        return {"byteLength": len(value), "sha256": c.sha_bytes(value)}
    if isinstance(value, (list, tuple)):
        return [raw_receipt_value(item) for item in value]
    if isinstance(value, dict) and all(isinstance(key, str) for key in value):
        return {key: raw_receipt_value(item) for key, item in value.items()}
    raise c.HarnessFailure(f"noncanonical raw port receipt: {type(value).__name__}")


def retained_raw_receipt_value(value: Any) -> Any:
    """Canonical, replayable receipt with the preregistered secret redacted."""
    if value is None or isinstance(value, (int, float, bool)):
        return value
    if isinstance(value, str):
        encoded = value.encode()
        if c.SECRET in value:
            return {"stringLength": len(encoded), "sha256": c.sha_bytes(encoded),
                    "secretRedacted": True}
        return value
    if isinstance(value, bytes):
        receipt = {"byteLength": len(value), "sha256": c.sha_bytes(value)}
        if c.SECRET.encode() in value:
            receipt["secretRedacted"] = True
        else:
            receipt["base64"] = base64.b64encode(value).decode()
        return receipt
    if isinstance(value, (list, tuple)):
        return [retained_raw_receipt_value(item) for item in value]
    if isinstance(value, dict) and all(isinstance(key, str) for key in value):
        return {key: retained_raw_receipt_value(item) for key, item in value.items()}
    raise c.HarnessFailure(f"noncanonical retained raw receipt: {type(value).__name__}")


class RawBoundaryPorts(Protocol):
    live: bool

    def http_request(self, method: str, endpoint: str, path: str,
                     headers: dict[str, str], body: bytes,
                     timeout_ms: int) -> dict[str, Any]: ...
    def process_action(self, action: str, argv: list[str], cwd: str | None,
                       env: dict[str, str], timeout_ms: int) -> dict[str, Any]: ...
    def store_query(self, store: str, namespace: str,
                    query: dict[str, Any], timeout_ms: int) -> dict[str, Any]: ...
    def filesystem_action(self, action: str, path: str,
                          options: dict[str, Any]) -> dict[str, Any]: ...
    def maven_subprocess(self, argv: list[str], cwd: str,
                         timeout_ms: int) -> dict[str, Any]: ...
    def monotonic_ns(self) -> int: ...
    def sleep_ns(self, duration_ns: int) -> None: ...


class LocalRawPorts:
    """Concrete local raw I/O adapter; inert until a live fence authorizes its caller.

    Execution identity must bind every endpoint and argv prefix.  Store helpers
    receive the canonical query on stdin and must return a JSON object with a
    `documents` array.  No shell is used.
    """

    live = True

    def __init__(self, *, endpoints: dict[str, str],
                 process_commands: dict[str, list[str]],
                 store_commands: dict[str, list[str]],
                 session_key_file: str) -> None:
        self.endpoints = {key: value.rstrip("/") for key, value in endpoints.items()}
        self.process_commands = {key: list(value) for key, value in process_commands.items()}
        self.store_commands = {key: list(value) for key, value in store_commands.items()}
        self.session_key_file = Path(session_key_file)

    @staticmethod
    def _subprocess_receipt(completed: subprocess.CompletedProcess[bytes], argv: list[str],
                            cwd: str | None, started: int, completed_ns: int) -> dict[str, Any]:
        return {"argv": argv, "cwd": cwd, "exitStatus": completed.returncode,
                "stdout": completed.stdout, "stderr": completed.stderr,
                "startedMonotonicNs": started, "completedMonotonicNs": completed_ns}

    def http_request(self, method: str, endpoint: str, path: str,
                     headers: dict[str, str], body: bytes,
                     timeout_ms: int) -> dict[str, Any]:
        if endpoint not in self.endpoints:
            raise c.EnvironmentFailure(f"unbound HTTP endpoint: {endpoint}")
        request_headers = dict(headers)
        if endpoint == "OPENHANDS":
            try:
                session_key = self.session_key_file.read_text().strip()
            except OSError as error:
                raise c.EnvironmentFailure("OpenHands session key file unavailable") from error
            if not session_key:
                raise c.EnvironmentFailure("OpenHands session key file empty")
            request_headers["X-Session-API-Key"] = session_key
        if body:
            request_headers.setdefault("Content-Type", "application/json")
        request = urllib.request.Request(self.endpoints[endpoint] + path, data=body or None,
                                         headers=request_headers, method=method)
        started = time.monotonic_ns()
        try:
            with urllib.request.urlopen(request, timeout=timeout_ms / 1000) as response:
                response_body = response.read()
                status = int(response.status)
                response_headers = [[key.lower(), value] for key, value in response.headers.items()]
        except urllib.error.HTTPError as error:
            response_body = error.read()
            status = int(error.code)
            response_headers = [[key.lower(), value] for key, value in error.headers.items()]
        return {"status": status, "headers": response_headers, "body": response_body,
                "startedMonotonicNs": started, "completedMonotonicNs": time.monotonic_ns()}

    def process_action(self, action: str, argv: list[str], cwd: str | None,
                       env: dict[str, str], timeout_ms: int) -> dict[str, Any]:
        prefix = self.process_commands.get(action)
        if not prefix:
            raise c.EnvironmentFailure(f"unbound process action: {action}")
        command = [*prefix, *argv[1:]]
        started = time.monotonic_ns()
        completed = subprocess.run(command, cwd=cwd, env={**os.environ, **env},
                                   input=b"", capture_output=True, check=False,
                                   timeout=timeout_ms / 1000)
        receipt = self._subprocess_receipt(completed, command, cwd, started, time.monotonic_ns())
        if completed.stdout:
            try:
                detail = json.loads(completed.stdout)
            except (UnicodeDecodeError, json.JSONDecodeError):
                detail = None
            if isinstance(detail, dict):
                receipt.update(detail)
        return receipt

    def store_query(self, store: str, namespace: str,
                    query: dict[str, Any], timeout_ms: int) -> dict[str, Any]:
        prefix = self.store_commands.get(store)
        if not prefix:
            raise c.EnvironmentFailure(f"unbound store adapter: {store}")
        request = c.canonical_bytes({"namespace": namespace, "query": query})
        started = time.monotonic_ns()
        completed = subprocess.run(prefix, input=request, capture_output=True, check=False,
                                   timeout=timeout_ms / 1000)
        if completed.returncode != 0:
            raise c.EnvironmentFailure("store query adapter failed")
        try:
            value = json.loads(completed.stdout)
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise c.HarnessFailure("store query adapter returned malformed JSON") from error
        if not isinstance(value, dict) or not isinstance(value.get("documents"), list):
            raise c.HarnessFailure("store query adapter returned no raw document list")
        return {**value, "store": store, "namespace": namespace, "query": query,
                "startedMonotonicNs": started, "completedMonotonicNs": time.monotonic_ns()}

    def filesystem_action(self, action: str, path: str,
                          options: dict[str, Any]) -> dict[str, Any]:
        target = Path(path)
        started = time.monotonic_ns()
        if action == "CREATE_EMPTY_DIRECTORY":
            target.mkdir(parents=True, exist_ok=False)
        elif action == "CREATE_CAPTURE_WORKSPACE":
            target.mkdir(parents=True, exist_ok=False)
            hook_source = Path(str(options["hookSource"]))
            if not hook_source.is_dir():
                raise c.EnvironmentFailure("bound capture hook source is unavailable")
            (target / ".openhands").symlink_to(hook_source, target_is_directory=True)
        elif action == "REMOVE_OWNED_TREE":
            if not target.is_absolute() or len(target.parts) < 3:
                raise c.SafetyFailure("unsafe cleanup target")
            shutil.rmtree(target)
        elif action != "INSPECT":
            raise c.HarnessFailure(f"unsupported filesystem action: {action}")
        return {"action": action, "path": path, "exists": target.exists(),
                "startedMonotonicNs": started, "completedMonotonicNs": time.monotonic_ns()}

    def maven_subprocess(self, argv: list[str], cwd: str,
                         timeout_ms: int) -> dict[str, Any]:
        if not argv or argv[0] != "mvn":
            raise c.SafetyFailure("Maven argv is not explicitly bound")
        started = time.monotonic_ns()
        completed = subprocess.run(argv, cwd=cwd, capture_output=True, check=False,
                                   timeout=timeout_ms / 1000)
        return self._subprocess_receipt(completed, argv, cwd, started, time.monotonic_ns())

    def monotonic_ns(self) -> int:
        return time.monotonic_ns()

    def sleep_ns(self, duration_ns: int) -> None:
        if duration_ns < 0:
            raise c.HarnessFailure("negative sleep duration")
        time.sleep(duration_ns / 1_000_000_000)


class OfflineRawPorts:
    """Stateful raw-system fake.  It emits raw receipts, never oracle values."""

    live = False

    def __init__(self, framing: dict[str, Any], *, unrelated_backlog: bool = True,
                 boundary_failure: str | None = None,
                 raw_mutation: str | None = None) -> None:
        self.framing = framing
        self.boundary_failure = boundary_failure
        self.raw_mutation = raw_mutation
        future = c.load_object(c.CONFIGURATION)["futureLiveIdentity"]
        self.namespaces = future["disposableNamespaces"]
        isolation = future["workspaceIsolation"]
        self.workspace_roles = {
            isolation["emptyWorkspace"]: "EMPTY_UNCAPTURED",
            isolation["captureAllowlist"][0]: "ACTOR_ALPHA",
            isolation["captureAllowlist"][1]: "ACTOR_BETA",
        }
        self.calls: list[dict[str, Any]] = []
        self.now_ns = 1_000_000_000
        self.counter = 0
        self.mode = c.ServiceMode.OFF
        self.stub_running = False
        self.workspaces: set[str] = set()
        self.conversations: dict[str, dict[str, Any]] = {}
        self.events: dict[str, list[dict[str, Any]]] = {}
        self.raw_requests: list[dict[str, Any]] = []
        self.terminals: list[dict[str, Any]] = []
        self.capture_documents: list[dict[str, Any]] = []
        self.pending_captures: list[dict[str, Any]] = []
        self.memories: dict[str, dict[str, Any]] = {}
        self.capture_query_reads: dict[str, int] = {}
        self.raw_query_reads: dict[str, int] = {}
        self.direct_context_reads: dict[str, int] = {}
        self.boundary_faults_observed: list[str] = []
        self.fault_observations: list[dict[str, Any]] = []
        self.model_fault_observations: list[dict[str, Any]] = []
        self.summary_events: list[dict[str, Any]] = []
        self.uncaptured_workspace_events: list[dict[str, Any]] = []
        self.cleanup_called = False
        self._lock = threading.RLock()
        self.bridge_fault_mode = "NONE"
        if unrelated_backlog:
            for index in range(1, 8):
                self.capture_documents.append({
                    "conversation_id": f"unrelated-{index}", "event_id": f"event-{index}",
                    "memory_id": f"unrelated-memory-{index}", "cardinality": 1,
                    "partition": "unrelated", "source": "PREEXISTING_BACKLOG",
                })

    def _id(self, prefix: str) -> str:
        with self._lock:
            self.counter += 1
            return f"{prefix}-{self.counter:05d}"

    def _tick(self, delta: int = 1_000_000) -> tuple[int, int]:
        with self._lock:
            started = self.now_ns
            self.now_ns += delta
            return started, self.now_ns

    def _record(self, port: str, request: dict[str, Any]) -> None:
        with self._lock:
            self.calls.append({"port": port, **request})

    def _fault(self, name: str) -> None:
        if self.boundary_failure != name:
            return
        self.boundary_faults_observed.append(name)
        failure = c.HarnessFailure if "MALFORMED" in name else \
            c.SafetyFailure if name in {"CANCELLATION", "CLIENT_DISCONNECT", "CLEANUP_FAILURE"} \
            else c.EnvironmentFailure
        raise failure(f"injected raw-port failure: {name}")

    def monotonic_ns(self) -> int:
        self._record("CLOCK", {"action": "MONOTONIC_NS"})
        return self.now_ns

    def sleep_ns(self, duration_ns: int) -> None:
        self._record("CLOCK", {"action": "SLEEP", "durationNs": duration_ns})
        self.now_ns += duration_ns

    def filesystem_action(self, action: str, path: str,
                          options: dict[str, Any]) -> dict[str, Any]:
        self._record("FILESYSTEM", {"action": action, "path": path, "options": options})
        self._fault("FILESYSTEM_FAILURE")
        started, completed = self._tick()
        if action == "CREATE_EMPTY_DIRECTORY":
            if path in self.workspaces:
                raise c.SafetyFailure("workspace collision")
            self.workspaces.add(path)
        elif action == "CREATE_CAPTURE_WORKSPACE":
            if path in self.workspaces:
                raise c.SafetyFailure("workspace collision")
            if options.get("hookSource") != c.load_object(c.CONFIGURATION)[
                    "futureLiveIdentity"]["workspaceIsolation"]["captureHookSource"]:
                raise c.SafetyFailure("capture hook source identity mismatch")
            self.workspaces.add(path)
        elif action == "REMOVE_OWNED_TREE":
            self.workspaces = {value for value in self.workspaces if not value.startswith(path)}
        elif action == "INSPECT":
            pass
        else:
            raise c.HarnessFailure(f"unsupported filesystem action: {action}")
        return {"action": action, "path": path, "exists": path in self.workspaces,
                "startedMonotonicNs": started, "completedMonotonicNs": completed}

    def process_action(self, action: str, argv: list[str], cwd: str | None,
                       env: dict[str, str], timeout_ms: int) -> dict[str, Any]:
        self._record("PROCESS", {"action": action, "argv": argv, "cwd": cwd,
                                 "envKeys": sorted(env), "timeoutMs": timeout_ms})
        if action == "TRANSITION_SERVICE":
            mode = c.ServiceMode(argv[-1])
            self._fault("PROCESS_STOP_FAILURE" if mode == c.ServiceMode.OFF else "PROCESS_START_FAILURE")
            self.mode = mode
            if mode in {c.ServiceMode.CAPTURE_ONLY, c.ServiceMode.CAPTURE_AND_RETRIEVAL}:
                self.capture_documents.extend(self.pending_captures)
                self.pending_captures.clear()
        elif action == "START_STUB":
            self._fault("STUB_START_FAILURE")
            self.stub_running = True
        elif action == "STOP_STUB":
            self.stub_running = False
        elif action == "CLEANUP_OWNED":
            self.cleanup_called = True
            self._fault("CLEANUP_FAILURE")
            self.mode = c.ServiceMode.OFF
            self.stub_running = False
            self.conversations.clear()
            self.events.clear()
            self.raw_requests.clear()
            self.terminals.clear()
            self.pending_captures.clear()
            self.memories.clear()
            self.fault_observations.clear()
            self.model_fault_observations.clear()
            self.summary_events.clear()
            self.uncaptured_workspace_events.clear()
            self.capture_documents = [document for document in self.capture_documents
                                      if document.get("source") == "PREEXISTING_BACKLOG"]
        elif action == "EMERGENCY_CLEANUP_OWNED":
            self.cleanup_called = True
            self.mode = c.ServiceMode.OFF
            self.stub_running = False
            self.conversations.clear()
            self.events.clear()
            self.raw_requests.clear()
            self.terminals.clear()
            self.pending_captures.clear()
            self.memories.clear()
            self.fault_observations.clear()
            self.model_fault_observations.clear()
            self.summary_events.clear()
            self.uncaptured_workspace_events.clear()
            self.capture_documents = [document for document in self.capture_documents
                                      if document.get("source") == "PREEXISTING_BACKLOG"]
        elif action == "CONFIGURE_BRIDGE_FAULT":
            self.bridge_fault_mode = argv[-1]
        elif action == "CLEAR_BRIDGE_FAULT":
            self.bridge_fault_mode = "NONE"
        elif action == "CLEANUP_OPERATION_NAMESPACES":
            self.pending_captures.clear()
            self.raw_requests.clear()
            self.terminals.clear()
            self.fault_observations.clear()
            self.model_fault_observations.clear()
            self.summary_events.clear()
            self.uncaptured_workspace_events.clear()
            self.capture_documents = [document for document in self.capture_documents
                                      if document.get("source") == "PREEXISTING_BACKLOG"]
            self.memories.clear()
            if self.raw_mutation == "NAMESPACE_ORPHAN":
                self.memories["orphan-memory"] = {
                    "memory_id": "orphan-memory", "partition": "actor-alpha",
                    "text": "orphan", "eligible": True, "state": "PROMOTED",
                    "trace_id": "orphan-trace", "projected": True,
                }
        elif action == "INVENTORY":
            pass
        else:
            raise c.HarnessFailure(f"unsupported process action: {action}")
        started, completed = self._tick()
        pending = sum(not any(value["requestId"] == request["requestId"]
                              for value in self.terminals)
                      for request in self.raw_requests)
        if self.raw_mutation == "ORPHANED_PROCESS" and action == "INVENTORY" and self.raw_requests:
            pending += 1
        return {"action": action, "exitStatus": 0, "stdout": b"", "stderr": b"",
                "serviceMode": self.mode.value, "stubRunning": self.stub_running,
                "activeWork": pending, "queuedWork": 0,
                "ownedChildProcessCount": pending,
                "startedMonotonicNs": started, "completedMonotonicNs": completed}

    def maven_subprocess(self, argv: list[str], cwd: str,
                         timeout_ms: int) -> dict[str, Any]:
        self._record("MAVEN", {"argv": argv, "cwd": cwd, "timeoutMs": timeout_ms})
        self._fault("MAVEN_FAILURE")
        properties = {item[2:].split("=", 1)[0]: item.split("=", 1)[1]
                      for item in argv if item.startswith("-D") and "=" in item}
        memory_id = properties.get("sma.s2.memoryId")
        if memory_id:
            self.memories[memory_id] = {
                "memory_id": memory_id, "partition": properties.get("sma.s2.partition", ""),
                "text": base64.b64decode(properties.get("sma.s2.textBase64", "")).decode(),
                "eligible": properties.get("sma.s2.eligible") == "true",
                "state": properties.get("sma.s2.state", "PROMOTED"),
                "trace_id": self._id("trace"), "projected": properties.get("sma.s2.eligible") == "true",
            }
        started, completed = self._tick(5_000_000)
        return {"argv": argv, "cwd": cwd, "exitStatus": 0, "stdout": b"", "stderr": b"",
                "startedMonotonicNs": started, "completedMonotonicNs": completed}

    def _context(self, workspace_role: str, operation: str) -> str:
        if self.mode not in {c.ServiceMode.RETRIEVAL_ONLY, c.ServiceMode.CAPTURE_AND_RETRIEVAL}:
            return ""
        if operation in {"RETRIEVAL_OUTAGE", "CAPTURE_OUTAGE_RECOVERY", "HOOK_FAULT_MATRIX",
                         "SECRET_DELIVERY_ABSENCE", "EMPTY_RESULT", "FIRST_PROMPT_EMPTY"}:
            return ""
        partition = "actor-beta" if workspace_role == "ACTOR_BETA" else "actor-alpha"
        eligible = [value for value in self.memories.values()
                    if value["partition"] == partition and value["eligible"]]
        if operation == "OVERSIZED_CONTEXT":
            eligible = [value for value in eligible if "timeout" in value["text"].lower()][:3]
        elif partition == "actor-alpha":
            preferred = [value for value in eligible if value["memory_id"] == "mem-alpha-adversarial"] \
                if operation == "UNTRUSTED_CONTEXT_PLACEMENT" else \
                [value for value in eligible if value["memory_id"] == "mem-alpha-timeout"]
            eligible = preferred or eligible[:1]
        else:
            eligible = [value for value in eligible if value["memory_id"] == "mem-beta-port"][:1]
        memories = [(value["memory_id"], value["text"]) for value in eligible[:3]]
        context = c.frame_context(self.framing, memories) if memories else ""
        if len(context) > 4096:
            while memories and len(c.frame_context(self.framing, memories)) > 4096:
                memories.pop()
            context = c.frame_context(self.framing, memories) if memories else ""
        return context

    @staticmethod
    def _json_body(value: dict[str, Any]) -> bytes:
        return c.canonical_bytes(value)

    @staticmethod
    def _operation_for_case(case_id: str) -> str:
        corpus = c.load_object(c.CORPUS)
        case = next((value for value in corpus["cases"] if value["id"] == case_id), None)
        if case is None:
            raise c.HarnessFailure(f"unknown control-prompt case identity: {case_id}")
        return str(case["operation"])

    def http_request(self, method: str, endpoint: str, path: str,
                     headers: dict[str, str], body: bytes,
                     timeout_ms: int) -> dict[str, Any]:
        self._record("HTTP", {"method": method, "endpoint": endpoint, "path": path,
                              "headerNames": sorted(headers), "body": body,
                              "timeoutMs": timeout_ms})
        started, completed = self._tick()
        payload = json.loads(body) if body else {}
        status = 200
        response: dict[str, Any] = {}
        if method == "GET" and path == "/api/settings":
            response = {"agent_settings": {"llm": {}, "tools": [], "mcp_config": {"mcpServers": {}}}}
        elif method == "POST" and path == "/api/hooks":
            response = {"hook_config": {"user_prompt_submit": [{
                "command": "candidate10-native-hook", "timeout": 2,
            }]}}
        elif method == "POST" and path == "/api/conversations":
            self._fault("CONVERSATION_CREATE_FAILURE")
            conversation_id = self._id("conversation")
            response = {"id": conversation_id}
            workspace_path = payload["workspace"]["working_dir"]
            workspace_role = self.workspace_roles.get(workspace_path)
            if workspace_role is None:
                raise c.SafetyFailure("conversation workspace is outside candidate-10 identity")
            self.conversations[conversation_id] = {
                "workspaceRole": workspace_role,
                "workspacePath": workspace_path,
                "parentId": payload.get("parent_conversation_id"),
                "executionStatus": "idle", "stableFetches": 0,
                "agentProfile": str(payload["agent_settings"]["llm"]["model"]),
            }
            self.events[conversation_id] = []
            parent = payload.get("parent_conversation_id")
            if parent:
                self.conversations[parent].setdefault("children", []).append(conversation_id)
        elif method == "POST" and re.fullmatch(r"/api/conversations/[^/]+/events", path):
            conversation_id = path.split("/")[3]
            self._submit_event(conversation_id, payload)
            response = {"accepted": True}
        elif method == "GET" and re.fullmatch(
                r"/api/conversations/[^/]+/events/search\?limit=100", path):
            conversation_id = path.split("/")[3]
            response = {"items": self.events.get(conversation_id, [])}
        elif method == "GET" and re.fullmatch(r"/api/conversations/[^/]+", path):
            conversation_id = path.split("/")[3]
            detail = self.conversations[conversation_id]
            detail["stableFetches"] += 1
            response = {"id": conversation_id, "execution_status": detail["executionStatus"],
                        "parent_conversation_id": detail.get("parentId"),
                        "sub_conversation_ids": detail.get("children", []),
                        "fetch_sequence": detail["stableFetches"]}
            if self.raw_mutation == "WRONG_PARENT_LINKAGE" and detail.get("parentId"):
                response["parent_conversation_id"] = "wrong-parent"
        elif method == "DELETE" and re.fullmatch(r"/api/conversations/[^/]+", path):
            conversation_id = path.split("/")[3]
            self.conversations.pop(conversation_id, None)
            self.events.pop(conversation_id, None)
            response = {"deleted": conversation_id}
        elif method == "POST" and path.endswith("/condense"):
            response = {"condensed": True}
            status = 204
            conversation_id = path.split("/")[3]
            summary = {"kind": "SummaryEvent", "id": self._id("summary-event"),
                       "conversationId": conversation_id, "usedAsDeliveryState": False,
                       "sequence": len(self.events[conversation_id]) + 1}
            if self.raw_mutation == "SUMMARY_USED_AS_DELIVERY":
                summary["usedAsDeliveryState"] = True
            self.summary_events.append(summary)
            self.events[conversation_id].append(summary)
        elif method == "POST" and path.endswith("/interrupt"):
            self._fault("CANCELLATION")
            self._fault("CLIENT_DISCONNECT")
            conversation_id = path.split("/")[3]
            pending = [record for record in self.raw_requests
                       if record["conversationId"] == conversation_id
                       and not any(value["requestId"] == record["requestId"] for value in self.terminals)]
            for record in pending:
                self.terminals.append({"requestId": record["requestId"],
                                       "conversationId": conversation_id,
                                       "responseBody": b"", "responseWriteOutcome": "CLIENT_DISCONNECTED",
                                       "completedMonotonicNs": self.now_ns})
            self.events[conversation_id].append({
                "kind": "ConversationErrorEvent", "id": self._id("cancel-event"),
                "source": "environment", "error": "cancelled",
                "sequence": len(self.events[conversation_id]) + 1,
            })
            self.conversations[conversation_id]["executionStatus"] = "stopped"
            response = {"interrupted": True}
        elif method == "POST" and endpoint == "BRIDGE" and path == "/v1/openhands/context":
            conversation_id = payload["session_id"]
            direct_read = self.direct_context_reads.get(conversation_id, 0)
            self.direct_context_reads[conversation_id] = direct_read + 1
            workspace_role = self.conversations[conversation_id]["workspaceRole"]
            operation_match = re.search(r"case=(SMA-S2-[^ ]+)", str(payload.get("prompt", "")))
            operation = self._operation_for_case(operation_match.group(1)) \
                if operation_match else "RESTART_CONTINUITY"
            context = self._context(workspace_role, operation)
            parsed = c.parse_context(context, self.framing)
            response = {"session_id": conversation_id,
                        "working_dir": self.conversations[conversation_id]["workspacePath"],
                        "partition": "actor-beta" if workspace_role == "ACTOR_BETA" else "actor-alpha",
                        "context_block": context, "memory_ids": parsed.memory_ids}
            if self.raw_mutation == "POST_RESTART_PARTITION_MUTATED":
                response["partition"] = "actor-beta"
            if self.raw_mutation == "POST_RESTART_RECALL_MISSING":
                response["context_block"] = ""
                response["memory_ids"] = []
            if self.raw_mutation == "PRE_RESTART_PARTITION_MUTATED" \
                    and direct_read == 0:
                response["partition"] = "actor-beta"
        else:
            raise c.HarnessFailure(f"unsupported raw HTTP route: {method} {path}")
        return {"status": status, "headers": [["content-type", "application/json"]],
                "body": self._json_body(response) if status != 204 else b"",
                "startedMonotonicNs": started, "completedMonotonicNs": completed}

    def _submit_event(self, conversation_id: str, payload: dict[str, Any]) -> None:
        if payload.get("role") != "user" or not isinstance(payload.get("content"), list):
            raise c.HarnessFailure("OpenHands SendMessageRequest shape mismatch")
        prompt = "".join(str(value.get("text", "")) for value in payload["content"]
                         if isinstance(value, dict) and value.get("type") == "text")
        workspace_role = self.conversations[conversation_id]["workspaceRole"]
        if payload.get("run") is False:
            user = {"kind": "MessageEvent", "id": self._id("user-event"), "source": "user",
                    "text": prompt, "llm_message": {"role": "user", "content": payload["content"]},
                    "extended_content": "", "sequence": len(self.events[conversation_id]) + 1}
            self.events[conversation_id].append(user)
            capture = {"conversation_id": conversation_id, "event_id": user["id"],
                       "memory_id": self._id("fixture-memory"), "cardinality": 1,
                       "partition": "actor-beta" if workspace_role == "ACTOR_BETA" else "actor-alpha",
                       "source": "CANDIDATE_10"}
            if self.mode in {c.ServiceMode.CAPTURE_ONLY, c.ServiceMode.CAPTURE_AND_RETRIEVAL}:
                self.capture_documents.append(capture)
            else:
                self.pending_captures.append(capture)
            return
        match = re.match(r"\[SMA-S2-STUB case=(SMA-S2-[^ ]+) repetition=(\d+) mode=([^\]]+)\]", prompt)
        if match is None:
            raise c.HarnessFailure("control prompt identity missing from OpenHands request")
        case_id = match.group(1)
        repetition = int(match.group(2))
        mode = match.group(3)
        operation = self._operation_for_case(case_id)
        interaction_index = 1 + sum(value["conversationId"] == conversation_id
                                    for value in self.raw_requests)
        if operation == "HOOK_FAULT_MATRIX" and self.boundary_failure in {
            "HOOK_TIMEOUT", "HOOK_MALFORMED", "HOOK_HTTP_503", "BRIDGE_OUTAGE"}:
            self._fault(self.boundary_failure)
        if operation == "HOOK_FAULT_MATRIX" and self.bridge_fault_mode != "NONE":
            observed_mode = "WRONG_MODE" if self.raw_mutation == "HOOK_FAULT_MODE_MISMATCH" \
                else self.bridge_fault_mode
            body_length = 1 if self.raw_mutation == "HOOK_FAULT_BODY_PRESENT" else 0
            self.fault_observations.append({"kind": "BRIDGE_FAULT", "mode": observed_mode,
                                            "caseId": case_id, "repetition": repetition,
                                            "requestBodyLength": body_length,
                                            "activatedMonotonicNs": self.now_ns,
                                            "observedMonotonicNs": self.now_ns + 1_000_000})
        if operation == "RETRIEVAL_OUTAGE":
            self.fault_observations.append({"kind": "RETRIEVAL_OUTAGE", "mode": "BRIDGE_DOWN",
                                            "caseId": case_id, "repetition": repetition,
                                            "requestBodyLength": 1
                                            if self.raw_mutation == "RETRIEVAL_FAULT_BODY_PRESENT" else 0,
                                            "activatedMonotonicNs": self.now_ns,
                                            "observedMonotonicNs": self.now_ns + 1_000_000})
        context = self._context(workspace_role, operation)
        hook_id = self._id("hook-event")
        user_id = self._id("user-event")
        hook = {"kind": "HookExecutionEvent", "id": hook_id, "source": "environment",
                "hook_input": {"message": prompt}, "additional_context": context,
                "success": True, "exit_code": 0, "sequence": len(self.events[conversation_id]) + 1}
        user = {"kind": "MessageEvent", "id": user_id, "source": "user", "text": prompt,
                "llm_message": {"role": "user", "content": payload["content"]},
                "extended_content": context, "sequence": len(self.events[conversation_id]) + 2}
        self.events[conversation_id].extend([hook, user])
        capture_allowed = workspace_role in {"ACTOR_ALPHA", "ACTOR_BETA"}
        if self.raw_mutation == "EMPTY_WORKSPACE_CAPTURED":
            capture_allowed = True
        if not capture_allowed:
            target = self.uncaptured_workspace_events
        elif self.mode in {c.ServiceMode.CAPTURE_ONLY, c.ServiceMode.CAPTURE_AND_RETRIEVAL}:
            target = self.capture_documents
        else:
            target = self.pending_captures
        for event in (user,):
            target.append({"conversation_id": conversation_id, "event_id": event["id"],
                           "memory_id": self._id("captured-memory"), "cardinality": 1,
                           "partition": "actor-beta" if workspace_role == "ACTOR_BETA" else "actor-alpha",
                           "source": "CANDIDATE_10"})
        if operation == "FEEDBACK_LOOP_PREVENTION":
            for event_kind in ("ActionEvent", "CompletionLogEvent", "ReasoningEvent",
                               "ToolEvent", "UtilityEvent"):
                self.events[conversation_id].append({"kind": event_kind,
                                                     "id": self._id(event_kind.lower()),
                                                     "source": "environment",
                                                     "sequence": len(self.events[conversation_id]) + 1})
            if self.raw_mutation == "FORBIDDEN_EVENT_CAPTURED":
                forbidden_event = next(value for value in self.events[conversation_id]
                                       if value.get("kind") == "HookExecutionEvent")
                self.capture_documents.append({
                    "conversation_id": conversation_id, "event_id": forbidden_event["id"],
                    "memory_id": self._id("captured-memory"), "cardinality": 1,
                    "partition": "actor-alpha", "source": "CANDIDATE_10",
                })
        if mode == "TRANSPORT_FAILURE_UNUSED_PORT":
            self.model_fault_observations.append({
                "kind": "MODEL_FAULT", "mode": mode, "requestId": None,
                "caseId": case_id, "repetition": repetition,
                "activatedMonotonicNs": self.now_ns,
                "observedMonotonicNs": self.now_ns + 1_000_000,
            })
            self.events[conversation_id].append({"kind": "ConversationErrorEvent",
                                                 "id": self._id("error"), "source": "environment",
                                                 "error": "transport",
                                                 "sequence": len(self.events[conversation_id]) + 1})
            self.conversations[conversation_id]["executionStatus"] = "error"
            return
        if operation == "MODEL_STUB_FAULT_MATRIX" and self.boundary_failure in {
            "MODEL_TIMEOUT", "MODEL_MALFORMED", "MODEL_HTTP_503", "TRANSPORT_OUTAGE"}:
            self._fault(self.boundary_failure)
        request_id = self._id("request")
        request_body = self._json_body({"messages": [{"role": "user", "content": [
            {"type": "text", "text": prompt},
            *([{"type": "text", "text": context}] if context else []),
        ]}]})
        if self.raw_mutation == "MODEL_CONTENT_FLATTENED":
            request_body = self._json_body({"messages": [{"role": "user",
                                                           "content": prompt + context}]})
        elif self.raw_mutation == "MODEL_SEGMENT_ZERO_MUTATED":
            request_body = self._json_body({"messages": [{"role": "user", "content": [
                {"type": "text", "text": "MUTATED_PROMPT"},
                *([{"type": "text", "text": context}] if context else []),
            ]}]})
        elif self.raw_mutation == "MODEL_CONTEXT_SEGMENT_MUTATED" and context:
            request_body = self._json_body({"messages": [{"role": "user", "content": [
                {"type": "text", "text": prompt},
                {"type": "text", "text": context + "MUTATED_CONTEXT"},
            ]}]})
        self.raw_requests.append({"requestId": request_id, "conversationId": conversation_id,
                                  "caseId": case_id, "repetition": repetition,
                                  "interactionIndex": interaction_index,
                                  "requestBody": request_body, "receivedMonotonicNs": self.now_ns})
        if operation == "MODEL_STUB_FAULT_MATRIX":
            observed_mode = "WRONG_MODE" if self.raw_mutation == "MODEL_FAULT_MODE_MISMATCH" else mode
            self.model_fault_observations.append({
                "kind": "MODEL_FAULT", "mode": observed_mode, "requestId": request_id,
                "caseId": case_id, "repetition": repetition,
                "activatedMonotonicNs": self.now_ns,
                "observedMonotonicNs": self.now_ns + 1_000_000,
            })
        if operation == "ACTIVE_CANCELLATION_AND_SHUTDOWN":
            self.conversations[conversation_id]["executionStatus"] = "running"
            return
        outcome = "CLIENT_DISCONNECTED" if mode == "TIMEOUT" else "CLIENT_RECEIVED"
        response_body = f"STUB_OK:{case_id}:{repetition}".encode() if mode in {
            "SUCCESS", "DELEGATION_TOOL"} else b""
        self.terminals.append({"requestId": request_id, "conversationId": conversation_id,
                               "responseBody": response_body, "responseWriteOutcome": outcome,
                               "completedMonotonicNs": self.now_ns + 500_000_000})
        if mode in {"SUCCESS", "DELEGATION_TOOL"}:
            agent_event = {"kind": "MessageEvent", "id": self._id("agent-event"),
                           "source": "agent", "text": response_body.decode(),
                           "llm_message": {"role": "assistant", "content": [
                               {"type": "text", "text": response_body.decode()}]},
                           "sequence": len(self.events[conversation_id]) + 1}
            if self.raw_mutation == "WRONG_EVENT_SEQUENCE" \
                    and operation == "PARENT_CHILD_PROVENANCE":
                agent_event["sequence"] = int(user["sequence"]) - 1
            self.events[conversation_id].append(agent_event)
            target.append({"conversation_id": conversation_id, "event_id": agent_event["id"],
                           "memory_id": self._id("captured-memory"), "cardinality": 1,
                           "partition": "actor-beta" if workspace_role == "ACTOR_BETA" else "actor-alpha",
                           "source": "CANDIDATE_10"})
            self.conversations[conversation_id]["executionStatus"] = "finished"
        else:
            self.events[conversation_id].append({"kind": "ConversationErrorEvent",
                                                 "id": self._id("error"), "source": "environment",
                                                 "error": mode,
                                                 "sequence": len(self.events[conversation_id]) + 1})
            self.conversations[conversation_id]["executionStatus"] = "error"

    def store_query(self, store: str, namespace: str,
                    query: dict[str, Any], timeout_ms: int) -> dict[str, Any]:
        self._record("STORE", {"store": store, "namespace": namespace,
                               "query": query, "timeoutMs": timeout_ms})
        self._fault("CAPTURE_READ_FAILURE" if query.get("kind") == "CAPTURE_EXACT" else "STORE_FAILURE")
        kind = query["kind"]
        documents: list[dict[str, Any]]
        if kind == "STUB_RAW":
            documents = [dict(value) for value in self.raw_requests
                         if value["caseId"] == query["caseId"]
                         and value["repetition"] == query["repetition"]
                         and (not query.get("conversationId")
                              or value["conversationId"] == query["conversationId"])
                         and (not query.get("interactionIndex")
                              or value["interactionIndex"] == query["interactionIndex"])]
            signature = "STUB_RAW:" + c.sha_bytes(c.canonical_bytes(query))
            read = self.raw_query_reads.get(signature, 0)
            self.raw_query_reads[signature] = read + 1
            if self.raw_mutation == "DELAYED_ASYNC_VISIBILITY" and read < 3:
                documents = []
        elif kind == "STUB_TERMINAL":
            ids = set(query["requestIds"])
            documents = [dict(value) for value in self.terminals if value["requestId"] in ids]
            signature = "STUB_TERMINAL:" + c.sha_bytes(c.canonical_bytes(query))
            read = self.raw_query_reads.get(signature, 0)
            self.raw_query_reads[signature] = read + 1
            if self.raw_mutation == "DELAYED_ASYNC_VISIBILITY" and read < 2:
                documents = []
        elif kind == "CAPTURE_EXACT":
            keys = {(value["conversationId"], value["eventId"]) for value in query["keys"]}
            documents = [dict(value) for value in self.capture_documents
                         if (value["conversation_id"], value["event_id"]) in keys]
            if self.raw_mutation == "WRONG_CAPTURE_PARTITION" and documents:
                documents[0]["partition"] = "actor-beta"
            signature = c.sha_bytes(c.canonical_bytes(query["keys"]))
            read = self.capture_query_reads.get(signature, 0)
            self.capture_query_reads[signature] = read + 1
            visibility_delay = 4 if self.raw_mutation == "DELAYED_ASYNC_VISIBILITY" else 1
            if read < visibility_delay and documents:
                documents = documents[:-1]
            if self.raw_mutation == "FIXTURE_CAPTURE_MISSING" \
                    and any(str(value["conversationId"]).startswith("conversation-")
                            for value in query["keys"]):
                documents = []
        elif kind == "CAPTURE_ALL":
            documents = [dict(value) for value in self.capture_documents]
        elif kind == "MEMORY_PROVENANCE":
            ids = set(query["memoryIds"])
            documents = [dict(value) for key, value in self.memories.items() if key in ids]
        elif kind == "PROJECTION_STATE":
            ids = set(query["memoryIds"])
            documents = [{"memory_id": key, "semantic_present": bool(value["projected"]),
                          "episodic_present": bool(value["projected"]),
                          "trace_id": value["trace_id"], "partition": value["partition"]}
                         for key, value in self.memories.items() if key in ids]
            if self.raw_mutation == "PROJECTION_MISSING" and documents:
                documents = documents[:-1]
            elif self.raw_mutation == "PROJECTION_ID_MUTATED" and documents:
                documents[0]["memory_id"] = "mutated-memory-id"
            elif self.raw_mutation == "PROJECTION_TRACE_MUTATED" and documents:
                documents[0]["trace_id"] = "mutated-trace"
            elif self.raw_mutation == "PROJECTION_PARTITION_MUTATED" and documents:
                documents[0]["partition"] = "actor-beta"
        elif kind == "DELIVERY_SURFACES":
            conversation_id = query["conversationId"]
            events = self.events.get(conversation_id, [])
            hook_contexts = [str(value.get("additional_context") or "") for value in events
                             if value.get("kind") == "HookExecutionEvent"]
            persisted_contexts = [str(value.get("extended_content") or "") for value in events
                                  if value.get("kind") == "MessageEvent"
                                  and value.get("source") == "user"]
            request_bodies = [value["requestBody"] for value in self.raw_requests
                              if value["conversationId"] == conversation_id]
            capture_values = [c.canonical_bytes(value) for value in self.capture_documents
                              if value.get("conversation_id") == conversation_id]
            documents = [{"surface": "hook", "values": hook_contexts},
                         {"surface": "persisted", "values": persisted_contexts},
                         {"surface": "modelRequestBodies", "values": request_bodies},
                         {"surface": "operationalLog", "values": []},
                         {"surface": "serviceLog", "values": []},
                         {"surface": "capture", "values": capture_values}]
            if self.raw_mutation == "DELIVERY_SURFACE_OMITTED":
                documents = [value for value in documents if value["surface"] != "capture"]
            if self.raw_mutation == "CROSS_PARTITION_SURFACE_LEAK":
                documents.append({"surface": "operational-log",
                                  "values": ["mem-alpha-timeout API timeout is 17 seconds."]})
            elif self.raw_mutation == "RAW_INELIGIBLE_SURFACE_LEAK":
                documents.append({"surface": "service-log",
                                  "values": ["mem-alpha-raw secret port 6444"]})
        elif kind == "BRIDGE_FAULT":
            documents = [dict(value) for value in self.fault_observations
                         if (not query.get("mode") or value["mode"] == query["mode"])
                         and (not query.get("faultKind") or value["kind"] == query["faultKind"])
                         and (not query.get("caseId") or value["caseId"] == query["caseId"])
                         and (not query.get("repetition") or value["repetition"] == query["repetition"])]
        elif kind == "MODEL_FAULT":
            documents = [dict(value) for value in self.model_fault_observations
                         if (not query.get("caseId") or value["caseId"] == query["caseId"])
                         and (not query.get("repetition") or value["repetition"] == query["repetition"])]
        elif kind == "SUMMARY_EVENTS":
            documents = [dict(value) for value in self.summary_events
                         if value["conversationId"] == query["conversationId"]]
            if self.raw_mutation == "SUMMARY_EVENT_MISSING":
                documents = []
            elif self.raw_mutation == "SUMMARY_WRONG_KIND" and documents:
                documents[0]["kind"] = "MessageEvent"
            elif self.raw_mutation == "SUMMARY_WRONG_CONVERSATION" and documents:
                documents[0]["conversationId"] = "wrong-conversation"
        elif kind == "UNCAPTURED_EVENTS":
            keys = {(value["conversationId"], value["eventId"]) for value in query["keys"]}
            documents = [dict(value) for value in self.uncaptured_workspace_events
                         if (value["conversation_id"], value["event_id"]) in keys]
        elif kind == "RECURSIVE_CAPTURES":
            documents = [dict(value) for value in self.capture_documents
                         if value.get("source") == "SMA_DERIVED"]
            if self.raw_mutation == "RECURSIVE_CAPTURE_PRESENT":
                documents.append({"conversation_id": query.get("conversationId", "mutated"),
                                  "event_id": "recursive-event", "memory_id": "recursive-memory",
                                  "source": "SMA_DERIVED", "cardinality": 1})
        elif kind == "SECRET_SURFACES":
            if query.get("markerSha256") != c.sha_bytes(c.SECRET.encode()):
                raise c.HarnessFailure("secret scan marker identity mismatch")
            marker = c.SECRET
            conversation_id = query["conversationId"]
            events = self.events.get(conversation_id, [])
            contexts = [str(value.get("additional_context") or "") for value in events
                        if value.get("kind") == "HookExecutionEvent"]
            persisted = [str(value.get("extended_content") or "") for value in events
                         if value.get("kind") == "MessageEvent" and value.get("source") == "user"]
            raw_bodies = [value["requestBody"] for value in self.raw_requests
                          if value["conversationId"] == conversation_id]
            model_segments = []
            for raw_body in raw_bodies:
                request = json.loads(raw_body)
                content = [value for value in request.get("messages", [])
                           if value.get("role") == "user"][-1]["content"]
                model_segments.append((str(content[0].get("text", "")),
                                       str(content[1].get("text", ""))
                                       if len(content) > 1 else ""))
            memory_text = [value["text"] for value in self.memories.values()]
            documents = [
                {"surface": "currentPromptSubmission", "values": [
                    str(value.get("text") or "") for value in events
                    if value.get("kind") == "MessageEvent" and value.get("source") == "user"]},
                {"surface": "currentPromptPersisted", "values": [
                    str(value.get("text") or "") for value in events
                    if value.get("kind") == "MessageEvent" and value.get("source") == "user"]},
                {"surface": "hookInput", "values": [
                    str((value.get("hook_input") or {}).get("message") or "") for value in events
                    if value.get("kind") == "HookExecutionEvent"]},
                {"surface": "modelSegment0", "values": [value[0] for value in model_segments]},
                {"surface": "sealedRawPrompt", "values": raw_bodies},
                {"surface": "additionalContext", "values": contexts + persisted},
                {"surface": "modelSegment1", "values": [value[1] for value in model_segments]},
                {"surface": "retrievalEvents", "values": memory_text},
                {"surface": "derivedProjection", "values": [value["text"]
                                                                for value in self.memories.values()
                                                                if value["projected"]]},
                {"surface": "operationalLog", "values": []},
            ]
            if self.raw_mutation == "SECRET_SURFACE_OMITTED":
                documents = [value for value in documents
                             if value["surface"] != "operationalLog"]
            if self.raw_mutation == "SECRET_FORBIDDEN_SURFACE_LEAK":
                documents[-1]["values"] = [marker]
            elif self.raw_mutation == "SECRET_RAW_DUPLICATE":
                documents[0]["values"].append(raw_bodies[0])
        elif kind == "ACTIVE_WORK":
            pending = sum(not any(value["requestId"] == request["requestId"]
                                  for value in self.terminals)
                          for request in self.raw_requests)
            if self.raw_mutation in {"ACTIVE_WORK_NONZERO", "ORPHANED_PROCESS"} and self.raw_requests:
                pending += 1
            documents = [{"active": pending, "queued": 0, "queueDepth": 0,
                          "timeoutCount": 0, "rejectionCount": 0,
                          "resourceSnapshot": {"ownedWorkers": pending,
                                               "openRequests": pending}}]
            if self.raw_mutation == "TELEMETRY_OMITTED":
                documents[0].pop("resourceSnapshot")
            elif self.raw_mutation == "TELEMETRY_TIMEOUT_REJECTION":
                documents[0]["timeoutCount"] = 1
                documents[0]["rejectionCount"] = 1
        elif kind == "INVENTORY":
            namespace_present = bool(self.memories or any(
                value.get("source") == "CANDIDATE_10" for value in self.capture_documents)) \
                if store == "MONGODB" else any(value["projected"] for value in self.memories.values())
            documents = [{"serviceMode": self.mode.value, "stubRunning": self.stub_running,
                         "conversationCount": len(self.conversations),
                         "workspaceCount": len(self.workspaces),
                         "ownedCaptureCount": sum(value.get("source") == "CANDIDATE_10"
                                                  for value in self.capture_documents),
                         "unrelatedCaptureCount": sum(value.get("source") == "PREEXISTING_BACKLOG"
                                                      for value in self.capture_documents),
                         "mongodbNamespacePresent": bool(self.memories or any(
                             value.get("source") == "CANDIDATE_10" for value in self.capture_documents)),
                         "semanticNamespacePresent": any(value["projected"] for value in self.memories.values()),
                         "episodicNamespacePresent": any(value["projected"] for value in self.memories.values())}]
            documents[0]["namespacePresent"] = namespace_present
        else:
            raise c.HarnessFailure(f"unsupported store query: {kind}")
        started, completed = self._tick()
        return {"store": store, "namespace": namespace, "query": query,
                "documents": documents, "startedMonotonicNs": started,
                "completedMonotonicNs": completed}


class RawCandidate10OrchestrationAdapter:
    """Candidate-10 workflows over canonical raw ports; shared by H0 and live."""

    def __init__(self, framing: dict[str, Any], ports: RawBoundaryPorts, *,
                 predicate_mutation: str | None = None,
                 evidence_mutation: str | None = None,
                 missing_evidence: str | None = None,
                 injected_failure: str | None = None,
                 scope_identity_mutation: str | None = None,
                 scope_shape_mutation: str | None = None) -> None:
        self.framing = framing
        self.ports = ports
        self.live = ports.live
        self.predicate_mutation = predicate_mutation
        self.evidence_mutation = evidence_mutation
        self.missing_evidence = missing_evidence
        self.injected_failure = injected_failure
        self.scope_identity_mutation = scope_identity_mutation
        self.scope_shape_mutation = scope_shape_mutation
        self.ledger: c.EvidenceLedger | None = None
        self.emergency_ledger: c.EvidenceLedger | None = None
        self.mode = c.ServiceMode.OFF
        self.seeded = False
        self.stub_started = False
        self.owned_conversations: set[str] = set()
        self.conversation_metadata: dict[str, dict[str, Any]] = {}
        self.owned_workspaces: set[str] = set()
        self.cleanup_called = False
        self.emergency_cleanup_used = False
        config = c.load_object(c.CONFIGURATION)["futureLiveIdentity"]
        self.root = config["workspaceIsolation"]["root"]
        self.workspace_paths = {
            "EMPTY_UNCAPTURED": config["workspaceIsolation"]["emptyWorkspace"],
            "ACTOR_ALPHA": config["workspaceIsolation"]["captureAllowlist"][0],
            "ACTOR_BETA": config["workspaceIsolation"]["captureAllowlist"][1],
        }
        self.maven_project = config["mavenPromotion"]["projectDirectory"]
        self.namespaces = config["disposableNamespaces"]
        self.protocol = config["openHandsProtocol"]
        self.hook_source = config["workspaceIsolation"]["captureHookSource"]
        self.excluded_workspace_keys: list[c.EventKey] = []

    @property
    def captures(self) -> dict[tuple[str, str], int]:
        receipt = self._raw("STORE", {"kind": "CAPTURE_ALL"}, lambda: self.ports.store_query(
            "MONGODB", self.namespaces["mongodbDatabase"], {"kind": "CAPTURE_ALL"}, 1000))
        values: dict[tuple[str, str], int] = {}
        for document in receipt["documents"]:
            key = (str(document["conversation_id"]), str(document["event_id"]))
            values[key] = values.get(key, 0) + 1
        return values

    def bind_ledgers(self, primary: c.EvidenceLedger, emergency: c.EvidenceLedger) -> None:
        self.ledger = primary
        self.emergency_ledger = emergency

    def _raw(self, port: str, request: dict[str, Any], action: Any) -> Any:
        if self.ledger is None:
            raise c.HarnessFailure("raw-port ledger not bound")
        pre = self.ledger.append("PRE_ACTION_RAW_PORT", {"port": port, **request})
        try:
            result = action()
            canonical = raw_receipt_value(result)
            retained = retained_raw_receipt_value(result)
        except Exception as error:
            self.ledger.append("RAW_PORT_FAILURE", {
                "port": port, "preActionRecordId": pre.record_id,
                "failureClass": type(error).__name__,
                "messageSha256": c.sha_bytes(str(error).encode()),
            })
            raise
        self.ledger.append("RAW_PORT_RESULT", {
            "port": port, "preActionRecordId": pre.record_id,
            "receiptSha256": c.sha_bytes(c.canonical_bytes(canonical)),
            "retainedReceipt": retained,
        })
        return result

    @staticmethod
    def _decode_http(receipt: dict[str, Any], expected: tuple[int, ...]) -> dict[str, Any]:
        if receipt.get("status") not in expected:
            raise c.EnvironmentFailure(f"unexpected HTTP status: {receipt.get('status')}")
        body = receipt.get("body")
        if not isinstance(body, bytes):
            raise c.HarnessFailure("HTTP body must be raw bytes")
        if not body:
            return {}
        try:
            value = json.loads(body)
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise c.HarnessFailure("malformed HTTP JSON body") from error
        if not isinstance(value, dict):
            raise c.HarnessFailure("HTTP JSON object required")
        return value

    def _http(self, method: str, path: str, body: dict[str, Any] | None = None,
              expected: tuple[int, ...] = (200,), timeout_ms: int = 15000,
              endpoint: str = "OPENHANDS",
              headers: dict[str, str] | None = None) -> tuple[dict[str, Any], dict[str, Any]]:
        raw = c.canonical_bytes(body or {}) if body is not None else b""
        receipt = self._raw("HTTP", {"method": method, "path": path, "timeoutMs": timeout_ms,
                                    "bodySha256": c.sha_bytes(raw), "bodyLength": len(raw)},
                            lambda: self.ports.http_request(
                                method, endpoint, path, headers or {}, raw, timeout_ms))
        return receipt, self._decode_http(receipt, expected)

    def _process(self, action: str, argv: list[str], timeout_ms: int = 30000) -> dict[str, Any]:
        receipt = self._raw("PROCESS", {"action": action, "argv": argv, "timeoutMs": timeout_ms},
                            lambda: self.ports.process_action(action, argv, None, {}, timeout_ms))
        if receipt.get("exitStatus") != 0:
            raise c.EnvironmentFailure(f"process action failed: {action}")
        return receipt

    def _store(self, kind: str, query: dict[str, Any], timeout_ms: int = 5000,
               store: str = "MONGODB", namespace: str | None = None) -> dict[str, Any]:
        full = {"kind": kind, **query}
        if namespace is None:
            namespace = self.namespaces["mongodbDatabase"] if store == "MONGODB" else \
                self.namespaces["qdrantSemanticCollection"] if store == "QDRANT_SEMANTIC" else \
                self.namespaces["qdrantEpisodicCollection"]
        receipt = self._raw("STORE", {"kind": kind, "store": store, "namespace": namespace,
                                      "querySha256": c.sha_bytes(c.canonical_bytes(full))},
                            lambda: self.ports.store_query(store, namespace, full, timeout_ms))
        if not isinstance(receipt.get("documents"), list):
            raise c.HarnessFailure("store raw document list required")
        return receipt

    def _workspace(self, role: str) -> str:
        path = self.workspace_paths[role]
        if path not in self.owned_workspaces:
            action = "CREATE_EMPTY_DIRECTORY" if role == "EMPTY_UNCAPTURED" \
                else "CREATE_CAPTURE_WORKSPACE"
            options = {"exclusive": True} if role == "EMPTY_UNCAPTURED" else {
                "exclusive": True, "hookSource": self.hook_source}
            receipt = self._raw("FILESYSTEM", {"action": action, "path": path},
                                lambda: self.ports.filesystem_action(
                                    action, path, options))
            if receipt.get("path") != path:
                raise c.HarnessFailure("filesystem receipt path mismatch")
            self.owned_workspaces.add(path)
        return path

    def _ensure_stub(self) -> None:
        if not self.stub_started:
            receipt = self._process("START_STUB", ["candidate10-stub", "--real-model-calls=0"])
            if receipt.get("stubRunning") is not True:
                raise c.EnvironmentFailure("stub failed to become ready")
            self.stub_started = True

    def _ensure_seeded(self) -> None:
        if self.seeded:
            return
        corpus = c.load_object(c.CORPUS)["syntheticMemoryCorpus"]
        for value in corpus:
            argv = ["mvn", "-q", "-Dunit.excludedGroups=",
                    "-Dtest=Wp5eReplayPromotionIntegrationTest",
                    f"-Dsma.s2.memoryId={value['logicalId']}",
                    f"-Dsma.s2.partition={value['partition']}",
                    f"-Dsma.s2.state={value['state']}",
                    f"-Dsma.s2.eligible={'true' if value['eligible'] else 'false'}",
                    "-Dsma.s2.textBase64=" + base64.b64encode(value["literalText"].encode()).decode(),
                    "test"]
            receipt = self._raw("MAVEN", {"argv": argv, "cwd": self.maven_project},
                                lambda a=argv: self.ports.maven_subprocess(a, self.maven_project, 900000))
            if receipt.get("exitStatus") != 0 or receipt.get("cwd") != self.maven_project:
                raise c.EnvironmentFailure("seed promotion failed or used wrong Maven cwd")
        self.seeded = True

    def ensure_mode(self, mode: c.ServiceMode, scope: c.OperationScope) -> None:
        if self.injected_failure == "RECOVERY_FAILURE":
            pass
        receipt = self._process("TRANSITION_SERVICE", ["candidate10-service", mode.value])
        if receipt.get("serviceMode") != mode.value:
            raise c.HarnessFailure("service mode transition not observed")
        self.mode = mode
        if mode in {c.ServiceMode.CAPTURE_ONLY, c.ServiceMode.CAPTURE_AND_RETRIEVAL} \
                and self.excluded_workspace_keys:
            query_keys = [{"conversationId": key.conversation_id, "eventId": key.event_id}
                          for key in self.excluded_workspace_keys]
            captured = self._store("CAPTURE_EXACT", {"keys": query_keys})["documents"]
            excluded = self._store("UNCAPTURED_EVENTS", {"keys": query_keys})["documents"]
            if captured or len({(value["conversation_id"], value["event_id"])
                                for value in excluded}) != len(self.excluded_workspace_keys):
                raise c.SafetyFailure("empty-workspace event crossed the capture allowlist")

    def _create_conversation(self, role: str, workspace_role: str,
                             parent: str | None = None) -> str:
        workspace = self._workspace(workspace_role)
        _, settings_payload = self._http(
            "GET", self.protocol["settingsPath"], headers={"X-Expose-Secrets": "encrypted"})
        settings = settings_payload.get("agent_settings")
        if not isinstance(settings, dict) or not isinstance(settings.get("llm"), dict):
            raise c.HarnessFailure("OpenHands settings response lacked agent_settings.llm")
        settings = json.loads(json.dumps(settings))
        settings["llm"].update({
            "model": self.protocol["modelName"],
            "model_canonical_name": self.protocol["modelCanonicalName"],
            "base_url": f"http://127.0.0.1:{c.load_object(c.CONFIGURATION)['futureLiveIdentity']['runtimePorts']['stubPort']}/v1",
            "api_mode": "chat", "api_key": self.protocol["apiKeyLabel"],
            "native_tool_calling": True, "force_string_serializer": False,
            "stream": False, "temperature": 0, "max_output_tokens": 64,
            "num_retries": 0, "retry_multiplier": 0,
            "retry_min_wait": 0, "retry_max_wait": 0, "timeout": 1,
            "log_completions": False,
        })
        settings["tools"] = []
        settings["mcp_config"] = {"mcpServers": {}}
        _, hooks_payload = self._http("POST", self.protocol["hooksPath"], {
            "project_dir": workspace})
        hook_config = hooks_payload.get("hook_config")
        if not isinstance(hook_config, dict) \
                or len(hook_config.get("user_prompt_submit", [])) != 1:
            raise c.HarnessFailure("exact UserPromptSubmit hook was not discovered")
        request: dict[str, Any] = {
            "agent_settings": settings, "secrets_encrypted": False,
            "workspace": {"kind": "LocalWorkspace", "working_dir": workspace},
            "worktree": False, "max_iterations": 1, "autotitle": False,
            "hook_config": hook_config,
        }
        if parent is not None:
            request["parent_conversation_id"] = parent
        _, payload = self._http("POST", "/api/conversations", request,
                                expected=(200, 201), timeout_ms=90000)
        conversation = payload.get("id")
        if not isinstance(conversation, str) or not conversation:
            raise c.HarnessFailure("conversation identity absent")
        self.owned_conversations.add(conversation)
        self.conversation_metadata[conversation] = {
            "role": role, "workspaceRole": workspace_role, "workspacePath": workspace,
            "profile": self.protocol["modelName"], "parentId": parent,
        }
        return conversation

    def _direct_context(self, conversation: str, workspace_role: str,
                        prompt: str) -> dict[str, Any]:
        _, payload = self._http(
            "POST", "/v1/openhands/context", {
                "session_id": conversation,
                "working_dir": self.workspace_paths[workspace_role],
                "prompt": prompt,
            }, endpoint="BRIDGE", timeout_ms=2000)
        if payload.get("session_id") != conversation \
                or payload.get("working_dir") != self.workspace_paths[workspace_role]:
            raise c.HarnessFailure("bridge context identity mismatch")
        context = str(payload.get("context_block") or "")
        memory_ids = payload.get("memory_ids")
        if not isinstance(memory_ids, list) \
                or memory_ids != c.parse_context(context, self.framing).memory_ids:
            raise c.HarnessFailure("bridge context memory identity mismatch")
        return {"partition": payload.get("partition"), "context": context,
                "memoryIds": memory_ids}

    def _create_source_event(self, conversation: str, text: str) -> c.EventKey:
        self._http("POST", f"/api/conversations/{conversation}/events", {
            "role": "user", "run": False,
            "content": [{"type": "text", "text": text}],
        }, timeout_ms=30000)
        _, payload = self._http(
            "GET", f"/api/conversations/{conversation}{self.protocol['eventSearchSuffix']}")
        events = payload.get("items")
        if not isinstance(events, list):
            raise c.HarnessFailure("source-event search returned no items")
        matches = [value for value in events if value.get("kind") == "MessageEvent"
                   and value.get("source") == "user" and self._event_text(value) == text]
        if len(matches) != 1 or not matches[0].get("id"):
            raise c.HarnessFailure("exact source event identity was not observed")
        return c.EventKey(conversation, str(matches[0]["id"]))

    @staticmethod
    def _event_text(event: dict[str, Any]) -> str:
        if isinstance(event.get("text"), str):
            return str(event["text"])
        message = event.get("llm_message") or {}
        content = message.get("content") if isinstance(message, dict) else None
        if isinstance(content, str):
            return content
        if isinstance(content, list):
            return "".join(str(value.get("text", "")) for value in content
                           if isinstance(value, dict) and value.get("type") == "text")
        return ""

    def _observe_interaction(self, item: c.OperationItem, conversation: str,
                             prompt: str, workspace_role: str, mode: str,
                             interaction_index: int) -> dict[str, Any]:
        submitted_ns = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
        self._http("POST", f"/api/conversations/{conversation}/events", {
            "role": "user", "run": True,
            "content": [{"type": "text", "text": prompt}],
        }, timeout_ms=30000)
        events_payload: dict[str, Any] = {}
        detail_payload: dict[str, Any] = {}
        raw_documents: list[dict[str, Any]] = []
        terminal_documents: list[dict[str, Any]] = []
        deadline_ns = submitted_ns + 120_000_000_000
        while True:
            _, events_payload = self._http(
                "GET", f"/api/conversations/{conversation}{self.protocol['eventSearchSuffix']}")
            _, detail_payload = self._http("GET", f"/api/conversations/{conversation}")
            raw_documents = self._store("STUB_RAW", {
                "caseId": item.case_id, "repetition": item.repetition,
                "conversationId": conversation,
                "interactionIndex": interaction_index})["documents"]
            terminal_documents = self._store("STUB_TERMINAL", {
                "requestIds": [value["requestId"] for value in raw_documents]})["documents"]
            terminal = detail_payload.get("execution_status") in set(self.protocol["terminalStates"])
            expected_raw = 0 if mode == "TRANSPORT_FAILURE_UNUSED_PORT" else 1
            if terminal and len(raw_documents) == len(terminal_documents) == expected_raw:
                break
            now_ns = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
            if now_ns >= deadline_ns:
                raise c.EnvironmentFailure("bounded interaction observation deadline expired")
            self._raw("CLOCK", {"action": "SLEEP", "durationNs": 50_000_000},
                      lambda: self.ports.sleep_ns(50_000_000))
        stable_fetches: list[dict[str, Any]] = []
        for _ in range(2):
            _, detail = self._http("GET", f"/api/conversations/{conversation}")
            stable_fetches.append(detail)
        if len({value.get("execution_status") for value in stable_fetches}) != 1:
            raise c.HarnessFailure("OpenHands terminal was not stable across two fetches")
        events = events_payload.get("items")
        if not isinstance(events, list):
            raise c.HarnessFailure("event list missing")
        user = next((value for value in events if value.get("kind") == "MessageEvent"
                     and value.get("source") == "user" and self._event_text(value) == prompt), None)
        hook = next((value for value in events if value.get("kind") == "HookExecutionEvent"
                     and (value.get("hook_input") or {}).get("message") == prompt), None)
        if user is None or hook is None:
            raise c.HarnessFailure("exact user/hook event identity absent")
        context = str(hook.get("additional_context") or "")
        persisted_context = str(user.get("extended_content") or "")
        selected = c.parse_context(context, self.framing)
        provenance = self._store("MEMORY_PROVENANCE", {"memoryIds": selected.memory_ids})["documents"]
        semantic_projection = self._store("PROJECTION_STATE", {"memoryIds": selected.memory_ids},
                                          store="QDRANT_SEMANTIC")["documents"]
        episodic_projection = self._store("PROJECTION_STATE", {"memoryIds": selected.memory_ids},
                                          store="QDRANT_EPISODIC")["documents"]
        request_receipts: list[dict[str, Any]] = []
        terminal_receipts: list[dict[str, Any]] = []
        request_oracles: list[dict[str, Any]] = []
        for raw in raw_documents:
            request_body = raw["requestBody"]
            try:
                request_payload = json.loads(request_body)
            except (UnicodeDecodeError, json.JSONDecodeError) as error:
                raise c.HarnessFailure("sealed stub request body is malformed JSON") from error
            messages = request_payload.get("messages") if isinstance(request_payload, dict) else None
            user_messages = [value for value in messages or []
                             if isinstance(value, dict) and value.get("role") == "user"]
            content = user_messages[-1].get("content") if user_messages else None
            if not isinstance(content, list) or not content or not isinstance(content[0], dict):
                raise c.HarnessFailure("model request did not retain list-serialized user content")
            segment0 = content[0].get("text")
            segment1 = content[1].get("text") if len(content) > 1 and isinstance(content[1], dict) else ""
            request_oracles.append({"segment0": segment0, "segment1": segment1,
                                    "segmentCount": len(content),
                                    "flattened": any(isinstance(value.get("content"), str)
                                                     for value in user_messages),
                                    "bodySha256": c.sha_bytes(request_body),
                                    "bodyLength": len(request_body)})
            request_receipts.append({
                "requestId": raw["requestId"], "conversationId": conversation,
                "interactionIndex": interaction_index,
                "bodySha256": c.sha_bytes(request_body), "bodyLength": len(request_body),
                "mode": mode, "receivedMonotonicNs": raw["receivedMonotonicNs"],
            })
        for terminal in terminal_documents:
            response = terminal["responseBody"]
            terminal_receipts.append({
                "requestId": terminal["requestId"], "conversationId": conversation,
                "responseSha256": c.sha_bytes(response), "responseLength": len(response),
                "responseWriteOutcome": terminal["responseWriteOutcome"],
                "completedMonotonicNs": terminal["completedMonotonicNs"],
            })
        prompt_bytes = prompt.encode()
        context_bytes = context.encode()
        prompt_boundary_bytes = {"submission": prompt_bytes,
                                 "persisted": str(user.get("text", "")).encode(),
                                 "hook": str((hook.get("hook_input") or {}).get("message", "")).encode()}
        context_boundary_bytes = {"hook": context_bytes,
                                  "persisted": persisted_context.encode()}
        if request_oracles:
            prompt_boundary_bytes["model"] = str(request_oracles[0]["segment0"]).encode()
            context_boundary_bytes["model"] = str(request_oracles[0]["segment1"]).encode()
        completed_ns = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
        return {
            "conversationId": conversation, "workspaceRole": workspace_role,
            "prompt": prompt, "context": context, "persistedContext": persisted_context,
            "promptBoundaryBytes": prompt_boundary_bytes,
            "contextBoundaryBytes": context_boundary_bytes,
            "requestOracles": request_oracles,
            "transportFailureBeforeModel": not request_oracles,
            "memoryProvenance": provenance,
            "projectionEvidence": {"semantic": semantic_projection,
                                   "episodic": episodic_projection},
            "requestReceipts": request_receipts, "terminalReceipts": terminal_receipts,
            "userEvent": user, "hookEvent": hook, "events": events,
            "terminalStatus": stable_fetches[-1].get("execution_status"),
            "submittedNs": submitted_ns, "completedNs": completed_ns,
        }

    def _one_prompt(self, item: c.OperationItem, role: str, workspace_role: str,
                    *, parent: str | None = None, prompt_suffix: str = "",
                    mode: str | None = None, existing: str | None = None,
                    interaction_index: int = 1) -> dict[str, Any]:
        self._ensure_stub()
        conversation = existing or self._create_conversation(role, workspace_role, parent)
        prompt = c.control_prompt(item) + prompt_suffix
        return self._observe_interaction(item, conversation, prompt, workspace_role,
                                         mode or item.mode, interaction_index)

    def _capture_key(self, interaction: dict[str, Any], source: str) -> c.EventKey:
        event = interaction["userEvent"] if source == "user" else next(
            value for value in interaction["events"]
            if value.get("kind") == "MessageEvent" and value.get("source") == "agent")
        return c.EventKey(interaction["conversationId"], str(event["id"]))

    def _stable_capture_counts(self, allowed: list[c.EventKey],
                               forbidden: list[c.EventKey]) -> tuple[dict[tuple[str, str], int],
                                                                    dict[tuple[str, str], int]]:
        keys = allowed + forbidden
        if not keys:
            return {}, {}
        query_keys = [{"conversationId": key.conversation_id, "eventId": key.event_id}
                      for key in keys]
        samples: list[dict[tuple[str, str], int]] = []
        deadline_ns = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns) \
            + 120_000_000_000
        stable = False
        while True:
            documents = self._store("CAPTURE_EXACT", {"keys": query_keys})["documents"]
            sample: dict[tuple[str, str], int] = {}
            for document in documents:
                key = (str(document["conversation_id"]), str(document["event_id"]))
                sample[key] = sample.get(key, 0) + 1
            samples.append(sample)
            stable = c.exact_capture_stabilized(
                samples, allowed, forbidden, consecutive_reads=2)
            if stable:
                break
            now_ns = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
            if now_ns >= deadline_ns:
                raise c.HarnessFailure("exact capture stabilization deadline expired")
            self._raw("CLOCK", {"action": "SLEEP", "durationNs": 50_000_000},
                      lambda: self.ports.sleep_ns(50_000_000))
        if not stable:
            raise c.HarnessFailure("exact capture keys did not stabilize across two reads")
        final = samples[-1]
        return ({key.value(): final.get(key.value(), 0) for key in allowed},
                {key.value(): final.get(key.value(), 0) for key in forbidden})

    def _build_observation(self, scope: c.OperationScope, interactions: list[dict[str, Any]],
                           values: dict[str, Any], exact: dict[tuple[str, str], int],
                           forbidden: dict[tuple[str, str], int],
                           action_record: c.JournalRecord) -> c.Observation:
        item = scope.item
        last = interactions[-1]
        requests = [receipt for value in interactions for receipt in value["requestReceipts"]]
        terminals = [receipt for value in interactions for receipt in value["terminalReceipts"]]
        prompt_hashes = [c.sha_bytes(raw) for value in interactions
                         for raw in value["promptBoundaryBytes"].values()]
        selected: list[dict[str, Any]] = []
        for interaction in interactions:
            for document in interaction["memoryProvenance"]:
                selected.append({"memoryId": document["memory_id"],
                                 "traceId": document["trace_id"],
                                 "partition": document["partition"]})
        event_sequence: list[dict[str, Any]] = []
        seen_events: set[tuple[str, str]] = set()
        terminal_events: list[dict[str, Any]] = []
        seen_terminal_events: set[tuple[str, str]] = set()
        for interaction in interactions:
            conversation_id = interaction["conversationId"]
            user_sequence = int(interaction["userEvent"].get("sequence", 0))
            for index, event in enumerate(interaction["events"], 1):
                if not event.get("id"):
                    continue
                identity = (conversation_id, str(event["id"]))
                if identity not in seen_events:
                    seen_events.add(identity)
                    event_sequence.append({
                        "eventId": str(event["id"]),
                        "sequence": int(event.get("sequence", index)),
                        "conversationId": conversation_id,
                        "kind": event.get("kind"), "source": event.get("source"),
                    })
                if int(event.get("sequence", 0)) > user_sequence \
                        and (event.get("kind") == "ConversationErrorEvent"
                             or (event.get("kind") == "MessageEvent"
                                 and event.get("source") == "agent")):
                    terminal_identity = (conversation_id, str(event["id"]))
                    if terminal_identity not in seen_terminal_events:
                        seen_terminal_events.add(terminal_identity)
                        terminal_events.append({"conversationId": conversation_id,
                                                "eventId": str(event["id"]),
                                                "kind": event.get("kind"),
                                                "sequence": int(event.get("sequence", 0))})
        conversation_evidence = []
        for role, conversation_id in scope.conversation_roles.items():
            metadata = self.conversation_metadata[conversation_id]
            conversation_evidence.append({
                "role": role, "conversationId": conversation_id,
                "workspaceRole": metadata["workspaceRole"],
                "workspacePath": metadata["workspacePath"],
                "profile": metadata["profile"], "parentId": metadata["parentId"],
                "childIds": sorted(value for value, child in self.conversation_metadata.items()
                                   if child.get("parentId") == conversation_id),
            })
        identity_evidence = {
            "conversations": conversation_evidence,
            "events": event_sequence,
            "hooks": [{"conversationId": value["conversationId"],
                       "eventId": str(value["hookEvent"]["id"])} for value in interactions],
            "terminals": [{"requestId": value.get("requestId"),
                           "conversationId": value.get("conversationId"),
                           "outcome": value.get("responseWriteOutcome")}
                          for value in terminals],
            "terminalEvents": terminal_events,
        }
        elapsed = max(value["completedNs"] for value in interactions) \
            - min(value["submittedNs"] for value in interactions)
        observation = c.Observation(
            item=item, prompt_hashes=prompt_hashes, context=last["context"],
            persisted_context=last["persistedContext"], request_count=len(requests),
            terminal_count=len(terminals),
            response_outcomes=[value["responseWriteOutcome"] for value in terminals],
            identity_fields={"conversation_id": ",".join(scope.conversation_roles.values()),
                             "user_event_id": str(last["userEvent"]["id"]),
                             "hook_event_id": str(last["hookEvent"]["id"]),
                             "workspace": ",".join(sorted(
                                 self.conversation_metadata[value]["workspacePath"]
                                 for value in scope.conversation_roles.values())),
                             "profile": self.protocol["modelName"],
                             "sequence": ",".join(str(value["sequence"])
                                                  for value in event_sequence),
                             "event_ordinals": ",".join(str(value["eventId"]) for value in event_sequence)},
            elapsed_ns=elapsed, raw_received_ns=sorted(value["receivedMonotonicNs"] for value in requests),
            case_values=values, exact_capture_cardinality=exact,
            forbidden_capture_cardinality=forbidden,
            fault_timing_complete=bool(values.get("fault_timing_complete"))
            if item.operation in {"MODEL_STUB_FAULT_MATRIX",
                                  "ACTIVE_CANCELLATION_AND_SHUTDOWN"} else
            all(value["submittedNs"] > 0 and value["completedNs"] >= value["submittedNs"]
                for value in interactions),
            terminal_observed=all(value["terminalStatus"] in set(self.protocol["terminalStates"])
                                  for value in interactions),
            owned_conversations=dict(scope.conversation_roles),
            prompt_boundary_bytes=last["promptBoundaryBytes"],
            context_boundary_bytes=last["contextBoundaryBytes"],
            prompt_boundaries={name: {"sha256": c.sha_bytes(raw), "length": len(raw)}
                               for name, raw in last["promptBoundaryBytes"].items()},
            context_boundaries={name: {"sha256": c.sha_bytes(raw), "length": len(raw)}
                                for name, raw in last["contextBoundaryBytes"].items()},
            selected_memory_provenance=selected, stub_requests=requests, stub_terminals=terminals,
            event_sequence=event_sequence,
            fault_activation_ns=int(values.get(
                "fault_activation_ns", min(value["submittedNs"] for value in interactions))),
            fault_observed_ns=int(values.get(
                "fault_observed_ns", max(value["completedNs"] for value in interactions))),
            append_action_record_id=action_record.record_id,
            append_action_sequence=action_record.sequence,
            interactions=interactions,
            identity_evidence=identity_evidence,
            timing_evidence={key: values.get(key) for key in (
                "fault_timing_complete", "fault_activation_ns", "fault_observed_ns",
                "retry_count", "activated_mode", "future_terminal",
                "interrupt_after_raw_ns",
                "future_completion_ns", "stub_disconnect_ns", "client_disconnected",
                "owned_shutdown_ns") if key in values},
        )
        return observation

    def perform(self, scope: c.OperationScope, action_record: c.JournalRecord) -> c.Observation:
        item = scope.item
        operation = item.operation
        seed_required = operation not in {"FIRST_PROMPT_EMPTY", "RETRIEVAL_OUTAGE",
                                          "CAPTURE_OUTAGE_RECOVERY", "HOOK_FAULT_MATRIX"}
        if seed_required:
            self._ensure_seeded()
        if operation == "HOOK_FAULT_MATRIX":
            self._process("CONFIGURE_BRIDGE_FAULT", ["candidate10-bridge-fault", item.mode])
        interactions: list[dict[str, Any]] = []
        exact: dict[tuple[str, str], int] = {}
        forbidden: dict[tuple[str, str], int] = {}
        values: dict[str, Any] = {}
        fixture_promotion_links: list[dict[str, str]] = []
        capture_recovery_started_ns: int | None = None

        def bind(role: str, key: c.EventKey, count: int = 1) -> None:
            scope.source_event_roles.setdefault(role, []).append(key)
            scope.allowed_source_keys.append(key)
            exact[key.value()] = count

        if operation == "PARENT_CHILD_PROVENANCE":
            parent = self._create_conversation("parent", "ACTOR_ALPHA")
            scope.conversation_roles["parent"] = parent
            child = self._create_conversation("child", "ACTOR_ALPHA", parent)
            scope.conversation_roles["child"] = child
            child_interaction = self._one_prompt(item, "child", "ACTOR_ALPHA", existing=child,
                                                 mode="SUCCESS", interaction_index=1)
            interactions = [child_interaction]
            for role, interaction, source in (("child.user", child_interaction, "user"),
                                              ("child.agent", child_interaction, "agent")):
                bind(role, self._capture_key(interaction, source))
            _, parent_detail = self._http("GET", f"/api/conversations/{parent}")
            _, child_detail = self._http("GET", f"/api/conversations/{child}")
            child_agent = next(value for value in child_interaction["events"]
                               if value.get("kind") == "MessageEvent"
                               and value.get("source") == "agent")
            values = {"lineage_complete": child in parent_detail.get("sub_conversation_ids", [])
                      and child_detail.get("parent_conversation_id") == parent,
                      "sequence_complete": len(scope.allowed_source_keys) == 2
                      and int(child_interaction["userEvent"]["sequence"])
                      < int(child_agent["sequence"]),
                      "partition_isolated": False}
        elif operation == "FOUR_CHANNEL_CONCURRENCY":
            roles = (("alpha-1", "ACTOR_ALPHA"), ("alpha-2", "ACTOR_ALPHA"),
                     ("beta-1", "ACTOR_BETA"), ("beta-2", "ACTOR_BETA"))
            scheduled: list[tuple[int, str, str, str]] = []
            for index, (role, workspace) in enumerate(roles, 1):
                conversation = self._create_conversation(role, workspace)
                scope.conversation_roles[role] = conversation
                scheduled.append((index, role, workspace, conversation))
            def invoke(entry: tuple[int, str, str, str]) -> dict[str, Any]:
                index, role, workspace, conversation = entry
                return self._one_prompt(item, role, workspace, existing=conversation,
                                        prompt_suffix=f" Channel {index}.",
                                        interaction_index=index)
            with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
                interactions = list(executor.map(invoke, scheduled))
            for (index, (role, workspace)), interaction in zip(enumerate(roles, 1), interactions, strict=True):
                bind(f"{role}.user", self._capture_key(interaction, "user"))
                bind(f"{role}.agent", self._capture_key(interaction, "agent"))
            parsed = [c.parse_context(value["context"], self.framing).memory_ids
                      for value in interactions]
            active_documents = self._store("ACTIVE_WORK", {})["documents"]
            process_inventory = self._process("INVENTORY", ["candidate10-inventory"])
            if len(active_documents) != 1:
                raise c.HarnessFailure("four-channel active-work receipt cardinality mismatch")
            values = {"terminal_count": sum(len(value["terminalReceipts"]) for value in interactions),
                      "all_within_bound": all(value["completedNs"] - value["submittedNs"]
                                              <= 120_000_000_000 for value in interactions),
                      "all_partitions_isolated": all(
                          (ids == ["mem-alpha-timeout"] if index < 2 else ids == ["mem-beta-port"])
                          for index, ids in enumerate(parsed)),
                      "telemetry_complete": all(key in active_documents[0]
                                                for key in ("active", "queued", "queueDepth",
                                                            "timeoutCount", "rejectionCount",
                                                            "resourceSnapshot"))
                      and all(key in process_inventory
                              for key in ("activeWork", "queuedWork", "ownedChildProcessCount")),
                      "active": int(active_documents[0]["active"]),
                      "queued": int(active_documents[0]["queued"]),
                      "process_active": int(process_inventory["activeWork"]),
                      "process_queued": int(process_inventory["queuedWork"]),
                      "owned_child_processes": int(process_inventory["ownedChildProcessCount"]),
                      "queue_depth": int(active_documents[0].get("queueDepth", -1)),
                      "timeout_count": int(active_documents[0].get("timeoutCount", -1)),
                      "rejection_count": int(active_documents[0].get("rejectionCount", -1)),
                      "resource_snapshot_complete": isinstance(
                          active_documents[0].get("resourceSnapshot"), dict)
                      and set(active_documents[0].get("resourceSnapshot", {}))
                      == {"ownedWorkers", "openRequests"}}
        elif operation == "CONDENSATION_REANCHOR":
            conversation = self._create_conversation("primary", "ACTOR_ALPHA")
            scope.conversation_roles["primary"] = conversation
            first = self._one_prompt(item, "primary", "ACTOR_ALPHA", existing=conversation,
                                     interaction_index=1)
            condense_receipt, _ = self._http("POST", f"/api/conversations/{conversation}/condense",
                                             expected=(200, 204), timeout_ms=120000)
            second = self._one_prompt(item, "primary", "ACTOR_ALPHA", existing=conversation,
                                      prompt_suffix=" Re-anchor after condensation.", interaction_index=2)
            interactions = [first, second]
            ids = c.parse_context(second["context"], self.framing).memory_ids
            summary_documents = self._store("SUMMARY_EVENTS", {
                "conversationId": conversation})["documents"]
            summary_ids = {str(value.get("id")) for value in summary_documents
                           if value.get("id")}
            delivered_memory_ids = {memory_id for value in interactions
                                    for memory_id in c.parse_context(
                                        value["context"], self.framing).memory_ids}
            values = {"reanchored": "mem-alpha-timeout" in ids,
                      "summary_count": len(summary_documents),
                      "summary_identity_complete": len(summary_documents) == 1
                      and summary_documents[0].get("kind") == "SummaryEvent"
                      and summary_documents[0].get("conversationId") == conversation
                      and bool(summary_documents[0].get("id"))
                      and type(summary_documents[0].get("sequence")) is int,
                      "summary_identity_absent_from_delivery": summary_ids.isdisjoint(
                          delivered_memory_ids),
                      "summary_used_as_delivery_state": any(
                          value.get("usedAsDeliveryState") is True for value in summary_documents),
                      "condense_status": condense_receipt["status"],
                      "terminal_received": len(second["terminalReceipts"]) == 1}
        elif operation == "ACTIVE_CANCELLATION_AND_SHUTDOWN":
            conversation = self._create_conversation("primary", "ACTOR_ALPHA")
            scope.conversation_roles["primary"] = conversation
            self._ensure_stub()
            prompt = c.control_prompt(item)
            submitted = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
            self._http("POST", f"/api/conversations/{conversation}/events", {
                "role": "user", "run": True,
                "content": [{"type": "text", "text": prompt}]})
            raws = self._store("STUB_RAW", {"caseId": item.case_id,
                                            "repetition": item.repetition,
                                            "conversationId": conversation})["documents"]
            if len(raws) != 1:
                raise c.HarnessFailure("cancellation raw request not observed exactly once")
            self._raw("CLOCK", {"action": "SLEEP", "durationNs": 250_000_000},
                      lambda: self.ports.sleep_ns(250_000_000))
            interrupt_start = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
            self._http("POST", f"/api/conversations/{conversation}/interrupt", timeout_ms=10000)
            terminal_docs: list[dict[str, Any]] = []
            terminal_deadline = interrupt_start + 10_000_000_000
            while True:
                terminal_docs = self._store("STUB_TERMINAL", {
                    "requestIds": [raws[0]["requestId"]]})["documents"]
                if len(terminal_docs) == 1:
                    break
                now_ns = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
                if now_ns >= terminal_deadline:
                    raise c.EnvironmentFailure("cancellation terminal deadline expired")
                self._raw("CLOCK", {"action": "SLEEP", "durationNs": 50_000_000},
                          lambda: self.ports.sleep_ns(50_000_000))
            completed = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
            _, events_payload = self._http(
                "GET", f"/api/conversations/{conversation}{self.protocol['eventSearchSuffix']}")
            _, detail = self._http("GET", f"/api/conversations/{conversation}")
            events = events_payload["items"]
            hook = next(value for value in events if value.get("kind") == "HookExecutionEvent")
            user = next(value for value in events if value.get("kind") == "MessageEvent"
                        and value.get("source") == "user")
            context = str(hook.get("additional_context") or "")
            context_receipt = c.parse_context(context, self.framing)
            provenance = self._store("MEMORY_PROVENANCE", {
                "memoryIds": context_receipt.memory_ids})["documents"]
            semantic_projection = self._store("PROJECTION_STATE", {
                "memoryIds": context_receipt.memory_ids}, store="QDRANT_SEMANTIC")["documents"]
            episodic_projection = self._store("PROJECTION_STATE", {
                "memoryIds": context_receipt.memory_ids}, store="QDRANT_EPISODIC")["documents"]
            request_payload = json.loads(raws[0]["requestBody"])
            request_content = [value for value in request_payload["messages"]
                               if value.get("role") == "user"][-1]["content"]
            model_prompt = str(request_content[0]["text"])
            model_context = str(request_content[1]["text"]) if len(request_content) > 1 else ""
            interaction = {"conversationId": conversation, "workspaceRole": "ACTOR_ALPHA",
                           "prompt": prompt, "context": context,
                           "persistedContext": str(user.get("extended_content") or ""),
                           "promptBoundaryBytes": {"submission": prompt.encode(),
                                                   "persisted": str(user["text"]).encode(),
                                                   "hook": str(hook["hook_input"]["message"]).encode(),
                                                   "model": model_prompt.encode()},
                           "contextBoundaryBytes": {"hook": context.encode(),
                                                    "persisted": str(user.get("extended_content") or "").encode(),
                                                    "model": model_context.encode()},
                           "requestOracles": [{"segment0": model_prompt, "segment1": model_context,
                                               "segmentCount": len(request_content), "flattened": False,
                                               "bodySha256": c.sha_bytes(raws[0]["requestBody"]),
                                               "bodyLength": len(raws[0]["requestBody"])}],
                           "transportFailureBeforeModel": False,
                           "memoryProvenance": provenance,
                           "projectionEvidence": {"semantic": semantic_projection,
                                                  "episodic": episodic_projection},
                           "requestReceipts": [{"requestId": raws[0]["requestId"],
                                                "conversationId": conversation,
                                                "interactionIndex": 1,
                                                "bodySha256": c.sha_bytes(raws[0]["requestBody"]),
                                                "bodyLength": len(raws[0]["requestBody"]), "mode": "TIMEOUT",
                                                "receivedMonotonicNs": raws[0]["receivedMonotonicNs"]}],
                           "terminalReceipts": [{"requestId": value["requestId"],
                                                 "conversationId": conversation,
                                                 "responseSha256": c.sha_bytes(value["responseBody"]),
                                                 "responseLength": len(value["responseBody"]),
                                                 "responseWriteOutcome": value["responseWriteOutcome"],
                                                 "completedMonotonicNs": value["completedMonotonicNs"]}
                                                for value in terminal_docs],
                           "userEvent": user, "hookEvent": hook, "events": events,
                           "terminalStatus": detail["execution_status"],
                           "submittedNs": submitted, "completedNs": completed}
            interactions = [interaction]
            self._http("DELETE", f"/api/conversations/{conversation}")
            self.owned_conversations.discard(conversation)
            workspace_path = self.workspace_paths["ACTOR_ALPHA"]
            self._raw("FILESYSTEM", {"action": "REMOVE_OWNED_TREE", "path": workspace_path},
                      lambda: self.ports.filesystem_action("REMOVE_OWNED_TREE", workspace_path, {}))
            self.owned_workspaces.discard(workspace_path)
            workspace_inspection = self._raw(
                "FILESYSTEM", {"action": "INSPECT", "path": workspace_path},
                lambda: self.ports.filesystem_action("INSPECT", workspace_path, {}))
            self._process("STOP_STUB", ["candidate10-stub"])
            self.stub_started = False
            self._process("CLEANUP_OPERATION_NAMESPACES", ["candidate10-operation-cleanup"])
            self.seeded = False
            active_documents = self._store("ACTIVE_WORK", {})["documents"]
            process_inventory = self._process("INVENTORY", ["candidate10-inventory"])
            mongo_inventory = self._store("INVENTORY", {}, store="MONGODB")["documents"]
            semantic_inventory = self._store("INVENTORY", {}, store="QDRANT_SEMANTIC")["documents"]
            episodic_inventory = self._store("INVENTORY", {}, store="QDRANT_EPISODIC")["documents"]
            namespace_absent = all(len(values_) == 1 and not values_[0].get("namespacePresent")
                                   for values_ in (mongo_inventory, semantic_inventory,
                                                   episodic_inventory))
            values = {"interrupt_after_raw_ns": interrupt_start - raws[0]["receivedMonotonicNs"],
                      "future_completion_ns": completed - interrupt_start,
                      "stub_disconnect_ns": completed - interrupt_start,
                      "client_disconnected": len(terminal_docs) == 1
                      and terminal_docs[0]["responseWriteOutcome"] == "CLIENT_DISCONNECTED",
                      "owned_shutdown_ns": completed - interrupt_start,
                      "fault_timing_complete": submitted > 0 and interrupt_start >= submitted
                      and completed >= interrupt_start,
                      "fault_activation_ns": interrupt_start,
                      "fault_observed_ns": completed,
                      "owned_work_zero": len(active_documents) == 1
                      and active_documents[0].get("active") == active_documents[0].get("queued") == 0
                      and process_inventory.get("activeWork") == process_inventory.get("queuedWork") == 0
                      and process_inventory.get("ownedChildProcessCount") == 0,
                      "repetition_cleanup_complete": detail["execution_status"] == "stopped"
                      and len(terminal_docs) == 1
                      and workspace_inspection.get("exists") is False
                      and len(mongo_inventory) == 1
                      and mongo_inventory[0].get("conversationCount") == 0
                      and namespace_absent and process_inventory.get("stubRunning") is False,
                      "conversation_absent": len(mongo_inventory) == 1
                      and mongo_inventory[0].get("conversationCount") == 0,
                      "workspace_absent": workspace_inspection.get("exists") is False,
                      "namespace_absent": namespace_absent,
                      "stub_absent": process_inventory.get("stubRunning") is False}
        else:
            workspace = "ACTOR_BETA" if operation in {"CROSS_PARTITION_DENIAL", "EMPTY_RESULT"} \
                else "EMPTY_UNCAPTURED" if operation == "FIRST_PROMPT_EMPTY" else "ACTOR_ALPHA"
            conversation = self._create_conversation("primary", workspace)
            scope.conversation_roles["primary"] = conversation
            if operation == "OVERSIZED_CONTEXT":
                fixture_conversations: list[str] = []
                for index in range(1, 4):
                    fixture = self._create_conversation(f"fixture-{index}", "ACTOR_ALPHA")
                    scope.conversation_roles[f"fixture-{index}"] = fixture
                    fixture_conversations.append(fixture)
                    fixture_text = (f"Repository alpha timeout evidence copy {index}: "
                                    "API timeout is 17 seconds. " + "A" * 1800)
                    event_key = self._create_source_event(fixture, fixture_text)
                    bind(f"fixture-{index}.user", event_key)
                    fixture_promotion_links.append({
                        "conversationId": event_key.conversation_id,
                        "eventId": event_key.event_id,
                        "memoryId": f"oversized-{index}",
                    })
                transition = self._process("TRANSITION_SERVICE", ["candidate10-service",
                                                                   c.ServiceMode.CAPTURE_ONLY.value])
                if transition["serviceMode"] != c.ServiceMode.CAPTURE_ONLY.value:
                    raise c.HarnessFailure("fixture capture service transition failed")
                self._stable_capture_counts(scope.allowed_source_keys, [])
                for index in range(1, 4):
                    argv = ["mvn", "-q", "-Dtest=Wp5eReplayPromotionIntegrationTest",
                            f"-Dsma.s2.memoryId=oversized-{index}", "-Dsma.s2.partition=actor-alpha",
                            "-Dsma.s2.state=PROMOTED", "-Dsma.s2.eligible=true",
                            "-Dsma.s2.textBase64=" + base64.b64encode(
                                (f"Repository alpha timeout evidence copy {index}: API timeout is 17 seconds. "
                                 + "A" * 1800).encode()).decode(), "test"]
                    receipt = self._raw("MAVEN", {"argv": argv, "cwd": self.maven_project},
                                        lambda a=argv: self.ports.maven_subprocess(a, self.maven_project, 900000))
                    if receipt["exitStatus"] != 0:
                        raise c.EnvironmentFailure("oversized fixture promotion failed")
                self._process("TRANSITION_SERVICE", ["candidate10-service",
                                                      c.ServiceMode.RETRIEVAL_ONLY.value])
                self.mode = c.ServiceMode.RETRIEVAL_ONLY
            interaction = self._one_prompt(item, "primary", workspace, existing=conversation)
            interactions = [interaction]
            context = c.parse_context(interaction["context"], self.framing)
            if operation == "FIRST_PROMPT_EMPTY":
                for source in ("user", "agent"):
                    self.excluded_workspace_keys.append(self._capture_key(interaction, source))
            if operation in {"DUPLICATE_PERSISTED_EVENT", "CAPTURE_OUTAGE_RECOVERY",
                             "ADDITIONAL_CONTEXT_INTEGRITY", "FEEDBACK_LOOP_PREVENTION",
                             "SECRET_DELIVERY_ABSENCE", "EMPTY_RESULT"}:
                bind("primary.user", self._capture_key(interaction, "user"))
            if operation in {"ADDITIONAL_CONTEXT_INTEGRITY", "EMPTY_RESULT"}:
                bind("primary.agent", self._capture_key(interaction, "agent"))
            if operation == "RESTART_CONTINUITY":
                bind("primary.user", self._capture_key(interaction, "user"))
                bind("primary.agent", self._capture_key(interaction, "agent"))
            if operation == "FEEDBACK_LOOP_PREVENTION":
                kinds = (("hook", "HookExecutionEvent"), ("action", "ActionEvent"),
                         ("completion", "CompletionLogEvent"), ("reasoning", "ReasoningEvent"),
                         ("tool", "ToolEvent"), ("utility", "UtilityEvent"))
                for name, kind in kinds:
                    event = next(value for value in interaction["events"] if value.get("kind") == kind)
                    key = c.EventKey(conversation, str(event["id"]))
                    scope.forbidden_source_keys.append(key)
                    forbidden[key.value()] = 0
            values = self._derived_case_values(item, interaction, context, scope, exact, forbidden)
            if operation == "OVERSIZED_CONTEXT":
                values["fixture_promotion_links"] = fixture_promotion_links
            if operation == "DUPLICATE_PERSISTED_EVENT":
                initial_exact, initial_forbidden = self._stable_capture_counts(
                    scope.allowed_source_keys, scope.forbidden_source_keys)
                values["initial_identity_seen"] = all(value == 1 for value in initial_exact.values())
                values["initial_capture_cardinality"] = next(iter(initial_exact.values()), 0)
                if initial_forbidden:
                    raise c.HarnessFailure("unexpected forbidden capture during initial duplicate check")
            if operation == "RESTART_CONTINUITY":
                pre_restart = self._direct_context(
                    conversation, "ACTOR_ALPHA", c.control_prompt(item))
                initial_exact, _ = self._stable_capture_counts(
                    scope.allowed_source_keys, scope.forbidden_source_keys)
                values["initial_capture_count"] = len(initial_exact)
                values["partition_before"] = pre_restart.get("partition")
                values["pre_restart_memory_ids"] = pre_restart.get("memoryIds")
        if operation in {"CAPTURE_OUTAGE_RECOVERY", "PARENT_CHILD_PROVENANCE"}:
            if operation == "CAPTURE_OUTAGE_RECOVERY":
                capture_recovery_started_ns = self._raw(
                    "CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
            transition = self._process("TRANSITION_SERVICE", ["candidate10-service",
                                                               c.ServiceMode.CAPTURE_AND_RETRIEVAL.value])
            if transition["serviceMode"] != c.ServiceMode.CAPTURE_AND_RETRIEVAL.value:
                raise c.HarnessFailure("capture recovery transition failed")
            self.mode = c.ServiceMode.CAPTURE_AND_RETRIEVAL
        if operation in {"DUPLICATE_PERSISTED_EVENT", "RESTART_CONTINUITY"}:
            self._process("TRANSITION_SERVICE", ["candidate10-service", c.ServiceMode.OFF.value])
            transition = self._process("TRANSITION_SERVICE", ["candidate10-service",
                                                               c.ServiceMode.CAPTURE_AND_RETRIEVAL.value])
            if transition["serviceMode"] != c.ServiceMode.CAPTURE_AND_RETRIEVAL.value:
                raise c.HarnessFailure("restart reconciliation transition failed")
            self.mode = c.ServiceMode.CAPTURE_AND_RETRIEVAL
            if operation == "RESTART_CONTINUITY":
                post_restart = self._direct_context(
                    scope.conversation_roles["primary"], "ACTOR_ALPHA", c.control_prompt(item))
                values["partition_after"] = post_restart.get("partition")
                post_context = c.parse_context(str(post_restart.get("context", "")), self.framing)
                values["recall_correct_after"] = post_restart.get("memoryIds") == post_context.memory_ids \
                    and "mem-alpha-timeout" in post_context.memory_ids
        exact, forbidden = self._stable_capture_counts(scope.allowed_source_keys,
                                                       scope.forbidden_source_keys)
        if operation == "PARENT_CHILD_PROVENANCE":
            capture_documents = self._store("CAPTURE_EXACT", {"keys": [
                {"conversationId": key.conversation_id, "eventId": key.event_id}
                for key in scope.allowed_source_keys]})["documents"]
            provenance_partitions = {document.get("partition") for interaction in interactions
                                     for document in interaction["memoryProvenance"]}
            values["partition_isolated"] = len(capture_documents) == 2 \
                and {document.get("partition") for document in capture_documents} == {"actor-alpha"} \
                and provenance_partitions <= {"actor-alpha"}
        if operation == "DUPLICATE_PERSISTED_EVENT":
            values["reconciled_identity_seen"] = all(value == 1 for value in exact.values())
            values["post_restart_cardinality"] = next(iter(exact.values()), 0)
        elif operation == "CAPTURE_OUTAGE_RECOVERY":
            values["recursive_capture_count"] += sum(forbidden.values())
            if capture_recovery_started_ns is None:
                raise c.HarnessFailure("capture recovery activation receipt missing")
            stabilized_ns = self._raw(
                "CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
            values["recovery_ms"] = max(
                0, (stabilized_ns - capture_recovery_started_ns) // 1_000_000)
        elif operation == "FEEDBACK_LOOP_PREVENTION":
            recursive = self._store("RECURSIVE_CAPTURES", {
                "conversationId": scope.conversation_roles["primary"]})["documents"]
            values["eligible_capture_count"] = len(exact)
            values["recursive_capture_count"] = sum(forbidden.values()) + len(recursive)
        elif operation == "RESTART_CONTINUITY":
            values["final_capture_count"] = len(exact)
        scope.workspace_roles.update(scope.profile["ownedWorkspaceRoles"])
        observation = self._build_observation(scope, interactions, values, exact, forbidden,
                                              action_record)
        self._apply_mutations(scope, observation)
        return observation

    def _derived_case_values(self, item: c.OperationItem, interaction: dict[str, Any],
                             context: c.ContextReceipt, scope: c.OperationScope,
                             exact: dict[tuple[str, str], int],
                             forbidden: dict[tuple[str, str], int]) -> dict[str, Any]:
        operation = item.operation
        request_count = len(interaction["requestReceipts"])
        terminal_count = len(interaction["terminalReceipts"])
        if operation == "FIRST_PROMPT_EMPTY":
            return {"hook_before_user": interaction["events"].index(interaction["hookEvent"])
                                        < interaction["events"].index(interaction["userEvent"]),
                    "hook_installed": interaction["hookEvent"].get("success") is True}
        if operation == "SAME_PARTITION_DELIVERY":
            return {"expected_memory_id": "mem-alpha-timeout",
                    "untrusted_marker": "untrusted evidence" in interaction["context"]}
        if operation == "CROSS_PARTITION_DENIAL":
            surfaces = self._store("DELIVERY_SURFACES", {
                "conversationId": interaction["conversationId"]})["documents"]
            forbidden_markers = ("mem-alpha-timeout", "mem-alpha-adversarial", "mem-alpha-raw",
                                 "Repository alpha uses an API timeout of 17 seconds.",
                                 "DELETE_CONFIRMED", "Repository alpha secretly uses port 9999.")
            hits = sorted({marker for document in surfaces for raw in document.get("values", [])
                           for marker in forbidden_markers
                           if (isinstance(raw, bytes) and marker.encode() in raw)
                           or (isinstance(raw, str) and marker in raw)})
            required_surfaces = {"hook", "modelRequestBodies", "operationalLog",
                                 "serviceLog", "capture"}
            return {"forbidden_surface_hits": hits,
                    "surface_set_complete": required_surfaces <= {
                        str(value.get("surface")) for value in surfaces}}
        if operation == "UNTRUSTED_CONTEXT_PLACEMENT":
            return {"untrusted_marker": "untrusted evidence" in interaction["context"],
                    "context_segment_separate": interaction["prompt"] not in interaction["context"]}
        if operation == "RAW_INELIGIBLE_ABSENCE":
            surfaces = self._store("DELIVERY_SURFACES", {
                "conversationId": interaction["conversationId"]})["documents"]
            markers = ("mem-alpha-raw", "Repository alpha secretly uses port 9999.", "9999")
            hits = sorted({marker for document in surfaces for raw in document.get("values", [])
                           for marker in markers
                           if (isinstance(raw, bytes) and marker.encode() in raw)
                           or (isinstance(raw, str) and marker in raw)})
            required_surfaces = {"hook", "modelRequestBodies", "operationalLog",
                                 "serviceLog", "capture"}
            return {"raw_ineligible_surface_hits": hits,
                    "surface_set_complete": required_surfaces <= {
                        str(value.get("surface")) for value in surfaces}}
        if operation == "DUPLICATE_PERSISTED_EVENT":
            return {"initial_identity_seen": bool(exact), "reconciled_identity_seen": bool(exact),
                    "post_restart_cardinality": next(iter(exact.values()), 0)}
        if operation == "RETRIEVAL_OUTAGE":
            faults = self._store("BRIDGE_FAULT", {
                "faultKind": "RETRIEVAL_OUTAGE", "mode": "BRIDGE_DOWN",
                "caseId": item.case_id, "repetition": item.repetition})["documents"]
            return {"hook_fail_open": interaction["hookEvent"].get("success") is True,
                    "hook_result_count": sum(value.get("kind") == "HookExecutionEvent"
                                             for value in interaction["events"]),
                    "hook_deadline_ms": (interaction["completedNs"] - interaction["submittedNs"]) // 1_000_000,
                    "body_free_fault": len(faults) == 1
                    and faults[0].get("kind") == "RETRIEVAL_OUTAGE"
                    and faults[0].get("requestBodyLength") == 0
                    and faults[0].get("activatedMonotonicNs", 0)
                    <= faults[0].get("observedMonotonicNs", -1)}
        if operation == "CAPTURE_OUTAGE_RECOVERY":
            recursive = self._store("RECURSIVE_CAPTURES", {
                "conversationId": interaction["conversationId"]})["documents"]
            return {"source_persisted": bool(interaction["userEvent"].get("id")),
                    "recovery_ms": None, "recursive_capture_count": len(recursive)}
        if operation == "ADDITIONAL_CONTEXT_INTEGRITY":
            return {"expected_memory_id": "mem-alpha-timeout",
                    "provenance_complete": len(interaction["memoryProvenance"]) == len(context.memory_ids)}
        if operation == "HOOK_FAULT_MATRIX":
            faults = self._store("BRIDGE_FAULT", {
                "faultKind": "BRIDGE_FAULT", "caseId": item.case_id,
                "repetition": item.repetition})["documents"]
            return {"fault_exact": len(faults) == 1 and faults[0].get("mode") == item.mode
                    and faults[0].get("requestBodyLength") == 0
                    and faults[0].get("activatedMonotonicNs", 0)
                    <= faults[0].get("observedMonotonicNs", -1),
                    "hook_deadline_ms": (interaction["completedNs"] - interaction["submittedNs"]) // 1_000_000,
                    "hook_result_count": sum(value.get("kind") == "HookExecutionEvent"
                                             for value in interaction["events"]),
                    "hook_success": interaction["hookEvent"].get("success") is True}
        if operation == "RESTART_CONTINUITY":
            return {"partition_before": item.partition, "partition_after": item.partition,
                    "recall_correct_after": "mem-alpha-timeout" in context.memory_ids,
                    "initial_capture_count": len(exact), "final_capture_count": len(exact)}
        if operation == "FEEDBACK_LOOP_PREVENTION":
            return {"eligible_capture_count": len(exact), "recursive_capture_count": 0}
        if operation == "SECRET_DELIVERY_ABSENCE":
            surfaces = self._store("SECRET_SURFACES", {
                "conversationId": interaction["conversationId"],
                "markerSha256": c.sha_bytes(c.SECRET.encode())})["documents"]
            occurrences: dict[str, int] = {}
            for document in surfaces:
                occurrences[str(document["surface"])] = sum(
                    c.SECRET.encode() in raw if isinstance(raw, bytes) else c.SECRET in str(raw)
                    for raw in document.get("values", []))
            required = {"currentPromptSubmission", "currentPromptPersisted", "hookInput",
                        "modelSegment0", "sealedRawPrompt", "operationalLog",
                        "additionalContext", "modelSegment1", "retrievalEvents",
                        "derivedProjection"}
            forbidden_surfaces = {"operationalLog", "additionalContext", "modelSegment1",
                                  "retrievalEvents", "derivedProjection"}
            return {"secret_prompt_boundaries": sum(
                        c.SECRET.encode() in interaction["promptBoundaryBytes"][name]
                        for name in ("submission", "persisted", "hook")),
                    "secret_forbidden_surface_hits": sorted(
                        name for name, count in occurrences.items()
                        if count and name in forbidden_surfaces),
                    "model_segment0_count": occurrences.get("modelSegment0", 0),
                    "sealed_raw_count": occurrences.get("sealedRawPrompt", 0),
                    "surface_set_complete": set(occurrences) == required,
                    "raw_policy_bound": set(occurrences) == required
                    and all(occurrences.get(name, 0) == 0 for name in forbidden_surfaces)}
        if operation == "EMPTY_RESULT":
            return {"empty": interaction["context"] == ""}
        if operation == "OVERSIZED_CONTEXT":
            return {"truncated_at_frame_boundary": context.valid,
                    "scientific_bounds_observed": len(context.memory_ids) <= 5}
        if operation == "MODEL_STUB_FAULT_MATRIX":
            expected = 0 if item.mode == "TRANSPORT_FAILURE_UNUSED_PORT" else 1
            faults = self._store("MODEL_FAULT", {"caseId": item.case_id,
                                                  "repetition": item.repetition})["documents"]
            exact_fault = len(faults) == 1 and faults[0].get("mode") == item.mode \
                and faults[0].get("activatedMonotonicNs", 0) \
                <= faults[0].get("observedMonotonicNs", -1)
            request_ids = {value["requestId"] for value in interaction["requestReceipts"]}
            fault_request = faults[0].get("requestId") if faults else None
            return {"activated_mode": faults[0].get("mode") if faults else None,
                    "independently_observed": exact_fault and request_count == expected
                    and ((fault_request in request_ids) if expected else fault_request is None),
                    "retry_count": max(0, request_count - expected),
                    "future_terminal": interaction["terminalStatus"] in {"ERROR", "STOPPED"},
                    "cleanup_evidence_complete": request_count == terminal_count,
                    "fault_timing_complete": exact_fault,
                    "fault_activation_ns": faults[0].get("activatedMonotonicNs", 0)
                    if faults else 0,
                    "fault_observed_ns": faults[0].get("observedMonotonicNs", 0)
                    if faults else 0}
        raise c.HarnessFailure(f"raw derived case values missing: {operation}")

    def _apply_mutations(self, scope: c.OperationScope, observation: c.Observation) -> None:
        # Mutation behavior is reused from the work-product adapter, but it
        # mutates the raw-derived Observation rather than supplying oracle values.
        helper = c.ObservationMutationApplier(
            self.framing,
            predicate_mutation=self.predicate_mutation,
            evidence_mutation=self.evidence_mutation,
            missing_evidence=self.missing_evidence,
            scope_identity_mutation=self.scope_identity_mutation,
            scope_shape_mutation=self.scope_shape_mutation)
        helper._apply_predicate_mutation(observation)
        helper._apply_evidence_mutation(observation)
        helper._apply_scope_identity_mutation(observation)
        helper._apply_scope_shape_mutation(scope, observation)
        if self.evidence_mutation in {"E-002", "E-002-RAW"} and observation.interactions:
            observation.interactions[0]["promptBoundaryBytes"]["model"] = b"mutated-raw-prompt"
        if self.evidence_mutation in {"E-003", "E-003-PERSISTED-RAW"} and observation.interactions:
            observation.interactions[0]["persistedContext"] += "mutated"

    def recover(self, scope: c.OperationScope, exit_mode: c.ServiceMode) -> c.RecoveryReceipt:
        if self.injected_failure == "RECOVERY_FAILURE":
            raise c.HarnessFailure("injected recovery failure")
        if scope.item.operation == "HOOK_FAULT_MATRIX":
            self._process("CLEAR_BRIDGE_FAULT", ["candidate10-bridge-fault", "NONE"])
        receipt = self._process("TRANSITION_SERVICE", ["candidate10-service", exit_mode.value])
        self.mode = exit_mode
        samples: list[dict[tuple[str, str], int]] = []
        keys = [{"conversationId": key.conversation_id, "eventId": key.event_id}
                for key in scope.allowed_source_keys + scope.forbidden_source_keys]
        deadline_ns = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns) \
            + 120_000_000_000
        stable = False
        while True:
            documents = self._store("CAPTURE_EXACT", {"keys": keys})["documents"] if keys else []
            sample: dict[tuple[str, str], int] = {}
            for document in documents:
                key = (str(document["conversation_id"]), str(document["event_id"]))
                sample[key] = sample.get(key, 0) + 1
            samples.append(sample)
            stable = c.exact_capture_stabilized(
                samples, scope.allowed_source_keys, scope.forbidden_source_keys,
                consecutive_reads=2)
            if stable:
                break
            now_ns = self._raw("CLOCK", {"action": "MONOTONIC_NS"}, self.ports.monotonic_ns)
            if now_ns >= deadline_ns:
                raise c.HarnessFailure("recovery exact-key stabilization deadline expired")
            self._raw("CLOCK", {"action": "SLEEP", "durationNs": 50_000_000},
                      lambda: self.ports.sleep_ns(50_000_000))
        for conversation in list(scope.conversation_roles.values()):
            self._http("DELETE", f"/api/conversations/{conversation}")
            self.owned_conversations.discard(conversation)
        active = self._store("ACTIVE_WORK", {})["documents"][0]
        return c.RecoveryReceipt(c.ServiceMode(receipt["serviceMode"]), int(active["active"]),
                                 int(active["queued"]),
                                 not any(value in self.owned_conversations
                                         for value in scope.conversation_roles.values()), stable)

    def cleanup(self) -> c.CleanupReceipt:
        self.cleanup_called = True
        process = self._process("CLEANUP_OWNED", ["candidate10-cleanup"])
        for path in sorted(self.owned_workspaces, reverse=True):
            self._raw("FILESYSTEM", {"action": "REMOVE_OWNED_TREE", "path": path},
                      lambda p=path: self.ports.filesystem_action("REMOVE_OWNED_TREE", p, {}))
        self.owned_workspaces.clear()
        mongo_inventory = self._store("INVENTORY", {}, store="MONGODB")["documents"][0]
        semantic_inventory = self._store("INVENTORY", {}, store="QDRANT_SEMANTIC")["documents"][0]
        episodic_inventory = self._store("INVENTORY", {}, store="QDRANT_EPISODIC")["documents"][0]
        namespaces_absent = all(not value.get("namespacePresent") for value in (
            mongo_inventory, semantic_inventory, episodic_inventory))
        result = c.CleanupReceipt(
            process["serviceMode"] == c.ServiceMode.OFF.value,
            mongo_inventory["conversationCount"] == 0,
            mongo_inventory["workspaceCount"] == 0,
            namespaces_absent and mongo_inventory["ownedCaptureCount"] == 0,
            not process["stubRunning"],
            int(process["activeWork"]), int(process["queuedWork"]))
        if self.missing_evidence is not None:
            result.evidence_presence.discard(self.missing_evidence)
        if self.evidence_mutation == "E-009":
            result.service_absent = False
        elif self.evidence_mutation == "E-010":
            result.active_work = 1
        return result

    def emergency_cleanup(self) -> c.CleanupReceipt:
        self.cleanup_called = True
        self.emergency_cleanup_used = True
        try:
            process = self.ports.process_action("EMERGENCY_CLEANUP_OWNED", ["candidate10-cleanup"],
                                                None, {}, 30000)
        except Exception:
            process = {"serviceMode": "UNKNOWN", "stubRunning": True,
                       "activeWork": 1, "queuedWork": 1}
        for path in list(self.owned_workspaces):
            try:
                self.ports.filesystem_action("REMOVE_OWNED_TREE", path, {})
            except Exception:
                pass
        self.owned_workspaces.clear()
        succeeded = process.get("serviceMode") == c.ServiceMode.OFF.value \
            and not process.get("stubRunning")
        return c.CleanupReceipt(succeeded, succeeded, succeeded, succeeded, succeeded,
                                int(process.get("activeWork", 1)),
                                int(process.get("queuedWork", 1)), True,
                                initial_cleanup_failure_observed=True,
                                emergency_cleanup_succeeded=succeeded)
