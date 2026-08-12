# Phase 3 Step 13 — isolated deterministic capstone

## Decision

Step 13 implements the next item in the accepted Phase 1B bootstrap sequence:
an isolated fixture and layered qualification harness. It composes already
accepted boundaries rather than adding a new organizational contract.

The capstone starts from a deterministic completed-story fixture and drives the
released `tekroo.kernel.contracts/0.5.0` release lifecycle through the real
application handler, MongoDB decision store, release coordinator, local bare-Git
provider, protocol gateway, and HTTP adapter.

## Scenario

The scenario performs these observable transitions:

1. A policy command creates a release plan tied to a completed story, its
   release-approval event, exact story revision, and evidence.
2. A service command records qualification against an isolated repository's
   exact base, planned head, Git version, and resulting tree.
3. The release coordinator commits `execution-requested` before invoking the
   provider. A provider wrapper reads MongoDB during `Merge` and requires the
   matching active attempt to be durably `EXECUTING`.
4. The local provider advances only the test-owned bare repository's `main` ref
   to the exact planned head. The coordinator then records the authoritative
   provider result.
5. An HTTP acceptance request made before release finalization is durably
   rejected without changing the story.
6. The release is finalized against the exact qualification and result-event
   vector. The same HTTP path then accepts the story.
7. Replaying the acceptance request returns the original receipt and event.
8. After closing and reopening the Mongo store, the story remains `ACCEPTED`,
   the release remains `READY_FOR_ACCEPTANCE`, the tree and finalization event
   remain exact, and event counts show no replay duplication.

## Isolation and authority boundary

The Git repository and MongoDB database are newly created test fixtures. The
repository URI is local `file://`; the Git adapter rejects network schemes and
is constrained to the fixture root. The MongoDB profile is a fresh local
single-node replica set.

This step does not mutate an existing repository, call a forge API, use
credentials, migrate historical Tekroo v3 data, deploy production services,
measure production performance, or execute the separately gated OpenHands and
SMA investigations. The released contract package is unchanged.

## Layered gate

The reproducible Step 13 gate runs:

- the released contract structure and reference runners;
- the focused capstone under Go's race detector;
- the complete MongoDB replica-set integration suite under the race detector;
- the full repository regression suite under the race detector; and
- `go vet ./...`.

Each raw JSON test stream is retained and content-addressed by the gate report.
