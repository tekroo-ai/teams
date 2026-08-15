# SMA-S1 evidence-v2 sealed identity and immediate preflight

Date: 2026-08-14  
Decision: **PREFLIGHT PASS / READY TO REQUEST SINGLE-USE AUTHORIZATION**  
Measured S1 execution: **NOT RUN / NOT AUTHORIZED**

## Accepted and frozen lineage

**OBSERVED:** the principal accepted and froze evidence-v2 candidate 4 identity
SHA-256 `d7a388816ccb4ea2c12db365de1116c445f6b8dd18eca8861f118afa7cfb8724`
and offline qualification SHA-256
`1b54fa137a153964d6b07e9f244631e354d2ef1df1ce82c960de0bd3bb4831c1`.
The exact statement is preserved in
`investigations/sma-q1/layered/sma-s1-p2m-evidence-v2-candidate-4-acceptance.json`.
Neither accepted artifact was edited.

**OBSERVED:** the measured harness requires a successor execution identity with
status `SEALED_ACCEPTED_NOT_AUTHORIZING_NOT_STARTED` and an accepted execution
fence. The immutable candidate deliberately retained its candidate status.

**COMPUTED:** the non-authorizing successor at
`investigations/sma-q1/layered/sma-s1-execution-identity-p2m-evidence-v2-sealed-v1.json`
has SHA-256
`139b38628e049ce2c0b04400014eacb2dd6ba9b4e9306c1347f4f0d3d12d3e36`.
It binds the accepted candidate, acceptance record, accepted candidate
qualification, unchanged scientific preregistration, exact P2-M implementation,
evidence-v2 driver and harness, and candidate-4 isolation names. It grants zero
measured attempts.

## Post-seal offline controls

**OBSERVED:** the post-seal self-test receipt reports `PASS` for 12 of 12
controls, zero measured cases, no SMA service start, no MongoDB or Qdrant
mutation, no OpenHands/model use, and no execution authority. The receipt is
`OUTPUT/phase-3/sma-s1-harness-p2m-evidence-v2-sealed-v1/self-test-receipt.json`,
SHA-256
`e57ae8a21a01a7b1955dcf95332fb9129cdf585f903538c4eb3a7301b64565e3`.
Its 91-record journal has SHA-256
`358f73f0ef4d19cd4b9c178657f2b0e5e24a507df780095f69212f9b80424cdf`.

## Immediate read-only preflight

The machine-readable receipt is
`OUTPUT/phase-3/sma-s1-p2m-evidence-v2-sealed-v1-preflight-receipt.json`.

**OBSERVED:** at `2026-08-14T14:53:59Z`:

- the isolated SMA worktree was clean at commit
  `5d58be508c74ae8577d2fd31e3346be23a912ab3` and tree
  `7222410443a6041496a5c313b2549f16eb937458`;
- the remote P2-M ref resolved to the same commit;
- the bound service and sources artifacts matched their sealed hashes;
- MongoDB 8.3.4 was the writable `rs0` primary and the candidate-4 measured
  database was absent;
- Qdrant 1.18.2 returned HTTP 404 for both candidate-4 measured collections;
- the create-once evidence-v2 measured output root was absent;
- system-wide memory free was 51%, swap use was 356 MiB of 2,048 MiB, load
  averages were 4.35, 4.13, and 3.92, and the filesystem reported
  4,736,829,848 KiB available.

**COMPUTED:** every declared immediate preflight check passed for the exact
sealed identity and post-seal receipt.

**INFERRED:** the next action is one explicit, single-use measured S1
authorization binding the exact sealed identity and post-seal receipt hashes.
This acceptance, sealed identity, offline run, and preflight do not authorize a
measured attempt.
