# Phase 7 integrated acceptance — federated exact routing

**Status:** PASS — Phase 7 is complete for the authorized local qualification
boundary.

**Recorded:** 2026-09-01

**Source baseline:** commit
`04b78533984091c4e5b15eaa8a9bb72b58fecea7`, tree
`89650690577dfd30e2c6fcf546279dc218c4da94`.

## Result

Tekroo Teams now supports exact, signed, replay-safe organizational messaging
between separately configured deployments. An operator-facing alias is resolved
before signing to one actor FQN, deployment, route, and revision. The alias is
retained only as provenance; it never replaces durable actor identity.

The receiving deployment verifies the Ed25519 signature, exact trust grant,
key epoch, validity window, exact route authority, payload digest, destination,
message expiry, and finite route trace before accepting the message. MongoDB
stores the replay reservation, organizational message, and retained delivery
receipt in one transaction. Repeating the identical delivery returns the
retained receipt; changing content under the same replay or delivery identity
fails closed.

Outbound intent and the fully resolved signed envelope are recorded before
network I/O. The bounded HTTP client may reconcile one uncertain result by
resending the identical delivery. Production routes require HTTPS; plain HTTP
is permitted only for an explicitly test-only loopback route.

Federation has a dedicated daemon listener separate from operator HTTP and MCP.
The supported operator HTTP, MCP, and `tekroo` surfaces can inspect configured
federation, resolve an alias, and send a federated message. None exposes direct
MongoDB administration.

## Organizational boundary

A federated message retains its existing thread, DAG node and parent, hop
budget, lifecycle epoch, scope revision, budget account, and progress digest.
Ingress can append an authorized message, but cannot launch a role or model,
create an execution lease, reset lineage or budget, or call SMA. Remote
task/story identifiers remain correlation evidence; they do not create local
execution authority.

## Verification

The final working tree passed:

- `go test ./... -count=1`
- `go test -tags=mongo_integration ./... -count=1`
- `go test -race ./organization ./adapters/federationhttp
  ./adapters/operatorhttp ./adapters/operatortools -count=1`
- `go vet ./...`
- contract `0.10.0` validation: `PASS 3642`, canonical result
  `8a40edfddb4a8bb82cb31d45f8db367ee352c56b641dc38cdfa3060b7a83e35f`

The tests include two disposable deployment coordinators connected through the
real federation HTTP client and handler, MongoDB transaction and duplicate
delivery coverage, production configuration validation, operator/MCP/CLI
coverage, and adversarial signature, authority, clock, replay, destination,
wildcard, route-loop, and insecure-route cases.

## Preservation and scope

The Phase 7 preservation delta marks `F041`, `F042`, and `F043`
`IMPLEMENTED_WIRED`, with zero implicit retirements. The accepted Phase 6 ledger
was not modified.

Contract manifest SHA-256:
`2752b876d5a71bb1367a088b9f8cc0ad5df6343b0833906c49aae5a404b8db98`.
Contract-validation receipt SHA-256:
`79af4f09f1eac838fde011c3650450cbd026515014511a0b1a908b401f307ec4`.
Preservation-delta SHA-256:
`06f6d1ec3db21fb97ee86f480b3b54288875419aa521bed43c4d58439bbb4a94`.

Public-network deployment, production deployment, cloud trust brokerage,
billing, wildcard routing, v3 data migration, SMA modification, and historical
or production data access remain `NOT_RUN`.

The machine-readable receipt is
`OUTPUT/phase-7/integrated-acceptance.json`.
