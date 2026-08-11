# Phase 3 contract revision — bounded validation and policy finalization

**Authority:** principal authorization to revise the contract and determine
whether earlier implementation steps required revision, 2026-08-11.

## Decision

`tekroo.kernel.contracts/0.3.0` is a new immutable normative package. It does
not modify `0.2.0`. The schema version is `1.2.0` and the catalogue revision is
3.

The revision closes the gap recorded by Step 6 between the deterministic join
substrate and the accepted NEG-002 organizational requirement. A completion
review now contains:

- an exact validator principal and resolution-owner FQN for every branch;
- explicit acceptance criteria and input evidence for every branch;
- a deadline and finite round limit for every branch;
- an exact adjudicator principal with its own deadline and round limit;
- six terminal result values: `PASS`, `FAIL`, `BLOCKED`, `INCONCLUSIVE`,
  `SUPERSEDED`, and `CANCELLED`;
- typed findings, result-event supersession, and changed-condition evidence;
- one policy-only finalization command that binds the current result-event set;
- completion commands that reference that exact finalized review and revision.

The kernel uses its trusted decision time for deadline evaluation. Caller
payloads cannot supply or backdate that timestamp. Validator and adjudicator
identity are compared with the authenticated command principal. A policy
timeout is accepted only after the branch deadline and only as `BLOCKED` or
`CANCELLED`.

## Compatibility

Unchanged `1.1.0` semantic types have an identity migration to `1.2.0`. The
four changed command families—review opening, result recording, task
completion, and story completion—are not inferred from old data. They require
an explicit adapter with the missing exact context. Historical readers retain
`0.2.0`; bounded-review and finalization records are never down-converted.

## Earlier-step adjudication

Steps 1–5 do not require semantic revision. Their thin-adapter, HTTP/MCP,
directed-channel, execution-coordinator, and assignment-readiness boundaries
do not depend on the missing NEG-002 fields. Their historical acceptance and
release receipts remain evidence for their released trees.

Step 6 also remains valid for the claim it actually made: canonical opening
plans, order-independent `ALL_PASS` joins, deterministic terminal precedence,
and conflict-on-contradiction. Its architecture record explicitly excluded the
missing organizational fields. The current change forward-augments Step 6 and
requalifies its implementation under `0.3.0`; it does not rewrite the Step 6
receipt or claim that `0.2.0` provided bounded adjudication.

## Implementation boundary

The implementation adds bounded branch specifications and immutable result
records alongside the existing join projection. The current result for a
branch may replace a prior result only by citing that exact result event and
non-empty changed-condition evidence. The result history remains retained.
Finding keys deduplicate identical logical findings across current branch
results; the same key with conflicting content is rejected.

Policy finalization succeeds only when its terminal status equals the current
complete join, its expected review revision is current, and its result-event
set exactly equals the current branch result set. Task and story completion
then require that exact finalized `PASS` review; an individual validator
`PASS` is not organizational completion. Finalization seals the review:
subsequent result submissions are rejected rather than making the finalized
decision stale.

This slice does not start validators, choose deadlines, resolve findings,
reopen work automatically, or execute release/provider actions. Those remain
application-policy and later coordination work over the normative kernel
commands.
