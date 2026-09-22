# HANDOFF — v204 resume (post agent-server restart)

Created 2026-09-21 just before restarting the agent-server (loads SDK fix
`95f3c376` + clears process-global client-tool registry).

## Decisions made
- MTP stays ON everywhere, including GUI operator profile (user decision,
  evidence-backed: template leak ran MTP-off; bracket errors are string-wrapped
  JSON; grammar-constrained tool calls are the fix).
- Keep conversation-title feature (`7a5e4ae`) in production.
- Zero tolerance: every anomaly is a stop-and-fix, no workaround tolerance.

## Production state (at restart time)
- Daemon binary deployed: HEAD `e374c7c` = title feature + submit_envelope +
  restored digest-bearing prompt constant. All 8 profiles validate. Team PAUSED.
- PO invocation `01a0c420-ebec-7d75-a1d1-eae6d6a948be` AUTHORIZED (stale, from
  crashed attempt — reset cycle 3 will clear it).
- Supervisor now DURABLE: `teams-v4-phase10-prod/keepalive.sh` (the /tmp
  original was lost to /tmp cleanup; launchd job com.tekroo.teams-v4-prod was
  re-submitted against the durable path). NEVER start a second supervisor
  manually.
- Deploy binaries with `rm + cp + codesign -s -` (in-place cp over running
  binary = SIGKILL Code Signature Invalid, silent).
- Root cause of "execution profile is invalid" SOLVED: the system prompt
  constant is digest-bearing (embedded in config agent_settings, validated by
  validTeamsSystemPrompt, hashed by ModelProfileDigest). Fixed in a39d786 by
  restoring the constant; terminator truth moved to runtime result_protocol.

## Next steps after this restart
1. Agent-server restarted: SDK fix 95f3c376 loaded + client-tool registry clean.
2. Reset cycle 3 (reset.sh), resume team, stop at FIRST hiccup.
3. FQN-alias feature to ACCEPTED (RSI test).

## Run outcome + fixes (2026-09-21, later same day)
- FQN-alias feature 01a0c4b9-dce8-75c6-92b1-d374ca4fef9c ran fully
  autonomously to AWAITING_ACCEPTANCE: refine (attempt 2 via
  retry-planning after hot-cache poisoning), specify, design, 5
  implementation tasks, whole-feature validation, PO acceptance
  recommendation PASS. Operator feature-accept is the only remaining
  transition (human gate; not yet issued).
- submit_envelope produced ZERO envelope-integrity failures across 12
  invocations (vs ~13% with string-wrapped JSON). Thesis proven.
- Harness corrections during the run (audit of all 11 conversations):
  8 grounding, 14 shell-discipline, 5 progress-checkpoint, 2
  checkpoint-completion, 1 repository-progress. Root-causing each:
  - GROUNDING (all 8): fence exempted orientation by TOOL name
    (terminal-only allowlist), so glob/view/git-log structure-only
    actions were falsely fenced in every stage of every run (hundreds
    historically). FIXED 5b3b19a: orientation is content-based
    (delegate to repositoryFileListingAction + gitMetadataAction);
    content reads (repository_search, git show/diff) still fence.
    Proven regression test. NOT YET DEPLOYED (daemon runs e374c7c).
  - SHELL-DISCIPLINE (14): DIAGNOSED + FIXED (3139dab). 3 of 14 were
    fence FALSE POSITIVES: `go build/test ... 2>&1` — the classifier
    flagged any `&` outside quotes, but `2>&1` is fd-duplication
    redirection, not chaining (the rule bans `&&`/`;`/`|`/`$()`/
    backgrounding, never redirection). Fix: an `&` directly after `>`
    is exempt; genuine chaining/backgrounding operators are never
    preceded by `>`. Regression test asserts the 3 FPs now pass AND
    `2>&1 && ...`, `2>&1; ...`, `sleep 5 &` still fence. The other 11
    are GENUINE model misbehavior (habitual `| tail`/`| head`, `;`
    chaining, `$(go env ...)`, `git -C <candidate>`) and still fence —
    no code fix; they are retry-reason/harness-guidance material.
  - CHECKPOINT_COMPLETION (2): both VALIDATION invocations that
    over-verified AFTER the progress checkpoint declared
    "submit_envelope now" as next_action. Guard correctly bounded the
    post-checkpoint read window (8 reads allowed, fenced on the 9th)
    and forced submit_envelope; BOTH invocations SUCCEEDED. Fence
    worked as designed — genuine model over-verification, not a false
    positive. No code fix.
  - REPOSITORY_PROGRESS (1): GENUINE exact repeat — identical
    `grep -n -A12 "func validRoleName" organization/manifest.go` at
    two points in one uninterrupted work period. One-shot correction
    fired and the model heeded it (moved to `go test`). Fence correct.
  - PROGRESS_CHECKPOINT (5): INFORMATIONAL, not a fence. This is the
    harness's own context-condensation recovery message
    (execution-progress-checkpoint/1.2.0) that restores authoritative
    evidence and RESETS the no-progress guard (client.go:1709). Normal
    operation; no action needed.
- mlx-serve hot-cache SSM checkpoint inheritance bug: report drafted at
  OUTPUT/mlx-serve-issue-hotcache-ssm-inheritance.md; not yet filed.

## Template-tag leakage: ROOT CAUSE FOUND (in-context imitation)
Tool-call parameter leakage (command arriving as "create>\n",
"str_replace>\nnull") was proven by controlled experiment to be
self-compounding in conversation context:
- virgin session, 10 file_editor calls: 0/10 leaks
- fork of virgin session: 0/10 leaks (fork mechanism exonerated)
- fork of a session whose history contained leaked fragments: leaked
  on its FIRST call (1/1)
Not MTP (historical leak ran MTP-off), not hot-cache, not the fork
mechanism. Once a tag fragment is in context, the model reproduces it.
OPERATIONAL RULE: the first leak terminates the session; never continue
or retry past it (continuation mechanically guarantees more leaks).
The session that discovered this leaked repeatedly after the rule was
stated, confirming it; it was abandoned. ddalcu repro: fork a
conversation containing command="create>\n" tool-call events; first
tool call in the fork leaks identically.

## Deploy + confirmation run (2026-09-21, later same day)
- Daemon redeployed with 5b3b19a + 3139dab: backed up running binary to
  `tekrood.pre-3139dab`, killed tekrood (keepalive 7720 relaunched it),
  swapped with `rm + cp + codesign -s -`. Health `ok`, contract 0.12.0.
- Confirmation feature `01a0c5ef-58e7-73f7-88cb-f7d3caff75a9` (tiny
  kernel doc-comment request) ran FULLY AUTONOMOUSLY to
  AWAITING_ACCEPTANCE, PO recommendation PASS. 8 invocations all
  SUCCEEDED: refine, specify, design, implementation, validation,
  2 promotion, acceptance.
- GROUNDING FIX (5b3b19a) CONFIRMED: every grounding firing in the run
  was a GENUINE content-before-AGENTS.md race (repository_search /
  repository_view / `sed`/`cat` of source before AGENTS.md read).
  Orientation (glob, git metadata) NEVER fired a false grounding
  correction. (Note: the task brief embeds a serialized prior journal
  that contains the correction prefix string; a naive grep over
  MessageEvents double-counts it — count only real fence messages.)
- SHELL-DISCIPLINE FIX (3139dab) CONFIRMED: the only `2>&1` command in
  the run (`go test ./kernel/... 2>&1 | tail -30`) fired on its genuine
  `| tail` pipe, NEVER on `2>&1` alone. Zero `2>&1` false positives.
- NEW genuine defect surfaced (not a fence bug): the PO refine
  work_product carried 3 extra keys
  (preserved_acceptance_criteria, preserved_constraints,
  ambiguity_findings) beyond the 5 the strict DisallowUnknownFields
  stage decoder allows, so refine blocked with
  "planning role returned an invalid structured handoff". This is the
  known cost of the UNIVERSAL submit_envelope schema leaving
  work_product unconstrained: the grammar mask guards JSON syntax but
  not per-handler field sets. Recovered with retry-planning + a
  method-changing reason ("build work_product as EXACTLY five keys").
  Candidate permanent fix: tighten the PO refine brief, or add a
  per-handler work_product schema to submit_envelope (the deferred
  per-handler-schema option).
- retry-planning gotcha: `deadline_at` must be strictly AFTER the
  terminal invocation's own deadline AND within now+planning-deadline
  (8h). A deadline earlier than the failed invocation's is rejected
  with PLANNING_RECOVERY_REJECTED / "invalid feature request or plan".
- Team re-PAUSED after the run (keepalive auto-resumes on restart).

## FQN-alias v8 resubmit — first hiccup, root-caused by offline guard replay
- Resubmitted identical FQN-alias content under key phase10-prod-agent-alias-v8
  -> feature 01a0c66f-5d55-71bb-8b2d-c7c62b96866d. (No feature-level
  rollback/cancel command exists anywhere in the kernel/operator surface;
  FeatureCancelled has no transition path. The old 01a0c4b9 is left inert;
  nothing was promoted to main, so there is no code to revert.)
- PO refine asked 3 clarification questions (alias syntax/uniqueness,
  change/remove authority + lifecycle persistence, resolution surfaces).
  Answered consistent with the brief's stated product decisions; feature
  advanced to READY_FOR_PLANNING. This is healthy behavior, not a fault.
- refine/specify/design/2 implementation tasks SUCCEEDED. Then the task
  "Add ProductionService agent-alias lifecycle operations with strict
  operator scoping" (92a635ef) FAILED 3 consecutive attempts and is now
  BLOCKED ("task invocation failed without an admissible automatic
  recovery; operator escalation is required").
- OFFLINE GUARD REPLAY (Go test in-package, production decodeEvent +
  production guards over the real 623-event stream): the SEARCH_LOOP guard
  fired on a TRUE identical repeat — `grep -n "func (host \*Host)
  Heartbeat" -A 18 organization/host.go` at idx 600 and idx 618, same
  result, no intervening mutation. OVERLAPPING_VIEW guard: no violation.
  So the fence is CORRECT (fence false-positive hypothesis REFUTED).
- REAL failure (OBSERVED): the agent created its deliverable
  adapters/operationalruntime/agent_alias.go at idx 161 (real progress),
  then spent ~440 events grepping host.go / role_worker.go / the alias
  store trying to understand the ProductionService integration surface,
  made one more str_replace at idx 597, then fell into a genuine
  re-grep loop (5 REPOSITORY_PROGRESS corrections, each heeded for 1-2
  actions then re-repeated). Terminal code
  REPEATED_CAPABILITY_MISMATCH_REPOSITORY_NO_PROGRESS. The deliverable
  was never committed. This is the integration-comprehension /
  candidate-not-committed family, NOT a search-loop false positive.
- Verdict: the task's integration surface (ProductionService + Host +
  InProcessRuntime + mongo alias store) is too large for one bounded
  invocation; the agent flailed and the guard correctly terminated it.
  Fix is NOT a fence change. Options: (a) method-changing retry reason
  ("agent_alias.go already exists; do NOT re-grep host.go; read the one
  integration point, wire it, commit"); (b) planning fix to decompose the
  ProductionService wiring into smaller tasks.

## DISCRIMINATING EXPERIMENT (2026-09-22) — verdict: NOT task size
Two controlled retry-task experiments on the blocked task 92a635ef, each
preceded by resetting the worktree to its exact pre-invocation state
(HEAD 18ace9e, discard modified production.go + untracked deliverables).
- EXP 1 (80ffc743, attempt 4): reason = "don't grep host.go/store; read
  the ONE production.go wiring point; commit before finish." RESULT: the
  agent wired the four methods + production.go CLEANLY (no corrections
  through idx 147, created file, go build, wrote tests, ran go test once)
  — the wiring half was fixed. Then it fell into a DIFFERENT rabbit hole:
  building the lifecycle-persistence acceptance test, which needs a full
  Host+Store+ProductionService fixture. It grepped store.go/host.go/
  workflow_store.go/manifest.go/fake for ~600 more events, 7
  REPOSITORY_PROGRESS corrections, FAILED, deliverables written but NOT
  committed. Its own thinking: "check starter team actor names for the
  role lifecycle test", "check Host.NewHost requirements and the store
  interface it needs" — i.e. reconstructing a test fixture from scratch.
- EXP 2 (c5a2efaa, attempt 5): reason = same wiring guidance PLUS "the
  repo ALREADY has the pattern: production_integration_test.go builds a
  full ProductionService via NewProductionService (line 41); helpers
  startRuntimeMongod/closeRuntimeStore/loadStarterTeam exist; COPY that
  setup verbatim, do not invent a fixture." RESULT: SUCCEEDED. Committed
  d8a405a (645 insertions: 4 methods + production.go wiring + unit tests
  + a mongo_integration lifecycle test proving persistence across
  start/pause/resume/stop + simulated daemon restart). Only 2
  shell-discipline corrections (genuine pipes). Task COMPLETED.
- CONCLUSION: the failure is NOT "task too large" and NOT a fence false
  positive. It is the model's inability to REUSE an existing test-fixture
  pattern: it researches the fixture from scratch instead of copying the
  one that already exists. The decisive lever is pointing it at the exact
  copyable artifact (file + line + helper names), not generic "don't
  grep" advice. EXP 1's generic reason fixed the wiring but not the
  fixture; EXP 2's specific pointer fixed both.
- PLANNING IMPLICATION: implementation tasks whose acceptance criteria
  require a heavyweight integration fixture should either (a) name the
  existing fixture/helper to copy in the task description, or (b) be
  decomposed so the fixture-dependent test is its own task with the
  pattern pre-identified. This is a planning-brief quality issue, not a
  kernel/fence defect.
- STATE AFTER EXPERIMENT: task 92a635ef COMPLETED; feature 01a0c66f
  auto-resumed and dispatched 2 more tasks (HTTP/CLI alias surfaces);
  team re-PAUSED to hold state. Experiment deliverables saved to
  /tmp/exp1-deliverables/.

## mlx-serve v26.9.5 release-notes check (2026-09-21)
- New release v26.9.5 published 2026-09-21T15:26Z (repo ddalcu/mlx-serve,
  app com.dalcu.mlx-core; running here is 26.9.4).
- NEITHER of our two bugs is fixed in v26.9.5, and neither is filed as
  an open issue. The 23 commits between 26.9.4 and 26.9.5 are all
  GPU-memory-overrun / MTP-throughput / Bonsai-2 / Qwen-Image work; the
  prefix-cache changes are about not OOM-ing, NOT about bounding SSM
  checkpoint inheritance to the request's actual matched prefix. So the
  hot-cache SSM checkpoint-inheritance bug and the in-context
  template-tag imitation are still open and still worth filing.
- v26.9.5 breaking note: "Next release, MLX Core.app will become
  MLX-Serve.app, this will reset your app settings & updates" — relevant
  if we ever upgrade the deployment.

## Resume here (virgin session)
1. DONE — SHELL_DISCIPLINE diagnosed + fixed (3139dab): 3/14 were
   `2>&1` false positives, 11 genuine model misbehavior.
2. DONE — CHECKPOINT families all diagnosed: CHECKPOINT_COMPLETION (2)
   and REPOSITORY_PROGRESS (1) are correct fences; PROGRESS_CHECKPOINT
   (5) is the informational context-condensation recovery.
3. DONE — daemon deployed with 5b3b19a + 3139dab, re-PAUSED.
4. DONE — confirmation feature 01a0c5ef ran to AWAITING_ACCEPTANCE
   (PASS); grounding + shell-discipline fixes both confirmed live.
   STILL OPEN: operator feature-accept the FQN-alias feature
   01a0c4b9-dce8-75c6-92b1-d374ca4fef9c (revision 5, PO PASS) to close
   the RSI test — a human gate, not yet issued.
5. STILL OPEN — file both mlx-serve reports with ddalcu. v26.9.5 does
   NOT fix either bug (see release-notes check above); both remain
   unfiled and valid.
6. NEW — decide the permanent fix for the PO refine extra-fields
   defect: tighten the refine brief vs. add a per-handler
   work_product schema to submit_envelope.
