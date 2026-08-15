# SMA-S2 candidate 7 dress-rehearsal adjudication

Date: 2026-08-14  
Status: `INCONCLUSIVE_HARNESS_WORKSPACE_ISOLATION_FAILURE`

## Decision

`NO-GO` for measured SMA-S2 execution.

The one authorized candidate-7 dress-rehearsal attempt is consumed and may not
be rerun. Candidate 7 and its accepted execution identity remain immutable.

## What happened

**OBSERVED:** The immediate live preflight passed. Both services were healthy,
no unrelated conversation was active, Tekroo Trader remained paused, both
runtime ports were available, and every candidate-7 namespace was absent.

**OBSERVED:** All ten fast-terminal repetitions of
`SMA-S2-001-FIRST-PROMPT-EMPTY` passed. Each retained its exact agent terminal
event and passed its scientific predicates.

**OBSERVED:** The run then stopped during corpus seeding for case 2. The receipt
classified the failure as a harness `RuntimeError` with message SHA-256:

`42aceb4995e5adee6ccccb7f4abf223522506f5f21ad06b88d909d0c57241844`

**COMPUTED:** That digest exactly matches the driver's executable exception:

`memory count exceeded expected 4: 10`

The receipt is therefore `INCONCLUSIVE`, not a scientific `FAIL`. It completed
10 of 34 dress operations, awarded zero measured credit, and earned no claim.

## Root cause

**OBSERVED:** The dress plan ran ten empty-partition prompts before case 2.
Those prompts used the same candidate workspace population later included in
the SMA capture allowlist. The source journal then recorded four bound corpus
source conversations and started capture. The exact four-memory seed oracle
immediately observed ten memories and stopped closed.

**INFERRED:** Existing case-1 events contaminated corpus capture before the
four-source seed could establish its exact cardinality. The failed driver did
not retain the ten memory documents themselves, so their exact event identities
are not asserted. The count, plan order, shared workspace, capture boundary,
and exact exception do establish a harness workspace-isolation defect.

## Cleanup

**OBSERVED:** Cleanup passed. All 14 candidate-created conversations were
deleted and returned `404`; the service and stub stopped; MongoDB and both
Qdrant namespaces were absent; the candidate workspace was absent; and both
global cleanup oracles passed.

**OBSERVED:** A post-cleanup audit found the original eight conversations, zero
active conversations, and Tekroo Trader still paused with its original creation
and update timestamps.

## Required candidate-8 correction

A successor should:

1. give case-1 empty-partition repetitions a dedicated workspace partition
   excluded from the corpus capture allowlist;
2. reserve the alpha/beta allowlisted workspaces for the four-source corpus and
   subsequent capture/retrieval cases;
3. bind the empty workspace into the execution identity and cleanup oracle;
4. offline-test workspace exclusion, exact four-source seeding, and cleanup;
5. preserve all candidate-7 science, thresholds, fault schedules, and the
   34-operation zero-credit rehearsal; and
6. require new candidate acceptance, identity acceptance, and single-use dress
   authority before another live attempt.

Candidate-8 remediation is not yet authorized.

## Evidence

- Dress receipt SHA-256:
  `82b5db3ec445cd9b22be484f48f26a03758f8f8587e4dc86c93ae588e0702dcf`
- Raw journal SHA-256:
  `7286a8b133e94b545f02eec3ba9d406b48b783c2262f170dd84b9e10b7316185`
- Authorization-consumption SHA-256:
  `7401dc5b5eb7393bcc184b2297bc0ab29ca1b9bf8487272d03305667d2ffb941`
- Adjudication SHA-256:
  `3f499ec0bd3f60d036d2c274444b0da94a6ae029de1f64572f9f6ac6f3ebb6da`
