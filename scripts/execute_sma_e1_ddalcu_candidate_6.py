#!/usr/bin/env python3
from __future__ import annotations

import argparse
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
from urllib.error import HTTPError
from urllib import request as urlrequest


ROOT = Path(__file__).resolve().parents[1]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [
    ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}"
    for number in (1, 3, 4, 5, 6)
]
for package in (S2_PACKAGE, *PACKAGES):
    sys.path.insert(0, str(package))

from e1_candidate6.live_entrypoint import compose, load_definition  # noqa: E402
from e1_candidate6.runner import verdict_vector  # noqa: E402
from e1_candidate6.authority import sealed_execution_authorization  # noqa: E402
from e1.runner import E1_CASES, e1_plan  # noqa: E402
from pymongo import MongoClient  # noqa: E402
from t1.model import (  # noqa: E402
    LiveAuthority,
    OperationKey,
    RunMode,
    canonical_bytes,
    digest,
    file_sha256,
)


PACKAGE_ROOT = PACKAGES[-1] / "e1_candidate6"
PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-acceptance.json"
LAUNCH = PACKAGE_ROOT / "live-launch-definition.json"
CANARY_OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-6-all-scenario-canary"
MEASURED_OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-6-measured-execution"
CANARY_RECEIPT = CANARY_OUTPUT / "execution-receipt.json"
ACTIVE = {"running", "starting", "queued", "working", "busy", "awaiting_input"}


def write_exclusive(path: Path, value: Any) -> None:
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
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as client:
        client.settimeout(0.25)
        return client.connect_ex(("127.0.0.1", port)) == 0


def http_json(
    url: str,
    key: str | None = None,
    timeout: int = 15,
) -> tuple[int, Any]:
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
        {"files": len(launch["boundFiles"]), "trees": len(launch["boundTrees"])},
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
        {"ingress": ingress_status, "agent": agent_status, "settings": settings_status},
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
            "statusCounts": dict(Counter(row["status"] for row in states)),
        },
    )
    model_status, models = http_json("http://127.0.0.1:8802/v1/models")
    model_ids = [str(row.get("id")) for row in (models or {}).get("data", [])]
    expected_model = launch["modelProfile"]["model"]
    check(
        "exact_model_ready",
        model_status == 200 and expected_model in model_ids,
        {"status": model_status, "expected": expected_model, "modelIds": model_ids},
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
    }


def action(launch: Mapping[str, Any], name: str) -> Mapping[str, Any]:
    command = [
        launch["environment"]["livePython"],
        str(PACKAGE_ROOT / "live_actions.py"),
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
            f"candidate-6 action {name} failed: "
            + completed.stderr.decode(errors="replace")
        )
    return json.loads(completed.stdout)


def clean(inventory: Mapping[str, Any]) -> bool:
    return bool(
        inventory.get("mongoDatabasePresent") is False
        and inventory.get("semanticCollectionPresent") is False
        and inventory.get("episodicCollectionPresent") is False
        and inventory.get("processState")
        and all(state == "STOPPED" for state in inventory["processState"].values())
        and not inventory.get("activeRequests")
        and not inventory.get("workspaces")
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--mode", choices=("canary", "measured"), required=True)
    parser.add_argument("--authorization", required=True)
    args = parser.parse_args()
    mode = (
        RunMode.ZERO_CREDIT_DRESS
        if args.mode == "canary"
        else RunMode.MEASURED
    )
    output = CANARY_OUTPUT if args.mode == "canary" else MEASURED_OUTPUT
    if output.exists():
        raise RuntimeError(f"candidate-6 {args.mode} output already exists")
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    acceptance = json.loads(ACCEPTANCE.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    authorization_path = Path(args.authorization).resolve()
    authorization = json.loads(authorization_path.read_text(encoding="utf-8"))
    if digest(package["identitySubject"]) != package.get("packageIdentity"):
        raise RuntimeError("candidate-6 package identity does not recompute")
    if (
        acceptance.get("status") != "ACCEPTED_FROZEN"
        or acceptance.get("packageIdentity") != package["packageIdentity"]
        or acceptance.get("packageIdentityRecordSha256") != file_sha256(PACKAGE)
    ):
        raise RuntimeError("candidate-6 package acceptance mismatch")
    expected_repetitions = 18 if args.mode == "canary" else 96
    expected_authority_mode = mode.value
    if (
        authorization.get("status") != "AUTHORIZED_BY_PRINCIPAL"
        or authorization.get("packageIdentity") != package["packageIdentity"]
        or authorization.get("mode") != expected_authority_mode
        or authorization.get("singleUse") is not True
        or authorization.get("maximumScenarios") != 18
        or authorization.get("maximumRepetitions") != expected_repetitions
        or authorization.get("automaticReruns") != 0
        or not str(authorization.get("principalStatement") or "").strip()
    ):
        raise RuntimeError("candidate-6 principal authorization mismatch")
    if args.mode == "measured":
        canary = json.loads(CANARY_RECEIPT.read_text(encoding="utf-8"))
        if (
            canary.get("status")
            != "PASS_ALL_SCENARIO_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION"
            or canary.get("packageIdentity") != package["packageIdentity"]
            or canary.get("workload", {}).get("executedRepetitions") != 18
            or canary.get("overallVerdicts") != {"PASS": 18}
            or canary.get("finalInventoryClean") is not True
        ):
            raise RuntimeError("candidate-6 all-scenario canary prerequisite is not green")
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    preflight_receipt = preflight(launch)
    preflight_receipt.update({
        "recordType": f"SMA_E1_DDALCU_CANDIDATE_6_{args.mode.upper()}_READ_ONLY_PREFLIGHT",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
    })
    preflight_receipt["receiptSha256"] = digest(preflight_receipt)
    output.mkdir(parents=True, exist_ok=False)
    preflight_path = output / "read-only-preflight.json"
    write_exclusive(preflight_path, preflight_receipt)
    plan = (
        e1_plan()
        if mode == RunMode.MEASURED
        else tuple(OperationKey(str(row["s2Id"]), 1) for row in E1_CASES)
    )
    if len(plan) != expected_repetitions:
        raise RuntimeError(
            f"candidate-6 {args.mode} plan has {len(plan)} operations"
        )
    identity_subject = {
        "packageIdentity": package["packageIdentity"],
        "packageAcceptanceSha256": file_sha256(ACCEPTANCE),
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "offlineQualification": package["identitySubject"]["offlineQualification"],
        "principalGrantSha256": file_sha256(authorization_path),
        "preflightSha256": file_sha256(preflight_path),
        "driverSha256": file_sha256(Path(__file__).resolve()),
        "mode": expected_authority_mode,
        "planSha256": digest([key.value for key in plan]),
        "scenarios": 18,
        "repetitions": expected_repetitions,
        "automaticReruns": 0,
    }
    execution_identity = digest(identity_subject)
    identity_path = output / "execution-identity.json"
    identity = {
        "schemaVersion": "1.0.0",
        "recordType": f"SMA_E1_DDALCU_CANDIDATE_6_{args.mode.upper()}_EXECUTION_IDENTITY",
        "status": "SEALED_SINGLE_USE_EXECUTION_ONLY",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "identitySubject": identity_subject,
        "executionIdentity": execution_identity,
    }
    write_exclusive(identity_path, identity)
    sealed_authorization_path = output / "single-use-authorization.json"
    sealed_authorization = sealed_execution_authorization(
        principal_grant_sha256=file_sha256(authorization_path),
        principal_statement=str(authorization["principalStatement"]),
        package_identity=package["packageIdentity"],
        execution_identity=execution_identity,
        mode=expected_authority_mode,
        maximum_scenarios=18,
        maximum_repetitions=expected_repetitions,
    )
    write_exclusive(sealed_authorization_path, sealed_authorization)
    authority = LiveAuthority(
        package_manifest_path=str(ACCEPTANCE),
        package_manifest_sha256=file_sha256(ACCEPTANCE),
        authorization_path=str(sealed_authorization_path),
        authorization_sha256=file_sha256(sealed_authorization_path),
        execution_identity_path=str(identity_path),
        execution_identity_sha256=file_sha256(identity_path),
        mode=mode,
        consumed_marker_path=str(output / "single-use-authority-consumed.json"),
    )
    runner, composed_plan = compose(ROOT, mode, authority, LAUNCH)
    if tuple(composed_plan) != tuple(plan):
        raise RuntimeError("candidate-6 composed plan changed after identity sealing")
    walk = output / "execution-walk.jsonl"
    records: list[dict[str, Any]] = []
    terminal_exception: dict[str, str] | None = None
    final_cleanup: Mapping[str, Any] | None = None
    final_inventory: Mapping[str, Any] | None = None
    descriptor = os.open(walk, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    started_ns = time.monotonic_ns()
    try:
        for index, key in enumerate(plan, start=1):
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
            os.write(descriptor, canonical_bytes(record) + b"\n")
            os.fsync(descriptor)
            print(json.dumps({
                "completed": index,
                "planned": expected_repetitions,
                "operationKey": key.value,
                "overall": record["vector"]["overallRepetitionVerdict"],
                "failure": observation.get("failure"),
                "modelRequests": len(observation.get("model_requests", [])),
                "selectedAnswers": record["vector"]["selectedInteractionAnswers"],
            }, sort_keys=True), flush=True)
            if record["vector"]["overallRepetitionVerdict"] != "PASS":
                break
    except BaseException as exc:
        terminal_exception = {"class": type(exc).__name__, "message": str(exc)}
    finally:
        os.fsync(descriptor)
        os.close(descriptor)
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
    prompted_statuses = [
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
    complete = len(records) == expected_repetitions
    ledger_valid = runner.ledger.verify()
    interaction_counts_match = all(
        record["vector"]["selectedInteractionAnswers"]
        == sum(
            receipt.get("kind") == "prompt_submission"
            for receipt in record["observation"].get("raw_receipts", [])
        )
        for record in records
    )
    all_pass = bool(
        complete
        and all(vector["overallRepetitionVerdict"] == "PASS" for vector in vectors)
        and set(prompted_statuses) == {"finished"}
        and finish_reasons
        and set(finish_reasons) == {"stop"}
        and interaction_counts_match
        and terminal_exception is None
        and ledger_valid
        and inventory_clean
    )
    pass_status = (
        "PASS_ALL_SCENARIO_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION"
        if args.mode == "canary"
        else "PASS_MEASURED_PENDING_SCIENTIFIC_ADJUDICATION"
    )
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": f"SMA_E1_DDALCU_CANDIDATE_6_{args.mode.upper()}_EXECUTION_RECEIPT",
        "status": pass_status if all_pass else "NO_GO_EXECUTION_STOPPED",
        "recordedAt": recorded,
        "durationMilliseconds": (time.monotonic_ns() - started_ns) / 1_000_000,
        "packageIdentity": package["packageIdentity"],
        "executionIdentity": execution_identity,
        "authorityConsumedBeforeFirstAction": Path(authority.consumed_marker_path).is_file(),
        "workload": {
            "scenarios": 18,
            "plannedRepetitions": expected_repetitions,
            "executedRepetitions": len(records),
            "completed": complete,
            "automaticReruns": 0,
        },
        "walk": {"path": str(walk.relative_to(ROOT)), "sha256": file_sha256(walk), "records": len(records)},
        "overallVerdicts": dict(Counter(vector["overallRepetitionVerdict"] for vector in vectors)),
        "conversationStatuses": dict(Counter(prompted_statuses)),
        "finishReasons": dict(Counter(finish_reasons)),
        "interactionCountsMatch": interaction_counts_match,
        "modelCalls": sum(len(record["observation"].get("model_requests", [])) for record in records),
        "ledgerValid": ledger_valid,
        "terminalException": terminal_exception,
        "finalCleanup": final_cleanup,
        "finalInventory": final_inventory,
        "finalInventoryClean": inventory_clean,
        "scientificAdjudication": "NOT_RUN",
        "automaticRerun": "NOT_RUN",
    }
    receipt["receiptSha256"] = digest(receipt)
    receipt_path = output / "execution-receipt.json"
    write_exclusive(receipt_path, receipt)
    print(json.dumps({
        "status": receipt["status"],
        "executed": len(records),
        "planned": expected_repetitions,
        "overallVerdicts": receipt["overallVerdicts"],
        "conversationStatuses": receipt["conversationStatuses"],
        "finishReasons": receipt["finishReasons"],
        "interactionCountsMatch": interaction_counts_match,
        "modelCalls": receipt["modelCalls"],
        "ledgerValid": ledger_valid,
        "finalInventoryClean": inventory_clean,
        "receiptFileSha256": file_sha256(receipt_path),
        "embeddedReceiptSha256": receipt["receiptSha256"],
    }, sort_keys=True), flush=True)
    return 0 if all_pass else 2


if __name__ == "__main__":
    raise SystemExit(main())
