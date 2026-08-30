from __future__ import annotations

from typing import Any, Mapping

from e1.runner import MODEL_ID, _compat_request
from e1_candidate3.runner import _extract_task, _request_payload
from e1_candidate4.runner import CONDENSATION_PROMPT
from e1_candidate5.runner import MAX_OUTPUT_TOKENS
from e1_native.runner import expected_native_tools
from e1_native_v4.runner import retained_workspace_role
from e1_native_v5.runner import NativeToolRunnerV5
from t1.model import FailureClass, HarnessFailure, OperationObservation, require
from t1.runner import Runner as S2Runner


CONDENSATION_PREFIX = "You are maintaining a context-aware state summary for an interactive agent."
CONDENSATION_SUFFIX = "Now summarize the events using the rules above."


def is_real_condensation_prompt(value: str, frozen_task: str) -> bool:
    text = value.strip()
    return text.startswith(CONDENSATION_PREFIX) and text.endswith(CONDENSATION_SUFFIX) and "<EVENT>" in text and frozen_task in text


class NativeToolRunnerV6(NativeToolRunnerV5):
    def _decode_stub_request(self, row: Mapping[str, Any], observation: OperationObservation) -> dict[str, Any]:
        observation.raw_receipts.append({"kind": "e1_model_audit_request_raw", "row": dict(row), "bodyDisclosed": False})
        require(row.get("recordType") == "SMA_E1_MODEL_AUDIT_REQUEST", FailureClass.HARNESS, "E1 request audit record type mismatch")
        require(row.get("operationKey") == observation.key.value, FailureClass.HARNESS, "E1 request operation identity mismatch")
        require(row.get("method") == "POST" and row.get("path") == "/v1/chat/completions", FailureClass.HARNESS, "E1 model request transport drift")
        normalized = S2Runner._decode_stub_request(self, _compat_request(row, observation, self._model_mode_for(observation)), observation)
        payload = _request_payload(row)
        require(int(row["requestBodyLength"]) <= 2_097_152, FailureClass.SAFETY, "E1 model request exceeds receipt payload bound")
        require(payload.get("model") in {MODEL_ID, f"openai/{MODEL_ID}"}, FailureClass.HARNESS, "E1 model identity mismatch")
        require(payload.get("stream", False) is False, FailureClass.HARNESS, "E1 streaming profile drift")
        require(payload.get("temperature") in {0, 0.0, None}, FailureClass.HARNESS, "E1 temperature drift")
        require(payload.get("max_tokens", payload.get("max_completion_tokens")) == MAX_OUTPUT_TOKENS, FailureClass.HARNESS, "E1 output-token ceiling drift")
        extra = payload.get("chat_template_kwargs") or (payload.get("extra_body") or {}).get("chat_template_kwargs") or {}
        require(extra.get("enable_thinking") is False, FailureClass.HARNESS, "E1 thinking control absent or enabled")
        frozen = self._prompt_for(observation)
        tool_names = {str((tool.get("function") or {}).get("name") or "") for tool in (payload.get("tools") or [])}
        selected: tuple[int, int, str, str] | None = None
        if not tool_names:
            require(observation.key.case_id.endswith("CONDENSATION-REANCHOR"), FailureClass.HARNESS, "tool-free request is not owned by condensation")
            candidates = []
            for message_index, message in enumerate(normalized["messages"]):
                if message.get("role") != "user":
                    continue
                for segment_index, segment in enumerate(message.get("contentSegments", [])):
                    if is_real_condensation_prompt(str(segment), frozen):
                        candidates.append((message_index, segment_index, CONDENSATION_PROMPT, str(segment)))
            require(len(candidates) == 1, FailureClass.HARNESS, "real OpenHands condensation prompt identity mismatch")
            selected = candidates[0]
        else:
            expected = expected_native_tools(observation.key.case_id, frozen)
            require(tool_names == expected, FailureClass.HARNESS, f"native tool schema drift: {sorted(tool_names)}")
            candidates = []
            for message_index, message in enumerate(normalized["messages"]):
                if message.get("role") != "user":
                    continue
                for segment_index, segment in enumerate(message.get("contentSegments", [])):
                    try:
                        task, framing = _extract_task(str(segment), frozen)
                    except HarnessFailure:
                        continue
                    candidates.append((message_index, segment_index, task, framing))
            require(bool(candidates), FailureClass.HARNESS, "E1 request lost the frozen task across the OpenHands agent loop")
            selected = candidates[0]
        message_index, segment_index, task, framing = selected
        segments = list(normalized["messages"][message_index]["contentSegments"])
        normalized["messages"][message_index]["wireContentSegments"] = list(segments)
        segments[segment_index] = task
        normalized["messages"][message_index]["contentSegments"] = segments
        normalized["messages"][message_index]["content"] = "".join(segments)
        normalized["prompt"] = task
        normalized["promptFraming"] = framing
        normalized["agentLoopRequest"] = len(normalized["messages"]) > 2
        normalized["sourceRecordType"] = row.get("recordType")
        role = str(row.get("interactionRole") or "")
        require(role in observation.descriptor.conversation_roles, FailureClass.HARNESS, f"model request interaction role is missing or unowned: {role!r}")
        normalized["interactionRole"] = role
        normalized["workspaceRole"] = retained_workspace_role(observation, role)
        normalized["nativeToolNames"] = sorted(tool_names)
        normalized["realCondensationPrompt"] = task == CONDENSATION_PROMPT
        return normalized
