#!/usr/bin/env python3
"""SMA-Q1 successor with a concurrency-safe append-only evidence writer."""

from __future__ import annotations

import contextlib
import hashlib
import importlib.util
import io
import json
import os
import sys
import threading
from pathlib import Path
from typing import Any


TEAMS = Path("/Users/paul/work/tekroo-ai/teams")
SMA = Path("/Users/paul/work/tekroo-ai/sma-step15")
PARENT = TEAMS / "scripts/smaq1_native_step15_v11.py"
MANIFEST = TEAMS / "investigations/sma-q1/preregistration-native-v12.json"
IDENTITY = TEAMS / "investigations/sma-q1/step-15-execution-identity-v12.json"
AUTHORIZATION = TEAMS / "investigations/sma-q1/step-15-execution-authorization-v12.json"
OUTPUT = TEAMS / "OUTPUT/phase-3/sma-q1n-step15-v12"
RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v12"
QUAL_RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v12-fixture"
MANIFEST_SHA = "2e97230c4f423b10cc6b98be1d9f02d6e3a97c71e13c67f6b585623b01e1c195"
EXPECTED_PARTITIONS = {
    "qualification_alpha": "openhands:58cd06d12c6b53e43453cb08:default",
    "qualification_beta": "openhands:847fc694c8ca5c192cfd31cc:default",
    "measured_alpha": "openhands:8565f4561b48843b294e9305:default",
    "measured_beta": "openhands:5e1854bd3238fa63bebafe60:default",
}
JOURNAL_LOCK = threading.Lock()


def load_parent():
    spec = importlib.util.spec_from_file_location("smaq1_v12_parent", PARENT)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load sealed V11 evaluator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


parent = load_parent()
v7 = parent.v7
v5 = parent.v5
v4 = parent.v4
support = parent.support
base_create_conversation = parent.base_create_conversation
base_submit_and_wait = parent.base_submit_and_wait
base_native_probe = parent.base_native_probe


class AppendableDurableJournal:
    """One creation fence plus shared-lock append handles for one run journal."""

    def __init__(self, path: Path):
        self.path = path
        path.parent.mkdir(parents=True, exist_ok=True)

    def append(self, kind: str, data: dict[str, Any]) -> None:
        record = {"recorded_at": v4.now(), "kind": kind, **data}
        encoded = json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n"
        with JOURNAL_LOCK:
            with self.path.open("a", encoding="utf-8") as stream:
                stream.write(encoded)
                stream.flush()
                os.fsync(stream.fileno())


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def workspace_partition(path: Path) -> str:
    return "openhands:" + hashlib.sha256(str(path).encode()).hexdigest()[:24] + ":default"


def configure_v12() -> None:
    parent.OUTPUT = OUTPUT
    v7.OUTPUT = OUTPUT
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
    v4.LABEL = "com.tekroo.sma-service-smaq1n-step15-v12"
    v4.DATABASE = "sma_q1n_step15_20260813_v12"
    v4.SEMANTIC = "sma_q1n_step15_v12_semantic_20260813"
    v4.EPISODIC = "sma_q1n_step15_v12_episodic_20260813"
    v4.QUAL_RUN_ROOT = QUAL_RUN_ROOT
    v4.QUAL_DATABASE = "sma_q1n_step15_v12_fixture_20260813"
    v4.QUAL_SEMANTIC = "sma_q1n_step15_v12_fixture_semantic_20260813"
    v4.QUAL_EPISODIC = "sma_q1n_step15_v12_fixture_episodic_20260813"
    v4.QUAL_LABEL = "com.tekroo.sma-service-smaq1n-step15-v12-fixture"
    v4.ALPHA_PARTITION = workspace_partition(v4.ALPHA)
    v4.BETA_PARTITION = workspace_partition(v4.BETA)
    support.SMA_LABEL = v4.LABEL
    v4.qualify_fixture = v7.inherited.parent.qualify_fixture
    v4.create_conversation = base_create_conversation
    v4.submit_and_wait = base_submit_and_wait
    v4.native_probe = base_native_probe
    v4.run_repetition = parent.run_repetition
    v4.run_delegated_repetition = parent.run_delegated_repetition
    v4.AssertionError = parent.SubstantiveCounterexample
    v4.DurableJournal = AppendableDurableJournal


configure_v12()


def validate_artifacts() -> dict[str, Any]:
    manifest = json.loads(MANIFEST.read_text())
    identity = json.loads(IDENTITY.read_text())
    authorization = json.loads(AUTHORIZATION.read_text())
    qualification = json.loads(parent.parent.parent.RESPONSE_QUALIFICATION.read_text())
    runner_hash = sha_file(Path(__file__))
    checks = {
        "manifest_hash_matches": sha_file(MANIFEST) == MANIFEST_SHA,
        "manifest_corpus_unchanged": (
            manifest["unchangedBase"]["scenarioCount"] == 18
            and manifest["unchangedBase"]["repetitionCount"] == 96
        ),
        "normalized_oracle_preserved": (
            manifest["unchangedV11"]["expectedNormalized"]
            == parent.EXPECTED_NORMALIZED
        ),
        "response_qualification_passed": (
            qualification["status"] == "PASS"
            and qualification["completedAttempts"] == 128
            and qualification["failedAttempts"] == 0
        ),
        "identity_manifest_matches": (
            identity["frozenExperiment"]["manifestSHA256"] == MANIFEST_SHA
        ),
        "identity_runner_matches": identity["harness"]["sha256"] == runner_hash,
        "authorization_manifest_matches": (
            authorization["frozenManifestSHA256"] == MANIFEST_SHA
        ),
        "authorization_identity_matches": (
            authorization["executionIdentitySHA256"] == sha_file(IDENTITY)
        ),
        "authorization_runner_matches": authorization["harnessSHA256"] == runner_hash,
        "authorization_is_live": authorization["executionAuthorized"] is True,
    }
    if not all(checks.values()):
        raise RuntimeError(f"V12 artifact contract failed: {checks}")
    return {"status": "PASS", "checks": checks, "liveServiceCalls": 0}


def static_expectation_checks() -> dict[str, Any]:
    computed = {
        "qualification_alpha": workspace_partition(QUAL_RUN_ROOT / "actor-alpha"),
        "qualification_beta": workspace_partition(QUAL_RUN_ROOT / "actor-beta"),
        "measured_alpha": workspace_partition(RUN_ROOT / "actor-alpha"),
        "measured_beta": workspace_partition(RUN_ROOT / "actor-beta"),
    }
    if computed != EXPECTED_PARTITIONS:
        raise RuntimeError(f"V12 workspace partition mismatch: {computed}")
    return {
        "artifact_contract": validate_artifacts(),
        "v12_partitions": computed,
        "journal_writer": "SHARED_LOCK_APPEND_FSYNC",
        "output_root_creation_fence": "mkdir_exist_ok_false",
        "normalized_safety_oracle": {
            "normalization": "NFKC_CASEFOLD_NONALNUM_TO_SPACE",
            "expected": parent.EXPECTED_NORMALIZED,
            "forbidden": parent.FORBIDDEN_NORMALIZED,
            "format_nonconformance_retained": True,
        },
        "preassertion_evidence": "REDACTED_RAW_SYNTHETIC_BODIES",
        "deterministic_temperature": v7.TEMPERATURE,
        "deterministic_seed": parent.parent.parent.SEED,
        "configured_tools": [],
        "include_default_tools": parent.parent.parent.EXPECTED_BUILTINS,
        "auto_attach_vision_inspect_tool": False,
        "semantic_hook_deadline_ms": 1000,
        "native_probe_process_timeout_seconds": parent.parent.parent.parent.PROCESS_TIMEOUT_SECONDS,
        "openhands_repair_commit": "3e05292b0ba5bccedd6e94f73f48b9f721a7d1dc",
    }


v4.static_expectation_checks = static_expectation_checks


def execute() -> int:
    validate_artifacts()
    captured = io.StringIO()
    exit_code = 0
    with contextlib.redirect_stdout(captured):
        try:
            v4.main()
        except SystemExit as error:
            exit_code = int(error.code or 0)
    if not v4.SUMMARY.exists():
        sys.stdout.write(captured.getvalue())
        return exit_code or 1
    receipt = json.loads(v4.SUMMARY.read_text())
    failure = receipt.get("failure") or {}
    if failure.get("type") == parent.SubstantiveCounterexample.__name__:
        failure["classification"] = "SUBSTANTIVE_COUNTEREXAMPLE"
        receipt["failure"] = failure
        AppendableDurableJournal(v4.JOURNAL).append(
            "RUN_FAILURE_ADJUDICATION",
            {
                "classification": "SUBSTANTIVE_COUNTEREXAMPLE",
                "type": failure.get("type"),
                "message_sha256": failure.get("message_sha256"),
            },
        )
        receipt["raw_journal_sha256"] = sha_file(v4.JOURNAL)
        receipt["report_sha256"] = ""
        receipt["report_sha256"] = v4.canonical_sha(receipt)
        v4.SUMMARY.write_text(json.dumps(receipt, indent=2, sort_keys=True) + "\n")
    print(
        json.dumps(
            {
                "status": receipt["status"],
                "decision": receipt["decision"],
                "completed_scenarios": receipt["completed_scenarios"],
                "completed_repetitions": receipt["completed_repetitions"],
                "failure": receipt.get("failure"),
                "summary": str(v4.SUMMARY),
                "summary_sha256": sha_file(v4.SUMMARY),
                "journal_sha256": sha_file(v4.JOURNAL),
            },
            indent=2,
        )
    )
    return 0 if receipt["status"] == "PASS" else 1


def main() -> int:
    if "--authorization-contract-test" in sys.argv or "--static-self-test" in sys.argv:
        print(json.dumps(static_expectation_checks(), indent=2, sort_keys=True))
        return 0
    return execute()


if __name__ == "__main__":
    raise SystemExit(main())
