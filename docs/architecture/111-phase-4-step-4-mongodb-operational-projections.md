# Phase 4 Step 4 — MongoDB task and story operational projections

Date: 2026-08-31
Status: implementation candidate complete
Starting commit: `6a4a262461e967498e876090079c05a0cbb8ce1d`
Contract manifest: `c7eb4baae3a8312e44f9946fabde5a9cddb1937c7a41eb02d9b014ef21027ad1`

## Outcome

Phase 4 Step 4 implements the accepted task/story operational read models in
the Teams Mongo adapter. The authoritative event ledger, aggregate snapshots,
and specialized Teams records remain unchanged as the sources of truth. No
kernel decision or command admission path reads these projections.

The three canonical collections are:

- `story_projections`;
- `task_projections`; and
- `projection_checkpoints`.

`projection_faults` retains non-authoritative gap, conflict, and rebuild-
mismatch observations. Each retained observation serializes directly as the
accepted `tekroo.command.projection.record-fault` payload; it does not itself
change organizational state.

## Incremental application

The projector consumes an already committed event by exact event ID. For each
relevant source aggregate it maintains a checkpoint containing the exact last
event ID, event digest, and source revision.

- The next source revision must equal the checkpoint revision plus one.
- An exact duplicate of the current event is a no-op.
- Redelivery of an older event already present in the authoritative stream is a
  no-op.
- A missing revision records `REVISION_GAP`, does not write a projection, and
  does not advance the checkpoint.
- A duplicate/current revision with different identity or content records
  `REVISION_CONFLICT`, does not write a projection, and does not advance the
  checkpoint.
- Projection documents are written before the checkpoint within one MongoDB
  transaction. A concurrent checkpoint change produces a write conflict and
  fails closed.

Events are routed only to affected projections. Task events also refresh their
story join; budget events refresh every bound task; invocation, completion-
review, variant, escalation, and release events refresh their exact task/story
subjects. Cross-aggregate ordering is safe because each view is reconstructed
from the current authoritative aggregate and specialized Teams records while
source-stream ordering is enforced independently.

## Projection content

Story views retain definition fields, lifecycle state, dependencies, sorted
task IDs, task counts by phase and condition, completion/acceptance/release,
blocker and escalation summaries, and exact projection metadata.

Task views retain definition fields, lifecycle and ownership state, operational
scope, work-risk classification, root budget account and counters, qualified
assignment identity, latest invocation and counts by purpose/outcome,
validation/findings/escalation/completion/acceptance summaries, and exact
projection metadata.

Task views become externally materialized once the accepted work profile and
budget binding exist; intermediate task events remain checkpointed and are
incorporated when those required contract fields become available. This avoids
inventing placeholder identities that the accepted schema forbids.

Indexes support story phase, task story/phase, and task owner/phase reads.

## Clean rebuild and comparison

The rebuild path:

1. reads the complete authoritative event ledger in source-aggregate revision
   order;
2. validates every source stream from revision zero and folds every task/story
   lifecycle from its creation event;
3. reconstructs task/story projection metadata and documents;
4. writes the result to new uniquely named temporary collections;
5. reads those temporary collections back;
6. compares their canonical documents and digest with the incremental views;
7. verifies every relevant source checkpoint covers the latest exact event;
8. atomically replaces the live projection documents only on exact agreement;
   and
9. drops the temporary collections.

Any mismatch records `REBUILD_MISMATCH` and leaves the live projections
untouched.

## Verification

OBSERVED:

- Frozen contract structure runner: `PASS 3604`.
- Frozen contract reference runner: `PASS 297`.
- `go test -count=1 ./...`: PASS for every package.
- `go test -tags=mongo_integration -count=1 ./adapters/mongo`: PASS against a
  disposable real MongoDB replica set.
- `go test -race -count=1 ./adapters/mongo`: PASS.
- `go test -race -tags=mongo_integration -count=1 ./adapters/mongo -run
  TestOperationalProjection`: PASS.
- `go vet ./...`: PASS.
- `git diff --check`: PASS.

The real-Mongo tests demonstrate initial projection, exact duplicate and older
redelivery no-ops, restart-stable checkpoints, exact clean-rebuild equality,
atomic replacement after equality, retained revision-gap evidence, mismatch
refusal, and the inability of a corrupted read projection to affect an
authoritative snapshot.

## Boundary of this step

This step does not start a projector service or deploy the collections. It does
not connect OpenHands or consume executable work invocations; that remains
Phase 4 Step 5. No SMA code, production database, or historical data was read
or modified.

## Recommendation

Accept Phase 4 Step 4 as the Teams MongoDB task/story projection
implementation, then authorize Phase 4 Step 5 operational execution
coordination.
