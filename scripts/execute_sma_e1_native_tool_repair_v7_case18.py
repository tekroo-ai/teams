#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
V6 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v6"
sys.path.insert(0, str(V6))

import execute_sma_e1_native_tool_repair_v5_case18 as implementation  # noqa: E402
from e1_native_v6.live_entrypoint import compose  # noqa: E402


def main() -> int:
    implementation.PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v7-package-identity.json"
    implementation.ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v7-acceptance.json"
    implementation.LIVE_ACCEPTANCE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v7-live-acceptance.json"
    implementation.AUTHORITY_RECORD = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v7-case18-authorization.json"
    implementation.OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v7-offline-compatibility/offline-qualification.json"
    implementation.OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v7-case18"
    implementation.compose = compose
    return implementation.main()


if __name__ == "__main__":
    raise SystemExit(main())
