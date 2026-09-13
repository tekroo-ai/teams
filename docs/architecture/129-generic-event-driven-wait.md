# Generic event-driven wait

## Decision

Teams exposes one generic, read-only wait operation for an exact kernel
aggregate. It is not specialized for any role, workflow stage, or model.

The caller supplies:

- the aggregate kind and UUID;
- the last aggregate revision it has already observed;
- an optional set of event types;
- a mandatory timeout no greater than 24 hours.

The result is either a notice identifying the first matching committed domain
event or a typed `TIMED_OUT` result. The notice contains event identity, type,
aggregate, revision, and commit time; it deliberately excludes the event
payload and authority details. The caller uses an existing authorized read
operation to inspect current state. Waiting never mutates organizational state.

## Interfaces

- Go: `ProductionService.WaitForEvent`
- HTTP: `POST /v1/events/wait`
- MCP: `tekroo.event.wait`
- CLI: `tekroo wait KIND ID --timeout DURATION [--after-revision REVISION]
  [--event-type TYPE]...`

The MCP and HTTP inputs use the same `EventWaitRequest` representation. The
operation is suitable for invocations, tasks, stories, reviews, escalations,
human interactions, and every other valid kernel aggregate kind.

The operator HTTP endpoint already authenticates only the configured operator.
The focused MCP tool additionally requires that same operator identity; a
registered advisory human cannot use the wait tool to broaden its read access.

## MongoDB behavior

The Mongo adapter opens an aggregate-filtered change stream before querying
the bounded backlog. This ordering prevents a commit between the initial read
and the subscription from being missed. The backlog query returns the lowest
matching revision after the caller's checkpoint; the live stream then returns
the first later matching insert.

No outbox item is claimed, no checkpoint is written, and no MongoDB credential
or BSON representation crosses the service boundary. If the client connection
is lost, the caller repeats the wait with the same `after_revision`; the
backlog check makes that restart safe.

## Resource and failure behavior

Ordinary operator requests retain their short server and operation deadlines.
Only the wait route clears the server-wide write deadline, because the request
itself carries a validated bounded timeout. Client cancellation closes the
change stream. Invalid filters fail before subscription; MongoDB and decoding
failures remain observable errors.

A waiting tool call consumes a server connection and MongoDB cursor but no
model tokens. The model is invoked again only when a matching event arrives,
the timeout expires, or the tool fails.

## Lineage and deployment

This capability was developed from committed Phase 9 infrastructure identity
`049fec7dc33b27cf677a085d765fa3ce991af022` in a separate worktree. Deployment
must use a binary built from the integrating commit; older qualification runs
remain historical evidence and are never mutated to claim use of this surface.
