# Phase 3 Step 5 — Deterministic assignment and readiness

**Authority:** Principal statement `Proceed.` on 2026-08-11 after acceptance
and release of Phase 3 Step 4.

## Boundary

This slice implements a pure assignment planner plus an application coordinator
over the frozen kernel commands. Repository/query adapters supply one bounded
snapshot containing a task, exact dependency states and accepted evidence-event
identities, and policy-filtered exact actor candidates. The planner does not
infer causality from timestamps, delivery order, provider status, or message
adjacency.

Dependencies use an explicit exact terminal phase. `COMPLETED`, `ACCEPTED`, and
`CLOSED` are not interchangeable. Every dependency evidence event named in a
readiness payload must also be a declared DAG parent. Inputs are validated and
canonically ordered before selection, so repository iteration order cannot alter
the result.

Eligible actors are exact FQNs with current provider-neutral execution tuples.
The deterministic bootstrap policy selects the lowest active-assignment count,
then the lexically lowest FQN. The FQN remains the durable actor across restart;
the selected execution tuple changes and is fenced independently. Duplicate or
conflicting candidate identities fail closed.

The policy coordinator emits at most one forward sequence:

1. `task.mark-ready` when the task is still `PLANNED`;
2. `task.dispatch` with `routing_mode = EXACT`.

Each command has a caller-supplied stable command ID and idempotency key. Every
stage names the prior accepted event as a DAG parent and stops immediately on a
non-applied receipt or observable error. Dispatch remains routing intent. The
policy coordinator never constructs an actor-authority command or claims that
delivery establishes acceptance.

The exact actor accepts separately through the existing authenticated command
gateway by issuing `task.acquire-ownership` with its own FQN, current execution
fence, the dispatch event as a DAG parent, and the expected ownership version.
The frozen kernel compare-and-set establishes at most one owner. A failed or
missing actor acceptance leaves the task dispatched but unowned; retry,
reconciliation, expiry, and reassignment policy remain separately qualified
work rather than an implicit loop in this coordinator.

Unsatisfied dependencies, an already-owned/non-runnable task, or no eligible
actor produces one stable no-effect planning outcome. This slice performs no
automatic retries or escalation loop. Validation/completion joins, reopening,
escalation execution, release coordination, role/wildcard routing, live
providers, OpenHands, SMA, deployment, migration, and performance remain later
separately qualified work.
