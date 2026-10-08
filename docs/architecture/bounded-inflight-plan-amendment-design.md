# Bounded in-flight plan amendment — reviewed candidate, not activated

## Durable implementation plan (2026-10-07)

The principal approved a successor contract allowing **one PM-proposed,
policy-approved amendment per feature**, preserving completed work and unchanged
product requirements. This authorizes implementation, not production activation.
Contract candidate: **0.15.0**. Accepted predecessor packages remain immutable.

Implementation checkpoints:

1. Closed raw-object proposal schema and deterministic admission policy: exact
   feature/plan/specification/question bindings; unchanged design and stories;
   completed/active work preservation; successor-only affected closure; explicit
   impact ownership, narrow scopes, validation and finite budget checks.
2. Durable single-use assessment and decision receipts, including rejected
   assessments. Bind approval to separately configured policy authority and exact
   authoritative revisions; stale state cannot publish a plan.
3. Forward PM assessment after the retained-context architect consultation;
   idempotent successor materialization and checkpoint lineage; no second model
   assessment and no reset of any feature budget or deadline.
4. Qualify schema/policy disagreement, unavailable evidence, stale/racing work,
   restart at durable boundaries, rejection/second assessment, completed work
   preservation and the end-to-end execution frontier before activation.

Keep this plan current through compaction. Do not describe a completed checkpoint
as qualification of the remaining checkpoints. No live r54 mutation is permitted
as an incidental implementation test.

## Why a separate contract is needed

The current design-owner consultation is advisory: `ANSWERED_WITHIN_SCOPE`
admits a changed-condition continuation of the blocked task; `NEEDS_SCOPE_CHANGE`
does not grant a larger write scope. `RequestFeatureReplan` deliberately rejects
a graph containing completed or accepted tasks. Reusing it for an in-flight
amendment would discard the preservation guarantee. Neither a model's answer nor
a role instruction is scope authority.

The existing 0.14 candidate's single PM/architect design revision happens before
the implementation graph is materialized. It does not authorize the operation
below. Accepted packages and deployed authority grants remain unchanged.

## Implemented forward path (disabled candidate)

1. An implementation submits `needs_decision`, grounded in its immutable output.
   Its task stays blocked; accepted candidates and completed work are retained.
2. The existing single architect advisory node receives the original design
   reasoning, exact question and candidate evidence. An in-scope answer uses the
   existing bounded continuation. A scope-change answer proceeds forward to one
   PM amendment assessment, never back into a conversation loop.
3. The PM receives the accepted specification, current plan digest/version,
   architect answer, blocked invocation and task revisions, completed candidate
   SHAs, active work and remaining budgets. It proposes the smallest repair of
   execution ownership, not new product behavior or a rewritten technical design.
4. A deterministic validator checks the proposal against authoritative state.
   A separately authorized policy or human decision accepts it or rejects it.
   Producing a proposal is not that decision. Which narrowly bounded authority
   may accept such amendments is an explicit contract acceptance decision.
5. Acceptance appends a successor plan revision and successor work nodes. It
   never changes an old event, result, candidate, task definition or completed
   task. The repaired task is based on its retained candidate and consumes the
   amendment evidence. Its independent validation remains mandatory.
6. An incomplete, rejected, expired or second amendment requires the operator.
   No automatic repeat, budget reset, reopened architect stage or new question
   chain is permitted.

## Structural payload and validation requirements

Use raw JSON objects with a formal closed JSON Schema; no JSON-valued strings.
Keep the tool/result names descriptive, with schema versions in metadata only.
The amendment proposal must bind:

- Feature ID, current plan version/digest and accepted specification digest.
- Original blocked task ID, invocation ID and exact task revision.
- Architect consultation ID and immutable answer evidence digest.
- Proposed successor task descriptions, acceptance criteria and file scopes;
  explicit stable task references for dependencies and capability handoffs.
- Explicit impact records for production implementations, construction/wiring,
  existing callers and test doubles. Each record identifies inspected repository
  paths, an owning successor task, scope coverage and distinguishing verification.
- A bounded reason explaining necessity within the existing product specification.

Syntax constraints: required identities and digests; bounded nonempty strings;
bounded arrays; unique identities; enum-valued dispositions and impact kinds;
integer indices with nonnegative bounds; `additionalProperties: false` at every
fixed object. Empty collections are legal where there are no entries to report.
All cross-reference/range/state checks also run after schema validation.

Semantic admission must enforce:

- One amendment assessment/acceptance allowance per feature, debited from the
  existing finite global, purpose, attempt, hop and deadline accounts. Exhausted
  budgets require an operator decision, not an implicit extension.
- Exact expected revisions and source digests; registered available evidence;
  idempotence across restart. Old rejections stay immutable.
- No deletion, rewriting, cancellation or implicit rerun of completed work.
  No alteration to accepted product requirements or release authority.
- No change to an active invocation's scope. Admit successor work only after its
  predecessor is terminal; fence overlapping active writers before admission.
- DAG acyclicity and closed handoffs across both retained and successor nodes.
  A consumer's required capability must come from its declared upstream owner.
- Every declared impact has an owner, covered paths and verification. Interface
  compatibility is checked at the relevant provider/consumer boundary, while
  whole-feature validation proves integrated wiring and behavior.
- No broad `.` scope when narrower inspected paths suffice; no privilege growth
  merely because the model reports a missing capability.

These checks prove consistency of the declared plan, not completeness of model
discovery. Repository inspection, compiler compatibility checks and independent
engineering validation remain necessary; a schema cannot prove that an omitted
implementer does not exist.

## Required qualification before activation

Create a successor contract candidate rather than modifying accepted packages.
Qualify real kernel/Mongo admission, signed role/result bindings, retained-context
forks, restart at each durable boundary, stale proposals, duplicate/rejected
commands, completed-work preservation, overlapping writers, budget exhaustion,
an in-scope answer with no PM call, one successful amendment, and a second scope
decision that escalates without another model call. Obtain explicit contract
acceptance and deployment authorization before activating this authority.

## Implementation and qualification receipts

OBSERVED in the working tree: the domain schema/validator and single-use ledger,
Mongo transaction boundary, optional acceptance-pinned activation, forward PM
assessment reconciliation, retained-context native result binding, private dirty
workspace checkpoints, fresh-only materialization and separately fenced plan
publication are implemented. The kernel command catalogue remains 0.12.0;
0.15.0 adds an organizational contract and uses existing kernel commands. It
does not introduce a version-numbered tool or alter an accepted package.

Qualification completed in isolated fixtures:

- `organization/plan_amendment_test.go`: preservation, independent policy,
  rejection/one-use replay, fresh-state revalidation, closed schema, task/attempt/
  purpose/hop/deadline limits, scope/impact/dependency/handoff checks and active
  writer overlap. These are deterministic declared-plan checks, not proof of
  repository impact completeness.
- `adapters/mongo/plan_amendment_store_integration_test.go`: real replica-set
  transactions, full-budget debit fencing even without a policy revision change,
  restart, generic-CAS bypass rejection, pinned assessment/input/source evidence,
  successor publication and failed assessment without an output. These tests
  seed projection fixtures; they are not a full kernel/model execution.
- `adapters/operationalruntime/plan_amendment_integration_test.go`: a real native
  service and kernel/Mongo preparation followed by service close/reopen. Fresh
  tasks remain unassigned PLANNED nodes, no invocation or dispatchable message
  is created before publication, retained task state/head are unchanged, and
  budget/replay are unchanged. The test supplies an approved-plan fixture to
  isolate preparation; it does not qualify automatic PM dispatch/publication.
- `adapters/nativeagent/plan_amendment_test.go`: mock HTTP at the real native
  boundary receives retained reasoning and raw nested admitted input; the
  formal response grammar accepts a raw 0.15.0 proposal and rejects an encoded
  object string. Journal replay performs one model request, not another
  assessment. This is protocol qualification, not a live-model outcome.
- `adapters/operationalruntime/plan_amendment_checkpoint_test.go`: source Git
  HEAD/index/status unchanged; dirty bytes, deletions and executable mode
  preserved in a private checkpoint and isolated successor; ignored artifacts
  and hooks excluded; restart replay immutable; tampering/symlinks rejected.
- Configuration and successor-evidence tests: exact frozen schema/manifest and
  independently pinned HUMAN acceptance required; unavailable or changed
  evidence rejected; retained-task evidence untouched; successor references
  deduplicated by evidence identity.
- `adapters/operationalruntime/plan_amendment_rejection_integration_test.go`:
  the actual reconciler and Mongo transactions retain distinct malformed,
  failed-without-output and expired decisions. A reopened store and repeated
  reconciliation preserve the decision, original plan and invocation count.
  Dispatch remains frozen and no second assessment is admitted. These receipts
  are exposed in the feature's amendment ledger; there is no separate alert
  channel in this change. Terminal source projections are seeded explicitly,
  so this does not qualify their preceding kernel/model execution.
- `adapters/operationalruntime/reconciliation_fault_isolation_test.go` and
  `task_consultation_integration_test.go`: known unchanged profile-bind rejection
  remains observable while unrelated terminal findings are retained. Pending
  question evidence is excluded from completion/dependency readiness; unknown
  outcomes fail closed; terminal-only reconciliation creates no repair/glitch
  invocation. Real Mongo/kernel tests cover four-reference profile binding,
  restart/idempotence, a negative finding and malformed promotion output.
  Terminal invocation bindings for the finding cases are fixture projections.
  Cross-feature error aggregation and cancellation are unit-qualified, not a
  multi-feature model-driven run.

- `adapters/operationalruntime/plan_amendment_forward_integration_test.go`:
  `TestAmendmentForwardCoordinatorNativeAssessmentAndPublicationAcrossRestart`
  passes with `mongo_integration` and `-race` (final receipt: 136.17s test,
  140.721s package, `OUTPUT/plan-amendment-015/forward-current.jsonl`).
  The real kernel/native worker produces source architecture, source PM output,
  blocked implementation, architect answer and one PM assessment using a local
  canned HTTP model. No terminal/APPROVED/APPLIED projection is inserted. The
  service performs independent policy approval, private checkpoint, publication
  and normal successor authorization, with close/reopen at reservation,
  assessment admission, terminal assessment, preparation, publication and
  successor admission. Completed state/head and the original source Git
  HEAD/index/status are unchanged. The successor receives the latest unstaged
  checkpoint bytes and exact invocation/output/policy provenance. Five model
  requests consume five debits; successor authorization consumes the sixth,
  without executing a sixth model request. Replay changes neither invocation
  identity nor budget/deadline, and the validator waits for a candidate.
  A locally changed amendment policy binding is rejected without a budget
  mutation; a checkpoint label alone cannot bypass candidate lineage checks.
  Accepted intake/context, initial planning nodes, dirty checkpoint bytes and
  signed minimal legacy role packages are explicit fixture inputs. This is an
  integrated harness qualification, not a current production-role or live-model
  feature qualification. Existing native component tests separately cover raw
  nested input and formal grammar on the HTTP wire.
- `adapters/nativeagent/protocol_prompt_binding_test.go`: exact server-resolved
  protocol prompt inspection and replay pass with `-race`. Session creation and
  inspection share one constructor. Changed prompt, contract, raw input,
  invocation or sequence is rejected; strict binding is not disabled globally.
  The isolated forward test exposed the prior mismatch: a valid protocol-only
  wrapper was generated by the worker but rejected during boundary inspection.
- The extended frontier exposed a second harness mismatch: candidate lineage
  treated an explicitly preserved blocked checkpoint as a live candidate
  producer, absent from the successor DAG. The new path validates its exact
  amendment/checkpoint identity before excluding it from live readiness checks;
  private Git identity remains checked. Ordinary candidate checks are unchanged.

The normative coverage review is complete for the organizational amendment
boundary. Freeze this reviewed implementation as `FROZEN_CANDIDATE`, with
production authority still `NONE`. Activation requires a separate principal
acceptance binding the exact frozen manifest and administrator-owned policy;
implementation approval is not that receipt. A current production-role
live-model feature qualification is a separate experiment and has not been run
for this candidate; the canned model cannot establish model decision quality.

At the end of implementation qualification, no production acceptance/configuration,
commit, deployment, live r54 recovery, retry or implementation steering had been
performed. Production authority remains `NONE`; ordinary deployments
with no optional amendment configuration retain existing behavior.

The isolated forward fixture above closes the automatic coordinator/frontier
gap without seeding APPROVED/APPLIED state. A real-model experiment still needs
separate activation/deployment authority.

Test caveat: one broad ordinary-suite run failed in `operationalruntime` while
concurrent race tests were running. Its combined output was truncated before
the individual failing-test receipt was retained. The package rerun and a full
JSON-output rerun passed; the original cause is **unverified**, not attributed
to load or dismissed as an unrelated failure. Keep that distinction when
reporting qualification. Subsequent broad receipts are retained under ignored
`OUTPUT/plan-amendment-015/full-suite.jsonl`.
During extended frontier qualification another broad run failed in the same
package with the individual receipt again truncated; its cause remains
unverified. An immediately subsequent package JSON-output run passed. Current
full JSON-output receipts are retained in
`OUTPUT/plan-amendment-015/full-suite-current.jsonl`; do not erase the failed-run
caveat merely because a rerun passes.
That logged full-suite rerun passes, with no failure events. Five consecutive
ordinary operationalruntime repetitions also pass (58.425s package receipt,
`OUTPUT/plan-amendment-015/operationalruntime-repeat.jsonl`). These establish the
recorded runs' outcomes, not the cause of the earlier intermittent failure.

## Review follow-up (2026-10-08)

OBSERVED: adding completed-journal inspection after actual publication reproduced
`native agent invocation binding changed or is invalid` at the historical
consultation inspection. The old resolver enumerated only the current plan,
whose version and task IDs changed, and the amendment resolver accepted only an
ASSESSING record. Its PM source lookup also compared against the successor's
assessment execution rather than the original PM execution.

The reviewed implementation reconstructs the exact recorded consultation and
assessment from the retained prior plan and recorded invocation identities.
Terminal feature states are included in session reconstruction; new amendment
admission still requires PLANNED and its single-use reservation. The immutable
admitted input and original source execution remain bound, and a changed
invocation identity is rejected. This uses the existing bounded feature lookup
(1000 results); it is not a claim of unlimited historical lookup across every
feature ever retained.

Qualification receipts:

- `OUTPUT/plan-amendment-015/forward-reviewed.jsonl`: the forward case passes
  with `-race` (137.21s). It inspects both completed journals after publication,
  rejects different invocation identities, and verifies the used assessment
  reservation remains unchanged for another question. This last check is a
  service-boundary allowance test, not a second model-driven question chain.
- `OUTPUT/plan-amendment-015/in-scope-final.jsonl`: the in-scope case passes
  with `-race` (16.60s). It authorizes the existing-workspace continuation without
  a PM assessment or scope expansion; four model calls occurred (source design,
  source PM, blocked implementation, architect answer), not a fifth assessment.
  The preceding fixture failures are retained: it initially omitted the existing
  operator budget-amend/policy-unblock grants, then fed a stale invocation back
  after successor admission. Production already has those grants; neither
  production authorization nor the existing recovery mechanism was changed.
  This is the pre-existing in-scope recovery path, which may append its bounded
  recovery-budget revision; it is not the deadline-preserving amendment path.
- `OUTPUT/plan-amendment-015/full-suite-reviewed.jsonl`: all 31 ordinary package
  results pass, with no failure events. Nativeagent, organization and
  operationalruntime also pass race tests (`review-race.txt`).

These receipts qualify the isolated harness and its supplied signed role fixtures,
not the current production-role/live model. The next experiment requires the
exact independent activation receipt and a fresh isolated feature run, preserving
r54 rather than reusing its stalled execution.
