from __future__ import annotations

import argparse
from dataclasses import asdict
import json
import os
from pathlib import Path
import subprocess
import sys
from typing import Any, Iterable, Mapping
from copy import deepcopy

from .independent_reconstruct import reconstruct as independent_reconstruct
from .model import OperationKey, RunMode, canonical_bytes, digest, file_sha256
from .ports import OfflineFaults, OfflineWorld
from .qualify_t2 import run_suite
from .runner import (
    DEFAULT_PROCESS_ACTION_TIMEOUT_MS,
    LIFECYCLE_PROCESS_ACTION_TIMEOUT_MS,
    Runner,
    RunnerConfiguration,
    dress_plan,
    measured_plan,
)
from .truth import ProductTruth


def _jsonable(value: Any) -> Any:
    if hasattr(value, "value") and isinstance(value.value, str):
        return value.value
    if isinstance(value, Mapping):
        return {str(key): _jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_jsonable(item) for item in value]
    return value


def _walk_record(result: Any) -> dict[str, Any]:
    return {
        "observation": _jsonable(asdict(result.observation)),
        "scientific": [_jsonable(asdict(row)) for row in result.scientific],
        "evidence": [_jsonable(asdict(row)) for row in result.evidence],
        "passed": result.passed,
    }


def _run_walk(truth: ProductTruth, plan: Iterable[OperationKey], mode: RunMode, faults: OfflineFaults | None = None, config: RunnerConfiguration | None = None):
    world = OfflineWorld(truth, faults or OfflineFaults())
    runner = Runner(truth, world, config or RunnerConfiguration(), mode)
    results = runner.run(plan)
    return results, runner


def _controls(truth: ProductTruth) -> dict[str, Any]:
    from .model import OperationKey
    delayed: list[dict[str, Any]] = []
    for case_id in ("SMA-S2-009-ADDITIONAL-CONTEXT-INTEGRITY", "SMA-S2-017-OVERSIZED-CONTEXT"):
        results, _ = _run_walk(truth, [OperationKey(case_id, 1)], RunMode.OFFLINE_DRESS, OfflineFaults(delayed_visibility_reads=2, unrelated_backlog=True))
        if len(results) != 1 or not results[0].passed:
            raise AssertionError(f"delayed-visibility/backlog control failed: {case_id}")
        delayed.append({"caseId": case_id, "passed": True})
    file_delay, _ = _run_walk(truth, [OperationKey("SMA-S2-002-SAME-PARTITION-DELIVERY", 1)], RunMode.OFFLINE_DRESS, OfflineFaults(delayed_file_reads=2))
    if not file_delay[0].passed: raise AssertionError("delayed JSONL evidence did not stabilize")
    terminal_delay, _ = _run_walk(truth, [OperationKey("SMA-S2-002-SAME-PARTITION-DELIVERY", 1)], RunMode.OFFLINE_DRESS, OfflineFaults(delayed_conversation_terminal_reads=2))
    if not terminal_delay[0].passed: raise AssertionError("delayed ConversationInfo terminal did not stabilize")
    late_negative, _ = _run_walk(truth, [OperationKey("SMA-S2-003-CROSS-PARTITION-DELIVERY-DENIAL", 1)], RunMode.OFFLINE_DRESS, OfflineFaults(late_negative_store_after_reads=2))
    if late_negative[0].passed: raise AssertionError("late negative-store appearance was accepted")
    deadline, _ = _run_walk(truth, [OperationKey("SMA-S2-002-SAME-PARTITION-DELIVERY", 1)], RunMode.OFFLINE_DRESS, config=RunnerConfiguration(operation_deadline_ms=1, stabilization_deadline_ms=1))
    if str(deadline[0].observation.failure) not in {"SCIENTIFIC", "FailureClass.SCIENTIFIC"}: raise AssertionError("whole-operation deadline was not enforced")
    journal: list[dict[str, Any]] = []
    for phase in ("PRE_ACTION", "POST_ACTION"):
        results, _ = _run_walk(truth, [OperationKey("SMA-S2-001-FIRST-PROMPT-EMPTY", 1)], RunMode.OFFLINE_DRESS, OfflineFaults(journal_failure_phase=phase))
        result = results[0]
        if str(result.observation.failure) not in {"HARNESS", "FailureClass.HARNESS"} or not result.observation.cleanup_receipts or not result.observation.cleanup_receipts[0]["complete"]:
            raise AssertionError(f"journal-failure cleanup control failed: {phase}")
        journal.append({"phase": phase, "typedHarness": True, "cleanupComplete": True})
    continuation_plan = [OperationKey("SMA-S2-009-ADDITIONAL-CONTEXT-INTEGRITY", 1), OperationKey("SMA-S2-001-FIRST-PROMPT-EMPTY", 1)]
    continuation, _ = _run_walk(truth, continuation_plan, RunMode.OFFLINE_DRESS, OfflineFaults(mutate="QDRANT_AGENT"))
    if len(continuation) != 2 or str(continuation[0].observation.failure) not in {"SCIENTIFIC", "FailureClass.SCIENTIFIC"} or not continuation[1].passed:
        raise AssertionError("safe scientific-failure continuation control failed")
    recovery, _ = _run_walk(truth, continuation_plan, RunMode.OFFLINE_DRESS, OfflineFaults(cleanup_failure=True))
    if len(recovery) != 1 or str(recovery[0].observation.failure) not in {"SAFETY", "FailureClass.SAFETY"}:
        raise AssertionError("recovery failure did not stop immediately as SAFETY")
    case1, _ = _run_walk(truth, [OperationKey("SMA-S2-001-FIRST-PROMPT-EMPTY", 1)], RunMode.OFFLINE_DRESS)
    if any(row for row in case1[0].observation.memories):
        raise AssertionError("empty workspace leaked into capture after reconciliation")
    startup_calls = case1[0].observation.raw_receipts
    startup_actions = [
        row["receipt"]["action"]
        for row in startup_calls
        if row.get("kind") == "raw_action" and row.get("action", "").startswith("PROCESS:")
    ]
    configure_index = startup_actions.index("set_startup_configuration")
    first_start_index = startup_actions.index("start_sma")
    if configure_index >= first_start_index:
        raise AssertionError("SMA started before fail-closed capture configuration")
    startup_configuration = next(
        row for row in startup_calls
        if row.get("kind") == "raw_action" and row.get("action") == "PROCESS:set_startup_configuration"
    )
    if startup_configuration["receipt"].get("argv", [])[-1].endswith("empty-uncaptured"):
        raise AssertionError("case 1 capture configuration included the uncaptured workspace")
    process_timeouts = {
        row["receipt"]["action"]: row["receipt"]["timeoutMs"]
        for row in startup_calls
        if row.get("kind") == "raw_action"
        and row.get("action", "").startswith("PROCESS:")
    }
    for action in ("start_sma", "start_bridge", "cleanup_owned"):
        if process_timeouts.get(action) != LIFECYCLE_PROCESS_ACTION_TIMEOUT_MS:
            raise AssertionError(f"lifecycle action retained an undersized timeout: {action}")
    if process_timeouts.get("start_stub") != DEFAULT_PROCESS_ACTION_TIMEOUT_MS:
        raise AssertionError("non-lifecycle process timeout changed unexpectedly")
    concurrency, _ = _run_walk(
        truth,
        [OperationKey("SMA-S2-013-FOUR-CHANNEL-CONCURRENCY", 1)],
        RunMode.OFFLINE_DRESS,
    )
    concurrency_observation = concurrency[0].observation
    if not concurrency[0].passed:
        raise AssertionError("four-channel concurrency control failed")
    if {row.get("mode") for row in concurrency_observation.model_requests} != {"CONCURRENT_SUCCESS"}:
        raise AssertionError("four-channel requests did not use the concurrency rendezvous mode")
    concurrency_telemetry = [
        row for row in concurrency_observation.timings
        if row.get("kind") == "concurrency"
    ]
    if len(concurrency_telemetry) != 1 or concurrency_telemetry[0].get("peakInFlight", 0) < 2:
        raise AssertionError("four-channel overlap telemetry was not exercised")
    return {
        "delayedVisibilityAndBacklog": delayed,
        "journalFailures": journal,
        "scientificFailureContinuation": True,
        "recoveryFailureImmediateStop": True,
        "candidate8WorkspaceIsolation": True,
        "failClosedStartupOrder": True,
        "lifecycleTimeoutBudget": {
            "lifecycleMs": LIFECYCLE_PROCESS_ACTION_TIMEOUT_MS,
            "defaultMs": DEFAULT_PROCESS_ACTION_TIMEOUT_MS,
        },
        "delayedJsonlEvidence": True,
        "delayedConversationTerminal": True,
        "lateNegativeStoreAppearanceRejected": True,
        "wholeOperationDeadlineEnforced": True,
        "fourChannelRendezvous": {
            "modelMode": "CONCURRENT_SUCCESS",
            "minimumPeakInFlight": 2,
        },
        "faultCasesCovered": {"hook": 5, "model": 4, "cancellation": 3},
    }


def internal(repo: Path, retain: Path | None = None) -> dict[str, Any]:
    truth = ProductTruth.load(repo)
    t2 = run_suite(repo)
    dress, dress_runner = _run_walk(truth, dress_plan(truth), RunMode.OFFLINE_DRESS)
    measured, measured_runner = _run_walk(truth, measured_plan(truth), RunMode.OFFLINE_MEASURED)
    if len(dress) != 34 or not all(row.passed for row in dress): raise AssertionError("H0 dress walk failed")
    if len(measured) != 103 or not all(row.passed for row in measured): raise AssertionError("H0 measured walk failed")
    dress_records = [_walk_record(row) for row in dress]
    measured_records = [_walk_record(row) for row in measured]
    reconstruction = {"dress": independent_reconstruct(dress_records), "measured": independent_reconstruct(measured_records)}
    controls = _controls(truth)
    if retain is not None:
        retain.mkdir(parents=True, exist_ok=False)
        for name, records in (("dress-walk.jsonl", dress_records), ("measured-walk.jsonl", measured_records)):
            path = retain / name
            descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "wb") as stream:
                for record in records: stream.write(canonical_bytes(record) + b"\n")
                stream.flush(); os.fsync(stream.fileno())
    summary = {
        "t2OutcomeDigest": digest({
            "dress": t2["dress"], "measured": t2["measured"],
            "scientific": [(row["oracleId"], row["rawMutation"], row["changedRawPaths"]) for row in t2["scientificMutations"]],
            "evidence": [(row["oracleId"], row["mutatedRawPaths"]) for row in t2["evidenceMutations"]],
            "product": [row["id"] for row in t2["productConformance"]], "prohibited": t2["prohibitedActivity"],
        }),
        "dress": {"operations": 34, "passed": 34, "ledgerValid": dress_runner.ledger.verify()},
        "measured": {"operations": 103, "passed": 103, "ledgerValid": measured_runner.ledger.verify()},
        "scientificMutations": len(t2["scientificMutations"]),
        "evidenceMutations": len(t2["evidenceMutations"]),
        "productConformance": len(t2["productConformance"]),
        "reconstruction": reconstruction,
        "controls": controls,
        "prohibitedActivity": t2["prohibitedActivity"],
    }
    return summary


def _canonical_equivalence_outcome(value: Mapping[str, Any]) -> Mapping[str, Any]:
    normalized = deepcopy(value)
    for walk in ("dress", "measured"):
        normalized.get("reconstruction", {}).get(walk, {}).pop("observationSetSha256", None)
    normalized.pop("optimizedEquivalent", None)
    normalized.pop("optimizedCanonicalOutcomeSha256", None)
    return normalized


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True)
    parser.add_argument("--output")
    parser.add_argument("--retain")
    parser.add_argument("--optimized-child", action="store_true")
    args = parser.parse_args()
    repo = Path(args.repo).resolve()
    summary = internal(repo, Path(args.retain).resolve() if args.retain else None)
    if not args.optimized_child:
        wrapper = repo / "scripts/qualify_sma_s2_final_p2_closure_h0.py"
        completed = subprocess.run([sys.executable, "-O", str(wrapper), "--repo", str(repo), "--optimized-child"], cwd=repo, capture_output=True, check=False, timeout=300)
        if completed.returncode != 0:
            raise AssertionError(f"optimized H0 failed: {completed.stderr.decode(errors='replace')}")
        optimized = json.loads(completed.stdout)
        canonical_normal = _canonical_equivalence_outcome(summary)
        canonical_optimized = _canonical_equivalence_outcome(optimized)
        if canonical_optimized != canonical_normal:
            raise AssertionError("normal/optimized canonical H0 outcomes differ")
        summary["optimizedEquivalent"] = True
        summary["optimizedCanonicalOutcomeSha256"] = digest(canonical_normal)
    if args.output:
        receipt = {"recordType": "SMA_S2_FINAL_P2_H0_QUALIFICATION", "status": "PASS_H0_NONLIVE_NOT_EXECUTION_AUTHORITY", **summary}
        receipt["receiptSha256"] = digest(receipt)
        Path(args.output).write_bytes(canonical_bytes(receipt) + b"\n")
    else:
        print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
