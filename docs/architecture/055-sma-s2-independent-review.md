# SMA-S2 independent review

Date: 2026-08-14  
Reviewer: workspace principal  
Coordinator recommendation: `NO_GO_REMEDIATION_REQUIRED`

## Decision

Do not accept or freeze SMA-S2 preregistration candidate 1. Its offline controls
are sound, but the independent review found two substantive blockers that must
be corrected prospectively in a successor candidate.

No measured execution occurred during this review. No service was restarted,
no conversation or model call was created, and no measured SMA namespace was
created.

**OBSERVED:** A read-only interruption check found zero running,
waiting-for-confirmation, or stuck OpenHands conversations. One persisted
conversation is paused: `Tekroo Trader`
(`4c2e5f84-ecd3-4971-b375-a0fa9905a058`), last updated
`2026-08-13T17:38:02.080929Z`. A controlled restart therefore requires explicit
authority to preserve and restart around that paused conversation; this review
does not supply that authority.

## What passed review

**OBSERVED:** All eight explicitly bound package digests matched. Sixteen
semantic and authority checks passed: accepted S1 lineage, unique cases, planned
repetition count, identity bindings, product and scientific limits, prompt and
context segment oracles, corrected secret and oversized-context predicates,
offline controls, acceptance state, execution fence, and successor-identity
requirement.

**OBSERVED:** Offline harness candidate 4 contains eight passing test records and
zero failures. The targeted OpenHands JUnit file contains three passing testcase
elements. The hook JUnit file contains eight passing testcase elements. Neither
file contains a failure or error element.

## Blocker 1: source and executing runtime are not the same identity

**OBSERVED:** The running `agent-server` process started at 15:56:43 local time
on 2026-08-13. The semantic-message commit was committed at 19:43:50, and the
bound HEAD was committed at 19:48:23.

**COMPUTED:** The process predates the semantic commit by 3:47:07 and bound HEAD
by 3:51:40.

**OBSERVED:** The OpenAPI schema fetched from that running process omits
`authorship_origin`, `semantic_purpose`, and `agent_response_finality` from
`MessageEvent`. The bound source and its targeted tests contain those fields.

**INFERRED:** Candidate 1 binds current files and a stale process/schema as if
they were one executing identity. That prevents reliable qualification of
feedback-loop eligibility and semantic provenance. Source tests cannot
substitute for the live schema.

Required correction: after explicit restart authority and an interruption check,
restart Agent Canvas/agent-server from the exact clean source, freeze a new live
OpenAPI snapshot, prove the three fields are present, and bind the new process
and schema identity in a successor candidate.

## Blocker 2: model-stub faults exist but are not preregistered as measurements

**OBSERVED:** The stub implements timeout/client disconnect, malformed response,
HTTP 503, and transport failure. The 18-case manifest schedules only the
hook/bridge fault matrix; it does not assign those model-stub modes or active
cancellation to exact cases and repetitions.

**OBSERVED:** The qualification configuration does not bind model retry count,
retry waits, model timeout, or a case-to-fault activation map. The bound
OpenHands source defaults to five retries and a 300-second timeout.

**INFERRED:** Candidate 1 cannot reproducibly prove when each model fault is
activated, how many requests are permitted, or whether cancellation and shutdown
complete within a frozen bound. Default retries can also violate one-request and
wall-clock predicates.

Required correction: add explicit fault/cancellation cases or schedules; bind
zero retries, exact timeout, activation channel, permitted request counts,
cancellation trigger and deadline, disconnect observation, process inventory,
and cleanup oracle; then repeat the offline qualification against that successor.

## Non-blocking disclosure

**OBSERVED:** Pytest reported eight collected hook tests and the JUnit file emits
eight testcase elements, but its aggregate `tests` attribute is 15. The eight
testcase records have zero failures/errors. This likely reflects unittest
subtest accounting, but that mechanism has not been independently verified.
Therefore the evidence floor is eight emitted hook testcase records, not an
unqualified claim of 15 tests.

## Recommendation

Authorize one bounded remediation package with two parts:

1. revise the static S2 preregistration/configuration/harness to bind the missing
   model-fault and cancellation controls; and
2. perform one controlled Agent Canvas/agent-server restart, only after proving
   that no protected OpenHands work will be interrupted, then rebind the live
   schema and process identity.

The successor returns to the principal for independent review and explicit
accept/freeze. Execution identity preparation and measured S2 execution remain
separate later decisions.

The machine-readable recommendation is
`OUTPUT/phase-3/sma-s2-independent-review-recommendation-candidate-1.json`.
