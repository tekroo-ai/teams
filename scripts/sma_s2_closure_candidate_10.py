#!/usr/bin/env python3
"""Consolidated SMA-S2 candidate-10 qualification runner.

This module contains the shared case runner and offline adapter contract.  It
does not import any predecessor measured driver.  Live execution remains
default-deny and is intentionally unavailable until a separately accepted
package, execution identity, and authorization exist.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sys
import threading
from dataclasses import dataclass, field
from enum import Enum
from pathlib import Path
from typing import Any, Iterable, Protocol


ROOT = Path(__file__).resolve().parents[1]
PREREGISTRATION = ROOT / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-2.json"
PREREGISTRATION_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-2-acceptance.json"
CORPUS = ROOT / "investigations/sma-q1/layered/sma-s2-measured-corpus-candidate-1.json"
MATRIX = ROOT / "investigations/sma-q1/layered/sma-s2-predicate-oracle-matrix-candidate-4.json"
FRAMING = ROOT / "investigations/sma-q1/layered/sma-s2-context-framing-contract-candidate-10.json"
STATE_MANIFEST = ROOT / "investigations/sma-q1/layered/sma-s2-state-transition-manifest-candidate-10.json"
CONFIGURATION = ROOT / "investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-10.json"
AUTHORIZATION = ROOT / "investigations/sma-q1/layered/sma-s2-candidate-10-h0-authorization.json"
RAW_RUNTIME = ROOT / "scripts/sma_s2_closure_candidate_10_runtime.py"

BOUND_SHA256 = {
    PREREGISTRATION: "b7315835b6291d44d3d8cb918dbec1f16c1e64f2106fd70212c6a8f7ec678188",
    PREREGISTRATION_ACCEPTANCE: "1c0cd9a01717b5e351bedde6971626d8104a482ac31bd95222ea1d53c408db9c",
    CORPUS: "c273c148a7b289f6650a26a82c38bda9680904701120e083543e9b7954b9a960",
    MATRIX: "806e12a59dd8dcbc8f248d0a8d486c1afb856fc310e1952f7855e9fdf955d4f4",
}

EXPECTED_CASES = 20
EXPECTED_REPETITIONS = 103
EXPECTED_DRESS_OPERATIONS = 34
EXPECTED_PREDICATES = 59
EXPECTED_EVIDENCE = 10
SECRET = "sk-test-SMA-S2-NEVER-PERSIST"


def canonical_bytes(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()


def sha_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        while block := stream.read(1024 * 1024):
            digest.update(block)
    return digest.hexdigest()


def boundary_receipt_value(value: Any) -> Any:
    """Convert only explicitly supported boundary receipts to canonical JSON values."""
    if value is None or isinstance(value, (str, int, float, bool)):
        return value
    if isinstance(value, EventKey):
        return {"conversationId": value.conversation_id, "eventId": value.event_id}
    if isinstance(value, CleanupReceipt):
        return {
            "serviceAbsent": value.service_absent,
            "conversationsAbsent": value.conversations_absent,
            "workspacesAbsent": value.workspaces_absent,
            "namespacesAbsent": value.namespaces_absent,
            "stubAbsent": value.stub_absent,
            "activeWork": value.active_work, "queuedWork": value.queued_work,
            "emergencyCleanupUsed": value.emergency_cleanup_used,
            "initialCleanupFailureObserved": value.initial_cleanup_failure_observed,
            "emergencyCleanupSucceeded": value.emergency_cleanup_succeeded,
        }
    if isinstance(value, tuple):
        return [boundary_receipt_value(item) for item in value]
    if isinstance(value, list):
        return [boundary_receipt_value(item) for item in value]
    if isinstance(value, dict):
        if all(isinstance(key, str) for key in value):
            return {key: boundary_receipt_value(item) for key, item in value.items()}
        if all(isinstance(key, tuple) and len(key) == 2 for key in value):
            return [{"conversationId": str(key[0]), "eventId": str(key[1]),
                     "cardinality": boundary_receipt_value(item)}
                    for key, item in sorted(value.items())]
    raise HarnessFailure(f"unsupported noncanonical boundary receipt: {type(value).__name__}")


def load_object(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text())
    if not isinstance(value, dict):
        raise HarnessFailure(f"object required: {path}")
    return value


class ScientificFailure(RuntimeError):
    """Complete evidence shows one or more preregistered predicates false."""


class HarnessFailure(RuntimeError):
    """The qualification implementation or fixture cannot support a result."""


class EnvironmentFailure(RuntimeError):
    """A bound external identity or dependency is unavailable or changed."""


class SafetyFailure(RuntimeError):
    """A leakage, authority, collision, or cleanup boundary requires stop."""


class ServiceMode(str, Enum):
    OFF = "OFF"
    RETRIEVAL_ONLY = "RETRIEVAL_ONLY"
    CAPTURE_ONLY = "CAPTURE_ONLY"
    CAPTURE_AND_RETRIEVAL = "CAPTURE_AND_RETRIEVAL"


@dataclass(frozen=True)
class EventKey:
    conversation_id: str
    event_id: str

    def value(self) -> tuple[str, str]:
        return self.conversation_id, self.event_id


@dataclass
class JournalRecord:
    sequence: int
    kind: str
    record_id: str
    data: dict[str, Any]


class EvidenceLedger:
    def __init__(self, fail_on_kind: str | None = None) -> None:
        self.records: list[JournalRecord] = []
        self.fail_on_kind = fail_on_kind
        self._lock = threading.Lock()
        self._durable_path: Path | None = None

    def activate_durable(self, path: Path) -> None:
        with self._lock:
            if self._durable_path is not None or path.exists():
                raise HarnessFailure("durable evidence ledger path is reused")
            descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            try:
                for record in self.records:
                    row = {"sequence": record.sequence, "kind": record.kind,
                           "recordId": record.record_id, **record.data}
                    os.write(descriptor, canonical_bytes(row) + b"\n")
                os.fsync(descriptor)
            finally:
                os.close(descriptor)
            self._durable_path = path

    def append(self, kind: str, data: dict[str, Any]) -> JournalRecord:
        with self._lock:
            if kind == self.fail_on_kind:
                raise HarnessFailure("injected durable-journal failure")
            sequence = len(self.records) + 1
            record_id = sha_bytes(canonical_bytes({"sequence": sequence, "kind": kind, "data": data}))
            record = JournalRecord(sequence, kind, record_id, data)
            self.records.append(record)
            if self._durable_path is not None:
                descriptor = os.open(self._durable_path, os.O_WRONLY | os.O_APPEND)
                try:
                    row = {"sequence": record.sequence, "kind": record.kind,
                           "recordId": record.record_id, **record.data}
                    os.write(descriptor, canonical_bytes(row) + b"\n")
                    os.fsync(descriptor)
                finally:
                    os.close(descriptor)
            return record

    def has_record_before(self, record_id: str, later_sequence: int) -> bool:
        return any(record.record_id == record_id and record.sequence < later_sequence
                   for record in self.records)

    def canonical_records(self) -> list[dict[str, Any]]:
        return [{"sequence": record.sequence, "kind": record.kind,
                 "recordId": record.record_id, **record.data} for record in self.records]


@dataclass
class ContextReceipt:
    valid: bool
    memory_ids: list[str]
    length: int
    sha256: str
    error: str | None


def parse_context(context: str, contract: dict[str, Any]) -> ContextReceipt:
    encoded = context.encode("utf-8")
    if context == "":
        return ContextReceipt(True, [], 0, sha_bytes(encoded), None)
    preamble = str(contract["safetyPreamble"])
    if not context.startswith(preamble):
        return ContextReceipt(False, [], len(context), sha_bytes(encoded), "SAFETY_PREAMBLE_MISMATCH")
    if len(context) > int(contract["bounds"]["maximumContextCharacters"]):
        return ContextReceipt(False, [], len(context), sha_bytes(encoded), "PRODUCT_CHARACTER_BOUND")
    memory_pattern = str(contract["frame"]["memoryIdPattern"])
    frame = re.compile(
        rf"\n--- MEMORY ({memory_pattern}) ---\n(.*?)\n--- END MEMORY \1 ---",
        re.DOTALL,
    )
    position = len(preamble)
    memory_ids: list[str] = []
    while position < len(context):
        match = frame.match(context, position)
        if match is None:
            return ContextReceipt(False, memory_ids, len(context), sha_bytes(encoded), "FRAME_GRAMMAR")
        memory_ids.append(match.group(1))
        position = match.end()
    if not memory_ids:
        return ContextReceipt(False, [], len(context), sha_bytes(encoded), "NO_COMPLETE_FRAME")
    if len(memory_ids) != len(set(memory_ids)):
        return ContextReceipt(False, memory_ids, len(context), sha_bytes(encoded), "DUPLICATE_MEMORY_ID")
    if len(memory_ids) > int(contract["bounds"]["maximumResults"]):
        return ContextReceipt(False, memory_ids, len(context), sha_bytes(encoded), "PRODUCT_RESULT_BOUND")
    return ContextReceipt(True, memory_ids, len(context), sha_bytes(encoded), None)


def frame_context(contract: dict[str, Any], memories: list[tuple[str, str]]) -> str:
    return str(contract["safetyPreamble"]) + "".join(
        f"\n--- MEMORY {memory_id} ---\n{text}\n--- END MEMORY {memory_id} ---"
        for memory_id, text in memories
    )


def validate_framing_source(contract: dict[str, Any]) -> dict[str, bool]:
    product = contract.get("productSource", {})
    path = Path(str(product.get("path", "")))
    if not path.is_file():
        raise EnvironmentFailure("framing product source is unavailable")
    source_sha = sha_file(path)
    if source_sha != product.get("sha256"):
        raise EnvironmentFailure("framing product source identity drifted")
    constant = str(product.get("reviewedConstant", ""))
    source = path.read_text()
    match = re.search(rf"\b{re.escape(constant)}\s*=\s*(.*?);", source, re.DOTALL)
    if match is None:
        raise HarnessFailure("reviewed framing constant was not found")
    literals = re.findall(r'"((?:\\.|[^"\\])*)"', match.group(1))
    if not literals:
        raise HarnessFailure("reviewed framing constant has no string literal")
    resolved = "".join(json.loads('"' + value + '"') for value in literals)
    checks = {
        "sourceExists": True, "sourceSha256Exact": True,
        "reviewedConstantResolved": resolved == contract.get("safetyPreamble"),
    }
    if not all(checks.values()):
        raise HarnessFailure("framing preamble differs from reviewed product constant")
    return checks


@dataclass
class OperationItem:
    case_id: str
    repetition: int
    operation: str
    mode: str
    stub_mode: str
    partition: str
    literal_prompt: str


@dataclass
class OperationScope:
    item: OperationItem
    profile: dict[str, Any] = field(default_factory=dict)
    conversation_roles: dict[str, str] = field(default_factory=dict)
    workspace_roles: set[str] = field(default_factory=set)
    source_event_roles: dict[str, list[EventKey]] = field(default_factory=dict)
    identity_assertions: dict[str, bool] = field(default_factory=dict)
    allowed_source_keys: list[EventKey] = field(default_factory=list)
    forbidden_source_keys: list[EventKey] = field(default_factory=list)


@dataclass
class Observation:
    item: OperationItem
    prompt_hashes: list[str]
    context: str
    persisted_context: str
    request_count: int
    terminal_count: int
    response_outcomes: list[str]
    identity_fields: dict[str, str]
    elapsed_ns: int
    raw_received_ns: list[int]
    case_values: dict[str, Any]
    exact_capture_cardinality: dict[tuple[str, str], int]
    forbidden_capture_cardinality: dict[tuple[str, str], int]
    fault_timing_complete: bool
    terminal_observed: bool
    owned_conversations: dict[str, str] = field(default_factory=dict)
    prompt_boundary_bytes: dict[str, bytes] = field(default_factory=dict)
    context_boundary_bytes: dict[str, bytes] = field(default_factory=dict)
    prompt_boundaries: dict[str, dict[str, Any]] = field(default_factory=dict)
    context_boundaries: dict[str, dict[str, Any]] = field(default_factory=dict)
    selected_memory_provenance: list[dict[str, Any]] = field(default_factory=list)
    stub_requests: list[dict[str, Any]] = field(default_factory=list)
    stub_terminals: list[dict[str, Any]] = field(default_factory=list)
    event_sequence: list[dict[str, Any]] = field(default_factory=list)
    fault_activation_ns: int = 0
    fault_observed_ns: int = 0
    interactions: list[dict[str, Any]] = field(default_factory=list)
    identity_evidence: dict[str, Any] = field(default_factory=dict)
    timing_evidence: dict[str, Any] = field(default_factory=dict)
    evidence_presence: set[str] = field(default_factory=lambda: {
        "E-001", "E-002", "E-003", "E-004", "E-005", "E-006", "E-007", "E-008"
    })
    append_action_record_id: str = ""
    append_action_sequence: int = 0


@dataclass
class RecoveryReceipt:
    service_mode: ServiceMode
    active_work: int
    queued_work: int
    owned_resources_absent: bool
    exact_keys_stable: bool


@dataclass
class CleanupReceipt:
    service_absent: bool
    conversations_absent: bool
    workspaces_absent: bool
    namespaces_absent: bool
    stub_absent: bool
    active_work: int
    queued_work: int
    emergency_cleanup_used: bool = False
    evidence_presence: set[str] = field(default_factory=lambda: {"E-009", "E-010"})
    initial_cleanup_failure_observed: bool = False
    emergency_cleanup_succeeded: bool = False


class BoundaryAdapter(Protocol):
    live: bool

    def ensure_mode(self, mode: ServiceMode, scope: OperationScope) -> None: ...
    def perform(self, scope: OperationScope, action_record: JournalRecord) -> Observation: ...
    def recover(self, scope: OperationScope, exit_mode: ServiceMode) -> RecoveryReceipt: ...
    def cleanup(self) -> CleanupReceipt: ...
    def emergency_cleanup(self) -> CleanupReceipt: ...
    def bind_ledgers(self, primary: EvidenceLedger, emergency: EvidenceLedger) -> None: ...


def exact_capture_stabilized(samples: list[dict[tuple[str, str], int]],
                             allowed: list[EventKey], forbidden: list[EventKey],
                             *, consecutive_reads: int = 2) -> bool:
    """Require bounded consecutive exact-key observations; ignore unrelated backlog."""
    if consecutive_reads < 2 or len(samples) < consecutive_reads:
        return False
    accepted = {key.value() for key in allowed}
    rejected = {key.value() for key in forbidden}
    stable = 0
    for sample in samples:
        exact = all(sample.get(key, 0) == 1 for key in accepted)
        absent = all(sample.get(key, 0) == 0 for key in rejected)
        if exact and absent:
            stable += 1
            if stable >= consecutive_reads:
                return True
        else:
            stable = 0
    return False


@dataclass(frozen=True)
class LiveExecutionFence:
    """File-bound, single-use authority for a future live invocation.

    H0 can exercise this fence with synthetic files, but candidate 10 never
    creates a real execution identity or authorization.  Consumption uses an
    exclusive create before the first adapter action.
    """

    identity_path: Path
    authorization_path: Path
    consumption_path: Path

    def validate(self, *, plan_name: str, operation_count: int,
                 measured_credit: int, package_bindings: dict[str, str]) -> dict[str, Any]:
        if self.consumption_path.exists():
            raise PermissionError("single-use live authorization already consumed")
        if not self.identity_path.is_file() or not self.authorization_path.is_file():
            raise PermissionError("live identity or authorization record missing")
        identity = load_object(self.identity_path)
        authorization = load_object(self.authorization_path)
        identity_sha = sha_file(self.identity_path)
        authorization_sha = sha_file(self.authorization_path)
        expected_mode = "ZERO_CREDIT_DRESS" if plan_name == "dress" else \
            "MEASURED" if plan_name == "measured" else None
        expected_count = EXPECTED_DRESS_OPERATIONS if expected_mode == "ZERO_CREDIT_DRESS" \
            else EXPECTED_REPETITIONS if expected_mode == "MEASURED" else None
        expected_credit = 0 if expected_mode == "ZERO_CREDIT_DRESS" else \
            EXPECTED_REPETITIONS if expected_mode == "MEASURED" else None
        required_identity = {
            "recordType": "SMA_S2_CANDIDATE_10_EXECUTION_IDENTITY",
            "candidate": 10,
            "status": "ACCEPTED_FROZEN_EXECUTION_IDENTITY",
            "liveExecutionAuthorized": False,
        }
        required_authorization = {
            "recordType": "SMA_S2_CANDIDATE_10_LIVE_AUTHORIZATION",
            "candidate": 10,
            "status": "AUTHORIZED_NOT_CONSUMED",
            "executionIdentitySha256": identity_sha,
            "executionMode": expected_mode,
            "plannedOperations": expected_count,
            "measuredCredit": expected_credit,
            "maximumAttempts": 1,
            "automaticReruns": 0,
            "realModelCallsAllowed": False,
        }
        if expected_mode is None or operation_count != expected_count or measured_credit != expected_credit:
            raise PermissionError("live plan/mode/cardinality/credit mismatch")
        if any(identity.get(key) != value for key, value in required_identity.items()):
            raise PermissionError("execution identity is fabricated, stale, or not accepted/frozen")
        if identity.get("packageBindings") != package_bindings:
            raise PermissionError("execution identity package binding mismatch")
        acceptance = identity.get("candidateAcceptance")
        if not isinstance(acceptance, dict):
            raise PermissionError("candidate acceptance binding absent")
        acceptance_path = Path(str(acceptance.get("path", "")))
        if not acceptance_path.is_file() or sha_file(acceptance_path) != acceptance.get("sha256"):
            raise PermissionError("candidate acceptance file binding mismatch")
        acceptance_value = load_object(acceptance_path)
        if acceptance_value.get("recordType") != "SMA_S2_CANDIDATE_10_ACCEPTANCE" \
                or acceptance_value.get("candidate") != 10 \
                or acceptance_value.get("status") != "ACCEPTED_FROZEN":
            raise PermissionError("candidate acceptance lineage invalid")
        if any(authorization.get(key) != value for key, value in required_authorization.items()):
            raise PermissionError("live authorization scope or identity mismatch")
        if authorization.get("authorizationSha256") not in (None, authorization_sha):
            raise PermissionError("self-declared authorization hash mismatch")
        return {"identitySha256": identity_sha, "authorizationSha256": authorization_sha,
                "executionMode": expected_mode, "plannedOperations": expected_count,
                "measuredCredit": expected_credit}

    def consume(self, validated: dict[str, Any]) -> str:
        payload = canonical_bytes({
            "recordType": "SMA_S2_CANDIDATE_10_LIVE_AUTHORIZATION_CONSUMPTION",
            "candidate": 10,
            "status": "CONSUMED_BEFORE_ACTION",
            **validated,
        }) + b"\n"
        try:
            descriptor = os.open(self.consumption_path,
                                 os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        except FileExistsError as error:
            raise PermissionError("single-use live authorization already consumed") from error
        try:
            os.write(descriptor, payload)
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
        return sha_bytes(payload)


def exact_plan(corpus: dict[str, Any]) -> list[OperationItem]:
    plan: list[OperationItem] = []
    for case in sorted(corpus["cases"], key=lambda value: value["id"]):
        for repetition in range(1, int(case["repetitions"]) + 1):
            mode = case.get("mode")
            if case.get("modeSchedule") is not None:
                mode = case["modeSchedule"][repetition - 1]
            text_mode = str(mode)
            plan.append(OperationItem(
                case_id=str(case["id"]), repetition=repetition,
                operation=str(case["operation"]), mode=text_mode,
                stub_mode="SUCCESS" if text_mode.startswith("BRIDGE_") else text_mode,
                partition=str(case["partition"]), literal_prompt=str(case["literalPrompt"]),
            ))
    return plan


def dress_plan(corpus: dict[str, Any]) -> list[OperationItem]:
    selected: list[OperationItem] = []
    for item in exact_plan(corpus):
        if item.case_id == "SMA-S2-001-FIRST-PROMPT-EMPTY":
            include = item.repetition <= 10
        elif item.case_id in {
            "SMA-S2-019-MODEL-STUB-FAULT-MATRIX",
            "SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN",
        }:
            include = True
        else:
            include = item.repetition == 1
        if include:
            selected.append(item)
    if len(selected) != EXPECTED_DRESS_OPERATIONS:
        raise HarnessFailure("dress plan cardinality mismatch")
    return selected


def control_prompt(item: OperationItem) -> str:
    return (f"[SMA-S2-STUB case={item.case_id} repetition={item.repetition} "
            f"mode={item.stub_mode}] {item.literal_prompt}")


def required_oracles(case_id: str, matrix: dict[str, Any]) -> list[str]:
    case = next((value for value in matrix["cases"] if value["caseId"] == case_id), None)
    if case is None:
        raise HarnessFailure(f"case missing from oracle matrix: {case_id}")
    values = list(matrix["commonPerRepetitionEvidenceOracleIds"])
    values.extend(case["predicateOracleIds"])
    for oracle_id, cases in matrix["conditionalEvidenceOracleIds"].items():
        if case_id in cases:
            values.append(oracle_id)
    return values


class Candidate10Runner:
    def __init__(self, adapter: BoundaryAdapter, ledger: EvidenceLedger,
                 live_fence: LiveExecutionFence | None = None,
                 live_output_path: Path | None = None,
                 live_runtime_binding_sha256: str | None = None) -> None:
        if adapter.live and live_fence is None:
            raise PermissionError("live adapter requires a file-bound single-use execution fence")
        self.adapter = adapter
        self.ledger = ledger
        self.live_fence = live_fence
        self.live_output_path = live_output_path
        self.live_runtime_binding_sha256 = live_runtime_binding_sha256
        self.emergency_ledger = EvidenceLedger()
        self.adapter.bind_ledgers(self.ledger, self.emergency_ledger)
        self.corpus = load_object(CORPUS)
        self.matrix = load_object(MATRIX)
        self.framing = load_object(FRAMING)
        manifest = load_object(STATE_MANIFEST)
        self.transitions = {value["operation"]: value for value in manifest["operations"]}
        self.scope_profiles = manifest["scopeProfiles"]
        self.handler_reach: set[str] = set()
        self.transition_reach: set[tuple[str, str, str]] = set()

    def _record_emergency(self, kind: str, data: dict[str, Any]) -> JournalRecord:
        return self.emergency_ledger.append(kind, data)

    def _record_stop(self, stopped: dict[str, Any]) -> None:
        try:
            self.ledger.append("STOP_FAILURE", stopped)
        except Exception as ledger_error:
            self._record_emergency("PRIMARY_LEDGER_FAILURE", {
                "failedKind": "STOP_FAILURE",
                "failureClass": type(ledger_error).__name__,
                "stop": stopped,
            })

    @staticmethod
    def _package_bindings() -> dict[str, str]:
        return {
            "driverSha256": sha_file(Path(__file__)),
            "rawRuntimeSha256": sha_file(RAW_RUNTIME),
            "framingContractSha256": sha_file(FRAMING),
            "stateManifestSha256": sha_file(STATE_MANIFEST),
            "configurationSha256": sha_file(CONFIGURATION),
            "preregistrationSha256": sha_file(PREREGISTRATION),
            "preregistrationAcceptanceSha256": sha_file(PREREGISTRATION_ACCEPTANCE),
            "corpusSha256": sha_file(CORPUS),
            "oracleMatrixSha256": sha_file(MATRIX),
        }

    def _identity_requirement(self, identity: str, scope: OperationScope,
                              observation: Observation, context: ContextReceipt,
                              *, forbidden: bool) -> bool:
        values = observation.case_values
        exact_counts = observation.exact_capture_cardinality
        forbidden_counts = observation.forbidden_capture_cardinality
        selected = set(context.memory_ids)
        if identity.startswith("mem-"):
            present = identity in selected
            return not present if forbidden else present
        if identity in {"ANY", "ANY_DELIVERED_CONTEXT", "ANY_DELIVERED_MEMORY"}:
            return len(selected) == 0
        if identity.startswith("EXACT(") or identity in {
            "EXACT_PRIMARY_EVENT_SET", "EXACT_PARENT_CHILD_EVENT_SET",
            "THREE_EXACT_FIXTURE_KEYS",
        }:
            return bool(exact_counts) and all(count == 1 for count in exact_counts.values())
        if identity == "DUPLICATE_OF_EXACT_KEY" or identity == "DUPLICATE_OF_EXACT_KEYS":
            return all(count == 1 for count in exact_counts.values())
        if identity == "RECURSIVE_CAPTURE":
            return values.get("recursive_capture_count") == 0
        if identity in {"HOOK_EVENT", "ACTION_EVENT", "COMPLETION_LOG_EVENT",
                        "REASONING_EVENT", "TOOL_EVENT", "UTILITY_EVENT"}:
            return all(count == 0 for count in forbidden_counts.values())
        if identity == "UNRELATED_PARTITION":
            return bool(values.get("partition_isolated"))
        if identity == "CROSS_PARTITION_DELIVERY":
            return bool(values.get("all_partitions_isolated"))
        if identity == "MEMORY_TEXT_IN_CURRENT_PROMPT":
            return bool(values.get("provenance_complete"))
        if identity == "CURRENT_PROMPT_SEGMENT_0_MEMORY_TEXT":
            return bool(values.get("context_segment_separate"))
        if identity.startswith("SECRET_IN_"):
            return not values.get("secret_forbidden_surface_hits")
        if identity == "SEALED_RAW_PROMPT_ONLY":
            return values.get("sealed_raw_count") == 1 and bool(values.get("raw_policy_bound"))
        if identity == "PARTIAL_FRAME":
            return context.valid and bool(values.get("truncated_at_frame_boundary"))
        if identity == "CONTEXT_OVER_4096":
            return context.length <= 4096
        if identity == "AT_MOST_THREE_SELECTED_MEMORIES":
            return len(context.memory_ids) <= 3
        if identity == "SUMMARY_EVENT_AS_DELIVERY_STATE":
            return not values.get("summary_used_as_delivery_state")
        if identity == "DUPLICATE_STUB_REQUEST":
            ids = [value.get("requestId") for value in observation.stub_requests]
            return len(ids) == len(set(ids))
        if identity == "UNCHANGED_CONTEXT_PROVENANCE":
            return context.valid and len(set(observation.prompt_hashes)) == 1
        if identity == "RECONSTRUCTIBLE_PROMPT_AND_CONTEXT":
            return set(observation.prompt_boundary_bytes) == {"submission", "persisted", "hook", "model"} \
                and set(observation.context_boundary_bytes) == {"hook", "persisted", "model"}
        if identity == "ORPHANED_PROCESS" or identity == "ORPHANED_FUTURE":
            return bool(values.get("owned_work_zero") and values.get("repetition_cleanup_complete"))
        if identity == "ORPHANED_CONVERSATION":
            return bool(values.get("conversation_absent") and values.get("repetition_cleanup_complete"))
        if identity == "ORPHANED_WORKSPACE":
            return bool(values.get("workspace_absent") and values.get("repetition_cleanup_complete"))
        if identity == "ORPHANED_NAMESPACE":
            return bool(values.get("namespace_absent") and values.get("repetition_cleanup_complete"))
        if identity == "ALPHA_CHANNELS:mem-alpha-timeout":
            return bool(values.get("all_partitions_isolated")) and observation.request_count == 4
        if identity == "BETA_CHANNELS:mem-beta-port":
            return bool(values.get("all_partitions_isolated")) and observation.request_count == 4
        raise HarnessFailure(f"scope identity evaluator missing: {identity}")

    def _enforce_scope(self, scope: OperationScope, observation: Observation) -> None:
        profile = scope.profile
        if set(scope.conversation_roles) != set(profile["ownedConversationRoles"]):
            raise HarnessFailure("owned conversation roles differ from scope profile")
        if observation.owned_conversations != scope.conversation_roles:
            raise HarnessFailure("observation conversation ownership differs from scope")
        if scope.workspace_roles != set(profile["ownedWorkspaceRoles"]):
            raise HarnessFailure("owned workspace roles differ from scope profile")
        if set(scope.source_event_roles) != set(profile["allowedSourceEventRoles"]):
            raise HarnessFailure("allowed source-event roles differ from scope profile")
        flattened = [key for keys in scope.source_event_roles.values() for key in keys]
        if sorted(key.value() for key in flattened) != \
                sorted(key.value() for key in scope.allowed_source_keys):
            raise HarnessFailure("source-event role binding differs from exact allowed keys")
        context = parse_context(observation.context, self.framing)
        evaluated: dict[str, bool] = {}
        for identity in profile["expectedMemoryOrProjectionIdentities"]:
            evaluated[f"EXPECTED:{identity}"] = self._identity_requirement(
                identity, scope, observation, context, forbidden=False)
        for identity in profile["forbiddenMemoryOrProjectionIdentities"]:
            evaluated[f"FORBIDDEN:{identity}"] = self._identity_requirement(
                identity, scope, observation, context, forbidden=True)
        scope.identity_assertions = evaluated
        if not all(evaluated.values()):
            raise ScientificFailure("scope identity assertion failed: "
                                    + ",".join(key for key, value in evaluated.items() if not value))
        if observation.elapsed_ns > int(profile["deadlineMilliseconds"]) * 1_000_000:
            raise ScientificFailure("scope deadline exceeded")
        if not observation.terminal_observed:
            raise ScientificFailure("declared terminal observation absent")
        request_conversations = [value.get("conversationId") for value in observation.stub_requests]
        if observation.item.operation == "FOUR_CHANNEL_CONCURRENCY":
            expected_request_conversations = list(scope.conversation_roles.values())
        elif observation.item.operation == "PARENT_CHILD_PROVENANCE":
            expected_request_conversations = [scope.conversation_roles["child"]]
        elif observation.item.operation == "CONDENSATION_REANCHOR":
            expected_request_conversations = [scope.conversation_roles["primary"]] * 2
        elif observation.item.mode == "TRANSPORT_FAILURE_UNUSED_PORT":
            expected_request_conversations = []
        else:
            expected_request_conversations = [scope.conversation_roles.get(
                "primary", next(iter(scope.conversation_roles.values())))]
        if request_conversations != expected_request_conversations:
            raise ScientificFailure("stub request ownership/multiplicity mismatch")

    def _common_oracles(self, observation: Observation, context: ContextReceipt,
                        evidence_sequence: int) -> dict[str, bool | None]:
        prompt_boundaries = observation.prompt_boundaries
        prompt_raw = observation.prompt_boundary_bytes
        prompt_pairs = [(sha_bytes(prompt_raw[name]), len(prompt_raw[name]))
                        for name in prompt_raw]
        prompt_claims = [(prompt_boundaries[name].get("sha256"),
                          prompt_boundaries[name].get("length")) for name in prompt_raw]
        context_boundaries = observation.context_boundaries
        context_raw = observation.context_boundary_bytes
        context_pairs = [(sha_bytes(context_raw[name]), len(context_raw[name]))
                         for name in context_raw]
        context_claims = [(context_boundaries[name].get("sha256"),
                           context_boundaries[name].get("length")) for name in context_raw]
        request_ids = [value.get("requestId") for value in observation.stub_requests]
        terminal_ids = [value.get("requestId") for value in observation.stub_terminals]
        request_receipts_complete = all(
            value.get("requestId") and value.get("conversationId") and value.get("bodySha256")
            and type(value.get("bodyLength")) is int and value["bodyLength"] > 0
            for value in observation.stub_requests
        )
        terminal_receipts_complete = all(
            value.get("requestId") and value.get("conversationId") and value.get("responseSha256")
            and type(value.get("responseLength")) is int and value["responseLength"] >= 0
            and value.get("responseWriteOutcome") in {"CLIENT_RECEIVED", "CLIENT_DISCONNECTED"}
            for value in observation.stub_terminals
        )
        provenance_complete = len(observation.selected_memory_provenance) == len(context.memory_ids) \
            and all(value.get("memoryId") in context.memory_ids
                    and value.get("traceId") and value.get("partition")
                    for value in observation.selected_memory_provenance)
        if observation.interactions:
            prompt_integrity = True
            context_integrity = True
            for interaction in observation.interactions:
                prompt_values = interaction.get("promptBoundaryBytes", {})
                context_values = interaction.get("contextBoundaryBytes", {})
                transport_pre_model = interaction.get("transportFailureBeforeModel") is True
                expected_prompt_boundaries = {"submission", "persisted", "hook"} \
                    if transport_pre_model else {"submission", "persisted", "hook", "model"}
                expected_context_boundaries = {"hook", "persisted"} \
                    if transport_pre_model else {"hook", "persisted", "model"}
                request_oracles = interaction.get("requestOracles", [])
                prompt_integrity = prompt_integrity and set(prompt_values) == expected_prompt_boundaries \
                    and len({(sha_bytes(value), len(value))
                             for value in prompt_values.values()}) == 1 \
                    and (not request_oracles if transport_pre_model else len(request_oracles) == 1 and all(
                        value.get("segment0") == interaction.get("prompt")
                        and value.get("segment1") == interaction.get("context")
                        and value.get("flattened") is False
                        and value.get("segmentCount") == (2 if interaction.get("context") else 1)
                        and value.get("bodySha256")
                        and type(value.get("bodyLength")) is int and value["bodyLength"] > 0
                        for value in request_oracles))
                raw_context = str(interaction.get("context", ""))
                persisted = str(interaction.get("persistedContext", ""))
                parsed = parse_context(raw_context, self.framing)
                provenance = interaction.get("memoryProvenance", [])
                provenance_ids = [value.get("memory_id") for value in provenance]
                projections = interaction.get("projectionEvidence", {})
                semantic_ids = [value.get("memory_id") for value in projections.get("semantic", [])
                                if value.get("semantic_present")]
                episodic_ids = [value.get("memory_id") for value in projections.get("episodic", [])
                                if value.get("episodic_present")]
                provenance_tuples = {value.get("memory_id"): (value.get("trace_id"),
                                                               value.get("partition"))
                                     for value in provenance}
                semantic_tuples = {value.get("memory_id"): (value.get("trace_id"),
                                                             value.get("partition"))
                                   for value in projections.get("semantic", [])
                                   if value.get("semantic_present")}
                episodic_tuples = {value.get("memory_id"): (value.get("trace_id"),
                                                             value.get("partition"))
                                   for value in projections.get("episodic", [])
                                   if value.get("episodic_present")}
                expected_partition = "actor-beta" \
                    if interaction.get("workspaceRole") == "ACTOR_BETA" else "actor-alpha"
                context_integrity = context_integrity \
                    and set(context_values) == expected_context_boundaries \
                    and len({(sha_bytes(value), len(value))
                             for value in context_values.values()}) == 1 \
                    and raw_context == persisted and parsed.valid \
                    and sorted(provenance_ids) == sorted(parsed.memory_ids) \
                    and sorted(semantic_ids) == sorted(parsed.memory_ids) \
                    and sorted(episodic_ids) == sorted(parsed.memory_ids) \
                    and provenance_tuples == semantic_tuples == episodic_tuples \
                    and all(value == expected_partition
                            for _, value in provenance_tuples.values()) \
                    and all(value.get("trace_id") and value.get("partition")
                            for value in provenance)
        else:
            prompt_integrity = set(prompt_boundaries) == set(prompt_raw) == {
                "submission", "persisted", "hook", "model"} \
                and prompt_pairs == prompt_claims and bool(prompt_pairs) \
                and len(set(prompt_pairs)) == 1
            context_integrity = context.valid and observation.context == observation.persisted_context \
                and set(context_boundaries) == set(context_raw) == {"hook", "persisted", "model"} \
                and context_pairs == context_claims and bool(context_pairs) \
                and len(set(context_pairs)) == 1 \
                and context_pairs[0] == (context.sha256, context.length) and provenance_complete
        expected_raw = observation.request_count
        identity = observation.identity_evidence
        conversation_rows = identity.get("conversations", [])
        event_rows = identity.get("events", [])
        hook_rows = identity.get("hooks", [])
        terminal_rows = identity.get("terminals", [])
        terminal_event_rows = identity.get("terminalEvents", [])
        expected_profile = load_object(CONFIGURATION)["futureLiveIdentity"][
            "openHandsProtocol"]["modelName"]
        event_identities = [(value.get("conversationId"), value.get("eventId"))
                            for value in event_rows]
        sequences_by_conversation: dict[str, list[int]] = {}
        for value in event_rows:
            sequences_by_conversation.setdefault(str(value.get("conversationId")), []).append(
                int(value.get("sequence", 0)))
        sequence_complete = bool(event_rows) \
            and len(event_identities) == len(set(event_identities)) \
            and all(values_ == sorted(values_) and len(values_) == len(set(values_))
                    and all(sequence > 0 for sequence in values_)
                    for values_ in sequences_by_conversation.values())
        conversation_complete = len(conversation_rows) == len(observation.owned_conversations) \
            and {value.get("role"): value.get("conversationId") for value in conversation_rows} \
            == observation.owned_conversations \
            and all(value.get("workspaceRole") and value.get("workspacePath")
                    and value.get("profile") == expected_profile
                    and isinstance(value.get("childIds"), list) for value in conversation_rows)
        if observation.item.operation == "PARENT_CHILD_PROVENANCE":
            by_role = {value.get("role"): value for value in conversation_rows}
            parent_child_complete = set(by_role) == {"parent", "child"} \
                and by_role["child"].get("parentId") == by_role["parent"].get("conversationId") \
                and by_role["child"].get("conversationId") in by_role["parent"].get("childIds", [])
        else:
            parent_child_complete = all(value.get("parentId") is None for value in conversation_rows)
        terminal_identity_complete = len(hook_rows) == len(observation.interactions) \
            and len({(value.get("conversationId"), value.get("eventId")) for value in hook_rows}) \
            == len(hook_rows) \
            and len(terminal_event_rows) >= len(observation.interactions) \
            and all(value.get("conversationId") and value.get("eventId")
                    and type(value.get("sequence")) is int for value in terminal_event_rows) \
            and terminal_rows == [{"requestId": value.get("requestId"),
                                   "conversationId": value.get("conversationId"),
                                   "outcome": value.get("responseWriteOutcome")}
                                  for value in observation.stub_terminals]
        values: dict[str, bool | None] = {
            "E-001": self.ledger.has_record_before(observation.append_action_record_id,
                                                   evidence_sequence),
            "E-002": prompt_integrity,
            "E-003": context_integrity,
            "E-004": observation.request_count == observation.terminal_count == len(request_ids) == len(terminal_ids)
                     and request_ids == terminal_ids and request_receipts_complete
                     and terminal_receipts_complete,
            "E-005": observation.request_count == observation.terminal_count
                     and len(observation.response_outcomes) == observation.terminal_count
                     and observation.response_outcomes
                         == [value.get("responseWriteOutcome") for value in observation.stub_terminals],
            "E-007": all(observation.identity_fields.get(key) for key in (
                "conversation_id", "user_event_id", "hook_event_id", "workspace", "profile",
                "sequence", "event_ordinals")) and observation.terminal_observed
                     and conversation_complete and sequence_complete
                     and parent_child_complete and terminal_identity_complete,
            "E-008": 0 < observation.elapsed_ns <= 120_000_000_000
                     and len(observation.raw_received_ns) == expected_raw
                     and observation.raw_received_ns == sorted(observation.raw_received_ns)
                     and 0 < observation.fault_activation_ns <= observation.fault_observed_ns,
        }
        if observation.item.case_id in self.matrix["conditionalEvidenceOracleIds"]["E-006"]:
            timing = observation.timing_evidence
            if observation.item.operation == "MODEL_STUB_FAULT_MATRIX":
                timing_complete = set(timing) >= {
                    "fault_timing_complete", "fault_activation_ns", "fault_observed_ns",
                    "retry_count", "activated_mode", "future_terminal"} \
                    and timing.get("fault_timing_complete") is True \
                    and timing.get("activated_mode") == observation.item.mode \
                    and timing.get("retry_count") == 0 \
                    and timing.get("future_terminal") is True
            else:
                timing_complete = set(timing) >= {
                    "fault_timing_complete", "fault_activation_ns", "fault_observed_ns",
                    "interrupt_after_raw_ns", "future_completion_ns", "stub_disconnect_ns",
                    "client_disconnected", "owned_shutdown_ns"} \
                    and timing.get("fault_timing_complete") is True \
                    and timing.get("interrupt_after_raw_ns", -1) >= 250_000_000 \
                    and timing.get("future_completion_ns", 10_000_000_001) <= 10_000_000_000 \
                    and timing.get("stub_disconnect_ns", 10_000_000_001) <= 10_000_000_000 \
                    and timing.get("client_disconnected") is True \
                    and timing.get("owned_shutdown_ns", 15_000_000_001) <= 15_000_000_000
            values["E-006"] = timing_complete and observation.fault_timing_complete \
                and 0 < observation.fault_activation_ns <= observation.fault_observed_ns
        return {key: (bool(value) if key in observation.evidence_presence else None)
                for key, value in values.items()}

    def _predicate(self, oracle_id: str, value: bool) -> tuple[str, bool | None]:
        return oracle_id, bool(value)

    def _case_oracles(self, observation: Observation,
                      context: ContextReceipt) -> dict[str, bool | None]:
        value = observation.case_values
        operation = observation.item.operation
        self.handler_reach.add(operation)
        methods = {
            "FIRST_PROMPT_EMPTY": self._case_001,
            "SAME_PARTITION_DELIVERY": self._case_002,
            "CROSS_PARTITION_DENIAL": self._case_003,
            "UNTRUSTED_CONTEXT_PLACEMENT": self._case_004,
            "RAW_INELIGIBLE_ABSENCE": self._case_005,
            "DUPLICATE_PERSISTED_EVENT": self._case_006,
            "RETRIEVAL_OUTAGE": self._case_007,
            "CAPTURE_OUTAGE_RECOVERY": self._case_008,
            "ADDITIONAL_CONTEXT_INTEGRITY": self._case_009,
            "HOOK_FAULT_MATRIX": self._case_010,
            "PARENT_CHILD_PROVENANCE": self._case_011,
            "RESTART_CONTINUITY": self._case_012,
            "FOUR_CHANNEL_CONCURRENCY": self._case_013,
            "FEEDBACK_LOOP_PREVENTION": self._case_014,
            "SECRET_DELIVERY_ABSENCE": self._case_015,
            "EMPTY_RESULT": self._case_016,
            "OVERSIZED_CONTEXT": self._case_017,
            "CONDENSATION_REANCHOR": self._case_018,
            "MODEL_STUB_FAULT_MATRIX": self._case_019,
            "ACTIVE_CANCELLATION_AND_SHUTDOWN": self._case_020,
        }
        if operation not in methods:
            raise HarnessFailure(f"unimplemented operation: {operation}")
        return dict(methods[operation](value, context, observation))

    def _case_001(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-001-P1", v["hook_before_user"] and v["hook_installed"]),
                self._predicate("S2-001-P2", c.length == 0),
                self._predicate("S2-001-P3", len(set(o.prompt_hashes)) == 1),
                self._predicate("S2-001-P4", o.request_count == o.terminal_count == 1)]

    def _case_002(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-002-P1", c.memory_ids.count(v["expected_memory_id"]) == 1),
                self._predicate("S2-002-P2", c.valid and v["untrusted_marker"]),
                self._predicate("S2-002-P3", len(set(o.prompt_hashes)) == 1)]

    def _case_003(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-003-P1", v["surface_set_complete"]
                                and not v["forbidden_surface_hits"])]

    def _case_004(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-004-P1", c.valid and v["untrusted_marker"]),
                self._predicate("S2-004-P2", v["context_segment_separate"]),
                self._predicate("S2-004-P3", len(set(o.prompt_hashes)) == 1)]

    def _case_005(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-005-P1", v["surface_set_complete"]
                                and not v["raw_ineligible_surface_hits"])]

    def _case_006(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        cardinalities = list(o.exact_capture_cardinality.values())
        return [self._predicate("S2-006-P1", v["initial_identity_seen"] and v["reconciled_identity_seen"]),
                self._predicate("S2-006-P2", cardinalities == [1]
                                and v["initial_capture_cardinality"] == 1
                                and v["post_restart_cardinality"] == 1)]

    def _case_007(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-007-P1", v["hook_fail_open"]
                                and v["hook_result_count"] == 1
                                and v["hook_deadline_ms"] <= 1000),
                self._predicate("S2-007-P2", o.request_count == o.terminal_count == 1),
                self._predicate("S2-007-P3", c.length == 0 and v["body_free_fault"])]

    def _case_008(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        cardinalities = list(o.exact_capture_cardinality.values())
        return [self._predicate("S2-008-P1", o.request_count == o.terminal_count == 1),
                self._predicate("S2-008-P2", v["source_persisted"] and v["recovery_ms"] <= 120000),
                self._predicate("S2-008-P3", cardinalities == [1] and v["recursive_capture_count"] == 0)]

    def _case_009(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-009-P1", len(set(o.prompt_hashes)) == 1),
                self._predicate("S2-009-P2", c.valid and c.memory_ids == [v["expected_memory_id"]]),
                self._predicate("S2-009-P3", v["provenance_complete"])]

    def _case_010(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-010-P1", v["fault_exact"] and v["hook_deadline_ms"] <= 1000),
                self._predicate("S2-010-P2", c.length == 0),
                self._predicate("S2-010-P3", v["hook_result_count"] == 1 and v["hook_success"]),
                self._predicate("S2-010-P4", o.request_count == 1)]

    def _case_011(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        owned = o.owned_conversations
        lineage = set(owned) == {"parent", "child"} and owned["parent"] != owned["child"] \
            and {value.get("conversationId") for value in o.stub_requests} == set(owned.values())
        return [self._predicate("S2-011-P1", lineage and v["lineage_complete"]
                                and v["sequence_complete"]
                                and len(o.exact_capture_cardinality) == 2
                                and len(o.event_sequence) >= 3),
                self._predicate("S2-011-P2", v["partition_isolated"])]

    def _case_012(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-012-P1", v["partition_before"] == v["partition_after"]),
                self._predicate("S2-012-P2", v["recall_correct_after"]),
                self._predicate("S2-012-P3", v["initial_capture_count"] == v["final_capture_count"] == 2)]

    def _case_013(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-013-P1", o.request_count == o.terminal_count == 4
                                and len(o.owned_conversations) == 4 and v["all_within_bound"]),
                self._predicate("S2-013-P2", v["all_partitions_isolated"]),
                self._predicate("S2-013-P3", v["telemetry_complete"]
                                and v["active"] == v["queued"] == 0
                                and v["process_active"] == v["process_queued"] == 0
                                and v["owned_child_processes"] == 0
                                and v["queue_depth"] == v["timeout_count"]
                                == v["rejection_count"] == 0
                                and v["resource_snapshot_complete"])]

    def _case_014(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-014-P1", all(count == 0 for count in o.forbidden_capture_cardinality.values())),
                self._predicate("S2-014-P2", o.request_count == o.terminal_count == 1
                                and v["eligible_capture_count"] == 1
                                and v["recursive_capture_count"] == 0)]

    def _case_015(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-015-P1", v["secret_prompt_boundaries"] == 3),
                self._predicate("S2-015-P2", v["surface_set_complete"]
                                and not v["secret_forbidden_surface_hits"] and c.length == 0),
                self._predicate("S2-015-P3", v["model_segment0_count"] == 1
                                and v["sealed_raw_count"] == 1 and v["raw_policy_bound"])]

    def _case_016(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-016-P1", c.length == 0),
                self._predicate("S2-016-P2", o.response_outcomes == ["CLIENT_RECEIVED"])]

    def _case_017(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        links = v.get("fixture_promotion_links", [])
        return [self._predicate("S2-017-P1", c.valid and v["truncated_at_frame_boundary"]
                                and len(links) == 3
                                and len({(value.get("conversationId"), value.get("eventId"),
                                         value.get("memoryId")) for value in links}) == 3),
                self._predicate("S2-017-P2", len(c.memory_ids) <= 3 and c.length <= 4096),
                self._predicate("S2-017-P3", len(c.memory_ids) <= 5 and c.length <= 10000
                                and v["scientific_bounds_observed"])]

    def _case_018(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-018-P1", c.valid and c.length <= 4096 and v["reanchored"]),
                self._predicate("S2-018-P2", v["summary_count"] == 1
                                and v["summary_identity_complete"]
                                and v["summary_identity_absent_from_delivery"]
                                and not v["summary_used_as_delivery_state"]),
                self._predicate("S2-018-P3", v["condense_status"] in (200, 204)
                                and o.request_count == o.terminal_count == 2)]

    def _case_019(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        expected = 0 if o.item.mode == "TRANSPORT_FAILURE_UNUSED_PORT" else 1
        return [self._predicate("S2-019-P1", v["activated_mode"] == o.item.mode and v["independently_observed"]),
                self._predicate("S2-019-P2", o.request_count == expected and v["retry_count"] == 0),
                self._predicate("S2-019-P3", v["future_terminal"]),
                self._predicate("S2-019-P4", len(set(o.prompt_hashes)) == 1 and c.valid),
                self._predicate("S2-019-P5", o.elapsed_ns <= 120_000_000_000 and v["cleanup_evidence_complete"])]

    def _case_020(self, v: dict[str, Any], c: ContextReceipt, o: Observation):
        return [self._predicate("S2-020-P1", v["interrupt_after_raw_ns"] >= 250_000_000),
                self._predicate("S2-020-P2", v["future_completion_ns"] <= 10_000_000_000),
                self._predicate("S2-020-P3", v["stub_disconnect_ns"] <= 10_000_000_000 and v["client_disconnected"]),
                self._predicate("S2-020-P4", v["owned_shutdown_ns"] <= 15_000_000_000 and v["owned_work_zero"]),
                self._predicate("S2-020-P5", v["repetition_cleanup_complete"]),
                self._predicate("S2-020-P6", len(set(o.prompt_hashes)) == 1 and c.valid)]

    def _grade(self, observation: Observation) -> dict[str, bool | None]:
        context = parse_context(observation.context, self.framing)
        evidence_record = self.ledger.append("PREASSERTION_EVIDENCE", {
            "caseId": observation.item.case_id,
            "repetition": observation.item.repetition,
            "contextSha256": context.sha256,
            "contextLength": context.length,
            "selectedMemoryIds": context.memory_ids,
            "exactCaptureKeys": [list(key) for key in sorted(observation.exact_capture_cardinality)],
            "forbiddenCaptureKeys": [list(key) for key in sorted(observation.forbidden_capture_cardinality)],
        })
        values = self._common_oracles(observation, context, evidence_record.sequence)
        values.update(self._case_oracles(observation, context))
        required = required_oracles(observation.item.case_id, self.matrix)
        missing = sorted(oracle for oracle in required if oracle not in values or values[oracle] is None)
        false = sorted(oracle for oracle in required if values.get(oracle) is False)
        unexpected = sorted(set(values) - set(required))
        self.ledger.append("PREASSERTION_ORACLE_BUNDLE", {
            "caseId": observation.item.case_id,
            "repetition": observation.item.repetition,
            "required": required,
            "values": values,
            "missing": missing,
            "false": false,
            "unexpected": unexpected,
        })
        if missing or unexpected:
            raise HarnessFailure(f"oracle bundle incomplete or unexpected: missing={missing} unexpected={unexpected}")
        if false:
            raise ScientificFailure("scientific oracle failure: " + ",".join(false))
        return values

    def run(self, plan: list[OperationItem], plan_name: str, measured_credit: int) -> dict[str, Any]:
        results: list[dict[str, Any]] = []
        stopped: dict[str, Any] | None = None
        cleanup: CleanupReceipt | None = None
        cleanup_action: JournalRecord | None = None
        if self.adapter.live:
            if self.live_fence is None:
                raise PermissionError("live execution fence absent")
            authority_preaction = self.ledger.append("PRE_ACTION_AUTHORIZATION_CONSUMPTION", {
                "planName": plan_name, "plannedOperations": len(plan),
                "measuredCredit": measured_credit,
            })
            bindings = self._package_bindings()
            if self.live_runtime_binding_sha256 is None or self.live_output_path is None:
                raise PermissionError("live output and raw-port binding identities are required")
            bindings["rawPortBindingSha256"] = self.live_runtime_binding_sha256
            output_preaction = self.ledger.append("PRE_ACTION_OUTPUT_CREATE", {
                "outputPath": str(self.live_output_path),
            })
            validated = self.live_fence.validate(
                plan_name=plan_name, operation_count=len(plan), measured_credit=measured_credit,
                package_bindings=bindings)
            consumption_sha = self.live_fence.consume(validated)
            self.ledger.append("AUTHORIZATION_CONSUMED", {
                **validated, "consumptionSha256": consumption_sha,
                "preActionRecordId": authority_preaction.record_id,
            })
            try:
                os.mkdir(self.live_output_path, 0o700)
            except FileExistsError as error:
                raise PermissionError("live output path already exists") from error
            self.ledger.activate_durable(self.live_output_path / "raw-evidence.jsonl")
            self.ledger.append("OUTPUT_CREATED", {
                "outputPath": str(self.live_output_path),
                "preActionRecordId": output_preaction.record_id,
            })
        try:
            for item in plan:
                transition = self.transitions.get(item.operation)
                if transition is None:
                    raise HarnessFailure(f"state transition missing: {item.operation}")
                entry = ServiceMode(transition["entry"])
                exit_mode = ServiceMode(transition["exit"])
                scope = OperationScope(item=item, profile=self.scope_profiles[item.operation])
                try:
                    transition_record = self.ledger.append("PRE_ACTION_SERVICE_TRANSITION", {
                        "caseId": item.case_id, "repetition": item.repetition,
                        "requiredEntry": entry.value,
                    })
                    self.adapter.ensure_mode(entry, scope)
                    action_record = self.ledger.append("PRE_ACTION_OPERATION", {
                        "caseId": item.case_id, "repetition": item.repetition,
                        "operation": item.operation, "entryReceiptId": transition_record.record_id,
                    })
                    observation = self.adapter.perform(scope, action_record)
                    if not observation.append_action_record_id:
                        observation.append_action_record_id = action_record.record_id
                        observation.append_action_sequence = action_record.sequence
                    self._enforce_scope(scope, observation)
                    self._grade(observation)
                    status = "PASS"
                    failure_class = None
                except ScientificFailure as error:
                    status = "FAIL"
                    failure_class = "SCIENTIFIC"
                    self.ledger.append("SCIENTIFIC_FAILURE", {
                        "caseId": item.case_id, "repetition": item.repetition,
                        "messageSha256": sha_bytes(str(error).encode()),
                    })
                except (HarnessFailure, EnvironmentFailure, SafetyFailure) as error:
                    stopped = {"class": type(error).__name__.replace("Failure", "").upper(),
                               "caseId": item.case_id, "repetition": item.repetition,
                               "messageSha256": sha_bytes(str(error).encode())}
                    self._record_stop(stopped)
                    break
                try:
                    recovery_action = self.ledger.append("PRE_ACTION_RECOVERY", {
                        "caseId": item.case_id, "repetition": item.repetition,
                        "requiredExit": exit_mode.value,
                    })
                    recovery = self.adapter.recover(scope, exit_mode)
                    recovery_ok = recovery.service_mode == exit_mode \
                        and recovery.active_work == recovery.queued_work == 0 \
                        and recovery.owned_resources_absent and recovery.exact_keys_stable
                    recovery_record = self.ledger.append("OPERATION_RECOVERY", {
                        "caseId": item.case_id, "repetition": item.repetition,
                        "serviceMode": recovery.service_mode.value,
                        "activeWork": recovery.active_work, "queuedWork": recovery.queued_work,
                        "ownedResourcesAbsent": recovery.owned_resources_absent,
                        "exactKeysStable": recovery.exact_keys_stable,
                        "preActionRecordId": recovery_action.record_id,
                        "passed": recovery_ok,
                    })
                    if not recovery_ok:
                        raise HarnessFailure("operation recovery barrier failed")
                    self.transition_reach.add((item.operation, entry.value, exit_mode.value))
                    results.append({"caseId": item.case_id, "repetition": item.repetition,
                                    "operation": item.operation, "status": status,
                                    "failureClass": failure_class,
                                    "recoveryReceiptId": recovery_record.record_id})
                except (HarnessFailure, EnvironmentFailure, SafetyFailure) as error:
                    stopped = {"class": type(error).__name__.replace("Failure", "").upper(),
                               "caseId": item.case_id, "repetition": item.repetition,
                               "phase": "RECOVERY",
                               "messageSha256": sha_bytes(str(error).encode())}
                    self._record_stop(stopped)
                    break
        except Exception as error:
            if stopped is None:
                stopped = {"class": type(error).__name__.replace("Failure", "").upper(),
                           "phase": "RUNNER", "messageSha256": sha_bytes(str(error).encode())}
                self._record_stop(stopped)
        finally:
            try:
                cleanup_action = self.ledger.append("PRE_ACTION_GLOBAL_CLEANUP", {
                    "plannedOperations": len(plan), "completedOperations": len(results),
                })
            except Exception as ledger_error:
                cleanup_action = self._record_emergency("PRE_ACTION_GLOBAL_CLEANUP", {
                    "plannedOperations": len(plan), "completedOperations": len(results),
                    "primaryLedgerFailureClass": type(ledger_error).__name__,
                })
                if stopped is None:
                    stopped = {"class": "HARNESS", "phase": "GLOBAL_CLEANUP_PREACTION",
                               "messageSha256": sha_bytes(str(ledger_error).encode())}
            try:
                cleanup = self.adapter.cleanup()
            except Exception as cleanup_error:
                if stopped is None:
                    stopped = {"class": "SAFETY", "phase": "GLOBAL_CLEANUP",
                               "messageSha256": sha_bytes(str(cleanup_error).encode())}
                self._record_emergency("PRIMARY_CLEANUP_FAILURE", stopped)
                try:
                    cleanup = self.adapter.emergency_cleanup()
                except Exception as emergency_error:
                    self._record_emergency("EMERGENCY_CLEANUP_FAILURE", {
                        "failureClass": type(emergency_error).__name__,
                        "messageSha256": sha_bytes(str(emergency_error).encode()),
                    })
                    cleanup = CleanupReceipt(False, False, False, False, False, 1, 1, True,
                                             initial_cleanup_failure_observed=True,
                                             emergency_cleanup_succeeded=False)
        if cleanup is None or cleanup_action is None:
            raise HarnessFailure("cleanup finalization invariant failed")
        cleanup_values: dict[str, bool | None] = {
            "E-009": cleanup.service_absent and cleanup.conversations_absent
                     and cleanup.workspaces_absent and cleanup.namespaces_absent
                     and cleanup.stub_absent,
            "E-010": cleanup.active_work == cleanup.queued_work == 0
                     and cleanup.service_absent and cleanup.namespaces_absent,
        }
        cleanup_values = {key: (bool(value) if key in cleanup.evidence_presence else None)
                          for key, value in cleanup_values.items()}
        cleanup_data = {
            "values": cleanup_values,
            "emergencyCleanupUsed": cleanup.emergency_cleanup_used,
            "preActionRecordId": cleanup_action.record_id,
            "initialCleanupFailureObserved": cleanup.initial_cleanup_failure_observed,
            "emergencyCleanupSucceeded": cleanup.emergency_cleanup_succeeded,
        }
        try:
            cleanup_record = self.ledger.append("GLOBAL_CLEANUP", cleanup_data)
        except Exception as ledger_error:
            cleanup_record = self._record_emergency("GLOBAL_CLEANUP", {
                **cleanup_data, "primaryLedgerFailureClass": type(ledger_error).__name__,
            })
            if stopped is None:
                stopped = {"class": "HARNESS", "phase": "GLOBAL_CLEANUP_RECEIPT",
                           "messageSha256": sha_bytes(str(ledger_error).encode())}
        cleanup_pass = all(cleanup_values.get(key) is True for key in ("E-009", "E-010"))
        if cleanup.initial_cleanup_failure_observed and stopped is None:
            stopped = {"class": "SAFETY", "phase": "GLOBAL_CLEANUP",
                       "messageSha256": sha_bytes(b"initial cleanup failure required emergency cleanup")}
        if not cleanup_pass and stopped is None:
            stopped = {"class": "SAFETY", "phase": "GLOBAL_CLEANUP",
                       "messageSha256": sha_bytes(b"global cleanup evidence failed")}
        all_pass = len(results) == len(plan) and all(result["status"] == "PASS" for result in results)
        status = "PASS" if all_pass and cleanup_pass and stopped is None else \
            "FAIL" if stopped is None else "INCONCLUSIVE"
        return {
            "planName": plan_name,
            "status": status,
            "plannedOperations": len(plan),
            "completedOperations": len(results),
            "measuredCredit": measured_credit if status == "PASS" else 0,
            "results": results,
            "stopped": stopped,
            "cleanup": {"status": "PASS" if cleanup_pass else "FAIL",
                        "recordId": cleanup_record.record_id,
                        "values": cleanup_values,
                        "emergencyCleanupUsed": cleanup.emergency_cleanup_used,
                        "initialCleanupFailureObserved": cleanup.initial_cleanup_failure_observed,
                        "emergencyCleanupSucceeded": cleanup.emergency_cleanup_succeeded},
            "handlerReach": sorted(self.handler_reach),
            "transitionReach": [list(value) for value in sorted(self.transition_reach)],
            "emergencyLedger": self.emergency_ledger.canonical_records(),
        }


class ObservationMutationApplier:
    """Test-only corruption of raw-derived observations for rejecting controls."""

    def __init__(self, framing: dict[str, Any], *,
                 predicate_mutation: str | None = None,
                 evidence_mutation: str | None = None,
                 missing_evidence: str | None = None,
                 scope_identity_mutation: str | None = None,
                 scope_shape_mutation: str | None = None) -> None:
        self.framing = framing
        self.predicate_mutation = predicate_mutation
        self.evidence_mutation = evidence_mutation
        self.missing_evidence = missing_evidence
        self.scope_identity_mutation = scope_identity_mutation
        self.scope_shape_mutation = scope_shape_mutation

    def _replace_context(self, observation: Observation,
                         memories: list[tuple[str, str]]) -> None:
        context = frame_context(self.framing, memories)
        observation.context = context
        observation.persisted_context = context
        digest = sha_bytes(context.encode())
        observation.context_boundaries = {
            name: {"sha256": digest, "length": len(context.encode())}
            for name in ("hook", "persisted", "model")
        }
        observation.context_boundary_bytes = {
            name: context.encode() for name in ("hook", "persisted", "model")
        }
        observation.selected_memory_provenance = [
            {"memoryId": memory_id, "traceId": "mutation-trace",
             "partition": observation.item.partition}
            for memory_id, _ in memories
        ]

    @staticmethod
    def _corrupt_prompt_boundary(observation: Observation) -> None:
        observation.prompt_boundaries["model"]["sha256"] = sha_bytes(b"mutated-prompt")
        observation.prompt_hashes[-1] = sha_bytes(b"mutated-prompt")

    @staticmethod
    def _corrupt_request_terminal(observation: Observation) -> None:
        if observation.stub_terminals:
            observation.stub_terminals[0]["requestId"] = "mutated-request-id"
        else:
            observation.terminal_count = 1

    def _apply_predicate_mutation(self, observation: Observation) -> None:
        target = self.predicate_mutation
        if target is None:
            return
        if f"SMA-S2-{target[3:6]}-" not in observation.item.case_id:
            return
        v = observation.case_values
        handled = True
        if target == "S2-001-P1": v["hook_before_user"] = False
        elif target == "S2-001-P2": self._replace_context(observation, [("unexpected", "memory")])
        elif target == "S2-001-P3": self._corrupt_prompt_boundary(observation)
        elif target == "S2-001-P4": observation.terminal_count = 0
        elif target == "S2-002-P1": v["expected_memory_id"] = "absent-memory"
        elif target == "S2-002-P2": v["untrusted_marker"] = False
        elif target == "S2-002-P3": self._corrupt_prompt_boundary(observation)
        elif target == "S2-003-P1": v["forbidden_surface_hits"] = ["mem-alpha-timeout"]
        elif target == "S2-004-P1": v["untrusted_marker"] = False
        elif target == "S2-004-P2": v["context_segment_separate"] = False
        elif target == "S2-004-P3": self._corrupt_prompt_boundary(observation)
        elif target == "S2-005-P1": v["raw_ineligible_surface_hits"] = ["mem-alpha-raw"]
        elif target == "S2-006-P1": v["initial_identity_seen"] = False
        elif target == "S2-006-P2": v["post_restart_cardinality"] = 2
        elif target == "S2-007-P1": v["hook_fail_open"] = False
        elif target == "S2-007-P2": observation.terminal_count = 0
        elif target == "S2-007-P3": v["body_free_fault"] = False
        elif target == "S2-008-P1": observation.terminal_count = 0
        elif target == "S2-008-P2": v["source_persisted"] = False
        elif target == "S2-008-P3": v["recursive_capture_count"] = 1
        elif target == "S2-009-P1": self._corrupt_prompt_boundary(observation)
        elif target == "S2-009-P2": v["expected_memory_id"] = "absent-memory"
        elif target == "S2-009-P3": v["provenance_complete"] = False
        elif target == "S2-010-P1": v["fault_exact"] = False
        elif target == "S2-010-P2": self._replace_context(observation, [("unexpected", "memory")])
        elif target == "S2-010-P3": v["hook_result_count"] = 2
        elif target == "S2-010-P4": observation.request_count = 0
        elif target == "S2-011-P1": observation.owned_conversations["child"] = observation.owned_conversations["parent"]
        elif target == "S2-011-P2": v["partition_isolated"] = False
        elif target == "S2-012-P1": v["partition_after"] = "different-partition"
        elif target == "S2-012-P2": v["recall_correct_after"] = False
        elif target == "S2-012-P3": v["final_capture_count"] = 3
        elif target == "S2-013-P1": observation.terminal_count = 3
        elif target == "S2-013-P2": v["all_partitions_isolated"] = False
        elif target == "S2-013-P3": v["telemetry_complete"] = False
        elif target == "S2-014-P1":
            key = next(iter(observation.forbidden_capture_cardinality))
            observation.forbidden_capture_cardinality[key] = 1
        elif target == "S2-014-P2": v["recursive_capture_count"] = 1
        elif target == "S2-015-P1": v["secret_prompt_boundaries"] = 2
        elif target == "S2-015-P2": v["secret_forbidden_surface_hits"] = ["operational-log"]
        elif target == "S2-015-P3": v["raw_policy_bound"] = False
        elif target == "S2-016-P1": self._replace_context(observation, [("unexpected", "memory")])
        elif target == "S2-016-P2": observation.response_outcomes = ["CLIENT_DISCONNECTED"]
        elif target == "S2-017-P1": v["truncated_at_frame_boundary"] = False
        elif target == "S2-017-P2": self._replace_context(observation, [(f"m-{index}", "x") for index in range(4)])
        elif target == "S2-017-P3": v["scientific_bounds_observed"] = False
        elif target == "S2-018-P1": v["reanchored"] = False
        elif target == "S2-018-P2": v["summary_used_as_delivery_state"] = True
        elif target == "S2-018-P3": v["condense_status"] = 500
        elif target == "S2-019-P1": v["independently_observed"] = False
        elif target == "S2-019-P2": v["retry_count"] = 1
        elif target == "S2-019-P3": v["future_terminal"] = False
        elif target == "S2-019-P4": self._corrupt_prompt_boundary(observation)
        elif target == "S2-019-P5": v["cleanup_evidence_complete"] = False
        elif target == "S2-020-P1": v["interrupt_after_raw_ns"] = 249_999_999
        elif target == "S2-020-P2": v["future_completion_ns"] = 10_000_000_001
        elif target == "S2-020-P3": v["client_disconnected"] = False
        elif target == "S2-020-P4": v["owned_shutdown_ns"] = 15_000_000_001
        elif target == "S2-020-P5": v["repetition_cleanup_complete"] = False
        elif target == "S2-020-P6": self._corrupt_prompt_boundary(observation)
        else: handled = False
        if not handled:
            raise HarnessFailure(f"predicate mutation has no underlying-evidence binding: {target}")

    def _apply_evidence_mutation(self, observation: Observation) -> None:
        if self.missing_evidence is not None:
            observation.evidence_presence.discard(self.missing_evidence)
        target = self.evidence_mutation
        if target is None:
            return
        if target == "E-001":
            observation.append_action_record_id = "missing-preaction-record"
        elif target == "E-002":
            self._corrupt_prompt_boundary(observation)
        elif target == "E-003":
            observation.context_boundaries["model"]["sha256"] = sha_bytes(b"mutated-context")
        elif target == "E-004":
            self._corrupt_request_terminal(observation)
        elif target == "E-005":
            observation.response_outcomes = ["CLIENT_DISCONNECTED"] if observation.terminal_count else ["CLIENT_RECEIVED"]
        elif target == "E-006":
            observation.fault_observed_ns = 0
        elif target == "E-007":
            observation.identity_fields["sequence"] = ""
        elif target == "E-008":
            observation.fault_observed_ns = observation.fault_activation_ns - 1
        elif target == "E-002-RAW":
            observation.prompt_boundary_bytes["model"] = b"mutated-raw-prompt"
        elif target == "E-003-PERSISTED-RAW":
            observation.persisted_context = observation.persisted_context + "mutated"
        elif target not in {"E-009", "E-010"}:
            raise HarnessFailure(f"evidence mutation has no underlying-evidence binding: {target}")

    def _apply_scope_identity_mutation(self, observation: Observation) -> None:
        target = self.scope_identity_mutation
        if target is None:
            return
        _, identity = target.split(":", 1)
        v = observation.case_values
        if identity.startswith("mem-"):
            receipt = parse_context(observation.context, self.framing)
            memories = [(memory_id, "mutated") for memory_id in receipt.memory_ids
                        if memory_id != identity]
            if target.startswith("FORBIDDEN:"):
                memories.append((identity, "forbidden"))
            self._replace_context(observation, memories)
        elif identity in {"ANY", "ANY_DELIVERED_CONTEXT", "ANY_DELIVERED_MEMORY"}:
            self._replace_context(observation, [("forbidden-memory", "present")])
        elif identity.startswith("EXACT(") or identity in {
            "EXACT_PRIMARY_EVENT_SET", "EXACT_PARENT_CHILD_EVENT_SET",
            "THREE_EXACT_FIXTURE_KEYS", "DUPLICATE_OF_EXACT_KEY", "DUPLICATE_OF_EXACT_KEYS",
        }:
            key = next(iter(observation.exact_capture_cardinality))
            observation.exact_capture_cardinality[key] = 2
        elif identity == "RECURSIVE_CAPTURE": v["recursive_capture_count"] = 1
        elif identity in {"HOOK_EVENT", "ACTION_EVENT", "COMPLETION_LOG_EVENT",
                          "REASONING_EVENT", "TOOL_EVENT", "UTILITY_EVENT"}:
            key = next(iter(observation.forbidden_capture_cardinality))
            observation.forbidden_capture_cardinality[key] = 1
        elif identity == "UNRELATED_PARTITION": v["partition_isolated"] = False
        elif identity == "CROSS_PARTITION_DELIVERY": v["all_partitions_isolated"] = False
        elif identity == "MEMORY_TEXT_IN_CURRENT_PROMPT": v["provenance_complete"] = False
        elif identity == "CURRENT_PROMPT_SEGMENT_0_MEMORY_TEXT": v["context_segment_separate"] = False
        elif identity.startswith("SECRET_IN_"): v["secret_forbidden_surface_hits"] = [identity]
        elif identity == "SEALED_RAW_PROMPT_ONLY": v["sealed_raw_count"] = 0
        elif identity == "PARTIAL_FRAME":
            observation.context += "\n--- MEMORY partial ---\n"
            observation.persisted_context = observation.context
        elif identity == "CONTEXT_OVER_4096":
            self._replace_context(observation, [("oversized", "x" * 5000)])
        elif identity == "AT_MOST_THREE_SELECTED_MEMORIES":
            self._replace_context(observation, [(f"memory-{index}", "x") for index in range(4)])
        elif identity == "SUMMARY_EVENT_AS_DELIVERY_STATE": v["summary_used_as_delivery_state"] = True
        elif identity == "DUPLICATE_STUB_REQUEST":
            if observation.stub_requests:
                observation.stub_requests.append(dict(observation.stub_requests[0]))
                observation.request_count += 1
        elif identity == "UNCHANGED_CONTEXT_PROVENANCE": self._corrupt_prompt_boundary(observation)
        elif identity == "RECONSTRUCTIBLE_PROMPT_AND_CONTEXT":
            observation.context_boundary_bytes.pop("model", None)
        elif identity in {"ORPHANED_PROCESS", "ORPHANED_FUTURE"}:
            v["owned_work_zero"] = False
        elif identity == "ORPHANED_CONVERSATION": v["conversation_absent"] = False
        elif identity == "ORPHANED_WORKSPACE": v["workspace_absent"] = False
        elif identity == "ORPHANED_NAMESPACE": v["namespace_absent"] = False
        elif identity in {"ALPHA_CHANNELS:mem-alpha-timeout", "BETA_CHANNELS:mem-beta-port"}:
            v["all_partitions_isolated"] = False
        else:
            raise HarnessFailure(f"scope mutation missing for identity: {identity}")

    def _apply_scope_shape_mutation(self, scope: OperationScope,
                                    observation: Observation) -> None:
        target = self.scope_shape_mutation
        if target is None:
            return
        if target == "ownedConversationRoles":
            scope.conversation_roles.pop(next(iter(scope.conversation_roles)))
        elif target == "ownedWorkspaceRoles":
            scope.workspace_roles.clear()
        elif target == "allowedSourceEventRoles":
            if scope.source_event_roles:
                scope.source_event_roles.pop(next(iter(scope.source_event_roles)))
            else:
                scope.source_event_roles["undeclared"] = [EventKey("x", "y")]
        elif target == "deadlineMilliseconds":
            observation.elapsed_ns = int(scope.profile["deadlineMilliseconds"]) * 1_000_000 + 1
        elif target == "terminalObservation":
            observation.terminal_observed = False
        else:
            raise HarnessFailure(f"scope shape mutation missing: {target}")


def validate_bindings() -> dict[str, Any]:
    checks = {str(path): path.is_file() and sha_file(path) == digest
              for path, digest in BOUND_SHA256.items()}
    if not all(checks.values()):
        raise EnvironmentFailure(f"accepted input binding mismatch: {checks}")
    preregistration = load_object(PREREGISTRATION)
    corpus = load_object(CORPUS)
    matrix = load_object(MATRIX)
    plan = exact_plan(corpus)
    predicate_count = sum(len(case["predicateOracleIds"]) for case in matrix["cases"])
    evidence_count = len(matrix["requiredEvidenceIndex"])
    operations = {item.operation for item in plan}
    state_manifest = load_object(STATE_MANIFEST)
    transition_operations = {item["operation"] for item in state_manifest["operations"]}
    scope_profiles = state_manifest.get("scopeProfiles", {})
    scope_fields = {"ownedConversationRoles", "ownedWorkspaceRoles", "allowedSourceEventRoles",
                    "expectedMemoryOrProjectionIdentities", "forbiddenMemoryOrProjectionIdentities",
                    "deadlineMilliseconds", "terminalObservation"}
    scope_profiles_complete = set(scope_profiles) == operations and all(
        set(scope_profiles[operation]) == scope_fields
        and isinstance(scope_profiles[operation]["deadlineMilliseconds"], int)
        and scope_profiles[operation]["deadlineMilliseconds"] > 0
        for operation in operations
    )
    operation_scope_references_exact = all(
        item.get("scopeProfile") == item.get("operation")
        and item.get("scopeProfile") in scope_profiles
        for item in state_manifest["operations"]
    )
    framing = load_object(FRAMING)
    future_live_checks = validate_future_live_contract(load_object(CONFIGURATION), require_files=True)
    preamble = framing["safetyPreamble"].encode()
    scientific_predicates = sum(len(case["predicates"]) for case in preregistration["cases"])
    validations = {
        "acceptedBindings": checks,
        "caseCount": len(corpus["cases"]),
        "repetitionCount": len(plan),
        "dressOperationCount": len(dress_plan(corpus)),
        "matrixPredicateCount": predicate_count,
        "preregistrationPredicateCount": scientific_predicates,
        "requiredEvidenceCount": evidence_count,
        "operationTransitionSetExact": operations == transition_operations,
        "scopeProfilesComplete": scope_profiles_complete,
        "operationScopeReferencesExact": operation_scope_references_exact,
        "preambleLengthExact": len(preamble) == framing["safetyPreambleUtf8Length"],
        "preambleShaExact": sha_bytes(preamble) == framing["safetyPreambleSha256"],
        "executionDefault": "DENY",
        "futureLiveContract": future_live_checks,
    }
    required = [validations["caseCount"] == EXPECTED_CASES,
                validations["repetitionCount"] == EXPECTED_REPETITIONS,
                validations["dressOperationCount"] == EXPECTED_DRESS_OPERATIONS,
                validations["matrixPredicateCount"] == EXPECTED_PREDICATES,
                validations["preregistrationPredicateCount"] == EXPECTED_PREDICATES,
                validations["requiredEvidenceCount"] == EXPECTED_EVIDENCE,
                validations["operationTransitionSetExact"], validations["scopeProfilesComplete"],
                validations["operationScopeReferencesExact"], validations["preambleLengthExact"],
                validations["preambleShaExact"]]
    if not all(required):
        raise HarnessFailure(f"candidate-10 binding validation failed: {validations}")
    return validations


def validate_future_live_contract(value: dict[str, Any], *, require_files: bool = False) -> dict[str, bool]:
    identity = value.get("futureLiveIdentity", {})
    ports = identity.get("runtimePorts", {})
    isolation = identity.get("workspaceIsolation", {})
    maven = identity.get("mavenPromotion", {})
    namespaces = identity.get("disposableNamespaces", {})
    root = Path(str(isolation.get("root", "")))
    empty = Path(str(isolation.get("emptyWorkspace", "")))
    allowlist = [Path(str(item)) for item in isolation.get("captureAllowlist", [])]
    project = Path(str(maven.get("projectDirectory", "")))
    pom = Path(str(maven.get("pomPath", "")))
    checks = {
        "stubHostLoopback": ports.get("stubHost") == "127.0.0.1",
        "bridgeHostLoopback": ports.get("bridgeHost") == "127.0.0.1",
        "stubPortNumeric": type(ports.get("stubPort")) is int and 1024 <= ports["stubPort"] <= 65535,
        "bridgePortExact": ports.get("bridgePort") == 8130,
        "portsDistinct": ports.get("stubPort") != ports.get("bridgePort"),
        "emptyUnderRoot": empty.parent == root,
        "allowlistCardinality": len(allowlist) == 2,
        "emptyExcluded": empty not in allowlist,
        "allowlistUnderRoot": all(path.parent == root for path in allowlist),
        "projectAbsolute": project.is_absolute(),
        "pomUnderProject": pom.parent == project,
        "pomIdentity": (not require_files) or (pom.is_file() and sha_file(pom) == maven.get("pomSha256")),
        "explicitMavenCwd": maven.get("explicitSubprocessWorkingDirectoryRequired") is True,
        "rawFailureOutputProhibited": maven.get("rawFailureOutputAllowed") is False,
        "seedConstructorBound": identity.get("seedConstructor") == "CANDIDATE_10_BOUND_DEPENDENCY_INJECTED",
        "stableTerminalFetches": identity.get("stableTerminalFetchCount") == 2,
        "mongoNamespaceExact": namespaces.get("mongodbDatabase")
                               == "sma_s2_closure_candidate_10_measured_20260814",
        "semanticNamespaceExact": namespaces.get("qdrantSemanticCollection")
                                  == "sma_s2_closure_candidate_10_semantic_20260814",
        "episodicNamespaceExact": namespaces.get("qdrantEpisodicCollection")
                                  == "sma_s2_closure_candidate_10_episodic_20260814",
        "conversationPrefixExact": namespaces.get("conversationPrefix")
                                   == "sma-s2-closure-candidate-10-",
    }
    if not all(checks.values()):
        raise HarnessFailure(f"future live contract rejected: {checks}")
    return checks


def canonical_offline_outcome(plan_name: str) -> dict[str, Any]:
    import sma_s2_closure_candidate_10_runtime as runtime
    validate_bindings()
    framing = load_object(FRAMING)
    corpus = load_object(CORPUS)
    plan = dress_plan(corpus) if plan_name == "dress" else exact_plan(corpus)
    ledger = EvidenceLedger()
    runner = Candidate10Runner(
        runtime.RawCandidate10OrchestrationAdapter(
            framing, runtime.OfflineRawPorts(framing)), ledger)
    result = runner.run(plan, plan_name, 0)
    return {
        "planName": plan_name,
        "status": result["status"],
        "plannedOperations": result["plannedOperations"],
        "completedOperations": result["completedOperations"],
        "handlerReach": result["handlerReach"],
        "transitionReach": result["transitionReach"],
        "cleanupStatus": result["cleanup"]["status"],
    }


def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser()
    value.add_argument("--offline-contract", action="store_true")
    value.add_argument("--offline-canonical", choices=("dress", "measured"))
    value.add_argument("--dress-rehearsal", action="store_true")
    value.add_argument("--execute", action="store_true")
    value.add_argument("--execution-identity", type=Path)
    value.add_argument("--authorization", type=Path)
    value.add_argument("--consumption", type=Path)
    value.add_argument("--raw-port-binding", type=Path)
    value.add_argument("--output", type=Path)
    return value


def live_execution(args: argparse.Namespace) -> int:
    import sma_s2_closure_candidate_10_runtime as runtime
    required = (args.execution_identity, args.authorization, args.consumption,
                args.raw_port_binding, args.output)
    if any(value is None for value in required):
        raise PermissionError("live mode requires identity, authorization, consumption, raw-port binding, and output")
    binding = load_object(args.raw_port_binding)
    if binding.get("recordType") != "SMA_S2_CANDIDATE_10_RAW_PORT_BINDING" \
            or binding.get("candidate") != 10 \
            or binding.get("status") != "ACCEPTED_FROZEN_RUNTIME_BINDING":
        raise PermissionError("raw-port binding is not accepted/frozen")
    future = load_object(CONFIGURATION)["futureLiveIdentity"]
    protocol = future["openHandsProtocol"]
    expected_endpoints = {
        "OPENHANDS": "http://127.0.0.1:8000",
        "BRIDGE": f"http://{future['runtimePorts']['bridgeHost']}:{future['runtimePorts']['bridgePort']}",
    }
    if binding.get("endpoints") != expected_endpoints \
            or binding.get("sessionKeyFile") != protocol["sessionKeyFile"]:
        raise PermissionError("raw-port binding endpoint or session-key identity mismatch")
    ports = runtime.LocalRawPorts(
        endpoints=binding.get("endpoints", {}),
        process_commands=binding.get("processCommands", {}),
        store_commands=binding.get("storeCommands", {}),
        session_key_file=str(binding.get("sessionKeyFile", "")))
    plan_name = "dress" if args.dress_rehearsal else "measured"
    corpus = load_object(CORPUS)
    plan = dress_plan(corpus) if plan_name == "dress" else exact_plan(corpus)
    measured_credit = 0 if plan_name == "dress" else EXPECTED_REPETITIONS
    ledger = EvidenceLedger()
    fence = LiveExecutionFence(args.execution_identity, args.authorization, args.consumption)
    adapter = runtime.RawCandidate10OrchestrationAdapter(load_object(FRAMING), ports)
    runner = Candidate10Runner(
        adapter, ledger, live_fence=fence, live_output_path=args.output,
        live_runtime_binding_sha256=sha_file(args.raw_port_binding))
    result = runner.run(plan, plan_name, measured_credit)
    receipt_path = args.output / "execution-receipt.json"
    pre = ledger.append("PRE_ACTION_EXECUTION_RECEIPT", {
        "path": str(receipt_path), "status": result["status"],
    })
    receipt = {
        "recordType": "SMA_S2_CANDIDATE_10_EXECUTION_RECEIPT",
        "candidate": 10, "planName": plan_name, "status": result["status"],
        "claim": "OPENHANDS_SMA_BOUNDARY_QUALIFIED"
                 if plan_name == "measured" and result["status"] == "PASS" else None,
        "result": result,
        "journalBeforeReceiptSha256": sha_file(args.output / "raw-evidence.jsonl"),
        "rawPortBindingSha256": sha_file(args.raw_port_binding),
        "preActionRecordId": pre.record_id,
    }
    descriptor = os.open(receipt_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        payload = canonical_bytes(receipt) + b"\n"
        os.write(descriptor, payload)
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    ledger.append("EXECUTION_RECEIPT_WRITTEN", {
        "path": str(receipt_path), "sha256": sha_file(receipt_path),
        "preActionRecordId": pre.record_id,
    })
    print(json.dumps({"status": result["status"], "receipt": str(receipt_path)}, sort_keys=True))
    return 0 if result["status"] == "PASS" else 1


def main() -> int:
    args = parser().parse_args()
    if args.dress_rehearsal or args.execute:
        if args.dress_rehearsal and args.execute:
            raise PermissionError("choose exactly one live mode")
        return live_execution(args)
    if args.offline_contract:
        print(json.dumps(validate_bindings(), indent=2, sort_keys=True))
        return 0
    if args.offline_canonical:
        print(json.dumps(canonical_offline_outcome(args.offline_canonical), sort_keys=True))
        return 0
    raise PermissionError("default deny: choose an offline inspection mode")


if __name__ == "__main__":
    raise SystemExit(main())
