#!/usr/bin/env python3
"""Offline-only qualification for the SMA-S2 deterministic stub candidate.

This script never contacts OpenHands, SMA, MongoDB, Qdrant, or a model. It uses
only an ephemeral loopback server and synthetic fixtures. Passing this harness
qualifies harness controls; it is not an SMA-S2 measured execution or PASS.
"""

from __future__ import annotations

import argparse
import base64
import concurrent.futures
import hashlib
import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Callable


SYNTHETIC_SECRET = "sk-test-SMA-S2-CANDIDATE-1-NEVER-USE"
UNTRUSTED_CONTEXT = (
    "<sma-memory-context trust=\"untrusted-evidence\">\n"
    "memory-id=mem-alpha-timeout\n"
    "</sma-memory-context>"
)


def canonical_json_bytes(value: Any) -> bytes:
    return json.dumps(
        value, ensure_ascii=False, sort_keys=True, separators=(",", ":")
    ).encode("utf-8")


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        while chunk := stream.read(1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


def read_json_lines(path: Path) -> list[dict[str, Any]]:
    return [json.loads(line) for line in path.read_text().splitlines() if line]


def post(
    port: int,
    payload: dict[str, Any],
    mode: str = "SUCCESS",
    timeout: float = 2.0,
) -> tuple[int, bytes, str]:
    body = canonical_json_bytes(payload)
    request = urllib.request.Request(
        f"http://127.0.0.1:{port}/v1/chat/completions",
        data=body,
        headers={
            "Content-Type": "application/json",
            "X-SMA-S2-Mode": mode,
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, response.read(), sha256_bytes(body)
    except urllib.error.HTTPError as error:
        return error.code, error.read(), sha256_bytes(body)


def fixture(case_id: str, repetition: int, stream: bool = False) -> dict[str, Any]:
    prompt = (
        f"[SMA-S2-STUB case={case_id} repetition={repetition}] "
        "Preserve this synthetic prompt exactly."
    )
    return {
        "model": "openai/sma-s2-deterministic-stub-candidate-1",
        "messages": [
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": prompt},
                    {"type": "text", "text": UNTRUSTED_CONTEXT},
                ],
            }
        ],
        "stream": stream,
        "temperature": 0,
    }


def wait_for_ready(path: Path, process: subprocess.Popen[str]) -> dict[str, Any]:
    deadline = time.monotonic() + 5.0
    while time.monotonic() < deadline:
        if path.exists():
            return json.loads(path.read_text())
        if process.poll() is not None:
            raise AssertionError(f"stub exited before ready: {process.returncode}")
        time.sleep(0.02)
    raise AssertionError("stub readiness timed out")


def reserve_then_release_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def assert_prompt_context_oracle() -> dict[str, Any]:
    request = fixture("OFFLINE-ORACLE", 0)
    content = request["messages"][0]["content"]
    prompt = content[0]["text"]
    context = content[1]["text"]
    assert len(content) == 2
    assert prompt.startswith("[SMA-S2-STUB case=OFFLINE-ORACLE repetition=0]")
    assert UNTRUSTED_CONTEXT == context
    assert UNTRUSTED_CONTEXT not in prompt
    return {
        "promptLength": len(prompt.encode("utf-8")),
        "promptSha256": sha256_bytes(prompt.encode("utf-8")),
        "contextLength": len(context.encode("utf-8")),
        "contextSha256": sha256_bytes(context.encode("utf-8")),
        "contentSegmentCount": len(content),
    }


def assert_create_once(output: Path) -> dict[str, Any]:
    marker = output / "create-once.marker"
    descriptor = os.open(marker, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, b"created-once\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    try:
        descriptor = os.open(marker, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except FileExistsError:
        return {"secondCreateRejected": True}
    else:
        os.close(descriptor)
        raise AssertionError("create-once guard allowed replacement")


def assert_stub_protocol(port: int) -> dict[str, Any]:
    status, body, request_sha = post(port, fixture("OFFLINE-SUCCESS", 1))
    response = json.loads(body)
    assert status == 200
    assert response["choices"][0]["message"]["content"] == (
        "STUB_OK:OFFLINE-SUCCESS:1"
    )

    stream_status, stream_body, stream_request_sha = post(
        port, fixture("OFFLINE-STREAM", 2, stream=True)
    )
    assert stream_status == 200
    assert b"STUB_OK:OFFLINE-STREAM:2" in stream_body
    assert stream_body.endswith(b"data: [DONE]\n\n")
    return {
        "nonStreamingRequestSha256": request_sha,
        "nonStreamingResponseSha256": sha256_bytes(body),
        "streamingRequestSha256": stream_request_sha,
        "streamingResponseSha256": sha256_bytes(stream_body),
    }


def assert_fault_controls(port: int) -> dict[str, Any]:
    status_503, body_503, _ = post(
        port, fixture("OFFLINE-HTTP503", 3), mode="HTTP_503"
    )
    assert status_503 == 503
    assert b"deterministic HTTP 503" in body_503

    malformed_status, malformed_body, _ = post(
        port, fixture("OFFLINE-MALFORMED", 4), mode="MALFORMED"
    )
    assert malformed_status == 200
    try:
        json.loads(malformed_body)
    except json.JSONDecodeError:
        malformed_rejected = True
    else:
        raise AssertionError("malformed mode emitted valid JSON")

    timeout_observed = False
    started = time.monotonic_ns()
    try:
        post(
            port,
            fixture("OFFLINE-TIMEOUT", 5),
            mode="TIMEOUT",
            timeout=0.05,
        )
    except (TimeoutError, urllib.error.URLError):
        timeout_observed = True
    timeout_elapsed_ns = time.monotonic_ns() - started
    assert timeout_observed

    unused_port = reserve_then_release_port()
    transport_failure_observed = False
    try:
        post(unused_port, fixture("OFFLINE-TRANSPORT", 6), timeout=0.2)
    except (ConnectionError, OSError, urllib.error.URLError):
        transport_failure_observed = True
    assert transport_failure_observed
    return {
        "http503Status": status_503,
        "malformedRejected": malformed_rejected,
        "timeoutObserved": timeout_observed,
        "timeoutElapsedNs": timeout_elapsed_ns,
        "transportFailureObserved": transport_failure_observed,
    }


def assert_concurrency(port: int) -> dict[str, Any]:
    def invoke(index: int) -> str:
        status, body, _ = post(port, fixture(f"OFFLINE-CONCURRENT-{index}", index))
        assert status == 200
        return json.loads(body)["choices"][0]["message"]["content"]

    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
        terminals = list(executor.map(invoke, range(4)))
    assert len(set(terminals)) == 4
    return {"channelCount": 4, "terminals": terminals}


def assert_secret_surfaces(port: int, raw: Path, operational: Path) -> dict[str, Any]:
    payload = fixture("OFFLINE-SECRET", 7)
    payload["messages"][0]["content"][0]["text"] += f" {SYNTHETIC_SECRET}"
    status, _, request_sha = post(port, payload)
    assert status == 200
    raw_records = read_json_lines(raw)
    raw_bodies = [
        base64.b64decode(record["requestBodyBase64"], validate=True)
        for record in raw_records
    ]
    raw_requests = [json.loads(body) for body in raw_bodies]
    operational_bytes = operational.read_bytes()
    assert any(
        SYNTHETIC_SECRET in request["messages"][0]["content"][0]["text"]
        for request in raw_requests
    )
    assert SYNTHETIC_SECRET.encode("utf-8") not in operational_bytes
    assert any(
        request["messages"][0]["content"][1]["text"] == UNTRUSTED_CONTEXT
        for request in raw_requests
    )
    assert SYNTHETIC_SECRET not in UNTRUSTED_CONTEXT
    return {
        "requestSha256": request_sha,
        "presentInSealedRawEvidence": True,
        "absentFromOperationalJournal": True,
        "absentFromInjectedContextFixture": True,
    }


def assert_journal_integrity(raw: Path, operational: Path) -> dict[str, Any]:
    raw_records = read_json_lines(raw)
    operational_records = read_json_lines(operational)
    assert len(raw_records) == len(operational_records)
    assert len(raw_records) >= 10
    raw_by_id = {record["requestId"]: record for record in raw_records}
    op_by_id = {record["requestId"]: record for record in operational_records}
    assert raw_by_id.keys() == op_by_id.keys()
    for request_id, raw_record in raw_by_id.items():
        body = base64.b64decode(raw_record["requestBodyBase64"], validate=True)
        assert len(body) == raw_record["requestBodyLength"]
        assert sha256_bytes(body) == raw_record["requestBodySha256"]
        assert op_by_id[request_id]["requestBodySha256"] == sha256_bytes(body)
        assert "requestBodyBase64" not in op_by_id[request_id]
    return {
        "recordCount": len(raw_records),
        "rawJournalSha256": sha256_file(raw),
        "operationalJournalSha256": sha256_file(operational),
        "allRawBodiesReconstructed": True,
        "identitySetsEqual": True,
    }


def run_test(
    test_id: str,
    function: Callable[[], dict[str, Any]],
) -> dict[str, Any]:
    started = time.monotonic_ns()
    try:
        detail = function()
    except Exception as error:
        return {
            "id": test_id,
            "status": "FAIL",
            "durationNs": time.monotonic_ns() - started,
            "errorType": type(error).__name__,
            "error": str(error),
        }
    return {
        "id": test_id,
        "status": "PASS",
        "durationNs": time.monotonic_ns() - started,
        "detail": detail,
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser()
    parser.add_argument("--stub", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    stub = args.stub.resolve()
    raw = output / "stub-raw-evidence.jsonl"
    operational = output / "stub-operational.jsonl"
    ready_file = output / "stub-ready.json"
    stdout_file = output / "stub-stdout.log"
    stderr_file = output / "stub-stderr.log"
    results: list[dict[str, Any]] = []
    process: subprocess.Popen[str] | None = None

    results.append(
        run_test("OFFLINE-001-PROMPT-CONTEXT-ORACLE", assert_prompt_context_oracle)
    )
    results.append(
        run_test("OFFLINE-002-CREATE-ONCE-GUARD", lambda: assert_create_once(output))
    )
    try:
        with stdout_file.open("w") as stdout, stderr_file.open("w") as stderr:
            process = subprocess.Popen(
                [
                    sys.executable,
                    str(stub),
                    "--raw-journal",
                    str(raw),
                    "--operational-journal",
                    str(operational),
                    "--timeout-delay-ms",
                    "250",
                    "--ready-file",
                    str(ready_file),
                ],
                stdin=subprocess.DEVNULL,
                stdout=stdout,
                stderr=stderr,
                text=True,
            )
            ready = wait_for_ready(ready_file, process)
            port = int(ready["port"])
            results.append(
                run_test(
                    "OFFLINE-003-STUB-PROTOCOL",
                    lambda: assert_stub_protocol(port),
                )
            )
            results.append(
                run_test(
                    "OFFLINE-004-FAULT-CONTROLS",
                    lambda: assert_fault_controls(port),
                )
            )
            results.append(
                run_test(
                    "OFFLINE-005-FOUR-CHANNEL-CONCURRENCY",
                    lambda: assert_concurrency(port),
                )
            )
            results.append(
                run_test(
                    "OFFLINE-006-SECRET-SURFACE-POLICY",
                    lambda: assert_secret_surfaces(port, raw, operational),
                )
            )
            time.sleep(0.35)
            results.append(
                run_test(
                    "OFFLINE-007-JOURNAL-RECONSTRUCTION",
                    lambda: assert_journal_integrity(raw, operational),
                )
            )
    finally:
        cleanup_started = time.monotonic_ns()
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=3.0)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3.0)
        results.append(
            {
                "id": "OFFLINE-008-CLEANUP",
                "status": "PASS" if process is not None and process.poll() is not None else "FAIL",
                "durationNs": time.monotonic_ns() - cleanup_started,
                "detail": {
                    "stubExited": process is not None and process.poll() is not None,
                    "stubExitCode": process.returncode if process is not None else None,
                },
            }
        )

    overall = "PASS" if all(result["status"] == "PASS" for result in results) else "FAIL"
    receipt = {
        "schemaVersion": "1.0.0-candidate",
        "recordType": "SMA_S2_OFFLINE_HARNESS_QUALIFICATION_RECEIPT",
        "status": overall,
        "scope": "OFFLINE_HARNESS_CONTROLS_ONLY",
        "explicitlyNot": [
            "SMA-S2 measured execution",
            "OpenHands/SMA boundary qualification",
            "model invocation",
            "production readiness",
        ],
        "networkUse": "EPHEMERAL_LOOPBACK_STUB_ONLY",
        "stub": {"path": str(stub), "sha256": sha256_file(stub)},
        "harness": {
            "path": str(Path(__file__).resolve()),
            "sha256": sha256_file(Path(__file__).resolve()),
        },
        "tests": results,
        "summary": {
            "passCount": sum(result["status"] == "PASS" for result in results),
            "failCount": sum(result["status"] == "FAIL" for result in results),
            "testCount": len(results),
        },
    }
    receipt_path = output / "offline-harness-receipt.json"
    descriptor = os.open(
        receipt_path,
        os.O_CREAT | os.O_EXCL | os.O_WRONLY,
        0o600,
    )
    try:
        os.write(descriptor, canonical_json_bytes(receipt) + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    print(
        canonical_json_bytes(
            {
                "status": overall,
                "receipt": str(receipt_path),
                "receiptSha256": sha256_file(receipt_path),
            }
        ).decode("utf-8")
    )
    return 0 if overall == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
