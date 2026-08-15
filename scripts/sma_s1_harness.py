#!/usr/bin/env python3
"""Layered SMA-S1 qualification harness.

The self-test mode qualifies harness mechanics only. The measured mode is
fenced by a separate accepted identity, an offline self-test receipt, and a
single-use authorization. It never calls a model or starts OpenHands.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import signal
import subprocess
import sys
import threading
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable


PROTOCOL_VERSION = "1.0.0"
HARNESS_VERSION = "1.0.0"
RESPONSE_FIELDS = {
    "protocolVersion",
    "operation",
    "runId",
    "caseId",
    "repetition",
    "driverIdentity",
    "terminalState",
    "faultActivation",
    "faultObservation",
    "predicateResults",
    "evidence",
    "safetyStop",
    "cleanupCorrelationId",
}
TERMINAL_STATES = {"SUCCEEDED", "FAILED", "TIMED_OUT", "CANCELLED"}


class HarnessError(RuntimeError):
    pass


def canonical_bytes(value: object) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load_json(path: Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as handle:
        value = json.load(handle)
    if not isinstance(value, dict):
        raise HarnessError(f"expected JSON object: {path}")
    return value


def write_new_file(path: Path, value: bytes) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        offset = 0
        while offset < len(value):
            offset += os.write(descriptor, value[offset:])
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def create_once_directory(path: Path) -> None:
    if not path.parent.is_dir():
        raise HarnessError(f"output parent does not exist: {path.parent}")
    os.mkdir(path, 0o700)


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


class DurableJournal:
    def __init__(self, path: Path) -> None:
        self.path = path
        self._descriptor = os.open(
            path,
            os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_APPEND,
            0o600,
        )
        self._lock = threading.Lock()
        self._sequence = 0
        self._closed = False

    def append(self, record: dict[str, Any]) -> int:
        with self._lock:
            if self._closed:
                raise HarnessError("journal is closed")
            sequence = self._sequence
            self._sequence += 1
            persisted = dict(record)
            persisted["journalSequence"] = sequence
            payload = canonical_bytes(persisted) + b"\n"
            offset = 0
            while offset < len(payload):
                offset += os.write(self._descriptor, payload[offset:])
            os.fsync(self._descriptor)
            return sequence

    @property
    def count(self) -> int:
        with self._lock:
            return self._sequence

    def close(self) -> None:
        with self._lock:
            if self._closed:
                return
            os.fsync(self._descriptor)
            os.close(self._descriptor)
            self._closed = True

    def __enter__(self) -> "DurableJournal":
        return self

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> None:
        self.close()


def read_journal(path: Path) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    with path.open("r", encoding="utf-8") as handle:
        for line_number, line in enumerate(handle, start=1):
            if not line.endswith("\n"):
                raise HarnessError(f"journal line {line_number} lacks newline")
            value = json.loads(line)
            if not isinstance(value, dict):
                raise HarnessError(f"journal line {line_number} is not an object")
            records.append(value)
    return records


def scan_tree_for_bytes(root: Path, forbidden: bytes) -> list[str]:
    matches: list[str] = []
    for path in sorted(root.rglob("*")):
        if path.is_file() and forbidden in path.read_bytes():
            matches.append(str(path.relative_to(root)))
    return matches


@dataclass(frozen=True)
class ProcessResult:
    return_code: int | None
    stdout: bytes
    stderr: bytes
    timed_out: bool
    elapsed_ns: int


def run_bounded_process(command: list[str], stdin: bytes, timeout_seconds: float) -> ProcessResult:
    started = time.monotonic_ns()
    process = subprocess.Popen(
        command,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        start_new_session=True,
    )
    try:
        stdout, stderr = process.communicate(stdin, timeout=timeout_seconds)
        return ProcessResult(
            process.returncode,
            stdout,
            stderr,
            False,
            time.monotonic_ns() - started,
        )
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        stdout, stderr = process.communicate()
        return ProcessResult(
            process.returncode,
            stdout,
            stderr,
            True,
            time.monotonic_ns() - started,
        )


def validate_driver_response(
    response: dict[str, Any],
    request: dict[str, Any],
) -> None:
    if set(response) != RESPONSE_FIELDS:
        raise HarnessError("driver response fields do not exactly match the protocol")
    for key in ("protocolVersion", "operation", "runId", "caseId", "repetition"):
        if response[key] != request[key]:
            raise HarnessError(f"driver response identity mismatch: {key}")
    if response["terminalState"] not in TERMINAL_STATES:
        raise HarnessError("invalid driver terminal state")
    if not isinstance(response["driverIdentity"], str) or not response["driverIdentity"]:
        raise HarnessError("missing driver identity")
    if not isinstance(response["predicateResults"], list):
        raise HarnessError("predicateResults must be an array")
    if not isinstance(response["safetyStop"], bool):
        raise HarnessError("safetyStop must be boolean")
    for result in response["predicateResults"]:
        if set(result) != {"predicate", "passed", "attributedComponent", "evidenceIds"}:
            raise HarnessError("predicate result fields do not match the protocol")
        if not isinstance(result["passed"], bool):
            raise HarnessError("predicate passed value must be boolean")
        if not isinstance(result["evidenceIds"], list):
            raise HarnessError("predicate evidenceIds must be an array")


def invoke_driver(
    driver: Path,
    request: dict[str, Any],
    timeout_seconds: float,
) -> tuple[dict[str, Any], ProcessResult]:
    response, result, error_class = capture_driver(driver, request, timeout_seconds)
    if error_class is not None or response is None:
        raise HarnessError(
            f"driver invocation failed: {error_class}; "
            f"stdoutSHA256={sha256_bytes(result.stdout)}; "
            f"stderrSHA256={sha256_bytes(result.stderr)}"
        )
    return response, result


def capture_driver(
    driver: Path,
    request: dict[str, Any],
    timeout_seconds: float,
) -> tuple[dict[str, Any] | None, ProcessResult, str | None]:
    result = run_bounded_process(
        [sys.executable, str(driver)],
        canonical_bytes(request),
        timeout_seconds,
    )
    if result.timed_out:
        return None, result, "DRIVER_TIMEOUT"
    if result.return_code != 0:
        return None, result, "DRIVER_NONZERO_EXIT"
    try:
        response = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None, result, "DRIVER_MALFORMED_JSON"
    if not isinstance(response, dict):
        return None, result, "DRIVER_NON_OBJECT_RESPONSE"
    try:
        validate_driver_response(response, request)
    except HarnessError:
        return None, result, "DRIVER_PROTOCOL_VIOLATION"
    return response, result, None


class FaultControl:
    def __init__(self) -> None:
        self._active: set[str] = set()
        self._lock = threading.Lock()

    def activate(self, fault: str) -> None:
        with self._lock:
            self._active.add(fault)

    def deactivate(self, fault: str) -> None:
        with self._lock:
            self._active.discard(fault)

    def observed(self, fault: str) -> bool:
        with self._lock:
            return fault in self._active


class SelfTestRunner:
    def __init__(
        self,
        output_root: Path,
        manifest: Path,
        identity: Path,
        protocol: Path,
        fixture_driver: Path,
    ) -> None:
        self.output_root = output_root
        self.manifest_path = manifest
        self.identity_path = identity
        self.protocol_path = protocol
        self.fixture_driver_path = fixture_driver
        self.results: list[dict[str, Any]] = []
        self.journal_path = output_root / "self-test-journal.jsonl"

    def evaluate(
        self,
        journal: DurableJournal,
        test_id: str,
        evidence: dict[str, Any],
        predicate: Callable[[], bool],
    ) -> None:
        evidence_id = f"evidence:{test_id}:{uuid.uuid4()}"
        journal.append(
            {
                "recordType": "HARNESS_SELF_TEST_PREASSERTION_EVIDENCE",
                "testId": test_id,
                "evidenceId": evidence_id,
                "observed": evidence,
            }
        )
        passed = False
        error_class: str | None = None
        try:
            passed = bool(predicate())
        except Exception as exc:  # retained as a class only; no body can leak
            error_class = type(exc).__name__
        result = {
            "testId": test_id,
            "status": "PASS" if passed else "FAIL",
            "evidenceId": evidence_id,
            "errorClass": error_class,
        }
        journal.append({"recordType": "HARNESS_SELF_TEST_RESULT", **result})
        self.results.append(result)

    def run(self) -> int:
        create_once_directory(self.output_root)
        with DurableJournal(self.journal_path) as journal:
            self._content_address_test(journal)
            self._create_once_test(journal)
            self._concurrent_journal_test(journal)
            self._fault_control_test(journal)
            self._preassertion_failure_test(journal)
            self._timeout_termination_test(journal)
            self._driver_protocol_test(journal)
            self._secret_exclusion_test(journal)
            self._cleanup_test(journal)
            self._reconstruction_test(journal)
            overall = "PASS" if all(item["status"] == "PASS" for item in self.results) else "FAIL"
            journal.append(
                {
                    "recordType": "HARNESS_SELF_TEST_SUMMARY",
                    "status": overall,
                    "testCount": len(self.results),
                    "passCount": sum(item["status"] == "PASS" for item in self.results),
                    "failCount": sum(item["status"] == "FAIL" for item in self.results),
                }
            )

        records = read_journal(self.journal_path)
        overall = "PASS" if all(item["status"] == "PASS" for item in self.results) else "FAIL"
        receipt = {
            "schemaVersion": "1.0.0",
            "recordType": "SMA_S1_HARNESS_OFFLINE_SELF_TEST_RECEIPT",
            "status": overall,
            "scope": "HARNESS_MECHANICS_ONLY_NO_SMA_PRODUCT_CREDIT",
            "recordedAt": utc_now(),
            "harnessVersion": HARNESS_VERSION,
            "harnessSHA256": sha256_file(Path(__file__).resolve()),
            "fixtureDriverSHA256": sha256_file(self.fixture_driver_path),
            "protocolSHA256": sha256_file(self.protocol_path),
            "manifestSHA256": sha256_file(self.manifest_path),
            "identityDraftSHA256": sha256_file(self.identity_path),
            "journalSHA256": sha256_file(self.journal_path),
            "journalRecordCount": len(records),
            "tests": self.results,
            "measuredCasesExecuted": 0,
            "smaServiceStarted": False,
            "mongoOrQdrantMutated": False,
            "openhandsOrModelUsed": False,
            "executionAuthorityCreated": False,
        }
        receipt_path = self.output_root / "self-test-receipt.json"
        write_new_file(receipt_path, canonical_bytes(receipt) + b"\n")
        print(
            f"SMA-S1 harness offline self-test {overall}: "
            f"{len(self.results)} tests, 0 measured cases"
        )
        return 0 if overall == "PASS" else 1

    def _content_address_test(self, journal: DurableJournal) -> None:
        manifest = load_json(self.manifest_path)
        identity = load_json(self.identity_path)
        manifest_hash = sha256_file(self.manifest_path)
        self.evaluate(
            journal,
            "CONTENT_ADDRESS_AND_FENCE",
            {
                "manifestSHA256": manifest_hash,
                "identityManifestSHA256": identity.get("scientificPreregistration", {}).get("sha256"),
                "manifestStatus": manifest.get("status"),
                "manifestExecutionAuthorized": manifest.get("executionPolicy", {}).get("executionAuthorized"),
                "identityExecutionAuthorized": identity.get("executionFence", {}).get("executionAuthorized"),
            },
            lambda: (
                manifest.get("status") == "ACCEPTED_FROZEN_NOT_AUTHORIZED"
                and identity.get("scientificPreregistration", {}).get("sha256") == manifest_hash
                and manifest.get("executionPolicy", {}).get("executionAuthorized") is False
                and identity.get("executionFence", {}).get("executionAuthorized") is False
            ),
        )

    def _create_once_test(self, journal: DurableJournal) -> None:
        target = self.output_root / "create-once-control"
        create_once_directory(target)
        rejected = False
        try:
            create_once_directory(target)
        except FileExistsError:
            rejected = True
        self.evaluate(
            journal,
            "CREATE_ONCE_OUTPUT_ROOT",
            {"firstCreateSucceeded": True, "secondCreateRejected": rejected},
            lambda: rejected,
        )

    def _concurrent_journal_test(self, journal: DurableJournal) -> None:
        before = journal.count
        thread_count = 8
        records_per_thread = 8

        def writer(thread_id: int) -> None:
            for item in range(records_per_thread):
                journal.append(
                    {
                        "recordType": "HARNESS_CONCURRENCY_CONTROL",
                        "thread": thread_id,
                        "item": item,
                    }
                )

        threads = [threading.Thread(target=writer, args=(index,)) for index in range(thread_count)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        appended = journal.count - before
        self.evaluate(
            journal,
            "CONCURRENT_APPEND_FLUSH_FSYNC",
            {
                "threadCount": thread_count,
                "recordsPerThread": records_per_thread,
                "appended": appended,
            },
            lambda: appended == thread_count * records_per_thread,
        )

    def _fault_control_test(self, journal: DurableJournal) -> None:
        control = FaultControl()
        fault = "SELF_TEST_FAULT"
        negative = control.observed(fault)
        control.activate(fault)
        positive = control.observed(fault)
        control.deactivate(fault)
        recovered = not control.observed(fault)
        self.evaluate(
            journal,
            "FAULT_POSITIVE_NEGATIVE_CONTROLS",
            {"negativeBefore": negative, "positiveDuring": positive, "negativeAfter": recovered},
            lambda: not negative and positive and recovered,
        )

    def _preassertion_failure_test(self, journal: DurableJournal) -> None:
        evidence_id = f"deliberate:{uuid.uuid4()}"
        journal.append(
            {
                "recordType": "HARNESS_SELF_TEST_PREASSERTION_EVIDENCE",
                "testId": "DELIBERATE_ASSERTION_FAILURE",
                "evidenceId": evidence_id,
                "observed": {"deliberatePredicate": False},
            }
        )
        assertion_failed = False
        try:
            if not False:
                raise AssertionError("deliberate")
        except AssertionError:
            assertion_failed = True
        persisted = any(
            item.get("evidenceId") == evidence_id for item in read_journal(self.journal_path)
        )
        self.evaluate(
            journal,
            "PREASSERTION_PERSISTENCE",
            {"assertionFailed": assertion_failed, "evidencePersistedBeforeFailure": persisted},
            lambda: assertion_failed and persisted,
        )

    def _timeout_termination_test(self, journal: DurableJournal) -> None:
        result = run_bounded_process(
            [sys.executable, "-c", "import time; time.sleep(30)"],
            b"",
            0.10,
        )
        self.evaluate(
            journal,
            "TIMEOUT_CANCELLATION_PROCESS_TERMINATION",
            {
                "timedOut": result.timed_out,
                "returnCode": result.return_code,
                "elapsedNanoseconds": result.elapsed_ns,
                "stdoutLength": len(result.stdout),
                "stderrLength": len(result.stderr),
            },
            lambda: result.timed_out and result.return_code is not None,
        )

    def _driver_protocol_test(self, journal: DurableJournal) -> None:
        manifest_hash = sha256_file(self.manifest_path)
        identity_hash = sha256_file(self.identity_path)
        request = {
            "protocolVersion": PROTOCOL_VERSION,
            "operation": "SELF_TEST",
            "runId": "sma-s1-harness-self-test",
            "caseId": "SELF_TEST_DRIVER_PROTOCOL",
            "repetition": 1,
            "manifestSHA256": manifest_hash,
            "identitySHA256": identity_hash,
            "fault": "SELF_TEST_FAULT",
            "fixture": {"predicate": "fixture predicate"},
            "namespaces": {"type": "SELF_TEST_NO_EXTERNAL_NAMESPACE"},
        }
        response, process = invoke_driver(self.fixture_driver_path, request, 5.0)
        self.evaluate(
            journal,
            "DETERMINISTIC_DRIVER_PROTOCOL",
            {
                "requestSHA256": sha256_bytes(canonical_bytes(request)),
                "responseSHA256": sha256_bytes(canonical_bytes(response)),
                "driverIdentity": response["driverIdentity"],
                "terminalState": response["terminalState"],
                "faultActivated": response["faultActivation"].get("activated"),
                "faultObserved": response["faultObservation"].get("observed"),
                "elapsedNanoseconds": process.elapsed_ns,
            },
            lambda: (
                response["driverIdentity"] == "SMA_S1_SELF_TEST_FIXTURE_ONLY"
                and response["terminalState"] == "SUCCEEDED"
                and response["faultActivation"].get("activated") is True
                and response["faultObservation"].get("observed") is True
                and response["predicateResults"][0]["passed"] is True
                and response["safetyStop"] is False
            ),
        )

    def _secret_exclusion_test(self, journal: DurableJournal) -> None:
        secret = b"sk-test-SMA-S1-HARNESS-NEVER-WRITE"
        journal.append(
            {
                "recordType": "HARNESS_SECRET_CONTROL",
                "bodySHA256": sha256_bytes(secret),
                "bodyLength": len(secret),
                "classification": "SYNTHETIC_SECRET",
                "secretDetected": True,
                "retainedBody": False,
            }
        )
        matches = scan_tree_for_bytes(self.output_root, secret)
        self.evaluate(
            journal,
            "SECRET_OPERATIONAL_OUTPUT_EXCLUSION",
            {"matchCount": len(matches), "retainedBody": False},
            lambda: not matches,
        )

    def _cleanup_test(self, journal: DurableJournal) -> None:
        target = self.output_root / "cleanup-control"
        target.mkdir()
        (target / "resource-a").write_bytes(b"synthetic")
        before = sorted(path.name for path in target.iterdir())
        shutil.rmtree(target)
        first_absent = not target.exists()
        if target.exists():
            shutil.rmtree(target)
        second_absent = not target.exists()
        self.evaluate(
            journal,
            "CLEANUP_IDEMPOTENCY_AND_INVENTORY",
            {"before": before, "firstAbsent": first_absent, "secondAbsent": second_absent},
            lambda: before == ["resource-a"] and first_absent and second_absent,
        )

    def _reconstruction_test(self, journal: DurableJournal) -> None:
        records = read_journal(self.journal_path)
        sequences = [item.get("journalSequence") for item in records]
        concurrency = [
            item for item in records if item.get("recordType") == "HARNESS_CONCURRENCY_CONTROL"
        ]
        self.evaluate(
            journal,
            "RECEIPT_RECONSTRUCTION_FROM_JOURNAL",
            {
                "recordCountBeforeReconstructionAssertion": len(records),
                "sequenceCount": len(sequences),
                "uniqueSequenceCount": len(set(sequences)),
                "concurrencyRecordCount": len(concurrency),
            },
            lambda: (
                sequences == list(range(len(sequences)))
                and len(concurrency) == 64
            ),
        )


def validate_measured_authorization(
    manifest_path: Path,
    identity_path: Path,
    authorization_path: Path,
    harness_receipt_path: Path,
    driver_path: Path,
) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    manifest = load_json(manifest_path)
    identity = load_json(identity_path)
    authorization = load_json(authorization_path)
    receipt = load_json(harness_receipt_path)
    if manifest.get("status") != "ACCEPTED_FROZEN_NOT_AUTHORIZED":
        raise HarnessError("scientific preregistration is not accepted and frozen")
    if identity.get("status") != "SEALED_ACCEPTED_NOT_AUTHORIZING_NOT_STARTED":
        raise HarnessError("execution identity is not sealed and accepted")
    if identity.get("executionFence", {}).get("identityAccepted") is not True:
        raise HarnessError("execution identity acceptance fence is false")
    if receipt.get("status") != "PASS" or receipt.get("measuredCasesExecuted") != 0:
        raise HarnessError("offline harness receipt is not an eligible PASS")
    if authorization.get("recordType") != "SMA_S1_EXECUTION_AUTHORIZATION":
        raise HarnessError("wrong authorization record type")
    if authorization.get("status") != "AUTHORIZED_UNUSED":
        raise HarnessError("authorization is not unused")
    if authorization.get("executionAuthorized") is not True:
        raise HarnessError("execution authorization fence is false")
    if authorization.get("attemptsAllowed") != 1:
        raise HarnessError("authorization must allow exactly one attempt")
    bindings = authorization.get("bindings", {})
    expected = {
        "manifestSHA256": sha256_file(manifest_path),
        "identitySHA256": sha256_file(identity_path),
        "harnessSHA256": sha256_file(Path(__file__).resolve()),
        "driverSHA256": sha256_file(driver_path),
        "offlineHarnessReceiptSHA256": sha256_file(harness_receipt_path),
    }
    if bindings != expected:
        raise HarnessError("authorization bindings do not match exact artifacts")
    return manifest, identity, authorization


def run_measured(args: argparse.Namespace) -> int:
    manifest_path = Path(args.manifest).resolve()
    identity_path = Path(args.identity).resolve()
    authorization_path = Path(args.authorization).resolve()
    harness_receipt_path = Path(args.harness_receipt).resolve()
    driver_path = Path(args.driver).resolve()
    manifest, identity, authorization = validate_measured_authorization(
        manifest_path,
        identity_path,
        authorization_path,
        harness_receipt_path,
        driver_path,
    )
    output_root = Path(args.output_root).resolve()
    create_once_directory(output_root)
    journal_path = output_root / "raw-receipts.jsonl"
    run_id = authorization["authorizationId"]
    failures = 0
    completed = 0
    safety_stop = False
    harness_failure = False
    cleanup_passed = False
    journal = DurableJournal(journal_path)

    planned_isolation = identity["plannedIsolation"]
    driver_namespaces = {
        "mongodbDatabase": planned_isolation["measuredMongoDatabase"],
        "semanticCollection": planned_isolation["measuredSemanticCollection"],
        "episodicCollection": planned_isolation["measuredEpisodicCollection"],
        "actorAlphaPartition": planned_isolation["actorAlphaPartition"],
        "actorBetaPartition": planned_isolation["actorBetaPartition"],
        "productionOrHistoricalDataAllowed": False,
    }

    def request_for(
        operation: str,
        case_id: str,
        repetition: int,
        fault: str,
        fixture: dict[str, Any],
    ) -> dict[str, Any]:
        return {
            "protocolVersion": PROTOCOL_VERSION,
            "operation": operation,
            "runId": run_id,
            "caseId": case_id,
            "repetition": repetition,
            "manifestSHA256": sha256_file(manifest_path),
            "identitySHA256": sha256_file(identity_path),
            "fault": fault,
            "fixture": fixture,
            "namespaces": driver_namespaces,
        }

    def capture_and_persist(
        request: dict[str, Any],
        record_type: str,
    ) -> tuple[dict[str, Any] | None, str | None, str]:
        response, process, error_class = capture_driver(
            driver_path,
            request,
            args.driver_timeout,
        )
        evidence_id = f"{request['caseId']}:{request['repetition']}:{uuid.uuid4()}"
        journal.append(
            {
                "recordType": record_type,
                "evidenceId": evidence_id,
                "caseId": request["caseId"],
                "repetition": request["repetition"],
                "operation": request["operation"],
                "requestSHA256": sha256_bytes(canonical_bytes(request)),
                "validatedResponse": response,
                "driverErrorClass": error_class,
                "driverElapsedNanoseconds": process.elapsed_ns,
                "driverTimedOut": process.timed_out,
                "driverReturnCode": process.return_code,
                "driverStdoutSHA256": sha256_bytes(process.stdout),
                "driverStdoutLength": len(process.stdout),
                "driverStderrSHA256": sha256_bytes(process.stderr),
                "driverStderrLength": len(process.stderr),
            }
        )
        return response, error_class, evidence_id

    try:
        for operation in ("INVENTORY", "PREPARE"):
            control_request = request_for(
                operation,
                f"__{operation}__",
                0,
                "NONE",
                {"predicates": []},
            )
            response, error_class, evidence_id = capture_and_persist(
                control_request,
                "SMA_S1_CONTROL_PREASSERTION_EVIDENCE",
            )
            passed = (
                error_class is None
                and response is not None
                and response["terminalState"] == "SUCCEEDED"
                and response["predicateResults"] == []
                and response["safetyStop"] is False
            )
            journal.append(
                {
                    "recordType": "SMA_S1_CONTROL_ASSERTION_RESULT",
                    "evidenceId": evidence_id,
                    "operation": operation,
                    "status": "PASS" if passed else "FAIL",
                    "failureClass": None if passed else "HARNESS",
                }
            )
            if not passed:
                harness_failure = True
                break

        if not harness_failure:
            for case in manifest["cases"]:
                for repetition in range(1, int(case["repetitions"]) + 1):
                    request = request_for(
                        "EXECUTE_CASE",
                        case["id"],
                        repetition,
                        case["fault"],
                        {
                            "predicates": case["predicates"],
                            "syntheticCorpus": manifest["syntheticCorpus"],
                        },
                    )
                    response, error_class, evidence_id = capture_and_persist(
                        request,
                        "SMA_S1_PREASSERTION_EVIDENCE",
                    )
                    if error_class is not None or response is None:
                        journal.append(
                            {
                                "recordType": "SMA_S1_ASSERTION_RESULT",
                                "evidenceId": evidence_id,
                                "caseId": case["id"],
                                "repetition": repetition,
                                "status": "INCONCLUSIVE",
                                "failureClass": "HARNESS",
                                "safetyStop": False,
                            }
                        )
                        harness_failure = True
                        break
                    expected_predicates = case["predicates"]
                    actual_predicates = [
                        item["predicate"] for item in response["predicateResults"]
                    ]
                    passed = (
                        response["terminalState"] == "SUCCEEDED"
                        and actual_predicates == expected_predicates
                        and all(item["passed"] for item in response["predicateResults"])
                        and response["faultActivation"].get("activated") is True
                        and response["faultObservation"].get("observed") is True
                    )
                    journal.append(
                        {
                            "recordType": "SMA_S1_ASSERTION_RESULT",
                            "evidenceId": evidence_id,
                            "caseId": case["id"],
                            "repetition": repetition,
                            "status": "PASS" if passed else "FAIL",
                            "failureClass": None if passed else "SUBSTANTIVE",
                            "safetyStop": response["safetyStop"],
                        }
                    )
                    completed += 1
                    if not passed:
                        failures += 1
                    if response["safetyStop"]:
                        safety_stop = True
                        break
                if safety_stop or harness_failure:
                    break
    except Exception as exc:
        harness_failure = True
        journal.append(
            {
                "recordType": "SMA_S1_HARNESS_FAILURE",
                "errorClass": type(exc).__name__,
                "messageRetained": False,
            }
        )
    finally:
        cleanup_request = request_for(
            "CLEANUP",
            "__CLEANUP__",
            0,
            "NONE",
            {"predicates": []},
        )
        try:
            response, error_class, evidence_id = capture_and_persist(
                cleanup_request,
                "SMA_S1_CLEANUP_PREASSERTION_EVIDENCE",
            )
            cleanup_passed = (
                error_class is None
                and response is not None
                and response["terminalState"] == "SUCCEEDED"
                and response["predicateResults"] == []
                and response["safetyStop"] is False
            )
            journal.append(
                {
                    "recordType": "SMA_S1_CLEANUP_ASSERTION_RESULT",
                    "evidenceId": evidence_id,
                    "status": "PASS" if cleanup_passed else "FAIL",
                    "failureClass": None if cleanup_passed else "HARNESS",
                }
            )
        except Exception as exc:
            journal.append(
                {
                    "recordType": "SMA_S1_CLEANUP_ASSERTION_RESULT",
                    "status": "FAIL",
                    "failureClass": "HARNESS",
                    "errorClass": type(exc).__name__,
                    "messageRetained": False,
                }
            )
        overall = (
            "PASS"
            if failures == 0 and not safety_stop and not harness_failure and cleanup_passed
            else "INCONCLUSIVE"
            if harness_failure or not cleanup_passed
            else "FAIL"
        )
        journal.append(
            {
                "recordType": "SMA_S1_RUN_SUMMARY",
                "completedRepetitions": completed,
                "failedRepetitions": failures,
                "safetyStop": safety_stop,
                "harnessFailure": harness_failure,
                "cleanupPassed": cleanup_passed,
                "status": overall,
            }
        )
        journal.close()

    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_S1_EXECUTION_RECEIPT",
        "status": overall,
        "authorizationId": run_id,
        "manifestSHA256": sha256_file(manifest_path),
        "identitySHA256": sha256_file(identity_path),
        "authorizationSHA256": sha256_file(authorization_path),
        "harnessSHA256": sha256_file(Path(__file__).resolve()),
        "driverSHA256": sha256_file(driver_path),
        "offlineHarnessReceiptSHA256": sha256_file(harness_receipt_path),
        "journalSHA256": sha256_file(journal_path),
        "journalRecordCount": len(read_journal(journal_path)),
        "completedRepetitions": completed,
        "failedRepetitions": failures,
        "safetyStop": safety_stop,
        "harnessFailure": harness_failure,
        "cleanupPassed": cleanup_passed,
    }
    write_new_file(
        output_root / "execution-receipt.json",
        canonical_bytes(receipt) + b"\n",
    )
    print(
        f"SMA-S1 measured run {overall}: completed={completed} failures={failures} "
        f"safetyStop={str(safety_stop).lower()} cleanupPassed={str(cleanup_passed).lower()}"
    )
    return 0 if overall == "PASS" else 1


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="mode", required=True)

    self_test = subparsers.add_parser("self-test")
    self_test.add_argument("--output-root", required=True)
    self_test.add_argument("--manifest", required=True)
    self_test.add_argument("--identity", required=True)
    self_test.add_argument("--protocol", required=True)
    self_test.add_argument("--fixture-driver", required=True)

    measured = subparsers.add_parser("measured")
    measured.add_argument("--output-root", required=True)
    measured.add_argument("--manifest", required=True)
    measured.add_argument("--identity", required=True)
    measured.add_argument("--authorization", required=True)
    measured.add_argument("--harness-receipt", required=True)
    measured.add_argument("--driver", required=True)
    measured.add_argument("--driver-timeout", type=float, default=120.0)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        if args.mode == "self-test":
            runner = SelfTestRunner(
                Path(args.output_root).resolve(),
                Path(args.manifest).resolve(),
                Path(args.identity).resolve(),
                Path(args.protocol).resolve(),
                Path(args.fixture_driver).resolve(),
            )
            return runner.run()
        return run_measured(args)
    except (HarnessError, FileExistsError, OSError, ValueError, KeyError) as exc:
        print(f"SMA-S1 harness refused: {type(exc).__name__}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
