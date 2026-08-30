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
CANDIDATE_3 = CANDIDATE_3_PACKAGE / "e1_candidate3"
LAUNCH = CANDIDATE_3 / "live-launch-definition.json"
OPENHANDS_ROOT = Path("/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel")
OPENHANDS_PYTHON = OPENHANDS_ROOT / ".venv/bin/python"
sys.path.insert(0, str(S2_PACKAGE))
sys.path.insert(0, str(CANDIDATE_1_PACKAGE))
sys.path.insert(0, str(CANDIDATE_3_PACKAGE))

from e1.runner import verdict_vector  # noqa: E402
from e1_candidate3.runner import Candidate3Runner, run_candidate3_offline  # noqa: E402
from t1.model import FailureClass, HarnessFailure, OperationKey, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402
from t1.truth import ProductTruth  # noqa: E402


OPENHANDS_SOURCES = (
    OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/llm.py",
    OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/options/chat_options.py",
    OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/options/common.py",
    OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/mixins/non_native_fc.py",
    OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/mixins/fn_call_converter.py",
    OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/mixins/fn_call_examples.py",
)


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
    body = canonical_bytes(payload)
    changed.update({
        "requestBodyLength": len(body),
        "requestBodySha256": hashlib.sha256(body).hexdigest(),
        "requestBodyBase64": base64.b64encode(body).decode("ascii"),
    })
    return changed


def expect_rejected(runner: Candidate3Runner, row: Mapping[str, Any], observation: Any, detail: str) -> str:
    try:
        runner._decode_stub_request(row, observation)
    except HarnessFailure as exc:
        return exc.failure_class.value
    raise AssertionError(f"candidate-3 request mutation accepted: {detail}")


def wire_probe() -> dict[str, Any]:
    environment = dict(os.environ)
    environment["OPENHANDS_SUPPRESS_BANNER"] = "1"
    environment.pop("PYTHONPATH", None)
    completed = subprocess.run(
        [str(OPENHANDS_PYTHON), str(CANDIDATE_3 / "wire_probe.py")],
        cwd=ROOT,
        env=environment,
        capture_output=True,
        timeout=120,
        check=False,
    )
    if completed.returncode != 0:
        raise AssertionError(f"exact OpenHands wire probe failed: {completed.stderr.decode(errors='replace')}")
    return json.loads(completed.stdout)


def subprocess_controls() -> dict[str, Any]:
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    python = launch["environment"]["livePython"]
    environment = dict(os.environ)
    environment.pop("PYTHONPATH", None)
    environment.pop("PYTHONHOME", None)
    action = subprocess.run(
        [python, str(CANDIDATE_3 / "live_actions.py"), "--help"],
        cwd=ROOT,
        env=environment,
        capture_output=True,
        timeout=30,
        check=False,
    )
    if action.returncode != 0:
        raise AssertionError(f"candidate-3 raw action import failed: {action.stderr.decode(errors='replace')}")
    composition = subprocess.run(
        [python, str(CANDIDATE_3 / "live_entrypoint.py"), "--validate-only", "--definition", str(LAUNCH)],
        cwd=ROOT,
        env=environment,
        capture_output=True,
        timeout=300,
        check=False,
    )
    if composition.returncode != 0:
        raise AssertionError(f"candidate-3 composition validation failed: {composition.stderr.decode(errors='replace')}")
    return {
        "exactLivePython": python,
        "exactLiveWorkingDirectory": str(ROOT),
        "pythonPathInherited": False,
        "liveActionSubprocessImport": "PASS",
        "defaultDenyComposition": "PASS",
        "actionStdoutSha256": hashlib.sha256(action.stdout).hexdigest(),
        "compositionStdoutSha256": hashlib.sha256(composition.stdout).hexdigest(),
    }


def controls(truth: ProductTruth, results: list[Any], runner: Candidate3Runner) -> dict[str, Any]:
    baseline = results[10]
    observation = baseline.observation
    raw = next(
        receipt["row"]
        for receipt in observation.raw_receipts
        if receipt.get("kind") == "e1_model_audit_request_raw"
    )
    absent_stream = mutate_request(raw, lambda payload: payload.pop("stream", None))
    if runner._decode_stub_request(absent_stream, observation)["prompt"] != "What API timeout applies to repository alpha? Answer with the number and unit only.":
        raise AssertionError("absent-stream request did not retain exact task")
    explicit_false = mutate_request(raw, lambda payload: payload.__setitem__("stream", False))
    runner._decode_stub_request(explicit_false, observation)
    true_class = expect_rejected(
        runner,
        mutate_request(raw, lambda payload: payload.__setitem__("stream", True)),
        observation,
        "stream true",
    )
    tools_class = expect_rejected(
        runner,
        mutate_request(raw, lambda payload: payload.__setitem__("tools", [{"type": "function"}])),
        observation,
        "native tools",
    )
    thinking_class = expect_rejected(
        runner,
        mutate_request(raw, lambda payload: payload["chat_template_kwargs"].__setitem__("enable_thinking", True)),
        observation,
        "thinking enabled",
    )

    def alter_task(payload: dict[str, Any]) -> None:
        user = [message for message in payload["messages"] if message.get("role") == "user"][-1]
        content = user["content"]
        if isinstance(content, str):
            user["content"] = content.replace(
                "What API timeout applies", "What database timeout applies"
            )
        else:
            content[0]["text"] = content[0]["text"].replace(
                "What API timeout applies", "What database timeout applies"
            )

    prompt_class = expect_rejected(runner, mutate_request(raw, alter_task), observation, "wrapped task mutation")
    route = dict(raw)
    route["path"] = "/v1/completions"
    route_class = expect_rejected(runner, route, observation, "route mutation")
    operation = dict(raw)
    operation["operationKey"] = "SMA-E1-WRONG#001"
    operation_class = expect_rejected(runner, operation, observation, "operation identity mutation")
    retained = observation.raw_receipts[-1]
    if retained.get("kind") != "e1_model_audit_request_raw":
        raise AssertionError("failed request validation did not retain the sealed raw row")

    terminal = dict(next(
        receipt["row"]
        for receipt in observation.raw_receipts
        if receipt.get("kind") == "e1_model_audit_response_raw"
    ))
    response = json.loads(base64.b64decode(terminal["responseBodyBase64"], validate=True))
    response["choices"][0]["message"]["reasoning_content"] = "forbidden reasoning"
    body = canonical_bytes(response)
    terminal.update({
        "responseBodyLength": len(body),
        "responseBodySha256": hashlib.sha256(body).hexdigest(),
        "responseBodyBase64": base64.b64encode(body).decode("ascii"),
    })
    reasoning_rejected = False
    reasoning_failure: str | None = None
    try:
        runner._decode_stub_terminal(terminal, observation)
    except HarnessFailure as exc:
        reasoning_failure = f"{exc.failure_class.value}: {exc}"
        reasoning_rejected = exc.failure_class == FailureClass.SCIENTIFIC
    if not reasoning_rejected:
        raise AssertionError(
            "reasoning-bearing response did not fail scientifically: "
            f"{reasoning_failure or 'accepted'}"
        )

    by_case: dict[str, set[int]] = {}
    request_counts: dict[str, set[int]] = {}
    for result in results:
        case_id = result.observation.key.case_id
        by_case.setdefault(case_id, set()).add(result.observation.key.repetition)
        request_counts.setdefault(case_id, set()).add(len(result.observation.model_requests))
    if len(by_case) != 18:
        raise AssertionError("candidate-3 did not execute all 18 handlers")
    expected_counts = {
        "SMA-S2-013-FOUR-CHANNEL-CONCURRENCY": {4},
        "SMA-S2-014-FEEDBACK-LOOP-PREVENTION": {1},
        "SMA-S2-018-CONDENSATION-REANCHOR": {3},
    }
    for case_id, counts in request_counts.items():
        expected = expected_counts.get(case_id, {1})
        if counts != expected:
            raise AssertionError(f"candidate-3 model receipt cardinality drift: {case_id}:{counts}")

    source_hashes = {str(path): file_sha256(path) for path in OPENHANDS_SOURCES}
    return {
        "streamAbsentAcceptedAsOpenAICompatibleFalse": True,
        "streamExplicitFalseAccepted": True,
        "streamTrueRejectedAs": true_class,
        "wrappedTaskExtractedExactly": True,
        "wrappedTaskMutationRejectedAs": prompt_class,
        "nativeToolsRejectedAs": tools_class,
        "thinkingEnabledRejectedAs": thinking_class,
        "wrongRouteRejectedAs": route_class,
        "wrongOperationIdentityRejectedAs": operation_class,
        "failedWireValidationRetainsSealedRawRow": True,
        "reasoningContentRejectedScientifically": True,
        "handlerCoverage": {"cases": len(by_case), "repetitions": len(results)},
        "modelReceiptCardinalities": {key: sorted(value) for key, value in sorted(request_counts.items())},
        "installedOpenHandsSourceSha256": source_hashes,
        "exactOpenHandsWireProbe": wire_probe(),
        "liveLaunch": subprocess_controls(),
        "realModelCalls": 0,
        "productServicesStarted": 0,
        "databaseConnections": 0,
    }


def run() -> tuple[dict[str, Any], list[dict[str, Any]]]:
    truth = ProductTruth.load(ROOT)
    results, runner, _ = run_candidate3_offline(truth)
    records = [record(result) for result in results]
    if len(records) != 96:
        raise AssertionError(f"candidate-3 offline walk terminated early: {len(records)}/96")
    failed = [row["vector"]["operationKey"] for row in records if row["vector"]["overallRepetitionVerdict"] != "PASS"]
    if failed:
        raise AssertionError(f"candidate-3 offline vectors failed: {failed[:5]}")
    if not runner.ledger.verify():
        raise AssertionError("candidate-3 append-before-action ledger failed")
    checks = controls(truth, results, runner)
    predecessor = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-2-measured-execution/measured-execution-receipt.json"
    summary = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_3_OFFLINE_QUALIFICATION",
        "status": "PASS_OFFLINE_NOT_EXECUTION_AUTHORITY",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "predecessorPackageIdentity": "addf15192595f39f07594305f7eaa31bfb03fa771c601b6047de111d0292fd16",
        "predecessorMeasuredReceipt": {"path": str(predecessor.relative_to(ROOT)), "sha256": file_sha256(predecessor)},
        "changeScope": "HARNESS_WIRE_SEMANTICS_AND_PROMPT_FRAMING_CORRECTION_ONLY",
        "science": {"scenarios": 18, "repetitions": 96, "passed": 96, "failed": 0, "changed": False},
        "ledgerValid": True,
        "controls": checks,
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "prohibitedActivity": {"serviceStartOrRestart": "NOT_RUN", "openhandsConversation": "NOT_RUN", "realModelCall": "NOT_RUN", "measuredExecution": "NOT_RUN", "productionOrHistoricalDataAccess": "NOT_RUN"},
    }
    summary["canonicalOutcomeSha256"] = digest({
        "vectors": [row["vector"] for row in records],
        "controls": checks,
        "ledgerValid": True,
    })
    return summary, records


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output-directory")
    parser.add_argument("--optimized-child", action="store_true")
    args = parser.parse_args()
    summary, records = run()
    if not args.optimized_child:
        environment = dict(os.environ)
        child = subprocess.run(
            [sys.executable, "-O", str(Path(__file__).resolve()), "--optimized-child"],
            cwd=ROOT,
            env=environment,
            capture_output=True,
            timeout=600,
            check=False,
        )
        if child.returncode != 0:
            raise AssertionError(f"optimized candidate-3 qualification failed: {child.stderr.decode(errors='replace')}")
        optimized = json.loads(child.stdout)
        if optimized["canonicalOutcomeSha256"] != summary["canonicalOutcomeSha256"]:
            raise AssertionError("candidate-3 normal/optimized outcome mismatch")
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
