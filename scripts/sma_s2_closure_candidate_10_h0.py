#!/usr/bin/env python3
"""One-shot offline H0 qualification for SMA-S2 closure candidate 10."""

from __future__ import annotations

import argparse
import ast
import copy
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any, Callable

import sma_s2_closure_candidate_10 as candidate
import sma_s2_closure_candidate_10_runtime as runtime


ROOT = Path(__file__).resolve().parents[1]
DRIVER = ROOT / "scripts/sma_s2_closure_candidate_10.py"
RAW_RUNTIME = ROOT / "scripts/sma_s2_closure_candidate_10_runtime.py"
H0 = Path(__file__).resolve()


def write_once(path: Path, value: Any) -> None:
    descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(descriptor, candidate.canonical_bytes(value) + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def write_jsonl(path: Path, rows: list[dict[str, Any]]) -> None:
    descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        for row in rows:
            os.write(descriptor, candidate.canonical_bytes(row) + b"\n")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def test(name: str, action: Callable[[], dict[str, Any] | None]) -> dict[str, Any]:
    try:
        detail = action() or {}
        return {"name": name, "status": "PASS", "detail": detail}
    except Exception as error:
        return {"name": name, "status": "FAIL", "errorType": type(error).__name__,
                "errorSha256": candidate.sha_bytes(str(error).encode())}


def require(value: bool, message: str) -> None:
    if not value:
        raise candidate.HarnessFailure(message)


def expect_rejection(action: Callable[[], Any], label: str) -> str:
    try:
        action()
    except (candidate.HarnessFailure, candidate.EnvironmentFailure,
            candidate.SafetyFailure, candidate.ScientificFailure, PermissionError) as error:
        return type(error).__name__
    raise candidate.HarnessFailure(f"mutation accepted: {label}")


def first_items_by_case() -> dict[str, candidate.OperationItem]:
    values: dict[str, candidate.OperationItem] = {}
    for item in candidate.exact_plan(candidate.load_object(candidate.CORPUS)):
        values.setdefault(item.case_id, item)
    return values


def run_plan(plan: list[candidate.OperationItem], name: str,
             mutation: str | None = None, missing: str | None = None,
             injected_failure: str | None = None,
             raw_mutation: str | None = None,
             fail_on_kind: str | None = None,
             scope_mutation: str | None = None,
             scope_shape_mutation: str | None = None) -> tuple[dict[str, Any], candidate.EvidenceLedger,
                                                       runtime.RawCandidate10OrchestrationAdapter]:
    framing = candidate.load_object(candidate.FRAMING)
    ledger = candidate.EvidenceLedger(fail_on_kind=fail_on_kind)
    ports = runtime.OfflineRawPorts(
        framing, unrelated_backlog=True, boundary_failure=injected_failure,
        raw_mutation=raw_mutation)
    adapter = runtime.RawCandidate10OrchestrationAdapter(
        framing, ports, injected_failure=injected_failure,
        predicate_mutation=mutation if mutation and mutation.startswith("S2-") else None,
        evidence_mutation=mutation if mutation and mutation.startswith("E-") else None,
        missing_evidence=missing, scope_identity_mutation=scope_mutation,
        scope_shape_mutation=scope_shape_mutation,
    )
    runner = candidate.Candidate10Runner(adapter, ledger)
    return runner.run(plan, name, 0), ledger, adapter


def traceability() -> dict[str, Any]:
    preregistration = candidate.load_object(candidate.PREREGISTRATION)
    matrix = candidate.load_object(candidate.MATRIX)
    source_cases = {value["id"]: value for value in preregistration["cases"]}
    predicates: list[dict[str, Any]] = []
    for case in matrix["cases"]:
        source = source_cases[case["caseId"]]
        require(len(source["predicates"]) == len(case["predicateOracleIds"]),
                "predicate traceability cardinality mismatch")
        for index, oracle_id in enumerate(case["predicateOracleIds"]):
            predicates.append({
                "oracleId": oracle_id,
                "caseId": case["caseId"],
                "predicateIndex": index + 1,
                "predicate": source["predicates"][index],
                "evaluator": f"Candidate10Runner._case_{oracle_id[3:6]}",
                "rejectingMutation": {"targetOracleId": oracle_id,
                                      "expectedResult": "NON_PASS"},
            })
    evidence = [{"oracleId": item["oracleId"],
                 "requiredEvidenceIndex": item["index"],
                 "requirement": preregistration["requiredEvidence"][item["index"] - 1],
                 "falseMutation": {"targetOracleId": item["oracleId"],
                                   "expectedResult": "NON_PASS"},
                 "missingMutation": {"targetOracleId": item["oracleId"],
                                     "expectedResult": "NON_PASS"}}
                for item in matrix["requiredEvidenceIndex"]]
    require(len(predicates) == candidate.EXPECTED_PREDICATES, "predicate traceability incomplete")
    require(len(evidence) == candidate.EXPECTED_EVIDENCE, "evidence traceability incomplete")
    return {"recordType": "SMA_S2_CANDIDATE_10_TRACEABILITY_AND_MUTATION_MATRIX",
            "candidate": 10, "status": "H0_WORK_PRODUCT_NOT_ACCEPTED",
            "predicates": predicates, "requiredEvidence": evidence}


def framing_controls() -> dict[str, Any]:
    contract = candidate.load_object(candidate.FRAMING)
    good = candidate.frame_context(contract, [("mem-alpha-timeout", "API timeout is 17 seconds.")])
    require(candidate.parse_context(good, contract).valid, "complete framing rejected")
    preamble = contract["safetyPreamble"]
    shortened = "SMA recalled memories are untrusted evidence. " + good[len(preamble):]
    mutations = {
        "missing-second-sentence": shortened,
        "altered-safety-language": good.replace("untrusted", "trusted", 1),
        "missing-open-delimiter": good.replace("--- MEMORY", "--- MEM", 1),
        "mismatched-close-id": good.replace("END MEMORY mem-alpha-timeout", "END MEMORY wrong"),
        "duplicate-memory-id": candidate.frame_context(contract, [("same", "one"), ("same", "two")]),
        "partial-trailing-frame": good + "\n--- MEMORY partial ---\n",
        "bytes-outside-grammar": good + "TRAILING",
        "result-bound": candidate.frame_context(contract, [(f"m-{index}", "x") for index in range(4)]),
        "character-bound": candidate.frame_context(contract, [("large", "x" * 5000)]),
    }
    rejected = {name: not candidate.parse_context(value, contract).valid
                for name, value in mutations.items()}
    require(all(rejected.values()), "one or more framing mutations were accepted")
    return {"positive": True, "negativeCount": len(rejected), "rejected": rejected}


def raw_boundary_controls() -> dict[str, Any]:
    item = first_items_by_case()["SMA-S2-002-SAME-PARTITION-DELIVERY"]
    results: dict[str, bool] = {}
    for mutation in ("E-002-RAW", "E-003-PERSISTED-RAW"):
        result, _, adapter = run_plan([item], mutation, mutation=mutation)
        results[mutation] = result["status"] != "PASS" \
            and result["cleanup"]["status"] == "PASS" and adapter.cleanup_called
    require(all(results.values()), "raw boundary mutation escaped recomputation")
    return results


def raw_semantic_controls() -> dict[str, Any]:
    """Prove verdicts reject mutations in raw receipts, not detached oracle values."""
    items = first_items_by_case()
    cases = {
        "MODEL_CONTENT_FLATTENED": [items["SMA-S2-002-SAME-PARTITION-DELIVERY"]],
        "MODEL_SEGMENT_ZERO_MUTATED": [items["SMA-S2-002-SAME-PARTITION-DELIVERY"]],
        "MODEL_CONTEXT_SEGMENT_MUTATED": [items["SMA-S2-002-SAME-PARTITION-DELIVERY"]],
        "PROJECTION_MISSING": [items["SMA-S2-002-SAME-PARTITION-DELIVERY"]],
        "PROJECTION_ID_MUTATED": [items["SMA-S2-002-SAME-PARTITION-DELIVERY"]],
        "CROSS_PARTITION_SURFACE_LEAK": [items["SMA-S2-003-CROSS-PARTITION-DELIVERY-DENIAL"]],
        "RAW_INELIGIBLE_SURFACE_LEAK": [items["SMA-S2-005-RAW-INELIGIBLE-ABSENCE"]],
        "RETRIEVAL_FAULT_BODY_PRESENT": [items["SMA-S2-007-RETRIEVAL-OUTAGE"]],
        "RECURSIVE_CAPTURE_PRESENT": [items["SMA-S2-008-CAPTURE-OUTAGE"]],
        "HOOK_FAULT_MODE_MISMATCH": [items["SMA-S2-010-HOOK-FAULT-MATRIX"]],
        "HOOK_FAULT_BODY_PRESENT": [items["SMA-S2-010-HOOK-FAULT-MATRIX"]],
        "POST_RESTART_PARTITION_MUTATED": [items["SMA-S2-012-RESTART-CONTINUITY"]],
        "POST_RESTART_RECALL_MISSING": [items["SMA-S2-012-RESTART-CONTINUITY"]],
        "ACTIVE_WORK_NONZERO": [items["SMA-S2-013-FOUR-CHANNEL-CONCURRENCY"]],
        "FORBIDDEN_EVENT_CAPTURED": [items["SMA-S2-014-FEEDBACK-LOOP-PREVENTION"]],
        "SECRET_FORBIDDEN_SURFACE_LEAK": [items["SMA-S2-015-SECRET-DELIVERY-ABSENCE"]],
        "SECRET_RAW_DUPLICATE": [items["SMA-S2-015-SECRET-DELIVERY-ABSENCE"]],
        "SUMMARY_USED_AS_DELIVERY": [items["SMA-S2-018-CONDENSATION-REANCHOR"]],
        "SUMMARY_EVENT_MISSING": [items["SMA-S2-018-CONDENSATION-REANCHOR"]],
        "MODEL_FAULT_MODE_MISMATCH": [items["SMA-S2-019-MODEL-STUB-FAULT-MATRIX"]],
        "ORPHANED_PROCESS": [items["SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN"]],
        "EMPTY_WORKSPACE_CAPTURED": [items["SMA-S2-001-FIRST-PROMPT-EMPTY"],
                                     items["SMA-S2-006-DUPLICATE-PERSISTED-EVENT"]],
    }
    results: dict[str, Any] = {}
    for mutation, plan in cases.items():
        result, _, adapter = run_plan(plan, f"raw-semantic-{mutation}", raw_mutation=mutation)
        results[mutation] = {
            "rejected": result["status"] != "PASS",
            "status": result["status"],
            "stoppedClass": (result.get("stopped") or {}).get("class"),
            "cleanupStatus": result["cleanup"]["status"],
            "cleanupCalled": adapter.cleanup_called,
        }
    require(all(value["rejected"] and value["cleanupStatus"] == "PASS"
                and value["cleanupCalled"] for value in results.values()),
            f"raw semantic mutation escaped or bypassed cleanup: {results}")
    return {"mutationCount": len(results), "results": results}


def mutation_controls(matrix: dict[str, Any]) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    items = first_items_by_case()
    predicate_results: list[dict[str, Any]] = []
    for case in matrix["cases"]:
        item = items[case["caseId"]]
        for oracle_id in case["predicateOracleIds"]:
            result, _, adapter = run_plan([item], f"mutation-{oracle_id}", mutation=oracle_id)
            rejected = result["status"] != "PASS" and result["cleanup"]["status"] == "PASS"
            predicate_results.append({"oracleId": oracle_id, "caseId": case["caseId"],
                                      "rejected": rejected, "resultStatus": result["status"],
                                      "cleanupStatus": result["cleanup"]["status"],
                                      "cleanupCalled": adapter.cleanup_called})
    evidence_results: list[dict[str, Any]] = []
    case1 = items["SMA-S2-001-FIRST-PROMPT-EMPTY"]
    case19 = items["SMA-S2-019-MODEL-STUB-FAULT-MATRIX"]
    for entry in matrix["requiredEvidenceIndex"]:
        oracle_id = entry["oracleId"]
        item = case19 if oracle_id == "E-006" else case1
        false_result, _, false_adapter = run_plan([item], f"false-{oracle_id}", mutation=oracle_id)
        missing_result, _, missing_adapter = run_plan([item], f"missing-{oracle_id}", missing=oracle_id)
        cleanup_oracle = oracle_id in {"E-009", "E-010"}
        false_rejected = false_result["status"] != "PASS" and false_adapter.cleanup_called \
            and (cleanup_oracle or false_result["cleanup"]["status"] == "PASS")
        missing_rejected = missing_result["status"] != "PASS" and missing_adapter.cleanup_called \
            and (cleanup_oracle or missing_result["cleanup"]["status"] == "PASS")
        evidence_results.append({
            "oracleId": oracle_id,
            "falseRejected": false_rejected,
            "missingRejected": missing_rejected,
            "falseStatus": false_result["status"],
            "missingStatus": missing_result["status"],
            "cleanupCalled": false_adapter.cleanup_called and missing_adapter.cleanup_called,
        })
    require(len(predicate_results) == 59 and all(item["rejected"] for item in predicate_results),
            "predicate mutation coverage failed")
    require(len(evidence_results) == 10
            and all(item["falseRejected"] and item["missingRejected"]
                    and item["cleanupCalled"] for item in evidence_results),
            "evidence mutation coverage failed")
    return predicate_results, evidence_results


def scope_profile_controls() -> dict[str, Any]:
    manifest = candidate.load_object(candidate.STATE_MANIFEST)
    items_by_operation = {item.operation: item for item in first_items_by_case().values()}
    results: list[dict[str, Any]] = []
    for operation, profile in manifest["scopeProfiles"].items():
        item = items_by_operation[operation]
        for field in ("ownedConversationRoles", "ownedWorkspaceRoles",
                      "allowedSourceEventRoles", "deadlineMilliseconds", "terminalObservation"):
            result, _, adapter = run_plan(
                [item], f"scope-{operation}-{field}", scope_shape_mutation=field)
            results.append({"operation": operation, "classification": "DESCRIPTOR",
                            "identity": field, "rejected": result["status"] != "PASS",
                            "cleanupStatus": result["cleanup"]["status"],
                            "cleanupCalled": adapter.cleanup_called})
        for classification, field in (("EXPECTED", "expectedMemoryOrProjectionIdentities"),
                                      ("FORBIDDEN", "forbiddenMemoryOrProjectionIdentities")):
            for identity in profile[field]:
                result, _, adapter = run_plan(
                    [item], f"scope-{operation}-{classification}",
                    scope_mutation=f"{classification}:{identity}")
                results.append({"operation": operation, "classification": classification,
                                "identity": identity, "rejected": result["status"] != "PASS",
                                "cleanupStatus": result["cleanup"]["status"],
                                "cleanupCalled": adapter.cleanup_called})
    require(results and all(value["rejected"] and value["cleanupStatus"] == "PASS"
                            and value["cleanupCalled"] for value in results),
            "one or more declared scope identities were not executably enforced")
    return {"declarationCount": len(results),
            "rejectedCount": sum(value["rejected"] for value in results),
            "results": results}


def known_defect_controls() -> dict[str, Any]:
    config = candidate.load_object(candidate.CONFIGURATION)
    controls: dict[str, bool] = {}
    controls["current-future-live-contract-valid"] = all(
        candidate.validate_future_live_contract(config, require_files=True).values())
    mutations: dict[str, dict[str, Any]] = {}
    for name in ("endpoint-mismatch", "unbound-seed-constructor", "workspace-contamination",
                 "wrong-maven-directory", "missing-maven-failure-policy", "terminal-fetch-instability"):
        mutations[name] = copy.deepcopy(config)
    mutations["endpoint-mismatch"]["futureLiveIdentity"]["runtimePorts"]["bridgePort"] = 9999
    mutations["unbound-seed-constructor"]["futureLiveIdentity"]["seedConstructor"] = "UNBOUND"
    mutations["workspace-contamination"]["futureLiveIdentity"]["workspaceIsolation"]["captureAllowlist"].append(
        mutations["workspace-contamination"]["futureLiveIdentity"]["workspaceIsolation"]["emptyWorkspace"])
    mutations["wrong-maven-directory"]["futureLiveIdentity"]["mavenPromotion"]["projectDirectory"] = "/tmp/wrong"
    mutations["missing-maven-failure-policy"]["futureLiveIdentity"]["mavenPromotion"]["rawFailureOutputAllowed"] = True
    mutations["terminal-fetch-instability"]["futureLiveIdentity"]["stableTerminalFetchCount"] = 1
    for name, value in mutations.items():
        controls[name] = expect_rejection(
            lambda candidate_value=value: candidate.validate_future_live_contract(candidate_value), name
        ) == "HarnessFailure"
    case6 = first_items_by_case()["SMA-S2-006-DUPLICATE-PERSISTED-EVENT"]
    result, _, adapter = run_plan([case6], "unrelated-backlog-case6")
    unrelated = [value for value in adapter.ports.capture_documents
                 if value.get("source") == "PREEXISTING_BACKLOG"]
    controls["unrelated-backlog-exact-key-pass"] = result["status"] == "PASS" \
        and len(unrelated) == 7
    controls["global-count-overshoot-irrelevant"] = len(adapter.captures) > 1 \
        and result["status"] == "PASS"
    key = candidate.EventKey("owned-conversation", "owned-event")
    backlog = {(f"other-{index}", f"event-{index}"): 1 for index in range(5)}
    incomplete = dict(backlog)
    complete = {**backlog, key.value(): 1}
    controls["delayed-bounded-two-read-stabilization"] = candidate.exact_capture_stabilized(
        [incomplete, complete, dict(complete)], [key], [], consecutive_reads=2)
    controls["single-read-not-stable"] = not candidate.exact_capture_stabilized(
        [complete], [key], [], consecutive_reads=2)
    controls["flapping-not-stable"] = not candidate.exact_capture_stabilized(
        [complete, incomplete, complete], [key], [], consecutive_reads=2)
    controls["full-framing-negative-controls"] = framing_controls()["negativeCount"] >= 9
    require(all(controls.values()), f"known-defect regression failed: {controls}")
    return controls


def interruption_controls() -> dict[str, Any]:
    items = first_items_by_case()
    cases = {
        "PROCESS_START_FAILURE": items["SMA-S2-002-SAME-PARTITION-DELIVERY"],
        "PROCESS_STOP_FAILURE": items["SMA-S2-001-FIRST-PROMPT-EMPTY"],
        "RECOVERY_FAILURE": items["SMA-S2-001-FIRST-PROMPT-EMPTY"],
        "HOOK_TIMEOUT": items["SMA-S2-010-HOOK-FAULT-MATRIX"],
        "HOOK_MALFORMED": items["SMA-S2-010-HOOK-FAULT-MATRIX"],
        "HOOK_HTTP_503": items["SMA-S2-010-HOOK-FAULT-MATRIX"],
        "BRIDGE_OUTAGE": items["SMA-S2-010-HOOK-FAULT-MATRIX"],
        "MODEL_TIMEOUT": items["SMA-S2-019-MODEL-STUB-FAULT-MATRIX"],
        "MODEL_MALFORMED": items["SMA-S2-019-MODEL-STUB-FAULT-MATRIX"],
        "MODEL_HTTP_503": items["SMA-S2-019-MODEL-STUB-FAULT-MATRIX"],
        "TRANSPORT_OUTAGE": items["SMA-S2-019-MODEL-STUB-FAULT-MATRIX"],
        "CANCELLATION": items["SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN"],
        "CLIENT_DISCONNECT": items["SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN"],
    }
    expected = {
        "PROCESS_START_FAILURE": "ENVIRONMENT",
        "PROCESS_STOP_FAILURE": "ENVIRONMENT",
        "RECOVERY_FAILURE": "HARNESS",
        "HOOK_TIMEOUT": "ENVIRONMENT", "HOOK_MALFORMED": "HARNESS",
        "HOOK_HTTP_503": "ENVIRONMENT", "BRIDGE_OUTAGE": "ENVIRONMENT",
        "MODEL_TIMEOUT": "ENVIRONMENT", "MODEL_MALFORMED": "HARNESS",
        "MODEL_HTTP_503": "ENVIRONMENT", "TRANSPORT_OUTAGE": "ENVIRONMENT",
        "CANCELLATION": "SAFETY", "CLIENT_DISCONNECT": "SAFETY",
    }
    results: dict[str, Any] = {}
    for failure, item in cases.items():
        result, _, adapter = run_plan([item], failure, injected_failure=failure)
        observed = (result.get("stopped") or {}).get("class")
        results[failure] = {"expected": expected[failure], "observed": observed,
                            "stopped": result["status"] == "INCONCLUSIVE",
                            "cleanupStatus": result["cleanup"]["status"],
                            "cleanupCalled": adapter.cleanup_called,
                            "primitiveBoundaryObserved": failure in getattr(
                                adapter.ports, "boundary_faults_observed", [])
                                if failure in {"HOOK_TIMEOUT", "HOOK_MALFORMED", "HOOK_HTTP_503",
                                               "BRIDGE_OUTAGE", "MODEL_TIMEOUT", "MODEL_MALFORMED",
                                               "MODEL_HTTP_503", "TRANSPORT_OUTAGE", "CANCELLATION",
                                               "CLIENT_DISCONNECT"} else True}
    journal_result, _, journal_adapter = run_plan(
        [items["SMA-S2-001-FIRST-PROMPT-EMPTY"]], "JOURNAL_FAILURE",
        fail_on_kind="PRE_ACTION_OPERATION",
    )
    results["JOURNAL_FAILURE"] = {
        "expected": "HARNESS", "observed": (journal_result.get("stopped") or {}).get("class"),
        "stopped": journal_result["status"] == "INCONCLUSIVE",
        "cleanupStatus": journal_result["cleanup"]["status"],
        "cleanupCalled": journal_adapter.cleanup_called,
    }
    journal_phase_results: dict[str, Any] = {}
    for kind in ("PRE_ACTION_SERVICE_TRANSITION", "PRE_ACTION_OPERATION",
                 "PREASSERTION_EVIDENCE", "PREASSERTION_ORACLE_BUNDLE",
                 "PRE_ACTION_RECOVERY", "OPERATION_RECOVERY",
                 "PRE_ACTION_GLOBAL_CLEANUP", "GLOBAL_CLEANUP"):
        phase_result, _, phase_adapter = run_plan(
            [items["SMA-S2-001-FIRST-PROMPT-EMPTY"]], f"journal-{kind}",
            fail_on_kind=kind)
        journal_phase_results[kind] = {
            "status": phase_result["status"], "cleanupCalled": phase_adapter.cleanup_called,
            "cleanupStatus": phase_result["cleanup"]["status"],
            "emergencyLedgerRecords": len(phase_result["emergencyLedger"]),
        }
    scientific_journal_result, _, scientific_journal_adapter = run_plan(
        [items["SMA-S2-001-FIRST-PROMPT-EMPTY"]], "journal-SCIENTIFIC_FAILURE",
        mutation="S2-001-P2", fail_on_kind="SCIENTIFIC_FAILURE")
    journal_phase_results["SCIENTIFIC_FAILURE"] = {
        "status": scientific_journal_result["status"],
        "cleanupCalled": scientific_journal_adapter.cleanup_called,
        "cleanupStatus": scientific_journal_result["cleanup"]["status"],
        "emergencyLedgerRecords": len(scientific_journal_result["emergencyLedger"]),
    }
    stop_journal_result, _, stop_journal_adapter = run_plan(
        [items["SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN"]], "journal-STOP_FAILURE",
        injected_failure="CANCELLATION", fail_on_kind="STOP_FAILURE")
    journal_phase_results["STOP_FAILURE"] = {
        "status": stop_journal_result["status"], "cleanupCalled": stop_journal_adapter.cleanup_called,
        "cleanupStatus": stop_journal_result["cleanup"]["status"],
        "emergencyLedgerRecords": len(stop_journal_result["emergencyLedger"]),
    }
    require(all(value["cleanupCalled"] and value["cleanupStatus"] == "PASS"
                and value["status"] != "PASS" for value in journal_phase_results.values()),
            f"journal-phase failure bypassed stop or cleanup: {journal_phase_results}")
    results["JOURNAL_APPEND_PHASES"] = {"phaseCount": len(journal_phase_results),
                                        "phases": journal_phase_results,
                                        "cleanupStatus": "PASS", "cleanupCalled": True}
    cleanup_result, _, cleanup_adapter = run_plan(
        [items["SMA-S2-001-FIRST-PROMPT-EMPTY"]], "CLEANUP_FAILURE",
        injected_failure="CLEANUP_FAILURE",
    )
    results["CLEANUP_FAILURE"] = {
        "expected": "SAFETY", "observed": (cleanup_result.get("stopped") or {}).get("class"),
        "emergencyCleanupUsed": cleanup_result["cleanup"]["emergencyCleanupUsed"],
        "initialCleanupFailureObserved": cleanup_result["cleanup"]["initialCleanupFailureObserved"],
        "emergencyCleanupSucceeded": cleanup_result["cleanup"]["emergencyCleanupSucceeded"],
        "cleanupStatus": cleanup_result["cleanup"]["status"],
        "cleanupCalled": cleanup_adapter.cleanup_called,
    }
    scheduled = {
        "hookFaultModes": sorted({item.mode for item in candidate.exact_plan(candidate.load_object(candidate.CORPUS))
                                  if item.operation == "HOOK_FAULT_MATRIX"}),
        "modelFaultModes": sorted({item.mode for item in candidate.exact_plan(candidate.load_object(candidate.CORPUS))
                                   if item.operation == "MODEL_STUB_FAULT_MATRIX"}),
        "cancellationMode": sorted({item.mode for item in candidate.exact_plan(candidate.load_object(candidate.CORPUS))
                                    if item.operation == "ACTIVE_CANCELLATION_AND_SHUTDOWN"}),
    }
    expected_hook = sorted(["BRIDGE_DOWN", "BRIDGE_HTTP_503", "BRIDGE_MALFORMED", "BRIDGE_TIMEOUT"])
    expected_model = sorted(["HTTP_503", "MALFORMED", "TIMEOUT", "TRANSPORT_FAILURE_UNUSED_PORT"])
    require(scheduled["hookFaultModes"] == expected_hook
            and scheduled["modelFaultModes"] == expected_model
            and scheduled["cancellationMode"] == ["TIMEOUT"], "fault schedule changed")
    require(all(value.get("cleanupStatus") == "PASS" and value.get("cleanupCalled")
                and value.get("primitiveBoundaryObserved", True)
                and ("observed" not in value or value["observed"] == value["expected"])
                for value in results.values()), f"injected interruption control failed: {results}")
    require(results["CLEANUP_FAILURE"]["initialCleanupFailureObserved"]
            and results["CLEANUP_FAILURE"]["emergencyCleanupUsed"]
            and results["CLEANUP_FAILURE"]["emergencyCleanupSucceeded"],
            "cleanup failure was not observed before emergency cleanup")
    return {"injected": results, "scheduled": scheduled}


def continuation_control() -> dict[str, Any]:
    items = first_items_by_case()
    plan = [items["SMA-S2-001-FIRST-PROMPT-EMPTY"],
            items["SMA-S2-002-SAME-PARTITION-DELIVERY"]]
    result, _, adapter = run_plan(plan, "safe-scientific-continuation", mutation="S2-001-P2")
    statuses = [value["status"] for value in result["results"]]
    require(statuses == ["FAIL", "PASS"], "safe scientific failure did not recover and continue")
    require(result["completedOperations"] == 2 and result["cleanup"]["status"] == "PASS"
            and adapter.cleanup_called, "continuation cleanup incomplete")
    recovery_result, _, recovery_adapter = run_plan(
        [items["SMA-S2-001-FIRST-PROMPT-EMPTY"], items["SMA-S2-002-SAME-PARTITION-DELIVERY"]],
        "recovery-stop", injected_failure="RECOVERY_FAILURE",
    )
    require(recovery_result["completedOperations"] == 0
            and (recovery_result["stopped"] or {}).get("class") == "HARNESS"
            and recovery_adapter.cleanup_called, "recovery failure did not stop immediately")
    return {"safeFailureStatuses": statuses,
            "recoveryFailureCompletedOperations": recovery_result["completedOperations"]}


def static_controls() -> dict[str, Any]:
    source = DRIVER.read_text() + "\n" + RAW_RUNTIME.read_text()
    tree = ast.parse(source)
    banned_import = "sma_s2_measured_driver_candidate_" in source
    banned_wait = "wait_memory_count" in source
    banned_rebind = "os.chdir" in source or "monkeypatch" in source
    assert_nodes = [node for node in ast.walk(tree) if isinstance(node, ast.Assert)]
    unconditional_evidence = any(token in source for token in (
        '"E-001": True', '"E-002": True', '"E-003": True', '"E-004": True',
        '"E-005": True', '"E-006": True', '"E-007": True', '"E-008": True',
        '"E-009": True', '"E-010": True'))
    parsed = candidate.parser().parse_args([])
    checks = {
        "predecessorMeasuredDriverImportAbsent": not banned_import,
        "totalMemoryWaitAbsent": not banned_wait,
        "globalRuntimeRebindingAbsent": not banned_rebind,
        "pythonAssertAbsent": not assert_nodes,
        "literalUnconditionalEvidenceAbsent": not unconditional_evidence,
        "defaultLiveFlagsFalse": not parsed.dress_rehearsal and not parsed.execute,
        "rawPortMethodsPresent": all(token in RAW_RUNTIME.read_text() for token in (
            "http_request", "process_action", "store_query", "filesystem_action",
            "maven_subprocess", "monotonic_ns", "sleep_ns")),
        "rawOrchestrationOwnsCriticalFlows": all(token in RAW_RUNTIME.read_text() for token in (
            "_ensure_seeded", "stable_fetches", "CAPTURE_EXACT", "PARENT_CHILD_PROVENANCE",
            "FOUR_CHANNEL_CONCURRENCY", "/condense", "/interrupt", "CLEANUP_OWNED")),
    }
    require(all(checks.values()), "static source control failed")
    return checks


def optimized_equivalence() -> dict[str, Any]:
    values: dict[str, Any] = {}
    for plan_name in ("dress", "measured"):
        normal = candidate.canonical_offline_outcome(plan_name)
        completed = subprocess.run(
            [sys.executable, "-O", str(DRIVER), "--offline-canonical", plan_name],
            check=True, capture_output=True, text=True, timeout=60,
            cwd=ROOT,
        )
        optimized = json.loads(completed.stdout)
        require(normal == optimized, f"optimized outcome changed: {plan_name}")
        values[plan_name] = {"equal": True, "sha256": candidate.sha_bytes(candidate.canonical_bytes(normal))}
    return values


def append_before_control(result: dict[str, Any], ledger: candidate.EvidenceLedger) -> dict[str, Any]:
    records = ledger.records
    operation_actions = [value for value in records if value.kind == "PRE_ACTION_OPERATION"]
    recover_actions = [value for value in records if value.kind == "PRE_ACTION_RECOVERY"]
    recover_receipts = [value for value in records if value.kind == "OPERATION_RECOVERY"]
    cleanup_actions = [value for value in records if value.kind == "PRE_ACTION_GLOBAL_CLEANUP"]
    cleanup_receipts = [value for value in records if value.kind == "GLOBAL_CLEANUP"]
    boundary_actions = [value for value in records if value.kind == "PRE_ACTION_BOUNDARY"]
    boundary_receipts = [value for value in records if value.kind in {"BOUNDARY_RESULT", "BOUNDARY_FAILURE"}]
    raw_actions = [value for value in records if value.kind == "PRE_ACTION_RAW_PORT"]
    raw_receipts = [value for value in records if value.kind in {"RAW_PORT_RESULT", "RAW_PORT_FAILURE"}]
    recovery_ordered = len(recover_actions) == len(recover_receipts) == result["completedOperations"] \
        and all(action.sequence < receipt.sequence
                and receipt.data.get("preActionRecordId") == action.record_id
                for action, receipt in zip(recover_actions, recover_receipts, strict=True))
    cleanup_ordered = len(cleanup_actions) == len(cleanup_receipts) == 1 \
        and cleanup_actions[0].sequence < cleanup_receipts[0].sequence \
        and cleanup_receipts[0].data.get("preActionRecordId") == cleanup_actions[0].record_id
    boundary_ordered = len(boundary_actions) == len(boundary_receipts) \
        and all(action.sequence < receipt.sequence
                and receipt.data.get("preActionRecordId") == action.record_id
                for action, receipt in zip(boundary_actions, boundary_receipts, strict=True))
    raw_ordered = bool(raw_actions) and len(raw_actions) == len(raw_receipts) \
        and all(action.sequence < receipt.sequence
                and receipt.data.get("preActionRecordId") == action.record_id
                for action, receipt in zip(raw_actions, raw_receipts, strict=True))
    require(len(operation_actions) == result["completedOperations"]
            and recovery_ordered and cleanup_ordered and boundary_ordered and raw_ordered,
            "append-before-action ordering incomplete")
    return {"operationPreActions": len(operation_actions),
            "recoveryPreActions": len(recover_actions),
            "cleanupPreActions": len(cleanup_actions),
            "boundaryPreActions": len(boundary_actions),
            "rawPortPreActions": len(raw_actions),
            "recoveryOrdered": recovery_ordered, "cleanupOrdered": cleanup_ordered,
            "boundaryOrdered": boundary_ordered, "rawPortOrdered": raw_ordered}


def raw_port_evolution_control(ledger: candidate.EvidenceLedger) -> dict[str, Any]:
    actions = [record.data for record in ledger.records if record.kind == "PRE_ACTION_RAW_PORT"]
    ports = {value.get("port") for value in actions}
    process_actions = {value.get("action") for value in actions if value.get("port") == "PROCESS"}
    store_kinds = {value.get("kind") for value in actions if value.get("port") == "STORE"}
    http_paths = {value.get("path") for value in actions if value.get("port") == "HTTP"}
    store_bindings = {(value.get("store"), value.get("namespace")) for value in actions
                      if value.get("port") == "STORE"}
    namespaces = candidate.load_object(candidate.CONFIGURATION)["futureLiveIdentity"]["disposableNamespaces"]
    checks = {
        "allRawPortsReached": ports == {"HTTP", "PROCESS", "STORE", "FILESYSTEM", "MAVEN", "CLOCK"},
        "processLifecycleReached": {"TRANSITION_SERVICE", "START_STUB", "CONFIGURE_BRIDGE_FAULT",
                                    "CLEAR_BRIDGE_FAULT", "CLEANUP_OWNED"}.issubset(process_actions),
        "storeEvolutionReached": {"STUB_RAW", "STUB_TERMINAL", "CAPTURE_EXACT",
                                  "MEMORY_PROVENANCE", "SECRET_SURFACES", "ACTIVE_WORK",
                                  "INVENTORY", "PROJECTION_STATE", "DELIVERY_SURFACES",
                                  "BRIDGE_FAULT", "MODEL_FAULT", "SUMMARY_EVENTS",
                                  "UNCAPTURED_EVENTS", "RECURSIVE_CAPTURES"}.issubset(store_kinds),
        "exactStoreIdentitiesReached": {
            ("MONGODB", namespaces["mongodbDatabase"]),
            ("QDRANT_SEMANTIC", namespaces["qdrantSemanticCollection"]),
            ("QDRANT_EPISODIC", namespaces["qdrantEpisodicCollection"]),
        }.issubset(store_bindings),
        "condensationReached": any(path and path.endswith("/condense") for path in http_paths),
        "interruptReached": any(path and path.endswith("/interrupt") for path in http_paths),
        "parentChildAndConversationRoutesReached": "/api/conversations" in http_paths,
        "rawSourceFixtureReached": "/candidate-10/raw-source" in http_paths,
    }
    require(all(checks.values()), f"raw-port evolution coverage incomplete: {checks}")
    return {"checks": checks, "rawCallCount": len(actions),
            "ports": sorted(str(value) for value in ports),
            "processActions": sorted(str(value) for value in process_actions),
            "storeKinds": sorted(str(value) for value in store_kinds),
            "storeBindings": [list(value) for value in sorted(store_bindings)]}


def default_deny_control() -> dict[str, Any]:
    attempts = {}
    for flag in ("--dress-rehearsal", "--execute"):
        completed = subprocess.run([sys.executable, str(DRIVER), flag],
                                   capture_output=True, text=True, timeout=30, cwd=ROOT)
        attempts[flag] = {"exitCodeNonzero": completed.returncode != 0,
                          "stderrSha256": candidate.sha_bytes(completed.stderr.encode())}
    require(all(value["exitCodeNonzero"] for value in attempts.values()),
            "live mode was not default-deny")
    live_ports = runtime.OfflineRawPorts(candidate.load_object(candidate.FRAMING))
    live_ports.live = True
    live_adapter = runtime.RawCandidate10OrchestrationAdapter(
        candidate.load_object(candidate.FRAMING), live_ports)
    attempts["live-adapter-without-fence"] = {
        "rejection": expect_rejection(
            lambda: candidate.Candidate10Runner(live_adapter, candidate.EvidenceLedger()),
            "live-adapter-without-fence",
        )
    }
    with tempfile.TemporaryDirectory(prefix="sma-s2-c10-authority-") as directory:
        root = Path(directory)
        acceptance_path = root / "acceptance.json"
        identity_path = root / "identity.json"
        authorization_path = root / "authorization.json"
        consumption_path = root / "consumption.json"
        acceptance = {"recordType": "SMA_S2_CANDIDATE_10_ACCEPTANCE", "candidate": 10,
                      "status": "ACCEPTED_FROZEN"}
        acceptance_path.write_bytes(candidate.canonical_bytes(acceptance) + b"\n")
        package_bindings = candidate.Candidate10Runner._package_bindings()
        identity = {
            "recordType": "SMA_S2_CANDIDATE_10_EXECUTION_IDENTITY", "candidate": 10,
            "status": "ACCEPTED_FROZEN_EXECUTION_IDENTITY", "liveExecutionAuthorized": False,
            "packageBindings": package_bindings,
            "candidateAcceptance": {"path": str(acceptance_path),
                                    "sha256": candidate.sha_file(acceptance_path)},
        }
        identity_path.write_bytes(candidate.canonical_bytes(identity) + b"\n")
        authorization = {
            "recordType": "SMA_S2_CANDIDATE_10_LIVE_AUTHORIZATION", "candidate": 10,
            "status": "AUTHORIZED_NOT_CONSUMED",
            "executionIdentitySha256": candidate.sha_file(identity_path),
            "executionMode": "ZERO_CREDIT_DRESS", "plannedOperations": 34,
            "measuredCredit": 0, "maximumAttempts": 1, "automaticReruns": 0,
            "realModelCallsAllowed": False,
        }
        authorization_path.write_bytes(candidate.canonical_bytes(authorization) + b"\n")
        fence = candidate.LiveExecutionFence(identity_path, authorization_path, consumption_path)
        validated = fence.validate(plan_name="dress", operation_count=34, measured_credit=0,
                                   package_bindings=package_bindings)
        candidate.Candidate10Runner(live_adapter, candidate.EvidenceLedger(), live_fence=fence)
        attempts["valid-file-bound-fence"] = {"accepted": True,
                                               "mode": validated["executionMode"]}
        invalid: dict[str, str] = {}
        for name, mutate_identity, mutate_authorization, plan_name, count, credit in (
            ("changed-package", {"packageBindings": {**package_bindings, "driverSha256": "0" * 64}}, {}, "dress", 34, 0),
            ("stale-identity", {"status": "STALE"}, {}, "dress", 34, 0),
            ("over-broad", {}, {"realModelCallsAllowed": True}, "dress", 34, 0),
            ("identity-mismatch", {}, {"executionIdentitySha256": "0" * 64}, "dress", 34, 0),
            ("mode-mismatch", {}, {"executionMode": "MEASURED"}, "dress", 34, 0),
            ("count-mismatch", {}, {}, "dress", 33, 0),
            ("credit-mismatch", {}, {}, "dress", 34, 103),
        ):
            candidate_identity = copy.deepcopy(identity)
            candidate_identity.update(mutate_identity)
            identity_path.write_bytes(candidate.canonical_bytes(candidate_identity) + b"\n")
            candidate_authorization = copy.deepcopy(authorization)
            candidate_authorization.update(mutate_authorization)
            candidate_authorization["executionIdentitySha256"] = mutate_authorization.get(
                "executionIdentitySha256", candidate.sha_file(identity_path))
            authorization_path.write_bytes(candidate.canonical_bytes(candidate_authorization) + b"\n")
            invalid[name] = expect_rejection(
                lambda pn=plan_name, n=count, c=credit: fence.validate(
                    plan_name=pn, operation_count=n, measured_credit=c,
                    package_bindings=package_bindings), name)
        identity_path.write_bytes(candidate.canonical_bytes(identity) + b"\n")
        authorization["executionIdentitySha256"] = candidate.sha_file(identity_path)
        authorization_path.write_bytes(candidate.canonical_bytes(authorization) + b"\n")
        validated = fence.validate(plan_name="dress", operation_count=34, measured_credit=0,
                                   package_bindings=package_bindings)
        fence.consume(validated)
        invalid["reused"] = expect_rejection(
            lambda: fence.validate(plan_name="dress", operation_count=34, measured_credit=0,
                                   package_bindings=package_bindings), "reused")
        invalid["missing-identity"] = expect_rejection(
            lambda: candidate.LiveExecutionFence(root / "absent.json", authorization_path,
                                                  root / "other-consumption.json").validate(
                plan_name="dress", operation_count=34, measured_credit=0,
                package_bindings=package_bindings), "missing-identity")
        require(len(invalid) == 9 and all(value in {"PermissionError", "FileNotFoundError"}
                                         for value in invalid.values()),
                "one or more authority-fence mutations were accepted")
        attempts["authority-negative-controls"] = invalid
    return attempts


def qualification_main() -> int:
    arguments = argparse.ArgumentParser()
    arguments.add_argument("--output", type=Path, required=True)
    args = arguments.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)

    tests: list[dict[str, Any]] = []
    tests.append(test("accepted-bindings-and-cardinalities", candidate.validate_bindings))
    tests.append(test("complete-context-framing-grammar-and-mutations", framing_controls))
    tests.append(test("raw-boundary-bytes-recomputed-against-claims", raw_boundary_controls))
    tests.append(test("raw-semantic-receipt-mutations-rejected", raw_semantic_controls))
    tests.append(test("consolidated-source-static-controls", static_controls))
    tests.append(test("live-modes-default-deny", default_deny_control))

    corpus = candidate.load_object(candidate.CORPUS)
    dress_result, dress_ledger, dress_adapter = run_plan(candidate.dress_plan(corpus), "dress-h0")
    measured_result, measured_ledger, measured_adapter = run_plan(candidate.exact_plan(corpus), "measured-h0")
    write_once(args.output / "dress-integrated-walk.json", dress_result)
    write_once(args.output / "measured-integrated-walk.json", measured_result)
    write_jsonl(args.output / "dress-evidence.jsonl", dress_ledger.canonical_records())
    write_jsonl(args.output / "measured-evidence.jsonl", measured_ledger.canonical_records())

    tests.append(test("integrated-34-operation-walk", lambda: {
        "status": dress_result["status"], "completed": dress_result["completedOperations"],
        "handlers": len(dress_result["handlerReach"]), "cleanup": dress_result["cleanup"]["status"],
        "cleanupCalled": dress_adapter.cleanup_called,
        **({} if dress_result["status"] == "PASS" and dress_result["completedOperations"] == 34
               and len(dress_result["handlerReach"]) == 20 and dress_adapter.cleanup_called
           else (_ for _ in ()).throw(candidate.HarnessFailure("34-operation walk incomplete"))),
    }))
    tests.append(test("integrated-103-repetition-walk", lambda: {
        "status": measured_result["status"], "completed": measured_result["completedOperations"],
        "handlers": len(measured_result["handlerReach"]), "cleanup": measured_result["cleanup"]["status"],
        "cleanupCalled": measured_adapter.cleanup_called,
        **({} if measured_result["status"] == "PASS" and measured_result["completedOperations"] == 103
               and len(measured_result["handlerReach"]) == 20 and measured_adapter.cleanup_called
           else (_ for _ in ()).throw(candidate.HarnessFailure("103-repetition walk incomplete"))),
    }))
    tests.append(test("append-before-operation-recovery-and-cleanup",
                      lambda: append_before_control(measured_result, measured_ledger)))
    tests.append(test("raw-port-workflow-evolution-coverage",
                      lambda: raw_port_evolution_control(measured_ledger)))

    matrix = candidate.load_object(candidate.MATRIX)
    predicate_mutations, evidence_mutations = mutation_controls(matrix)
    write_once(args.output / "predicate-mutation-results.json", predicate_mutations)
    write_once(args.output / "evidence-mutation-results.json", evidence_mutations)
    tests.append(test("59-of-59-predicate-mutations-rejected", lambda: {
        "mutationCount": len(predicate_mutations),
        "rejectedCount": sum(value["rejected"] for value in predicate_mutations),
        **({} if len(predicate_mutations) == sum(value["rejected"] for value in predicate_mutations) == 59
           else (_ for _ in ()).throw(candidate.HarnessFailure("predicate mutation deficit"))),
    }))
    tests.append(test("10-of-10-evidence-false-and-missing-mutations-rejected", lambda: {
        "mutationClassCount": len(evidence_mutations),
        "falseRejected": sum(value["falseRejected"] for value in evidence_mutations),
        "missingRejected": sum(value["missingRejected"] for value in evidence_mutations),
        **({} if len(evidence_mutations) == 10
               and sum(value["falseRejected"] for value in evidence_mutations) == 10
               and sum(value["missingRejected"] for value in evidence_mutations) == 10
           else (_ for _ in ()).throw(candidate.HarnessFailure("evidence mutation deficit"))),
    }))

    trace = traceability()
    write_once(args.output / "traceability-and-mutation-matrix.json", trace)
    tests.append(test("69-requirement-traceability-closed", lambda: {
        "predicateCount": len(trace["predicates"]),
        "evidenceCount": len(trace["requiredEvidence"]),
        **({} if len(trace["predicates"]) == 59 and len(trace["requiredEvidence"]) == 10
           else (_ for _ in ()).throw(candidate.HarnessFailure("traceability incomplete"))),
    }))
    tests.append(test("all-scope-profile-identities-executably-enforced", scope_profile_controls))
    tests.append(test("known-defect-regression-controls", known_defect_controls))
    tests.append(test("typed-fault-interruption-and-cleanup-controls", interruption_controls))
    tests.append(test("scientific-continuation-and-recovery-stop", continuation_control))
    tests.append(test("normal-and-optimized-outcomes-identical", optimized_equivalence))

    passed = sum(value["status"] == "PASS" for value in tests)
    artifacts = {}
    for name in ("dress-integrated-walk.json", "measured-integrated-walk.json",
                 "dress-evidence.jsonl", "measured-evidence.jsonl",
                 "predicate-mutation-results.json", "evidence-mutation-results.json",
                 "traceability-and-mutation-matrix.json"):
        path = args.output / name
        artifacts[name] = {"sha256": candidate.sha_file(path), "bytes": path.stat().st_size}
    receipt = {
        "schemaVersion": "1.0.0-candidate",
        "recordType": "SMA_S2_CANDIDATE_10_H0_QUALIFICATION_RECEIPT",
        "candidate": 10,
        "status": "PASS" if passed == len(tests) else "FAIL",
        "testCount": len(tests), "passCount": passed, "failCount": len(tests) - passed,
        "metrics": {
            "dressOperations": dress_result["completedOperations"],
            "measuredRepetitions": measured_result["completedOperations"],
            "handlersReached": len(measured_result["handlerReach"]),
            "lifecycleTransitionsReached": len(measured_result["transitionReach"]),
            "scientificPredicateMutationsRejected": sum(value["rejected"] for value in predicate_mutations),
            "evidenceFalseMutationsRejected": sum(value["falseRejected"] for value in evidence_mutations),
            "evidenceMissingMutationsRejected": sum(value["missingRejected"] for value in evidence_mutations),
        },
        "prohibitedActivity": {
            "networkUse": "NONE", "openhandsConversations": 0,
            "smaServiceStarts": 0, "mavenInvocations": 0,
            "liveNamespaces": 0, "dressRehearsalAttempts": 0,
            "measuredExecutionAttempts": 0, "realModelCalls": 0,
            "productChanges": 0, "measuredCredit": 0,
        },
        "boundArtifacts": {
            "driverSha256": candidate.sha_file(DRIVER),
            "rawRuntimeSha256": candidate.sha_file(candidate.RAW_RUNTIME),
            "h0HarnessSha256": candidate.sha_file(H0),
            "framingContractSha256": candidate.sha_file(candidate.FRAMING),
            "stateManifestSha256": candidate.sha_file(candidate.STATE_MANIFEST),
            "configurationSha256": candidate.sha_file(candidate.CONFIGURATION),
            "authorizationSha256": candidate.sha_file(candidate.AUTHORIZATION),
            "preregistrationSha256": candidate.sha_file(candidate.PREREGISTRATION),
            "preregistrationAcceptanceSha256": candidate.sha_file(candidate.PREREGISTRATION_ACCEPTANCE),
            "corpusSha256": candidate.sha_file(candidate.CORPUS),
            "oracleMatrixSha256": candidate.sha_file(candidate.MATRIX),
        },
        "artifacts": artifacts,
        "tests": tests,
        "claim": None,
        "liveAuthority": False,
        "measuredExecutionAuthorized": False,
        "independentReviewStatus": "PENDING",
    }
    receipt_path = args.output / "h0-qualification-receipt.json"
    write_once(receipt_path, receipt)
    print(json.dumps({"status": receipt["status"], "tests": len(tests),
                      "passed": passed, "receipt": str(receipt_path)}, sort_keys=True))
    return 0 if receipt["status"] == "PASS" else 1


def main() -> int:
    try:
        return qualification_main()
    except Exception as error:
        arguments = argparse.ArgumentParser(add_help=False)
        arguments.add_argument("--output", type=Path, required=True)
        args, _ = arguments.parse_known_args()
        args.output.mkdir(parents=True, exist_ok=True)
        failure = {
            "schemaVersion": "1.0.0-candidate",
            "recordType": "SMA_S2_CANDIDATE_10_H0_QUALIFICATION_RECEIPT",
            "candidate": 10, "status": "FAIL", "claim": None,
            "unhandledFailure": {"class": type(error).__name__,
                                 "messageSha256": candidate.sha_bytes(str(error).encode())},
            "prohibitedActivity": {"networkUse": "NONE", "openhandsConversations": 0,
                                   "smaServiceStarts": 0, "mavenInvocations": 0,
                                   "liveNamespaces": 0, "dressRehearsalAttempts": 0,
                                   "measuredExecutionAttempts": 0, "realModelCalls": 0,
                                   "productChanges": 0, "measuredCredit": 0},
            "liveAuthority": False, "measuredExecutionAuthorized": False,
            "independentReviewStatus": "PENDING",
        }
        receipt_path = args.output / "h0-qualification-receipt.json"
        if not receipt_path.exists():
            write_once(receipt_path, failure)
        print(json.dumps({"status": "FAIL", "errorType": type(error).__name__,
                          "receipt": str(receipt_path)}, sort_keys=True))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
