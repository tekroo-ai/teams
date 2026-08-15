#!/usr/bin/env python3
"""Telemetry-only corrective runner for frozen SMA-Q1 Step 15 V5.

This runner preserves V5 calls, order, criteria, corpus, and outcomes. It adds
content-free qualification response telemetry and one bridge snapshot only
after all three existing qualification retrieval outcomes have occurred. It
requires a separate content-addressed execution authorization.
"""

from __future__ import annotations

import importlib.util
import json
import sys
import time
from pathlib import Path
from typing import Any


TEAMS = Path("/Users/paul/work/tekroo-ai/teams")
PARENT_RUNNER = TEAMS / "scripts/smaq1_native_step15_v5.py"
IDENTITY = TEAMS / "investigations/sma-q1/step-15-execution-identity-v5-rerun1.json"
AUTHORIZATION = TEAMS / "investigations/sma-q1/step-15-execution-authorization-v5-rerun1.json"
OUTPUT = TEAMS / "OUTPUT/phase-3/sma-q1n-step15-v5-rerun1"


def load_parent():
    spec = importlib.util.spec_from_file_location("smaq1_v5_corrective_parent", PARENT_RUNNER)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load frozen V5 runner")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


v5 = load_parent()
v4 = v5.v4


def direct_context_observed(conversation: str, workspace: Path, prompt: str) -> dict[str, Any]:
    started = time.monotonic_ns()
    status, payload = v4.http(
        "POST", v4.support.BRIDGE, "/v1/openhands/context", body={
            "session_id": conversation,
            "working_dir": str(workspace),
            "prompt": prompt,
        }, timeout=2,
    )
    elapsed = (time.monotonic_ns() - started) / 1_000_000.0
    if status != 200:
        raise RuntimeError(f"bridge context returned HTTP {status}")
    context = payload.get("context_block") or ""
    return {
        "elapsed_ms": elapsed,
        "http_status": status,
        "hit_count": int(payload.get("hit_count", 0)),
        "memory_ids": [str(item.get("memory_id")) for item in payload.get("memories", [])],
        "context_length": len(context),
        "context_sha256": v4.sha_text(context),
        "trace_id": payload.get("trace_id"),
        "agent_id": payload.get("agent_id"),
        "workspace_fingerprint": payload.get("workspace_fingerprint"),
        "profile": payload.get("profile"),
        "decision": payload.get("decision"),
        "over_capacity": bool(payload.get("over_capacity", False)),
        "context": context,
    }


def safe_direct_receipt(result: dict[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in result.items() if key != "context"}


def qualify_fixture(corpus: list[dict[str, Any]], markers: dict[str, str],
                    journal: Any) -> dict[str, Any]:
    measured_namespace = v4.current_namespace()
    qualification_conversations: list[str] = []
    qualified = False
    cleanup_result: dict[str, Any] | None = None
    v4.activate_namespace(
        v4.QUAL_RUN_ROOT, v4.QUAL_DATABASE, v4.QUAL_SEMANTIC,
        v4.QUAL_EPISODIC, v4.QUAL_LABEL,
    )
    try:
        absent = {
            "service_absent": not v4.support.launch_identity(v4.LABEL)["loaded"],
            "bridge_absent": v4.support.listener_pid(8130) is None,
            "mongo_absent": not v4.support.mongo_database_exists(v4.DATABASE),
            "semantic_absent": not v4.support.qdrant_collection_exists(v4.SEMANTIC),
            "episodic_absent": not v4.support.qdrant_collection_exists(v4.EPISODIC),
            "run_root_absent": not v4.RUN_ROOT.exists(),
        }
        journal.append("QUALIFICATION_PREFLIGHT", absent)
        if not all(absent.values()):
            raise RuntimeError(f"qualification namespace not empty: {absent}")
        for workspace in (v4.ALPHA, v4.BETA):
            workspace.mkdir(parents=True, exist_ok=False)
            (workspace / ".openhands").symlink_to(v4.SMA / ".openhands", target_is_directory=True)
        mapped = v4.seed_corpus(
            corpus, qualification_conversations, journal, "QUALIFICATION_",
        )

        # These are the same three calls, in the same order, as frozen V5.
        same_partition = direct_context_observed(
            qualification_conversations[0], v4.ALPHA,
            "What API timeout applies to repository alpha?",
        )
        cross_partition = direct_context_observed(
            qualification_conversations[-1], v4.BETA,
            "What API timeout applies to repository alpha?",
        )
        raw_ineligible = direct_context_observed(
            qualification_conversations[2], v4.ALPHA,
            "Which secret port does repository alpha use?",
        )

        # This first additional service query occurs only after all three
        # qualification outcomes, so it cannot affect any of them.
        terminal_bridge_metrics = v4.support.bridge_health()
        retrieval_receipt = {
            "same_partition": safe_direct_receipt(same_partition),
            "cross_partition": safe_direct_receipt(cross_partition),
            "raw_ineligible": safe_direct_receipt(raw_ineligible),
            "terminal_bridge_metrics": terminal_bridge_metrics,
            "telemetry_only_correction": True,
            "existing_retrieval_call_count": 3,
            "added_retrieval_call_count": 0,
            "warmup_retry_or_delay_added": False,
        }
        journal.append("QUALIFICATION_RETRIEVAL_OBSERVED", retrieval_receipt)

        # Frozen V5 criteria, unchanged.
        if mapped["mem-alpha-timeout"] not in same_partition["memory_ids"]:
            raise RuntimeError("qualification same-partition retrieval missed expected memory")
        if mapped["mem-alpha-timeout"] in cross_partition["memory_ids"]:
            raise RuntimeError("qualification cross-partition retrieval leaked memory")
        if mapped["mem-alpha-raw"] in raw_ineligible["memory_ids"]:
            raise RuntimeError("qualification retrieved raw ineligible memory")
        qualified = True
        journal.append("QUALIFICATION_TERMINAL", {"status": "PASS"})
    except Exception as error:
        journal.append("QUALIFICATION_TERMINAL", {
            "status": "INCONCLUSIVE",
            "error_type": type(error).__name__,
            "message_sha256": v4.sha_text(str(error)),
        })
        raise
    finally:
        cleanup_result = v4.cleanup(
            qualification_conversations, journal, "QUALIFICATION_CLEANUP",
        )
        v4.activate_namespace(*measured_namespace)
    measured_absent = {
        "service_absent": not v4.support.launch_identity(v4.LABEL)["loaded"],
        "bridge_absent": v4.support.listener_pid(8130) is None,
        "mongo_absent": not v4.support.mongo_database_exists(v4.DATABASE),
        "semantic_absent": not v4.support.qdrant_collection_exists(v4.SEMANTIC),
        "episodic_absent": not v4.support.qdrant_collection_exists(v4.EPISODIC),
        "run_root_absent": not v4.RUN_ROOT.exists(),
    }
    journal.append("MEASURED_NAMESPACE_RECHECK", measured_absent)
    if not all(measured_absent.values()):
        raise RuntimeError(f"measured namespace not empty after qualification: {measured_absent}")
    return {
        "qualified_fixture": qualified,
        "qualification_cleanup_verified": (
            bool(cleanup_result) and all(cleanup_result["verified"].values())
        ),
        "measured_namespace_empty": all(measured_absent.values()),
    }


def configure_corrective_runner() -> None:
    v5.__file__ = str(Path(__file__))
    v5.IDENTITY = IDENTITY
    v5.AUTHORIZATION = AUTHORIZATION
    v5.OUTPUT = OUTPUT
    v5.configure_inherited_runner()
    v4.__file__ = str(Path(__file__))
    v4.IDENTITY = IDENTITY
    v4.AUTHORIZATION = AUTHORIZATION
    v4.OUTPUT = OUTPUT
    v4.JOURNAL = OUTPUT / "raw-receipts.jsonl"
    v4.SUMMARY = OUTPUT / "execution-receipt.json"
    v4.qualify_fixture = qualify_fixture


configure_corrective_runner()


def main() -> None:
    if "--static-self-test" in sys.argv:
        receipt = v5.static_expectation_checks()
        receipt["corrective_runner"] = {
            "parent_runner_sha256": v5.sha_file(PARENT_RUNNER),
            "telemetry_only": True,
            "added_retrieval_calls": 0,
            "warmup_retry_or_delay_added": False,
        }
        print(json.dumps(receipt, indent=2, sort_keys=True))
        return
    v4.main()


if __name__ == "__main__":
    main()
