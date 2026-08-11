# Phase 3 Step 9 — deterministic escalation coordination

**Authority:** principal statement `Accept and proceed.` on 2026-08-11 after
acceptance and verified release of `tekroo.kernel.contracts/0.4.0`.

## Boundary

Step 9 implements the escalation operations added by contract `0.4.0` without
turning escalation into implicit process control. It has two operations:

1. `Open` consumes one explicit, policy-classified escalation proposal and
   submits exactly one `tekroo.command.escalation.open` command.
2. `Resolve` consumes one projected open escalation and one explicit
   adjudicator or timeout-policy decision and submits exactly one
   `tekroo.command.escalation.resolve` command.

This separately qualified implementation replaces the temporary
`ESCALATION_NOT_IMPLEMENTED` evaluator boundary recorded by the Step 9
contract-revision gate. The historical gate remains an accurate statement of
what that earlier contract-only slice qualified.

The coordinator does not discover retry exhaustion, validation conflict,
validation budget or deadline exhaustion, handoff cycles, or handoff budget
exhaustion. Those are upstream policy classifications. This implementation
validates and durably records an already explicit classification.

## Deterministic opening

The pure opening planner binds the exact subject revision and lifecycle epoch,
trigger, triggering-condition digest, adjudicator, timeout policy, resolution
owner, deadline, policy revision, finite round limit, ordered causal path,
unresolved question, and canonical evidence set. The route limit is fixed to
one.

Only policy authority can submit the opening. The directed causal path is
preserved as supplied and every referenced event must already be accepted.
The target subject must still exist at the exact revision and lifecycle epoch
and must not be terminal.

The durable semantic key is:

`(subject kind, subject ID, lifecycle epoch, trigger, condition digest)`.

Both the evaluator snapshot and the transactional repositories enforce that
this key is absent. The key is retained after terminal resolution, so an exact
duplicate cannot reopen under a different escalation ID. A materially new
condition requires a different evidence-backed condition digest.

## One-way resolution

Resolution is permitted only while the projected escalation is open and at
its exact revision. The current subject must still be nonterminal in the same
lifecycle epoch, and the opening event must be an explicit `RESPONSE` parent.

Before or at the deadline, only the exact designated adjudicator can decide,
within the finite resolution-round limit. Strictly after the deadline, only
the exact timeout policy can decide, and only as `BLOCKED` or
`HUMAN_REQUIRED`. `REROUTE` is prohibited. Every accepted decision makes the
escalation terminal; a terminal escalation cannot transition again.

The payload carries the planner's proposed decision time for a complete wire
record, but the kernel evaluates the deadline against its trusted
`DecisionContext.DecidedAt`. The committed projection applies the event's
trusted `CommittedAt`; caller-provided time cannot extend the deadline.

## Durable projection and concurrency

The in-memory and Mongo repositories project every accepted opening and
resolution. A projection contains the opening event identity, exact subject
binding, authorities, bounds, causal path, evidence, state, and terminal event
and outcome when resolved.

Projection validation occurs before an in-memory mutation and inside the
Mongo transaction. Mongo stores the semantic key as the document `_id`; the
in-memory store checks it while holding the commit lock. Therefore competing
openings for one semantic key have one durable winner and conflict losers.
Exact command replay remains governed by the existing receipt and idempotency
rules.

## Failure behavior

Invalid or ineligible planner inputs return a stable `NO_EFFECT` decision and
issue no command. Authoritative kernel evaluation rejects stale revisions,
stale lifecycle epochs, missing evidence or parents, semantic duplicates,
wrong adjudicators, premature timeout decisions, expired adjudicator
decisions, exhausted round budgets, invalid timeout outcomes, and terminal
replays without emitting an event.

## Exclusions

This slice does not select escalation triggers, infer escalation from legacy
blocking or handoff events, transfer work ownership, execute retries, choose a
successor, create successor work, establish organizational acceptance,
release or merge code, call provider APIs, integrate OpenHands or SMA, deploy,
or authorize production use.
