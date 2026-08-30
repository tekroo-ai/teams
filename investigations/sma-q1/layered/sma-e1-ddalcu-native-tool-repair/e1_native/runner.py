from __future__ import annotations

import base64
from hashlib import sha256
import json
from pathlib import Path
from typing import Any, Mapping

from e1.runner import BY_S2, MODEL_ID, SMA_ORACLES, SYNTHETIC_SECRET, _compat_request
from e1_candidate3.runner import Candidate3Runner, _extract_task, _request_payload
from e1_candidate4.runner import CONDENSATION_PROMPT, MAX_MODEL_CALLS_PER_INTERACTION
from e1_candidate5.runner import FINISH_MESSAGE, MAX_OUTPUT_TOKENS
from e1_candidate6.runner import Candidate6Runner, _submitted_roles
from e1_candidate6_adjudication.verdict import interaction_oracle_overrides
from t1.model import FailureClass, HarnessFailure, OperationObservation, RawHttpRequest, canonical_bytes, digest, require
from t1.runner import Runner as S2Runner


NATIVE_TOOL_NAMES = {"finish", "think", "switch_llm"}
ALLOWED_NATIVE_TOOL_NAMES = NATIVE_TOOL_NAMES | {"launch_child_conversation"}


def expected_native_tools(case_id: str, task: str) -> set[str]:
    if task == CONDENSATION_PROMPT:
        return set()
    names = set(NATIVE_TOOL_NAMES)
    if case_id.endswith("FEEDBACK-LOOP-PREVENTION"):
        names.add("launch_child_conversation")
    return names


def _native_tool(message: Mapping[str, Any]) -> tuple[str | None, dict[str, Any]]:
    calls = message.get("tool_calls") or []
    if not calls:
        return None, {}
    require(len(calls) == 1, FailureClass.SCIENTIFIC, "model emitted multiple native tool calls in one turn")
    function = calls[0].get("function") or {}
    name = str(function.get("name") or "")
    require(name in ALLOWED_NATIVE_TOOL_NAMES, FailureClass.SCIENTIFIC, f"model emitted an unowned native tool: {name!r}")
    raw_arguments = function.get("arguments") or "{}"
    try:
        arguments = json.loads(raw_arguments) if isinstance(raw_arguments, str) else dict(raw_arguments)
    except (TypeError, ValueError) as exc:
        raise HarnessFailure(FailureClass.SCIENTIFIC, "model emitted malformed native tool arguments") from exc
    require(isinstance(arguments, dict), FailureClass.SCIENTIFIC, "native tool arguments are not an object")
    return name, arguments


def _semantic_content(
    provider_content: Any,
    tool_name: str | None,
    arguments: Mapping[str, Any],
) -> str:
    if tool_name == "finish":
        message = str(arguments.get("message") or "").strip()
        require(bool(message), FailureClass.SCIENTIFIC, "FinishTool message is empty")
        return f"<function=finish>\n<parameter=message>{message}</parameter>\n</function>"
    if tool_name == "think":
        thought = str(arguments.get("thought") or "").strip()
        require(bool(thought), FailureClass.SCIENTIFIC, "ThinkTool thought is empty")
        return f"<function=think>\n<parameter=thought>{thought}</parameter>\n</function>"
    if tool_name == "switch_llm":
        return json.dumps(dict(arguments), sort_keys=True)
    return str(provider_content or "").strip()


class NativeToolRunner(Candidate6Runner):
    """Runs the accepted E1 science through OpenHands' native tool protocol."""

    def _create(
        self,
        observation: OperationObservation,
        role: str,
        parent_id: str | None = None,
    ) -> Mapping[str, Any]:
        workspace_role = (
            observation.descriptor.workspace_roles[0]
            if role == "primary"
            else "beta" if role.startswith("beta") else "alpha"
        )
        settings = self._http(
            observation,
            "OPENHANDS",
            RawHttpRequest("GET", "/api/settings", {"X-Session-API-Key": self.config.session_header_value}, b"", 10_000),
        )
        require(settings.status == 200 and settings.error_kind is None, FailureClass.ENVIRONMENT, "OpenHands settings retrieval failed")
        agent_settings = dict(settings.json_body().get("agent_settings") or {})
        require(bool(agent_settings), FailureClass.HARNESS, "OpenHands SettingsResponse missing agent_settings")
        llm = dict(agent_settings.get("llm", {}))
        llm.update({
            "model": f"openai/{MODEL_ID}",
            "model_canonical_name": "openai/gpt-4o",
            "base_url": self.config.stub_base_url,
            "api_mode": "chat",
            "api_key": "sma-e1-loopback-only",
            "native_tool_calling": True,
            "force_string_serializer": False,
            "stream": False,
            "temperature": 0,
            "max_output_tokens": MAX_OUTPUT_TOKENS,
            "num_retries": 0,
            "retry_multiplier": 0,
            "retry_min_wait": 0,
            "retry_max_wait": 0,
            "timeout": 120,
            "log_completions": False,
            "litellm_extra_body": {"chat_template_kwargs": {"enable_thinking": False}},
            "extra_headers": {
                "X-SMA-E1-Operation": observation.key.value,
                "X-SMA-E1-Interaction-Role": role,
            },
        })
        agent_settings["llm"] = llm
        agent_settings["tools"] = []
        agent_settings["mcp_config"] = {"mcpServers": {}}
        body: dict[str, Any] = {
            "conversation_id": None,
            "parent_conversation_id": parent_id,
            "workspace": {"kind": "LocalWorkspace", "working_dir": self._workspace(workspace_role, observation)},
            "agent_settings": agent_settings,
            "hook_config": self.config.resolved_hook_config(),
            "max_iterations": 10,
            "autotitle": False,
        }
        if observation.key.case_id.endswith("FEEDBACK-LOOP-PREVENTION"):
            tool_path = (
                self.truth.paths.repo
                / "investigations/sma-q1/launch-child-conversation-client-tool.json"
            )
            require(
                tool_path.is_file()
                and sha256(tool_path.read_bytes()).hexdigest()
                == "89575b6d659666f45b51c3d963b421cd174fd23e84b394714b2580b1a74e2c29",
                FailureClass.HARNESS,
                "bound client-tool definition drift",
            )
            body["client_tools"] = [
                json.loads(tool_path.read_text(encoding="utf-8"))
            ]
        receipt = self._http(
            observation,
            "OPENHANDS",
            RawHttpRequest(
                "POST",
                "/api/conversations",
                {"Content-Type": "application/json", "X-Session-API-Key": self.config.session_header_value},
                canonical_bytes(body),
                10_000,
            ),
        )
        require(receipt.status in {200, 201} and receipt.error_kind is None, FailureClass.ENVIRONMENT, "conversation creation failed")
        value = receipt.json_body()
        require(value.get("id") and value.get("workspace"), FailureClass.HARNESS, "conversation response missing product identity")
        value["role"] = role
        value["profile"] = self.config.profile
        observation.conversations.append(value)
        observation.raw_receipts.extend((
            {
                "kind": "model_endpoint",
                "baseUrl": llm["base_url"],
                "upstream": "http://127.0.0.1:8802/v1",
                "model": MODEL_ID,
                "thinking": False,
                "nativeToolCalling": True,
                "bodyDisclosed": False,
            },
            {
                "kind": "hook_installation",
                "conversationId": value["id"],
                "hookConfigSha256": digest(body["hook_config"]),
                "ledgerIndex": len(self.ledger.records),
            },
            {
                "kind": "interaction_binding",
                "conversationId": value["id"],
                "interactionRole": role,
                "header": "X-SMA-E1-Interaction-Role",
            },
        ))
        return value

    def _decode_stub_request(
        self,
        row: Mapping[str, Any],
        observation: OperationObservation,
    ) -> dict[str, Any]:
        observation.raw_receipts.append({"kind": "e1_model_audit_request_raw", "row": dict(row), "bodyDisclosed": False})
        require(row.get("recordType") == "SMA_E1_MODEL_AUDIT_REQUEST", FailureClass.HARNESS, "E1 request audit record type mismatch")
        require(row.get("operationKey") == observation.key.value, FailureClass.HARNESS, "E1 request operation identity mismatch")
        require(row.get("method") == "POST" and row.get("path") == "/v1/chat/completions", FailureClass.HARNESS, "E1 model request transport drift")
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
        require(payload.get("max_tokens", payload.get("max_completion_tokens")) == MAX_OUTPUT_TOKENS, FailureClass.HARNESS, "E1 practical output-token ceiling drift")
        extra = payload.get("chat_template_kwargs") or (payload.get("extra_body") or {}).get("chat_template_kwargs") or {}
        require(extra.get("enable_thinking") is False, FailureClass.HARNESS, "E1 thinking control absent or enabled")
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
        tool_names = {
            str((tool.get("function") or {}).get("name") or "")
            for tool in (payload.get("tools") or [])
        }
        expected_tools = expected_native_tools(observation.key.case_id, task)
        require(tool_names == expected_tools, FailureClass.HARNESS, f"native tool schema drift: {sorted(tool_names)}")
        segments = list(normalized["messages"][message_index]["contentSegments"])
        segments[segment_index] = task
        normalized["messages"][message_index]["wireContentSegments"] = list(normalized["messages"][message_index]["contentSegments"])
        normalized["messages"][message_index]["contentSegments"] = segments
        normalized["messages"][message_index]["content"] = "".join(segments)
        normalized["prompt"] = task
        normalized["promptFraming"] = framing
        normalized["agentLoopRequest"] = len(normalized["messages"]) > 2
        normalized["sourceRecordType"] = row.get("recordType")
        role = str(row.get("interactionRole") or "")
        require(role in observation.descriptor.conversation_roles, FailureClass.HARNESS, f"model request interaction role is missing or unowned: {role!r}")
        normalized["interactionRole"] = role
        normalized["nativeToolNames"] = sorted(tool_names)
        return normalized

    def _decode_stub_terminal(
        self,
        row: Mapping[str, Any],
        observation: OperationObservation,
    ) -> dict[str, Any]:
        normalized = Candidate3Runner._decode_stub_terminal(self, row, observation)
        body = base64.b64decode(str(row["responseBodyBase64"]), validate=True)
        payload = json.loads(body)
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
        content = _semantic_content(message.get("content"), tool_name, arguments)
        require(bool(content), FailureClass.SCIENTIFIC, "E1 native model response is empty")
        normalized["content"] = content
        normalized["providerContent"] = message.get("content")
        normalized["nativeToolName"] = tool_name
        normalized["nativeToolArguments"] = arguments
        normalized["finishReason"] = finish_reason
        normalized["usage"] = payload.get("usage")
        return normalized


def interaction_answers(
    observation: OperationObservation,
) -> tuple[list[str], list[str], bool, str]:
    requests = {str(row["requestId"]): row for row in observation.model_requests}
    terminals = {str(row["requestId"]): row for row in observation.model_terminals}
    if set(requests) != set(terminals):
        return [], [], False, "MODEL_REQUEST_TERMINAL_IDENTITY_MISMATCH"
    ordered = sorted(requests.values(), key=lambda row: int(row.get("receivedNs", 0)))
    all_outputs = [str(terminals[str(row["requestId"])].get("content") or "") for row in ordered]
    submitted_roles = _submitted_roles(observation)
    groups: dict[str, list[tuple[Mapping[str, Any], Mapping[str, Any]]]] = {}
    if observation.key.case_id.endswith("CONDENSATION-REANCHOR"):
        if submitted_roles != ["primary", "primary"]:
            return [], all_outputs, False, "CONDENSATION_SUBMITTED_TURN_SET_MISMATCH"
        phase = 1
        condensation_calls = 0
        for request in ordered:
            terminal = terminals[str(request["requestId"])]
            if request.get("prompt") == CONDENSATION_PROMPT:
                condensation_calls += 1
                phase = 2
                continue
            groups.setdefault(f"primary:turn-{phase}", []).append((request, terminal))
        expected = ["primary:turn-1", "primary:turn-2"]
        if condensation_calls != 1:
            return [], all_outputs, False, f"CONDENSATION_CALL_CARDINALITY_{condensation_calls}"
    else:
        for request in ordered:
            role = str(request.get("interactionRole") or "")
            if not role:
                return [], all_outputs, False, "MODEL_CALL_MISSING_INTERACTION_ROLE"
            groups.setdefault(role, []).append((request, terminals[str(request["requestId"])]))
        expected = list(submitted_roles)
    if len(set(expected)) != len(expected) or set(groups) != set(expected):
        return [], all_outputs, False, f"INTERACTION_SET_MISMATCH:expected={sorted(expected)} observed={sorted(groups)}"
    answers: list[str] = []
    modes: list[str] = []
    for interaction in expected:
        finishes = [
            str(terminal.get("nativeToolArguments", {}).get("message") or "").strip()
            for _, terminal in groups[interaction]
            if terminal.get("nativeToolName") == "finish"
        ]
        finishes = [value for value in finishes if value]
        if len(finishes) > 1:
            return [], all_outputs, False, f"MULTIPLE_FINISH_RESPONSES:{interaction}"
        if finishes:
            answers.append(finishes[0])
            modes.append(f"{interaction}=NATIVE_FINISH_TOOL")
            continue
        plain = [
            str(terminal.get("content") or "").strip()
            for _, terminal in groups[interaction]
            if terminal.get("nativeToolName") is None
            and terminal.get("finishReason") == "stop"
            and str(terminal.get("content") or "").strip()
        ]
        if not plain:
            return [], all_outputs, False, f"NO_COMPLETED_RESPONSE:{interaction}"
        answers.append(plain[-1])
        modes.append(f"{interaction}=LAST_COMPLETED_RESPONSE")
    return answers, all_outputs, True, ",".join(modes)


def model_behavior(
    case_id: str,
    answers: list[str],
    all_outputs: list[str],
    selection_ok: bool,
    selection_detail: str,
) -> tuple[bool, str]:
    from e1_candidate6.runner import model_behavior as candidate6_model_behavior

    return candidate6_model_behavior(case_id, answers, all_outputs, selection_ok, selection_detail)


def verdict_vector(result: Any) -> dict[str, Any]:
    observation = result.observation
    serialized = __import__("dataclasses").asdict(observation)
    outcomes = {row.oracle_id: bool(row.passed) for row in result.scientific}
    overrides = interaction_oracle_overrides(serialized)
    for oracle_id, (passed, _) in overrides.items():
        outcomes[oracle_id] = passed
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
        outcomes["E1-015-BOUNDARY"] = all(SYNTHETIC_SECRET not in str(hook.get("additional_context") or "") for hook in observation.hooks)
    sma_ids = SMA_ORACLES[case_id]
    sma = all(outcomes.get(oracle_id, False) for oracle_id in sma_ids)
    replaced = {"S2-015-P1", "S2-015-P2"} if case_id.endswith("SECRET-DELIVERY-ABSENCE") else set()
    boundary_ids = [row.oracle_id for row in result.scientific if row.oracle_id not in set(sma_ids) | replaced]
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
