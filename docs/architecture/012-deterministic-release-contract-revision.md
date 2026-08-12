# Deterministic release contract revision

## Decision

`tekroo.kernel.contracts/0.5.0` is a separately sealed successor package. It
does not modify `0.4.0`. The wire schema is `1.4.0` and the catalogue revision
is `5`.

The revision adds an explicit story release-approval command/event and a
dedicated `release-plan` aggregate with six command/event pairs for creation,
qualification, execution request, result recording, provider reconciliation,
and finalization. These are organizational records. They do not perform Git or
provider I/O.

## Frozen plan and qualification

A code release plan binds the exact author-approval principal, event, and
revision; repository; base ref and commit; ordered changes and heads; merge
strategy; Git version; conflict policy; contract manifest; required profiles;
synthesized tree; execution-round limit; evidence; and canonical plan digest.
Qualification records the exact base, ordered heads, tree, gate definition,
toolchain, dependency lock, artifacts, and evidence.

Changing the plan, base, head, order, lock, generated artifact, or expected tree
invalidates prior qualification. Conflict handling is `FAIL_NO_IMPROVISATION`;
an executor cannot silently reorder or repair a release plan.

## Durable intent and uncertainty

Every external merge attempt has a durable execution-request event and a
provider idempotency key before the effect is attempted. Its result is recorded
as `MERGED`, `ALREADY_MERGED`, `FAILED`, or `UNKNOWN`.

`UNKNOWN` is not success or failure. It carries no invented provider commit or
tree observation, prohibits retry, and can advance only through an exact
reconciliation record for the same attempt that supersedes the uncertain
result event. Retry rounds are finite and must advance exactly. Duplicate
requests replay one durable idempotency decision, while concurrent distinct
attempts conflict with the active attempt. Partial progress remains explicit;
it implies neither rollback nor acceptance.

## Acceptance boundary

A code release reaches `READY_FOR_ACCEPTANCE` only after every ordered merge is
verified and the provider tree equals the qualified synthesized tree. Story
acceptance in schema `1.4.0` binds the exact finalized release-plan identity,
revision, finalization event, and qualified tree.

Non-code stories use the explicit `NO_RELEASE_REQUIRED` path and still bind a
finalized release-plan record. Evidence identifiers do not substitute for that
aggregate or its ordered transitions.

## Compatibility

All unchanged `1.3.0` semantic types migrate identically to `1.4.0`. The two
story-acceptance types require explicit release context and therefore have no
identity transform. Release-plan types are additive and have no `1.3.0`
representation. Readers retain `0.4.0` for historical replay.

No adapter may infer a release plan or verified provider-tree equality from a
historical story-acceptance record.

## Qualification boundary

The Step 10 contract-revision gate qualifies package structure, independent
reference fixtures, the provider-neutral pure release transition model, exact
`oneOf` validation in the Go catalogue reader, and forward compatibility of
the accepted Step 9 implementation.

It does not qualify a release coordinator, Git/provider access, MongoDB release
projection, synthesized-merge execution, deployment, migration, or production
use. Until those implementation layers are separately authorized and
qualified, the Go evaluator rejects all authoritative release commands and the
revised story-acceptance command with the stable no-effect reason
`RELEASE_NOT_IMPLEMENTED`.
