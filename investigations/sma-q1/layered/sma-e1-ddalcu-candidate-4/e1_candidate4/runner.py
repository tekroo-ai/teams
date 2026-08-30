from __future__ import annotations

import base64
from hashlib import sha256
import json
from typing import Any, Mapping

from e1.runner import (
    E1RunnerConfiguration,
    MODEL_ID,
    _compat_request,
    e1_plan,
)
from e1_candidate3.runner import (
    Candidate3OfflineWorld,
    Candidate3Runner,
    _extract_task,
    _request_payload,
)
from t1.model import (
    FailureClass,
    HarnessFailure,
    OperationKey,
    OperationObservation,
    RunMode,
    canonical_bytes,
    require,
)
from t1.runner import Runner as S2Runner
from t1.truth import ProductTruth


CONDENSATION_PROMPT = "Summarize the conversation for condensation."
MAX_MODEL_CALLS_PER_INTERACTION = 12
MULTI_CALL_CASES = {
    "SMA-S2-002-SAME-PARTITION-DELIVERY",
    "SMA-S2-009-ADDITIONAL-CONTEXT-INTEGRITY",
    "SMA-S2-012-RESTART-CONTINUITY",
}


def _interaction_count(observation: OperationObservation) -> int:
    if observation.key.case_id.endswith("FOUR-CHANNEL-CONCURRENCY"):
        return 4
    if observation.key.case_id.endswith("CONDENSATION-REANCHOR"):
        return 3
    return 1


class Candidate4Runner(Candidate3Runner):
    """Collects a bounded OpenHands agent loop instead of assuming one LLM call."""

    def _poll_jsonl(
        self,
        observation: OperationObservation,
        path: str,
        expected: int | tuple[int, ...],
        exact: bool = False,
    ) -> list[dict[str, Any]]:
        if path not in {self.config.stub_raw_path, self.config.stub_terminal_path}:
            return super()._poll_jsonl(observation, path, expected, exact)
        minimum = _interaction_count(observation)
        rows = super()._poll_jsonl(observation, path, minimum, exact=False)
        maximum = minimum * MAX_MODEL_CALLS_PER_INTERACTION
        observation.raw_receipts.append({
            "kind": "model_call_cardinality",
            "path": path,
            "minimumInteractions": minimum,
            "maximumCalls": maximum,
            "observedCalls": len(rows),
            "exactCallCountRequired": False,
            "withinBound": len(rows) <= maximum,
            "rowIdentitySha256": sha256(canonical_bytes([
                {
                    "requestId": row.get("requestId"),
                    "operationKey": row.get("operationKey"),
                    "recordType": row.get("recordType"),
                }
                for row in rows
            ])).hexdigest(),
        })
        require(
            len(rows) <= maximum,
            FailureClass.SAFETY,
            f"bounded OpenHands model-call ceiling exceeded: observed={len(rows)} maximum={maximum}",
        )
        return rows

    def _decode_stub_request(
        self,
        row: Mapping[str, Any],
        observation: OperationObservation,
    ) -> dict[str, Any]:
        observation.raw_receipts.append({
            "kind": "e1_model_audit_request_raw",
            "row": dict(row),
            "bodyDisclosed": False,
        })
        require(row.get("recordType") == "SMA_E1_MODEL_AUDIT_REQUEST", FailureClass.HARNESS, "E1 request audit record type mismatch")
        require(row.get("operationKey") == observation.key.value, FailureClass.HARNESS, "E1 request operation identity mismatch")
        require(row.get("method") == "POST", FailureClass.HARNESS, "E1 model request method drift")
        require(row.get("path") == "/v1/chat/completions", FailureClass.HARNESS, "E1 model request route drift")
        normalized = S2Runner._decode_stub_request(
            self,
            _compat_request(row, observation, self._model_mode_for(observation)),
            observation,
        )
        payload = _request_payload(row)
        require(int(row["requestBodyLength"]) <= 2_097_152, FailureClass.SAFETY, "E1 model request exceeds receipt payload bound")
        require(payload.get("model") in {MODEL_ID, f"openai/{MODEL_ID}"}, FailureClass.HARNESS, "E1 model identity mismatch")
        require(payload.get("stream", False) is False, FailureClass.HARNESS, "E1 streaming profile drift")
        require(payload.get("temperature") in {0, 0.0, None}, FailureClass.HARNESS, "E1 temperature drift")
        require(payload.get("max_tokens", payload.get("max_completion_tokens", 128)) == 128, FailureClass.HARNESS, "E1 output-token bound drift")
        extra = payload.get("chat_template_kwargs") or (payload.get("extra_body") or {}).get("chat_template_kwargs") or {}
        require(extra.get("enable_thinking") is False, FailureClass.HARNESS, "E1 thinking control absent or enabled")
        tools = payload.get("tools")
        require(tools is None or tools == [] or tools == (), FailureClass.HARNESS, "E1 model request unexpectedly exposes native tools")

        frozen = self._prompt_for(observation)
        allowed = [frozen]
        if observation.key.case_id.endswith("CONDENSATION-REANCHOR"):
            allowed.append(CONDENSATION_PROMPT)
        candidates: list[tuple[int, int, str, str]] = []
        for message_index, message in enumerate(normalized["messages"]):
            if message.get("role") != "user":
                continue
            for segment_index, segment in enumerate(message.get("contentSegments", [])):
                for expected_prompt in allowed:
                    try:
                        task, framing = _extract_task(str(segment), expected_prompt)
                    except HarnessFailure:
                        continue
                    candidates.append((message_index, segment_index, task, framing))
        require(bool(candidates), FailureClass.HARNESS, "E1 request lost the frozen task across the OpenHands agent loop")
        selected = next((item for item in candidates if item[2] == CONDENSATION_PROMPT), candidates[0])
        message_index, segment_index, task, framing = selected
        segments = list(normalized["messages"][message_index]["contentSegments"])
        segments[segment_index] = task
        normalized["messages"][message_index]["wireContentSegments"] = list(normalized["messages"][message_index]["contentSegments"])
        normalized["messages"][message_index]["contentSegments"] = segments
        normalized["messages"][message_index]["content"] = "".join(segments)
        normalized["prompt"] = task
        normalized["promptFraming"] = framing
        normalized["agentLoopRequest"] = len(normalized["messages"]) > 2
        normalized["sourceRecordType"] = row.get("recordType")
        return normalized


def _response_body(request_id: str, content: str, finish_reason: str) -> bytes:
    return canonical_bytes({
        "id": request_id,
        "choices": [{"finish_reason": finish_reason, "message": {"role": "assistant", "content": content, "reasoning_content": None}}],
        "usage": {"prompt_tokens": 32, "completion_tokens": 8, "total_tokens": 40},
    })


def _replace_body(row: dict[str, Any], payload: dict[str, Any]) -> None:
    body = canonical_bytes(payload)
    row.update({
        "requestBodyLength": len(body),
        "requestBodySha256": sha256(body).hexdigest(),
        "requestBodyBase64": base64.b64encode(body).decode("ascii"),
    })


def _terminal(row: Mapping[str, Any], request_id: str, content: str, finish_reason: str, offset: int) -> dict[str, Any]:
    result = dict(row)
    body = _response_body(request_id, content, finish_reason)
    result.update({
        "requestId": request_id,
        "completedMonotonicNs": int(row["completedMonotonicNs"]) + offset,
        "responseContent": content,
        "reasoningContent": None,
        "responseBodyLength": len(body),
        "responseBodySha256": sha256(body).hexdigest(),
        "responseBodyBase64": base64.b64encode(body).decode("ascii"),
    })
    return result


class Candidate4OfflineWorld(Candidate3OfflineWorld):
    """Replays the three-call shape that escaped the candidate-3 audit."""

    def _submit_prompt(self, conversation_id: str, prompt: str):
        raw_start = len(self.stub_raw)
        terminal_start = len(self.stub_terminal)
        result = super()._submit_prompt(conversation_id, prompt)
        if self.current_key is None or self.current_key.case_id not in MULTI_CALL_CASES:
            return result
        raw_final = dict(self.stub_raw[raw_start])
        terminal_final = dict(self.stub_terminal[terminal_start])
        first_id = str(raw_final["requestId"]) + "-length"
        think_id = str(raw_final["requestId"]) + "-think"
        first = dict(raw_final)
        first.update({"requestId": first_id, "receivedMonotonicNs": int(raw_final["receivedMonotonicNs"]) - 2})
        think = dict(raw_final)
        think.update({"requestId": think_id, "receivedMonotonicNs": int(raw_final["receivedMonotonicNs"]) - 1})
        think_payload = _request_payload(think)
        think_payload["messages"].extend([
            {"role": "assistant", "content": "<function=think>\n<parameter=thought>Use the recalled evidence.</parameter>\n</function>"},
            {"role": "user", "content": "EXECUTION RESULT of [think]:\nYour thought has been logged."},
        ])
        _replace_body(think, think_payload)
        first_terminal = _terminal(terminal_final, first_id, "I will use the recalled evidence", "length", -2)
        think_terminal = _terminal(terminal_final, think_id, "<function=think>\n<parameter=thought>Use the recalled evidence.</parameter>\n</function>", "stop", -1)
        self.stub_raw[raw_start:] = [first, think, raw_final]
        self.stub_terminal[terminal_start:] = [first_terminal, think_terminal, terminal_final]
        return result


def run_candidate4_offline(
    truth: ProductTruth,
    plan: tuple[OperationKey, ...] | None = None,
):
    world = Candidate4OfflineWorld(truth)
    runner = Candidate4Runner(truth, world, E1RunnerConfiguration(), RunMode.OFFLINE_MEASURED)
    results = []
    for key in plan or e1_plan():
        world.current_key = key
        result = runner.run_operation(key)
        results.append(result)
        if result.observation.failure in {FailureClass.HARNESS, FailureClass.ENVIRONMENT, FailureClass.SAFETY}:
            break
    return results, runner, world
