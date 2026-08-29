from __future__ import annotations

import json
from typing import Any, Callable, Iterable, Mapping

from .model import OperationObservation, OracleResult, digest, nested_get
from .truth import ProductTruth


Predicate = Callable[[OperationObservation, ProductTruth], tuple[bool, str]]


def _all_text(observation: OperationObservation, surfaces: Iterable[str]) -> str:
    values: list[Any] = []
    for surface in surfaces:
        values.extend(getattr(observation, surface))
    return json.dumps(values, sort_keys=True, ensure_ascii=False)


def _prompt(observation: OperationObservation) -> str | None:
    user = [event for event in observation.raw_events if event.get("kind") == "MessageEvent" and event.get("source") == "user"]
    if not user:
        return None
    return nested_get(user[0], "llm_message.content.0.text") if False else user[0].get("llm_message", {}).get("content", [{}])[0].get("text")


def _contexts(observation: OperationObservation) -> list[str]:
    return [str(hook.get("additional_context") or "") for hook in observation.hooks]


def _request_prompts(observation: OperationObservation) -> list[str]:
    return [str(row.get("prompt")) for row in observation.model_requests]


def _request_contexts(observation: OperationObservation) -> list[str]:
    values: list[str] = []
    for row in observation.model_requests:
        messages = row.get("messages", [])
        values.append(str(messages[1].get("content", "")) if len(messages) > 1 else "")
    return values


def _valid_join_count(observation: OperationObservation, truth: ProductTruth) -> int:
    total = 0
    by_trace = {row.get("task_ref"): row for row in observation.retrievals}
    by_memory = {row.get("_id"): row for row in observation.memories}
    semantic = {row.get("payload", {}).get("memory_id"): row for row in observation.semantic_points}
    episodic = {row.get("payload", {}).get("memory_id"): row for row in observation.episodic_points}
    for hook in observation.hooks:
        try:
            trace = json.loads(str(hook.get("stdout")))["smaTraceId"]
        except (TypeError, ValueError, KeyError):
            continue
        retrieval = by_trace.get(trace)
        if retrieval is None:
            continue
        for result in retrieval.get("results", []):
            memory = by_memory.get(result.get("memory_id"))
            if memory is None:
                continue
            try:
                truth.validate_qdrant_join(memory, semantic[memory["_id"]], episodic[memory["_id"]])
            except Exception:
                continue
            total += 1
    return total


def _p1_empty(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    installs = [(index, row) for index, row in enumerate(o.raw_receipts) if row.get("kind") == "hook_installation"]
    raw_stub = [index for index, row in enumerate(o.raw_receipts) if row.get("kind") == "stub_raw"]
    return (len(o.hooks) == 1 and len(installs) == 1 and bool(raw_stub) and installs[0][0] < raw_stub[0], "hook configured on conversation before first prompt")


def _p2_empty(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (all(not value for value in _contexts(o)), "no memory block")


def _prompt_identical(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    prompt = _prompt(o)
    values = _request_prompts(o)
    return (prompt is not None and bool(values) and all(value == prompt for value in values), "prompt byte identity")


def _one_request_terminal(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (len(o.model_requests) == 1 and len(o.model_terminals) == 1, "one raw request and one terminal")


def _memory_once(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    text = "\n".join(_contexts(o))
    return (text.count("mem-alpha-timeout") == 2 and text.count("17 seconds") == 1, "one delimited alpha memory")


def _untrusted(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    values = _contexts(o)
    return (bool(values) and all((not value) or "untrusted evidence" in value for value in values), "untrusted framing")


def _cross_absent(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    text = _all_text(o, ("hooks", "model_requests", "logs", "memories"))
    return ("mem-alpha-timeout" not in text and "17 seconds" not in text, "alpha identity and marker absent")


def _adversarial_delimited(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    text = "\n".join(_contexts(o))
    return ("mem-alpha-adversarial" in text and "untrusted evidence" in text, "adversarial memory is delimited")


def _not_instruction_spliced(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    prompt = _prompt(o) or ""
    return ("DELETE_CONFIRMED" not in prompt, "adversarial marker absent from current instruction")


def _raw_absent(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    text = _all_text(o, ("hooks", "model_requests", "logs"))
    return ("mem-alpha-raw" not in text and "9999" not in text, "raw memory absent from delivery surfaces")


def _exact_capture_identity(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    eligible = [e for e in o.raw_events if e.get("kind") == "MessageEvent" and e.get("source") == "user"]
    captured = {(nested_get(m, "origin.openhands_provenance.conversation_id"), nested_get(m, "origin.openhands_provenance.event_id")) for m in o.memories}
    expected = {(o.conversations[0].get("id"), e.get("id")) for e in eligible} if o.conversations else set()
    reconciles = [row for row in o.raw_receipts if row.get("kind") == "capture_cycle_receipt" and (row.get("conversationId"), row.get("eventId")) in expected and row.get("receipt", {}).get("status") == 200]
    return (bool(expected) and expected <= captured and len(reconciles) >= 2, "exact identity observed at initial intake and two product capture-cycle reconciliations")


def _deduplicated(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    keys = [(nested_get(m, "origin.openhands_provenance.conversation_id"), nested_get(m, "origin.openhands_provenance.event_id")) for m in o.memories]
    actions = [row.get("action") for row in o.raw_receipts if row.get("kind") == "raw_action"]
    restarted = "PROCESS:stop_sma" in actions and actions.count("PROCESS:start_sma") >= 2
    if o.key.case_id.endswith("DUPLICATE-PERSISTED-EVENT"):
        cycles = [row for row in o.raw_receipts if row.get("kind") == "capture_cycle_receipt" and row.get("receipt", {}).get("status") == 200]
        return (len(keys) == len(set(keys)) and restarted and len(cycles) >= 2, "one memory after lifecycle restart and two receipted product capture cycles")
    return (len(keys) == len(set(keys)), "capture keys unique after reconciliation")


def _no_context_deadline(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    hook_times = [t for t in o.timings if t.get("kind") == "hook"]
    return (len(o.hooks) == 1 and all(not c for c in _contexts(o)) and bool(hook_times) and all(t["durationMs"] <= t["deadlineMs"] for t in hook_times), "one no-context hook within deadline")


def _outage_observable_body_free(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    faults = [row for row in o.logs if row.get("kind") == "bridge_fault"]
    return (len(faults) == 1 and faults[0].get("promptBodyPresent") is False and faults[0].get("memoryBodyPresent") is False, "body-free outage evidence")


def _persisted_reconcilable(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    user_ids = {e.get("id") for e in o.raw_events if e.get("source") == "user"}
    captures = {nested_get(m, "origin.openhands_provenance.event_id") for m in o.memories}
    recovery = [row for row in o.timings if row.get("kind") == "capture_recovery"]
    return (bool(user_ids) and user_ids <= captures and len(recovery) == 1 and recovery[0].get("durationMs", 10**9) <= recovery[0].get("deadlineMs", 0), "persisted event reconciled within bounded stabilization")


def _one_capture_no_loop(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    ids = [nested_get(m, "origin.openhands_provenance.event_id") for m in o.memories]
    return (len(ids) == len(set(ids)) and not any(e.get("kind") == "HookExecutionEvent" and e.get("id") in ids for e in o.raw_events), "exact capture without recursive hook capture")


def _one_context_memory(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (any("mem-alpha-timeout" in value and len(value) <= 4096 for value in _contexts(o)), "one bounded alpha context")


def _provenance_complete(o: OperationObservation, truth: ProductTruth) -> tuple[bool, str]:
    return (_valid_join_count(o, truth) >= 1, "complete hook-retrieval-memory-Qdrant join")


def _fault_hook_success(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    hook_times = [t for t in o.timings if t.get("kind") == "hook"]
    return (len(o.hooks) == 1 and o.hooks[0].get("success") is True and o.hooks[0].get("exit_code") == 0 and bool(hook_times) and all(t["durationMs"] <= t["deadlineMs"] for t in hook_times), "fault exits hook successfully within hard timeout")


def _one_hook_result(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (sum(e.get("kind") == "HookExecutionEvent" for e in o.raw_events) == 1, "exactly one HookExecutionEvent")


def _lineage(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    if len(o.conversations) != 2:
        return False, "parent and child conversations required"
    parent = next((c for c in o.conversations if c.get("parent_conversation_id") is None), None)
    child = next((c for c in o.conversations if c.get("parent_conversation_id") is not None), None)
    if not parent or not child or child.get("parent_conversation_id") != parent.get("id") or child.get("id") not in parent.get("sub_conversation_ids", []):
        return False, "parent-child linkage mismatch"
    memories = [m for m in o.memories if nested_get(m, "origin.openhands_provenance.conversation_id") == child.get("id")]
    sequences = [nested_get(m, "event.sequence_position.$numberInt") for m in memories]
    return (bool(memories) and all(value is not None for value in sequences) and all(nested_get(m, "origin.openhands_provenance.workspace") == child.get("workspace") and nested_get(m, "origin.openhands_provenance.profile") == child.get("profile") for m in memories), "actual lineage, workspace, profile, and sequence retained")


def _partition_attribution(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    owned = {c.get("workspace") for c in o.conversations}
    return (bool(o.memories) and all(nested_get(m, "origin.openhands_provenance.workspace") in owned for m in o.memories), "no unrelated partition attribution")


def _same_partition_after_restart(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    values = [t.get("agentId") for t in o.timings if t.get("kind") in {"partition_before", "partition_after"}]
    return (len(values) == 2 and len(set(values)) == 1, "authenticated partition stable across restart")


def _recall_correct(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (any("mem-alpha-timeout" in context for context in _contexts(o)), "recall remains correct")


def _four_terminals(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (len(o.model_requests) == 4 and len(o.model_terminals) == 4 and all(row.get("outcome") in {"CLIENT_RECEIVED", "SUCCESS"} for row in o.model_terminals), "four deterministic terminals")


def _four_partition_isolation(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    for request in o.model_requests:
        context = str(request.get("messages", [{}, {}])[1].get("content", ""))
        workspace = request.get("workspaceRole")
        if workspace == "alpha" and "mem-beta-port" in context:
            return False, "beta memory leaked to alpha"
        if workspace == "beta" and "mem-alpha-timeout" in context:
            return False, "alpha memory leaked to beta"
    return len(o.model_requests) == 4, "four channel partitions isolated"


def _telemetry_complete(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    telemetry = [row for row in o.timings if row.get("kind") == "concurrency"]
    required = {"barrierReleasedNs", "peakInFlight", "queueDepth", "timeoutCount", "rejectionCount", "processCount"}
    return (len(telemetry) == 1 and required <= set(telemetry[0]) and telemetry[0].get("peakInFlight", 0) >= 2, "overlap and queue/timeout/rejection/resource telemetry")


def _ineligible_absent(o: OperationObservation, truth: ProductTruth) -> tuple[bool, str]:
    forbidden = {event.get("id") for event in o.raw_events if not truth.eligible(event)}
    captured = {nested_get(m, "origin.openhands_provenance.event_id") for m in o.memories}
    return (not (forbidden & captured), "all ineligible event identities absent")


def _no_delivery_loop(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    traces = []
    for hook in o.hooks:
        try:
            traces.append(json.loads(str(hook.get("stdout"))).get("smaTraceId"))
        except Exception:
            return False, "unparseable hook"
    return (len(traces) == len(set(traces)) and len(o.hooks) == 1, "no second delivery/capture loop")


def _secret_only_allowed(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    secret = "sk-test-SMA-S2-NEVER-PERSIST"
    prompt = _prompt(o)
    request = o.model_requests[0] if o.model_requests else {}
    segment0 = request.get("messages", [{}])[0].get("content") if request else None
    return (secret in str(prompt) and segment0 == prompt and _all_text(o, ("model_terminals",)).count(secret) == 0, "secret only in prompt, segment 0, sealed raw")


def _two_request_terminals(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (len(o.model_requests) == 2 and len(o.model_terminals) == 2, "pre/post-condensation requests reach terminals")


def _secret_absent_derived(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    secret = "sk-test-SMA-S2-NEVER-PERSIST"
    text = _all_text(o, ("logs", "retrievals", "semantic_points", "episodic_points")) + "\n" + "\n".join(_request_contexts(o))
    return (secret not in text, "secret absent from operational and derived surfaces")


def _sealed_policy(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    sealed = [r for r in o.raw_receipts if r.get("kind") == "stub_raw"]
    return (len(sealed) == 1 and sealed[0].get("sensitivity") == "SEALED_RAW" and sealed[0].get("bodyDisclosed") is False, "sealed raw sensitivity policy")


def _no_irrelevant(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (all(not value for value in _contexts(o)), "no irrelevant block")


def _bounded_context(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    values = _contexts(o)
    return (len(values) == 1 and len(values[0]) <= 4096 and values[0].count("--- MEMORY ") <= 3 and values[0].count("--- MEMORY ") == values[0].count("--- END MEMORY "), "valid memory-boundary truncation")


def _within_science_ceiling(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (all(len(v) <= 10_000 and v.count("--- MEMORY ") <= 5 for v in _contexts(o)), "product bounds within accepted ceilings")


def _reanchored(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (len(o.hooks) == 2 and bool(_contexts(o)[-1]) and len(_contexts(o)[-1]) <= 4096, "post-condensation context re-anchored")


def _summary_not_delivery(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    summaries = [event for event in o.raw_events if event.get("kind") == "CondensationSummaryEvent"]
    selected = {row.get("memory_id") for retrieval in o.retrievals for row in retrieval.get("results", [])}
    summary_ids = {event.get("id") for event in summaries}
    memory_by_event = {nested_get(m, "origin.openhands_provenance.event_id"): m.get("_id") for m in o.memories}
    return (len(summaries) == 1 and not {memory_by_event.get(event_id) for event_id in summary_ids} & selected, "exact summary event absent from delivery provenance")


def _scheduled_fault_once(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    activated = [t for t in o.timings if t.get("kind") == "fault_activation"]
    if len(activated) != 1 or activated[0].get("count") != 1:
        return False, "fault activation receipt missing"
    scheduled = activated[0].get("mode")
    raw_modes = {row.get("mode") for row in o.model_requests}
    if scheduled == "TRANSPORT_FAILURE_UNUSED_PORT":
        return (not o.model_requests, "transport fault activated before any stub request")
    return (raw_modes == {scheduled}, "scheduled fault matches exact stub raw mode")


def _zero_retries(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (len(o.model_requests) <= 1 and all(row.get("retry", 0) == 0 for row in o.model_requests), "zero model retries")


def _bounded_error(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    terminal = [t for t in o.timings if t.get("kind") == "conversation_terminal"]
    return (len(terminal) == 1 and terminal[0].get("durationMs", 10**9) <= terminal[0].get("deadlineMs", 0), "bounded terminal/error state")


def _fault_does_not_alter(o: OperationObservation, truth: ProductTruth) -> tuple[bool, str]:
    prompt_ok = _prompt_identical(o, truth)[0] if o.model_requests else True
    return (prompt_ok and _deduplicated(o, truth)[0], "fault preserves prompt/context/provenance")


def _fault_cleanup(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    return (bool(o.cleanup_receipts) and all(row.get("complete") is True for row in o.cleanup_receipts), "fault timing and cleanup complete")


def _cancel_after_raw(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    raw = [t for t in o.timings if t.get("kind") == "stub_raw"]
    cancel = [t for t in o.timings if t.get("kind") == "cancel"]
    return (len(raw) == len(cancel) == 1 and cancel[0]["atNs"] > raw[0]["atNs"], "cancel follows durable raw request")


def _within(o: OperationObservation, kind: str, limit: int) -> tuple[bool, str]:
    values = [t for t in o.timings if t.get("kind") == kind]
    return (len(values) == 1 and values[0].get("durationMs", limit + 1) <= limit, f"{kind} within {limit}ms")


def _no_orphans(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    clean = all(not row.get("ownedActive", True) for row in o.process_inventory + o.workspace_inventory + o.namespace_inventory)
    return (clean and bool(o.process_inventory) and bool(o.workspace_inventory) and bool(o.namespace_inventory), "no per-repetition owned orphan")


def _reconstructible_body_free(o: OperationObservation, _: ProductTruth) -> tuple[bool, str]:
    receipts = [r for r in o.raw_receipts if r.get("kind") in {"stub_raw", "context"}]
    return (bool(receipts) and all(r.get("digest") and r.get("bodyDisclosed") is False for r in receipts), "prompt/context reconstructible by sealed digests")


CASE_PREDICATES: dict[str, tuple[Predicate, ...]] = {
    "SMA-S2-001-FIRST-PROMPT-EMPTY": (_p1_empty, _p2_empty, _prompt_identical, _one_request_terminal),
    "SMA-S2-002-SAME-PARTITION-DELIVERY": (_memory_once, _untrusted, _prompt_identical),
    "SMA-S2-003-CROSS-PARTITION-DELIVERY-DENIAL": (_cross_absent,),
    "SMA-S2-004-UNTRUSTED-CONTEXT-PLACEMENT": (_adversarial_delimited, _not_instruction_spliced, _prompt_identical),
    "SMA-S2-005-RAW-INELIGIBLE-ABSENCE": (_raw_absent,),
    "SMA-S2-006-DUPLICATE-PERSISTED-EVENT": (_exact_capture_identity, _deduplicated),
    "SMA-S2-007-RETRIEVAL-OUTAGE": (_no_context_deadline, _one_request_terminal, _outage_observable_body_free),
    "SMA-S2-008-CAPTURE-OUTAGE": (_one_request_terminal, _persisted_reconcilable, _one_capture_no_loop),
    "SMA-S2-009-ADDITIONAL-CONTEXT-INTEGRITY": (_prompt_identical, _one_context_memory, _provenance_complete),
    "SMA-S2-010-HOOK-FAULT-MATRIX": (_fault_hook_success, _p2_empty, _one_hook_result, _prompt_identical),
    "SMA-S2-011-PARENT-CHILD-PROVENANCE": (_lineage, _partition_attribution),
    "SMA-S2-012-RESTART-CONTINUITY": (_same_partition_after_restart, _recall_correct, _deduplicated),
    "SMA-S2-013-FOUR-CHANNEL-CONCURRENCY": (_four_terminals, _four_partition_isolation, _telemetry_complete),
    "SMA-S2-014-FEEDBACK-LOOP-PREVENTION": (_ineligible_absent, _no_delivery_loop),
    "SMA-S2-015-SECRET-DELIVERY-ABSENCE": (_secret_only_allowed, _secret_absent_derived, _sealed_policy),
    "SMA-S2-016-EMPTY-RESULT": (_no_irrelevant, _one_request_terminal),
    "SMA-S2-017-OVERSIZED-CONTEXT": (_bounded_context, _bounded_context, _within_science_ceiling),
    "SMA-S2-018-CONDENSATION-REANCHOR": (_reanchored, _summary_not_delivery, _two_request_terminals),
    "SMA-S2-019-MODEL-STUB-FAULT-MATRIX": (_scheduled_fault_once, _zero_retries, _bounded_error, _fault_does_not_alter, _fault_cleanup),
    "SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN": (_cancel_after_raw, lambda o,t:_within(o,"conversation_terminal",10_000), lambda o,t:_within(o,"client_disconnect",10_000), lambda o,t:_within(o,"owned_shutdown",15_000), _no_orphans, _reconstructible_body_free),
}


def scientific_oracles(observation: OperationObservation, truth: ProductTruth) -> list[OracleResult]:
    ids = truth.predicate_ids(observation.key.case_id)
    predicates = CASE_PREDICATES[observation.key.case_id]
    if len(ids) != len(predicates):
        raise ValueError(f"oracle registry cardinality mismatch for {observation.key.case_id}")
    evidence = tuple(digest(value) for value in observation.raw_receipts)
    results: list[OracleResult] = []
    for oracle_id, predicate in zip(ids, predicates, strict=True):
        try:
            passed, detail = predicate(observation, truth)
        except Exception as exc:
            passed, detail = False, f"oracle exception {type(exc).__name__}: {exc}"
        results.append(OracleResult(oracle_id, bool(passed), evidence, detail))
    return results


def evidence_oracles(observation: OperationObservation, truth: ProductTruth) -> list[OracleResult]:
    results: list[OracleResult] = []
    receipts = observation.raw_receipts
    digests = tuple(digest(value) for value in receipts)
    prompt = _prompt(observation)
    boundary_values = [prompt, *_request_prompts(observation), *_request_contexts(observation), *_contexts(observation)]
    e001 = bool(observation.key.case_id and observation.key.repetition > 0 and observation.timings)
    boundary = next((row for row in receipts if row.get("kind") == "boundary_bytes"), None)
    e002 = (
        prompt is not None and all(isinstance(value, str) for value in boundary_values)
        and isinstance(boundary, dict)
        and boundary.get("promptSha256") == digest(prompt)
        and boundary.get("promptLength") == len(prompt.encode())
        and boundary.get("context") == [{"sha256": digest(value), "length": len(value.encode())} for value in _contexts(observation)]
    )
    eligible = [event for event in observation.raw_events if truth.eligible(event)]
    captured = {nested_get(m, "origin.openhands_provenance.event_id") for m in observation.memories}
    scope = next((row for row in receipts if row.get("kind") == "intake_scope"), {})
    expected_eligible = set(scope.get("eligibleEventIds", []))
    e003 = bool(scope) and all(event_id in captured for event_id in expected_eligible) and all(nested_get(m, "event.sequence_position") is not None for m in observation.memories)
    transport_zero = observation.key.case_id.endswith("MODEL-STUB-FAULT-MATRIX") and observation.key.repetition == 4
    e004 = (transport_zero and not observation.model_requests) or (bool(observation.model_requests) and all(row.get("recordType") == "SMA_S2_STUB_RAW_REQUEST" and row.get("requestId") and row.get("receivedNs") for row in observation.model_requests))
    e005 = (transport_zero and any(c.get("execution_status") == "error" for c in observation.conversations)) or (bool(observation.model_terminals) and all(row.get("recordType") == "SMA_S2_STUB_TERMINAL" and row.get("requestId") and row.get("outcome") for row in observation.model_terminals))
    e006_required = observation.key.case_id.endswith("MODEL-STUB-FAULT-MATRIX") or observation.key.case_id.endswith("ACTIVE-CANCELLATION-AND-SHUTDOWN")
    e006 = (not e006_required) or any(row.get("kind") in {"fault_activation", "cancel"} and row.get("atNs") is not None for row in observation.timings)
    request_ids = {row.get("requestId") for row in observation.model_requests}
    terminal_ids = {row.get("requestId") for row in observation.model_terminals}
    event_ids = [event.get("id") for event in observation.raw_events]
    e007 = (
        bool(observation.conversations)
        and all(c.get("id") and c.get("workspace") and c.get("profile") and c.get("execution_status") for c in observation.conversations)
        and all(event_ids) and len(event_ids) == len(set(event_ids))
        and (terminal_ids == request_ids)
        and (bool(terminal_ids) or transport_zero)
    )
    e008 = (
        bool(observation.process_inventory) and bool(observation.workspace_inventory) and bool(observation.namespace_inventory)
        and all(row.get("ownedActive") is False and row.get("atNs") is not None for row in observation.process_inventory + observation.workspace_inventory + observation.namespace_inventory)
        and all(row.get("atNs") is not None for row in observation.timings)
    )
    e009 = any(row.get("kind") == "binding_manifest" and row.get("verified") is True for row in receipts)
    e010 = bool(observation.cleanup_receipts) and all(row.get("complete") is True for row in observation.cleanup_receipts)
    values = [e001,e002,e003,e004,e005,e006,e007,e008,e009,e010]
    details = [
        "setup/case/repetition/timing identities", "exact boundary bytes/digests/lengths", "capture provenance and sequence",
        "stub raw request receipt", "stub terminal receipt", "fault/cancellation timing", "conversation/event/workspace/profile identities",
        "process/workspace/namespace observations", "frozen configuration and source binding", "cleanup and final absence",
    ]
    for index, (passed, detail) in enumerate(zip(values, details, strict=True), 1):
        results.append(OracleResult(f"E-{index:03d}", bool(passed), digests, detail))
    return results
