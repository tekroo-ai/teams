# Phase 3 Step 4 — Deterministic execution coordinator

**Authority:** Principal statement `Proceed with Step 4.` on 2026-08-11 after
acceptance and release of Phase 3 Step 3.

## Boundary

This slice implements the provider-neutral execution coordinator required by
the accepted Phase 1B handoff, section 6. The organizational kernel authorizes
an exact execution registry command before the coordinator calls an external
execution engine. Provider state, errors, process exit, and observations remain
operational evidence; none completes, reassigns, or otherwise changes work.

A start binds one actor FQN, execution ID, fencing epoch, runtime-identity
digest, start idempotency key, and exact `execution.register` or
`execution.replace` command. The coordinator rejects any mismatch before either
kernel or provider dispatch. An applied kernel receipt is required before start.
A rejected or unavailable authorization never calls the provider.

Operational control records are deliberately separate from organizational
execution-registry events. They make `PENDING_AUTHORIZATION`, `AUTHORIZED`,
`STARTING`, `RUNNING`, `STOPPING`, `STOPPED`, `REJECTED`, and `UNCERTAIN`
observable without promoting provider status into organizational truth.
At-least-once coordinator invocation is safe through stable start keys and
compare-and-set control transitions.

## Bounds and uncertainty

Configured policy imposes positive operation timeouts, active-instance limits,
a finite per-actor start window, and finite reconciliation attempts. Capacity
counts uncertain executions fail closed. Calls cancelled before provider
dispatch are known to have no provider effect. Timeout, cancellation, malformed
observation, or provider error after dispatch records `UNCERTAIN`; it is never
reported as stopped, rolled back, or failed with certainty.

Reconciliation is explicit and bounded. A valid exact observation may resolve
uncertainty to `RUNNING` or `STOPPED`. An observation for another actor,
execution, or fencing epoch is rejected and leaves the record uncertain.

The in-memory control store and deterministic fake engine qualify semantics
only. They do not qualify production durability, isolation, recovery, load, or
any live provider. OpenHands, SMA, Git/provider release, automated work-DAG
coordination, migration, deployment, security isolation, and performance remain
separate authorization profiles.
