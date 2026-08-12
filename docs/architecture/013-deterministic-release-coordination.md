# Deterministic release coordination

Status: implementation candidate authorized by the accepted `tekroo.kernel.contracts/0.5.0` contract. This document does not authorize a production release provider or a push.

## Evidence floor

The implementation makes the weakest claims supported by this slice:

- OBSERVED in durable kernel records: exact author approval, immutable ordered plan, qualification identity, provider-effect intent, provider result, reconciliation supersession, finalization, and story acceptance.
- COMPUTED by the kernel: whether each proposed transition is admissible and the next release-plan projection.
- INFERRED nowhere in the release state: provider timeout or cancellation becomes `UNKNOWN`; it is not interpreted as success or failure.

The provider adapter remains outside the kernel. The kernel adjudicates durable facts; the application coordinator sequences the effect; an adapter performs or observes the provider operation.

## Ordered effect boundary

For one planned merge, the coordinator executes this sequence:

1. Construct `tekroo.command.release-plan.request-execution` from the persisted plan's exact next merge, round, and provider idempotency key.
2. Obtain an `APPLIED` receipt for `tekroo.event.release-plan.execution-requested` before invoking the provider.
3. Invoke the provider with the persisted repository, base ref, base commit, change ref, head commit, merge strategy, attempt identity, and idempotency key.
4. Persist `tekroo.command.release-plan.record-result` as `MERGED`, `ALREADY_MERGED`, `FAILED`, or `UNKNOWN`.
5. If the result is `UNKNOWN`, prohibit another execution transition until `tekroo.command.release-plan.record-reconciliation` supersedes that exact result event.

The result command is persisted with a bounded context detached from caller cancellation. A provider timeout or cancellation therefore does not erase the durable `UNKNOWN` observation.

## Deterministic identity and replay

The caller allocates command IDs, correlation ID, idempotency keys, evidence references, and the observation timestamp before coordination. Replaying the same operation reconstructs byte-equivalent command payloads. The deterministic fake provider caches the scripted external outcome by `provider_idempotency_key`; concurrent duplicate calls observe one idempotent effect.

The command DAG is directed:

- first execution request responds to the exact qualification event;
- retry responds to the prior effective failed result;
- result responds to the execution-requested event;
- reconciliation responds to and supersedes the exact unknown result;
- acceptance responds to the exact release finalization.

No coordination transition creates a causal cycle.

## Projection invariants

The in-memory repository transactionally projects release state and fences one release plan per story lifecycle. The projection retains:

- exact story revision and lifecycle;
- author and approval event;
- plan digest, ordered merge IDs and heads, base, policies, contract, manifest, profiles, and expected tree;
- qualification event and reproducibility identities;
- next merge and bounded round;
- active attempt and provider idempotency key;
- per-merge result plus reconciliation supersession;
- final provider tree and finalization event.

Successful provider observations must name the planned base and head. Failed and unavailable observations must not carry inferred commit or tree identity. Finalization requires every ordered merge to have an effective successful result, the exact qualification event, the exact ordered effective result-event vector, and equality between the last observed provider tree and the qualified tree.

`NO_RELEASE_REQUIRED` is explicit: it has no merge plan, qualification, provider attempt, result, or provider tree, and it reaches only the contract's terminal no-release state.

## Explicit exclusions

- No GitHub, Git, forge, or other real provider adapter is included.
- No external repository, branch, pull request, worktree, or deployment is mutated.
- MongoDB release-plan projection is not claimed. The Mongo decision store explicitly returns `ErrReleaseProjectionUnsupported` for release-plan events so this profile fails closed instead of silently losing the projection.
- No production rollout, migration, load, latency, OpenHands, or SMA behavior is included.

The candidate may be pushed only after review and explicit principal acceptance.
