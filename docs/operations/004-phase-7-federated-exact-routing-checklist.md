# Phase 7 federated exact-routing checklist

This checklist executes
`docs/architecture/122-phase-7-federated-exact-routing-plan.md`.

## 1. Scope and contract

- [x] Inspect the exact v3 implementation, archaeology decisions, Phase 6
  ledger, and current v4 product path.
- [x] Record the threat model and explicit non-goals.
- [x] Generate immutable contract `0.10.0` from `0.9.0`.
- [x] Validate contract manifest, schemas, fixtures, invariants, compatibility,
  and traceability.
- [x] Update the runtime contract identity and governing repository guidance.

## 2. Federation domain

- [x] Exact alias and revision validation.
- [x] Exact trust grant, key epoch, route, actor, type, and purpose validation.
- [x] Canonical signed material and Ed25519 signing/verification.
- [x] Issued-at, expiry, future-skew, payload-digest, destination, and route-trace
  enforcement.
- [x] Replay-safe ingress with retained duplicate receipt.
- [x] Prove ingress has no execution, role-launch, or SMA dependency.

## 3. Persistence and transport

- [x] In-memory store for deterministic tests.
- [x] File-backed exact alias/trust/route registry plus MongoDB outbound records,
  replay reservations, receipts, and atomic message append.
- [x] Required unique and query indexes.
- [x] Dedicated bounded ingress handler.
- [x] HTTPS-only production outbound client; explicit loopback-only test mode.
- [x] Timeout and uncertain-result reconciliation.

## 4. Product surface

- [x] File-backed exact federation configuration.
- [x] Dedicated daemon listener, separate from operator/MCP.
- [x] Operator HTTP routes for inspect, resolve, and send.
- [x] Focused MCP tools for inspect, resolve, and send.
- [x] `tekroo` commands for inspect, resolve, and send.
- [x] No direct MongoDB administration in CLI or MCP.

## 5. Qualification

- [x] Unit tests for all validation and cryptographic boundaries.
- [x] Negative tests for unknown/revoked/stale/future/replayed/mutated/mismatched
  input, wildcards, insecure production routes, and route loops.
- [x] Mongo transaction and duplicate-delivery integration tests.
- [x] Two-deployment localhost end-to-end test through supported daemon wiring.
- [x] Verify message receipt starts no role/model and preserves DAG/budget
  lineage.
- [x] Run `go test ./... -count=1`.
- [x] Run `go test -tags=mongo_integration ./... -count=1`.
- [x] Update F041–F043 in a preservation-ledger delta without modifying the
  accepted Phase 6 ledger.
- [x] Publish Phase 7 acceptance record and machine receipt.
- [x] Commit the completed phase without staging unrelated files.
