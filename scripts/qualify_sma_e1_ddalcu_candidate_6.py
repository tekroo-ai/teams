#!/usr/bin/env python3
from __future__ import annotations

import argparse
from dataclasses import asdict
from datetime import datetime, timezone
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
from typing import Any, Mapping
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [
    ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}"
    for number in (1, 3, 4, 5, 6)
]
for package in (S2_PACKAGE, *PACKAGES):
    sys.path.insert(0, str(package))

from e1_candidate6.runner import (  # noqa: E402
    model_behavior,
    run_candidate6_offline,
    select_interaction_answers,
    verdict_vector,
)
from e1_candidate6.model_audit_proxy import handler as audit_handler  # noqa: E402
from e1_candidate6.authority import sealed_execution_authorization  # noqa: E402
from t1.model import (  # noqa: E402
    HarnessFailure,
    LiveAuthority,
    RunMode,
    canonical_bytes,
    digest,
    file_sha256,
)
from t1.ports import _consume_live_authority  # noqa: E402
from t1.truth import ProductTruth  # noqa: E402


PACKAGE = PACKAGES[-1] / "e1_candidate6"
LAUNCH = PACKAGE / "live-launch-definition.json"
CANDIDATE5_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-5-measured-execution/measured-walk.jsonl"
CANDIDATE5_RECEIPT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-5-measured-execution/measured-execution-receipt.json"
CANDIDATE5_RESTART_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-5-restart-continuity-canary/canary-walk.jsonl"
CASE2 = "SMA-S2-002-SAME-PARTITION-DELIVERY"
CASE3 = "SMA-S2-003-CROSS-PARTITION-DELIVERY-DENIAL"


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
        "scientific": [jsonable(asdict(row)) for row in result.scientific],
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


def response_controls() -> dict[str, Any]:
    def call(
        role: str,
        received: int,
        content: str,
        prompt: str = "task",
        finish_reason: str = "stop",
    ) -> dict[str, Any]:
        return {
            "interactionRole": role,
            "receivedNs": received,
            "content": content,
            "prompt": prompt,
            "finishReason": finish_reason,
        }

    def evaluate(
        case_id: str,
        calls: list[dict[str, Any]],
        roles: list[str],
    ) -> tuple[bool, str, list[str]]:
        answers, emitted, selected, detail = select_interaction_answers(
            case_id,
            calls,
            roles,
        )
        passed, predicate_detail = model_behavior(
            case_id,
            answers,
            emitted,
            selected,
            detail,
        )
        return passed, predicate_detail, answers

    plain = [call("primary", 1, "I will inspect the workspace.")]
    case3_pass, case3_detail, _ = evaluate(CASE3, plain, ["primary"])
    if not case3_pass or "LAST_COMPLETED_RESPONSE" not in case3_detail:
        raise AssertionError("accepted plain final case-3 response was rejected")
    thought_then_plain = [
        call("primary", 1, "<function=think><parameter=thought>17 seconds</parameter></function>"),
        call("primary", 2, "UNKNOWN"),
    ]
    if evaluate(CASE2, thought_then_plain, ["primary"])[0]:
        raise AssertionError("intermediate think text satisfied a plain final response")
    thought_then_finish = [
        thought_then_plain[0],
        call("primary", 2, "<function=finish><parameter=message>UNKNOWN</parameter>"),
    ]
    if evaluate(CASE2, thought_then_finish, ["primary"])[0]:
        raise AssertionError("intermediate think text satisfied a FinishTool final response")
    correct_finish = [
        call("primary", 1, "<function=think><parameter=thought>Use evidence.</parameter></function>"),
        call("primary", 2, "<function=finish><parameter=message>17 seconds</parameter>"),
    ]
    if not evaluate(CASE2, correct_finish, ["primary"])[0]:
        raise AssertionError("correct FinishTool final response was rejected")
    forbidden_intermediate = [
        call("primary", 1, "17 seconds"),
        call("primary", 2, "UNKNOWN"),
    ]
    if evaluate(CASE3, forbidden_intermediate, ["primary"])[0]:
        raise AssertionError("forbidden intermediate output escaped the absence predicate")
    four_channel = [
        call("alpha-1", 1, "<function=think><parameter=thought>alpha</parameter></function>"),
        call("alpha-1", 2, "<function=finish><parameter=message>ALPHA</parameter>"),
        call("alpha-2", 3, "ALPHA"),
        call("beta-1", 4, "<function=think><parameter=thought>beta</parameter></function>"),
        call("beta-1", 5, "BETA"),
        call("beta-2", 6, "<function=finish><parameter=message>BETA</parameter>"),
    ]
    four_pass, _, four_answers = evaluate(
        "SMA-S2-013-FOUR-CHANNEL-CONCURRENCY",
        four_channel,
        ["alpha-1", "alpha-2", "beta-1", "beta-2"],
    )
    if not four_pass or len(four_answers) != 4:
        raise AssertionError("four-channel interaction selection failed")
    condensation = [
        call("primary", 1, "<function=think><parameter=thought>recall</parameter></function>"),
        call("primary", 2, "<function=finish><parameter=message>17 seconds</parameter>"),
        call("primary", 3, "summary", "Summarize the conversation for condensation."),
        call("primary", 4, "17 seconds"),
    ]
    condensed_pass, _, condensed_answers = evaluate(
        "SMA-S2-018-CONDENSATION-REANCHOR",
        condensation,
        ["primary", "primary"],
    )
    if not condensed_pass or len(condensed_answers) != 2:
        raise AssertionError("two-turn condensation interaction selection failed")
    missing_condensation = [condensation[0], condensation[1], condensation[3]]
    if evaluate(
        "SMA-S2-018-CONDENSATION-REANCHOR",
        missing_condensation,
        ["primary", "primary"],
    )[0]:
        raise AssertionError("missing condensation boundary was accepted")
    truncated = [call("primary", 1, "17 seconds", finish_reason="length")]
    if evaluate(CASE2, truncated, ["primary"])[0]:
        raise AssertionError("truncated response was accepted")
    duplicate_finish = [
        call("primary", 1, "<function=finish><parameter=message>17 seconds</parameter>"),
        call("primary", 2, "<function=finish><parameter=message>17 seconds</parameter>"),
    ]
    if evaluate(CASE2, duplicate_finish, ["primary"])[0]:
        raise AssertionError("multiple final answers for one interaction were accepted")
    missing_role = [call("", 1, "UNKNOWN")]
    if evaluate(CASE3, missing_role, ["primary"])[0]:
        raise AssertionError("unbound model response was accepted")
    return {
        "acceptedCase3PlainFinalResponse": "PASS",
        "intermediateThinkCannotSatisfyPlainFinal": "PASS",
        "intermediateThinkCannotSatisfyFinishToolFinal": "PASS",
        "forbiddenIntermediateOutputRejected": "PASS",
        "correctFinishToolFinal": "PASS",
        "fourChannelInteractionIdentity": "PASS",
        "twoTurnCondensationBoundary": "PASS",
        "missingCondensationBoundaryRejected": "PASS",
        "truncatedResponseRejected": "PASS",
        "multipleFinalResponsesRejected": "PASS",
        "missingInteractionRoleRejected": "PASS",
    }


def historical_failure() -> dict[str, Any]:
    rows = [json.loads(line) for line in CANDIDATE5_WALK.read_text().splitlines() if line]
    failed = rows[-1]
    if (
        len(rows) != 21
        or failed["vector"]["operationKey"]
        != "SMA-S2-003-CROSS-PARTITION-DELIVERY-DENIAL#001"
        or failed["vector"]["overallRepetitionVerdict"] != "FAIL"
        or failed["vector"]["smaSemanticsVerdict"] != "PASS"
        or failed["vector"]["openHandsBoundaryVerdict"] != "PASS"
        or failed["observation"]["failure"] is not None
    ):
        raise AssertionError("candidate-5 false-negative evidence drift")
    requests = {
        row["requestId"]: row
        for row in failed["observation"]["model_requests"]
    }
    calls = [
        {
            "interactionRole": "primary",
            "receivedNs": requests[row["requestId"]]["receivedNs"],
            "prompt": requests[row["requestId"]]["prompt"],
            "content": row["content"],
            "finishReason": row["finishReason"],
        }
        for row in failed["observation"]["model_terminals"]
    ]
    answers, emitted, selected, selection_detail = select_interaction_answers(
        CASE3,
        calls,
        ["primary"],
    )
    corrected, detail = model_behavior(
        CASE3,
        answers,
        emitted,
        selected,
        selection_detail,
    )
    if not corrected:
        raise AssertionError("candidate-6 did not correct the exact candidate-5 false negative")
    return {
        "walk": {"path": str(CANDIDATE5_WALK.relative_to(ROOT)), "sha256": file_sha256(CANDIDATE5_WALK), "records": len(rows)},
        "receipt": {"path": str(CANDIDATE5_RECEIPT.relative_to(ROOT)), "sha256": file_sha256(CANDIDATE5_RECEIPT)},
        "failedOperation": failed["vector"]["operationKey"],
        "candidate5Verdict": "FAIL",
        "candidate6AcceptedPredicateVerdict": "PASS",
        "candidate6Detail": detail,
        "observedSmaSemantics": "PASS",
        "observedOpenHandsBoundary": "PASS",
        "observedExecutionFailure": None,
        "rootCause": "CANDIDATE_5_ADDED_AN_UNREGISTERED_FINISH_TOOL_REQUIREMENT",
    }


def historical_live_reclassification() -> dict[str, Any]:
    sources = (CANDIDATE5_WALK, CANDIDATE5_RESTART_WALK)
    reclassified: list[dict[str, Any]] = []
    for source in sources:
        rows = [
            json.loads(line)
            for line in source.read_text().splitlines()
            if line
        ]
        for row in rows:
            observation = row["observation"]
            case_id = row["vector"]["operationKey"].split("#", 1)[0]
            conversation_roles = {
                value["id"]: value["role"]
                for value in observation["conversations"]
                if value.get("id") and value.get("role")
            }
            submitted_roles = [
                conversation_roles[value["conversationId"]]
                for value in observation["raw_receipts"]
                if value.get("kind") == "prompt_submission"
            ]
            if len(set(submitted_roles)) != 1:
                raise AssertionError(
                    "historical live reclassification only supports the retained single-interaction traces"
                )
            requests = {
                value["requestId"]: value
                for value in observation["model_requests"]
            }
            calls = [
                {
                    "interactionRole": submitted_roles[0],
                    "receivedNs": requests[value["requestId"]]["receivedNs"],
                    "prompt": requests[value["requestId"]]["prompt"],
                    "content": value["content"],
                    "finishReason": value["finishReason"],
                }
                for value in observation["model_terminals"]
            ]
            answers, emitted, selected, selection_detail = (
                select_interaction_answers(
                    case_id,
                    calls,
                    submitted_roles,
                )
            )
            passed, detail = model_behavior(
                case_id,
                answers,
                emitted,
                selected,
                selection_detail,
            )
            if not passed:
                raise AssertionError(
                    f"historical live response topology failed: {row['vector']['operationKey']}: {detail}"
                )
            reclassified.append({
                "operationKey": row["vector"]["operationKey"],
                "modelCalls": len(calls),
                "selectedAnswers": len(answers),
                "detail": detail,
            })
    return {
        "sources": [
            {
                "path": str(source.relative_to(ROOT)),
                "sha256": file_sha256(source),
            }
            for source in sources
        ],
        "observedLiveOperations": len(reclassified),
        "passed": len(reclassified),
        "failed": 0,
        "operationKeys": [value["operationKey"] for value in reclassified],
    }
def subprocess_controls() -> dict[str, Any]:
    launch = json.loads(LAUNCH.read_text())
    python = launch["environment"]["livePython"]
    environment = dict(os.environ)
    environment.pop("PYTHONPATH", None)
    environment.pop("PYTHONHOME", None)
    action = subprocess.run([python, str(PACKAGE / "live_actions.py"), "--help"], cwd=ROOT, env=environment, capture_output=True, timeout=30, check=False)
    composition = subprocess.run([python, str(PACKAGE / "live_entrypoint.py"), "--validate-only", "--definition", str(LAUNCH)], cwd=ROOT, env=environment, capture_output=True, timeout=300, check=False)
    driver = subprocess.run([python, str(ROOT / "scripts/execute_sma_e1_ddalcu_candidate_6.py"), "--help"], cwd=ROOT, env=environment, capture_output=True, timeout=30, check=False)
    if action.returncode != 0 or composition.returncode != 0 or driver.returncode != 0:
        raise AssertionError("candidate-6 live composition failed default-deny validation")
    return {
        "liveActionSubprocessImport": "PASS",
        "defaultDenyComposition": "PASS",
        "singleDriverCanaryAndMeasuredModes": "PASS",
        "actionStdoutSha256": hashlib.sha256(action.stdout).hexdigest(),
        "compositionStdoutSha256": hashlib.sha256(composition.stdout).hexdigest(),
        "driverStdoutSha256": hashlib.sha256(driver.stdout).hexdigest(),
    }


def proxy_controls() -> dict[str, Any]:
    upstream_headers: dict[str, str] = {}

    class UpstreamHandler(BaseHTTPRequestHandler):
        def log_message(self, *_: object) -> None:
            return

        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            self.rfile.read(length)
            upstream_headers.update({
                key.lower(): value for key, value in self.headers.items()
            })
            body = b'{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"UNKNOWN"}}]}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    with tempfile.TemporaryDirectory(prefix="sma-e1-candidate6-proxy-") as directory:
        request_journal = Path(directory) / "requests.jsonl"
        response_journal = Path(directory) / "responses.jsonl"
        request_journal.touch(mode=0o600)
        response_journal.touch(mode=0o600)
        upstream = ThreadingHTTPServer(("127.0.0.1", 0), UpstreamHandler)
        upstream_thread = threading.Thread(target=upstream.serve_forever)
        upstream_thread.start()
        proxy = ThreadingHTTPServer(
            ("127.0.0.1", 0),
            audit_handler(
                f"http://127.0.0.1:{upstream.server_port}/v1",
                request_journal,
                response_journal,
            ),
        )
        proxy_thread = threading.Thread(target=proxy.serve_forever)
        proxy_thread.start()
        try:
            request = urllib.request.Request(
                f"http://127.0.0.1:{proxy.server_port}/v1/chat/completions",
                data=b"{}",
                headers={
                    "Content-Type": "application/json",
                    "X-SMA-E1-Operation": "SMA-S2-016-EMPTY-RESULT#001",
                    "X-SMA-E1-Interaction-Role": "primary",
                },
                method="POST",
            )
            with urllib.request.urlopen(request, timeout=5) as response:
                response.read()
        finally:
            proxy.shutdown()
            upstream.shutdown()
            proxy.server_close()
            upstream.server_close()
            proxy_thread.join(timeout=5)
            upstream_thread.join(timeout=5)
        requests = [
            json.loads(line)
            for line in request_journal.read_text().splitlines()
            if line
        ]
        responses = [
            json.loads(line)
            for line in response_journal.read_text().splitlines()
            if line
        ]
    if (
        len(requests) != 1
        or len(responses) != 1
        or requests[0].get("interactionRole") != "primary"
        or responses[0].get("interactionRole") != "primary"
        or requests[0].get("requestId") != responses[0].get("requestId")
        or "x-sma-e1-operation" in upstream_headers
        or "x-sma-e1-interaction-role" in upstream_headers
    ):
        raise AssertionError("interaction-bound audit proxy control failed")
    return {
        "requestRoleRetained": "PASS",
        "responseRoleRetained": "PASS",
        "requestResponseIdentityPaired": "PASS",
        "qualificationHeadersRemovedBeforeModel": "PASS",
        "realModelCalls": 0,
    }


def authority_controls() -> dict[str, Any]:
    with tempfile.TemporaryDirectory(prefix="sma-e1-candidate6-authority-") as directory:
        root = Path(directory)
        package_identity = "a" * 64
        execution_identity = "b" * 64
        package_path = root / "acceptance.json"
        identity_path = root / "identity.json"
        authorization_path = root / "authorization.json"
        consumed_path = root / "consumed.json"
        package_path.write_bytes(canonical_bytes({
            "status": "ACCEPTED_FROZEN",
            "packageIdentity": package_identity,
        }))
        identity_path.write_bytes(canonical_bytes({
            "packageIdentity": package_identity,
            "executionIdentity": execution_identity,
        }))
        authorization = sealed_execution_authorization(
            principal_grant_sha256="c" * 64,
            principal_statement="Proceed as recommended.",
            package_identity=package_identity,
            execution_identity=execution_identity,
            mode=RunMode.ZERO_CREDIT_DRESS.value,
            maximum_scenarios=18,
            maximum_repetitions=18,
        )
        authorization_path.write_bytes(canonical_bytes(authorization))
        authority = LiveAuthority(
            package_manifest_path=str(package_path),
            package_manifest_sha256=file_sha256(package_path),
            authorization_path=str(authorization_path),
            authorization_sha256=file_sha256(authorization_path),
            execution_identity_path=str(identity_path),
            execution_identity_sha256=file_sha256(identity_path),
            mode=RunMode.ZERO_CREDIT_DRESS,
            consumed_marker_path=str(consumed_path),
        )
        _consume_live_authority(authority, RunMode.ZERO_CREDIT_DRESS)
        if not consumed_path.is_file():
            raise AssertionError("valid authority was not consumed")
        bad_authorization_path = root / "bad-authorization.json"
        bad = dict(authorization)
        bad["executionIdentity"] = "d" * 64
        bad_authorization_path.write_bytes(canonical_bytes(bad))
        bad_authority = LiveAuthority(
            package_manifest_path=str(package_path),
            package_manifest_sha256=file_sha256(package_path),
            authorization_path=str(bad_authorization_path),
            authorization_sha256=file_sha256(bad_authorization_path),
            execution_identity_path=str(identity_path),
            execution_identity_sha256=file_sha256(identity_path),
            mode=RunMode.ZERO_CREDIT_DRESS,
            consumed_marker_path=str(root / "bad-consumed.json"),
        )
        try:
            _consume_live_authority(bad_authority, RunMode.ZERO_CREDIT_DRESS)
        except HarnessFailure:
            pass
        else:
            raise AssertionError("mismatched execution identity was accepted")
    return {
        "principalGrantSeparatedFromSealedAuthorization": "PASS",
        "executionIdentityBoundBeforeConsumption": "PASS",
        "validAuthorityConsumedBeforeFirstAction": "PASS",
        "mismatchedExecutionIdentityRejected": "PASS",
        "externalCalls": 0,
    }


def run() -> tuple[dict[str, Any], list[dict[str, Any]]]:
    results, runner, _ = run_candidate6_offline(ProductTruth.load(ROOT))
    records = [record(result) for result in results]
    failed = [row["vector"]["operationKey"] for row in records if row["vector"]["overallRepetitionVerdict"] != "PASS"]
    if len(records) != 96 or failed or not runner.ledger.verify():
        raise AssertionError(f"candidate-6 offline qualification failed: records={len(records)} failed={failed[:3]}")
    controls = {
        "responseSelection": response_controls(),
        "historicalCandidate5Failure": historical_failure(),
        "historicalLiveReclassification": historical_live_reclassification(),
        "handlerCoverage": {"cases": 18, "repetitions": 96},
        "liveLaunch": subprocess_controls(),
        "interactionBoundProxy": proxy_controls(),
        "authorityHandshake": authority_controls(),
        "realModelCalls": 0,
        "productServicesStarted": 0,
        "databaseConnections": 0,
    }
    summary = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_6_OFFLINE_QUALIFICATION",
        "status": "PASS_OFFLINE_REQUIRES_ONE_REPETITION_PER_SCENARIO_REAL_PATH_CANARY_NOT_EXECUTION_AUTHORITY",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "predecessorPackageIdentity": "525aec038546f787a56b6eb05ae3a6584f1c2728bae4815666af8f1e206fca54",
        "changeScope": "HARNESS_ACCEPTED_FINAL_RESPONSE_SELECTION_CORRECTION_ONLY",
        "science": {"scenarios": 18, "repetitions": 96, "passed": 96, "failed": 0, "changed": False},
        "ledgerValid": True,
        "controls": controls,
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "prohibitedActivity": {"serviceStartOrRestart": "NOT_RUN", "openhandsConversation": "NOT_RUN", "realModelCall": "NOT_RUN", "measuredExecution": "NOT_RUN"},
    }
    summary["canonicalOutcomeSha256"] = digest({"vectors": [row["vector"] for row in records], "controls": controls, "ledgerValid": True})
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
            raise AssertionError(child.stderr.decode(errors="replace"))
        optimized = json.loads(child.stdout)
        if optimized["canonicalOutcomeSha256"] != summary["canonicalOutcomeSha256"]:
            raise AssertionError("candidate-6 normal/optimized outcome mismatch")
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
