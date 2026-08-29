"""Default-deny composition of the frozen runner with the live raw ports."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
from typing import Any, Mapping

from .model import FailureClass, HarnessFailure, LiveAuthority, RunMode, file_sha256, require
from .ports import LiveConfiguration, LocalRawPorts
from .runner import Runner, RunnerConfiguration, dress_plan, measured_plan
from .truth import ProductTruth


DEFINITION = Path(__file__).with_name("live-launch-definition.json")


def _tree_sha(root: Path) -> str:
    entries = [
        [str(path.relative_to(root)), file_sha256(path)]
        for path in sorted(root.rglob("*"))
        if path.is_file() and "__pycache__" not in path.parts and path.suffix != ".pyc"
    ]
    return hashlib.sha256(json.dumps(entries, separators=(",", ":"), ensure_ascii=False).encode()).hexdigest()


def load_definition(path: Path = DEFINITION) -> Mapping[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    require(value.get("recordType") == "SMA_S2_FINAL_P2_FROZEN_LIVE_LAUNCH_DEFINITION", FailureClass.SAFETY, "wrong live launch definition")
    require(value.get("status") == "FROZEN_NONLIVE_REQUIRES_SEPARATE_EXECUTION_IDENTITY_AND_SINGLE_USE_AUTHORITY", FailureClass.SAFETY, "live definition is not frozen")
    require(value.get("authority") == {"defaultDeny": True, "acceptedPackageRequired": True, "singleUseRequired": True, "modeExact": True, "consumeBeforeFirstRawCall": True}, FailureClass.SAFETY, "live authority policy drift")
    for binding in value.get("boundFiles", []):
        file = Path(binding["path"])
        require(file.is_file() and file_sha256(file) == binding["sha256"], FailureClass.SAFETY, f"live bound file drift: {file}")
    for binding in value.get("boundTrees", []):
        root = Path(binding["path"])
        require(root.is_dir() and _tree_sha(root) == binding["sha256"], FailureClass.SAFETY, f"live bound tree drift: {root}")
    return value


def compose(repo: Path, mode: RunMode, authority: LiveAuthority, definition_path: Path = DEFINITION) -> tuple[Runner, tuple[Any, ...]]:
    require(mode in {RunMode.ZERO_CREDIT_DRESS, RunMode.MEASURED}, FailureClass.SAFETY, "live entrypoint requires live mode")
    definition = load_definition(definition_path)
    disposable = definition["disposable"]; environment = definition["environment"]; endpoints = definition["loopback"]; evidence = definition["evidenceFiles"]
    action_script = Path(__file__).with_name("live_actions.py")
    process_prefix = (environment["livePython"], str(action_script), "--definition", str(definition_path.resolve()))
    actions = {name: process_prefix + (name,) for name in definition["actions"]}
    live = LiveConfiguration(
        openhands_base_url=endpoints["openhands"], sma_base_url=endpoints["sma"], qdrant_base_url=endpoints["qdrant"],
        mongo_uri=environment["mongoUri"], mongo_database=disposable["mongoDatabase"], semantic_collection=disposable["semanticCollection"], episodic_collection=disposable["episodicCollection"],
        stub_raw_jsonl=evidence["stubRaw"], stub_terminal_jsonl=evidence["stubTerminal"], operational_log_jsonl=evidence["operationalLog"],
        session_key_file=environment["sessionKeyFile"], workspace_root=disposable["workspaceRoot"], process_cwd=str(repo), process_allowed_root=str(repo.parent), allowed_processes=actions,
    )
    runner_config = RunnerConfiguration(
        mongo_database=disposable["mongoDatabase"], semantic_collection=disposable["semanticCollection"], episodic_collection=disposable["episodicCollection"],
        workspace_root=disposable["workspaceRoot"], profile=environment["profile"], session_header_value="LIVE_SESSION_KEY_INJECTED_BY_LOCAL_RAW_PORTS",
        stub_raw_path=evidence["stubRaw"], stub_terminal_path=evidence["stubTerminal"], operational_log_path=evidence["operationalLog"], stub_base_url=endpoints["stub"], transport_failure_base_url=endpoints["transportFailure"],
        hook_config={"user_prompt_submit": [{"matcher": "*", "hooks": [{"type": "command", "command": str(Path(environment["smaRoot"]) / ".openhands/hooks/sma_context_hook.py"), "timeout": 1}]}]},
    )
    truth = ProductTruth.load(repo)
    runner = Runner(truth, LocalRawPorts(live, authority, mode), runner_config, mode)
    plan = dress_plan(truth) if mode == RunMode.ZERO_CREDIT_DRESS else measured_plan(truth)
    return runner, plan


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--validate-only", action="store_true")
    parser.add_argument("--definition", default=str(DEFINITION))
    args = parser.parse_args()
    if not args.validate_only:
        raise HarnessFailure(FailureClass.SAFETY, "live execution requires programmatic composition with separately frozen LiveAuthority")
    value = load_definition(Path(args.definition).resolve())
    print(json.dumps({"status": "PASS_DEFAULT_DENY_LIVE_COMPOSITION", "actions": len(value["actions"]), "liveExecution": False}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
