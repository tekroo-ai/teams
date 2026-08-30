#!/usr/bin/env python3
"""Self-contained E1 live-action launcher.

Candidate 1 assumed its sibling S2 package was already on PYTHONPATH. Raw
subprocesses do not inherit that package path. This successor establishes both
frozen dependency roots before importing any action implementation.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import sys
from typing import Any, Mapping


ROOT = Path(__file__).resolve().parents[5]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
CANDIDATE_1_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
sys.path.insert(0, str(S2_PACKAGE))
sys.path.insert(0, str(CANDIDATE_1_PACKAGE))

from e1.live_actions import ACTIONS, Engine  # noqa: E402
from t1 import live_actions as s2  # noqa: E402


def load_definition(path: Path) -> Mapping[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if value.get("recordType") != "SMA_E1_DDALCU_CANDIDATE_2_LIVE_LAUNCH_DEFINITION":
        raise ValueError("wrong E1 candidate-2 launch definition")
    if set(value.get("actions", [])) != ACTIONS:
        raise ValueError("incomplete E1 candidate-2 action set")
    for binding in value.get("boundFiles", []):
        file = Path(binding["path"])
        if not file.is_file() or s2._sha(file) != binding["sha256"]:
            raise ValueError(f"bound E1 candidate-2 file drift: {file}")
    for binding in value.get("boundTrees", []):
        root = Path(binding["path"])
        if not root.is_dir() or s2._tree_sha(root) != binding["sha256"]:
            raise ValueError(f"bound E1 candidate-2 tree drift: {root}")
    exact = {
        "apiRoot": "http://127.0.0.1:8802/v1",
        "model": "ddalcu--Qwen3.8-27B-MLX-Serve-8bit",
        "thinking": False,
        "tools": [],
        "temperature": 0,
        "maximumOutputTokens": 128,
        "requestTimeoutMilliseconds": 120000,
    }
    profile = value.get("modelProfile", {})
    if any(profile.get(key) != expected for key, expected in exact.items()):
        raise ValueError("E1 candidate-2 model profile drift")
    return value


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--definition", required=True)
    parser.add_argument("action", choices=sorted(ACTIONS))
    parser.add_argument("arguments", nargs="*")
    args = parser.parse_args()
    result = Engine(load_definition(Path(args.definition).resolve())).run(args.action, list(args.arguments))
    print(json.dumps(result, sort_keys=True, separators=(",", ":"), default=str))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
