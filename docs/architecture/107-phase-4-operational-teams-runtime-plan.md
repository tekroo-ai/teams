# Phase 4 — Operational Teams runtime plan

## Status and authority

**Status:** `APPROVED`

**Principal authorization:** "Approved. You may proceed with the plan."

This document defines the recommended phase after accepted Phase 3 Step 15. It
does not itself authorize implementation, deployment, migration, production or
historical data access, or a change to SMA's semantic-memory lifecycle.

## Baseline

- **OBSERVED:** Phase 3 Step 15 is accepted as
  `INTEGRATED_RUNTIME_TUPLE_QUALIFIED` for its exact SMA, OpenHands boundary,
  and model-profile tuple. It does not qualify production readiness, Teams model
  routing, event-export consumption, or SMA live shadowing.
- **OBSERVED:** contract `0.7.0` and the current Go kernel define a directed work
  DAG, exact ownership and execution fencing, work-risk profiles, qualified
  assignment, finite work budgets, deterministic validation, escalation, and
  provider-neutral execution evidence.
- **OBSERVED:** Teams owns authoritative task, DAG, assignment, authority,
  budget, acceptance, and execution-fence state. SMA is semantic memory and may
  return non-authoritative context; it does not classify work, select a model,
  assign work, or determine an organizational outcome.
- **OBSERVED:** Teams' Mongo adapter persists current aggregate state in
  `aggregates`, complete domain events in `events`, and task work-risk profiles
  in `work_profiles`. There is no dedicated materialized task or story read
  projection.
- **OBSERVED:** the current generic attempt-budget evaluator applies a limit only
  when the command type is present in the authorization policy's
  `attempt_limits` map.
- **COMPUTED:** DAG acyclicity alone cannot prevent an unbounded forward chain
  such as `A1 -> B1 -> A2 -> B2`. Structural termination additionally requires
  mandatory admission, progress, and finite-budget enforcement at the point
  where another agent execution can be started.

## Phase objective

Deliver one operational, provider-neutral Teams runtime in which:

1. Teams is the only authority for story/task state, classification, routing,
   ownership, budgets, validation, and completion;
2. an agent or arbitrary message cannot directly start another agent;
3. every model-backed invocation is admitted by a single-use Teams decision,
   attached to an exact task/DAG/lifecycle/work-profile revision, and charged to
   a mandatory finite budget;
4. continuation requires an explicit accepted state transition, required new
   evidence, or a changed-condition result;
5. exhaustion produces a durable terminal or escalated outcome instead of
   another conversational turn;
6. Teams provides rebuildable MongoDB task and story read models containing the
   operational information needed by coordinators and operators; and
7. OpenHands executes admitted work while SMA contributes bounded semantic
   context without acquiring workflow authority.

The phase exit demonstration is a complete local task lifecycle: create a
story and task, classify the task, select a qualified actor/model, execute it
through OpenHands, collect exact evidence, validate it, and reach accepted
completion. The same runtime must reject a deliberately induced agent
back-and-forth loop before the finite invocation bound can be exceeded.

## Binding engineering decisions to close first

### 1. Kernel-only execution admission

Only an accepted Teams decision may emit an executable invocation. Agent text,
tool output, provider status, channel delivery, SMA recall, and adapter-local
state are evidence or proposals and cannot wake another agent directly.

Each executable invocation binds at least:

- task ID, lifecycle epoch, scope revision, and expected task revision;
- exact DAG parent and invocation purpose;
- work-profile and qualified-assignment identities;
- actor FQN, execution ID, fencing epoch, model profile, and runtime identity;
- attempt family, ordinal, remaining budget, and absolute deadline;
- expected output/evidence predicate and allowed terminal results; and
- a single-use idempotency identity.

The design may strengthen the existing qualified-assignment/dispatch records or
introduce a distinct invocation-permit record. It must not create two competing
sources of execution authority.

### 2. Mandatory finite continuation

All model-executable transitions and all loop-capable organizational operations
must be governed by a finite budget derived from the current work profile. A
missing applicable budget fails closed; it is not interpreted as unlimited.

A further invocation is permitted only when at least one declared condition is
true:

- an accepted task or lifecycle transition requires it;
- a required evidence predicate remains unsatisfied and the next action is
  explicitly authorized;
- new evidence changes the condition digest;
- an authorized review, correction, handoff, promotion, or escalation transition
  creates the next directed node; or
- the operator authorizes a versioned scope or policy change.

Repeating the same task/lifecycle/purpose/actor/condition combination does not
create progress. It is rejected or terminates through the configured blocker or
escalation policy. A changed role name or message type cannot reset a budget.
Child tasks, replanning, handoffs, review corrections, model promotion, process
replacement, and restart do not silently reset the parent phase's finite bound.

The kernel must be able to compute a conservative maximum number of permitted
model invocations for an exact frozen work graph and policy.

### 3. Teams/SMA ownership boundary

Teams owns and persists the canonical form of:

- stories, tasks, descriptions, scope, criteria, dependencies, and DAG edges;
- lifecycle, condition, ownership, assignment, execution fences, and deadlines;
- work-risk classification, routing requirements, selected model/profile, and
  qualification identity;
- attempts, budgets, reviews, findings, escalations, completion, acceptance, and
  release state; and
- current shared operational state used to decide what may run next.

SMA may persist normalized semantic memories, their provenance, uncertainty,
contradiction/supersession relationships, and evidence references. It may also
receive read-only Teams metadata for partitioning, retrieval, and context
labelling. Such metadata is a versioned mirror or request context, never a
mutable authority or fallback source of task state.

Agent-local working notes may exist in a bounded execution workspace. Any
shared working-state structure that influences scheduling, ownership, budgets,
or completion belongs in Teams or is a disposable projection of Teams. It is
not semantic memory.

SMA-originated difficulty, relevance, or routing suggestions are evidence. A
Teams policy decision is required before they affect the work profile or route.

### 4. Domain protocol ownership

- `tekroo.command.task.*` requests task transitions and
  `tekroo.event.task.*` records accepted task facts.
- `tekroo.command.story.*` and `tekroo.event.story.*` do the same for stories.
- A separate first-class aggregate such as evidence, completion review,
  escalation, variant group, or release retains its own namespace and exact
  task/story reference.
- Generic conversation messages cannot substitute for domain commands or
  events and cannot create canonical effects.

### 5. Rebuildable task and story read models

Add Teams-owned MongoDB projections for operational reads. The exact collection
names are fixed by the successor design, but the logical views contain:

- identity, title, description, current scope, acceptance criteria, and
  dependencies;
- aggregate revision, lifecycle epoch, scope revision, phase, and condition;
- owner and ownership version;
- current work-profile classification and finite budgets, including used and
  remaining amounts;
- qualified assignment, required and selected decision routes, exact
  actor/execution/model/runtime identities, and dispatch/invocation status;
- current validation, escalation, completion, and acceptance summary; and
- last applied event identity and projection revision.

The event ledger remains authoritative. Projections must be idempotent,
rebuildable from accepted events, detect gaps or conflicting revisions, and
never authorize a transition by themselves. A scalar complexity value, if
useful for display, is a derived value tied to an exact classification-policy
revision; the multidimensional work profile remains authoritative.

## Execution sequence

### Step 1 — Design closure and boundary inventory

Inspect the exact current Teams and SMA schemas and Mongo collections. Classify
every task-, story-, assignment-, execution-, AMW-, and shared-working-state
field as canonical Teams state, disposable execution state, read-only mirror,
external evidence, or semantic memory. Resolve every duplicate or ambiguous
owner.

Complete the invocation-admission, progress, finite-bound, projection, and
Teams/SMA interface designs above. Produce state/sequence diagrams and a field
ownership matrix. No implementation begins with an unresolved owner or two
authoritative copies.

**Exit:** one approved design with no ambiguous workflow owner and a computable
invocation bound.

### Step 2 — Immutable successor contract

Create a successor to contract `0.7.0`; do not edit released packages in place.
The successor freezes the minimum required command/event/schema changes,
mandatory budget semantics, single-use invocation authority, progress and
termination outcomes, task/story projection contracts, compatibility rules,
and Teams/SMA boundary invariants.

Required negative fixtures include:

- missing budget;
- permit reuse, stale task revision, stale lifecycle, stale execution fence, or
  wrong actor/model/runtime identity;
- direct agent-to-agent wakeup;
- the same loop expressed through different message types;
- unchanged-condition redispatch, circular handoff, and budget reset through
  child creation, restart, handoff, promotion, or replanning;
- projection gap, duplicate event, conflicting revision, and deterministic
  rebuild;
- SMA attempt to write or supply authoritative task/routing state; and
- valid changed-evidence review/fix and bounded handoff paths, proving that the
  termination rules do not block legitimate iteration.

**Exit:** one accepted immutable contract package whose reference runner rejects
every loop and boundary counterexample.

### Step 3 — Kernel termination and admission implementation

Implement the accepted contract in the pure Go kernel and application layer:

- mandatory work-profile-derived budgets for every loop-capable operation;
- single-use invocation admission and durable consumption;
- explicit progress/condition identity;
- bounded continuation, handoff, review correction, promotion, and escalation;
- restart-stable accounting and conservative maximum-invocation calculation;
- rejection of agent-, channel-, provider-, or SMA-originated execution bypass;
  and
- deterministic terminal outcomes on exhaustion.

Use property and mutation tests to show that renaming or rerouting a message
cannot evade the bound.

**Exit:** kernel and application tests prove that no accepted path can create an
unbounded number of model invocations from a finite frozen work graph and
policy.

### Step 4 — Teams MongoDB task/story projections

Implement the accepted projections and indexes in the Teams Mongo adapter. Add
incremental event application, full rebuild, checkpointing, gap/conflict
detection, and differential comparison between incremental and rebuilt views.
Keep the projections read-only with respect to organizational decisions.

**Exit:** task and story views reconstruct deterministically after restart,
redelivery, permitted ordering, and complete rebuild, with exact agreement to
the authoritative event/state ledger.

### Step 5 — Operational execution coordinator

Connect the accepted Teams dispatch path to the qualified OpenHands execution
boundary. The coordinator:

1. consumes one admitted invocation;
2. verifies current task, work profile, assignment, actor, execution fence,
   model/runtime identity, deadline, and remaining budget;
3. constructs the bounded execution brief from Teams state and evidence;
4. starts or resumes only the authorized OpenHands execution;
5. records tools, artifacts, terminal state, and provider output as evidence;
6. returns evidence/proposals to Teams for the next kernel decision; and
7. never forwards agent prose directly into another agent invocation.

Ambiguous start, timeout, cancellation, host suspension, process replacement,
and restart use durable reconciliation rather than guessing or replaying the
entire task.

**Exit:** the runtime can execute one bounded task and recover from interruption
without duplicate work, lost budget, stale authority, or direct agent chaining.

### Step 6 — SMA boundary enforcement

Use the already qualified SMA/OpenHands/model tuple without changing SMA's
semantic-memory lifecycle. Bind the operational interface so that:

- Teams supplies exact read-only task and provenance metadata needed for context
  partitioning;
- SMA returns labelled, bounded, non-authoritative context;
- Teams state and routing never fall back to SMA when Teams data is absent;
- captured exchanges may later yield semantic lessons through SMA policy but do
  not become task transitions; and
- database identities and write paths prevent SMA from mutating Teams
  organizational collections and prevent Teams from directly mutating SMA
  internals.

**Exit:** positive recall improves context, while missing, stale, contradictory,
or malicious recall cannot change assignment, budget, ownership, acceptance, or
completion.

### Step 7 — Integrated qualification

Run one preregistered local suite over the exact assembled runtime. At minimum it
covers:

1. normal single-owner task completion;
2. explicit DAG decomposition and deterministic join;
3. an attempted A/B back-and-forth loop using ordinary and deliberately
   co-opted message types;
4. a legitimate review/fix cycle with changed evidence;
5. handoff, promotion, and replanning within finite inherited budgets;
6. budget exhaustion and terminal escalation;
7. process restart, stale result, duplicate delivery, timeout, and cancellation;
8. task/story projection incremental update and clean rebuild;
9. absent, stale, contradictory, and cross-partition SMA context; and
10. concurrent independent tasks without cross-task state or budget leakage.

The loop test must report the computed invocation ceiling and the exact terminal
reason. Success is not merely that the test eventually stopped; no unauthorized
model invocation may occur.

**Exit:** all scenarios pass against retained raw receipts and the exact runtime
identity. Prior accepted Step 15 scenarios are not rerun wholesale; only affected
integration paths are forward-qualified.

### Step 8 — Controlled local operating pilot

Run a small disposable software task through the complete Teams runtime with
operator start, inspect, pause, resume, cancel, and stop controls. Use a sandbox
repository and disposable Teams/SMA namespaces. Confirm that the task/story read
models are sufficient to understand current work without reading raw agent
transcripts.

**Exit:** accept or reject `TEAMS_V4_LOCAL_OPERATIONAL_RUNTIME_QUALIFIED` for the
exact assembled runtime. Production deployment and v3 migration remain separate
decisions.

## Qualification discipline

- Complete each implementation slice and its negative controls before asking for
  review. Do not use review as a substitute for engineering completion.
- On failure, repair and rerun only the affected slice and not previously accepted
  unrelated work.
- Preserve the accepted Step 15 runtime qualification as an input. Requalify it
  only if a bound SMA, OpenHands, model, prompt, tool, endpoint, or runtime
  identity materially changes.
- Use fakes below the real orchestration seam. The same coordinator, serializers,
  state machines, and evidence interpretation must run over fake and live ports.
- A green emulator is insufficient when the live adapter would require new
  unqualified logic.

## Explicit non-goals

This phase does not include:

- wholesale transplantation of v3 source;
- migration or mutation of v3 or historical production data;
- changing SMA into a workflow database, scheduler, auditor, or acceptance
  engine;
- redesigning SMA memory semantics or rerunning accepted SMA work without a
  material identity change;
- a dashboard or broad user-interface project;
- automatic production startup or unattended production operation;
- broad cloud deployment; or
- a claim that every model profile is qualified because one exact runtime tuple
  passed Step 15.

## Phase exit and following phase

Phase 4 is complete only when the exact local runtime demonstrates the full
task lifecycle, deterministic read projections, restart recovery, SMA boundary,
and structural termination properties above.

After Phase 4, the recommended next phase is controlled v3 transition and daily
operation: map salvageable v3 task/story data into the accepted v4 import
contract, dry-run migration against copies, qualify operator workflows and
observability, and then decide whether to cut over. Migration is intentionally
not allowed to shape or weaken the Phase 4 runtime semantics.
