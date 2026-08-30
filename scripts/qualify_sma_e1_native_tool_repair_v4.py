#!/usr/bin/env python3
from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import subprocess
import sys
from types import SimpleNamespace
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
S2 = ROOT / "investigations/sma-q1/layered/sma-s2-final-p2-closure"
PACKAGES = [ROOT / f"investigations/sma-q1/layered/sma-e1-ddalcu-candidate-{number}" for number in (1, 3, 4, 5, 6)]
ADJUDICATION = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-candidate-6-interaction-adjudication"
NATIVE = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair"
V4 = ROOT / "investigations/sma-q1/layered/sma-e1-ddalcu-native-tool-repair-v4"
for package in (S2, *PACKAGES, ADJUDICATION, NATIVE, V4):
    sys.path.insert(0, str(package))

from e1_native_v4.runner import retained_workspace_role, semantic_with_provider_reasoning  # noqa: E402
from t1.model import canonical_bytes, digest, file_sha256  # noqa: E402


SOURCE_WALK = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v3-all-scenario-canary/execution-walk.jsonl"
BASE_OFFLINE = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-offline-qualification/offline-qualification.json"
OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-native-tool-repair-v4-offline-qualification/offline-qualification.json"
SOURCES = (
    V4 / "e1_native_v4/__init__.py",
    V4 / "e1_native_v4/runner.py",
    V4 / "e1_native_v4/live_entrypoint.py",
    ROOT / "scripts/qualify_sma_e1_native_tool_repair_v4.py",
    ROOT / "scripts/execute_sma_e1_native_tool_repair_v4_continuation.py",
    ROOT / "scripts/build_sma_e1_native_tool_repair_v4.py",
)


def source_case13() -> dict[str, Any]:
    rows = [json.loads(line) for line in SOURCE_WALK.read_text(encoding="utf-8").splitlines() if line]
    if len(rows) != 13:
        raise RuntimeError("v3 source walk is not the 13-record checkpoint")
    return rows[-1]


def controls() -> list[dict[str, str]]:
    row = source_case13()
    observed = row["observation"]
    descriptor = SimpleNamespace(workspace_roles=tuple(observed["descriptor"]["workspace_roles"]))
    fake = SimpleNamespace(descriptor=descriptor, conversations=observed["conversations"])
    roles = ("alpha-1", "alpha-2", "beta-1", "beta-2")
    derived = {role: retained_workspace_role(fake, role) for role in roles}
    requests = observed["model_requests"]
    partition_evidence = all(
        ("mem-alpha-timeout" in json.dumps(request) and "mem-beta-port" not in json.dumps(request))
        if str(request.get("interactionRole", "")).startswith("alpha-")
        else ("mem-beta-port" in json.dumps(request) and "mem-alpha-timeout" not in json.dumps(request))
        for request in requests
    )
    response_rows = [receipt["row"] for receipt in observed["raw_receipts"] if receipt.get("kind") == "e1_model_audit_response_raw"]
    request_rows = [receipt["row"] for receipt in observed["raw_receipts"] if receipt.get("kind") == "e1_model_audit_request_raw"]
    request_payloads = [json.loads(base64.b64decode(item["requestBodyBase64"])) for item in request_rows]
    payloads = [json.loads(base64.b64decode(item["responseBodyBase64"])) for item in response_rows]
    reasoning = [payload["choices"][0].get("message", {}).get("reasoning_content") for payload in payloads]
    finish_payloads = [payload["choices"][0].get("message", {}).get("tool_calls") for payload in payloads]
    combined = semantic_with_provider_reasoning("<function=finish>\n<parameter=message>alpha</parameter>\n</function>", next(value for value in reasoning if value))
    base = json.loads(BASE_OFFLINE.read_text(encoding="utf-8"))
    values = {
        "source_checkpoint_exact": row["vector"]["operationKey"] == "SMA-S2-013-FOUR-CHANNEL-CONCURRENCY#001" and observed.get("failure_detail") == "E1 model emitted thinking/reasoning content",
        "source_first_twelve_green": all(json.loads(line)["vector"]["overallRepetitionVerdict"] == "PASS" for line in SOURCE_WALK.read_text(encoding="utf-8").splitlines()[:12]),
        "four_actual_workspace_roles_derived": derived == {"alpha-1": "alpha", "alpha-2": "alpha", "beta-1": "beta", "beta-2": "beta"},
        "four_request_partitions_were_isolated": len(requests) == 4 and partition_evidence,
        "provider_reasoning_is_retained": bool(combined and next(value for value in reasoning if value) in combined),
        "native_finish_is_retained_with_reasoning": all(value for value in finish_payloads),
        "thinking_request_control_remains_false": len(request_payloads) == 4 and all((payload.get("chat_template_kwargs") or {}).get("enable_thinking") is False for payload in request_payloads),
        "base_native_tool_controls_green": base.get("status") == "PASS_NATIVE_TOOL_OFFLINE_QUALIFICATION" and base.get("summary") == {"passed": 20, "total": 20},
    }
    return [{"control": key, "verdict": "PASS" if value else "FAIL"} for key, value in values.items()]


def source_identity() -> tuple[list[dict[str, str]], str]:
    rows = [{"path": str(path.relative_to(ROOT)), "sha256": file_sha256(path)} for path in SOURCES]
    return rows, digest(rows)


def main() -> int:
    child = "--child" in sys.argv
    result = controls()
    if child:
        print(json.dumps(result, sort_keys=True, separators=(",", ":")))
        return 0 if all(row["verdict"] == "PASS" for row in result) else 2
    completed = subprocess.run([sys.executable, "-O", str(Path(__file__).resolve()), "--child"], cwd=ROOT, capture_output=True, text=True, check=False)
    optimized = json.loads(completed.stdout) if completed.returncode == 0 else []
    result.append({"control": "optimized_runtime_equivalence", "verdict": "PASS" if optimized == result else "FAIL"})
    sources, identity = source_identity()
    value = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_NATIVE_TOOL_REPAIR_V4_OFFLINE_QUALIFICATION",
        "status": "PASS_NATIVE_TOOL_V4_OFFLINE_QUALIFICATION" if all(row["verdict"] == "PASS" for row in result) else "NO_GO_NATIVE_TOOL_V4_OFFLINE_QUALIFICATION",
        "packageSourceIdentity": identity,
        "sources": sources,
        "controls": result,
        "summary": {"passed": sum(row["verdict"] == "PASS" for row in result), "total": len(result)},
        "sourceCheckpoint": {"path": str(SOURCE_WALK.relative_to(ROOT)), "sha256": file_sha256(SOURCE_WALK)},
        "baseOffline": {"path": str(BASE_OFFLINE.relative_to(ROOT)), "sha256": file_sha256(BASE_OFFLINE)},
        "liveActivity": "NOT_RUN",
        "modelCalls": 0,
        "measuredExecution": "NOT_RUN",
    }
    value["receiptSha256"] = digest(value)
    if OUTPUT.exists():
        raise RuntimeError("v4 offline qualification output already exists")
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(OUTPUT, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical_bytes(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())
    print(json.dumps({"status": value["status"], "summary": value["summary"], "packageSourceIdentity": identity, "fileSha256": file_sha256(OUTPUT), "receiptSha256": value["receiptSha256"]}, sort_keys=True))
    return 0 if value["status"].startswith("PASS_") else 2


if __name__ == "__main__":
    raise SystemExit(main())
