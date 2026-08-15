#!/usr/bin/env python3

from __future__ import annotations

import sys
import unittest
from pathlib import Path


sys.path.insert(0, str(Path(__file__).parent))
import openhands_response_lane_qualification_v1 as lane  # noqa: E402


class AgentFinalResponseTest(unittest.TestCase):
    def test_extracts_finish_action_message(self) -> None:
        event = {
            "id": "finish-event",
            "kind": "ActionEvent",
            "source": "agent",
            "tool_name": "finish",
            "action": {"kind": "FinishAction", "message": "OPENHANDS_LANE_OK"},
        }

        self.assertEqual(
            lane.agent_final_response([event]),
            (event, "OPENHANDS_LANE_OK", "FINISH_ACTION"),
        )

    def test_extracts_agent_message_text(self) -> None:
        event = {
            "id": "message-event",
            "kind": "MessageEvent",
            "source": "agent",
            "llm_message": {
                "content": [{"type": "text", "text": "OPENHANDS_LANE_OK"}]
            },
        }

        self.assertEqual(
            lane.agent_final_response([event]),
            (event, "OPENHANDS_LANE_OK", "AGENT_MESSAGE"),
        )

    def test_latest_terminal_shape_wins(self) -> None:
        message = {
            "kind": "MessageEvent",
            "source": "agent",
            "llm_message": {"content": [{"type": "text", "text": "old"}]},
        }
        finish = {
            "kind": "ActionEvent",
            "source": "agent",
            "tool_name": "finish",
            "action": {"kind": "FinishAction", "message": "new"},
        }

        self.assertEqual(lane.agent_final_response([message, finish]), (finish, "new", "FINISH_ACTION"))

    def test_malformed_finish_does_not_fall_back_to_stale_message(self) -> None:
        message = {
            "kind": "MessageEvent",
            "source": "agent",
            "llm_message": {"content": [{"type": "text", "text": "stale"}]},
        }
        malformed = {
            "kind": "ActionEvent",
            "source": "agent",
            "tool_name": "finish",
            "action": {"kind": "FinishAction"},
        }

        self.assertEqual(
            lane.agent_final_response([message, malformed]),
            (malformed, "", "MALFORMED_FINISH_ACTION"),
        )


if __name__ == "__main__":
    unittest.main()
