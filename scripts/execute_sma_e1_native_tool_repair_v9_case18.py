#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
V9 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9"
sys.path.insert(0, str(V9))

import execute_sma_e1_native_tool_repair_v5_case18 as implementation  # noqa: E402
import e1_native_v9.live_entrypoint as entrypoint  # noqa: E402


def main() -> int:
    identity = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9-package-identity.json"
    acceptance = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9-acceptance.json"
    live_acceptance = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9-live-acceptance.json"
    implementation.PACKAGE = identity
    implementation.ACCEPTANCE = acceptance
    implementation.LIVE_ACCEPTANCE = live_acceptance
    implementation.AUTHORITY_RECORD = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v9-case18-authorization.json"
    implementation.OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v9-offline-qualification/offline-qualification.json"
    implementation.OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v9-case18"
    entrypoint.LIVE_ACCEPTANCE = live_acceptance
    implementation.compose = entrypoint.compose
    return implementation.main()


if __name__ == "__main__":
    raise SystemExit(main())
