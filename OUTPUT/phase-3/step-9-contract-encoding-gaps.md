# Phase 3 Step 9 — deterministic-escalation contract encoding gaps

## Status

Successor contract revision required before implementation. Released contract
`tekroo.kernel.contracts/0.3.0` remains immutable.

## Observed contract surface

The `0.3.0` catalogue contains no type whose identifier includes `escalation`,
and `schemas/payloads.schema.json` contains no escalation payload definition.
The package does contain generic work-block and task-handoff commands, plus the
general bounded-iteration invariant. Those records do not encode an escalation
identity, its one-time route, exact adjudicator, accountable resolution owner,
complete causal path, unresolved question, policy-selected escalation budget,
or the accepted terminal outcome vocabulary.

Therefore neither `tekroo.command.work.block` nor
`tekroo.command.task.handoff` is a semantically sufficient substitute for the
accepted escalation contract.

## Accepted requirements that are not encoded

The binding Phase 1B decisions require:

- automatic retry exhaustion to stop and produce an explicit blocked or
  escalated outcome with an accountable owner and intervention evidence
  (`NEG-001`);
- conflicting or inconclusive validation to reach one designated adjudicator
  after bounded review and not circulate indefinitely (`NEG-002`);
- cycle detection or handoff-budget exhaustion to route once to a designated
  adjudicator with the complete causal path and unresolved question
  (`NEG-004`);
- finite handoff-depth and escalation budgets selected by workflow policy, not
  inferred from Phase 1A counts (`NEG-004`); and
- escalation to terminate without bouncing among roles as `RESOLVED`,
  `REJECTED`, `SPLIT`, `BLOCKED`, or `HUMAN_REQUIRED` (`NEG-004`).

## Minimum successor encoding

Create a separately sealed `tekroo.kernel.contracts/0.4.0` package with wire
schema `1.3.0` and catalogue revision `4`. Preserve every `0.3.0` artifact and
add only the escalation semantics needed for Step 9:

1. `tekroo.command.escalation.open` and
   `tekroo.event.escalation.opened` create one durable escalation aggregate for
   one exact work lifecycle epoch and triggering condition.
2. The opening record binds an exact designated adjudicator principal, an exact
   timeout-policy principal, one accountable resolution-owner FQN, an absolute
   deadline, a finite policy budget, the governing policy revision, the
   complete ordered causal path, the unresolved question, trigger
   classification, and evidence identifiers.
3. The trigger vocabulary is restricted to accepted sources:
   `RETRY_EXHAUSTED`, `VALIDATION_CONFLICT`, `VALIDATION_INCONCLUSIVE`,
   `VALIDATION_BUDGET_EXHAUSTED`, `VALIDATION_DEADLINE_EXPIRED`,
   `HANDOFF_CYCLE_DETECTED`, and `HANDOFF_BUDGET_EXHAUSTED`.
4. `tekroo.command.escalation.resolve` and
   `tekroo.event.escalation.resolved` require the expected escalation revision,
   current work lifecycle epoch, terminal outcome, reasons, and resolution
   evidence. Before the deadline, only the exact designated adjudicator may
   resolve. After the deadline, only the exact timeout-policy principal may
   terminate as `BLOCKED` or `HUMAN_REQUIRED`.
5. Terminal outcomes are exactly `RESOLVED`, `REJECTED`, `SPLIT`, `BLOCKED`,
   and `HUMAN_REQUIRED`. A terminal escalation cannot be reopened or escalated
   again; new work uses causally linked successor nodes.
6. Opening an equivalent escalation is idempotent. A conflicting open request,
   stale lifecycle epoch, stale revision, wrong adjudicator, expired resolution,
   budget overflow, or attempt to make the escalation path circular returns an
   explicit stable no-effect outcome.
7. Escalation does not itself transfer task ownership, complete work, establish
   organizational acceptance, or execute provider/Git effects.

Existing `1.2.0` semantic types receive an explicit identity migration to
`1.3.0`; no adapter may infer escalation records from historical block or
handoff events.

## Step 8 impact

The Step 8 completion/reopening behavior need not be redesigned. Its contract
identity and schema constants, frozen-package corpus, catalogue loading, and
qualification reports must be forward-augmented and requalified under `0.4.0`
before Step 9 can consume the successor contract.

## Recommendation

Authorize the `0.4.0` successor contract revision and Step 8 forward
requalification. Only after that package passes its structure, fixture,
compatibility, immutability, and Go regression gates should deterministic
escalation coordination be implemented.
