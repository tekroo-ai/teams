# Phase 3 Step 3 — Exact-directed channel adapter

**Authority:** Principal statement `Accepted. You may push and proceed to the
next step.` on 2026-08-11 after acceptance of Phase 3 Step 2.

## Accepted evidence boundary

This slice implements the channel surface required by the accepted Phase 1B
handoff and Phase 2 contract freeze. It relies specifically on:

- `PHASE-1B/008-final-architecture-handoff.md`, sections 2–4 and 8;
- `PHASE-2/001-kernel-contract-freeze.md`, Decision 4 routing and unknown-type
  rules, Decision 5 DAG rules, Decision 6 ownership rules, and Decision 10
  MongoDB delivery rules.

Channel routing is exact by default. A canonical channel frame addresses one
durable actor FQN and one provider-neutral execution tuple. The configured
receiver must match both before application code runs. Role-class, wildcard,
fan-out, and broadcast routing are unsupported because their recipient-set,
ownership, join, timeout, and partial-delivery semantics have not been
separately approved.

The authenticated sender is injected by trusted transport composition and is
not decoded from client JSON. The underlying command still carries its stable
command ID, semantic idempotency key, exact authority and execution fencing,
provenance, and declared DAG parents. Duplicate channel delivery can repeat an
invocation but cannot bypass kernel idempotency.

Delivery, a delivery claim, and a delivery failure are transport facts. They
do not acquire, release, or transfer task ownership and do not create DAG edges.
Only the enclosed authorized kernel command can propose an organizational
transition, and only its declared accepted causation edges establish lineage.

## Unknown input and historical aliases

The sole canonical channel type in this slice is
`tekroo.channel.command/1.0.0`. An unknown channel type or unsupported version
within the configured body bound is retained byte-for-byte through a required
quarantine port with authenticated source, receive time, transport provenance,
reason, and SHA-256 digest. It is never dispatched to the application service.

There are no aliases for `tekroo-agent-chat`, `tekroo-agent-ping`, or
`tekroo-agent-pong`. Their historical frequency remains evidence, not a v4
contract or permission. Malformed canonical frames are also quarantined and
have no canonical effect.

This slice does not replace or modify MongoDB change streams, outbox claims,
claim epochs, leases, backlog recovery, readdressing, or dead letters. It is the
thin post-claim/inbound channel boundary that provider-specific channel hosts
may use later. Live providers, production composition, and performance remain
separately authorized profiles.
