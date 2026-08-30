#!/usr/bin/env python3
"""No-network H0 qualification for the final-P2 SMA-S2 successor."""

from __future__ import annotations

import argparse
import copy
import json
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any, Callable

import sma_s2_final_p2_successor_v1 as s2


Mutation = tuple[str, Callable[[dict[str, Any]], None]]


def result(name: str, passed: bool, evidence: Any = None) -> dict[str, Any]:
    return {"name": name, "status": "PASS" if passed else "FAIL", "evidence": evidence}


def run_walk(mode: str) -> tuple[dict[str, Any], s2.Runner, s2.OfflineRawPorts]:
    ports = s2.OfflineRawPorts()
    runner = s2.Runner(ports, Path("/offline/not-written"), mode)
    receipt = runner.execute()
    return receipt, runner, ports


def base_observations() -> dict[str, dict[str, Any]]:
    ports = s2.OfflineRawPorts()
    runner = s2.Runner(ports, Path("/offline/not-written"), "dress")
    runner.setup()
    observations: dict[str, dict[str, Any]] = {}
    for case in runner.corpus["cases"]:
        repetition = 4 if case["id"] == "SMA-S2-019-MODEL-STUB-FAULT-MATRIX" else 1
        observations[case["id"]] = runner.perform(case, repetition)
    return observations


def mutations() -> list[Mutation]:
    def set_value(path: tuple[Any, ...], value: Any) -> Callable[[dict[str, Any]], None]:
        def apply(item: dict[str, Any]) -> None:
            target: Any = item
            for key in path[:-1]:
                target = target[key]
            target[path[-1]] = value
        return apply

    def corrupt_raw(item: dict[str, Any]) -> None:
        if item["rawStub"]:
            item["rawStub"][0]["requestBodySha256"] = "0" * 64

    def corrupt_terminal(item: dict[str, Any]) -> None:
        if item["terminalStub"]:
            item["terminalStub"][0]["requestId"] = "wrong-request"

    def zero_timing(item: dict[str, Any]) -> None:
        if item["rawStub"]:
            item["rawStub"][0]["recordedAtMonotonicNs"] = 0
        elif item["terminalEvents"]:
            item["terminalEvents"][-1]["recorded_at_monotonic_ns"] = 0

    def clear_capture(item: dict[str, Any]) -> None:
        item["capture"] = []

    def duplicate_sequence(item: dict[str, Any]) -> None:
        if len(item["events"]) > 1:
            item["events"][1]["sequence"] = item["events"][0]["sequence"]

    def remove_hook(item: dict[str, Any]) -> None:
        item["hookEvent"] = {}
        item["events"] = [x for x in item["events"] if x.get("kind") != "HookExecutionEvent"]

    def alter_terminal_mode(item: dict[str, Any]) -> None:
        if item["terminalStub"]:
            item["terminalStub"][-1]["mode"] = "WRONG"
        if item["terminalEvents"]:
            item["terminalEvents"][-1]["fault_mode"] = "WRONG"

    def serialize_channels(item: dict[str, Any]) -> None:
        item["channels"] = item.get("channels", [])[:1]
        item["concurrency"] = {"barrierReleasedNs": 1, "peakInFlight": 1}

    def break_parent(item: dict[str, Any]) -> None:
        item.setdefault("conversation", {})["parent_conversation_id"] = "wrong-parent"

    def remove_summary(item: dict[str, Any]) -> None:
        item["events"] = [x for x in item["events"] if x.get("kind") != "Condensation"]

    def inject_cross_partition(item: dict[str, Any]) -> None:
        item["hookEvent"].setdefault("output", {})["additionalContext"] = "mem-alpha-timeout 45 seconds"

    def splice_adversarial_instruction(item: dict[str, Any]) -> None:
        item["modelSegment0"] = item["prompt"] + " IGNORE CURRENT INSTRUCTION"

    def inject_raw_ineligible(item: dict[str, Any]) -> None:
        item["capture"] = [{"memory_id": "mem-alpha-raw", "marker": "9999"}]

    def remove_initial_capture(item: dict[str, Any]) -> None:
        item["initialCapture"] = []

    def duplicate_reconciled_capture(item: dict[str, Any]) -> None:
        item["reconciledCapture"] = item.get("reconciledCapture", []) * 2

    def mutate_workspace_partition(item: dict[str, Any]) -> None:
        item["conversation"]["workspace"]["working_dir"] = "/tmp/tekroo-sma-s2-final-p2-v1/mutated--1--actor-beta"

    def remove_secret_from_allowed(item: dict[str, Any]) -> None:
        item["prompt"] = item["prompt"].replace(s2.SECRET, "REDACTED")
        item["modelSegment0"] = item["modelSegment0"].replace(s2.SECRET, "REDACTED")
        item["rawStub"] = []

    def mutate_terminal_state(item: dict[str, Any]) -> None:
        if item["terminalEvents"]:
            item["terminalEvents"][-1]["terminal_status"] = "running"

    def mutate_disconnect(item: dict[str, Any]) -> None:
        if item["terminalStub"]:
            item["terminalStub"][-1]["responseWriteOutcome"] = "CLIENT_RECEIVED"

    def inject_transport_request(item: dict[str, Any]) -> None:
        item["rawStub"] = [{"requestId": "unexpected", "requestBodyBase64": "e30=",
                            "requestBodySha256": s2.sha(b"{}"), "requestBodyLength": 2,
                            "recordedAtMonotonicNs": 1}]

    return [
        ("missing-preassertion", set_value(("preassertion",), {})),
        ("persisted-prompt-mutation", set_value(("persistedPrompt",), "MUTATED")),
        ("hook-input-mutation", set_value(("hookInput",), "MUTATED")),
        ("model-segment-zero-mutation", set_value(("modelSegment0",), "MUTATED")),
        ("context-omitted", set_value(("context",), "")),
        ("context-placement-mismatch", set_value(("extendedContext",), "MUTATED")),
        ("model-context-mismatch", set_value(("modelSegment1",), "MUTATED")),
        ("oversized-context", set_value(("context",), "X" * 10001)),
        ("raw-request-omitted", set_value(("rawStub",), [])),
        ("raw-request-digest-mutated", corrupt_raw),
        ("terminal-omitted", set_value(("terminalStub",), [])),
        ("terminal-identity-mutated", corrupt_terminal),
        ("timing-zeroed", zero_timing),
        ("capture-omitted", clear_capture),
        ("event-sequence-duplicated", duplicate_sequence),
        ("hook-event-omitted", remove_hook),
        ("terminal-fault-mutated", alter_terminal_mode),
        ("channels-serialized", serialize_channels),
        ("parent-link-mutated", break_parent),
        ("summary-omitted", remove_summary),
        ("hook-cross-partition-marker-injected", inject_cross_partition),
        ("adversarial-instruction-spliced", splice_adversarial_instruction),
        ("raw-ineligible-capture-injected", inject_raw_ineligible),
        ("initial-capture-omitted", remove_initial_capture),
        ("reconciled-capture-duplicated", duplicate_reconciled_capture),
        ("outage-persistence-proof-removed", set_value(("persistedDuringOutage",), False)),
        ("workspace-partition-mutated", mutate_workspace_partition),
        ("pre-restart-partition-mutated", set_value(("partitionBefore",), "actor-beta")),
        ("ineligible-capture-created", set_value(("capture",), [{"memory_id": "recursive"}])),
        ("secret-removed-from-permitted-surfaces", remove_secret_from_allowed),
        ("stub-sealed-evidence-omitted", set_value(("rawStub",), [])),
        ("unexpected-transport-stub-request", inject_transport_request),
        ("terminal-state-nonterminal", mutate_terminal_state),
        ("disconnect-outcome-mutated", mutate_disconnect),
        ("cross-partition-marker-injected", set_value(("context",), "mem-alpha-timeout 45 seconds")),
        ("raw-ineligible-injected", set_value(("modelSegment1",), "mem-alpha-raw 9999")),
        ("secret-forbidden-surface-injected", set_value(("modelSegment1",), s2.SECRET)),
        ("resource-orphan-injected", set_value(("resourceInventory", "workspaces"), 1)),
        ("owned-cleanup-conversation-orphan", set_value(("ownedCleanup", "conversationAbsent"), False)),
        ("cleanup-failure-injected", set_value(("cleanupReceipt", "complete"), False)),
        ("emergency-cleanup-injected", set_value(("cleanupReceipt", "emergencyCleanupUsed"), True)),
        ("conversation-identity-omitted", set_value(("conversation", "id"), "")),
        ("workspace-identity-omitted", set_value(("conversation", "workspace"), {})),
        ("terminal-events-omitted", set_value(("terminalEvents",), [])),
        ("prompt-secret-removed", set_value(("prompt",), "credential removed")),
    ]


def oracle_mutation_controls(observations: dict[str, dict[str, Any]]) -> tuple[list[dict[str, Any]], int, int]:
    matrix = s2.load(s2.MATRIX)
    scientific: list[tuple[str, str]] = []
    for case in matrix["cases"]:
        scientific.extend((case["caseId"], oracle) for oracle in case["predicateOracleIds"])
    evidence = [("SMA-S2-001-FIRST-PROMPT-EMPTY", f"E-{index:03d}") for index in range(1, 11)]
    outcomes: list[dict[str, Any]] = []
    for kind, targets in [("scientific", scientific), ("evidence", evidence)]:
        for case_id, oracle in targets:
            base = observations[case_id]
            if not base["evaluation"].get(oracle, False):
                outcomes.append({"oracleId": oracle, "kind": kind, "status": "FAIL", "reason": "base-not-pass"})
                continue
            selected = None
            for mutation_name, mutate in mutations():
                candidate = copy.deepcopy(base)
                try:
                    mutate(candidate)
                    evaluated = s2.evaluate(candidate)
                    rejected = not evaluated.get(oracle, False)
                except (KeyError, IndexError, RuntimeError, TypeError, ValueError):
                    rejected = False
                if rejected:
                    selected = mutation_name
                    break
            outcomes.append({"oracleId": oracle, "kind": kind,
                             "status": "PASS" if selected else "FAIL",
                             "rawEvidenceMutation": selected})
    return outcomes, len(scientific), len(evidence)


class DelayedOfflinePorts(s2.OfflineRawPorts):
    def __init__(self) -> None:
        super().__init__()
        self.event_delay_remaining = 4
        self.capture_delay_remaining = 4

    def http(self, method: str, url: str, headers: dict[str, str], body: bytes) -> s2.HttpReceipt:
        receipt = super().http(method, url, headers, body)
        if method == "GET" and "/events/search?limit=100" in url and self.event_delay_remaining > 0:
            self.event_delay_remaining -= 1
            return dataclass_replace(receipt, response_body=s2.canonical({"items": [], "next_page_id": None}))
        return receipt

    def mongo_find(self, database: str, collection: str, selector: dict[str, Any], projection: dict[str, int]) -> list[dict[str, Any]]:
        if self.capture_delay_remaining > 0:
            self.capture_delay_remaining -= 1
            return []
        return super().mongo_find(database, collection, selector, projection)


def dataclass_replace(value: Any, **changes: Any) -> Any:
    import dataclasses
    return dataclasses.replace(value, **changes)


def api_schema_control(ports: s2.OfflineRawPorts) -> tuple[bool, dict[str, Any]]:
    creates = [x for x in ports.http_log if x.method == "POST" and x.url.endswith("/api/conversations")]
    submits = [x for x in ports.http_log if x.method == "POST" and x.url.endswith("/events")]
    allowed_routes = (
        "/api/settings", "/api/hooks", "/api/conversations", "/events/search?limit=100",
        "/events", "/condense", "/pause", "/healthz"
        , "/collections/"
    )
    no_fake_routes = all(any(token in x.url for token in allowed_routes) for x in ports.http_log)
    create_shapes = all(
        set(json.loads(x.request_body)).issuperset({"agent_settings", "secrets_encrypted", "workspace", "worktree", "max_iterations", "autotitle", "hook_config"})
        and not ({"role", "workspaceRole", "caseId", "repetition", "operation", "mode"} & set(json.loads(x.request_body)))
        for x in creates
    )
    submit_shapes = all(json.loads(x.request_body) == {
        "role": "user", "run": True,
        "content": [{"type": "text", "text": json.loads(x.request_body)["content"][0]["text"]}],
    } for x in submits)
    auth = all(x.request_headers.get("X-Session-API-Key") for x in ports.http_log if ":8000" in x.url)
    return bool(creates and submits and no_fake_routes and create_shapes and submit_shapes and auth), {
        "createRequests": len(creates), "submitRequests": len(submits),
        "noFakeRoutes": no_fake_routes, "createShapes": create_shapes,
        "submitShapes": submit_shapes, "authenticated": auth,
    }


def suite() -> dict[str, Any]:
    tests: list[dict[str, Any]] = []
    try:
        bindings = s2.verify_bindings()
        tests.append(result("immutable-bindings-and-cardinalities", True, bindings))
    except Exception as error:
        tests.append(result("immutable-bindings-and-cardinalities", False, str(error)))

    package_source = s2.Path(s2.__file__).read_text()
    tests.append(result("no-predecessor-import-or-fake-route",
                        "candidate_10" not in package_source
                        and "measured_driver_candidate" not in package_source
                        and "/candidate-10/" not in package_source))

    denied = False
    try:
        s2.LiveRawPorts()
    except PermissionError:
        denied = True
    tests.append(result("live-default-deny", denied))

    authority_controls: dict[str, bool] = {}
    with tempfile.TemporaryDirectory(prefix="sma-s2-authority-") as directory:
        authority_path = Path(directory) / "authority.json"
        valid = {
            "recordType": "SMA_S2_FINAL_P2_SINGLE_USE_AUTHORIZATION",
            "status": "ACCEPTED_SINGLE_USE_NOT_CONSUMED",
            "subjectPackageSha256": s2.sha(s2.PACKAGE.read_bytes()),
            "subjectRunnerSha256": s2.sha(Path(s2.__file__).read_bytes()),
            "mode": "ZERO_CREDIT_DRESS", "maximumAttempts": 1,
            "automaticReruns": 0,
        }
        authority_path.write_bytes(s2.canonical(valid) + b"\n")
        live = s2.LiveRawPorts(authority_path)
        authority_controls["validAccepted"] = live.authority["mode"] == "ZERO_CREDIT_DRESS"
        try:
            live.consume("MEASURED_103")
            authority_controls["modeMismatchRejected"] = False
        except PermissionError:
            authority_controls["modeMismatchRejected"] = True
        mutated = copy.deepcopy(valid)
        mutated["subjectPackageSha256"] = "0" * 64
        authority_path.write_bytes(s2.canonical(mutated) + b"\n")
        try:
            s2.LiveRawPorts(authority_path)
            authority_controls["fabricatedBindingRejected"] = False
        except PermissionError:
            authority_controls["fabricatedBindingRejected"] = True
        authority_path.write_bytes(s2.canonical(valid) + b"\n")
        authority_path.with_suffix(".json.consumed").write_text("consumed\n")
        try:
            s2.LiveRawPorts(authority_path)
            authority_controls["reuseRejected"] = False
        except PermissionError:
            authority_controls["reuseRejected"] = True
    tests.append(result("file-bound-single-use-authority-controls",
                        all(authority_controls.values()), authority_controls))

    dress, dress_runner, dress_ports = run_walk("dress")
    tests.append(result("shared-runner-34-operation-dress", dress["status"] == "PASS" and dress["completedOperations"] == 34, dress))
    measured, measured_runner, measured_ports = run_walk("measured")
    tests.append(result("shared-runner-103-repetition-measured-plan", measured["status"] == "PASS" and measured["completedOperations"] == 103, measured))

    schema_pass, schema_evidence = api_schema_control(measured_ports)
    tests.append(result("supported-openhands-api-schema", schema_pass, schema_evidence))

    observations = base_observations()
    controls, scientific_count, evidence_count = oracle_mutation_controls(observations)
    tests.append(result("59-scientific-raw-mutations",
                        scientific_count == 59 and all(x["status"] == "PASS" for x in controls if x["kind"] == "scientific"),
                        {"count": scientific_count, "controls": [x for x in controls if x["kind"] == "scientific"]}))
    tests.append(result("10-required-evidence-raw-mutations",
                        evidence_count == 10 and all(x["status"] == "PASS" for x in controls if x["kind"] == "evidence"),
                        {"count": evidence_count, "controls": [x for x in controls if x["kind"] == "evidence"]}))

    delayed_ports = DelayedOfflinePorts()
    delayed_runner = s2.Runner(delayed_ports, Path("/offline/not-written"), "dress")
    delayed_runner.setup()
    delayed_observation = delayed_runner.perform(delayed_runner.corpus["cases"][1], 1)
    tests.append(result("bounded-delayed-event-and-capture-visibility",
                        all(delayed_observation["evaluation"].values())
                        and delayed_ports.event_delay_remaining == 0
                        and delayed_ports.capture_delay_remaining == 0,
                        {"eventDelayConsumed": 4, "captureDelayConsumed": 4}))

    source_names_present = (
        "conversation_router.py" in load_source_binding_text()
        and "event_router.py" in load_source_binding_text()
        and "OpenHandsBridgeServer.java" in load_source_binding_text()
        and "sma_context_hook.py" in load_source_binding_text()
    )
    source_verified, source_evidence = verify_source_bindings()
    tests.append(result("product-source-capability-bindings-verified",
                        source_names_present and source_verified, source_evidence))

    live_activity = all(
        receipt["liveActivity"] == {"networkCalls": 0, "serviceStarts": 0,
                                     "modelCalls": 0, "mavenInvocations": 0}
        for receipt in [dress, measured]
    )
    tests.append(result("no-prohibited-live-activity", live_activity))

    canonical_summary = {
        "dress": {key: dress[key] for key in ["status", "plannedOperations", "completedOperations", "failedEvaluations", "cleanupInventory"]},
        "measured": {key: measured[key] for key in ["status", "plannedOperations", "completedOperations", "failedEvaluations", "cleanupInventory"]},
        "tests": [{"name": item["name"], "status": item["status"]} for item in tests],
        "mutationCount": len(controls),
    }
    return {"tests": tests, "canonicalSummary": canonical_summary,
            "status": "PASS" if all(item["status"] == "PASS" for item in tests) else "FAIL"}


def load_source_binding_text() -> str:
    path = s2.ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-supported-source-bindings-v1.json"
    return path.read_text() if path.exists() else ""


def verify_source_bindings() -> tuple[bool, dict[str, Any]]:
    path = s2.ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-supported-source-bindings-v1.json"
    binding = json.loads(path.read_text())
    checks: list[dict[str, Any]] = []
    oh_root = binding["openhands"]["repositoryRoot"]
    commit = binding["openhands"]["commit"]
    for source in binding["openhands"]["sources"]:
        completed = subprocess.run(
            ["git", "rev-parse", f"{commit}:{source['path']}"], cwd=oh_root,
            capture_output=True, text=True, timeout=10, check=False)
        observed = completed.stdout.strip()
        checks.append({"path": source["path"], "kind": "GIT_BLOB",
                       "expected": source["gitBlob"], "observed": observed,
                       "pass": completed.returncode == 0 and observed == source["gitBlob"]})
    sma_root = Path(binding["sma"]["immutableWorktreeRoot"])
    for source in binding["sma"]["sources"]:
        target = sma_root / source["path"]
        observed = s2.sha(target.read_bytes()) if target.exists() else None
        checks.append({"path": source["path"], "kind": "SHA256",
                       "expected": source["sha256"], "observed": observed,
                       "pass": observed == source["sha256"]})
    return all(item["pass"] for item in checks), {"checks": checks}


def optimized_equivalence(parent: dict[str, Any]) -> tuple[bool, dict[str, Any]]:
    with tempfile.TemporaryDirectory(prefix="sma-s2-final-p2-opt-") as directory:
        output = Path(directory) / "optimized.json"
        completed = subprocess.run(
            [sys.executable, "-O", str(Path(__file__).resolve()), "--optimized-child", str(output)],
            cwd=s2.ROOT, capture_output=True, text=True, timeout=120, check=False,
        )
        if completed.returncode != 0 or not output.exists():
            return False, {"exitCode": completed.returncode,
                           "stdoutSha256": s2.sha(completed.stdout),
                           "stderrSha256": s2.sha(completed.stderr)}
        child = json.loads(output.read_text())
        same = child["canonicalSummary"] == parent["canonicalSummary"] and child["status"] == parent["status"]
        return same, {"normalSha256": s2.sha(s2.canonical(parent["canonicalSummary"])),
                      "optimizedSha256": s2.sha(s2.canonical(child["canonicalSummary"])),
                      "optimizedStatus": child["status"]}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path)
    parser.add_argument("--optimized-child", type=Path)
    args = parser.parse_args()
    report = suite()
    if args.optimized_child:
        args.optimized_child.write_bytes(s2.canonical(report) + b"\n")
        return 0 if report["status"] == "PASS" else 1
    if args.output is None:
        parser.error("--output is required")
    optimized_pass, optimized_evidence = optimized_equivalence(report)
    report["tests"].append(result("complete-optimized-runtime-equivalence", optimized_pass, optimized_evidence))
    report["status"] = "PASS" if all(item["status"] == "PASS" for item in report["tests"]) else "FAIL"
    report["recordType"] = "SMA_S2_FINAL_P2_SUCCESSOR_V1_OFFLINE_QUALIFICATION"
    report["authorityBoundary"] = {
        "livePreflight": "NOT_AUTHORIZED_NOT_RUN", "serviceStart": "NOT_AUTHORIZED_NOT_RUN",
        "dressRehearsal": "NOT_AUTHORIZED_NOT_RUN", "measuredExecution": "NOT_AUTHORIZED_NOT_RUN",
        "modelCalls": "NOT_AUTHORIZED_NOT_RUN", "m1": "NOT_AUTHORIZED_NOT_RUN",
        "e1": "NOT_AUTHORIZED_NOT_RUN", "productionHistorical": "NOT_AUTHORIZED_NOT_RUN",
    }
    args.output.mkdir(parents=True, exist_ok=False)
    publication_artifacts: dict[str, str] = {}
    if report["status"] == "PASS":
        dress, dress_runner, _ = run_walk("dress")
        measured, measured_runner, _ = run_walk("measured")
        if dress["status"] != "PASS" or measured["status"] != "PASS":
            raise RuntimeError("publication walk diverged from qualification")
        for name, value in [("dress-walk-receipt.json", dress),
                            ("measured-plan-walk-receipt.json", measured)]:
            target = args.output / name
            target.write_bytes(s2.canonical(value) + b"\n")
            publication_artifacts[name] = s2.sha(target.read_bytes())
        for name, ledger in [("dress-walk-ledger.jsonl", dress_runner.ledger),
                             ("measured-plan-walk-ledger.jsonl", measured_runner.ledger)]:
            target = args.output / name
            target.write_bytes(b"".join(s2.canonical(row) + b"\n" for row in ledger))
            publication_artifacts[name] = s2.sha(target.read_bytes())
    report["retainedArtifacts"] = publication_artifacts
    receipt = args.output / "offline-qualification-receipt.json"
    receipt.write_bytes(s2.canonical(report) + b"\n")
    print(json.dumps({"status": report["status"], "tests": len(report["tests"]),
                      "passed": sum(x["status"] == "PASS" for x in report["tests"]),
                      "receipt": str(receipt), "receiptSha256": s2.sha(receipt.read_bytes())}, sort_keys=True))
    return 0 if report["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
