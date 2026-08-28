# SMA-S1 final-P2 R1 sealed identity and preflight

Date: 2026-08-28
Status: **PASS — READY FOR EXACT SINGLE-USE AUTHORIZATION**
Measured S1 execution: **NOT RUN / NOT AUTHORIZED**

The accepted candidate-2 identity cannot be passed directly to the frozen
measured harness because the harness requires a sealed, accepted,
nonauthorizing execution wrapper. The principal's `Proceed.` therefore caused
preparation and read-only preflight of that wrapper, not consumption of a
scientific execution attempt.

## Sealed identity

The nonauthorizing sealed identity is
`investigations/sma-q1/layered/sma-s1-execution-identity-p2final-r1-sealed-v1.json`,
SHA-256
`2cd705ab40a0aabee0fea5115154920675ebba600a2e2e670a368c51c25b1738`.
It binds the accepted candidate identity, offline qualification, independent
review, acceptance receipt, final-P2 implementation, unchanged 14-case/71-
repetition preregistration, and exact disposable measured namespaces.

## Post-seal qualification

**OBSERVED:** the unchanged harness passed 12/12 offline mechanics tests against
the sealed identity. The receipt SHA-256 is
`0fbc9bf3a671143305173304caf0bf6f0e6b67ecc2ac681f8fe5bd411b73c283`;
the append-only journal SHA-256 is
`04eddbe1492d2a744dd7c38cb0c734acfabdc452bceaac7c4713dfc64f9586e3`.
It started no SMA service, mutated no MongoDB or Qdrant resource, used no model
or OpenHands process, and executed zero measured cases.

## Immediate preflight

**OBSERVED:** the final-P2 SMA worktree remains clean at commit
`fe9903cdf59497aabf12cbe7722e1397a1372068`, tree
`fc2ae06c6333e94f6db34d691e50cb8bb1e5aa36`, with the exact accepted service
and source artifact hashes.

**OBSERVED:** MongoDB is the writable `rs0` primary and the exact measured
database is absent. Qdrant reports version 1.18.2 and both exact measured
collection reads return 404. The create-once measured output root is absent.

The preflight receipt is
`OUTPUT/phase-3/sma-s1-p2final-r1-sealed-v1-preflight-receipt.json`, SHA-256
`0a897a45629e2dea9bf0badaabaf98e6d4fef63e690a2ec250cf40315d02ce6e`.

## Next gate

The next gate is an explicit authorization for exactly one measured attempt
bound to the sealed identity and post-seal offline receipt hashes. This record
does not itself authorize or start that attempt.
