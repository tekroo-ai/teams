from __future__ import annotations

from dataclasses import asdict
import json
from pathlib import Path
from typing import Any

from .model import canonical_bytes, digest, file_sha256
from .oracles import CASE_PREDICATES
from .ports import LocalRawPorts
from .runner import RunnerConfiguration, build_descriptors, dress_plan, measured_plan
from .truth import ProductTruth
from .live_actions import ACTIONS
from .live_entrypoint import DEFINITION, load_definition


CASE_WORKFLOWS = {
    "SMA-S2-001-FIRST-PROMPT-EMPTY": ("_case_first_empty", ("reconcile_persisted",)),
    "SMA-S2-002-SAME-PARTITION-DELIVERY": ("_case_standard", ("seed_partition",)),
    "SMA-S2-004-UNTRUSTED-CONTEXT-PLACEMENT": ("_case_standard", ("seed_partition",)),
    "SMA-S2-005-RAW-INELIGIBLE-ABSENCE": ("_case_standard", ("seed_partition",)),
    "SMA-S2-006-DUPLICATE-PERSISTED-EVENT": ("_case_duplicate", ("reconcile_exact_event", "stop_sma", "start_sma")),
    "SMA-S2-007-RETRIEVAL-OUTAGE": ("_case_retrieval_outage", ("configure_bridge_fault",)),
    "SMA-S2-008-CAPTURE-OUTAGE": ("_case_capture_outage", ("configure_service_mode", "reconcile_persisted")),
    "SMA-S2-009-ADDITIONAL-CONTEXT-INTEGRITY": ("_case_standard", ("seed_partition",)),
    "SMA-S2-010-HOOK-FAULT-MATRIX": ("_case_hook_fault", ("configure_bridge_fault",)),
    "SMA-S2-011-PARENT-CHILD-PROVENANCE": ("_case_parent_child", ()),
    "SMA-S2-012-RESTART-CONTINUITY": ("_case_restart", ("seed_partition", "stop_bridge", "stop_sma", "start_sma", "start_bridge")),
    "SMA-S2-013-FOUR-CHANNEL-CONCURRENCY": ("_case_concurrency", ("seed_partition", "configure_model_fault")),
    "SMA-S2-014-FEEDBACK-LOOP-PREVENTION": ("_case_feedback", ("configure_model_fault", "reconcile_persisted")),
    "SMA-S2-017-OVERSIZED-CONTEXT": ("_case_oversized", ("seed_partition", "promote_memories")),
    "SMA-S2-018-CONDENSATION-REANCHOR": ("_case_condensation", ("seed_partition",)),
    "SMA-S2-019-MODEL-STUB-FAULT-MATRIX": ("_case_model_fault", ("configure_model_fault",)),
    "SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN": ("_case_cancel", ("configure_model_fault", "stop_stub", "stop_bridge", "stop_sma", "stop_intake_proxy", "stop_transport_guard", "inventory_owned")),
}


def build(repo: Path, output: Path) -> dict[str, Any]:
    truth = ProductTruth.load(repo)
    package = Path(__file__).parent
    sources = {path.name: file_sha256(path) for path in sorted(package.iterdir()) if path.suffix in {".py", ".json"}}
    for wrapper in (repo / "scripts/qualify_sma_s2_final_p2_closure.py", repo / "scripts/qualify_sma_s2_final_p2_closure_h0.py"):
        sources[f"scripts/{wrapper.name}"] = file_sha256(wrapper)
    launch = load_definition()
    for index, binding in enumerate(launch["boundFiles"]):
        path = Path(binding["path"])
        sources[f"liveBound/{index:02d}:{path}"] = file_sha256(path)
    for index, binding in enumerate(launch.get("boundTrees", [])):
        sources[f"liveBoundTree/{index:02d}:{binding['path']}"] = binding["sha256"]
    config = RunnerConfiguration()
    descriptors = build_descriptors(truth, config)
    manifest = {
        "schemaVersion": "1.0.0-dev",
        "recordType": "SMA_S2_FINAL_P2_CLOSURE_MUTABLE_PACKAGE_MANIFEST",
        "status": "MUTABLE_UNPUBLISHED_NOT_CANDIDATE_NOT_EXECUTION_AUTHORITY",
        "productTruth": {
            "manifestSha256": file_sha256(truth.paths.t0 / "product-truth-manifest.json"),
            "qualificationSha256": file_sha256(truth.paths.t0 / "qualification-receipt.json"),
            "independentReviewSha256": file_sha256(truth.paths.t0_review),
        },
        "science": {
            "preregistrationSha256": file_sha256(truth.paths.prereg),
            "corpusSha256": file_sha256(truth.paths.corpus),
            "matrixSha256": file_sha256(truth.paths.matrix),
            "cases": len(descriptors), "measuredRepetitions": len(measured_plan(truth)),
            "dressOperations": len(dress_plan(truth)), "predicates": 59, "evidenceClasses": 10,
        },
        "sourceFiles": sources,
        "mutableTreeIdentity": digest(sources),
        "rawPortMethods": [name for name in ("http", "store", "read_file", "process") if hasattr(LocalRawPorts, name)],
        "supportedRoutes": truth.surface["openhands"],
        "mongoContract": truth.surface["mongo"],
        "qdrantContract": truth.surface["qdrant"],
        "bridgeContract": truth.surface["bridge"],
        "liveProcessActions": sorted(ACTIONS),
        "liveLaunchDefinition": {"path": str(DEFINITION.relative_to(repo)), "sha256": file_sha256(DEFINITION), "recordType": load_definition(DEFINITION)["recordType"]},
        "operationDescriptors": {case_id: asdict(descriptor) for case_id, descriptor in descriptors.items()},
        "plans": {
            "dress": [key.value for key in dress_plan(truth)],
            "measured": [key.value for key in measured_plan(truth)],
        },
        "authority": {"live": False, "dress": False, "measured": False, "model": False, "serviceStart": False},
    }
    traceability = {
        "schemaVersion": "1.0.0-dev",
        "recordType": "SMA_S2_FINAL_P2_CLOSURE_TRACEABILITY",
        "status": "MUTABLE_T2_TRACEABILITY_NOT_EXECUTION_AUTHORITY",
        "productJoin": truth.surface["evidenceJoin"],
        "cases": [],
        "evidence": [{"oracleId": f"E-{index:03d}", "registry": "evidence_oracles", "rawEvidenceOnly": True} for index in range(1, 11)],
    }
    for case in truth.matrix["cases"]:
        predicates = CASE_PREDICATES[case["caseId"]]
        handler, special_actions = CASE_WORKFLOWS.get(case["caseId"], ("_case_standard", ()))
        routes = ["GET /api/settings", "POST /api/conversations", "POST /api/conversations/{id}/events", "GET /api/conversations/{id}/events/search?limit=100", "GET /api/conversations/{id}", "DELETE /api/conversations/{id}"]
        if case["caseId"].endswith("RESTART-CONTINUITY"): routes.append("POST /v1/openhands/context")
        if case["caseId"].endswith("CONDENSATION-REANCHOR"): routes.append("POST /api/conversations/{id}/condense")
        if case["caseId"].endswith("ACTIVE-CANCELLATION-AND-SHUTDOWN"): routes.append("POST /api/conversations/{id}/interrupt")
        traceability["cases"].append({
            "caseId": case["caseId"],
            "oracles": [{"oracleId": oracle_id, "function": predicate.__name__, "rawEvidenceOnly": True} for oracle_id, predicate in zip(case["predicateOracleIds"], predicates, strict=True)],
            "httpRoutes": routes,
            "requestSchemas": {"createConversation": truth.surface["openhands"]["createConversation"]["productFields"], "submitEvent": truth.surface["openhands"]["submitEvent"]["productFields"], "bridge": truth.surface["bridge"]["requestFields"]},
            "rawPorts": ["http", "store", "read_file", "process"],
            "processActions": sorted(set(("start_intake_proxy", "start_transport_guard", "start_sma", "start_bridge", "start_stub", "configure_bridge_fault", "configure_model_fault", "configure_service_mode", "prepare_workspaces", "configure_capture_allowlist", "cleanup_owned", "inventory_owned") + tuple(special_actions))),
            "lifecycleBranch": handler,
            "recovery": list(descriptors[case["caseId"]].recovery),
            "cleanup": list(descriptors[case["caseId"]].cleanup),
            "evidenceFields": ["raw_events", "conversations", "hooks", "model_requests", "model_terminals", "memories", "retrievals", "semantic_points", "episodic_points", "timings", "raw_receipts", "cleanup_receipts"],
            "liveActionImplementation": "t1/live_actions.py",
        })
    output.mkdir(parents=True, exist_ok=True)
    (output / "mutable-package-manifest.json").write_bytes(canonical_bytes(manifest) + b"\n")
    (output / "traceability.json").write_bytes(canonical_bytes(traceability) + b"\n")
    return {"manifest": manifest, "traceability": traceability}


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    build(Path(args.repo).resolve(), Path(args.output).resolve())
