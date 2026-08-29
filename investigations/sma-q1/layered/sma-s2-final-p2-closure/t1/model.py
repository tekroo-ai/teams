from __future__ import annotations

from dataclasses import dataclass, field
from enum import StrEnum
from hashlib import sha256
import json
from pathlib import Path
from typing import Any, Mapping, Sequence


def canonical_bytes(value: Any) -> bytes:
    return json.dumps(
        value,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
        allow_nan=False,
    ).encode("utf-8")


def digest(value: Any) -> str:
    return sha256(canonical_bytes(value)).hexdigest()


def file_sha256(path: Path) -> str:
    h = sha256()
    with path.open("rb") as stream:
        while chunk := stream.read(1024 * 1024):
            h.update(chunk)
    return h.hexdigest()


class FailureClass(StrEnum):
    SCIENTIFIC = "SCIENTIFIC"
    HARNESS = "HARNESS"
    ENVIRONMENT = "ENVIRONMENT"
    SAFETY = "SAFETY"


class RunMode(StrEnum):
    OFFLINE_DRESS = "OFFLINE_DRESS"
    OFFLINE_MEASURED = "OFFLINE_MEASURED"
    ZERO_CREDIT_DRESS = "ZERO_CREDIT_DRESS"
    MEASURED = "MEASURED"


class ServiceMode(StrEnum):
    CAPTURE_AND_RETRIEVAL = "CAPTURE_AND_RETRIEVAL"
    CAPTURE_ONLY = "CAPTURE_ONLY"
    RETRIEVAL_ONLY = "RETRIEVAL_ONLY"
    STOPPED = "STOPPED"


@dataclass(frozen=True)
class RawHttpRequest:
    method: str
    route: str
    headers: Mapping[str, str]
    body: bytes = b""
    timeout_ms: int = 10_000

    def as_json(self) -> dict[str, Any]:
        headers = {}
        for key, value in sorted(self.headers.items()):
            headers[key] = f"sha256:{sha256(value.encode()).hexdigest()}" if key.lower() in {"x-session-api-key", "authorization", "api-key"} else value
        return {
            "method": self.method,
            "route": self.route,
            "headers": headers,
            "bodySha256": sha256(self.body).hexdigest(),
            "bodyLength": len(self.body),
            "timeoutMs": self.timeout_ms,
        }


@dataclass(frozen=True)
class RawHttpReceipt:
    request: RawHttpRequest
    status: int
    headers: Mapping[str, str]
    body: bytes
    started_ns: int
    completed_ns: int
    error_kind: str | None = None

    def json_body(self) -> Any:
        return json.loads(self.body.decode("utf-8"))

    def as_json(self) -> dict[str, Any]:
        return {
            "request": self.request.as_json(),
            "status": self.status,
            "headers": dict(sorted(self.headers.items())),
            "bodySha256": sha256(self.body).hexdigest(),
            "bodyLength": len(self.body),
            "startedNs": self.started_ns,
            "completedNs": self.completed_ns,
            "errorKind": self.error_kind,
        }


@dataclass(frozen=True)
class RawStoreQuery:
    store: str
    namespace: str
    collection: str
    selector: Mapping[str, Any]
    projection: tuple[str, ...]
    timeout_ms: int = 10_000

    def as_json(self) -> dict[str, Any]:
        return {
            "store": self.store,
            "namespace": self.namespace,
            "collection": self.collection,
            "selector": self.selector,
            "projection": list(self.projection),
            "timeoutMs": self.timeout_ms,
        }


@dataclass(frozen=True)
class RawStoreReceipt:
    query: RawStoreQuery
    rows: tuple[Mapping[str, Any], ...]
    started_ns: int
    completed_ns: int
    error_kind: str | None = None

    def as_json(self) -> dict[str, Any]:
        return {
            "query": self.query.as_json(),
            "rows": list(self.rows),
            "startedNs": self.started_ns,
            "completedNs": self.completed_ns,
            "errorKind": self.error_kind,
        }


@dataclass(frozen=True)
class RawFileReceipt:
    path: str
    exists: bool
    content: bytes
    started_ns: int
    completed_ns: int
    error_kind: str | None = None

    def as_json(self) -> dict[str, Any]:
        return {
            "path": self.path,
            "exists": self.exists,
            "contentSha256": sha256(self.content).hexdigest(),
            "contentLength": len(self.content),
            "startedNs": self.started_ns,
            "completedNs": self.completed_ns,
            "errorKind": self.error_kind,
        }


@dataclass(frozen=True)
class RawProcessRequest:
    action: str
    argv: tuple[str, ...]
    cwd: str
    timeout_ms: int


@dataclass(frozen=True)
class RawProcessReceipt:
    request: RawProcessRequest
    exit_code: int
    stdout: bytes
    stderr: bytes
    started_ns: int
    completed_ns: int
    error_kind: str | None = None

    def as_json(self) -> dict[str, Any]:
        return {
            "action": self.request.action,
            "argv": list(self.request.argv),
            "cwd": self.request.cwd,
            "timeoutMs": self.request.timeout_ms,
            "exitCode": self.exit_code,
            "stdoutSha256": sha256(self.stdout).hexdigest(),
            "stdoutLength": len(self.stdout),
            "stderrSha256": sha256(self.stderr).hexdigest(),
            "stderrLength": len(self.stderr),
            "startedNs": self.started_ns,
            "completedNs": self.completed_ns,
            "errorKind": self.error_kind,
        }


@dataclass(frozen=True)
class EvidenceRecord:
    action: str
    phase: str
    operation_id: str
    monotonic_ns: int
    receipt_digest: str
    previous_digest: str
    digest: str


@dataclass
class EvidenceLedger:
    records: list[EvidenceRecord] = field(default_factory=list)

    def append(self, action: str, phase: str, operation_id: str, now_ns: int, receipt: Any) -> EvidenceRecord:
        previous = self.records[-1].digest if self.records else "0" * 64
        receipt_digest = digest(receipt)
        body = {
            "action": action,
            "phase": phase,
            "operationId": operation_id,
            "monotonicNs": now_ns,
            "receiptDigest": receipt_digest,
            "previousDigest": previous,
        }
        record = EvidenceRecord(
            action=action,
            phase=phase,
            operation_id=operation_id,
            monotonic_ns=now_ns,
            receipt_digest=receipt_digest,
            previous_digest=previous,
            digest=digest(body),
        )
        self.records.append(record)
        return record

    def verify(self) -> bool:
        previous = "0" * 64
        for record in self.records:
            body = {
                "action": record.action,
                "phase": record.phase,
                "operationId": record.operation_id,
                "monotonicNs": record.monotonic_ns,
                "receiptDigest": record.receipt_digest,
                "previousDigest": previous,
            }
            if record.previous_digest != previous or record.digest != digest(body):
                return False
            previous = record.digest
        return True


@dataclass(frozen=True)
class OperationDescriptor:
    case_id: str
    repetitions: int
    entry_mode: ServiceMode
    exit_mode: ServiceMode
    conversation_roles: tuple[str, ...]
    workspace_roles: tuple[str, ...]
    owned_namespaces: tuple[str, ...]
    conversation_prefix: str
    actual_event_identity_rule: str
    expected_event_labels: tuple[str, ...]
    forbidden_event_labels: tuple[str, ...]
    operation_deadline_ms: int
    stabilization_deadline_ms: int
    stable_reads: int
    terminal_observation: str
    stabilization_rule: str
    recovery: tuple[str, ...]
    cleanup: tuple[str, ...]


@dataclass(frozen=True)
class OperationKey:
    case_id: str
    repetition: int

    @property
    def value(self) -> str:
        return f"{self.case_id}#{self.repetition:03d}"


@dataclass
class OperationObservation:
    key: OperationKey
    descriptor: OperationDescriptor
    raw_events: list[Mapping[str, Any]] = field(default_factory=list)
    conversations: list[Mapping[str, Any]] = field(default_factory=list)
    hooks: list[Mapping[str, Any]] = field(default_factory=list)
    model_requests: list[Mapping[str, Any]] = field(default_factory=list)
    model_terminals: list[Mapping[str, Any]] = field(default_factory=list)
    memories: list[Mapping[str, Any]] = field(default_factory=list)
    retrievals: list[Mapping[str, Any]] = field(default_factory=list)
    semantic_points: list[Mapping[str, Any]] = field(default_factory=list)
    episodic_points: list[Mapping[str, Any]] = field(default_factory=list)
    logs: list[Mapping[str, Any]] = field(default_factory=list)
    process_inventory: list[Mapping[str, Any]] = field(default_factory=list)
    workspace_inventory: list[Mapping[str, Any]] = field(default_factory=list)
    namespace_inventory: list[Mapping[str, Any]] = field(default_factory=list)
    timings: list[Mapping[str, Any]] = field(default_factory=list)
    raw_receipts: list[Mapping[str, Any]] = field(default_factory=list)
    cleanup_receipts: list[Mapping[str, Any]] = field(default_factory=list)
    failure: FailureClass | None = None
    failure_detail: str | None = None


@dataclass(frozen=True)
class OracleResult:
    oracle_id: str
    passed: bool
    evidence_digests: tuple[str, ...]
    detail: str


@dataclass
class OperationResult:
    observation: OperationObservation
    scientific: list[OracleResult]
    evidence: list[OracleResult]
    passed: bool


@dataclass(frozen=True)
class LiveAuthority:
    package_manifest_path: str
    package_manifest_sha256: str
    authorization_path: str
    authorization_sha256: str
    execution_identity_path: str
    execution_identity_sha256: str
    mode: RunMode
    consumed_marker_path: str


class HarnessFailure(RuntimeError):
    def __init__(self, failure_class: FailureClass, message: str):
        super().__init__(message)
        self.failure_class = failure_class


def require(condition: bool, failure_class: FailureClass, message: str) -> None:
    if not condition:
        raise HarnessFailure(failure_class, message)


def nested_get(value: Mapping[str, Any], path: str) -> Any:
    current: Any = value
    for part in path.split("."):
        if not isinstance(current, Mapping) or part not in current:
            return None
        current = current[part]
    return current


def stable_rows(rows: Sequence[Mapping[str, Any]]) -> tuple[str, ...]:
    return tuple(sorted(digest(row) for row in rows))
