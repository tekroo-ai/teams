# Read-only event-export boundary candidate

Date: 2026-08-13  
Implementation status: **PASS — candidate, not accepted or deployed**  
Production authority: **NONE**

## Outcome

Teams now has a supported, versioned, authenticated read-only event-export
surface for complete persisted `kernel.DomainEvent` records. It is governed by
the separately sealed candidate contract
`tekroo.event-export.contracts/0.1.0`, manifest SHA-256
`4aa87278b6812f967331bef98b2c7e63585b6742b39d0b64c0ff693632aa9731`.
The contract is bound to the accepted `tekroo.kernel.contracts/0.7.0` manifest
SHA-256 `e2b9b5224a860a3eaa07451cf48fb5ac16a440b22b8dd592ff1662c1cff67f16`.
Accepted package 0.7.0 was not edited.

## Boundary

- `POST /v1/event-export`, protocol version `1.0.0`
- trusted bearer authentication followed by exact principal/source
  authorization
- exact persisted event bytes as base64, with byte length and SHA-256
- parsed event identity metadata accompanies but never replaces exact bytes
- at-least-once delivery; `event_id` is the consumer deduplication identity
- opaque HMAC-protected cursor bound to source, authenticated identity, and TTL
- live MongoDB change stream opens before bounded backlog
- invalid/tampered cursors fail closed; history loss is `RESYNC_REQUIRED`
- limits: at most 1,000 records and 30 seconds per request
- content-free telemetry only

Cursor position is operational state only. It does not define event identity,
organizational order, causality, authority, acceptance, or completion.

## Read-only proof

**OBSERVED:** The Mongo implementation watches only inserts in the `events`
collection and reads the event backlog. It contains no outbox collection access,
claim, resolve, readdress, checkpoint replacement, metadata write, or index
creation.

**OBSERVED:** The replica-set integration profile compared raw outbox BSON
before and after export and found it byte-identical. It also observed zero
export-created consumer checkpoints.

**OBSERVED:** The standalone connector verifies existing kernel metadata and
opens the event source without creating collections. Its integration fixture
observed an identical collection inventory before and after opening, reading,
and closing the connector.

**OBSERVED:** A nonempty resumable change-stream token is required before
backlog is exposed. If MongoDB cannot establish that token, the source fails
closed instead of risking an observation gap.

## Runnable local composition

`cmd/event-export-server` supplies the local-only composition needed by SMA:

- explicit loopback IP binding only (default `127.0.0.1:8087`)
- existing MongoDB URI/database
- stable source ID and service-principal identity
- independent bearer and cursor HMAC secrets, each at least 32 bytes
- fixed-window principal rate limit
- bounded HTTP reads, writes, idle time, startup, shutdown, and source close

Required environment inputs are:

- `TEKROO_EVENT_EXPORT_MONGO_URI`
- `TEKROO_EVENT_EXPORT_MONGO_DATABASE`
- `TEKROO_EVENT_EXPORT_BEARER_TOKEN`
- `TEKROO_EVENT_EXPORT_CURSOR_KEY_HEX`

Optional inputs are the loopback address, source ID, and principal ID. Secrets
are configuration inputs and are not printed or persisted by the server.

## Verification

**OBSERVED PASS:**

- candidate contract validator: 6 inventoried files, 12 invariants, 10 fixtures
- `go test ./...`
- `go vet ./...`
- race profile for event-export service, memory source, HTTP adapter, Mongo source,
  and runnable server
- Mongo replica-set integration profile, including exact bytes, stream-before-
  backlog, reconnect/no omission, backlog bound, immutable outbox, zero
  checkpoints, and read-only connector inventory
- existing thin-adapter import boundary

## SMA disposition

The original SMA preflight blocker—no supported complete-event export
boundary—is resolved in the Teams implementation candidate. The SMA team may
now implement and qualify its client against this contract.

The live-shadow gate itself remains **NOT_RUN**. It must not be reported as
passing until:

1. this Teams candidate is accepted and made available to the SMA worktree;
2. the local server is started against the intended Teams MongoDB database with
   stable secrets;
3. SMA consumes exact bytes through `/v1/event-export`, persists its cursor,
   deduplicates by `event_id`, and proves `RESYNC_REQUIRED` recovery; and
4. the cross-repository live qualification passes without private BSON access
   or outbox claims.

Therefore: **GO** for candidate review and SMA adapter work; **NO-GO** for a
claim that TV4 live shadow has already passed.
