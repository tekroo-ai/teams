# SMA-S2 candidate 9 execution-identity review

Date: 2026-08-14  
Status: `PASS_READY_FOR_PRINCIPAL_IDENTITY_REVIEW`

## Recommendation

`GO` for principal acceptance/freeze of the exact candidate-9 execution
identity only.

This is not authorization for live-state preflight, Maven invocation, a live
dress rehearsal, or measured execution.

## Proposed identity

The prepared execution identity has SHA-256:

`a52d19eba7cdfd219f0a4977361c3d06054300e5669671f292523428cf894cfd`

It binds:

- deterministic stub endpoint `127.0.0.1:19129`;
- SMA bridge endpoint `127.0.0.1:8130`;
- the accepted candidate-9 preregistration and acceptance;
- the complete 15-entry executable driver dependency closure;
- the exact candidate-9 workspace-isolation contract;
- Maven promotion to `/Users/paul/work/tekroo-ai/sma-s1-p2m` with explicit
  subprocess working directory, immutable parent working directory, exact
  `pom.xml`, and sanitized failure evidence;
- the frozen SMA and OpenHands commits, trees, Maven POM, and runtime binaries;
- candidate-9 disposable MongoDB, Qdrant, workspace, conversation, and output
  namespaces; and
- a maximum one future 34-operation, zero-credit dress rehearsal while
  measured-attempt, real-model, and service-restart authority remains zero.

## Offline verification

**OBSERVED:** All 23 structured path-and-SHA references matched. The identity's
15-entry dependency list exactly matched the accepted candidate-9
preregistration closure. Its workspace-isolation, Maven-promotion, and
disposable-namespace objects exactly matched the candidate-9 configuration.

**OBSERVED:** The SMA worktree was clean at commit
`5d58be508c74ae8577d2fd31e3346be23a912ab3` and tree
`7222410443a6041496a5c313b2549f16eb937458`. The OpenHands worktree was clean
at commit `a338ba9b6cbb529886b755a335bae3dee0004700` and tree
`9e72f1b4ee0b857025d9f170600b9dce539b169a`.

**OBSERVED:** The bound Maven POM, SMA JAR, agent-server executable, and Agent
Canvas executable hashes matched the proposed identity.

**OBSERVED:** The preparation authority permits no Maven invocation.

**OBSERVED:** No live health check, process audit, active-conversation audit,
port-availability check, namespace-absence check, Maven invocation, OpenHands
conversation, SMA or stub call, dress rehearsal, or measured operation occurred.

**INFERRED:** The identity is internally correspondent and ready for the
principal's exact accept/freeze decision. This says nothing yet about mutable
live suitability or Maven execution success; those require later, separately
authorized checks or execution.

## Authority boundary

The identity file contains the immutable status required by the accepted
candidate-9 driver, but its publication record explicitly marks principal
acceptance as pending. It becomes effective only when the principal accepts its
exact SHA-256 in a separate decision record.

Acceptance of this identity will still not authorize live-state preflight,
Maven invocation, or the live dress rehearsal. A later gate must separately
authorize immediate service-state, endpoint, namespace, active-work,
workspace-isolation, and Maven-project checks before any single-use,
34-operation, zero-credit rehearsal. Measured execution remains a subsequent
gate requiring a passing bound rehearsal receipt and its own single-use
authorization.
