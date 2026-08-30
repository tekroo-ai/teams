#!/usr/bin/env python3
from __future__ import annotations

from collections import Counter
from datetime import datetime, timezone
import json
from pathlib import Path
import stat

from pymongo import MongoClient

from prepare_sma_e1_ddalcu_candidate_2_execution_identity import exact_file
from preflight_sma_e1_ddalcu_candidate_1 import (
    assert_check,
    digest,
    git_status,
    git_value,
    http_get,
    launch_loaded,
    listener,
    model_ids,
    sha,
    tree_sha,
    write_exclusive,
)


ROOT = Path(__file__).resolve().parents[1]
SMA_ROOT = Path("/Users/paul/work/tekroo-ai/sma-step15-s1-p2final")
OPENHANDS_ROOT = Path("/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel")
CANVAS_ROOT = Path("/opt/homebrew/lib/node_modules/@openhands/agent-canvas")
PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4-package-identity.json"
PACKAGE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4-acceptance.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4-execution-identity.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live-launch-definition.json"
CANARY = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-case2-live-canary-adjudication/adjudication.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-live-state-preflight-v2/preflight.json"
ACTIVE = {"running", "waiting", "deleting"}


def public_http(value: dict) -> dict:
    return {key: item for key, item in value.items() if key != "json"}


def main() -> int:
    if OUTPUT.exists():
        raise RuntimeError("candidate-4 single-use preflight receipt already exists")
    checks: list[dict] = []
    observations: dict = {}
    failed: str | None = None
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    try:
        package = json.loads(PACKAGE.read_text(encoding="utf-8"))
        acceptance = json.loads(PACKAGE_ACCEPTANCE.read_text(encoding="utf-8"))
        identity = json.loads(IDENTITY.read_text(encoding="utf-8"))
        launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
        canary = json.loads(CANARY.read_text(encoding="utf-8"))
        subject = identity["identitySubject"]
        assert_check(checks, "package_identity_recomputes", digest(package["identitySubject"]) == package.get("packageIdentity") == identity.get("packageIdentity"), package.get("packageIdentity"))
        assert_check(checks, "execution_identity_recomputes", digest(subject) == identity.get("executionIdentity"), identity.get("executionIdentity"))
        assert_check(checks, "package_accepted_frozen", acceptance.get("status") == "ACCEPTED_FROZEN" and sha(PACKAGE_ACCEPTANCE) == subject["packageAcceptanceRecordSha256"], {"status": acceptance.get("status"), "sha256": sha(PACKAGE_ACCEPTANCE)})
        assert_check(checks, "execution_identity_accepted_frozen", identity.get("status") == "ACCEPTED_FROZEN_EXECUTION_IDENTITY_BY_PRINCIPAL_PROCEED_AUTHORITY", identity.get("status"))
        assert_check(checks, "case2_real_path_canary_pass", canary.get("status") == "PASS_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION" and sha(CANARY) == subject["case2RealPathCanaryAdjudication"]["sha256"], {"status": canary.get("status"), "sha256": sha(CANARY)})
        assert_check(checks, "launch_definition_bound", sha(LAUNCH) == subject["liveLaunchDefinitionSha256"], sha(LAUNCH))
        file_failures = []
        for binding in launch["boundFiles"]:
            path = Path(binding["path"])
            actual = sha(path) if path.is_file() else None
            if actual != binding["sha256"]:
                file_failures.append({"path": str(path), "expected": binding["sha256"], "actual": actual})
        assert_check(checks, "all_launch_bound_files_current", not file_failures, {"count": len(launch["boundFiles"]), "failures": file_failures})
        tree_failures = []
        for binding in launch["boundTrees"]:
            path = Path(binding["path"])
            actual = tree_sha(path) if path.is_dir() else None
            if actual != binding["sha256"]:
                tree_failures.append({"path": str(path), "expected": binding["sha256"], "actual": actual})
        assert_check(checks, "all_launch_bound_trees_current", not tree_failures, {"count": len(launch["boundTrees"]), "failures": tree_failures})
        successor = {
            "runner": exact_file(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/runner.py"),
            "liveActions": exact_file(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live_actions.py"),
            "liveEntrypoint": exact_file(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live_entrypoint.py"),
        }
        assert_check(checks, "successor_files_identity_current", successor == subject["successorLaunchFiles"], successor)
        current_git = {
            "teams": {"commit": git_value(ROOT, "HEAD^{commit}"), "tree": git_value(ROOT, "HEAD^{tree}")},
            "sma": {"commit": git_value(SMA_ROOT, "HEAD^{commit}"), "tree": git_value(SMA_ROOT, "HEAD^{tree}"), "trackedStatus": git_status(SMA_ROOT)},
            "openhands": {"commit": git_value(OPENHANDS_ROOT, "HEAD^{commit}"), "tree": git_value(OPENHANDS_ROOT, "HEAD^{tree}"), "trackedStatus": git_status(OPENHANDS_ROOT)},
        }
        assert_check(checks, "teams_git_identity_current", current_git["teams"] == subject["teams"], current_git["teams"])
        assert_check(checks, "sma_git_identity_current", current_git["sma"] == {key: subject["sma"][key] for key in ("commit", "tree", "trackedStatus")}, current_git["sma"])
        assert_check(checks, "openhands_git_identity_current", current_git["openhands"] == {key: subject["openhands"][key] for key in ("commit", "tree", "trackedStatus")}, current_git["openhands"])
        assert_check(checks, "openhands_venv_current", tree_sha(OPENHANDS_ROOT / ".venv") == subject["openhands"]["venvTreeSha256"], subject["openhands"]["venvTreeSha256"])
        assert_check(checks, "agent_canvas_tree_current", tree_sha(CANVAS_ROOT) == subject["agentCanvas"]["packageTreeSha256"], subject["agentCanvas"]["packageTreeSha256"])
        key_path = Path(subject["sessionKey"]["path"])
        key_mode = stat.S_IMODE(key_path.stat().st_mode) if key_path.is_file() else None
        key_hash = sha(key_path) if key_path.is_file() else None
        assert_check(checks, "session_key_identity_and_mode", key_hash == subject["sessionKey"]["sha256"] and key_mode == 0o600, {"sha256": key_hash, "mode": format(key_mode, "04o") if key_mode is not None else None, "secretRetained": False})
        session_key = key_path.read_text(encoding="utf-8").strip()
        ports = launch["ports"]
        port_state = {name: listener(int(port)) for name, port in ports.items()}
        port_state["mongodb"] = listener(27017)
        port_state["model"] = listener(8802)
        assert_check(checks, "required_base_ports_owned", all(port_state[name]["occupied"] for name in ("mongodb", "qdrant", "openhands", "model")), {name: port_state[name] for name in ("mongodb", "qdrant", "openhands", "model")})
        assert_check(checks, "qualification_ports_free", all(not port_state[name]["occupied"] for name in ("smaBridge", "stub", "transportFailure", "openhandsCaptureProxy")), {name: port_state[name] for name in ("smaBridge", "stub", "transportFailure", "openhandsCaptureProxy")})
        observations["ports"] = port_state
        labels = [launch["disposable"]["launchLabel"], launch["disposable"]["stubLaunchLabel"], launch["disposable"]["bridgeFaultLaunchLabel"]]
        label_state = {label: launch_loaded(label) for label in labels}
        assert_check(checks, "qualification_launch_labels_absent", not any(label_state.values()), label_state)
        ingress_health = http_get("http://127.0.0.1:8000/health")
        agent_health = http_get("http://127.0.0.1:18000/health")
        settings = http_get("http://127.0.0.1:18000/api/settings", session_key)
        assert_check(checks, "openhands_endpoints_healthy", ingress_health["status"] == agent_health["status"] == settings["status"] == 200, {"ingress": public_http(ingress_health), "agent": public_http(agent_health), "settings": public_http(settings)})
        search = http_get("http://127.0.0.1:18000/api/conversations/search?limit=100", session_key, 15)
        assert_check(checks, "openhands_conversation_search", search["status"] == 200 and isinstance(search["json"], dict), public_http(search))
        states = []
        for item in search["json"].get("items", []):
            conversation_id = str(item.get("id"))
            detail = http_get(f"http://127.0.0.1:18000/api/conversations/{conversation_id}", session_key, 15)
            assert_check(checks, f"conversation_detail_{conversation_id}", detail["status"] == 200 and isinstance(detail["json"], dict), public_http(detail))
            states.append({"id": conversation_id, "executionStatus": str(detail["json"].get("execution_status") or "unknown").lower()})
        active = [row for row in states if row["executionStatus"] in ACTIVE]
        assert_check(checks, "no_active_openhands_work", not active, active)
        observations["activeWork"] = {"inspected": len(states), "statusCounts": dict(sorted(Counter(row["executionStatus"] for row in states).items())), "active": active}
        models = http_get("http://127.0.0.1:8802/v1/models", timeout=15)
        ids = model_ids(models["json"])
        expected_model = launch["modelProfile"]["model"]
        assert_check(checks, "exact_cognition_model_ready", models["status"] == 200 and expected_model in ids, {"status": models["status"], "sha256": models["sha256"], "modelIds": ids, "expected": expected_model})
        qdrant = {}
        for name in (launch["disposable"]["semanticCollection"], launch["disposable"]["episodicCollection"]):
            qdrant[name] = public_http(http_get(f"http://127.0.0.1:6333/collections/{name}"))
        assert_check(checks, "disposable_qdrant_collections_absent", all(value["status"] == 404 for value in qdrant.values()), qdrant)
        client = MongoClient(launch["environment"]["mongoUri"], serverSelectionTimeoutMS=5000, connectTimeoutMS=5000)
        names = client.list_database_names()
        client.close()
        mongo_name = launch["disposable"]["mongoDatabase"]
        assert_check(checks, "disposable_mongo_database_absent", mongo_name not in names, {"database": mongo_name, "absent": mongo_name not in names})
        workspace = Path(launch["disposable"]["workspaceRoot"])
        owned_workspaces = [str(path) for path in workspace.iterdir() if path.name != "runtime"] if workspace.is_dir() else []
        assert_check(checks, "no_owned_operation_workspaces", not owned_workspaces, {"path": str(workspace), "retainedRuntimeEvidence": workspace.joinpath("runtime").is_dir(), "ownedOperationWorkspaces": owned_workspaces})
    except Exception as exc:
        failed = str(exc)
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_4_READ_ONLY_LIVE_STATE_PREFLIGHT_V2",
        "recordedAt": recorded,
        "status": "PASS_READ_ONLY_PREFLIGHT" if failed is None else "NO_GO_READ_ONLY_PREFLIGHT",
        "packageIdentity": json.loads(PACKAGE.read_text(encoding="utf-8")).get("packageIdentity"),
        "executionIdentity": json.loads(IDENTITY.read_text(encoding="utf-8")).get("executionIdentity"),
        "checks": checks,
        "passedChecks": sum(row["pass"] for row in checks),
        "failedChecks": [row for row in checks if not row["pass"]],
        "terminalFailure": failed,
        "observations": observations,
        "prohibitedActivity": {"serviceStartOrRestart": "NOT_RUN", "openhandsConversation": "NOT_RUN", "modelCall": "NOT_RUN", "measuredExecution": "NOT_RUN", "productionOrHistoricalDataAccess": "NOT_RUN"},
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(OUTPUT, receipt)
    print(json.dumps({"status": receipt["status"], "passedChecks": receipt["passedChecks"], "failedChecks": len(receipt["failedChecks"]), "receiptFileSha256": sha(OUTPUT), "embeddedReceiptSha256": receipt["receiptSha256"]}, sort_keys=True))
    return 0 if failed is None else 2


if __name__ == "__main__":
    raise SystemExit(main())
