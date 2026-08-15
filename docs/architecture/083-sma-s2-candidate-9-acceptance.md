# SMA-S2 candidate 9 acceptance

Date: 2026-08-14  
Status: `ACCEPTED_FROZEN`

## Accepted identity

The workspace principal accepted and froze SMA-S2 candidate 9 with
preregistration SHA-256:

`d7680cc0b7b5ed501201e4b9dcc54b089071267c75ca43e3b9fab13b95b251a8`

Principal statement:

`ACCEPT/FREEZE SMA-S2 candidate 9, identity d7680cc0b7b5ed501201e4b9dcc54b089071267c75ca43e3b9fab13b95b251a8. This does not authorize execution-identity creation, live-state preflight, live dress rehearsal, Maven invocation, or measured execution.`

## Authority boundary

**OBSERVED:** This decision accepts the candidate-9 preregistration,
configuration, driver, offline working-directory harness, final 13/13 offline
receipt, and review packet as an immutable package.

**OBSERVED:** The accepted package preserves candidate-8 workspace isolation,
binds Maven promotion to the exact SMA project via explicit subprocess `cwd`,
prohibits parent-process cwd mutation, and retains sanitized subprocess failure
evidence.

**OBSERVED:** This decision does not authorize execution-identity creation or
acceptance, live-state preflight, Maven invocation, a live no-credit dress
rehearsal, measured execution, OpenHands conversation creation, SMA or stub
network invocation, real-model use, or a qualification claim.

**INFERRED:** The next permissible gate is a separate principal decision about
preparing one exact candidate-9 execution identity for review. Later live-state
preflight and single-use, 34-operation, zero-credit dress-rehearsal authorities
remain separate. Measured execution remains closed until the bound rehearsal
passes and a separate single-use measured authorization is issued.

Candidate 9 and its bound package must not be edited in place. Any correction
requires a separately reviewed successor.

## Acceptance receipt

The machine-readable acceptance record is
`investigations/sma-q1/layered/sma-s2-preregistration-candidate-9-acceptance.json`
with SHA-256:

`2579d877f36da0e5a90188db8a038fa8c2cf8aada529feb0047d4e13fce5332f`
