"""Independent, serialized-receipt reconstruction for final-P2 S2.

This module deliberately does not import the primary oracle registry.  It
reconstructs the operational grade from retained JSON records using a second,
case-oriented implementation so H0 can detect shared-oracle serialization
errors rather than merely replaying the original functions.
"""

from __future__ import annotations

from hashlib import sha256
import json
from typing import Any, Iterable, Mapping


def _digest(value: Any) -> str:
    return sha256(json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()).hexdigest()


def _contexts(o: Mapping[str, Any]) -> list[str]:
    return [str(row.get("additional_context") or "") for row in o.get("hooks", [])]


def _timing(o: Mapping[str, Any], kind: str) -> list[Mapping[str, Any]]:
    return [row for row in o.get("timings", []) if row.get("kind") == kind]


def _generic(o: Mapping[str, Any]) -> bool:
    receipts = o.get("raw_receipts", [])
    boundary = next((row for row in receipts if row.get("kind") == "boundary_bytes"), None)
    binding = next((row for row in receipts if row.get("kind") == "binding_manifest"), None)
    requests = o.get("model_requests", [])
    terminals = o.get("model_terminals", [])
    request_ids = {row.get("requestId") for row in requests}
    terminal_ids = {row.get("requestId") for row in terminals}
    inventories = o.get("process_inventory", []) + o.get("workspace_inventory", []) + o.get("namespace_inventory", [])
    return bool(
        o.get("failure") is None
        and binding and binding.get("verified") is True
        and boundary and len(str(boundary.get("promptSha256", ""))) == 64
        and request_ids == terminal_ids
        and all(row.get("recordType") == "SMA_S2_STUB_RAW_REQUEST" for row in requests)
        and all(row.get("recordType") == "SMA_S2_STUB_TERMINAL" for row in terminals)
        and o.get("conversations")
        and o.get("cleanup_receipts") and all(row.get("complete") is True for row in o["cleanup_receipts"])
        and inventories and all(row.get("ownedActive") is False for row in inventories)
    )


def _case(o: Mapping[str, Any]) -> bool:
    case_id = str(o["key"]["case_id"])
    contexts = _contexts(o)
    requests = o.get("model_requests", [])
    terminals = o.get("model_terminals", [])
    memories = o.get("memories", [])
    events = o.get("raw_events", [])
    if case_id.endswith("FIRST-PROMPT-EMPTY"):
        return contexts == [""] and len(requests) == len(terminals) == 1 and not memories
    if case_id.endswith("SAME-PARTITION-DELIVERY"):
        return len(contexts) == 1 and "mem-alpha-timeout" in contexts[0] and "untrusted evidence" in contexts[0]
    if case_id.endswith("CROSS-PARTITION-DELIVERY-DENIAL"):
        return "mem-alpha-timeout" not in json.dumps(o)
    if case_id.endswith("UNTRUSTED-CONTEXT-PLACEMENT"):
        return len(contexts) == 1 and "--- MEMORY" in contexts[0] and "DELETE_CONFIRMED" not in str(requests[0].get("prompt"))
    if case_id.endswith("RAW-INELIGIBLE-ABSENCE"):
        return "mem-alpha-raw" not in "\n".join(contexts)
    if case_id.endswith("DUPLICATE-PERSISTED-EVENT"):
        ids = [row.get("_id") for row in memories]
        return len(ids) == len(set(ids)) and bool(ids)
    if case_id.endswith("RETRIEVAL-OUTAGE"):
        return contexts == [""] and len(requests) == len(terminals) == 1 and bool(o.get("logs"))
    if case_id.endswith("CAPTURE-OUTAGE"):
        return len(requests) == len(terminals) == 1 and bool(_timing(o, "capture_recovery"))
    if case_id.endswith("ADDITIONAL-CONTEXT-INTEGRITY"):
        return len(contexts) == 1 and contexts[0].count("--- MEMORY") == 1 and bool(o.get("retrievals")) and bool(o.get("semantic_points")) and bool(o.get("episodic_points"))
    if case_id.endswith("HOOK-FAULT-MATRIX"):
        return len(o.get("hooks", [])) == 1 and contexts == [""] and len(requests) == len(terminals) == 1
    if case_id.endswith("PARENT-CHILD-PROVENANCE"):
        conversations = o.get("conversations", [])
        return len(conversations) == 2 and conversations[1].get("parent_conversation_id") == conversations[0].get("id") and conversations[1].get("id") in conversations[0].get("sub_conversation_ids", [])
    if case_id.endswith("RESTART-CONTINUITY"):
        before = _timing(o, "partition_before"); after = _timing(o, "partition_after")
        return len(before) == len(after) == 1 and before[0].get("agentId") == after[0].get("agentId") and "mem-alpha-timeout" in "\n".join(contexts)
    if case_id.endswith("FOUR-CHANNEL-CONCURRENCY"):
        telemetry = _timing(o, "concurrency")
        return len(requests) == len(terminals) == 4 and len(telemetry) == 1 and int(telemetry[0].get("peakInFlight", 0)) >= 2
    if case_id.endswith("FEEDBACK-LOOP-PREVENTION"):
        return len(requests) == len(terminals) == 2 and len(o.get("hooks", [])) == 1 and len({row.get("id") for row in events}) == len(events)
    if case_id.endswith("SECRET-DELIVERY-ABSENCE"):
        return "sk-test-SMA-S2-NEVER-PERSIST" not in json.dumps({"logs": o.get("logs"), "retrievals": o.get("retrievals"), "semantic": o.get("semantic_points"), "episodic": o.get("episodic_points")})
    if case_id.endswith("EMPTY-RESULT"):
        return contexts == [""] and len(requests) == len(terminals) == 1
    if case_id.endswith("OVERSIZED-CONTEXT"):
        return len(contexts) == 1 and len(contexts[0]) <= 4096 and contexts[0].count("--- MEMORY") == contexts[0].count("--- END MEMORY")
    if case_id.endswith("CONDENSATION-REANCHOR"):
        summaries = [row for row in events if row.get("kind") == "CondensationSummaryEvent"]
        prompt = next(
            row.get("prompt")
            for row in requests
            if str(row.get("prompt", "")).startswith("[SMA-S2-STUB case=")
        )
        return len(requests) == len(terminals) == 3 and sum(row.get("prompt") == prompt for row in requests) == 2 and len(contexts) == 2 and len(summaries) == 1
    if case_id.endswith("MODEL-STUB-FAULT-MATRIX"):
        repetition = int(o["key"]["repetition"])
        return len(_timing(o, "fault_activation")) == 1 and (not requests and not terminals if repetition == 4 else len(requests) == len(terminals) == 1)
    if case_id.endswith("ACTIVE-CANCELLATION-AND-SHUTDOWN"):
        required = {kind: _timing(o, kind) for kind in ("stub_raw", "cancel", "conversation_terminal", "client_disconnect", "owned_shutdown")}
        return all(len(rows) == 1 for rows in required.values()) and required["cancel"][0]["atNs"] > required["stub_raw"][0]["atNs"] and all(rows[0].get("durationMs", 10**9) <= (15_000 if kind == "owned_shutdown" else 10_000) for kind, rows in required.items() if "durationMs" in rows[0])
    return False


def reconstruct(records: Iterable[Mapping[str, Any]]) -> dict[str, Any]:
    count = passed = 0
    operation_digests: list[str] = []
    for record in records:
        observation = record["observation"]
        reconstructed = _generic(observation) and _case(observation)
        if reconstructed != bool(record.get("passed")):
            raise AssertionError(f"independent reconstruction mismatch: {observation['key']}")
        count += 1
        passed += int(reconstructed)
        operation_digests.append(_digest(observation))
    return {"operations": count, "passed": passed, "observationSetSha256": _digest(operation_digests), "implementation": "INDEPENDENT_CASE_ORIENTED_NO_PRIMARY_ORACLE_IMPORT"}
