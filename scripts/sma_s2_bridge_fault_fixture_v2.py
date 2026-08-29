#!/usr/bin/env python3
"""Loopback-only deterministic SMA bridge fault fixture for final-P2 S2."""

from __future__ import annotations

import argparse
import hashlib
import json
import signal
import threading
import time
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ThreadingHTTPServer.allow_reuse_address = True


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, format: str, *args: object) -> None:
        return

    def do_GET(self) -> None:
        if self.path == "/healthz": self.send_payload(HTTPStatus.OK, b'{"status":"ok"}')
        else: self.send_payload(HTTPStatus.NOT_FOUND, b"{}")

    def do_POST(self) -> None:
        started = time.monotonic_ns()
        length = int(self.headers.get("Content-Length", "0")); request_body = self.rfile.read(length)
        if self.path != "/v1/openhands/context":
            self.send_payload(HTTPStatus.NOT_FOUND, b"{}"); return
        mode = self.server.fault_mode  # type: ignore[attr-defined]
        if mode == "timeout": time.sleep(self.server.delay_ms / 1000)  # type: ignore[attr-defined]
        if mode == "malformed": status, response_body = HTTPStatus.OK, b"{"
        elif mode == "non2xx": status, response_body = HTTPStatus.SERVICE_UNAVAILABLE, b'{"error":"synthetic"}'
        else: status, response_body = HTTPStatus.GATEWAY_TIMEOUT, b'{"error":"deadline"}'
        outcome = self.send_payload(status, response_body)
        record = {"recordType": "SMA_S2_V2_BRIDGE_FAULT_RECEIPT", "mode": mode,
                  "requestBodySha256": hashlib.sha256(request_body).hexdigest(), "requestBodyLength": len(request_body),
                  "responseStatus": status.value, "responseBodySha256": hashlib.sha256(response_body).hexdigest(),
                  "responseBodyLength": len(response_body), "responseWriteOutcome": outcome,
                  "startedMonotonicNs": started, "finishedMonotonicNs": time.monotonic_ns()}
        with self.server.journal_lock:  # type: ignore[attr-defined]
            with open(self.server.journal, "ab") as stream:  # type: ignore[attr-defined]
                stream.write(json.dumps(record, sort_keys=True, separators=(",", ":")).encode() + b"\n")
                stream.flush()

    def send_payload(self, status: HTTPStatus, body: bytes) -> str:
        try:
            self.send_response(status.value); self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body))); self.send_header("Connection", "close")
            self.end_headers(); self.wfile.write(body); self.wfile.flush(); return "CLIENT_RECEIVED"
        except (BrokenPipeError, ConnectionResetError):
            return "CLIENT_DISCONNECTED"


def main() -> int:
    parser = argparse.ArgumentParser(); parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8130); parser.add_argument("--mode", choices=["timeout", "malformed", "non2xx"], required=True)
    parser.add_argument("--delay-ms", type=int, default=2000); parser.add_argument("--journal", required=True); args = parser.parse_args()
    server = ThreadingHTTPServer((args.host, args.port), Handler); server.fault_mode = args.mode; server.delay_ms = args.delay_ms
    server.journal = args.journal; server.journal_lock = threading.Lock()
    stop = threading.Event()
    def shutdown(_signal: int, _frame: object) -> None:
        if not stop.is_set(): stop.set(); threading.Thread(target=server.shutdown, daemon=True).start()
    signal.signal(signal.SIGTERM, shutdown); signal.signal(signal.SIGINT, shutdown)
    print(json.dumps({"status": "ready", "host": args.host, "port": args.port, "mode": args.mode}, sort_keys=True), flush=True)
    try: server.serve_forever(poll_interval=0.05)
    finally: server.server_close()
    return 0


if __name__ == "__main__": raise SystemExit(main())
