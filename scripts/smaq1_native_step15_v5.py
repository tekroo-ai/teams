#!/usr/bin/env python3
"""Prospective SMA-Q1 Step 15 v5 runner.

V5 inherits the frozen v4 corrective runner and replaces only controlled
fixture preparation, raw-projection readiness semantics, replay isolation, and
subprocess evidence retention. It must be accepted and separately authorized
before execution.
"""

from __future__ import annotations

import hashlib
import importlib.util
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Any


TEAMS = Path("/Users/paul/work/tekroo-ai/teams")
SMA = Path("/Users/paul/work/tekroo-ai/sma-step15")
INHERITED_RUNNER = TEAMS / "scripts/smaq1_native_step15_v4_rerun1.py"
MANIFEST = TEAMS / "investigations/sma-q1/preregistration-native-v5.json"
BASE_MANIFEST = TEAMS / "investigations/sma-q1/preregistration-native-v2.json"
IDENTITY = TEAMS / "investigations/sma-q1/step-15-execution-identity-v5.json"
AUTHORIZATION = TEAMS / "investigations/sma-q1/step-15-execution-authorization-v5.json"
FIXTURE_SOURCE = TEAMS / "investigations/sma-q1/fixture-support/DeterministicReplayPromotion.java"
OUTPUT = TEAMS / "OUTPUT/phase-3/sma-q1n-step15-v5"
RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v5"
QUAL_RUN_ROOT = SMA / "target/sma-q1n-step15-20260813-v5-fixture"
MANIFEST_SHA = "21030ef4a7004217401b0243e5bb7e94be6bc44ecb9869873a72f112fe676422"
BASE_MANIFEST_SHA = "c618371d571d5333aebd2bdc83d2db2559115f5ec3e1903b33e8e0ab157edf5d"
FIXTURE_SOURCE_SHA = "580f29edefc8c703e68adc3ba6536b58ba1b577a9ed0ca6969ff172eaacb8117"
INHERITED_RUNNER_SHA = "91e42b9208134a5fb1df91f4b37988a465f34e6efad8c1a5bfb47469fb2721f2"
REPLAY_CANARY = "smaq1:v5:controlled-no-background-replay"


def load_inherited_runner():
    spec = importlib.util.spec_from_file_location("smaq1_v5_inherited", INHERITED_RUNNER)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load frozen v4 corrective runner")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


v4 = load_inherited_runner()
ACTIVE_JOURNAL: Any | None = None


def sha_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def sha_text(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


def subprocess_receipt(name: str, completed: subprocess.CompletedProcess[str],
                       retain_failure: bool) -> dict[str, Any]:
    stdout = completed.stdout or ""
    stderr = completed.stderr or ""
    receipt: dict[str, Any] = {
        "name": name,
        "exit_code": completed.returncode,
        "stdout_length": len(stdout.encode()),
        "stdout_sha256": sha_text(stdout),
        "stderr_length": len(stderr.encode()),
        "stderr_sha256": sha_text(stderr),
        "status": "PASS" if completed.returncode == 0 else "FAIL",
    }
    if completed.returncode != 0 and retain_failure and OUTPUT.exists():
        report_root = OUTPUT / "subprocess-reports"
        report_root.mkdir(parents=True, exist_ok=True)
        stdout_path = report_root / f"{name}.stdout.txt"
        stderr_path = report_root / f"{name}.stderr.txt"
        stdout_path.write_text(stdout, encoding="utf-8")
        stderr_path.write_text(stderr, encoding="utf-8")
        receipt.update({
            "stdout_path": str(stdout_path),
            "stdout_file_sha256": sha_file(stdout_path),
            "stderr_path": str(stderr_path),
            "stderr_file_sha256": sha_file(stderr_path),
        })
        if ACTIVE_JOURNAL is not None:
            ACTIVE_JOURNAL.append("SUBPROCESS_FAILURE_REPORT", receipt)
    return receipt


def run_captured(args: list[str], timeout: float,
                 input_text: str | None = None) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(
            args, input=input_text, cwd=SMA, capture_output=True, text=True,
            timeout=timeout, check=False,
        )
    except subprocess.TimeoutExpired as error:
        stdout = error.stdout.decode(errors="replace") if isinstance(error.stdout, bytes) else (error.stdout or "")
        stderr = error.stderr.decode(errors="replace") if isinstance(error.stderr, bytes) else (error.stderr or "")
        stderr += f"\nTIMEOUT_AFTER_SECONDS={timeout}\n"
        return subprocess.CompletedProcess(args=args, returncode=124, stdout=stdout, stderr=stderr)


def compile_fixture_driver() -> dict[str, Any]:
    build_root = SMA / "target/sma-q1n-step15-v5-fixture-driver"
    classes = build_root / "classes"
    classpath_file = build_root / "classpath.txt"
    shutil.rmtree(build_root, ignore_errors=True)
    classes.mkdir(parents=True, exist_ok=False)
    dependency = run_captured(
        ["mvn", "-q", "-DincludeScope=test", "dependency:build-classpath",
         f"-Dmdep.outputFile={classpath_file}"], timeout=120,
    )
    dependency_receipt = subprocess_receipt(
        "fixture-driver-dependency-classpath", dependency, OUTPUT.exists(),
    )
    if dependency.returncode != 0:
        raise RuntimeError(f"fixture dependency classpath failed: {dependency_receipt}")
    classpath = str(SMA / "target/sma-1.0-SNAPSHOT.jar") + os.pathsep + classpath_file.read_text().strip()
    compiled = run_captured(
        ["javac", "-cp", classpath, "-d", str(classes), str(FIXTURE_SOURCE)],
        timeout=120,
    )
    compile_receipt = subprocess_receipt("fixture-driver-javac", compiled, OUTPUT.exists())
    if compiled.returncode != 0:
        raise RuntimeError(f"fixture driver compile failed: {compile_receipt}")
    return {
        "status": "PASS",
        "dependency": dependency_receipt,
        "compile": compile_receipt,
        "classes_root": str(classes),
        "classpath_file_sha256": sha_file(classpath_file),
    }


def deterministic_promote(database: str, semantic: str, episodic: str,
                          memory_id: str, canonical_text: str,
                          logical_id: str) -> dict[str, Any]:
    build_root = SMA / "target/sma-q1n-step15-v5-fixture-driver"
    classes = build_root / "classes"
    classpath_file = build_root / "classpath.txt"
    if not classes.exists() or not classpath_file.exists():
        compile_fixture_driver()
    classpath = os.pathsep.join([
        str(classes), str(SMA / "target/sma-1.0-SNAPSHOT.jar"), classpath_file.read_text().strip(),
    ])
    completed = run_captured(
        ["java", "-cp", classpath,
         "ai.tekroo.sma.qualification.DeterministicReplayPromotion",
         database, semantic, episodic, memory_id],
        input_text=canonical_text, timeout=180,
    )
    receipt = subprocess_receipt(f"fixture-promotion-{logical_id}", completed, True)
    if completed.returncode != 0:
        raise RuntimeError(f"deterministic fixture promotion failed for {logical_id}: {receipt}")
    return receipt


def static_expectation_checks(compile_driver: bool = True) -> dict[str, Any]:
    intake_text = v4.INTAKE_SOURCE.read_text(encoding="utf-8")
    replay_text = v4.REPLAY_SOURCE.read_text(encoding="utf-8")
    fixture_text = FIXTURE_SOURCE.read_text(encoding="utf-8")
    hashes = {
        "intake": sha_file(v4.INTAKE_SOURCE),
        "replay": sha_file(v4.REPLAY_SOURCE),
        "vector": sha_file(v4.VECTOR_SOURCE),
        "fixture_driver": sha_file(FIXTURE_SOURCE),
        "inherited_runner": sha_file(INHERITED_RUNNER),
    }
    rules = {
        "workspace_fingerprint_24": "sha256(canonicalWorkspace(workspaceDir)).substring(0, 24)" in intake_text,
        "episodic_qdrant_upsert": "vectorStorePort.upsert(VectorCollection.EPISODIC" in replay_text,
        "canonical_update_semantic_only": (
            "updateCanonical(locked.id(), currentDocVersion, newCanonical, semanticRef, now)" in replay_text
        ),
        "production_replay": "new ReplayWorker(" in fixture_text,
        "production_mongo_repositories": "new MongoInterpretationLogRepository(" in fixture_text,
        "production_lifecycle": "new LifecyclePolicy(config)" in fixture_text,
        "production_embedding": "OllamaEmbeddingService.create" in fixture_text,
        "production_vector_store": "new QdrantVectorStore(" in fixture_text,
        "deterministic_cognition": "deterministicCandidate(" in fixture_text,
        "no_ollama_cognitive_engine": "OllamaCognitiveEngine" not in fixture_text,
        "fixture_text_via_stdin": "System.in.readAllBytes()" in fixture_text,
        "no_direct_lifecycle_mutation": all(token not in fixture_text for token in (
            ".applyForwardToConsistent(", ".applyRegressionToRaw(",
            ".applyRegressionToConsistent(", ".updateConvergence(",
        )),
        "no_direct_vector_upsert": ".upsert(" not in fixture_text,
    }
    expected_partitions = {
        "qualification_alpha": "openhands:f45d75ea182fa6b20dd31a8d:default",
        "qualification_beta": "openhands:c0b9791cd008ef719c7c6a05:default",
        "measured_alpha": "openhands:4d8627c381dc4e9a69dd2b69:default",
        "measured_beta": "openhands:07bd77aca857ae44a559c6d8:default",
    }
    computed_partitions = {
        "qualification_alpha": v4.workspace_partition(QUAL_RUN_ROOT / "actor-alpha"),
        "qualification_beta": v4.workspace_partition(QUAL_RUN_ROOT / "actor-beta"),
        "measured_alpha": v4.workspace_partition(RUN_ROOT / "actor-alpha"),
        "measured_beta": v4.workspace_partition(RUN_ROOT / "actor-beta"),
    }
    retained = v4.retained_v3_qualification_observation()
    retained_problems: list[str] = []
    v3_root = SMA / "target/sma-q1n-step15-20260813-v3-fixture"
    for logical_id, state in retained["states"].items():
        projection = retained["projections"][logical_id]
        eligible = bool(projection["eligible"])
        expected_workspace = v3_root / ("actor-beta" if logical_id == "mem-beta-port" else "actor-alpha")
        expected_agent = v4.workspace_partition(expected_workspace)
        expected_point_id = v4.java_name_uuid(state["memory_id"])
        if state["agent_id"] != expected_agent:
            retained_problems.append(f"{logical_id}:agent")
        if state["state"] != ("consistent" if eligible else "raw"):
            retained_problems.append(f"{logical_id}:state")
        if bool(state["reasoning_eligible"]) != eligible:
            retained_problems.append(f"{logical_id}:eligibility")
        if eligible:
            if state["semantic_ref"] != expected_point_id or state["episodic_ref"] is not None:
                retained_problems.append(f"{logical_id}:mongo_refs")
            for collection_name in ("semantic", "episodic"):
                receipt = projection[collection_name]
                point = receipt["points"][0] if receipt["point_count"] == 1 else {}
                if (receipt["http_status"] != 200 or receipt["point_count"] != 1
                        or point.get("point_id") != expected_point_id
                        or point.get("memory_id") != state["memory_id"]
                        or point.get("agent_id") != expected_agent
                        or int(point.get("vector_length", 0)) <= 0):
                    retained_problems.append(f"{logical_id}:{collection_name}_projection")
    compile_receipt = compile_fixture_driver() if compile_driver else {"status": "NOT_RUN"}
    checks = {
        "source_hashes": hashes,
        "source_hashes_match": (
            hashes["intake"] == v4.INTAKE_SOURCE_SHA
            and hashes["replay"] == v4.REPLAY_SOURCE_SHA
            and hashes["vector"] == v4.VECTOR_SOURCE_SHA
            and hashes["fixture_driver"] == FIXTURE_SOURCE_SHA
            and hashes["inherited_runner"] == INHERITED_RUNNER_SHA
        ),
        "source_rules": rules,
        "v5_partitions_match": computed_partitions == expected_partitions,
        "expected_v5_partitions": expected_partitions,
        "computed_v5_partitions": computed_partitions,
        "retained_v3_journal_sha256": sha_file(v4.V3_JOURNAL),
        "retained_v3_journal_matches": sha_file(v4.V3_JOURNAL) == v4.V3_JOURNAL_SHA,
        "retained_v3_expectation_problems": retained_problems,
        "retained_v3_expectations_pass": not retained_problems,
        "fixture_compile": compile_receipt,
    }
    if not (checks["source_hashes_match"] and all(rules.values())
            and checks["v5_partitions_match"]
            and checks["retained_v3_journal_matches"]
            and checks["retained_v3_expectations_pass"]
            and compile_receipt["status"] in ("PASS", "NOT_RUN")):
        raise RuntimeError(f"v5 static expectation self-test failed: {checks}")
    return checks


def service_environment(capture: bool, retrieval: bool,
                        _replay_canary: str | None = None) -> dict[str, str]:
    environment = original_service_environment(capture, retrieval, REPLAY_CANARY)
    environment["SMA_CANARY_PARTITION"] = REPLAY_CANARY
    return environment


def start_service(capture: bool, retrieval: bool,
                  _replay_canary: str | None = None) -> dict[str, Any]:
    result = original_start_service(capture, retrieval, REPLAY_CANARY)
    result["replay_canary"] = REPLAY_CANARY
    return result


def canonical_text_for_memory(memory_id: str) -> str:
    matches = [item for item in v4.memory_documents() if str(item.get("_id")) == memory_id]
    if len(matches) != 1:
        raise RuntimeError(f"fixture memory identity cardinality mismatch: {len(matches)}")
    text = str((matches[0].get("canonical") or {}).get("text") or "")
    if not text:
        raise RuntimeError("fixture memory canonical text absent")
    return text


def promote_memory(database: str, semantic: str, episodic: str, memory_id: str,
                   _preferred_marker: str, _required_marker: str) -> dict[str, Any]:
    return deterministic_promote(
        database, semantic, episodic, memory_id,
        canonical_text_for_memory(memory_id), memory_id,
    )


def seed_corpus(corpus: list[dict[str, Any]], conversations: list[str],
                journal: Any, receipt_prefix: str = "") -> dict[str, str]:
    sources = []
    for item in corpus:
        workspace = v4.ALPHA if item["partition"] == "actor-alpha" else v4.BETA
        sources.append({
            "memory_id": item["memoryId"],
            **v4.create_raw_source(workspace, item["text"], conversations),
        })
    journal.append(f"{receipt_prefix}CORPUS_SOURCE_EVENTS", {"sources": sources})
    journal.append(f"{receipt_prefix}SERVICE_START", v4.start_service(True, True))
    v4.wait_memory_count(4)
    mapped = {
        item["memoryId"]: str(v4.find_memory(item["expectedMarker"])["_id"])
        for item in corpus
    }
    journal.append(f"{receipt_prefix}SERVICE_STOP_FOR_PROMOTION", v4.stop_service())
    for item in corpus:
        if not item["eligible"]:
            continue
        memory_id = mapped[item["memoryId"]]
        promotion = deterministic_promote(
            v4.DATABASE, v4.SEMANTIC, v4.EPISODIC,
            memory_id, item["text"], item["memoryId"],
        )
        journal.append(f"{receipt_prefix}CORPUS_PROMOTION", {
            "logical_memory_id": item["memoryId"],
            "memory_id": memory_id,
            **promotion,
        })

    documents = v4.memory_documents()
    states: dict[str, Any] = {}
    projections: dict[str, Any] = {}
    problems: list[str] = []
    for item in corpus:
        document = v4.find_memory(item["expectedMarker"])
        memory_id = mapped[item["memoryId"]]
        embeddings = document.get("embeddings") or {}
        state = {
            "memory_id": memory_id,
            "state": document.get("state"),
            "reasoning_eligible": document.get("reasoning_eligible"),
            "agent_id": document.get("agent_id"),
            "doc_version": document.get("doc_version"),
            "episodic_ref": embeddings.get("episodic_ref"),
            "semantic_ref": embeddings.get("semantic_ref"),
            "source_event_id": next(
                source["event_id"] for source in sources
                if source["memory_id"] == item["memoryId"]
            ),
        }
        projection = {
            "eligible": item["eligible"],
            "semantic": v4.qdrant_point_receipt(v4.SEMANTIC, memory_id),
            "episodic": v4.qdrant_point_receipt(v4.EPISODIC, memory_id),
        }
        states[item["memoryId"]] = state
        projections[item["memoryId"]] = projection
        expected_agent = v4.ALPHA_PARTITION if item["partition"] == "actor-alpha" else v4.BETA_PARTITION
        if state["agent_id"] != expected_agent:
            problems.append(f"{item['memoryId']}:mongo_agent")
        if state["state"] != item["state"] or bool(state["reasoning_eligible"]) != bool(item["eligible"]):
            problems.append(f"{item['memoryId']}:mongo_lifecycle")
        if item["eligible"]:
            expected_point_id = v4.java_name_uuid(memory_id)
            for collection_name in ("semantic", "episodic"):
                receipt = projection[collection_name]
                point = receipt["points"][0] if receipt["point_count"] == 1 else {}
                if (receipt["http_status"] != 200 or receipt["point_count"] != 1
                        or point.get("point_id") != expected_point_id
                        or point.get("memory_id") != memory_id
                        or point.get("agent_id") != expected_agent
                        or int(point.get("vector_length", 0)) <= 0):
                    problems.append(f"{item['memoryId']}:{collection_name}_projection")
            if state["episodic_ref"] is not None or state["semantic_ref"] != expected_point_id:
                problems.append(f"{item['memoryId']}:mongo_embedding_refs")

    observed = {
        "states": states,
        "projections": projections,
        "mongo_memories": len(documents),
        "semantic_points": v4.support.qdrant_count(v4.SEMANTIC),
        "episodic_points": v4.support.qdrant_count(v4.EPISODIC),
        "distinct_actual_memory_ids": len(set(mapped.values())),
        "raw_projection_presence_is_observational": True,
    }
    journal.append(f"{receipt_prefix}CORPUS_OBSERVED", observed)
    if len(documents) != len(corpus):
        problems.append("mongo_memory_count")
    if len(set(mapped.values())) != len(corpus):
        problems.append("actual_memory_ids_not_distinct")
    if problems:
        raise RuntimeError(f"fixture readiness mismatch: {sorted(set(problems))}")
    journal.append(f"{receipt_prefix}CORPUS_READY", {
        "logical_memory_ids": sorted(mapped),
        "eligible_memory_count": sum(bool(item["eligible"]) for item in corpus),
        "semantic_points_observed": observed["semantic_points"],
        "episodic_points_observed": observed["episodic_points"],
        "eligible_point_level_receipts_retained": True,
        "raw_projection_presence_is_nondecisive": True,
    })
    journal.append(f"{receipt_prefix}SERVICE_START_RETRIEVAL", v4.start_service(False, True))
    return mapped


def preflight(journal: Any) -> dict[str, Any]:
    manifest = json.loads(MANIFEST.read_text())
    base_manifest = json.loads(BASE_MANIFEST.read_text())
    identity = json.loads(IDENTITY.read_text())
    authorization = json.loads(AUTHORIZATION.read_text())
    harness_sha = sha_file(Path(__file__))
    checks = {
        "manifest_sha256": sha_file(MANIFEST),
        "manifest_matches": sha_file(MANIFEST) == MANIFEST_SHA,
        "base_manifest_matches": sha_file(BASE_MANIFEST) == BASE_MANIFEST_SHA,
        "authorization_identity_matches": authorization["executionIdentitySHA256"] == sha_file(IDENTITY),
        "authorization_harness_matches": authorization["harnessSHA256"] == harness_sha,
        "authorization_manifest_matches": authorization["frozenManifestSHA256"] == MANIFEST_SHA,
        "authorization_scope_matches": (
            bool(authorization["executionAuthorized"])
            and bool(authorization["authorizedScope"]["fixtureQualification"])
            and bool(authorization["authorizedScope"]["measuredScenarioCorpus"])
            and int(authorization["authorizedScope"]["scenarioCount"]) == 18
            and int(authorization["authorizedScope"]["repetitionCount"]) == 96
        ),
        "scenario_count": len(base_manifest["scenarioCorpus"]),
        "repetition_count": sum(int(item["repetitions"]) for item in base_manifest["scenarioCorpus"]),
        "identity_is_non_authorizing_fence": not bool(identity["executionAuthorized"]),
        "identity_freeze_accepted": bool(identity["executionFence"]["freezeAccepted"]),
        "identity_manifest_matches": identity["frozenExperiment"]["manifestSHA256"] == MANIFEST_SHA,
        "identity_harness_matches": identity["harness"]["sha256"] == harness_sha,
        "identity_fixture_driver_matches": identity["harness"]["fixtureDriverSHA256"] == sha_file(FIXTURE_SOURCE),
        "sma_commit": v4.command("git", "rev-parse", "HEAD", timeout=15).stdout.strip(),
        "sma_tree": v4.command("git", "rev-parse", "HEAD^{tree}", timeout=15).stdout.strip(),
        "sma_clean": v4.command("git", "status", "--short", timeout=15).stdout == "",
        "service_absent": not v4.support.launch_identity(v4.LABEL)["loaded"],
        "bridge_absent": v4.support.listener_pid(8130) is None,
        "mongo_absent": not v4.support.mongo_database_exists(v4.DATABASE),
        "semantic_absent": not v4.support.qdrant_collection_exists(v4.SEMANTIC),
        "episodic_absent": not v4.support.qdrant_collection_exists(v4.EPISODIC),
        "run_root_absent": not v4.RUN_ROOT.exists(),
        "qualification_absent": {
            "service": not v4.support.launch_identity(v4.QUAL_LABEL)["loaded"],
            "mongo": not v4.support.mongo_database_exists(v4.QUAL_DATABASE),
            "semantic": not v4.support.qdrant_collection_exists(v4.QUAL_SEMANTIC),
            "episodic": not v4.support.qdrant_collection_exists(v4.QUAL_EPISODIC),
            "run_root": not v4.QUAL_RUN_ROOT.exists(),
        },
        "service_jar_sha256": sha_file(SMA / "target/sma-1.0-SNAPSHOT.jar"),
        "harness_sha256": harness_sha,
        "fixture_driver_sha256": sha_file(FIXTURE_SOURCE),
        "mutable_runtime": v4.runtime_recheck(),
        "agent_canvas_health": v4.http("GET", v4.support.INGRESS, "/health", timeout=10)[0],
        "agent_server_health": v4.http("GET", v4.support.AGENT_SERVER, "/health", timeout=10)[0],
        "scheduler_isolation": REPLAY_CANARY,
    }
    checks["sma_identity_matches"] = checks["sma_commit"] == v4.SMA_COMMIT and checks["sma_tree"] == v4.SMA_TREE
    required = [
        checks["manifest_matches"], checks["base_manifest_matches"],
        checks["authorization_identity_matches"], checks["authorization_harness_matches"],
        checks["authorization_manifest_matches"], checks["authorization_scope_matches"],
        checks["scenario_count"] == 18, checks["repetition_count"] == 96,
        checks["identity_is_non_authorizing_fence"], checks["identity_freeze_accepted"],
        checks["identity_manifest_matches"], checks["identity_harness_matches"],
        checks["identity_fixture_driver_matches"], checks["sma_identity_matches"],
        checks["sma_clean"], checks["service_absent"], checks["bridge_absent"],
        checks["mongo_absent"], checks["semantic_absent"], checks["episodic_absent"],
        checks["run_root_absent"], all(checks["qualification_absent"].values()),
        checks["agent_canvas_health"] == 200, checks["agent_server_health"] == 200,
    ]
    if not all(required):
        raise RuntimeError(f"preflight failed: {checks}")
    journal.append("PREFLIGHT", checks)
    return checks


class DurableJournal(v4.DurableJournal):
    def __init__(self, path: Path):
        super().__init__(path)
        global ACTIVE_JOURNAL
        ACTIVE_JOURNAL = self


def cleanup(conversations: list[str], journal: Any,
            receipt_kind: str = "CLEANUP") -> dict[str, Any]:
    result = original_cleanup(conversations, journal, receipt_kind)
    build_root = SMA / "target/sma-q1n-step15-v5-fixture-driver"
    shutil.rmtree(build_root, ignore_errors=True)
    result["verified"]["fixture_driver_build_absent"] = not build_root.exists()
    journal.append(f"{receipt_kind}_FIXTURE_DRIVER", {
        "build_root": str(build_root),
        "absent": not build_root.exists(),
    })
    return result


def configure_inherited_runner() -> None:
    v4.__file__ = str(Path(__file__))
    v4.MANIFEST = MANIFEST
    v4.BASE_MANIFEST = BASE_MANIFEST
    v4.IDENTITY = IDENTITY
    v4.AUTHORIZATION = AUTHORIZATION
    v4.OUTPUT = OUTPUT
    v4.RUN_ROOT = RUN_ROOT
    v4.ALPHA = RUN_ROOT / "actor-alpha"
    v4.BETA = RUN_ROOT / "actor-beta"
    v4.RUNTIME = RUN_ROOT / "runtime"
    v4.JOURNAL = OUTPUT / "raw-receipts.jsonl"
    v4.SUMMARY = OUTPUT / "execution-receipt.json"
    v4.LABEL = "com.tekroo.sma-service-smaq1n-step15-v5"
    v4.DATABASE = "sma_q1n_step15_20260813_v5"
    v4.SEMANTIC = "sma_q1n_step15_v5_semantic_20260813"
    v4.EPISODIC = "sma_q1n_step15_v5_episodic_20260813"
    v4.QUAL_RUN_ROOT = QUAL_RUN_ROOT
    v4.QUAL_DATABASE = "sma_q1n_step15_v5_fixture_20260813"
    v4.QUAL_SEMANTIC = "sma_q1n_step15_v5_fixture_semantic_20260813"
    v4.QUAL_EPISODIC = "sma_q1n_step15_v5_fixture_episodic_20260813"
    v4.QUAL_LABEL = "com.tekroo.sma-service-smaq1n-step15-v5-fixture"
    v4.MANIFEST_SHA = MANIFEST_SHA
    v4.ALPHA_PARTITION = v4.workspace_partition(v4.ALPHA)
    v4.BETA_PARTITION = v4.workspace_partition(v4.BETA)
    v4.support.SMA_LABEL = v4.LABEL
    v4.DurableJournal = DurableJournal
    v4.static_expectation_checks = static_expectation_checks
    v4.preflight = preflight
    v4.cleanup = cleanup
    v4.seed_corpus = seed_corpus
    v4.service_environment = service_environment
    v4.start_service = start_service
    v4.support.promote_memory = promote_memory


original_service_environment = v4.service_environment
original_start_service = v4.start_service
original_cleanup = v4.cleanup
configure_inherited_runner()


def main() -> None:
    if "--static-self-test" in sys.argv:
        print(json.dumps(static_expectation_checks(), indent=2, sort_keys=True))
        return
    v4.main()


if __name__ == "__main__":
    main()
