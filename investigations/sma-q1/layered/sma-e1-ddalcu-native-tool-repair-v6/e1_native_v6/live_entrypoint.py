from __future__ import annotations

from dataclasses import replace
import json
from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parents[5]
S2 = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}" for number in (1, 3, 4, 5, 6)]
ADJUDICATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-interaction-adjudication"
NATIVE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair"
V4 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4"
V5 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v5"
V6 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v6"
for package in (S2, *PACKAGES, ADJUDICATION, NATIVE, V4, V5, V6):
    sys.path.insert(0, str(package))

from e1.runner import E1_CASES, E1RunnerConfiguration  # noqa: E402
from e1_native.live_entrypoint import load_definition  # noqa: E402
from e1_native_v6.runner import NativeToolRunnerV6  # noqa: E402
from t1.model import FailureClass, LiveAuthority, OperationKey, RunMode, file_sha256, require  # noqa: E402
from t1.ports import LiveConfiguration, LocalRawPorts  # noqa: E402
from t1.truth import ProductTruth  # noqa: E402


LAUNCH = NATIVE / "e1_native/live-launch-definition.json"
LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v6-live-acceptance.json"


def compose(repo: Path, mode: RunMode, authority: LiveAuthority, definition_path: Path = LAUNCH):
    require(mode == RunMode.ZERO_CREDIT_DRESS, FailureClass.SAFETY, "v6 continuation is zero-credit only")
    definition = load_definition(definition_path)
    accepted = json.loads(LIVE_ACCEPTANCE.read_text(encoding="utf-8"))
    identity = json.loads(Path(authority.execution_identity_path).read_text(encoding="utf-8"))
    require(accepted.get("status") == "ACCEPTED_FROZEN" and accepted.get("packageIdentity") == identity.get("packageIdentity"), FailureClass.SAFETY, "v6 live acceptance mismatch")
    authority = replace(authority, package_manifest_path=str(LIVE_ACCEPTANCE), package_manifest_sha256=file_sha256(LIVE_ACCEPTANCE))
    disposable, environment, endpoints, evidence = definition["disposable"], definition["environment"], definition["loopback"], definition["evidenceFiles"]
    action_script = Path(definition["nativeToolRepair"]["actionScript"])
    prefix = (environment["livePython"], str(action_script), "--definition", str(definition_path.resolve()))
    actions = {name: prefix + (name,) for name in definition["actions"]}
    live = LiveConfiguration(openhands_base_url=endpoints["openhands"], sma_base_url=endpoints["sma"], qdrant_base_url=endpoints["qdrant"], mongo_uri=environment["mongoUri"], mongo_database=disposable["mongoDatabase"], semantic_collection=disposable["semanticCollection"], episodic_collection=disposable["episodicCollection"], stub_raw_jsonl=evidence["stubRaw"], stub_terminal_jsonl=evidence["stubTerminal"], operational_log_jsonl=evidence["operationalLog"], session_key_file=environment["sessionKeyFile"], workspace_root=disposable["workspaceRoot"], process_cwd=str(repo), process_allowed_root=str(repo.parent), allowed_processes=actions)
    config = E1RunnerConfiguration(mongo_database=disposable["mongoDatabase"], semantic_collection=disposable["semanticCollection"], episodic_collection=disposable["episodicCollection"], workspace_root=disposable["workspaceRoot"], profile=environment["profile"], session_header_value="LIVE_SESSION_KEY_INJECTED_BY_LOCAL_RAW_PORTS", stub_raw_path=evidence["stubRaw"], stub_terminal_path=evidence["stubTerminal"], operational_log_path=evidence["operationalLog"], stub_base_url=endpoints["stub"], transport_failure_base_url=endpoints["transportFailure"], hook_config={"user_prompt_submit": [{"matcher": "*", "hooks": [{"type": "command", "command": str(Path(environment["smaRoot"]) / ".openhands/hooks/sma_context_hook.py"), "timeout": 1}]}]})
    return NativeToolRunnerV6(ProductTruth.load(repo), LocalRawPorts(live, authority, mode), config, mode), tuple(OperationKey(str(row["s2Id"]), 1) for row in E1_CASES)
