# Multi-agent delegation and memory-handoff guidance

Date: 2026-08-15
Status: `OPERATIONAL_GUIDANCE_FOR_CONTRACT_0.7.0`
Contract authority: accepted `tekroo.kernel.contracts/0.7.0`
Production authority created by this note: **NONE**

## 1. Authority, purpose, and non-effects

This note translates the essential operational lessons from Anthropic's
“How we built our multi-agent research system” into provider-neutral Teams v4
practice. It uses the accepted 0.7.0 contract and the existing SMA Programming
Memory Adapter (PMA) boundary. It is implementation guidance, not a normative
contract amendment.

This note does **not**:

- modify or supersede `CONTRACTS/tekroo.kernel.contracts/0.7.0/`;
- create a new aggregate, command, event, lifecycle state, authority, or model
  route;
- authorize production deployment, live model execution, live evidence
  registration, or an SMA lifecycle change;
- make SMA responsible for planning, delegation, budgets, acceptance, or
  organizational truth; or
- interrupt or expand the SMA Stage 6 package.

Teams remains authoritative for planning, decomposition, task DAGs,
assignment, execution, authority, budgets, parallelism, validation, completion,
and organizational truth. SMA remains semantic memory. AMW contains one
agent's bounded task-local working knowledge. SPWS exposes current shared
coordination state without becoming institutional memory. Large reports, code,
datasets, traces, and other substantive outputs remain external artifacts.

## 2. Evidence floor and source calibration

### 2.1 Inspected sources

**OBSERVED:** this review inspected:

- the accepted 0.7.0 manifest, catalogue, aggregate-state, work-profile,
  EvidenceRecord, team-continuity, and command/event payload schemas;
- task creation, work-profile binding, qualified assignment, ownership,
  handoff, completion request, completion review, variant-group, escalation,
  evidence, lifecycle-epoch, execution-fence, and continuity surfaces;
- `docs/architecture/020-model-capability-and-verification-policy.md`;
- `docs/architecture/022-operator-and-host-continuity-control.md`;
- SMA's `docs/PROGRAMMING_MEMORY_ADAPTER_AND_CONTEXT_PROTOCOL.md` and
  `docs/OPENHANDS_SMA_INTEGRATION_PLAN.md`; and
- Anthropic's article directly at
  <https://www.anthropic.com/engineering/multi-agent-research-system>, accessed
  2026-08-15.

**OBSERVED:** the Anthropic system uses an orchestrator-worker pattern. Its
reported practical lessons include precise delegation briefs, effort scaled to
task complexity, deliberate tool selection, selective parallelism, checkpoints
and resumption, production tracing, outcome-oriented evaluation, and workers
writing durable artifacts rather than forcing all substantive output through
the coordinator.

**OBSERVED:** Anthropic reports that multi-agent systems were especially useful
for breadth-first research with independent directions, but used substantially
more tokens and were a poor fit for work requiring shared context or many
inter-agent dependencies. The article explicitly identifies many coding tasks
as less parallelizable than research tasks.

**INFERRED:** Anthropic's quantitative internal results are evidence about its
research system, not transferable Teams thresholds. Teams should adopt the
operational lessons while continuing to derive fan-out, cost, and acceptance
limits from its own work profile, qualification evidence, and measured
outcomes.

### 2.2 Surfaces not inspected

This documentation task did not inspect private model prompts, live Teams or
SMA service state, production telemetry, live AMW/SPWS collections, provider
usage records, or the mutable implementation inside the active SMA Stage 6
package. No claim in this note depends on those surfaces being operational.

The Teams worktree contained substantial pre-existing modified and untracked
Step 15 and event-export work. This task does not interpret, alter, stage, or
claim ownership of that work.

## 3. Existing-contract mapping

The mappings below cite exact 0.7.0 fields. A behavior described as policy is
not misrepresented as a first-class kernel enum.

| Operational behavior | Existing binding | Use |
|---|---|---|
| Objective and scope | `tekroo.command.task.create` fields `title`, `description` | State the bounded objective, inclusions, exclusions, and artifact destination in the task description. |
| Acceptance | task-create `acceptance_criteria`; work-profile `acceptance_criteria_digest`; completion-review branch `acceptance_criteria` | Freeze criteria, bind their digest to the work profile, and evaluate the immutable candidate against explicit branches. |
| DAG dependencies | task-create `depends_on` | Represent decomposition and joins as explicit task nodes; never hide fan-out in a prompt. |
| Work classification | work-profile `work_kind`, `ambiguity`, `novelty`, `blast_radius`, `security_sensitivity` | Determine whether decomposition, investigation, isolation, or human authority is required. |
| Reasoning route | `minimum_decision_route`; qualified-assignment `required_decision_route` and `selected_decision_route` | Bind the lowest qualified route; model self-confidence cannot change it. |
| Verification | `required_deterministic_gate_ids`, `required_validation_branches`, `required_independence_dimensions`, `verification_topology_digest`, `valid_candidate_quorum` | Freeze deterministic gates and the required review topology before execution. |
| Parallel variants | work-profile `implementation_variant_count`; variant-group `candidate_count`, `valid_candidate_quorum`, independence fields, comparator, adjudicator, deadlines, and replacement budget | Use independent variants only through the existing isolation, submission, comparison, and selection contract. |
| Effort limits | work-profile `budgets.attempt_limit`, `review_round_limit`, `promotion_limit`, `escalation_limit`, `deadline_at` | Bound attempts and organizational cycles. Task expansion consumes these budgets rather than resetting them. |
| Assignment authority | qualified-assignment `assignment_id`, task/work-profile identity, selected actor/execution/fencing identity, model/runtime/qualification digests, policy digest, hard-constraint results, reasons, and `evidence_ids` | Make the worker and its permitted route auditable. A delegation artifact becomes an input through `evidence_ids`; prose alone grants no authority. |
| Artifact/evidence identity | EvidenceRecord `evidence_id`, immutable `locator`, `raw_sha256`, `canonical_digest`, `byte_length`, media/provenance/source fields, integrity state, retention and access partition | Keep large output external while retaining exact identity, lineage, integrity, availability, and policy boundaries. |
| Worker submission | variant candidate `artifact_digest`, `changed_file_inventory_digest`, `deterministic_gate_receipt_ids`, `assumptions`, `unresolved_exceptions`, identity and isolation digests; task completion `artifact_digests`, `evidence_ids`, `unresolved_exceptions` | Return content-addressed artifacts and receipts, not an unverifiable narrative assertion. |
| Ownership and handoff | aggregate `ownership.owner_fqn`, `ownership_version`, `assigned_event_id`; task handoff `prior_owner_fqn`, `new_owner_fqn`, `expected_ownership_version`, `reason` | Transfer durable ownership with optimistic versioning. The handoff command does not itself carry arbitrary artifact fields; the successor assignment binds the handoff EvidenceRecord through its `evidence_ids`. |
| Lifecycle and fencing | aggregate `lifecycle_epoch` and `scope_revision`; qualified-assignment `selected_execution_id`, `selected_fencing_epoch`, `runtime_identity_digest` | Replace a process or context without changing actor identity, while preventing stale execution output from acquiring authority. |
| Host continuity | system continuity `control_state`, `power_epoch`, `admission_open`, policy/health digests, `unresolved_execution_ids`, and `last_transition_event_id` | Quiesce, reconcile, and resume without treating a remote result or process restart as organizational truth. |
| Validation and final state | completion-review immutable candidate/evidence/topology fields; branch validators, methods, deadlines and independence; branch results, findings and evidence; `ALL_PASS` join; completion request `validation_finalized_event_id`, artifact/evidence IDs and exceptions | Evaluate correct outcomes and required safeguards while permitting multiple valid execution trajectories. |
| Bounded escalation | escalation trigger, condition digest, evidence, causal path, adjudicator, owner, deadline, round limit, route limit, and timeout policy | Stop loops and route genuine ambiguity or exhaustion through an explicit finite decision. |

### 3.1 Contract sufficiency decision

**COMPUTED:** every essential behavior in this note has an existing binding
through task/DAG state, work profiles, qualified assignments, EvidenceRecords,
variant groups, completion review, ownership/fencing, continuity, or PMA
working-memory mechanisms.

`SINGLE_OWNER`, `PARALLEL_DECOMPOSITION`, and `INDEPENDENT_VARIANTS` are
operational decision classifications. The delegation brief and compact handoff
are content-addressed execution artifacts registered as evidence. None needs to
become a new kernel aggregate or command.

**INFERRED:** no successor to contract 0.7.0 is required for this guidance.
Implementations must nevertheless enforce the policy-to-field mappings; the
presence of a field alone is not proof that a live adapter enforces it.

## 4. Deterministic delegation-brief derivation

### 4.1 Canonical brief

Before model-backed execution, the coordinator derives one canonical delegation
brief from committed organizational records and registered evidence. Its
logical content is:

```text
delegation_brief_version
task_id, lifecycle_epoch, scope_revision, expected_task_revision
objective
scope, exclusions
acceptance_criteria, acceptance_criteria_digest
depends_on
required_inputs[{evidence_id, locator, raw_sha256, canonical_digest?}]
work_profile{profile_id, revision, digest}
required_decision_route
tool_and_source_policy_refs
verification{deterministic_gate_ids, branches, independence, topology_digest}
artifact_destination
evidence_expectations
budget{attempt_limit, review_round_limit, promotion_limit,
       escalation_limit, deadline_at}
stop_conditions
handoff_expectations
derivation_policy_digest
```

The fields derive as follows:

- objective, scope, exclusions, acceptance criteria, dependencies, and artifact
  destination derive from the current task revision and referenced policy;
- route, gates, independence, variants, and budgets derive from the current
  work profile;
- tools, sources, evidence expectations, stop conditions, and handoff format
  derive from content-addressed operational policies referenced by evidence;
- the complete canonical brief receives a byte digest and immutable external
  locator; and
- its EvidenceRecord ID is included in the qualified assignment's
  `evidence_ids`, alongside classification and hard-constraint evidence.

Derivation fails closed on missing required input, digest mismatch, stale
lifecycle/scope/task revision, expired deadline, incompatible route, missing
qualification, or unsatisfied hard constraint. A worker may propose a revised
scope or new DAG node, but it may not silently alter the brief.

### 4.2 Output and stopping contract

The brief tells the worker what durable artifact and evidence are expected, not
which private chain of thought to produce. The worker stops when one of these
conditions occurs:

1. the acceptance-ready artifact and required evidence are persisted;
2. a declared blocker or policy trigger is supported by evidence;
3. the attempt or absolute deadline budget is exhausted;
4. a scope, authority, security, or execution-fence mismatch is observed; or
5. handoff is required under the frozen policy.

Stopping on sufficient evidence is as important as continuing on a genuine
gap. Repetition without changed-condition evidence consumes the existing
attempt budget and cannot become an unbounded research or coding loop.

## 5. Selective parallelism decision

### 5.1 Decision procedure

The planning policy records exactly one classification for each proposed work
revision:

- `SINGLE_OWNER`
- `PARALLEL_DECOMPOSITION`
- `INDEPENDENT_VARIANTS`

The classification, reasons, dependency analysis, mutable-state analysis, join
method, and policy digest are retained as evidence used by the work profile and
qualified assignments.

Choose `SINGLE_OWNER` when any of these holds:

- work shares mutable files, symbols, environment, datastore state, or an
  evolving hypothesis that cannot be isolated safely;
- one subtask's specification materially depends on another's unfinished
  result;
- workers require the same large, changing context;
- independently useful artifact boundaries cannot be named;
- no deterministic or prospectively adjudicated join exists; or
- available attempt, deadline, review, or token/compute budget cannot afford
  fan-out.

Choose `PARALLEL_DECOMPOSITION` only when all of these hold:

- each subtask has an explicit task node, owner, inputs, outputs, acceptance
  criteria, artifact destination, and dependency edges;
- writable state is disjoint or safely leased;
- each output remains useful and reviewable independently;
- a deterministic or authorized adjudication join is declared before work;
- failure and cancellation of one node have a declared effect on siblings; and
- fan-out fits the parent work profile's existing budgets.

Choose `INDEPENDENT_VARIANTS` only when the work profile requires more than one
implementation variant and the existing variant-group contract freezes the
common input set, isolation dimensions, candidate/quorum counts, comparator,
materiality policy, adjudicator, deadlines, and replacement budget. Variants
must not see one another's candidate, patch, or private reasoning before
immutable submission. Agreement is corroboration, not acceptance.

### 5.2 DAG and budget rules

Parallel decomposition creates explicit DAG nodes using task `depends_on`.
Agents cannot create hidden subwork, widen scope, or increase fan-out only in a
prompt. Any expansion must consume the applicable planning/attempt/deadline
budget and pass the same work-profile and qualified-assignment gates.

The coordinator may start independent ready nodes concurrently. It must not
start a dependent node merely because an upstream worker reported conversational
progress. The required upstream artifact or accepted event must satisfy the
declared dependency.

## 6. Artifact-first execution and durable handoff

### 6.1 Worker output

Workers persist substantive code, reports, datasets, decisions, fixtures, and
test output directly at their declared artifact destination. They register or
propose EvidenceRecords containing immutable locators, digests, sizes,
provenance, access partition, integrity, and retention data. Their lightweight
return to the coordinator contains:

- task, actor, execution, fence, work-profile, and lifecycle identities;
- artifact digest(s) and EvidenceRecord IDs;
- deterministic-gate and validation status;
- assumptions and unresolved exceptions;
- the terminal result or next authorized action; and
- resource observations when available.

The coordinator must not become the only copy of a worker artifact or rewrite
the complete artifact through successive conversational summaries. A summary
is navigation; the immutable artifact is the source.

### 6.2 Compact handoff artifact

Before task handoff or context replacement, the current execution persists a
content-addressed compact handoff containing:

```text
handoff_version
task_id, lifecycle_epoch, scope_revision, ownership_version
prior_owner_fqn, prior_execution_id, prior_fencing_epoch
current_plan
completed_milestones
artifact_and_evidence_refs
validation_status
assumptions
unresolved_questions_and_blockers
next_safe_action
applicability{branch, baseline, head, paths, symbols, policy revisions}
handoff_reason
```

The artifact is an AMW `handoff` entry and, when it must survive execution or
agent replacement, an external artifact registered as an EvidenceRecord. The
task handoff command changes ownership using the expected ownership version.
Because that command's 0.7.0 payload contains only the two owners, expected
version, and reason, it must not be falsely described as carrying the artifact.
Instead, the successor's new qualified assignment binds the handoff
EvidenceRecord through `evidence_ids` after the ownership/revision transition.

The successor verifies the handoff artifact digest, current task/lifecycle/scope
revision, branch/head applicability, ownership, execution fence, and referenced
artifacts before mutation. Missing or stale handoff state produces a bounded
blocker or reconstruction from authoritative artifacts; it does not authorize
guessing.

## 7. Operational state, working memory, and durable semantic memory

| Information | Owner | Lifetime and use |
|---|---|---|
| Task, DAG, assignment, authority, budgets, acceptance, execution fence | Teams | Authoritative organizational state; event-sourced and revision checked. |
| One worker's plan, hypotheses, observations, attempts, blockers, questions, and compact handoff | AMW | Mutable task-local working knowledge; closes on completion, handoff, abandonment, or branch disposal. |
| Current ownership/lease, branch, interface, blocker, review, merge, and operational condition visible across agents | SPWS | Small, versioned, expiring coordination state; not reasoning-eligible institutional memory. |
| Code, reports, datasets, traces, raw receipts | External artifact/evidence store | Immutable or revisioned source material addressed by locator and digest. |
| Durable normalized decisions, constraints, verified lessons, failures, and semantic relationships | SMA | Enter through ordinary raw intake and the existing lifecycle; may later become reasoning eligible under SMA policy. |

### 7.1 Promotion boundary

At task completion, handoff, accepted decision, review, release, reproduced
failure/fix, incident resolution, or explicit instruction, PMA may compile an
evidence episode from relevant AMW entries. It proposes atomic normalized
memories with references to source entries, artifacts, and evidence. SMA—not
Teams prompts or the worker—applies its existing intake and lifecycle.

The entire artifact, transcript, delegation brief, execution trace, or handoff
must not be ingested as one semantic memory. The durable memory contains the
minimum sufficient proposition, scope, applicability, uncertainty,
contradiction/supersession relationships, and evidence references. Durable
failures are eligible; successful completion is not a prerequisite for a useful
lesson.

Summaries and index cards improve navigation but do not replace original
artifacts or atomic memories. Model output, tool output, provider state, SMA
recall, and PMA promotion proposals remain evidence until accepted through the
applicable Teams or SMA policy.

### 7.2 Context replacement and rehydration

When a context is condensed or an execution is replaced:

1. fence the prior execution or advance the applicable context/power epoch;
2. load the current task, ownership, work profile, qualified assignment, DAG,
   budget, and continuity state from Teams;
3. verify the compact handoff and referenced artifact/EvidenceRecord digests;
4. load current AMW and authorized SPWS state, or reconstruct the minimum AMW
   from authoritative references;
5. request bounded, authorized SMA context using current project, task, branch,
   execution, topology, confidentiality, and epoch metadata;
6. re-anchor current constraints after condensation using the PMA delivery
   manifest rather than trusting a generic conversation summary; and
7. resume only after current ownership, execution fencing, admission, and
   dependencies are valid.

Temporary plans and delegation briefs remain operational working state. They
are not promoted into institutional truth merely because they were needed to
resume work.

## 8. Cost and effort governance

Anthropic's guidance supports scaling effort to value and genuine complexity,
not maximizing agent count. Teams applies that lesson through existing work
profiles and evidence:

- default to `SINGLE_OWNER` and one implementation variant;
- add decomposition only for demonstrably independent outputs;
- use independent variants only when disagreement is valuable enough to pay
  the duplication cost;
- select the least costly qualified route after capability, safety, privacy,
  tool, residency, and independence filters;
- charge task creation/fan-out, attempts, review rounds, route promotions,
  escalations, and replacements to existing finite budgets;
- record reported tokens, compute, tool calls, latency, monetary cost, local
  resource observations, rework, and escalation where available; and
- represent unavailable economic data as `NOT_REPORTED`, never zero.

The stop policy prevents a coordinator from creating many workers for a simple
task, searching indefinitely for nonexistent evidence, or sending distracting
progress chatter. Additional work requires a coverage gap, changed condition,
failed criterion, material disagreement, or accepted replanning decision.

## 9. Outcome-oriented evaluation and observability

Evaluation permits different valid trajectories. It judges the frozen final
state, required checkpoints, authority, evidence, and resource bounds rather
than prescribing one exact tool-call sequence for evaluator convenience.

The operational evaluation vector is:

1. **Task acceptance and final-state correctness:** criteria-by-criteria branch
   results, immutable candidate digest, finalized `ALL_PASS` join, and accepted
   organizational terminal state.
2. **Coverage gaps:** declared acceptance/delegation requirements lacking a
   recovered artifact, evidence record, validator result, or terminal outcome.
3. **Duplicated work:** materially overlapping delegated scope or redundant
   artifact production not justified as an accepted independent variant.
4. **Handoff loss:** required compact-handoff fields or referenced artifacts
   unavailable, mismatched, stale, or unusable after replacement.
5. **Artifact/evidence recoverability:** immutable locators resolve under
   policy and bytes verify against retained length and digest.
6. **Unauthorized disclosure:** cross-project, cross-variant, reviewer-embargo,
   confidentiality, stale-fence, or stale-epoch content exposure; the acceptable
   count is zero.
7. **Wrongly parallelized work:** state collision, dependency violation,
   inconsistent shared context, invalid join, or rework attributable to a
   parallelism decision that should have been `SINGLE_OWNER`.
8. **Effort and latency:** tokens, compute, tool calls, elapsed and critical-path
   latency, monetary cost, local resource observations, retries, and review or
   escalation cycles where reported.
9. **Recovery:** bounded time and evidence completeness after process restart,
   context replacement, condensation, suspension, or unexpected outage.
10. **Relative value:** acceptance quality, coverage, safety, latency, and total
    cost compared with a preregistered single-agent or no-memory baseline.

Operational tracing should retain content-free decision structure: task and
execution identities, DAG edges, delegation/fan-out decisions, tool classes,
attempts, state transitions, timing, artifact/evidence IDs, validation outcomes,
and stop reasons. It need not retain private reasoning or unrestricted
conversation content. This is sufficient to identify loops, duplicated work,
bad tool selection, bottlenecks, and dependency stalls while preserving the
evidence/content boundary.

## 10. Coordinator implementation checklist

For each task revision, the coordinator must:

1. verify the task and work profile are current and internally consistent;
2. classify parallelism deterministically and persist the decision evidence;
3. create explicit dependency nodes for any decomposition;
4. derive and register the canonical delegation brief;
5. authorize only qualified assignments that bind that brief and all required
   evidence;
6. enforce workspace, context, actor, execution, and variant isolation;
7. receive content-addressed worker artifacts and lightweight references;
8. stop or escalate on the frozen conditions and finite budgets;
9. verify compact handoff before replacement or ownership transfer;
10. validate the immutable candidate through the frozen topology; and
11. promote only atomic durable lessons through PMA/SMA, never operational
    transcripts as organizational memory.

## Appendix A — SMA Stage 6 adoption guidance

### A.1 Relevance

This guidance is directly relevant to PMA Stage 6 canary and promotion rollout
because that stage compares task outcomes and context cost against a no-memory
baseline before widening visibility. It requires no SMA lifecycle state,
datastore, collection, embedding space, cognition role, or orchestration
authority.

### A.2 Existing PMA mechanisms that satisfy the design

- AMW already scopes immediate mutable knowledge to one agent, task, session,
  worktree, branch, and Git identity; it includes typed observations, attempts,
  outcomes, blockers, questions, evidence references, and `handoff` entries.
- SPWS already provides small versioned, owned, expiring coordination facts
  while explicitly remaining non-authoritative and non-institutional.
- promotion proposals already group evidence episodes, reference artifacts and
  evidence, propose normalized atomic memories, use idempotency keys, retain
  conflicts, and enter SMA through ordinary raw intake.
- context assembly already separates durable SMA memory from labelled AMW/SPWS
  working-state deltas, applies Teams identity/topology/confidentiality gates,
  and prefers empty context to unsafe or irrelevant recall.
- session delivery manifests, context epochs, snapshots, deltas, tombstones,
  and condensation re-anchoring already support context replacement without
  trusting a generic summary as exact delivery state.
- the OpenHands boundary already treats memory as untrusted additional context,
  fails open for prompt submission, and fails closed for unauthorized content.

These are specification mappings. Stage 6 must retain receipts proving the
mechanisms exercised by its exact canary identity; this note does not claim
that an uninspected live deployment has passed them.

### A.3 Stage 6 acceptance scenarios

Stage 6 should exercise, within its already authorized scope:

1. one `SINGLE_OWNER` task with AMW updates, artifact-first completion, atomic
   promotion proposals, and no unnecessary multi-agent context;
2. one genuinely independent two-node decomposition with distinct task IDs,
   worktrees, outputs, evidence, and a deterministic dependency/join;
3. one tightly coupled task rejected for parallelism and completed by one owner;
4. one independent-variant case proving context/workspace embargo until
   immutable submission and comparison;
5. one agent/context replacement that rehydrates a verified compact handoff,
   authorized artifacts, AMW/SPWS state, and bounded SMA context;
6. one missing or digest-mismatched handoff that fails closed for mutation;
7. one condensation event that advances the context epoch and re-anchors
   current constraints without treating summary text as delivery truth;
8. one artifact too large for context whose reference remains recoverable and
   whose atomic promoted meanings retain evidence lineage;
9. one failure/blocker promoted as a durable lesson without promoting the full
   execution trace;
10. one cross-project, cross-variant, stale-fence, or confidentiality request
    denied with content-free evidence;
11. one restart/resume case preserving task-local working continuity without
    changing SMA lifecycle state; and
12. paired canary tasks comparing acceptance quality, coverage, latency,
    automatic-context tokens, total reported effort, and handoff recovery
    against the no-memory baseline.

The exact recommended insertion point for an SMA handoff is immediately after
the Stage 6 bullet list in
`docs/PROGRAMMING_MEMORY_ADAPTER_AND_CONTEXT_PROTOCOL.md`, before
`## 23. Acceptance gates`. The inserted handoff should reference this note and
its receipt; it should not copy the full note into SMA or change the Stage 6
authority boundary.

Teams remains the orchestration and organizational-truth authority throughout.
SMA observes authorized references, maintains semantic-memory lifecycle, and
returns evidence; it does not delegate, schedule, budget, accept, or complete
Teams work.
