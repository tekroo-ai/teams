# Tekroo v4

Tekroo v4 is a contract-first reimplementation of the Tekroo organizational
kernel. It is not a port of the Tekroo v3 codebase.

## Current state

This repository is at the implementation-bootstrap boundary. It contains the
principal-approved, content-addressed kernel contract release
`tekroo.kernel.contracts/0.1.0` and the exact architecture and gate records that
authorize it. No runtime implementation is qualified by this bootstrap commit.

The frozen contract identity is:

```text
tekroo.kernel.contracts/0.1.0
manifest SHA-256: db3f38d4794a6993aad0b9ddf6d8f093eebf36b484d99de42dddc21bf3877c2f
```

Accepted contract files are immutable. Any normative change requires a new
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

## Contract validation

The checked-in reference tools require Node.js and write their reports only to
the paths supplied by the caller:

```sh
node CONTRACTS/tekroo.kernel.contracts/0.1.0/runner/validate-package.mjs /tmp/tekroo-contract-structure.json
node CONTRACTS/tekroo.kernel.contracts/0.1.0/runner/reference-runner.mjs /tmp/tekroo-reference-corpus.json
```

These checks validate the contract structure and reference corpus. They do not
qualify a Tekroo implementation, MongoDB deployment, provider, migration, or
production system.

## Authoritative records

- [Final architecture handoff](PHASE-1B/008-final-architecture-handoff.md)
- [Kernel contract freeze](PHASE-2/001-kernel-contract-freeze.md)
- [Accepted contract package](CONTRACTS/tekroo.kernel.contracts/0.1.0/manifest.json)
- [Phase 2 Step 1 principal gate](OUTPUT/phase-2/step-1-gate.json)
- [Repository bootstrap authority](docs/architecture/000-bootstrap-authority.md)
