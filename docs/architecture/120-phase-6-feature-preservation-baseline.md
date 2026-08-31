# Phase 6 Step 1 — Feature-preservation baseline

## Status

**Status:** complete

**Machine ledger:**
`OUTPUT/phase-6/step-1/feature-preservation-ledger.json`

**Authority:** the principal accepted the Phase 6 plan and authorized continuous
execution without a stop after every step. This baseline contains no retirement
decision, deployment, data migration, external-system change, or Tekroo v3
modification.

## Source receipts

- **OBSERVED:** the current Tekroo v3 source identity inspected for this step is
  `0fda6ae0cc5239794bcdbfa11402782d62cf1092`.
- **OBSERVED:** the Phase 1A archaeology code inventory is pinned to
  `7630ca20ccfa0fc2f4102147b10c3705e9ba0158`.
- **OBSERVED:** current v3 contains six commits after that archaeology pin. They
  add story-lifecycle guards, security-gate evidence, message-parent hardening,
  liveness obligations, catalogue/fanout corrections, and branch-prefix
  teaching. The ledger uses the current source wherever those paths apply.
- **OBSERVED:** the v4 source identity at the beginning of the inventory is
  `3fc5db05d20e5db89adc4fb736a91fc933a9e84f`.
- **OBSERVED:** current v4 is governed by immutable contract
  `tekroo.kernel.contracts/0.8.0`.
- **OBSERVED:** Tekroo v3 and the archaeology workspace were read only. Their
  pre-existing untracked files were not changed.

The named sources were inspected directly:

- `/Users/paul/work/tekroo-ai/teams-v3`;
- `/Users/paul/work/tekroo-ai/teams-v3/tekroo-v4-archaeology/OUTPUT`;
- `/Users/paul/work/tekroo-ai/teams-v3/tekroo-v4-archaeology/PHASE-1B`; and
- `/Users/paul/work/tekroo-ai/teams`.

## Inventory reconciliation

### Directly observed surfaces

- **OBSERVED:** v3 has 27 immediate `mcp/internal` packages.
- **OBSERVED:** v3 has eight configured starter roles: operator, product-owner,
  PM, architect, coder, senior-coder, tester, and security.
- **OBSERVED:** those roles contain 31 message-handler skills.
- **OBSERVED:** the current v3 message catalogue defines 31 message types.
- **OBSERVED:** the v3 CLI registers 15 root command groups.
- **OBSERVED:** the v3 daemon descriptor registry exposes 22 MCP tools.
- **OBSERVED:** v3's Mongo subscriber opens its change stream before draining
  backlog and uses an atomic claim to prevent duplicate delivery.
- **OBSERVED:** the current v4 catalogue contains 134 entries: 67 commands and
  67 events.
- **OBSERVED:** v4's production `tekrood` composition wires the operational
  runtime and operator HTTP handler, but not its MCP or channel adapters and not
  a role host.
- **OBSERVED:** v4's current MCP adapter advertises one generic command tool.
- **OBSERVED:** v4's channel adapter accepts one generic directed command frame;
  it does not implement an organizational-message inbox.

### Ledger result

- **COMPUTED:** 50 feature capabilities are individually classified.
- **COMPUTED:** 20 are `PRESERVE`.
- **COMPUTED:** 21 are `REIMPLEMENT`.
- **COMPUTED:** 6 are `REPLACE_WITH_EQUIVALENT`.
- **COMPUTED:** 3 are `DEFER` to Step 8 after the local team path works.
- **COMPUTED:** 0 are `RETIRE`.
- **COMPUTED:** 0 are unclassified.
- **COMPUTED:** every row has a v3 source locator, archaeology locator, target,
  implementation step, acceptance test, and defect-to-avoid list.

## What v4 already solves

The ledger preserves the following current v4 implementation rather than
recreating it:

- stable actor FQN separated from execution ID and fencing epoch;
- event and work DAG enforcement;
- task/story lifecycle and MongoDB operational projections;
- exact ownership, qualified assignment, multidimensional work profiles, and
  model/runtime profile bindings;
- mandatory finite budgets and single-use work invocations;
- independent validation joins, completion review, acceptance, reopening,
  escalation, cancellation, and release semantics;
- deterministic local Git provider and release-plan reconciliation;
- team continuity control and exact worktree/writable-path binding;
- human participant and human interaction authority; and
- OpenHands execution with SMA restricted to non-authoritative semantic memory.

## What must be added

The missing product is the organizational runtime around that kernel:

1. activated team manifests and immutable role bundles;
2. a production role host with stable FQN continuity and bounded lifecycle;
3. an organizational-message catalogue and durable exact inbox;
4. MongoDB change-stream wakeup with delivery claims distinct from ownership;
5. product-owner/PM/architect/coder/tester/security workflows;
6. feature intake and automatic finite story/task decomposition;
7. production-wired focused MCP and CLI operations;
8. human delivery/notification and role liveness/recovery;
9. role-driven deterministic validation, merge, release, and acceptance; and
10. deferred local-team-adjacent operations such as aliases, signed federation,
    cross-team routing, and library sync.

## Consolidated contract impact

### Successor required

**COMPUTED:** `0.8.0` is sufficient for the existing task, story, work profile,
budget, invocation, evidence, validation, completion, release, human, worktree,
and continuity state. It does not contain the missing team-manifest, role-host,
feature-intake, or organizational-message contracts. Implementing those as
uncontracted adapter state would create a second source of organizational truth.

Phase 6 therefore requires one immutable successor package:
`tekroo.kernel.contracts/0.9.0`.

### Preserve unchanged from 0.8.0

- all current command/event semantics and negative fixtures;
- actor FQN plus execution fencing;
- story/task DAG and lifecycle;
- work profiles, assignments, budgets, invocations, evidence, and validation;
- completion, acceptance, release, human interaction, worktree, and continuity;
  and
- OpenHands/SMA boundaries.

### Add once in 0.9.0

- versioned team-manifest and role-bundle schemas;
- team/role activation and exact actor-roster state;
- provider-neutral actor-runtime lifecycle and liveness observations;
- versioned exact-directed organizational messages with typed purpose and exact
  story/task/DAG linkage;
- delivery state, claim epoch, lease, renewal, resolution, redelivery, and dead
  letter, explicitly distinct from task ownership and work invocation;
- deterministic role-request resolution to an exact qualified actor;
- exact per-recipient fanout;
- feature-request intake and deterministic product-owner/story linkage; and
- focused operator views and commands required to use these capabilities.

### Required negative fixtures

- ordinary message delivery cannot launch an agent or invoke a model;
- message delivery claim cannot establish or replace task ownership;
- role/message renaming cannot reset a root work budget;
- stale actor execution or stale delivery epoch cannot act;
- role request resolves to an exact actor before ownership or invocation;
- fanout produces one exact delivery per recipient;
- stream-before-backlog has no loss or duplicate;
- poison delivery terminates in a dead letter within a finite budget;
- unknown/incompatible messages remain lossless but have no canonical effect;
- team or role bundle digest/signature mismatch fails activation;
- actor process replacement preserves FQN and obligations while fencing prior
  output; and
- SMA, prompt text, tool output, provider state, and channel delivery cannot
  create team, role, task, routing, budget, or outcome authority.

## Explicitly preserved v3 value

The following v3 behaviors are not permitted to disappear during
implementation:

- configured roles and team roster;
- long-running wait-for-work behavior;
- MongoDB change-stream wakeup;
- exact directed delivery, claims, leases, recovery, and dead letters;
- feature intake through product ownership, design, decomposition, task
  assignment, validation, and acceptance;
- operator MCP/CLI access;
- human participants, notifications, and replies;
- worktree continuity and deterministic release;
- status, liveness, diagnostics, and recovery; and
- deferred-but-recorded aliases, trusted partners, cross-team routing, and
  library sync.

## Explicitly rejected v3 defects

- model-to-model wakeup from a message;
- free-form or renamed-message loops;
- role cycling and validation/planning repetition without changed evidence;
- task fanout or child creation that resets the root budget;
- ordinary messages accidentally triggering lazy launch;
- one-winner claim behavior mislabeled as broadcast;
- provider session identity replacing actor FQN;
- prompt prose acting as enforcement;
- stale local memory acting as task truth; and
- provider/tool/model assertions completing organizational work.

## Step result

Step 1 satisfies the Phase 6 preservation rule: all 50 inventoried capabilities
have explicit destinations and acceptance tests; none is silently eliminated.
The next engineering action is to build the single `0.9.0` successor and the
production team/role vertical slice defined by Step 2.
