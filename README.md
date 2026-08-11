# Tekroo v4

Tekroo v4 is a contract-first reimplementation of the Tekroo organizational
kernel. It is not a port of the Tekroo v3 codebase.

## Current state

This repository is implementing the pure-kernel bootstrap in Go. It
contains the principal-authorized, content-addressed kernel contract release
`tekroo.kernel.contracts/0.2.0`, the preserved `0.1.0` release, their exact
compatibility and gate records, and a
standard-library-only deterministic kernel foundation. No runtime or production
system is qualified by this slice.

**COMPUTED GATE RESULT:** The Phase 2 Step 2 `core-hermetic` profile is `PASS`.
The executable evidence covers all 26 frozen commands, lifecycle and DAG
properties, exact execution fencing, semantic idempotency, in-memory atomic fault
schedules, event folding, unknown-event quarantine, and content-addressed
provenance/evidence primitives. Authorization/delegation, exact internal
multi-aggregate guards, lifecycle decision gates, durable attempt budgets, and
evidence access/redaction/deletion/audit rebuild are also executable. Fourteen
disposable-copy implementation mutants across all core-hermetic invariant
families are detected. MongoDB and synthesized-merge remain `NOT_RUN` in their
own required profiles; optional provider E2E also remains `NOT_RUN`. The four
previously reported `0.1.0` encoding gaps are resolved by the
authorized `0.2.0` contract; `1.0.0` commands lacking exact context require an
explicit migration and are not silently upgraded.

The frozen contract identity is:

```text
tekroo.kernel.contracts/0.2.0
manifest SHA-256: cd582fb163e17d49a1a0a627f33cdbb184d15ecace3200981738782cca977dae
```

The preserved `0.1.0` manifest SHA-256 is
`db3f38d4794a6993aad0b9ddf6d8f093eebf36b484d99de42dddc21bf3877c2f`.
Released contract files are immutable. Any normative change requires a new
contract version, compatibility analysis, a new detached manifest digest, and
the appropriate decision gate.

## Architectural boundary

The kernel owns organizational truth: durable actor identity, the versioned
catalogue, schemas, the directed acyclic work graph, stories and tasks,
ownership, authorization, validation, acceptance, completion, reopening,
escalation, release policy, and provenance.

MongoDB is the approved organizational persistence and change-stream wakeup
substrate, but it is not part of the first pure-kernel implementation slice.
Execution providers and semantic memory remain replaceable behind explicit
ports. OpenHands and SMA adoption require separate qualification and authority.

## Bootstrap sequence

1. Preserve and validate the frozen contract package.
2. Implement a pure deterministic kernel with in-memory repositories, fake
   clock and ID sources, and a deterministic fake execution engine.
3. Qualify that implementation against the normative fixtures and invariants.
4. Establish the canonical build and synthesized-merge gate.
5. Add MongoDB and external adapters only in separately qualified slices.

## Go implementation

The Go module is pinned to the toolchain recorded in `go.mod`. The initial
packages are deliberately narrow:

- `kernel` — provider-neutral values, envelopes, lifecycle/DAG/idempotency
  models, pure evaluation, decisions, and semantic ports;
- `application` — receipt-first orchestration of snapshot load, pure evaluation,
  and one atomic decision commit;
- `contract` — read-only loading and payload validation for the frozen catalogue;
- `adapters/memory` — atomic in-memory state/event/receipt storage; and
- `adapters/fake` — deterministic clock, ID source, and execution engine.

`internal/conformance` runs the Go implementation against all 72 frozen fixtures.
That is corpus coverage, not full `core-hermetic`, MongoDB, synthesized-merge,
provider, performance, or production qualification.

## Contract validation

The checked-in reference tools require Node.js and write their reports only to
the paths supplied by the caller:

```sh
node CONTRACTS/tekroo.kernel.contracts/0.2.0/runner/validate-package.mjs /tmp/tekroo-contract-structure.json
node CONTRACTS/tekroo.kernel.contracts/0.2.0/runner/reference-runner.mjs /tmp/tekroo-reference-corpus.json
```

These checks validate the contract structure and reference corpus. They do not
qualify a Tekroo implementation, MongoDB deployment, provider, migration, or
production system.

The current local verification entrypoint runs Go tests with the race detector,
Go vet, the deterministic mutation-sensitivity suite, and both frozen reference
checks:

```sh
./scripts/verify.sh
```

The Step 2 evidence runner independently executes the race suite, Go vet, and
both frozen contract runners before writing a self-digesting report:

```sh
go run ./cmd/core-hermetic-report -output build/reports/core-hermetic.json
go run ./cmd/core-hermetic-report -verify build/reports/core-hermetic.json
```

## Authoritative records

- [Final architecture handoff](PHASE-1B/008-final-architecture-handoff.md)
- [Kernel contract freeze](PHASE-2/001-kernel-contract-freeze.md)
- [Accepted contract package](CONTRACTS/tekroo.kernel.contracts/0.2.0/manifest.json)
- [0.1.0 to 0.2.0 compatibility rule](CONTRACTS/tekroo.kernel.contracts/0.2.0/compatibility/from-0.1.0.json)
- [Step 2 contract-revision authorization](OUTPUT/phase-2/step-2-contract-revision-authorization.json)
- [Phase 2 Step 1 principal gate](OUTPUT/phase-2/step-1-gate.json)
- [Repository bootstrap authority](docs/architecture/000-bootstrap-authority.md)
- [Go kernel bootstrap decision](docs/architecture/001-go-kernel-bootstrap.md)
- [Phase 2 Step 2 core-hermetic gate](OUTPUT/phase-2/step-2-core-hermetic-gate.json)
- [Historical Step 2 `0.1.0` encoding-gap record](OUTPUT/phase-2/step-2-contract-encoding-gaps.md)
