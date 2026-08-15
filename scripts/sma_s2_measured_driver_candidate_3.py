#!/usr/bin/env python3
"""Candidate-5 SMA-S2 driver with semantic evidence-field oracles.

Candidate 4 remains immutable.  This successor changes only the harness
implementation identified by the candidate-4 coordinator review.  The accepted
candidate-2 cases, predicates, thresholds, corpus, and claim remain authoritative.
"""

from __future__ import annotations

import base64
import importlib.util
import json
import shutil
import time
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
DRIVER = Path(__file__).resolve()
BASE_DRIVER = ROOT / "scripts/sma_s2_measured_driver_candidate_2.py"
CONFIG = ROOT / "investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-5.json"
MATRIX = ROOT / "investigations/sma-q1/layered/sma-s2-predicate-oracle-matrix-candidate-4.json"
CORPUS = ROOT / "investigations/sma-q1/layered/sma-s2-measured-corpus-candidate-1.json"
SEMANTIC_SOURCE = ROOT / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-2.json"
STUB = ROOT / "scripts/sma_s2_deterministic_model_stub_candidate_3.py"
MEASURED_ROOT = Path("/tmp/tekroo-sma-s2-successor-candidate-5")
DATABASE = "sma_s2_successor_candidate_5_measured_20260814"
SEMANTIC = "sma_s2_successor_candidate_5_semantic_20260814"
EPISODIC = "sma_s2_successor_candidate_5_episodic_20260814"
LABEL = "com.tekroo.sma-service-s2-successor-candidate-5"
CONDENSATION_MARKER = "STUB_OK:UNBOUND_CASE:-1"


def load_candidate4():
    spec = importlib.util.spec_from_file_location("sma_s2_driver_candidate_2", BASE_DRIVER)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load candidate-4 driver")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


c4 = load_candidate4()
CANDIDATE3_PRIMITIVE_RUNTIME = c4.base.LiveRuntime
for module in (c4, c4.base):
    module.CONFIG = CONFIG
    module.MATRIX = MATRIX
    module.CORPUS = CORPUS
    module.STUB = STUB
    module.MEASURED_ROOT = MEASURED_ROOT
    module.DATABASE = DATABASE
    module.SEMANTIC = SEMANTIC
    module.EPISODIC = EPISODIC
    module.LABEL = LABEL
c4.__file__ = str(DRIVER)

PredicateFailure = c4.PredicateFailure
require = c4.require
sha_file = c4.sha_file
sha_bytes = c4.sha_bytes
canonical_bytes = c4.canonical_bytes
matrix_value = c4.matrix_value
required_oracles = c4.required_oracles
evaluate_oracles = c4.evaluate_oracles
context_ids = c4.context_ids
valid_context_framing = c4.valid_context_framing
base = c4.base


def classify_context_provenance(context: str, trace_id: Any) -> str:
    if trace_id:
        return "TRACE"
    if context == "":
        return "EXPLICIT_FAIL_OPEN_EMPTY"
    return "MISSING"


def trace_id_from_stdout(value: Any) -> Any:
    try:
        parsed = json.loads(str(value or "{}"))
    except json.JSONDecodeError:
        return None
    return parsed.get("smaTraceId") if isinstance(parsed, dict) else None


def bodies_absent_from_surfaces(surfaces: dict[str, bytes], bodies: tuple[bytes, ...]) -> bool:
    return bool(surfaces) and all(
        body not in surface
        for surface in surfaces.values()
        for body in bodies
        if body
    )


def sequence_provenance_matches(events: list[dict[str, Any]], documents: list[dict[str, Any]]) -> bool:
    event_sequences = {str(event.get("id")): event.get("sequence") for event in events if event.get("id")}
    if not event_sequences or len(documents) != len(event_sequences):
        return False
    for document in documents:
        provenance = document.get("openhands_provenance") or {}
        event_id = str(provenance.get("event_id"))
        if event_id not in event_sequences or event_sequences[event_id] is None:
            return False
        if provenance.get("sequence") != event_sequences[event_id]:
            return False
    return True


def summary_not_delivery_state(*, marker: str, context: str, captured_event_ids: set[str],
                               summary_event_ids: set[str], retrieval_bytes: bytes,
                               selected_memory_ids: set[str], allowed_memory_ids: set[str]) -> bool:
    encoded = marker.encode()
    return bool(marker and summary_event_ids and selected_memory_ids) \
        and marker not in context \
        and encoded not in retrieval_bytes \
        and not (captured_event_ids & summary_event_ids) \
        and selected_memory_ids.issubset(allowed_memory_ids)


def cancellation_oracles(*, interrupt_ns: int, future_observed_ns: int,
                         stub_completed_ns: int, shutdown_ns: int,
                         no_owned_work: bool, deletion_complete: bool,
                         repetition_workspace_absent: bool,
                         repetition_namespace_absent: bool) -> dict[str, bool]:
    future_bound = future_observed_ns > 0 and 0 <= future_observed_ns - interrupt_ns <= 10_000_000_000
    disconnect_bound = stub_completed_ns > 0 and 0 <= stub_completed_ns - interrupt_ns <= 10_000_000_000
    return {
        "futureBound": future_bound,
        "disconnectBound": disconnect_bound,
        "ownedShutdownBound": 0 <= shutdown_ns <= 15_000_000_000 and no_owned_work,
        "repetitionCleanup": no_owned_work and deletion_complete
                             and repetition_workspace_absent and repetition_namespace_absent,
    }


class Candidate5Runtime(c4.Candidate4Runtime):
    def one_prompt(self, item: dict[str, Any], workspace: Path, **kwargs: Any) -> dict[str, Any]:
        record = super().one_prompt(item, workspace, **kwargs)
        record["contextProvenanceClass"] = classify_context_provenance(
            str(record.get("_contextEphemeral", "")), record.get("smaTraceId")
        )
        return record

    def case_retrieval_outage(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        operational_before = self.operational.stat().st_size if self.operational.exists() else 0
        service_paths = (self.p.RUNTIME / "sma.out.log", self.p.RUNTIME / "sma.err.log")
        service_before = {path: path.stat().st_size if path.exists() else 0 for path in service_paths}
        retrieval_before = {
            str(doc.get("_id")) for doc in
            self.p.mongosh("EJSON.stringify(db.retrieval_events.find({}).toArray())")
        }
        self.p.stop_service()
        records = [self.one_prompt(item, self.alpha)]
        record = records[0]
        prompt = base.control_prompt(item).encode()
        corpus = json.loads(CORPUS.read_text())
        memory_bodies = tuple(
            value.encode()
            for memory in corpus["syntheticMemoryCorpus"]
            for value in (memory["literalText"], memory["marker"])
        )
        logs = self.operational.read_bytes()[operational_before:] if self.operational.exists() else b""
        service_logs = b""
        for path in service_paths:
            if path.exists():
                service_logs += path.read_bytes()[service_before[path]:]
        retrieval_docs = self.p.mongosh("EJSON.stringify(db.retrieval_events.find({}).toArray())")
        retrieval = canonical_bytes([doc for doc in retrieval_docs if str(doc.get("_id")) not in retrieval_before])
        hook_stdout = ""
        for event in self.events(record["conversationId"]):
            if event.get("id") == record.get("hookEventId"):
                hook_stdout = str(event.get("stdout") or "")
                break
        surfaces = {"stubOperationalDelta": logs, "serviceLogDelta": service_logs,
                    "newRetrievalEvents": retrieval, "hookStdout": hook_stdout.encode()}
        body_free = bodies_absent_from_surfaces(surfaces, (prompt, *memory_bodies))
        outage = {
            "serviceAbsent": not self.support.launch_identity(LABEL)["loaded"],
            "contextEmpty": record["contextLength"] == 0,
            "bodyFreeMeasured": body_free,
        }
        return self._grade(item, records, {
            "S2-007-P1": record["hookSuccess"] is True and record["hookResultCount"] == 1,
            "S2-007-P2": record["rawStubRequestCount"] == record["stubTerminalCount"] == 1,
            "S2-007-P3": all(outage.values()),
        }, detail={**outage, "surfaceLengths": {key: len(value) for key, value in surfaces.items()}})

    def case_parent_child_provenance(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded()
        before = self.p.mongo_counts()["memories"]
        parent, tool = self._tool_prompt(item, self.alpha, "DELEGATION_TOOL")
        action = tool["action"] or {}
        task = str((action.get("action") or {}).get("task", ""))
        child_item = dict(item)
        child_item["stubMode"] = "SUCCESS"
        require(task == base.control_prompt(child_item), "delegation tool did not preserve exact child task")
        child = self.one_prompt(child_item, self.alpha, parent=parent,
                                expected=(self.mapped["mem-alpha-timeout"],))
        self.p.start_service(True, True)
        self.p.wait_memory_count(before + 3)
        parent_docs = self._conversation_memories(parent)
        child_docs = self._conversation_memories(child["conversationId"])
        child_events = [
            event for event in self.events(child["conversationId"])
            if event.get("kind") == "MessageEvent"
            and event.get("id") in {(doc.get("openhands_provenance") or {}).get("event_id") for doc in child_docs}
        ]
        parent_status, parent_detail = self.p.http(
            "GET", self.support.INGRESS, f"/api/conversations/{parent}", self.support.session_key(), timeout=15
        )
        child_status, child_detail = self.p.http(
            "GET", self.support.INGRESS, f"/api/conversations/{child['conversationId']}",
            self.support.session_key(), timeout=15
        )
        provenances = [doc.get("openhands_provenance") or {} for doc in child_docs]
        complete = parent_status == child_status == 200 and len(parent_docs) == 1 and len(child_docs) == 2
        complete = complete and child["conversationId"] in [str(value) for value in parent_detail.get("sub_conversation_ids", [])]
        complete = complete and str(child_detail.get("parent_conversation_id")) == parent
        complete = complete and all(
            provenance.get("conversation_id") == child["conversationId"]
            and provenance.get("parent_conversation_id") == parent
            and provenance.get("workspace") == str(self.alpha)
            and provenance.get("profile") is not None
            for provenance in provenances
        )
        complete = complete and sequence_provenance_matches(child_events, child_docs)
        isolated = all(doc.get("agent_id") == self.p.ALPHA_PARTITION for doc in parent_docs + child_docs)
        return self._grade(item, [child], {"S2-011-P1": complete, "S2-011-P2": isolated},
                           evidence=self._common([child]), detail={
                               "parentId": parent,
                               "childId": child["conversationId"],
                               "parentCaptureCount": len(parent_docs),
                               "childCaptureCount": len(child_docs),
                               "openhandsSequences": {event.get("id"): event.get("sequence") for event in child_events},
                               "smaSequences": {(doc.get("openhands_provenance") or {}).get("event_id"):
                                                (doc.get("openhands_provenance") or {}).get("sequence")
                                                for doc in child_docs},
                           })

    def case_condensation_reanchor(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        self.ensure_seeded()
        first = self.one_prompt(item, self.alpha, expected=(self.mapped["mem-alpha-timeout"],))
        status, _ = self.p.http("POST", self.support.INGRESS,
                                f"/api/conversations/{first['conversationId']}/condense",
                                self.support.session_key(), timeout=120)
        second_item = dict(item)
        second_item["literalPrompt"] += " Re-anchor after condensation."
        original = self.create_conversation
        self.create_conversation = lambda *_args, **_kwargs: first["conversationId"]
        try:
            second = self.one_prompt(second_item, self.alpha,
                                     expected=(self.mapped["mem-alpha-timeout"],))
        finally:
            self.create_conversation = original
        summaries = [event for event in self.events(first["conversationId"])
                     if event.get("kind") == "CondensationSummaryEvent"]
        summary_payload = canonical_bytes(summaries)
        summary_event_ids = {str(event.get("id")) for event in summaries if event.get("id")}
        captured_event_ids = {
            str((doc.get("openhands_provenance") or {}).get("event_id"))
            for doc in self._full_memories()
            if (doc.get("openhands_provenance") or {}).get("event_id")
        }
        retrieval = canonical_bytes(self.p.mongosh("EJSON.stringify(db.retrieval_events.find({}).toArray())"))
        selected = set(second["selectedMemoryIds"])
        allowed = {str(value) for value in self.mapped.values()}
        summary_clean = CONDENSATION_MARKER.encode() in summary_payload and summary_not_delivery_state(
            marker=CONDENSATION_MARKER,
            context=str(second.get("_contextEphemeral", "")),
            captured_event_ids=captured_event_ids,
            summary_event_ids=summary_event_ids,
            retrieval_bytes=retrieval,
            selected_memory_ids=selected,
            allowed_memory_ids=allowed,
        )
        return self._grade(item, [first, second], {
            "S2-018-P1": second["contextFramingValid"] and second["contextLength"] <= 4096,
            "S2-018-P2": summary_clean,
            "S2-018-P3": status in (200, 204) and second["stubOutcomes"] == ["CLIENT_RECEIVED"],
        }, detail={
            "condenseStatus": status,
            "summaryEventIds": sorted(summary_event_ids),
            "summaryMarkerObservedInSummary": CONDENSATION_MARKER.encode() in summary_payload,
            "selectedMemoryIds": sorted(selected),
        })

    def case_active_cancellation_and_shutdown(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        owned_marker = self.alpha / f".sma-s2-c20-{item['repetition']}.owned"
        owned_marker.write_text("candidate-5 repetition-owned workspace marker\n")
        conversation = self.create_conversation(self.alpha, int(self.stub_port))
        prompt = base.control_prompt(item)
        submitted = time.monotonic_ns()
        require(self.submit(conversation, prompt) == 200, "cancellation submission failed")
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline and not self.raw_for(item["caseId"], item["repetition"], prompt):
            time.sleep(0.01)
        raws = self.raw_for(item["caseId"], item["repetition"], prompt)
        require(len(raws) == 1, "cancellation raw request was not durably recorded")
        raw_ns = int(raws[0]["receivedMonotonicNs"])
        time.sleep(0.25)
        interrupt_ns = time.monotonic_ns()
        interrupt_status, _ = self.p.http(
            "POST", self.support.INGRESS, f"/api/conversations/{conversation}/interrupt",
            self.support.session_key(), timeout=10
        )
        terminal_deadline = time.monotonic() + 10
        terminal_detail: dict[str, Any] = {}
        future_observed_ns = 0
        while time.monotonic() < terminal_deadline:
            _, terminal_detail = self.p.http("GET", self.support.INGRESS,
                                             f"/api/conversations/{conversation}",
                                             self.support.session_key(), timeout=15)
            if future_observed_ns == 0 and str(terminal_detail.get("execution_status")) in base.TERMINAL_STATES:
                future_observed_ns = time.monotonic_ns()
            terminals = self.terminal_for(item["caseId"], item["repetition"])
            if future_observed_ns and terminals:
                break
            time.sleep(0.02)
        terminals = self.terminal_for(item["caseId"], item["repetition"])
        stub_completed_ns = int(terminals[0]["completedMonotonicNs"]) if terminals else 0
        events = self.events(conversation)
        user = next((event for event in events if event.get("kind") == "MessageEvent"
                     and event.get("source") == "user" and self.p.event_text(event) == prompt), {})
        hook = next((event for event in events if event.get("kind") == "HookExecutionEvent"
                     and (event.get("hook_input") or {}).get("message") == prompt), {})
        context = str(hook.get("additional_context") or "")
        request = self.request_oracle(raws[0], prompt, context)
        deletion = self.support.delete_conversation(self.support.session_key(), conversation)
        if conversation in self.conversations:
            self.conversations.remove(conversation)
        owned_marker.unlink(missing_ok=True)
        health = self.support.bridge_health()
        shutdown_deadline = interrupt_ns + 15_000_000_000
        while time.monotonic_ns() < shutdown_deadline and (
                health.get("context_active_work") != 0 or health.get("context_queued_work") != 0):
            time.sleep(0.02)
            health = self.support.bridge_health()
        shutdown_ns = time.monotonic_ns() - interrupt_ns
        no_work = health.get("context_active_work") == 0 and health.get("context_queued_work") == 0
        repetition_namespace_absent = not self._conversation_memories(conversation)
        bounds = cancellation_oracles(
            interrupt_ns=interrupt_ns,
            future_observed_ns=future_observed_ns,
            stub_completed_ns=stub_completed_ns,
            shutdown_ns=shutdown_ns,
            no_owned_work=no_work,
            deletion_complete=bool(deletion),
            repetition_workspace_absent=not owned_marker.exists(),
            repetition_namespace_absent=repetition_namespace_absent,
        )
        record = {
            "caseId": item["caseId"], "repetition": item["repetition"], "conversationId": conversation,
            "promptSha256": sha_bytes(prompt.encode()),
            "persistedPromptSha256": sha_bytes(self.p.event_text(user).encode()),
            "hookInputSha256": sha_bytes(str((hook.get("hook_input") or {}).get("message", "")).encode()),
            "contextSha256": sha_bytes(context.encode()),
            "persistedContextSha256": sha_bytes(self.content_text(user.get("extended_content")).encode()),
            "contextLength": len(context),
            "persistedContextLength": len(self.content_text(user.get("extended_content"))),
            "contextFramingValid": valid_context_framing(context),
            "selectedMemoryIds": context_ids(context),
            "contextProvenanceClass": classify_context_provenance(
                context, trace_id_from_stdout(hook.get("stdout")),
            ),
            "requestOracle": request, "rawStubRequestCount": len(raws), "stubTerminalCount": len(terminals),
            "elapsedNs": time.monotonic_ns() - submitted, "rawReceivedMonotonicNs": [raw_ns],
            "conversationDetailStatus": 200, "userEventId": user.get("id"), "hookEventId": hook.get("id"),
            "workspaceIdentity": str(self.alpha), "profileIdentity": "default",
            "workspaceSha256": sha_bytes(str(self.alpha).encode()), "profileSha256": sha_bytes(b"default"),
            "eventOrdinals": {event.get("id"): index for index, event in enumerate(events)},
        }
        common = self._common([record])
        common["E-006"] = bounds["futureBound"] and bounds["disconnectBound"]
        return self._grade(item, [record], {
            "S2-020-P1": interrupt_ns >= raw_ns + 250_000_000,
            "S2-020-P2": bounds["futureBound"],
            "S2-020-P3": bool(terminals) and terminals[0].get("responseWriteOutcome") == "CLIENT_DISCONNECTED"
                          and bounds["disconnectBound"],
            "S2-020-P4": bounds["ownedShutdownBound"],
            "S2-020-P5": bounds["repetitionCleanup"],
            "S2-020-P6": common["E-002"] and common["E-003"],
        }, evidence=common, detail={
            "interruptStatus": interrupt_status,
            "futureObservedMonotonicNs": future_observed_ns,
            "stubCompletedMonotonicNs": stub_completed_ns,
            "shutdownNs": shutdown_ns,
            "bridgeActive": health.get("context_active_work"),
            "bridgeQueued": health.get("context_queued_work"),
            "repetitionWorkspaceAbsent": not owned_marker.exists(),
            "repetitionNamespaceAbsent": repetition_namespace_absent,
            "globalNamespacesDeferredToE009E010": True,
        })


c4.Candidate4Runtime = Candidate5Runtime
c4.base.LiveRuntime = Candidate5Runtime


def validate_static_contract() -> dict[str, Any]:
    value = c4.validate_static_contract()
    value["candidate5SemanticHelpers"] = {
        "nonEmptyContextWithoutTraceRejected": classify_context_provenance("memory", None) == "MISSING",
        "emptyFailOpenClassified": classify_context_provenance("", None) == "EXPLICIT_FAIL_OPEN_EMPTY",
        "traceClassified": classify_context_provenance("memory", "trace") == "TRACE",
    }
    require(all(value["candidate5SemanticHelpers"].values()), "candidate-5 semantic helper self-test failed")
    return value


validate_execution_fence = c4.validate_execution_fence
execute = c4.execute
parser = c4.parser


def main() -> int:
    args = parser().parse_args()
    if args.offline_contract:
        print(json.dumps(validate_static_contract(), indent=2, sort_keys=True))
        return 0
    if not args.execute:
        raise PermissionError("default deny: use --offline-contract or separately authorized --execute")
    if args.execution_identity is None or args.authorization is None or args.output is None:
        raise PermissionError("measured execution requires identity, authorization, and output")
    return execute(args)


if __name__ == "__main__":
    raise SystemExit(main())
