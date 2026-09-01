# Phase 6 organizational-runtime recovery checklist

This is the working checklist for
`docs/architecture/119-phase-6-organizational-runtime-recovery-plan.md`.

**Plan status:** accepted and authorized for continuous execution.

## Operating rules

- [x] Preserve Tekroo v3 source and historical data read-only.
- [x] Preserve accepted v4 contract packages; create a successor rather than
      editing `0.8.0` in place.
- [x] Do not migrate v3 data.
- [x] Do not remove a v3 feature without an explicit principal-approved
      `RETIRE` decision.
- [x] Do not count an internal type, test double, or unwired adapter as a
      restored product feature.
- [x] Require one normal supported-path acceptance test for every replacement.
- [x] Preserve v4 DAG, finite-budget, single-use invocation, execution-fence,
      evidence, projection, OpenHands, and SMA-boundary behavior.
- [x] Prevent messages, prompts, tools, providers, channels, and SMA from
      directly starting another model or creating organizational truth.
- [x] Record production defects as regression tests; fix the affected path
      rather than starting a candidate/rehearsal cycle.
- [x] Update this checklist and the feature ledger at every accepted step.

## Step 1 — feature-preservation baseline

Named sources:

- [x] Use `/Users/paul/work/tekroo-ai/teams-v3` as the v3 implementation source.
- [x] Use
      `/Users/paul/work/tekroo-ai/teams-v3/tekroo-v4-archaeology/OUTPUT` as the
      Phase 1 archaeology source.
- [x] Use `/Users/paul/work/tekroo-ai/teams` as the current v4 source.
- [x] Do not substitute a derived summary for any named source.

### Source inventory

- [x] Inspect current Tekroo v3 source, not only archaeology summaries.
- [x] Inspect the complete Phase 1 archaeology output.
- [x] Inspect current v4 source and production composition.
- [x] Inventory v3 team and role configuration.
- [x] Inventory all v3 role instruction and handler bundles.
- [x] Inventory the v3 message catalogue and delivery semantics.
- [x] Inventory v3 MongoDB collections, indexes, change streams, claims, leases,
      epochs, redelivery, and dead letters.
- [x] Inventory v3 MCP tool descriptors and handlers.
- [x] Inventory v3 CLI command groups and supported operations.
- [x] Inventory lifecycle, worktree, liveness, restart, and recovery behavior.
- [x] Inventory feature/story/task/specification/planning/validation behavior.
- [x] Inventory operator, participant, notification, and channel behavior.
- [x] Inventory Git/release/provider behavior.
- [x] Inventory alias, trusted-partner, cross-host, cross-team, library, and
      diagnostic behavior.

### Ledger requirements

- [x] Assign a stable feature ID to every inventoried capability.
- [x] Record exact v3 source evidence for every feature.
- [x] Record archaeology evidence and disposition for every feature.
- [x] Record exact current v4 implementation and production-wiring evidence.
- [x] Record the user-visible behavior and operational value.
- [x] Record known v3 defects or limitations separately from the feature value.
- [x] Assign exactly one disposition: `PRESERVE`, `REIMPLEMENT`,
      `REPLACE_WITH_EQUIVALENT`, `DEFER`, or principal-approved `RETIRE`.
- [x] Record target component and dependency step.
- [x] Define an end-to-end acceptance test for every retained/replaced feature.
- [x] Reconcile inventory counts; unclassified count must equal zero.
- [x] Produce the human-readable feature map.
- [x] Produce the machine-readable feature ledger.
- [x] Propose one consolidated successor-contract scope if required.
- [x] Confirm continuous execution authority permits advancement with no
      separate Step 1 stop; no feature is retired.

## Required feature-area accounting

No row may be removed. Add rows when source inspection discovers more.

| Feature area | Inventoried | Disposition recorded | Target recorded | Acceptance test defined | Implemented | Accepted |
|---|---:|---:|---:|---:|---:|---:|
| Team manifests and roster | [x] | [x] | [x] | [x] | [x] | [x] |
| Role bundles and handler library | [x] | [x] | [x] | [x] | [x] | [x] |
| Operator role | [x] | [x] | [x] | [x] | [x] | [x] |
| Product-owner role | [x] | [x] | [x] | [x] | [x] | [x] |
| Project-manager role | [x] | [x] | [x] | [x] | [x] | [x] |
| Architect/planner role | [x] | [x] | [x] | [x] | [x] | [x] |
| Coder and senior-coder roles | [x] | [x] | [x] | [x] | [x] | [x] |
| Tester and security roles | [x] | [x] | [x] | [x] | [x] | [x] |
| Stable FQN and process replacement | [x] | [x] | [x] | [x] | [x] | [x] |
| Agent lifecycle and instance ceilings | [x] | [x] | [x] | [x] | [x] | [x] |
| Worktree and local continuity | [x] | [x] | [x] | [x] | [x] | [x] |
| Directed message catalogue | [x] | [x] | [x] | [x] | [x] | [x] |
| MongoDB change-stream wakeup | [x] | [x] | [x] | [x] | [x] | [x] |
| Claims, leases, redelivery, dead letters | [x] | [x] | [x] | [x] | [x] | [x] |
| Exact multi-recipient fanout | [x] | [x] | [x] | [x] | [x] | [x] |
| Feature intake | [x] | [x] | [x] | [x] | [x] | [x] |
| Stories and specifications | [x] | [x] | [x] | [x] | [x] | [x] |
| Planning and task decomposition | [x] | [x] | [x] | [x] | [x] | [x] |
| Complexity/risk and model routing | [x] | [x] | [x] | [x] | [x] | [x] |
| Assignment and execution | [x] | [x] | [x] | [x] | [x] | [x] |
| Validation and bounded repair | [x] | [x] | [x] | [x] | [x] | [x] |
| Completion and product acceptance | [x] | [x] | [x] | [x] | [x] | [x] |
| MCP operator surface | [x] | [x] | [x] | [x] | [x] | [x] |
| CLI operator surface | [x] | [x] | [x] | [x] | [x] | [x] |
| Human/SME/client participation | [x] | [x] | [x] | [x] | [x] | [x] |
| Notifications and console channels | [x] | [x] | [x] | [x] | [x] | [x] |
| Status, heartbeats, orphan recovery | [x] | [x] | [x] | [x] | [x] | [x] |
| Team pause/resume/recovery | [x] | [x] | [x] | [x] | [x] | [x] |
| Git/worktree provider | [x] | [x] | [x] | [x] | [x] | [x] |
| Deterministic merge and release | [x] | [x] | [x] | [x] | [x] | [x] |
| Cross-team routing and aliases | [x] | [x] | [x] | [x] | deferred | [x] |
| Trusted partner and signed ingress | [x] | [x] | [x] | [x] | deferred | [x] |
| Library sync | [x] | [x] | [x] | [x] | [x] | [x] |
| Diagnostics and lifecycle trace | [x] | [x] | [x] | [x] | [x] | [x] |

## Step 2 — teams, roles, and role host

- [x] Implement versioned team-manifest schema and validation.
- [x] Implement immutable/content-addressed role bundles.
- [x] Provide starter bundles for all eight required roles.
- [x] Bind exact capabilities, subscriptions, permissions, model profiles,
      instance policies, and workspaces.
- [x] Implement stable FQN separate from execution/process/session identity.
- [x] Implement start, stop, restart, pause, resume, and status.
- [x] Implement heartbeat, checkpoint, orphan detection, and bounded recovery.
- [x] Enforce instance ceilings and explicit launch policy.
- [x] Production-wire the role host into `tekrood`.
- [x] Demonstrate role replacement with FQN/workspace continuity and no
      duplicate active work.
- [x] Run regression tests for all preserved v4 invariants.
- [x] Complete Step 2 under the principal's continuous-execution authority.

## Step 3 — directed messaging and MongoDB wakeup

- [x] Implement versioned exact-FQN message envelopes.
- [x] Bind story/task/DAG, causation, correlation, and message-purpose identity.
- [x] Implement durable inbox, claims, leases, renewal, redelivery, and dead
      letters.
- [x] Open the change stream before backlog reconciliation.
- [x] Implement exact per-recipient fanout; do not use one-winner broadcast.
- [x] Resolve role-wide routing to exact actors before delivery.
- [x] Prohibit ordinary message delivery from launching a new agent.
- [x] Prohibit message receipt from directly invoking a model.
- [x] Preserve budget across role, message-type, handoff, restart, and child-task
      changes.
- [x] Test crash recovery and duplicate delivery.
- [x] Test renamed-message and role-cycle loop attacks.
- [x] Test legitimate directed handoff and changed-evidence continuation.
- [x] Production-wire messaging and wakeup into `tekrood`.
- [x] Complete Step 3 under the principal's continuous-execution authority.

## Step 4 — operator MCP and CLI

- [x] Production-wire MCP into `tekrood`.
- [x] Add focused feature/team submission tools.
- [x] Add role roster and lifecycle tools.
- [x] Add directed message/reply tools.
- [x] Add story/task inspection and control tools.
- [x] Add status, liveness, active-work, claim, trace, budget, notification, and
      dead-letter tools.
- [x] Add pause, resume, cancellation, and recovery tools.
- [x] Provide matching human-friendly `tekroo` commands.
- [x] Retain raw command submission only as a diagnostic escape hatch.
- [x] Verify authentication, exact human identity, deadlines, bounded bodies,
      and stable errors.
- [x] Demonstrate normal operation without hand-authored kernel JSON.
- [x] Complete Step 4 under the principal's continuous-execution authority.

## Step 5 — feature intake, design, and planning

- [x] Implement operator feature-request intake.
- [x] Route intake to the exact product-owner actor.
- [x] Implement product-owner clarification and acceptance-criteria workflow.
- [x] Implement project-manager story/specification workflow.
- [x] Implement architect/planner design and decomposition workflow.
- [x] Persist all canonical story/task state in Teams MongoDB projections.
- [x] Use `story.*` and `task.*` commands/events for canonical transitions.
- [x] Implement priority, dependencies, critical path, amendments, splits, and
      successors.
- [x] Implement complexity/risk classification and capability/model routing.
- [x] Enforce finite task expansion and explicit approval for scope changes.
- [x] Demonstrate a feature request becoming a finite executable DAG.
- [x] Complete Step 5 under the principal's continuous-execution authority.

## Step 6 — implementation, verification, and release

- [x] Connect exact assignment to existing single-use work invocation.
- [x] Create deterministic worktree ownership and cleanup.
- [x] Capture changed files, tool output, tests, and artifacts as evidence.
- [x] Route findings to exact responsible DAG nodes.
- [x] Enforce bounded repair and escalation.
- [x] Run independent tester review.
- [x] Run required security review.
- [x] Implement completion review and product-owner acceptance.
- [x] Implement provider-neutral Git/release coordination.
- [x] Make merge idempotent and deterministic.
- [x] Detect and report conflicts without destructive cleanup.
- [x] Test interruption and recovery during work, review, merge, and release.
- [x] Demonstrate a real Git change through the release provider and the
      supported feature-release path.
- [x] Complete Step 6 under the principal's continuous-execution authority.

## Step 7 — human participation and operations

- [x] Implement participant directory and exact recipient identity.
- [x] Support operator, SME, client/end-user, and other authorized human roles.
- [x] Persist request, authority, response, and task/DAG linkage in Teams.
- [x] Authenticate human responses and reject stale/wrong-recipient responses.
- [x] Wait for a human without consuming model invocations.
- [x] Resume the correct DAG node after response or restart.
- [x] Implement notifications and console/intermediary delivery.
- [x] Expose per-role status, heartbeat, active work, and blocker state.
- [x] Implement team pause/resume and host/service recovery.
- [x] Demonstrate task-linked human question/response with durable Mongo state.
- [x] Complete Step 7 under the principal's continuous-execution authority.

## Step 8 — secondary operational capabilities

- [x] Defer cross-team exact routing to Phase 7 federation after an approved
      trust/routing threat model and successor contract.
- [x] Defer aliases to Phase 7 federation without weakening exact identity.
- [x] Defer trusted-partner and signed-ingress support to Phase 7 federation.
- [x] Complete library synchronization.
- [x] Complete diagnostics and lifecycle tracing.
- [x] Complete dead-letter inspection and repair operations.
- [x] Complete remaining CLI outputs and controls.
- [x] Reconcile the ledger: no item remains silently omitted.
- [x] Confirm the ledger proposes no retirement.
- [x] Record explicit dependencies and target phase for every deferred item.
- [x] Complete Step 8 under the principal's continuous-execution authority.

## Step 9 — integrated acceptance and operating pilot

- [ ] Use the supported production `tekrood`, `tekroo`, and MCP paths.
- [ ] Replay retained productive archaeology workflows.
- [ ] Reject validation thrashing.
- [ ] Reject role cycling and circular handoff.
- [ ] Reject renamed-message loop evasion.
- [ ] Reject post-completion activity.
- [ ] Reject child-task/replanning/restart budget resets.
- [ ] Pass legitimate planning, handoff, review, repair, and escalation.
- [ ] Prove stable FQN and workspace continuity after role restart.
- [ ] Run 1, 2, 4, and 8 concurrent task executions.
- [ ] Test MongoDB interruption and recovery.
- [ ] Test OpenHands/model interruption and recovery.
- [ ] Test service and host interruption and recovery.
- [ ] Test human-participant wait/response across restart.
- [ ] Test merge conflict and deterministic release recovery.
- [ ] Complete one real feature from operator submission through accepted
      release.
- [ ] Confirm no direct model chaining or duplicate work occurred.
- [ ] Confirm no manual kernel-command choreography was required.
- [ ] Obtain Phase 6 acceptance.

## Final completion check

- [ ] Operator can submit a feature through MCP or `tekroo`.
- [ ] Configured product owner receives it.
- [ ] Configured team designs and decomposes it.
- [ ] Teams creates a finite DAG and selects qualified actors/model profiles.
- [ ] OpenHands executes only single-use Teams-authorized work.
- [ ] Independent validation and bounded repair complete.
- [ ] Merge/release is deterministic and recoverable.
- [ ] Product owner records acceptance.
- [ ] Operator can inspect every lifecycle stage.
- [ ] Role restart retains FQN/workspace/organizational continuity.
- [ ] Every v3 feature has an explicit final disposition.
- [ ] Every preserved or replacement feature has supported-path evidence.
- [ ] No prohibited v3 loop or authority bypass has returned.
