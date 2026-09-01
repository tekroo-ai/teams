# Phase 6 integrated acceptance and operating pilot

**Status:** PASS — Phase 6 complete for the supported local, single-team
operating path.

**Recorded:** 2026-09-01 UTC

**Source baseline:** commit
`447329e27208f32f990115b62eeb269df04adc59`, tree
`4419f7e935258e67814566b21055bdfc7aa9449a`, plus the reviewed Step 9 working
tree changes described below. No v3 data migration was performed.

## Supported-path result

The production `tekrood` daemon and `tekroo` operator client completed one real
feature through feature intake, product-owner refinement, specification,
architectural decomposition, implementation, independent validation,
product-owner recommendation, explicit operator acceptance, and deterministic
Git release. The released bare-repository `main` commit and tree were asserted
to equal the qualified implementation commit and tree. This run used the live
OpenHands server, the exact configured local model, the SMA prompt hook, a
disposable MongoDB replica set, isolated worktrees, and the ordinary operator
feature/release commands. It did not use raw kernel-command choreography.

The end-to-end test completed successfully in 431.91 seconds:

`go test -v -tags=phase6_pilot,mongo_integration
./adapters/operationalruntime -run '^TestPhase6LiveFeatureToAcceptance$'
-count=1 -timeout=70m`

The production scheduler also completed the required live concurrency ladder.
Each request reached OpenHands and the configured local model; the observed
Teams active-work peak equaled the requested concurrency:

| Requests | Observed peak | Batch elapsed |
|---:|---:|---:|
| 1 | 1 | 47.336s |
| 2 | 2 | 1m12.207s |
| 4 | 4 | 1m56.560s |
| 8 | 8 | 3m31.757s |

The eight-request run exposed the accepted profile's former 120-second LLM
timeout as too short for a saturated local batch. Raw OpenHands evidence showed
`LLMTimeoutError` at 120.01 seconds. The profile timeout is now 300 seconds;
the isolated eight-request rerun then completed all eight requests with peak
eight. No scheduler limit or authority rule was weakened.

The live interruption test passed in 9.36 seconds. It demonstrated an
OpenHands proxy outage, recovery without reauthorization, daemon
`SIGSTOP`/`SIGCONT`, operator cancellation of started work, terminal evidence,
one-invocation budget retention, and cancellation projection survival across a
clean daemon restart:

`go test -v -tags=phase6_pilot,mongo_integration
./adapters/operationalruntime -run
'^TestPhase5LiveSoakRecoversFromDependencyOutageAndCancelsAfterSuspend$'
-count=1 -timeout=45m`

## Archaeology workflow coverage

The retained catastrophic patterns are represented by executable regression
tests, not prompt guidance:

- renamed-message, role-cycle, circular-handoff, and budget-reset attacks are
  rejected by `TestMessageThreadAllowsProgressAndRejectsRenamedLoopBudgetResetAndCycle`;
- cyclic or unbounded plans are rejected by
  `TestFeaturePlanRejectsCyclesAndMaterializesFiniteDAG` and the bounded
  validation tests;
- validation conflicts, duplicate findings, stale completion, post-completion
  work, and restart budget resets are rejected by kernel and application tests;
- ambiguous starts and restart recovery reconcile before submission, preventing
  duplicate model work;
- role restart preserves stable FQN and fences the prior execution;
- human wait/response survives a fresh production-service reconstruction;
- merge timeout, replay, conflict, duplicate execution, and deterministic local
  release are covered by the release coordinator and Git-provider suites.

The legitimate path covers planning, directed assignment, implementation,
independent review, changed-evidence repair, escalation primitives, product
acceptance, and release. Promotion is not validated by another promotion task,
which removes a structural source of validator/product-owner back-and-forth.
Malformed validator output receives one changed-evidence bounded retry and then
blocks instead of crashing or looping.

## Regression receipts

The following final suites passed after the production fixes:

- `go test ./... -count=1`
- `go test -tags=mongo_integration ./... -count=1`
- `go test -v ./application -run
  '^TestOperationalCoordinatorExecutesOneInvocationAndNeverChainsAgentProse$'
  -count=1`

The first suite passed every package. The Mongo-tagged suite passed every
package, including the real replica-set tests. The explicit no-chaining test
passed and proves that agent prose cannot directly launch another model call.
The end-to-end feature run and the normal operator/MCP suites prove that raw
kernel JSON is not required for normal operation. The concurrency harness uses
the production service's internal command API only to create exact isolated
capacity probes; it is not the user workflow or evidence for operator usability.

## Production defects repaired during Step 9

- OpenHands `FinishObservation.message` is accepted as the exact final output.
- Feature-stage prompts carry authoritative feature state and strict result
  schemas; planning retries are finite.
- Validation targets are bound to the implementation workspace, branch, and
  baseline rather than the product-owner workspace.
- Promotion PASS completes directly; non-PASS promotion blocks rather than
  spawning another validation cycle.
- Malformed structured results cannot crash `tekrood` or create an unbounded
  retry.
- Idle MongoDB change-stream deadlines are normalized as no-work in both the
  execution outbox and organizational inbox, including the role worker.
- The accepted local-model OpenHands request timeout is 300 seconds so the
  qualified eight-request concurrency level can complete under saturation.
- Legacy live fixtures now use current workspace identity, profile,
  authentication, and typed cancellation contracts.

## Acceptance boundary

Phase 6 is accepted for the local single-team product path. The feature ledger
contains no retirement and no unclassified feature. Cross-team federation,
aliases, and trusted signed ingress remain explicit Phase 7 deferrals; they are
not silently claimed as implemented here. Production/historical databases and
v3 data were not accessed or migrated.

The machine-readable receipt is
`OUTPUT/phase-6/step-9/integrated-acceptance.json`.
