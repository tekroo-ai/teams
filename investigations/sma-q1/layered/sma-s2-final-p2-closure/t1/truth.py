from __future__ import annotations

from dataclasses import dataclass
import json
from pathlib import Path
from typing import Any, Iterable, Mapping
from uuid import UUID

from .model import FailureClass, file_sha256, nested_get, require


T0_QUALIFICATION_SHA256 = "fc28087c5ab54930c45b2665bac19be1cdaac229bf746bde9a5c88d577f45af9"
T0_MANIFEST_SHA256 = "c2520df45c0ac318fc3d23cef864586b609032179eae6a59c955898758bb476e"
T0_REVIEW_SHA256 = "f622ffdf898fdb9a0a426208bec816724771de010ac3d96a4ff1f1d414758eb3"
CORPUS_SHA256 = "c273c148a7b289f6650a26a82c38bda9680904701120e083543e9b7954b9a960"
MATRIX_SHA256 = "806e12a59dd8dcbc8f248d0a8d486c1afb856fc310e1952f7855e9fdf955d4f4"
PREREG_SHA256 = "b7315835b6291d44d3d8cb918dbec1f16c1e64f2106fd70212c6a8f7ec678188"
S1_SHA256 = "3315838d0c5047189494cece49b953481431321d16e238efa338712be076bee4"


def _read(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


@dataclass(frozen=True)
class TruthPaths:
    repo: Path

    @property
    def t0(self) -> Path:
        return self.repo / "OUTPUT/phase-3/sma-s2-final-p2-closure-product-truth-v5"

    @property
    def t0_review(self) -> Path:
        return self.repo / "OUTPUT/phase-3/sma-s2-final-p2-closure-product-truth-v5-independent-review.json"

    @property
    def corpus(self) -> Path:
        return self.repo / "investigations/sma-q1/layered/sma-s2-measured-corpus-candidate-1.json"

    @property
    def matrix(self) -> Path:
        return self.repo / "investigations/sma-q1/layered/sma-s2-predicate-oracle-matrix-candidate-4.json"

    @property
    def prereg(self) -> Path:
        return self.repo / "investigations/sma-q1/layered/sma-s2-preregistration-candidate-2.json"

    @property
    def s1(self) -> Path:
        return self.repo / "investigations/sma-q1/layered/sma-s1-p2final-r1-scientific-adjudication-acceptance.json"


@dataclass(frozen=True)
class ProductTruth:
    paths: TruthPaths
    surface: Mapping[str, Any]
    openhands: Mapping[str, Any]
    sma: Mapping[str, Any]
    hook: Mapping[str, Any]
    manifest: Mapping[str, Any]
    corpus: Mapping[str, Any]
    matrix: Mapping[str, Any]
    prereg: Mapping[str, Any]

    @classmethod
    def load(cls, repo: Path) -> "ProductTruth":
        paths = TruthPaths(repo.resolve())
        exact = {
            paths.t0 / "qualification-receipt.json": T0_QUALIFICATION_SHA256,
            paths.t0 / "product-truth-manifest.json": T0_MANIFEST_SHA256,
            paths.t0_review: T0_REVIEW_SHA256,
            paths.corpus: CORPUS_SHA256,
            paths.matrix: MATRIX_SHA256,
            paths.prereg: PREREG_SHA256,
            paths.s1: S1_SHA256,
        }
        for path, expected in exact.items():
            require(path.is_file(), FailureClass.HARNESS, f"missing frozen binding: {path}")
            require(file_sha256(path) == expected, FailureClass.HARNESS, f"frozen binding drift: {path}")
        truth = cls(
            paths=paths,
            surface=_read(paths.t0 / "product-surface-contract.json"),
            openhands=_read(paths.t0 / "openhands-product-truth.json"),
            sma=_read(paths.t0 / "sma-final-p2-product-truth.json"),
            hook=_read(paths.t0 / "sma-hook-product-truth.json"),
            manifest=_read(paths.t0 / "product-truth-manifest.json"),
            corpus=_read(paths.corpus),
            matrix=_read(paths.matrix),
            prereg=_read(paths.prereg),
        )
        truth.validate()
        return truth

    def validate(self) -> None:
        require(self.openhands.get("authoritativePositiveSource") == "BOUND_OPENHANDS_PYDANTIC_CLASSES", FailureClass.HARNESS, "OpenHands fixtures are not product generated")
        require(self.sma.get("authoritativePositiveSource") == "BOUND_FINAL_P2_IMPLEMENTATION", FailureClass.HARNESS, "SMA fixtures are not product generated")
        require(self.sma.get("replayPersistenceContracts", {}).get("probeAuthoredPromotedMongoDocument") is False, FailureClass.HARNESS, "probe-authored promoted Mongo fixture is forbidden")
        require(self.surface.get("mongo", {}).get("memorySelector") == {
            "origin.openhands_provenance.conversation_id": "{conversation_id}",
            "origin.openhands_provenance.event_id": "{event_id}",
        }, FailureClass.HARNESS, "unexpected Mongo selector")
        require("_id" in self.surface["mongo"]["memoryRequiredProjection"], FailureClass.HARNESS, "Mongo projection omits _id")
        require(self.surface["mongo"]["retrievalSelector"] == {"task_ref": "{hook.smaTraceId}"}, FailureClass.HARNESS, "retrieval selector is not task_ref")
        require(self.surface["qdrant"]["requiredPayloadFields"] == ["agent_id", "memory_id"], FailureClass.HARNESS, "Qdrant payload contract drift")
        require(self.surface["openhands"]["authentication"]["header"] == "X-Session-API-Key", FailureClass.HARNESS, "OpenHands auth contract drift")
        require(len(self.cases) == 20, FailureClass.HARNESS, "science must contain 20 cases")
        require(sum(int(case["repetitions"]) for case in self.cases) == 103, FailureClass.HARNESS, "science must contain 103 repetitions")
        predicate_count = sum(len(case["predicateOracleIds"]) for case in self.matrix["cases"])
        require(predicate_count == 59, FailureClass.HARNESS, "matrix must contain 59 predicates")
        require(len(self.matrix["requiredEvidenceIndex"]) == 10, FailureClass.HARNESS, "matrix must contain 10 evidence classes")

    @property
    def cases(self) -> list[Mapping[str, Any]]:
        return list(self.prereg["cases"])

    @property
    def event_fixtures(self) -> list[Mapping[str, Any]]:
        return list(self.openhands["events"])

    @property
    def intake_decisions(self) -> list[Mapping[str, Any]]:
        return list(self.sma["intake"]["decisions"])

    def event_by_label(self, label: str) -> Mapping[str, Any]:
        matches = [entry for entry in self.event_fixtures if entry["label"] == label]
        require(len(matches) == 1, FailureClass.HARNESS, f"unknown or duplicate product event label {label}")
        return matches[0]["event"]

    def decision_by_label(self, label: str) -> Mapping[str, Any]:
        matches = [entry for entry in self.intake_decisions if entry["label"] == label]
        require(len(matches) == 1, FailureClass.HARNESS, f"unknown or duplicate intake label {label}")
        return matches[0]

    def eligible(self, event: Mapping[str, Any]) -> bool:
        if event.get("kind") != "MessageEvent":
            return False
        triple = (
            event.get("source"),
            event.get("authorship_origin"),
            event.get("semantic_purpose"),
            event.get("agent_response_finality"),
        )
        accepted = {
            ("user", "conversation_input", "task_input", "not_applicable"),
            ("user", "delegated_agent", "task_input", "not_applicable"),
            ("agent", "agent_model", "agent_response", "final"),
        }
        return triple in accepted

    def validate_event_population(self, events: Iterable[Mapping[str, Any]]) -> tuple[set[str], set[str]]:
        eligible: set[str] = set()
        forbidden: set[str] = set()
        for event in events:
            event_id = event.get("id")
            require(isinstance(event_id, str) and event_id, FailureClass.HARNESS, "event missing product identity")
            (eligible if self.eligible(event) else forbidden).add(event_id)
        return eligible, forbidden

    def validate_memory_document(self, document: Mapping[str, Any], conversation_id: str, event_id: str) -> None:
        require(document.get("_id") is not None, FailureClass.SCIENTIFIC, "memory document omits _id")
        require(nested_get(document, "origin.openhands_provenance.conversation_id") == conversation_id, FailureClass.SCIENTIFIC, "memory conversation provenance mismatch")
        require(nested_get(document, "origin.openhands_provenance.event_id") == event_id, FailureClass.SCIENTIFIC, "memory event provenance mismatch")
        require(nested_get(document, "event.sequence_position") is not None, FailureClass.SCIENTIFIC, "memory omits authoritative intake sequence")
        require(nested_get(document, "embeddings.episodic_ref") is None, FailureClass.HARNESS, "forbidden Mongo episodic reference assumption")

    def validate_retrieval_join(self, hook: Mapping[str, Any], retrieval: Mapping[str, Any], memories: Iterable[Mapping[str, Any]]) -> set[str]:
        stdout = hook.get("stdout")
        require(isinstance(stdout, str), FailureClass.SCIENTIFIC, "hook stdout missing")
        trace = json.loads(stdout).get("smaTraceId")
        require(isinstance(trace, str) and trace, FailureClass.SCIENTIFIC, "hook trace missing")
        require(retrieval.get("task_ref") == trace, FailureClass.SCIENTIFIC, "retrieval task_ref mismatch")
        selected = {row.get("memory_id") for row in retrieval.get("results", [])}
        require(None not in selected, FailureClass.SCIENTIFIC, "retrieval result memory identity missing")
        available = {memory.get("_id") for memory in memories}
        require(selected <= available, FailureClass.SCIENTIFIC, "retrieval selects unknown memory")
        return {str(value) for value in selected}

    def validate_qdrant_join(self, memory: Mapping[str, Any], semantic: Mapping[str, Any], episodic: Mapping[str, Any]) -> None:
        memory_id = memory.get("_id")
        agent_id = memory.get("agent_id")
        require(semantic.get("payload") == {"agent_id": agent_id, "memory_id": memory_id}, FailureClass.SCIENTIFIC, "semantic payload mismatch or phantom provenance")
        require(episodic.get("payload") == {"agent_id": agent_id, "memory_id": memory_id}, FailureClass.SCIENTIFIC, "episodic payload mismatch or phantom provenance")
        require((semantic.get("pointId") or semantic.get("id")) == nested_get(memory, "embeddings.semantic_ref"), FailureClass.SCIENTIFIC, "semantic reference mismatch")
        expected = str(UUID(bytes=UUID(bytes=bytes.fromhex("00000000000000000000000000000000")).bytes))
        # Java UUID.nameUUIDFromBytes is MD5 with RFC-4122 version/variant bits.
        import hashlib
        raw = bytearray(hashlib.md5(str(memory_id).encode("utf-8")).digest())
        raw[6] = (raw[6] & 0x0F) | 0x30
        raw[8] = (raw[8] & 0x3F) | 0x80
        expected = str(UUID(bytes=bytes(raw)))
        require((episodic.get("pointId") or episodic.get("id")) == expected, FailureClass.SCIENTIFIC, "episodic deterministic point identity mismatch")

    def case(self, case_id: str) -> Mapping[str, Any]:
        matches = [case for case in self.cases if case["id"] == case_id]
        require(len(matches) == 1, FailureClass.HARNESS, f"unknown case {case_id}")
        return matches[0]

    def predicate_ids(self, case_id: str) -> tuple[str, ...]:
        matches = [case for case in self.matrix["cases"] if case["caseId"] == case_id]
        require(len(matches) == 1, FailureClass.HARNESS, f"matrix missing case {case_id}")
        return tuple(matches[0]["predicateOracleIds"])
