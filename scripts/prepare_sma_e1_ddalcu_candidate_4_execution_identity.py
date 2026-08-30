#!/usr/bin/env python3
from __future__ import annotations

from datetime import datetime, timezone
import json
from pathlib import Path
import stat

from prepare_sma_e1_ddalcu_candidate_2_execution_identity import (
    digest,
    exact_file,
    git_status,
    git_value,
    sha,
    tree_sha,
    write_exclusive,
)


ROOT = Path(__file__).resolve().parents[1]
SMA_ROOT = Path("/Users/paul/work/tekroo-ai/sma-step15-s1-p2final")
OPENHANDS_ROOT = Path("/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel")
CANVAS_ROOT = Path("/opt/homebrew/lib/node_modules/@openhands/agent-canvas")
PACKAGE_IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4-package-identity.json"
PACKAGE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4-acceptance.json"
CANARY_ADJUDICATION = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-case2-live-canary-adjudication/adjudication.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live-launch-definition.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4-execution-identity.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-4-execution-identity-preparation/execution-identity-preparation-receipt.json"


def main() -> int:
    if IDENTITY.exists() or OUTPUT.exists():
        raise RuntimeError("candidate-4 execution identity target already exists")
    package = json.loads(PACKAGE_IDENTITY.read_text(encoding="utf-8"))
    acceptance = json.loads(PACKAGE_ACCEPTANCE.read_text(encoding="utf-8"))
    canary = json.loads(CANARY_ADJUDICATION.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    if package.get("status") != "PREPARED_NOT_ACCEPTED_NOT_EXECUTION_AUTHORITY" or digest(package["identitySubject"]) != package.get("packageIdentity"):
        raise RuntimeError("candidate-4 package identity mismatch")
    if acceptance.get("status") != "ACCEPTED_FROZEN" or acceptance.get("packageIdentity") != package.get("packageIdentity") or acceptance.get("packageIdentityRecordSha256") != sha(PACKAGE_IDENTITY):
        raise RuntimeError("candidate-4 package acceptance mismatch")
    if canary.get("status") != "PASS_REAL_PATH_CANARY_NOT_MEASURED_EXECUTION" or acceptance.get("case2RealPathCanaryAdjudicationSha256") != sha(CANARY_ADJUDICATION):
        raise RuntimeError("candidate-4 real-path canary acceptance mismatch")
    for binding in launch["boundFiles"]:
        path = Path(binding["path"])
        if not path.is_file() or sha(path) != binding["sha256"]:
            raise RuntimeError(f"candidate-4 bound file drift: {path}")
    for binding in launch["boundTrees"]:
        root = Path(binding["path"])
        if not root.is_dir() or tree_sha(root) != binding["sha256"]:
            raise RuntimeError(f"candidate-4 bound tree drift: {root}")
    session_key = Path(launch["environment"]["sessionKeyFile"])
    if stat.S_IMODE(session_key.stat().st_mode) != 0o600:
        raise RuntimeError("OpenHands session key permissions are not 0600")
    subject = {
        "packageIdentity": package["packageIdentity"],
        "packageIdentityRecordSha256": sha(PACKAGE_IDENTITY),
        "packageAcceptanceRecordSha256": sha(PACKAGE_ACCEPTANCE),
        "offlineQualification": package["identitySubject"]["offlineQualification"],
        "case2RealPathCanaryAdjudication": exact_file(CANARY_ADJUDICATION),
        "liveLaunchDefinitionSha256": sha(LAUNCH),
        "successorLaunchFiles": {
            "runner": exact_file(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/runner.py"),
            "liveActions": exact_file(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live_actions.py"),
            "liveEntrypoint": exact_file(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live_entrypoint.py"),
        },
        "preparationDriver": exact_file(Path(__file__).resolve()),
        "acceptedScienceRunner": exact_file(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1/e1/runner.py"),
        "teams": {"commit": git_value(ROOT, "HEAD^{commit}"), "tree": git_value(ROOT, "HEAD^{tree}")},
        "sma": {
            "commit": git_value(SMA_ROOT, "HEAD^{commit}"),
            "tree": git_value(SMA_ROOT, "HEAD^{tree}"),
            "trackedStatus": git_status(SMA_ROOT),
            "runtimeJar": exact_file(Path(launch["promotionFixture"]["smaJar"])),
            "launchSupport": exact_file(SMA_ROOT / "scripts/wp5e_restart_recovery.py"),
            "hook": exact_file(SMA_ROOT / ".openhands/hooks/sma_context_hook.py"),
        },
        "openhands": {
            "commit": git_value(OPENHANDS_ROOT, "HEAD^{commit}"),
            "tree": git_value(OPENHANDS_ROOT, "HEAD^{tree}"),
            "trackedStatus": git_status(OPENHANDS_ROOT),
            "venvTreeSha256": tree_sha(OPENHANDS_ROOT / ".venv"),
            "agentServer": exact_file(OPENHANDS_ROOT / ".venv/bin/agent-server"),
        },
        "agentCanvas": {
            "plist": exact_file(Path("/Users/paul/Library/LaunchAgents/com.tekroo.openhands-agent-canvas.plist")),
            "wrapper": exact_file(Path("/Users/paul/.local/bin/agent-canvas")),
            "loopbackPreload": exact_file(Path("/Users/paul/.local/bin/agent-canvas-loopback.mjs")),
            "packageRoot": str(CANVAS_ROOT),
            "packageTreeSha256": tree_sha(CANVAS_ROOT),
        },
        "sessionKey": {"path": str(session_key), "sha256": sha(session_key), "mode": "0600", "secretRetained": False},
        "modelProfile": launch["modelProfile"],
        "agentLoopAudit": launch["agentLoopAudit"],
        "ports": launch["ports"],
        "disposableNamespaces": launch["disposable"],
        "boundFileCount": len(launch["boundFiles"]),
        "boundTreeCount": len(launch["boundTrees"]),
        "remediation": [
            "BOUNDED_VARIABLE_OPENHANDS_MODEL_CALL_CARDINALITY",
            "EXACT_REQUEST_TERMINAL_IDENTITY_PAIRING",
            "ORIGINAL_FROZEN_TASK_RETENTION_ACROSS_AGENT_LOOP",
            "REAL_PATH_CASE2_CANARY",
        ],
    }
    execution_identity = digest(subject)
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    identity = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_4_EXECUTION_IDENTITY",
        "status": "ACCEPTED_FROZEN_EXECUTION_IDENTITY_BY_PRINCIPAL_PROCEED_AUTHORITY",
        "recordedAt": recorded,
        "identityAlgorithm": "SHA256_CANONICAL_JSON_OF_IDENTITY_SUBJECT",
        "packageIdentity": package["packageIdentity"],
        "identitySubject": subject,
        "executionIdentity": execution_identity,
        "limits": {"livePreflightAuthorized": True, "serviceStartsAuthorized": 0, "openhandsConversationsAuthorized": 0, "modelCallsAuthorized": 0, "measuredRepetitionsAuthorized": 0},
        "principalStatement": "Accepted. Please proceed.",
        "nextDecision": "READ_ONLY_PREFLIGHT_THEN_SINGLE_96_REPETITION_MEASURED_EXECUTION_IF_ALL_PRECONDITIONS_PASS",
    }
    write_exclusive(IDENTITY, identity)
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_4_EXECUTION_IDENTITY_PREPARATION_RECEIPT",
        "status": "PASS_ACCEPTED_FROZEN_EXECUTION_IDENTITY_NOT_YET_LIVE_AUTHORITY",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "packageAcceptanceRecordSha256": sha(PACKAGE_ACCEPTANCE),
        "executionIdentity": execution_identity,
        "executionIdentityRecordSha256": sha(IDENTITY),
        "launchBindings": {"files": len(launch["boundFiles"]), "trees": len(launch["boundTrees"]), "allCurrent": True},
        "realPathCanary": {"status": canary["status"], "sha256": sha(CANARY_ADJUDICATION)},
        "prohibitedActivity": {"serviceStartOrRestart": "NOT_RUN", "openhandsConversation": "NOT_RUN", "modelCall": "NOT_RUN", "measuredExecution": "NOT_RUN", "productionOrHistoricalDataAccess": "NOT_RUN"},
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(OUTPUT, receipt)
    print(json.dumps({"packageIdentity": package["packageIdentity"], "executionIdentity": execution_identity, "identityRecordSha256": sha(IDENTITY), "preparationReceiptSha256": sha(OUTPUT), "embeddedReceiptSha256": receipt["receiptSha256"]}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
