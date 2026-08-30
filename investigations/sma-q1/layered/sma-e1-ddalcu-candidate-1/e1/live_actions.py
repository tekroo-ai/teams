#!/usr/bin/env python3
"""E1 live actions: accepted S2 lifecycle plus a transparent model audit proxy.

This file is unreachable from offline qualification. A later execution identity
must bind it and a single-use authority must be consumed before any action.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import time
from typing import Any, Mapping

from t1 import live_actions as s2


ACTIONS = s2.ACTIONS


def _load(path: Path) -> Mapping[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if value.get("recordType") != "SMA_E1_DDALCU_CANDIDATE_1_LIVE_LAUNCH_DEFINITION":
        raise ValueError("wrong E1 launch definition")
    if set(value.get("actions", [])) != ACTIONS:
        raise ValueError("incomplete E1 action set")
    for binding in value.get("boundFiles", []):
        file = Path(binding["path"])
        if not file.is_file() or s2._sha(file) != binding["sha256"]:
            raise ValueError(f"bound E1 live file drift: {file}")
    for binding in value.get("boundTrees", []):
        root = Path(binding["path"])
        if not root.is_dir() or s2._tree_sha(root) != binding["sha256"]:
            raise ValueError(f"bound E1 live tree drift: {root}")
    profile = value.get("modelProfile", {})
    exact = {
        "apiRoot":"http://127.0.0.1:8802/v1",
        "model":"ddalcu--Qwen3.8-27B-MLX-Serve-8bit",
        "thinking":False,
        "tools":[],
        "temperature":0,
        "maximumOutputTokens":128,
        "requestTimeoutMilliseconds":120000,
    }
    if any(profile.get(key) != expected for key, expected in exact.items()):
        raise ValueError("E1 model profile drift")
    return value


class Engine(s2.Engine):
    def start_stub(self) -> Mapping[str, Any]:
        self.stop_pid("stubPid")
        self.reset_stub_evidence()
        evidence = self.d["evidenceFiles"]
        proxy = Path(self.d["environment"]["teamsRoot"]) / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1/e1/model_audit_proxy.py"
        command = [
            self.d["environment"]["livePython"], str(proxy),
            "--host", "127.0.0.1", "--port", str(self.d["ports"]["stub"]),
            "--upstream", self.d["modelProfile"]["apiRoot"],
            "--request-journal", evidence["stubRaw"],
            "--response-journal", evidence["stubTerminal"],
            "--ready-file", str(self.runtime / "stub-ready.json"),
        ]
        process = subprocess.Popen(
            command, cwd=self.d["environment"]["teamsRoot"],
            stdout=self.runtime.joinpath("model-audit.out.log").open("ab"),
            stderr=self.runtime.joinpath("model-audit.err.log").open("ab"),
            start_new_session=True,
        )
        ready_path = self.runtime / "stub-ready.json"
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline and not ready_path.is_file():
            if process.poll() is not None:
                raise RuntimeError(f"model audit proxy exited before readiness: {process.returncode}")
            time.sleep(0.05)
        if not ready_path.is_file():
            raise RuntimeError("model audit proxy readiness deadline exceeded")
        ready = json.loads(ready_path.read_text(encoding="utf-8"))
        if ready.get("pid") != process.pid or ready.get("port") != self.d["ports"]["stub"] or ready.get("upstream") != self.d["modelProfile"]["apiRoot"]:
            raise RuntimeError("model audit proxy readiness identity mismatch")
        state = self.state()
        state["stubPid"] = process.pid
        self.save(state)
        return {
            "pid":process.pid,"ready":ready,
            "argvSha256":hashlib.sha256(json.dumps(command, separators=(",", ":")).encode()).hexdigest(),
            "role":"TRANSPARENT_MODEL_AUDIT_PROXY",
        }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--definition", required=True)
    parser.add_argument("action", choices=sorted(ACTIONS))
    parser.add_argument("arguments", nargs="*")
    args = parser.parse_args()
    result = Engine(_load(Path(args.definition).resolve())).run(args.action, list(args.arguments))
    print(json.dumps(result, sort_keys=True, separators=(",", ":"), default=str))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
