# SMA-S2 candidate 4 coordinator review

Date: 2026-08-14

## Decision

**NO-GO for principal acceptance/freeze of candidate 4.**

This is a coordinator verification and principal decision packet, not the
preregistered independent-reviewer act. The coordinator authored the
qualification harness and therefore cannot complete the independent review or
accept/freeze the candidate. No live preflight, OpenHands conversation, SMA
service invocation, deterministic-stub network invocation, measured execution,
or real-model call was performed.

## Verified floor

- **OBSERVED:** Candidate 4 SHA-256 is
  `5875ec59ec1cb0e8d8d20f18bc0429e1324a160eed827222dcfb4c0cabc6d0ae`.
- **COMPUTED:** All 19 path/hash bindings in candidate 4 currently match their
  files.
- **OBSERVED:** Re-running the driver's read-only offline-contract mode under
  Python optimization reproduced 20 cases, 103 repetitions, 59 predicate
  slots, 10 required-evidence slots, zero retries, a one-second model timeout,
  the frozen case-19 and case-20 schedules, matching candidate-4 namespaces,
  and default-deny execution.
- **OBSERVED:** The retained offline receipt is a 10/10 PASS scoped to
  `OFFLINE_MATRIX_DRIVER_AND_FIXTURE_CONTROLS_ONLY`; it explicitly excludes a
  live preflight, measured execution, OpenHands conversation, SMA service,
  stub network invocation, real model call, and the
  `OPENHANDS_SMA_BOUNDARY_QUALIFIED` claim.
- **OBSERVED:** Candidate 4 repairs material candidate-3 defects: it binds one
  effective namespace configuration, maps all 59 predicate slots and all 10
  evidence slots, rejects missing or false oracle bundles, preserves predicates
  under Python optimization, adds deterministic tool stimuli for cases 11 and
  14, and strengthens cases 2, 3, 5, 9, 10, 12, 13, and 17.

## Gate-blocking findings

### 1. The independent-reviewer designation names candidate 1, not candidate 4

- **OBSERVED:** The designation SHA-256 remains
  `1afa7c23963c69b9a2eff4d79cb4694d1f819fef94164d87a50d13ad946ac8ac`,
  but its `reviewSubject.path` and hash identify preregistration candidate 1.
- **OBSERVED:** Candidate 4's next gate is independent review and explicit
  principal accept/freeze of candidate 4.
- **Impact:** The old designation cannot itself be used as a candidate-4 review
  receipt. A successor principal statement must name candidate 4 and its exact
  hash after the substantive blockers below are corrected.

### 2. The provenance evidence oracle admits a false positive

- **OBSERVED:** `one_prompt` classifies every missing trace as
  `EXPLICIT_FAIL_OPEN_EMPTY`, without requiring the context to be empty.
- **OBSERVED:** `E-003` accepts that class for any context whose digest, length,
  and framing match.
- **OBSERVED:** The frozen evidence clause requires selected memory identities
  **and provenance** for the exact `additionalContext`.
- **Impact:** A non-empty recalled context with no SMA trace/provenance can pass
  `E-003`. Candidate 4 can therefore claim complete delivery evidence when the
  provenance is absent.

### 3. Case 7's body-free observability predicate is asserted, not measured

- **OBSERVED:** The case-7 handler assigns `bodyFree: True` and then grades that
  literal as part of `S2-007-P3`; it performs no operational-journal, service-log,
  or retrieval-event body scan for this predicate.
- **Impact:** An outage could disclose a prompt or memory body and still pass
  case 7.

### 4. Case 11 has an inconsistent capture cardinality and incomplete sequence proof

- **OBSERVED:** The driver creates a parent user prompt that ends in a tool
  action, then a child user prompt and final agent response, and only afterward
  enables capture. It waits for `before + 2` memories.
- **OBSERVED:** The bound SMA intake accepts authoritative conversation-input
  and final-agent-response `MessageEvent`s, including delegated task inputs.
- **INFERRED:** The producible eligible population is three events—parent input,
  child input, and child final response—so the exact-count waiter is expected to
  fail when the third memory arrives.
- **OBSERVED:** The case records an ordinal map derived from list position and
  requires non-null SMA sequence values, but it does not retain and compare the
  actual OpenHands event sequence values to their SMA provenance values.
- **Impact:** Case 11 is not acceptance-ready: its cardinality can stop the
  harness even when the product behaves correctly, while its sequence oracle is
  weaker than the frozen cross-boundary retention predicate.

### 5. Case 18 compares identifiers from different semantic domains

- **OBSERVED:** `S2-018-P2` compares OpenHands condensation-summary event IDs
  with SMA memory IDs parsed from recalled context.
- **OBSERVED:** The frozen predicate says generic summary **text** must not be
  treated as exact delivery state.
- **Impact:** Non-intersection of event IDs and memory IDs does not prove that
  summary text was excluded from retrieval or delivery state. Generic summary
  text can be treated as state while the oracle still passes.

### 6. Case 20 does not measure the frozen future or per-repetition cleanup claims

- **OBSERVED:** The timestamp used for the 10-second `S2-020-P2` bound is the
  deterministic stub terminal's `completedMonotonicNs`, not the time at which
  the OpenHands conversation future is observed terminal.
- **OBSERVED:** The same stub timestamp is used for the disconnect bound.
- **OBSERVED:** `S2-020-P5` checks only zero bridge active/queued work and
  successful conversation deletion. Workspace and disposable namespace absence
  are checked only by the global cleanup after all 103 repetitions.
- **OBSERVED:** The frozen case requires the conversation future within 10
  seconds and no orphaned process, future, conversation work, workspace, or
  disposable namespace per repetition.
- **Impact:** A late OpenHands future, surviving owned process, or surviving
  workspace/namespace can pass the per-repetition case.

## Why the offline PASS did not catch these findings

- **OBSERVED:** The offline harness proves that all oracle IDs are present in
  handler source, and that an already-constructed Boolean bundle is rejected
  when one Boolean is missing or false.
- **OBSERVED:** It does not mutate the underlying evidence fields or prove that
  each Boolean implements the semantic predicate attached to its label.
- **COMPUTED:** The matrix is structurally exhaustive but not semantically
  exhaustive.
- **INFERRED:** This is the current cycle's root cause. Successor work should be
  generated from evidence-field mutation tests, not another pass that merely
  adds oracle labels.

## Required successor scope

Candidate 4 remains immutable and receives zero measured credit. A bounded
candidate 5 should change only the harness/preparation artifacts:

1. Require `TRACE` for every non-empty context; allow explicit fail-open only
   when the context is exactly empty.
2. Replace case 7's literal `bodyFree` value with scans of every frozen
   operational surface.
3. Make case 11 capture cardinality follow the exact eligible event set, retain
   actual OpenHands sequence values, and compare them with SMA provenance.
4. Test case 18 with a unique generic-summary marker and prove that marker is
   absent from selected memories, hook context, model-request context, and
   retrieval delivery records.
5. Record a separate OpenHands-future terminal-observation timestamp in case 20;
   separately measure the stub disconnect; verify all owned work and each
   per-repetition disposable resource required by the predicate.
6. Add evidence-field mutation tests for each correction so the corresponding
   oracle is proved to turn false when the semantic evidence is corrupted.
7. Publish a candidate-5-specific principal review designation/decision naming
   its exact path and SHA-256 before acceptance/freeze.

## Recommendation

Do **not** accept/freeze candidate 4, prepare an execution identity, authorize a
live preflight, or authorize measured S2 execution. Authorize one bounded
candidate-5 harness remediation against the unchanged candidate-2 semantics,
then return directly to a candidate-specific principal decision packet. No SMA
product remediation is indicated by these findings.

