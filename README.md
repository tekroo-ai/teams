# Tekroo v4

Tekroo v4 is a contract-first reimplementation of the Tekroo organizational
kernel. It is not a port of the Tekroo v3 codebase.

## Current state

This repository is implementing the contract-bound kernel and MongoDB adapter in Go. It
contains the principal-authorized, content-addressed kernel contract release
`tekroo.kernel.contracts/0.2.0`, the preserved `0.1.0` release, their exact
compatibility and gate records, and a
standard-library-only deterministic kernel foundation. The MongoDB adapter is
qualified only against the pinned local integration topology; no production
runtime or deployment is qualified by this slice.

**COMPUTED GATE RESULT:** The Phase 2 Step 2 `core-hermetic` profile is `PASS`.
The executable evidence covers all 26 frozen commands, lifecycle and DAG
properties, exact execution fencing, semantic idempotency, in-memory atomic fault
schedules, event folding, unknown-event quarantine, and content-addressed
provenance/evidence primitives. Authorization/delegation, exact internal
multi-aggregate guards, lifecycle decision gates, durable attempt budgets, and
evidence access/redaction/deletion/audit rebuild are also executable. Fourteen
disposable-copy implementation mutants across all core-hermetic invariant
families are detected. MongoDB is reported separately below; synthesized-merge
remains `NOT_RUN`, and optional provider E2E also remains `NOT_RUN`. The four
previously reported `0.1.0` encoding gaps are resolved by the
authorized `0.2.0` contract; `1.0.0` commands lacking exact context require an
explicit migration and are not silently upgraded.

**COMPUTED GATE RESULT:** The Phase 2 Step 3 `mongo-integration` profile is
`PASS` for MongoDB 8.3.4 on the tested local replica-set topology with official
Go driver v2.8.0. Its race-enabled raw receipt contains 17 top-level tests and 5
subtests with zero failures. The suite executes majority transactions, five
all-or-none fault boundaries, lost-ack reconciliation, 32-writer contention,
event-fold corruption detection, stream-before-backlog delivery, 24-way claim
contention, epoch/lease fencing, finite sweep/dead-letter behavior, durable
resume checkpoints, readdress isolation, application restart, abrupt MongoDB
crash/recovery, and differential state against the accepted in-memory reference.
This does not qualify an untested production cluster, migration,
`synthesized-merge`, provider E2E, performance, or deployment.

**OBSERVED RELEASE RECEIPT:** Phase 2 Step 4 qualified candidate commit
`e19e3fc647d20e5c487b04bbd08c80d8d9289a6d` with tree
`2f5460d4a35d6f6a83de6735dd3b81f8e76917b6`; that exact commit is now
`origin/main`. The receipt does not extend qualification beyond the recorded
synthesized-merge profile.

**COMPUTED GATE RESULT:** Phase 3 Step 1 `thin-adapter-bootstrap` is `PASS`.
Its raw race-enabled receipts contain 23 focused adapter test cases across 4
packages and 183 full-regression test cases across 10 packages, with zero test
or vet failures. The slice adds a provider-neutral typed command gateway,
strict JSON and newline-delimited stdio framing, a cancellation-aware daemon
host, and an injectable CLI runner. Its gate distinguishes known no effect
before dispatch from uncertain outcomes after dispatch, and verifies that the
production thin adapters do not import persistence. HTTP/MCP, channel
transports, OpenHands, SMA, provider E2E, deployment, migration, and performance
remain unqualified.

**COMPUTED GATE RESULT:** Phase 3 Step 2 `http-mcp-adapter` is `PASS`.
Its raw race-enabled receipts contain 53 focused HTTP/MCP test cases across 3
packages and 218 full-regression test cases across 12 packages, with zero test
or vet failures. The MCP adapter targets the current official `2026-07-28`
stateless protocol: every POST supplies body metadata and matching protocol,
method, and tool-name headers. The server derives organizational identity from
trusted authentication rather than MCP client metadata or command JSON.
Request-scoped SSE, subscriptions, multi-round-trip requests, legacy MCP
sessions, public deployment, and performance remain unqualified.

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
substrate. The adapter keeps transaction, index, change-stream, claim, lease,
resume, readdress, and dead-letter mechanics outside the pure kernel.
Execution providers and semantic memory remain replaceable behind explicit
ports. OpenHands and SMA adoption require separate qualification and authority.

## Bootstrap sequence

1. Preserve and validate the frozen contract package.
2. Implement a pure deterministic kernel with in-memory repositories, fake
   clock and ID sources, and a deterministic fake execution engine.
3. Qualify that implementation against the normative fixtures and invariants.
4. Qualify MongoDB transactions, indexes, change streams, claims, and recovery.
5. Establish the canonical build and synthesized-merge gate.
6. Add external adapters only in separately qualified slices.

## Go implementation

The Go module is pinned to the toolchain recorded in `go.mod`. The initial
packages are deliberately narrow:

- `kernel` — provider-neutral values, envelopes, lifecycle/DAG/idempotency
  models, pure evaluation, decisions, and semantic ports;
- `application` — receipt-first orchestration of snapshot load, pure evaluation,
  and one atomic decision commit;
- `contract` — read-only loading and payload validation for the frozen catalogue;
- `adapters/memory` — atomic in-memory state/event/receipt storage;
- `adapters/mongo` — majority-transaction persistence and change-stream outbox
  delivery with epoch-fenced claims; and
- `adapters/fake` — deterministic clock, ID source, and execution engine;
- `adapters/protocol` — authenticated transport DTOs and the typed command
  gateway;
- `adapters/httpapi` — strict authenticated HTTP command invocation;
- `adapters/mcp` — stateless MCP `2026-07-28` Streamable HTTP tool discovery
  and command invocation;
- `adapters/stdio` and `adapters/daemon` — bounded framing and host lifecycle;
  and
- `adapters/cli` — an injectable command/stdio runner with stable exit codes.

`internal/conformance` runs the Go implementation against all 72 frozen fixtures.
That is corpus coverage, not full `core-hermetic`, MongoDB, synthesized-merge,
provider, performance, or production qualification.

The Step 3 evidence runner starts disposable MongoDB processes, including a
standalone negative control, replica-set integration topology, and isolated
crash/recovery topology. It writes and verifies a self-digesting report plus the
raw Go test JSONL receipt:

```sh
go run ./cmd/mongo-integration-report
go run ./cmd/mongo-integration-report -verify OUTPUT/phase-2/step-3-mongo-integration-gate.json
```

The Phase 3 Step 1 runner preserves raw race-enabled adapter and full-regression
test receipts, runs `go vet`, and writes a self-verifying gate report:

```sh
go run ./cmd/thin-adapter-report
go run ./cmd/thin-adapter-report -verify OUTPUT/phase-3/step-1-thin-adapter-gate.json
```

The Phase 3 Step 2 runner preserves raw HTTP/MCP and full-regression receipts,
runs `go vet`, and binds the report to the official MCP wire revision:

```sh
go run ./cmd/http-mcp-report
go run ./cmd/http-mcp-report -verify OUTPUT/phase-3/step-2-http-mcp-gate.json
```

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
- [Phase 2 Step 2 principal acceptance](OUTPUT/phase-2/step-2-acceptance.json)
- [Phase 2 Step 3 Mongo integration gate](OUTPUT/phase-2/step-3-mongo-integration-gate.json)
- [Phase 2 Step 4 synthesized-merge gate](OUTPUT/phase-2/step-4-synthesized-merge-gate.json)
- [Phase 2 Step 4 release receipt](OUTPUT/phase-2/step-4-release-receipt.json)
- [Phase 3 Step 1 authority](OUTPUT/phase-3/step-1-authorization.json)
- [Phase 3 thin-adapter bootstrap boundary](docs/architecture/002-thin-adapter-bootstrap.md)
- [Phase 3 Step 1 thin-adapter gate](OUTPUT/phase-3/step-1-thin-adapter-gate.json)
- [Phase 3 Step 1 acceptance](OUTPUT/phase-3/step-1-acceptance.json)
- [Phase 3 Step 1 release receipt](OUTPUT/phase-3/step-1-release-receipt.json)
- [Phase 3 Step 2 authority](OUTPUT/phase-3/step-2-authorization.json)
- [Phase 3 HTTP/MCP boundary](docs/architecture/003-http-mcp-adapter.md)
- [Phase 3 Step 2 HTTP/MCP gate](OUTPUT/phase-3/step-2-http-mcp-gate.json)
- [Historical Step 2 `0.1.0` encoding-gap record](OUTPUT/phase-2/step-2-contract-encoding-gaps.md)
