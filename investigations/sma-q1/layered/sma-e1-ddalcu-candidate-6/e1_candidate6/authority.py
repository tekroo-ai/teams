from __future__ import annotations

from typing import Any


def sealed_execution_authorization(
    *,
    principal_grant_sha256: str,
    principal_statement: str,
    package_identity: str,
    execution_identity: str,
    mode: str,
    maximum_scenarios: int,
    maximum_repetitions: int,
) -> dict[str, Any]:
    if mode not in {"ZERO_CREDIT_DRESS", "MEASURED"}:
        raise ValueError("unsupported live execution mode")
    if maximum_scenarios != 18 or maximum_repetitions not in {18, 96}:
        raise ValueError("execution cardinality is outside the accepted E1 plan")
    if not principal_statement.strip():
        raise ValueError("principal statement is required")
    return {
        "schemaVersion": "1.0.0",
        "recordType": "SMA_E1_DDALCU_CANDIDATE_6_SEALED_EXECUTION_AUTHORIZATION",
        "status": "SEALED_FROM_PRINCIPAL_GRANT",
        "packageIdentity": package_identity,
        "executionIdentity": execution_identity,
        "mode": mode,
        "singleUse": True,
        "maximumScenarios": maximum_scenarios,
        "maximumRepetitions": maximum_repetitions,
        "automaticReruns": 0,
        "principalGrantSha256": principal_grant_sha256,
        "principalStatement": principal_statement,
    }
