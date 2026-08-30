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
from urllib.error import HTTPError

from pymongo import MongoClient


ROOT = Path(__file__).resolve().parents[1]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
CANDIDATE_1_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
CANDIDATE_3_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-3"
CANDIDATE_4_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4"
CANDIDATE_5_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-5"
for package in (
    S2_PACKAGE,
    CANDIDATE_1_PACKAGE,
    CANDIDATE_3_PACKAGE,
    CANDIDATE_4_PACKAGE,
    CANDIDATE_5_PACKAGE,
):
    sys.path.insert(0, str(package))

from e1_candidate5.live_entrypoint import compose, load_definition, tree_sha  # noqa: E402
from e1_candidate5.runner import verdict_vector  # noqa: E402
from t1.model import (  # noqa: E402
    LiveAuthority,
    OperationKey,
    RunMode,
    canonical_bytes,
    digest,
    file_sha256,
)


PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-5-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-5-acceptance.json"
LAUNCH = CANDIDATE_5_PACKAGE / "e1_candidate5/live-launch-definition.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-5-restart-continuity-canary"
PREFLIGHT = OUTPUT / "read-only-preflight.json"
IDENTITY = OUTPUT / "execution-identity.json"
AUTHORIZATION = OUTPUT / "single-use-authorization.json"
CONSUMED = OUTPUT / "single-use-authority-consumed.json"
WALK = OUTPUT / "canary-walk.jsonl"
RECEIPT = OUTPUT / "canary-receipt.json"
PLAN = tuple(
    OperationKey("SMA-S2-012-RESTART-CONTINUITY", repetition)
    for repetition in (1, 2, 3)
)
ACTIVE = {"running", "waiting", "deleting"}


def write_exclusive(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical_bytes(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def jsonable(value: Any) -> Any:
    if hasattr(value, "value") and isinstance(value.value, str):
        return value.value
    if isinstance(value, Mapping):
        return {str(key): jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [jsonable(item) for item in value]
    return value


def occupied(port: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as stream:
        stream.settimeout(0.25)
        return stream.connect_ex(("127.0.0.1", port)) == 0


def http_json(url: str, key: str | None = None, timeout: int = 15) -> tuple[int, Any]:
    headers = {"X-Session-API-Key": key} if key else {}
    try:
        with urlrequest.urlopen(
            urlrequest.Request(url, headers=headers),
            timeout=timeout,
        ) as response:
            body = response.read()
            return response.status, json.loads(body) if body else None
    except HTTPError as exc:
        body = exc.read()
        return exc.code, json.loads(body) if body else None


def preflight(launch: Mapping[str, Any]) -> dict[str, Any]:
    checks: list[dict[str, Any]] = []

    def check(name: str, passed: bool, evidence: Any) -> None:
        checks.append({"name": name, "pass": bool(passed), "evidence": evidence})
        if not passed:
            raise RuntimeError(f"preflight failed: {name}: {evidence}")

    load_definition(LAUNCH)
    check(
        "all_bound_files_and_trees_current",
        True,
        {
            "files": len(launch["boundFiles"]),
            "trees": len(launch["boundTrees"]),
        },
    )
    ports = {
        str(port): occupied(port)
        for port in (27017, 6333, 8000, 8130, 8802, 19191, 19192, 19193)
    }
    check(
        "required_services_available",
        all(ports[str(port)] for port in (27017, 6333, 8000, 8802)),
        ports,
    )
    check(
        "disposable_ports_free",
        all(not ports[str(port)] for port in (8130, 19191, 19192, 19193)),
        ports,
    )
    session_key = Path(launch["environment"]["sessionKeyFile"]).read_text(
        encoding="utf-8"
    ).strip()
    ingress_status, _ = http_json("http://127.0.0.1:8000/health")
    agent_status, _ = http_json("http://127.0.0.1:18000/health")
    settings_status, _ = http_json(
        "http://127.0.0.1:18000/api/settings",
        session_key,
    )
    check(
        "openhands_healthy",
        ingress_status == agent_status == settings_status == 200,
        {
            "ingress": ingress_status,
            "agent": agent_status,
            "settings": settings_status,
        },
    )
    search_status, search = http_json(
        "http://127.0.0.1:18000/api/conversations/search?limit=100",
        session_key,
    )
    states: list[dict[str, str]] = []
    for item in (search or {}).get("items", []):
        conversation_id = str(item["id"])
        detail_status, detail = http_json(
            f"http://127.0.0.1:18000/api/conversations/{conversation_id}",
            session_key,
        )
        if detail_status != 200:
            raise RuntimeError(
                f"preflight failed: conversation detail unavailable: {conversation_id}"
            )
        states.append({
            "id": conversation_id,
            "status": str((detail or {}).get("execution_status") or "unknown").lower(),
        })
    active = [row for row in states if row["status"] in ACTIVE]
    check(
        "no_active_openhands_work",
        search_status == 200 and not active,
        {
            "inspected": len(states),
            "active": active,
            "statusCounts": dict(sorted(Counter(row["status"] for row in states).items())),
        },
    )
    model_status, models = http_json("http://127.0.0.1:8802/v1/models")
    model_ids = [str(row.get("id")) for row in (models or {}).get("data", [])]
    expected_model = launch["modelProfile"]["model"]
    check(
        "exact_model_ready",
        model_status == 200 and expected_model in model_ids,
        {
            "status": model_status,
            "expected": expected_model,
            "modelIds": model_ids,
        },
    )
    qdrant_statuses = {}
    for collection in (
        launch["disposable"]["semanticCollection"],
        launch["disposable"]["episodicCollection"],
    ):
        status, _ = http_json(
            f"http://127.0.0.1:6333/collections/{collection}"
        )
        qdrant_statuses[collection] = status
    check(
        "disposable_qdrant_absent",
        set(qdrant_statuses.values()) == {404},
        qdrant_statuses,
    )
    client = MongoClient(
        launch["environment"]["mongoUri"],
        serverSelectionTimeoutMS=5000,
        connectTimeoutMS=5000,
    )
    database_names = client.list_database_names()
    client.close()
    mongo_database = launch["disposable"]["mongoDatabase"]
    check(
        "disposable_mongo_absent",
        mongo_database not in database_names,
        {"database": mongo_database, "absent": mongo_database not in database_names},
    )
    workspace = Path(launch["disposable"]["workspaceRoot"])
    owned = [
        str(path)
        for path in workspace.iterdir()
        if path.name != "runtime"
    ] if workspace.is_dir() else []
    check(
        "operation_workspaces_absent",
        not owned,
        {"workspaceRoot": str(workspace), "owned": owned},
    )
    return {
        "status": "PASS_READ_ONLY_PREFLIGHT",
        "checks": checks,
        "model": expected_model,
        "activeWork": active,
        "prohibitedActivity": {
            "serviceStartOrRestart": "NOT_RUN",
            "openhandsConversation": "NOT_RUN",
            "modelCall": "NOT_RUN",
        },
    }


def action(launch: Mapping[str, Any], name: str) -> Mapping[str, Any]:
    command = [
        launch["environment"]["livePython"],
        str(CANDIDATE_5_PACKAGE / "e1_candidate5/live_actions.py"),
        "--definition",
        str(LAUNCH),
        name,
    ]
    completed = subprocess.run(
        command,
        cwd=ROOT,
        capture_output=True,
        timeout=180,
        check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError(
            f"candidate-5 action {name} failed: "
            + completed.stderr.decode(errors="replace")
        )
    return json.loads(completed.stdout)


def clean(inventory: Mapping[str, Any]) -> bool:
    return bool(
        inventory.get("mongoDatabasePresent") is False
        and inventory.get("semanticCollectionPresent") is False
        and inventory.get("episodicCollectionPresent") is False
        and inventory.get("processState")
        and all(
            state == "STOPPED"
            for state in inventory["processState"].values()
        )
        and not inventory.get("activeRequests")
        and not inventory.get("workspaces")
    )


def main() -> int:
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    if digest(package["identitySubject"]) != package.get("packageIdentity"):
        raise RuntimeError("candidate-5 package identity does not recompute")
    existing_acceptance = (
        json.loads(ACCEPTANCE.read_text(encoding="utf-8"))
        if ACCEPTANCE.exists()
        else None
    )
    recorded = (
        str(existing_acceptance["recordedAt"])
        if existing_acceptance is not None
        else datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    )
    intended_acceptance = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_PACKAGE_ACCEPTANCE",
        "status": "ACCEPTED_FROZEN",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "packageIdentityRecordSha256": file_sha256(PACKAGE),
        "offlineQualification": package["identitySubject"]["offlineQualification"],
        "principalStatement": "Accepted. Please proceed.",
        "scope": "CANDIDATE_5_PACKAGE_AND_EXACT_THREE_REPETITION_RESTART_CONTINUITY_CANARY",
        "measuredExecutionAuthorized": False,
    }
    if existing_acceptance is not None:
        acceptance = existing_acceptance
        if acceptance != intended_acceptance:
            raise RuntimeError("existing candidate-5 acceptance differs from this run")
    else:
        acceptance = intended_acceptance
        write_exclusive(ACCEPTANCE, acceptance)
    if OUTPUT.exists():
        unexpected = [
            str(path)
            for path in (PREFLIGHT, IDENTITY, AUTHORIZATION, CONSUMED, WALK, RECEIPT)
            if path.exists()
        ]
        if unexpected:
            raise RuntimeError(
                f"candidate-5 canary already advanced past preflight: {unexpected}"
            )
    else:
        OUTPUT.mkdir(parents=True, exist_ok=False)
    preflight_receipt = preflight(launch)
    preflight_receipt.update({
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_READ_ONLY_PREFLIGHT",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
    })
    preflight_receipt["receiptSha256"] = digest(preflight_receipt)
    write_exclusive(PREFLIGHT, preflight_receipt)
    execution_subject = {
        "packageIdentity": package["packageIdentity"],
        "packageAcceptanceSha256": file_sha256(ACCEPTANCE),
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "preflightSha256": file_sha256(PREFLIGHT),
        "driverSha256": file_sha256(Path(__file__).resolve()),
        "mode": "ZERO_CREDIT_DRESS",
        "operationKeys": [key.value for key in PLAN],
        "maximumOperations": len(PLAN),
        "automaticReruns": 0,
    }
    execution_identity = digest(execution_subject)
    identity = {
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_RESTART_CANARY_EXECUTION_IDENTITY",
        "status": "SEALED_SINGLE_USE_RESTART_CANARY_ONLY",
        "packageIdentity": package["packageIdentity"],
        "identitySubject": execution_subject,
        "executionIdentity": execution_identity,
    }
    authorization = {
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_RESTART_CANARY_AUTHORIZATION",
        "status": "AUTHORIZED_BY_PRINCIPAL_ACCEPT_AND_PROCEED",
        "executionIdentity": execution_identity,
        "mode": "ZERO_CREDIT_DRESS",
        "singleUse": True,
        "maximumOperations": len(PLAN),
        "operationKeys": [key.value for key in PLAN],
        "automaticReruns": 0,
        "measuredCredit": False,
        "recordedAt": recorded,
    }
    write_exclusive(IDENTITY, identity)
    write_exclusive(AUTHORIZATION, authorization)
    authority = LiveAuthority(
        package_manifest_path=str(ACCEPTANCE),
        package_manifest_sha256=file_sha256(ACCEPTANCE),
        authorization_path=str(AUTHORIZATION),
        authorization_sha256=file_sha256(AUTHORIZATION),
        execution_identity_path=str(IDENTITY),
        execution_identity_sha256=file_sha256(IDENTITY),
        mode=RunMode.ZERO_CREDIT_DRESS,
        consumed_marker_path=str(CONSUMED),
    )
    records: list[dict[str, Any]] = []
    runner = None
    terminal_exception: dict[str, str] | None = None
    final_cleanup: Mapping[str, Any] | None = None
    final_inventory: Mapping[str, Any] | None = None
    walk_descriptor = os.open(
        WALK,
        os.O_WRONLY | os.O_CREAT | os.O_EXCL,
        0o600,
    )
    started_ns = time.monotonic_ns()
    try:
        runner, _ = compose(ROOT, RunMode.ZERO_CREDIT_DRESS, authority, LAUNCH)
        for index, key in enumerate(PLAN, start=1):
            result = runner.run_operation(key)
            observation = jsonable(asdict(result.observation))
            record = {
                "vector": verdict_vector(result),
                "observation": observation,
                "scientific": [jsonable(asdict(row)) for row in result.scientific],
                "evidence": [jsonable(asdict(row)) for row in result.evidence],
                "observationSha256": digest(observation),
            }
            records.append(record)
            os.write(walk_descriptor, canonical_bytes(record) + b"\n")
            os.fsync(walk_descriptor)
            print(json.dumps({
                "completed": index,
                "planned": len(PLAN),
                "operationKey": key.value,
                "overall": record["vector"]["overallRepetitionVerdict"],
                "failure": observation.get("failure"),
                "modelRequests": len(observation.get("model_requests", [])),
            }, sort_keys=True), flush=True)
            if record["vector"]["overallRepetitionVerdict"] != "PASS":
                break
    except BaseException as exc:
        terminal_exception = {"class": type(exc).__name__, "message": str(exc)}
    finally:
        os.fsync(walk_descriptor)
        os.close(walk_descriptor)
        try:
            final_cleanup = action(launch, "cleanup_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {
                "class": type(exc).__name__,
                "message": str(exc),
            }
        try:
            final_inventory = action(launch, "inventory_owned")
        except BaseException as exc:
            terminal_exception = terminal_exception or {
                "class": type(exc).__name__,
                "message": str(exc),
            }
    vectors = [record["vector"] for record in records]
    finished = [
        conversation.get("execution_status")
        for record in records
        for conversation in record["observation"].get("conversations", [])
        if any(
            receipt.get("kind") == "prompt_submission"
            and receipt.get("conversationId") == conversation.get("id")
            for receipt in record["observation"].get("raw_receipts", [])
        )
    ]
    finish_reasons = [
        terminal.get("finishReason")
        for record in records
        for terminal in record["observation"].get("model_terminals", [])
    ]
    inventory_clean = final_inventory is not None and clean(final_inventory)
    passed = bool(
        len(records) == len(PLAN)
        and all(vector["overallRepetitionVerdict"] == "PASS" for vector in vectors)
        and set(finished) == {"finished"}
        and finish_reasons
        and set(finish_reasons) == {"stop"}
        and terminal_exception is None
        and runner is not None
        and runner.ledger.verify()
        and inventory_clean
    )
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_RESTART_CONTINUITY_CANARY_RECEIPT",
        "status": "PASS_RESTART_CONTINUITY_CANARY_NOT_MEASURED_EXECUTION" if passed else "NO_GO_RESTART_CONTINUITY_CANARY",
        "recordedAt": recorded,
        "durationMilliseconds": (time.monotonic_ns() - started_ns) / 1_000_000,
        "packageIdentity": package["packageIdentity"],
        "executionIdentity": execution_identity,
        "preflight": {"path": str(PREFLIGHT.relative_to(ROOT)), "sha256": file_sha256(PREFLIGHT)},
        "authorityConsumedBeforeFirstAction": CONSUMED.is_file(),
        "workload": {
            "operationKeys": [key.value for key in PLAN],
            "planned": len(PLAN),
            "executed": len(records),
            "automaticReruns": 0,
        },
        "walk": {"path": str(WALK.relative_to(ROOT)), "sha256": file_sha256(WALK), "records": len(records)},
        "overallVerdicts": Counter(
            vector["overallRepetitionVerdict"] for vector in vectors
        ),
        "conversationStatuses": Counter(finished),
        "finishReasons": Counter(finish_reasons),
        "ledgerValid": bool(runner is not None and runner.ledger.verify()),
        "terminalException": terminal_exception,
        "finalCleanup": final_cleanup,
        "finalInventory": final_inventory,
        "finalInventoryClean": inventory_clean,
        "measuredExecution": "NOT_RUN",
        "automaticRerun": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(RECEIPT, receipt)
    print(json.dumps({
        "status": receipt["status"],
        "executed": len(records),
        "planned": len(PLAN),
        "conversationStatuses": dict(Counter(finished)),
        "finishReasons": dict(Counter(finish_reasons)),
        "ledgerValid": receipt["ledgerValid"],
        "finalInventoryClean": inventory_clean,
        "receiptFileSha256": file_sha256(RECEIPT),
        "embeddedReceiptSha256": receipt["receiptSha256"],
    }, sort_keys=True), flush=True)
    return 0 if passed else 2


if __name__ == "__main__":
    raise SystemExit(main())
