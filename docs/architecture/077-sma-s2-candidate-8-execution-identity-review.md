# SMA-S2 candidate 8 execution-identity review

Date: 2026-08-14  
Status: `PASS_READY_FOR_PRINCIPAL_IDENTITY_REVIEW`

## Recommendation

`GO` for principal acceptance/freeze of the exact candidate-8 execution
identity only.

This is not authorization for live-state preflight, a live dress rehearsal, or
measured execution.

## Proposed identity

The prepared execution identity has SHA-256:

`285ef6fefa5fb5b4b71afe1ff368a119c80a5b3b839467d06883a9cb7e31cdcb`

It binds:

- deterministic stub endpoint `127.0.0.1:19128`;
- SMA bridge endpoint `127.0.0.1:8130`;
- the accepted candidate-8 preregistration and acceptance;
- the complete executable driver dependency closure;
- the exact candidate-8 workspace-isolation contract;
- the frozen SMA and OpenHands commits, trees, and runtime binaries;
- candidate-8 disposable MongoDB, Qdrant, workspace, conversation, and output
  namespaces; and
- a maximum 34-operation, zero-credit dress rehearsal while measured-attempt,
  real-model, and service-restart authority remains zero.

## Offline verification

**OBSERVED:** All 21 structured path-and-SHA references matched. The identity's
dependency list exactly matched the accepted candidate-8 preregistration
closure, and its workspace-isolation object exactly matched the accepted
candidate-8 configuration.

**OBSERVED:** The SMA and OpenHands source worktrees were clean at their bound
commits and trees. The SMA JAR, agent-server executable, and Agent Canvas
executable hashes matched the bound product identity.

**OBSERVED:** No live health check, process audit, active-conversation audit,
port-availability check, namespace-absence check, OpenHands conversation, SMA
or stub call, dress rehearsal, or measured operation occurred.

**INFERRED:** The identity is internally correspondent and ready for the
principal's exact accept/freeze decision. This says nothing yet about immediate
live suitability; those mutable conditions must be checked under a later,
separate authority immediately before any live mode.

## Authority boundary

The identity file contains the immutable status required by the accepted
candidate-8 driver, but its publication record explicitly marks principal
acceptance as pending. It becomes effective only when the principal accepts its
exact SHA-256 in a separate decision record.

Acceptance of this identity will still not authorize live-state preflight or
the live dress rehearsal. The next later gate will require separate authority
for immediate service-state, endpoint, namespace, and active-work checks before
any single-use, 34-operation, zero-credit rehearsal. Measured execution remains
a subsequent gate.
