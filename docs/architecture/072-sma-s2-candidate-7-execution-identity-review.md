# SMA-S2 candidate 7 execution-identity review

Date: 2026-08-14  
Status: `PASS_READY_FOR_PRINCIPAL_IDENTITY_REVIEW`

## Recommendation

`GO` for principal acceptance/freeze of the exact candidate-7 execution
identity only.

This is not authorization for a live dress rehearsal or measured execution.

## Proposed identity

The prepared execution identity has SHA-256:

`40235ba0eda978c497dc17740aab09ebeb8fe3852e961b0f4d522737f3c38f22`

It binds:

- deterministic stub endpoint `127.0.0.1:19127`;
- SMA bridge endpoint `127.0.0.1:8130`;
- the accepted candidate-7 preregistration and acceptance;
- the complete executable driver dependency closure;
- the frozen SMA and OpenHands commits, trees, and runtime binaries;
- candidate-7 disposable MongoDB, Qdrant, workspace, conversation, and output
  namespaces; and
- a maximum 34-operation, zero-credit dress rehearsal while measured-attempt,
  real-model, and service-restart authority remains zero.

## Offline verification

**OBSERVED:** The candidate-7 executable identity validator accepted the
identity, including the exact loopback endpoint shape and dependency closure.

**OBSERVED:** All 19 path-and-SHA references matched. The SMA and OpenHands
source worktrees were clean at their bound commits and trees. The SMA JAR,
agent-server executable, and Agent Canvas executable hashes also matched the
frozen product identity.

**OBSERVED:** No live health check, process audit, active-conversation audit,
port-availability check, namespace-absence check, OpenHands conversation, SMA
or stub call, dress rehearsal, or measured operation occurred.

**INFERRED:** The identity is internally correspondent and ready for the
principal's exact accept/freeze decision. This says nothing yet about immediate
live suitability; those mutable conditions must be checked immediately before
the separately authorized dress rehearsal.

## Authority boundary

The identity file contains the immutable status expected by the accepted
driver, but its publication record explicitly marks principal acceptance as
pending. It becomes effective only when the principal accepts its exact
SHA-256 in a separate decision record.

Acceptance of this identity will still not authorize the live dress rehearsal.
The next later gate would be a single-use authorization for the 34-operation
zero-credit rehearsal, preceded by immediate live-state, endpoint, namespace,
and active-work checks. Measured execution remains a subsequent gate.
