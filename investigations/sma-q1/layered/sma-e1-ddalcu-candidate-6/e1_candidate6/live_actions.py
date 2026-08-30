#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import time
from typing import Any, Mapping


ROOT = Path(__file__).resolve().parents[5]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
CANDIDATE_1_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
sys.path.insert(0, str(S2_PACKAGE))
sys.path.insert(0, str(CANDIDATE_1_PACKAGE))

from e1.live_actions import ACTIONS, Engine as Candidate1Engine  # noqa: E402
from t1 import live_actions as s2  # noqa: E402


def load_definition(path: Path) -> Mapping[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if value.get("recordType") != "SMA_E1_DDALCU_CANDIDATE_6_LIVE_LAUNCH_DEFINITION":
        raise ValueError("wrong E1 candidate-6 launch definition")
    if set(value.get("actions", [])) != ACTIONS:
        raise ValueError("incomplete E1 candidate-6 action set")
    if Path(value.get("modelAuditProxyPath", "")).resolve() != Path(
        __file__
    ).with_name("model_audit_proxy.py").resolve():
        raise ValueError("candidate-6 interaction-bound model proxy drift")
    for binding in value.get("boundFiles", []):
        file = Path(binding["path"])
        if not file.is_file() or s2._sha(file) != binding["sha256"]:
            raise ValueError(f"bound E1 candidate-6 file drift: {file}")
    for binding in value.get("boundTrees", []):
        root = Path(binding["path"])
        if not root.is_dir() or s2._tree_sha(root) != binding["sha256"]:
            raise ValueError(f"bound E1 candidate-6 tree drift: {root}")
    exact = {
        "apiRoot": "http://127.0.0.1:8802/v1",
        "model": "ddalcu--Qwen3.8-27B-MLX-Serve-8bit",
        "thinking": False,
        "tools": [],
        "temperature": 0,
        "maximumOutputTokens": 8192,
        "requestTimeoutMilliseconds": 120000,
    }
    if any(
        value.get("modelProfile", {}).get(key) != expected
        for key, expected in exact.items()
    ):
        raise ValueError("E1 candidate-6 model profile drift")
    return value


class Engine(Candidate1Engine):
    def start_stub(self) -> Mapping[str, Any]:
        self.stop_pid("stubPid")
        self.reset_stub_evidence()
        evidence = self.d["evidenceFiles"]
        proxy = Path(self.d["modelAuditProxyPath"])
        command = [
            self.d["environment"]["livePython"],
            str(proxy),
            "--host",
            "127.0.0.1",
            "--port",
            str(self.d["ports"]["stub"]),
            "--upstream",
            self.d["modelProfile"]["apiRoot"],
            "--request-journal",
            evidence["stubRaw"],
            "--response-journal",
            evidence["stubTerminal"],
            "--ready-file",
            str(self.runtime / "stub-ready.json"),
        ]
        process = subprocess.Popen(
            command,
            cwd=self.d["environment"]["teamsRoot"],
            stdout=self.runtime.joinpath("model-audit.out.log").open("ab"),
            stderr=self.runtime.joinpath("model-audit.err.log").open("ab"),
            start_new_session=True,
        )
        ready_path = self.runtime / "stub-ready.json"
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline and not ready_path.is_file():
            if process.poll() is not None:
                raise RuntimeError(
                    "model audit proxy exited before readiness: "
                    f"{process.returncode}"
                )
            time.sleep(0.05)
        if not ready_path.is_file():
            raise RuntimeError("model audit proxy readiness deadline exceeded")
        ready = json.loads(ready_path.read_text(encoding="utf-8"))
        if (
            ready.get("pid") != process.pid
            or ready.get("port") != self.d["ports"]["stub"]
            or ready.get("upstream") != self.d["modelProfile"]["apiRoot"]
            or ready.get("interactionBinding")
            != "X-SMA-E1-Interaction-Role"
        ):
            raise RuntimeError("model audit proxy readiness identity mismatch")
        state = self.state()
        state["stubPid"] = process.pid
        self.save(state)
        return {
            "pid": process.pid,
            "ready": ready,
            "argvSha256": hashlib.sha256(
                json.dumps(command, separators=(",", ":")).encode()
            ).hexdigest(),
            "role": "INTERACTION_BOUND_MODEL_AUDIT_PROXY",
        }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--definition", required=True)
    parser.add_argument("action", choices=sorted(ACTIONS))
    parser.add_argument("arguments", nargs="*")
    args = parser.parse_args()
    result = Engine(load_definition(Path(args.definition).resolve())).run(
        args.action,
        list(args.arguments),
    )
    print(json.dumps(result, sort_keys=True, separators=(",", ":"), default=str))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
