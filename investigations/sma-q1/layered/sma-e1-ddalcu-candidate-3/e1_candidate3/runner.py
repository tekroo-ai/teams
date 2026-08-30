from __future__ import annotations

import base64
from hashlib import sha256
import json
from typing import Any, Mapping

from e1.runner import (
    BY_S2,
    E1OfflineWorld,
    E1Runner,
    E1RunnerConfiguration,
    MODEL_ID,
    _compat_request,
    _compat_terminal,
    e1_plan,
)
from t1.model import (
    FailureClass,
    OperationKey,
    OperationObservation,
    RunMode,
    canonical_bytes,
    require,
)
from t1.runner import Runner as S2Runner
from t1.truth import ProductTruth


EXAMPLE_START = "Here's a running example of how to perform a task with the provided tools."
TASK_START = "--------------------- NEW TASK DESCRIPTION ---------------------\n"
TASK_END = "\n--------------------- END OF NEW TASK DESCRIPTION ---------------------"


def _request_payload(row: Mapping[str, Any]) -> dict[str, Any]:
    body = base64.b64decode(str(row["requestBodyBase64"]), validate=True)
    require(
        len(body) == int(row["requestBodyLength"]),
        FailureClass.HARNESS,
        "E1 request length mismatch",
    )
    require(
        sha256(body).hexdigest() == row["requestBodySha256"],
        FailureClass.HARNESS,
        "E1 request digest mismatch",
    )
    value = json.loads(body)
    require(isinstance(value, dict), FailureClass.HARNESS, "E1 request is not an object")
    return value


def _extract_task(text: str, expected: str) -> tuple[str, str]:
    if text == expected:
        return expected, "UNWRAPPED"
    require(text.startswith(EXAMPLE_START), FailureClass.HARNESS, "E1 model prompt has an unknown prefix")
    require(text.count(TASK_START) == 1, FailureClass.HARNESS, "E1 model prompt start marker cardinality drift")
    require(text.count(TASK_END) == 1, FailureClass.HARNESS, "E1 model prompt end marker cardinality drift")
    prefix, remainder = text.split(TASK_START, 1)
    task, suffix = remainder.split(TASK_END, 1)
    require(prefix.startswith(EXAMPLE_START), FailureClass.HARNESS, "E1 model prompt example framing drift")
    require(
        "PLEASE follow the format strictly!" in suffix,
        FailureClass.HARNESS,
        "E1 model prompt tool-call suffix drift",
    )
    require(task == expected, FailureClass.HARNESS, "E1 task bytes differ inside OpenHands framing")
    return task, "OPENHANDS_NON_NATIVE_TOOL_WRAPPER"


class Candidate3Runner(E1Runner):
    """E1 runner whose wire checks match the installed OpenHands transport."""

    def _decode_stub_request(
        self,
        row: Mapping[str, Any],
        observation: OperationObservation,
    ) -> dict[str, Any]:
        # Retain the exact sealed audit row before validation so a decoder
        # failure remains diagnosable after mandatory cleanup removes journals.
        observation.raw_receipts.append({
            "kind": "e1_model_audit_request_raw",
            "row": dict(row),
            "bodyDisclosed": False,
        })
        require(
            row.get("recordType") == "SMA_E1_MODEL_AUDIT_REQUEST",
            FailureClass.HARNESS,
            "E1 request audit record type mismatch",
        )
        require(
            row.get("operationKey") == observation.key.value,
            FailureClass.HARNESS,
            "E1 request operation identity mismatch",
        )
        require(row.get("method") == "POST", FailureClass.HARNESS, "E1 model request method drift")
        require(
            row.get("path") == "/v1/chat/completions",
            FailureClass.HARNESS,
            "E1 model request route drift",
        )
        normalized = S2Runner._decode_stub_request(
            self,
            _compat_request(row, observation, self._model_mode_for(observation)),
            observation,
        )
        payload = _request_payload(row)
        require(
            int(row["requestBodyLength"]) <= 2_097_152,
            FailureClass.SAFETY,
            "E1 model request exceeds receipt payload bound",
        )
        require(
            payload.get("model") in {MODEL_ID, f"openai/{MODEL_ID}"},
            FailureClass.HARNESS,
            "E1 model identity mismatch",
        )
        # OpenHands omits the field when non-streaming. An explicit true is a
        # profile violation; absence and explicit false have identical wire
        # semantics for the OpenAI-compatible API.
        require(
            payload.get("stream", False) is False,
            FailureClass.HARNESS,
            "E1 streaming profile drift",
        )
        require(
            payload.get("temperature") in {0, 0.0, None},
            FailureClass.HARNESS,
            "E1 temperature drift",
        )
        require(
            payload.get("max_tokens", payload.get("max_completion_tokens", 128)) == 128,
            FailureClass.HARNESS,
            "E1 output-token bound drift",
        )
        extra = payload.get("chat_template_kwargs") or (
            payload.get("extra_body") or {}
        ).get("chat_template_kwargs") or {}
        require(
            extra.get("enable_thinking") is False,
            FailureClass.HARNESS,
            "E1 thinking control absent or enabled",
        )
        tools = payload.get("tools")
        require(
            tools is None or tools == [] or tools == (),
            FailureClass.HARNESS,
            "E1 model request unexpectedly exposes native tools",
        )

        frozen = self._prompt_for(observation)
        condensation = "Summarize the conversation for condensation."
        expected = condensation if (
            observation.key.case_id.endswith("CONDENSATION-REANCHOR")
            and any(
                condensation in str(segment)
                for message in normalized["messages"]
                if message.get("role") == "user"
                for segment in message.get("contentSegments", [])
            )
        ) else frozen
        user_messages = [
            message for message in normalized["messages"]
            if message.get("role") == "user"
        ]
        require(bool(user_messages), FailureClass.HARNESS, "E1 request has no user message")
        segments = list(user_messages[-1].get("contentSegments", []))
        require(bool(segments), FailureClass.HARNESS, "E1 request user content is empty")
        task, framing = _extract_task(str(segments[0]), expected)
        require(
            all(expected not in str(segment) for segment in segments[1:]),
            FailureClass.HARNESS,
            "E1 task was duplicated into recalled context",
        )
        user_messages[-1]["wireContentSegments"] = segments
        user_messages[-1]["contentSegments"] = [task, *segments[1:]]
        user_messages[-1]["content"] = "".join(user_messages[-1]["contentSegments"])
        normalized["prompt"] = task
        normalized["promptFraming"] = framing
        normalized["sourceRecordType"] = row.get("recordType")
        return normalized

    def _decode_stub_terminal(
        self,
        row: Mapping[str, Any],
        observation: OperationObservation,
    ) -> dict[str, Any]:
        observation.raw_receipts.append({
            "kind": "e1_model_audit_response_raw",
            "row": dict(row),
            "bodyDisclosed": False,
        })
        require(
            row.get("recordType") == "SMA_E1_MODEL_AUDIT_RESPONSE",
            FailureClass.HARNESS,
            "E1 response audit record type mismatch",
        )
        require(
            row.get("operationKey") == observation.key.value,
            FailureClass.HARNESS,
            "E1 response operation identity mismatch",
        )
        normalized = S2Runner._decode_stub_terminal(
            self,
            _compat_terminal(row, observation, self._model_mode_for(observation)),
            observation,
        )
        content = row.get("responseContent")
        reasoning = row.get("reasoningContent")
        if row.get("responseBodyBase64"):
            body = base64.b64decode(str(row["responseBodyBase64"]), validate=True)
            require(len(body) <= 2_097_152, FailureClass.SAFETY, "E1 model response exceeds receipt payload bound")
            require(len(body) == int(row["responseBodyLength"]), FailureClass.HARNESS, "E1 response length mismatch")
            require(sha256(body).hexdigest() == row["responseBodySha256"], FailureClass.HARNESS, "E1 response digest mismatch")
            payload = json.loads(body)
            choices = payload.get("choices") or []
            require(len(choices) == 1, FailureClass.HARNESS, "E1 response choice cardinality drift")
            message = choices[0].get("message") or {}
            content = message.get("content")
            reasoning = message.get("reasoning_content")
            normalized["usage"] = payload.get("usage")
        require(int(row.get("httpStatus", 0)) == 200, FailureClass.SCIENTIFIC, "E1 model request did not terminate successfully")
        require(reasoning is None or reasoning == "", FailureClass.SCIENTIFIC, "E1 model emitted thinking/reasoning content")
        normalized["content"] = content
        normalized["reasoningContent"] = reasoning
        normalized["sourceRecordType"] = row.get("recordType")
        return normalized


def _wrap_first_user_segment(row: dict[str, Any]) -> None:
    payload = _request_payload(row)
    users = [message for message in payload.get("messages", []) if message.get("role") == "user"]
    require(bool(users), FailureClass.HARNESS, "offline E1 request has no user message")
    content = users[-1].get("content")
    prefix = (
        EXAMPLE_START
        + "\n\n--------------------- START OF EXAMPLE ---------------------\n\n"
        + "USER: synthetic offline tool example.\n\n"
        + TASK_START
    )
    suffix = TASK_END + "\n\nPLEASE follow the format strictly! PLEASE EMIT ONE AND ONLY ONE FUNCTION CALL PER MESSAGE.\n"
    if isinstance(content, str):
        users[-1]["content"] = prefix + content + suffix
    else:
        require(isinstance(content, list) and bool(content), FailureClass.HARNESS, "offline E1 content serializer drift")
        content[0]["text"] = prefix + str(content[0].get("text", "")) + suffix
    wire = canonical_bytes(payload)
    row.update({
        "method": "POST",
        "path": "/v1/chat/completions",
        "requestBodyLength": len(wire),
        "requestBodySha256": sha256(wire).hexdigest(),
        "requestBodyBase64": base64.b64encode(wire).decode("ascii"),
    })


class Candidate3OfflineWorld(E1OfflineWorld):
    def http(self, target: str, request: Any):
        start = len(self.stub_raw)
        result = super().http(target, request)
        if target == "OPENHANDS" and request.route.endswith("/condense"):
            for row in self.stub_raw[start:]:
                _wrap_first_user_segment(row)
        return result

    def _submit_prompt(self, conversation_id: str, prompt: str):
        start = len(self.stub_raw)
        result = super()._submit_prompt(conversation_id, prompt)
        for row in self.stub_raw[start:]:
            _wrap_first_user_segment(row)
        return result


def run_candidate3_offline(
    truth: ProductTruth,
    plan: tuple[OperationKey, ...] | None = None,
):
    world = Candidate3OfflineWorld(truth)
    runner = Candidate3Runner(
        truth,
        world,
        E1RunnerConfiguration(),
        RunMode.OFFLINE_MEASURED,
    )
    results = []
    for key in plan or e1_plan():
        world.current_key = key
        result = runner.run_operation(key)
        results.append(result)
        if result.observation.failure in {
            FailureClass.HARNESS,
            FailureClass.ENVIRONMENT,
            FailureClass.SAFETY,
        }:
            break
    return results, runner, world
