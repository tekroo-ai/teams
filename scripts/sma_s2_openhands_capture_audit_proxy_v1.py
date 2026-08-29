#!/usr/bin/env python3
"""Loopback-only transparent OpenHands proxy with append-only capture-cycle receipts."""

from __future__ import annotations

import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import hashlib
import json
import os
from pathlib import Path
import threading
import time
import urllib.error
import urllib.request


LOCK = threading.Lock()


def canonical(value: object) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode("utf-8")


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _forward(self) -> None:
        started = time.monotonic_ns()
        length = int(self.headers.get("Content-Length", "0"))
        request_body = self.rfile.read(length) if length else b""
        headers = {
            key: value for key, value in self.headers.items()
            if key.lower() not in {"host", "content-length", "connection"}
        }
        request = urllib.request.Request(
            self.server.upstream + self.path,
            data=request_body if length else None,
            headers=headers,
            method=self.command,
        )
        try:
            with urllib.request.urlopen(request, timeout=15) as response:
                status = response.status
                response_body = response.read()
                content_type = response.headers.get("Content-Type", "application/json")
        except urllib.error.HTTPError as error:
            status = error.code
            response_body = error.read()
            content_type = error.headers.get("Content-Type", "application/json")

        event_ids: list[str] = []
        try:
            payload = json.loads(response_body)
            if isinstance(payload, dict) and isinstance(payload.get("items"), list):
                event_ids = sorted(
                    str(item["id"]) for item in payload["items"]
                    if isinstance(item, dict) and item.get("id") is not None
                )
        except (UnicodeDecodeError, json.JSONDecodeError):
            pass

        receipt = {
            "recordType": "SMA_S2_OPENHANDS_CAPTURE_AUDIT_PROXY_RECEIPT",
            "method": self.command,
            "path": self.path,
            "status": status,
            "requestBodySha256": hashlib.sha256(request_body).hexdigest(),
            "responseBodySha256": hashlib.sha256(response_body).hexdigest(),
            "responseBytes": len(response_body),
            "eventIds": event_ids,
            "startedNs": started,
            "completedNs": time.monotonic_ns(),
        }
        with LOCK:
            with self.server.journal.open("ab", buffering=0) as stream:
                stream.write(canonical(receipt) + b"\n")
                os.fsync(stream.fileno())

        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(response_body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(response_body)

    do_GET = _forward
    do_POST = _forward
    do_DELETE = _forward

    def log_message(self, _format: str, *_args: object) -> None:
        return


class Server(ThreadingHTTPServer):
    allow_reuse_address = True

    def __init__(self, address: tuple[str, int], upstream: str, journal: Path):
        super().__init__(address, Handler)
        self.upstream = upstream.rstrip("/")
        self.journal = journal


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", required=True)
    parser.add_argument("--port", required=True, type=int)
    parser.add_argument("--upstream", required=True)
    parser.add_argument("--journal", required=True)
    parser.add_argument("--ready-file", required=True)
    args = parser.parse_args()
    journal = Path(args.journal)
    ready = Path(args.ready_file)
    journal.parent.mkdir(parents=True, exist_ok=True)
    server = Server((args.host, args.port), args.upstream, journal)
    payload = canonical({
        "recordType": "SMA_S2_OPENHANDS_CAPTURE_AUDIT_PROXY_READY",
        "pid": os.getpid(),
        "host": args.host,
        "port": args.port,
        "upstream": args.upstream.rstrip("/"),
    })
    descriptor = os.open(ready, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(payload + b"\n")
        stream.flush()
        os.fsync(stream.fileno())
    server.serve_forever(poll_interval=0.05)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
