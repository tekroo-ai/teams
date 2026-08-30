from __future__ import annotations

from typing import Any, Mapping

from e1_native_v6.runner import NativeToolRunnerV6
from t1.model import FailureClass, HarnessFailure, OperationObservation, RawHttpRequest, require


class NativeToolRunnerV9(NativeToolRunnerV6):
    """Retain the raw completion timestamp for the second scenario-18 turn."""

    def _poll_new_turn_terminal(
        self,
        observation: OperationObservation,
        conversation: Mapping[str, Any],
        previous_user_message_id: str,
    ) -> None:
        terminal = {"finished", "error", "paused", "stuck", "stopped"}
        deadline = self.ports.now_ns() + self.config.operation_deadline_ms * 1_000_000
        stable: tuple[str, str, str] | None = None
        stable_reads = 0
        while self.ports.now_ns() < deadline:
            request = RawHttpRequest(
                "GET",
                f"/api/conversations/{conversation['id']}",
                {"X-Session-API-Key": self.config.session_header_value},
                b"",
                10_000,
            )
            receipt = self._http(observation, "OPENHANDS", request)
            require(
                receipt.status == 200 and receipt.error_kind is None,
                FailureClass.ENVIRONMENT,
                "conversation terminal retrieval failed",
            )
            current = receipt.json_body()
            status = str(current.get("execution_status") or "")
            user_message_id = str(current.get("last_user_message_id") or "")
            leaf_event_id = str(current.get("leaf_event_id") or "")
            candidate = (status, user_message_id, leaf_event_id)
            if user_message_id and user_message_id != previous_user_message_id and status in terminal:
                stable_reads = stable_reads + 1 if candidate == stable else 1
                stable = candidate
                if stable_reads >= 2:
                    require(status == "finished", FailureClass.SCIENTIFIC, f"conversation terminated as {status}")
                    conversation.update(current)
                    observation.timings.append(
                        {
                            "kind": "conversation_terminal",
                            "atNs": receipt.completed_ns,
                            "conversationId": conversation["id"],
                            "status": status,
                            "durationMs": 0,
                            "deadlineMs": self.config.operation_deadline_ms,
                            "lastUserMessageId": user_message_id,
                            "leafEventId": leaf_event_id,
                        }
                    )
                    return
            else:
                stable = None
                stable_reads = 0
            self.ports.sleep_ms(50)
        raise HarnessFailure(FailureClass.SCIENTIFIC, "new conversation turn did not reach a stable terminal state")
