#!/usr/bin/env python3
"""Deterministic protocol fixture for SMA-S1 harness self-tests only.

This is not the product qualification driver. It touches no SMA service,
MongoDB, Qdrant, OpenHands process, or model endpoint.
"""

from __future__ import annotations

import hashlib
import json
import sys


PROTOCOL_VERSION = "1.0.0"


def canonical_bytes(value: object) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def fail(message: str) -> int:
    print(message, file=sys.stderr)
    return 2


def main() -> int:
    try:
        request = json.loads(sys.stdin.read())
    except (json.JSONDecodeError, UnicodeError) as exc:
        return fail(f"invalid request ({type(exc).__name__})")

    required = {
        "protocolVersion",
        "operation",
        "runId",
        "caseId",
        "repetition",
        "manifestSHA256",
        "identitySHA256",
        "fault",
        "fixture",
        "namespaces",
    }
    if set(request) != required:
        return fail("request fields do not match the self-test protocol")
    if request["protocolVersion"] != PROTOCOL_VERSION:
        return fail("unsupported protocol version")
    if request["operation"] != "SELF_TEST":
        return fail("fixture driver accepts SELF_TEST only")

    request_digest = hashlib.sha256(canonical_bytes(request)).hexdigest()
    predicate = request["fixture"].get("predicate", "fixture predicate")
    response = {
        "protocolVersion": PROTOCOL_VERSION,
        "operation": request["operation"],
        "runId": request["runId"],
        "caseId": request["caseId"],
        "repetition": request["repetition"],
        "driverIdentity": "SMA_S1_SELF_TEST_FIXTURE_ONLY",
        "terminalState": "SUCCEEDED",
        "faultActivation": {
            "requested": request["fault"],
            "activated": True,
        },
        "faultObservation": {
            "observed": True,
            "fault": request["fault"],
        },
        "predicateResults": [
            {
                "predicate": predicate,
                "passed": True,
                "attributedComponent": "SELF_TEST_FIXTURE",
                "evidenceIds": ["fixture-request-digest"],
            }
        ],
        "evidence": {
            "requestSHA256": request_digest,
            "requestLength": len(canonical_bytes(request)),
            "syntheticBodiesRetained": False,
        },
        "safetyStop": False,
        "cleanupCorrelationId": f"cleanup:{request['runId']}",
    }
    sys.stdout.buffer.write(canonical_bytes(response) + b"\n")
    sys.stdout.buffer.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
