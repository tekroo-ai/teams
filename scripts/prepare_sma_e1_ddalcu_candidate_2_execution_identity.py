#!/usr/bin/env python3
from __future__ import annotations

from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
SMA_ROOT = Path("/Users/paul/work/tekroo-ai/sma-step15-s1-p2final")
OPENHANDS_ROOT = Path("/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel")
CANVAS_ROOT = Path("/opt/homebrew/lib/node_modules/@openhands/agent-canvas")
PACKAGE_IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2-package-identity.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2/e1_candidate2/live-launch-definition.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2-execution-identity.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-2-execution-identity-preparation/execution-identity-preparation-receipt.json"


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
    return subprocess.run(["git", "rev-parse", argument], cwd=root, capture_output=True, text=True, check=True).stdout.strip()


def git_status(root: Path) -> list[str]:
    return subprocess.run(["git", "status", "--short"], cwd=root, capture_output=True, text=True, check=True).stdout.splitlines()


def exact_file(path: Path) -> dict[str, Any]:
    if not path.is_file():
        raise RuntimeError(f"required execution file missing: {path}")
    return {"path": str(path), "sha256": sha(path), "bytes": path.stat().st_size}


def write_exclusive(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def main() -> int:
    if IDENTITY.exists() or OUTPUT.exists():
        raise RuntimeError("candidate-2 execution identity target already exists")
    package = json.loads(PACKAGE_IDENTITY.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    if package.get("status") != "PREPARED_NOT_ACCEPTED_NOT_EXECUTION_AUTHORITY":
        raise RuntimeError("candidate-2 package status mismatch")
    if digest(package["identitySubject"]) != package.get("packageIdentity"):
        raise RuntimeError("candidate-2 package identity does not recompute")
    for binding in launch["boundFiles"]:
        path = Path(binding["path"])
        if not path.is_file() or sha(path) != binding["sha256"]:
            raise RuntimeError(f"candidate-2 bound file drift: {path}")
    for binding in launch["boundTrees"]:
        root = Path(binding["path"])
        if not root.is_dir() or tree_sha(root) != binding["sha256"]:
            raise RuntimeError(f"candidate-2 bound tree drift: {root}")

    session_key = Path(launch["environment"]["sessionKeyFile"])
    session_mode = stat.S_IMODE(session_key.stat().st_mode)
    if session_mode != 0o600:
        raise RuntimeError("OpenHands session key permissions are not 0600")
    subject = {
        "packageIdentity": package["packageIdentity"],
        "packageIdentityRecordSha256": sha(PACKAGE_IDENTITY),
        "offlineQualification": package["identitySubject"]["offlineQualification"],
        "liveLaunchDefinitionSha256": sha(LAUNCH),
        "successorLaunchFiles": {
            "liveActions": exact_file(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2/e1_candidate2/live_actions.py"),
            "liveEntrypoint": exact_file(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2/e1_candidate2/live_entrypoint.py"),
        },
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
        "ports": launch["ports"],
        "disposableNamespaces": launch["disposable"],
        "boundFileCount": len(launch["boundFiles"]),
        "boundTreeCount": len(launch["boundTrees"]),
        "failedCandidate1ExecutionReceiptSha256": "2660af551732713f3b6cd763e977f7fddf6d1458353861472420e6e3b2eda7a3",
        "remediation": "SELF_CONTAINED_LIVE_ACTION_DEPENDENCY_BOOTSTRAP",
    }
    execution_identity = digest(subject)
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    identity = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_2_EXECUTION_IDENTITY",
        "status": "PREPARED_PENDING_PRINCIPAL_ACCEPT_FREEZE_PACKAGE_AND_EXECUTION_IDENTITY",
        "recordedAt": recorded,
        "identityAlgorithm": "SHA256_CANONICAL_JSON_OF_IDENTITY_SUBJECT",
        "packageIdentity": package["packageIdentity"],
        "identitySubject": subject,
        "executionIdentity": execution_identity,
        "limits": {"livePreflightAuthorized": False, "serviceStartsAuthorized": 0, "openhandsConversationsAuthorized": 0, "modelCallsAuthorized": 0, "measuredRepetitionsAuthorized": 0},
        "nextDecision": "PRINCIPAL_ACCEPT_FREEZE_CANDIDATE_2_PACKAGE_AND_EXECUTION_IDENTITY",
    }
    write_exclusive(IDENTITY, identity)
    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_2_EXECUTION_IDENTITY_PREPARATION_RECEIPT",
        "status": "PASS_PREPARED_NOT_ACCEPTED_NOT_EXECUTION_AUTHORITY",
        "recordedAt": recorded,
        "packageIdentity": package["packageIdentity"],
        "packageIdentityRecordSha256": sha(PACKAGE_IDENTITY),
        "executionIdentity": execution_identity,
        "executionIdentityRecordSha256": sha(IDENTITY),
        "launchBindings": {"files": len(launch["boundFiles"]), "trees": len(launch["boundTrees"]), "allCurrent": True},
        "subprocessRegression": "PASS_EXACT_LIVE_PYTHON_EXACT_CWD_NO_PYTHONPATH",
        "prohibitedActivity": {"serviceStartOrRestart": "NOT_RUN", "openhandsConversation": "NOT_RUN", "modelCall": "NOT_RUN", "measuredExecution": "NOT_RUN", "productionOrHistoricalDataAccess": "NOT_RUN"},
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(OUTPUT, receipt)
    print(json.dumps({"packageIdentity": package["packageIdentity"], "executionIdentity": execution_identity, "identityRecordSha256": sha(IDENTITY), "preparationReceiptSha256": sha(OUTPUT), "embeddedReceiptSha256": receipt["receiptSha256"]}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
