#!/usr/bin/env python3
"""Emit deterministic OpenHands event fixtures using the frozen product classes.

This is an extractor, not a schema emulator.  Every positive event is constructed,
serialized, and revalidated by the bound OpenHands SDK implementation.
"""

from __future__ import annotations

import argparse
import copy
import json
from pathlib import Path
from typing import Any

from openhands.sdk.event import (
    ACPToolCallEvent,
    ActionEvent,
    AgentErrorEvent,
    AgentResponseFinality,
    AuthorshipOrigin,
    Condensation,
    CondensationRequest,
    ConversationStateUpdateEvent,
    MessageEvent,
    ObservationEvent,
    SemanticPurpose,
    TokenEvent,
)
from openhands.sdk.llm import Message, MessageToolCall, TextContent
from openhands.sdk.tool import Action, Observation
from openhands.agent_server.models import EventPage
from openhands.sdk.conversation.request import SendMessageRequest, StartConversationRequest


FIXED_TIMESTAMP = "2026-08-28T12:00:00+00:00"


class ProductTruthAction(Action):
    """Minimal action payload carried by the real ActionEvent class."""

    command: str = "printf product-truth"

    def execute(self) -> "ProductTruthObservation":
        return ProductTruthObservation(
            content=[TextContent(text="product-truth observation")]
        )


class ProductTruthObservation(Observation):
    """Minimal observation payload carried by the real ObservationEvent class."""


def _message_event(
    *,
    event_id: str,
    source: str,
    role: str,
    text: str,
    origin: AuthorshipOrigin,
    purpose: SemanticPurpose,
    finality: AgentResponseFinality,
) -> MessageEvent:
    return MessageEvent(
        id=event_id,
        timestamp=FIXED_TIMESTAMP,
        source=source,
        authorship_origin=origin,
        semantic_purpose=purpose,
        agent_response_finality=finality,
        llm_message=Message(
            role=role,
            content=[TextContent(text=text)],
        ),
    )


def build_events() -> list[tuple[str, Any]]:
    user = _message_event(
        event_id="evt-user-task",
        source="user",
        role="user",
        text="Implement the product-bound fixture.",
        origin=AuthorshipOrigin.CONVERSATION_INPUT,
        purpose=SemanticPurpose.TASK_INPUT,
        finality=AgentResponseFinality.NOT_APPLICABLE,
    )
    delegated = _message_event(
        event_id="evt-delegated-task",
        source="user",
        role="user",
        text="Inspect the child conversation identity.",
        origin=AuthorshipOrigin.DELEGATED_AGENT,
        purpose=SemanticPurpose.TASK_INPUT,
        finality=AgentResponseFinality.NOT_APPLICABLE,
    )
    final_agent = _message_event(
        event_id="evt-agent-final",
        source="agent",
        role="assistant",
        text="The bounded task is complete.",
        origin=AuthorshipOrigin.AGENT_MODEL,
        purpose=SemanticPurpose.AGENT_RESPONSE,
        finality=AgentResponseFinality.FINAL,
    )
    intermediate_agent = _message_event(
        event_id="evt-agent-intermediate",
        source="agent",
        role="assistant",
        text="I am still working.",
        origin=AuthorshipOrigin.AGENT_MODEL,
        purpose=SemanticPurpose.AGENT_RESPONSE,
        finality=AgentResponseFinality.INTERMEDIATE,
    )
    framework_feedback = _message_event(
        event_id="evt-framework-feedback",
        source="user",
        role="user",
        text="Framework feedback must not become memory.",
        origin=AuthorshipOrigin.FRAMEWORK,
        purpose=SemanticPurpose.CONTROL_FEEDBACK,
        finality=AgentResponseFinality.NOT_APPLICABLE,
    )
    unknown_message = _message_event(
        event_id="evt-unknown-message",
        source="user",
        role="user",
        text="Legacy unknown metadata must fail closed.",
        origin=AuthorshipOrigin.UNKNOWN,
        purpose=SemanticPurpose.UNKNOWN,
        finality=AgentResponseFinality.UNKNOWN,
    )

    error = AgentErrorEvent(
        id="evt-error",
        timestamp=FIXED_TIMESTAMP,
        tool_name="terminal",
        tool_call_id="call-error",
        error="synthetic product-truth error",
    )
    state_update = ConversationStateUpdateEvent(
        id="evt-state",
        timestamp=FIXED_TIMESTAMP,
        key="execution_status",
        value="finished",
    )
    tool_call = MessageToolCall(
        id="call-product-truth",
        name="terminal",
        arguments='{"command":"printf product-truth"}',
        origin="completion",
    )
    action = ActionEvent(
        id="evt-action",
        timestamp=FIXED_TIMESTAMP,
        thought=[TextContent(text="Run the deterministic probe.")],
        action=ProductTruthAction(),
        tool_name="terminal",
        tool_call_id=tool_call.id,
        tool_call=tool_call,
        llm_response_id="response-product-truth",
    )
    observation = ObservationEvent(
        id="evt-observation",
        timestamp=FIXED_TIMESTAMP,
        observation=ProductTruthObservation(
            content=[TextContent(text="product-truth observation")]
        ),
        action_id=action.id,
        tool_name="terminal",
        tool_call_id=tool_call.id,
    )
    tool = ACPToolCallEvent(
        id="evt-tool",
        timestamp=FIXED_TIMESTAMP,
        tool_call_id="acp-tool-product-truth",
        title="Read product source",
        status="completed",
        tool_kind="read",
        raw_input={"path": "/tmp/sma-s2-product-truth/source.txt"},
        raw_output={"bytes": 42},
    )
    utility = TokenEvent(
        id="evt-utility",
        timestamp=FIXED_TIMESTAMP,
        source="environment",
        prompt_token_ids=[1, 2, 3],
        response_token_ids=[4, 5],
    )
    condensation_request = CondensationRequest(
        id="evt-condensation-request",
        timestamp=FIXED_TIMESTAMP,
    )
    condensation = Condensation(
        id="evt-condensation",
        timestamp=FIXED_TIMESTAMP,
        forgotten_event_ids={user.id},
        summary="The user requested a product-bound fixture.",
        summary_offset=0,
        llm_response_id="response-condensation",
    )

    return [
        ("eligible_user_task", user),
        ("eligible_delegated_task", delegated),
        ("eligible_final_agent_response", final_agent),
        ("ineligible_intermediate_agent_response", intermediate_agent),
        ("ineligible_framework_feedback", framework_feedback),
        ("ineligible_unknown_message", unknown_message),
        ("ineligible_error", error),
        ("ineligible_state_update", state_update),
        ("ineligible_action", action),
        ("ineligible_tool", tool),
        ("ineligible_observation", observation),
        ("ineligible_utility", utility),
        ("ineligible_condensation_request", condensation_request),
        ("ineligible_condensation", condensation),
        ("ineligible_condensation_summary", condensation.summary_event),
    ]


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()

    records: list[dict[str, Any]] = []
    for label, event in build_events():
        # The REST surface serializes events through EventPage's abstract Event
        # union; that exact product path materializes the persisted ``kind`` tag.
        page = EventPage(items=[event])
        initial_payload = page.model_dump(mode="json", by_alias=True)
        # Validation deliberately pops the discriminator from its input dict;
        # copy the input so the retained wire fixture is never mutated in place.
        normalized_page = EventPage.model_validate(copy.deepcopy(initial_payload))
        page_payload = normalized_page.model_dump(mode="json", by_alias=True)
        serialized = copy.deepcopy(page_payload["items"][0])
        revalidated_page = EventPage.model_validate(copy.deepcopy(page_payload))
        if (
            revalidated_page.model_dump(mode="json", by_alias=True)["items"][0]
            != serialized
        ):
            raise RuntimeError(f"round-trip drift for {label}")
        records.append(
            {
                "label": label,
                "class": f"{event.__class__.__module__}.{event.__class__.__qualname__}",
                "event": serialized,
            }
        )

    payload = {
        "schemaVersion": "1.0.0-dev",
        "recordType": "OPENHANDS_PRODUCT_TRUTH_FIXTURES",
        "authoritativePositiveSource": "BOUND_OPENHANDS_PYDANTIC_CLASSES",
        "eventCount": len(records),
        "events": records,
        "requestExample": SendMessageRequest(
            role="user",
            run=True,
            content=[TextContent(text="Implement the product-bound fixture.")],
        ).model_dump(mode="json", by_alias=True),
        "schemas": {
            "SendMessageRequest": SendMessageRequest.model_json_schema(),
            "StartConversationRequest": StartConversationRequest.model_json_schema(),
            "EventPage": EventPage.model_json_schema(),
            "MessageEvent": MessageEvent.model_json_schema(),
        },
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()
