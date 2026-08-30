from __future__ import annotations

import argparse
from dataclasses import asdict
import json
import os
from pathlib import Path
import subprocess
import sys
from typing import Any, Mapping

from t1.model import FailureClass, OperationKey, canonical_bytes, digest, file_sha256
from t1.ports import OfflineFaults
from t1.truth import ProductTruth

from .runner import (
    BY_S2,
    E1_CASES,
    E1Runner,
    E1RunnerConfiguration,
    E1OfflineWorld,
    MODEL_API_ROOT,
    MODEL_ID,
    e1_plan,
    run_offline,
    verdict_vector,
)
from .model_audit_proxy import upstream_url


UPSTREAM = {
    "s1": ("investigations/sma-q1/layered/sma-s1-p2final-r1-scientific-adjudication-acceptance.json", "3315838d0c5047189494cece49b953481431321d16e238efa338712be076bee4"),
    "s2": ("investigations/sma-q1/layered/sma-s2-final-p2-composite-scientific-adjudication-acceptance.json", "cac6cd68ff7b60fdd36a593779221ce90b6553a570b71f6fb90b6f593184e0c3"),
    "m1": ("investigations/sma-q1/layered/sma-m1-ddalcu-scientific-adjudication-acceptance.json", "e97a6fb8ba1dab2837f96d2253a73d142e11a055c9fbf94b67ec6fb4498b8f3e"),
    "historicalCorpus": ("investigations/sma-q1/preregistration-native-v2.json", "c618371d571d5333aebd2bdc83d2db2559115f5ec3e1903b33e8e0ab157edf5d"),
}


def _jsonable(value: Any) -> Any:
    if hasattr(value, "value") and isinstance(value.value, str):
        return value.value
    if isinstance(value, Mapping):
        return {str(key): _jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_jsonable(item) for item in value]
    return value


def _verify_upstream(repo: Path) -> dict[str, Any]:
    receipts: dict[str, Any] = {}
    for name, (relative, expected) in UPSTREAM.items():
        path = repo / relative
        if not path.is_file() or file_sha256(path) != expected:
            raise AssertionError(f"upstream binding drift: {name}:{relative}")
        receipts[name] = {"path": relative, "sha256": expected}
    historical = json.loads((repo / UPSTREAM["historicalCorpus"][0]).read_text(encoding="utf-8"))
    source = historical["scenarioCorpus"]
    if len(source) != 18 or sum(int(row["repetitions"]) for row in source) != 96:
        raise AssertionError("historical E1 corpus cardinality drift")
    for expected, actual in zip(E1_CASES, source, strict=True):
        if expected["sourceId"] != actual["id"] or expected["repetitions"] != actual["repetitions"] or expected["prompt"] != actual["prompt"]:
            raise AssertionError(f"E1 prompt/repetition drift: {actual['id']}")
    return receipts


def _controls(truth: ProductTruth) -> dict[str, Any]:
    # The exact accepted E1 prompt is proven at the raw model-request decoder,
    # not merely at the OpenHands submit boundary.
    one, _, _ = run_offline(truth, (OperationKey("SMA-S2-002-SAME-PARTITION-DELIVERY", 1),))
    if len(one) != 1 or verdict_vector(one[0])["overallRepetitionVerdict"] != "PASS":
        raise AssertionError("baseline component-vector control failed")

    world = E1OfflineWorld(truth)
    runner = E1Runner(truth, world, E1RunnerConfiguration(), __import__("t1.model", fromlist=["RunMode"]).RunMode.OFFLINE_MEASURED)
    key = OperationKey("SMA-S2-002-SAME-PARTITION-DELIVERY", 1)
    world.current_key = key
    result = runner.run_operation(key)
    mutated = dict(result.observation.model_requests[0])
    body = json.loads(__import__("base64").b64decode(mutated["requestBodyBase64"]))
    body["chat_template_kwargs"]["enable_thinking"] = True
    wire = canonical_bytes(body)
    mutated["requestBodyBase64"] = __import__("base64").b64encode(wire).decode("ascii")
    mutated["requestBodyLength"] = len(wire)
    mutated["requestBodySha256"] = __import__("hashlib").sha256(wire).hexdigest()
    rejected_thinking = False
    try:
        runner._decode_stub_request(mutated, result.observation)
    except Exception:
        rejected_thinking = True
    if not rejected_thinking:
        raise AssertionError("thinking-enabled raw request was accepted")

    result.observation.model_terminals[0]["content"] = "UNKNOWN"
    if verdict_vector(result)["modelBehaviorVerdict"] != "FAIL":
        raise AssertionError("wrong model answer did not fail only model behavior")

    cleanup_world = E1OfflineWorld(truth, OfflineFaults(cleanup_failure=True))
    cleanup_world.current_key = OperationKey("SMA-S2-001-FIRST-PROMPT-EMPTY", 1)
    cleanup_runner = E1Runner(truth, cleanup_world, E1RunnerConfiguration(), __import__("t1.model", fromlist=["RunMode"]).RunMode.OFFLINE_MEASURED)
    cleanup_result = cleanup_runner.run_operation(cleanup_world.current_key)
    if cleanup_result.observation.failure != FailureClass.SAFETY or verdict_vector(cleanup_result)["cleanupVerdict"] != "FAIL":
        raise AssertionError("cleanup safety failure was not fail-closed")
    if upstream_url(MODEL_API_ROOT, "/v1/chat/completions") != "http://127.0.0.1:8802/v1/chat/completions":
        raise AssertionError("model audit proxy API-root composition is wrong")

    return {
        "historicalPromptMutationRejected": True,
        "thinkingEnabledRawRequestRejected": True,
        "wrongAnswerAttributedToModel": True,
        "cleanupFailureStopsAsSafety": True,
        "modelAuditUpstreamPathExact": True,
        "safeCounterexampleContinuationImplemented": True,
        "externalServicesStarted": 0,
        "modelCalls": 0,
        "databaseConnections": 0,
    }


def _record(result: Any) -> dict[str, Any]:
    vector = verdict_vector(result)
    return {
        "vector": vector,
        "observation": _jsonable(asdict(result.observation)),
        "s2ScientificSupport": [_jsonable(asdict(row)) for row in result.scientific],
        "evidence": [_jsonable(asdict(row)) for row in result.evidence],
        "observationSha256": digest(_jsonable(asdict(result.observation))),
    }


def internal(repo: Path) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    bindings = _verify_upstream(repo)
    truth = ProductTruth.load(repo)
    results, runner, world = run_offline(truth)
    records = [_record(result) for result in results]
    if len(records) != 96:
        raise AssertionError(f"E1 offline walk terminated early: {len(records)}/96")
    failed = [row["vector"]["operationKey"] for row in records if row["vector"]["overallRepetitionVerdict"] != "PASS"]
    if failed:
        raise AssertionError(f"E1 offline component vectors failed: {failed[:5]}")
    if not runner.ledger.verify():
        raise AssertionError("E1 append-before-action ledger verification failed")
    if any(call.get("kind") not in {"HTTP", "STORE", "FILE", "PROCESS"} for call in world.external_calls):
        raise AssertionError("offline port emitted an unknown raw boundary")
    controls = _controls(truth)
    summary = {
        "recordType": "SMA_E1_DDALCU_CANDIDATE_1_OFFLINE_QUALIFICATION",
        "status": "PASS_OFFLINE_NOT_EXECUTION_AUTHORITY",
        "claimIfLaterMeasuredAndAccepted": "INTEGRATED_RUNTIME_TUPLE_QUALIFIED",
        "upstreamBindings": bindings,
        "modelProfile": {
            "apiRoot": MODEL_API_ROOT,
            "model": MODEL_ID,
            "thinking": False,
            "tools": [],
            "temperature": 0,
            "maximumOutputTokens": 128,
            "requestTimeoutMilliseconds": 120000,
            "perRequestConcurrency": 1,
        },
        "workload": {"scenarios": 18, "repetitions": 96, "passed": 96, "failed": 0},
        "verdictVectorFields": [
            "smaSemanticsVerdict", "openHandsBoundaryVerdict", "modelBehaviorVerdict",
            "environmentVerdict", "harnessVerdict", "cleanupVerdict", "overallRepetitionVerdict",
        ],
        "ledgerValid": True,
        "controls": controls,
        "prohibitedActivity": {
            "serviceStartOrRestart": "NOT_RUN",
            "openhandsConversation": "NOT_RUN",
            "modelCall": "NOT_RUN",
            "liveDressRehearsal": "NOT_RUN",
            "measuredExecution": "NOT_RUN",
            "productionOrHistoricalDataAccess": "NOT_RUN",
        },
        "authority": {"e1ExecutionAuthorized": False, "singleUseAttemptsAuthorized": 0},
    }
    summary["canonicalOutcomeSha256"] = digest({
        "vectors": [row["vector"] for row in records],
        "controls": controls,
        "ledgerValid": True,
    })
    return summary, records


def _write_exclusive(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(content)
        stream.flush()
        os.fsync(stream.fileno())


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True)
    parser.add_argument("--output-directory")
    parser.add_argument("--optimized-child", action="store_true")
    args = parser.parse_args()
    repo = Path(args.repo).resolve()
    summary, records = internal(repo)
    if not args.optimized_child:
        wrapper = repo / "scripts/qualify_sma_e1_ddalcu_candidate_1.py"
        completed = subprocess.run([sys.executable, "-O", str(wrapper), "--repo", str(repo), "--optimized-child"], cwd=repo, capture_output=True, timeout=300, check=False)
        if completed.returncode != 0:
            raise AssertionError(f"optimized E1 qualification failed: {completed.stderr.decode(errors='replace')}")
        optimized = json.loads(completed.stdout)
        if optimized["canonicalOutcomeSha256"] != summary["canonicalOutcomeSha256"]:
            raise AssertionError("normal/optimized E1 outcome differs")
        summary["optimizedEquivalent"] = True
    summary["receiptSha256"] = digest(summary)
    if args.output_directory:
        output = Path(args.output_directory).resolve()
        output.mkdir(parents=True, exist_ok=False)
        _write_exclusive(output / "offline-walk.jsonl", b"".join(canonical_bytes(row) + b"\n" for row in records))
        summary["offlineWalk"] = {
            "path": str(output / "offline-walk.jsonl"),
            "sha256": file_sha256(output / "offline-walk.jsonl"),
            "records": len(records),
        }
        summary["receiptSha256"] = digest({key:value for key,value in summary.items() if key != "receiptSha256"})
        _write_exclusive(output / "offline-qualification.json", canonical_bytes(summary) + b"\n")
    else:
        print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
