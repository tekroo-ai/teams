#!/usr/bin/env python3
"""Execute the corrected V2 OpenHands response-lane qualification."""

from pathlib import Path

import openhands_response_lane_qualification_v1 as lane


ROOT = Path("/Users/paul/work/tekroo-ai/teams")
lane.RUNNER = Path(__file__)
lane.MANIFEST = ROOT / "investigations/openhands-lane/qualification-v2.json"
lane.AUTHORIZATION = ROOT / "investigations/openhands-lane/qualification-v2-authorization.json"
lane.OUTPUT = ROOT / "OUTPUT/phase-3/openhands-response-lane-v2"
lane.JOURNAL = lane.OUTPUT / "raw-receipts.jsonl"
lane.SUMMARY = lane.OUTPUT / "execution-receipt.json"
lane.WORKSPACE = ROOT / "target/openhands-response-lane-v2"
lane.MANIFEST_SHA = "c5fa9e62e15c35a6cdbaeeb32bb28698636ff6fb8033209e3530c6e303cd1e74"


if __name__ == "__main__":
    raise SystemExit(lane.execute())
