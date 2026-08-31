# Phase 6 — Organizational runtime recovery

## Status and authority

**Status:** accepted

**Authority:** requested by the principal after the Tekroo v3/v4 feature
comparison and accepted with authorization to execute continuously through
completion. Deployment, data migration, modification of Tekroo v3, or a change
to an external system still requires its own authority.

**Execution checklist:**
`docs/operations/003-phase-6-organizational-runtime-recovery-checklist.md`

## Why this phase exists

- **OBSERVED:** Tekroo v4 has a strong deterministic kernel, task/story state,
  DAG enforcement, finite budgets, single-use execution authority, MongoDB
  projections, OpenHands execution, and an enforced SMA boundary.
- **OBSERVED:** the current production composition does not provide the complete
  organizational runtime that made Tekroo v3 operate as a team: configured
  roles, long-running role workers, feature intake and decomposition, broad
  operator/MCP tools, role-directed messaging, human participation, release
  coordination, or complete lifecycle operations.
- **OBSERVED:** the Phase 1 archaeology identified these v3 capabilities as
  valuable and generally recommended preservation, refactoring, or
  reimplementation. Their absence from the current runtime was an
  implementation-sequencing omission, not an archaeology recommendation to
  discard them.
- **INFERRED:** the safest recovery is to retain the v4 control kernel and add
  the missing organizational runtime around it. Reverting to the v3 message
  loop would restore both useful behavior and the failure mode that motivated
  v4.

Phase 6 therefore restores the useful organizational behavior of v3 without
weakening the v4 execution-admission, DAG, budget, evidence, or ownership
invariants.

## Named source locations

Step 1 must use these exact sources:

- Tekroo v3 implementation: `/Users/paul/work/tekroo-ai/teams-v3`;
- Phase 1 archaeology output:
  `/Users/paul/work/tekroo-ai/teams-v3/tekroo-v4-archaeology/OUTPUT`; and
- current Tekroo v4 implementation: `/Users/paul/work/tekroo-ai/teams`.

Derived summaries may help navigate these sources, but may not replace direct
inspection of the named implementation and archaeology artifacts.

## Non-negotiable preservation rules

1. No v3 capability disappears implicitly.
2. Every feature discovered in the v3 source, role library, CLI/MCP surface, or
   archaeology output receives exactly one recorded disposition:
   `PRESERVE`, `REIMPLEMENT`, `REPLACE_WITH_EQUIVALENT`, `DEFER`, or
   principal-approved `RETIRE`.
3. `RETIRE` requires an explicit principal decision. It is never inferred from
   absence in v4.
4. A replacement is complete only after its user-visible behavior passes an
   end-to-end acceptance test. Similar internal types or handlers do not count
   as feature parity.
5. Tekroo v3 source and historical data remain read-only. No data migration is
   required.
6. Existing accepted v4 packages are immutable. If new organizational semantics
   require contract changes, create one consolidated successor package rather
   than editing `0.8.0` or issuing a series of narrow revisions.
7. After the inventory step, every step must produce production code and a
   runnable supported-path demonstration, not only planning or test artifacts.
8. Qualification uses normal engineering terms: implementation, test,
   integration test, acceptance test, and operating pilot.

## Features that must remain intact

Phase 6 extends rather than replaces these v4 capabilities:

- exact actor FQN with replaceable process/execution identity;
- Teams-owned story, task, assignment, routing, budget, invocation, review,
  acceptance, and release authority;
- directed acyclic task and invocation graphs;
- mandatory finite execution and continuation budgets;
- single-use work invocation authority and execution fencing;
- deterministic replay and durable MongoDB projections;
- provider-neutral model profiles and OpenHands execution;
- SMA as semantic memory, never workflow or process authority;
- exact evidence, provenance, validation, completion, and cancellation state;
  and
- prevention of direct agent-to-agent model wakeup.

## V3 capability areas to recover

The Step 1 ledger must account for, at minimum, the following observed v3
surfaces. This list is a floor, not a substitute for source inventory.

1. Team definitions, role roster, role configuration, role instructions, and
   role-handler libraries.
2. Operator, product-owner, project-manager, architect, coder, senior-coder,
   tester, and security roles.
3. Stable role FQNs, instance ceilings, lazy launch, restart, stop, worktree
   continuity, and per-role working memory continuity.
4. Directed role messaging, inbox state, delivery claims, epochs, leases,
   redelivery, dead letters, and MongoDB change-stream wakeup.
5. Feature intake, story creation, specifications, design, planning,
   decomposition, complexity/risk classification, task routing, assignment,
   validation, bounded repair, acceptance, and completion.
6. Operator MCP tools and CLI commands for team work, messages, agents, stories,
   tasks, status, liveness, claims, traces, notifications, and dead letters.
7. Human participants, subject-matter experts, clients/end users, authenticated
   responses, notifications, and console/intermediary channels.
8. Worktree creation and cleanup, Git integration, deterministic merge/release,
   and repository-provider abstraction.
9. Cross-team routing, aliases, trusted partners, signed ingress, library sync,
   diagnostics, lifecycle traces, and operational recovery.

## Behaviors that must not return

- free-form agent conversations that can wake another model;
- unbounded back-and-forth under renamed or repurposed message types;
- role cycling, validation thrashing, post-completion activity, or planning
  without a finite convergence condition;
- accidental agent launch caused by ordinary message delivery;
- false broadcast semantics in which a declared broadcast is actually claimed
  by one recipient;
- provider-specific assumptions in the domain or organizational layer;
- prompts, tool output, channel delivery, model assertions, or SMA recall acting
  as organizational authority; and
- placeholder dashboards, webhooks, or telemetry being reported as operational
  features.

## Target operating flow

```text
Operator or human participant
        |
        v
Product owner -> project manager -> architect/planner
        |                |                 |
        +----------------+-----------------+
                         v
               Teams task/story DAG
                         |
          policy + budget + exact assignment
                         |
                         v
                single-use invocation
                         |
                         v
                  OpenHands worker
                         |
       evidence -> tester/security/reviewer
                         |
          bounded repair or acceptance
                         |
                         v
             deterministic release/completion
```

Messages carry facts, requests, and evidence between exact actors. They do not
start models. Only a current Teams kernel decision may issue the single-use
invocation that wakes an execution worker.

## Engineering sequence

### Step 1 — Freeze the feature-preservation baseline

Inventory the actual v3 source and the Phase 1 archaeology, then map every
discovered capability to the current v4 source and supported product surface.
Do not rely solely on earlier summaries.

Deliver:

- a machine-readable feature ledger;
- a concise human-readable feature map;
- source references for the v3 implementation and v4 state;
- archaeology disposition and evidence references;
- the selected disposition, target component, dependencies, and acceptance test
  for every feature;
- an explicit list of known v3 defects that must not be reproduced;
- a count reconciliation proving that no inventoried item is unclassified; and
- one proposed consolidated successor-contract scope, if the inventory proves a
  contract change is necessary.

**Exit:** every discovered v3 feature is accounted for and no retirement is
implicit. The principal can approve dispositions as one coherent baseline.

### Step 2 — Rebuild teams, roles, and the role host

Implement versioned team manifests and immutable/content-addressed role bundles.
Provide starter definitions for the eight v3 roles while keeping role behavior
provider-neutral.

The role host must support:

- stable FQN identity across process replacement;
- exact team, role, capabilities, subscriptions, permissions, model profile,
  instance policy, and workspace binding;
- start, stop, restart, pause, resume, status, heartbeat, checkpoint, and bounded
  recovery;
- instance ceilings and explicit launch policy; and
- separation of durable role identity from ephemeral process, session, and
  execution identity.

**Acceptance:** start a configured team, replace one role process, and prove it
resumes with the same FQN and authorized workspace without duplicating active
work.

### Step 3 — Restore directed messaging and MongoDB wakeup

Implement versioned role-directed messages over the accepted Teams transport
boundary. Preserve MongoDB change streams as the efficient blocking wakeup
mechanism.

Required behavior:

- exact sender and recipient FQNs;
- explicit message purpose, related story/task/DAG node, causation, and
  correlation identities;
- durable inbox, claims, leases, renewal, recovery, redelivery, and dead letter;
- exact per-recipient fanout for multi-recipient delivery;
- startup order that opens the change stream before backlog reconciliation;
- no message type or recipient change can reset a task budget; and
- message receipt may create a proposal or kernel command, but may not directly
  invoke a model.

**Acceptance:** demonstrate exact delivery, crash recovery, real fanout, and
bounded no-progress loop rejection while legitimate directed handoff continues.

### Step 4 — Restore the operator MCP and usable CLI

Wire the existing v4 MCP adapter into `tekrood` and replace the single generic
entry point with a focused, discoverable operator surface. Extend `tekroo` with
the corresponding human-friendly commands.

The supported surface must cover:

- feature and team submission;
- team/role roster and lifecycle;
- directed messages and replies;
- story/task creation, inspection, assignment, and cancellation;
- liveness, active work, claims, traces, budgets, notifications, and dead
  letters; and
- pause, resume, recovery, and deterministic error reporting.

Low-level raw command submission may remain for diagnostics, but it is not the
normal workflow.

**Acceptance:** an operator submits and inspects team work through MCP and CLI
without hand-authoring the current sequence of kernel JSON commands.

### Step 5 — Restore feature intake, design, and planning

Implement the organizational path:

`feature request -> product owner -> project manager -> architect/planner ->
story/task DAG -> exact assignment`.

Support specifications, amendments, story splitting/successors, acceptance
criteria, priorities, dependencies, design decisions, critical path,
complexity/risk classification, capability/model routing, and bounded task
expansion. Task and story state remains canonical Teams state in MongoDB;
`task.*` and `story.*` commands/events remain its protocol.

**Acceptance:** one operator feature request produces a reviewable design and a
finite executable task DAG with explicit owners, budgets, dependencies, and
model profiles.

### Step 6 — Restore implementation, verification, and release

Connect the role organization to the existing v4 execution coordinator:

`coder -> deterministic checks -> tester/security/reviewer -> bounded repair ->
completion -> product-owner acceptance -> release`.

Implement deterministic worktree assignment, changed-file ownership, evidence
collection, validation findings, correction routing, completion review, and Git
provider/release coordination. Merge behavior must be deterministic,
idempotent, recoverable, and explicit on conflict.

**Acceptance:** a real repository change travels from assigned task through
tests, independent review, at most the authorized repair paths, deterministic
merge/release, and product-owner acceptance.

### Step 7 — Restore human participation and operations

Implement a participant directory and exact human-recipient work requests for
operator, SME, client/end-user, and other authorized participants. The operator
intermediary may carry the conversation, but Teams retains the durable request,
authority, response, and task linkage.

Add notifications, console/intermediary delivery, wait-for-work behavior,
per-role status, heartbeats, orphan detection, bounded restart, team-wide
pause/resume, and recovery after service or host interruption.

**Acceptance:** a team asks an exact human participant a task-linked question,
waits without consuming model turns, accepts an authenticated response, and
resumes the correct DAG node after restart.

### Step 8 — Restore secondary operational capabilities

Implement or deliberately schedule the remaining ledger items: aliases, trusted
partners, signed cross-host ingress, cross-team exact routing, library sync,
diagnostics, lifecycle trace, dead-letter operations, and complete CLI output.

These features may be sequenced after the local single-team path, but they may
not disappear from the ledger. Any feature proposed for retirement returns to
the principal for an explicit decision.

**Acceptance:** every Step 1 item is implemented, equivalently replaced,
explicitly deferred with a scheduled dependency, or explicitly retired by the
principal.

### Step 9 — Integrated acceptance and operating pilot

Run the supported production path, not a test-only assembly. Include:

- retained archaeology-derived productive and catastrophic workflow fixtures;
- loop attacks using renamed messages, role cycling, circular handoff,
  validation thrashing, post-completion work, and child-task budget reset;
- legitimate plan, implementation, review, repair, escalation, and acceptance;
- role restart with stable FQN and workspace continuity;
- 1, 2, 4, and 8 concurrent task execution;
- MongoDB, OpenHands, model-server, and host interruption/recovery;
- human participant wait and response;
- deterministic merge conflict and release recovery; and
- one real feature from operator submission through accepted release.

**Acceptance:** all required product workflows pass with finite execution,
complete evidence, no direct model chaining, no duplicate work, and no manual
kernel-command choreography.

## Dependency order and parallel work

- Step 1 precedes all implementation because it prevents another silent feature
  loss.
- Steps 2 and 3 establish the organizational runtime foundation.
- Step 4 can proceed alongside the latter part of Step 3 once the role and
  messaging APIs are stable.
- Step 5 depends on Steps 2–4.
- Step 6 depends on Step 5 and the existing v4 execution coordinator.
- Step 7 may begin after the message and role contracts are stable, then joins
  the end-to-end flow before Step 9.
- Step 8 may proceed in parallel after the local team path is stable.
- Step 9 is the only phase-closing step.

## Implementation discipline

For each step:

1. inspect the actual predecessor and current source paths;
2. state the user-visible behavior being recovered;
3. implement the smallest complete vertical slice through the supported product
   composition;
4. add unit tests for invariants and integration tests for wiring;
5. run one supported-path demonstration;
6. update the preservation ledger with exact implementation and test evidence;
7. confirm that no previously accepted capability regressed; and
8. stop at the step boundary for principal acceptance.

Do not create candidate/rehearsal cycles for ordinary software defects. Diagnose
and repair the production path, retain a regression test, and rerun only the
affected scope plus the necessary integration suite.

## Phase 6 definition of done

Phase 6 is complete only when the supported `tekrood`/`tekroo`/MCP path can:

1. accept a feature request from the operator;
2. deliver it to the configured product-owner role;
3. design and decompose it through configured team roles;
4. create a finite task DAG and select appropriate actors/model profiles;
5. execute authorized work through OpenHands;
6. validate independently and perform only bounded evidence-driven repair;
7. merge and release deterministically;
8. obtain product-owner acceptance;
9. let the operator inspect the entire lifecycle; and
10. restart any role process without changing its FQN, losing authorized
    workspace continuity, duplicating work, or bypassing Teams authority.

The phase is not complete merely because kernel commands, individual adapters,
or integration tests exist. A working configured team must complete the full
workflow through the normal product surface.
