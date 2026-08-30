#!/usr/bin/env python3
from __future__ import annotations

from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))

import execute_sma_e1_ddalcu_candidate_6_continuation as continuation  # noqa: E402
from t1.model import canonical_bytes, digest, file_sha256  # noqa: E402


OUTPUT = ROOT / "OUTPUT/phase-3/sma-e1-ddalcu-candidate-6-continuation-readiness"


def main() -> int:
    package = json.loads(continuation.PACKAGE.read_text(encoding="utf-8"))
    acceptance = json.loads(continuation.ACCEPTANCE.read_text(encoding="utf-8"))
    offline = json.loads(continuation.OFFLINE_RECEIPT.read_text(encoding="utf-8"))
    if (
        acceptance.get("status") != "ACCEPTED_FROZEN"
        or acceptance.get("packageIdentity") != package.get("packageIdentity")
        or offline.get("status") != "PASS_OFFLINE_INTERACTION_ADJUDICATION_QUALIFIED"
        or offline.get("implementation", {}).get("verdictSourceSha256")
        != file_sha256(continuation.VERDICT_SOURCE)
    ):
        raise RuntimeError("candidate-6 continuation inputs are not stable")
    basis = continuation.source_basis()
    record = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_6_CONTINUATION_READINESS",
        "status": "READY_REQUIRES_NEW_SINGLE_USE_PRINCIPAL_AUTHORIZATION",
        "recordedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "packageIdentity": package["packageIdentity"],
        "packageIdentityRecordSha256": file_sha256(continuation.PACKAGE),
        "packageAcceptanceSha256": file_sha256(continuation.ACCEPTANCE),
        "sourceBasis": basis,
        "interactionAdjudication": {
            "offlineReceipt": str(continuation.OFFLINE_RECEIPT.relative_to(ROOT)),
            "offlineReceiptSha256": file_sha256(continuation.OFFLINE_RECEIPT),
            "embeddedReceiptSha256": offline["receiptSha256"],
            "verdictSource": str(continuation.VERDICT_SOURCE.relative_to(ROOT)),
            "verdictSourceSha256": file_sha256(continuation.VERDICT_SOURCE),
            "checks": offline["summary"],
        },
        "continuationDriver": {
            "path": str(Path(continuation.__file__).resolve().relative_to(ROOT)),
            "sha256": file_sha256(Path(continuation.__file__).resolve()),
            "mode": "ZERO_CREDIT_DRESS",
            "cases": list(continuation.TAIL_CASES),
            "scenarios": 5,
            "repetitions": 5,
            "automaticReruns": 0,
        },
        "compositeTarget": {
            "preservedCompletedScenarios": 13,
            "newScenarios": 5,
            "totalScenarios": 18,
            "restartFromScenario1": False,
        },
        "authority": {
            "liveContinuation": False,
            "measuredExecution": False,
            "newSingleUseGrantRequired": True,
        },
        "liveActivity": "NOT_RUN",
        "serviceStartOrRestart": "NOT_RUN",
        "modelCalls": "NOT_RUN",
    }
    record["readinessIdentity"] = digest(record)
    OUTPUT.mkdir(parents=True, exist_ok=False)
    path = OUTPUT / "continuation-readiness.json"
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical_bytes(record) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())
    print(json.dumps({
        "status": record["status"],
        "readinessIdentity": record["readinessIdentity"],
        "receiptFileSha256": file_sha256(path),
        "sourceScenariosPreserved": 13,
        "continuationScenarios": 5,
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
