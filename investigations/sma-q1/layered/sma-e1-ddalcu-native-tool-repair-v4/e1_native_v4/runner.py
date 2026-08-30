from __future__ import annotations

import base64
from hashlib import sha256
import json
from pathlib import Path
from typing import Any, Mapping

from e1.runner import _compat_terminal
from e1_native.runner import (
    NativeToolRunner,
    _native_tool,
    _semantic_content,
    verdict_vector,
)
from t1.model import FailureClass, HarnessFailure, OperationObservation, require
from t1.runner import Runner as S2Runner


def retained_workspace_role(observation: OperationObservation, role: str) -> str:
    expected = (
        observation.descriptor.workspace_roles[0]
        if role == "primary"
        else "beta" if role.startswith("beta") else "alpha"
    )
    matches = [row for row in observation.conversations if row.get("role") == role]
    require(len(matches) == 1, FailureClass.HARNESS, f"conversation ownership mismatch for role {role!r}")
    workspace = matches[0].get("workspace") or {}
    working_dir = workspace.get("working_dir") if isinstance(workspace, Mapping) else workspace
    require(Path(str(working_dir or "")).name == expected, FailureClass.HARNESS, f"workspace identity mismatch for role {role!r}")
    require(expected in observation.descriptor.workspace_roles, FailureClass.HARNESS, f"workspace role is outside operation scope: {expected!r}")
    return expected


def semantic_with_provider_reasoning(semantic: str, reasoning: Any) -> str:
    retained = str(reasoning or "").strip()
    return semantic if not retained else retained + "\n" + semantic


class NativeToolRunnerV4(NativeToolRunner):
    def _decode_stub_request(self, row: Mapping[str, Any], observation: OperationObservation) -> dict[str, Any]:
        normalized = super()._decode_stub_request(row, observation)
        normalized["workspaceRole"] = retained_workspace_role(observation, str(normalized["interactionRole"]))
        return normalized

    def _decode_stub_terminal(self, row: Mapping[str, Any], observation: OperationObservation) -> dict[str, Any]:
        observation.raw_receipts.append({"kind": "e1_model_audit_response_raw", "row": dict(row), "bodyDisclosed": False})
        require(row.get("recordType") == "SMA_E1_MODEL_AUDIT_RESPONSE", FailureClass.HARNESS, "E1 response audit record type mismatch")
        require(row.get("operationKey") == observation.key.value, FailureClass.HARNESS, "E1 response operation identity mismatch")
        normalized = S2Runner._decode_stub_terminal(
            self,
            _compat_terminal(row, observation, self._model_mode_for(observation)),
            observation,
        )
        try:
            body = base64.b64decode(str(row["responseBodyBase64"]), validate=True)
            require(len(body) <= 2_097_152, FailureClass.SAFETY, "E1 model response exceeds receipt payload bound")
            require(len(body) == int(row["responseBodyLength"]), FailureClass.HARNESS, "E1 response length mismatch")
            require(sha256(body).hexdigest() == row["responseBodySha256"], FailureClass.HARNESS, "E1 response digest mismatch")
            payload = json.loads(body)
        except HarnessFailure:
            raise
        except (KeyError, TypeError, ValueError, json.JSONDecodeError) as exc:
            raise HarnessFailure(FailureClass.HARNESS, "E1 native response framing is malformed") from exc
        require(int(row.get("httpStatus", 0)) == 200, FailureClass.SCIENTIFIC, "E1 model request did not terminate successfully")
        choices = payload.get("choices") or []
        require(len(choices) == 1, FailureClass.HARNESS, "E1 response choice cardinality drift")
        choice = choices[0]
        message = choice.get("message") or {}
        finish_reason = choice.get("finish_reason")
        tool_name, arguments = _native_tool(message)
        require(
            (tool_name is None and finish_reason == "stop")
            or (tool_name is not None and finish_reason in {"tool_calls", "stop"}),
            FailureClass.SCIENTIFIC,
            f"E1 native model response is incomplete: finish_reason={finish_reason!r} tool={tool_name!r}",
        )
        answer = _semantic_content(message.get("content"), tool_name, arguments)
        require(bool(answer), FailureClass.SCIENTIFIC, "E1 native model response is empty")
        reasoning = message.get("reasoning_content")
        normalized["content"] = semantic_with_provider_reasoning(answer, reasoning)
        normalized["answerContent"] = answer
        normalized["providerContent"] = message.get("content")
        normalized["providerReasoningContent"] = reasoning
        normalized["nativeToolName"] = tool_name
        normalized["nativeToolArguments"] = arguments
        normalized["finishReason"] = finish_reason
        normalized["usage"] = payload.get("usage")
        normalized["sourceRecordType"] = row.get("recordType")
        return normalized
