# Phase 4 Step 3 — Kernel termination and invocation admission

Date: 2026-08-31
Status: implementation candidate complete
Starting commit: `6a4a262461e967498e876090079c05a0cbb8ce1d`
Contract manifest: `c7eb4baae3a8312e44f9946fabde5a9cddb1937c7a41eb02d9b014ef21027ad1`

## Outcome

Phase 4 Step 3 implements the accepted `tekroo.kernel.contracts/0.8.0`
termination and invocation-admission model in the Go kernel, application path,
and durable stores.

The executable unit is now a single-use `work-invocation`. Ordinary task,
handoff, review, repair, replan, promotion, escalation, dispatch, actor,
provider, channel, and SMA messages remain domain events; they do not create an
executable outbox item. Only an accepted
`tekroo.command.work-invocation.authorize` emits
`WORK_INVOCATION_AUTHORIZED`.

## Implemented behavior

- A root work-budget account owns the finite graph-wide model-invocation limit,
  per-purpose limits, consumption counters, lifecycle epoch, policy identity,
  and deadline.
- Each task binds to that same budget account and receives limits that can only
  reduce the root capacity. Rebinding cannot replace the root account, increase
  the root limit, or erase already consumed global or per-purpose capacity.
- Invocation authorization checks the exact task revision, budget revision,
  lifecycle epoch, scope revision, owner, actor execution and fencing epoch,
  qualified assignment, model profile, runtime identity, workspace, policy,
  deadline, purpose budget, and condition digest.
- Authorization atomically debits the root counter, task counter, root-purpose
  counter, and task-purpose counter while creating the invocation record.
- A stale concurrent authorization cannot consume the same budget unit twice.
- A permit is claimable once. Start, terminal, cancellation-request, and expiry
  transitions are explicit and revision guarded. A cancellation request is not
  itself a terminal outcome.
- Reuse of an unchanged condition is rejected unless it is an exact retry of
  the latest retryable terminal invocation. The retry ordinal and predecessor
  identity are both bound.
- Child-task creation, handoff, replan, review, repair, promotion, actor
  replacement, model replacement, and process restart cannot mint a new root
  budget.
- The in-memory and Mongo stores persist budget accounts, task budget bindings,
  operational scopes, and invocation state. Mongo applies the debit and
  invocation insert in the existing transaction boundary, so write conflicts
  fail closed.

## Verification

OBSERVED:

- Frozen manifest SHA-256:
  `c7eb4baae3a8312e44f9946fabde5a9cddb1937c7a41eb02d9b014ef21027ad1`.
- Contract structure runner: `PASS 3604`.
- Contract reference runner: `PASS 297`.
- `go test -count=1 ./...`: PASS for every package.
- `go test -race -count=1 ./kernel ./adapters/memory ./application`: PASS.
- `go vet ./...`: PASS.
- The focused memory-store test creates a budget, binds it to a task,
  atomically authorizes/debits one invocation, reads back both projections,
  rejects a stale competing debit, and confirms usage remains exactly one.

COMPUTED from the property test:

- For every root limit from 1 through 32 and every initial consumption value
  from zero through the limit, the number of further accepted changed-condition
  invocations equals `limit - used` and the next authorization is rejected.

The mutation tests also reject missing/stale budget state, stale task/lifecycle/
scope identity, wrong actor/execution/fence/model/runtime/workspace identity,
root exhaustion, purpose exhaustion, invalid unchanged-condition retries, and
all named budget-reset mechanisms.

## Boundary of this step

This step does not implement the task/story read projections assigned to Phase
4 Step 4. It also does not connect OpenHands or start operational work; that is
Phase 4 Step 5. No services were started and no production or historical data
was accessed.

## Recommendation

Accept Phase 4 Step 3 as the kernel termination and invocation-admission
implementation, then proceed to Phase 4 Step 4 task/story projections.
