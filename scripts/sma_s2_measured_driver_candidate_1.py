#!/usr/bin/env python3
"""Prospective measured executor for the frozen SMA-S2 corpus.

The default mode is offline contract inspection. Live execution is impossible
without a separately frozen execution identity and single-use authorization.
No result or PASS credit is imported from the historical Step-15 V12 lineage;
that code is loaded only as an identity-bound library of service/API primitives.
"""

from __future__ import annotations

import argparse
import base64
import concurrent.futures
import hashlib
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path
from typing import Any, Callable


ROOT = Path(__file__).resolve().parents[1]
CORPUS = ROOT / "investigations/sma-q1/layered/sma-s2-measured-corpus-candidate-1.json"
CONFIG = ROOT / "investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-2.json"
STUB = ROOT / "scripts/sma_s2_deterministic_model_stub_candidate_2.py"
PRIMITIVES = ROOT / "scripts/smaq1_native_step15_v4.py"
SMA = Path("/Users/paul/work/tekroo-ai/sma-s1-p2m")
MEASURED_ROOT = Path("/tmp/tekroo-sma-s2-successor-candidate-3")
DATABASE = "sma_s2_successor_candidate_3_measured_20260814"
SEMANTIC = "sma_s2_successor_candidate_3_semantic_20260814"
EPISODIC = "sma_s2_successor_candidate_3_episodic_20260814"
LABEL = "com.tekroo.sma-service-s2-successor-candidate-3"
SECRET = "sk-test-SMA-S2-NEVER-PERSIST"
EXPECTED_CASES = 20
EXPECTED_REPETITIONS = 103
TERMINAL_STATES = frozenset({"finished", "error", "paused", "stuck"})


class PredicateFailure(AssertionError):
    pass


def require(condition: bool, message: str) -> None:
    if not condition:
        raise PredicateFailure(message)


def canonical_bytes(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()


def sha_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        while block := stream.read(1024 * 1024):
            digest.update(block)
    return digest.hexdigest()


def read_jsonl(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line]


class DurableJournal:
    def __init__(self, path: Path, *, create: bool = True) -> None:
        self.path = path
        self.lock = threading.Lock()
        path.parent.mkdir(parents=True, exist_ok=True)
        if create:
            descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            os.close(descriptor)

    def append(self, kind: str, data: dict[str, Any]) -> str:
        record = {
            "recordId": sha_bytes(canonical_bytes({"kind": kind, "data": data})),
            "recordedMonotonicNs": time.monotonic_ns(),
            "kind": kind,
            **data,
        }
        encoded = canonical_bytes(record) + b"\n"
        with self.lock:
            descriptor = os.open(self.path, os.O_APPEND | os.O_WRONLY)
            try:
                os.write(descriptor, encoded)
                os.fsync(descriptor)
            finally:
                os.close(descriptor)
        return str(record["recordId"])


def exact_plan(corpus: dict[str, Any]) -> list[dict[str, Any]]:
    plan: list[dict[str, Any]] = []
    for case in sorted(corpus["cases"], key=lambda item: item["id"]):
        for repetition in range(1, int(case["repetitions"]) + 1):
            mode = case.get("mode")
            if "modeSchedule" in case:
                mode = case["modeSchedule"][repetition - 1]
            plan.append(
                {
                    "caseId": case["id"],
                    "repetition": repetition,
                    "operation": case["operation"],
                    "mode": mode,
                    "stubMode": "SUCCESS" if str(mode).startswith("BRIDGE_") else mode,
                    "partition": case["partition"],
                    "literalPrompt": case["literalPrompt"],
                }
            )
    return plan


def control_prompt(item: dict[str, Any]) -> str:
    return (
        f"[SMA-S2-STUB case={item['caseId']} repetition={item['repetition']} "
        f"mode={item.get('stubMode', item['mode'])}] {item['literalPrompt']}"
    )


def validate_static_contract() -> dict[str, Any]:
    corpus = json.loads(CORPUS.read_text())
    config = json.loads(CONFIG.read_text())
    plan = exact_plan(corpus)
    ids = [item["id"] for item in corpus["cases"]]
    operations = {item["operation"] for item in corpus["cases"]}
    required_operations = set(LiveRuntime.OPERATIONS)
    checks = {
        "caseCount": len(ids),
        "repetitionCount": len(plan),
        "caseIdsUnique": len(ids) == len(set(ids)),
        "caseOrderExact": ids == sorted(ids),
        "planOrderExact": plan == sorted(plan, key=lambda item: (item["caseId"], item["repetition"])),
        "operationsExact": operations == required_operations,
        "controlTagsUnique": len({control_prompt(item).split("]", 1)[0] for item in plan}) == len(plan),
        "fault019": [item["mode"] for item in plan if item["caseId"].startswith("SMA-S2-019-")],
        "fault020": [item["mode"] for item in plan if item["caseId"].startswith("SMA-S2-020-")],
        "configuredRetries": config["openhands"]["numRetries"],
        "configuredTimeoutSeconds": config["openhands"]["modelTimeoutSeconds"],
        "driverExecutionDefault": "DENY",
    }
    required = [
        checks["caseCount"] == EXPECTED_CASES,
        checks["repetitionCount"] == EXPECTED_REPETITIONS,
        checks["caseIdsUnique"],
        checks["caseOrderExact"],
        checks["planOrderExact"],
        checks["operationsExact"],
        checks["controlTagsUnique"],
        checks["fault019"] == ["TIMEOUT", "MALFORMED", "HTTP_503", "TRANSPORT_FAILURE_UNUSED_PORT"],
        checks["fault020"] == ["TIMEOUT", "TIMEOUT", "TIMEOUT"],
        checks["configuredRetries"] == 0,
        checks["configuredTimeoutSeconds"] == 1,
    ]
    if not all(required):
        raise RuntimeError(f"static driver contract mismatch: {checks}")
    return checks


def validate_execution_fence(identity_path: Path, authorization_path: Path) -> dict[str, Any]:
    identity = json.loads(identity_path.read_text())
    authorization = json.loads(authorization_path.read_text())
    dependency_matches = [
        Path(item["path"]).is_file() and sha_file(Path(item["path"])) == item["sha256"]
        for item in identity.get("dependencies", [])
    ]
    configuration = identity.get("configuration", {})
    preregistration = identity.get("preregistration", {})
    checks = {
        "identityAcceptedFrozen": identity.get("status") == "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
        "identityNonAuthorizing": identity.get("measuredExecutionAuthorized") is False,
        "identityDriverMatches": identity.get("driver", {}).get("sha256") == sha_file(Path(__file__)),
        "identityCorpusMatches": identity.get("corpus", {}).get("sha256") == sha_file(CORPUS),
        "identityStubMatches": identity.get("stub", {}).get("sha256") == sha_file(STUB),
        "identityConfigurationMatches": Path(configuration.get("path", "")).is_file()
                                        and sha_file(Path(configuration["path"])) == configuration.get("sha256"),
        "identityPreregistrationMatches": Path(preregistration.get("path", "")).is_file()
                                          and sha_file(Path(preregistration["path"])) == preregistration.get("sha256"),
        "identityDependencyClosureMatches": bool(dependency_matches) and all(dependency_matches),
        "authorizationLive": authorization.get("status") == "AUTHORIZED_NOT_CONSUMED",
        "authorizationSingleUse": authorization.get("maximumMeasuredAttempts") == 1,
        "authorizationIdentityMatches": authorization.get("executionIdentitySha256") == sha_file(identity_path),
        "authorizationDriverMatches": authorization.get("driverSha256") == sha_file(Path(__file__)),
        "authorizationCorpusMatches": authorization.get("corpusSha256") == sha_file(CORPUS),
        "authorizationPreregistrationMatches": authorization.get("preregistrationSha256")
                                               == preregistration.get("sha256"),
        "authorizationScope": authorization.get("caseCount") == 20 and authorization.get("repetitionCount") == 103,
        "realModelProhibited": authorization.get("realModelCallsAllowed") is False,
    }
    if not all(checks.values()):
        raise PermissionError(f"measured execution fence rejected: {checks}")
    return checks


class LiveRuntime:
    OPERATIONS = (
        "FIRST_PROMPT_EMPTY",
        "SAME_PARTITION_DELIVERY",
        "CROSS_PARTITION_DENIAL",
        "UNTRUSTED_CONTEXT_PLACEMENT",
        "RAW_INELIGIBLE_ABSENCE",
        "DUPLICATE_PERSISTED_EVENT",
        "RETRIEVAL_OUTAGE",
        "CAPTURE_OUTAGE_RECOVERY",
        "ADDITIONAL_CONTEXT_INTEGRITY",
        "HOOK_FAULT_MATRIX",
        "PARENT_CHILD_PROVENANCE",
        "RESTART_CONTINUITY",
        "FOUR_CHANNEL_CONCURRENCY",
        "FEEDBACK_LOOP_PREVENTION",
        "SECRET_DELIVERY_ABSENCE",
        "EMPTY_RESULT",
        "OVERSIZED_CONTEXT",
        "CONDENSATION_REANCHOR",
        "MODEL_STUB_FAULT_MATRIX",
        "ACTIVE_CANCELLATION_AND_SHUTDOWN",
    )

    def __init__(self, output: Path, journal: DurableJournal) -> None:
        self.output = output
        self.journal = journal
        self.raw = output / "stub-raw-evidence.jsonl"
        self.operational = output / "stub-operational.jsonl"
        self.terminal = output / "stub-terminal.jsonl"
        self.ready = output / "stub-ready.json"
        self.stub_process: subprocess.Popen[str] | None = None
        self.stub_port: int | None = None
        self.conversations: list[str] = []
        self.seeded = False
        self.oversized_prepared = False
        self.mapped: dict[str, str] = {}
        self.p = self._load_primitives()
        self.support = self.p.support
        self.alpha = MEASURED_ROOT / "actor-alpha"
        self.beta = MEASURED_ROOT / "actor-beta"
        self._bind_primitives()

    def _load_primitives(self):
        previous = Path.cwd()
        try:
            spec = importlib.util.spec_from_file_location("sma_s2_bound_primitives", PRIMITIVES)
            if spec is None or spec.loader is None:
                raise RuntimeError("cannot load bound service primitives")
            module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)
            return module
        finally:
            os.chdir(previous)

    def _bind_primitives(self) -> None:
        p = self.p
        p.SMA = SMA
        p.RUN_ROOT = MEASURED_ROOT
        p.ALPHA = self.alpha
        p.BETA = self.beta
        p.RUNTIME = MEASURED_ROOT / "runtime"
        p.LABEL = LABEL
        p.DATABASE = DATABASE
        p.SEMANTIC = SEMANTIC
        p.EPISODIC = EPISODIC
        p.ALPHA_PARTITION = "openhands:" + sha_bytes(str(self.alpha).encode())[:24] + ":default"
        p.BETA_PARTITION = "openhands:" + sha_bytes(str(self.beta).encode())[:24] + ":default"
        p.support.REPOSITORY = SMA
        p.support.JAR = SMA / "target/sma-1.0-SNAPSHOT.jar"
        p.support.PROBE = SMA / ".openhands/hooks/native_hook_probe.py"
        p.support.WRAPPER = SMA / "scripts/start-sma-service-with-key-file.sh"
        p.support.SMA_LABEL = LABEL

    def start_stub(self) -> None:
        stdout = (self.output / "stub-stdout.log").open("w")
        stderr = (self.output / "stub-stderr.log").open("w")
        self.stub_process = subprocess.Popen(
            [sys.executable, str(STUB), "--raw-journal", str(self.raw),
             "--operational-journal", str(self.operational), "--terminal-journal", str(self.terminal),
             "--timeout-delay-ms", "5000", "--ready-file", str(self.ready)],
            stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr, text=True,
        )
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if self.ready.exists():
                self.stub_port = int(json.loads(self.ready.read_text())["port"])
                return
            if self.stub_process.poll() is not None:
                raise RuntimeError("deterministic stub exited before ready")
            time.sleep(0.02)
        raise TimeoutError("deterministic stub readiness exceeded five seconds")

    def preflight_absence(self) -> dict[str, bool]:
        checks = {
            "ownedServiceAbsent": not self.support.launch_identity(LABEL)["loaded"],
            "bridgePortAbsent": self.support.listener_pid(8130) is None,
            "mongodbDatabaseAbsent": not self.support.mongo_database_exists(DATABASE),
            "semanticCollectionAbsent": not self.support.qdrant_collection_exists(SEMANTIC),
            "episodicCollectionAbsent": not self.support.qdrant_collection_exists(EPISODIC),
            "workspaceRootAbsent": not MEASURED_ROOT.exists(),
        }
        self.journal.append("PREASSERTION_MEASURED_NAMESPACE_ABSENCE", checks)
        require(all(checks.values()), "one or more measured resources existed before execution")
        return checks

    def setup_workspaces(self) -> None:
        for workspace in (self.alpha, self.beta):
            workspace.mkdir(parents=True, exist_ok=False)
            (workspace / ".openhands").symlink_to(SMA / ".openhands", target_is_directory=True)

    def settings(self, port: int) -> dict[str, Any]:
        value = self.support.settings(self.support.session_key())
        llm = value["llm"]
        llm.update({
            "model": "openai/sma-s2-deterministic-stub-candidate-2",
            "model_canonical_name": "openai/gpt-4o",
            "base_url": f"http://127.0.0.1:{port}/v1",
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
            "timeout": 1,
            "log_completions": False,
        })
        value["tools"] = []
        value["mcp_config"] = {"mcpServers": {}}
        return value

    def create_conversation(self, workspace: Path, port: int, parent: str | None = None) -> str:
        body: dict[str, Any] = {
            "agent_settings": self.settings(port),
            "secrets_encrypted": False,
            "workspace": {"kind": "LocalWorkspace", "working_dir": str(workspace)},
            "worktree": False,
            "max_iterations": 1,
            "autotitle": False,
            "hook_config": self.support.hooks(self.support.session_key(), str(workspace)),
        }
        if parent is not None:
            body["parent_conversation_id"] = parent
        status, created = self.p.http("POST", self.support.INGRESS, "/api/conversations",
                                      self.support.session_key(), body, 90)
        if status not in (200, 201):
            raise RuntimeError(f"conversation creation failed: HTTP {status}")
        conversation = str(created["id"])
        self.conversations.append(conversation)
        return conversation

    def events(self, conversation: str) -> list[dict[str, Any]]:
        return self.p.fetch_events(conversation)

    @staticmethod
    def content_text(content: Any) -> str:
        if isinstance(content, str):
            return content
        if not isinstance(content, list):
            return ""
        return "".join(str(item.get("text", "")) for item in content if isinstance(item, dict)
                       and item.get("type") == "text")

    def submit(self, conversation: str, prompt: str) -> int:
        status, _ = self.p.http(
            "POST", self.support.INGRESS, f"/api/conversations/{conversation}/events",
            self.support.session_key(),
            {"role": "user", "run": True, "content": [{"type": "text", "text": prompt}]}, 30,
        )
        return status

    def raw_for(self, case_id: str, repetition: int, prompt: str | None = None) -> list[dict[str, Any]]:
        records = [record for record in read_jsonl(self.raw)
                   if record.get("caseId") == case_id and record.get("repetition") == repetition]
        if prompt is None:
            return records
        return [record for record in records
                if prompt.encode() in base64.b64decode(record["requestBodyBase64"], validate=True)]

    def terminal_for(self, case_id: str, repetition: int) -> list[dict[str, Any]]:
        return [record for record in read_jsonl(self.terminal)
                if record.get("caseId") == case_id and record.get("repetition") == repetition]

    def wait_observation(self, item: dict[str, Any], conversation: str, *, raw_expected: int = 1,
                         timeout: float = 15) -> dict[str, Any]:
        prompt = control_prompt(item)
        deadline = time.monotonic() + timeout
        last: dict[str, Any] = {}
        while time.monotonic() < deadline:
            events = self.events(conversation)
            user = next((event for event in events if event.get("kind") == "MessageEvent"
                         and event.get("source") == "user" and self.p.event_text(event) == prompt), None)
            hook = next((event for event in events if event.get("kind") == "HookExecutionEvent"
                         and (event.get("hook_input") or {}).get("message") == prompt), None)
            raws = self.raw_for(item["caseId"], item["repetition"], prompt)
            raw_ids = {record["requestId"] for record in raws}
            terminals = [record for record in read_jsonl(self.terminal)
                         if record.get("requestId") in raw_ids]
            detail_status, detail = self.p.http(
                "GET", self.support.INGRESS, f"/api/conversations/{conversation}",
                self.support.session_key(), timeout=15,
            )
            last = {"events": events, "user": user, "hook": hook, "raws": raws,
                    "terminals": terminals,
                    "detailStatus": detail_status, "detail": detail}
            terminal = str(detail.get("execution_status")) in TERMINAL_STATES
            stub_terminal = len(terminals) >= raw_expected
            if user is not None and hook is not None and len(raws) >= raw_expected and stub_terminal and terminal:
                break
            time.sleep(0.05)
        return last

    def request_oracle(self, raw: dict[str, Any], prompt: str, context: str) -> dict[str, Any]:
        body = base64.b64decode(raw["requestBodyBase64"], validate=True)
        request = json.loads(body)
        candidates = [message for message in request.get("messages", []) if message.get("role") == "user"]
        matched = None
        for message in candidates:
            content = message.get("content")
            if isinstance(content, list) and content and content[0].get("text") == prompt:
                matched = content
        return {
            "requestBodySha256": sha_bytes(body),
            "requestBodyLength": len(body),
            "currentPromptSegmentMatched": matched is not None,
            "contentSegmentCount": len(matched or []),
            "contextSegmentMatched": bool(context) and len(matched or []) > 1
                                     and matched[1].get("text") == context,
            "contextAbsentWhenEmpty": not context and (matched is not None) and len(matched) == 1,
            "stringFlatteningObserved": any(isinstance(message.get("content"), str) for message in candidates),
        }

    def one_prompt(self, item: dict[str, Any], workspace: Path, *, parent: str | None = None,
                   port: int | None = None, expected: tuple[str, ...] = (),
                   forbidden: tuple[str, ...] = (), raw_expected: int = 1) -> dict[str, Any]:
        selected_port = int(port if port is not None else self.stub_port)
        conversation = self.create_conversation(workspace, selected_port, parent)
        prompt = control_prompt(item)
        submitted = time.monotonic_ns()
        submit_status = self.submit(conversation, prompt)
        observed = self.wait_observation(item, conversation, raw_expected=raw_expected)
        user = observed["user"] or {}
        hook = observed["hook"] or {}
        context = str(hook.get("additional_context") or "")
        persisted_context = self.content_text(user.get("extended_content"))
        raws = observed["raws"]
        terminals = observed.get("terminals") or []
        events = observed["events"]
        user_index = events.index(user) if user in events else -1
        hook_index = events.index(hook) if hook in events else -1
        agent = next((event for event in events if event.get("kind") == "MessageEvent"
                      and event.get("source") == "agent" and events.index(event) > user_index), None)
        errors = [event for event in events if event.get("kind") == "ConversationErrorEvent"
                  and events.index(event) > user_index]
        request = self.request_oracle(raws[0], prompt, context) if raws else None
        record = {
            "caseId": item["caseId"], "repetition": item["repetition"],
            "operation": item["operation"], "mode": item["mode"],
            "conversationId": conversation, "submitHttpStatus": submit_status,
            "elapsedNs": time.monotonic_ns() - submitted,
            "promptSha256": sha_bytes(prompt.encode()), "promptLength": len(prompt.encode()),
            "persistedPromptSha256": sha_bytes(self.p.event_text(user).encode()),
            "persistedPromptLength": len(self.p.event_text(user).encode()),
            "hookInputSha256": sha_bytes(str((hook.get("hook_input") or {}).get("message", "")).encode()),
            "hookSuccess": hook.get("success"), "hookExitCode": hook.get("exit_code"),
            "contextSha256": sha_bytes(context.encode()), "contextLength": len(context),
            "persistedContextSha256": sha_bytes(persisted_context.encode()),
            "persistedContextLength": len(persisted_context),
            "contextMarksUntrusted": "untrusted evidence" in context.lower(),
            "rawStubRequestCount": len(raws), "requestOracle": request,
            "stubTerminalCount": len(terminals),
            "stubOutcomes": [terminal.get("responseWriteOutcome") for terminal in terminals],
            "terminalStatus": (observed["detail"] or {}).get("execution_status"),
            "userEventId": user.get("id"), "hookEventId": hook.get("id"),
            "userParentId": user.get("parent_id"), "hookParentId": hook.get("parent_id"),
            "hookBeforeUser": user_index >= 0 and 0 <= hook_index < user_index,
            "agentEventId": (agent or {}).get("id"),
            "agentTerminalSha256": sha_bytes(self.p.event_text(agent or {}).encode()),
            "conversationErrorCount": len(errors),
        }
        evidence_id = self.journal.append("PREASSERTION_REPETITION_EVIDENCE", record)
        require(submit_status == 200, "prompt submission did not return HTTP 200")
        require(record["promptSha256"] == record["persistedPromptSha256"] == record["hookInputSha256"],
                "prompt identity diverged across a defined boundary")
        require(len(raws) == raw_expected, "deterministic stub request count mismatch")
        require(len(terminals) == raw_expected, "deterministic stub terminal count mismatch")
        require(bool(record["hookBeforeUser"]), "hook was not persisted before the user event")
        require(context == persisted_context, "hook context and persisted extended context diverged")
        if request is not None:
            require(request["currentPromptSegmentMatched"] and not request["stringFlatteningObserved"],
                    "model request did not preserve the list-serialized prompt segment")
            require(request["contextSegmentMatched"] if context else request["contextAbsentWhenEmpty"],
                    "model request context segment placement mismatch")
        if item.get("stubMode", item["mode"]) == "SUCCESS":
            expected_terminal = f"STUB_OK:{item['caseId']}:{item['repetition']}"
            require(agent is not None and self.p.event_text(agent) == expected_terminal,
                    "deterministic success terminal mismatch")
            require(not errors, "success request emitted a conversation error")
            require(record["stubOutcomes"] == ["CLIENT_RECEIVED"],
                    "success response was not received by the OpenHands client")
        for marker in expected:
            require(marker in context, "expected context identity or marker absent")
        for marker in forbidden:
            require(marker not in context, "forbidden context identity or marker present")
        record["evidenceRecordId"] = evidence_id
        return record

    def ensure_seeded(self) -> None:
        if self.seeded:
            if self.support.listener_pid(8130) is None:
                self.p.start_service(False, True)
            return
        corpus = json.loads(CORPUS.read_text())["syntheticMemoryCorpus"]
        translated = [{"memoryId": item["logicalId"], "partition": item["partition"],
                       "state": item["state"], "eligible": item["eligible"],
                       "text": item["literalText"], "expectedMarker": item["marker"]} for item in corpus]
        self.mapped = self.p.seed_corpus(translated, self.conversations, self.journal)
        self.seeded = True

    def run_operation(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        operation = item["operation"]
        handler: Callable[[dict[str, Any]], list[dict[str, Any]]] = getattr(self, "case_" + operation.lower())
        return handler(item)

    def case_first_prompt_empty(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        record = self.one_prompt(item, self.alpha)
        require(record["contextLength"] == 0, "first prompt in empty partition injected context")
        return [record]

    def case_same_partition_delivery(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); return [self.one_prompt(item, self.alpha, expected=(self.mapped["mem-alpha-timeout"], "17 seconds"), forbidden=("9999", "4312"))]

    def case_cross_partition_denial(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); return [self.one_prompt(item, self.beta, forbidden=("mem-alpha", "17 seconds", "DELETE_CONFIRMED", "9999"))]

    def case_untrusted_context_placement(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); record=self.one_prompt(item,self.alpha,expected=(self.mapped["mem-alpha-adversarial"],"DELETE_CONFIRMED")); require(bool(record["contextMarksUntrusted"]),"context was not framed as untrusted evidence"); return [record]

    def case_raw_ineligible_absence(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); return [self.one_prompt(item,self.alpha,forbidden=("mem-alpha-raw","9999"))]

    def case_duplicate_persisted_event(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); before=self.p.mongo_counts()["memories"]; record=self.one_prompt(item,self.alpha); self.p.start_service(True,True); self.p.wait_memory_count(before+2); self.p.stop_service(); self.p.start_service(True,True); time.sleep(5); after=self.p.mongo_counts()["memories"]; matching=sum(record["userEventId"] in canonical_bytes(doc).decode(errors="ignore") for doc in self.p.memory_documents()); self.journal.append("PREASSERTION_DUPLICATE_OBSERVATION",{"caseId":item["caseId"],"repetition":item["repetition"],"sourceEventId":record["userEventId"],"sourceEventMemoryCount":matching,"before":before,"after":after}); require(after==before+2 and matching==1,"reconciliation changed the exact captured-event identity or cardinality"); return [record]

    def case_retrieval_outage(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.p.stop_service(); record=self.one_prompt(item,self.alpha); require(record["contextLength"]==0,"retrieval outage injected context"); return [record]

    def case_capture_outage_recovery(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        before=self.p.mongo_counts()["memories"]; self.p.stop_service(); record=self.one_prompt(item,self.alpha); self.p.start_service(True,True); self.p.wait_memory_count(before+2); after=self.p.mongo_counts()["memories"]; matching=sum(record["userEventId"] in canonical_bytes(doc).decode(errors="ignore") for doc in self.p.memory_documents()); self.journal.append("PREASSERTION_CAPTURE_RECOVERY",{"caseId":item["caseId"],"repetition":item["repetition"],"sourceEventId":record["userEventId"],"sourceEventMemoryCount":matching,"before":before,"after":after}); require(after==before+2 and matching==1,"capture recovery identity or cardinality mismatch"); return [record]

    def case_additional_context_integrity(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); return [self.one_prompt(item,self.alpha,expected=(self.mapped["mem-alpha-timeout"],"17 seconds"))]

    def case_hook_fault_matrix(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.p.stop_service(); mode=item["mode"]
        if mode=="BRIDGE_DOWN": record=self.one_prompt(item,self.alpha)
        else:
            mapped={"BRIDGE_TIMEOUT":"timeout","BRIDGE_MALFORMED":"malformed","BRIDGE_HTTP_503":"non2xx"}
            with self.p.FaultFixture(mapped[mode]): record=self.one_prompt(item,self.alpha)
        require(record["hookSuccess"] is True and record["contextLength"]==0,
                "hook fault did not produce one valid fail-open no-context result")
        return [record]

    def case_parent_child_provenance(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); parent=self.create_conversation(self.alpha,int(self.stub_port)); record=self.one_prompt(item,self.alpha,parent=parent,expected=(self.mapped["mem-alpha-timeout"],)); ps,pd=self.p.http("GET",self.support.INGRESS,f"/api/conversations/{parent}",self.support.session_key(),timeout=15); cs,cd=self.p.http("GET",self.support.INGRESS,f"/api/conversations/{record['conversationId']}",self.support.session_key(),timeout=15); obs={"caseId":item["caseId"],"repetition":item["repetition"],"parentStatus":ps,"childStatus":cs,"parentId":parent,"childId":record["conversationId"],"parentChildren":pd.get("sub_conversation_ids",[]),"childParent":cd.get("parent_conversation_id")}; self.journal.append("PREASSERTION_PARENT_CHILD",obs); require(record["conversationId"] in [str(x) for x in obs["parentChildren"]] and str(obs["childParent"])==parent,"parent-child topology mismatch"); return [record]

    def case_restart_continuity(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); first=self.one_prompt(item,self.alpha,expected=(self.mapped["mem-alpha-timeout"],)); self.p.stop_service(); self.p.start_service(False,True); second=self.p.direct_context(first["conversationId"],self.alpha,control_prompt(item)); self.journal.append("PREASSERTION_RESTART",{"caseId":item["caseId"],"repetition":item["repetition"],"memoryIds":second["memory_ids"]}); require(self.mapped["mem-alpha-timeout"] in second["memory_ids"],"restart changed partition-correct recall"); return [first]

    def case_four_channel_concurrency(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); workspaces=[self.alpha,self.alpha,self.beta,self.beta]
        def invoke(index:int)->dict[str,Any]:
            channel=dict(item); channel["literalPrompt"] += f" Channel {index}."; return self.one_prompt(channel,workspaces[index-1],expected=((self.mapped["mem-alpha-timeout"],) if index<3 else (self.mapped["mem-beta-port"],)),forbidden=((self.mapped["mem-beta-port"],) if index<3 else (self.mapped["mem-alpha-timeout"],)))
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor: records=list(executor.map(invoke,range(1,5)))
        self.journal.append("PREASSERTION_CONCURRENCY_COHORT",{"caseId":item["caseId"],"repetition":item["repetition"],"conversationIds":[r["conversationId"] for r in records]}); require(len({r["conversationId"] for r in records})==4,"concurrent channel identities were not distinct"); return records

    def case_feedback_loop_prevention(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); self.p.start_service(True,True); before=self.p.mongo_counts()["memories"]; record=self.one_prompt(item,self.alpha); self.p.wait_memory_count(before+2); time.sleep(5); after=self.p.mongo_counts()["memories"]; self.journal.append("PREASSERTION_FEEDBACK",{"caseId":item["caseId"],"repetition":item["repetition"],"before":before,"after":after}); require(after==before+2,"feedback-loop capture cardinality exceeded eligible events"); return [record]

    def case_secret_delivery_absence(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); self.p.start_service(True,True); before=self.p.mongo_counts()["memories"]; record=self.one_prompt(item,self.alpha,forbidden=(SECRET,)); self.p.wait_memory_count(before+2); secret_docs=[doc for doc in self.p.memory_documents() if SECRET in self.p.content_of(doc)]; eligible=[doc for doc in secret_docs if doc.get("reasoning_eligible")]; projection_count=0
        for doc in secret_docs:
            memory_id=str(doc["_id"]); projection_count += self.p.qdrant_point_receipt(SEMANTIC,memory_id)["point_count"] + self.p.qdrant_point_receipt(EPISODIC,memory_id)["point_count"]
        operational=self.operational.read_bytes(); retrieval=canonical_bytes(self.p.mongosh("EJSON.stringify(db.retrieval_events.find({}).toArray())")); logs=b"".join(path.read_bytes() for path in (self.p.RUNTIME/"sma.out.log",self.p.RUNTIME/"sma.err.log") if path.exists()); observation={"caseId":item["caseId"],"repetition":item["repetition"],"rawSecretMemoryCount":len(secret_docs),"eligibleSecretMemoryCount":len(eligible),"secretProjectionCount":projection_count,"operationalMarkerPresent":SECRET.encode() in operational,"retrievalMarkerPresent":SECRET.encode() in retrieval,"serviceLogMarkerPresent":SECRET.encode() in logs}; self.journal.append("PREASSERTION_SECRET_SCAN",observation); require(not eligible and projection_count==0 and not observation["operationalMarkerPresent"] and not observation["retrievalMarkerPresent"] and not observation["serviceLogMarkerPresent"],"synthetic secret escaped an allowed sealed surface"); return [record]

    def case_empty_result(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); record=self.one_prompt(item,self.beta,forbidden=(self.mapped["mem-alpha-timeout"],self.mapped["mem-alpha-adversarial"],self.mapped["mem-alpha-raw"],self.mapped["mem-beta-port"],"17 seconds","4312","9999")); require(record["contextLength"]==0,"empty-result case injected context"); return [record]

    def case_oversized_context(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded()
        if not self.oversized_prepared:
            self.p.stop_service(); before=self.p.mongo_counts()["memories"]
            for index in range(3): self.p.create_raw_source(self.alpha,f"Repository alpha timeout evidence copy {index+1}: API timeout is 17 seconds. "+("A"*1800),self.conversations)
            self.p.start_service(True,False); self.p.wait_memory_count(before+3); self.p.stop_service(); extra=[doc for doc in self.p.memory_documents() if "timeout evidence copy" in self.p.content_of(doc)]
            self.journal.append("PREASSERTION_OVERSIZED_FIXTURE",{"capturedCount":len(extra),"expectedCount":3}); require(len(extra)==3,"oversized fixture capture cardinality mismatch")
            for doc in extra: self.support.promote_memory(DATABASE,SEMANTIC,EPISODIC,str(doc["_id"]),"Repository alpha","17 seconds")
            self.p.start_service(False,True); self.oversized_prepared=True
        record=self.one_prompt(item,self.alpha,expected=("17 seconds",)); direct=self.p.direct_context(record["conversationId"],self.alpha,control_prompt(item)); self.journal.append("PREASSERTION_CONTEXT_BOUND",{"caseId":item["caseId"],"repetition":item["repetition"],"hookContextLength":record["contextLength"],"directContextLength":direct["context_length"],"directHitCount":direct["hit_count"]}); require(record["contextLength"]<=4096 and direct["context_length"]<=4096 and direct["hit_count"]<=3,"context exceeded product count or character bounds"); return [record]

    def case_condensation_reanchor(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); first=self.one_prompt(item,self.alpha,expected=(self.mapped["mem-alpha-timeout"],)); status,_=self.p.http("POST",self.support.INGRESS,f"/api/conversations/{first['conversationId']}/condense",self.support.session_key(),timeout=120); second=dict(item); second["literalPrompt"] += " Re-anchor after condensation."; self.submit(first["conversationId"],control_prompt(second)); observed=self.wait_observation(second,first["conversationId"]); context=str((observed["hook"] or {}).get("additional_context") or ""); self.journal.append("PREASSERTION_CONDENSATION",{"caseId":item["caseId"],"repetition":item["repetition"],"condenseStatus":status,"contextSha256":sha_bytes(context.encode()),"contextLength":len(context),"expectedPresent":self.mapped["mem-alpha-timeout"] in context}); require(status in (200,204) and self.mapped["mem-alpha-timeout"] in context,"post-condensation context was not re-anchored"); return [first]

    def case_model_stub_fault_matrix(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        if item["mode"]=="TRANSPORT_FAILURE_UNUSED_PORT":
            import socket
            with socket.socket() as listener: listener.bind(("127.0.0.1",0)); port=listener.getsockname()[1]
            record=self.one_prompt(item,self.alpha,port=port,raw_expected=0)
            require(record["conversationErrorCount"]>=1,"transport failure did not terminate as an error")
            return [record]
        record=self.one_prompt(item,self.alpha)
        require(record["conversationErrorCount"]>=1,"scheduled model fault did not terminate as an error")
        if item["mode"]=="TIMEOUT": require(record["stubOutcomes"]==["CLIENT_DISCONNECTED"],"timeout did not disconnect the client")
        return [record]

    def case_active_cancellation_and_shutdown(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        conversation=self.create_conversation(self.alpha,int(self.stub_port)); prompt=control_prompt(item); require(self.submit(conversation,prompt)==200,"cancellation prompt submission failed"); deadline=time.monotonic()+5
        while time.monotonic()<deadline and not self.raw_for(item["caseId"],item["repetition"],prompt): time.sleep(0.01)
        raws=self.raw_for(item["caseId"],item["repetition"],prompt); require(len(raws)==1,"cancellation trigger lacked one durable raw stub request"); time.sleep(0.25); started=time.monotonic_ns(); status,_=self.p.http("POST",self.support.INGRESS,f"/api/conversations/{conversation}/interrupt",self.support.session_key(),timeout=10); terminal_deadline=time.monotonic()+10
        while time.monotonic()<terminal_deadline and not self.terminal_for(item["caseId"],item["repetition"]): time.sleep(0.02)
        terminals=self.terminal_for(item["caseId"],item["repetition"]); record={"caseId":item["caseId"],"repetition":item["repetition"],"conversationId":conversation,"interruptHttpStatus":status,"completionNs":time.monotonic_ns()-started,"stubRequestCount":len(raws),"stubTerminalCount":len(terminals),"stubOutcome":terminals[0].get("responseWriteOutcome") if terminals else None}; self.journal.append("PREASSERTION_CANCELLATION",record); require(status==200 and record["completionNs"]<=10_000_000_000 and record["stubOutcome"]=="CLIENT_DISCONNECTED","active cancellation did not terminate within bounds"); return [record]

    def cleanup(self) -> dict[str, Any]:
        result: dict[str, Any] = {}
        try: result["service"] = self.p.stop_service()
        except Exception as error: result["serviceError"] = type(error).__name__
        result["conversations"] = []
        for conversation in reversed(self.conversations):
            try: result["conversations"].append(self.support.delete_conversation(self.support.session_key(),conversation))
            except Exception as error: result["conversations"].append({"conversationId":conversation,"error":type(error).__name__})
        if self.stub_process is not None and self.stub_process.poll() is None:
            self.stub_process.terminate()
            try: self.stub_process.wait(timeout=3)
            except subprocess.TimeoutExpired: self.stub_process.kill(); self.stub_process.wait(timeout=3)
        if self.support.mongo_database_exists(DATABASE): self.support.drop_mongo_database(DATABASE)
        if self.support.qdrant_collection_exists(SEMANTIC): self.support.qdrant_delete_collection(SEMANTIC)
        if self.support.qdrant_collection_exists(EPISODIC): self.support.qdrant_delete_collection(EPISODIC)
        shutil.rmtree(MEASURED_ROOT,ignore_errors=True)
        result["verified"]={"serviceAbsent":not self.support.launch_identity(LABEL)["loaded"],"bridgeAbsent":self.support.listener_pid(8130) is None,"mongoAbsent":not self.support.mongo_database_exists(DATABASE),"semanticAbsent":not self.support.qdrant_collection_exists(SEMANTIC),"episodicAbsent":not self.support.qdrant_collection_exists(EPISODIC),"workspaceAbsent":not MEASURED_ROOT.exists(),"stubExited":self.stub_process is None or self.stub_process.poll() is not None}
        self.journal.append("CLEANUP",result)
        return result


def execute(args: argparse.Namespace) -> int:
    validate_execution_fence(args.execution_identity, args.authorization)
    corpus=json.loads(CORPUS.read_text()); plan=exact_plan(corpus)
    args.output.mkdir(parents=True,exist_ok=False)
    journal=DurableJournal(args.output/"raw-evidence.jsonl")
    journal.append("EXECUTION_START",{"driverSha256":sha_file(Path(__file__)),"corpusSha256":sha_file(CORPUS),"caseCount":20,"repetitionCount":103})
    runtime=LiveRuntime(args.output,journal); results=[]; failure=None
    try:
        runtime.preflight_absence(); runtime.start_stub(); runtime.setup_workspaces()
        for item in plan:
            try: records=runtime.run_operation(item); results.append({"caseId":item["caseId"],"repetition":item["repetition"],"status":"PASS","recordCount":len(records)})
            except AssertionError as error:
                failure={"class":"SUBSTANTIVE","caseId":item["caseId"],"repetition":item["repetition"],"messageSha256":sha_bytes(str(error).encode())}; journal.append("REPETITION_FAILURE",failure); results.append({"caseId":item["caseId"],"repetition":item["repetition"],"status":"FAIL"})
            except Exception as error:
                failure={"class":"HARNESS","caseId":item["caseId"],"repetition":item["repetition"],"type":type(error).__name__,"messageSha256":sha_bytes(str(error).encode())}; journal.append("REPETITION_FAILURE",failure); break
    finally: cleanup=runtime.cleanup()
    complete=len(results)==103 and all(item["status"]=="PASS" for item in results) and all(cleanup["verified"].values())
    receipt={"recordType":"SMA_S2_MEASURED_EXECUTION_RECEIPT","status":"PASS" if complete else "FAIL" if failure and failure["class"]=="SUBSTANTIVE" else "INCONCLUSIVE","claim":"OPENHANDS_SMA_BOUNDARY_QUALIFIED" if complete else None,"completedRepetitions":len(results),"results":results,"failure":failure,"cleanup":cleanup,"rawJournalSha256":sha_file(args.output/"raw-evidence.jsonl")}
    descriptor=os.open(args.output/"execution-receipt.json",os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
    try: os.write(descriptor,canonical_bytes(receipt)+b"\n"); os.fsync(descriptor)
    finally: os.close(descriptor)
    print(json.dumps({"status":receipt["status"],"receipt":str(args.output/"execution-receipt.json")},sort_keys=True))
    return 0 if complete else 1


def parser() -> argparse.ArgumentParser:
    value=argparse.ArgumentParser(); value.add_argument("--offline-contract",action="store_true"); value.add_argument("--execute",action="store_true"); value.add_argument("--execution-identity",type=Path); value.add_argument("--authorization",type=Path); value.add_argument("--output",type=Path); return value


def main() -> int:
    args=parser().parse_args()
    if args.offline_contract:
        print(json.dumps(validate_static_contract(),indent=2,sort_keys=True)); return 0
    if not args.execute: raise PermissionError("default deny: use --offline-contract or a separately authorized --execute")
    if args.execution_identity is None or args.authorization is None or args.output is None: raise PermissionError("measured execution requires identity, authorization, and output")
    return execute(args)


if __name__ == "__main__":
    raise SystemExit(main())
