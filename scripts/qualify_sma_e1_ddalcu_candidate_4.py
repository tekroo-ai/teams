#!/usr/bin/env python3
from __future__ import annotations

import argparse
import base64
from dataclasses import asdict
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
from typing import Any, Mapping


ROOT = Path(__file__).resolve().parents[1]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
CANDIDATE_1_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
CANDIDATE_3_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-3"
CANDIDATE_4_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4"
CANDIDATE_4 = CANDIDATE_4_PACKAGE / "e1_candidate4"
LAUNCH = CANDIDATE_4 / "live-launch-definition.json"
PREREG = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-preregistration-candidate-1.json"
PREDECESSOR_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-3-measured-execution/measured-walk.jsonl"
SERVER_LOG = Path("/Users/paul/.local/share/tekroo-vllm-mlx/logs/server-ddalcu-batch.log")
for package in (S2_PACKAGE, CANDIDATE_1_PACKAGE, CANDIDATE_3_PACKAGE, CANDIDATE_4_PACKAGE):
    sys.path.insert(0, str(package))

from e1.runner import E1RunnerConfiguration, verdict_vector  # noqa: E402
from e1_candidate4.runner import (  # noqa: E402
    Candidate4OfflineWorld,
    Candidate4Runner,
    MAX_MODEL_CALLS_PER_INTERACTION,
    _replace_body,
    _request_payload,
    run_candidate4_offline,
)
from t1.model import FailureClass, HarnessFailure, OperationKey, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402
from t1.truth import ProductTruth  # noqa: E402


CASE2 = OperationKey("SMA-S2-002-SAME-PARTITION-DELIVERY", 1)
FROZEN_TASK = "What API timeout applies to repository alpha? Answer with the number and unit only."


def jsonable(value: Any) -> Any:
    if hasattr(value, "value") and isinstance(value.value, str):
        return value.value
    if isinstance(value, Mapping):
        return {str(key): jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [jsonable(item) for item in value]
    return value


def record(result: Any) -> dict[str, Any]:
    observation = jsonable(asdict(result.observation))
    return {
        "vector": verdict_vector(result),
        "observation": observation,
        "s2ScientificSupport": [jsonable(asdict(row)) for row in result.scientific],
        "evidence": [jsonable(asdict(row)) for row in result.evidence],
        "observationSha256": digest(observation),
    }


def write_exclusive(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(content)
        stream.flush()
        os.fsync(stream.fileno())


def mutate_request(row: Mapping[str, Any], mutation) -> dict[str, Any]:
    changed = dict(row)
    payload = json.loads(base64.b64decode(str(changed["requestBodyBase64"]), validate=True))
    mutation(payload)
    _replace_body(changed, payload)
    return changed


def expect_rejected(runner: Candidate4Runner, row: Mapping[str, Any], observation: Any, detail: str) -> str:
    try:
        runner._decode_stub_request(row, observation)
    except HarnessFailure as exc:
        return exc.failure_class.value
    raise AssertionError(f"candidate-4 request mutation accepted: {detail}")


def replace_task(value: Any) -> Any:
    if isinstance(value, str):
        return value.replace(FROZEN_TASK, "What database timeout applies to repository alpha?")
    if isinstance(value, list):
        return [replace_task(item) for item in value]
    if isinstance(value, dict):
        return {key: replace_task(item) for key, item in value.items()}
    return value


def run_world(world: Candidate4OfflineWorld):
    truth = ProductTruth.load(ROOT)
    runner = Candidate4Runner(truth, world, E1RunnerConfiguration(), RunMode.OFFLINE_MEASURED)
    world.current_key = CASE2
    return runner.run_operation(CASE2), runner


class MissingTaskWorld(Candidate4OfflineWorld):
    def _submit_prompt(self, conversation_id: str, prompt: str):
        result = super()._submit_prompt(conversation_id, prompt)
        if self.current_key == CASE2:
            for row in self.stub_raw:
                _replace_body(row, replace_task(_request_payload(row)))
        return result


class UnpairedWorld(Candidate4OfflineWorld):
    def _submit_prompt(self, conversation_id: str, prompt: str):
        result = super()._submit_prompt(conversation_id, prompt)
        if self.current_key == CASE2:
            self.stub_terminal.pop()
        return result


class OverflowWorld(Candidate4OfflineWorld):
    def _submit_prompt(self, conversation_id: str, prompt: str):
        result = super()._submit_prompt(conversation_id, prompt)
        if self.current_key == CASE2:
            raw = dict(self.stub_raw[0])
            terminal = dict(self.stub_terminal[0])
            while len(self.stub_raw) <= MAX_MODEL_CALLS_PER_INTERACTION:
                suffix = f"-overflow-{len(self.stub_raw):02d}"
                raw_copy = dict(raw)
                terminal_copy = dict(terminal)
                raw_copy["requestId"] = str(raw["requestId"]) + suffix
                terminal_copy["requestId"] = raw_copy["requestId"]
                self.stub_raw.append(raw_copy)
                self.stub_terminal.append(terminal_copy)
        return result


def fault_controls(truth: ProductTruth) -> dict[str, Any]:
    missing, _ = run_world(MissingTaskWorld(truth))
    if missing.observation.failure != FailureClass.HARNESS:
        raise AssertionError("missing frozen task did not fail as HARNESS")
    unpaired, _ = run_world(UnpairedWorld(truth))
    if unpaired.observation.failure != FailureClass.HARNESS:
        raise AssertionError("unpaired request/terminal identity did not fail as HARNESS")
    overflow, _ = run_world(OverflowWorld(truth))
    if overflow.observation.failure != FailureClass.SAFETY:
        raise AssertionError("model-call ceiling overflow did not fail as SAFETY")
    receipts = [row for row in overflow.observation.raw_receipts if row.get("kind") == "model_call_cardinality"]
    if not receipts or receipts[-1].get("observedCalls") != 13 or receipts[-1].get("withinBound") is not False:
        raise AssertionError("overflow did not retain diagnostic cardinality before rejection")
    return {
        "missingOriginalFrozenTask": missing.observation.failure.value,
        "unpairedRequestTerminalIdentity": unpaired.observation.failure.value,
        "modelCallCeilingOverflow": overflow.observation.failure.value,
        "overflowObservedCalls": receipts[-1]["observedCalls"],
        "overflowEvidenceRetainedBeforeReject": True,
    }


def historical_diagnosis() -> dict[str, Any]:
    rows = [json.loads(line) for line in PREDECESSOR_WALK.read_text(encoding="utf-8").splitlines() if line]
    failures = [
        row for row in rows
        if row["vector"]["operationKey"].startswith("SMA-S2-002-")
        and "exact=1 observed=3" in str(row["observation"].get("failure_detail"))
    ]
    if len(rows) != 14 or len(failures) != 4:
        raise AssertionError("candidate-3 historical failure shape changed")
    prereg = json.loads(PREREG.read_text(encoding="utf-8"))
    case2 = next(row for row in prereg["cases"] if row["s2OrchestrationCaseId"] == CASE2.case_id)
    if "17 seconds" not in case2["modelPredicate"] or any("call" in key.lower() and "count" in key.lower() for key in case2):
        raise AssertionError("accepted E1 case-2 science unexpectedly requires exact internal model-call cardinality")
    source = (S2_PACKAGE / "t1/runner.py").read_text(encoding="utf-8")
    if "_poll_jsonl(observation, self.config.stub_raw_path, expected_model, exact=True)" not in source:
        raise AssertionError("predecessor exact-count collector defect is not present at the bound source")
    log_text = SERVER_LOG.read_text(encoding="utf-8", errors="replace")
    think_count = log_text.count("EXECUTION RESULT of [think]")
    length_count = log_text.count("] [length]")
    if think_count < 4 or length_count < 4:
        raise AssertionError("real model log no longer supports the observed multi-call agent-loop diagnosis")
    return {
        "predecessorWalk": {"path": str(PREDECESSOR_WALK.relative_to(ROOT)), "sha256": file_sha256(PREDECESSOR_WALK), "records": len(rows)},
        "repeatedCase2ExactOneObservedThreeFailures": len(failures),
        "acceptedCase2ModelPredicate": case2["modelPredicate"],
        "acceptedCase2ExactInternalCallCount": False,
        "predecessorCollectorExactInternalCallCount": True,
        "serverLog": {"path": str(SERVER_LOG), "sha256AtAudit": file_sha256(SERVER_LOG), "thinkFollowupMarkers": think_count, "lengthFinishMarkers": length_count},
        "rootCause": "OFFLINE_FAKE_AND_COLLECTOR_SHARED_AN_INVALID_SINGLE_CALL_ASSUMPTION",
    }


def subprocess_controls() -> dict[str, Any]:
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    python = launch["environment"]["livePython"]
    environment = dict(os.environ)
    environment.pop("PYTHONPATH", None)
    environment.pop("PYTHONHOME", None)
    action = subprocess.run([python, str(CANDIDATE_4 / "live_actions.py"), "--help"], cwd=ROOT, env=environment, capture_output=True, timeout=30, check=False)
    if action.returncode != 0:
        raise AssertionError(f"candidate-4 raw action import failed: {action.stderr.decode(errors='replace')}")
    composition = subprocess.run([python, str(CANDIDATE_4 / "live_entrypoint.py"), "--validate-only", "--definition", str(LAUNCH)], cwd=ROOT, env=environment, capture_output=True, timeout=300, check=False)
    if composition.returncode != 0:
        raise AssertionError(f"candidate-4 composition validation failed: {composition.stderr.decode(errors='replace')}")
    return {
        "exactLivePython": python,
        "liveActionSubprocessImport": "PASS",
        "defaultDenyComposition": "PASS",
        "actionStdoutSha256": hashlib.sha256(action.stdout).hexdigest(),
        "compositionStdoutSha256": hashlib.sha256(composition.stdout).hexdigest(),
    }


def controls(truth: ProductTruth, results: list[Any], runner: Candidate4Runner) -> dict[str, Any]:
    baseline = results[10]
    observation = baseline.observation
    requests = [receipt["row"] for receipt in observation.raw_receipts if receipt.get("kind") == "e1_model_audit_request_raw"]
    terminals = [receipt["row"] for receipt in observation.raw_receipts if receipt.get("kind") == "e1_model_audit_response_raw"]
    if len(requests) != 3 or len(terminals) != 3:
        raise AssertionError("candidate-4 escaped-defect replay did not execute exactly three paired calls")
    decoded = [runner._decode_stub_request(row, observation) for row in requests]
    if not any(row.get("agentLoopRequest") for row in decoded):
        raise AssertionError("follow-up agent-loop request was not recognized")
    followup = requests[1]
    stream_class = expect_rejected(runner, mutate_request(followup, lambda payload: payload.__setitem__("stream", True)), observation, "stream true")
    thinking_class = expect_rejected(runner, mutate_request(followup, lambda payload: payload["chat_template_kwargs"].__setitem__("enable_thinking", True)), observation, "thinking enabled")
    route = dict(followup)
    route["path"] = "/v1/completions"
    route_class = expect_rejected(runner, route, observation, "route mutation")
    task_class = expect_rejected(runner, mutate_request(followup, lambda payload: payload.update(replace_task(payload))), observation, "original task missing from follow-up history")
    cardinalities: dict[str, set[tuple[int, int]]] = {}
    for result in results:
        case_id = result.observation.key.case_id
        cardinalities.setdefault(case_id, set()).add((len(result.observation.model_requests), len(result.observation.model_terminals)))
    expected_three = {"SMA-S2-002-SAME-PARTITION-DELIVERY", "SMA-S2-009-ADDITIONAL-CONTEXT-INTEGRITY", "SMA-S2-012-RESTART-CONTINUITY"}
    for case_id in expected_three:
        if cardinalities[case_id] != {(3, 3)}:
            raise AssertionError(f"candidate-4 three-call regression coverage missing: {case_id}")
    return {
        "historicalDiagnosis": historical_diagnosis(),
        "threeCallRegressionCases": sorted(expected_three),
        "followupRequestPreservesOriginalFrozenTask": True,
        "followupAgentLoopRecognized": True,
        "streamTrueRejectedAs": stream_class,
        "thinkingEnabledRejectedAs": thinking_class,
        "wrongRouteRejectedAs": route_class,
        "missingFrozenTaskRejectedAs": task_class,
        "requestTerminalPairing": "EXACT_IDENTITY_SET_REQUIRED",
        "modelCallBound": {"minimumPerLogicalInteraction": 1, "maximumPerLogicalInteraction": MAX_MODEL_CALLS_PER_INTERACTION, "exactCountRequired": False},
        "faultControls": fault_controls(truth),
        "handlerCoverage": {"cases": len(cardinalities), "repetitions": len(results)},
        "modelReceiptCardinalities": {key: [list(item) for item in sorted(value)] for key, value in sorted(cardinalities.items())},
        "liveLaunch": subprocess_controls(),
        "realPathCase2CanaryRequiredBeforeMeasuredExecution": True,
        "realModelCalls": 0,
        "productServicesStarted": 0,
        "databaseConnections": 0,
    }


def run() -> tuple[dict[str, Any], list[dict[str, Any]]]:
    truth = ProductTruth.load(ROOT)
    results, runner, _ = run_candidate4_offline(truth)
    records = [record(result) for result in results]
    if len(records) != 96:
        raise AssertionError(f"candidate-4 offline walk terminated early: {len(records)}/96")
    failed = [row["vector"]["operationKey"] for row in records if row["vector"]["overallRepetitionVerdict"] != "PASS"]
    if failed:
        raise AssertionError(f"candidate-4 offline vectors failed: {failed[:5]}")
    if not runner.ledger.verify():
        raise AssertionError("candidate-4 append-before-action ledger failed")
    checks = controls(truth, results, runner)
    summary = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_4_OFFLINE_QUALIFICATION",
        "status": "PASS_OFFLINE_REQUIRES_REAL_PATH_CASE2_CANARY_NOT_EXECUTION_AUTHORITY",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "predecessorPackageIdentity": "449665cb18ae5d168cf84e855545eb4a4fef3963ba274460c92cde236653b88e",
        "changeScope": "HARNESS_AGENT_LOOP_CARDINALITY_AND_FOLLOWUP_AUDIT_CORRECTION_ONLY",
        "science": {"scenarios": 18, "repetitions": 96, "passed": 96, "failed": 0, "changed": False},
        "ledgerValid": True,
        "controls": checks,
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "prohibitedActivity": {"serviceStartOrRestart": "NOT_RUN", "openhandsConversation": "NOT_RUN", "realModelCall": "NOT_RUN", "measuredExecution": "NOT_RUN", "productionOrHistoricalDataAccess": "NOT_RUN"},
    }
    summary["canonicalOutcomeSha256"] = digest({"vectors": [row["vector"] for row in records], "controls": checks, "ledgerValid": True})
    return summary, records


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output-directory")
    parser.add_argument("--optimized-child", action="store_true")
    args = parser.parse_args()
    summary, records = run()
    if not args.optimized_child:
        child = subprocess.run([sys.executable, "-O", str(Path(__file__).resolve()), "--optimized-child"], cwd=ROOT, env=dict(os.environ), capture_output=True, timeout=600, check=False)
        if child.returncode != 0:
            raise AssertionError(f"optimized candidate-4 qualification failed: {child.stderr.decode(errors='replace')}")
        optimized = json.loads(child.stdout)
        if optimized["canonicalOutcomeSha256"] != summary["canonicalOutcomeSha256"]:
            raise AssertionError("candidate-4 normal/optimized outcome mismatch")
        summary["optimizedEquivalent"] = True
    summary["receiptSha256"] = digest(summary)
    if args.output_directory:
        output = Path(args.output_directory).resolve()
        output.mkdir(parents=True, exist_ok=False)
        walk = output / "offline-walk.jsonl"
        write_exclusive(walk, b"".join(canonical_bytes(row) + b"\n" for row in records))
        summary["offlineWalk"] = {"path": str(walk), "sha256": file_sha256(walk), "records": len(records)}
        summary["receiptSha256"] = digest({key: value for key, value in summary.items() if key != "receiptSha256"})
        write_exclusive(output / "offline-qualification.json", canonical_bytes(summary) + b"\n")
    else:
        print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
