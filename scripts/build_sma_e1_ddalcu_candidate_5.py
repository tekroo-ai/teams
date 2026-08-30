#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
SOURCE_LAUNCH = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-4/e1_candidate4/live-launch-definition.json"
PACKAGE_ROOT = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-5/e1_candidate5"
LAUNCH = PACKAGE_ROOT / "live-launch-definition.json"
QUALIFICATION = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-5-offline-qualification/offline-qualification.json"
PACKAGE_IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-5-package-identity.json"


def canonical(value: Any) -> bytes:
    return json.dumps(
        value,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
    ).encode()


def digest(value: Any) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_exclusive(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def build_launch() -> None:
    if LAUNCH.exists():
        raise RuntimeError("candidate-5 launch definition already exists")
    value = json.loads(SOURCE_LAUNCH.read_text(encoding="utf-8"))
    value["recordType"] = "SMA_E1_DDALCU_CANDIDATE_5_LIVE_LAUNCH_DEFINITION"
    value["modelProfile"]["maximumOutputTokens"] = 8192
    value["modelProfileCompatibility"] = {
        "acceptedM1ProfileMaximumOutputTokens": 128,
        "successorMaximumOutputTokens": 8192,
        "onlyChangedProfileField": "maximumOutputTokens",
        "acceptedM1MeasuredResponses": 72,
        "acceptedM1MaximumObservedCompletionTokens": 7,
        "acceptedM1FinishReasons": {"stop": 72},
        "classification": "COMPATIBILITY_EXTENSION_NOT_NEW_M1_QUALIFICATION",
        "evidence": "OUTPUT/phase-3/sma-m1-ddalcu-candidate-1-measured-attempt-1/evidence-ledger.jsonl",
    }
    value["disposable"].update({
        "mongoDatabase": "sma_e1_ddalcu_candidate_5",
        "semanticCollection": "sma_e1_ddalcu_candidate_5_semantic",
        "episodicCollection": "sma_e1_ddalcu_candidate_5_episodic",
        "workspaceRoot": "/Users/paul/work/tekroo-ai/sma-step15-s1-p2final/target/sma-e1-ddalcu-candidate-5",
        "launchLabel": "com.tekroo.sma-service-e1-ddalcu-candidate-5",
        "stubLaunchLabel": "com.tekroo.sma-e1-model-audit-proxy-candidate-5",
    })
    runtime = "/Users/paul/work/tekroo-ai/sma-step15-s1-p2final/target/sma-e1-ddalcu-candidate-5/runtime"
    value["evidenceFiles"] = {
        "captureAudit": runtime + "/capture-audit.jsonl",
        "operationalLog": runtime + "/operational-log.jsonl",
        "stubRaw": runtime + "/stub-raw.jsonl",
        "stubTerminal": runtime + "/stub-terminal.jsonl",
    }
    value["agentLoopAudit"].update({
        "successfulConversationStatusRequired": "finished",
        "acceptedModelFinishReasons": ["stop"],
        "truncationRejected": True,
        "finalFinishToolMessageRequiredPerUserPrompt": True,
        "intermediateReasoningCannotSatisfyModelPredicate": True,
        "failureDiagnosticsRetainedBeforeCleanup": True,
        "restartContinuityCanaryRequiredBeforeMeasuredExecution": True,
    })
    additions = [
        PACKAGE_ROOT / "__init__.py",
        PACKAGE_ROOT / "runner.py",
        PACKAGE_ROOT / "live_actions.py",
        PACKAGE_ROOT / "live_entrypoint.py",
        ROOT / "scripts/build_sma_e1_ddalcu_candidate_5.py",
        ROOT / "scripts/qualify_sma_e1_ddalcu_candidate_5.py",
    ]
    known = {binding["path"] for binding in value["boundFiles"]}
    value["boundFiles"].extend(
        {"path": str(path), "sha256": sha(path)}
        for path in additions
        if str(path) not in known
    )
    write_exclusive(LAUNCH, value)
    print(json.dumps({
        "launchDefinition": str(LAUNCH),
        "sha256": sha(LAUNCH),
        "boundFiles": len(value["boundFiles"]),
        "boundTrees": len(value["boundTrees"]),
    }, sort_keys=True))


def build_identity() -> None:
    if PACKAGE_IDENTITY.exists():
        raise RuntimeError("candidate-5 package identity already exists")
    if not LAUNCH.is_file() or not QUALIFICATION.is_file():
        raise RuntimeError("candidate-5 launch or offline qualification is missing")
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    files = [{
        "path": str(LAUNCH.relative_to(ROOT)),
        "sha256": sha(LAUNCH),
    }]
    for binding in launch["boundFiles"]:
        path = Path(binding["path"])
        if sha(path) != binding["sha256"]:
            raise RuntimeError(f"candidate-5 bound file drift: {path}")
        files.append({
            "path": str(path.relative_to(ROOT)) if path.is_relative_to(ROOT) else str(path),
            "sha256": binding["sha256"],
        })
    qualification = json.loads(QUALIFICATION.read_text(encoding="utf-8"))
    subject = {
        "lineage": "SMA-E1-DDALCU-CANDIDATE-5",
        "predecessorPackageIdentity": "c6bede7336f11e1be5c5c958737f88ccbdf8a0b6cf468da3d1db00ff722be5a4",
        "changeScope": "HARNESS_PRACTICAL_OUTPUT_SUCCESSFUL_TERMINAL_AND_FINAL_ANSWER_CORRECTION_ONLY",
        "files": files,
        "offlineQualification": {
            "path": str(QUALIFICATION.relative_to(ROOT)),
            "sha256": sha(QUALIFICATION),
            "embeddedReceiptSha256": qualification["receiptSha256"],
        },
        "science": {
            "corpusSha256": "69ad47c6b73f3d3d5b326e3c1817f22f1d5aa6e6ebe91970a448ec21dd6778c2",
            "preregistrationSha256": "de9f90bdbbf5a422c2d77dc1e958ac27a09c6a6e2977a198d2cc6ca07c5fd045",
            "scenarios": 18,
            "repetitions": 96,
            "changed": False,
        },
        "modelProfile": launch["modelProfile"],
        "modelProfileCompatibility": launch["modelProfileCompatibility"],
        "agentLoopAudit": launch["agentLoopAudit"],
    }
    value = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_5_PACKAGE_IDENTITY",
        "status": "PREPARED_NOT_ACCEPTED_NOT_EXECUTION_AUTHORITY",
        "identityAlgorithm": "SHA256_CANONICAL_JSON_OF_IDENTITY_SUBJECT",
        "identitySubject": subject,
        "packageIdentity": digest(subject),
        "authority": {
            "accepted": False,
            "executionIdentityPrepared": False,
            "liveExecution": False,
        },
    }
    write_exclusive(PACKAGE_IDENTITY, value)
    print(json.dumps({
        "packageIdentity": value["packageIdentity"],
        "recordSha256": sha(PACKAGE_IDENTITY),
        "offlineQualificationSha256": sha(QUALIFICATION),
    }, sort_keys=True))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("phase", choices=("launch", "identity"))
    args = parser.parse_args()
    if args.phase == "launch":
        build_launch()
    else:
        build_identity()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
