# Phase 4 Step 2 — contract 0.8.0 candidate

## Status

**Status:** `READY_FOR_PRINCIPAL_ACCEPTANCE`

**Contract candidate:** `tekroo.kernel.contracts/0.8.0`

**Manifest SHA-256:**
`c7eb4baae3a8312e44f9946fabde5a9cddb1937c7a41eb02d9b014ef21027ad1`

This candidate implements the accepted Phase 4 Step 1 design as an immutable
successor package. It does not modify accepted contract `0.7.0` and does not
implement the Go kernel, MongoDB projections, OpenHands runtime, or SMA boundary.

## Contract result

- **OBSERVED:** the package contains 134 catalogue entries: 67 commands and 67
  events.
- **OBSERVED:** it adds 22 protocol types: 11 command/event pairs.
- **OBSERVED:** it contains 17 schemas, 297 fixtures, 52 invariants, and 185
  traceability requirements.
- **OBSERVED:** the structure validator passed 3,604 of 3,604 checks.
- **OBSERVED:** the hermetic reference runner passed 297 of 297 cases.
- **OBSERVED:** a clean second generation produced a byte-identical package.
- **OBSERVED:** `go test -count=1 ./...` passed against the unchanged current Go
  implementation and accepted `0.7.0` integration.
- **OBSERVED:** the accepted `0.7.0` manifest remains
  `e2b9b5224a860a3eaa07451cf48fb5ac16a440b22b8dd592ff1662c1cff67f16`.

## Added aggregates and schemas

The package adds:

- aggregate `work-budget-account`;
- aggregate `work-invocation`;
- schema `work-budget-account.schema.json`;
- schema `work-invocation.schema.json`; and
- schema `operational-projection.schema.json` for task, story, and checkpoint
  projections.

The projection schemas bind descriptive fields, lifecycle state, ownership,
operational scope, work classification, exact assignment/runtime identities,
root budget use and remaining capacity, invocation summary, and
validation/finding/escalation/completion/acceptance state.

## Added protocol

The candidate adds command/event pairs for:

1. creating and explicitly amending a root work budget;
2. binding a task to its inherited budget;
3. binding Teams-owned operational scope, worktree, writable paths, and
   interface constraints;
4. authorizing one exact model-backed invocation;
5. claiming that invocation once;
6. recording the accepted external start;
7. recording exact terminal evidence;
8. requesting cancellation without falsely claiming cancellation completed;
9. expiring an unclaimed invocation after its deadline; and
10. retaining projection gap, conflict, or rebuild-mismatch evidence.

An authorization event includes the global and purpose debit ordinals plus both
remaining-budget counts. Budget debit and invocation authority therefore have
one reconstructable causal record.

## Structural termination rules

The reference model and negative fixtures enforce:

- missing budget fails closed;
- graph-wide and purpose-specific exhaustion fails closed;
- task dispatch, agent prose, provider status, and SMA output cannot wake a
  model;
- a permit is single-use and identity-bound;
- stale task revision, lifecycle, scope, execution fence, actor, model, runtime,
  workspace, or deadline is rejected;
- unchanged-condition redispatch is rejected unless an exact retryable terminal
  predecessor permits the next bounded retry ordinal;
- changed authoritative evidence may create a legitimate continuation;
- child task, handoff, replan, review, repair, promotion, restart, actor
  replacement, and model replacement cannot reset or increase the root budget;
  and
- only an explicit authorized budget amendment can change the frozen ceiling.

For a frozen root account, the conservative maximum additional model calls
remains:

```text
max(0, model_invocation_limit - model_invocations_used)
```

## Teams/SMA boundary

The contract freezes these rules:

- Teams owns task/story state, work classification, routing, ownership, leases,
  budgets, invocations, review, acceptance, and current operational state.
- SMA owns semantic and bounded task-local memory.
- OpenHands and SMA may submit evidence or proposals but cannot create an
  organizational effect without a Teams command being accepted.
- Task/story projections and SMA event mirrors are non-authoritative reads.

## Compatibility

`compatibility/from-0.7.0.json` binds the exact accepted predecessor manifest.
The successor is version-gated and requires an adapter because
`task.dispatch`/`task.dispatched` gains one explicit semantic restriction: it is
an assignment fact, not executable authority.

Historical `0.7.0` events remain replayable under `0.7.0`. No historical task is
silently given a budget, invocation, ownership lease, or projection. Phase 4
execution requires explicit successor budget and operational-scope bindings.

## Qualification boundary

The package qualifies the contract structure and hermetic reference semantics
only. The following remain `NOT_RUN`:

- Go kernel/application implementation of `0.8.0`;
- MongoDB task/story projection implementation;
- operational OpenHands invocation coordination;
- SMA programming-adapter boundary correction;
- live or measured Phase 4 integration; and
- v3 migration or production deployment.

## Recommendation

`ACCEPT/FREEZE tekroo.kernel.contracts/0.8.0` at manifest identity
`c7eb4baae3a8312e44f9946fabde5a9cddb1937c7a41eb02d9b014ef21027ad1`,
then authorize Phase 4 Step 3 implementation against that exact identity.
