#!/usr/bin/env python3
from __future__ import annotations

from dataclasses import replace
import json
from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))

import execute_sma_e1_native_tool_repair as implementation  # noqa: E402
from t1.model import file_sha256  # noqa: E402


IDENTITY = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v2-package-identity.json"
ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v2-acceptance.json"
LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v2-live-acceptance.json"
DIAGNOSTIC_OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v2-case16-diagnostic"
CANARY_OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v2-all-scenario-canary"
ORIGINAL_COMPOSE = implementation.compose


def compose(repo, mode, authority, definition_path):
    compatibility = json.loads(LIVE_ACCEPTANCE.read_text(encoding="utf-8"))
    identity = json.loads(IDENTITY.read_text(encoding="utf-8"))
    if (
        compatibility.get("status") != "ACCEPTED_FROZEN"
        or compatibility.get("packageIdentity") != identity.get("packageIdentity")
        or compatibility.get("zeroCreditOnly") is not True
        or compatibility.get("measuredExecutionAuthorized") is not False
    ):
        raise RuntimeError("v2 live acceptance compatibility record mismatch")
    compatible = replace(
        authority,
        package_manifest_path=str(LIVE_ACCEPTANCE),
        package_manifest_sha256=file_sha256(LIVE_ACCEPTANCE),
    )
    return ORIGINAL_COMPOSE(repo, mode, compatible, definition_path)


def main() -> int:
    implementation.PACKAGE = IDENTITY
    implementation.ACCEPTANCE = ACCEPTANCE
    implementation.DIAGNOSTIC_OUTPUT = DIAGNOSTIC_OUTPUT
    implementation.CANARY_OUTPUT = CANARY_OUTPUT
    implementation.compose = compose
    return implementation.main()


if __name__ == "__main__":
    raise SystemExit(main())
