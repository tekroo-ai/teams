#!/usr/bin/env python3
"""Execute the bound SMA hook with an in-process bridge-response transport."""

from __future__ import annotations

import argparse
import contextlib
import importlib.util
import io
import json
import sys
from pathlib import Path
from typing import Any


class _Response:
    def __init__(self, payload: bytes) -> None:
        self._payload = payload

    def __enter__(self) -> "_Response":
        return self

    def __exit__(self, *_args: Any) -> None:
        return None

    def read(self) -> bytes:
        return self._payload


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--hook", required=True, type=Path)
    parser.add_argument("--sma-truth", required=True, type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--command-mode", action="store_true")
    parser.add_argument("--command-receipt", type=Path)
    args = parser.parse_args()

    spec = importlib.util.spec_from_file_location("bound_sma_context_hook", args.hook)
    if spec is None or spec.loader is None:
        raise RuntimeError("could not load bound hook")
    hook = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(hook)

    sma_truth = json.loads(args.sma_truth.read_text(encoding="utf-8"))
    bridge_response = sma_truth["bridgeResponse"]
    retained_request: dict[str, Any] = {}

    def fake_urlopen(request: Any, timeout: float) -> _Response:
        retained_request["url"] = request.full_url
        retained_request["method"] = request.get_method()
        retained_request["timeoutSeconds"] = timeout
        retained_request["body"] = json.loads(request.data.decode("utf-8"))
        return _Response(
            json.dumps(bridge_response, separators=(",", ":")).encode("utf-8")
        )

    if args.command_mode:
        hook_input = json.load(sys.stdin)
    else:
        hook_input = {
            "message": "Implement the product-bound fixture.",
            "session_id": "conv-product-truth",
            "working_dir": "/tmp/sma-s2-product-truth",
        }
    stdin = io.StringIO(json.dumps(hook_input))
    stdout = io.StringIO()
    original_stdin = hook.sys.stdin
    original_urlopen = hook.urllib.request.urlopen
    hook.sys.stdin = stdin
    hook.urllib.request.urlopen = fake_urlopen
    try:
        with contextlib.redirect_stdout(stdout):
            hook.main()
    finally:
        hook.sys.stdin = original_stdin
        hook.urllib.request.urlopen = original_urlopen

    hook_output = json.loads(stdout.getvalue())
    if hook_output.get("smaTraceId") != bridge_response.get("trace_id"):
        raise RuntimeError("bound hook did not preserve bridge trace identity")
    if hook_output.get("additionalContext") != bridge_response.get("context_block"):
        raise RuntimeError("bound hook altered the bridge context block")

    receipt: dict[str, Any] = {
        "schemaVersion": "1.0.0-dev",
        "recordType": "BOUND_SMA_HOOK_PRODUCT_TRUTH",
        "authoritativePositiveSource": "BOUND_SMA_CONTEXT_HOOK",
        "hookInput": hook_input,
        "bridgeRequest": retained_request,
        "bridgeResponse": bridge_response,
        "hookOutput": hook_output,
    }
    if args.command_receipt:
        args.command_receipt.parent.mkdir(parents=True, exist_ok=True)
        args.command_receipt.write_text(
            json.dumps(receipt, sort_keys=True, separators=(",", ":")) + "\n",
            encoding="utf-8",
        )
    if args.command_mode:
        sys.stdout.write(json.dumps(hook_output, separators=(",", ":")) + "\n")
        return
    if args.output is None:
        raise RuntimeError("--output is required outside command mode")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(receipt, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()
