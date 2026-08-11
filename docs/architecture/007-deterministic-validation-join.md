# Phase 3 Step 6 — Deterministic validation join

**Authority:** Principal statement `Accepted. Push and proceed to the next
step.` on 2026-08-11 after acceptance and release of Phase 3 Step 5.

## Boundary

This slice implements the deterministic join substrate named next in the
accepted Phase 1B handoff. It uses the frozen `completion-review` commands and
payloads from `tekroo.kernel.contracts/0.2.0`; it does not add or reinterpret a
catalogue type.

One review names an exact story or task lifecycle epoch, criteria revision,
evidence-set digest, branch-policy revision, explicit required branch IDs, the
`ALL_PASS` join, and either `WAIT_ALL` or `FAIL_FAST`. Branches and causal
parents are bounded, validated, and canonically ordered before the open command
is emitted. Repository or validator arrival order cannot alter the plan.

The join has five stable states under the frozen contract:

- `PENDING` while `WAIT_ALL` lacks any required result;
- `PASS` when every required branch passes;
- `FAIL` when any complete or fail-fast result set contains a failure;
- `INCONCLUSIVE` when no failure exists and a relevant result is inconclusive;
- `CONFLICT` for malformed policy, unknown branches/results, or contradictory
  repeats.

`FAIL` deterministically takes precedence over `INCONCLUSIVE`, removing map and
arrival-order dependence when both are present. An identical result replay is
idempotent. A contradictory result for the same branch conflicts and cannot
replace the accepted result.

## Authority separation

The application coordinator may open a review only as an exact policy
principal. It emits no actor, service, or execution identity. Validators submit
`completion-review.record-result` separately through the existing authenticated
command gateway; policy opening a review never impersonates a validator and
delivery never becomes validation evidence by itself.

The accepted negative requirement also calls for a resolution owner, deadline,
per-branch round budget, adjudicator, changed-condition evidence, and finding
supersession. Those organizational fields are not present in the frozen `0.2.0`
completion-review schemas. This slice therefore does not claim the complete
bounded-review lifecycle. Adding those facts requires an explicit later
contract revision rather than hidden application or adapter state.

Completion, reopening, escalation execution, acceptance, release coordination,
live validators/providers, OpenHands, SMA, deployment, migration, and
performance remain separately qualified work.
