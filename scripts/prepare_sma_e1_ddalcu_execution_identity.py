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
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-acceptance.json"
PACKAGE_IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-package-identity.json"
LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1/e1/live-launch-definition.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-execution-identity.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-1-execution-identity-preparation"


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(value: Any) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def sha(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as stream:
        while chunk := stream.read(1024 * 1024):
            value.update(chunk)
    return value.hexdigest()


def tree_sha(root: Path) -> str:
    entries = [
        [str(path.relative_to(root)), sha(path)]
        for path in sorted(root.rglob("*"))
        if path.is_file() and "__pycache__" not in path.parts and path.suffix != ".pyc"
    ]
    return hashlib.sha256(canonical(entries)).hexdigest()


def git_value(root: Path, argument: str) -> str:
    return subprocess.run(["git", "rev-parse", argument], cwd=root, capture_output=True, text=True, check=True).stdout.strip()


def git_status(root: Path) -> list[str]:
    return subprocess.run(["git", "status", "--short"], cwd=root, capture_output=True, text=True, check=True).stdout.splitlines()


def exact_file(path: Path) -> dict[str, Any]:
    if not path.is_file():
        raise RuntimeError(f"required execution file missing: {path}")
    return {"path":str(path),"sha256":sha(path),"bytes":path.stat().st_size}


def write_exclusive(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def main() -> int:
    acceptance = json.loads(ACCEPTANCE.read_text(encoding="utf-8"))
    package = json.loads(PACKAGE_IDENTITY.read_text(encoding="utf-8"))
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    if acceptance.get("status") != "ACCEPTED_FROZEN":
        raise RuntimeError("E1 package is not accepted/frozen")
    if acceptance.get("packageIdentity") != package.get("contentIdentity"):
        raise RuntimeError("accepted E1 package identity mismatch")
    for binding in launch["boundFiles"]:
        path = Path(binding["path"])
        if not path.is_file() or sha(path) != binding["sha256"]:
            raise RuntimeError(f"live bound file drift: {path}")
    for binding in launch["boundTrees"]:
        root = Path(binding["path"])
        if not root.is_dir() or tree_sha(root) != binding["sha256"]:
            raise RuntimeError(f"live bound tree drift: {root}")

    session_key = Path(launch["environment"]["sessionKeyFile"])
    session_mode = stat.S_IMODE(session_key.stat().st_mode)
    if session_mode != 0o600:
        raise RuntimeError("OpenHands session key permissions are not 0600")

    plist = Path("/Users/paul/Library/LaunchAgents/com.tekroo.openhands-agent-canvas.plist")
    wrapper = Path("/Users/paul/.local/bin/agent-canvas")
    preload = Path("/Users/paul/.local/bin/agent-canvas-loopback.mjs")
    agent_server = OPENHANDS_ROOT / ".venv/bin/agent-server"
    m1_profile = ROOT / "investigations/sma-q1/layered/sma-m1-ddalcu-profile-identity-candidate-1.json"
    m1_acceptance = ROOT / "investigations/sma-q1/layered/sma-m1-ddalcu-scientific-adjudication-acceptance.json"
    runner = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1/e1/runner.py"
    live_entrypoint = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1/e1/live_entrypoint.py"
    live_actions = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1/e1/live_actions.py"
    audit_proxy = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1/e1/model_audit_proxy.py"

    subject = {
        "acceptedPackageIdentity":package["contentIdentity"],
        "packageAcceptanceSha256":sha(ACCEPTANCE),
        "packageIdentityRecordSha256":sha(PACKAGE_IDENTITY),
        "offlineQualificationSha256":package["offlineQualification"]["sha256"],
        "liveLaunchDefinitionSha256":sha(LAUNCH),
        "runnerSha256":sha(runner),
        "liveEntrypointSha256":sha(live_entrypoint),
        "liveActionsSha256":sha(live_actions),
        "modelAuditProxySha256":sha(audit_proxy),
        "teams":{"commit":git_value(ROOT,"HEAD^{commit}"),"tree":git_value(ROOT,"HEAD^{tree}")},
        "sma":{
            "commit":git_value(SMA_ROOT,"HEAD^{commit}"),"tree":git_value(SMA_ROOT,"HEAD^{tree}"),
            "trackedStatus":git_status(SMA_ROOT),
            "runtimeJar":exact_file(Path(launch["promotionFixture"]["smaJar"])),
            "launchSupport":exact_file(SMA_ROOT / "scripts/wp5e_restart_recovery.py"),
            "hook":exact_file(SMA_ROOT / ".openhands/hooks/sma_context_hook.py"),
        },
        "openhands":{
            "commit":git_value(OPENHANDS_ROOT,"HEAD^{commit}"),"tree":git_value(OPENHANDS_ROOT,"HEAD^{tree}"),
            "trackedStatus":git_status(OPENHANDS_ROOT),
            "llmTransportSources":[
                exact_file(OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/llm.py"),
                exact_file(OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/options/chat_options.py"),
                exact_file(OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/options/common.py"),
            ],
            "venvTreeSha256":tree_sha(OPENHANDS_ROOT / ".venv"),
            "agentServer":exact_file(agent_server),
        },
        "agentCanvas":{
            "plist":exact_file(plist),"wrapper":exact_file(wrapper),"loopbackPreload":exact_file(preload),
            "packageRoot":str(CANVAS_ROOT),"packageTreeSha256":tree_sha(CANVAS_ROOT),
        },
        "sessionKey":{"path":str(session_key),"sha256":sha(session_key),"mode":"0600","secretRetained":False},
        "modelProfile":{
            "profileIdentity":exact_file(m1_profile),"scientificAcceptance":exact_file(m1_acceptance),
            "apiRoot":"http://127.0.0.1:8802/v1","model":"ddalcu--Qwen3.8-27B-MLX-Serve-8bit",
            "thinking":False,"tools":[],"temperature":0,"maximumOutputTokens":128,"requestTimeoutMilliseconds":120000,
        },
        "ports":launch["ports"],
        "disposableNamespaces":{
            "mongoDatabase":launch["disposable"]["mongoDatabase"],
            "semanticCollection":launch["disposable"]["semanticCollection"],
            "episodicCollection":launch["disposable"]["episodicCollection"],
            "workspaceRoot":launch["disposable"]["workspaceRoot"],
        },
        "boundFileCount":len(launch["boundFiles"]),"boundTreeCount":len(launch["boundTrees"]),
    }
    execution_identity = digest(subject)
    recorded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    identity = {
        "schemaVersion":"1.0.0","recordType":"SMA_E1_DDALCU_CANDIDATE_1_EXECUTION_IDENTITY",
        "status":"PREPARED_SEALED_PENDING_PRINCIPAL_ACCEPT_FREEZE","recordedAt":recorded,
        "identityAlgorithm":"SHA256_CANONICAL_JSON_OF_IDENTITY_SUBJECT","packageIdentity":package["contentIdentity"],
        "identitySubject":subject,"executionIdentity":execution_identity,
        "offlinePreparationAudit":{
            "acceptedPackageRecomputed":"PASS","launchBindingsRecomputed":"PASS",
            "runtimeDependenciesBound":"PASS","sessionKeyPermission":"PASS_0600",
            "liveServiceState":"NOT_RUN","endpointHealth":"NOT_RUN","activeWork":"NOT_RUN","namespaceAbsence":"NOT_RUN",
        },
        "limits":{"livePreflightAuthorized":False,"serviceStartsAuthorized":0,"openhandsConversationsAuthorized":0,"modelCallsAuthorized":0,"liveDressOperationsAuthorized":0,"measuredRepetitionsAuthorized":0},
        "authority":{"executionIdentityAccepted":False,"liveStatePreflight":False,"serviceStartOrRestart":False,"openhandsConversation":False,"modelCall":False,"liveDressRehearsal":False,"measuredExecution":False,"productionOrHistoricalDataAccess":False,"commit":False,"push":False},
        "nextDecision":"PRINCIPAL_ACCEPT_FREEZE_EXECUTION_IDENTITY_ONLY",
    }
    if IDENTITY.exists() or OUTPUT.exists():
        raise RuntimeError("E1 execution identity publication target already exists")
    write_exclusive(IDENTITY, identity)
    receipt = {
        "schemaVersion":"1.0.0","recordType":"SMA_E1_DDALCU_CANDIDATE_1_EXECUTION_IDENTITY_PREPARATION_RECEIPT",
        "status":"PASS_PREPARED_NOT_ACCEPTED_NOT_EXECUTION_AUTHORITY","recordedAt":recorded,
        "executionIdentity":execution_identity,"executionIdentityRecord":{"path":str(IDENTITY.relative_to(ROOT)),"sha256":sha(IDENTITY)},
        "acceptedPackage":{"path":str(ACCEPTANCE.relative_to(ROOT)),"sha256":sha(ACCEPTANCE),"packageIdentity":package["contentIdentity"]},
        "checks":{"launchBoundFiles":len(launch["boundFiles"]),"launchBoundTrees":len(launch["boundTrees"]),"allBindingsCurrent":True,"sessionKeyMode0600":True},
        "prohibitedActivity":{"serviceStartOrRestart":"NOT_RUN","endpointHealth":"NOT_RUN","openhandsConversation":"NOT_RUN","modelCall":"NOT_RUN","liveDressRehearsal":"NOT_RUN","measuredExecution":"NOT_RUN","productionOrHistoricalDataAccess":"NOT_RUN"},
        "authority":{"executionIdentityAccepted":False,"liveStatePreflight":False,"liveExecution":False},
    }
    receipt["receiptSha256"] = digest(receipt)
    write_exclusive(OUTPUT / "execution-identity-preparation-receipt.json", receipt)
    print(json.dumps({"executionIdentity":execution_identity,"identityRecordSha256":sha(IDENTITY),"preparationReceiptSha256":sha(OUTPUT / "execution-identity-preparation-receipt.json"),"embeddedReceiptSha256":receipt["receiptSha256"]}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
