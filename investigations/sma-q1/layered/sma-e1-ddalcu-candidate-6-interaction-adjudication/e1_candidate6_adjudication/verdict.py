from __future__ import annotations

import json
from dataclasses import asdict
from typing import Any, Mapping

from e1.runner import BY_S2, SMA_ORACLES, SYNTHETIC_SECRET
from e1_candidate4.runner import CONDENSATION_PROMPT, MAX_MODEL_CALLS_PER_INTERACTION
from e1_candidate6.runner import interaction_answers, model_behavior
from t1.model import FailureClass


CASE_13 = "SMA-S2-013-FOUR-CHANNEL-CONCURRENCY"
CASE_15 = "SMA-S2-015-SECRET-DELIVERY-ABSENCE"
CASE_16 = "SMA-S2-016-EMPTY-RESULT"
CASE_18 = "SMA-S2-018-CONDENSATION-REANCHOR"
CASE_13_ROLES = {"alpha-1", "alpha-2", "beta-1", "beta-2"}


def _prompt_submissions(observation: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    return [
        row
        for row in observation.get("raw_receipts", [])
        if row.get("kind") == "prompt_submission"
    ]


def _request_terminal_maps(
    observation: Mapping[str, Any],
) -> tuple[dict[str, Mapping[str, Any]], dict[str, Mapping[str, Any]]]:
    requests = {
        str(row.get("requestId")): row
        for row in observation.get("model_requests", [])
        if row.get("requestId")
    }
    terminals = {
        str(row.get("requestId")): row
        for row in observation.get("model_terminals", [])
        if row.get("requestId")
    }
    return requests, terminals


def _successful_terminal(terminal: Mapping[str, Any]) -> bool:
    return bool(
        terminal.get("outcome") in {"CLIENT_RECEIVED", "SUCCESS"}
        and str(terminal.get("content") or "").strip()
    )


def _terminal_timings(
    observation: Mapping[str, Any],
) -> list[Mapping[str, Any]]:
    return [
        row
        for row in observation.get("timings", [])
        if row.get("kind") == "conversation_terminal"
    ]


def _conversation_roles(observation: Mapping[str, Any]) -> dict[str, str]:
    return {
        str(row.get("id")): str(row.get("role"))
        for row in observation.get("conversations", [])
        if row.get("id") and row.get("role")
    }


def _context_text(request: Mapping[str, Any]) -> str:
    return "\n".join(
        str(message.get("content") or "")
        for message in request.get("messages", [])
        if message.get("role") == "user"
    )


def _case13_terminal_boundary(
    observation: Mapping[str, Any],
) -> tuple[bool, str]:
    submissions = _prompt_submissions(observation)
    conversations = _conversation_roles(observation)
    submitted_ids = [str(row.get("conversationId")) for row in submissions]
    submitted_roles = [conversations.get(value) for value in submitted_ids]
    requests, terminals = _request_terminal_maps(observation)
    grouped: dict[str, list[Mapping[str, Any]]] = {}
    for request in requests.values():
        grouped.setdefault(str(request.get("interactionRole") or ""), []).append(request)
    terminal_by_conversation = {
        str(row.get("conversationId")): row
        for row in _terminal_timings(observation)
        if row.get("conversationId")
    }
    passed = bool(
        len(submissions) == 4
        and len(set(submitted_ids)) == 4
        and set(submitted_roles) == CASE_13_ROLES
        and set(grouped) == CASE_13_ROLES
        and all(1 <= len(grouped[role]) <= MAX_MODEL_CALLS_PER_INTERACTION for role in CASE_13_ROLES)
        and set(requests) == set(terminals)
        and all(_successful_terminal(row) for row in terminals.values())
        and set(terminal_by_conversation) == set(submitted_ids)
        and all(
            row.get("status") == "finished"
            and float(row.get("durationMs", float("inf")))
            <= float(row.get("deadlineMs", -1))
            for row in terminal_by_conversation.values()
        )
    )
    return passed, "four submitted OpenHands interactions terminate within the per-interaction call bound"


def _case13_partition_isolation(
    observation: Mapping[str, Any],
) -> tuple[bool, str]:
    requests, _ = _request_terminal_maps(observation)
    if not requests:
        return False, "no model requests retained"
    roles: set[str] = set()
    for request in requests.values():
        role = str(request.get("interactionRole") or "")
        workspace = str(request.get("workspaceRole") or "")
        context = _context_text(request)
        roles.add(role)
        expected_workspace = "alpha" if role.startswith("alpha-") else "beta"
        expected_marker = "mem-alpha-timeout" if expected_workspace == "alpha" else "mem-beta-port"
        forbidden_marker = "mem-beta-port" if expected_workspace == "alpha" else "mem-alpha-timeout"
        if (
            role not in CASE_13_ROLES
            or workspace != expected_workspace
            or expected_marker not in context
            or forbidden_marker in context
        ):
            return False, f"partition evidence mismatch for {role or '<missing>'}"
    return roles == CASE_13_ROLES, "all internal calls retain their owning channel partition and exclude the opposite partition"


def _one_interaction_terminal(
    observation: Mapping[str, Any],
) -> tuple[bool, str]:
    submissions = _prompt_submissions(observation)
    requests, terminals = _request_terminal_maps(observation)
    terminal_rows = _terminal_timings(observation)
    passed = bool(
        len(submissions) == 1
        and 1 <= len(requests) <= MAX_MODEL_CALLS_PER_INTERACTION
        and set(requests) == set(terminals)
        and all(_successful_terminal(row) for row in terminals.values())
        and len(terminal_rows) == 1
        and terminal_rows[0].get("status") == "finished"
        and float(terminal_rows[0].get("durationMs", float("inf")))
        <= float(terminal_rows[0].get("deadlineMs", -1))
    )
    return passed, "one submitted OpenHands interaction terminates through bounded internal model calls"


def _sealed_raw_per_model_call(
    observation: Mapping[str, Any],
) -> tuple[bool, str]:
    requests, _ = _request_terminal_maps(observation)
    sealed = [
        row
        for row in observation.get("raw_receipts", [])
        if row.get("kind") == "stub_raw"
    ]
    passed = bool(
        requests
        and len(sealed) == len(requests)
        and all(
            row.get("sensitivity") == "SEALED_RAW"
            and row.get("bodyDisclosed") is False
            and row.get("digest")
            for row in sealed
        )
    )
    return passed, "every internal model call is retained only as a sealed raw digest"


def _condensation_interactions(
    observation: Mapping[str, Any],
) -> tuple[bool, str]:
    submissions = _prompt_submissions(observation)
    submitted_ids = {str(row.get("conversationId")) for row in submissions}
    requests, terminals = _request_terminal_maps(observation)
    ordered = sorted(requests.values(), key=lambda row: int(row.get("receivedNs", 0)))
    condensation_indexes = [
        index
        for index, row in enumerate(ordered)
        if row.get("prompt") == CONDENSATION_PROMPT
    ]
    terminal_rows = _terminal_timings(observation)
    passed = bool(
        len(submissions) == 2
        and 3 <= len(requests) <= 3 * MAX_MODEL_CALLS_PER_INTERACTION
        and set(requests) == set(terminals)
        and all(_successful_terminal(row) for row in terminals.values())
        and len(condensation_indexes) == 1
        and condensation_indexes[0] > 0
        and condensation_indexes[0] < len(ordered) - 1
        and bool(terminal_rows)
        and {
            str(row.get("conversationId"))
            for row in terminal_rows
        } == submitted_ids
        and all(
            row.get("status") == "finished"
            and float(row.get("durationMs", float("inf")))
            <= float(row.get("deadlineMs", -1))
            for row in terminal_rows
        )
    )
    return passed, "two user turns bracket one condensation call with bounded internal model calls"


def interaction_oracle_overrides(
    observation: Mapping[str, Any],
) -> dict[str, tuple[bool, str]]:
    case_id = str(observation.get("key", {}).get("case_id") or "")
    if case_id == CASE_13:
        return {
            "S2-013-P1": _case13_terminal_boundary(observation),
            "S2-013-P2": _case13_partition_isolation(observation),
        }
    if case_id == CASE_15:
        return {"S2-015-P3": _sealed_raw_per_model_call(observation)}
    if case_id == CASE_16:
        return {"S2-016-P2": _one_interaction_terminal(observation)}
    if case_id == CASE_18:
        return {"S2-018-P3": _condensation_interactions(observation)}
    return {}


def case13_continuation_basis(record: Mapping[str, Any]) -> dict[str, Any]:
    """Re-adjudicate the stopped case-13 record without altering it."""

    observation = record.get("observation", {})
    overrides = interaction_oracle_overrides(observation)
    original_failures = {
        str(row.get("oracle_id"))
        for row in record.get("scientific", [])
        if row.get("passed") is not True
    }
    vector = record.get("vector", {})
    unaffected_vector_pass = all(
        vector.get(field) == "PASS"
        for field in (
            "modelBehaviorVerdict",
            "environmentVerdict",
            "harnessVerdict",
            "cleanupVerdict",
            "safetyVerdict",
            "executionVerdict",
        )
    )
    passed = bool(
        str(observation.get("key", {}).get("case_id")) == CASE_13
        and original_failures == {"S2-013-P1", "S2-013-P2"}
        and set(overrides) == original_failures
        and all(value[0] for value in overrides.values())
        and all(row.get("passed") is True for row in record.get("evidence", []))
        and observation.get("failure") is None
        and unaffected_vector_pass
    )
    return {
        "operationKey": vector.get("operationKey"),
        "originalOverallVerdict": vector.get("overallRepetitionVerdict"),
        "originalScientificFailures": sorted(original_failures),
        "interactionOracleOverrides": {
            oracle_id: {"passed": value[0], "detail": value[1]}
            for oracle_id, value in overrides.items()
        },
        "correctedOverallVerdict": "PASS" if passed else "FAIL",
    }


def verdict_vector(result: Any) -> dict[str, Any]:
    observation = result.observation
    serialized = asdict(observation)
    outcomes = {row.oracle_id: bool(row.passed) for row in result.scientific}
    outcome_details = {row.oracle_id: row.detail for row in result.scientific}
    overrides = interaction_oracle_overrides(serialized)
    for oracle_id, (passed, detail) in overrides.items():
        outcomes[oracle_id] = passed
        outcome_details[oracle_id] = detail
    case_id = observation.key.case_id
    if case_id.endswith("SECRET-DELIVERY-ABSENCE"):
        derived = json.dumps({
            "hookAdditionalContext": [hook.get("additional_context") for hook in observation.hooks],
            "logs": observation.logs,
            "retrievals": observation.retrievals,
            "semantic": observation.semantic_points,
            "episodic": observation.episodic_points,
        }, sort_keys=True)
        outcomes["E1-015-SMA"] = SYNTHETIC_SECRET not in derived
        outcomes["E1-015-BOUNDARY"] = all(
            SYNTHETIC_SECRET not in str(hook.get("additional_context") or "")
            for hook in observation.hooks
        )
    sma_ids = SMA_ORACLES[case_id]
    sma = all(outcomes.get(oracle_id, False) for oracle_id in sma_ids)
    replaced = {"S2-015-P1", "S2-015-P2"} if case_id.endswith("SECRET-DELIVERY-ABSENCE") else set()
    boundary_ids = [
        row.oracle_id
        for row in result.scientific
        if row.oracle_id not in set(sma_ids) | replaced
    ]
    boundary = all(outcomes.get(oracle_id, False) for oracle_id in boundary_ids)
    if case_id.endswith("SECRET-DELIVERY-ABSENCE"):
        boundary = boundary and outcomes["E1-015-BOUNDARY"]
    answers, all_outputs, selection_ok, selection_detail = interaction_answers(observation)
    model, model_detail = model_behavior(case_id, answers, all_outputs, selection_ok, selection_detail)
    environment = observation.failure != FailureClass.ENVIRONMENT
    harness = observation.failure != FailureClass.HARNESS and all(row.passed for row in result.evidence)
    cleanup = bool(observation.cleanup_receipts) and all(row.get("complete") is True for row in observation.cleanup_receipts)
    safety = observation.failure != FailureClass.SAFETY
    no_failure = observation.failure is None
    overall = all((sma, boundary, model, environment, harness, cleanup, safety, no_failure))
    return {
        "sourceScenario": BY_S2[case_id]["sourceId"],
        "operationKey": observation.key.value,
        "smaSemanticsVerdict": "PASS" if sma else "FAIL",
        "openHandsBoundaryVerdict": "PASS" if boundary else "FAIL",
        "modelBehaviorVerdict": "PASS" if model else "FAIL",
        "modelBehaviorDetail": model_detail,
        "selectedInteractionAnswers": len(answers),
        "environmentVerdict": "PASS" if environment else "INCONCLUSIVE",
        "harnessVerdict": "PASS" if harness else "INCONCLUSIVE",
        "cleanupVerdict": "PASS" if cleanup else "FAIL",
        "safetyVerdict": "PASS" if safety else "FAIL",
        "executionVerdict": "PASS" if no_failure else "FAIL",
        "overallRepetitionVerdict": "PASS" if overall else "FAIL",
        "interactionOracleOverrides": {
            oracle_id: {"passed": passed, "detail": detail}
            for oracle_id, (passed, detail) in overrides.items()
        },
    }
