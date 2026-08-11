# Phase 3 Step 1 — Thin adapter bootstrap

**Authority:** Principal statement `Accepted. Proceed to the next step.` on
2026-08-11, following release of the exact Phase 2 qualified candidate.

## Boundary

CLI, stdio, daemon, future HTTP/MCP, and future channel hosts are transport and
composition adapters over one typed command service. They do not import a
persistence adapter, write organizational state, interpret provider output as an
organizational fact, or bypass the kernel authorization path.

Every request carries an authenticated principal and, when actor-scoped, the
exact durable actor FQN plus provider-neutral execution ID and fencing epoch.
Those authenticated values must equal the command values before application
code runs. Command identity, semantic idempotency key, contract/catalogue/policy
revisions, and a valid provenance basis are mandatory.

Every invocation has an explicit bounded timeout. Cancellation observed before
service dispatch is known to have no organizational effect. Cancellation,
timeout, or a service failure after dispatch returns an invocation failure with
an uncertain organizational outcome; it does not assert rollback or command
failure. A caller reconciles that uncertainty using the stable command identity.

## Step 1 deliverable

This step implements:

1. a reusable provider-neutral command gateway;
2. strict JSON request/response DTOs at the transport boundary;
3. a newline-delimited stdio server;
4. a cancellation-aware daemon host over a closeable stdio input;
5. an injectable CLI runner; and
6. semantic endpoint contracts reusable by later HTTP/MCP and channel adapters.

Live OpenHands, SMA, paid models, provider credentials, production deployment,
and data migration remain outside this authority.
