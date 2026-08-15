#!/usr/bin/env python3
"""SMA-Q1 successor with exact OpenHands capabilities and deterministic seed."""

from __future__ import annotations

import copy
import hashlib
import importlib.util
import json
import sys
from pathlib import Path
from typing import Any


TEAMS = Path("/Users/paul/work/tekroo-ai/teams")
SMA = Path("/Users/paul/work/tekroo-ai/sma-step15")
PARENT = TEAMS / "scripts/smaq1_native_step15_v8.py"
MANIFEST = TEAMS / "investigations/sma-q1/preregistration-native-v9.json"
IDENTITY = TEAMS / "investigations/sma-q1/step-15-execution-identity-v9.json"
AUTHORIZATION = TEAMS / "investigations/sma-q1/step-15-execution-authorization-v9.json"
OUTPUT = TEAMS / "OUTPUT/phase-3/sma-q1n-step15-v9"
RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v9"
QUAL_RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v9-fixture"
RESPONSE_QUALIFICATION = (
    TEAMS / "OUTPUT/phase-3/openhands-exact-capability-v2/execution-receipt.json"
)
RESPONSE_QUALIFICATION_SHA = (
    "cf3c371079330ea52dc1f8417bbfffe1845f10bc0aaab1cc835e635c8abb63fa"
)
MANIFEST_SHA = "7077c843e31ea3b897c612e6de700fea63b46ac7f8cf7c6c30e29ea204050b2a"
SEED = 20260813
EXPECTED_BUILTINS = ["FinishTool"]
EXPECTED_PARTITIONS = {
    "qualification_alpha": "openhands:4d415f9b8a64a20e176db26b:default",
    "qualification_beta": "openhands:cd91da053f1d5ea8766d55be:default",
    "measured_alpha": "openhands:571c583cff76952ea93c5c32:default",
    "measured_beta": "openhands:b5e22b001f794f0e7d6817e6:default",
}


def load_parent():
    spec = importlib.util.spec_from_file_location("smaq1_v9_parent", PARENT)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load sealed V8 evaluator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


parent = load_parent()
v7 = parent.parent
v5 = parent.v5
v4 = parent.v4
support = parent.support
base_settings = support.settings


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def workspace_partition(path: Path) -> str:
    return "openhands:" + hashlib.sha256(str(path).encode()).hexdigest()[:24] + ":default"


def exact_settings(session_key: str) -> dict[str, Any]:
    settings = copy.deepcopy(base_settings(session_key))
    settings["include_default_tools"] = EXPECTED_BUILTINS
    settings["auto_attach_vision_inspect_tool"] = False
    settings["llm"]["seed"] = SEED
    return settings


def configure_v9() -> None:
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
    v4.LABEL = "com.tekroo.sma-service-smaq1n-step15-v9"
    v4.DATABASE = "sma_q1n_step15_20260813_v9"
    v4.SEMANTIC = "sma_q1n_step15_v9_semantic_20260813"
    v4.EPISODIC = "sma_q1n_step15_v9_episodic_20260813"
    v4.QUAL_RUN_ROOT = QUAL_RUN_ROOT
    v4.QUAL_DATABASE = "sma_q1n_step15_v9_fixture_20260813"
    v4.QUAL_SEMANTIC = "sma_q1n_step15_v9_fixture_semantic_20260813"
    v4.QUAL_EPISODIC = "sma_q1n_step15_v9_fixture_episodic_20260813"
    v4.QUAL_LABEL = "com.tekroo.sma-service-smaq1n-step15-v9-fixture"
    v4.ALPHA_PARTITION = workspace_partition(v4.ALPHA)
    v4.BETA_PARTITION = workspace_partition(v4.BETA)
    support.SMA_LABEL = v4.LABEL
    support.settings = exact_settings
    v4.qualify_fixture = v7.inherited.parent.qualify_fixture
    v4.create_conversation = v7.create_conversation
    v4.submit_and_wait = v7.submit_and_wait
    v4.native_probe = parent.native_probe


configure_v9()


def validate_artifacts() -> dict[str, Any]:
    manifest = json.loads(MANIFEST.read_text())
    identity = json.loads(IDENTITY.read_text())
    authorization = json.loads(AUTHORIZATION.read_text())
    qualification = json.loads(RESPONSE_QUALIFICATION.read_text())
    runner_hash = sha_file(Path(__file__))
    checks = {
        "manifest_hash_matches": sha_file(MANIFEST) == MANIFEST_SHA,
        "manifest_corpus_unchanged": (
            manifest["unchangedBase"]["scenarioCount"] == 18
            and manifest["unchangedBase"]["repetitionCount"] == 96
        ),
        "response_qualification_hash_matches": (
            sha_file(RESPONSE_QUALIFICATION) == RESPONSE_QUALIFICATION_SHA
        ),
        "response_qualification_passed": (
            qualification["status"] == "PASS"
            and qualification["completedAttempts"] == 128
            and qualification["failedAttempts"] == 0
            and qualification["unexpectedAgentToolNames"] == []
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
        raise RuntimeError(f"V9 artifact contract failed: {checks}")
    return {"status": "PASS", "checks": checks, "liveServiceCalls": 0}


def static_expectation_checks() -> dict[str, Any]:
    computed = {
        "qualification_alpha": workspace_partition(QUAL_RUN_ROOT / "actor-alpha"),
        "qualification_beta": workspace_partition(QUAL_RUN_ROOT / "actor-beta"),
        "measured_alpha": workspace_partition(RUN_ROOT / "actor-alpha"),
        "measured_beta": workspace_partition(RUN_ROOT / "actor-beta"),
    }
    if computed != EXPECTED_PARTITIONS:
        raise RuntimeError(f"V9 workspace partition mismatch: {computed}")
    return {
        "artifact_contract": validate_artifacts(),
        "v9_partitions": computed,
        "deterministic_temperature": v7.TEMPERATURE,
        "deterministic_seed": SEED,
        "configured_tools": [],
        "include_default_tools": EXPECTED_BUILTINS,
        "auto_attach_vision_inspect_tool": False,
        "semantic_hook_deadline_ms": 1000,
        "native_probe_process_timeout_seconds": parent.PROCESS_TIMEOUT_SECONDS,
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
