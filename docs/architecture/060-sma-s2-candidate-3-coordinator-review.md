# SMA-S2 candidate 3 coordinator review

Date: 2026-08-14

## Decision

**NO-GO for principal acceptance/freeze of candidate 3.**

This is a read-only coordinator verification, not the preregistered independent-reviewer act. The qualification-harness author cannot satisfy the independent-review role assigned to the workspace principal. No live preflight or measured execution was performed.

## Verified floor

- **OBSERVED:** Candidate 3 SHA-256 is `6271d240faa4ad88cbbe6048dfd35f55456b770a9be862adb2cad9ce059bb622`.
- **OBSERVED:** The frozen corpus contains 20 ordered cases and 103 ordered repetitions.
- **OBSERVED:** The measured driver SHA-256 is `5f69ed4d3b439bf1d4d342c75f05781efee326fc2e1c39ad5002a92ffcc7464a`.
- **OBSERVED:** Running the driver's offline-contract mode under Python optimization reproduced the expected 20-case/103-repetition structure, zero retries, one-second configured model timeout, and exact case-19/case-20 fault schedules.
- **OBSERVED:** The existing offline qualification receipt is a 10/10 PASS explicitly scoped to `OFFLINE_DRIVER_CONTROLS_ONLY`; it explicitly excludes live preflight, measured execution, OpenHands conversation execution, and the `OPENHANDS_SMA_BOUNDARY_QUALIFIED` claim.

## Gate-blocking findings

### 1. Frozen configuration and driver namespaces disagree

- **OBSERVED:** Candidate 3 binds `sma-s2-nonsecret-configuration-candidate-2.json` as its non-secret configuration, but separately declares candidate-3 disposable namespaces.
- **OBSERVED:** The bound configuration names candidate-2 MongoDB, Qdrant, workspace, conversation, and output namespaces, while the frozen driver hard-codes candidate-3 namespaces.
- **COMPUTED:** The execution fence verifies only that the configuration file hash matches the execution identity; it does not verify that the configuration contents correspond to the driver's effective namespaces.
- **Impact:** A future execution identity could pass the fence while binding mutually inconsistent effective configuration. The measured subject is therefore not unambiguously frozen.

### 2. Required per-repetition evidence is not emitted

- **OBSERVED:** Accepted candidate 2 requires selected memory identities and provenance, plus conversation sequence, workspace, and profile identities for every repetition.
- **OBSERVED:** The generic `one_prompt` evidence records prompt/context digests, event IDs, parent IDs, request counts, and terminal state, but does not record selected memory identities/provenance, sequence, workspace, or profile.
- **Impact:** A PASS receipt could be produced without the evidence required to reconstruct those predicates.

### 3. Same-partition delivery does not assert its frozen predicate

- **OBSERVED:** Case 2 requires `mem-alpha-timeout` exactly once and requires untrusted-evidence framing.
- **OBSERVED:** Its handler asserts only substring presence for the memory ID and marker; it neither counts occurrences nor asserts framing.
- **Impact:** Duplicate or unframed delivery could pass case 2.

### 4. Absence predicates do not cover the frozen surfaces

- **OBSERVED:** Cases 3 and 5 require forbidden actor-alpha/raw markers to be absent from hook output, model request, logs, and capture surfaces.
- **OBSERVED:** Their handlers pass `forbidden` markers to `one_prompt`, where the check is applied only to the hook context string.
- **Impact:** Leakage into logs or capture surfaces could pass both cases.

### 5. Timing, fault-observability, and telemetry predicates are incomplete

- **OBSERVED:** Case 7 requires outage observability without bodies; its handler checks only no context.
- **OBSERVED:** Case 10 requires each hook fault to complete within the 1,000 ms hard timeout and exactly one valid hook result; its handler checks success and empty context, but records neither hook duration nor hook-result cardinality.
- **OBSERVED:** Case 13 requires queue, timeout, rejection, and resource telemetry; its handler records only four conversation IDs and validates distinctness after four concurrent prompts.
- **Impact:** These cases can pass while their accepted observability and boundedness predicates remain unproved.

### 6. Provenance, restart, and loop-prevention handlers prove weaker claims

- **OBSERVED:** Case 11 checks only parent/child topology, not the frozen sequence, workspace, profile, OpenHands-boundary, SMA-boundary, and partition-attribution evidence.
- **OBSERVED:** Case 12 checks recall after service restart, but does not assert authenticated metadata partition continuity or duplicate-free reconciliation.
- **OBSERVED:** Case 14 checks only a total memory-count delta; it does not identify and reconcile the required hook-context, reasoning, tool, and utility events or prove that each ineligible event created no memory.
- **Impact:** The handlers do not establish the accepted case predicates.

### 7. Oversize and condensation oracles are incomplete

- **OBSERVED:** Case 17 checks result-count and character ceilings but does not assert truncation only at memory boundaries or validate the complete framing.
- **OBSERVED:** Case 18 submits a second post-condensation prompt but does not run the generic model-request and terminal oracle on that second prompt, and does not test that generic summary text is excluded as delivery state.
- **Impact:** Boundary-splitting, invalid framing, summary-as-state, or a missing second terminal could pass.

### 8. Model-fault and cancellation completion evidence is incomplete

- **OBSERVED:** Case 19 checks request cardinality and a conversation error, with an additional disconnect check only for timeout. It does not assert the exact terminal classification for malformed, HTTP 503, and transport failure, or retain all required independent fault/timing and cleanup evidence.
- **OBSERVED:** Case 20 measures time from interrupt issuance to stub terminal, but does not prove the OpenHands conversation future became terminal, does not measure the stub disconnect interval separately, and does not prove owned hook/bridge/stub/conversation work terminated within 15 seconds per repetition. Cleanup is performed only after the entire corpus.
- **Impact:** A case-19 or case-20 PASS would be weaker than the frozen predicates and thresholds.

## Adjudication

- **COMPUTED:** The offline PASS is valid for executor-control mechanics only.
- **COMPUTED:** Candidate 3 is not acceptance-ready because its frozen driver admits false-positive scientific PASS outcomes relative to accepted candidate 2.
- **INFERRED:** A bounded candidate 4 remediation can preserve the 20 cases, 103 repetitions, thresholds, fault schedules, and claim while correcting only configuration identity, evidence capture, and predicate oracles.

## Required successor scope

1. Create a candidate-3-specific non-secret configuration whose disposable namespaces exactly match the driver and bind it prospectively.
2. Add a machine-checkable predicate-to-oracle matrix covering every accepted predicate and required-evidence item.
3. Strengthen the driver to record and explicitly assert every missing identity, provenance, surface-absence, timing, telemetry, framing, condensation, fault-terminal, future, and per-repetition shutdown predicate.
4. Extend offline qualification to verify oracle presence and negative controls for each corrected predicate; do not claim live behavior from offline tests.
5. Publish candidate 4 with new hashes for independent review. Candidate 3 remains unchanged and receives zero measured credit.

No live preflight or measured execution should be authorized from candidate 3.
