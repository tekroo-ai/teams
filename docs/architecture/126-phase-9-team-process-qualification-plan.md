# Phase 9 — Team process qualification

## Purpose

Phase 9 proves that Tekroo Teams can carry one software feature through its
normal organizational flow without operator coaching:

`product-owner -> project-manager -> plan author -> independent plan review -> implementation -> tester -> product-owner acceptance`

The repeatable feature is actor naming: start `teams::coder-1` as `Bob` and
allow the operator to address that same actor as `Bob`, while preserving the
actor's FQN as its authoritative identity. The feature implementation is test
material and must remain off canonical `main` until the team process passes.

This plan distinguishes two things:

1. deterministic orchestration, which must be proved without a model; and
2. role performance, which must be demonstrated by the exact configured model
   and tool profile.

Repeating the same model-driven feature does not prove orchestration more
reliably than deterministic tests and does not establish breadth. Phase 9
therefore requires one clean end-to-end actor-name run after the offline suite
and role-profile canaries pass. A later, materially different real feature is
the breadth check.

## Current engineering state

- **OBSERVED:** canonical `main` was
  `5b840227112abfa6242c5e4ee11095270745aab4` when Phase 9 repair began.
- **OBSERVED:** the first actor-name run produced disposable feature code but
  exposed failures in candidate handoff, validator workspace materialization,
  context continuity, retry control, and process supervision.
- **COMPUTED:** retained database records for that run contain 49 work
  invocations: 9 succeeded, 33 failed, 4 were cancelled, and 3 timed out.
- **OBSERVED:** a standalone architect probe consumed a very large context,
  exhausted its 8,192-token answer limit, and then repeated repository reads.
  Raw events showed that this probe bypassed Teams' compaction checkpoint and
  loop controls, so it was not representative of the managed execution path.
- **OBSERVED:** the repaired, Teams-controlled architect canary used dedicated
  read-only repository tools, 32,768 primary output tokens, and a separate
  non-thinking 4,096-token condenser. It crossed compaction, received the
  authoritative Teams checkpoint, and completed with a finite eight-task DAG.
- **OBSERVED:** the Teams-controlled bounded planning, repository-editing, and
  candidate-validation canaries also completed successfully. The coder made
  and committed only the intended one-line production repair; the independent
  tester confirmed the test was unchanged and ran the suite without cache.
- **OBSERVED:** the first coder canary exposed a Teams loop-guard defect after
  otherwise completing correctly: successful Git state changes did not reset
  the repeated-action signature. The guard now recognizes state-changing Git
  operations, and a fresh coder canary completed without that false positive.
- **OBSERVED:** the current Phase 9 source passes the normal Go suite, Go vet,
  Git whitespace validation, the complete Mongo-backed operational-runtime
  integration package, and the four exact role-profile canaries.
- **OBSERVED:** clean run 048 reached its architecture handoff without a role,
  tool, compaction, or repetition failure, but the proposed MongoDB rename
  task required changing an existing document `_id` through `ReplaceOne`.
  MongoDB does not permit that state transition. Teams paused the run and the
  first coder was interrupted before any repository edit.
- **OBSERVED:** Teams now requires a second configured instance of the plan
  author's FQRN to inspect the exact digest-bound plan with an independent
  workspace and execution. A non-passing review leaves the feature at
  `SPECIFIED`, resolves the rejected handoff, and materializes no implementation
  task.
- **OBSERVED:** clean run 050 reached the then-current task-scoped plan review.
  The reviewer correctly rejected a task that assigned lifecycle orchestration
  to a MongoDB store lacking the required runtime dependencies. No
  implementation task materialized.
- **OBSERVED:** run 050 then exposed a workflow gap: a valid technical rejection
  had no bounded route back to the plan author, so safe review also stopped
  useful progress.
- **OBSERVED:** the repaired run-050 canary created one new architecture task
  with a different task and invocation identity. The successor inspected the
  repository and corrected the rejected ownership inversion by keeping
  persistence in the Mongo adapter and lifecycle orchestration in the
  operational service.
- **OBSERVED:** the successor still named two nonexistent Go APIs and its first
  independent task reviewer incorrectly returned `PASS`. This was not accepted
  as proof of those APIs. The mistake is correctable during implementation and
  must be caught by compilation and independent candidate validation; it does
  not justify hard-coding feature-specific answers into the role or runtime.
- **OBSERVED:** clean run 051 produced a strong five-task architecture plan,
  but the second of five serialized task-scoped reviews continued repository
  discovery for approximately 39 minutes after it had enough evidence to make
  a material decision. The review was interrupted and no implementation task
  materialized.
- **OBSERVED:** that same plan specified a unique `(team, normalized_name)`
  index but only a non-unique `(team, actor_fqn)` index while claiming
  concurrency-safe first-time binding. Two concurrent different-name binds to
  the same actor could therefore both insert.
- **OBSERVED:** the accepted complete-plan-review canary used one initial
  assignment and no follow-up guidance. It covered all five task descriptions,
  all 40 task acceptance criteria, and all six plan-level checks. It rejected
  the plan because the prescribed `StartRoleWithName` delegation could not
  satisfy the existing structural lifecycle test. It did not identify the
  separate actor-side uniqueness risk above.
- **INFERRED:** serializing one speculative review per planned task and then a
  sixth integration review duplicates architectural judgment and encourages
  repository touring where no implementation exists to test. The repaired
  process uses one independent complete-plan review with compact, complete
  coverage. A correct material rejection is sufficient to prevent a bad plan
  from advancing; downstream compilation, tests, candidate validation,
  security review when required, and product acceptance remain responsible for
  detecting residual defects rather than requiring one model review to be
  infallible.

The earlier failed run and failed canary remain historical evidence. They do
not qualify the repaired process or the repaired architect profile.

## Non-negotiable boundaries

1. FQRN is the role name alone, such as `coder`. FQN is
   `<team>::<role>-<instance>`, such as `teams::coder-1`.
2. An alias is operator-facing lookup and provenance. It resolves to one FQN
   before Teams creates an authoritative command; it never replaces identity.
3. MongoDB change streams remain the wakeup mechanism.
4. The task/story lifecycle, routing metadata, work budgets, assignments,
   candidates, validation state, and acceptance state belong to Teams, not SMA.
5. SMA supplies non-authoritative semantic memory context only.
6. Accepted contracts are immutable.
7. A long-running role host has no arbitrary iteration ceiling. Each work item
   is bounded by its authority, deadline, state, and observable progress.
8. No automatic model retry follows a failed terminal invocation. Recovery is
   an explicit, evidence-bound operator action with a changed condition.
9. Generated task prose cannot add, remove, or change tools. Signed role
   permissions and the bound execution profile determine the tool surface.
10. No prior actor-name branch, patch, transcript, database state, or solution
    is visible in a clean qualification workspace.
11. A passing run receives no manual coaching or rescue.
12. Unrelated tracked and untracked workspace content is preserved.
13. The software-development roles exercised here are one supplied workflow,
    not framework vocabulary. Generic Teams infrastructure must not require a
    source change to register, start, address, wake, or bind work to a new role.
14. Closing the host laptop is a runtime suspension, not elapsed team work.
    Teams preserves the same durable feature, completed stages, task, actor
    identity, and surviving OpenHands conversation without spending the
    suspended interval from execution deadlines. Wake follows the accepted
    `RECONCILING` state and resumption gate before admission reopens, especially
    when a remote provider may have continued running. Only an external provider
    call that did not survive may use the existing explicit retry path;
    completed roles and model invocations are never replayed.

## Structural repair

### Candidate handoff

Teams captures an immutable candidate before validation. Its receipt binds the
source task and invocation, repository, baseline commit, candidate commit and
tree, diff digest, changed-file inventory digest, required gate receipts, and
unresolved exceptions. A later repair produces a new candidate; it does not
mutate the old one.

An unaccepted candidate is visible only to tasks explicitly assigned to
validate it. Every ordinary downstream DAG dependency waits until the upstream
task and its required independent validation are complete. This scheduling
rule is role- and domain-independent; sharing an actor or workspace never
bypasses it.

### Candidate workspace

Teams materializes a fresh read-only consumer workspace at the exact candidate.
It verifies repository, baseline, commit, tree, diff, changed files, clean
state, allowed reference, and required gates before OpenHands is called. The
tester and accepting product owner receive the candidate identity directly;
they never search for an implementation branch.

### Role and tool binding

Every invocation binds the actor FQN, role FQRN, signed role bundle, model
profile, tool policy, workspace, execution identity, and fence. Teams derives
the OpenHands tools from signed permissions:

- roles without repository authority receive no repository tools;
- read-only design roles receive glob, repository search, and repository view;
- read-only validation/security roles additionally receive terminal execution;
- edit roles receive terminal, glob, repository search, file editing, and task
  tracking.

Task text is untrusted input to this selection and cannot override it. Any
observed tool action outside the signed role permissions fails the invocation.

### Progress and recovery

Teams records repository actions and their outcomes in a structured checkpoint.
After OpenHands compaction, the checkpoint restates authoritative execution
state, completed work, test state, and the next permitted action. Condenser and
agent summaries remain explicitly non-authoritative.

One task can have at most one active invocation. A terminal failure remains
terminal. The operator may authorize one successor only when retained evidence
classifies the failure as recoverable and identifies a changed condition.
Process restart may resume an already-authorized invocation; it may not invent
a new one.

### Planning and DAG construction

The product owner preserves operator intent and makes ambiguity explicit. The
project manager produces the smallest coherent story set. The architect must
read `AGENTS.md` plus relevant source and tests, then return a finite acyclic
implementation plan. Teams validates and normalizes that plan:

- dependencies point only backward;
- implementation work uses one selected implementation role;
- individual task complexity is at most 6;
- every cross-cutting invariant has one authoritative owning component, while
  dependent tasks consume that component's abstraction;
- lower-level storage and transport components do not depend on higher-level
  workflow, deployment, or team configuration;
- architect-authored dependency edges are preserved instead of gaining an
  artificial total order;
- ready implementation nodes are distributed deterministically across the
  configured instances of the selected FQRN, with at most one active work item
  per FQN;
- every implementation node receives its own editable Git workspace rooted at
  the configured baseline plus its completed implementation dependencies;
- divergent completed branches are assembled deterministically for validation,
  and a merge conflict stops before any validator model call; and
- Teams, not the architect, appends independent validation and acceptance.

This makes the workflow DAG authoritative. Agents cannot create conversational
cycles by repurposing message types.

Before Teams materializes that DAG, a distinct configured instance of the same
plan-authoring FQRN independently reviews the exact output digest. The reviewer
uses the same signed read-only role bundle but a different FQN, execution, and
workspace. One complete-plan review decides whether every task is materially
implementable as written and whether the tasks compose without gaps in shared
invariants, ownership, concurrency, dependency direction, interfaces,
authorization, or partial-failure behavior. It inspects the minimum repository
evidence needed for that decision and does not attempt to prove ordinary
implementation details for code that has not been written. `PASS` is required
before implementation. A valid `FAIL` may create exactly one new plan task
causally descended from the rejected plan and review. The new task receives the
immutable predecessor output plus the rejected review output and digests; its
review uses a new task identity. A malformed result, mismatched digest, or
second rejected plan stops the feature before implementation. This is a finite
review-directed DAG, not a retry or an agent conversation. The rule is attached
to executable-plan handoff, not to a built-in role name or to feature-specific
technical advice.

## Closure sequence

### 1. Deterministic offline proof

Run the normal Go suite, Go vet, whitespace validation, and the complete
Mongo-backed operational-runtime integration package. These tests must cover:

- role/FQRN/FQN and tool-policy binding;
- candidate creation, immutability, materialization, restart recovery, and
  rejection of dirty, stale, mutated, wrong-repository, or ungated input;
- one-active-invocation enforcement;
- explicit recovery and changed-condition evidence;
- checkpoint preservation and custom repository-tool event decoding;
- durable host-suspension detection, exactly-once deadline allowance, wake-up
  ordering, and same-invocation continuation;
- planning DAG validation and architect repository grounding;
- implementation-to-validation-to-acceptance candidate continuity; and
- the supported daemon/CLI product surface, including cancellation.

Any failure is repaired at that boundary and added as a regression test. The
complete suite runs once after the focused repair.

### 2. Exact role-profile canaries

Exercise each materially different execution profile and work product
independently before a team run:

1. bounded no-repository planning;
2. complex read-only architecture;
3. bounded repository editing and testing; and
4. read-only candidate validation/testing.
5. independent read-only complete review of a digest-bound plan, covering both
   task feasibility and plan composition.

A canary must use the exact model, endpoint, thinking mode, output limit,
condenser, role bundle, tools, and system prompt that the team will use. It
passes only when the role performs reasonable work, obeys its permissions, and
returns the required structured result. A failed tuple remains unqualified;
changing any material setting creates a new tuple.

### 3. One clean actor-name flow

Create a disposable repository containing only the reviewed baseline and its
permitted ancestry, a fresh Teams database namespace, fresh workspaces, fresh
identities, and an evidence directory outside all agent-visible workspaces.
Submit the unchanged actor-name feature through the supported operator surface.

The coordinator inspects actions, code, tests, and outputs continuously. A
valid first plan rejection follows the bounded review-directed successor path
without operator coaching. The coordinator stops on a malformed review, a
second plan rejection, or the first material process or implementation defect.
A stopped run is retained as evidence, the structural boundary is repaired
offline, and a new clean run begins only after the deterministic suite and
affected profile canary pass.

### 4. Breadth confirmation

After actor naming passes, use the team for one materially different real
feature. This is not another actor-name rehearsal. It confirms that the
qualified process generalizes beyond its construction test.

## End-to-end pass criteria

The actor-name flow passes only when:

1. product owner, project manager, plan author, independent plan reviewer, each
   implementation task, tester, and accepting product owner complete without
   orchestration retry; at most one review-directed successor plan may occur;
2. every role has the exact FQRN, FQN, profile, permissions, workspace, and
   execution identity;
3. the plan author performs relevant repository inspection and returns a
   reasonable finite DAG, and a distinct FQN independently verifies that exact
   digest as technically implementable before task materialization;
4. implementation work stays within scope and produces an immutable candidate;
5. the tester starts on the exact read-only candidate, executes the required
   positive, negative, lifecycle, persistence, CLI, MCP, and regression tests,
   and returns raw receipts;
6. acceptance evaluates that same candidate and the original criteria;
7. no duplicate active work, conversational cycle, unauthorized tool use,
   hidden automatic retry, prior-run leakage, or manual intervention occurs;
8. the feature reaches the correct terminal state; and
9. disposable feature code remains off canonical `main`.

Phase 9 is complete after this clean flow and its evidence review pass. Delivery
of the actor-name implementation remains a separate decision.

The qualification runtime must not treat a planning agent's suggested role as
operational authority. Planning output describes work, dependencies,
complexity, risk, and acceptance criteria. Teams applies a validated workflow
routing policy to select the FQRN and serializes the directed work where that
policy requires it. The supplied software workflow preserves the architect's
dependency DAG and does not request additional serialization. It currently routes bounded
plans to `coder` and plans containing complexity 5-6 work to `senior-coder`;
those names are workflow data, not vocabulary in the generic routing engine.
Legacy planning output may contain a `role` field, but Teams ignores and
canonicalizes it through the same routing policy.

## Mandatory general-purpose successor

Phase 9 qualifies the supplied software-development workflow; it does not by
itself qualify Teams as a general-purpose organization runtime. The next design
and implementation phase must move workflow structure and interaction policy
out of hard-coded role selection and into versioned, validated configuration.

That successor must provide:

- declarative stages, dependencies, work-product schemas, and completion rules;
- capability- and policy-based role selection rather than literal built-in role
  names in generic infrastructure;
- externally loaded workflow routing policies, replacing the supplied
  software policy currently constructed in the runtime;
- role bundles that can introduce roles such as `evangelist`, `sales`, or
  `social-media-manager` without modifying or rebuilding Teams;
- configurable directed interactions that preserve the authoritative DAG,
  budgets, provenance, and no-loop guarantees; and
- a configurable project-management stage that may own priorities, milestones,
  sequencing constraints, delivery-risk interpretation, escalation decisions,
  and proposed staffing needs, while deterministic Teams policy retains final
  authority for actor selection, assignment, budgets, and dependency admission;
  the current `project-manager` stage is limited to coherent story
  specification and must not be described as full traditional project
  management; and
- the present software feature flow as a shipped workflow definition using the
  same public configuration mechanism available to other kinds of teams.

Acceptance requires adding and operating at least one non-software role and one
non-software workflow without changing Go source. Until that passes, claims are
limited to the supplied software-development workflow.
