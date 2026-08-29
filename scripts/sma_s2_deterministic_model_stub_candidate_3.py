#!/usr/bin/env python3
"""Candidate-4 deterministic stub fixture.

This successor preserves candidate 2 byte-for-byte behavior for SUCCESS and
fault modes. It adds two non-generative tool-call fixtures so the accepted
delegation and feedback-loop predicates have producible OpenHands events.
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
import signal
import threading
import time
import uuid
from http import HTTPStatus
from http.server import ThreadingHTTPServer

ThreadingHTTPServer.allow_reuse_address = True
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
BASE_PATH = ROOT / "scripts/sma_s2_deterministic_model_stub_candidate_2.py"
SPECIAL_MODES = frozenset({"DELEGATION_TOOL", "INELIGIBLE_TOOL_TRAFFIC"})


def load_base():
    spec = importlib.util.spec_from_file_location("sma_s2_stub_candidate_2", BASE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load deterministic stub candidate 2")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


base = load_base()


class Candidate3Handler(base.DeterministicStubHandler):
    server_version = "SMA-S2-Deterministic-Stub-Candidate-3"

    def do_POST(self) -> None:
        if self.path != "/v1/chat/completions":
            super().do_POST()
            return
        try:
            body_length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            super().do_POST()
            return
        preview = self.rfile.peek(body_length)[:body_length]
        match = base.CONTROL_PATTERN.search(preview)
        tag_mode = match.group(3).decode("ascii") if match and match.group(3) else None
        mode = self.headers.get("X-SMA-S2-Mode", tag_mode or "SUCCESS").upper()
        if mode not in SPECIAL_MODES:
            super().do_POST()
            return

        body = self.rfile.read(body_length)
        request_id = str(uuid.uuid4())
        received_ns = time.monotonic_ns()
        match = base.CONTROL_PATTERN.search(body)
        case_id = match.group(1).decode("ascii") if match else "UNBOUND_CASE"
        repetition = int(match.group(2)) if match else -1
        raw_record = {
            "recordType": "SMA_S2_STUB_RAW_REQUEST",
            "requestId": request_id,
            "caseId": case_id,
            "repetition": repetition,
            "mode": mode,
            "receivedMonotonicNs": received_ns,
            "requestBodyLength": len(body),
            "requestBodySha256": base.sha256_bytes(body),
            "requestBodyBase64": base.base64.b64encode(body).decode("ascii"),
        }
        self.state.raw.append(raw_record)
        self.state.operational.append({key: value for key, value in raw_record.items()
                                       if key != "requestBodyBase64"})

        if mode == "DELEGATION_TOOL":
            child_task = (
                f"[SMA-S2-STUB case={case_id} repetition={repetition} mode=SUCCESS] "
                "Record the current child conversation identity."
            )
        else:
            child_task = "Deterministic ineligible-event fixture; do not launch a child."
        arguments = base.canonical_json_bytes({
            "task": child_task,
            "target": "local",
            "isolation": "shared",
        }).decode("utf-8")
        response = base.canonical_json_bytes({
            "id": f"sma-s2-tool-{case_id}-{repetition}",
            "object": "chat.completion",
            "created": 0,
            "model": "sma-s2-deterministic-stub-candidate-3",
            "choices": [{
                "index": 0,
                "message": {
                    "role": "assistant",
                    "content": None,
                    "reasoning_content": "deterministic fixture reasoning",
                    "tool_calls": [{
                        "id": f"sma-s2-tool-call-{case_id}-{repetition}",
                        "type": "function",
                        "function": {
                            "name": "launch_child_conversation",
                            "arguments": arguments,
                        },
                    }],
                },
                "finish_reason": "tool_calls",
            }],
            "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
        })
        outcome = self._send_bytes(HTTPStatus.OK, response, "application/json")
        self._record_terminal(
            request_id=request_id,
            case_id=case_id,
            repetition=repetition,
            mode=mode,
            http_status=HTTPStatus.OK.value,
            response_body=response,
            outcome=outcome,
        )


def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser()
    value.add_argument("--host", default="127.0.0.1")
    value.add_argument("--port", type=int, default=0)
    value.add_argument("--raw-journal", type=Path, required=True)
    value.add_argument("--operational-journal", type=Path, required=True)
    value.add_argument("--terminal-journal", type=Path, required=True)
    value.add_argument("--timeout-delay-ms", type=int, default=2000)
    value.add_argument("--ready-file", type=Path)
    return value


def main() -> int:
    args = parser().parse_args()
    state = base.StubState(args.raw_journal, args.operational_journal,
                           args.terminal_journal, args.timeout_delay_ms)
    server = ThreadingHTTPServer((args.host, args.port), Candidate3Handler)
    server.stub_state = state
    stop_once = threading.Event()

    def stop_server(_signum: int, _frame: object) -> None:
        if not stop_once.is_set():
            stop_once.set()
            threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, stop_server)
    signal.signal(signal.SIGINT, stop_server)
    host, port = server.server_address
    ready = base.canonical_json_bytes({
        "recordType": "SMA_S2_STUB_READY",
        "host": host,
        "port": port,
        "pid": os.getpid(),
        "fixtureModes": sorted(SPECIAL_MODES),
    })
    if args.ready_file is not None:
        args.ready_file.parent.mkdir(parents=True, exist_ok=True)
        descriptor = os.open(args.ready_file, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
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
