# Phase 3 Step 7 — bounded-validation contract encoding gaps

## Status

Contract revision authorized. Released packages `0.1.0` and `0.2.0` remain
immutable.

## Observed gap

The accepted `NEG-002` conditions require exact resolution ownership, a finite
round budget and deadline for every branch, typed terminal outcomes, finding
deduplication and supersession, changed-condition evidence for revalidation,
and bounded exact-principal adjudication.

Contract `0.2.0` completion-review opening records only the subject, lifecycle
epoch, criteria and policy revisions, evidence-set digest, branch IDs, join
rule, and partial-result policy. Its result schema records a branch, policy
revision, one of `PASS`, `FAIL`, or `INCONCLUSIVE`, reasons, and evidence.

The generic attempt budget is keyed by target aggregate and command type, so a
completion-review target has one shared record-result budget rather than one
budget per branch. Story completion checks accepted result-event IDs qualified
`PASS`; it cannot prove that the result came from the assigned validator, was
within its deadline and round limit, has no pending adjudication, or supersedes
a prior finding through changed-condition evidence.

## Required successor encoding

Contract `0.3.0` will introduce wire schema `1.2.0` and catalogue revision `3`.
The completion-review opening payload will replace bare branch IDs with bounded
branch specifications containing:

- stable branch ID;
- exact validator principal;
- accountable resolution-owner FQN;
- explicit acceptance criteria and input evidence;
- absolute policy-selected deadline; and
- finite round limit.

It will also record one exact adjudicator principal with its own deadline and
round limit. Result payloads will record source role, round, the complete
terminal vocabulary, typed finding identities, prior-result supersession, and
changed-condition evidence.

No adapter may infer missing `1.2.0` review fields from `1.1.0` history. Old
events remain readable under their original contract; using them for a new
authoritative completion requires an explicit migration or principal-approved
legacy policy.
