# SMA-S2 candidate 5 remediation readiness

Date: 2026-08-14

## Recommendation

**GO for candidate-specific principal review and explicit acceptance/freeze of
candidate 5.**

This recommendation does not accept the candidate, complete the independent
review, prepare an execution identity, authorize a live preflight, or authorize
measured execution. The coordinator authored the successor harness and cannot
perform the principal's independent act.

## Sealed subject

- Candidate path:
  `investigations/sma-q1/layered/sma-s2-preregistration-candidate-5.json`
- Candidate SHA-256:
  `2afe7930e20a95dd40d7477c4a7d4ee638477d0ea0a339b3742ff2b39446f939`
- Accepted semantic source SHA-256:
  `b7315835b6291d44d3d8cb918dbec1f16c1e64f2106fd70212c6a8f7ec678188`

## Verified floor

- **COMPUTED:** All 21 candidate path/hash bindings match their current files.
- **OBSERVED:** Read-only static qualification under Python optimization
  reproduces 20 cases, 103 repetitions, 59 predicate slots, 10 evidence slots,
  zero retries, the one-second timeout, the frozen fault schedules, matching
  candidate-5 namespaces, and default-deny execution.
- **OBSERVED:** Final offline semantic qualification is 11/11 PASS with 25
  evidence-field mutations and no network use.
- **OBSERVED:** Every single false and every single missing Boolean in the 201
  per-case oracle bundles is rejected.
- **OBSERVED:** No Python `assert` node is used for a scientific predicate.
- **OBSERVED:** The execution fence rejects a mutated two-attempt authorization.

## Candidate-4 blockers closed

1. Non-empty context without an SMA trace is classified `MISSING`; only an
   exactly empty context may use the explicit fail-open class.
2. Case 7 scans the new operational-journal, service-log, retrieval-event, and
   hook-stdout deltas; no body-free literal remains.
3. Case 11 waits for the exact three eligible parent/child messages and compares
   actual OpenHands sequence fields with stored SMA provenance sequences.
4. Case 18 requires the deterministic generic-summary marker to occur in the
   summary event while remaining absent from context, retrieval delivery state,
   and captured-event provenance; selected IDs must be known SMA memories.
5. Case 20 separately timestamps OpenHands future observation and stub
   disconnect, measures owned shutdown from interruption, and inventories
   repetition-owned workspace and conversation namespace cleanup.
6. The stale candidate-1 reviewer designation is not imported. Candidate 5
   explicitly requires a principal decision naming its exact path and hash.

## Root-cause control

Candidate 4 proved that oracle labels existed and that false labels were
rejected. Candidate 5 additionally corrupts the evidence underneath the labels:

- 3 provenance mutations;
- 6 operational-body surface mutations;
- 4 parent-child sequence/cardinality mutations;
- 5 condensation-summary mutations; and
- 7 cancellation/future/cleanup mutations.

All 25 corruptions turn the relevant semantic helper false. This is the
specific control intended to prevent another syntactically complete but
semantically weaker harness revision.

## Retained nonqualifying work

- Qualification 1: FAIL because dynamic reflection could not inspect the class;
  zero scientific credit.
- Qualification 2: PASS for its then-current inputs, but zero successor credit
  because the driver was subsequently strengthened.
- Qualification 3: final bound 11/11 PASS.

No failed or superseded receipt was overwritten or regraded.

## What remains

1. Principal independently reviews and, if satisfied, explicitly accepts/freezes
   candidate 5 by exact SHA-256.
2. Coordinator prepares a new sealed execution identity and performs the
   separately authorized live preflight.
3. If preflight passes, the principal separately authorizes one single-use
   103-repetition measured execution.

No SMA or OpenHands product remediation is presently indicated. No live
preflight, OpenHands conversation, SMA service call, stub network call, measured
execution, or real-model call occurred during candidate-5 preparation.

