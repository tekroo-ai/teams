from __future__ import annotations

import argparse
import base64
from hashlib import sha256
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import threading
import time
import urllib.error
import urllib.request


def canonical(value: object) -> bytes:
    return json.dumps(
        value,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
    ).encode()


def append(path: Path, row: dict[str, object]) -> None:
    data = canonical(row) + b"\n"
    descriptor = os.open(path, os.O_WRONLY | os.O_APPEND)
    try:
        os.write(descriptor, data)
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def upstream_url(api_root: str, request_path: str) -> str:
    root = api_root.rstrip("/")
    if root.endswith("/v1") and request_path.startswith("/v1/"):
        return root[:-3] + request_path
    return root + request_path


def handler(upstream: str, request_journal: Path, response_journal: Path):
    class AuditHandler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_: object) -> None:
            return

        def do_GET(self) -> None:
            self._forward(b"")

        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            self._forward(self.rfile.read(length))

        def _forward(self, body: bytes) -> None:
            started = time.monotonic_ns()
            operation = self.headers.get("X-SMA-E1-Operation")
            interaction_role = self.headers.get("X-SMA-E1-Interaction-Role")
            if not operation or not interaction_role:
                self.send_error(400, "missing E1 operation or interaction role")
                return
            request_id = sha256(
                f"{operation}:{interaction_role}:{started}:{threading.get_ident()}".encode()
            ).hexdigest()
            append(request_journal, {
                "recordType": "SMA_E1_MODEL_AUDIT_REQUEST",
                "requestId": request_id,
                "operationKey": operation,
                "interactionRole": interaction_role,
                "method": self.command,
                "path": self.path,
                "receivedMonotonicNs": started,
                "requestBodyLength": len(body),
                "requestBodySha256": sha256(body).hexdigest(),
                "requestBodyBase64": base64.b64encode(body).decode("ascii"),
            })
            excluded = {
                "host",
                "content-length",
                "x-sma-e1-operation",
                "x-sma-e1-interaction-role",
            }
            headers = {
                key: value
                for key, value in self.headers.items()
                if key.lower() not in excluded
            }
            wire = urllib.request.Request(
                upstream_url(upstream, self.path),
                data=body if self.command != "GET" else None,
                headers=headers,
                method=self.command,
            )
            error_kind = None
            try:
                with urllib.request.urlopen(wire, timeout=120) as response:
                    status = response.status
                    response_headers = dict(response.headers.items())
                    response_body = response.read()
            except urllib.error.HTTPError as exc:
                status = exc.code
                response_headers = dict(exc.headers.items())
                response_body = exc.read()
                error_kind = "HTTP_ERROR"
            except Exception as exc:
                status = 502
                response_headers = {"Content-Type": "application/json"}
                response_body = canonical({
                    "error": {
                        "type": type(exc).__name__,
                        "message": "upstream model transport failure",
                    }
                })
                error_kind = type(exc).__name__
            completed = time.monotonic_ns()
            append(response_journal, {
                "recordType": "SMA_E1_MODEL_AUDIT_RESPONSE",
                "requestId": request_id,
                "operationKey": operation,
                "interactionRole": interaction_role,
                "completedMonotonicNs": completed,
                "httpStatus": status,
                "responseBodyLength": len(response_body),
                "responseBodySha256": sha256(response_body).hexdigest(),
                "responseBodyBase64": base64.b64encode(response_body).decode("ascii"),
                "responseWriteOutcome": "CLIENT_RECEIVED",
                "upstreamErrorKind": error_kind,
            })
            self.send_response(status)
            for key, value in response_headers.items():
                if key.lower() not in {
                    "connection",
                    "transfer-encoding",
                    "content-length",
                }:
                    self.send_header(key, value)
            self.send_header("Content-Length", str(len(response_body)))
            self.end_headers()
            self.wfile.write(response_body)

    return AuditHandler


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--upstream", required=True)
    parser.add_argument("--request-journal", required=True)
    parser.add_argument("--response-journal", required=True)
    parser.add_argument("--ready-file", required=True)
    args = parser.parse_args()
    if args.host != "127.0.0.1":
        raise SystemExit("audit proxy must bind to 127.0.0.1")
    request_journal = Path(args.request_journal)
    response_journal = Path(args.response_journal)
    for path in (request_journal, response_journal):
        if not path.is_file():
            raise SystemExit(f"precreated journal missing: {path}")
    server = ThreadingHTTPServer(
        (args.host, args.port),
        handler(args.upstream, request_journal, response_journal),
    )
    ready = Path(args.ready_file)
    descriptor = os.open(ready, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical({
            "pid": os.getpid(),
            "host": args.host,
            "port": args.port,
            "upstream": args.upstream,
            "interactionBinding": "X-SMA-E1-Interaction-Role",
        }))
        stream.flush()
        os.fsync(stream.fileno())
    server.serve_forever()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
