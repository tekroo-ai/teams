# Phase 10 — Hybrid message-driven organizational runtime

**Status:** PROPOSED
**Purpose:** Replace the hard-coded software-development coordinator with a
configurable organizational workflow runtime while preserving the accepted v4
kernel, DAG, authority, budget, evidence, MongoDB, OpenHands, and SMA boundaries.

This is an implementation plan. Except for one consolidated successor contract,
every step must produce working code and tests. It must not become another chain
of documentation-only gates.

## Mandatory companion amendment

The role-charter, per-message handler, structural anti-conversation-loop, and
change-stream idle-wait work is specified in
`131-phase-10-message-handler-runtime-plan.md`. That plan is part of Phase 10,
not optional follow-up work. Steps 9 and 10 below must not be accepted until its
completion definition is satisfied.

## 1. Outcome

Teams will combine the useful behavior of the v3 message-passing organization
with the safety properties of the v4 kernel:

```text
directed message or operator request
              |
              v
     deterministic admission
       /                 \
 rejected/stored       admitted
                           |
                           v
                 workflow DAG node
                           |
              budget + assignment + policy
                           |
                           v
                single-use invocation
                           |
                           v
                 bounded role execution
                           |
                           v
        evidence + proposed outgoing messages
                           |
                           v
          deterministic state transition
```

A message can cause Teams to consider work, but it never grants execution
authority by itself. Only an admitted workflow node can create an invocation.

## 2. Constraints

- Preserve the accepted v4 kernel and contract packages; do not edit an accepted
  package in place.
- Keep Teams authoritative for workflow, task, story, assignment, budget,
  validation, acceptance, and release state.
- Keep SMA non-authoritative and limited to semantic memory.
- Keep OpenHands behind the execution adapter.
- Keep MongoDB change streams as the role wakeup mechanism.
- Preserve FQRN as the role-bundle identity and FQN as the concrete actor
  identity.
- Preserve existing CLI, MCP, and HTTP compatibility while the supplied software
  workflow is moved out of Go code.
- Do not require v3 data migration.
- Do not restore unbounded agent-to-agent conversation or allow message types to
  bypass the DAG, budgets, or admission policy.
- Do not encode product-owner, project-manager, architect, coder, tester,
  security, or any other domain role in generic runtime code.

## 3. Current defects being corrected

1. `planningStageDefinition` embeds software roles, prompts, work-product shapes,
   and sequencing policy in Go.
2. The role worker receives MongoDB messages but only copies them into an
   in-process inbox; there is no generic message-to-work admission path.
3. Feature coordination separately advances messages, feature state, tasks, and
   invocations, making messages secondary records rather than the communication
   input to the organization.
4. Message-thread cycle detection prohibits revisiting an entire role class. It
   should prohibit cycles in work-node identity and causal topology instead.
5. Independent plan-task reviews are chained serially.
6. Validation policy creates too many model-backed review passes regardless of
   risk and duplicates evidence already produced by implementations and tests.
7. Failed late stages can cause predecessor work to be repeated instead of
   resuming from the last successful stage boundary.

## 4. Target components

### 4.1 Versioned workflow definition

Add a provider-neutral `WorkflowDefinition` loaded from signed/versioned
configuration. It contains:

- workflow name, semantic version, and content digest;
- accepted trigger types;
- stages and stable stage identifiers;
- directed dependencies between stages;
- input and output schema references;
- completion and failure conditions;
- capability requirements and optional preferred FQRNs;
- work purpose, risk derivation, and complexity limits;
- concurrency group and maximum parallelism;
- retry, repair, review, validation, and escalation policy;
- allowed outgoing message purposes and target-selection rules;
- time, token, attempt, hop, and root-work budget limits; and
- projection rules for domain-specific views such as features, stories, and
  tasks.

The generic runtime must select actors by declared capabilities and current
policy. Preferred FQRNs belong in a workflow definition, not in the runtime.

The current software-development flow becomes a shipped definition under
`config/workflows/`. A second, small non-software workflow must use the same
public loading mechanism without a Go change.

### 4.2 Workflow instance and stage state

Add durable, replayable state for:

- workflow definition identity;
- root request and budget account;
- current and completed DAG nodes;
- node input and output evidence identities;
- admitted actor and invocation identity;
- checkpoint and continuation identity;
- failure classification and remaining bounded recovery options; and
- proposed and accepted outgoing messages.

Kernel events are authoritative. Feature, story, task, inbox, and status records
are projections. A projection must not independently decide the next stage.

### 4.3 Message-to-work admission bridge

Replace the passive inbox-only path with a generic admission service:

1. A recipient-scoped MongoDB feed observes the durable message.
2. The role worker submits a `WorkProposal` containing the exact message,
   workflow, stage, actor, execution, causation, and budget identities.
3. Deterministic policy verifies that the proposed transition is declared,
   predecessors are complete, the node is new, the actor is eligible, and the
   relevant budgets remain available.
4. Rejection records a reason without invoking a model.
5. Admission atomically records the workflow-node transition and authorized
   work; only then may the role host start an actor or the execution coordinator
   issue an OpenHands invocation.

Ad hoc informational messages remain deliverable without automatically creating
work. Operator commands may explicitly request admitted work through the same
proposal path.

### 4.4 Node-based loop prevention

Replace the blanket visited-role prohibition with these invariants:

- the workflow DAG cannot contain a cycle;
- a stage-execution/node identity cannot be admitted twice;
- a causation edge cannot point to itself or an unapproved predecessor;
- the same progress digest cannot consume another attempt;
- changing message type, recipient, actor instance, or thread cannot reset the
  root budget;
- a repair or reconsideration is a new successor node with an explicit edge,
  bounded attempt count, and changed-condition evidence; and
- the same FQRN or FQN may execute multiple distinct downstream nodes when the
  DAG and policy permit it.

This makes loops structurally impossible at the work level without forbidding a
legitimate later review or repair by a role used earlier.

### 4.5 Generic role execution

The execution prompt is composed from:

- the immutable role bundle selected by FQRN;
- the workflow-stage definition;
- the exact admitted work item;
- relevant authoritative artifacts and evidence;
- bounded non-authoritative SMA context; and
- one generic result envelope.

Infrastructure must not tell an architect how many tasks to author, tell a
tester to design a solution, or otherwise reproduce role-specific prompt
programming. Role bundles define expertise and conduct; workflow definitions
define responsibilities and deliverables; the runtime only binds and enforces
their contracts.

## 5. Efficient planning and validation policy

### 5.1 Planning

- Product refinement and project planning may overlap when their required inputs
  are available.
- Architecture remains a deliberately substantial activity when the problem
  warrants it.
- Use one independent whole-plan review for moderate or higher risk plans.
- Do not create one model-backed feasibility review per task by default.
- A task-specific pre-implementation review is allowed only for an identified
  high-risk invariant, security boundary, unverified external API, or
  cross-component atomicity claim.
- Independent task reviews have no dependency on one another and run in
  parallel, followed by one deterministic join.
- The planner declares true dependencies. Teams must not add serialization merely
  to simplify scheduling.

### 5.2 Implementation

- Admit every dependency-ready implementation node up to configured model,
  workspace, and repository concurrency limits.
- Give each task an isolated workspace and deterministic dependency composition.
- Allow multiple tasks on the same model server so the server can batch them.
- Serialize only conflicting repository mutations or explicitly declared
  dependencies—not roles or task indexes.

### 5.3 Validation

Validation has three layers:

1. **Deterministic task checks:** compilation, focused tests, static analysis,
   schema validation, and diff checks required by the task. The implementer runs
   them and Teams retains the raw receipts.
2. **Selective independent review:** model-backed review only when selected by
   declared risk policy or an explicit workflow requirement. It reuses existing
   receipts and does not rerun passing deterministic checks without a reason.
3. **One joined feature check:** after implementation branches are composed,
   run the complete integration suite and product acceptance once against the
   exact candidate tree. Security review is conditional on security-relevant
   scope or risk classification.

Default policy:

| Work classification | Independent model review | Feature-level check |
| --- | --- | --- |
| Low | none | deterministic integration + acceptance |
| Moderate | one whole-plan review; task review only on a named risk | deterministic integration + acceptance |
| High | independent task review for the named risk | integration + specialized review + acceptance |
| Critical | explicit operator-approved validation plan | as approved |

Existing evidence is content-addressed. An unchanged candidate, criterion,
toolchain, and environment reuse the existing passing result. Any rerun must
record which changed condition invalidated it.

### 5.4 Efficiency limits

For low and moderate work:

- **Target:** validation wall time is no more than 50% of design plus
  implementation wall time.
- **Hard ceiling:** validation wall time may not exceed design plus implementation
  wall time.
- **At the ceiling:** stop admitting additional validation passes, report the
  responsible stage and duplicated evidence, and require a changed condition or
  explicit escalation before continuing.
- No more than one whole-plan model review and one whole-feature model review by
  default.
- No successful predecessor stage is repeated to repair a later stage.
- A failed stage resumes from the last durable successful transition.
- Automatic model retries remain disabled; deterministic transport recovery does
  not consume a model attempt.

Track both wall-clock critical-path time and aggregate model-compute time so
parallel work is not misreported as cheap merely because it completes quickly.

## 6. Consolidated contract revision

Create one `tekroo.kernel.contracts/0.11.0` successor containing all required
changes:

- workflow-definition and workflow-instance schemas;
- work-proposal and admission-result schemas;
- workflow and stage event catalogue entries;
- node-based message-loop invariants;
- validation-policy and evidence-reuse rules;
- compatibility from `0.10.0`; and
- conformance fixtures for parallel branches, bounded repair, repeated roles on
  distinct nodes, forbidden node cycles, message rejection, restart recovery,
  and non-software workflow loading.

Do not issue multiple narrow contract revisions during implementation. If an
implementation discovery does not alter externally observable semantics, fix
the implementation without revising the contract.

## 7. Implementation sequence

### Step 1 — Preserve the current qualification state — COMPLETE

- [x] Record the current Phase 9 run, worktree, workflow instance, task DAG,
  completed stages, active stage, and raw timing/evidence identities.
- [x] Preserve all current uncommitted work; do not restart or erase the current
  canary.
- [x] Separate the completed Phase 9 material into three explicit products:
  - an infrastructure baseline containing only reusable Teams process/runtime
    repairs;
  - an actor-name acceptance fixture containing the original feature request,
    criteria, clean starting identity, expected process observations, and no
    implementation; and
  - a quarantined reference candidate containing the team's actor-name commits
    and evidence, retained for later comparison but excluded from every Phase 10
    agent workspace and from canonical `main`.
- [x] Prove the infrastructure baseline contains no actor-name production code,
  actor-name tests, solution commit, or generated candidate artifact.
- [x] Prove the acceptance fixture is sufficient to submit the same feature
  again without revealing the prior implementation.
- [x] Add characterization tests for the existing public CLI, MCP, HTTP, task,
  story, message, alias, and restart behavior.

**Exit:** the current behavior can be compared with the successor without using
the live canary as a development loop, and the actor-name feature remains a
clean reusable test rather than part of the Phase 10 baseline.

### Step 2 — Freeze contract 0.11.0 once — COMPLETE

- [x] Implement the consolidated package described in section 6.
- [x] Run its structural validator and reference fixtures.
- [x] Confirm `0.10.0` remains byte-for-byte unchanged.

**Status:** accepted and frozen at the manifest identity recorded in the Step 2
machine receipt.

**Exit:** one stable target contract for all remaining production work.

### Step 3 — Implement the workflow loader and state machine — COMPLETE

- [x] Add workflow definition parsing, validation, digesting, and version lookup.
- [x] Add workflow instance folding, stage readiness, completion, blocking,
  cancellation, and checkpoint transitions.
- [x] Add deterministic fake-engine tests for every transition and recovery path.

**Exit:** arbitrary valid workflow definitions can run without role-specific Go
branches.

### Step 4 — Implement message admission

- [x] Connect the existing MongoDB recipient feed to `WorkProposal` admission.
- [ ] Make admission idempotent and transactionally bind message, workflow node,
  task, budget, and invocation authority.
- [ ] Start an eligible role only after admission.
- [x] Preserve informational messages and human replies that create no work.
- [ ] Test backlog-before-stream ordering, crash recovery, duplicate delivery,
  stale execution fencing, and dead-letter behavior.

**Exit:** an accepted message advances work through the kernel; an unaccepted
message cannot invoke a model.

### Step 5 — Correct loop and continuation semantics

- [x] Replace visited-role rejection with node/DAG cycle rejection.
- [x] Test a legitimate `architect -> coder -> architect` sequence using three
  distinct nodes.
- [x] Test renamed-message, new-thread, different-instance, and budget-reset loop
  attempts.
- [x] Test bounded repair successors and changed-condition evidence.

**Exit:** legitimate role reuse works and conversational work cycles remain
structurally impossible.

### Step 6 — Externalize the software workflow

- [x] Encode the current software flow as a versioned workflow definition.
- [x] Move stage prompts and work-product schemas to workflow and role bundles.
- [x] Replace literal software-role branching in the configured operational path with
  generic capability selection and stage execution.
- [x] Preserve existing feature/story/task projections and operator surfaces.
- [ ] Delete obsolete hard-coded paths after compatibility tests pass.

**Exit:** the current software flow operates from configuration with no built-in
software role names in generic runtime code.

### Step 7 — Implement efficient scheduling and validation

- [x] Admit all dependency-ready DAG roots concurrently within configured
  capacity.
- [x] Remove index-based serialization of independent plan-task reviews.
- [x] Implement risk-based validation selection and evidence reuse.
- [x] Add durable per-stage checkpoints and resume from the failed stage only.
- [x] Add validation-time target and ceiling enforcement.
- [ ] Expose critical-path time, aggregate model time, queue time, model tokens,
  evidence reuse, duplicate checks, and achieved parallelism. Timing now also
  exposes logical task and invocation counts, retry and recovery counts, and
  non-successful model time; model-token and evidence-reuse counters still need
  raw adapter receipts.

**Exit:** deterministic tests demonstrate parallel execution, no duplicated
passing checks, and ceiling enforcement.

### Step 8 — Prove generality

- [x] Add one non-software role bundle and workflow through configuration only.
- [ ] Submit and complete it through the normal operator surface.
- [x] Confirm no Go source change is needed to introduce its stages or roles.

**Exit:** Teams is demonstrated as an organizational runtime rather than a
hard-coded software pipeline.

### Step 9 — Bounded live qualification

Do not restart the entire workflow after every defect.

- [ ] Run the software workflow one stage at a time, checkpointing after each
  successful transition.
- [ ] If a stage fails, repair and rerun that stage from its predecessor
  checkpoint; do not repeat successful predecessors.
- [ ] Use the alias feature as the retained moderate-complexity canary without
  task-specific steering.
- [ ] Inspect role boundaries and work-product quality at each transition.
- [ ] Confirm independent implementation nodes and eligible reviews run in
  parallel.
- [ ] Confirm pause, daemon restart, computer sleep, and resume preserve exact
  progress.
- [ ] Measure the validation/design-plus-implementation ratio.

**Exit:** every stage has passed independently, the completed result is usable,
and validation stays below the hard ceiling.

### Step 10 — One clean end-to-end acceptance run

- [ ] Restore one clean baseline only after Step 9 passes completely.
- [ ] Submit the preserved actor-name acceptance fixture without exposing the
  quarantined prior implementation, then run it once without operator steering
  or manual repair.
- [ ] Verify code quality, role fidelity, DAG correctness, exact restart
  continuity, deterministic merge, and final acceptance.
- [ ] Compare timing, tokens, model calls, duplicate work, and parallelism with
  the preserved Phase 9 run, and compare the result with the quarantined
  reference candidate only after the new run is terminal.
- [ ] Commit and merge only after the complete run passes.

**Exit:** the hybrid runtime is the supported production path.

## 8. Required acceptance tests

- [ ] A directed message causes admitted work through the kernel but cannot
  directly launch a model.
- [ ] An informational message creates no work.
- [ ] A legitimate later node may use the same role or actor.
- [ ] A node cycle, no-progress retry, or budget-reset attempt is rejected.
- [ ] Four independent implementation nodes can execute concurrently when model
  and workspace capacity permit.
- [ ] Independent reviews execute concurrently.
- [x] Low-risk work receives no unnecessary model-backed task review.
- [x] High-risk work receives only its declared specialized review.
- [x] Passing evidence is reused until a recorded changed condition invalidates
  it.
- [x] Failure at stage N resumes at stage N without repeating stages 1 through
  N-1.
- [ ] Sleep/restart recovery resumes the exact workflow and invocation state.
- [ ] A new non-software role and workflow run without recompiling Teams.
- [ ] Existing feature, story, task, role, alias, CLI, MCP, HTTP, MongoDB, and
  OpenHands behavior remains compatible.
- [ ] Validation wall time is less than or equal to design plus implementation
  wall time for the moderate canary.

## 9. Completion definition

Phase 10 is complete only when:

1. contract `0.11.0` is accepted and all earlier contracts remain unchanged;
2. generic runtime code contains no dependency on supplied software role names;
3. MongoDB messages participate in deterministic work admission rather than
   serving only as passive records;
4. node-level DAG rules replace role-level cycle prohibition;
5. the software and non-software workflows both run from public configuration;
6. dependency-ready work and review branches run concurrently;
7. validation is selective, evidence-reusing, checkpointed, and within the hard
   efficiency ceiling; and
8. the single clean end-to-end canary completes without steering, duplicated
   predecessor work, or unbounded conversation.

## 10. Explicitly not required

- v3 data migration;
- restoration of v3 Claude-specific launch or prompt machinery;
- direct model wakeup from raw message insertion;
- a unique process per role at all times;
- universal independent model review of every task;
- repeating completed stages to prove a later repair; or
- redesigning SMA, OpenHands, MongoDB, or the accepted v4 kernel.
