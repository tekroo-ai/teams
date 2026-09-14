# V3 role and message-handler recovery inventory

**Status:** IMPLEMENTATION INPUT
**Source inspected:** `teams-v3/mcp/starter-library/roles/*/{CLAUDE.md,skills/*/SKILL.md}` and `teams-v3/mcp/starter-library/operator/CLAUDE.md`
**Target:** successor Teams v4 role-package format defined by `131-phase-10-message-handler-runtime-plan.md`

This inventory ports behavior, not the v3 Claude-specific operating substrate.
The v3 files remain historical source material and are not copied into the v4
runtime.

## Role boundaries

| V3 role | Decision | V4 responsibility retained | V3 behavior not retained |
|---|---|---|---|
| product-owner | ADAPT | preserve user intent; clarify requirements; define usable stories; product acceptance | direct bus publication, task design, fork-based consultation loops |
| pm | ADAPT as `project-manager` | story organization; dependency/readiness coordination; assignment proposals; throughput | central conversational routing hub, private per-story control memory, direct task dispatch |
| architect | ADAPT | repository-grounded design; invariants and boundaries; finite task DAG | implementation, actor-instance selection, annotation chatter, direct design-ready publication |
| coder | ADAPT | exact-node implementation; focused checks; evidence; genuine blocker proposals | validator join ownership, direct fan-out, peer invocation, scope or architecture expansion |
| senior-coder | ADAPT | difficult implementation and root-cause repair within an admitted node | implicit role escalation, peer invocation, validation join ownership |
| tester | ADAPT | independent functional verification against assigned criteria | redesign, implementation edits, unbounded validation matrix, direct gate-defect loops |
| security | ADAPT | security and authority-boundary review implicated by assigned scope | general architecture/product review, implementation edits, recursive gate-defect exchange |
| operator | ADAPT | authenticated human request/decision relay and explicit runtime control | operator-authored task decomposition, synthetic monitoring messages, implicit engineering decisions |

## Message-handler inventory

| V3 handler | Decision | Successor v4 mapping or disposition |
|---|---|---|
| product-owner `tekroo-spec-new` | ADAPT | `tekroo.message.feature.submitted` → `feature-intake` model handler |
| product-owner `tekroo-story-completed` | ADAPT | `tekroo.message.release.ready` → `product-acceptance` model handler |
| product-owner `tekroo-story-question` | ADAPT | one declared human/product decision node; correlated answer closes it |
| product-owner `tekroo-spec-change` | ADAPT | future explicit scope-amendment workflow node; no direct spec mutation |
| product-owner `tekroo-notification-responded` | ADAPT | generic correlated human-response continuation |
| pm `tekroo-story-new` | ADAPT | `tekroo.message.feature.refined` → `story-planning` model handler |
| pm `tekroo-design-ready` | ADAPT | deterministic acceptance of an architect result followed by declared DAG expansion |
| pm `tekroo-task-completed` | ADAPT | deterministic node completion and readiness calculation; no PM model call for bookkeeping |
| pm `tekroo-story-status` | ADAPT | deterministic workflow/status projection; explicit successor node for authorized rework |
| pm `tekroo-story-answer` and `tekroo-notification-responded` | ADAPT | correlated decision continuation at the paused node |
| architect `tekroo-design-request` | ADAPT | `tekroo.message.story.design-requested` → `design-and-decomposition` model handler |
| architect `tekroo-story-new` | RETIRE | unsolicited annotation fan-out is not required by the finite DAG |
| architect answer/notification resume handlers | ADAPT | correlated continuation of the same design node and handler identity |
| coder and senior-coder `tekroo-task-assigned` | ADAPT | `tekroo.message.task.assigned` or `.escalated` → exact implementation handler |
| coder and senior-coder `tekroo-validate-result` | ADAPT | deterministic Teams validation join; not an implementer model handler |
| coder and senior-coder `tekroo-story-new` | RETIRE | unsolicited annotation fan-out |
| coder and senior-coder answer/notification resume handlers | ADAPT | correlated continuation of the same implementation node and handler identity |
| tester and security `tekroo-validate-request` | ADAPT | distinct task review-request message types and per-role handlers |
| tester and security `tekroo-gate-defect` | RETIRE as conversation | a failed gate becomes evidence and an explicit repair/successor proposal |
| tester and security `tekroo-story-new` | RETIRE | unsolicited annotation fan-out |
| every role `tekroo-idle` | RETIRE | empty change-stream timeout is internal and creates no message or model work |
| v3 shutdown/control messages | ADAPT | deterministic lifecycle control owned by Teams; never a model handler |

## Framework and shared-skill inventory

These v3 skills were outside individual role folders but were still part of
the effective agent runtime. They are therefore classified explicitly rather
than silently disappearing:

| V3 framework/shared skill | Decision | Successor disposition |
|---|---|---|
| `tekroo-default-message` | RETIRE | an unmapped subscribed message fails closed before model execution; it is not acknowledged and discarded by a fallback model handler |
| `tekroo-ping` / `tekroo-pong` | RETIRE as messages | readiness and liveness are deterministic service/role-host observations, not agent conversation |
| `tekroo-agent-hint` | RETIRE as broadcast conversation | general guidance belongs in a signed charter or handler revision; task-specific information belongs in admitted work or bounded SMA context |
| `tekroo-shutdown` | ADAPT | authenticated lifecycle control checkpoints and stops the role through the deterministic role host |
| `tekroo-initialize` | ADAPT | bind FQN, FQRN, package identity, handler authority, checkpoint, and bounded memory context into the execution brief; do not ask the model to run an initialization skill |
| `operand-spine` | ADAPT as engineering policy | retain the general requirement to bind operation operands explicitly in execution policy and tests; do not inject its accumulated incident narrative into every role or handler |
| `tekroo-both-directions` | ADAPT as test policy | retain paired positive/negative controls where a discriminator can silently under-fire; do not make it an organizational message handler |
| `tekroo-idle` | RETIRE | an empty long-poll result returns directly to the change-stream wait loop with no model call or durable write |

## Hard-coded behavior found in current v4

- `legacyPlanningStageDefinition` contains software-role instructions and is a
  compatibility path only. Configured successor workflows must use signed role
  handlers instead.
- `BuildExecutionBrief` contains purpose-based generic execution and result
  protocol guidance. The successor path binds the selected handler and schema;
  compatibility guidance remains only for legacy invocations.
- Task routing derives eligible roles from signed capabilities and qualified
  profiles. It is generic and should remain in the substrate.
- Validation, candidate assembly, budget enforcement, DAG admission, retries,
  release mechanics, and message delivery are deterministic Teams behavior,
  not role instructions.

## Initial restored library

`config/starter-team/roles-v4/` implements the currently shipped message paths
as signed, content-addressed charters and handlers. The additional ADAPT rows
above are restored through explicit workflow nodes and deterministic handlers;
they must not be reintroduced as open-ended agent-to-agent conversations.
