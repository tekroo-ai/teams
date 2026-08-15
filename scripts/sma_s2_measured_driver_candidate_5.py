#!/usr/bin/env python3
"""Candidate-7 SMA-S2 driver with stabilized events and bound corpus seeding.

Candidate 6 remains immutable. This successor preserves its scientific corpus
and predicates while correcting the two harness defects observed in the sole
candidate-6 measured attempt. Live modes remain default-deny.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import time
from pathlib import Path
from typing import Any, Callable

import sma_s2_measured_driver_candidate_4 as c6


ROOT = Path(__file__).resolve().parents[1]
DRIVER = Path(__file__).resolve()
CONFIG = ROOT / "investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-7.json"
MATRIX = ROOT / "investigations/sma-q1/layered/sma-s2-predicate-oracle-matrix-candidate-4.json"
CORPUS = ROOT / "investigations/sma-q1/layered/sma-s2-measured-corpus-candidate-1.json"
SEMANTIC_SOURCE = ROOT / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-2.json"
STUB = ROOT / "scripts/sma_s2_deterministic_model_stub_candidate_3.py"
MEASURED_ROOT = Path("/tmp/tekroo-sma-s2-successor-candidate-7")
DATABASE = "sma_s2_successor_candidate_7_measured_20260814"
SEMANTIC = "sma_s2_successor_candidate_7_semantic_20260814"
EPISODIC = "sma_s2_successor_candidate_7_episodic_20260814"
LABEL = "com.tekroo.sma-service-s2-successor-candidate-7"
DRESS_OPERATION_COUNT = 34
STABLE_FETCH_COUNT = 2

for module in (c6, c6.c5, c6.c5.c4, c6.c5.c4.base):
    module.CONFIG = CONFIG
    module.MATRIX = MATRIX
    module.CORPUS = CORPUS
    module.STUB = STUB
    module.MEASURED_ROOT = MEASURED_ROOT
    module.DATABASE = DATABASE
    module.SEMANTIC = SEMANTIC
    module.EPISODIC = EPISODIC
    module.LABEL = LABEL

PredicateFailure = c6.PredicateFailure
require = c6.require
sha_file = c6.sha_file
sha_bytes = c6.sha_bytes
canonical_bytes = c6.canonical_bytes
base = c6.base


def expected_terminal_event(item: dict[str, Any], events: list[dict[str, Any]], user: Any,
                            event_text: Callable[[dict[str, Any]], str]) -> dict[str, Any] | None:
    if not isinstance(user, dict) or user not in events:
        return None
    user_index = events.index(user)
    later = events[user_index + 1:]
    if item.get("stubMode", item.get("mode")) == "SUCCESS":
        expected = f"STUB_OK:{item['caseId']}:{item['repetition']}"
        return next((event for event in later
                     if event.get("kind") == "MessageEvent"
                     and event.get("source") == "agent"
                     and event_text(event) == expected), None)
    return next((event for event in later if event.get("kind") == "ConversationErrorEvent"), None)


def observation_signature(observed: dict[str, Any], terminal_event: dict[str, Any]) -> str:
    value = {
        "userId": (observed.get("user") or {}).get("id"),
        "hookId": (observed.get("hook") or {}).get("id"),
        "terminalEventId": terminal_event.get("id"),
        "terminalStatus": (observed.get("detail") or {}).get("execution_status"),
        "rawRequestIds": sorted(record.get("requestId") for record in observed.get("raws", [])),
        "stubTerminalIds": sorted(record.get("requestId") for record in observed.get("terminals", [])),
    }
    return sha_bytes(canonical_bytes(value))


def dress_rehearsal_plan(corpus: dict[str, Any]) -> list[dict[str, Any]]:
    complete = base.exact_plan(corpus)
    selected = []
    for item in complete:
        case_id = item["caseId"]
        if case_id == "SMA-S2-001-FIRST-PROMPT-EMPTY":
            include = item["repetition"] <= 10
        elif case_id == "SMA-S2-019-MODEL-STUB-FAULT-MATRIX":
            include = True
        elif case_id == "SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN":
            include = True
        else:
            include = item["repetition"] == 1
        if include:
            selected.append(item)
    require(len(selected) == DRESS_OPERATION_COUNT, "dress-rehearsal plan cardinality changed")
    return selected


def sanitized_failure(error: Exception, phase: str) -> dict[str, Any]:
    message = str(error)
    status = re.search(r"HTTP\s+(\d{3})", message)
    return {
        "phase": phase,
        "errorType": type(error).__name__,
        "messageSha256": sha_bytes(message.encode()),
        "httpStatus": int(status.group(1)) if status else None,
    }


class Candidate7Runtime(c6.Candidate6Runtime):
    def wait_observation(self, item: dict[str, Any], conversation: str, *, raw_expected: int = 1,
                         timeout: float = 15) -> dict[str, Any]:
        prompt = base.control_prompt(item)
        deadline = time.monotonic() + timeout
        prior_signature: str | None = None
        stable_count = 0
        last: dict[str, Any] = {}
        while time.monotonic() < deadline:
            events = self.events(conversation)
            user = next((event for event in events if event.get("kind") == "MessageEvent"
                         and event.get("source") == "user" and self.p.event_text(event) == prompt), None)
            hook = next((event for event in events if event.get("kind") == "HookExecutionEvent"
                         and (event.get("hook_input") or {}).get("message") == prompt), None)
            raws = self.raw_for(item["caseId"], item["repetition"], prompt)
            raw_ids = {record["requestId"] for record in raws}
            terminals = [record for record in base.read_jsonl(self.terminal)
                         if record.get("requestId") in raw_ids]
            detail_status, detail = self.p.http(
                "GET", self.support.INGRESS, f"/api/conversations/{conversation}",
                self.support.session_key(), timeout=15,
            )
            last = {"events": events, "user": user, "hook": hook, "raws": raws,
                    "terminals": terminals, "detailStatus": detail_status, "detail": detail}
            terminal_status = str(detail.get("execution_status")) in base.TERMINAL_STATES
            raw_complete = len(raws) == raw_expected
            stub_complete = len(terminals) == raw_expected
            terminal_event = expected_terminal_event(item, events, user, self.p.event_text)
            complete = user is not None and hook is not None and raw_complete and stub_complete \
                and terminal_status and terminal_event is not None
            if complete:
                signature = observation_signature(last, terminal_event)
                stable_count = stable_count + 1 if signature == prior_signature else 1
                prior_signature = signature
                if stable_count >= STABLE_FETCH_COUNT:
                    last["terminalEventId"] = terminal_event.get("id")
                    last["stableFetchCount"] = stable_count
                    return last
            else:
                stable_count = 0
                prior_signature = None
            time.sleep(0.05)
        self.journal.append("HARNESS_BOUNDARY_FAILURE", {
            "phase": "WAIT_STABLE_TERMINAL_EVENT",
            "caseId": item["caseId"],
            "repetition": item["repetition"],
            "userObserved": last.get("user") is not None,
            "hookObserved": last.get("hook") is not None,
            "rawCount": len(last.get("raws", [])),
            "stubTerminalCount": len(last.get("terminals", [])),
            "terminalStatus": (last.get("detail") or {}).get("execution_status"),
            "requiredTerminalEventObserved": False,
        })
        raise TimeoutError("stable required terminal event was not observed")

    def _bound_raw_source(self, workspace: Path, text: str,
                          conversations: list[str]) -> dict[str, Any]:
        require(conversations is self.conversations, "seed conversation registry identity mismatch")
        conversation = self.create_conversation(workspace, int(self.stub_port), max_iterations=1)
        status, response = self.p.http(
            "POST", self.support.INGRESS, f"/api/conversations/{conversation}/events",
            self.support.session_key(),
            {"role": "user", "run": False, "content": [{"type": "text", "text": text}]}, 30,
        )
        if status != 200:
            error_id = response.get("error_id") if isinstance(response, dict) else None
            self.journal.append("HARNESS_BOUNDARY_FAILURE", {
                "phase": "BOUND_CORPUS_SOURCE_PROMPT_SUBMIT",
                "httpStatus": status,
                "responseSha256": sha_bytes(canonical_bytes(response)),
                "responseLength": len(canonical_bytes(response)),
                "serverErrorIdSha256": sha_bytes(str(error_id).encode()) if error_id else None,
            })
            raise RuntimeError(f"bound corpus-source prompt submission failed: HTTP {status}")
        audit = self.support.wait_prompt_audit(
            self.support.session_key(), conversation, text, None, "", "", timeout=30,
        )
        return {
            "conversation_id": conversation,
            "event_id": audit["user_event_id"],
            "prompt_sha256": sha_bytes(text.encode()),
            "prompt_length": len(text),
            "hook_before_user": audit["hook_before_user"],
            "constructor": "CANDIDATE_7_BOUND_DETERMINISTIC",
        }

    def ensure_seeded(self) -> None:
        if self.seeded:
            if self.support.listener_pid(c6.BRIDGE_PORT) is None:
                self.p.start_service(False, True)
            return
        corpus = json.loads(CORPUS.read_text())["syntheticMemoryCorpus"]
        translated = [{"memoryId": item["logicalId"], "partition": item["partition"],
                       "state": item["state"], "eligible": item["eligible"],
                       "text": item["literalText"], "expectedMarker": item["marker"]}
                      for item in corpus]
        original = self.p.create_raw_source
        self.p.create_raw_source = self._bound_raw_source
        try:
            self.mapped = self.p.seed_corpus(translated, self.conversations, self.journal)
            self.seeded = True
        except Exception as error:
            self.journal.append("HARNESS_BOUNDARY_FAILURE", sanitized_failure(error, "BOUND_CORPUS_SEED"))
            raise
        finally:
            self.p.create_raw_source = original

    def run_operation(self, item: dict[str, Any]) -> list[dict[str, Any]]:
        phase = f"OPERATION:{item['caseId']}:{item['repetition']}"
        try:
            return super().run_operation(item)
        except PredicateFailure:
            raise
        except Exception as error:
            self.journal.append("HARNESS_BOUNDARY_FAILURE", sanitized_failure(error, phase))
            raise


def load_object(path: Path, label: str) -> dict[str, Any]:
    return c6.load_object(path, label)


def validate_static_contract() -> dict[str, Any]:
    value = c6.c5.validate_static_contract()
    config = load_object(CONFIG, "candidate-7 configuration")
    namespaces = config["disposableNamespaces"]
    value["candidate7ConfigurationMatchesDriver"] = (
        namespaces["mongodbDatabase"] == DATABASE
        and namespaces["qdrantSemanticCollection"] == SEMANTIC
        and namespaces["qdrantEpisodicCollection"] == EPISODIC
        and namespaces["workspaceRoot"] == str(MEASURED_ROOT)
    )
    rehearsal = dress_rehearsal_plan(json.loads(CORPUS.read_text()))
    value["candidate7HarnessCorrections"] = {
        "stableFetchCount": STABLE_FETCH_COUNT,
        "dressOperationCount": len(rehearsal),
        "fastTerminalRepetitions": sum(item["caseId"] == "SMA-S2-001-FIRST-PROMPT-EMPTY"
                                       for item in rehearsal),
        "boundSeedConstructorRequired": config["corpusSeeding"]["candidateBoundConstructorRequired"],
        "dressReceiptRequired": config["executionFence"]["passingBoundDressRehearsalReceiptRequired"],
    }
    require(value["candidate7ConfigurationMatchesDriver"], "candidate-7 namespace mismatch")
    require(value["candidate7HarnessCorrections"] == {
        "stableFetchCount": 2,
        "dressOperationCount": 34,
        "fastTerminalRepetitions": 10,
        "boundSeedConstructorRequired": True,
        "dressReceiptRequired": True,
    }, "candidate-7 harness contract mismatch")
    return value


def validate_execution_identity(identity_path: Path) -> dict[str, Any]:
    identity = load_object(identity_path, "execution identity")
    ports = c6.validate_runtime_ports(identity.get("runtimePorts"))
    prereg_ref = identity.get("preregistration", {})
    acceptance_ref = identity.get("acceptance", {})
    prereg_path = Path(str(prereg_ref.get("path", "")))
    acceptance_path = Path(str(acceptance_ref.get("path", "")))
    prereg = load_object(prereg_path, "candidate-7 preregistration") if prereg_path.is_file() else {}
    acceptance = load_object(acceptance_path, "candidate-7 acceptance") if acceptance_path.is_file() else {}
    checks = {
        "identityRecordType": identity.get("recordType") == "SMA_S2_CANDIDATE_7_EXECUTION_IDENTITY",
        "identityCandidateExact": identity.get("candidate") == 7,
        "identityAcceptedFrozen": identity.get("status") == "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
        "identityNonAuthorizing": identity.get("measuredExecutionAuthorized") is False,
        "driverMatches": c6.referenced_file_matches(identity.get("driver"), DRIVER),
        "corpusMatches": c6.referenced_file_matches(identity.get("corpus"), CORPUS),
        "configurationMatches": c6.referenced_file_matches(identity.get("configuration"), CONFIG),
        "matrixMatches": c6.referenced_file_matches(identity.get("oracleMatrix"), MATRIX),
        "stubMatches": c6.referenced_file_matches(identity.get("stub"), STUB),
        "preregistrationMatches": c6.referenced_file_matches(prereg_ref)
                                  and prereg.get("candidate") == 7,
        "acceptanceMatches": c6.referenced_file_matches(acceptance_ref)
                             and acceptance.get("status") == "ACCEPTED_FROZEN"
                             and acceptance.get("candidate") == 7
                             and acceptance.get("candidatePreregistrationSha256") == prereg_ref.get("sha256")
                             and acceptance.get("measuredExecutionAuthorized") is False,
        "dependenciesExactAndMatch": c6.dependency_closure_matches(identity, prereg),
        "stubPortAvailable": c6.port_is_available(ports["stubHost"], ports["stubPort"]),
        "bridgePortAvailable": c6.port_is_available(ports["bridgeHost"], ports["bridgePort"]),
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-7 execution identity rejected: {checks}")
    return {"identity": identity, "identitySha256": sha_file(identity_path), "runtimePorts": ports,
            "preregistration": prereg_ref, "acceptance": acceptance_ref, "checks": checks}


def validate_dress_rehearsal_fence(identity_path: Path, authorization_path: Path) -> dict[str, Any]:
    bound = validate_execution_identity(identity_path)
    authorization = load_object(authorization_path, "dress-rehearsal authorization")
    checks = {
        "recordType": authorization.get("recordType") == "SMA_S2_CANDIDATE_7_DRESS_REHEARSAL_AUTHORIZATION",
        "live": authorization.get("status") == "AUTHORIZED_NOT_CONSUMED",
        "identity": authorization.get("executionIdentitySha256") == bound["identitySha256"],
        "ports": authorization.get("runtimePorts") == bound["runtimePorts"],
        "driver": authorization.get("driverSha256") == sha_file(DRIVER),
        "configuration": authorization.get("configurationSha256") == sha_file(CONFIG),
        "preregistration": authorization.get("preregistrationSha256")
                           == bound["preregistration"].get("sha256"),
        "acceptance": authorization.get("acceptanceSha256") == bound["acceptance"].get("sha256"),
        "singleAttempt": authorization.get("maximumDressRehearsalAttempts") == 1,
        "operationCount": authorization.get("operationCount") == DRESS_OPERATION_COUNT,
        "noMeasuredCases": authorization.get("measuredCaseExecutions") == 0,
        "noMeasuredRepetitions": authorization.get("measuredRepetitionExecutions") == 0,
        "realModelProhibited": authorization.get("realModelCallsAllowed") is False,
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-7 dress-rehearsal fence rejected: {checks}")
    return {**bound, "authorizationChecks": checks}


def validate_execution_fence(identity_path: Path, authorization_path: Path) -> dict[str, Any]:
    bound = validate_execution_identity(identity_path)
    authorization = load_object(authorization_path, "measured authorization")
    dress_ref = authorization.get("dressRehearsalReceipt", {})
    dress_path = Path(str(dress_ref.get("path", "")))
    dress = load_object(dress_path, "dress-rehearsal receipt") if dress_path.is_file() else {}
    checks = {
        "recordType": authorization.get("recordType") == "SMA_S2_CANDIDATE_7_SINGLE_USE_MEASURED_AUTHORIZATION",
        "live": authorization.get("status") == "AUTHORIZED_NOT_CONSUMED",
        "singleUse": authorization.get("maximumMeasuredAttempts") == 1,
        "identity": authorization.get("executionIdentitySha256") == bound["identitySha256"],
        "ports": authorization.get("runtimePorts") == bound["runtimePorts"],
        "driver": authorization.get("driverSha256") == sha_file(DRIVER),
        "matrix": authorization.get("oracleMatrixSha256") == sha_file(MATRIX),
        "corpus": authorization.get("corpusSha256") == sha_file(CORPUS),
        "configuration": authorization.get("configurationSha256") == sha_file(CONFIG),
        "stub": authorization.get("stubSha256") == sha_file(STUB),
        "preregistration": authorization.get("preregistrationSha256")
                           == bound["preregistration"].get("sha256"),
        "acceptance": authorization.get("acceptanceSha256") == bound["acceptance"].get("sha256"),
        "scope": authorization.get("caseCount") == 20 and authorization.get("repetitionCount") == 103,
        "realModelProhibited": authorization.get("realModelCallsAllowed") is False,
        "dressReference": c6.referenced_file_matches(dress_ref),
        "dressRecordType": dress.get("recordType") == "SMA_S2_CANDIDATE_7_DRESS_REHEARSAL_RECEIPT",
        "dressPass": dress.get("status") == "PASS",
        "dressIdentity": dress.get("executionIdentitySha256") == bound["identitySha256"],
        "dressPorts": dress.get("runtimePorts") == bound["runtimePorts"],
        "dressOperations": dress.get("completedOperations") == DRESS_OPERATION_COUNT,
        "dressCleanup": dress.get("cleanupStatus") == "PASS",
        "dressNoClaim": dress.get("claim") is None,
        "dressNoMeasuredCredit": dress.get("measuredCaseExecutions") == 0
                                 and dress.get("measuredRepetitionExecutions") == 0,
    }
    if not all(checks.values()):
        raise PermissionError(f"candidate-7 measured fence rejected: {checks}")
    return {**bound, "authorizationChecks": checks, "dressReceiptSha256": dress_ref["sha256"]}


c6.Candidate6Runtime = Candidate7Runtime
c6.c5.Candidate5Runtime = Candidate7Runtime
c6.c5.c4.Candidate4Runtime = Candidate7Runtime
c6.c5.c4.base.LiveRuntime = Candidate7Runtime


def run_plan(args: argparse.Namespace, plan: list[dict[str, Any]], fence: dict[str, Any],
             *, dress: bool) -> int:
    args.output.mkdir(parents=True, exist_ok=False)
    journal = base.DurableJournal(args.output / "raw-evidence.jsonl")
    journal.append("DRESS_REHEARSAL_START" if dress else "EXECUTION_START", {
        "driverSha256": sha_file(DRIVER), "matrixSha256": sha_file(MATRIX),
        "corpusSha256": sha_file(CORPUS), "runtimePorts": fence["runtimePorts"],
        "plannedOperations": len(plan), "measuredCredit": 0 if dress else len(plan),
        "dressReceiptSha256": None if dress else fence["dressReceiptSha256"],
    })
    runtime = Candidate7Runtime(args.output, journal, fence["runtimePorts"])
    results: list[dict[str, Any]] = []
    failure: dict[str, Any] | None = None
    try:
        runtime.preflight_absence()
        runtime.start_stub()
        runtime.setup_workspaces()
        for item in plan:
            try:
                records = runtime.run_operation(item)
                results.append({"caseId": item["caseId"], "repetition": item["repetition"],
                                "status": "PASS", "recordCount": len(records)})
            except PredicateFailure as error:
                failure = {"class": "SUBSTANTIVE", "caseId": item["caseId"],
                           "repetition": item["repetition"],
                           "messageSha256": sha_bytes(str(error).encode())}
                journal.append("REPETITION_FAILURE", failure)
                results.append({"caseId": item["caseId"], "repetition": item["repetition"],
                                "status": "FAIL"})
            except Exception as error:
                failure = {"class": "HARNESS", "caseId": item["caseId"],
                           "repetition": item["repetition"], **sanitized_failure(error, "RUN_PLAN")}
                journal.append("REPETITION_FAILURE", failure)
                break
    finally:
        cleanup = runtime.cleanup()
    complete = len(results) == len(plan) and all(item["status"] == "PASS" for item in results) \
        and all(cleanup["oracleValues"].values())
    receipt = {
        "recordType": "SMA_S2_CANDIDATE_7_DRESS_REHEARSAL_RECEIPT" if dress
                      else "SMA_S2_MEASURED_EXECUTION_RECEIPT",
        "status": "PASS" if complete else "FAIL" if failure and failure["class"] == "SUBSTANTIVE"
                  else "INCONCLUSIVE",
        "claim": None if dress or not complete else "OPENHANDS_SMA_BOUNDARY_QUALIFIED",
        "executionIdentitySha256": fence["identitySha256"],
        "runtimePorts": fence["runtimePorts"],
        "completedOperations": len(results),
        "completedRepetitions": 0 if dress else len(results),
        "measuredCaseExecutions": 0 if dress else 20,
        "measuredRepetitionExecutions": 0 if dress else len(results),
        "results": results, "failure": failure, "cleanup": cleanup,
        "cleanupStatus": "PASS" if all(cleanup["oracleValues"].values()) else "FAIL",
        "rawJournalSha256": sha_file(args.output / "raw-evidence.jsonl"),
    }
    name = "dress-rehearsal-receipt.json" if dress else "execution-receipt.json"
    descriptor = os.open(args.output / name, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, canonical_bytes(receipt) + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    print(json.dumps({"status": receipt["status"], "receipt": str(args.output / name)}, sort_keys=True))
    return 0 if complete else 1


def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser()
    value.add_argument("--offline-contract", action="store_true")
    value.add_argument("--dress-rehearsal", action="store_true")
    value.add_argument("--execute", action="store_true")
    value.add_argument("--execution-identity", type=Path)
    value.add_argument("--authorization", type=Path)
    value.add_argument("--output", type=Path)
    return value


def main() -> int:
    args = parser().parse_args()
    if args.offline_contract:
        print(json.dumps(validate_static_contract(), indent=2, sort_keys=True))
        return 0
    if args.dress_rehearsal and args.execute:
        raise PermissionError("dress rehearsal and measured execution are mutually exclusive")
    if not args.dress_rehearsal and not args.execute:
        raise PermissionError("default deny: use --offline-contract or separately authorized live mode")
    if args.execution_identity is None or args.authorization is None or args.output is None:
        raise PermissionError("live mode requires identity, authorization, and output")
    if args.dress_rehearsal:
        fence = validate_dress_rehearsal_fence(args.execution_identity, args.authorization)
        return run_plan(args, dress_rehearsal_plan(json.loads(CORPUS.read_text())), fence, dress=True)
    fence = validate_execution_fence(args.execution_identity, args.authorization)
    return run_plan(args, base.exact_plan(json.loads(CORPUS.read_text())), fence, dress=False)


if __name__ == "__main__":
    raise SystemExit(main())
