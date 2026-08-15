# SMA-S2 preregistration candidate 2 acceptance

Date: 2026-08-14  
Status: `ACCEPTED_FROZEN_MEASURED_EXECUTION_NOT_AUTHORIZED`

## Accepted identity

The workspace principal, acting as the designated independent reviewer and
acceptance owner, accepted and froze:

`investigations/sma-q1/layered/sma-s2-preregistration-candidate-2.json`

SHA-256:
`b7315835b6291d44d3d8cb918dbec1f16c1e64f2106fd70212c6a8f7ec678188`

The accepted package contains 20 cases and 103 planned repetitions. It binds
the remediated live OpenHands/SMA boundary, exact prompt/context fields, product
and scientific limits, zero-retry model-fault schedule, active cancellation and
client-disconnect oracles, evidence requirements, disposable namespaces, and
cleanup obligations.

## Effect of acceptance

**OBSERVED:** The principal explicitly accepted/froze the candidate and
explicitly withheld measured S2 authorization.

This acceptance freezes the experimental plan. It does not report a measured
PASS and does not earn `OPENHANDS_SMA_BOUNDARY_QUALIFIED`.

The accepted preregistration and every bound package artifact are immutable.
Any correction requires a prospectively reviewed successor; no accepted file
may be edited in place.

## Authority fence

The following remain unauthorized and `NOT_RUN`:

- immediate execution-identity preparation and preflight;
- creation of measured conversations, workspaces, MongoDB databases, or Qdrant
  collections;
- deterministic-stub invocation through OpenHands;
- any measured S2 case or repetition;
- any real model call; and
- production deployment.

The next separate gate is authority to prepare and seal one immediate S2
execution identity and preflight receipt. Even if that later preparation passes,
one explicit single-use measured-attempt authorization will still be required.

The machine-readable acceptance record is
`investigations/sma-q1/layered/sma-s2-preregistration-candidate-2-acceptance.json`.
