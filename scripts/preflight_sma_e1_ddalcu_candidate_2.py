#!/usr/bin/env python3
from __future__ import annotations

from collections import Counter
from datetime import datetime, timezone
import json
from pathlib import Path
import stat

from pymongo import MongoClient

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
PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2-package-identity.json"
PACKAGE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2-acceptance.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2-execution-identity.json"
IDENTITY_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2-execution-identity-acceptance.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2/e1_candidate2/live-launch-definition.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-2-live-state-preflight/preflight.json"
EXPECTED_PACKAGE = "addf15192595f39f07594305f7eaa31bfb03fa771c601b6047de111d0292fd16"
EXPECTED_EXECUTION = "8aec5b1b6923264cb552edf7d9b666bc2dc304891a3b2e73dbafcbdad40a521d"
EXPECTED_PACKAGE_ACCEPTANCE_SHA = "83cf5968f367ad85b6c66ae6ce5710e1b0e5420ac0180edcf4ec6e7f80485dc6"
EXPECTED_IDENTITY_ACCEPTANCE_SHA = "59e6ff757d0e3fcd82ee547a7911053e15fe4d1501d341e694f87760f6cd4f6c"
ACTIVE = {"running", "waiting", "deleting"}


def public_http(value: dict) -> dict:
    return {key: item for key, item in value.items() if key != "json"}


def main() -> int:
    if OUTPUT.exists():
        raise RuntimeError("candidate-2 single-use preflight receipt already exists")
    checks: list[dict] = []
    observations: dict = {}
    failed: str | None = None
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    try:
        package = json.loads(PACKAGE.read_text(encoding="utf-8"))
        package_acceptance = json.loads(PACKAGE_ACCEPTANCE.read_text(encoding="utf-8"))
        identity = json.loads(IDENTITY.read_text(encoding="utf-8"))
        identity_acceptance = json.loads(IDENTITY_ACCEPTANCE.read_text(encoding="utf-8"))
        launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
        subject = identity["identitySubject"]

        assert_check(checks, "package_identity_recomputes", digest(package["identitySubject"]) == EXPECTED_PACKAGE == package.get("packageIdentity"), package.get("packageIdentity"))
        assert_check(checks, "execution_identity_recomputes", digest(subject) == EXPECTED_EXECUTION == identity.get("executionIdentity"), identity.get("executionIdentity"))
        assert_check(checks, "package_acceptance_exact", sha(PACKAGE_ACCEPTANCE) == EXPECTED_PACKAGE_ACCEPTANCE_SHA and package_acceptance.get("status") == "ACCEPTED_FROZEN", {"sha256": sha(PACKAGE_ACCEPTANCE), "status": package_acceptance.get("status")})
        assert_check(checks, "execution_identity_acceptance_exact", sha(IDENTITY_ACCEPTANCE) == EXPECTED_IDENTITY_ACCEPTANCE_SHA and identity_acceptance.get("status") == "ACCEPTED_FROZEN_EXECUTION_IDENTITY", {"sha256": sha(IDENTITY_ACCEPTANCE), "status": identity_acceptance.get("status")})
        assert_check(checks, "package_record_bound", sha(PACKAGE) == subject["packageIdentityRecordSha256"], sha(PACKAGE))
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

        current_git = {
            "teams": {"commit": git_value(ROOT, "HEAD^{commit}"), "tree": git_value(ROOT, "HEAD^{tree}")},
            "sma": {"commit": git_value(SMA_ROOT, "HEAD^{commit}"), "tree": git_value(SMA_ROOT, "HEAD^{tree}"), "trackedStatus": git_status(SMA_ROOT)},
            "openhands": {"commit": git_value(OPENHANDS_ROOT, "HEAD^{commit}"), "tree": git_value(OPENHANDS_ROOT, "HEAD^{tree}"), "trackedStatus": git_status(OPENHANDS_ROOT)},
        }
        assert_check(checks, "teams_git_identity_current", current_git["teams"] == subject["teams"], current_git["teams"])
        assert_check(checks, "sma_git_identity_current", current_git["sma"] == {"commit": subject["sma"]["commit"], "tree": subject["sma"]["tree"], "trackedStatus": subject["sma"]["trackedStatus"]}, current_git["sma"])
        assert_check(checks, "openhands_git_identity_current", current_git["openhands"] == {"commit": subject["openhands"]["commit"], "tree": subject["openhands"]["tree"], "trackedStatus": subject["openhands"]["trackedStatus"]}, current_git["openhands"])
        observations["git"] = current_git

        key_path = Path(subject["sessionKey"]["path"])
        key_mode = stat.S_IMODE(key_path.stat().st_mode) if key_path.is_file() else None
        key_hash = sha(key_path) if key_path.is_file() else None
        assert_check(checks, "session_key_identity_and_mode", key_hash == subject["sessionKey"]["sha256"] and key_mode == 0o600, {"sha256": key_hash, "mode": format(key_mode, "04o") if key_mode is not None else None, "secretRetained": False})
        session_key = key_path.read_text(encoding="utf-8").strip()

        ports = launch["ports"]
        port_state = {name: listener(int(port)) for name, port in ports.items()}
        port_state["mongodb"] = listener(27017)
        assert_check(checks, "required_base_ports_owned", all(port_state[name]["occupied"] for name in ("mongodb", "qdrant", "openhands")), {name: port_state[name] for name in ("mongodb", "qdrant", "openhands")})
        assert_check(checks, "qualification_ports_free", all(not port_state[name]["occupied"] for name in ("smaBridge", "stub", "transportFailure", "openhandsCaptureProxy")), {name: port_state[name] for name in ("smaBridge", "stub", "transportFailure", "openhandsCaptureProxy")})
        port_state["model"] = listener(8802)
        assert_check(checks, "model_port_owned", port_state["model"]["occupied"], port_state["model"])
        observations["ports"] = port_state

        labels = [launch["disposable"]["launchLabel"], launch["disposable"]["stubLaunchLabel"], launch["disposable"]["bridgeFaultLaunchLabel"]]
        label_state = {label: launch_loaded(label) for label in labels}
        assert_check(checks, "qualification_launch_labels_absent", not any(label_state.values()), label_state)
        observations["qualificationLaunchLabels"] = label_state

        ingress_health = http_get("http://127.0.0.1:8000/health")
        agent_health = http_get("http://127.0.0.1:18000/health")
        settings = http_get("http://127.0.0.1:18000/api/settings", session_key)
        assert_check(checks, "openhands_ingress_health", ingress_health["status"] == 200, public_http(ingress_health))
        assert_check(checks, "openhands_agent_server_health", agent_health["status"] == 200, public_http(agent_health))
        assert_check(checks, "openhands_authenticated_settings", settings["status"] == 200, public_http(settings))
        observations["openhandsEndpoints"] = {"ingressHealth": public_http(ingress_health), "agentServerHealth": public_http(agent_health), "authenticatedSettings": public_http(settings)}

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
        assert_check(checks, "exact_cognition_model_ready", models["status"] == 200 and expected_model in ids, {"status": models["status"], "bytes": models["bytes"], "sha256": models["sha256"], "modelIds": ids, "expected": expected_model})
        observations["modelEndpoint"] = {"apiRoot": launch["modelProfile"]["apiRoot"], "modelsStatus": models["status"], "modelsSha256": models["sha256"], "modelIds": ids, "modelCallPerformed": False}

        qdrant = {}
        for name in (launch["disposable"]["semanticCollection"], launch["disposable"]["episodicCollection"]):
            response = http_get(f"http://127.0.0.1:6333/collections/{name}")
            qdrant[name] = public_http(response)
        assert_check(checks, "disposable_qdrant_collections_absent", all(value["status"] == 404 for value in qdrant.values()), qdrant)
        mongo_client = MongoClient(launch["environment"]["mongoUri"], serverSelectionTimeoutMS=5000, connectTimeoutMS=5000)
        names = mongo_client.list_database_names()
        mongo_client.close()
        mongo_name = launch["disposable"]["mongoDatabase"]
        assert_check(checks, "disposable_mongo_database_absent", mongo_name not in names, {"database": mongo_name, "absent": mongo_name not in names})
        workspace = Path(launch["disposable"]["workspaceRoot"])
        assert_check(checks, "disposable_workspace_absent", not workspace.exists(), {"path": str(workspace), "absent": not workspace.exists()})
        observations["disposableState"] = {"mongoDatabase": mongo_name, "mongoDatabaseAbsent": mongo_name not in names, "qdrantCollections": qdrant, "workspaceRoot": str(workspace), "workspaceRootAbsent": not workspace.exists()}
    except Exception as exc:
        failed = str(exc)

    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_2_READ_ONLY_LIVE_STATE_PREFLIGHT",
        "recordedAt": recorded,
        "status": "PASS_READ_ONLY_PREFLIGHT" if failed is None else "NO_GO_READ_ONLY_PREFLIGHT",
        "packageIdentity": EXPECTED_PACKAGE,
        "executionIdentity": EXPECTED_EXECUTION,
        "acceptance": {"packageAcceptanceSha256": sha(PACKAGE_ACCEPTANCE), "executionIdentityAcceptanceSha256": sha(IDENTITY_ACCEPTANCE)},
        "checks": checks,
        "observations": observations,
        "failedPrecondition": failed,
        "prohibitedActivity": {"serviceStartOrRestart": "NOT_RUN", "openhandsConversationMutation": "NOT_RUN", "modelGeneration": "NOT_RUN", "measuredExecution": "NOT_RUN", "productionOrHistoricalDataMutation": "NOT_RUN"},
        "verdict": "GO_FOR_SEPARATELY_AUTHORIZED_MEASURED_EXECUTION" if failed is None else "NO_GO_STOPPED",
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(OUTPUT, receipt)
    print(json.dumps({"status": receipt["status"], "failedPrecondition": failed, "receiptFileSha256": sha(OUTPUT), "embeddedReceiptSha256": receipt["receiptSha256"]}, sort_keys=True))
    return 0 if failed is None else 2


if __name__ == "__main__":
    raise SystemExit(main())
