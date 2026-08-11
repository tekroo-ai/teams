# Deterministic escalation contract revision

## Decision

`tekroo.kernel.contracts/0.4.0` is a separately sealed successor package. It
does not modify `0.3.0`. The wire schema is `1.3.0` and the catalogue revision
is `4`.

The revision adds a dedicated escalation aggregate and four types:

- `tekroo.command.escalation.open`;
- `tekroo.event.escalation.opened`;
- `tekroo.command.escalation.resolve`; and
- `tekroo.event.escalation.resolved`.

Generic work blocking and task handoff remain distinct operations and are not
legacy aliases for escalation.

## Exact bounded opening

An escalation opening binds one exact story or task lifecycle epoch and
expected revision to:

- one accepted trigger classification and condition digest;
- one designated adjudicator principal;
- one timeout-policy principal;
- one accountable resolution-owner FQN;
- one policy revision, finite resolution-round limit, route limit of one, and
  absolute deadline;
- one ordered, duplicate-free causal event path;
- one unresolved question; and
- explicit evidence identities.

Only policy authority may open an escalation. Opening does not transfer work
ownership, complete work, establish organizational acceptance, or perform an
external effect.

## One-way terminal resolution

Before the deadline, only the exact designated adjudicator may resolve, within
the finite round limit. After the deadline, only the exact timeout-policy
principal may terminate, and only as `BLOCKED` or `HUMAN_REQUIRED`.

The complete terminal vocabulary is `RESOLVED`, `REJECTED`, `SPLIT`, `BLOCKED`,
and `HUMAN_REQUIRED`. A terminal escalation cannot be reopened or rerouted.
Materially new work is represented by causally linked successor work rather
than by bouncing the escalation among roles.

## Compatibility

All existing `1.2.0` semantic types have an explicit identity migration to
`1.3.0`. Escalation types are additive and have no `1.2.0` representation.
Readers retain `0.3.0` for historical replay.

No adapter may infer an escalation from historical `work.block` or
`task.handoff` events because those records do not provide the exact
adjudicator, timeout policy, resolution owner, budget, deadline, causal path,
unresolved question, or terminal outcome required by the new contract.

## Qualification boundary

The Step 9 contract-revision gate qualifies package structure, the independent
reference corpus, the pure escalation transition model, and forward
compatibility of the accepted Step 8 behavior. It does not qualify escalation
coordination, automatic trigger selection, repositories, provider effects,
organizational acceptance, release execution, deployment, or production use.

Until those implementation layers are separately qualified, the Go evaluator
rejects authoritative escalation commands with the stable no-effect reason
`ESCALATION_NOT_IMPLEMENTED`. Catalogue payload validation and pure transition
model conformance do not by themselves authorize organizational mutation.
