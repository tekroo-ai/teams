# Read-only event-export boundary acceptance

Date: 2026-08-28  
Decision: **ACCEPTED**  
Deployment: **NOT_RUN**  
SMA client qualification: **NOT_RUN**  
Live-shadow qualification: **NOT_RUN**

## Accepted identity

**OBSERVED:** The bounded review began from Teams commit
`ec28b10e1962ae5d6765a27f613ca89d0237a986`, tree
`17f1dfeb27f4e8e7253b35fae7bd07d8d17f0bb2`. The main checkout contained
unrelated untracked roots, so review artifacts were created on the isolated
`codex/event-export-acceptance` branch from that exact commit.

**OBSERVED:** The event-export candidate manifest SHA-256 is
`4aa87278b6812f967331bef98b2c7e63585b6742b39d0b64c0ff693632aa9731`.
Its accepted source is `tekroo.kernel.contracts/0.7.0`, manifest SHA-256
`e2b9b5224a860a3eaa07451cf48fb5ac16a440b22b8dd592ff1662c1cff67f16`.
Neither contract package was modified.

The machine-readable acceptance receipt is
`OUTPUT/phase-3/event-export-boundary-acceptance.json`, SHA-256
`48298fee9e784d46b76adff869ef30ca93df092dcb1cc921aeb25b274069d70c`.
The historical implementation receipt remains unchanged at SHA-256
`011113ce369eda8001df0b9b1b324a76d5f487f89bc5bd0fa99e07a7eb87d147`.

## Verification

**OBSERVED:** Every required gate passed:

| Gate | Result |
|---|---|
| Contract package validator | PASS — 6 files, 12 invariants, 10 fixtures |
| `go test ./...` | PASS — 18 packages; 194 top-level tests and 560 test/subtest terminals; zero failures or skips |
| `go vet ./...` | PASS — zero diagnostics |
| Targeted race profile | PASS — 5 packages; 30 top-level tests and 35 test/subtest terminals; zero failures or skips |
| Mongo integration profile | PASS — 30 top-level tests and 35 test/subtest terminals, including all 4 event-export integration tests; zero failures or skips |

**OBSERVED:** Source inspection and disposable Mongo evidence establish exact
persisted event bytes with byte length and SHA-256, `event_id` deduplication,
at-least-once delivery, HMAC-protected source/principal/TTL-bound cursors,
live-stream establishment before bounded backlog, explicit
`RESYNC_REQUIRED`, unchanged raw outbox BSON, zero export checkpoints, and an
unchanged collection inventory through the read-only connector.

**OBSERVED:** The HTTP composition is `POST /v1/event-export`, protocol
`1.0.0`, with bearer authentication, exact principal/source authorization,
fail-closed request and cursor validation, a 32 KiB request limit, at most
1,000 records, at most 30 seconds of wait, fixed-window rate limiting, bounded
server startup/read/write/idle/shutdown behavior, and explicit loopback-only
binding. Telemetry types contain status, source, principal, count, duration,
and cursor digest only; bearer and cursor secrets are not telemetry fields.

## Accepted boundary

The candidate is accepted as the supported read-only source boundary from
Teams to a future SMA Teams-v4 integration. It conveys exact immutable event
evidence only. It grants SMA no organizational, workflow, acceptance,
continuity-control, process-control, or memory authority. Cursor and insertion
position are operational state and must not be interpreted as organizational
order or causality.

## Exclusions and residual risks

- Delivery remains deliberately at-least-once; the SMA client must deduplicate
  by `event_id`.
- Cursor expiry, signing-key rotation, or lost change-stream history requires
  explicit resynchronization and bounded replay.
- The runnable composition uses loopback-only bearer authentication without
  TLS and must not be exposed remotely.
- Response size is bounded by record count, MongoDB persisted-document limits,
  and HTTP write timeout; there is no separate aggregate response-byte cap.
- SMA cursor persistence, deduplication, resynchronization, deployment, and
  cross-repository live behavior remain unqualified.

This acceptance authorizes no deployment, server start, SMA client work,
live-shadow run, Step 15 execution, SMA-Q1 execution, or WP6 work.
