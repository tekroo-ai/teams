# Phase 5 — local operational productization

## Status and scope

**Status:** complete

**Completion record:** `OUTPUT/phase-5/phase-5-completion.json`

**Starting commit:** `44baba12ecda6321c79b65ea179eb22c385b26c9`

Phase 4 proved the complete Teams execution engine in a controlled local pilot.
Phase 5 turns that engine into the persistent local application an operator can
use for normal work.

Data migration is explicitly excluded. Tekroo Teams v4 starts with its own new
database and does not import or mutate Tekroo v3 data.

This phase does not redesign the accepted `0.8.0` domain contract. A contract
successor is required only if implementation discovers a genuine missing domain
command, event, or invariant. Host configuration, process lifecycle, health,
and local operator transport are application concerns, not new organizational
semantics.

## Product outcome

At phase completion, the local machine can run:

- one persistent `tekrood` service containing the accepted Phase 4 runtime;
- one `tekroo` command-line client for operator work and lifecycle control;
- authenticated localhost-only HTTP endpoints for health, status, pause,
  resume, command submission, and task/story reads;
- durable recovery after service restart and host sleep/resume;
- explicit configuration for MongoDB, OpenHands, SMA, model profiles,
  workspaces, evidence storage, budgets, timeouts, and concurrency; and
- bounded multi-task operation with useful logs and no direct agent chaining.

OpenHands remains the execution environment. SMA remains semantic memory.
Teams remains the sole authority for stories, tasks, assignments, budgets,
invocations, acceptance, and operational state.

## Engineering sequence

### Step 1 — production configuration and service assembly

Add a typed, validated configuration package and a production constructor for
the accepted runtime. Configuration must support:

- a dedicated new Teams MongoDB database;
- localhost OpenHands and an API-key file rather than an inline secret;
- exact workspace and execution-profile bindings;
- separate Teams and SMA database identities;
- evidence directory, worker concurrency, leases, reconciliation, deadlines,
  and HTTP timeouts;
- fail-closed startup on a missing secret, invalid path, duplicate binding,
  unsafe non-loopback endpoint, or inconsistent runtime identity; and
- human-readable validation errors without logging credentials.

Add `cmd/tekrood` with foreground operation, signal-aware graceful shutdown, and
structured startup/shutdown reporting. It must assemble the same production
runtime used by the accepted pilot rather than a parallel test-only path.

**Exit:** `tekrood` starts the accepted runtime from a validated local config and
stops cleanly without leaking workers, feeds, files, or secrets.

### Step 2 — localhost operator control API

Add an authenticated localhost-only control server around the runtime
controller. Provide:

- `GET /health`;
- `GET /v1/status`;
- `POST /v1/pause`;
- `POST /v1/resume`;
- `POST /v1/stop`;
- command submission through the existing contract-aware handler; and
- read-only task and story projection endpoints.

Pause gates new invocation admission but does not cancel active work. Task
cancellation remains a durable Teams command. Requests have bounded bodies and
deadlines, return stable machine-readable errors, and never expose secrets or
raw semantic-memory internals.

**Exit:** the operator can control and inspect the service without a Go test or
direct MongoDB access.

### Step 3 — `tekroo` operator client

Add a small client that reads the same secret file and supports:

- health, status, pause, resume, and stop;
- submit a validated Teams command from a file or standard input;
- inspect a task or story projection; and
- request durable cancellation of an exact work invocation.

The client must not synthesize organizational authority. Human identity,
authority, expected revision, and idempotency remain explicit command fields.

**Exit:** a human can operate Teams through stable commands with no database or
HTTP knowledge.

### Step 4 — operational installation and recovery

Add local start, stop, restart, and status scripts plus an optional macOS
LaunchAgent definition. Do not enable automatic startup without separate
operator action. Implement and test:

- restart recovery of pending and in-flight invocations;
- stale process and stale lease recovery;
- missing MongoDB and OpenHands dependency reporting, with downstream
  model/SMA-hook failures retained through the OpenHands execution result;
- host sleep/wake behavior without duplicate work; and
- clean termination during active, paused, and idle states.

**Exit:** the service can be installed and operated repeatedly without manual
cleanup or loss of durable work.

Teams does not add a direct SMA, model-server, embedding-server, or SMA-database
health probe. The accepted Phase 4 boundary makes OpenHands the integration
point and makes SMA absence fail-open for memory; a second Teams-to-SMA path
would violate that boundary.

### Step 5 — normal operator workflow

Use `tekroo` and `tekrood`, not test fixtures, to run the normal workflow:

1. create a story and task;
2. classify and scope the task;
3. bind ownership, budget, assignment, workspace, and model profile;
4. authorize one execution;
5. inspect progress through projections;
6. handle cancellation or escalation; and
7. record validation, completion, and acceptance.

Provide concise starter templates for the exact required commands. Do not add a
second workflow engine or hide required authority fields behind model prose.

**Exit:** one complete task can be operated from the supported product surface
without custom Go code.

### Step 6 — multi-task soak and release readiness

Run a bounded local soak using independent disposable repositories and the
installed local model stack. Cover concurrent tasks, pause/resume, cancellation,
dependency outage and recovery, service restart, host sleep/wake, bounded
budgets, projection correctness, SMA isolation, and absence of agent-to-agent
wakeup.

Record latency and throughput only as operational observations; there is no
benchmark-ranking requirement. Fix product defects found by the soak and retain
their regression tests.

**Exit:** the persistent service and operator client are ready for ordinary
local use. Production hosting elsewhere, remote multi-user exposure, and v3
data migration are outside this phase.

## Completion rule

Phase 5 is complete only when the supported `tekrood` + `tekroo` path—not a
test-only assembly—can execute, inspect, control, recover, and finish real local
work through the accepted Teams/OpenHands/SMA boundaries.
