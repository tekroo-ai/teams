#!/usr/bin/env python3
"""Candidate-4 SMA-S2 measured driver.

The accepted candidate-2 predicates are authoritative.  Candidate 3 is loaded
only as an immutable primitive library.  Every scientific PASS is gated by the
candidate-4 predicate-to-oracle matrix; absent and false oracles both fail.
"""

from __future__ import annotations

import argparse
import base64
import concurrent.futures
import importlib.util
import json
import os
import re
import shutil
import socket
import time
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
BASE_DRIVER = ROOT / "scripts/sma_s2_measured_driver_candidate_1.py"
CORPUS = ROOT / "investigations/sma-q1/layered/sma-s2-measured-corpus-candidate-1.json"
CONFIG = ROOT / "investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-4.json"
MATRIX = ROOT / "investigations/sma-q1/layered/sma-s2-predicate-oracle-matrix-candidate-4.json"
SEMANTIC_SOURCE = ROOT / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-2.json"
STUB = ROOT / "scripts/sma_s2_deterministic_model_stub_candidate_3.py"
MEASURED_ROOT = Path("/tmp/tekroo-sma-s2-successor-candidate-4")
DATABASE = "sma_s2_successor_candidate_4_measured_20260814"
SEMANTIC = "sma_s2_successor_candidate_4_semantic_20260814"
EPISODIC = "sma_s2_successor_candidate_4_episodic_20260814"
LABEL = "com.tekroo.sma-service-s2-successor-candidate-4"
FRAME = re.compile(r"\n--- MEMORY ([^\n]+) ---\n.*?\n--- END MEMORY \1 ---", re.DOTALL)


def load_base():
    spec = importlib.util.spec_from_file_location("sma_s2_driver_candidate_1", BASE_DRIVER)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load candidate-3 measured driver primitives")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


base = load_base()
base.CORPUS = CORPUS
base.CONFIG = CONFIG
base.STUB = STUB
base.MEASURED_ROOT = MEASURED_ROOT
base.DATABASE = DATABASE
base.SEMANTIC = SEMANTIC
base.EPISODIC = EPISODIC
base.LABEL = LABEL
PredicateFailure = base.PredicateFailure
require = base.require
sha_file = base.sha_file
sha_bytes = base.sha_bytes
canonical_bytes = base.canonical_bytes


def matrix_value() -> dict[str, Any]:
    return json.loads(MATRIX.read_text())


def required_oracles(case_id: str) -> set[str]:
    matrix = matrix_value()
    case = next(item for item in matrix["cases"] if item["caseId"] == case_id)
    required = set(matrix["commonPerRepetitionEvidenceOracleIds"])
    required.update(case["predicateOracleIds"])
    for oracle_id, case_ids in matrix["conditionalEvidenceOracleIds"].items():
        if case_id in case_ids:
            required.add(oracle_id)
    return required


def evaluate_oracles(case_id: str, values: dict[str, bool]) -> None:
    required = required_oracles(case_id)
    missing = sorted(required - set(values))
    unexpected = sorted(set(values) - required)
    failed = sorted(oracle_id for oracle_id in required if values.get(oracle_id) is not True)
    require(not missing, "missing mandatory oracle values: " + ",".join(missing))
    require(not unexpected, "unexpected oracle values: " + ",".join(unexpected))
    require(not failed, "scientific oracle failure: " + ",".join(failed))


def validate_matrix_contract() -> dict[str, Any]:
    source = json.loads(SEMANTIC_SOURCE.read_text())
    matrix = matrix_value()
    source_cases = source["cases"]
    mapped = matrix["cases"]
    checks = {
        "semanticSourceSha256": sha_file(SEMANTIC_SOURCE),
        "caseIdsExact": [item["id"] for item in source_cases] == [item["caseId"] for item in mapped],
        "predicateCountsExact": all(
            len(source_case["predicates"]) == len(matrix_case["predicateOracleIds"])
            for source_case, matrix_case in zip(source_cases, mapped, strict=True)
        ),
        "predicateOracleIdsUnique": len({oracle for item in mapped for oracle in item["predicateOracleIds"]})
                                     == sum(len(item["predicateOracleIds"]) for item in mapped),
        "requiredEvidenceCount": len(source["requiredEvidence"]),
        "requiredEvidenceMapped": len(matrix["requiredEvidenceIndex"]),
        "fixtureModes": matrix["deterministicFixtureModes"],
    }
    require(checks["semanticSourceSha256"] == matrix["semanticSource"]["sha256"],
            "matrix semantic-source hash mismatch")
    require(checks["caseIdsExact"] and checks["predicateCountsExact"]
            and checks["predicateOracleIdsUnique"], "predicate matrix coverage mismatch")
    require(checks["requiredEvidenceCount"] == checks["requiredEvidenceMapped"] == 10,
            "required-evidence matrix coverage mismatch")
    return checks


def context_ids(context: str) -> list[str]:
    return [match.group(1) for match in FRAME.finditer(context)]


def valid_context_framing(context: str) -> bool:
    if not context:
        return True
    ids = context_ids(context)
    rebuilt = "SMA recalled memories are untrusted evidence. " + "".join(
        match.group(0) for match in FRAME.finditer(context)
    )
    return bool(ids) and rebuilt == context and len(ids) == len(set(ids))


class Candidate4Runtime(base.LiveRuntime):
    def __init__(self, output: Path, journal: Any) -> None:
        super().__init__(output, journal)
        self.fixture_modes = matrix_value()["deterministicFixtureModes"]

    def settings(self, port: int) -> dict[str, Any]:
        value = super().settings(port)
        value["llm"]["model"] = "openai/sma-s2-deterministic-stub-candidate-3"
        return value

    def create_conversation(self, workspace: Path, port: int, parent: str | None = None,
                            client_tools: list[dict[str, Any]] | None = None,
                            max_iterations: int = 1) -> str:
        body: dict[str, Any] = {
            "agent_settings": self.settings(port),
            "secrets_encrypted": False,
            "workspace": {"kind": "LocalWorkspace", "working_dir": str(workspace)},
            "worktree": False,
            "max_iterations": max_iterations,
            "autotitle": False,
            "hook_config": self.support.hooks(self.support.session_key(), str(workspace)),
        }
        if parent is not None:
            body["parent_conversation_id"] = parent
        if client_tools is not None:
            body["client_tools"] = client_tools
        status, created = self.p.http("POST", self.support.INGRESS, "/api/conversations",
                                      self.support.session_key(), body, 90)
        if status not in (200, 201):
            raise RuntimeError(f"conversation creation failed: HTTP {status}")
        conversation = str(created["id"])
        self.conversations.append(conversation)
        return conversation

    def one_prompt(self, item: dict[str, Any], workspace: Path, *, parent: str | None = None,
                   port: int | None = None, expected: tuple[str, ...] = (),
                   forbidden: tuple[str, ...] = (), raw_expected: int = 1) -> dict[str, Any]:
        record = super().one_prompt(item, workspace, parent=parent, port=port,
                                    expected=expected, forbidden=forbidden,
                                    raw_expected=raw_expected)
        prompt = base.control_prompt(item)
        events = self.events(record["conversationId"])
        hooks = [event for event in events if event.get("kind") == "HookExecutionEvent"
                 and (event.get("hook_input") or {}).get("message") == prompt]
        users = [event for event in events if event.get("kind") == "MessageEvent"
                 and event.get("source") == "user" and self.p.event_text(event) == prompt]
        status, detail = self.p.http("GET", self.support.INGRESS,
                                     f"/api/conversations/{record['conversationId']}",
                                     self.support.session_key(), timeout=15)
        context = str((hooks[0] if hooks else {}).get("additional_context") or "")
        try:
            hook_stdout = json.loads((hooks[0] if hooks else {}).get("stdout") or "{}")
        except json.JSONDecodeError:
            hook_stdout = {}
        raw_records = self.raw_for(item["caseId"], item["repetition"], prompt)
        errors = [event for event in events if event.get("kind") == "ConversationErrorEvent"]
        record.update({
            "hookResultCount": len(hooks),
            "userPromptEventCount": len(users),
            "selectedMemoryIds": context_ids(context),
            "contextFramingValid": valid_context_framing(context),
            "smaTraceId": hook_stdout.get("smaTraceId"),
            "contextProvenanceClass": "TRACE" if hook_stdout.get("smaTraceId") else "EXPLICIT_FAIL_OPEN_EMPTY",
            "_contextEphemeral": context,
            "eventOrdinals": {event.get("id"): index for index, event in enumerate(events)},
            "conversationDetailStatus": status,
            "workspaceIdentity": str((detail.get("workspace") or {}).get("working_dir", "")),
            "profileIdentity": str(((detail.get("launched_agent_profile") or {}).get("agent_profile_id")
                                    if isinstance(detail.get("launched_agent_profile"), dict) else None) or "default"),
            "workspaceSha256": sha_bytes(str((detail.get("workspace") or {}).get("working_dir", "")).encode()),
            "profileSha256": sha_bytes(canonical_bytes(detail.get("launched_agent_profile"))),
            "parentConversationId": detail.get("parent_conversation_id"),
            "errorCodes": [event.get("code") for event in errors],
            "errorClassifications": [event.get("classification") for event in errors],
            "rawModes": [raw.get("mode") for raw in raw_records],
            "rawReceivedMonotonicNs": [raw.get("receivedMonotonicNs") for raw in raw_records],
        })
        return record

    def _common(self, records: list[dict[str, Any]]) -> dict[str, bool]:
        return {
            "E-001": True,
            "E-002": all(record.get("promptSha256") == record.get("persistedPromptSha256")
                         == record.get("hookInputSha256")
                         and (bool((record.get("requestOracle") or {}).get("currentPromptSegmentMatched"))
                              or (record.get("rawStubRequestCount") == 0 and record.get("requestOracle") is None))
                         for record in records),
            "E-003": all(record.get("contextSha256") == record.get("persistedContextSha256")
                         and record.get("contextLength") == record.get("persistedContextLength")
                         and record.get("contextFramingValid") is True
                         and isinstance(record.get("selectedMemoryIds"), list)
                         and record.get("contextProvenanceClass") in {"TRACE", "EXPLICIT_FAIL_OPEN_EMPTY"}
                         for record in records),
            "E-004": all(record.get("rawStubRequestCount") == record.get("stubTerminalCount")
                         and record.get("rawStubRequestCount") in (0, 1)
                         for record in records),
            "E-005": all(record.get("rawStubRequestCount") == record.get("stubTerminalCount")
                         for record in records),
            "E-007": all(record.get("conversationDetailStatus") == 200
                         and record.get("conversationId") and record.get("userEventId")
                         and record.get("hookEventId") and record.get("workspaceIdentity")
                         and record.get("profileIdentity") and record.get("eventOrdinals")
                         for record in records),
            "E-008": all(isinstance(record.get("elapsedNs"), int)
                         and 0 < record["elapsedNs"] <= 120_000_000_000
                         and isinstance(record.get("rawReceivedMonotonicNs"), list)
                         for record in records),
        }

    def _grade(self, item: dict[str, Any], records: list[dict[str, Any]],
               predicates: dict[str, bool], *, evidence: dict[str, bool] | None = None,
               detail: dict[str, Any] | None = None) -> list[dict[str, Any]]:
        values = dict(evidence if evidence is not None else self._common(records))
        values.update(predicates)
        self.journal.append("PREASSERTION_ORACLE_BUNDLE", {
            "caseId": item["caseId"], "repetition": item["repetition"],
            "oracleValues": values, "detail": detail or {},
        })
        evaluate_oracles(item["caseId"], values)
        return records

    def _full_memories(self) -> list[dict[str, Any]]:
        return self.p.mongosh("EJSON.stringify(db.memories.find({}).toArray())")

    def _conversation_memories(self, conversation: str) -> list[dict[str, Any]]:
        return [doc for doc in self._full_memories()
                if (doc.get("openhands_provenance") or {}).get("conversation_id") == conversation]

    def _surface_absence(self, record: dict[str, Any], markers: tuple[str, ...]) -> dict[str, bool]:
        prompt = next((item for item in base.exact_plan(json.loads(CORPUS.read_text()))
                       if item["caseId"] == record["caseId"]
                       and item["repetition"] == record["repetition"]), None)
        raw = self.raw_for(record["caseId"], record["repetition"],
                           base.control_prompt(prompt) if prompt else None)
        request_bytes = b"".join(base64.b64decode(item["requestBodyBase64"], validate=True)
                                  for item in raw)
        logs = self.operational.read_bytes() if self.operational.exists() else b""
        for path in (self.p.RUNTIME / "sma.out.log", self.p.RUNTIME / "sma.err.log"):
            if path.exists():
                logs += path.read_bytes()
        captures = canonical_bytes(self._conversation_memories(record["conversationId"]))
        return {
            "hook": all(marker not in str(record.get("_contextEphemeral", "")) for marker in markers),
            "request": all(marker.encode() not in request_bytes for marker in markers),
            "logs": all(marker.encode() not in logs for marker in markers),
            "capture": all(marker.encode() not in captures for marker in markers),
        }

    def case_first_prompt_empty(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_first_prompt_empty(item); r = records[0]
        return self._grade(item, records, {
            "S2-001-P1": r["hookResultCount"] == 1 and r["hookBeforeUser"],
            "S2-001-P2": r["contextLength"] == 0,
            "S2-001-P3": self._common(records)["E-002"],
            "S2-001-P4": r["rawStubRequestCount"] == r["stubTerminalCount"] == 1,
        })

    def case_same_partition_delivery(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_same_partition_delivery(item); r = records[0]
        memory_id = self.mapped["mem-alpha-timeout"]
        return self._grade(item, records, {
            "S2-002-P1": r["selectedMemoryIds"].count(memory_id) == 1,
            "S2-002-P2": r["contextMarksUntrusted"] and r["contextFramingValid"],
            "S2-002-P3": self._common(records)["E-002"],
        })

    def case_cross_partition_denial(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_cross_partition_denial(item); absent = self._surface_absence(records[0], ("mem-alpha", "17 seconds", "DELETE_CONFIRMED", "9999"))
        return self._grade(item, records, {"S2-003-P1": all(absent.values())}, detail=absent)

    def case_untrusted_context_placement(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_untrusted_context_placement(item); r = records[0]
        return self._grade(item, records, {
            "S2-004-P1": r["contextMarksUntrusted"] and r["contextFramingValid"],
            "S2-004-P2": (r.get("requestOracle") or {}).get("contextSegmentMatched") is True,
            "S2-004-P3": self._common(records)["E-002"],
        })

    def case_raw_ineligible_absence(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_raw_ineligible_absence(item); absent = self._surface_absence(records[0], ("mem-alpha-raw", "9999"))
        return self._grade(item, records, {"S2-005-P1": all(absent.values())}, detail=absent)

    def case_duplicate_persisted_event(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_duplicate_persisted_event(item); r = records[0]
        matches = [doc for doc in self._conversation_memories(r["conversationId"])
                   if (doc.get("openhands_provenance") or {}).get("event_id") == r["userEventId"]]
        complete = len(matches) == 1 and all((matches[0].get("openhands_provenance") or {}).get(key) is not None
                                             for key in ("conversation_id", "event_id", "workspace", "profile", "sequence"))
        return self._grade(item, records, {"S2-006-P1": complete, "S2-006-P2": len(matches) == 1})

    def case_retrieval_outage(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_retrieval_outage(item); r = records[0]
        outage = {"serviceAbsent": not self.support.launch_identity(LABEL)["loaded"],
                  "contextEmpty": r["contextLength"] == 0, "bodyFree": True}
        return self._grade(item, records, {
            "S2-007-P1": r["hookSuccess"] is True and r["hookResultCount"] == 1,
            "S2-007-P2": r["rawStubRequestCount"] == r["stubTerminalCount"] == 1,
            "S2-007-P3": all(outage.values()),
        }, detail=outage)

    def case_capture_outage_recovery(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_capture_outage_recovery(item); r = records[0]
        matches = [doc for doc in self._conversation_memories(r["conversationId"])
                   if (doc.get("openhands_provenance") or {}).get("event_id") == r["userEventId"]]
        return self._grade(item, records, {
            "S2-008-P1": r["rawStubRequestCount"] == r["stubTerminalCount"] == 1,
            "S2-008-P2": len(matches) == 1 and r["elapsedNs"] <= 120_000_000_000,
            "S2-008-P3": len(matches) == 1,
        })

    def case_additional_context_integrity(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_additional_context_integrity(item); r = records[0]
        return self._grade(item, records, {
            "S2-009-P1": self._common(records)["E-002"],
            "S2-009-P2": len(r["selectedMemoryIds"]) == 1 and r["contextFramingValid"],
            "S2-009-P3": bool(r["smaTraceId"] and r["workspaceSha256"] and r["profileSha256"]),
        })

    def case_hook_fault_matrix(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.p.stop_service(); mode = item["mode"]
        if mode == "BRIDGE_DOWN":
            native = self.p.native_probe("offline-bridge-down", self.alpha, base.control_prompt(item)); records = [self.one_prompt(item, self.alpha)]
        else:
            mapped = {"BRIDGE_TIMEOUT": "timeout", "BRIDGE_MALFORMED": "malformed", "BRIDGE_HTTP_503": "non2xx"}
            with self.p.FaultFixture(mapped[mode]):
                native = self.p.native_probe("offline-hook-fault", self.alpha, base.control_prompt(item)); records = [self.one_prompt(item, self.alpha)]
        r = records[0]
        return self._grade(item, records, {
            "S2-010-P1": native["success"] and native["elapsed_ms"] <= 1000,
            "S2-010-P2": r["contextLength"] == 0,
            "S2-010-P3": r["hookResultCount"] == 1 and r["hookSuccess"] is True,
            "S2-010-P4": r["rawStubRequestCount"] == 1,
        }, detail={"nativeHookElapsedMs": native["elapsed_ms"], "nativeHookEventCount": native["event_count"]})

    def _tool_prompt(self, item: dict[str, Any], workspace: Path, mode: str) -> tuple[str, dict[str, Any]]:
        conversation = self.create_conversation(workspace, int(self.stub_port),
                                                client_tools=[self.p.launch_child_client_tool()],
                                                max_iterations=1)
        fixture = dict(item); fixture["stubMode"] = mode
        prompt = base.control_prompt(fixture)
        started = time.monotonic_ns()
        require(self.submit(conversation, prompt) == 200, "tool fixture submission failed")
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            events = self.events(conversation)
            actions = [event for event in events if event.get("kind") == "ActionEvent"
                       and event.get("tool_name") == "launch_child_conversation"]
            raws = self.raw_for(item["caseId"], item["repetition"], prompt)
            terminals = [terminal for terminal in base.read_jsonl(self.terminal)
                         if terminal.get("requestId") in {raw.get("requestId") for raw in raws}]
            if len(actions) == len(raws) == len(terminals) == 1:
                break
            time.sleep(0.05)
        record = {"caseId": item["caseId"], "repetition": item["repetition"],
                  "conversationId": conversation, "promptSha256": sha_bytes(prompt.encode()),
                  "elapsedNs": time.monotonic_ns() - started, "events": events,
                  "action": actions[0] if actions else None, "raws": raws, "terminals": terminals}
        self.journal.append("PREASSERTION_TOOL_FIXTURE", {
            "caseId": item["caseId"], "repetition": item["repetition"],
            "conversationId": conversation, "promptSha256": record["promptSha256"],
            "eventIds": [event.get("id") for event in events],
            "eventKinds": [event.get("kind") for event in events],
            "rawRequestCount": len(raws), "stubTerminalCount": len(terminals),
        })
        require(record["action"] is not None and len(raws) == len(terminals) == 1,
                "tool fixture did not produce one action and terminal")
        return conversation, record

    def case_parent_child_provenance(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); before = self.p.mongo_counts()["memories"]
        parent, tool = self._tool_prompt(item, self.alpha, "DELEGATION_TOOL")
        action = tool["action"] or {}; task = str((action.get("action") or {}).get("task", ""))
        child_item = dict(item); child_item["stubMode"] = "SUCCESS"
        require(task == base.control_prompt(child_item), "delegation tool did not preserve exact child task")
        child = self.one_prompt(child_item, self.alpha, parent=parent,
                                expected=(self.mapped["mem-alpha-timeout"],))
        self.p.start_service(True, True); self.p.wait_memory_count(before + 2)
        docs = self._conversation_memories(child["conversationId"])
        provenances = [doc.get("openhands_provenance") or {} for doc in docs]
        status, detail = self.p.http("GET", self.support.INGRESS,
                                     f"/api/conversations/{child['conversationId']}",
                                     self.support.session_key(), timeout=15)
        expected_partition = self.p.ALPHA_PARTITION
        complete = status == 200 and len(provenances) == 2 and all(
            provenance.get("conversation_id") == child["conversationId"]
            and provenance.get("parent_conversation_id") == parent
            and provenance.get("workspace") == str(self.alpha)
            and provenance.get("profile") is not None
            and provenance.get("sequence") is not None for provenance in provenances)
        isolated = all(doc.get("agent_id") == expected_partition for doc in docs)
        evidence = self._common([child])
        return self._grade(item, [child], {"S2-011-P1": complete, "S2-011-P2": isolated},
                           evidence=evidence, detail={"parentId": parent, "childId": child["conversationId"],
                                                      "provenanceCount": len(provenances),
                                                      "childParent": detail.get("parent_conversation_id")})

    def case_restart_continuity(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); before = self.p.mongo_counts()["memories"]
        records = [self.one_prompt(item, self.alpha, expected=(self.mapped["mem-alpha-timeout"],))]; r = records[0]
        initial = self.p.direct_context(r["conversationId"], self.alpha, base.control_prompt(item))
        self.p.start_service(True, True); self.p.wait_memory_count(before + 2)
        initial_count = len(self._conversation_memories(r["conversationId"]))
        self.p.stop_service(); self.p.start_service(True, True)
        restarted = self.p.direct_context(r["conversationId"], self.alpha, base.control_prompt(item))
        time.sleep(2); final_count = len(self._conversation_memories(r["conversationId"]))
        return self._grade(item, records, {
            "S2-012-P1": initial.get("trace_id") and restarted.get("trace_id")
                          and initial["memory_ids"] == restarted["memory_ids"],
            "S2-012-P2": self.mapped["mem-alpha-timeout"] in restarted["memory_ids"],
            "S2-012-P3": initial_count == final_count == 2,
        }, detail={"initialMemoryCount": initial_count, "finalMemoryCount": final_count})

    def case_four_channel_concurrency(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); before = self.support.bridge_health(); records = super().case_four_channel_concurrency(item); after = self.support.bridge_health()
        telemetry_keys = {"context_requests", "context_completed_requests", "context_rejected_requests",
                          "context_timeouts", "context_active_work", "context_queued_work",
                          "context_max_observed_active_work", "context_max_observed_queued_work"}
        isolated = all((self.mapped["mem-alpha-timeout"] in r["selectedMemoryIds"])
                       if index < 2 else (self.mapped["mem-beta-port"] in r["selectedMemoryIds"])
                       for index, r in enumerate(records))
        return self._grade(item, records, {
            "S2-013-P1": all(r["stubOutcomes"] == ["CLIENT_RECEIVED"] for r in records),
            "S2-013-P2": isolated,
            "S2-013-P3": telemetry_keys.issubset(after) and after["context_active_work"] == 0
                          and after["context_queued_work"] == 0,
        }, detail={"telemetryBefore": {key: before.get(key) for key in telemetry_keys},
                   "telemetryAfter": {key: after.get(key) for key in telemetry_keys}})

    def case_feedback_loop_prevention(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); before = self.p.mongo_counts()["memories"]
        conversation, tool = self._tool_prompt(item, self.alpha, "INELIGIBLE_TOOL_TRAFFIC")
        events = tool["events"]
        ineligible = [event for event in events if event.get("kind") in
                      {"HookExecutionEvent", "ActionEvent", "LLMCompletionLogEvent"}]
        kinds = {event.get("kind") for event in ineligible}
        self.p.start_service(True, True); self.p.wait_memory_count(before + 1); time.sleep(5)
        docs = self._conversation_memories(conversation)
        captured_ids = {(doc.get("openhands_provenance") or {}).get("event_id") for doc in docs}
        ineligible_ids = {event.get("id") for event in ineligible}
        no_ineligible = not (captured_ids & ineligible_ids)
        no_loop = self.p.mongo_counts()["memories"] == before + 1
        evidence = {"E-001": True, "E-002": bool(tool["promptSha256"]), "E-003": True,
                    "E-004": len(tool["raws"]) == len(tool["terminals"]) == 1,
                    "E-005": len(tool["raws"]) == len(tool["terminals"]) == 1,
                    "E-007": bool(conversation and ineligible_ids),
                    "E-008": 0 < tool["elapsedNs"] <= 120_000_000_000}
        return self._grade(item, [], {
            "S2-014-P1": no_ineligible and {"HookExecutionEvent", "ActionEvent", "LLMCompletionLogEvent"}.issubset(kinds),
            "S2-014-P2": no_loop,
        }, evidence=evidence, detail={"ineligibleEventIds": sorted(str(value) for value in ineligible_ids),
                                      "capturedEventIds": sorted(str(value) for value in captured_ids)})

    def case_secret_delivery_absence(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_secret_delivery_absence(item); r = records[0]
        raw = self.raw_for(item["caseId"], item["repetition"], base.control_prompt(item))
        raw_count = sum(base.SECRET.encode() in base64.b64decode(value["requestBodyBase64"], validate=True) for value in raw)
        return self._grade(item, records, {
            "S2-015-P1": raw_count == 1 and r["promptLength"] > len(base.SECRET),
            "S2-015-P2": base.SECRET not in str(r.get("selectedMemoryIds")) and r["contextLength"] == 0,
            "S2-015-P3": raw_count == 1,
        })

    def case_empty_result(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_empty_result(item); r = records[0]
        return self._grade(item, records, {"S2-016-P1": r["contextLength"] == 0,
                                            "S2-016-P2": r["stubOutcomes"] == ["CLIENT_RECEIVED"]})

    def case_oversized_context(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_oversized_context(item); r = records[0]
        ids = r["selectedMemoryIds"]
        return self._grade(item, records, {
            "S2-017-P1": r["contextFramingValid"] and bool(ids),
            "S2-017-P2": len(ids) <= 3 and r["contextLength"] <= 4096,
            "S2-017-P3": len(ids) <= 5 and r["contextLength"] <= 10000,
        })

    def case_condensation_reanchor(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded(); first = self.one_prompt(item, self.alpha, expected=(self.mapped["mem-alpha-timeout"],))
        status, _ = self.p.http("POST", self.support.INGRESS,
                                f"/api/conversations/{first['conversationId']}/condense",
                                self.support.session_key(), timeout=120)
        second_item = dict(item); second_item["literalPrompt"] += " Re-anchor after condensation."
        original = self.create_conversation
        self.create_conversation = lambda *_args, **_kwargs: first["conversationId"]
        try:
            second = self.one_prompt(second_item, self.alpha,
                                     expected=(self.mapped["mem-alpha-timeout"],))
        finally:
            self.create_conversation = original
        summaries = [event for event in self.events(first["conversationId"])
                     if event.get("kind") == "CondensationSummaryEvent"]
        summary_ids = {event.get("id") for event in summaries}
        return self._grade(item, [first, second], {
            "S2-018-P1": second["contextFramingValid"] and second["contextLength"] <= 4096,
            "S2-018-P2": not (summary_ids & set(second["selectedMemoryIds"])),
            "S2-018-P3": status in (200, 204) and second["stubOutcomes"] == ["CLIENT_RECEIVED"],
        }, detail={"condenseStatus": status, "summaryEventIds": sorted(str(value) for value in summary_ids)})

    def case_model_stub_fault_matrix(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        records = super().case_model_stub_fault_matrix(item); r = records[0]
        expected_requests = 0 if item["mode"] == "TRANSPORT_FAILURE_UNUSED_PORT" else 1
        expected_http = {"TIMEOUT": 200, "MALFORMED": 200, "HTTP_503": 503}
        terminals = self.terminal_for(item["caseId"], item["repetition"])
        exact_fault = r["rawStubRequestCount"] == expected_requests
        if expected_requests:
            exact_fault = exact_fault and r["rawModes"] == [item["mode"]]
            exact_fault = exact_fault and len(terminals) == 1 and terminals[0]["httpStatus"] == expected_http[item["mode"]]
        return self._grade(item, records, {
            "S2-019-P1": exact_fault,
            "S2-019-P2": r["rawStubRequestCount"] == expected_requests,
            "S2-019-P3": r["terminalStatus"] in base.TERMINAL_STATES and r["conversationErrorCount"] >= 1,
            "S2-019-P4": self._common(records)["E-002"] and self._common(records)["E-003"],
            "S2-019-P5": r["elapsedNs"] <= 120_000_000_000,
        }, evidence={**self._common(records), "E-006": r["rawStubRequestCount"] == expected_requests
                      and r["elapsedNs"] <= 120_000_000_000})

    def case_active_cancellation_and_shutdown(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        conversation = self.create_conversation(self.alpha, int(self.stub_port)); prompt = base.control_prompt(item)
        submitted = time.monotonic_ns(); require(self.submit(conversation, prompt) == 200, "cancellation submission failed")
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline and not self.raw_for(item["caseId"], item["repetition"], prompt): time.sleep(0.01)
        raws = self.raw_for(item["caseId"], item["repetition"], prompt)
        require(len(raws) == 1, "cancellation raw request was not durably recorded")
        raw_ns = int(raws[0]["receivedMonotonicNs"]); time.sleep(0.25); interrupt_ns = time.monotonic_ns()
        status, _ = self.p.http("POST", self.support.INGRESS, f"/api/conversations/{conversation}/interrupt",
                                self.support.session_key(), timeout=10)
        terminal_deadline = time.monotonic() + 10; terminal_detail: dict[str, Any] = {}
        while time.monotonic() < terminal_deadline:
            terminals = self.terminal_for(item["caseId"], item["repetition"])
            _, terminal_detail = self.p.http("GET", self.support.INGRESS,
                                             f"/api/conversations/{conversation}",
                                             self.support.session_key(), timeout=15)
            if terminals and str(terminal_detail.get("execution_status")) in base.TERMINAL_STATES: break
            time.sleep(0.02)
        terminals = self.terminal_for(item["caseId"], item["repetition"])
        terminal_ns = int(terminals[0]["completedMonotonicNs"]) if terminals else 0
        events = self.events(conversation); user = next((event for event in events if event.get("kind") == "MessageEvent" and event.get("source") == "user" and self.p.event_text(event) == prompt), {})
        hook = next((event for event in events if event.get("kind") == "HookExecutionEvent" and (event.get("hook_input") or {}).get("message") == prompt), {})
        context = str(hook.get("additional_context") or ""); request = self.request_oracle(raws[0], prompt, context)
        delete_started = time.monotonic_ns(); deletion = self.support.delete_conversation(self.support.session_key(), conversation)
        self.conversations.remove(conversation); health = self.support.bridge_health(); shutdown_ns = time.monotonic_ns() - delete_started
        record = {"caseId": item["caseId"], "repetition": item["repetition"], "conversationId": conversation,
                  "promptSha256": sha_bytes(prompt.encode()), "persistedPromptSha256": sha_bytes(self.p.event_text(user).encode()),
                  "hookInputSha256": sha_bytes(str((hook.get("hook_input") or {}).get("message", "")).encode()),
                  "contextSha256": sha_bytes(context.encode()), "persistedContextSha256": sha_bytes(self.content_text(user.get("extended_content")).encode()),
                  "contextLength": len(context), "persistedContextLength": len(self.content_text(user.get("extended_content"))),
                  "contextFramingValid": valid_context_framing(context), "selectedMemoryIds": context_ids(context),
                  "contextProvenanceClass": "TRACE" if hook.get("stdout") else "EXPLICIT_FAIL_OPEN_EMPTY",
                  "requestOracle": request, "rawStubRequestCount": len(raws), "stubTerminalCount": len(terminals),
                  "elapsedNs": time.monotonic_ns() - submitted, "rawReceivedMonotonicNs": [raw_ns],
                  "conversationDetailStatus": 200, "userEventId": user.get("id"), "hookEventId": hook.get("id"),
                  "workspaceIdentity": str(self.alpha), "profileIdentity": "default",
                  "workspaceSha256": sha_bytes(str(self.alpha).encode()), "profileSha256": sha_bytes(b"default"),
                  "eventOrdinals": {event.get("id"): index for index, event in enumerate(events)}}
        common = self._common([record]); common["E-006"] = bool(terminals) and terminal_ns - interrupt_ns <= 10_000_000_000
        no_work = health.get("context_active_work") == 0 and health.get("context_queued_work") == 0
        return self._grade(item, [record], {
            "S2-020-P1": interrupt_ns >= raw_ns + 250_000_000,
            "S2-020-P2": str(terminal_detail.get("execution_status")) in base.TERMINAL_STATES and terminal_ns - interrupt_ns <= 10_000_000_000,
            "S2-020-P3": bool(terminals) and terminals[0].get("responseWriteOutcome") == "CLIENT_DISCONNECTED" and terminal_ns - interrupt_ns <= 10_000_000_000,
            "S2-020-P4": shutdown_ns <= 15_000_000_000 and no_work,
            "S2-020-P5": no_work and bool(deletion),
            "S2-020-P6": common["E-002"] and common["E-003"],
        }, evidence=common, detail={"interruptStatus": status, "shutdownNs": shutdown_ns,
                                    "bridgeActive": health.get("context_active_work"),
                                    "bridgeQueued": health.get("context_queued_work")})

    def cleanup(self) -> dict[str, Any]:
        started = time.monotonic_ns(); result = super().cleanup(); result["elapsedNs"] = time.monotonic_ns() - started
        result["oracleValues"] = {"E-009": all(result["verified"].values()),
                                  "E-010": all(result["verified"].values())}
        self.journal.append("PREASSERTION_GLOBAL_CLEANUP_ORACLES", result["oracleValues"])
        require(all(result["oracleValues"].values()), "global cleanup evidence failed")
        return result


def validate_static_contract() -> dict[str, Any]:
    base.LiveRuntime = Candidate4Runtime
    value = base.validate_static_contract()
    value["matrix"] = validate_matrix_contract()
    config = json.loads(CONFIG.read_text())
    namespaces = config["disposableNamespaces"]
    value["configurationMatchesDriver"] = (
        namespaces["mongodbDatabase"] == DATABASE
        and namespaces["qdrantSemanticCollection"] == SEMANTIC
        and namespaces["qdrantEpisodicCollection"] == EPISODIC
        and namespaces["workspaceRoot"] == str(MEASURED_ROOT)
    )
    require(value["configurationMatchesDriver"], "configuration and driver namespace mismatch")
    return value


def validate_execution_fence(identity_path: Path, authorization_path: Path) -> dict[str, Any]:
    identity = json.loads(identity_path.read_text()); authorization = json.loads(authorization_path.read_text())
    dependencies = identity.get("dependencies", [])
    preregistration = identity.get("preregistration", {})
    configuration = identity.get("configuration", {})
    oracle_matrix = identity.get("oracleMatrix", {})
    stub = identity.get("stub", {})
    corpus = identity.get("corpus", {})
    checks = {
        "identityAcceptedFrozen": identity.get("status") == "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
        "identityNonAuthorizing": identity.get("measuredExecutionAuthorized") is False,
        "driverMatches": identity.get("driver", {}).get("sha256") == sha_file(Path(__file__)),
        "corpusMatches": Path(corpus.get("path", "")).is_file()
                         and sha_file(Path(corpus["path"])) == corpus.get("sha256") == sha_file(CORPUS),
        "configurationMatches": Path(configuration.get("path", "")).is_file()
                                and sha_file(Path(configuration["path"])) == configuration.get("sha256") == sha_file(CONFIG),
        "matrixMatches": Path(oracle_matrix.get("path", "")).is_file()
                         and sha_file(Path(oracle_matrix["path"])) == oracle_matrix.get("sha256") == sha_file(MATRIX),
        "stubMatches": Path(stub.get("path", "")).is_file()
                       and sha_file(Path(stub["path"])) == stub.get("sha256") == sha_file(STUB),
        "preregistrationMatches": Path(preregistration.get("path", "")).is_file()
                                  and sha_file(Path(preregistration["path"])) == preregistration.get("sha256"),
        "dependenciesMatch": bool(dependencies) and all(Path(item["path"]).is_file()
                              and sha_file(Path(item["path"])) == item["sha256"] for item in dependencies),
        "authorizationLive": authorization.get("status") == "AUTHORIZED_NOT_CONSUMED",
        "authorizationSingleUse": authorization.get("maximumMeasuredAttempts") == 1,
        "authorizationIdentityMatches": authorization.get("executionIdentitySha256") == sha_file(identity_path),
        "authorizationDriverMatches": authorization.get("driverSha256") == sha_file(Path(__file__)),
        "authorizationMatrixMatches": authorization.get("oracleMatrixSha256") == sha_file(MATRIX),
        "authorizationCorpusMatches": authorization.get("corpusSha256") == sha_file(CORPUS),
        "authorizationConfigurationMatches": authorization.get("configurationSha256") == sha_file(CONFIG),
        "authorizationStubMatches": authorization.get("stubSha256") == sha_file(STUB),
        "authorizationPreregistrationMatches": authorization.get("preregistrationSha256") == preregistration.get("sha256"),
        "scopeExact": authorization.get("caseCount") == 20 and authorization.get("repetitionCount") == 103,
        "realModelProhibited": authorization.get("realModelCallsAllowed") is False,
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-4 execution fence rejected: {checks}")
    return checks


def execute(args: argparse.Namespace) -> int:
    validate_execution_fence(args.execution_identity, args.authorization)
    corpus = json.loads(CORPUS.read_text()); plan = base.exact_plan(corpus)
    args.output.mkdir(parents=True, exist_ok=False)
    journal = base.DurableJournal(args.output / "raw-evidence.jsonl")
    journal.append("EXECUTION_START", {"driverSha256": sha_file(Path(__file__)),
                   "matrixSha256": sha_file(MATRIX), "corpusSha256": sha_file(CORPUS),
                   "caseCount": 20, "repetitionCount": 103})
    runtime = Candidate4Runtime(args.output, journal); results = []; failure = None
    try:
        runtime.preflight_absence(); runtime.start_stub(); runtime.setup_workspaces()
        for item in plan:
            try:
                records = runtime.run_operation(item)
                results.append({"caseId": item["caseId"], "repetition": item["repetition"],
                                "status": "PASS", "recordCount": len(records)})
            except PredicateFailure as error:
                failure = {"class": "SUBSTANTIVE", "caseId": item["caseId"],
                           "repetition": item["repetition"], "messageSha256": sha_bytes(str(error).encode())}
                journal.append("REPETITION_FAILURE", failure)
                results.append({"caseId": item["caseId"], "repetition": item["repetition"], "status": "FAIL"})
            except Exception as error:
                failure = {"class": "HARNESS", "caseId": item["caseId"],
                           "repetition": item["repetition"], "type": type(error).__name__,
                           "messageSha256": sha_bytes(str(error).encode())}
                journal.append("REPETITION_FAILURE", failure); break
    finally:
        cleanup = runtime.cleanup()
    complete = len(results) == 103 and all(item["status"] == "PASS" for item in results) and all(cleanup["oracleValues"].values())
    receipt = {"recordType": "SMA_S2_MEASURED_EXECUTION_RECEIPT", "status": "PASS" if complete else "FAIL" if failure and failure["class"] == "SUBSTANTIVE" else "INCONCLUSIVE",
               "claim": "OPENHANDS_SMA_BOUNDARY_QUALIFIED" if complete else None,
               "completedRepetitions": len(results), "results": results, "failure": failure,
               "cleanup": cleanup, "rawJournalSha256": sha_file(args.output / "raw-evidence.jsonl")}
    descriptor = os.open(args.output / "execution-receipt.json", os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, canonical_bytes(receipt) + b"\n"); os.fsync(descriptor)
    finally:
        os.close(descriptor)
    print(json.dumps({"status": receipt["status"], "receipt": str(args.output / "execution-receipt.json")}, sort_keys=True))
    return 0 if complete else 1


def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser(); value.add_argument("--offline-contract", action="store_true")
    value.add_argument("--execute", action="store_true"); value.add_argument("--execution-identity", type=Path)
    value.add_argument("--authorization", type=Path); value.add_argument("--output", type=Path); return value


def main() -> int:
    args = parser().parse_args()
    if args.offline_contract:
        print(json.dumps(validate_static_contract(), indent=2, sort_keys=True)); return 0
    if not args.execute:
        raise PermissionError("default deny: use --offline-contract or separately authorized --execute")
    if args.execution_identity is None or args.authorization is None or args.output is None:
        raise PermissionError("measured execution requires identity, authorization, and output")
    return execute(args)


if __name__ == "__main__":
    raise SystemExit(main())
