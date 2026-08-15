# SMA-S2 candidate 8 dress-rehearsal preflight NO-GO

Date: 2026-08-14  
Status: `NO_GO_BEFORE_REHEARSAL`

## Result

The single-use candidate-8 dress authority was consumed by its immediate live
preflight. No dress-rehearsal operation began.

**OBSERVED:** The accepted execution identity, acceptance, authorization,
driver, and configuration hashes matched their frozen values.

**OBSERVED:** Agent Canvas ingress `http://127.0.0.1:8000/health` and agent-server
`http://127.0.0.1:18000/health` both rejected the TCP connection. Each `curl`
probe exited with code 7 and produced HTTP status `000`.

**OBSERVED:** The authorization allowed zero service restarts. The coordinator
therefore stopped without starting a service, auditing conversations, probing
the remaining mutable conditions, creating the dress output directory, or
invoking the harness.

**COMPUTED:** Live preflight attempts: 1. Dress-rehearsal attempts: 0.
Dress-rehearsal operations: 0. Measured executions: 0. Real-model calls: 0.
Service restarts: 0. Credit: 0.

**INFERRED:** This is an environmental prerequisite failure, not evidence for
or against the candidate-8 workspace-isolation correction. Candidate 8 does not
need a scientific redesign on this evidence.

## Evidence

The live-preflight receipt is
`OUTPUT/phase-3/sma-s2-candidate-8-dress-rehearsal-live-preflight.json` with
SHA-256:

`02eca6b2587492428bf5a2df8bc9d514f0a14eaef6a961944e36da1cefc35a2d`

The consumed authorization is
`investigations/sma-q1/layered/sma-s2-candidate-8-dress-rehearsal-single-use-authorization.json`
with SHA-256:

`3051d37b768a500e5df887277e555add531f89550804c32d95c7f6fee17a1044`

## Next gate

A separate principal authority is required to perform one controlled start of
the already-bound Agent Canvas/agent-server services. After those services are
healthy, a new single-use candidate-8 dress authority must be issued; the
consumed authority cannot be reused.
