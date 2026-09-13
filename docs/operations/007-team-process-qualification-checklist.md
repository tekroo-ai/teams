# Phase 9 team-process qualification checklist

This checklist executes
`docs/architecture/126-phase-9-team-process-qualification-plan.md`.

## A. Preserved boundary

- [x] Preserve canonical process work; keep disposable actor-name code off
      canonical `main`.
- [x] Preserve accepted contracts unchanged.
- [x] Preserve FQRN as role name and FQN as `<team>::<role>-<instance>`.
- [x] Preserve long-running role hosts without an arbitrary iteration ceiling.
- [x] Keep SMA, v3 migration, production deployment, and unrelated Teams work
      outside Phase 9.
- [x] Inventory and preserve unrelated untracked workspace paths.

## B. Structural process repair

- [x] Bind candidate repository, baseline, commit, tree, diff, changed files,
      source task/invocation, and deterministic gate receipts.
- [x] Materialize a fresh read-only validator/acceptor workspace at the exact
      candidate.
- [x] Reject dirty, stale, mutated, wrong-repository, wrong-tree, and ungated
      candidate sources before a model call.
- [x] Bind role FQRN, actor FQN, role bundle, model profile, tool policy,
      workspace, execution, and fence on every invocation.
- [x] Derive OpenHands tools from signed role permissions.
- [x] Prevent task prose from overriding the role tool surface.
- [x] Enforce repository read/edit permissions against observed tool actions.
- [x] Preserve progress checkpoints across retry and compaction.
- [x] Preserve the durable feature/task/actor/conversation checkpoint across
      laptop sleep and daemon restart; exclude each recorded suspension from
      deadlines exactly once and prevent wake-up timeout races.
- [x] Permit at most one active invocation per task.
- [x] Leave terminal failures stopped until an evidence-bound operator recovery
      supplies a changed condition.
- [x] Require architect reads of `AGENTS.md` and relevant repository content.
- [x] Validate and normalize architect output into a finite implementation DAG.
- [x] Preserve architect-authored implementation dependencies without forcing
      independent nodes into a serial chain.
- [x] Allocate ready nodes deterministically across configured role instances,
      while permitting at most one active work item per FQN.
- [x] Give every implementation node an isolated editable Git workspace and
      assemble completed dependency branches deterministically before immutable
      candidate validation; stop on merge conflict before a validator call.
- [x] Require a distinct instance of the plan author's FQRN to review the exact
      plan digest once for material task feasibility and complete-plan
      composition before implementation materializes.
- [x] Keep implementation stopped at `SPECIFIED` after a non-passing plan
      review, resolve the rejected handoff, and allow exactly one immutable,
      review-directed successor plan with new task identities. Bind the rejected
      complete-plan review into that successor. A malformed review or second
      rejection remains stopped.
- [x] Bind validation and acceptance to the same immutable candidate.

## C. Deterministic verification

- [x] Focused supported product-surface test passes, including start, execution,
      observation, cancellation, and second-actor profile binding.
- [x] Complete Mongo-backed operational-runtime integration package passes.
- [x] Mongo-backed admission proves two independent roots are authorized
      together in distinct editable workspaces and their commits form one
      immutable assembled candidate; the execution worker's concurrency test
      proves simultaneous model-backed requests when capacity is greater than
      one.
- [x] Normal Go test suite passes.
- [x] Go vet passes.
- [x] Git whitespace validation passes.
- [x] OpenHands repository-view and settings tests pass.
- [x] OpenHands targeted type and lint checks pass.
- [x] Isolated OpenHands settings probe reports the exact architect model,
      endpoint, tools, output limit, thinking mode, and condenser profile.
- [x] Runtime-continuity tests cover clean restart, same-process sleep,
      content-addressed suspension receipts, and an invocation worker waking
      before the heartbeat writer.
- [ ] Wire physical sleep/outage detection through the accepted system
      `ACTIVE -> RECONCILING -> ACTIVE` transition, power epoch, admission
      fence, remote-provider reconciliation, and resumption gate. The lower-level
      checkpoint/deadline mechanism is complete; this production safety path is
      not yet claimed.

## D. Role-profile canaries

- [x] Bounded no-repository planning profile passes.
- [x] Complex read-only architect profile passes.
- [x] Bounded repository-editing profile passes.
- [x] Read-only candidate-validation profile passes.
- [x] Digest-bound complete-plan review work product passes against the exact
      complex read-only role profile and rejects the retained run-051 plan for
      a material lifecycle-path defect without unrelated discovery or
      coordinator guidance. Residual defects remain the responsibility of the
      independent implementation-validation chain.
- [x] Multi-task integration coverage proves exactly one independent
      complete-plan review materializes and no obsolete per-task architecture
      review is created before implementation.
- [x] Each retained qualification names the exact model/settings/role/tool
      tuple used by the end-to-end run.

Retained successful canaries:

| Profile class | Actor / conversation | Bound identities | Exact execution settings | Result |
|---|---|---|---|---|
| Bounded no-repository planning | `teams::product-owner-1` / `01a066f2-85cc-711e-abb5-c04f308e4652` | bundle `3c9242ace6deae75115220d47f5261eed30998a65938fe6eea43444d9b0fe690`; model profile `e767b8910799377b5c3115f8a127012e4de1ea47773db02cb2252521d2c8bd83`; tool policy `5fd9dcb4573b80d98efe7584eb9702ea818e295ef6f95982aaf7bbb32e0f397f`; settings `ce9f86b4f5a4d0bd96e42358630494f7373bfbd713cb9a44ae67c020c2ba6d94` | Qwen3.8-27B MLX 8-bit; `127.0.0.1:8802`; thinking off; 8,192 output tokens; no repository tools | `SUCCEEDED`; structured feature refinement preserved the submitted request |
| Complex read-only architecture | `teams::architect-1` / `01a066dc-fc34-750a-af82-0dba57304305` | bundle `ca2225cce471bb3b11732b9737474eb825e02df64a4e86111f2d51436f1ae405`; model profile `00ed7cd01906a9e968bc017a6d4fa44075b47bfa89f828ec926444ad55f56db0`; tool policy `0d7c147c226967dd5842ac6a6e463d38e64f405ea9d8aab382f07423be876a28`; settings `f2ee7152c72a7630d578e301d693ab01c5353c1483af168aa27cb691ac4ed9d8` | same model; `127.0.0.1:8800`; thinking on; 32,768 output tokens; glob/search/view; non-thinking 4,096-token condenser | `SUCCEEDED`; source-grounded finite eight-task DAG after one managed compaction |
| Repository editing | `teams::coder-1` / `01a066fd-b24d-75a0-a263-82d7304baddb` | bundle `65b65b479693d0a8fdaa1a74fe1dcb5b1c3e6f7ac50f3971e091f2e574124cdf`; model profile `67a13117d5eeb77aa55671dffa74aa08bbaca4175858127616721652ec29da82`; tool policy `b91a6fe88cff76692b01243e1bde66ae26cee2d0532df94da34b5e064e84bb24`; settings `7cd90c2654cee380165724d5a4b8602bb193511a1a412b829ff892a0d4f17a4e` | same model; `127.0.0.1:8802`; thinking off; 8,192 output tokens; terminal/glob/search/editor/task tracker | `SUCCEEDED`; one-line production repair committed as `6cd08735b58e990a28c41679918e8fbbf70c5e4d` with clean status and passing tests |
| Candidate validation | `teams::tester-1` / `01a066ff-9df6-72c4-a584-8c37b8c7c97d` | bundle `69d585b8b8ef8cab59951b55f5496de2fbec34a045eb6818fe27e1e5af1d1ed1`; model profile `2fea2c3ad88c58944ade719f261079e1efce434f9c9c53783f9b4faa99049142`; tool policy `773e6b88bbdd182ea6edabacba932d5a734b401cdeeedb8cdfa28314d6f67b2f`; settings `b3debf1e1e6d0be50d74d6b203bd03d1c72747e6e91d2702b22379d787247f57` | same model; `127.0.0.1:8802`; thinking off; 8,192 output tokens; terminal/glob/search/view | `SUCCEEDED`; structured PASS, unchanged test confirmed, uncached suite passed, workspace unchanged |
| Historical task-scoped plan review | `teams::architect-2` / `b0c42274-cb7f-4c9d-92f5-4e2d8d6fa5a0` | bundle `ca2225cce471bb3b11732b9737474eb825e02df64a4e86111f2d51436f1ae405`; model profile `415ebb2ddfe4d4b66db79c80a4d537503d592744533b4d655ce657308cbbb91e`; runtime `172635d0bd717b081df2e6770782a973607d7f7e557cbe2fd937b7e085cbf5d2`; tool policy `b9b6f5bfe6729953cfd6f0dacfadcf23dda6c6487a184a19957cf3d1e4d6bed4`; effect policy `388ee8a8df9db2d64d882ddbfdf1ade29bf346238e67f8716d714dcbd92b09ca`; reviewed plan `f0273ee46c6d61efe38d87184ce6010c62ad44702bd165c1f5ff44388ff292e0`; reviewed task `c6c9905704d0403ea7be02fae2e800482c91afac4b59c7e19d31b25fc40f9a34` | same model; `127.0.0.1:8800`; thinking on; 32,768 output tokens; glob/search/view; non-thinking 4,096-token condenser | `SUCCEEDED`; retained evidence for the superseded per-task review design, not qualification of the complete-plan review work product |
| Complete-plan review | `teams::architect-2` / `01a076ea-f8d0-7275-b48d-1db17b59a67f` | bundle `ca2225cce471bb3b11732b9737474eb825e02df64a4e86111f2d51436f1ae405`; profile `a8ddef465fe5b953835865315daf9866309ddab8999f3ba13485eb4bbd3442cb`; reviewed plan `d6140606748eabfda17c1d7dc963ec3b3d8550e9211d4e9ca289f86bb1272413`; baseline `a472937078ebe88c8a499696fce2010c2d41ab05` | same model; `127.0.0.1:8800`; thinking on; 32,768 output tokens; glob/search/view; non-thinking 4,096-token condenser | `SUCCEEDED`; one initial prompt, 5 task descriptions, 40 criteria, and 6 plan checks covered; material `FAIL` prevented implementation |

## E. Clean actor-name flow preparation

- [x] Record the reviewed disposable qualification baseline: commit
      `a472937078ebe88c8a499696fce2010c2d41ab05`, tree
      `90c6b7a380eabd3732391994873ffbf57f364adc`.
- [x] Create a disposable repository with only permitted baseline ancestry.
- [x] Verify no prior actor-name solution or run artifact is agent-visible;
      neutralize feature-specific process-test fixtures and omit the Phase 9
      coordinator documents from agent workspaces.
- [x] Create the fresh database namespace, fourteen workspaces, and external
      evidence directory for run 049.
- [x] Create fresh feature `01a07473-0726-74f2-9dcb-7369e574332f` and
      per-role conversation identities when run 049 starts.
- [x] Verify exact role bundles, qualified profiles, repository allowlist, and
      external evidence configuration.
- [x] Verify the run 049 daemon reports `RUNNING`, its worker reports
      `RUNNING` with no error, and its operator surface listens only on
      `127.0.0.1:8849`.
- [x] Record the unchanged feature request digest:
      `852518cecb2618af896fb86d6b064ee59d6d613c0dd51a697979d98187becdfc`.

## F. End-to-end role review

- [x] Run 049 product owner preserved intent and produced testable criteria.
- [x] Run 049 project manager produced one coherent story rather than
      layer-oriented stories.
- [x] Plan author reads relevant source/tests and produces a reasonable finite
      DAG. Run 049 did so. Its persistence task described rename in terms of a
      changed MongoDB `_id`; the independent task review found the repository's
      existing transaction support sufficient to implement the required atomic
      behavior as delete-plus-insert without weakening the feature invariants.
- [ ] A distinct plan-author FQN independently verifies the exact plan digest
      once for material task feasibility and complete-plan composition before
      any implementation task starts. The simplified adapter has passed its
      focused integration test; the affected live profile canary remains.
- [ ] If a valid review rejects the first plan, Teams carries the exact plan and
      all rejected-review digests into at most one successor plan task; no
      conversation loop or operator coaching occurs.
- [ ] Each implementation task stays in scope, runs its tests, and completes
      without orchestration retry.
- [ ] Teams records an immutable candidate matching the repository exactly.
- [ ] Tester starts on the exact read-only candidate, runs every required test
      category, and returns raw receipts.
- [ ] Product-owner acceptance evaluates the same candidate and original
      criteria.
- [ ] Every role carries the correct FQRN, FQN, profile, tools, workspace,
      execution, and fence.
- [ ] Zero duplicate active work, conversational cycles, unauthorized tool
      actions, hidden automatic retries, prior-run leakage, or manual coaching.
- [ ] Feature reaches the correct terminal state.
- [ ] Disposable actor-name code remains off canonical `main`.

## G. Stop-and-repair rule

- [x] On the first material defect, stop the run and preserve the first wrong
      transition plus raw evidence. Run 051 stopped after the second serialized
      task review continued discovery after sufficient material evidence was
      available; no implementation task materialized.
- [x] Add focused parser, identity-binding, failure-path, and single-review
      regressions and repair the production boundary.
- [x] Re-run only the focused tests while diagnosing.
- [x] Exercise the repaired review-directed successor path against the live
      complex-reasoning profile. The successor used new task/invocation
      identities, consumed the immutable rejection, inspected the repository,
      and corrected the material ownership defect. A later reviewer incorrectly
      treated two unverified Go API names as available; that output is retained
      as a model-quality observation, not accepted API evidence.
- [x] After the fix, run the complete deterministic suite once. The affected
      complete-plan-review canary has passed and will not be repeated.
- [ ] Start a new clean end-to-end run; never coach or resume a failed run into
      a pass.

## H. Completion

- [ ] Publish the clean run's identities, invocation states, candidate
      continuity, tool actions, test receipts, role-by-role quality review, and
      remaining limitations.
- [ ] Mark Phase 9 complete after one clean actor-name flow passes.
- [ ] Use one materially different real feature as the subsequent breadth
      confirmation.
- [ ] Decide separately whether to deliver an actor-name implementation.

**Current next action:** complete deterministic verification once, then begin a
fresh clean run. Audit every role's scope and work product as it completes;
allow at most one evidence-bound successor plan and do not coach or resume a
failed run into a pass.
