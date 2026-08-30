#!/usr/bin/env python3
from __future__ import annotations

from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import socket
import stat
import subprocess
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

from pymongo import MongoClient


ROOT = Path(__file__).resolve().parents[1]
SMA_ROOT = Path("/Users/paul/work/tekroo-ai/sma-step15-s1-p2final")
OPENHANDS_ROOT = Path("/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel")
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-execution-identity.json"
IDENTITY_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-execution-identity-acceptance.json"
PACKAGE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-acceptance.json"
PACKAGE_IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-package-identity.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1/e1/live-launch-definition.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-1-live-state-preflight/preflight.json"
EXPECTED_EXECUTION_IDENTITY = "b8a0fcd0ea9ff067bfcbb57b8d257bfe2977ce71a47e0831bd8fd94deed70198"
EXPECTED_IDENTITY_ACCEPTANCE_SHA256 = "0d54462a142333ebea8322a0d8e12dacc2a483d9dc47a2765ac7cfe891544d9c"
ACTIVE_STATUSES = {"running", "waiting", "deleting"}


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(value: Any) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def sha(path: Path) -> str:
    result = hashlib.sha256()
    with path.open("rb") as stream:
        while chunk := stream.read(1024 * 1024):
            result.update(chunk)
    return result.hexdigest()


def tree_sha(root: Path) -> str:
    entries = [
        [str(path.relative_to(root)), sha(path)]
        for path in sorted(root.rglob("*"))
        if path.is_file() and "__pycache__" not in path.parts and path.suffix != ".pyc"
    ]
    return digest(entries)


def git_value(root: Path, argument: str) -> str:
    return subprocess.run(
        ["git", "rev-parse", argument], cwd=root, capture_output=True, text=True, check=True
    ).stdout.strip()


def git_status(root: Path) -> list[str]:
    return subprocess.run(
        ["git", "status", "--short"], cwd=root, capture_output=True, text=True, check=True
    ).stdout.splitlines()


def http_get(url: str, key: str | None = None, timeout: float = 10) -> dict[str, Any]:
    headers = {"Accept": "application/json"}
    if key is not None:
        headers["X-Session-API-Key"] = key
    request = Request(url, method="GET", headers=headers)
    try:
        with urlopen(request, timeout=timeout) as response:
            body = response.read()
            status_code = response.status
    except HTTPError as exc:
        body = exc.read()
        status_code = exc.code
    except (URLError, TimeoutError, socket.timeout, ConnectionError, OSError) as exc:
        return {"status": None, "bytes": 0, "sha256": None, "json": None, "errorClass": type(exc).__name__}
    try:
        parsed = json.loads(body)
    except (json.JSONDecodeError, UnicodeDecodeError):
        parsed = None
    return {
        "status": status_code,
        "bytes": len(body),
        "sha256": hashlib.sha256(body).hexdigest(),
        "json": parsed,
        "errorClass": None,
    }


def listener(port: int) -> dict[str, Any]:
    process = subprocess.run(
        ["lsof", "-nP", f"-iTCP:{port}", "-sTCP:LISTEN"], capture_output=True, text=True
    )
    rows = []
    for line in process.stdout.splitlines()[1:]:
        columns = line.split()
        if len(columns) >= 9:
            rows.append({"command": columns[0], "pid": int(columns[1]), "user": columns[2], "endpoint": columns[-1]})
    return {"port": port, "occupied": bool(rows), "listeners": rows}


def launch_loaded(label: str) -> bool:
    result = subprocess.run(
        ["launchctl", "print", f"gui/{os.getuid()}/{label}"], capture_output=True, text=True
    )
    return result.returncode == 0


def assert_check(checks: list[dict[str, Any]], name: str, condition: bool, evidence: Any) -> None:
    checks.append({"name": name, "pass": bool(condition), "evidence": evidence})
    if not condition:
        raise RuntimeError(name)


def write_exclusive(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def model_ids(payload: Any) -> list[str]:
    if not isinstance(payload, dict) or not isinstance(payload.get("data"), list):
        return []
    return sorted(str(item.get("id")) for item in payload["data"] if isinstance(item, dict) and item.get("id"))


def main() -> int:
    if OUTPUT.exists():
        raise RuntimeError("single-use E1 preflight receipt already exists")
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    checks: list[dict[str, Any]] = []
    observations: dict[str, Any] = {}
    failed: str | None = None
    try:
        identity = json.loads(IDENTITY.read_text(encoding="utf-8"))
        identity_acceptance = json.loads(IDENTITY_ACCEPTANCE.read_text(encoding="utf-8"))
        package_acceptance = json.loads(PACKAGE_ACCEPTANCE.read_text(encoding="utf-8"))
        package_identity = json.loads(PACKAGE_IDENTITY.read_text(encoding="utf-8"))
        launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
        subject = identity["identitySubject"]

        assert_check(checks, "execution_identity_exact", identity.get("executionIdentity") == EXPECTED_EXECUTION_IDENTITY, identity.get("executionIdentity"))
        assert_check(checks, "execution_identity_recomputes", digest(subject) == EXPECTED_EXECUTION_IDENTITY, digest(subject))
        assert_check(checks, "execution_identity_acceptance_hash", sha(IDENTITY_ACCEPTANCE) == EXPECTED_IDENTITY_ACCEPTANCE_SHA256, sha(IDENTITY_ACCEPTANCE))
        assert_check(checks, "execution_identity_accepted_frozen", identity_acceptance.get("status") == "ACCEPTED_FROZEN_EXECUTION_IDENTITY" and identity_acceptance.get("executionIdentity") == EXPECTED_EXECUTION_IDENTITY, {"status": identity_acceptance.get("status"), "executionIdentity": identity_acceptance.get("executionIdentity")})
        assert_check(checks, "package_acceptance_current", sha(PACKAGE_ACCEPTANCE) == subject["packageAcceptanceSha256"] and package_acceptance.get("status") == "ACCEPTED_FROZEN", sha(PACKAGE_ACCEPTANCE))
        assert_check(checks, "package_identity_current", sha(PACKAGE_IDENTITY) == subject["packageIdentityRecordSha256"] and package_identity.get("contentIdentity") == subject["acceptedPackageIdentity"], sha(PACKAGE_IDENTITY))
        assert_check(checks, "launch_definition_current", sha(LAUNCH) == subject["liveLaunchDefinitionSha256"], sha(LAUNCH))

        bound_files = []
        for binding in launch["boundFiles"]:
            path = Path(binding["path"])
            actual = sha(path) if path.is_file() else None
            bound_files.append({"path": str(path), "expected": binding["sha256"], "actual": actual, "pass": actual == binding["sha256"]})
        assert_check(checks, "all_launch_bound_files_current", all(row["pass"] for row in bound_files), {"count": len(bound_files), "failures": [row for row in bound_files if not row["pass"]]})
        bound_trees = []
        for binding in launch["boundTrees"]:
            path = Path(binding["path"])
            actual = tree_sha(path) if path.is_dir() else None
            bound_trees.append({"path": str(path), "expected": binding["sha256"], "actual": actual, "pass": actual == binding["sha256"]})
        assert_check(checks, "all_launch_bound_trees_current", all(row["pass"] for row in bound_trees), {"count": len(bound_trees), "failures": [row for row in bound_trees if not row["pass"]]})

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
        required_occupied = ["mongodb", "qdrant", "openhands"]
        required_free = ["smaBridge", "stub", "transportFailure", "openhandsCaptureProxy"]
        assert_check(checks, "required_base_ports_owned", all(port_state[name]["occupied"] for name in required_occupied), {name: port_state[name] for name in required_occupied})
        assert_check(checks, "qualification_ports_free", all(not port_state[name]["occupied"] for name in required_free), {name: port_state[name] for name in required_free})
        model_port = listener(8802)
        assert_check(checks, "model_port_owned", model_port["occupied"], model_port)
        observations["ports"] = {**port_state, "model": model_port}

        qualification_labels = [launch["disposable"]["launchLabel"], launch["disposable"]["stubLaunchLabel"], launch["disposable"]["bridgeFaultLaunchLabel"]]
        label_state = {label: launch_loaded(label) for label in qualification_labels}
        assert_check(checks, "qualification_launch_labels_absent", not any(label_state.values()), label_state)
        observations["qualificationLaunchLabels"] = label_state

        ingress_health = http_get("http://127.0.0.1:8000/health")
        agent_health = http_get("http://127.0.0.1:18000/health")
        settings = http_get("http://127.0.0.1:18000/api/settings", session_key)
        assert_check(checks, "openhands_ingress_health", ingress_health["status"] == 200, {key: value for key, value in ingress_health.items() if key != "json"})
        assert_check(checks, "openhands_agent_server_health", agent_health["status"] == 200, {key: value for key, value in agent_health.items() if key != "json"})
        assert_check(checks, "openhands_authenticated_settings", settings["status"] == 200, {key: value for key, value in settings.items() if key != "json"})
        observations["openhandsEndpoints"] = {
            "ingressHealth": {key: value for key, value in ingress_health.items() if key != "json"},
            "agentServerHealth": {key: value for key, value in agent_health.items() if key != "json"},
            "authenticatedSettings": {key: value for key, value in settings.items() if key != "json"},
        }

        search = http_get("http://127.0.0.1:18000/api/conversations/search?limit=100", session_key, 15)
        assert_check(checks, "openhands_conversation_search", search["status"] == 200 and isinstance(search["json"], dict), {key: value for key, value in search.items() if key != "json"})
        items = search["json"].get("items", [])
        conversation_states = []
        for item in items:
            conversation_id = str(item.get("id"))
            detail = http_get(f"http://127.0.0.1:18000/api/conversations/{conversation_id}", session_key, 15)
            assert_check(checks, f"conversation_detail_{conversation_id}", detail["status"] == 200 and isinstance(detail["json"], dict), {key: value for key, value in detail.items() if key != "json"})
            conversation_states.append({"id": conversation_id, "executionStatus": str(detail["json"].get("execution_status") or "unknown").lower()})
        active = [row for row in conversation_states if row["executionStatus"] in ACTIVE_STATUSES]
        assert_check(checks, "no_active_openhands_work", not active, active)
        observations["activeWork"] = {"inspected": len(conversation_states), "statusCounts": dict(sorted(Counter(row["executionStatus"] for row in conversation_states).items())), "active": active}

        models = http_get("http://127.0.0.1:8802/v1/models", timeout=15)
        ids = model_ids(models["json"])
        expected_model = launch["modelProfile"]["model"]
        assert_check(checks, "exact_cognition_model_ready", models["status"] == 200 and expected_model in ids, {"status": models["status"], "bytes": models["bytes"], "sha256": models["sha256"], "modelIds": ids, "expected": expected_model})
        observations["modelEndpoint"] = {"apiRoot": launch["modelProfile"]["apiRoot"], "modelsStatus": models["status"], "modelsBytes": models["bytes"], "modelsSha256": models["sha256"], "modelIds": ids, "exactModelReady": expected_model in ids, "modelCallPerformed": False}

        qdrant = {}
        for name in (launch["disposable"]["semanticCollection"], launch["disposable"]["episodicCollection"]):
            response = http_get(f"http://127.0.0.1:6333/collections/{name}")
            qdrant[name] = {key: value for key, value in response.items() if key != "json"}
        assert_check(checks, "disposable_qdrant_collections_absent", all(value["status"] == 404 for value in qdrant.values()), qdrant)
        mongo_client = MongoClient(launch["environment"]["mongoUri"], serverSelectionTimeoutMS=5000, connectTimeoutMS=5000)
        database_names = mongo_client.list_database_names()
        mongo_client.close()
        mongo_name = launch["disposable"]["mongoDatabase"]
        assert_check(checks, "disposable_mongo_database_absent", mongo_name not in database_names, {"database": mongo_name, "absent": mongo_name not in database_names})
        workspace = Path(launch["disposable"]["workspaceRoot"])
        assert_check(checks, "disposable_workspace_absent", not workspace.exists(), {"path": str(workspace), "absent": not workspace.exists()})
        observations["disposableState"] = {"mongoDatabase": mongo_name, "mongoDatabaseAbsent": mongo_name not in database_names, "qdrantCollections": qdrant, "workspaceRoot": str(workspace), "workspaceRootAbsent": not workspace.exists()}
    except Exception as exc:
        failed = str(exc)

    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_1_READ_ONLY_LIVE_STATE_PREFLIGHT",
        "recordedAt": recorded,
        "status": "PASS_READ_ONLY_PREFLIGHT" if failed is None else "NO_GO_READ_ONLY_PREFLIGHT",
        "authorization": {
            "executionIdentity": EXPECTED_EXECUTION_IDENTITY,
            "scope": "ONE_READ_ONLY_LIVE_STATE_PREFLIGHT",
            "serviceStartOrRestart": False,
            "openhandsConversation": False,
            "modelCall": False,
            "liveDressRehearsal": False,
            "measuredExecution": False,
        },
        "identityAcceptance": {"path": str(IDENTITY_ACCEPTANCE.relative_to(ROOT)), "sha256": sha(IDENTITY_ACCEPTANCE)},
        "checks": checks,
        "observations": observations,
        "failedPrecondition": failed,
        "prohibitedActivity": {
            "serviceStartOrRestart": "NOT_RUN",
            "openhandsConversationMutation": "NOT_RUN",
            "modelGeneration": "NOT_RUN",
            "liveDressRehearsal": "NOT_RUN",
            "measuredExecution": "NOT_RUN",
            "productionOrHistoricalDataMutation": "NOT_RUN",
        },
        "verdict": "GO_FOR_SEPARATELY_AUTHORIZED_NEXT_STAGE" if failed is None else "NO_GO_STOPPED",
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(OUTPUT, receipt)
    print(json.dumps({"status": receipt["status"], "failedPrecondition": failed, "receiptPath": str(OUTPUT), "receiptFileSha256": sha(OUTPUT), "embeddedReceiptSha256": receipt["receiptSha256"]}, sort_keys=True))
    return 0 if failed is None else 2


if __name__ == "__main__":
    raise SystemExit(main())
