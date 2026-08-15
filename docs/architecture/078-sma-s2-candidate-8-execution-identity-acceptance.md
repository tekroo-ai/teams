# SMA-S2 candidate 8 execution-identity acceptance

Date: 2026-08-14  
Status: `ACCEPTED_FROZEN`

## Accepted identity

The workspace principal accepted and froze only the candidate-8 execution
identity:

`285ef6fefa5fb5b4b71afe1ff368a119c80a5b3b839467d06883a9cb7e31cdcb`

The identity binds deterministic stub endpoint `127.0.0.1:19128`, SMA bridge
endpoint `127.0.0.1:8130`, and the exact candidate-8 workspace-isolation
contract.

Principal statement:

`ACCEPT/FREEZE the candidate-8 execution identity 285ef6fefa5fb5b4b71afe1ff368a119c80a5b3b839467d06883a9cb7e31cdcb only. This does not authorize live-state preflight, live dress rehearsal, or measured execution.`

## Authority boundary

**OBSERVED:** The identity and its bound package are immutable. Any correction
requires a successor identity.

**OBSERVED:** This decision does not establish current port availability,
service health, active-work safety, disposable-namespace absence, or live
workspace isolation. It does not authorize a live check, service restart,
OpenHands conversation, SMA or stub invocation, live dress rehearsal, measured
execution, real-model call, or qualification claim.

**INFERRED:** The next permissible gate is a separate single-use authorization
for the 34-operation, zero-credit candidate-8 dress rehearsal. That future gate
must begin with immediate live-state, active-work, endpoint-availability,
namespace-absence, and workspace-isolation checks. A failed preflight must
consume the authority without starting the rehearsal. Measured execution remains
a later gate requiring a passing identity-bound dress-rehearsal receipt.

## Acceptance receipt

The machine-readable acceptance record is
`investigations/sma-q1/layered/sma-s2-candidate-8-execution-identity-acceptance.json`
with SHA-256:

`669c186c27e5240eef3cdf94a34a531253cc4d4db6adb7ce59fddf8f544ef5db`
