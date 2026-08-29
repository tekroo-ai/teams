#!/usr/bin/env python3
"""Create the T0 product-truth pack from frozen OpenHands and final-P2 SMA."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import uuid
from pathlib import Path
from typing import Any


TEAMS = Path("/Users/paul/work/tekroo-ai/teams-step15-s1-p2final-r1")
SMA = Path("/Users/paul/work/tekroo-ai/sma-step15-s1-p2final")
OPENHANDS = Path(
    "/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel"
)
T0 = TEAMS / "investigations/sma-q1/layered/sma-s2-final-p2-closure/t0"
OH_PYTHON = OPENHANDS / ".venv/bin/python"
SMA_JAR = SMA / "target/sma-1.0-SNAPSHOT.jar"
SMA_ORIGINAL_JAR = SMA / "target/original-sma-1.0-SNAPSHOT.jar"

EXPECTED_SMA_COMMIT = "fe9903cdf59497aabf12cbe7722e1397a1372068"
EXPECTED_SMA_TREE = "fc2ae06c6333e94f6db34d691e50cb8bb1e5aa36"
EXPECTED_SMA_JAR_SHA256 = (
    "1561d18739cc53e744a0014ce562c8a7b2adfc988494a984728d680e887fbb8b"
)
EXPECTED_OPENHANDS_COMMIT = "a338ba9b6cbb529886b755a335bae3dee0004700"
EXPECTED_OPENHANDS_TREE = "9e72f1b4ee0b857025d9f170600b9dce539b169a"

BOUND_TEAM_ARTIFACTS = {
    "acceptedS1Receipt": (
        "investigations/sma-q1/layered/"
        "sma-s1-p2final-r1-scientific-adjudication-acceptance.json",
        "3315838d0c5047189494cece49b953481431321d16e238efa338712be076bee4",
    ),
    "acceptedS2Preregistration": (
        "investigations/sma-q1/layered/sma-s2-preregistration-candidate-2.json",
        "b7315835b6291d44d3d8cb918dbec1f16c1e64f2106fd70212c6a8f7ec678188",
    ),
    "acceptedS2Corpus": (
        "investigations/sma-q1/layered/sma-s2-measured-corpus-candidate-1.json",
        "c273c148a7b289f6650a26a82c38bda9680904701120e083543e9b7954b9a960",
    ),
    "acceptedS2OracleMatrix": (
        "investigations/sma-q1/layered/sma-s2-predicate-oracle-matrix-candidate-4.json",
        "806e12a59dd8dcbc8f248d0a8d486c1afb856fc310e1952f7855e9fdf955d4f4",
    ),
    "definitiveClosurePlan": (
        "docs/architecture/100-step-15-sma-s2-definitive-first-pass-closure-plan.md",
        "68d44b1cdef272b46b9e984a2598d2e6bbce2e7758c6f844d5697c70d51964ae",
    ),
}

OPENHANDS_SOURCES = [
    "openhands-agent-server/openhands/agent_server/api.py",
    "openhands-agent-server/openhands/agent_server/conversation_router.py",
    "openhands-agent-server/openhands/agent_server/event_router.py",
    "openhands-agent-server/openhands/agent_server/conversation_service.py",
    "openhands-agent-server/openhands/agent_server/event_service.py",
    "openhands-agent-server/openhands/agent_server/models.py",
    "openhands-agent-server/openhands/agent_server/dependencies.py",
    "openhands-agent-server/openhands/agent_server/hooks_router.py",
    "openhands-agent-server/openhands/agent_server/hooks_service.py",
    "openhands-agent-server/openhands/agent_server/settings_router.py",
    "openhands-agent-server/openhands/agent_server/server_details_router.py",
    "openhands-sdk/openhands/sdk/conversation/request.py",
    "openhands-sdk/openhands/sdk/conversation/state.py",
    "openhands-sdk/openhands/sdk/conversation/impl/local_conversation.py",
    "openhands-sdk/openhands/sdk/agent/response_dispatch.py",
    "openhands-sdk/openhands/sdk/event/base.py",
    "openhands-sdk/openhands/sdk/event/types.py",
    "openhands-sdk/openhands/sdk/event/hook_execution.py",
    "openhands-sdk/openhands/sdk/event/llm_convertible/message.py",
    "openhands-sdk/openhands/sdk/event/llm_convertible/action.py",
    "openhands-sdk/openhands/sdk/event/llm_convertible/observation.py",
    "openhands-sdk/openhands/sdk/event/acp_tool_call.py",
    "openhands-sdk/openhands/sdk/event/token.py",
    "openhands-sdk/openhands/sdk/event/condenser.py",
    "openhands-sdk/openhands/sdk/event/conversation_state.py",
    "openhands-sdk/openhands/sdk/hooks/config.py",
    "openhands-sdk/openhands/sdk/hooks/executor.py",
    "openhands-sdk/openhands/sdk/hooks/manager.py",
    "openhands-sdk/openhands/sdk/hooks/conversation_hooks.py",
    "openhands-sdk/openhands/sdk/hooks/types.py",
    "openhands-sdk/openhands/sdk/llm/message.py",
    "openhands-sdk/openhands/sdk/tool/schema.py",
    "openhands-sdk/openhands/sdk/tool/tool.py",
    "openhands-sdk/openhands/sdk/utils/models.py",
]

SMA_SOURCES = [
    "src/main/java/ai/tekroo/sma/openhands/OpenHandsBridgeServer.java",
    "src/main/java/ai/tekroo/sma/openhands/OpenHandsHttpClientFactory.java",
    "src/main/java/ai/tekroo/sma/openhands/OpenHandsEventIntakeService.java",
    "src/main/java/ai/tekroo/sma/mongo/MemoryDocumentMapper.java",
    "src/main/java/ai/tekroo/sma/mongo/RetrievalEventDocumentMapper.java",
    "src/main/java/ai/tekroo/sma/mongo/MongoRepositories.java",
    "src/main/java/ai/tekroo/sma/mongo/MongoBootstrap.java",
    "src/main/java/ai/tekroo/sma/mongo/MongoMemoryRepository.java",
    "src/main/java/ai/tekroo/sma/mongo/MongoRetrievalEventRepository.java",
    "src/main/java/ai/tekroo/sma/mongo/MemoryUpdateBuilders.java",
    "src/main/java/ai/tekroo/sma/replay/ReplayWorker.java",
    "src/main/java/ai/tekroo/sma/vector/QdrantVectorStore.java",
    "src/main/java/ai/tekroo/sma/vector/VectorStorePort.java",
    "src/main/java/ai/tekroo/sma/retrieval/SmaRetrievalService.java",
    "src/main/java/ai/tekroo/sma/retrieval/RetrievalDisclosureRepository.java",
    "src/main/java/ai/tekroo/sma/config/SemanticMemoryConfig.java",
    "src/main/java/ai/tekroo/sma/config/CollectionNames.java",
    "src/main/java/ai/tekroo/sma/domain/Memory.java",
    "src/main/java/ai/tekroo/sma/domain/RetrievalEvent.java",
    "src/main/java/ai/tekroo/sma/runtime/RuntimeEnvironment.java",
    "src/main/java/ai/tekroo/sma/runtime/SmaServiceMain.java",
    ".openhands/hooks/sma_context_hook.py",
    ".openhands/hooks.json",
    "scripts/start-sma-service-with-key-file.sh",
    "launchagents/com.tekroo.sma-service.plist",
    "ops/launchd/com.tekroo.qdrant.plist",
    "ops/launchd/com.tekroo.qwen3-embeddings.plist",
]

TEAM_FIXTURE_SOURCES = [
    "scripts/sma_s2_deterministic_model_stub_candidate_2.py",
    "scripts/sma_s2_deterministic_model_stub_candidate_3.py",
    "scripts/sma_s2_bridge_fault_fixture_v2.py",
]

EXTERNAL_LAUNCH_BINDINGS = [
    Path("/Users/paul/Library/LaunchAgents/com.tekroo.openhands-agent-canvas.plist"),
    Path("/Users/paul/Library/LaunchAgents/com.tekroo.sma-retrieval-bridge.plist"),
    Path("/Users/paul/Library/LaunchAgents/com.tekroo.qdrant.plist"),
    Path("/Users/paul/Library/LaunchAgents/com.tekroo.mlx-nomic-embeddings.plist"),
    Path("/Users/paul/.local/bin/agent-canvas"),
    OPENHANDS / ".venv/bin/agent-server",
]


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def run(command: list[str], *, cwd: Path, env: dict[str, str] | None = None) -> str:
    try:
        completed = subprocess.run(
            command,
            cwd=cwd,
            env=env,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    except subprocess.CalledProcessError as error:
        raise RuntimeError(
            f"command failed: {command!r}\nstdout:\n{error.stdout}\nstderr:\n{error.stderr}"
        ) from error
    return completed.stdout


def git(repo: Path, *args: str) -> str:
    return run(["git", *args], cwd=repo).strip()


def source_bindings(repo: Path, paths: list[str]) -> list[dict[str, str]]:
    result: list[dict[str, str]] = []
    for relative in paths:
        path = repo / relative
        if not path.is_file():
            raise RuntimeError(f"missing bound source: {path}")
        working_blob = git(repo, "hash-object", relative)
        head_blob = git(repo, "rev-parse", f"HEAD:{relative}")
        if working_blob != head_blob:
            raise RuntimeError(f"bound source differs from HEAD: {relative}")
        result.append(
            {
                "path": relative,
                "sha256": sha256(path),
                "gitBlob": working_blob,
            }
        )
    return result


def exact_line(path: Path, needle: str) -> dict[str, Any]:
    lines = path.read_text(encoding="utf-8").splitlines()
    matches = [
        {"line": index, "text": line.strip()}
        for index, line in enumerate(lines, start=1)
        if needle in line
    ]
    if not matches:
        raise RuntimeError(f"missing producer/source proof {needle!r} in {path}")
    return {
        "path": str(path),
        "needle": needle,
        "matches": matches,
    }


def canonical_write(path: Path, value: Any) -> None:
    path.write_text(
        json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )


def artifact_binding(path: Path) -> dict[str, Any]:
    if not path.is_file():
        raise RuntimeError(f"missing bound artifact: {path}")
    return {"path": str(path), "sha256": sha256(path), "bytes": path.stat().st_size}


def pack_binding(path: Path, relative: str) -> dict[str, Any]:
    if not path.is_file():
        raise RuntimeError(f"missing pack artifact: {path}")
    return {"path": relative, "sha256": sha256(path), "bytes": path.stat().st_size}


def generate(staging: Path) -> list[dict[str, Any]]:
    commands: list[dict[str, Any]] = []
    openhands_output = staging / "openhands-product-truth.json"
    sma_output = staging / "sma-final-p2-product-truth.json"
    hook_output = staging / "sma-hook-product-truth.json"
    hook_command_receipt = staging / "sma-hook-command-receipt.json"
    hook_intake_output = staging / "sma-hook-intake-product-truth.json"

    oh_env = dict(os.environ)
    oh_env["OPENHANDS_SUPPRESS_BANNER"] = "1"
    oh_env["LITELLM_LOCAL_MODEL_COST_MAP"] = "True"
    command = [
        str(OH_PYTHON),
        str(T0 / "extract_openhands_product_truth.py"),
        "--output",
        str(openhands_output),
    ]
    run(command, cwd=OPENHANDS, env=oh_env)
    commands.append(
        {
            "name": "openhandsExtractor",
            "networkIntent": "NONE",
            "litellmCostMap": "LOCAL_ONLY_ENVIRONMENT_REQUESTED",
        }
    )

    probe_project = T0 / "java-product-probe"
    run(["mvn", "-o", "-q", "compile"], cwd=probe_project)
    commands.append({"name": "javaProbeCompile", "mavenOffline": True})
    classpath = f"target/classes:{SMA_JAR}"
    command = [
        "java",
        "-cp",
        classpath,
        "ai.tekroo.sma.openhands.FinalP2ProductTruthProbe",
        str(openhands_output),
        str(sma_output),
    ]
    run(command, cwd=probe_project)
    commands.append({"name": "finalP2JavaProbe", "serviceStart": False})

    command = [
        str(OH_PYTHON),
        str(T0 / "probe_openhands_hook_path.py"),
        "--adapter",
        str(T0 / "probe_bound_hook.py"),
        "--hook",
        str(SMA / ".openhands/hooks/sma_context_hook.py"),
        "--sma-truth",
        str(sma_output),
        "--output",
        str(hook_output),
        "--command-receipt",
        str(hook_command_receipt),
    ]
    run(command, cwd=OPENHANDS, env=oh_env)
    commands.append(
        {
            "name": "boundHookOpenHandsProductPath",
            "productComponents": [
                "HookManager",
                "HookExecutor",
                "HookEventProcessor",
                "HookExecutionEvent",
                "EventPage",
            ],
            "transport": "IN_PROCESS_BRIDGE_RESPONSE_NO_NETWORK",
        }
    )
    command = [
        "java",
        "-cp",
        classpath,
        "ai.tekroo.sma.openhands.FinalP2ProductTruthProbe",
        "--classify-hook",
        str(hook_output),
        str(hook_intake_output),
    ]
    run(command, cwd=probe_project)
    commands.append(
        {
            "name": "finalP2HookIntakeDiscriminator",
            "serviceStart": False,
            "input": "PRODUCT_GENERATED_HOOK_EXECUTION_EVENT",
        }
    )
    return commands


def verify_generated(staging: Path) -> list[dict[str, Any]]:
    oh = json.loads((staging / "openhands-product-truth.json").read_text())
    sma = json.loads((staging / "sma-final-p2-product-truth.json").read_text())
    hook = json.loads((staging / "sma-hook-product-truth.json").read_text())
    hook_intake = json.loads(
        (staging / "sma-hook-intake-product-truth.json").read_text()
    )
    checks: list[dict[str, Any]] = []

    def passed(name: str, condition: bool, evidence: Any) -> None:
        if not condition:
            raise RuntimeError(f"product-truth check failed: {name}: {evidence}")
        checks.append({"name": name, "status": "PASS", "evidence": evidence})

    def bson_int(value: Any) -> int:
        if isinstance(value, int):
            return value
        if isinstance(value, dict) and "$numberInt" in value:
            return int(value["$numberInt"])
        raise RuntimeError(f"not a BSON integer: {value!r}")

    passed("openhands-event-count", oh["eventCount"] == 15, oh["eventCount"])
    events = {record["label"]: record["event"] for record in oh["events"]}
    passed(
        "openhands-message-metadata",
        events["eligible_user_task"]["authorship_origin"] == "conversation_input"
        and events["eligible_delegated_task"]["authorship_origin"] == "delegated_agent"
        and events["eligible_final_agent_response"]["authorship_origin"] == "agent_model"
        and events["eligible_final_agent_response"]["semantic_purpose"] == "agent_response"
        and events["eligible_final_agent_response"]["agent_response_finality"] == "final",
        {
            label: {
                key: events[label][key]
                for key in (
                    "kind",
                    "source",
                    "authorship_origin",
                    "semantic_purpose",
                    "agent_response_finality",
                )
            }
            for label in (
                "eligible_user_task",
                "eligible_delegated_task",
                "eligible_final_agent_response",
            )
        },
    )

    decisions = {item["label"]: item for item in sma["intake"]["decisions"]}
    captured = {label for label, item in decisions.items() if item["captured"]}
    expected_captured = {
        "eligible_user_task",
        "eligible_delegated_task",
        "eligible_final_agent_response",
    }
    passed("actual-intake-discriminator", captured == expected_captured, sorted(captured))

    raw_memory = sma["rawMemoryDocument"]
    provenance = raw_memory["origin"]["openhands_provenance"]
    join = sma["evidenceJoin"]
    passed(
        "actual-raw-mongo-nested-provenance-and-id",
        raw_memory["_id"] == join["mongoRawMemoryId"]
        and provenance["conversation_id"] == join["mongoConversationId"]
        and provenance["event_id"] == join["mongoEventId"]
        and provenance.get("sequence") == join.get("mongoSequence")
        and bson_int(raw_memory["event"]["sequence_position"])
        == join["memorySequencePosition"]
        == 0,
        {
            "_id": raw_memory["_id"],
            "conversation_id": provenance["conversation_id"],
            "event_id": provenance["event_id"],
            "sequence": provenance.get("sequence"),
            "event.sequence_position": raw_memory["event"]["sequence_position"],
        },
    )

    replay = sma["replayPersistenceContracts"]
    canonical_set = replay["canonicalUpdate"]["$set"]
    promotion_set = replay["promotionToConsistentUpdate"]["$set"]
    passed(
        "actual-replay-update-contract",
        canonical_set["embeddings.semantic_ref"] == join["qdrantSemanticPointId"]
        and "embeddings.episodic_ref" not in canonical_set
        and promotion_set["state"] == "consistent"
        and promotion_set["reasoning_eligible"] is True
        and replay["episodicReferencePersistence"]
        == "QDRANT_ONLY_RETURN_IGNORED_BY_REPLAY_WORKER"
        and replay["probeAuthoredPromotedMongoDocument"] is False,
        replay,
    )

    writes = sma["qdrantWrites"]
    payload_keys = [sorted(write["payload"]) for write in writes]
    passed(
        "actual-qdrant-payload",
        len(writes) == 2
        and all(keys == ["agent_id", "memory_id"] for keys in payload_keys)
        and all(write["payload"]["memory_id"] == raw_memory["_id"] for write in writes)
        and all(write["payload"]["agent_id"] == raw_memory["agent_id"] for write in writes)
        and "conversation_id" not in writes[0]["payload"]
        and "trace_id" not in writes[0]["payload"],
        {"payloadKeys": payload_keys, "writes": writes},
    )
    qdrant_ids = {write["collection"]: write["pointId"] for write in writes}
    expected_point_id = str(
        uuid.UUID(bytes=hashlib.md5(raw_memory["_id"].encode("utf-8")).digest(), version=3)
    )
    passed(
        "product-reachable-semantic-reference-and-qdrant-only-episodic",
        canonical_set["embeddings.semantic_ref"]
        == qdrant_ids["sma_s2_truth_semantic"]
        and join["qdrantEpisodicPointId"] == qdrant_ids["sma_s2_truth_episodic"]
        and qdrant_ids["sma_s2_truth_episodic"] == expected_point_id
        and join["mongoEpisodicReferenceExpected"] is False,
        {
            "mongoCanonicalUpdate": canonical_set,
            "qdrant": qdrant_ids,
            "expectedUuidNameFromMemoryId": expected_point_id,
        },
    )

    retrieval = sma["retrievalEventDocument"]
    retrieval_boundary = sma["retrievalFixtureBoundary"]
    passed(
        "retrieval-test-double-below-product-boundary",
        retrieval_boundary["memoryStore"]
        == "IN_MEMORY_TEST_DOUBLE_BELOW_ACTUAL_RETRIEVAL_SERVICE"
        and retrieval_boundary["eligibilityFieldsDerivedFromActualReplayUpdates"]
        is True
        and retrieval_boundary["serializedAsAuthoritativeMongoDocument"] is False
        and retrieval_boundary["authoritativeOutputs"]
        == "RETRIEVAL_EVENT_MAPPER_AND_BRIDGE_HANDLER_RESPONSE",
        retrieval_boundary,
    )
    passed(
        "retrieval-trace-memory-join",
        hook["parsedHookStdout"]["smaTraceId"]
        == retrieval["task_ref"]
        == join["bridgeTraceId"]
        and retrieval["results"][0]["memory_id"] == raw_memory["_id"],
        {
            "hook.smaTraceId": hook["parsedHookStdout"]["smaTraceId"],
            "retrieval.task_ref": retrieval["task_ref"],
            "retrieval.results[0].memory_id": retrieval["results"][0]["memory_id"],
            "memories._id": raw_memory["_id"],
        },
    )
    hook_event = hook["persistedHookExecutionEvent"]
    hook_command = hook["boundHookCommandReceipt"]
    hook_path_conditions = {
        "input": hook_event["hook_input"]
        == {"message": "Implement the product-bound fixture."},
        "kind": hook_event["kind"] == "HookExecutionEvent",
        "stdout": json.loads(hook_event["stdout"])
        == hook["parsedHookStdout"],
        "order": hook["emittedEventKinds"]
        == ["HookExecutionEvent", "MessageEvent"],
        "route": hook_command["bridgeRequest"]["url"].endswith(
            "/v1/openhands/context"
        ),
        "body": hook_command["bridgeRequest"]["body"]
        == {
            "session_id": "conv-product-truth",
            "working_dir": "/tmp/sma-s2-product-truth",
            "prompt": "Implement the product-bound fixture.",
        },
        "context": hook_event["additional_context"]
        == sma["bridgeResponse"]["context_block"],
    }
    passed(
        "actual-openhands-hook-product-path",
        all(hook_path_conditions.values()),
        {
            "conditions": hook_path_conditions,
            "persistedHookInput": hook_event["hook_input"],
            "request": hook_command["bridgeRequest"],
            "trace": hook["parsedHookStdout"]["smaTraceId"],
        },
    )
    lifecycle = sma["bridgeLifecycle"]
    passed(
        "actual-handler-without-service-start",
        lifecycle["serverStartCalled"] is False
        and lifecycle["handlerInvokedDirectly"] is True
        and lifecycle["acceptedNetworkConnections"] is False
        and lifecycle["fakeOpenHandsRequests"] == 1
        and lifecycle["responseStatus"] == 200
        and lifecycle["ownedExecutorsTerminated"] is True,
        lifecycle,
    )
    hook_intake_decision = hook_intake["intake"]["decisions"]
    passed(
        "actual-intake-excludes-product-generated-hook",
        hook_intake["intake"]["capturedEvents"] == 0
        and len(hook_intake_decision) == 1
        and hook_intake_decision[0]["label"] == "ineligible_hook"
        and hook_intake_decision[0]["eventKind"] == "HookExecutionEvent"
        and hook_intake_decision[0]["captured"] is False,
        hook_intake["intake"],
    )
    return checks


def surface_contract(staging: Path) -> dict[str, Any]:
    oh = json.loads((staging / "openhands-product-truth.json").read_text())
    sma = json.loads((staging / "sma-final-p2-product-truth.json").read_text())
    request_fields = sorted(oh["schemas"]["SendMessageRequest"]["properties"])
    start_fields = sorted(oh["schemas"]["StartConversationRequest"]["properties"])
    qdrant_payload_fields = sorted(sma["qdrantWrites"][0]["payload"])
    return {
        "schemaVersion": "1.0.0-dev",
        "recordType": "SMA_S2_T0_PRODUCT_SURFACE_CONTRACT",
        "status": "PRODUCT_TRUTH_ONLY_NOT_LIVE_AUTHORITY",
        "openhands": {
            "authentication": {
                "header": "X-Session-API-Key",
                "appliesTo": "/api/*",
                "valueSource": "SEALED_SESSION_KEY_FILE_BOUND_BY_LATER_EXECUTION_IDENTITY",
                "cookieFallbackProhibitedForHarness": True,
            },
            "createConversation": {
                "method": "POST",
                "route": "/api/conversations",
                "requestModel": "StartConversationRequest",
                "productFields": start_fields,
            },
            "submitEvent": {
                "method": "POST",
                "route": "/api/conversations/{conversation_id}/events",
                "requestModel": "SendMessageRequest",
                "productFields": request_fields,
                "exactQualifiedExample": oh["requestExample"],
            },
            "searchEvents": {
                "method": "GET",
                "route": "/api/conversations/{conversation_id}/events/search",
                "responseModel": "EventPage",
            },
            "getConversation": {
                "method": "GET",
                "route": "/api/conversations/{conversation_id}",
                "responseModel": "ConversationInfo",
            },
            "deleteConversation": {
                "method": "DELETE",
                "route": "/api/conversations/{conversation_id}",
            },
            "interruptConversation": {
                "method": "POST",
                "route": "/api/conversations/{conversation_id}/interrupt",
            },
            "condenseConversation": {
                "method": "POST",
                "route": "/api/conversations/{conversation_id}/condense",
            },
            "settings": {
                "method": "GET",
                "route": "/api/settings",
            },
            "hooks": {
                "method": "POST",
                "route": "/api/hooks",
            },
            "health": {
                "method": "GET",
                "route": "/health",
            },
            "terminalStateSource": "ConversationInfo.execution_status",
            "eventKindSource": "EventPage.items[].kind",
            "eligibleMetadata": [
                {
                    "source": "user",
                    "authorship_origin": "conversation_input",
                    "semantic_purpose": "task_input",
                    "agent_response_finality": "not_applicable",
                },
                {
                    "source": "user",
                    "authorship_origin": "delegated_agent",
                    "semantic_purpose": "task_input",
                    "agent_response_finality": "not_applicable",
                },
                {
                    "source": "agent",
                    "authorship_origin": "agent_model",
                    "semantic_purpose": "agent_response",
                    "agent_response_finality": "final",
                },
            ],
        },
        "mongo": {
            "defaultDatabase": "sma",
            "databaseOverrideEnvironment": "SMA_MONGO_DATABASE",
            "s2Requirement": "EXACT_DISPOSABLE_DATABASE_BOUND_BY_LATER_EXECUTION_IDENTITY",
            "collections": {
                "memories": "memories",
                "retrievalEvents": "retrieval_events",
                "retrievalDisclosureEvents": "retrieval_disclosure_events",
                "rawArtifacts": "raw_artifacts",
                "jobState": "job_state",
            },
            "memorySelector": {
                "origin.openhands_provenance.conversation_id": "{conversation_id}",
                "origin.openhands_provenance.event_id": "{event_id}",
            },
            "memoryRequiredProjection": [
                "_id",
                "agent_id",
                "origin.source_ref",
                "origin.openhands_provenance.conversation_id",
                "origin.openhands_provenance.parent_conversation_id",
                "origin.openhands_provenance.event_id",
                "origin.openhands_provenance.workspace",
                "origin.openhands_provenance.profile",
                "origin.openhands_provenance.event_role",
                "origin.openhands_provenance.sequence",
                "origin.openhands_provenance.model",
                "origin.openhands_provenance.authorship_origin",
                "origin.openhands_provenance.semantic_purpose",
                "origin.openhands_provenance.agent_response_finality",
                "canonical.text",
                "event.sequence_position",
                "embeddings.semantic_ref",
            ],
            "retrievalSelector": {"task_ref": "{hook.smaTraceId}"},
            "sequenceEvidence": {
                "authoritativeIntakeOrder": "event.sequence_position",
                "optionalUpstreamField": "origin.openhands_provenance.sequence",
                "rule": "DO_NOT_REQUIRE_OPTIONAL_UPSTREAM_SEQUENCE_WHEN_ENVELOPE_OMITS_IT",
            },
            "retrievalRequiredProjection": [
                "_id",
                "agent_id",
                "task_ref",
                "retrieval_mode",
                "results.memory_id",
                "results.role",
                "results.rank",
                "results.similarity_score",
            ],
            "forbiddenTopLevelMemoryAssumptions": [
                "conversation_id",
                "event_id",
                "memory_id",
                "trace_id",
            ],
        },
        "qdrant": {
            "semanticLookup": "POINT_ID_FROM_MONGO_EMBEDDINGS_SEMANTIC_REF",
            "episodicLookup": (
                "POINT_ID_IS_UUID_NAME_FROM_MEMORY_ID; "
                "REPLAY_WORKER_DOES_NOT_PERSIST_EPISODIC_REF"
            ),
            "requiredPayloadFields": qdrant_payload_fields,
            "partitionCheck": "payload.agent_id == memories.agent_id",
            "memoryCheck": "payload.memory_id == memories._id",
            "forbiddenPayloadAssumptions": ["conversation_id", "event_id", "trace_id"],
        },
        "bridge": {
            "method": "POST",
            "route": "/v1/openhands/context",
            "requestFields": ["session_id", "working_dir", "prompt"],
            "traceField": "trace_id",
            "hookTraceField": "smaTraceId",
            "contextField": "context_block",
            "health": {"method": "GET", "route": "/healthz"},
        },
        "evidenceJoin": [
            "JSON.parse(HookExecutionEvent.stdout).smaTraceId",
            "retrieval_events.task_ref",
            "retrieval_events.results[].memory_id",
            "memories._id",
            "memories.origin.openhands_provenance.*",
            "memories.embeddings.semantic_ref",
            "Qdrant.point_id",
            "Qdrant.payload.{memory_id,agent_id}",
            "episodic Qdrant point_id == UUID.nameUUIDFromBytes(memories._id UTF-8) "
            "(no Mongo episodic_ref)",
        ],
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output-root", required=True, type=Path)
    args = parser.parse_args()
    output_root = args.output_root.resolve()
    if output_root.exists():
        raise RuntimeError(f"refusing to overwrite product-truth pack: {output_root}")
    output_root.parent.mkdir(parents=True, exist_ok=True)

    if git(SMA, "rev-parse", "HEAD") != EXPECTED_SMA_COMMIT:
        raise RuntimeError("final-P2 SMA commit drift")
    if git(SMA, "rev-parse", "HEAD^{tree}") != EXPECTED_SMA_TREE:
        raise RuntimeError("final-P2 SMA tree drift")
    if git(OPENHANDS, "rev-parse", "HEAD") != EXPECTED_OPENHANDS_COMMIT:
        raise RuntimeError("OpenHands commit drift")
    if git(OPENHANDS, "rev-parse", "HEAD^{tree}") != EXPECTED_OPENHANDS_TREE:
        raise RuntimeError("OpenHands tree drift")
    if sha256(SMA_JAR) != EXPECTED_SMA_JAR_SHA256:
        raise RuntimeError("final-P2 SMA runtime JAR drift")

    with tempfile.TemporaryDirectory(
        prefix="sma-s2-t0-", dir=output_root.parent
    ) as temporary:
        staging = Path(temporary)
        commands = generate(staging)
        checks = verify_generated(staging)

        team_bindings: dict[str, Any] = {}
        for name, (relative, expected) in BOUND_TEAM_ARTIFACTS.items():
            path = TEAMS / relative
            actual = sha256(path)
            if actual != expected:
                raise RuntimeError(f"bound Teams artifact drift: {name}")
            team_bindings[name] = {
                "path": relative,
                "sha256": actual,
            }

        source_receipt = {
            "schemaVersion": "1.0.0-dev",
            "recordType": "SMA_S2_T0_PRODUCT_SOURCE_BINDINGS",
            "openhands": {
                "root": str(OPENHANDS),
                "commit": EXPECTED_OPENHANDS_COMMIT,
                "tree": EXPECTED_OPENHANDS_TREE,
                "sources": source_bindings(OPENHANDS, OPENHANDS_SOURCES),
            },
            "sma": {
                "root": str(SMA),
                "commit": EXPECTED_SMA_COMMIT,
                "tree": EXPECTED_SMA_TREE,
                "sources": source_bindings(SMA, SMA_SOURCES),
                "runtimeJar": artifact_binding(SMA_JAR),
                "originalRuntimeJar": artifact_binding(SMA_ORIGINAL_JAR),
            },
            "teams": {
                "acceptedArtifacts": team_bindings,
                "fixtureSources": [
                    artifact_binding(TEAMS / relative)
                    for relative in TEAM_FIXTURE_SOURCES
                ],
                "extractors": [
                    artifact_binding(T0 / "extract_openhands_product_truth.py"),
                    artifact_binding(T0 / "probe_bound_hook.py"),
                    artifact_binding(T0 / "probe_openhands_hook_path.py"),
                    artifact_binding(T0 / "java-product-probe/pom.xml"),
                    artifact_binding(
                        T0
                        / "java-product-probe/src/main/java/ai/tekroo/sma/openhands/"
                        "FinalP2ProductTruthProbe.java"
                    ),
                    artifact_binding(T0 / "qualify_product_truth.py"),
                ],
            },
            "launchBindings": [
                artifact_binding(path) for path in EXTERNAL_LAUNCH_BINDINGS
            ],
            "launchBindingStatus": {
                "externalFiles": "OBSERVED_CURRENT_ENVIRONMENT_ONLY_NOT_FINAL_P2_EXECUTION_AUTHORITY",
                "repositoryLaunchFiles": "BOUND_FINAL_P2_PRODUCT_DEFINITIONS_NOT_STARTED",
                "t1Requirement": "PACKAGE_SEALED_DISPOSABLE_LAUNCH_DEFINITION_BEFORE_H0",
            },
        }
        canonical_write(staging / "source-bindings.json", source_receipt)

        producer_proofs = {
            "schemaVersion": "1.0.0-dev",
            "recordType": "SMA_S2_T0_PRODUCT_PRODUCER_PROOFS",
            "proofs": [
                exact_line(
                    OPENHANDS
                    / "openhands-agent-server/openhands/agent_server/conversation_service.py",
                    "AuthorshipOrigin.CONVERSATION_INPUT",
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-sdk/openhands/sdk/conversation/impl/local_conversation.py",
                    "AuthorshipOrigin.CONVERSATION_INPUT",
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-sdk/openhands/sdk/agent/response_dispatch.py",
                    "AuthorshipOrigin.AGENT_MODEL",
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-sdk/openhands/sdk/agent/response_dispatch.py",
                    "AgentResponseFinality.FINAL",
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-sdk/openhands/sdk/hooks/conversation_hooks.py",
                    'hook_input={"message": message}',
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-sdk/openhands/sdk/hooks/conversation_hooks.py",
                    "event = HookExecutionEvent(",
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-agent-server/openhands/agent_server/conversation_router.py",
                    '"/{conversation_id}", responses={404:',
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-agent-server/openhands/agent_server/conversation_router.py",
                    '"/{conversation_id}/interrupt",',
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-agent-server/openhands/agent_server/conversation_router.py",
                    '"/{conversation_id}/condense",',
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-agent-server/openhands/agent_server/hooks_router.py",
                    '@hooks_router.post("", response_model=HooksResponse)',
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-agent-server/openhands/agent_server/settings_router.py",
                    "SETTINGS_PATH = \"\"",
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-agent-server/openhands/agent_server/server_details_router.py",
                    '@server_details_router.get("/health")',
                ),
                exact_line(
                    OPENHANDS
                    / "openhands-agent-server/openhands/agent_server/dependencies.py",
                    'APIKeyHeader(name="X-Session-API-Key"',
                ),
                exact_line(
                    SMA
                    / "src/main/java/ai/tekroo/sma/openhands/OpenHandsBridgeServer.java",
                    'response.addProperty("trace_id", traceId)',
                ),
                exact_line(
                    SMA / "src/main/java/ai/tekroo/sma/replay/ReplayWorker.java",
                    "vectorStorePort.upsert(VectorCollection.EPISODIC",
                ),
                exact_line(
                    SMA / "src/main/java/ai/tekroo/sma/replay/ReplayWorker.java",
                    "memoryRepository.updateCanonical",
                ),
                exact_line(
                    SMA / "src/main/java/ai/tekroo/sma/mongo/MemoryUpdateBuilders.java",
                    'Updates.set("embeddings.semantic_ref"',
                ),
                exact_line(
                    SMA
                    / "src/main/java/ai/tekroo/sma/mongo/MemoryDocumentMapper.java",
                    '"openhands_provenance"',
                ),
                exact_line(
                    SMA
                    / "src/main/java/ai/tekroo/sma/mongo/RetrievalEventDocumentMapper.java",
                    '"task_ref"',
                ),
                exact_line(
                    SMA / "src/main/java/ai/tekroo/sma/vector/QdrantVectorStore.java",
                    'putPayload("memory_id"',
                ),
                exact_line(
                    SMA / ".openhands/hooks/sma_context_hook.py",
                    'result["smaTraceId"] = trace_id',
                ),
            ],
        }
        canonical_write(staging / "producer-proofs.json", producer_proofs)
        canonical_write(
            staging / "product-surface-contract.json",
            surface_contract(staging),
        )

        generated_names = [
            "openhands-product-truth.json",
            "sma-final-p2-product-truth.json",
            "sma-hook-product-truth.json",
            "sma-hook-command-receipt.json",
            "sma-hook-intake-product-truth.json",
            "source-bindings.json",
            "producer-proofs.json",
            "product-surface-contract.json",
        ]
        manifest = {
            "schemaVersion": "1.0.0-dev",
            "recordType": "SMA_S2_T0_PRODUCT_TRUTH_MANIFEST",
            "status": "READY_FOR_INDEPENDENT_T0_SOURCE_REVIEW",
            "candidateIdentityPublished": False,
            "h0QualificationRun": False,
            "artifacts": {
                name: pack_binding(staging / name, name) for name in generated_names
            },
        }
        canonical_write(staging / "product-truth-manifest.json", manifest)

        receipt = {
            "schemaVersion": "1.0.0-dev",
            "recordType": "SMA_S2_T0_PRODUCT_TRUTH_QUALIFICATION",
            "status": "PASS_AWAITING_INDEPENDENT_T0_SOURCE_REVIEW",
            "checks": checks,
            "checkCount": len(checks),
            "commands": commands,
            "manifest": pack_binding(
                staging / "product-truth-manifest.json",
                "product-truth-manifest.json",
            ),
            "prohibitedActivity": {
                "serviceStart": "NOT_RUN",
                "openhandsConversation": "NOT_RUN",
                "livePreflight": "NOT_RUN",
                "liveDress": "NOT_RUN",
                "measuredExecution": "NOT_RUN",
                "modelCall": "NOT_RUN",
                "productionOrHistoricalDataAccess": "NOT_RUN",
                "commit": "NOT_RUN",
                "push": "NOT_RUN",
            },
        }
        canonical_write(staging / "qualification-receipt.json", receipt)
        shutil.move(str(staging), str(output_root))

    print(
        json.dumps(
            {
                "status": "PASS_AWAITING_INDEPENDENT_T0_SOURCE_REVIEW",
                "outputRoot": str(output_root),
                "receiptSha256": sha256(output_root / "qualification-receipt.json"),
                "manifestSha256": sha256(output_root / "product-truth-manifest.json"),
            },
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    main()
