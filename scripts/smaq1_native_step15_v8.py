#!/usr/bin/env python3
"""Final SMA-Q1 runner after independent qualification of both OpenHands lanes."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import os
import subprocess
import sys
import time
from pathlib import Path
from typing import Any


TEAMS = Path("/Users/paul/work/tekroo-ai/teams")
SMA = Path("/Users/paul/work/tekroo-ai/sma-step15")
PARENT = TEAMS / "scripts/smaq1_native_step15_v7.py"
MANIFEST = TEAMS / "investigations/sma-q1/preregistration-native-v8.json"
IDENTITY = TEAMS / "investigations/sma-q1/step-15-execution-identity-v8.json"
AUTHORIZATION = TEAMS / "investigations/sma-q1/step-15-execution-authorization-v8.json"
OUTPUT = TEAMS / "OUTPUT/phase-3/sma-q1n-step15-v8"
RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v8"
QUAL_RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v8-fixture"
NATIVE_QUALIFICATION = TEAMS / "OUTPUT/phase-3/openhands-native-hook-lane-v1/execution-receipt.json"
NATIVE_QUALIFICATION_SHA = "b3859410dc2831f81df59afc3da964c3e9dbcbe8175aa3b8a290cb870db7ed87"
MANIFEST_SHA = "26098c415217b26d92602d8acb3c11c23c852758f062c0caedb3eb8384233116"
PROCESS_TIMEOUT_SECONDS = 30
EXPECTED_PARTITIONS = {
    "qualification_alpha": "openhands:274387fd02800ff75d7f6668:default",
    "qualification_beta": "openhands:599d546700c5bfbb80638298:default",
    "measured_alpha": "openhands:7854e1397be949fc724f0dd7:default",
    "measured_beta": "openhands:dba594259aec6caa81896e27:default",
}


def load_parent():
    spec = importlib.util.spec_from_file_location("smaq1_v8_parent", PARENT)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load sealed V7 evaluator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


parent = load_parent()
v5 = parent.v5
v4 = parent.v4
support = parent.support


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def workspace_partition(path: Path) -> str:
    return "openhands:" + hashlib.sha256(str(path).encode()).hexdigest()[:24] + ":default"


def configure_v8() -> None:
    parent.OUTPUT = OUTPUT
    v5.__file__ = str(Path(__file__))
    v5.MANIFEST = MANIFEST
    v5.MANIFEST_SHA = MANIFEST_SHA
    v5.IDENTITY = IDENTITY
    v5.AUTHORIZATION = AUTHORIZATION
    v5.OUTPUT = OUTPUT
    v5.RUN_ROOT = RUN_ROOT
    v5.QUAL_RUN_ROOT = QUAL_RUN_ROOT
    v5.configure_inherited_runner()

    v4.__file__ = str(Path(__file__))
    v4.MANIFEST = MANIFEST
    v4.MANIFEST_SHA = MANIFEST_SHA
    v4.IDENTITY = IDENTITY
    v4.AUTHORIZATION = AUTHORIZATION
    v4.OUTPUT = OUTPUT
    v4.JOURNAL = OUTPUT / "raw-receipts.jsonl"
    v4.SUMMARY = OUTPUT / "execution-receipt.json"
    v4.RUN_ROOT = RUN_ROOT
    v4.ALPHA = RUN_ROOT / "actor-alpha"
    v4.BETA = RUN_ROOT / "actor-beta"
    v4.RUNTIME = RUN_ROOT / "runtime"
    v4.LABEL = "com.tekroo.sma-service-smaq1n-step15-v8"
    v4.DATABASE = "sma_q1n_step15_20260813_v8"
    v4.SEMANTIC = "sma_q1n_step15_v8_semantic_20260813"
    v4.EPISODIC = "sma_q1n_step15_v8_episodic_20260813"
    v4.QUAL_RUN_ROOT = QUAL_RUN_ROOT
    v4.QUAL_DATABASE = "sma_q1n_step15_v8_fixture_20260813"
    v4.QUAL_SEMANTIC = "sma_q1n_step15_v8_fixture_semantic_20260813"
    v4.QUAL_EPISODIC = "sma_q1n_step15_v8_fixture_episodic_20260813"
    v4.QUAL_LABEL = "com.tekroo.sma-service-smaq1n-step15-v8-fixture"
    v4.ALPHA_PARTITION = workspace_partition(v4.ALPHA)
    v4.BETA_PARTITION = workspace_partition(v4.BETA)
    support.SMA_LABEL = v4.LABEL
    v4.qualify_fixture = parent.inherited.parent.qualify_fixture
    v4.create_conversation = parent.create_conversation
    v4.submit_and_wait = parent.submit_and_wait


configure_v8()


def retain_probe_failure(conversation: str, terminal: str,
                         elapsed_ms: float, stdout: str, stderr: str) -> None:
    path = OUTPUT / "native-probe-terminal-evidence.json"
    if path.exists():
        return
    payload = {
        "schemaVersion": "1.0.0",
        "recordType": "NATIVE_HOOK_PROBE_TERMINAL_EVIDENCE",
        "classification": "OBSERVED",
        "conversationId": conversation,
        "terminal": terminal,
        "processElapsedMs": elapsed_ms,
        "processTimeoutSeconds": PROCESS_TIMEOUT_SECONDS,
        "stdoutLength": len(stdout),
        "stdoutSHA256": hashlib.sha256(stdout.encode()).hexdigest(),
        "stderrLength": len(stderr),
        "stderrSHA256": hashlib.sha256(stderr.encode()).hexdigest(),
        "bodiesPersisted": False,
    }
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")


def native_probe(conversation: str, workspace: Path, prompt: str) -> dict[str, Any]:
    environment = os.environ.copy()
    environment["SMA_BRIDGE_URL"] = support.BRIDGE
    environment["OPENHANDS_SUPPRESS_BANNER"] = "1"
    started = time.monotonic_ns()
    args = [
        str(support.PROBE_PYTHON), str(support.PROBE), str(SMA), conversation,
        str(workspace), prompt,
    ]
    try:
        completed = subprocess.run(
            args, cwd=SMA, env=environment, capture_output=True, text=True,
            timeout=PROCESS_TIMEOUT_SECONDS, check=False,
        )
    except subprocess.TimeoutExpired as error:
        elapsed_ms = (time.monotonic_ns() - started) / 1_000_000.0
        stdout = error.stdout.decode(errors="replace") if isinstance(error.stdout, bytes) else (error.stdout or "")
        stderr = error.stderr.decode(errors="replace") if isinstance(error.stderr, bytes) else (error.stderr or "")
        retain_probe_failure(conversation, "PROCESS_TIMEOUT", elapsed_ms, stdout, stderr)
        raise RuntimeError("native hook probe process envelope timed out") from error
    elapsed_ms = (time.monotonic_ns() - started) / 1_000_000.0
    if completed.returncode != 0 or completed.stderr:
        retain_probe_failure(
            conversation, "PROCESS_FAILURE", elapsed_ms,
            completed.stdout, completed.stderr,
        )
        raise RuntimeError("native hook probe process failed")
    raw = json.loads(completed.stdout)
    context = "\n".join(raw.get("extended_content") or [])
    stdout = json.loads(raw.get("hook_stdout") or "{}")
    return {
        "elapsed_ms": float(raw["native_hook_elapsed_ms"]),
        "process_elapsed_ms": elapsed_ms,
        "process_timeout_seconds": PROCESS_TIMEOUT_SECONDS,
        "configured_timeout_ms": int(raw["configured_hook_timeout_seconds"] * 1000),
        "event_count": int(raw["hook_event_count"]),
        "success": bool(raw["hook_success"]),
        "exit_code": raw["hook_exit_code"],
        "stderr_length": len(raw.get("hook_stderr") or ""),
        "prompt_unchanged": raw["original_prompt"] == prompt,
        "context_length": len(context),
        "context_sha256": v4.sha_text(context),
        "trace_id": stdout.get("smaTraceId"),
        "context": context,
    }


v4.native_probe = native_probe


def validate_artifacts() -> dict[str, Any]:
    manifest = json.loads(MANIFEST.read_text())
    identity = json.loads(IDENTITY.read_text())
    authorization = json.loads(AUTHORIZATION.read_text())
    native_receipt = json.loads(NATIVE_QUALIFICATION.read_text())
    runner_hash = sha_file(Path(__file__))
    checks = {
        "manifest_hash_matches": sha_file(MANIFEST) == MANIFEST_SHA,
        "manifest_corpus_unchanged": (
            manifest["unchangedBase"]["scenarioCount"] == 18
            and manifest["unchangedBase"]["repetitionCount"] == 96
        ),
        "native_qualification_hash_matches": sha_file(NATIVE_QUALIFICATION) == NATIVE_QUALIFICATION_SHA,
        "native_qualification_passed": (
            native_receipt["status"] == "PASS"
            and native_receipt["completed_attempts"] == 128
            and native_receipt["failed_attempts"] == 0
        ),
        "identity_manifest_matches": identity["frozenExperiment"]["manifestSHA256"] == MANIFEST_SHA,
        "identity_runner_matches": identity["harness"]["sha256"] == runner_hash,
        "authorization_manifest_matches": authorization["frozenManifestSHA256"] == MANIFEST_SHA,
        "authorization_identity_matches": authorization["executionIdentitySHA256"] == sha_file(IDENTITY),
        "authorization_runner_matches": authorization["harnessSHA256"] == runner_hash,
        "authorization_is_live": authorization["executionAuthorized"] is True,
    }
    if not all(checks.values()):
        raise RuntimeError(f"V8 artifact contract failed: {checks}")
    return {"status": "PASS", "checks": checks, "liveServiceCalls": 0}


def static_expectation_checks() -> dict[str, Any]:
    computed = {
        "qualification_alpha": workspace_partition(QUAL_RUN_ROOT / "actor-alpha"),
        "qualification_beta": workspace_partition(QUAL_RUN_ROOT / "actor-beta"),
        "measured_alpha": workspace_partition(RUN_ROOT / "actor-alpha"),
        "measured_beta": workspace_partition(RUN_ROOT / "actor-beta"),
    }
    if computed != EXPECTED_PARTITIONS:
        raise RuntimeError(f"V8 workspace partition mismatch: {computed}")
    return {
        "artifact_contract": validate_artifacts(),
        "v8_partitions": computed,
        "deterministic_temperature": parent.TEMPERATURE,
        "nonessential_tools_removed": True,
        "terminal_forms": ["FINISH_ACTION", "AGENT_MESSAGE"],
        "semantic_hook_deadline_ms": 1000,
        "native_probe_process_timeout_seconds": PROCESS_TIMEOUT_SECONDS,
    }


v4.static_expectation_checks = static_expectation_checks


def main() -> None:
    if "--authorization-contract-test" in sys.argv or "--static-self-test" in sys.argv:
        print(json.dumps(static_expectation_checks(), indent=2, sort_keys=True))
        return
    validate_artifacts()
    v4.main()


if __name__ == "__main__":
    main()
