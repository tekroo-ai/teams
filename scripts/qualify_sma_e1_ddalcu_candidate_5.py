#!/usr/bin/env python3
from __future__ import annotations

import argparse
import base64
from dataclasses import asdict, replace
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
CANDIDATE_5_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-5"
CANDIDATE_5 = CANDIDATE_5_PACKAGE / "e1_candidate5"
LAUNCH = CANDIDATE_5 / "live-launch-definition.json"
CANDIDATE4_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-measured-execution/measured-walk.jsonl"
CANDIDATE4_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-measured-execution/measured-execution-receipt.json"
M1_LEDGER = ROOT / "OUTPUT/phase-3/sma-m1-ddalcu-candidate-1-measured-attempt-1/evidence-ledger.jsonl"
for package in (
    S2_PACKAGE,
    CANDIDATE_1_PACKAGE,
    CANDIDATE_3_PACKAGE,
    CANDIDATE_4_PACKAGE,
    CANDIDATE_5_PACKAGE,
):
    sys.path.insert(0, str(package))

from e1.runner import E1RunnerConfiguration  # noqa: E402
from e1_candidate4.runner import _replace_body  # noqa: E402
from e1_candidate5.runner import (  # noqa: E402
    MAX_OUTPUT_TOKENS,
    Candidate5OfflineWorld,
    Candidate5Runner,
    _replace_terminal_content,
    model_behavior,
    run_candidate5_offline,
    verdict_vector,
)
from t1.model import (  # noqa: E402
    FailureClass,
    HarnessFailure,
    OperationKey,
    RawHttpReceipt,
    RawProcessReceipt,
    RawProcessRequest,
    RunMode,
    canonical_bytes,
    digest,
    file_sha256,
)
from t1.truth import ProductTruth  # noqa: E402


CASE2 = OperationKey("SMA-S2-002-SAME-PARTITION-DELIVERY", 1)


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
        "s2ScientificSupport": [
            jsonable(asdict(row)) for row in result.scientific
        ],
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


def _rows(path: Path) -> list[dict[str, Any]]:
    return [
        json.loads(line)
        for line in path.read_text(encoding="utf-8").splitlines()
        if line
    ]


def _finish_reason(row: Mapping[str, Any]) -> str | None:
    payload = json.loads(
        base64.b64decode(str(row["responseBodyBase64"]), validate=True)
    )
    return (payload.get("choices") or [{}])[0].get("finish_reason")


def run_world(
    world: Candidate5OfflineWorld,
    config: E1RunnerConfiguration | None = None,
):
    truth = ProductTruth.load(ROOT)
    runner = Candidate5Runner(
        truth,
        world,
        config or E1RunnerConfiguration(),
        RunMode.OFFLINE_MEASURED,
    )
    world.current_key = CASE2
    return runner.run_operation(CASE2), runner


class TerminalStatusWorld(Candidate5OfflineWorld):
    forced_status = "error"

    def http(self, target: str, request: Any):
        receipt = super().http(target, request)
        if (
            target == "OPENHANDS"
            and request.method == "GET"
            and request.route.startswith("/api/conversations/")
            and "/events/" not in request.route
        ):
            body = json.loads(receipt.body)
            body["execution_status"] = self.forced_status
            wire = canonical_bytes(body)
            return RawHttpReceipt(
                request=receipt.request,
                status=receipt.status,
                headers=receipt.headers,
                body=wire,
                started_ns=receipt.started_ns,
                completed_ns=receipt.completed_ns,
                error_kind=receipt.error_kind,
            )
        return receipt


class RunningTerminalWorld(TerminalStatusWorld):
    forced_status = "running"


def request_and_response_controls(
    truth: ProductTruth,
    results: list[Any],
    runner: Candidate5Runner,
) -> dict[str, Any]:
    baseline = results[10]
    observation = baseline.observation
    requests = [
        receipt["row"]
        for receipt in observation.raw_receipts
        if receipt.get("kind") == "e1_model_audit_request_raw"
    ]
    terminals = [
        receipt["row"]
        for receipt in observation.raw_receipts
        if receipt.get("kind") == "e1_model_audit_response_raw"
    ]
    if len(requests) != 2 or len(terminals) != 2:
        raise AssertionError("candidate-5 restart regression did not execute two calls")
    if not all(_finish_reason(row) == "stop" for row in terminals):
        raise AssertionError("candidate-5 success fixture includes non-stop completion")

    cap = dict(requests[-1])
    payload = json.loads(base64.b64decode(cap["requestBodyBase64"]))
    payload["max_tokens"] = 128
    _replace_body(cap, payload)
    try:
        runner._decode_stub_request(cap, observation)
    except HarnessFailure as exc:
        cap_class = exc.failure_class.value
    else:
        raise AssertionError("128-token request was accepted by candidate-5")

    missing_cap = dict(requests[-1])
    missing_payload = json.loads(
        base64.b64decode(missing_cap["requestBodyBase64"])
    )
    missing_payload.pop("max_tokens", None)
    missing_payload.pop("max_completion_tokens", None)
    _replace_body(missing_cap, missing_payload)
    try:
        runner._decode_stub_request(missing_cap, observation)
    except HarnessFailure as exc:
        missing_cap_class = exc.failure_class.value
    else:
        raise AssertionError("missing output-token ceiling was accepted")

    truncated = dict(terminals[-1])
    content = str(truncated.get("responseContent") or "")
    _replace_terminal_content(truncated, content, "length")
    try:
        runner._decode_stub_terminal(truncated, observation)
    except HarnessFailure as exc:
        truncation_class = exc.failure_class.value
    else:
        raise AssertionError("finish_reason=length was accepted by candidate-5")

    think_only = [
        {
            "content": (
                "<function=think><parameter=thought>17 seconds"
                "</parameter></function>"
            ),
            "finishReason": "stop",
        },
        {
            "content": (
                "<function=finish><parameter=message>UNKNOWN</parameter>"
            ),
            "finishReason": "stop",
        },
    ]
    model_ok, _ = model_behavior(CASE2.case_id, think_only, 1)
    if model_ok:
        raise AssertionError("intermediate thought satisfied final-answer predicate")

    error_result, _ = run_world(TerminalStatusWorld(truth))
    if error_result.observation.failure != FailureClass.SCIENTIFIC:
        raise AssertionError("OpenHands error status was not rejected as SCIENTIFIC")
    error_diagnostics = [
        row
        for row in error_result.observation.raw_receipts
        if row.get("kind") == "conversation_terminal_failure_diagnostic"
    ]
    if not error_diagnostics or error_diagnostics[-1].get("requestCount", 0) < 1:
        raise AssertionError("error status did not retain model diagnostics")
    if verdict_vector(error_result)["overallRepetitionVerdict"] != "FAIL":
        raise AssertionError("failed observation produced a PASS vector")

    timeout_config = replace(
        E1RunnerConfiguration(),
        stabilization_deadline_ms=500,
        operation_deadline_ms=2_000,
    )
    timeout_result, _ = run_world(
        RunningTerminalWorld(truth),
        timeout_config,
    )
    if timeout_result.observation.failure != FailureClass.SCIENTIFIC:
        raise AssertionError("running-through-deadline status was not SCIENTIFIC")
    timeout_diagnostics = [
        row
        for row in timeout_result.observation.raw_receipts
        if row.get("kind") == "conversation_terminal_failure_diagnostic"
    ]
    if (
        not timeout_diagnostics
        or timeout_diagnostics[-1].get("requestCount", 0) < 1
        or timeout_diagnostics[-1].get("terminalCount", 0) < 1
    ):
        raise AssertionError("timeout did not retain paired model diagnostics")

    process_observation = baseline.observation
    process_receipt = RawProcessReceipt(
        request=RawProcessRequest("synthetic_failure", ("synthetic_failure",), "/tmp", 1000),
        exit_code=1,
        stdout=b"bounded stdout",
        stderr=b"exact actionable stderr",
        started_ns=runner.ports.now_ns(),
        completed_ns=runner.ports.now_ns(),
    )
    runner._post_action(
        process_observation,
        "PROCESS:synthetic_failure",
        process_receipt,
    )
    process_diagnostics = [
        row
        for row in process_observation.raw_receipts
        if row.get("kind") == "failed_process_diagnostic"
    ]
    if not process_diagnostics or process_diagnostics[-1]["stderr"] != "exact actionable stderr":
        raise AssertionError("failed process stderr was not retained")
    return {
        "practicalOutputCeiling": MAX_OUTPUT_TOKENS,
        "legacy128TokenRequestRejectedAs": cap_class,
        "missingOutputTokenCeilingRejectedAs": missing_cap_class,
        "finishReasonLengthRejectedAs": truncation_class,
        "intermediateThoughtCannotSatisfyFinalAnswer": True,
        "openHandsErrorStatusRejectedAs": error_result.observation.failure.value,
        "failedObservationCannotPassVector": True,
        "terminalTimeoutRetainsRequestCount": timeout_diagnostics[-1]["requestCount"],
        "terminalTimeoutRetainsTerminalCount": timeout_diagnostics[-1]["terminalCount"],
        "failedProcessRetainsBoundedStderr": True,
    }


def historical_candidate4_failure() -> dict[str, Any]:
    rows = _rows(CANDIDATE4_WALK)
    restart = [
        row
        for row in rows
        if row["vector"]["operationKey"].startswith(
            "SMA-S2-012-RESTART-CONTINUITY"
        )
    ]
    if len(rows) != 69 or len(restart) != 3:
        raise AssertionError("candidate-4 measured failure population drift")
    false_passes = []
    for row in restart[:2]:
        statuses = {
            conversation.get("execution_status")
            for conversation in row["observation"]["conversations"]
        }
        terminal_rows = [
            receipt["row"]
            for receipt in row["observation"]["raw_receipts"]
            if receipt.get("kind") == "e1_model_audit_response_raw"
        ]
        reasons = [_finish_reason(item) for item in terminal_rows]
        length_contents = []
        for item in terminal_rows:
            payload = json.loads(
                base64.b64decode(item["responseBodyBase64"], validate=True)
            )
            choice = payload["choices"][0]
            if choice.get("finish_reason") == "length":
                length_contents.append(
                    str((choice.get("message") or {}).get("content") or "")
                )
        if (
            row["vector"]["overallRepetitionVerdict"] != "PASS"
            or statuses != {"error"}
            or len(terminal_rows) != 10
            or reasons.count("length") != 5
            or any(not content.startswith("<function=think>") for content in length_contents)
            or any("</function>" in content for content in length_contents)
        ):
            raise AssertionError("candidate-4 restart false-positive shape drift")
        false_passes.append({
            "operationKey": row["vector"]["operationKey"],
            "reportedVerdict": "PASS",
            "actualOpenHandsStatus": "error",
            "modelCalls": len(terminal_rows),
            "lengthTerminations": reasons.count("length"),
            "truncatedOpenThinkCalls": len(length_contents),
        })
    stopped = restart[2]
    stats = stopped["observation"]["conversations"][0]["stats"]
    latencies = stats["usage_to_metrics"]["default"]["response_latencies"]
    if (
        stopped["vector"]["overallRepetitionVerdict"] != "FAIL"
        or stopped["observation"]["conversations"][0].get("execution_status")
        != "running"
        or len(stopped["observation"]["model_requests"]) != 0
        or len(latencies) != 4
    ):
        raise AssertionError("candidate-4 stopped-operation evidence drift")
    return {
        "measuredWalk": {
            "path": str(CANDIDATE4_WALK.relative_to(ROOT)),
            "sha256": file_sha256(CANDIDATE4_WALK),
            "records": len(rows),
        },
        "measuredReceipt": {
            "path": str(CANDIDATE4_RECEIPT.relative_to(ROOT)),
            "sha256": file_sha256(CANDIDATE4_RECEIPT),
        },
        "restartFalsePasses": false_passes,
        "stoppedOperation": {
            "operationKey": stopped["vector"]["operationKey"],
            "openHandsStatus": "running",
            "modelResponsesInConversationStats": len(latencies),
            "modelResponsesCollectedByCandidate4": 0,
        },
        "observedRootCause": "128_TOKEN_RESPONSES_TERMINATED_WITH_LENGTH_AND_OPENHANDS_REJECTED_TRUNCATED_NON_NATIVE_TOOL_CALLS",
        "candidate4ValidatorDefects": [
            "ERROR_STATUS_ACCEPTED_AS_TERMINAL_SUCCESS",
            "INTERMEDIATE_MODEL_TEXT_COULD_SATISFY_FINAL_ANSWER_PREDICATE",
            "FAILURE_PATH_DID_NOT_RETAIN_MODEL_JOURNALS",
        ],
    }


def m1_profile_compatibility() -> dict[str, Any]:
    rows = _rows(M1_LEDGER)
    responses = [row for row in rows if row.get("kind") == "MODEL_RESPONSE"]
    payloads = [json.loads(row["rawResponseUtf8"]) for row in responses]
    reasons = [payload["choices"][0].get("finish_reason") for payload in payloads]
    completions = [int(payload["usage"]["completion_tokens"]) for payload in payloads]
    if len(responses) != 72 or set(reasons) != {"stop"} or max(completions) > 7:
        raise AssertionError("accepted M1 response population no longer supports cap compatibility")
    return {
        "classification": "OBSERVED_COMPATIBILITY_EVIDENCE_NOT_NEW_M1_QUALIFICATION",
        "ledger": {
            "path": str(M1_LEDGER.relative_to(ROOT)),
            "sha256": file_sha256(M1_LEDGER),
        },
        "responses": len(responses),
        "finishReasons": {"stop": reasons.count("stop")},
        "completionTokens": {
            "minimum": min(completions),
            "maximum": max(completions),
        },
        "observed128TokenCeilingReached": False,
        "inference": "Raising the output ceiling does not alter any response observed in the accepted M1 run because every response ended normally at seven or fewer completion tokens.",
    }


def subprocess_controls() -> dict[str, Any]:
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    python = launch["environment"]["livePython"]
    environment = dict(os.environ)
    environment.pop("PYTHONPATH", None)
    environment.pop("PYTHONHOME", None)
    action = subprocess.run(
        [python, str(CANDIDATE_5 / "live_actions.py"), "--help"],
        cwd=ROOT,
        env=environment,
        capture_output=True,
        timeout=30,
        check=False,
    )
    if action.returncode != 0:
        raise AssertionError(
            f"candidate-5 raw action import failed: {action.stderr.decode(errors='replace')}"
        )
    composition = subprocess.run(
        [
            python,
            str(CANDIDATE_5 / "live_entrypoint.py"),
            "--validate-only",
            "--definition",
            str(LAUNCH),
        ],
        cwd=ROOT,
        env=environment,
        capture_output=True,
        timeout=300,
        check=False,
    )
    if composition.returncode != 0:
        raise AssertionError(
            "candidate-5 composition validation failed: "
            + composition.stderr.decode(errors="replace")
        )
    return {
        "exactLivePython": python,
        "liveActionSubprocessImport": "PASS",
        "defaultDenyComposition": "PASS",
        "actionStdoutSha256": hashlib.sha256(action.stdout).hexdigest(),
        "compositionStdoutSha256": hashlib.sha256(composition.stdout).hexdigest(),
    }


def run() -> tuple[dict[str, Any], list[dict[str, Any]]]:
    truth = ProductTruth.load(ROOT)
    results, runner, _ = run_candidate5_offline(truth)
    records = [record(result) for result in results]
    if len(records) != 96:
        raise AssertionError(
            f"candidate-5 offline walk terminated early: {len(records)}/96"
        )
    failed = [
        row["vector"]["operationKey"]
        for row in records
        if row["vector"]["overallRepetitionVerdict"] != "PASS"
    ]
    if failed:
        raise AssertionError(f"candidate-5 offline vectors failed: {failed[:5]}")
    if not runner.ledger.verify():
        raise AssertionError("candidate-5 append-before-action ledger failed")
    statuses = {
        conversation.get("execution_status")
        for result in results
        for conversation in result.observation.conversations
        if any(
            receipt.get("kind") == "prompt_submission"
            and receipt.get("conversationId") == conversation.get("id")
            for receipt in result.observation.raw_receipts
        )
    }
    if statuses != {"finished"}:
        raise AssertionError(f"candidate-5 successful status set drift: {statuses}")
    controls = {
        "historicalCandidate4Failure": historical_candidate4_failure(),
        "requestAndResponse": request_and_response_controls(
            truth,
            results,
            runner,
        ),
        "m1ProfileCompatibility": m1_profile_compatibility(),
        "successfulPromptedConversationStatuses": sorted(statuses),
        "handlerCoverage": {"cases": 18, "repetitions": len(results)},
        "liveLaunch": subprocess_controls(),
        "realModelCalls": 0,
        "productServicesStarted": 0,
        "databaseConnections": 0,
    }
    summary = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_OFFLINE_QUALIFICATION",
        "status": "PASS_OFFLINE_REQUIRES_RESTART_CONTINUITY_REAL_PATH_CANARY_NOT_EXECUTION_AUTHORITY",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "predecessorPackageIdentity": "c6bede7336f11e1be5c5c958737f88ccbdf8a0b6cf468da3d1db00ff722be5a4",
        "changeScope": "HARNESS_PRACTICAL_OUTPUT_SUCCESSFUL_TERMINAL_AND_FINAL_ANSWER_CORRECTION_ONLY",
        "science": {
            "scenarios": 18,
            "repetitions": 96,
            "passed": 96,
            "failed": 0,
            "changed": False,
        },
        "ledgerValid": True,
        "controls": controls,
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "prohibitedActivity": {
            "serviceStartOrRestart": "NOT_RUN",
            "openhandsConversation": "NOT_RUN",
            "realModelCall": "NOT_RUN",
            "measuredExecution": "NOT_RUN",
            "productionOrHistoricalDataAccess": "NOT_RUN",
        },
    }
    summary["canonicalOutcomeSha256"] = digest({
        "vectors": [row["vector"] for row in records],
        "controls": controls,
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
        child = subprocess.run(
            [sys.executable, "-O", str(Path(__file__).resolve()), "--optimized-child"],
            cwd=ROOT,
            env=dict(os.environ),
            capture_output=True,
            timeout=600,
            check=False,
        )
        if child.returncode != 0:
            raise AssertionError(
                "optimized candidate-5 qualification failed: "
                + child.stderr.decode(errors="replace")
            )
        optimized = json.loads(child.stdout)
        if optimized["canonicalOutcomeSha256"] != summary["canonicalOutcomeSha256"]:
            raise AssertionError("candidate-5 normal/optimized outcome mismatch")
        summary["optimizedEquivalent"] = True
    summary["receiptSha256"] = digest(summary)
    if args.output_directory:
        output = Path(args.output_directory).resolve()
        output.mkdir(parents=True, exist_ok=False)
        walk = output / "offline-walk.jsonl"
        write_exclusive(
            walk,
            b"".join(canonical_bytes(row) + b"\n" for row in records),
        )
        summary["offlineWalk"] = {
            "path": str(walk),
            "sha256": file_sha256(walk),
            "records": len(records),
        }
        summary["receiptSha256"] = digest({
            key: value
            for key, value in summary.items()
            if key != "receiptSha256"
        })
        write_exclusive(
            output / "offline-qualification.json",
            canonical_bytes(summary) + b"\n",
        )
    else:
        print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
