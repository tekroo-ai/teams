#!/usr/bin/env python3
"""Single-attempt SMA-Q1 Step 15 V6 runner with offline preflight contract.

V6 preserves the frozen scientific corpus and V5 telemetry-only qualification.
Before any execution authorization is usable, the exact artifacts must traverse
the runtime preflight field-access path up to a sentinel replacing the first
live-service operation.
"""

from __future__ import annotations

import hashlib
import importlib.util
import json
import sys
from pathlib import Path
from typing import Any


TEAMS = Path("/Users/paul/work/tekroo-ai/teams")
SMA = Path("/Users/paul/work/tekroo-ai/sma-step15")
PARENT = TEAMS / "scripts/smaq1_native_step15_v5_rerun1.py"
MANIFEST = TEAMS / "investigations/sma-q1/preregistration-native-v6.json"
IDENTITY = TEAMS / "investigations/sma-q1/step-15-execution-identity-v6.json"
AUTHORIZATION_TEMPLATE = TEAMS / "investigations/sma-q1/step-15-execution-authorization-v6.template.json"
AUTHORIZATION = TEAMS / "investigations/sma-q1/step-15-execution-authorization-v6.json"
OUTPUT = TEAMS / "OUTPUT/phase-3/sma-q1n-step15-v6"
RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v6"
QUAL_RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v6-fixture"
MANIFEST_SHA = "3b025b9adf135918ec0d955ef6cb59bbd46f26aec963611b1f846a2c0f15037e"
EXPECTED_PARTITIONS = {
    "qualification_alpha": "openhands:6bebf627d656f88fbc585279:default",
    "qualification_beta": "openhands:5c48db35135e7bd1e1b448ad:default",
    "measured_alpha": "openhands:feaf8bfe0cb7f5752b657132:default",
    "measured_beta": "openhands:bea92737d4d6748ec0fe7f26:default",
}


def load_parent():
    spec = importlib.util.spec_from_file_location("smaq1_v6_parent", PARENT)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load sealed V5 telemetry runner")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


parent = load_parent()
v5 = parent.v5
v4 = parent.v4


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def configure_v6() -> None:
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
    v4.LABEL = "com.tekroo.sma-service-smaq1n-step15-v6"
    v4.DATABASE = "sma_q1n_step15_20260813_v6"
    v4.SEMANTIC = "sma_q1n_step15_v6_semantic_20260813"
    v4.EPISODIC = "sma_q1n_step15_v6_episodic_20260813"
    v4.QUAL_RUN_ROOT = QUAL_RUN_ROOT
    v4.QUAL_DATABASE = "sma_q1n_step15_v6_fixture_20260813"
    v4.QUAL_SEMANTIC = "sma_q1n_step15_v6_fixture_semantic_20260813"
    v4.QUAL_EPISODIC = "sma_q1n_step15_v6_fixture_episodic_20260813"
    v4.QUAL_LABEL = "com.tekroo.sma-service-smaq1n-step15-v6-fixture"
    v4.ALPHA_PARTITION = v4.workspace_partition(v4.ALPHA)
    v4.BETA_PARTITION = v4.workspace_partition(v4.BETA)
    v4.support.SMA_LABEL = v4.LABEL
    v4.qualify_fixture = parent.qualify_fixture


configure_v6()


class OfflineBoundaryReached(RuntimeError):
    pass


class NullJournal:
    def append(self, _kind: str, _data: dict[str, Any]) -> None:
        raise AssertionError("offline preflight must stop before journal append")


def validate_artifact_hashes(authorization_path: Path,
                             require_authorized: bool) -> dict[str, Any]:
    manifest = json.loads(MANIFEST.read_text())
    identity = json.loads(IDENTITY.read_text())
    authorization = json.loads(authorization_path.read_text())
    checks = {
        "manifest_hash": sha_file(MANIFEST),
        "manifest_hash_matches": sha_file(MANIFEST) == MANIFEST_SHA,
        "manifest_scenario_count": int(manifest["unchangedBase"]["scenarioCount"]),
        "manifest_repetition_count": int(manifest["unchangedBase"]["repetitionCount"]),
        "identity_manifest_matches": identity["frozenExperiment"]["manifestSHA256"] == MANIFEST_SHA,
        "identity_runner_matches": identity["harness"]["sha256"] == sha_file(Path(__file__)),
        "identity_freeze_field_present": isinstance(identity["executionFence"]["freezeAccepted"], bool),
        "identity_freeze_matches_request": bool(identity["executionFence"]["freezeAccepted"]) == require_authorized,
        "identity_execution_authorized_field_present": isinstance(identity["executionAuthorized"], bool),
        "authorization_manifest_matches": authorization["frozenManifestSHA256"] == MANIFEST_SHA,
        "authorization_identity_matches": authorization["executionIdentitySHA256"] == sha_file(IDENTITY),
        "authorization_runner_matches": authorization["harnessSHA256"] == sha_file(Path(__file__)),
        "authorization_scope_shape": (
            isinstance(authorization["authorizedScope"]["fixtureQualification"], bool)
            and isinstance(authorization["authorizedScope"]["measuredScenarioCorpus"], bool)
            and int(authorization["authorizedScope"]["scenarioCount"]) == 18
            and int(authorization["authorizedScope"]["repetitionCount"]) == 96
        ),
        "authorization_state_matches_request": (
            bool(authorization["executionAuthorized"]) == require_authorized
        ),
    }
    if not all(value for key, value in checks.items()
               if key not in ("manifest_hash", "manifest_scenario_count", "manifest_repetition_count")):
        raise RuntimeError(f"offline artifact hash/shape contract failed: {checks}")
    if checks["manifest_scenario_count"] != 18 or checks["manifest_repetition_count"] != 96:
        raise RuntimeError(f"offline corpus cardinality contract failed: {checks}")
    return checks


def offline_preflight_contract(authorization_path: Path,
                               require_authorized: bool) -> dict[str, Any]:
    hashes = validate_artifact_hashes(authorization_path, require_authorized)
    original_v5_authorization = v5.AUTHORIZATION
    original_v4_authorization = v4.AUTHORIZATION
    original_launch_identity = v4.support.launch_identity

    def stop_at_first_live_operation(_label: str):
        raise OfflineBoundaryReached("OFFLINE_PREFLIGHT_REACHED_FIRST_LIVE_SERVICE_OPERATION")

    v5.AUTHORIZATION = authorization_path
    v4.AUTHORIZATION = authorization_path
    v4.support.launch_identity = stop_at_first_live_operation
    try:
        try:
            v5.preflight(NullJournal())
        except OfflineBoundaryReached as boundary:
            if str(boundary) != "OFFLINE_PREFLIGHT_REACHED_FIRST_LIVE_SERVICE_OPERATION":
                raise
        else:
            raise AssertionError("offline preflight crossed the live-service boundary")
    finally:
        v4.support.launch_identity = original_launch_identity
        v5.AUTHORIZATION = original_v5_authorization
        v4.AUTHORIZATION = original_v4_authorization
    return {
        "status": "PASS",
        "boundary": "OFFLINE_PREFLIGHT_REACHED_FIRST_LIVE_SERVICE_OPERATION",
        "authorization_artifact": str(authorization_path),
        "authorization_required_state": require_authorized,
        "hash_and_shape_checks": hashes,
        "live_service_calls": 0,
    }


def inherited_static_checks() -> dict[str, Any]:
    saved_run_root = v5.RUN_ROOT
    saved_qual_root = v5.QUAL_RUN_ROOT
    try:
        v5.RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v5"
        v5.QUAL_RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v5-fixture"
        return v5.static_expectation_checks()
    finally:
        v5.RUN_ROOT = saved_run_root
        v5.QUAL_RUN_ROOT = saved_qual_root


def runtime_static_checks() -> dict[str, Any]:
    inherited = inherited_static_checks()
    computed_partitions = {
        "qualification_alpha": v4.workspace_partition(QUAL_RUN_ROOT / "actor-alpha"),
        "qualification_beta": v4.workspace_partition(QUAL_RUN_ROOT / "actor-beta"),
        "measured_alpha": v4.workspace_partition(RUN_ROOT / "actor-alpha"),
        "measured_beta": v4.workspace_partition(RUN_ROOT / "actor-beta"),
    }
    if computed_partitions != EXPECTED_PARTITIONS:
        raise RuntimeError(f"V6 workspace partition mismatch: {computed_partitions}")
    return {
        "inherited_v5_checks": inherited,
        "v6_partitions": computed_partitions,
        "v6_partitions_match": True,
        "offline_preflight_contract": offline_preflight_contract(
            AUTHORIZATION, require_authorized=True,
        ),
    }


v4.static_expectation_checks = runtime_static_checks


def static_candidate_check() -> dict[str, Any]:
    inherited = inherited_static_checks()
    computed_partitions = {
        "qualification_alpha": v4.workspace_partition(QUAL_RUN_ROOT / "actor-alpha"),
        "qualification_beta": v4.workspace_partition(QUAL_RUN_ROOT / "actor-beta"),
        "measured_alpha": v4.workspace_partition(RUN_ROOT / "actor-alpha"),
        "measured_beta": v4.workspace_partition(RUN_ROOT / "actor-beta"),
    }
    if computed_partitions != EXPECTED_PARTITIONS:
        raise RuntimeError(f"V6 workspace partition mismatch: {computed_partitions}")
    return {
        "status": "PASS",
        "inherited_v5_checks": inherited,
        "v6_partitions": computed_partitions,
        "offline_preflight_contract": offline_preflight_contract(
            AUTHORIZATION_TEMPLATE, require_authorized=False,
        ),
    }


def main() -> None:
    if "--static-self-test" in sys.argv:
        print(json.dumps(static_candidate_check(), indent=2, sort_keys=True))
        return
    if "--authorization-contract-test" in sys.argv:
        print(json.dumps(offline_preflight_contract(
            AUTHORIZATION, require_authorized=True,
        ), indent=2, sort_keys=True))
        return
    offline_preflight_contract(AUTHORIZATION, require_authorized=True)
    v4.main()


if __name__ == "__main__":
    main()
