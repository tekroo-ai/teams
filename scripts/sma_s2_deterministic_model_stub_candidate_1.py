#!/usr/bin/env python3
"""Sealed deterministic OpenAI-compatible model stub for SMA-S2.

This candidate performs no generative reasoning. It records the exact synthetic
request body in an append-only raw evidence journal before emitting a frozen
success or fault response. Operational output contains identities, lengths,
digests, and status only; request bodies remain confined to the raw journal.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import re
import signal
import threading
import time
import uuid
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any


CONTROL_PATTERN = re.compile(
    rb"\[SMA-S2-STUB case=([A-Za-z0-9._-]+) repetition=([0-9]+)"
    rb"(?: mode=([A-Z0-9_-]+))?\]"
)
SUPPORTED_MODES = frozenset({"SUCCESS", "TIMEOUT", "MALFORMED", "HTTP_503"})


def canonical_json_bytes(value: Any) -> bytes:
    return json.dumps(
        value, ensure_ascii=False, sort_keys=True, separators=(",", ":")
    ).encode("utf-8")


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


class AppendOnlyJournal:
    def __init__(self, path: Path) -> None:
        self.path = path
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()

    def append(self, record: dict[str, Any]) -> None:
        encoded = canonical_json_bytes(record) + b"\n"
        with self._lock:
            descriptor = os.open(
                self.path,
                os.O_APPEND | os.O_CREAT | os.O_WRONLY,
                0o600,
            )
            try:
                os.write(descriptor, encoded)
                os.fsync(descriptor)
            finally:
                os.close(descriptor)


class StubState:
    def __init__(
        self,
        raw_journal: Path,
        operational_journal: Path,
        timeout_delay_ms: int,
    ) -> None:
        self.raw = AppendOnlyJournal(raw_journal)
        self.operational = AppendOnlyJournal(operational_journal)
        self.timeout_delay_ms = timeout_delay_ms


class DeterministicStubHandler(BaseHTTPRequestHandler):
    server_version = "SMA-S2-Deterministic-Stub-Candidate-1"
    protocol_version = "HTTP/1.1"

    @property
    def state(self) -> StubState:
        return self.server.stub_state  # type: ignore[attr-defined, no-any-return]

    def log_message(self, format: str, *args: object) -> None:
        return

    def _send_bytes(
        self,
        status: HTTPStatus,
        body: bytes,
        content_type: str,
    ) -> None:
        self.send_response(status.value)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)
        self.wfile.flush()

    def do_GET(self) -> None:
        if self.path == "/health":
            body = canonical_json_bytes(
                {"status": "ok", "service": self.server_version}
            )
            self._send_bytes(HTTPStatus.OK, body, "application/json")
            return
        self._send_bytes(HTTPStatus.NOT_FOUND, b"not found\n", "text/plain")

    def do_POST(self) -> None:
        if self.path != "/v1/chat/completions":
            self._send_bytes(HTTPStatus.NOT_FOUND, b"not found\n", "text/plain")
            return

        try:
            body_length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            self._send_bytes(
                HTTPStatus.BAD_REQUEST, b"invalid content length\n", "text/plain"
            )
            return
        body = self.rfile.read(body_length)
        request_id = str(uuid.uuid4())
        received_ns = time.monotonic_ns()
        match = CONTROL_PATTERN.search(body)
        case_id = match.group(1).decode("ascii") if match else "UNBOUND_CASE"
        repetition = int(match.group(2)) if match else -1
        tag_mode = match.group(3).decode("ascii") if match and match.group(3) else None
        mode = self.headers.get("X-SMA-S2-Mode", tag_mode or "SUCCESS").upper()
        if mode not in SUPPORTED_MODES:
            mode = "MALFORMED"

        raw_record = {
            "recordType": "SMA_S2_STUB_RAW_REQUEST",
            "requestId": request_id,
            "caseId": case_id,
            "repetition": repetition,
            "mode": mode,
            "receivedMonotonicNs": received_ns,
            "requestBodyLength": len(body),
            "requestBodySha256": sha256_bytes(body),
            "requestBodyBase64": base64.b64encode(body).decode("ascii"),
        }
        self.state.raw.append(raw_record)
        self.state.operational.append(
            {
                "recordType": "SMA_S2_STUB_OPERATION",
                "requestId": request_id,
                "caseId": case_id,
                "repetition": repetition,
                "mode": mode,
                "receivedMonotonicNs": received_ns,
                "requestBodyLength": len(body),
                "requestBodySha256": sha256_bytes(body),
            }
        )

        if mode == "TIMEOUT":
            time.sleep(self.state.timeout_delay_ms / 1000.0)
        if mode == "HTTP_503":
            response = canonical_json_bytes(
                {"error": {"message": "SMA-S2 deterministic HTTP 503"}}
            )
            self._send_bytes(
                HTTPStatus.SERVICE_UNAVAILABLE, response, "application/json"
            )
            return
        if mode == "MALFORMED":
            self._send_bytes(HTTPStatus.OK, b"{malformed", "application/json")
            return

        terminal = f"STUB_OK:{case_id}:{repetition}"
        response_id = f"sma-s2-{case_id}-{repetition}"
        try:
            request = json.loads(body)
        except json.JSONDecodeError:
            request = {}
        if request.get("stream") is True:
            chunks = [
                {
                    "id": response_id,
                    "object": "chat.completion.chunk",
                    "created": 0,
                    "model": "sma-s2-deterministic-stub-candidate-1",
                    "choices": [
                        {
                            "index": 0,
                            "delta": {"role": "assistant", "content": terminal},
                            "finish_reason": None,
                        }
                    ],
                },
                {
                    "id": response_id,
                    "object": "chat.completion.chunk",
                    "created": 0,
                    "model": "sma-s2-deterministic-stub-candidate-1",
                    "choices": [
                        {"index": 0, "delta": {}, "finish_reason": "stop"}
                    ],
                },
            ]
            payload = b"".join(
                b"data: " + canonical_json_bytes(chunk) + b"\n\n" for chunk in chunks
            ) + b"data: [DONE]\n\n"
            self._send_bytes(HTTPStatus.OK, payload, "text/event-stream")
            return

        response = canonical_json_bytes(
            {
                "id": response_id,
                "object": "chat.completion",
                "created": 0,
                "model": "sma-s2-deterministic-stub-candidate-1",
                "choices": [
                    {
                        "index": 0,
                        "message": {"role": "assistant", "content": terminal},
                        "finish_reason": "stop",
                    }
                ],
                "usage": {
                    "prompt_tokens": 0,
                    "completion_tokens": 0,
                    "total_tokens": 0,
                },
            }
        )
        self._send_bytes(HTTPStatus.OK, response, "application/json")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=0)
    parser.add_argument("--raw-journal", type=Path, required=True)
    parser.add_argument("--operational-journal", type=Path, required=True)
    parser.add_argument("--timeout-delay-ms", type=int, default=2000)
    parser.add_argument("--ready-file", type=Path)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    state = StubState(
        args.raw_journal,
        args.operational_journal,
        args.timeout_delay_ms,
    )
    server = ThreadingHTTPServer((args.host, args.port), DeterministicStubHandler)
    server.stub_state = state  # type: ignore[attr-defined]
    shutdown_once = threading.Event()

    def stop_server(signum: int, frame: object) -> None:
        del signum, frame
        if not shutdown_once.is_set():
            shutdown_once.set()
            threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, stop_server)
    signal.signal(signal.SIGINT, stop_server)
    host, port = server.server_address
    ready = canonical_json_bytes(
        {
            "recordType": "SMA_S2_STUB_READY",
            "host": host,
            "port": port,
            "pid": os.getpid(),
        }
    )
    if args.ready_file is not None:
        args.ready_file.parent.mkdir(parents=True, exist_ok=True)
        descriptor = os.open(
            args.ready_file, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600
        )
        try:
            os.write(descriptor, ready + b"\n")
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    print(ready.decode("utf-8"), flush=True)
    try:
        server.serve_forever(poll_interval=0.05)
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
