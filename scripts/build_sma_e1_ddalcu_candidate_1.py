#!/usr/bin/env python3
from __future__ import annotations

import argparse
from copy import deepcopy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
OPENHANDS_ROOT = Path("/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel")
sys.path.insert(0, str(ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"))
sys.path.insert(0, str(ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"))

from e1.runner import E1_CASES, MODEL_API_ROOT, MODEL_ID
from t1.model import canonical_bytes, digest, file_sha256


PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
CORPUS = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-corpus-candidate-1.json"
PREREG = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-preregistration-candidate-1.json"
LAUNCH = PACKAGE / "e1/live-launch-definition.json"
OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-1-offline-qualification/offline-qualification.json"
IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1-package-identity.json"


def write(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(canonical_bytes(value) + b"\n")


def git_identity(path: Path) -> dict[str, str]:
    commit = subprocess.run(["git", "rev-parse", "HEAD^{commit}"], cwd=path, capture_output=True, text=True, check=True).stdout.strip()
    tree = subprocess.run(["git", "rev-parse", "HEAD^{tree}"], cwd=path, capture_output=True, text=True, check=True).stdout.strip()
    return {"commit":commit,"tree":tree}


def prepare() -> None:
    historical_path = ROOT / "investigations/sma-q1/preregistration-native-v2.json"
    historical = json.loads(historical_path.read_text(encoding="utf-8"))
    draft = json.loads((ROOT / "investigations/sma-q1/layered/sma-e1-runtime-preregistration-draft.json").read_text(encoding="utf-8"))
    draft_by_id = {row["sourceScenario"]: row for row in draft["cases"]}
    source_by_id = {row["id"]: row for row in historical["scenarioCorpus"]}
    corpus = {
        "schemaVersion":"1.0.0-candidate",
        "recordType":"SMA_E1_DDALCU_CANDIDATE_1_CORPUS",
        "status":"PREPARED_NOT_ACCEPTED_NOT_EXECUTION_AUTHORITY",
        "source":{"path":"investigations/sma-q1/preregistration-native-v2.json","sha256":file_sha256(historical_path)},
        "scenarioIntentMutation":"NONE","promptMutation":"NONE","repetitionMutation":"NONE",
        "scenarioCount":18,"repetitionCount":96,
        "cases":[{
            **dict(source_by_id[str(row["sourceId"])]),
            "s2OrchestrationCaseId":row["s2Id"],
        } for row in E1_CASES],
    }
    write(CORPUS, corpus)
    upstream = {
        "S1":{"path":"investigations/sma-q1/layered/sma-s1-p2final-r1-scientific-adjudication-acceptance.json","sha256":"3315838d0c5047189494cece49b953481431321d16e238efa338712be076bee4","claim":"SMA_SEMANTICS_QUALIFIED"},
        "S2":{"path":"investigations/sma-q1/layered/sma-s2-final-p2-composite-scientific-adjudication-acceptance.json","sha256":"cac6cd68ff7b60fdd36a593779221ce90b6553a570b71f6fb90b6f593184e0c3","claim":"OPENHANDS_SMA_BOUNDARY_QUALIFIED"},
        "M1":{"path":"investigations/sma-q1/layered/sma-m1-ddalcu-scientific-adjudication-acceptance.json","sha256":"e97a6fb8ba1dab2837f96d2253a73d142e11a055c9fbf94b67ec6fb4498b8f3e","claim":"MODEL_PROFILE_MEMORY_USE_QUALIFIED"},
    }
    prereg = {
        "schemaVersion":"1.0.0-candidate","recordType":"SMA_E1_DDALCU_CANDIDATE_1_PREREGISTRATION",
        "status":"PREPARED_NOT_ACCEPTED_NOT_EXECUTION_AUTHORITY",
        "lineageId":"SMA-E1-DDALCU-CANDIDATE-1",
        "decisiveQuestion":draft["decisiveQuestion"],"claimOnAcceptedPass":"INTEGRATED_RUNTIME_TUPLE_QUALIFIED",
        "step15ClosureOnAcceptedPass":True,"upstreamClaims":upstream,
        "corpus":{"path":str(CORPUS.relative_to(ROOT)),"sha256":file_sha256(CORPUS),"scenarios":18,"repetitions":96},
        "modelProfile":{
            "acceptedM1ProfileIdentity":"investigations/sma-q1/layered/sma-m1-ddalcu-profile-identity-candidate-1.json",
            "apiRoot":MODEL_API_ROOT,"model":MODEL_ID,"thinking":False,"thinkingControl":{"chat_template_kwargs":{"enable_thinking":False}},
            "temperature":0,"tools":[],"stream":False,"maximumOutputTokens":128,"requestTimeoutMilliseconds":120000,
            "perRequestConcurrency":1,"integratedScenario13ChannelConcurrency":4,
        },
        "verdictVector":["smaSemanticsVerdict","openHandsBoundaryVerdict","modelBehaviorVerdict","environmentVerdict","harnessVerdict","cleanupVerdict","overallRepetitionVerdict"],
        "continuation":{"safeCounterexample":"CONTINUE_TO_COMPLETE_CORPUS","safetyViolation":"STOP_IMMEDIATELY"},
        "absoluteSafetyThresholds":draft["thresholds"]["absoluteSafety"],
        "semanticThresholds":draft["thresholds"]["semantic"],
        "reliabilityThresholds":draft["thresholds"]["reliability"],
        "latencyThresholdMilliseconds":draft["thresholds"]["latencyMilliseconds"],
        "bounds":draft["thresholds"]["bounds"],
        "cases":[{
            **dict(draft_by_id[str(row["sourceId"])]),
            "s2OrchestrationCaseId":row["s2Id"],"literalPrompt":row["prompt"],
        } for row in E1_CASES],
        "evidence":{
            "rawModelRequestAndResponse":"transparent localhost audit proxy with exact operation header, lengths, hashes, and sealed base64 bodies",
            "smaAndOpenHands":"shared accepted S2 raw-receipt orchestration",
            "appendBeforeAction":True,"componentAttribution":True,"finalDisposableInventoryRequiredAbsent":True,
        },
        "authority":{"accepted":False,"offlineOnly":True,"serviceStartOrRestart":False,"openhandsConversation":False,"modelCall":False,"liveDress":False,"measuredExecution":False},
        "excludedClaims":["another runtime tuple","model routing","Teams event export or SMA live shadow","production readiness"],
    }
    write(PREREG, prereg)

    s2_launch_path = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure/t1/live-launch-definition.json"
    launch = deepcopy(json.loads(s2_launch_path.read_text(encoding="utf-8")))
    launch["recordType"] = "SMA_E1_DDALCU_CANDIDATE_1_LIVE_LAUNCH_DEFINITION"
    launch["schemaVersion"] = "1.0.0-candidate"
    launch["status"] = "PREPARED_NONLIVE_REQUIRES_ACCEPTANCE_EXECUTION_IDENTITY_AND_SINGLE_USE_AUTHORITY"
    launch["disposable"].update({
        "mongoDatabase":"sma_e1_ddalcu_candidate_1",
        "semanticCollection":"sma_e1_ddalcu_candidate_1_semantic",
        "episodicCollection":"sma_e1_ddalcu_candidate_1_episodic",
        "workspaceRoot":"/Users/paul/work/tekroo-ai/sma-step15-s1-p2final/target/sma-e1-ddalcu-candidate-1",
        "launchLabel":"com.tekroo.sma-service-e1-ddalcu-candidate-1",
        "stubLaunchLabel":"com.tekroo.sma-e1-model-audit-proxy",
    })
    launch["modelProfile"] = prereg["modelProfile"]
    runtime_root = Path(launch["disposable"]["workspaceRoot"]) / "runtime"
    launch["evidenceFiles"] = {
        "stubRaw":str(runtime_root / "stub-raw.jsonl"),
        "stubTerminal":str(runtime_root / "stub-terminal.jsonl"),
        "operationalLog":str(runtime_root / "operational-log.jsonl"),
        "captureAudit":str(runtime_root / "capture-audit.jsonl"),
    }
    source_files = [
        PACKAGE / "e1/__init__.py", PACKAGE / "e1/runner.py", PACKAGE / "e1/h0.py",
        PACKAGE / "e1/live_actions.py", PACKAGE / "e1/live_entrypoint.py", PACKAGE / "e1/model_audit_proxy.py",
        ROOT / "scripts/qualify_sma_e1_ddalcu_candidate_1.py", ROOT / "scripts/build_sma_e1_ddalcu_candidate_1.py",
        CORPUS, PREREG,
        ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure/t1/model.py",
        ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure/t1/ports.py",
        ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure/t1/runner.py",
        ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure/t1/oracles.py",
        ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure/t1/truth.py",
        ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure/t1/live_actions.py",
        OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/llm.py",
        OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/options/chat_options.py",
        OPENHANDS_ROOT / "openhands-sdk/openhands/sdk/llm/options/common.py",
    ]
    retained = [row for row in launch["boundFiles"] if "sma_s2_deterministic_model_stub_candidate_" not in row["path"]]
    bound = {row["path"]:row for row in retained}
    for path in source_files:
        bound[str(path)] = {"path":str(path),"sha256":file_sha256(path)}
    launch["boundFiles"] = [bound[key] for key in sorted(bound)]
    launch["upstreamClaims"] = upstream
    launch["corpus"] = prereg["corpus"]
    launch["prohibitedWithoutLaterAuthority"] = ["serviceStart","OpenHandsConversation","modelCall","liveDressRehearsal","measuredExecution","productionOrHistoricalDataAccess"]
    write(LAUNCH, launch)
    print(json.dumps({"corpus":file_sha256(CORPUS),"preregistration":file_sha256(PREREG),"launchDefinition":file_sha256(LAUNCH)}, sort_keys=True))


def identity() -> None:
    if not OFFLINE.is_file():
        raise SystemExit("offline qualification receipt is absent")
    files = [
        CORPUS, PREREG, LAUNCH,
        PACKAGE / "e1/__init__.py", PACKAGE / "e1/runner.py", PACKAGE / "e1/h0.py",
        PACKAGE / "e1/live_actions.py", PACKAGE / "e1/live_entrypoint.py", PACKAGE / "e1/model_audit_proxy.py",
        ROOT / "scripts/qualify_sma_e1_ddalcu_candidate_1.py", ROOT / "scripts/build_sma_e1_ddalcu_candidate_1.py",
    ]
    receipt = json.loads(OFFLINE.read_text(encoding="utf-8"))
    value = {
        "schemaVersion":"1.0.0-candidate","recordType":"SMA_E1_DDALCU_CANDIDATE_1_PACKAGE_IDENTITY",
        "status":"PREPARED_NOT_ACCEPTED_NOT_EXECUTION_AUTHORITY",
        "teamsGit":git_identity(ROOT),"smaGit":git_identity(Path("/Users/paul/work/tekroo-ai/sma-step15-s1-p2final")),
        "openHandsGit":git_identity(OPENHANDS_ROOT),
        "files":[{"path":str(path.relative_to(ROOT)),"sha256":file_sha256(path)} for path in files],
        "offlineQualification":{"path":str(OFFLINE.relative_to(ROOT)),"sha256":file_sha256(OFFLINE),"embeddedReceiptSha256":receipt["receiptSha256"]},
        "upstreamClaims":json.loads(PREREG.read_text(encoding="utf-8"))["upstreamClaims"],
        "authority":{"accepted":False,"liveExecution":False,"singleUseAttempts":0},
    }
    value["contentIdentity"] = digest(value)
    write(IDENTITY, value)
    print(json.dumps({"packageIdentity":file_sha256(IDENTITY),"contentIdentity":value["contentIdentity"]}, sort_keys=True))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("phase", choices=("prepare", "identity"))
    args = parser.parse_args()
    prepare() if args.phase == "prepare" else identity()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
