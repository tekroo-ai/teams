# Prompt for the SMA team — prospective S1 remediation R1–R4

You are authorized to perform one prospective, non-measured SMA remediation
work product. This is not an SMA-S1 execution and must not claim qualification.

Work in `/Users/paul/work/tekroo-ai/sma`. The verified starting state is clean
`master` at `e89ff9e0bab192689122d5e7b9d36d16cae5d368`, synchronized with
`origin/master`. Verify that state yourself before editing. If it has changed or
contains uncommitted work, stop and report the exact difference; do not discard,
overwrite, or silently incorporate it.

Read these governing inputs completely before changing code:

1. `/Users/paul/work/tekroo-ai/teams/docs/architecture/045-sma-s1-advance-gap-adjudication-and-remediation.md`
2. `/Users/paul/work/tekroo-ai/teams/OUTPUT/phase-3/sma-s1-advance-gap-adjudication.json`
3. `/Users/paul/work/tekroo-ai/sma/docs/OPENHANDS_SMA_INTEGRATION_PLAN.md`
4. `/Users/paul/work/tekroo-ai/sma/docs/PROGRAMMING_MEMORY_ADAPTER_AND_CONTEXT_PROTOCOL.md`

The adjudication boundary is binding for this task:

- implement the four confirmed prospective remediations R1–R4;
- investigate P1 and P2 from primary evidence and return proposed bindings;
- do not invent a field, event discriminator, or sensitivity policy;
- do not weaken accepted predicates, fixtures, or existing tests;
- do not edit the Teams repository or accepted contract packages; and
- do not start OpenHands, invoke a model, use real credentials, access
  production data, or run the measured S1 schedule.

## R1 — typed parent/child provenance

Add typed, persistence-mapped provenance sufficient to preserve actual
conversation ID, optional parent conversation ID, event ID, workspace,
profile/role, sequence, and model when supplied. Preserve deterministic memory
identity, duplicate reconciliation, and backward-compatible reads of existing
documents. Do not encode lineage in free text or infer it from actor identity.
Add mapper round-trip, missing-parent, parent/child, restart, and duplicate
regressions.

## R2 — irrelevant-query rejection

Add an explicitly configurable semantic-retrieval acceptance threshold with
validated deterministic defaults. Apply it before retrieval-event/result
construction and before pressure or other retrieval side effects. Retain exact
content-free exclusion reasons suitable for qualification evidence. Add
below/equal/above-threshold, empty, partition, raw/ineligible, and no-side-effect
regressions. Record the proposed default and its rationale; the Teams execution
identity will bind the actual measured value later.

## R3 — whole-memory context budgeting

Change context construction so only complete framed memory entries are emitted.
Append an entry only when the entire frame fits the remaining count and
character budget; otherwise omit it and retain a content-free omission
reason/count. Never emit an empty or partially truncated memory frame. Preserve
the current stricter product limits unless a separately authorized design change
exists. Add exact-fit, one-character-over, single-entry-too-large, multi-entry,
Unicode, stable-order, and framing regressions.

## R4 — delivery manifest and condensation re-anchor

Implement a deterministic delivery-manifest domain service plus a declared
persistence adapter independent of a live OpenHands process. Preserve session
identity, context epoch, applicable principal/project/task scope, delivered
memory revision/card digest/state, expanded state, last OpenHands event, last
condensation event, and update time. Condensation must atomically advance the
epoch, invalidate prior model-visible delivery certainty, and force a fresh
bounded snapshot on the next request. Duplicate condensation and restart/replay
must be idempotent. Generic summary text must never establish delivery truth.
Add new-session, unchanged-delta, condensation, duplicate-condensation,
crash/restart, stale-epoch, and bounded-snapshot regressions.

## P1 — primary evidence only

Identify the exact supported OpenHands event schema/version and the actual raw
event discriminator(s), if any, that distinguish final user/agent semantic
messages from reasoning, system scaffolding, hook context, tool, utility, and
replayed-context traffic. Cite exact local source paths and revisions and attach
content-free raw structural receipts. Explicitly specify absence/unknown-field
behavior. Do not implement capture filtering against the synthetic
`semantic_source` field used by the candidate handler unless primary evidence
proves that exact field and value contract. If the supported interface cannot
make the distinction, return `BLOCKED_INTERFACE_GAP` with the minimum interface
amendment required.

## P2 — proposed policy, not guessed behavior

Propose one deterministic synthetic-secret policy specifying detection scope,
false-positive handling, raw retention versus redaction, classification label,
promotion/vector/retrieval eligibility, event and operational-log treatment,
deletion, and audit receipts. Use only synthetic credentials. Separate what the
current product already does from what the proposal would require. Do not
implement policy-dependent changes until the principal accepts a precise policy
and digest.

## Verification and delivery

Use disposable MongoDB/Qdrant namespaces for integration tests. Run the full
clean build plus focused regressions. Every asynchronous test path must have a
timeout, and all created resources must be cleaned up. Preserve evidence without
secret or recalled-memory bodies.

Create one receipt with exactly these sections:

1. `CONFIRMED_REMEDIATIONS` — R1–R4, changed symbols, tests, and mechanical
   before/after observations;
2. `PREREQUISITE_FINDINGS` — P1/P2 primary evidence and proposed bindings,
   clearly labeled `OBSERVED`, `COMPUTED`, or `INFERRED`;
3. `COMPATIBILITY` — old-document reads, schema/index changes, rollback, and
   cleanup proof; and
4. `IDENTITY` — branch, commit, tree, clean status, dependency digests, built
   artifact digests, exact commands, test counts, and zero-skip status.

Commit the completed prospective work on a dedicated branch, but do not push it
and do not merge it. Stop after returning the receipt, commit/tree hashes, clean
status, and an explicit statement that measured S1 was not run. The Teams
coordinator will review the work, adjudicate P1/P2 with the principal, and issue
separate authorization for any push, merge, or measured execution.
