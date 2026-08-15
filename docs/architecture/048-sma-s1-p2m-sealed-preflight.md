# SMA-S1 P2-M sealed identity and immediate preflight

Date: 2026-08-14  
Decision: **PREFLIGHT PASS / READY TO REQUEST SINGLE-USE AUTHORIZATION**  
Measured S1 execution: **NOT RUN / NOT AUTHORIZED**

## Acceptance continuity

The principal accepted and froze candidate 7 and its offline harness
qualification. Neither accepted artifact was edited.

**OBSERVED:** the measured harness requires an execution identity whose status is
`SEALED_ACCEPTED_NOT_AUTHORIZING_NOT_STARTED` and whose
`executionFence.identityAccepted` value is `true`. The accepted candidate was
correctly immutable and retained its candidate status, so it could not itself
satisfy that mechanical input contract.

A successor acceptance envelope was therefore created at
`investigations/sma-q1/layered/sma-s1-execution-identity-p2m-sealed-v1.json`.
It binds the exact accepted candidate, acceptance record, candidate offline
qualification, preregistration, SMA commit/tree and artifacts, driver, harness,
configuration, and isolated namespaces. It changes no scientific or product
binding and creates no execution authority.

## Post-seal harness qualification

**OBSERVED:** the exact sealed identity has SHA-256
`f8bf0323680504df1afb14fe6184e87eaaa675170079c005ce827fcfdfe15e0e`.

**OBSERVED:** the offline harness self-test bound to that identity passed 10 of
10 tests, executed zero measured cases, did not start SMA, and did not mutate
MongoDB or Qdrant. Its receipt is
`OUTPUT/phase-3/sma-s1-harness-p2m-sealed-v1/self-test-receipt.json`, SHA-256
`ca3c5aedebb05fe69fd88d4a2570ff6560c45f49cfe0e8d109591b9c95b7f076`.

## Immediate preflight

The machine-readable receipt is
`OUTPUT/phase-3/sma-s1-p2m-sealed-v1-preflight-receipt.json`.

**OBSERVED:** immediately before the receipt was written:

- the isolated SMA worktree was clean at commit
  `5d58be508c74ae8577d2fd31e3346be23a912ab3` and tree
  `7222410443a6041496a5c313b2549f16eb937458`;
- the remote P2-M ref resolved to that same commit;
- MongoDB 8.3.4 was the writable `rs0` primary and the measured database was
  absent;
- Qdrant 1.18.2 returned HTTP 404 for both measured collections;
- the create-once measured output root was absent;
- system-wide memory free was 51%, swap use was 356 MiB of 2048 MiB, and the
  filesystem reported 4,736,882,184 KiB available.

**COMPUTED:** all declared immediate preflight checks passed for the exact sealed
identity and post-seal receipt.

**INFERRED:** the next correct action is to request one explicit, single-use
measured S1 authorization. That authorization must bind the exact five hashes
required by the harness. This preflight and the sealed identity do not
themselves authorize an attempt.
