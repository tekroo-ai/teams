# Phase 3 Step 8 — deterministic completion and reopening coordination

**Authority:** principal statement `Step 8 authorized.` on 2026-08-11 after
acceptance and verified release of the Step 7 bounded-validation revision.

## Boundary

Step 8 coordinates two separate frozen-contract operations over
`tekroo.kernel.contracts/0.3.0`:

1. `Complete` consumes an active, runnable task or story plus one current
   policy-finalized `PASS` review and submits exactly one completion command.
2. `Reopen` consumes a completed task or completed/accepted story and submits
   exactly one explicit evidence-backed reopening command.

The coordinator does not infer completion from provider status, a validator
result, a shell exit, or a successful delivery. It does not automatically
reopen a failed review. Planning failures and ineligible states return a stable
no-command decision.

## Completion

The pure completion plan binds the exact subject, lifecycle epoch, criteria
revision, branch-policy revision, review revision, finalization event, evidence
references, artifact digests, dependency revisions, and causal parents.
Inputs are bounded and canonically ordered so evidence, artifact, dependency,
and parent arrival order cannot alter the command.

Task completion requires the current owning actor and an exact fenced
execution tuple. Story completion is coordinated by a human or policy
principal and carries exact completed-task revision preconditions. The kernel
still makes the authoritative durable transition; the coordinator only submits
the request.

No completion command is emitted when work is not active and runnable, the
review is stale or not finalized, the finalized status is not `PASS`, an
exception remains unresolved, a story dependency is incomplete, or the work
is already complete. Handler idempotency makes an exact replay return the
stored receipt rather than create another completion event.

## Reopening

Reopening is a distinct human/policy-authorized command. It records the prior
epoch, a new scope revision, reason, owner carry-forward choice, exact evidence,
and causal parents. The kernel increments the lifecycle epoch. Therefore, a
review finalized for the prior epoch cannot qualify completion of the reopened
work.

Owner carry-forward is valid only when an owner exists. When carry-forward is
false, the kernel clears ownership. A task may reopen only from `COMPLETED`; a
story may reopen from `COMPLETED` or `ACCEPTED`. Active, closed, draft, ready,
and planning work returns a no-command decision.

## Exclusions

This slice does not execute escalation, choose whether a failed review should
be reopened, coordinate organizational acceptance, generate or merge release
plans, call Git/provider APIs, integrate OpenHands or SMA, execute production
migrations, or make performance claims.
