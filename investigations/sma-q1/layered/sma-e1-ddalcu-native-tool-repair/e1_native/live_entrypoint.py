from __future__ import annotations

import json
from pathlib import Path
import sys
from typing import Any, Mapping


ROOT = Path(__file__).resolve().parents[5]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [
    ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}"
    for number in (1, 3, 4, 5, 6)
]
ADJUDICATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-interaction-adjudication"
NATIVE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair"
for package in (S2_PACKAGE, *PACKAGES, ADJUDICATION, NATIVE):
    sys.path.insert(0, str(package))

from e1.runner import E1_CASES, E1RunnerConfiguration, e1_plan  # noqa: E402
from e1_candidate5.live_entrypoint import tree_sha  # noqa: E402
from e1_native.runner import NativeToolRunner  # noqa: E402
from t1.model import FailureClass, LiveAuthority, OperationKey, RunMode, file_sha256, require  # noqa: E402
from t1.ports import LiveConfiguration, LocalRawPorts  # noqa: E402
from t1.truth import ProductTruth  # noqa: E402


DEFINITION = Path(__file__).with_name("live-launch-definition.json")


def load_definition(path: Path = DEFINITION) -> Mapping[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    require(value.get("recordType") == "SMA_E1_DDALCU_CANDIDATE_6_LIVE_LAUNCH_DEFINITION", FailureClass.SAFETY, "wrong native-repair live definition")
    repair = value.get("nativeToolRepair") or {}
    require(repair.get("enabled") is True, FailureClass.SAFETY, "native-tool repair is not enabled")
    require(repair.get("protocol") == "OPENAI_NATIVE_TOOLS", FailureClass.SAFETY, "native-tool protocol drift")
    require(Path(str(repair.get("actionScript") or "")).is_file(), FailureClass.SAFETY, "native-tool action script is missing")
    require(
        value.get("authority") == {
            "defaultDeny": True,
            "acceptedPackageRequired": True,
            "singleUseRequired": True,
            "modeExact": True,
            "consumeBeforeFirstRawCall": True,
        },
        FailureClass.SAFETY,
        "native-repair authority policy drift",
    )
    for binding in value.get("boundFiles", []):
        file = Path(binding["path"])
        require(file.is_file() and file_sha256(file) == binding["sha256"], FailureClass.SAFETY, f"native-repair bound file drift: {file}")
    for binding in value.get("boundTrees", []):
        root = Path(binding["path"])
        require(root.is_dir() and tree_sha(root) == binding["sha256"], FailureClass.SAFETY, f"native-repair bound tree drift: {root}")
    return value


def compose(repo: Path, mode: RunMode, authority: LiveAuthority, definition_path: Path = DEFINITION):
    require(mode in {RunMode.ZERO_CREDIT_DRESS, RunMode.MEASURED}, FailureClass.SAFETY, "native repair requires a live mode")
    definition = load_definition(definition_path)
    disposable = definition["disposable"]
    environment = definition["environment"]
    endpoints = definition["loopback"]
    evidence = definition["evidenceFiles"]
    action_script = Path(definition["nativeToolRepair"]["actionScript"])
    prefix = (environment["livePython"], str(action_script), "--definition", str(definition_path.resolve()))
    actions = {name: prefix + (name,) for name in definition["actions"]}
    live = LiveConfiguration(
        openhands_base_url=endpoints["openhands"],
        sma_base_url=endpoints["sma"],
        qdrant_base_url=endpoints["qdrant"],
        mongo_uri=environment["mongoUri"],
        mongo_database=disposable["mongoDatabase"],
        semantic_collection=disposable["semanticCollection"],
        episodic_collection=disposable["episodicCollection"],
        stub_raw_jsonl=evidence["stubRaw"],
        stub_terminal_jsonl=evidence["stubTerminal"],
        operational_log_jsonl=evidence["operationalLog"],
        session_key_file=environment["sessionKeyFile"],
        workspace_root=disposable["workspaceRoot"],
        process_cwd=str(repo),
        process_allowed_root=str(repo.parent),
        allowed_processes=actions,
    )
    config = E1RunnerConfiguration(
        mongo_database=disposable["mongoDatabase"],
        semantic_collection=disposable["semanticCollection"],
        episodic_collection=disposable["episodicCollection"],
        workspace_root=disposable["workspaceRoot"],
        profile=environment["profile"],
        session_header_value="LIVE_SESSION_KEY_INJECTED_BY_LOCAL_RAW_PORTS",
        stub_raw_path=evidence["stubRaw"],
        stub_terminal_path=evidence["stubTerminal"],
        operational_log_path=evidence["operationalLog"],
        stub_base_url=endpoints["stub"],
        transport_failure_base_url=endpoints["transportFailure"],
        hook_config={
            "user_prompt_submit": [{
                "matcher": "*",
                "hooks": [{
                    "type": "command",
                    "command": str(Path(environment["smaRoot"]) / ".openhands/hooks/sma_context_hook.py"),
                    "timeout": 1,
                }],
            }],
        },
    )
    runner = NativeToolRunner(ProductTruth.load(repo), LocalRawPorts(live, authority, mode), config, mode)
    plan = e1_plan() if mode == RunMode.MEASURED else tuple(OperationKey(str(row["s2Id"]), 1) for row in E1_CASES)
    return runner, plan
