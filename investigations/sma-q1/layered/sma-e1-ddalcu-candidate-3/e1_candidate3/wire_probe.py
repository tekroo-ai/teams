#!/usr/bin/env python3
"""Capture the exact OpenHands/LiteLLM request shape against a loopback fake."""
from __future__ import annotations

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import threading
from typing import Any

from openhands.sdk.llm import LLM, Message, TextContent
from openhands.sdk.tool import FinishTool


MODEL = "ddalcu--Qwen3.8-27B-MLX-Serve-8bit"
OPERATION = "SMA-E1-CANDIDATE-3-WIRE-PROBE#001"
PROMPT = "Reply with exactly WIRE_PROBE_OK."


class CaptureHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    captured: dict[str, Any] | None = None

    def log_message(self, *_: object) -> None:
        return

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        type(self).captured = {
            "method": self.command,
            "path": self.path,
            "operationHeader": self.headers.get("X-SMA-E1-Operation"),
            "body": json.loads(body),
        }
        response = json.dumps({
            "id": "chatcmpl-e1-wire-probe",
            "object": "chat.completion",
            "created": 1,
            "model": MODEL,
            "choices": [{
                "index": 0,
                "finish_reason": "stop",
                "message": {
                    "role": "assistant",
                    "content": (
                        "<function=finish>\n"
                        "<parameter=message>WIRE_PROBE_OK</parameter>\n"
                        "<parameter=summary>wire probe complete</parameter>\n"
                        "</function>"
                    ),
                },
            }],
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
        }, separators=(",", ":")).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(response)))
        self.end_headers()
        self.wfile.write(response)


def main() -> int:
    server = ThreadingHTTPServer(("127.0.0.1", 0), CaptureHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        llm = LLM(
            model=f"openai/{MODEL}",
            model_canonical_name="openai/gpt-4o",
            api_key="loopback-wire-probe",
            base_url=f"http://127.0.0.1:{server.server_address[1]}/v1",
            api_mode="chat",
            native_tool_calling=False,
            force_string_serializer=False,
            stream=False,
            temperature=0,
            top_k=20,
            top_p=0.95,
            max_output_tokens=128,
            num_retries=0,
            retry_multiplier=0,
            retry_min_wait=0,
            retry_max_wait=0,
            timeout=120,
            log_completions=False,
            litellm_extra_body={"chat_template_kwargs": {"enable_thinking": False}},
            extra_headers={"X-SMA-E1-Operation": OPERATION},
            capability_overrides={
                "supports_prompt_cache": False,
                "supports_prompt_cache_retention": False,
                "supports_reasoning_effort": False,
                "supports_responses_api": False,
                "supports_sampling_params": True,
                "supports_stop_words": True,
                "supports_vision": True,
                "thinking_mode": "unknown",
            },
        )
        llm.completion(
            [Message(role="user", content=[TextContent(text=PROMPT)])],
            tools=FinishTool.create(),
        )
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)

    captured = CaptureHandler.captured
    if captured is None:
        raise AssertionError("OpenHands wire probe captured no request")
    payload = captured["body"]
    users = [message for message in payload.get("messages", []) if message.get("role") == "user"]
    if len(users) != 1:
        raise AssertionError("OpenHands wire probe user-message cardinality drift")
    content = users[0].get("content")
    segments = [content] if isinstance(content, str) else [item.get("text", "") for item in content if item.get("type") == "text"]
    wire_text = "".join(segments)
    checks = {
        "method": captured["method"] == "POST",
        "path": captured["path"] == "/v1/chat/completions",
        "operationHeader": captured["operationHeader"] == OPERATION,
        "model": payload.get("model") == MODEL,
        "nonStreaming": payload.get("stream", False) is False,
        "streamFieldOmitted": "stream" not in payload,
        "temperature": payload.get("temperature") in {0, 0.0},
        "outputBound": payload.get("max_tokens", payload.get("max_completion_tokens")) == 128,
        "thinkingDisabled": (payload.get("chat_template_kwargs") or {}).get("enable_thinking") is False,
        "nativeToolsAbsent": payload.get("tools") in (None, []),
        "wrapperStart": wire_text.startswith("Here's a running example of how to perform a task with the provided tools."),
        "taskExactInsideWrapper": (
            "--------------------- NEW TASK DESCRIPTION ---------------------\n"
            + PROMPT
            + "\n--------------------- END OF NEW TASK DESCRIPTION ---------------------"
        ) in wire_text,
    }
    if not all(checks.values()):
        raise AssertionError(f"OpenHands wire probe failed: {checks}")
    print(json.dumps({
        "status": "PASS_EXACT_OPENHANDS_LOOPBACK_WIRE_PROBE",
        "checks": checks,
        "requestKeys": sorted(payload),
        "messageCount": len(payload.get("messages", [])),
        "syntheticLoopbackTransportCalls": 1,
        "realModelCalls": 0,
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
