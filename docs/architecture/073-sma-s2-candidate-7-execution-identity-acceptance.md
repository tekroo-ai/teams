# SMA-S2 candidate 7 execution-identity acceptance

Date: 2026-08-14  
Status: `ACCEPTED_FROZEN`

## Accepted identity

The workspace principal accepted and froze the candidate-7 execution identity
only:

`40235ba0eda978c497dc17740aab09ebeb8fe3852e961b0f4d522737f3c38f22`

The identity binds deterministic stub endpoint `127.0.0.1:19127` and SMA
bridge endpoint `127.0.0.1:8130`.

Principal statement: `ACCEPT/FREEZE the execution identity only.`

## Authority boundary

**OBSERVED:** The identity and its bound package are immutable. Any correction
requires a successor identity.

**OBSERVED:** This decision does not establish current port availability,
service health, active-conversation safety, or disposable-namespace absence.
It does not authorize a live check, service restart, OpenHands conversation,
SMA or stub invocation, live dress rehearsal, measured execution, real-model
call, or qualification claim.

**INFERRED:** The next permissible gate is a separate single-use authorization
for the 34-operation, zero-credit candidate-7 dress rehearsal. That future gate
must begin with immediate live-state, active-work, endpoint-availability, and
namespace-absence checks. Measured execution remains a still-later gate and
requires a passing identity-bound dress-rehearsal receipt.

## Acceptance receipt

The machine-readable acceptance record is
`investigations/sma-q1/layered/sma-s2-candidate-7-execution-identity-acceptance.json`
with SHA-256:

`cf520fe891c218bfac60500a2625c399211be7be36c64b0c483332fb57e83055`
