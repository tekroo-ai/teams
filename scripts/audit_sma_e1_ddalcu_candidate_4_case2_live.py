#!/usr/bin/env python3
from __future__ import annotations

from collections import Counter
from dataclasses import asdict
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import time
from typing import Any, Mapping
from urllib import request as urlrequest


ROOT = Path(__file__).resolve().parents[1]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
CANDIDATE_1_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
CANDIDATE_3_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-3"
CANDIDATE_4_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4"
for package in (S2_PACKAGE, CANDIDATE_1_PACKAGE, CANDIDATE_3_PACKAGE, CANDIDATE_4_PACKAGE):
    sys.path.insert(0, str(package))

from e1.runner import verdict_vector  # noqa: E402
from e1_candidate4.live_entrypoint import compose  # noqa: E402
from t1.model import LiveAuthority, OperationKey, RunMode, canonical_bytes, digest, file_sha256  # noqa: E402


PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4-package-identity.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live-launch-definition.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-case2-live-canary"
PACKAGE_SEAL = OUTPUT / "engineering-canary-package-seal.json"
IDENTITY = OUTPUT / "engineering-canary-execution-identity.json"
AUTHORIZATION = OUTPUT / "engineering-canary-single-use-authorization.json"
CONSUMED = OUTPUT / "single-use-authority-consumed.json"
RESULT = OUTPUT / "case2-result.json"
RECEIPT = OUTPUT / "case2-live-canary-receipt.json"
CASE2 = OperationKey("SMA-S2-002-SAME-PARTITION-DELIVERY", 1)
ACTIVE = {"running", "waiting", "deleting"}


def jsonable(value: Any) -> Any:
    if hasattr(value, "value") and isinstance(value.value, str):
        return value.value
    if isinstance(value, Mapping):
        return {str(key): jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [jsonable(item) for item in value]
    return value


def write_exclusive(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical_bytes(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def http_json(url: str, key: str | None = None, timeout: int = 15) -> tuple[int, Any]:
    headers = {"X-Session-API-Key": key} if key else {}
    with urlrequest.urlopen(urlrequest.Request(url, headers=headers), timeout=timeout) as response:
        body = response.read()
        return response.status, json.loads(body) if body else None


def occupied(port: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as stream:
        stream.settimeout(0.25)
        return stream.connect_ex(("127.0.0.1", port)) == 0


def tree_sha(root: Path) -> str:
    rows = [[str(path.relative_to(root)), file_sha256(path)] for path in sorted(root.rglob("*")) if path.is_file() and "__pycache__" not in path.parts and path.suffix != ".pyc"]
    return digest(rows)


def preflight(launch: Mapping[str, Any]) -> dict[str, Any]:
    file_drift = []
    for binding in launch["boundFiles"]:
        path = Path(binding["path"])
        actual = file_sha256(path) if path.is_file() else None
        if actual != binding["sha256"]:
            file_drift.append({"path": str(path), "expected": binding["sha256"], "actual": actual})
    tree_drift = []
    for binding in launch["boundTrees"]:
        path = Path(binding["path"])
        actual = tree_sha(path) if path.is_dir() else None
        if actual != binding["sha256"]:
            tree_drift.append({"path": str(path), "expected": binding["sha256"], "actual": actual})
    if file_drift or tree_drift:
        raise RuntimeError(f"sealed package drift: files={file_drift} trees={tree_drift}")
    port_state = {str(port): occupied(port) for port in (27017, 6333, 8000, 8130, 8802, 19191, 19192, 19193)}
    required = (27017, 6333, 8000, 8802)
    disposable = (8130, 19191, 19192, 19193)
    if not all(port_state[str(port)] for port in required):
        raise RuntimeError(f"required service unavailable: {port_state}")
    if any(port_state[str(port)] for port in disposable):
        raise RuntimeError(f"qualification port already occupied: {port_state}")
    session_key = Path(launch["environment"]["sessionKeyFile"]).read_text(encoding="utf-8").strip()
    health_status, _ = http_json("http://127.0.0.1:8000/health")
    model_status, models = http_json("http://127.0.0.1:8802/v1/models")
    search_status, search = http_json("http://127.0.0.1:18000/api/conversations/search?limit=100", session_key)
    active = []
    statuses: list[str] = []
    for item in search.get("items", []):
        conversation_id = str(item["id"])
        detail_status, detail = http_json(f"http://127.0.0.1:18000/api/conversations/{conversation_id}", session_key)
        if detail_status != 200:
            raise RuntimeError(f"conversation detail unavailable: {conversation_id}")
        status = str(detail.get("execution_status") or "unknown").lower()
        statuses.append(status)
        if status in ACTIVE:
            active.append({"id": conversation_id, "status": status})
    model_ids = [str(row.get("id")) for row in models.get("data", [])]
    expected_model = launch["modelProfile"]["model"]
    if health_status != 200 or model_status != 200 or search_status != 200 or expected_model not in model_ids or active:
        raise RuntimeError(f"live-state precondition failed: health={health_status} model={model_status}:{model_ids} search={search_status} active={active}")
    return {
        "boundFiles": len(launch["boundFiles"]),
        "boundTrees": len(launch["boundTrees"]),
        "ports": port_state,
        "openhandsHealth": health_status,
        "modelEndpointStatus": model_status,
        "exactModel": expected_model,
        "activeWork": active,
        "conversationStatusCounts": dict(sorted(Counter(statuses).items())),
    }


def action(launch: Mapping[str, Any], name: str) -> Mapping[str, Any]:
    command = [
        launch["environment"]["livePython"],
        str(CANDIDATE_4_PACKAGE / "e1_candidate4/live_actions.py"),
        "--definition",
        str(LAUNCH),
        name,
    ]
    completed = subprocess.run(command, cwd=ROOT, capture_output=True, timeout=180, check=False)
    if completed.returncode != 0:
        raise RuntimeError(f"candidate-4 action {name} failed: {completed.stderr.decode(errors='replace')}")
    return json.loads(completed.stdout)


def main() -> int:
    if OUTPUT.exists():
        raise RuntimeError("candidate-4 live canary output already exists")
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    if digest(package["identitySubject"]) != package.get("packageIdentity"):
        raise RuntimeError("candidate-4 package identity does not recompute")
    state = preflight(launch)
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    package_seal = {
        "recordType": "SMA_E1_DDALCU_CANDIDATE_4_ENGINEERING_CANARY_PACKAGE_SEAL",
        "status": "SEALED",
        "packageIdentity": package["packageIdentity"],
        "packageIdentityRecordSha256": file_sha256(PACKAGE),
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "scope": "ONE_ZERO_CREDIT_REAL_PATH_CASE2_CANARY",
    }
    execution_subject = {
        "packageIdentity": package["packageIdentity"],
        "packageSealSha256": digest(package_seal),
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "operationKey": CASE2.value,
        "modelProfile": launch["modelProfile"],
        "driverSha256": file_sha256(Path(__file__).resolve()),
    }
    execution_identity = digest(execution_subject)
    identity = {
        "recordType": "SMA_E1_DDALCU_CANDIDATE_4_ENGINEERING_CANARY_EXECUTION_IDENTITY",
        "status": "SEALED_SINGLE_USE_ENGINEERING_CANARY_ONLY",
        "packageIdentity": package["packageIdentity"],
        "identitySubject": execution_subject,
        "executionIdentity": execution_identity,
    }
    authorization = {
        "recordType": "SMA_E1_DDALCU_CANDIDATE_4_ENGINEERING_CANARY_AUTHORIZATION",
        "status": "AUTHORIZED_BY_PRINCIPAL_REQUEST_FOR_CORRECTED_AUDIT",
        "executionIdentity": execution_identity,
        "mode": "ZERO_CREDIT_DRESS",
        "singleUse": True,
        "maximumOperations": 1,
        "operationKey": CASE2.value,
        "automaticReruns": 0,
        "measuredCredit": False,
        "recordedAt": recorded,
    }
    write_exclusive(PACKAGE_SEAL, package_seal)
    write_exclusive(IDENTITY, identity)
    write_exclusive(AUTHORIZATION, authorization)
    authority = LiveAuthority(
        package_manifest_path=str(PACKAGE_SEAL),
        package_manifest_sha256=file_sha256(PACKAGE_SEAL),
        authorization_path=str(AUTHORIZATION),
        authorization_sha256=file_sha256(AUTHORIZATION),
        execution_identity_path=str(IDENTITY),
        execution_identity_sha256=file_sha256(IDENTITY),
        mode=RunMode.ZERO_CREDIT_DRESS,
        consumed_marker_path=str(CONSUMED),
    )
    result = None
    terminal_exception = None
    started = time.monotonic_ns()
    try:
        runner, _ = compose(ROOT, RunMode.ZERO_CREDIT_DRESS, authority, LAUNCH)
        result = runner.run_operation(CASE2)
    except BaseException as exc:
        terminal_exception = {"class": type(exc).__name__, "message": str(exc)}
    finally:
        emergency_cleanup = action(launch, "cleanup_owned")
        inventory = action(launch, "inventory_owned")
    if result is None:
        result_record = None
        vector = None
        requests: list[Mapping[str, Any]] = []
        terminals: list[Mapping[str, Any]] = []
        ledger_valid = False
    else:
        observation = result.observation
        result_record = {
            "vector": verdict_vector(result),
            "observation": jsonable(asdict(observation)),
            "scientific": [jsonable(asdict(row)) for row in result.scientific],
            "evidence": [jsonable(asdict(row)) for row in result.evidence],
        }
        vector = result_record["vector"]
        requests = observation.model_requests
        terminals = observation.model_terminals
        ledger_valid = runner.ledger.verify()
        write_exclusive(RESULT, result_record)
    request_ids = {str(row.get("requestId")) for row in requests}
    terminal_ids = {str(row.get("requestId")) for row in terminals}
    # The bound live action returns product inventory fields, not the older
    # preflight wrapper's nested mongo/qdrant/processes representation.
    cleanup_exact = (
        inventory.get("mongoDatabasePresent") is False
        and inventory.get("semanticCollectionPresent") is False
        and inventory.get("episodicCollectionPresent") is False
        and all(value == "STOPPED" for value in inventory.get("processState", {}).values())
        and not inventory.get("activeRequests")
        and not inventory.get("workspaces")
    )
    passed = bool(
        terminal_exception is None
        and vector is not None
        and vector["overallRepetitionVerdict"] == "PASS"
        and 1 <= len(requests) <= 12
        and len(requests) == len(terminals)
        and request_ids == terminal_ids
        and ledger_valid
        and cleanup_exact
    )
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_4_CASE2_REAL_PATH_CANARY_RECEIPT",
        "status": "PASS_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION" if passed else "NO_GO_REAL_PATH_CANARY",
        "recordedAt": recorded,
        "durationMilliseconds": (time.monotonic_ns() - started) / 1_000_000,
        "packageIdentity": package["packageIdentity"],
        "executionIdentity": execution_identity,
        "operationKey": CASE2.value,
        "preflight": state,
        "authorityConsumedBeforeFirstAction": CONSUMED.is_file(),
        "result": {"path": str(RESULT.relative_to(ROOT)), "sha256": file_sha256(RESULT) if RESULT.is_file() else None},
        "verdict": vector,
        "modelCalls": {"requests": len(requests), "terminals": len(terminals), "pairedIdentities": request_ids == terminal_ids, "minimum": 1, "maximum": 12, "exactCountRequired": False},
        "ledgerValid": ledger_valid,
        "terminalException": terminal_exception,
        "cleanup": {"runnerAndEmergencyCleanup": emergency_cleanup, "finalInventory": inventory, "exactDisposableAbsence": cleanup_exact},
        "measuredExecution": "NOT_RUN",
        "automaticRerun": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(RECEIPT, receipt)
    print(json.dumps({"status": receipt["status"], "modelRequests": len(requests), "modelTerminals": len(terminals), "paired": request_ids == terminal_ids, "overall": vector["overallRepetitionVerdict"] if vector else None, "cleanupExact": cleanup_exact, "receiptSha256": file_sha256(RECEIPT)}, sort_keys=True))
    return 0 if passed else 2


if __name__ == "__main__":
    raise SystemExit(main())
