#!/usr/bin/env python3
"""Read-only package validator for SMA-S1 final-P2 R1 candidate 2."""

from __future__ import annotations

import hashlib
import json
import subprocess
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
SELF = Path(__file__).resolve()
IDENTITY = ROOT / (
    "investigations/sma-q1/layered/"
    "sma-s1-execution-identity-p2final-r1-candidate-2.json"
)
QUALIFICATION = ROOT / (
    "OUTPUT/phase-3/sma-s1-p2final-r1-candidate-2-offline-qualification.json"
)
SMA = Path("/Users/paul/work/tekroo-ai/sma-step15-s1-p2final")


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"object required: {path}")
    return value


def git_value(*args: str) -> str:
    result = subprocess.run(
        ["git", *args],
        cwd=SMA,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
        timeout=10,
    )
    if result.returncode != 0:
        raise RuntimeError("git identity query failed")
    return result.stdout.decode("utf-8").strip()


def main() -> int:
    identity = load(IDENTITY)
    qualification = load(QUALIFICATION)
    checks: list[tuple[str, bool]] = []

    def check(name: str, condition: bool) -> None:
        checks.append((name, bool(condition)))

    check(
        "package_validator_sha256",
        sha256_file(SELF) == qualification["packageValidator"]["sha256"],
    )
    check(
        "identity_sha256",
        sha256_file(IDENTITY)
        == qualification["executionIdentityCandidate"]["sha256"],
    )
    manifest = ROOT / identity["scientificPreregistration"]["path"]
    prereg = load(manifest)
    check(
        "accepted_preregistration_sha256",
        sha256_file(manifest) == identity["scientificPreregistration"]["sha256"],
    )
    check("accepted_preregistration_cases", len(prereg["cases"]) == 14)
    check(
        "accepted_preregistration_repetitions",
        sum(item["repetitions"] for item in prereg["cases"]) == 71,
    )

    bound = identity["boundImplementation"]
    check("sma_commit", git_value("rev-parse", "HEAD^{commit}") == bound["smaCommit"])
    check("sma_tree", git_value("rev-parse", "HEAD^{tree}") == bound["smaTree"])
    check("sma_worktree_clean", git_value("status", "--porcelain=v1") == "")
    check(
        "service_artifact",
        sha256_file(Path(bound["serviceArtifactPath"]))
        == bound["serviceArtifactSHA256"],
    )
    check(
        "sources_artifact",
        sha256_file(Path(bound["sourcesArtifactPath"]))
        == bound["sourcesArtifactSHA256"],
    )

    for key, relative in (
        ("serviceMain", "src/main/java/ai/tekroo/sma/runtime/SmaServiceMain.java"),
        ("bridge", "src/main/java/ai/tekroo/sma/openhands/OpenHandsBridgeServer.java"),
        ("retrieval", "src/main/java/ai/tekroo/sma/retrieval/SmaRetrievalService.java"),
        ("replay", "src/main/java/ai/tekroo/sma/replay/ReplayWorker.java"),
        ("delivery", "src/main/java/ai/tekroo/sma/delivery/DeliveryManifestService.java"),
    ):
        check(
            f"source_blob_{key}",
            git_value("rev-parse", f"HEAD:{relative}") == bound["sourceBlobs"][key],
        )

    for name, path_key, hash_key in (
        ("driver_source", "productDriverSource", "productDriverSourceSHA256"),
        ("driver_artifact", "productDriverArtifact", "productDriverArtifactSHA256"),
        ("driver_launcher", "productDriverLauncher", "productDriverLauncherSHA256"),
        ("build_receipt", "productDriverBuildReceipt", "productDriverBuildReceiptSHA256"),
        ("base_harness", "acceptedBaseHarness", "acceptedBaseHarnessSHA256"),
        ("handler_check", "handlerCheck", "handlerCheckSHA256"),
        ("p2_delta", "p2DeltaControls", "p2DeltaControlsSHA256"),
        ("driver_protocol", "driverProtocol", "driverProtocolSHA256"),
        ("environment", "environmentBinding", "environmentBindingSHA256"),
    ):
        check(name, sha256_file(ROOT / bound[path_key]) == bound[hash_key])

    for section, receipt_name, journal_name in (
        ("baseHarnessQualification", "receiptSHA256", "journalSHA256"),
        ("p2DeltaQualification", "receiptSHA256", "journalSHA256"),
        ("handlerQualification", "receiptSHA256", "journalSHA256"),
    ):
        value = qualification[section]
        receipt_path = ROOT / value["receipt"]
        journal_path = receipt_path.parent / (
            "self-test-journal.jsonl"
            if section == "baseHarnessQualification"
            else "p2-delta-control-journal.jsonl"
            if section == "p2DeltaQualification"
            else "handler-check-journal.jsonl"
        )
        check(f"{section}_receipt_hash", sha256_file(receipt_path) == value[receipt_name])
        check(f"{section}_journal_hash", sha256_file(journal_path) == value[journal_name])
        check(f"{section}_status", load(receipt_path)["status"] == "PASS")

    predecessor = qualification["retainedNegativeControl"]
    predecessor_identity = ROOT / (
        "investigations/sma-q1/layered/"
        "sma-s1-execution-identity-p2final-r1-candidate-1.json"
    )
    predecessor_receipt = ROOT / (
        "OUTPUT/phase-3/sma-s1-p2final-r1-candidate-1-handler-check/"
        "handler-check-receipt.json"
    )
    predecessor_journal = predecessor_receipt.parent / "handler-check-journal.jsonl"
    check(
        "predecessor_identity_hash",
        sha256_file(predecessor_identity) == predecessor["identitySHA256"],
    )
    check(
        "predecessor_receipt_hash",
        sha256_file(predecessor_receipt) == predecessor["handlerReceiptSHA256"],
    )
    check(
        "predecessor_journal_hash",
        sha256_file(predecessor_journal) == predecessor["handlerJournalSHA256"],
    )
    predecessor_value = load(predecessor_receipt)
    check("predecessor_zero_handlers", predecessor_value["handlers"] == [])
    check("predecessor_zero_credit", predecessor_value["scientificRepetitionsExecuted"] == 0)

    execution = identity["executionFence"]
    check("identity_not_accepted", execution["identityAccepted"] is False)
    check("execution_not_authorized", execution["executionAuthorized"] is False)
    check("execution_not_started", execution["executionStarted"] is False)
    check("zero_measured_repetitions", execution["scientificRepetitionsExecuted"] == 0)
    check(
        "qualification_not_authorizing",
        qualification["authorityFence"]["measuredExecutionAuthorized"] is False,
    )
    check(
        "independent_review_not_claimed",
        qualification["review"]["independentReview"] == "NOT_RUN",
    )

    failures = [name for name, passed in checks if not passed]
    if failures:
        print(
            f"SMA-S1 final-P2 R1 candidate-2 package FAIL: "
            f"{len(checks) - len(failures)}/{len(checks)}; failures={','.join(failures)}"
        )
        return 1
    print(
        f"SMA-S1 final-P2 R1 candidate-2 package PASS: "
        f"{len(checks)}/{len(checks)}; independentReview=NOT_RUN"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
