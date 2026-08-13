# Proposed model-capability and verification policy remediation

**Status:** `APPROVED DESIGN — CONTRACT 0.6.0 ACCEPTED — IMPLEMENTATION AUTHORIZED`

**Draft authority:** principal statement `Proceed as you recommend.` on
2026-08-13 authorizes this design remediation and one-at-a-time adjudication.

**Contract authority:** accepted `tekroo.kernel.contracts/0.6.0`, manifest
SHA-256 `eff818a5ea876e8be71d18d9555dd281298db6c667956602d6d7467d1c0ff9ed`.

**Implementation authority:** provider-neutral implementation and local forward
requalification authorized by principal statement `Accepted and authorized` on
2026-08-13.

**Production authority:** `NONE`

## Purpose

Tekroo v4 already treats model and provider output as evidence rather than
organizational truth. It has a directed acyclic work graph, bounded attempt
budgets, exact authority, deterministic validation joins, explicit escalation,
content-addressed evidence, execution fencing, and deterministic release.

Those controls substantially reduce the cost of model mistakes, but they do
not yet define how work is classified by reasoning risk, how an actor becomes
eligible for that work, when independent solutions are required, or when
evidence requires promotion to a stronger reasoning route. The current
assignment planner receives an unexplained `Eligible` Boolean and then selects
the least-loaded eligible actor. The escalation coordinator records an already
classified trigger but deliberately does not discover triggers. The OpenHands
investigation direction names a preferred local route and a fallback without
defining their capability relationship or selection policy.

This remediation externalizes those missing decisions before live OpenHands
execution, role-library activation, or model-based scheduling makes them costly
to change.

## Evidence classification

- **OBSERVED:** `kernel.AssignmentCandidate` contains actor identity, execution
  identity, active-assignment count, and `Eligible`; it contains no capability
  class, qualification identity, eligibility evidence, or selection rationale.
- **OBSERVED:** the assignment planner selects by active-assignment count and
  lexical FQN after upstream eligibility filtering.
- **OBSERVED:** the bounded validation contract has exact validators,
  resolution owners, criteria, evidence, deadlines, round limits, findings,
  supersession, and adjudication, but no separation-of-duty invariant prevents
  the work owner from also being a validator or adjudicator.
- **OBSERVED:** escalation triggers cover retry, validation, and handoff
  exhaustion/conflict conditions. Trigger discovery and automatic selection are
  outside the implemented escalation coordinator.
- **OBSERVED:** v4 preserves model changes as execution provenance rather than
  actor identity and binds an opaque runtime-identity digest to each execution.
- **OBSERVED:** no checked-in design defines a provider-neutral capability
  ladder, N-version implementation group, model-risk work profile, or
  evidence-driven route-promotion policy.
- **INFERRED:** proceeding directly to a live provider would force these
  choices into prompts, role names, and adapter configuration, where they would
  be difficult to audit, replay, or enforce consistently.

## Boundary

The organizational kernel must remain provider-neutral. It must not contain
vendor names, commercial product names, model identifiers, token prices, or
provider-specific confidence fields.

The kernel may own durable provider-neutral facts such as:

- the reasoning and execution characteristics required by a work revision;
- the verification and independence policy required by that work;
- the policy revision and evidence used to classify it;
- the minimum qualified capability class;
- the exact qualification and runtime identities used for assignment;
- observed disagreement or failure conditions requiring escalation; and
- whether completion satisfied the required verification topology.

Provider adapters and signed role/model profiles may map those abstract facts
to concrete models, endpoints, context limits, tools, prices, and credentials.
Changing a concrete model must create a new runtime/profile identity, not a new
actor or a silent policy change.

## Decision 1 — decision-route ladder and provider-neutral boundary

**Status:** `APPROVED BY PRINCIPAL`

**Principal statement:** `Approved` on 2026-08-13.

The following names and semantics describe minimum qualified decision routes,
not claims that every model in a class is interchangeable. Capability and
deployment are orthogonal: `local`, `remote`, `open-weight`, and `proprietary`
are profile or policy facts, not reasoning tiers.

1. `DETERMINISTIC` — compiler, tests, schema validation, static analysis,
   repository queries, AST operations, Git verification, and other bounded
   non-model tooling.
2. `BOUNDED_EXECUTION` — qualified model route for bounded implementation with
   an explicit specification and deterministic acceptance path.
3. `COMPLEX_REASONING` — qualified stronger route for investigation, diagnosis,
   review, decomposition, and bounded architectural comparison.
4. `NOVEL_REASONING` — the strongest separately qualified available route for
   novel, ambiguous, cross-domain, high-impact, or repeatedly unresolved
   decisions.
5. `HUMAN_REQUIRED` — no model route has authority to resolve the named
   question under current policy and evidence.

The ladder is monotonic only with respect to the policy's minimum qualification
claim. A higher route cannot bypass required deterministic gates, evidence,
authority, isolation, or review. A task does not begin at a high tier merely
because budget is available; promotion requires policy or evidence. A concrete
profile additionally declares deployment location, weight availability,
provider, data-residency eligibility, tool support, context bound, cost class,
and qualification digest. Those facts may constrain selection without changing
the required reasoning tier.

## Decision 2 — mandatory work-risk profile and decomposition

**Status:** `APPROVED BY PRINCIPAL`

**Principal statement:** `Approved` on 2026-08-13.

Before assignment, each executable task revision should bind a versioned,
content-addressed profile with at least:

- work kind: `INVESTIGATION`, `DESIGN`, `IMPLEMENTATION`, `DEBUGGING`,
  `VALIDATION`, `SECURITY_REVIEW`, `RELEASE`, or an explicitly versioned
  extension;
- ambiguity: `LOW`, `MEDIUM`, `HIGH`, or `UNKNOWN`;
- novelty: `ROUTINE`, `UNFAMILIAR`, `NOVEL`, or `UNKNOWN`;
- blast radius: `LOCAL`, `MULTI_COMPONENT`, `ARCHITECTURAL`,
  `EXTERNAL_EFFECT`, or `UNKNOWN`;
- security sensitivity: `ORDINARY`, `SENSITIVE`, `CRITICAL`, or `UNKNOWN`;
- minimum capability class;
- required deterministic gates;
- required independent validation branches;
- implementation-variant count and comparison rule;
- separation-of-duty rule;
- finite attempt, review, and escalation budgets;
- fallback and promotion policy revision; and
- classification evidence and accountable policy authority.

An absent, stale, unqualified, or internally contradictory profile makes the
task ineligible for live model execution. It cannot silently fall back to the
cheapest or currently available model. The minimum route is derived by a
versioned policy matrix rather than selected ad hoc by the work actor.

### Binding decomposition and reclassification rules

The DAG is already the correct structural representation. This remediation
adds prospective policy over its construction:

- `HIGH` or `UNKNOWN` ambiguity begins with a read-only investigation or
  design-decision node, not an implementation node.
- `ARCHITECTURAL`, `EXTERNAL_EFFECT`, `SENSITIVE`, and `CRITICAL` work requires
  explicit decision and validation predecessors before implementation or
  release.
- `NOVEL` or materially cross-domain work begins at `NOVEL_REASONING` unless
  policy routes it to `HUMAN_REQUIRED`.
- Routine, low-ambiguity, local work with explicit acceptance criteria may
  proceed through `BOUNDED_EXECUTION`.
- A task must have bounded scope, explicit acceptance criteria, named inputs,
  declared assumptions or unknowns, and an executable or adjudicable terminal
  condition.
- Task expansion consumes the existing multidimensional policy budget and
  creates explicit successor nodes; a model cannot grow scope silently.
- Replanning must produce changed-condition evidence, an executable milestone,
  an accepted decomposition, a verified blocker, an authorized deferral, or a
  terminal rejection.
- If execution discovers understated ambiguity, novelty, blast radius, or
  security sensitivity, further mutation stops. The actor submits evidence and
  requests policy reclassification; it cannot self-promote or expand scope
  silently.
- The profile is immutable for its exact task revision. Reclassification
  creates an explicit new revision or successor with causal lineage.
- Risk may be lowered only by an evidence-backed policy decision, never by a
  model assertion or merely to obtain a cheaper route.

## Decision 3 — separation of duty and independence

**Status:** `APPROVED BY PRINCIPAL`

**Principal statement:** `Approved` on 2026-08-13.

For work covered by this policy:

- an implementation owner or execution cannot be the sole validator or
  adjudicator of its own output;
- at least one required validation branch uses a different exact principal;
- another execution of the same durable FQN does not satisfy actor
  independence;
- deterministic tools remain independent evidence producers even when invoked
  by an agent, but running them does not make the implementer an independent
  reviewer;
- a reviewer must receive the frozen requirements, relevant evidence, and
  immutable candidate artifact identity, not the implementer's mutable worktree
  or private reasoning;
- policy may require distinct actors, executions, model profiles, or provider
  families according to risk;
- organizational completion still requires a finalized all-pass join and
  cannot be inferred from agreement alone; and
- a stronger model does not waive separation of duties.

Every required relationship declares the exact independence dimensions it must
prove: `PRINCIPAL`, `ACTOR`, `EXECUTION`, `CONTEXT`, `WORKSPACE`,
`MODEL_PROFILE`, `PROVIDER`, `METHOD`, and `HUMAN`. The dimensions are
cumulative policy requirements. A receipt cannot claim merely `independent`
without naming and proving the applicable dimensions.

The minimum topology for routine model-executed implementation is a distinct
validating principal and actor, a distinct execution, independently
reconstructed context, an immutable candidate artifact or isolated workspace,
and deterministic gates. Multi-component or complex work additionally requires
at least two materially different validation methods and an adjudicator distinct
from the implementer. Architectural or external-effect work separates design
authority, implementer, reviewer, and any conflict/exception adjudicator.
Sensitive or critical work adds an independent security reviewer and specialized
deterministic scanners; that reviewer is neither the implementer nor the
ordinary completion adjudicator.

Different model or provider profiles are not universally mandatory. Policy may
require them for high-risk or N-version work, but diversity alone is not proof
of correctness. Two FQNs sharing mutable state or copied private reasoning do
not prove full independence. A different model shown the first solution is a
reviewer, not an independent solution variant.

A human may resolve an explicit, evidence-backed exception but cannot
retroactively claim missing independence existed. If the required topology
cannot be proven, the result is `INCONCLUSIVE`, `BLOCKED`, or
`HUMAN_REQUIRED`, never `PASS`.

## Decision 5 — N-version isolation, comparison, and selection

**Status:** `APPROVED BY PRINCIPAL`

**Principal statement:** `Approved` on 2026-08-13.

N-version work is optional and risk-driven, not a universal duplication tax;
the default implementation-variant count is one. It is appropriate when
disagreement materially improves detection for consequential architecture,
ambiguous competing interpretations, security-critical algorithms,
concurrency/recovery/migration/distributed-state changes, difficult-to-reverse
blast radius, or failures left unexplained after bounded investigation. For
architectural uncertainty, independent design proposals precede any decision to
duplicate full implementation.

When N-version work is required:

1. one frozen task/lifecycle revision, work-risk profile, policy revision,
   input-evidence set, base commit/tree, toolchain, dependency lock, acceptance
   and gate manifest, candidate count, valid-candidate quorum, independence
   dimensions, comparison method, materiality rules, budgets, comparator, and
   adjudicator define a variant group;
2. two or more exact actors receive isolated executions and worktrees;
3. no variant sees another variant's patch, reasoning, or intermediate result
   before submitting its immutable candidate receipt;
4. each candidate records its patch/tree digest, changed-file inventory, tests
   and deterministic gates, assumptions, unresolved exceptions, affected
   contracts/interfaces, execution/profile/runtime identities, and available
   usage, latency, and cost;
5. comparison first applies deterministic eligibility, build, test, schema,
   static-analysis, API, migration, and artifact checks;
6. structured behavioral/architectural comparison then classifies
   `MATERIAL_AGREEMENT`, `NON_MATERIAL_VARIATION`, `MATERIAL_DISAGREEMENT`,
   `INSUFFICIENT_VALID_CANDIDATES`, or `INCONCLUSIVE`; and
7. material disagreement opens the Decision 4 bounded escalation. It is never
   averaged or resolved by selecting the first completion.

Different actor FQNs, fenced executions, isolated worktrees, and independent
context are mandatory. Model-profile or provider diversity is optional unless
the work profile requires it. Variants receive identical frozen inputs except
when the experiment explicitly varies a declared factor.

Agreement is corroboration, not proof or organizational `PASS`. If a required
candidate fails eligibility, policy may replace it within the existing variant
budget but cannot silently lower quorum. The comparator cannot select its own
variant. The exact adjudicator may select one, reject all, require redesign,
split work, or require a human. Exactly one candidate tree may become the
selected successor. Competing patches are never blended automatically; a
synthesis is a new derivation-linked successor implementation that receives
full validation. Rejected candidates remain immutable evidence and cannot enter
synthesized merge. The selection still requires normal independent review,
synthesized-merge, completion, and release gates.

## Decision 4 — evidence-driven reclassification, promotion, and escalation

**Status:** `APPROVED BY PRINCIPAL`

**Principal statement:** `Approved` on 2026-08-13.

Reclassification means that the current work-risk profile is no longer valid.
Promotion means that a stronger qualified decision route is required.
Escalation means that an authoritative bounded decision is required. They may
occur together but are not synonyms.

Promotion should be triggered by observable conditions rather than model
self-confidence. Approved triggers are:

- retry budget exhausted;
- validation conflict, inconclusive result, budget exhaustion, or deadline;
- handoff cycle or budget exhaustion;
- requirement contradiction;
- architecture ambiguity;
- material N-version disagreement;
- assigned-route capability mismatch;
- work-risk profile breach;
- discovered blast radius above the profile;
- elevated security classification;
- inability to establish root cause within the finite investigation budget;
- required tool or qualified capability unavailable; and
- novelty reclassification.

The normative identifiers are `RETRY_EXHAUSTED`, `VALIDATION_CONFLICT`,
`VALIDATION_INCONCLUSIVE`, `VALIDATION_BUDGET_EXHAUSTED`,
`VALIDATION_DEADLINE_EXPIRED`, `HANDOFF_CYCLE_DETECTED`,
`HANDOFF_BUDGET_EXHAUSTED`, `REQUIREMENT_CONTRADICTION`,
`ARCHITECTURE_AMBIGUITY`, `MATERIAL_VARIANT_DISAGREEMENT`,
`CAPABILITY_MISMATCH`, `RISK_PROFILE_BREACH`, `BLAST_RADIUS_EXCEEDED`,
`SECURITY_CLASSIFICATION_ELEVATED`, `ROOT_CAUSE_UNRESOLVED`,
`REQUIRED_TOOL_UNAVAILABLE`, and `NOVELTY_RECLASSIFIED`.

Every trigger requires typed evidence, a deterministic or versioned
classification method, a policy revision, and an exact condition digest. A
model may submit evidence and propose reclassification, but its confidence or
unsupported difficulty assertion cannot establish or resolve a trigger. Policy
authority decides whether the evidence satisfies the trigger. An ordinary
single test failure does not promote work unless the finite attempt policy or
another approved trigger is satisfied. Material disagreement is computed from
immutable independent receipts rather than narrative summaries. A discovered
risk-profile breach stops further mutation before adjudication.

Promotion selects the lowest qualified route satisfying the reclassified
profile, although policy may jump directly to `NOVEL_REASONING` or
`HUMAN_REQUIRED`. Provider failure may select another profile only when it is
qualified for the same route and constraints; unavailability never authorizes
a capability downgrade. Promotion creates a new fenced execution or successor
decision task with explicit causal lineage. The prior execution is stopped or
fenced before new mutation authority begins. Promotion does not mutate the
current model in place, transfer ownership silently, erase evidence, reset
budgets, or alter acceptance criteria. The new route receives frozen work
state, evidence, failed hypotheses, and the unresolved question rather than
only the prior model's narrative.

Every task revision has a finite promotion count, finite attempts per route, an
absolute decision deadline, an accountable adjudicator, and a timeout policy.
Exhaustion terminates as `RESOLVED`, `SPLIT`, `BLOCKED`, or
`HUMAN_REQUIRED`; it cannot bounce indefinitely among models.

## Decision 6 — default role-to-capability allocation

**Status:** `APPROVED BY PRINCIPAL`

**Principal statement:** `Approved` on 2026-08-13.

Concrete mappings remain adapter configuration, but the default policy should
prefer:

- coordinator and routine PM synthesis: `BOUNDED_EXECUTION`, promoted to
  `COMPLEX_REASONING` for difficult decomposition, contradiction, or
  adjudication preparation;
- programmer: `BOUNDED_EXECUTION` for bounded, well-specified work, normally
  mapped to a qualified local profile;
- investigator, architect, and difficult debugger: `COMPLEX_REASONING`, promoted
  to `NOVEL_REASONING` on evidence;
- localized known-failure debugging: `BOUNDED_EXECUTION`, promoted for ambiguous
  diagnosis or bounded root-cause failure;
- reviewer: at least the qualified route required to judge the candidate's
  work-risk profile, with Decision 3 independence;
- N-version comparator: `COMPLEX_REASONING`, promoted for material architectural
  disagreement;
- security reviewer: a qualified specialized `COMPLEX_REASONING` or stronger
  profile plus deterministic scanners;
- test/harness author: `BOUNDED_EXECUTION`, promoted for complex fault models or
  distributed recovery;
- test executor, compiler, schema, AST, and Git adjudication:
  `DETERMINISTIC`; and
- escalation adjudicator: at least `COMPLEX_REASONING` and matched to the
  reclassified work profile; and
- final organizational acceptance: kernel policy over exact evidence, never a
  model role.

The policy assigns capability, not prestige. High-volume code production is
not automatically the highest reasoning tier. Expensive reasoning is
concentrated at consequential decision points.

The exact work-risk profile always overrides role defaults. Selection chooses
the least costly qualified profile satisfying route, deployment/data-residency,
tool/context, independence, latency, and budget constraints. Qualification
belongs to the exact content-addressed profile and applicable role/route corpus;
a role name, role-library assertion, related model, or other quantization cannot
substitute. A stronger route may perform lower work only under policy; a cheaper
route cannot perform higher work because it is available.

A concrete profile change creates a new execution/runtime identity without
changing actor FQN. Promotion of the same actor does not satisfy actor
independence. Local profiles are preferred when equally qualified and compliant;
external or proprietary profiles are selected because they are the least costly
qualified option satisfying the profile, not because frontier status is a
prestige default. Outage fallback requires the same route and constraints or
the work blocks/escalates. Every model-originated coordination, design, review,
and adjudication result remains evidence/proposal until its exact kernel
transition succeeds.

## Decision 7 — qualification, revocation, provenance, and economics

**Status:** `APPROVED BY PRINCIPAL`

**Principal statement:** `Approved` on 2026-08-13.

Qualification belongs to an exact deployable model profile, not to a marketing
name or model family. Its content-addressed identity records, as applicable:

- provider and endpoint class, exact model identifier and revision, and weights
  digest when available;
- quantization, inference engine and version, sampling/reasoning configuration,
  context and output bounds, and tool/structured-output surface;
- system-prompt and role-library digests;
- hardware/runtime, isolation, data-residency, and cost-schedule or local-cost
  accounting identities; and
- qualification-corpus and receipt digests.

When a hosted provider does not expose weight identity, the profile records
that limitation and pins every provider identity and configuration that is
available. It cannot claim weight-level reproducibility. Qualification is
specific to the exact profile, decision route, role or work kind, tool surface,
and runtime constraints. Benchmark rank, parameter count, vendor assertion,
model family, related model, different quantization, or one successful task is
not a substitute. A higher-route qualification covers lower-route work only
when its executed corpus actually included the lower route's requirements.

The qualification corpus is finite and preregistered. It preserves raw failures
and corrective reruns and reports `PASS`, `FAIL`, `NOT_RUN`, or `INCONCLUSIVE`.
It tests the artifact that will actually execute, including:

- instruction and current-authority precedence;
- repository navigation and exact evidence citation;
- bounded tool use, cancellation, termination, and provider failure;
- ambiguous diagnosis and recovery after a disproven hypothesis;
- multi-file correctness, regression avoidance, and contract fidelity;
- unfamiliar or novel strategy construction;
- security, isolation, secret handling, and data-residency constraints;
- context-bound and long-intent behavior;
- latency, resource, reliability, and cost observations; and
- role-specific scenarios for every eligibility claim.

Material changes to model or weights, quantization, inference engine or
configuration, prompts or role library, tools or schemas, context bounds,
provider API, relevant hardware, isolation or residency controls, corpus, or
thresholds require requalification. Policy may also impose a finite freshness
period. A material safety or regression signal suspends eligibility pending an
explicit revocation review or requalification. A prior pass remains historical
evidence; it is not future authority after its binding becomes stale or is
revoked.

Every assignment and result retains the work-profile and routing-policy
identities; required and selected routes; qualification identity; actor,
execution, and fence; model, provider, runtime, prompt, role, tool, and context
digests; base and result artifacts; selection, fallback, promotion, and
rejection reasons; required gates and independence topology; attempts,
disagreements, escalations, and outcome; and reported tokens, latency, and
monetary cost. Local execution records measured hardware, energy, occupancy,
and latency when available. Missing economic data is `NOT_REPORTED`, never
inferred as zero. Credentials, secrets, private reasoning, and raw sensitive
prompts are excluded from ordinary telemetry.

Economics is applied only after hard capability, safety, privacy, tool,
residency, and independence filters. A frozen deterministic selection policy
may choose among passing profiles using attributable prior observations and
may optimize expected total cost, including inference, latency, retries,
variants, review, escalation, and observed failure or rework. Uncontrolled
aggregates do not establish causal model superiority; controlled comparisons
may update a later policy revision. No observation changes the current frozen
profile or assignment retroactively. Cost never authorizes a capability
downgrade or omitted gate. Model self-confidence is not an economic signal
unless separately calibrated and qualified, and never substitutes for observed
behavior.

OpenHands-Q1 must therefore qualify not only provider start/stop mechanics but
also exact profile loading, capability-policy enforcement, independent
execution, promotion/failover behavior, evidence retention, and termination.

## Decision 8 — contract revision and compatibility boundary

**Status:** `APPROVED BY PRINCIPAL`

**Principal statement:** `Approved` on 2026-08-13.

The normative remediation is an immutable successor,
`tekroo.kernel.contracts/0.6.0`, with schema version `1.5.0`. Released packages
`0.1.0` through `0.5.0` remain unchanged.

The successor introduces a semantic `scope_revision` in story and task state.
It is initialized at creation and changes only when semantic scope changes or a
lifecycle is reopened with revised scope. Readiness, dispatch, ownership, and
other routine aggregate transitions do not change it. A task work-profile
binding is identified by task ID, lifecycle epoch, scope revision, profile
revision, and profile digest. Reclassification creates an explicit superseding
profile while retaining the prior profile as evidence; a routine aggregate
revision cannot make a valid profile stale or silently refresh an invalid one.

Before live model dispatch or acquisition, a durable qualified-assignment
authorization records the required and selected decision routes; exact actor,
execution, and fencing identity; exact model-profile and qualification
identities; work-profile and selection-policy digests; hard-constraint results
and evidence; and selection, fallback, or promotion rationale. The current
planner `Eligible` Boolean becomes a derived result and has no independent
authority.

Completion review binds the immutable candidate, required gates, required
validators and independence dimensions, applicable variant group, and exact
adjudicator. Result receipts prove the dimensions actually satisfied, and
finalization requires an exact match to the frozen topology.

The successor adds durable variant-group operations for opening a frozen group,
submitting immutable candidates, applying deterministic eligibility checks,
recording structured comparison and disagreement classification, and selecting
exactly one candidate or explicitly rejecting, redesigning, splitting, or
escalating. It also adds the Decision 4 escalation vocabulary with typed
evidence and policy authority. Automatic patch blending remains prohibited.

Concrete model catalogues, qualification execution, prices, credentials, and
economic optimization stay outside the provider-neutral kernel. The kernel
retains content-addressed identities, decisions, receipts, and observable
outcomes.

Compatibility is explicit and fail-closed:

- historical `0.5.0` records replay under their original semantics;
- unchanged `1.4.0` message families may have declared identity transforms to
  `1.5.0`;
- records missing work-profile, qualified-assignment, or verification-topology
  evidence cannot be silently upconverted;
- active tasks require explicit scope/profile binding and a new qualified
  assignment before live model execution; and
- completed prior gates retain exactly their recorded scope and are not
  retroactively invalidated or broadened.

The `0.6.0` release gate requires schemas, catalogue entries, compatibility
declarations, positive and negative fixtures, invariants, traceability,
reference-runner validation, and Go implementation forward requalification.
Approval of this decision fixes the successor boundary but does not itself
authorize package generation, implementation, OpenHands-Q1, or production use.

## Decision 9 — implementation and OpenHands-Q1 sequence

**Status:** `APPROVED BY PRINCIPAL`

**Principal statement:** `Approved` on 2026-08-13.

Execution proceeds through explicit gates:

1. Generate the `0.6.0` candidate from Decisions 1 through 8, validate its
   schemas, catalogue, fixtures, invariants, traceability, compatibility,
   digests, and reference runner, and obtain principal acceptance before
   implementation depends on it.
2. Implement, in dependency order, semantic scope revision; work-profile
   binding and supersession; qualified-assignment authorization; derived
   eligibility and deterministic selection; verification-topology enforcement;
   escalation and promotion fencing; variant-group coordination; and affected
   projections, persistence, and adapters.
3. Forward-requalify affected surfaces with positive, negative, replay,
   staleness, revocation, downgrade, independence, disagreement, fencing,
   timeout, and budget tests. Preserve prior tests and repeat the deterministic
   capstone. Prior accepted steps are not rerun wholesale or silently broadened.
4. Build the content-addressed concrete-profile registry, preregistered
   qualification corpora, qualification and revocation records, hard filters,
   and deterministic economic selection. Provider-neutral fake profiles are
   qualified first.
5. Create and freeze a standalone OpenHands-Q1 preregistration covering exact
   identity, instruction and tool fidelity, qualified assignment, rejection,
   bounded execution, promotion, failover, cancellation, recovery,
   independence, variants, provenance, secrets, SMA's non-authoritative
   boundary, finite budgets, and terminal outcomes.
6. Run a separate OpenHands-Q1 preexecution gate that freezes source, adapter,
   OpenHands, model profiles, prompts, tools, endpoints, corpus, credential
   boundary, budgets, and expected evidence. Missing identity or qualified
   capability is `NO_GO`.
7. Obtain separate principal authority before live provider/OpenHands execution,
   expenditure, production deployment, or autonomous operation.
8. Adjudicate exact results. Only attributable `PASS` profiles become eligible;
   `FAIL`, `NOT_RUN`, `INCONCLUSIVE`, stale, or revoked profiles remain
   ineligible. Repeat the integrated capstone before any production-readiness
   recommendation.

SMA-Q1 remains under its separate gate and may proceed independently. Its result
cannot qualify model routing or OpenHands. Approval of this decision authorizes
candidate generation, local deterministic work, and preregistration work. The
approved intermediate acceptance gates remain binding.

## Local implementation gate

**Status:** `PASS_LOCAL_DETERMINISTIC_IMPLEMENTATION`

The accepted `0.6.0` contract is implemented across the kernel, coordinators,
provider-neutral model registry, fake qualification runner, and memory and Mongo
projections. The full Go suite, static analysis, race detector, isolated Mongo
integration suite, immutable-package structure runner, and reference runner
pass. The exact receipts and qualification boundary are recorded in
`OUTPUT/phase-3/model-capability-implementation-gate.json`.

OpenHands-Q1 remains a separate `FROZEN_CANDIDATE`. Its 20-scenario document
passes structural validation but retains unbound preexecution identities and
`executionAuthorized=false`. No OpenHands, provider, model, Ollama, or SMA
operation was executed by this implementation gate. Candidate acceptance,
preexecution identity binding, and live-execution authority remain separate
future gates.

## Interaction with SMA

SMA remains semantic memory only. It may reconstruct bounded, partitioned,
explicitly untrusted working context. It does not classify work risk, select a
model tier, authorize promotion, determine independence, adjudicate
disagreement, or accept work.

SMA-Q1 may continue because it qualifies memory capture and retrieval rather
than model-routing policy. Passing SMA-Q1 is not evidence that the capability
policy works. The capability-policy gate must precede the OpenHands-Q1 freeze
and any live model-based assignment.

## Stop boundary

Until this remediation is adjudicated and its normative impact implemented:

- do not freeze or execute OpenHands-Q1;
- do not adopt live model-based assignment;
- do not claim N-version independence;
- do not claim evidence-driven model escalation;
- do not treat a preferred route plus fallback as a capability ladder; and
- do not claim Teams v4 is production-ready as an autonomous programming
  organization.

The existing deterministic kernel, persistence, adapter, validation,
escalation, release, capstone, and SMA preregistration results retain their
recorded scope. This remediation does not invalidate or broaden them.

## Principal decision sequence

The coordinator will present these decisions individually:

1. capability ladder and provider-neutral boundary;
2. mandatory work-risk profile and decomposition rules;
3. separation-of-duty and independence requirements;
4. evidence-driven promotion and escalation triggers;
5. N-version eligibility, isolation, comparison, and selection;
6. default role-to-capability allocation;
7. qualification, provenance, and economic accounting;
8. exact contract revision and compatibility boundary; and
9. OpenHands-Q1 amendment and implementation sequence.

Only decisions explicitly marked `APPROVED BY PRINCIPAL` are binding design
inputs. Every remaining decision requires principal disposition, followed by an
exact contract-impact update. Released packages remain immutable.
