# SMA-S2 candidate 8 dress rehearsal: INCONCLUSIVE

Date: 2026-08-14  
Status: `INCONCLUSIVE`

## Outcome

The host-boundary preflight passed and the single authorized 34-operation,
zero-credit dress rehearsal ran once. It completed 10 operations, then stopped
on a harness boundary failure while beginning case 2. Cleanup passed. No rerun
occurred.

**OBSERVED:** All 10 repetitions of
`SMA-S2-001-FIRST-PROMPT-EMPTY` passed. Every result had zero injected-context
length. Each raw isolation receipt showed `actor-empty` excluded from the
two-workspace `actor-alpha`/`actor-beta` capture allowlist.

**OBSERVED:** Corpus construction then created four source events and captured
them. The service stopped for promotion. Before any `CORPUS_PROMOTION` receipt
was recorded, the first promotion raised `CalledProcessError`. Case 2 did not
complete.

**OBSERVED:** The driver was launched with working directory
`/Users/paul/work/tekroo-ai/teams`, which has no `pom.xml`. The bound SMA Maven
project `/Users/paul/work/tekroo-ai/sma-s1-p2m` has a `pom.xml`.
`promote_memory` invokes `mvn ... test` through a helper that does not pass an
explicit working directory, so Maven inherits the driver process directory.

**INFERRED (high confidence):** The Maven promotion failed because it ran from
the Teams directory rather than the bound SMA Maven project. The inference is
not upgraded to observation because the harness captured Maven stderr inside
`CalledProcessError` but failed to retain it in the raw journal.

## Scientific interpretation

**COMPUTED:** Case-1 pass rate was 10/10. Overall dress completion was 10/34.
Measured case executions, measured repetitions, measured credit, automatic
reruns, and real-model calls were all zero.

**INFERRED:** Candidate 8 closed the candidate-7 workspace-contamination
failure at the live dress level. Candidate 8 did not earn a dress PASS because
the remaining 24 operations were not completed.

This is a new harness execution-environment defect, not evidence against the
workspace-isolation design and not a scientific FAIL.

## Cleanup

**OBSERVED:** The receipt's cleanup oracles all passed. An independent post-run
check confirmed the candidate service, bridge, stub, MongoDB database, Qdrant
collections, workspace root, and empty workspace absent. The OpenHands audit
returned zero active conversations; Tekroo Trader remains paused.

## Evidence and next gate

- Dress receipt SHA-256:
  `f91a7f8c7c02cbb70be302dba78bdc1f8ca6ff2ed6b8ec1cb6df882d08a29cca`
- Raw journal SHA-256:
  `d97410588579c77c6bd43c1dfc09176a1ac4c843199d985315aa5889cb819e0f`
- Host preflight SHA-256:
  `532efbe846553973e9d35f399e75d60d3a89e99382c0b10d6e7e525a2cc29a33`

The authorization is consumed and cannot be reused. Measured execution remains
closed.

Recommended next gate: authorize one bounded candidate-9
harness-working-directory remediation. It should bind Maven promotion to the
exact SMA project directory, retain subprocess exit status and sanitized
stdout/stderr digests on failure, add missing/mutated/non-project working-
directory controls, and perform offline qualification only. No live rerun is
authorized by that remediation.
