# Go kernel bootstrap decision

**Date:** 2026-08-11

**Status:** AUTHORIZED

## Observed principal decision

When asked to choose the implementation language before the pure deterministic
kernel slice, the principal stated: `Yes, let's stay in Go. You can proceed.`

## Decision

- The Tekroo v4 implementation language is Go.
- The initial module identity is `github.com/tekroo-ai/teams`.
- The bootstrap pins the exact locally verified Go toolchain in `go.mod`.
- The first slice uses only the Go standard library.
- The pure evaluator receives all time, IDs, snapshots, catalogue state, policy,
  and evidence context explicitly; it performs no database, network, filesystem,
  process, model, provider, or wall-clock I/O.
- MongoDB, OpenHands, SMA, HTTP/MCP, CLI, channels, and Git-provider behavior
  remain outside this slice.

## Implemented bootstrap surfaces

- typed FQN, UUIDv7, digest, aggregate, authority, execution, DAG, command, event,
  receipt, state, and outcome values;
- deterministic story/task lifecycle, DAG, identity, and idempotency models;
- frozen catalogue loading and payload validation;
- a pure catalogue-driven command evaluator with stable rejection outcomes;
- an atomic in-memory state/event/receipt adapter;
- deterministic fake clock, ID source, and execution engine; and
- execution of all 65 frozen contract fixtures in Go.

## Qualification boundary

Passing the 65 fixtures proves that this bootstrap agrees with the currently
frozen example/reference corpus for the exercised semantics. It does not yet
complete the full `core-hermetic` profile: generated property histories,
systematic schedules, mutation evidence, complete transition/authority policy,
machine report validation, and all invariant families still require later work.
MongoDB, synthesized-merge, provider E2E, performance, migration, and production
profiles remain `NOT_RUN`.
