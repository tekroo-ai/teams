# Phase 10 — Role charters, message handlers, and efficient wakeup

**Status:** APPROVED IMPLEMENTATION PLAN
**Parent plan:** `128-phase-10-hybrid-organizational-runtime-plan.md`
**Purpose:** Restore the useful v3 role/skill dispatch architecture without
restoring unrestricted agent conversations or synthetic idle work.

This document is the durable execution plan and checklist. After a context
compaction or session restart, resume at the first incomplete item whose
predecessors are complete. Do not replace this plan with a sequence of ad hoc
qualification attempts.

## 1. Required outcome

For every subscribed organizational message, Teams must deterministically
resolve:

```text
recipient FQN
  -> role FQRN and signed role bundle
  -> exact message type and purpose
  -> one signed per-role handler
  -> deterministic admission to one runnable DAG node
  -> one bounded role invocation
  -> validated result proposals
  -> deterministic DAG transition
```

The role handler explains how that role performs the admitted action. It does
not grant authority to invent work, enlarge scope, send arbitrary messages, or
continue a conversation.

When no message is present, the role worker blocks on its recipient-scoped
MongoDB change stream. A timeout creates no message, handler dispatch, model
invocation, workflow event, or semantic-memory record.

## 2. Verified starting point

- Implementation branch: `codex/phase10-hybrid`.
- Local and remote starting commit: `19c5810488b6a5799d68dc1dd658c0f3576122bc`.
- Accepted production contract at the starting point: immutable 0.11.0;
  successor 0.12.0 remains a candidate until separately accepted.
- No Phase 10 live qualification or deployment was started while the
  deterministic handler substrate was under construction.
- V4 role bundles already contain capabilities, subscriptions, permissions,
  instructions, and a `handlers` string map.
- The current Go runtime validates and signs the handler strings but does not
  dereference them during dispatch.
- Current handler names such as `design-story`, `implement-task`, and
  `validate-task` therefore do not select executable handler content.
- The execution brief receives the selected role's general instruction and
  generic runtime guidance, but not a message-specific role procedure.
- The existing MongoDB organizational-message feed already provides:
  recipient filtering, stream-before-backlog ordering, resume checkpoints,
  duplicate suppression, and change-stream recovery.
- The current feed uses a 100 ms server-side maximum await, while the example
  OpenHands poll interval is 250 ms. This is responsive but needlessly active
  for an idle role.
- V3 contains per-role, per-message `SKILL.md` files and substantially richer
  role definitions. It also contains historical workarounds and behavior that
  must not be copied blindly.
- Contract 0.11.0 defines handler values as strings. Content-addressed handler
  resources and their permitted results require one compatible successor
  schema rather than an in-place change to 0.11.0.

## 3. Non-negotiable design boundaries

### 3.1 Role identity

- FQRN identifies a role definition, such as `coder`.
- FQN identifies a concrete actor instance, such as `teams::coder-1`.
- A concise role charter is always present for an invocation and is reloaded
  after context compaction.
- The charter defines mission, owned decisions, non-responsibilities,
  escalation boundaries, and general standards. It must not encode the current
  feature's desired implementation.

### 3.2 Handler identity

- The dispatch key is the exact tuple `(FQRN, message_type, message_purpose)`;
  the binding also names its semantic subscription purpose.
- Every subscribed pair has exactly one disposition:
  `model_handler`, `deterministic_handler`, or `observe_only`.
- A model handler identifies one content-addressed handler document plus input
  and result schemas.
- A deterministic handler identifies a registered generic substrate operation,
  not a domain-role branch in Go.
- An unmapped or ambiguous subscribed message fails closed before any model is
  invoked.
- The same message type may map to different handlers for different FQRNs.

### 3.3 Handler authority

Every handler declares:

- accepted message type and purpose;
- input schema;
- role-owned decisions;
- prohibited decisions and side effects;
- required work product and evidence;
- result schema and terminal dispositions;
- permitted outgoing message proposals; and
- escalation conditions.

Agents return proposals. They do not publish organizational work directly.
Teams validates every proposal against the current workflow node, authority,
budget, and declared outgoing edge before committing it.

### 3.4 Structural loop prevention

- Workflow definitions must be acyclic.
- Each workflow-node execution identity is admitted at most once.
- A result may satisfy only the current node or a declared outgoing edge.
- A message cannot create an undeclared edge, point back to an ancestor, or
  reopen a completed node.
- Changing recipient, actor instance, thread, alias, or message type does not
  create new authority or reset the root budget.
- Duplicate delivery is idempotent.
- A question creates one correlated decision request; its answer closes that
  request rather than beginning a reply chain.
- Repair and revision create explicit successor nodes with `supersedes`
  lineage. They do not add backward graph edges.
- The same role may legitimately appear at several distinct nodes in a DAG.
- Long-running agents are not constrained by an arbitrary reasoning-iteration
  limit. Work remains governed by durable node authority, resource budgets,
  cancellation, and explicit completion or escalation.

### 3.5 Idle behavior

- Preserve MongoDB change streams as the primary wakeup mechanism.
- Use a configurable bounded wait with a production default near 60 seconds.
  A matching insert or admissible state change must unblock it immediately.
- Context cancellation must interrupt the wait immediately.
- On an empty timeout, re-enter the wait internally without invoking a model.
- Do not create or dispatch `tekroo-idle`.
- Health, liveness, lease ownership, and optional idle shutdown are separate
  substrate concerns. None is inferred from a synthetic idle message.
- Resume-token loss triggers the existing bounded resynchronization and backlog
  reconciliation path; it must not silently drop work.

## 4. Target package layout

The exact names may follow repository conventions, but the separation must be
preserved:

```text
config/<team>/roles/<role>/
  role.json
  ROLE.md
  handlers/
    <message-type>/
      HANDLER.md
      input.schema.json
      result.schema.json
```

The signed role bundle contains a discrete map resembling:

```json
{
  "handlers": {
    "task.assigned": {
      "execute": {
        "disposition": "model_handler",
        "message_purpose": "HANDOFF",
        "resource": "handlers/task.assigned/HANDLER.md",
        "sha256": "...",
        "input_schema": "handlers/task.assigned/input.schema.json",
        "result_schema": "handlers/task.assigned/result.schema.json",
        "allowed_results": ["completed", "failed", "blocked", "needs_decision"],
        "allowed_message_proposals": ["task.completed", "task.blocked"]
      }
    }
  }
}
```

All paths are confined to the signed role package. File contents are verified
against their declared digests before the bundle becomes eligible. The role
bundle digest, charter digest, handler digest, schemas, workflow identity, and
message identity are bound into the execution brief and retained evidence.

`HANDLER.md` is provider-neutral source content. An execution adapter may render
it into a provider's native skill mechanism, but provider-specific files must
not become the authoritative organizational definition.

## 5. V3 recovery policy

Inventory every v3 role charter and per-message skill, then classify each item:

- **ADOPT:** semantically useful and compatible with the DAG model;
- **ADAPT:** useful role behavior whose transport, naming, output, or authority
  assumptions must be converted to v4; or
- **RETIRE:** synthetic idle behavior, unrestricted reply loops, obsolete Claude
  lifecycle workarounds, direct persistence mutations, or superseded message
  types.

Port behavior, not prose volume. In particular:

- preserve role ownership, non-responsibilities, handoffs, and message-specific
  work procedures;
- remove feature-specific prescriptions and accumulated incident folklore from
  always-on context;
- move uncommon reference material behind explicit on-demand references;
- replace direct sends with validated outgoing-message proposals; and
- retire `tekroo-idle` rather than mapping it into the successor catalogue.

The initial restored library must cover the currently shipped product-owner,
project-manager, architect, coder, senior-coder, tester, security, and operator
roles. A small non-software role must prove that no runtime code change is
needed to add another FQRN and its handlers.

## 6. Implementation sequence and checklist

### A — Baseline and inventory

- [x] Record the current branch, commit, tracked status, contract identities,
  and active Phase 10 qualification state.
- [x] Generate a v3 role/message/skill inventory with ADOPT, ADAPT, or RETIRE
  decisions and corresponding v4 message types.
- [x] Locate every current hard-coded role or message procedure in Go and
  distinguish generic orchestration from domain behavior.
- [x] Add characterization tests for current role loading, message admission,
  change-stream wakeup, compaction grounding, and execution briefs before
  changing semantics.

**Exit:** no relevant v3 behavior or current compatibility surface remains
implicit.

### B — One successor contract

- [x] Create one compatible successor to contract 0.11.0 with the structured
  role charter and handler bindings described above.
- [x] Define exact dispatch-key uniqueness, content-digest verification,
  permitted results, permitted message proposals, and unmapped-message failure.
- [x] Define timeout-without-message as an internal transport outcome, not an
  organizational message.
- [x] Preserve 0.11.0 byte-for-byte and provide compatibility fixtures for its
  existing string handlers during a bounded transition.
- [x] Add conformance fixtures for role-specific mappings of the same message,
  invalid handler digests, ambiguous mappings, and prohibited output proposals.

**Exit:** the runtime has one stable contract to implement; do not issue a chain
of narrow revisions during implementation.

### C — Content-addressed role-package loader

- [x] Load and verify the selected role charter and every declared handler
  resource without escaping its package root.
- [x] Reject missing, mutated, duplicate, oversized, or schema-invalid content.
- [x] Build an immutable dispatch index keyed by FQRN, message type, and purpose.
- [x] Support atomic version replacement so a running invocation keeps its
  bound version while new work uses the accepted successor.
- [x] Cache verified immutable content by digest without weakening validation.

**Exit:** role packages are independently loadable and their handler identities
are deterministic.

### D — Deterministic message dispatcher

- [x] Resolve FQN to its bound FQRN and exact role-bundle version.
- [x] Match one exact handler disposition from the admitted message.
- [x] Validate the input before constructing or resuming an invocation.
- [x] Include only the selected charter, selected handler, admitted work,
  relevant evidence, and bounded SMA context in the execution brief.
- [x] Reinject the same immutable charter and handler after compaction.
- [x] Validate the result envelope and convert allowed emissions to proposals.
- [x] Reject all direct, unmapped, ambiguous, or unauthorized work emissions.
- [x] Remove role-specific semantic branches from the generic dispatch path.

**Exit:** adding a new role or handler package requires configuration and signed
content, not a Go source change.

### E — Efficient change-stream wait loop

- [x] Separate organizational-message wait duration from OpenHands status-poll
  configuration.
- [x] Replace the 100 ms idle change-stream cadence with the configurable
  bounded long wait while retaining immediate event wakeup.
- [x] Preserve stream-before-backlog ordering, atomic admission, checkpoints,
  duplicate suppression, resynchronization, and cancellation.
- [x] Ensure an idle timeout performs zero model calls and zero durable
  organizational writes.
- [x] Ensure an admitted message can automatically start an eligible stopped
  role and then dispatch its bound handler.
- [x] Keep idle shutdown, if enabled, based on absence of claims/work rather
  than synthetic messages or a busy handler's age.

**Exit:** an idle organization is quiescent, while a new message wakes the
correct role without polling latency.

### F — Restore and simplify the starter role library

- [x] Write concise provider-neutral role charters from the accepted portions
  of the v3 definitions.
- [x] Port the required v3 per-message skills into discrete v4 handlers using
  the inventory decisions from Step A.
- [x] Remove `tekroo-idle` and other Claude-specific loop-maintenance behavior.
- [x] Keep planning, design, implementation, validation, security, acceptance,
  and operator authority in their proper role packages.
- [x] Check that no handler embeds the actor-name canary solution or another
  feature-specific answer.
- [x] Add a non-software role and workflow entirely through signed
  configuration.

**Exit:** each role has sufficient general guidance to do its own work without
receiving other roles' instructions or task-specific coaching.

### G — Structural loop and recovery qualification

- [x] Prove duplicate message delivery creates one admission and one invocation.
- [x] Prove an agent cannot continue work by substituting another allowed
  message type, recipient, alias, FQN, or thread.
- [x] Prove a direct reply cannot create an undeclared node.
- [x] Prove a correlated question and answer close normally without a reply
  chain.
- [x] Prove legitimate repeated use of one role on distinct downstream nodes.
- [x] Prove repair creates a successor node without modifying completed nodes.
- [x] Prove handler failure resumes from the node checkpoint without repeating
  successful predecessors.
- [x] Prove daemon restart and computer sleep preserve stream and workflow
  progress.
- [x] Prove handler or role-package mutation invalidates eligibility before a
  model call.

**Exit:** cycles are structurally impossible, while valid rework and long-running
work remain possible.

### H — Integrated qualification and rollout

- [x] Run deterministic unit and integration suites first; repair failures at
  their owning layer rather than restarting a live workflow.
- [ ] Run one message-driven non-software workflow to prove generality.
- [ ] Resume Phase 10 stage-by-stage qualification using durable checkpoints.
- [ ] Re-run the simple actor-name request without feature-specific steering.
- [ ] Inspect role fidelity, work-product quality, model calls, compactions,
  duplicate work, idle activity, and validation cost at each transition.
- [ ] After every stage passes independently, run one clean end-to-end canary.
- [ ] Deploy only the exact accepted commit and role-package identities.
- [ ] Preserve rollback to the preceding production identity.

**Exit:** the complete workflow finishes autonomously, roles remain within
scope, idle operation is quiescent, and no agent-to-agent conversation cycle can
be formed.

## 7. Required acceptance tests

- [x] The same message type selects different handlers for two different roles.
- [x] Exactly one handler is selected for a valid `(FQRN, type, purpose)` key.
- [x] An unmapped, ambiguous, or digest-mismatched handler invokes no model.
- [x] An execution brief contains its selected role charter and handler but no
  unrelated role or handler content.
- [x] A compaction continuation restores the identical charter and handler
  digests.
- [x] A message arriving during a one-minute wait wakes the intended role
  promptly rather than waiting for the timeout.
- [x] Repeated empty waits generate no messages, skills, model calls, or workflow
  transitions.
- [x] Backlog plus concurrent stream delivery cannot lose or duplicate work.
- [x] A stopped eligible role starts when admitted work arrives.
- [x] A malicious or mistaken sequence of otherwise valid message types cannot
  create a DAG cycle or reset authority.
- [x] An authorized repair and a question/answer round trip complete without
  violating acyclicity.
- [ ] A non-software role and handler run without recompiling Teams.
- [x] Existing CLI, MCP, HTTP, MongoDB, FQN/FQRN, alias, task/story projection,
  OpenHands, and SMA boundaries remain compatible.

## 8. Completion definition

This plan is complete only when:

1. role charters and message handlers are executable, signed configuration;
2. every subscribed message has one explicit per-role disposition;
3. only the selected charter and handler enter the model context;
4. agents can propose only results and messages allowed by the current DAG node;
5. message substitution, reply chains, duplicate delivery, and actor changes
   cannot form work cycles;
6. idle workers block efficiently on MongoDB change streams without
   `tekroo-idle` or model activity;
7. restart, sleep, compaction, and handler-version continuity are proven;
8. both software and non-software workflows run without role-specific runtime
   code; and
9. one clean canary finishes autonomously with acceptable role fidelity and no
   repeated successful predecessor work.

## 9. Scope exclusions

- No v3 database or historical-message migration.
- No restoration of unrestricted agent-to-agent chat.
- No `tekroo-idle` compatibility message.
- No task-specific prompt programming.
- No hard-coded catalogue of software roles in generic runtime code.
- No change to SMA's semantic-memory authority boundary.
- No requirement that an idle role keep a model invocation resident.
- No live qualification loop until the deterministic layers and focused
  integration tests pass.
