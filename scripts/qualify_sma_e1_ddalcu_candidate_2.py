#!/usr/bin/env python3
from __future__ import annotations

from datetime import datetime, timezone
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
S2_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
CANDIDATE_1_PACKAGE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-1"
CANDIDATE_2 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-2/e1_candidate2"
LAUNCH = CANDIDATE_2 / "live-launch-definition.json"
sys.path.insert(0, str(S2_PACKAGE))
sys.path.insert(0, str(CANDIDATE_1_PACKAGE))

from e1.h0 import internal as candidate_1_internal  # noqa: E402
from t1.model import canonical_bytes, digest, file_sha256  # noqa: E402


def write_exclusive(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(content)
        stream.flush()
        os.fsync(stream.fileno())


def subprocess_controls() -> dict[str, Any]:
    launch = json.loads(LAUNCH.read_text(encoding="utf-8"))
    python = launch["environment"]["livePython"]
    environment = dict(os.environ)
    environment.pop("PYTHONPATH", None)
    environment.pop("PYTHONHOME", None)
    action = subprocess.run(
        [python, str(CANDIDATE_2 / "live_actions.py"), "--help"],
        cwd=ROOT,
        env=environment,
        capture_output=True,
        timeout=30,
        check=False,
    )
    if action.returncode != 0:
        raise AssertionError(f"candidate-2 raw action import failed: {action.stderr.decode(errors='replace')}")
    composition = subprocess.run(
        [python, str(CANDIDATE_2 / "live_entrypoint.py"), "--validate-only", "--definition", str(LAUNCH)],
        cwd=ROOT,
        env=environment,
        capture_output=True,
        timeout=300,
        check=False,
    )
    if composition.returncode != 0:
        raise AssertionError(f"candidate-2 composition validation failed: {composition.stderr.decode(errors='replace')}")
    return {
        "exactLivePython": python,
        "exactLiveWorkingDirectory": str(ROOT),
        "pythonPathInherited": False,
        "liveActionSubprocessImport": "PASS",
        "defaultDenyComposition": "PASS",
        "actionStdoutSha256": hashlib.sha256(action.stdout).hexdigest(),
        "compositionStdoutSha256": hashlib.sha256(composition.stdout).hexdigest(),
        "externalActions": 0,
        "modelCalls": 0,
    }


def run() -> tuple[dict[str, Any], list[dict[str, Any]]]:
    candidate_1_summary, records = candidate_1_internal(ROOT)
    if len(records) != 96 or any(row["vector"]["overallRepetitionVerdict"] != "PASS" for row in records):
        raise AssertionError("unchanged E1 offline science did not pass 96/96")
    controls = subprocess_controls()
    summary = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_2_OFFLINE_QUALIFICATION",
        "status": "PASS_OFFLINE_NOT_EXECUTION_AUTHORITY",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "predecessorPackageIdentity": "7c87e8d1348e92fc7ead0a590e8549e3403daf8be77db262115a04cc80e30f1b",
        "changeScope": "LIVE_ACTION_SUBPROCESS_DEPENDENCY_BOOTSTRAP_ONLY",
        "science": {"scenarios": 18, "repetitions": 96, "passed": 96, "failed": 0, "canonicalOutcomeSha256": candidate_1_summary["canonicalOutcomeSha256"], "changed": False},
        "launchRegression": controls,
        "launchDefinitionSha256": file_sha256(LAUNCH),
        "prohibitedActivity": {"serviceStartOrRestart": "NOT_RUN", "openhandsConversation": "NOT_RUN", "modelCall": "NOT_RUN", "measuredExecution": "NOT_RUN", "productionOrHistoricalDataAccess": "NOT_RUN"},
    }
    return summary, records


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output-directory")
    parser.add_argument("--optimized-child", action="store_true")
    args = parser.parse_args()
    summary, records = run()
    canonical_outcome = digest({"science": summary["science"], "launchRegression": summary["launchRegression"]})
    summary["canonicalOutcomeSha256"] = canonical_outcome
    if not args.optimized_child:
        child = subprocess.run(
            [sys.executable, "-O", str(Path(__file__).resolve()), "--optimized-child"],
            cwd=ROOT,
            capture_output=True,
            timeout=600,
            check=False,
        )
        if child.returncode != 0:
            raise AssertionError(f"optimized candidate-2 qualification failed: {child.stderr.decode(errors='replace')}")
        optimized = json.loads(child.stdout)
        if optimized["canonicalOutcomeSha256"] != canonical_outcome:
            raise AssertionError("candidate-2 normal/optimized outcome mismatch")
        summary["optimizedEquivalent"] = True
    summary["receiptSha256"] = digest(summary)
    if args.output_directory:
        output = Path(args.output_directory).resolve()
        output.mkdir(parents=True, exist_ok=False)
        walk = output / "offline-walk.jsonl"
        write_exclusive(walk, b"".join(canonical_bytes(row) + b"\n" for row in records))
        summary["offlineWalk"] = {"path": str(walk), "sha256": file_sha256(walk), "records": len(records)}
        summary["receiptSha256"] = digest({key: value for key, value in summary.items() if key != "receiptSha256"})
        write_exclusive(output / "offline-qualification.json", canonical_bytes(summary) + b"\n")
    else:
        print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
