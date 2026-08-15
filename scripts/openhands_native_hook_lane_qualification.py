#!/usr/bin/env python3
"""Qualify the fresh-process OpenHands native hook probe envelope.

The semantic hook deadline remains the configured one second. The separate
process envelope covers interpreter startup and OpenHands SDK imports and is
bounded at thirty seconds. No SMA service, model, MongoDB, or Qdrant is used.
"""

from __future__ import annotations

import datetime as dt
import hashlib
import json
import os
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any


TEAMS = Path("/Users/paul/work/tekroo-ai/teams")
SMA = Path("/Users/paul/work/tekroo-ai/sma-step15")
PYTHON = Path("/Users/paul/.cache/uv/archive-v0/sx6_8DYmdKseZALr/bin/python")
PROBE = SMA / ".openhands/hooks/native_hook_probe.py"
HOOK = SMA / ".openhands/hooks/sma_context_hook.py"
CONFIG = SMA / ".openhands/hooks.json"
OUTPUT = TEAMS / "OUTPUT/phase-3/openhands-native-hook-lane-v1"
JOURNAL = OUTPUT / "raw-receipts.jsonl"
SUMMARY = OUTPUT / "execution-receipt.json"
ATTEMPTS = 128
PROCESS_TIMEOUT_SECONDS = 30
HOOK_DEADLINE_MS = 1000


def now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat()


def sha_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def sha_text(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        request = json.loads(raw)
        body = json.dumps({
            "hit_count": 0,
            "context_block": "",
            "trace_id": "ctx_native_hook_qualification",
            "request_sha256": sha_text(json.dumps(request, sort_keys=True)),
        }, separators=(",", ":")).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, _format: str, *args: Any) -> None:
        return


def append(record: dict[str, Any]) -> None:
    with JOURNAL.open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n")
        stream.flush()
        os.fsync(stream.fileno())


def run_attempt(ordinal: int, bridge_url: str) -> dict[str, Any]:
    environment = os.environ.copy()
    environment["SMA_BRIDGE_URL"] = bridge_url
    environment["OPENHANDS_SUPPRESS_BANNER"] = "1"
    started = time.monotonic_ns()
    args = [
        str(PYTHON), str(PROBE), str(SMA), f"qualification-{ordinal}",
        str(SMA), "native-hook-qualification-prompt",
    ]
    try:
        completed = subprocess.run(
            args, cwd=SMA, env=environment, capture_output=True, text=True,
            timeout=PROCESS_TIMEOUT_SECONDS, check=False,
        )
    except subprocess.TimeoutExpired as error:
        elapsed = (time.monotonic_ns() - started) / 1_000_000.0
        return {
            "kind": "ATTEMPT",
            "ordinal": ordinal,
            "status": "FAIL",
            "terminal": "PROCESS_TIMEOUT",
            "process_elapsed_ms": elapsed,
            "stdout_length": len(error.stdout or b""),
            "stderr_length": len(error.stderr or b""),
        }
    elapsed = (time.monotonic_ns() - started) / 1_000_000.0
    try:
        payload = json.loads(completed.stdout)
    except json.JSONDecodeError:
        payload = {}
    passed = (
        completed.returncode == 0
        and not completed.stderr
        and payload.get("hook_success") is True
        and payload.get("hook_exit_code") == 0
        and payload.get("hook_event_count") == 1
        and payload.get("message_count") == 1
        and payload.get("original_prompt") == "native-hook-qualification-prompt"
        and payload.get("configured_hook_timeout_seconds") == 1
        and float(payload.get("native_hook_elapsed_ms", float("inf"))) <= HOOK_DEADLINE_MS
    )
    return {
        "kind": "ATTEMPT",
        "ordinal": ordinal,
        "status": "PASS" if passed else "FAIL",
        "terminal": "COMPLETED",
        "process_elapsed_ms": elapsed,
        "native_hook_elapsed_ms": payload.get("native_hook_elapsed_ms"),
        "hook_success": payload.get("hook_success"),
        "hook_exit_code": payload.get("hook_exit_code"),
        "hook_event_count": payload.get("hook_event_count"),
        "message_count": payload.get("message_count"),
        "prompt_unchanged": payload.get("original_prompt") == "native-hook-qualification-prompt",
        "configured_hook_timeout_seconds": payload.get("configured_hook_timeout_seconds"),
        "stdout_length": len(completed.stdout),
        "stdout_sha256": sha_text(completed.stdout),
        "stderr_length": len(completed.stderr),
        "stderr_sha256": sha_text(completed.stderr),
        "returncode": completed.returncode,
    }


def main() -> int:
    OUTPUT.mkdir(parents=True, exist_ok=False)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    bridge_url = f"http://127.0.0.1:{server.server_port}"
    append({
        "kind": "PREFLIGHT",
        "recorded_at": now(),
        "attempts": ATTEMPTS,
        "process_timeout_seconds": PROCESS_TIMEOUT_SECONDS,
        "hook_deadline_ms": HOOK_DEADLINE_MS,
        "probe_sha256": sha_file(PROBE),
        "hook_sha256": sha_file(HOOK),
        "config_sha256": sha_file(CONFIG),
        "sma_service_used": False,
        "model_used": False,
    })
    results: list[dict[str, Any]] = []
    try:
        for ordinal in range(1, ATTEMPTS + 1):
            result = run_attempt(ordinal, bridge_url)
            result["recorded_at"] = now()
            results.append(result)
            append(result)
            if result["status"] != "PASS":
                break
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)
    passed = len(results) == ATTEMPTS and all(item["status"] == "PASS" for item in results)
    process_times = [float(item["process_elapsed_ms"]) for item in results]
    hook_times = [float(item["native_hook_elapsed_ms"]) for item in results
                  if item.get("native_hook_elapsed_ms") is not None]
    summary = {
        "schema_version": "1.0.0",
        "receipt_type": "OPENHANDS_NATIVE_HOOK_LANE_QUALIFICATION",
        "recorded_at": now(),
        "status": "PASS" if passed else "FAIL",
        "required_attempts": ATTEMPTS,
        "completed_attempts": len(results),
        "passed_attempts": sum(item["status"] == "PASS" for item in results),
        "failed_attempts": sum(item["status"] != "PASS" for item in results),
        "process_timeout_seconds": PROCESS_TIMEOUT_SECONDS,
        "semantic_hook_deadline_ms": HOOK_DEADLINE_MS,
        "process_elapsed_ms": {
            "minimum": min(process_times),
            "maximum": max(process_times),
        },
        "native_hook_elapsed_ms": {
            "minimum": min(hook_times) if hook_times else None,
            "maximum": max(hook_times) if hook_times else None,
        },
        "raw_journal_sha256": sha_file(JOURNAL),
        "sma_q1_claim": "NONE",
    }
    SUMMARY.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n")
    print(json.dumps(summary, indent=2, sort_keys=True))
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
