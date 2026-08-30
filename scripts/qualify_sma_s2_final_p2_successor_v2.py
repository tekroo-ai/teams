#!/usr/bin/env python3
"""Complete no-live H0 qualification for the final-P2 S2 successor v2."""

from __future__ import annotations

import argparse
import base64
import json
import plistlib
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

import sma_s2_final_p2_successor_v2 as s2


SCIENCE_MUTATIONS: dict[str, str] = {
    "S2-001-P1": "HOOK_SEQUENCE_AFTER", "S2-001-P2": "CONTEXT_INJECT",
    "S2-001-P3": "PROMPT_MUTATE", "S2-001-P4": "RAW_OMIT",
    "S2-002-P1": "CONTEXT_EMPTY", "S2-002-P2": "CONTEXT_HEADER", "S2-002-P3": "PROMPT_MUTATE",
    "S2-003-P1": "CROSS_LEAK",
    "S2-004-P1": "CONTEXT_EMPTY", "S2-004-P2": "SEG0_SPLICE", "S2-004-P3": "PROMPT_MUTATE",
    "S2-005-P1": "RAW_LEAK",
    "S2-006-P1": "DUPLICATE_RECONCILE", "S2-006-P2": "CAPTURE_DUPLICATE",
    "S2-007-P1": "BRIDGE_SLOW", "S2-007-P2": "CONTEXT_INJECT", "S2-007-P3": "FAULT_ACTIVATION_SUCCESS",
    "S2-008-P1": "PROMPT_MUTATE", "S2-008-P2": "OUTAGE_EVENT_OMIT", "S2-008-P3": "CAPTURE_OMIT",
    "S2-009-P1": "PROMPT_MUTATE", "S2-009-P2": "CONTEXT_EMPTY", "S2-009-P3": "TRACE_MUTATE",
    "S2-010-P1": "BRIDGE_SLOW", "S2-010-P2": "CONTEXT_INJECT",
    "S2-010-P3": "HOOK_DUPLICATE", "S2-010-P4": "RAW_OMIT",
    "S2-011-P1": "PARENT_MUTATE", "S2-011-P2": "MEMORY_PARTITION",
    "S2-012-P1": "PARTITION_MUTATE", "S2-012-P2": "CONTEXT_EMPTY", "S2-012-P3": "CAPTURE_OMIT",
    "S2-013-P1": "STATUS_RUNNING", "S2-013-P2": "CROSS_LEAK", "S2-013-P3": "CHANNELS_SERIALIZED",
    "S2-014-P1": "FORCE_INELIGIBLE_CAPTURE", "S2-014-P2": "SECOND_LOOP",
    "S2-015-P1": "PROMPT_REDACT", "S2-015-P2": "SECRET_LEAK", "S2-015-P3": "RAW_OMIT",
    "S2-016-P1": "CONTEXT_INJECT", "S2-016-P2": "PROMPT_MUTATE",
    "S2-017-P1": "CONTEXT_MALFORMED", "S2-017-P2": "CONTEXT_OVERSIZED",
    "S2-017-P3": "CONTEXT_OVERSIZED",
    "S2-018-P1": "CONTEXT_EMPTY", "S2-018-P2": "SUMMARY_OMIT", "S2-018-P3": "TERMINAL_OMIT",
    "S2-019-P1": "MODE_WRONG", "S2-019-P2": "SECOND_LOOP", "S2-019-P3": "STATUS_RUNNING",
    "S2-019-P4": "PROMPT_MUTATE", "S2-019-P5": "TIMING_OVER",
    "S2-020-P1": "RAW_OMIT", "S2-020-P2": "STATUS_FINISHED", "S2-020-P3": "DISCONNECT_WRONG",
    "S2-020-P4": "TIMING_OVER", "S2-020-P5": "CLEANUP_ORPHAN", "S2-020-P6": "EXTENDED_MISMATCH",
}

EVIDENCE_MUTATIONS: dict[str, tuple[int, int, str]] = {
    "E-001": (1, 1, "STUB_MODEL_MUTATE"),
    "E-002": (2, 1, "PROMPT_MUTATE"),
    "E-003": (2, 1, "TRACE_MUTATE"),
    "E-004": (2, 1, "RAW_OMIT"),
    "E-005": (2, 1, "TERMINAL_OMIT"),
    "E-006": (19, 1, "TIMING_ZERO"),
    "E-007": (2, 1, "SEQUENCE_DUPLICATE"),
    "E-008": (2, 1, "HOOK_RESULT_OMIT"),
}


def test(name: str, passed: bool, evidence: Any = None) -> dict[str, Any]:
    return {"name": name, "status": "PASS" if passed else "FAIL", "evidence": evidence}


def case_number(case_id: str) -> int:
    return int(case_id.split("-")[2])


def run_walk(mode: str) -> tuple[dict[str, Any], s2.Runner, s2.OfflinePorts]:
    cfg = s2.read_json(s2.CONFIG_PATH); ports = s2.OfflinePorts(cfg); runner = s2.Runner(ports, cfg, mode)
    return runner.execute(), runner, ports


def run_target(case_id: str, repetition: int, mutation: str) -> dict[str, Any]:
    cfg = s2.read_json(s2.CONFIG_PATH); matrix = s2.read_json(s2.MATRIX_PATH)
    number = case_number(case_id); ports = s2.OfflinePorts(cfg, f"{number}:{mutation}")
    runner = s2.Runner(ports, cfg, "dress"); observation = None; error = None
    try:
        runner.setup()
        case = next(item for item in runner.corpus["cases"] if item["id"] == case_id)
        observation = runner.perform(case, repetition)
    except Exception as caught:
        error = f"{type(caught).__name__}:{caught}"
    finally:
        try: runner.cleanup()
        except Exception as caught:
            error = error or f"{type(caught).__name__}:{caught}"
    grades = {} if observation is None else s2.grade(observation, matrix, cfg)
    return {"caseId": case_id, "repetition": repetition, "mutation": mutation,
            "grades": grades, "typedRejection": error, "rawReceiptCount": len(ports.receipts)}


def science_controls() -> list[dict[str, Any]]:
    matrix = s2.read_json(s2.MATRIX_PATH); outcomes = []
    for case in matrix["cases"]:
        number = case_number(case["caseId"])
        repetition = 1
        for oracle in case["predicateOracleIds"]:
            outcome = run_target(case["caseId"], repetition, SCIENCE_MUTATIONS[oracle])
            rejected = outcome["typedRejection"] is not None or outcome["grades"].get(oracle) is False
            outcomes.append({"oracleId": oracle, "status": "PASS" if rejected else "FAIL",
                             "rawMutation": SCIENCE_MUTATIONS[oracle],
                             "rejection": outcome["typedRejection"],
                             "targetValue": outcome["grades"].get(oracle),
                             "rawReceiptCount": outcome["rawReceiptCount"]})
    return outcomes


def evidence_controls() -> list[dict[str, Any]]:
    outcomes = []
    for oracle, (number, repetition, mutation) in EVIDENCE_MUTATIONS.items():
        case_id = next(x["id"] for x in s2.read_json(s2.CORPUS_PATH)["cases"] if case_number(x["id"]) == number)
        outcome = run_target(case_id, repetition, mutation)
        rejected = outcome["typedRejection"] is not None or outcome["grades"].get(oracle) is False
        outcomes.append({"oracleId": oracle, "status": "PASS" if rejected else "FAIL",
                         "rawMutation": mutation, "rejection": outcome["typedRejection"],
                         "targetValue": outcome["grades"].get(oracle),
                         "rawReceiptCount": outcome["rawReceiptCount"]})
    cfg = s2.read_json(s2.CONFIG_PATH); ports = s2.OfflinePorts(cfg, "0:GLOBAL_CLEANUP_ORPHAN")
    receipt = s2.Runner(ports, cfg, "dress").execute()
    for oracle in ("E-009", "E-010"):
        rejected = receipt["globalEvidence"].get(oracle) is False and receipt["status"] == "FAIL"
        outcomes.append({"oracleId": oracle, "status": "PASS" if rejected else "FAIL",
                         "rawMutation": "GLOBAL_CLEANUP_ORPHAN", "targetValue": receipt["globalEvidence"].get(oracle)})
    return outcomes


def authority_controls() -> dict[str, Any]:
    cfg = s2.read_json(s2.CONFIG_PATH); result = {"missing": False, "fabricated": False,
                                                  "modeMismatch": False, "reuse": False, "valid": False}
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory); runner_sha = s2.digest(Path(s2.__file__).read_bytes())
        base = {"recordType": "SMA_S2_FINAL_P2_V2_SINGLE_USE_AUTHORIZATION",
                "status": "ACCEPTED_SINGLE_USE_NOT_CONSUMED",
                "configurationSha256": s2.digest(s2.CONFIG_PATH.read_bytes()), "runnerSha256": runner_sha,
                "maximumAttempts": 1, "automaticReruns": 0, "mode": "ZERO_CREDIT_DRESS"}
        missing = root / "missing.json"
        try: s2.LivePorts(cfg, missing)
        except (FileNotFoundError, PermissionError): result["missing"] = True
        fabricated = root / "fabricated.json"; fabricated.write_bytes(s2.canonical({**base, "runnerSha256": "0" * 64}) + b"\n")
        try: s2.LivePorts(cfg, fabricated)
        except PermissionError: result["fabricated"] = True
        valid = root / "valid.json"; valid.write_bytes(s2.canonical(base) + b"\n")
        port = s2.LivePorts(cfg, valid); port.consume("ZERO_CREDIT_DRESS"); result["valid"] = True
        try: s2.LivePorts(cfg, valid)
        except PermissionError: result["reuse"] = True
        mismatch = root / "mismatch.json"; mismatch.write_bytes(s2.canonical(base) + b"\n")
        try: s2.LivePorts(cfg, mismatch).consume("MEASURED_103")
        except PermissionError: result["modeMismatch"] = True
    return result


def raw_schema_control(runner: s2.Runner) -> dict[str, Any]:
    cfg = runner.config; creates = []; submits = []; contexts = []; unsupported = []
    allowed = (cfg["protocol"]["settings"], cfg["protocol"]["hooks"], cfg["protocol"]["conversations"],
               "/events", "/events/search", "/interrupt", "/condense", "/healthz", "/collections/",
               cfg["protocol"]["context"], cfg["deterministicStub"]["healthPath"])
    for row in runner.ledger:
        if row["kind"] != "RAW_RECEIPT" or row["rawReceipt"]["kind"] != "HTTP": continue
        raw = row["rawReceipt"]; action = raw["action"]
        if not any(token in action for token in allowed): unsupported.append(action)
        body = json.loads(base64.b64decode(raw["request"]["bodyBase64"])) if raw["request"]["bodyLength"] else {}
        if action == "POST " + cfg["endpoints"]["openhands"] + cfg["protocol"]["conversations"]: creates.append(body)
        if action.endswith("/events"): submits.append(body)
        if cfg["protocol"]["context"] in action: contexts.append(body)
    create_ok = all(set(x).issuperset({"agent_settings", "secrets_encrypted", "workspace", "worktree",
                                       "max_iterations", "autotitle", "hook_config"}) for x in creates)
    stub = cfg["deterministicStub"]
    stub_settings_ok = all(
        x["agent_settings"]["llm"].get("model", "").startswith(stub["model"] + "-case")
        and x["agent_settings"]["llm"].get("base_url") in {stub["baseUrl"], f"http://{stub['host']}:{stub['unusedTransportFailurePort']}/v1"}
        and x["agent_settings"]["llm"].get("num_retries") == 0
        and stub["faultModeHeader"] in x["agent_settings"]["llm"].get("extra_headers", {})
        and x.get("secrets_encrypted") is False and x.get("max_iterations") == 1
        for x in creates)
    submit_ok = all(set(x) == {"role", "run", "content"} and x["role"] == "user"
                    and isinstance(x["run"], bool) and isinstance(x["content"], list) for x in submits)
    context_ok = all(set(x) == {"session_id", "working_dir", "prompt"} for x in contexts)
    return {"createCount": len(creates), "submitCount": len(submits),
            "sourceEventSubmitCount": sum(x["run"] is False for x in submits),
            "modelSubmitCount": sum(x["run"] is True for x in submits), "contextCount": len(contexts),
            "createShapes": create_ok, "submitShapes": submit_ok, "contextShapes": context_ok,
            "stubSettingsBound": stub_settings_ok, "uniqueBoundModels": len({x["agent_settings"]["llm"]["model"] for x in creates}) == len(creates),
            "unsupportedRoutes": unsupported}


def delayed_visibility_control() -> dict[str, Any]:
    outcome = run_target("SMA-S2-002-SAME-PARTITION-DELIVERY", 1, "DELAY_VISIBILITY")
    return {"passed": outcome["typedRejection"] is None and all(outcome["grades"].values()),
            "rawReceiptCount": outcome["rawReceiptCount"], "error": outcome["typedRejection"]}


def fixture_seed_control(runner: s2.Runner) -> dict[str, Any]:
    promotions = [x for x in runner.ledger if x["kind"] == "PRE_ACTION" and x.get("boundary") == "PROCESS"
                  and x.get("argv", [""])[0] == "mvn"]
    expected = set(runner.fixture_ids)
    exact = {"mem-alpha-timeout", "mem-alpha-adversarial", "mem-alpha-raw", "mem-beta-port",
             *(f"mem-alpha-large-{index}" for index in range(1, 6))}
    return {"fixtureCount": len(runner.fixture_ids), "sourceEventCount": len(runner.fixture_source_events),
            "promotionCount": len(promotions), "logicalIdsExact": expected == exact,
            "promotionArgumentsComplete": all(any(a.startswith("-Dwp5e.memoryId=") for a in x["argv"])
                and any(a.startswith("-Dwp5e.code=") for a in x["argv"])
                and any(a.startswith("-Dwp5e.checksum=") for a in x["argv"])
                and x.get("cwd") == runner.config["paths"]["mavenProject"] for x in promotions)}


def packaged_runtime_control(runner: s2.Runner) -> dict[str, Any]:
    writes = {x["rawReceipt"]["request"]["path"]: plistlib.loads(base64.b64decode(x["rawReceipt"]["request"]["bodyBase64"]))
              for x in runner.ledger if x["kind"] == "RAW_RECEIPT" and x["rawReceipt"]["kind"] == "FILESYSTEM"
              and x["rawReceipt"]["action"].startswith("WRITE ")}
    cfg = runner.config; p = cfg["paths"]; n = cfg["namespaces"]; stub = cfg["deterministicStub"]
    sma = writes.get(p["launchdPlist"], {}); model = writes.get(p["stubLaunchdPlist"], {})
    bridge = [document for path, document in writes.items() if Path(path).name.startswith("bridge-fault-")]
    allowlist = (sma.get("EnvironmentVariables") or {}).get("SMA_OPENHANDS_WORKSPACE_ALLOWLIST", "").split(",")
    process_actions = [x.get("argv", []) for x in runner.ledger if x["kind"] == "PRE_ACTION" and x.get("boundary") == "PROCESS"]
    return {"ownedPlistsRetained": len(writes) == 2 + len(bridge) and len(bridge) == 4,
            "bridgeFaultProgramsBound": all(str(s2.ROOT / p["bridgeFaultScript"]) in document.get("ProgramArguments", [])
                and "--journal" in document.get("ProgramArguments", [])
                and document.get("Label", "").startswith(n["bridgeFaultLaunchdLabelPrefix"] + "-")
                for document in bridge),
            "smaLabelBound": sma.get("Label") == n["launchdLabel"],
            "stubLabelBound": model.get("Label") == n["stubLaunchdLabel"],
            "smaProgramExact": sma.get("ProgramArguments") == [p["smaWrapper"], p["java"],
                "--enable-native-access=ALL-UNNAMED", "-jar", p["smaJar"]],
            "stubProgramBound": bool(model.get("ProgramArguments") and str(s2.ROOT / stub["script"]) in model["ProgramArguments"]
                                     and str(stub["port"]) in model["ProgramArguments"]),
            "captureAllowlistExact": allowlist == [p["alphaWorkspace"], p["betaWorkspace"]]
                                     and p["emptyWorkspace"] not in allowlist,
            "ownedStartsAndStops": all(any(token in " ".join(argv) for argv in process_actions)
                for token in [n["launchdLabel"], n["stubLaunchdLabel"]])
                and sum("bootstrap" in argv for argv in process_actions) >= 2
                and sum("bootout" in argv for argv in process_actions) >= 2}


def cancellation_order_control(runner: s2.Runner) -> dict[str, Any]:
    rows = runner.ledger; cases = []
    for obs in runner.observations:
        if case_number(obs["caseId"]) != 20: continue
        cid = obs["conversation"]["id"]
        raw_ordinals = [x["ordinal"] for x in rows if x["kind"] == "RAW_RECEIPT"
            and x["rawReceipt"]["kind"] == "FILESYSTEM"
            and x["rawReceipt"]["action"].startswith("READ ")
            and any(y.get("conversationId") == cid for y in s2.file_records(s2.RawReceipt(**x["rawReceipt"]))) ]
        interrupt = [x["ordinal"] for x in rows if x["kind"] == "PRE_ACTION" and x.get("action", "").endswith(f"/{cid}/interrupt")]
        cases.append(bool(raw_ordinals and interrupt and min(raw_ordinals) < interrupt[0]
                          and obs.get("ownedCleanup") == {"conversationAbsent": True, "workspaceAbsent": True}))
    return {"caseCount": len(cases), "allRawBeforeInterruptAndClean": bool(cases) and all(cases)}


def canonical_summary(receipt: dict[str, Any], science: list[dict[str, Any]], evidence: list[dict[str, Any]]) -> dict[str, Any]:
    return {"dressStatus": receipt["dress"]["status"], "dressCompleted": receipt["dress"]["completed"],
            "measuredStatus": receipt["measured"]["status"], "measuredCompleted": receipt["measured"]["completed"],
            "scienceControls": sum(x["status"] == "PASS" for x in science),
            "evidenceControls": sum(x["status"] == "PASS" for x in evidence),
            "testStatuses": [x["status"] for x in receipt["tests"]]}


def suite() -> tuple[dict[str, Any], s2.Runner, s2.Runner]:
    bindings = s2.verify_bindings(); dress, dress_runner, _ = run_walk("dress"); measured, measured_runner, _ = run_walk("measured")
    science = science_controls(); evidence = evidence_controls(); authority = authority_controls()
    schema = raw_schema_control(measured_runner); delayed = delayed_visibility_control(); cancellation = cancellation_order_control(measured_runner)
    fixtures = fixture_seed_control(measured_runner)
    runtime = packaged_runtime_control(measured_runner)
    regraded = all(s2.grade(obs, measured_runner.matrix, measured_runner.config) == obs["grades"]
                   for obs in measured_runner.observations)
    tests = [
        test("immutable-bindings-cardinalities-and-exact-dress-selector", bindings == {"dressOperations": 34, "measuredRepetitions": 103, "scientificPredicates": 59, "evidenceClasses": 10}, bindings),
        test("shared-raw-runner-exact-34-operation-dress", dress["status"] == "PASS" and dress["completed"] == 34, dress),
        test("shared-raw-runner-exact-103-repetition-plan", measured["status"] == "PASS" and measured["completed"] == 103, measured),
        test("supported-openhands-sma-and-stub-contracts", bool(schema["createCount"] and schema["submitCount"] and schema["contextCount"] and schema["createShapes"] and schema["submitShapes"] and schema["contextShapes"] and schema["stubSettingsBound"] and schema["uniqueBoundModels"] and not schema["unsupportedRoutes"]), schema),
        test("supported-source-events-captured-before-eight-bound-promotions",
             fixtures == {"fixtureCount": 9, "sourceEventCount": 9, "promotionCount": 8,
                          "logicalIdsExact": True, "promotionArgumentsComplete": True}, fixtures),
        test("packaged-owned-stub-and-sma-lifecycle", all(runtime.values()), runtime),
        test("file-bound-default-deny-single-use-live-authority", all(authority.values()), authority),
        test("all-59-scientific-oracles-reject-raw-boundary-mutations", len(science) == 59 and all(x["status"] == "PASS" for x in science), science),
        test("all-10-evidence-oracles-reject-underlying-evidence-mutations", len(evidence) == 10 and all(x["status"] == "PASS" for x in evidence), evidence),
        test("bounded-delayed-event-and-capture-visibility", delayed["passed"], delayed),
        test("active-interrupt-order-and-per-repetition-cleanup", cancellation["caseCount"] == 3 and cancellation["allRawBeforeInterruptAndClean"], cancellation),
        test("retained-observations-independently-regrade", regraded, {"observations": len(measured_runner.observations)}),
        test("no-prohibited-live-activity", dress["liveActivity"] is False and measured["liveActivity"] is False,
             {"livePreflight": "NOT_RUN", "serviceStart": "NOT_RUN", "modelCalls": "NOT_RUN", "measuredExecution": "NOT_RUN"}),
    ]
    receipt = {"recordType": "SMA_S2_FINAL_P2_SUCCESSOR_V2_OFFLINE_QUALIFICATION",
               "status": "PASS" if all(x["status"] == "PASS" for x in tests) else "FAIL",
               "bindings": bindings, "dress": dress, "measured": measured,
               "scienceMutations": science, "evidenceMutations": evidence, "tests": tests,
               "authorityBoundary": {"livePreflight": "NOT_AUTHORIZED_NOT_RUN", "serviceStart": "NOT_AUTHORIZED_NOT_RUN",
                   "dressRehearsal": "NOT_AUTHORIZED_NOT_RUN", "measuredExecution": "NOT_AUTHORIZED_NOT_RUN",
                   "modelCalls": "NOT_AUTHORIZED_NOT_RUN", "m1": "NOT_AUTHORIZED_NOT_RUN",
                   "e1": "NOT_AUTHORIZED_NOT_RUN", "productionHistorical": "NOT_AUTHORIZED_NOT_RUN"}}
    return receipt, dress_runner, measured_runner


def write_walk(output: Path, name: str, runner: s2.Runner, receipt: dict[str, Any]) -> None:
    (output / f"{name}-walk-receipt.json").write_bytes(s2.canonical(receipt) + b"\n")
    (output / f"{name}-raw-ledger.jsonl").write_bytes(b"".join(s2.canonical(x) + b"\n" for x in runner.ledger))
    (output / f"{name}-observations.jsonl").write_bytes(b"".join(s2.canonical(x) + b"\n" for x in runner.observations))


def main() -> int:
    parser = argparse.ArgumentParser(); parser.add_argument("--output", type=Path); parser.add_argument("--child", action="store_true")
    args = parser.parse_args(); receipt, dress_runner, measured_runner = suite()
    if args.child:
        print(json.dumps(canonical_summary(receipt, receipt["scienceMutations"], receipt["evidenceMutations"]), sort_keys=True))
        return 0 if receipt["status"] == "PASS" else 1
    if args.output is None: raise ValueError("--output required")
    optimized = subprocess.run([sys.executable, "-O", str(Path(__file__)), "--child"], capture_output=True, text=True, timeout=120, check=False)
    normal_summary = canonical_summary(receipt, receipt["scienceMutations"], receipt["evidenceMutations"])
    try: optimized_summary = json.loads(optimized.stdout)
    except json.JSONDecodeError: optimized_summary = None
    optimized_pass = optimized.returncode == 0 and optimized_summary == normal_summary
    receipt["tests"].append(test("complete-normal-vs-optimized-equivalence", optimized_pass,
                                 {"normal": normal_summary, "optimized": optimized_summary, "stderr": optimized.stderr}))
    receipt["status"] = "PASS" if all(x["status"] == "PASS" for x in receipt["tests"]) else "FAIL"
    args.output.mkdir(parents=True, exist_ok=False)
    write_walk(args.output, "dress", dress_runner, receipt["dress"])
    write_walk(args.output, "measured-plan", measured_runner, receipt["measured"])
    (args.output / "offline-qualification-receipt.json").write_bytes(s2.canonical(receipt) + b"\n")
    print(json.dumps({"status": receipt["status"], "tests": len(receipt["tests"]),
                      "scienceMutations": len(receipt["scienceMutations"]),
                      "evidenceMutations": len(receipt["evidenceMutations"])}, sort_keys=True))
    return 0 if receipt["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
