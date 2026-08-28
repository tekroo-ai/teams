#!/usr/bin/env python3
"""Offline P2 compatibility controls for the final-P2 S1 successor."""

from __future__ import annotations

import argparse
import copy
import json
import subprocess
import time
from pathlib import Path
from typing import Any

from sma_s1_harness_evidence_v2_candidate_4 import (
    DurableJournal,
    canonical_bytes,
    create_once_directory,
    read_journal,
    sha256_bytes,
    sha256_file,
    utc_now,
    write_new_file,
)


SOURCE_PATHS = {
    "serviceMain": "src/main/java/ai/tekroo/sma/runtime/SmaServiceMain.java",
    "bridge": "src/main/java/ai/tekroo/sma/openhands/OpenHandsBridgeServer.java",
    "retrieval": "src/main/java/ai/tekroo/sma/retrieval/SmaRetrievalService.java",
    "replay": "src/main/java/ai/tekroo/sma/replay/ReplayWorker.java",
    "delivery": "src/main/java/ai/tekroo/sma/delivery/DeliveryManifestService.java",
}


def run(command: list[str], cwd: Path, timeout: float = 180.0) -> dict[str, Any]:
    started = time.monotonic_ns()
    try:
        completed = subprocess.run(
            command,
            cwd=cwd,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
            timeout=timeout,
        )
        return {
            "returnCode": completed.returncode,
            "timedOut": False,
            "elapsedNanoseconds": time.monotonic_ns() - started,
            "stdoutSHA256": sha256_bytes(completed.stdout),
            "stdoutLength": len(completed.stdout),
            "stderrSHA256": sha256_bytes(completed.stderr),
            "stderrLength": len(completed.stderr),
            "stdout": completed.stdout,
        }
    except subprocess.TimeoutExpired as exc:
        stdout = exc.stdout or b""
        stderr = exc.stderr or b""
        return {
            "returnCode": None,
            "timedOut": True,
            "elapsedNanoseconds": time.monotonic_ns() - started,
            "stdoutSHA256": sha256_bytes(stdout),
            "stdoutLength": len(stdout),
            "stderrSHA256": sha256_bytes(stderr),
            "stderrLength": len(stderr),
            "stdout": stdout,
        }


def exact_configuration_valid(value: object) -> bool:
    return value == {
        "p2AFEnabled": False,
        "deletionFenceMode": "PERMIT_ALL_COMPATIBILITY",
        "defaultEntrypoint": "ai.tekroo.sma.runtime.SmaServiceMain",
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-root", required=True)
    parser.add_argument("--identity", required=True)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--sma-worktree", required=True)
    args = parser.parse_args()

    output_root = Path(args.output_root).resolve()
    identity_path = Path(args.identity).resolve()
    manifest_path = Path(args.manifest).resolve()
    sma = Path(args.sma_worktree).resolve()
    create_once_directory(output_root)
    journal_path = output_root / "p2-delta-control-journal.jsonl"
    identity = json.loads(identity_path.read_text(encoding="utf-8"))
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    results: list[dict[str, Any]] = []

    def record(
        journal: DurableJournal,
        control_id: str,
        observed: dict[str, Any],
        passed: bool,
    ) -> None:
        evidence_id = f"p2-delta:{control_id}"
        journal.append(
            {
                "recordType": "SMA_S1_R1_P2_DELTA_PREASSERTION",
                "controlId": control_id,
                "evidenceId": evidence_id,
                "observed": observed,
            }
        )
        result = {
            "controlId": control_id,
            "status": "PASS" if passed else "FAIL",
            "evidenceId": evidence_id,
        }
        journal.append({"recordType": "SMA_S1_R1_P2_DELTA_RESULT", **result})
        results.append(result)

    with DurableJournal(journal_path) as journal:
        head = run(["git", "rev-parse", "HEAD^{commit}"], sma)
        tree = run(["git", "rev-parse", "HEAD^{tree}"], sma)
        status = run(["git", "status", "--porcelain=v1"], sma)
        actual_head = head["stdout"].decode("utf-8").strip()
        actual_tree = tree["stdout"].decode("utf-8").strip()
        bound = identity["boundImplementation"]
        clean = status["returnCode"] == 0 and status["stdout"] == b""
        record(
            journal,
            "IMMUTABLE_FINAL_P2_IDENTITY",
            {
                "head": actual_head,
                "tree": actual_tree,
                "worktreeClean": clean,
                "statusStdoutSHA256": status["stdoutSHA256"],
            },
            head["returnCode"] == 0
            and tree["returnCode"] == 0
            and actual_head == bound["smaCommit"]
            and actual_tree == bound["smaTree"]
            and clean,
        )

        service_jar = Path(bound["serviceArtifactPath"])
        sources_jar = Path(bound["sourcesArtifactPath"])
        artifact_observed = {
            "servicePresent": service_jar.is_file(),
            "sourcesPresent": sources_jar.is_file(),
            "serviceSHA256": sha256_file(service_jar) if service_jar.is_file() else None,
            "sourcesSHA256": sha256_file(sources_jar) if sources_jar.is_file() else None,
        }
        record(
            journal,
            "FINAL_P2_BUILD_ARTIFACTS",
            artifact_observed,
            artifact_observed["serviceSHA256"] == bound["serviceArtifactSHA256"]
            and artifact_observed["sourcesSHA256"] == bound["sourcesArtifactSHA256"],
        )

        source_text = {
            key: (sma / relative).read_text(encoding="utf-8")
            for key, relative in SOURCE_PATHS.items()
        }
        blob_results: dict[str, dict[str, Any]] = {}
        blobs_pass = True
        for key, relative in SOURCE_PATHS.items():
            blob = run(["git", "rev-parse", f"HEAD:{relative}"], sma)
            actual_blob = blob["stdout"].decode("utf-8").strip()
            expected_blob = bound["sourceBlobs"][key]
            blob_results[key] = {
                "path": relative,
                "expected": expected_blob,
                "actual": actual_blob,
            }
            blobs_pass = blobs_pass and blob["returnCode"] == 0 and actual_blob == expected_blob
        record(journal, "FINAL_P2_SOURCE_BLOBS", blob_results, blobs_pass)

        default_entrypoint_pass = (
            "ProtectedRawDeletionRuntime" not in source_text["serviceMain"]
            and "createEnabled(" not in source_text["serviceMain"]
        )
        record(
            journal,
            "P2_AF_DISABLED_DEFAULT_ENTRYPOINT",
            {
                "protectedRawDeletionRuntimeReferenceCount": source_text["serviceMain"].count(
                    "ProtectedRawDeletionRuntime"
                ),
                "createEnabledReferenceCount": source_text["serviceMain"].count(
                    "createEnabled("
                ),
            },
            default_entrypoint_pass,
        )

        compatibility_counts = {
            "retrievalPermitAll": source_text["retrieval"].count(
                "ProtectedRawDeletionFence.permitAll()"
            ),
            "replayPermitAll": source_text["replay"].count(
                "ProtectedRawDeletionFence.permitAll()"
            ),
            "deliveryPermitAll": source_text["delivery"].count(
                "ProtectedRawDeletionFence.permitAll()"
            ),
            "retrievalIdentityCheck": source_text["retrieval"].count(
                "usesDeletionFence("
            ),
            "replayIdentityCheck": source_text["replay"].count("usesDeletionFence("),
            "deliveryIdentityCheck": source_text["delivery"].count(
                "usesDeletionFence("
            ),
        }
        compatibility_pass = (
            compatibility_counts["retrievalPermitAll"] >= 1
            and compatibility_counts["replayPermitAll"] >= 1
            and compatibility_counts["deliveryPermitAll"] >= 1
            and compatibility_counts["retrievalIdentityCheck"] >= 1
            and compatibility_counts["replayIdentityCheck"] >= 1
            and compatibility_counts["deliveryIdentityCheck"] >= 1
        )
        record(
            journal,
            "PERMIT_ALL_COMPATIBILITY_COMPOSITION",
            compatibility_counts,
            compatibility_pass,
        )

        disclosure_observed = {
            "bridgePrepareSemantic": "retrievalService.prepareSemantic(" in source_text["bridge"],
            "bridgeDisclosureFilter": "retrievalService.filterForDisclosure(prepared)"
            in source_text["bridge"],
            "retrievalDisclosureBoundary": "ProtectedRawDeletionFence.Boundary.DISCLOSURE"
            in source_text["retrieval"],
            "preparedAuditPreserved": "prepared.auditEventId()" in source_text["retrieval"],
            "preparedAgentPreserved": "prepared.agentId()" in source_text["retrieval"],
            "preparedTaskPreserved": "prepared.taskRef()" in source_text["retrieval"],
        }
        record(
            journal,
            "BRIDGE_PREPARE_AND_DISCLOSURE_RECHECK",
            disclosure_observed,
            all(disclosure_observed.values()),
        )

        selected = identity["selectedConfiguration"]
        mutations: list[dict[str, Any]] = []
        for field, value in (
            ("p2AFEnabled", True),
            ("deletionFenceMode", "JOURNAL_FENCE_ENABLED"),
            ("defaultEntrypoint", "mutated.Main"),
        ):
            mutation = copy.deepcopy(selected)
            mutation[field] = value
            mutations.append(
                {
                    "field": field,
                    "mutationRejected": not exact_configuration_valid(mutation),
                }
            )
        configuration_pass = exact_configuration_valid(selected) and all(
            item["mutationRejected"] for item in mutations
        )
        record(
            journal,
            "MATERIAL_CONFIGURATION_IDENTITY_FENCE",
            {
                "selectedConfiguration": selected,
                "selectedConfigurationValid": exact_configuration_valid(selected),
                "mutations": mutations,
            },
            configuration_pass,
        )

        p2_receipt_path = sma / "docs/SMA_S1_P2_AF_RUNTIME_COMPOSITION_RECEIPT.json"
        p2_receipt = json.loads(p2_receipt_path.read_text(encoding="utf-8"))
        p2_receipt_pass = (
            p2_receipt["composition"]["default_enabled"] is False
            and p2_receipt["composition"]["permit_all_used_by_enabled_composition"] is False
            and "ProtectedRawDeletionRuntime.createEnabled starts no service and is absent from the default SMA entrypoint."
            in p2_receipt["evidence_classification"]["observed"]
        )
        record(
            journal,
            "P2_AF_ACCEPTED_RECEIPT_ALIGNMENT",
            {
                "receiptSHA256": sha256_file(p2_receipt_path),
                "defaultEnabled": p2_receipt["composition"]["default_enabled"],
                "permitAllUsedByEnabledComposition": p2_receipt["composition"][
                    "permit_all_used_by_enabled_composition"
                ],
            },
            p2_receipt_pass,
        )

        focused = run(
            [
                "mvn",
                "-q",
                "-Dtest=SmaRetrievalServiceDeletionFenceTest,ReplayWorkerDeletionFenceTest,DeliveryManifestDeletionFenceTest,OpenHandsBridgeDisclosureLifecycleTest,ProtectedRawDeletionRuntimeTest",
                "test",
            ],
            sma,
            240.0,
        )
        focused_observed = {
            key: value for key, value in focused.items() if key != "stdout"
        }
        record(
            journal,
            "FOCUSED_FINAL_P2_DELTA_TESTS",
            focused_observed,
            focused["returnCode"] == 0 and focused["timedOut"] is False,
        )

        prereg_pass = (
            sha256_file(manifest_path)
            == identity["scientificPreregistration"]["sha256"]
            and len(manifest["cases"]) == 14
            and sum(item["repetitions"] for item in manifest["cases"]) == 71
        )
        record(
            journal,
            "UNCHANGED_ACCEPTED_S1_SCIENCE",
            {
                "manifestSHA256": sha256_file(manifest_path),
                "caseCount": len(manifest["cases"]),
                "repetitionCount": sum(item["repetitions"] for item in manifest["cases"]),
            },
            prereg_pass,
        )

        overall = "PASS" if all(item["status"] == "PASS" for item in results) else "FAIL"
        journal.append(
            {
                "recordType": "SMA_S1_R1_P2_DELTA_SUMMARY",
                "status": overall,
                "controlCount": len(results),
                "passCount": sum(item["status"] == "PASS" for item in results),
                "failCount": sum(item["status"] == "FAIL" for item in results),
                "measuredExecution": False,
                "openhandsOrModelUsed": False,
            }
        )

    receipt = {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_S1_FINAL_P2_R1_DELTA_CONTROL_RECEIPT",
        "status": overall,
        "scope": "OFFLINE_P2_COMPATIBILITY_ZERO_SCIENTIFIC_CREDIT",
        "recordedAt": utc_now(),
        "identitySHA256": sha256_file(identity_path),
        "manifestSHA256": sha256_file(manifest_path),
        "journalSHA256": sha256_file(journal_path),
        "journalRecordCount": len(read_journal(journal_path)),
        "controls": results,
        "measuredExecution": False,
        "scientificRepetitionsExecuted": 0,
        "openhandsOrModelUsed": False,
        "productionOrHistoricalDataUsed": False,
        "smaProductSourceChanged": False,
    }
    write_new_file(
        output_root / "p2-delta-control-receipt.json",
        canonical_bytes(receipt) + b"\n",
    )
    print(
        f"SMA-S1 final-P2 R1 delta controls {overall}: "
        f"{sum(item['status'] == 'PASS' for item in results)}/{len(results)}"
    )
    return 0 if overall == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
