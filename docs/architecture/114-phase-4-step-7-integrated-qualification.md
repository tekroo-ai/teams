# Phase 4 Step 7 — integrated qualification

## Result

**Status:** `PASS`

**Runtime identity:**
`32beef3756531111fa3d4fa8fcc986fe2b6423fc3430ce5d4f49e9a133156018`

The retained preregistered suite passed all ten Phase 4 integration scenarios.
The qualification used the production Teams kernel, Mongo store, task/story
projectors, operational coordinator, worker, OpenHands HTTP client, execution
evidence recorder, and exact accepted Phase 3 Step 15 profile binding. External
OpenHands behavior was supplied below the real HTTP client by a deterministic
local protocol server; no live model or SMA execution was repeated.

## Production assembly completed in this step

Step 7 first closed the assembly gap identified after Step 6:

- `adapters/operationalruntime.Runtime` now constructs one runnable dependency
  graph from the Mongo store through the kernel handler, invocation feed and
  lease, OpenHands client, immutable evidence recorder, and durable execution
  coordinator.
- `SystemClock` and a cryptographically random UUIDv7 source provide the missing
  production clock and identity ports.
- the OpenHands adapter now constructs the exact accepted
  SMA/OpenHands/ddalcu profile through one production function instead of
  requiring callers to reproduce its settings;
- runtime construction rejects a worker/coordinator consumer-identity mismatch;
  and
- the execution worker now runs a bounded number of independent invocations
  concurrently instead of serializing the whole team.

## Integrated observations

- Two independent tasks used different story, task, actor, execution, workspace,
  worktree, budget, invocation, conversation, and projection identities.
- Both model-backed invocations overlapped, reached `SUCCEEDED`, retained exact
  evidence, and consumed exactly one unit from their own budgets.
- The tested finite graph had a computed remaining invocation ceiling of `6`.
  Attempts to continue after exhaustion terminate admission with
  `BUDGET_EXHAUSTED`; role, message-type, handoff, restart, promotion, repair,
  and replanning changes cannot mint another budget.
- DAG joins, changed-evidence continuation, bounded review/repair, restart
  recovery, duplicate delivery, stale authority, provider timeout,
  cancellation, incremental projection, and clean projection rebuild all
  passed.
- Missing or hostile semantic context remains evidence only. It cannot change
  task/story state, assignment, ownership, routing, budget, invocation
  authority, completion, acceptance, release, or database ownership.

## Retained evidence

- Preregistration:
  `OUTPUT/phase-4/step-7-integrated-qualification-preregistration.json`
- Machine receipt:
  `OUTPUT/phase-4/step-7-integrated-qualification-receipt.json`
- Raw command and test receipts:
  `OUTPUT/phase-4/step-7-raw/`

The receipt file SHA-256 is
`f7e1e24475b389412943353c0f625e592a3f92496b73bf4130866cd53f010041`.
Its embedded canonical receipt identity is
`0b16c702e54a93d9f8e67be0e976bea522c9810cb64c5bac81d53e1605cf4fe4`.

## Additional regression checks

After the retained suite passed, the complete ordinary Go test suite, complete
race-enabled Go test suite, full Mongo integration packages for the Mongo and
operational-runtime adapters, `go vet ./...`, and `git diff --check` all passed.

## Authority boundary

The suite used only disposable local Mongo state and a deterministic local
OpenHands protocol server. Deployment, historical or production data access,
v3 mutation, SMA lifecycle changes, and live model calls remain `NOT_RUN`.

Phase 4 Step 7 is complete. Step 8—the controlled local operating pilot—still
requires separate authorization.
