# SMA-S2 preregistration candidate 5 acceptance

Date: 2026-08-14  
Status: `ACCEPTED_FROZEN_PREFLIGHT_AND_MEASURED_EXECUTION_NOT_AUTHORIZED`

## Accepted identity

The workspace principal, acting as the candidate-specific independent reviewer
and acceptance owner, explicitly accepted and froze:

`investigations/sma-q1/layered/sma-s2-preregistration-candidate-5.json`

SHA-256:
`2afe7930e20a95dd40d7477c4a7d4ee638477d0ea0a339b3742ff2b39446f939`

The hash was reproduced immediately before this record was created. The final
offline semantic receipt remains 11/11 PASS with 25 evidence-field mutations;
all 21 candidate bindings remained matched.

## Effect

**OBSERVED:** The principal's statement names candidate 5 and its exact hash,
accepts/freezes it, and explicitly withholds both live preflight and measured
execution authority.

Candidate 5 and all artifacts it binds are now immutable. Any correction must
be a prospectively reviewed successor. This acceptance freezes the experiment;
it does not report a measured PASS or earn
`OPENHANDS_SMA_BOUNDARY_QUALIFIED`.

The prior candidate-1 reviewer designation is not imported. The principal's
candidate-specific decision is the independent acceptance act for candidate 5.

## Authority fence

The following remain unauthorized and `NOT_RUN`:

- execution-identity preparation;
- live preflight;
- creation of OpenHands conversations or measured workspaces;
- creation of candidate-5 MongoDB or Qdrant namespaces;
- deterministic-stub network invocation through OpenHands;
- every measured S2 case and repetition;
- real-model calls; and
- production deployment.

The next separate gate is authorization to prepare and seal one candidate-5
execution identity and conduct one live preflight. A passing preflight would
still require a later explicit single-use authorization for the 103-repetition
measured execution.

