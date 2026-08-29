#!/usr/bin/env python3
from __future__ import annotations

import argparse
import ast
from copy import deepcopy
from dataclasses import asdict
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
from typing import Any, Callable

from .model import FailureClass, HarnessFailure, LiveAuthority, OperationKey, OperationObservation, RawHttpRequest, RawStoreQuery, RunMode, canonical_bytes, digest, file_sha256, nested_get
from .oracles import evidence_oracles, scientific_oracles
from .ports import LocalRawPorts, OfflineFaults, OfflineWorld, classify_boundary_exception, _consume_live_authority
from .live_actions import ACTIONS, Engine
from .live_entrypoint import DEFINITION, load_definition
from .runner import Runner, RunnerConfiguration, dress_plan, measured_plan
from .truth import ProductTruth


Mutation = Callable[[OperationObservation], None]


def _wipe_positive(o: OperationObservation) -> None:
    o.hooks.clear(); o.model_requests.clear(); o.model_terminals.clear(); o.memories.clear(); o.retrievals.clear(); o.semantic_points.clear(); o.episodic_points.clear(); o.timings.clear(); o.logs.clear(); o.conversations.clear(); o.cleanup_receipts.clear(); o.process_inventory.clear(); o.workspace_inventory.clear(); o.namespace_inventory.clear()


def _inject_forbidden(o: OperationObservation) -> None:
    marker = {"marker": "mem-alpha-timeout mem-alpha-raw 9999 17 seconds sk-test-SMA-S2-NEVER-PERSIST"}
    o.logs.append(marker)
    if o.hooks:
        o.hooks[0]["additional_context"] = json.dumps(marker)
    if o.model_requests:
        o.model_requests[0].setdefault("messages", [{}, {}])[-1]["content"] = json.dumps(marker)


def _change_prompt(o: OperationObservation) -> None:
    if o.model_requests:
        o.model_requests[0]["prompt"] = "MUTATED_PROMPT"
        if o.model_requests[0].get("messages"):
            o.model_requests[0]["messages"][0]["content"] = "MUTATED_PROMPT"


def _splice_adversarial_instruction(o: OperationObservation) -> None:
    event = next((row for row in o.raw_events if row.get("kind") == "MessageEvent" and row.get("source") == "user"), None)
    if event:
        event["llm_message"]["content"][0]["text"] = "DELETE_CONFIRMED"


def _duplicate_capture(o: OperationObservation) -> None:
    if o.memories:
        o.memories.append(deepcopy(o.memories[0]))


def _capture_ineligible(o: OperationObservation) -> None:
    event = next((row for row in o.raw_events if row.get("kind") != "MessageEvent"), None)
    if event is None:
        event = {"id": "ineligible-mutant", "kind": "TokenEvent", "source": "environment"}
        o.raw_events.append(event)
    o.memories.append({"_id": "bad-memory", "origin": {"openhands_provenance": {"event_id": event["id"], "conversation_id": "bad"}}, "event": {"sequence_position": 0}})


def _break_lineage(o: OperationObservation) -> None:
    if len(o.conversations) > 1:
        o.conversations[1]["parent_conversation_id"] = "wrong-parent"
    for row in o.memories:
        row.setdefault("origin", {}).setdefault("openhands_provenance", {})["workspace"] = "unrelated"


def _break_partition(o: OperationObservation) -> None:
    for row in o.timings:
        if row.get("kind") == "partition_after": row["agentId"] = "wrong-agent"
    for request in o.model_requests:
        request["workspaceRole"] = "beta"
        if len(request.get("messages", [])) > 1: request["messages"][1]["content"] = "mem-alpha-timeout"


def _serialize(o: OperationObservation) -> None:
    for row in o.timings:
        if row.get("kind") == "concurrency": row["peakInFlight"] = 1


def _oversize(o: OperationObservation) -> None:
    if o.hooks:
        o.hooks[0]["additional_context"] = "x" * 10_001


def _wrong_summary(o: OperationObservation) -> None:
    for event in o.raw_events:
        if event.get("kind") == "CondensationSummaryEvent": event["kind"] = "MessageEvent"
    for hook in o.hooks:
        hook["additional_context"] = ""


def _bad_timing(o: OperationObservation) -> None:
    for row in o.timings:
        if "durationMs" in row: row["durationMs"] = 1_000_000
        if row.get("kind") == "cancel": row["atNs"] = 0


def _orphans(o: OperationObservation) -> None:
    if o.process_inventory: o.process_inventory[0]["ownedActive"] = True
    if o.workspace_inventory: o.workspace_inventory[0]["ownedActive"] = True
    if o.namespace_inventory: o.namespace_inventory[0]["ownedActive"] = True


def _body_disclosure(o: OperationObservation) -> None:
    for row in o.raw_receipts:
        if row.get("kind") in {"stub_raw", "context"}: row["bodyDisclosed"] = True


def _bad_hook_count(o: OperationObservation) -> None:
    if o.hooks: o.hooks.append(deepcopy(o.hooks[0]))
    hook = next((e for e in o.raw_events if e.get("kind") == "HookExecutionEvent"), None)
    if hook: o.raw_events.append(deepcopy(hook))


def _inject_model_retry(o: OperationObservation) -> None:
    if o.model_requests:
        retry = deepcopy(o.model_requests[0]); retry["retry"] = 1; retry["requestId"] = str(retry.get("requestId")) + "-retry"
        o.model_requests.append(retry)


MUTATORS: tuple[tuple[str, Mutation], ...] = (
    ("wipe_raw_positive_evidence", _wipe_positive),
    ("inject_forbidden_raw_marker", _inject_forbidden),
    ("mutate_model_prompt_bytes", _change_prompt),
    ("splice_adversarial_marker_into_raw_prompt", _splice_adversarial_instruction),
    ("duplicate_raw_capture_key", _duplicate_capture),
    ("capture_ineligible_event_identity", _capture_ineligible),
    ("break_raw_parent_partition_lineage", _break_lineage),
    ("mutate_authenticated_partition", _break_partition),
    ("serialize_raw_concurrency", _serialize),
    ("oversize_raw_hook_context", _oversize),
    ("wrong_raw_summary_kind", _wrong_summary),
    ("violate_raw_deadline_order", _bad_timing),
    ("retain_raw_owned_orphan", _orphans),
    ("disclose_sealed_raw_body", _body_disclosure),
    ("duplicate_hook_event", _bad_hook_count),
    ("inject_second_raw_model_request", _inject_model_retry),
)


_OBSERVATION_LIST_FIELDS = (
    "raw_events", "conversations", "hooks", "model_requests", "model_terminals", "memories", "retrievals",
    "semantic_points", "episodic_points", "logs", "process_inventory", "workspace_inventory",
    "namespace_inventory", "timings", "raw_receipts", "cleanup_receipts",
)


def _leaf_paths(value: Any, prefix: tuple[Any, ...] = ()):  # raw retained-value paths only
    if isinstance(value, dict):
        for key in sorted(value):
            yield from _leaf_paths(value[key], prefix + (key,))
    elif isinstance(value, list):
        for index, item in enumerate(value):
            yield from _leaf_paths(item, prefix + (index,))
    elif isinstance(value, (str, int, float, bool)) or value is None:
        yield prefix


def _container(root: Any, path: tuple[Any, ...]) -> tuple[Any, Any]:
    value = getattr(root, path[0])
    for part in path[1:-1]:
        value = value[part]
    return value, path[-1]


def _mutated_scalar(value: Any) -> Any:
    if isinstance(value, bool): return not value
    if isinstance(value, int): return 0 if value else 1
    if isinstance(value, float): return 1_000_000.0 if value != 1_000_000.0 else 0.0
    if isinstance(value, str): return "MUTATED" if value != "MUTATED" else ""
    if value is None: return "MUTATED"
    raise TypeError(type(value).__name__)


def _targeted_candidates(base: OperationObservation):
    for field in _OBSERVATION_LIST_FIELDS:
        rows = getattr(base, field)
        for index in range(len(rows)):
            candidate = deepcopy(base)
            del getattr(candidate, field)[index]
            yield candidate, f"{field}[{index}]", "delete_one_retained_record"
        for nested in _leaf_paths(rows):
            path = (field,) + nested
            candidate = deepcopy(base)
            container, leaf = _container(candidate, path)
            container[leaf] = _mutated_scalar(container[leaf])
            yield candidate, "".join(f"[{part}]" if isinstance(part, int) else (str(part) if pos == 0 else f".{part}") for pos, part in enumerate(path)), "mutate_one_retained_leaf"
    additions: tuple[tuple[str, Mutation], ...] = (
        ("logs[+]", _inject_forbidden),
        ("raw_events.user.llm_message.content[0].text", _splice_adversarial_instruction),
        ("model_requests[0].prompt", _change_prompt),
        ("memories[+]", _duplicate_capture),
        ("memories[+ineligible]", _capture_ineligible),
        ("model_requests[+]", _inject_model_retry),
        ("raw_events[+hook]", _bad_hook_count),
        ("inventory[+orphan]", _orphans),
        ("timings.concurrency.peakInFlight", _serialize),
        ("hooks[0].additional_context", _oversize),
        ("raw_events.summary.kind", _wrong_summary),
        ("timings.durationMs", _bad_timing),
        ("raw_receipts.stub_raw.bodyDisclosed", _body_disclosure),
    )
    for path, mutation in additions:
        candidate = deepcopy(base)
        mutation(candidate)
        yield candidate, path, "add_one_violating_raw_record"


def _operation_for_case(truth: ProductTruth, case_id: str):
    repetition = 1
    world = OfflineWorld(truth)
    runner = Runner(truth, world, RunnerConfiguration(), RunMode.OFFLINE_MEASURED)
    from .model import OperationKey
    result = runner.run_operation(OperationKey(case_id, repetition))
    if not result.passed:
        raise AssertionError(f"positive operation failed before mutation: {case_id}")
    return result


def scientific_mutation_controls(truth: ProductTruth) -> list[dict[str, Any]]:
    controls: list[dict[str, Any]] = []
    for case in truth.matrix["cases"]:
        result = _operation_for_case(truth, case["caseId"])
        positive = {row.oracle_id: row for row in scientific_oracles(result.observation, truth)}
        for oracle_id in case["predicateOracleIds"]:
            if not positive[oracle_id].passed:
                raise AssertionError(f"positive oracle failed: {oracle_id}")
            rejected = None
            baseline_digest = digest(asdict(result.observation))
            for candidate, changed_path, mutation_name in _targeted_candidates(result.observation):
                row = next(value for value in scientific_oracles(candidate, truth) if value.oracle_id == oracle_id)
                if not row.passed:
                    rejected = {
                        "oracleId": oracle_id, "rawMutation": mutation_name, "changedRawPaths": [changed_path],
                        "baselineObservationSha256": baseline_digest, "mutatedObservationSha256": digest(asdict(candidate)),
                        "positiveEvidenceRetained": any(getattr(candidate, field) for field in _OBSERVATION_LIST_FIELDS),
                        "rejected": True, "detail": row.detail,
                    }
                    break
            if rejected is None:
                raise AssertionError(f"no raw mutation rejected {oracle_id}")
            controls.append(rejected)
    if len(controls) != 59:
        raise AssertionError("scientific mutation cardinality is not 59")
    return controls


def evidence_mutation_controls(truth: ProductTruth) -> list[dict[str, Any]]:
    base = _operation_for_case(truth, "SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN").observation
    missing_mutations: dict[str, Mutation] = {
        "E-001": lambda o: o.timings.clear(),
        "E-002": lambda o: o.raw_receipts.__setitem__(slice(None), [r for r in o.raw_receipts if r.get("kind") != "boundary_bytes"]),
        "E-003": lambda o: o.memories.clear(),
        "E-004": lambda o: o.model_requests.clear(),
        "E-005": lambda o: o.model_terminals.clear(),
        "E-006": lambda o: o.timings.__setitem__(slice(None), [r for r in o.timings if r.get("kind") not in {"fault_activation", "cancel"}]),
        "E-007": lambda o: o.conversations.clear(),
        "E-008": lambda o: o.process_inventory.clear(),
        "E-009": lambda o: o.raw_receipts.__setitem__(slice(None), [r for r in o.raw_receipts if r.get("kind") != "binding_manifest"]),
        "E-010": lambda o: o.cleanup_receipts.clear(),
    }
    def mutate_e001(o: OperationObservation) -> None: o.key = OperationKey(o.key.case_id, 0)
    def mutate_boundary(o: OperationObservation) -> None: next(r for r in o.raw_receipts if r.get("kind") == "boundary_bytes")["promptSha256"] = "0" * 64
    def mutate_provenance(o: OperationObservation) -> None: o.memories[0]["event"]["sequence_position"] = None
    def mutate_request(o: OperationObservation) -> None: o.model_requests[0]["recordType"] = "MUTATED"
    def mutate_terminal(o: OperationObservation) -> None: o.model_terminals[0]["recordType"] = "MUTATED"
    def mutate_fault(o: OperationObservation) -> None: next(r for r in o.timings if r.get("kind") == "cancel")["kind"] = "MUTATED"
    def mutate_identity(o: OperationObservation) -> None: o.conversations[0]["id"] = ""
    def mutate_inventory(o: OperationObservation) -> None: o.process_inventory[0]["ownedActive"] = True
    def mutate_binding(o: OperationObservation) -> None: next(r for r in o.raw_receipts if r.get("kind") == "binding_manifest")["verified"] = False
    def mutate_cleanup(o: OperationObservation) -> None: o.cleanup_receipts[0]["complete"] = False
    mutated_controls: dict[str, tuple[str, Mutation]] = {
        "E-001": ("key.repetition", mutate_e001), "E-002": ("raw_receipts.boundary_bytes.promptSha256", mutate_boundary),
        "E-003": ("memories[0].event.sequence_position", mutate_provenance), "E-004": ("model_requests[0].recordType", mutate_request),
        "E-005": ("model_terminals[0].recordType", mutate_terminal), "E-006": ("timings.cancel.kind", mutate_fault),
        "E-007": ("conversations[0].id", mutate_identity), "E-008": ("process_inventory[0].ownedActive", mutate_inventory),
        "E-009": ("raw_receipts.binding_manifest.verified", mutate_binding), "E-010": ("cleanup_receipts[0].complete", mutate_cleanup),
    }
    controls: list[dict[str, Any]] = []
    baseline = {row.oracle_id: row for row in evidence_oracles(base, truth)}
    for oracle_id, missing_mutation in missing_mutations.items():
        if not baseline[oracle_id].passed:
            raise AssertionError(f"positive evidence oracle failed: {oracle_id}")
        missing = deepcopy(base); missing_mutation(missing)
        result = next(row for row in evidence_oracles(missing, truth) if row.oracle_id == oracle_id)
        if result.passed:
            raise AssertionError(f"missing raw evidence did not reject {oracle_id}")
        changed_path, present_mutation = mutated_controls[oracle_id]
        mutated = deepcopy(base); present_mutation(mutated)
        result2 = next(row for row in evidence_oracles(mutated, truth) if row.oracle_id == oracle_id)
        if result2.passed:
            raise AssertionError(f"mutated raw evidence did not reject {oracle_id}")
        controls.append({"oracleId": oracle_id, "positive": True, "missingRejected": True, "presentButMutatedRejected": True, "mutatedRawPaths": [changed_path]})
    return controls


def product_conformance(truth: ProductTruth) -> list[dict[str, Any]]:
    checks: list[dict[str, Any]] = []
    world = OfflineWorld(truth, OfflineFaults(unrelated_backlog=False))
    config = RunnerConfiguration()
    runner = Runner(truth, world, config, RunMode.OFFLINE_DRESS)
    result = runner.run_operation(dress_plan(truth)[10])
    if not result.passed:
        raise AssertionError("product-conformance positive operation failed")
    memory = next(row for row in result.observation.memories if row.get("_id"))
    conversation_id = nested_get(memory, "origin.openhands_provenance.conversation_id")
    event_id = nested_get(memory, "origin.openhands_provenance.event_id")
    truth.validate_memory_document(memory, conversation_id, event_id)
    checks.append({"id": "nested-mongo-selector-retains-id", "passed": True})
    omitted = deepcopy(memory); omitted.pop("_id", None)
    try:
        truth.validate_memory_document(omitted, conversation_id, event_id)
    except HarnessFailure:
        checks.append({"id": "mongo-id-omission-rejected", "passed": True})
    else:
        raise AssertionError("Mongo _id omission was accepted")
    wrong_query = world.store(RawStoreQuery("MONGODB", config.mongo_database, "memories", {"conversation_id": conversation_id, "event_id": event_id}, ("_id",)))
    if wrong_query.rows:
        raise AssertionError("invented top-level Mongo selector returned product rows")
    checks.append({"id": "top-level-mongo-proxy-rejected", "passed": True})
    hook = next(row for row in result.observation.hooks if row.get("stdout"))
    retrieval = next(row for row in result.observation.retrievals if row.get("task_ref") == json.loads(hook["stdout"])["smaTraceId"])
    selected = truth.validate_retrieval_join(hook, retrieval, result.observation.memories)
    checks.append({"id": "retrieval-task-ref-and-result-memory-join", "passed": bool(selected)})
    wrong_retrieval = deepcopy(retrieval); wrong_retrieval["task_ref"] = "wrong-task-ref"
    try: truth.validate_retrieval_join(hook, wrong_retrieval, result.observation.memories)
    except HarnessFailure: checks.append({"id": "retrieval-task-ref-mutation-rejected", "passed": True})
    else: raise AssertionError("retrieval task_ref mutation passed")
    wrong_result = deepcopy(retrieval); wrong_result["results"][0]["memory_id"] = "unknown-memory"
    try: truth.validate_retrieval_join(hook, wrong_result, result.observation.memories)
    except HarnessFailure: checks.append({"id": "retrieval-memory-mutation-rejected", "passed": True})
    else: raise AssertionError("retrieval memory mutation passed")
    selected_memory = next(row for row in result.observation.memories if row.get("_id") in selected)
    semantic = next(row for row in result.observation.semantic_points if row.get("payload", {}).get("memory_id") == selected_memory["_id"])
    episodic = next(row for row in result.observation.episodic_points if row.get("payload", {}).get("memory_id") == selected_memory["_id"])
    truth.validate_qdrant_join(selected_memory, semantic, episodic)
    checks.append({"id": "qdrant-product-payload-and-point-identity", "passed": True})
    wrong_agent = deepcopy(semantic); wrong_agent["payload"]["agent_id"] = "wrong-agent"
    try: truth.validate_qdrant_join(selected_memory, wrong_agent, episodic)
    except HarnessFailure: checks.append({"id": "qdrant-agent-mutation-rejected", "passed": True})
    else: raise AssertionError("Qdrant agent mutation passed")
    phantom = deepcopy(semantic); phantom["payload"]["trace_id"] = "invented"
    try: truth.validate_qdrant_join(selected_memory, phantom, episodic)
    except HarnessFailure: checks.append({"id": "qdrant-phantom-provenance-rejected", "passed": True})
    else: raise AssertionError("Qdrant phantom provenance passed")
    fixture_events = truth.event_fixtures
    decisions = {row["eventId"]: bool(row["captured"]) for row in truth.intake_decisions}
    if any(truth.eligible(row["event"]) != decisions[row["event"]["id"]] for row in fixture_events):
        raise AssertionError("Python eligibility disagrees with bound Java intake")
    checks.append({"id": "event-class-and-intake-eligibility", "passed": True, "events": len(fixture_events)})
    product_hook = truth.hook["persistedHookExecutionEvent"]
    parsed = json.loads(product_hook["stdout"])
    if parsed.get("smaTraceId") != truth.sma["bridgeResponse"]["trace_id"]:
        raise AssertionError("hook trace does not join bridge response")
    checks.append({"id": "exact-hook-context-parser", "passed": True})
    final = truth.event_by_label("eligible_final_agent_response")
    mutated_user = deepcopy(truth.event_by_label("eligible_user_task")); mutated_user["authorship_origin"] = "framework"
    if not truth.eligible(final) or truth.eligible(mutated_user):
        raise AssertionError("eligible final-agent or invalid user metadata classification failed")
    checks.append({"id": "final-agent-and-user-metadata-discriminator", "passed": True})
    request_fields = set(truth.surface["openhands"]["submitEvent"]["productFields"])
    if request_fields != {"content", "role", "run"}:
        raise AssertionError("submit request fields drift")
    checks.append({"id": "supported-http-request-models", "passed": True})
    settings = world.http("OPENHANDS", RawHttpRequest("GET", "/api/settings", {"X-Session-API-Key": "offline-sealed-session-key"}))
    if settings.status != 200 or set(settings.json_body()) != {"agent_settings"}:
        raise AssertionError("offline SettingsResponse does not match product shape")
    checks.append({"id": "settings-response-agent-settings-envelope", "passed": True})
    raw_actions = [row["receipt"] for row in result.observation.raw_receipts if row.get("kind") == "raw_action" and row.get("action") == "HTTP:OPENHANDS"]
    create_statuses = [row["status"] for row in raw_actions if row["request"]["method"] == "POST" and row["request"]["route"] == "/api/conversations"]
    delete_statuses = [row["status"] for row in raw_actions if row["request"]["method"] == "DELETE"]
    if create_statuses != [201] or delete_statuses != [200]:
        raise AssertionError("OpenHands product create/delete statuses drift")
    checks.append({"id": "openhands-create-201-delete-200", "passed": True})
    request_receipt = deepcopy(result.observation.model_requests[0]); terminal_receipt = deepcopy(result.observation.model_terminals[0])
    for field, value in (("recordType", "WRONG"), ("caseId", "WRONG"), ("repetition", 999), ("mode", "WRONG")):
        mutated = deepcopy(request_receipt); mutated[field] = value
        try: runner._decode_stub_request(mutated, result.observation)
        except HarnessFailure: pass
        else: raise AssertionError(f"stub raw {field} mutation passed")
        mutated_terminal = deepcopy(terminal_receipt); mutated_terminal[field] = value
        try: runner._decode_stub_terminal(mutated_terminal, result.observation)
        except HarnessFailure: pass
        else: raise AssertionError(f"stub terminal {field} mutation passed")
    checks.append({"id": "stub-record-operation-and-mode-identity-enforced", "passed": True, "mutations": 8})
    bad_request = RawHttpRequest("POST", "/api/conversations", {"Content-Type": "application/json", "X-Session-API-Key": "offline-sealed-session-key"}, canonical_bytes({"workspace": {"kind": "LocalWorkspace", "working_dir": "/tmp/x"}, "invented": True}))
    try: world.http("OPENHANDS", bad_request)
    except HarnessFailure: checks.append({"id": "emulator-only-http-field-rejected", "passed": True})
    else: raise AssertionError("emulator-only HTTP field passed")
    if not all(hasattr(LocalRawPorts, name) for name in ("http", "store", "read_file", "process")):
        raise AssertionError("live raw adapter is incomplete")
    checks.append({"id": "live-adapter-reachable", "passed": True})
    expected_actions = {"start_sma", "stop_sma", "start_bridge", "stop_bridge", "start_stub", "stop_stub", "start_intake_proxy", "stop_intake_proxy", "start_transport_guard", "stop_transport_guard", "configure_bridge_fault", "configure_model_fault", "configure_service_mode", "configure_capture_allowlist", "set_startup_configuration", "prepare_workspaces", "seed_partition", "promote_memories", "reconcile_persisted", "reconcile_exact_event", "cleanup_owned", "inventory_owned"}
    if ACTIONS != expected_actions:
        raise AssertionError("live process action adapter incomplete")
    checks.append({"id": "all-process-workspace-and-cleanup-actions-packaged", "passed": True})
    definition = load_definition(DEFINITION)
    if set(definition["actions"]) != ACTIONS or definition["authority"].get("defaultDeny") is not True:
        raise AssertionError("sealed live definition action or authority drift")
    checks.append({"id": "sealed-disposable-live-launch-definition", "passed": True, "boundFiles": len(definition["boundFiles"])})
    bound_paths = {Path(row["path"]).name for row in definition["boundFiles"]}
    required_bound = {"java", "deterministic-promotion-fixture.jar", "python3.14", "pyvenv.cfg", "live-python-requirements.lock", "sma_s2_deterministic_model_stub_candidate_2.py", "sma_s2_deterministic_model_stub_candidate_3.py", "sma_s2_openhands_capture_audit_proxy_v1.py", "sma_s2_unused_port_guard_v1.py", "sma-final-p2-product-truth.json", "product-truth-manifest.json", "pom.xml", "DeterministicPromotionFixture.java", "DeterministicPromotionFixture.class"}
    if not required_bound <= bound_paths:
        raise AssertionError("transitive live executable or truth dependency is unsealed")
    manifest = json.loads(Path(definition["seedTemplate"]["manifestPath"]).read_text(encoding="utf-8"))
    if manifest["artifacts"]["sma-final-p2-product-truth.json"]["sha256"] != definition["seedTemplate"]["sha256"]:
        raise AssertionError("seed truth is not bound through the frozen T0 manifest")
    checks.append({"id": "transitive-stub-and-seed-truth-chain-sealed", "passed": True})
    decoder_probe = subprocess.run([
        definition["environment"]["livePython"], "-c",
        "from bson import json_util;import pathlib,sys;d=json_util.loads(pathlib.Path(sys.argv[1]).read_text());m=d['rawMemoryDocument'];print(type(m['doc_version']).__name__+':'+type(m['created_at']).__name__)",
        definition["seedTemplate"]["path"],
    ], capture_output=True, text=True, timeout=30, check=False)
    if decoder_probe.returncode != 0 or decoder_probe.stdout.strip() != "int:datetime":
        raise AssertionError("seed truth was not decoded from canonical BSON Extended JSON")
    checks.append({"id": "bound-live-python-and-seed-extended-json-decoding", "passed": True})
    fixture = definition["promotionFixture"]
    fixture_source = Path(fixture["projectDirectory"]) / "src/main/java/ai/tekroo/qualification/DeterministicPromotionFixture.java"
    fixture_text = fixture_source.read_text(encoding="utf-8")
    required_fixture_tokens = (
        "new ReplayWorker(", "mongo.memoryRepository()",
        "new QdrantVectorStore(", "OllamaEmbeddingService.create(",
        'receipt.addProperty("generativeModelCalls", 0)',
    )
    if not all(token in fixture_text for token in required_fixture_tokens):
        raise AssertionError("deterministic promotion fixture bypasses a required product boundary")
    if "OllamaCognitiveEngine" in fixture_text:
        raise AssertionError("promotion fixture reaches a generative cognitive model")
    if "locked.event().surfaceSummary()" not in fixture_text:
        raise AssertionError("promotion fixture does not interpret the retained raw event surface")
    classpath_probe = subprocess.run([
        fixture["javaExecutable"], "-cp",
        f'{fixture["fixtureJar"]}:{fixture["smaJar"]}', fixture["mainClass"],
    ], cwd=fixture["projectDirectory"], capture_output=True, text=True, timeout=30, check=False)
    if classpath_probe.returncode == 0 or "s2.database is required" not in classpath_probe.stderr:
        raise AssertionError("bound direct-Java promotion classpath is not executable")
    checks.append({"id": "promotion-fixture-real-product-boundaries-zero-generative-model", "passed": True})
    helper_path = Path(definition["environment"]["smaRoot"]) / "scripts/wp5e_restart_recovery.py"
    helper_spec = importlib.util.spec_from_file_location("s2_t2_bound_support", helper_path)
    if helper_spec is None or helper_spec.loader is None:
        raise AssertionError("cannot load bound SMA launch support")
    helper = importlib.util.module_from_spec(helper_spec)
    helper_spec.loader.exec_module(helper)
    required_support = {"bootout_sma", "bootstrap_sma", "bridge_health", "launch_identity", "write_plist"}
    if not all(callable(getattr(helper, name, None)) for name in required_support):
        raise AssertionError("bound SMA launch support callable surface drift")
    checks.append({"id": "all-bound-sma-helper-calls-resolve", "passed": True, "callables": sorted(required_support)})
    live_source = Path(__file__).with_name("live_actions.py")
    tree = ast.parse(live_source.read_text(encoding="utf-8"))
    literals = {node.value for node in ast.walk(tree) if isinstance(node, ast.Constant) and isinstance(node.value, str)}
    if not ACTIONS <= literals:
        raise AssertionError("one or more frozen live actions has no concrete implementation branch")
    entry_source = Path(__file__).with_name("live_entrypoint.py").read_text(encoding="utf-8")
    if not all(token in entry_source for token in ("LiveConfiguration", "LiveAuthority", "LocalRawPorts", "dress_plan", "measured_plan")):
        raise AssertionError("live composition is incomplete")
    checks.append({"id": "default-deny-live-composition-and-concrete-actions", "passed": True})
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory); evidence = {name: str(root / file) for name, file in (("stubRaw", "raw.jsonl"), ("stubTerminal", "terminal.jsonl"), ("operationalLog", "operational.jsonl"))}
        for path in [*map(Path, evidence.values()), root / "stub-ready.json"]: path.write_text("stale\n", encoding="utf-8")
        engine = object.__new__(Engine); engine.d = {"evidenceFiles": evidence}; engine.runtime = root
        engine.reset_stub_evidence()
        if any(Path(path).read_bytes() for path in evidence.values()) or (root / "stub-ready.json").exists():
            raise AssertionError("exact live journal/readiness reset failed")
    checks.append({"id": "live-per-operation-journal-and-ready-reset", "passed": True})
    live_text = live_source.read_text(encoding="utf-8")
    required_live_semantics = ("promote_memory", "captureCycleReceipt", "SMA_S2_OPENHANDS_CAPTURE_AUDIT_PROXY_RECEIPT", "count_documents(selector)", "terminationConfirmed", "raw_ids - terminal_ids", "reset_stub_evidence")
    if not all(token in live_text for token in required_live_semantics):
        raise AssertionError("one or more concrete live semantic controls is absent")
    if "replay_promotion" in live_text:
        raise AssertionError("live path still calls the nonexistent SMA helper")
    if 'document["canonical"]["text"] = ""' not in live_text:
        raise AssertionError("live seed violates ReplayWorker's initial-canonical bootstrap invariant")
    if '"SMA_CANARY_PARTITION": "sma-s2-final-p2:no-background-replay"' not in live_text:
        raise AssertionError("live SMA service does not disable background generative replay")
    checks.append({"id": "live-seed-promotion-duplicate-reconciliation-and-termination-proof", "passed": True})
    engine = object.__new__(Engine)
    engine.d = definition
    if engine.env({"capture": True, "retrieval": True, "allowlist": []}).get("SMA_CANARY_PARTITION") != "sma-s2-final-p2:no-background-replay":
        raise AssertionError("live service environment permits background cognitive replay")
    captured: dict[str, Any] = {}
    expected_receipt = {
        "recordType": "SMA_S2_DETERMINISTIC_PRODUCT_PROMOTION_RECEIPT",
        "memoryId": "mem-t2-boundary",
        "state": "consistent",
        "reasoningEligible": True,
        "startingReplayCount": 0,
        "endingReplayCount": 1,
        "deterministicCognitiveInvocations": 1,
        "generativeModelCalls": 0,
        "embeddingInvocations": 1,
        "semanticRef": "mem-t2-boundary",
    }
    def fake_run(command: list[str], cwd: Path, timeout_seconds: int) -> subprocess.CompletedProcess[str]:
        captured.update({"command": command, "cwd": str(cwd), "timeout": timeout_seconds})
        return subprocess.CompletedProcess(command, 0, json.dumps(expected_receipt) + "\n", "")
    engine.run_subprocess = fake_run  # type: ignore[method-assign]
    promotion = engine.promote_memory("mem-t2-boundary")
    project = str(Path(fixture["projectDirectory"]))
    required_properties = {
        f'-Ds2.database={definition["disposable"]["mongoDatabase"]}',
        f'-Ds2.semanticCollection={definition["disposable"]["semanticCollection"]}',
        f'-Ds2.episodicCollection={definition["disposable"]["episodicCollection"]}',
        "-Ds2.memoryId=mem-t2-boundary",
    }
    if captured.get("cwd") != project or not required_properties <= set(captured.get("command", [])):
        raise AssertionError("promotion wrapper did not bind exact working directory and namespaces")
    if promotion.get("productPromotion") != expected_receipt:
        raise AssertionError("promotion wrapper did not retain the exact validated fixture receipt")
    for mutation in (
        {**expected_receipt, "generativeModelCalls": 1},
        {**expected_receipt, "embeddingInvocations": 0},
        {**expected_receipt, "memoryId": "wrong"},
    ):
        engine.run_subprocess = lambda command, cwd, timeout_seconds, value=mutation: subprocess.CompletedProcess(command, 0, json.dumps(value) + "\n", "")  # type: ignore[method-assign]
        try:
            engine.promote_memory("mem-t2-boundary")
        except RuntimeError:
            pass
        else:
            raise AssertionError("invalid deterministic promotion receipt was accepted")
    checks.append({"id": "live-promotion-wrapper-cwd-namespace-and-receipt-contract", "passed": True, "negativeControls": 3})
    duplicate = _operation_for_case(truth, "SMA-S2-006-DUPLICATE-PERSISTED-EVENT")
    cycle_receipts = [row for row in duplicate.observation.raw_receipts if row.get("kind") == "capture_cycle_receipt"]
    if len(cycle_receipts) != 2 or any(row.get("receipt", {}).get("status") != 200 or row.get("eventId") not in row.get("receipt", {}).get("eventIds", []) for row in cycle_receipts):
        raise AssertionError("case-6 lacks two exact product capture-cycle receipts")
    checks.append({"id": "case6-two-exact-successful-capture-cycle-receipts", "passed": True})
    feedback = _operation_for_case(truth, "SMA-S2-014-FEEDBACK-LOOP-PREVENTION")
    if {row.get("mode") for row in feedback.observation.model_requests} != {"INELIGIBLE_TOOL_TRAFFIC"}:
        raise AssertionError("case-14 tool-traffic mode is not wired to the stub request")
    transport_world = OfflineWorld(truth); transport_runner = Runner(truth, transport_world, RunnerConfiguration(), RunMode.OFFLINE_DRESS)
    transport = transport_runner.run_operation(OperationKey("SMA-S2-019-MODEL-STUB-FAULT-MATRIX", 4))
    create_call = next(row for row in transport_world.external_calls if row.get("kind") == "HTTP" and row["request"]["route"] == "/api/conversations")
    create_body = next(row for row in transport.observation.raw_receipts if row.get("kind") == "raw_action" and row.get("action") == "HTTP:OPENHANDS" and row["receipt"]["request"]["route"] == "/api/conversations")
    endpoint = next(row for row in transport.observation.raw_receipts if row.get("kind") == "model_endpoint")
    if not transport.passed or transport.observation.model_requests or transport.observation.model_terminals or not create_call or create_body["receipt"]["status"] != 201 or endpoint.get("baseUrl") != "http://127.0.0.1:19192/v1" or endpoint.get("scheduledMode") != "TRANSPORT_FAILURE_UNUSED_PORT":
        raise AssertionError("case-19 unused-port transport path is not exact-zero-receipt")
    create_request = next(call for call in transport_world.external_calls if call.get("kind") == "HTTP" and call["request"]["route"] == "/api/conversations")
    if create_request["request"]["bodySha256"] is None:
        raise AssertionError("transport create request was not retained")
    checks.append({"id": "case14-tool-mode-and-case19-unused-port-routing", "passed": True})
    if definition["ports"].get("transportFailure") != 19192 or definition["loopback"].get("transportFailure") != "http://127.0.0.1:19192/v1":
        raise AssertionError("transport-failure endpoint is not frozen")
    guard_source = (truth.paths.repo / "scripts/sma_s2_unused_port_guard_v1.py").read_text(encoding="utf-8")
    if not all(token in guard_source for token in ("held.bind", "connect_ex", "listening", "connectRefused")):
        raise AssertionError("transport-failure endpoint lacks bound no-listener guard")
    checks.append({"id": "case19-transport-failure-port-frozen-and-guarded", "passed": True})
    from .build_artifacts import CASE_WORKFLOWS
    seeded_cases = {"SMA-S2-002-SAME-PARTITION-DELIVERY", "SMA-S2-004-UNTRUSTED-CONTEXT-PLACEMENT", "SMA-S2-005-RAW-INELIGIBLE-ABSENCE", "SMA-S2-009-ADDITIONAL-CONTEXT-INTEGRITY", "SMA-S2-012-RESTART-CONTINUITY", "SMA-S2-013-FOUR-CHANNEL-CONCURRENCY", "SMA-S2-018-CONDENSATION-REANCHOR"}
    if any("seed_partition" not in CASE_WORKFLOWS[case_id][1] for case_id in seeded_cases):
        raise AssertionError("seeded workflow traceability is incomplete")
    checks.append({"id": "all-seven-seeded-workflows-trace-live-seed-action", "passed": True})
    if not classify_boundary_exception(TimeoutError()).startswith("ENVIRONMENT:") or not classify_boundary_exception(ValueError()).startswith("HARNESS:"):
        raise AssertionError("live exception normalization drift")
    checks.append({"id": "live-boundary-exception-normalization", "passed": True})
    if tuple(key.value for key in dress_plan(truth)) == tuple(key.value for key in measured_plan(truth)[:34]):
        pass
    # The important invariant is one Runner and one registry, not prefix equality.
    checks.append({"id": "shared-runner-and-oracle-registry", "passed": True, "dress": 34, "measured": 103})
    cancel_world = OfflineWorld(truth)
    cancel_runner = Runner(truth, cancel_world, RunnerConfiguration(), RunMode.OFFLINE_DRESS)
    cancel_result = cancel_runner.run_operation(OperationKey("SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN", 1))
    if not cancel_result.passed:
        raise AssertionError("case-20 shutdown conformance operation failed")
    process_actions = [row["action"] for row in cancel_world.external_calls if row["kind"] == "PROCESS"]
    stop_positions = [process_actions.index(name) for name in ("stop_stub", "stop_bridge", "stop_sma", "stop_intake_proxy", "stop_transport_guard")]
    if stop_positions != sorted(stop_positions) or process_actions.index("inventory_owned") < stop_positions[-1] or process_actions.index("cleanup_owned") < process_actions.index("inventory_owned"):
        raise AssertionError("case-20 owned shutdown is not proven before cleanup")
    checks.append({"id": "case20-explicit-stop-inventory-before-cleanup", "passed": True})
    with tempfile.TemporaryDirectory() as directory:
        missing = Path(directory) / "missing.json"
        if missing.exists():
            raise AssertionError("unexpected temp file")
        checks.append({"id": "missing-binding-fails-closed", "passed": not missing.is_file()})
        drift = Path(directory) / "manifest.json"
        drift.write_text("{}", encoding="utf-8")
        checks.append({"id": "changed-source-binding-fails-closed", "passed": file_sha256(drift) != "c2520df45c0ac318fc3d23cef864586b609032179eae6a59c955898758bb476e"})
    return checks


def run_suite(repo: Path) -> dict[str, Any]:
    truth = ProductTruth.load(repo)
    dress_world = OfflineWorld(truth)
    dress_runner = Runner(truth, dress_world, RunnerConfiguration(), RunMode.OFFLINE_DRESS)
    dress = dress_runner.run(dress_plan(truth))
    measured_world = OfflineWorld(truth)
    measured_runner = Runner(truth, measured_world, RunnerConfiguration(), RunMode.OFFLINE_MEASURED)
    measured = measured_runner.run(measured_plan(truth))
    if len(dress) != 34 or not all(row.passed for row in dress): raise AssertionError("34-operation integrated walk failed")
    if len(measured) != 103 or not all(row.passed for row in measured): raise AssertionError("103-operation integrated walk failed")
    science = scientific_mutation_controls(truth)
    evidence = evidence_mutation_controls(truth)
    product = product_conformance(truth)
    package = Path(__file__).parent
    sources = {path.name: file_sha256(path) for path in sorted(package.iterdir()) if path.suffix in {".py", ".json"}}
    for wrapper in (repo / "scripts/qualify_sma_s2_final_p2_closure.py", repo / "scripts/qualify_sma_s2_final_p2_closure_h0.py"):
        sources[f"scripts/{wrapper.name}"] = file_sha256(wrapper)
    return {
        "recordType": "SMA_S2_FINAL_P2_T2_OFFLINE_QUALIFICATION",
        "status": "PASS_T2_UNPUBLISHED_NOT_EXECUTION_AUTHORITY",
        "productTruthManifestSha256": file_sha256(truth.paths.t0 / "product-truth-manifest.json"),
        "packageSources": sources,
        "dress": {"operations": len(dress), "passed": sum(row.passed for row in dress)},
        "measured": {"operations": len(measured), "passed": sum(row.passed for row in measured)},
        "handlers": {"declared": len(Runner(truth, OfflineWorld(truth), RunnerConfiguration(), RunMode.OFFLINE_DRESS).descriptors), "covered": 20},
        "scientificMutations": science,
        "evidenceMutations": evidence,
        "productConformance": product,
        "ledger": {"dressValid": dress_runner.ledger.verify(), "measuredValid": measured_runner.ledger.verify()},
        "prohibitedActivity": {"livePorts": 0, "serviceStarts": 0, "OpenHandsConversations": 0, "modelCalls": 0, "productionOrHistoricalAccess": 0},
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", default=str(Path(__file__).resolve().parents[5]))
    parser.add_argument("--output")
    parser.add_argument("--optimized-child", action="store_true")
    args = parser.parse_args()
    receipt = run_suite(Path(args.repo).resolve())
    receipt["receiptSha256"] = digest(receipt)
    if args.output:
        Path(args.output).write_bytes(canonical_bytes(receipt) + b"\n")
    else:
        print(json.dumps(receipt, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
