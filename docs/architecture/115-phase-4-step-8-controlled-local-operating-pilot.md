# Phase 4 Step 8 — controlled local operating pilot

## Result

**Status:** `PASS`

**Qualification:** `TEAMS_V4_LOCAL_OPERATIONAL_RUNTIME_QUALIFIED`

Tekroo Teams v4 completed a disposable local software task through the real
Teams runtime, MongoDB, OpenHands, the installed local Qwen model, and SMA. The
operator started, inspected, paused, resumed, cancelled, and stopped the
runtime. The primary task repaired a Go defect and finished `SUCCEEDED`; a
separate in-flight task was interrupted and recorded `CANCELLED`.

## What ran

- OpenHands ran locally at `127.0.0.1:8000`.
- The model server ran locally at `127.0.0.1:8802` with exact model
  `ddalcu--Qwen3.8-27B-MLX-Serve-8bit`.
- Teams and SMA used separate disposable MongoDB databases.
- SMA used separate disposable semantic and episodic Qdrant collections.
- Both workspaces used the accepted SMA context hook, SHA-256
  `e50005e82ac6de4aa11efd552cbf1586a764dbf09fc6fd617cc462ba73b0d3d9`.

While the runtime was paused, both authorized tasks remained durable and no
OpenHands conversation existed. Resume admitted both tasks. The cancellation
probe produced an OpenHands interrupt event and no workspace changes. The
primary task changed only `calculator/calculator.go`; its existing test was not
changed, and `go test ./...` passed.

## Runtime repair made during the pilot

The pilot exposed one production runtime defect: after a bounded reconciliation
window, a worker could yield a still-running intent, but the already-open Mongo
feed observed only new inserts. The completed work could therefore remain at
`STARTED` until a process restart.

The feed now also observes an intent when it transitions back to `PENDING` and
deduplicates deliveries by intent ID plus claim epoch. A Mongo integration test
proves same-session redelivery. The pilot then continued from its durable state,
observed the already-finished OpenHands conversation, and recorded `SUCCEEDED`
without sending another prompt or repeating the task.

## Operator visibility and ownership boundary

Two task projections and two story projections were materialized. The task
views expose assignment, owner, scope, budget, latest invocation, and terminal
state; the story view exposes its task membership and state summary. Those
views were sufficient to inspect the pilot without reading raw OpenHands
transcripts.

SMA captured the two authorized user prompts into its disposable namespace with
exact OpenHands provenance. It did not own or alter task state, story state,
assignment, routing, budgets, invocation authority, or completion state.

## Verification and cleanup

The ordinary Go suite, race-enabled Go suite, Mongo and operational integration
suites, `go vet ./...`, and `git diff --check` passed. The immutable execution
evidence remains under `OUTPUT/phase-4/step-8-runtime/evidence/`.

After evidence collection, the disposable SMA process was stopped, both pilot
OpenHands conversations were deleted, and the exact pilot MongoDB databases and
Qdrant collections were removed and verified absent. The sandbox repositories
remain under `OUTPUT/` as retained local evidence.

The machine-readable receipt is
`OUTPUT/phase-4/step-8-controlled-local-operating-pilot-receipt.json`.

Production deployment, Tekroo v3 migration, and production or historical data
access remain `NOT_RUN`.
