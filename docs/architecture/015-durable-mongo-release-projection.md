# Durable MongoDB release-plan projection

## Decision

Step 12 removes the temporary MongoDB release-plan fail-closed fence and
persists the already-released `tekroo.kernel.contracts/0.5.0` release state
machine. It does not revise the contract.

The event log remains authoritative. Each transaction writes the aggregate
revision, event, release projection, semantic-key binding, receipt, authority,
provenance, and outbox together. A failed projection transition therefore
aborts the entire decision.

## Durable documents

- `release_plans` is keyed by the qualified release-plan aggregate reference.
  Its JSON data contains that reference and the exact `ReleasePlanSnapshot`.
- `release_plan_keys` is keyed by the SHA-256 digest of the canonical JSON
  `ReleasePlanKey` (`story` plus lifecycle epoch). Its JSON data binds the key
  to exactly one release-plan aggregate.

MongoDB `_id` uniqueness and the transaction's absent-key guard make concurrent
creation deterministic: one transaction can establish a story-lifecycle
binding and competing transactions conflict. No terminal key is deleted, so a
later plan cannot silently replace the lifecycle's release evidence.

## Transition boundary

Creation reconstructs the projection with `ReleasePlanFromCreatePayload` and
the committed opening event identifier. Every later released event uses
`ApplyReleaseEvent` against the durable prior projection. The stored revision
must be exactly one less than the event revision, and the transition's computed
revision must equal the event revision. Any parse, identity, revision, result
vector, reconciliation, or finalization mismatch fails the transaction.

`LoadDecision` reconstructs both release maps for every evaluation snapshot.
This allows a story acceptance decision to resolve the finalized release plan
and semantic binding even though the story and release plan are distinct
aggregates.

## Qualification boundary

The Step 12 profile uses a fresh test-owned MongoDB replica set and proves:

- create, qualification, execution request, ambiguous result, authoritative
  reconciliation, and finalization persist as six ordered events;
- closing and reopening the store reloads the exact projection and semantic
  binding;
- an invalid final result-event vector aborts without advancing the revision;
- a transition evaluated from a stale release revision conflicts without an
  additional event;
- two simultaneous plans for one story lifecycle produce one success and one
  conflict;
- the finalized projection is visible when loading the story acceptance
  snapshot; and
- malformed or contradictory durable release documents fail closed during
  snapshot reconstruction; and
- focused and full Go race profiles and `go vet` pass.

This slice does not call a Git provider, touch an external repository, migrate
historical data, deploy production services, or change OpenHands or SMA.
