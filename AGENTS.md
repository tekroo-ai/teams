# Tekroo v4 engineering instructions

## Governing authority

The latest accepted package is
`CONTRACTS/tekroo.kernel.contracts/0.11.0/`; its source-lineage decisions are
binding successor requirements. Packages `0.1.0` through `0.10.0` remain
preserved for historical replay and compatibility analysis. Public-network or
production deployment retains separate authorization. Do not edit an accepted
or released package in place. If code and its governing contract disagree,
stop and report the disagreement; do not weaken fixtures to make code pass.

## Scope boundaries

- Implement v4 from the approved semantics, not by copying the v3 codebase.
- Begin with the pure deterministic kernel and in-memory reference adapters.
- Keep provider, MongoDB, transport, API, CLI, and serialization concerns outside
  the pure domain evaluator.
- Treat provider status, tool output, model assertions, delivery claims, and SMA
  recall as evidence only; none creates organizational truth directly.
- Do not execute OpenHands/SMA investigations, migrate data, deploy production
  systems, or adopt providers without their separate authorization gates.

## Correctness and evidence

- Label substantive findings `OBSERVED`, `COMPUTED`, or `INFERRED`.
- Preserve command, event, receipt, authority, evidence, and provenance identity.
- Make all asynchronous failures observable and bounded by timeouts.
- Preserve deterministic outcomes under replay, duplicate delivery, permitted
  reordering, fake-clock variation, and actor-process restart.
- Every counterexample becomes a retained regression fixture before closure.

## Qualification

Implementation unit tests supplement but never replace the normative contract
corpus. `PASS`, `FAIL`, `NOT_RUN`, and `INCONCLUSIVE` retain their exact meanings;
skipped or unavailable work is never reported as passing.

## Operational recovery lessons (run-060)

- Task recovery re-materializes the candidate workspace; the operator HTTP
  30-second read timeout killed its own `git` subprocess mid-recovery.
  Recovery runs on a detached context with its own bound.
- An interrupted operator recovery (unblock committed, authorize failed) must be
  resumable from its durable event checkpoint; never re-submit an `unblock`
  that is already in the task's event chain.
- An expired completion review whose successor ID is deterministic over the
  evidence set must be closed on the *same* ID before re-entry; otherwise the
  flow re-selects the stuck review forever.
- A validator stranded ACTIVE while its review target is COMPLETED must be
  re-driven through `completeEvidenceTask` on every pass.
- `POST /v1/conversations` status is `idle|running`; Teams treats `idle` as
  still-active, so a seeded predecessor conversation for an explicit-recovery
  profile must be `paused` before a recovery attempt can build its checkpoint.
- Feature acceptance binds the assembled candidate identity only to the
  whole-feature validator and the promotion; task-local validators may name
  narrower candidates.
- A `retry-task` request whose `deadline_at` exceeds now + the configured
  planning deadline window is rejected with
  `TASK_RECOVERY_REJECTED` / "validate recovery preconditions" — the block
  classification is not at fault. Reissue with a nearer deadline and a fresh
  idempotency key.
- Blocked-task recovery reasons must change the *working method*, not restate
  the task: agents that fail on the no-progress guard need explicit ordering
  ("create the deliverable file first; never grep for symbols in files you
  have not created this session"). Same reason text re-fences identically.
- The daemon can silently wedge: `/v1/status` shows `Worker.Active: 0` with
  `LastError: "external execution outcome is unknown"` and ~110% CPU while no
  authorize command is written for hours, even though dispatchable tasks and
  budget headroom exist. The projection/reconcile loop keeps serving reads.
  `tekroo stop` + relaunch restores reconciliation immediately; in-flight
  invocations resume. Check `last event timestamp` (UUIDv7 prefix) against the
  clock to distinguish a wedge from genuine idleness.
- Two agent-behavior fences now dominate failures: `REPEATED_CAPABILITY_
  MISMATCH_REPOSITORY_NO_PROGRESS` fires on `RepositorySearchAction` loops
  (ban search actions in the retry reason), and
  `EDITABLE_CANDIDATE_NOT_COMMITTED` when an agent writes its deliverable but
  ends without committing (require "commit before finish" in the reason).
  Both were cured by method-changing retry reasons. `REPEATED_SHELL_
  DISCIPLINE_VIOLATION` (grep/head pipes on go test, /tmp dumps) is the third
  family; ban pipes and redirection and select tests with -run in the reason.
- Wake-latency diagnosis must use one clock. Conversation event files under
  `~/.openhands/agent-canvas/dev_conversations/<id>/events/` timestamp in
  LOCAL time (UTC-6); daemon/kernel events are UTC. Comparing them without
  conversion makes correctly-firing wakes look queued or ghosted.
- Wake latency budget was 3x60s polls plus delivery (~8 min). Tightened to
  2x30s polls plus REWATCH every 3 min so a still-idle team re-alerts; when
  summoned by any wake, scan ALL task projections for BLOCKED or
  retryable-failure states, not only the wake's stated symptom.

## Operating a qualification daemon

Phase 9 run daemons may be supervised by ad-hoc `launchctl submit` KeepAlive jobs
that leave no plist on disk; discover them with `launchctl list | grep tekroo`
before stopping a daemon. Never combine `tekroo stop` with a manual foreground
start: the overlapping processes are recorded as physical host suspensions in the
qualification database, which fabricates execution-deadline allowance and
contaminates the run. Stop the supervisor first. With
`continuity.suspension_threshold` at 10 seconds, run-059 accumulated 34 bogus
suspension windows this way in about ten minutes.

Startup suspension reconciliation cost currently grows with suspension history
inside a fixed 20-second `startupTimeout`, so a crash-looping supervised daemon
degrades until it can no longer boot. See
`docs/architecture/127-phase-9-run059-shakedown-findings.md`.

## Official production stack (canonical since 2026-09-14)

Paul retired the phase-9 run daemons (run-060/090/095/100, phase10 run-009/010)
and replaced them with one official deployment. Its source tree is this
repository on `main` (contract 0.12.0, event-wait included).

| Service | Endpoint | Verified |
| --- | --- | --- |
| Teams daemon (operator HTTP) | `127.0.0.1:8787` | `RUNNING`, contract `0.12.0` |
| OpenHands / Agent Canvas | `127.0.0.1:8000` | `/health` `ok` |
| Qwen model server | `127.0.0.1:8800/v1` | `ddalcu--Qwen3.8-Flash-Next-MLX-Serve-mixed-4-8bit` |
| SMA retrieval bridge | `127.0.0.1:8130` | java `SmaRetrievalBridgeMain`, health route `/healthz` |
| Nomic embeddings | `127.0.0.1:11434` | ollama `/api/tags`, `nomic-embed-text:v1.5` |

Daemon config:
`/Users/paul/.local/share/tekroo/teams-v4-phase10-prod/config/tekrood.json`
(database `tekroo_teams_v4_phase10_prod`; bearer token in `./operator-token`,
mode 600 — never print it). All eight role profiles and their condensors use
port 8800; the daemon's `openhands.base_url` is port 8000.

Operate it with the installed CLI rather than hand-rolled polling:
`/Users/paul/.local/opt/tekroo-teams-v4/bin/tekroo -config <config> <command>`.
Aggregate kinds are lowercase kernel kinds (`task`, `story`, `work-invocation`,
`work-budget-account`, …). To wait for work instead of sleeping:

```
tekroo -config <config> wait task <uuid> --timeout 1h \
  --after-revision N --event-type tekroo.event.task.phase-changed
```

(`POST /v1/events/wait`, change-stream backed, returns `MATCHED`/`TIMED_OUT`,
bounded at 24h; MCP equivalent `tekroo.event.wait`.)

## Phase 9 run-060 outcome (historical, do not mutate)

Feature `01a09280` reached `ACCEPTED` on 2026-09-13: validators passed (the
production-wiring validator needed four attempts — two infrastructure failures,
one harness false positive on an out-of-repository redirect, one genuine output
defect), security reviews cleared, whole-feature validation passed, and the
release plan finalized. Database `tekroo_teams_v4_phase9_run060` is a
qualification record; preserve it for the harness-evaluation harvest.

Harness fixes proven during run-060, retained as regression fixtures:
checkpoint-completion rule (reads allowed), effect-gated purpose-mutation
policy, per-field candidate-workspace attribution, state-proportional
construction timeout, projector survivability, idempotent suspension replay,
suspension provenance kept out of the bounded classification-evidence slot
(`Valid()` caps that list at 64 — repeated windows accumulated 65 and made
startup fail permanently), deadline evidence bound to its causing budget
amendment (a second unbounded accumulator, 69 ids, fault-looped 826 times),
and out-of-repository scratch redirects (`> /tmp/…`) exempt from the mutation
classifier.

## Terminal transport hazard (A/B verified 2026-09-19)

Bulk multi-line input sent through the agent terminal corrupts: lines
duplicate and splice at the 80-column wrap boundary (26-line inputs landed
as 22-24 lines with 161/242-char splices). Cause isolated by A/B experiment:
bash readline is the trigger. With readline ON, 2/2 trials corrupted; with
`set +o emacs` (readline OFF), 4/4 trials landed byte-exact. The terminal
tool types keystrokes and scrapes the screen; readline's redraw makes it
re-send chunks, so bash receives duplicated bytes. The tool does not
"insert characters to wrap lines" — it re-types them.

Mitigations, in order of preference:
1. **FIXED in harness (SDK commit `95f3c376`, 2026-09-19):**
   `TmuxTerminal.send_keys` now sends multi-line payloads over 20 lines
   line-by-line with pacing, mirroring the #2181 fix in
   `SubprocessTerminal`. Regression tests in
   `tests/tools/terminal/test_tmux_bulk_input_corruption.py` (burst path
   corrupts 3/3 with a 60-line payload; chunked path byte-exact).
   Takes effect when the agent-server restarts — the currently running
   server still has the old code loaded.
2. Write file content with the file editor tool (direct disk write, no PTY),
   run it with a short single-line command.
3. Keep readline off in the agent shell: `set +o emacs` at session start
   (persists for the session; the harness may reset it, so re-check after
   terminal resets).

A "mangled heredoc" is this corruption, not a syntax error in your script.

## Operational monitoring (phase10-prod run)

- `organization.RoleInstanceState` has **no** `invocations` field. Reading
  `diagnostics.roles[].invocations` silently yields `[]` and makes any
  live-work counter read zero forever. The only accurate live-work signal is
  the `work_invocations` collection: states AUTHORIZED/CLAIMED/STARTED.
- Status-transition watchers cannot see a parked team (blocked tasks awaiting
  operator recovery produce no transition). Use `/tmp/run_idle_watchdog.sh`:
  wakes the agent on status change OR on live==0 with unfinished features
  (3-minute debounce, re-notify every 10 minutes).
- Agent conversations live under
  `~/.openhands/agent-canvas/dev_conversations/<invocation-id-no-dashes>/`;
  `base_state.json` `execution_status` plus `stuck_detection` is the ground
  truth for whether a STARTED invocation is actually making progress. A
  `paused` + `stuck_detection=true` conversation will not resume by itself;
  the harness does not consult the stuck flag (known gap).

## v202 amendment and honest re-qualification (2026-09-20)

Product-owner 2.0.1 and project-manager 2.0.2 add `repository.read` (tool
surface `glob,repository_search,repository_view`, tool-policy digest
`b9b6f5bf…`, identical to the architect's read-only surface). The original
`tekroo-message-handlers-20260913` private key is unrecoverable (cryptographic
search of every Ed25519-sized file found no match; never committed), so the
amendments are signed under the trusted deployment-owned key
`tekroo-teams-amendment-20260917`, mirroring the v201 project-manager 2.0.1
pattern.

Applying the amendment requires three coordinated steps, all digest-bound:
team.json (bundle path/digest/publisher/model-profile), tekrood.json (profile
identity + patched agent_settings + manifest rebind to the new team.json
sha256), and the durable `role_instances` documents — a STOPPED instance keeps
the model_profile_digest it started with, and
`registerRoleState` fails startup with `role is not configured` unless the
instance is re-bound in place to the new bundle/model digests (revision
preserved). The keepalive script auto-resumes the team to RUNNING on restart;
re-PAUSE after every restart.

The v201 project-manager qualification is NOT trustworthy: its evidence
conversation `01a0b0f8…` ran with `tools: []` and shows the model hallucinating
`read_file`/`web_fetch` ("Tool not found. Available: ['finish']"). A PASS was
recorded on a run that exhibited the bug. Do not treat v201 as a working
tool-surface precedent.

Honest v202 replay (conversation `65230500…`, PM worktree, new 3-tool profile):
tool use is correct — `glob` + `repository_view` read AGENTS.md and the
testdata fixtures, zero hallucinated tools, zero tool errors. But the result
envelope was malformed JSON (a stray `]` closed the stories array early), which
the strict parser in `role_handler_result.go` (single marker + strict Decode +
EOF) rejects. So the tool-surface fix is proven, while the model's
large-envelope JSON integrity remains a separate, unresolved defect. Do not
write a PASS qualification from a replay whose envelope the daemon would reject.

## mlx-serve MTP non-determinism — ROOT CAUSE CONFIRMED (2026-08-13)

Decisive per-request A/B on :8800 (idle server, identical large-envelope
content-mode request, temp 0, `max_tokens=4096`, brief =
`/tmp/pm_scenario_message.txt`):

- `enable_mtp:true`  → 3/3 runs **diverge** (sha 43defd5/cb62a47/9800396,
  lens 5598/4969/4759). Server log shows `spec-stats mode=mtp depth=6` active.
- `enable_mtp:false` → 3/3 runs **byte-identical** (sha 926bdd57, len 6081).
  No `mode=mtp` spec-stats emitted — server drops to PLD-only.
- `enable_mtp` field omitted → 3/3 byte-identical, same 926bdd57/6081.
- `enable_mtp:false` under forced concurrent load (3 background requests,
  `--max-concurrent 4`, prefix-cache contention) → still 3/3 byte-identical
  (c35627e4/5730), and returns to 926bdd57 when idle. PLD + prefix cache are
  NOT a non-determinism source on their own.

Conclusion: the per-request `enable_mtp` flag is fully respected by mlx-serve
26.9.4 and is the sole non-determinism source. MTP speculative decode accepts a
draft token the target model would not at temp 0 → single-token substitution at
the envelope tail (`]` emitted where `}` required, or vice-versa). PLD
(launch-flag `--pld`) is deterministic. Hypothesis 1 (MTP verification bug)
confirmed; hypotheses 2 and 3 refuted.

Operational fix: set `enable_mtp:false` on every role profile's
`litellm_extra_body` (agent AND condenser). The daemon's
`NewOpenAICompatibleAgentSettings` (`adapters/openhands/client.go:210`) already
emits `enable_mtp` per-profile; profiles 0 (architect), 5 (security), 6
(senior-coder) still carry `enable_mtp:true` in tekrood.json and must be
flipped to false before re-qualification, or their envelopes will keep
diverging. The GUI profiles `~/.openhands/profiles/tekroo-{architect,coder,
product-owner,project-manager,security,senior-architect}.json` also hardcode
`enable_mtp:true` in `litellm_extra_body`.

§6.5c "config drift / mutation layer": my own operator session's
`base_state.json` shows `enable_mtp:false` AND the server runs my requests
PLD-only (no `mode=mtp`), so the per-request flag is honored end-to-end for the
operator. The PM replays' `base_state.json` also recorded `enable_mtp:false`,
yet they diverged — the replay-era log window has rotated so the spec mode of
those exact envelope requests is unrecoverable. The most likely explanation is
that the replay `agent_settings` were sent but the conversation's LLM was
materialized from a profile that still had MTP on, OR the divergence was the
same MTP path via a stale value. Either way the fix is identical: force
`enable_mtp:false` everywhere and re-run.

## Qualification replay mechanics + PM/PO replay outcome (2026-08-13)

Evidence-id format: the daemon creates conversations with a client-generated
UUIDv7 (`conversation_id` field in `createConversation`, client.go:3445). The
qualification record's `validUniqueUUIDs` requires UUIDv7 evidence ids. The
earlier out-of-band replays let the server mint a UUIDv4 (no `conversation_id`
sent), which CANNOT serve as evidence. `/tmp/replay_qualify_v7.py` supplies a
client UUIDv7 and reads events from disk (events API rejects `?limit=` with 422).

Qualification record embedding: the running `tekrood` reads
`qualification`/`qualification_corpus` DIRECTLY from each tekrood.json profile
entry (currently null for PM/PO). The separate
`model-profile-qualifications.json` file is only consumed by `tekroo
init-local`, NOT by the running daemon. To qualify PM/PO, add the corpus +
qualification objects into their tekrood.json profile entries.
`qualificationDefinitionValid` (production.go:149) requires:
`corpus.ToolSurfaceDigest == profile.ToolPolicyDigest` (b9b6f5bf…),
`corpus.DecisionRoute == qualification.DecisionRoute == profile.DecisionRoute`,
`corpus.QualifiedRole == qualification.QualifiedRole == profile.RoleFQRN`,
same work kinds, and `QualificationDigest`/`corpus.Digest()` recomputable.
Digest = `sha256(json.Marshal(struct))` in Go field order; I verified a Python
replication (compact separators, sorted work_kinds+scenario_ids for corpus;
drop qualification_digest+revoked_at, sort qualified_work_kinds+evidence_ids for
qualification) reproduces all 5 existing records byte-exactly.

PM replay: PASS. Conversation `01a0bf47-ff51-76a6-be07-e0655e2b23bd` (UUIDv7),
tools glob×1 + repository_view×3 + repository_search×3 + finish×1, zero
hallucinated tools, envelope strict-VALID (marker idx 0, single, EOF-clean,
non-empty work_product, outcome=completed), ran PLD-only (MTP off). This is a
legitimate qualification evidence id.

PO replay: NOT YET QUALIFIED — 3 attempts all hit the no-progress guard. The PO
role has ZERO invocations in every DB (prod + phase9 + phase10 runs) and no
saved scenario, so the PO brief is a reconstruction from the genuine
`feature.submitted` handler + the real `actor-name-feature-request.json` fixture
+ PO role grounding. The tool surface works (glob/repository_view/
repository_search all function, zero hallucinations) — this is NOT a tool-surface
or MTP defect. The failure is agent behavior: the model drifts into Go
implementation archaeology (`func ParseActorFQN`, struct/schema names,
role_inbox.go/role_host.go alternating) — engineering work the PO charter
explicitly forbids ("Keep requirements at the user and product level; do not
pre-solve the engineering work") — then loops. A product-owner refinement should
read AGENTS.md + the admitted request and emit the refinement envelope in one
pass. Decision point surfaced to user: tighten the PO brief's method guidance
further vs. accept that PO needs a different qualification scenario.
