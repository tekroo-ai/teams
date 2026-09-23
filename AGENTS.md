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

Second corruption family (observed 2026-09-21, same session): **TAB
characters inside terminal payloads trigger bash filename completion**,
which splices a directory listing into the heredoc body. Never send
tab-indented content (Go source, patch scripts) through the terminal;
create it with the file editor and invoke it by path. A third family is
generation-level: template-tag leakage in tool-call parameters
(`command` arriving as `str_replace>\nnull`) — the same defect as the
`</summary></invoke>` leak in conv `01a0b4d5e4e2`, seen on MTP-on
sessions; retry the call or use the file editor.

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

## response_format json_schema: the RIGHT fix (keeps MTP speed) (2026-08-13)

The model card advertises `json_schema` capability, and mlx-serve DOES implement
grammar-constrained decoding. Decisive A/B (bracket-heavy schema, temp 0, 3 runs
per arm):

- `response_format: json_schema` + `enable_mtp:true`  -> 3/3 byte-identical, schema-conforming.
- `response_format: json_schema` + `enable_mtp:false` -> 3/3 byte-identical, schema-conforming (same bytes as MTP-on).
- plain (no schema) + `enable_mtp:true`               -> 3/3 DIVERGE.
- plain (no schema) + `enable_mtp:false`              -> 3/3 byte-identical.

So grammar-constrained decoding makes output valid-by-construction AND
deterministic EVEN WITH MTP ON: the MTP verifier rejects grammar-violating
draft tokens, so lossy acceptance cannot emit a wrong bracket. This is strictly
better than disabling MTP — it keeps the MTP speed boost AND guarantees integrity.

CRITICAL CAVEAT for our production path: the envelope is emitted as the `finish`
TOOL-CALL argument (`finish.message`), not a plain completion. When both `tools`
and `response_format: json_schema` are sent, mlx-serve ACCEPTS the request but
the schema does NOT constrain the tool-call arguments — the model returned a
free-text `message` string, not a schema-shaped object. OpenAI-compatible APIs
treat response_format and tool_calls as mutually exclusive; mlx-serve silently
ignores the schema on the tool path. So json_schema cannot directly guard the
current envelope, which lives inside a tool-call string argument.

BUT tool-call ARGUMENTS ARE grammar-constrained to the DECLARED TOOL SCHEMA.
Decisive test: a `finish` tool whose parameters are a strict nested schema
(enum + required + additionalProperties:false + minItems), 5 runs with
`enable_mtp:true` and 5 with false — ALL 10 conform to the schema. So the
grammar mask applies to tool args and MTP lossy acceptance cannot emit a
schema-violating token there.

THE CLEAN FIX (keeps MTP speed AND guarantees integrity): change the `finish`
tool schema so the envelope is the tool's STRUCTURED parameters, not a free-text
`message` string. Today the envelope (marker + JSON) is embedded inside a
free-form string argument, so the grammar cannot see the inner JSON and cannot
guard it. If `finish`'s parameters ARE the result schema (outcome/summary/
evidence/message_proposals/work_product as typed fields), grammar-constrained
decoding makes the envelope valid-by-construction even with MTP on. This is a
role/handler PROTOCOL change (role bundle finish tool + the daemon's
`role_handler_result.go` parser + result_protocol), not a config flip.

Options, ranked:
1. BEST: restructure `finish` to carry the envelope as structured parameters;
   keep `enable_mtp:true` everywhere. Requires editing the role bundles' finish
   tool schema + the daemon result parser. Preserves MTP speed.
2. Interim: disable MTP for envelope-emitting roles (proven to work, loses speed).
3. response_format json_schema on a plain completion — works but incompatible
   with the current tool-call-based finish protocol.

## VALIDATED: structured tool-call envelope fixes JSON with MTP ON (2026-08-13)

The user's insight was correct and the fix is now proven end-to-end. The
free-text wrapper (`finish.message` = marker + JSON string) was the entire
problem: the grammar mask guards tool-call ARGUMENTS as JSON at every depth,
but a JSON document smuggled inside a string literal is invisible to it.

Agent-loop validation (real PM brief, real agent loop via
`POST /api/conversations` with `client_tools:[submit_result]` whose parameters
are the full result schema, `enable_mtp:true` confirmed active server-side
`mode=mtp depth=6`):
- run 1 `01a0bfc6-e501-704f-877a-748e8b4d9678`: repository_view + submit_result,
  complete envelope, 4 stories (criteria 9/6/5/4), 8,816 chars.
- run 2 `01a0bfca-0734-7432-8064-440eb1065ac7`: same shape, 4 stories
  (7/5/4/4), 8,141 chars.
- run 3 `01a0bfcd-2592-75be-bf0b-ac2c6fc0fc4b`: STUCK in a repository_search
  loop (24 searches) and never submitted — the known agent-behavior
  no-progress failure family, NOT a JSON defect.
- JSON integrity: 2/2 submitted envelopes syntactically valid AND semantically
  complete, zero corruption, MTP on. The structured-envelope mechanism is
  proven. Remaining failure mode is agent behavior (search loops), orthogonal
  to envelope integrity and present with the old finish protocol too.

Key facts established:
* The SDK's lossy schema round-trip (from_mcp_schema -> pydantic ->
  to_mcp_schema strips enum/minItems/additionalProperties/const) does NOT
  matter: the JSON grammar mask enforces SYNTACTIC validity at every depth
  regardless of schema strictness, and the daemon's strict parser only checks
  structure. Wrong-value risk (e.g. bad enum member) remains but was not
  observed; the daemon validates values after parse anyway.
* `client_tools` is a top-level field of StartConversationRequest
  (ConversationConfig.client_tools); it coexists with agent_settings and the
  server injects the tool into the agent (conversation_service.py:1787-1797).
* A client tool CANNOT be named `finish` (collides with the builtin);
  `submit_result` works and the model calls it reliably when instructed.
* Raw-API (non-agent-loop) tests of tool choice are INVALID: with no system
  prompt the model hallucinates training-time tools (exec/Read/Agent). All
  earlier "scale test failures" were this harness artifact.

Implementation (small):
1. Daemon `client.go`: send `client_tools:[{name:submit_result,
   parameters:<handler result_schema>}]` on conversation create; add
   submit_result to the tool surface.
2. Daemon `role_handler_result.go`: read the envelope from the submit_result
   ActionEvent's parsed action fields instead of marker-scanning finish.message.
3. result_protocol text: instruct roles to call submit_result once with the
   structured envelope.
MTP stays ON everywhere. No SDK change needed.

## IMPLEMENTED: submit_result injection (2026-08-13, this session)

Design settled differently than the sketch above, for three discovered reasons:
1. Process-global client-tool registry: OpenHands registers one action kind
   per tool NAME and 422s a name reused with a different schema
   (ClientToolSchemaConflictError). Per-handler result schemas therefore
   CANNOT ride on one tool name. The injected schema is UNIVERSAL (envelope
   fields, work_product unconstrained {"type":"object"}); per-handler
   result_schema validation stays daemon-side after extraction. Proven: with
   work_product fully unconstrained the grammar mask still produced a
   structurally valid 12,032-char envelope (conv 01a0bff5…, MTP on) — the
   mask enforces JSON syntax at every depth regardless of schema strictness.
2. Fork loses client_tools injection: a fork's agent is rebuilt from
   agent_settings, so the tool entry must ride INSIDE agent_settings.tools
   ({"name":"submit_result","params":{"spec":…}}) to survive fork/restart;
   the spec is ALSO sent as client_tools so the class registers (create path
   does not self-register from settings — a spec-only create 500s at first
   run). Verified combination: create 201, no duplicate entry, resolves at
   first run, model calls it (probe_combo2, conv 01a0bff8…).
3. No digest ripple: injection happens at REQUEST time in
   createConversation/createOrForkConversation, gated on brief.MessageHandler
   != nil. Stored profiles/qualifications are untouched; the 5 existing
   qualifications survive. conversationAgentMatches strips the injected tool
   from the materialized surface before comparing. withSubmitResultTool
   refuses profiles without an explicit tools list (Step-15 defaulting).
Extraction: observationAt captures the last submit_result ActionEvent after
the prompt index; submitResultActionOutput strips the SDK "kind"
discriminator (additionalProperties:false) and emits
marker + "\n" + envelope, so ALL downstream validation
(ValidateRoleHandlerResult, roleHandlerWorkProduct, strict parser) is
unchanged. finish+marker remains the fallback path.
Tests: adapters/openhands/client_submit_result_test.go (4 tests: kind-strip,
idempotent injection + refusal, agent-matches tolerance, end-to-end create
payload). Handler result-protocol instruction now directs submit_result
first, finish+marker as fallback. Validation/review/promotion/handoff
purposes are NOT handler-bound and keep finish+marker (their shapes differ
from the universal schema).

## Fence false-positive fixes and recovery gotchas (2026-09-21)

- Shell-discipline fence: an `&` directly after `>` is the file-descriptor
  duplication operator (`2>&1`, `>&file`), NOT chaining. The rule bans
  `&&`/`;`/`|`/`$()`/backgrounding, never redirection. `violatesShellDiscipline`
  now exempts `&` preceded by `>` (3139dab). Genuine chaining/backgrounding
  operators are never preceded by `>`, so they still fence.
- Grounding fence: orientation is content-based (glob, git metadata via
  `repositoryFileListingAction`/`gitMetadataAction`), not tool-based. Only a
  genuine content read (repository_search, repository_view of source,
  `sed`/`cat` of source) before the AGENTS.md result lands is a real violation
  (5b3b19a).
- When counting fence firings from conversation event files, the task brief
  embeds a serialized prior journal that literally contains the correction
  prefix string; a naive grep over user MessageEvents double-counts it. Count
  only genuine fence messages (a `TEKROO_*_CORRECTION:<action-id>` whose id
  matches a real ActionEvent after the prompt).
- retry-planning `deadline_at` must be strictly AFTER the failed invocation's
  own deadline AND within now + planning-deadline (8h). An earlier deadline is
  rejected with `PLANNING_RECOVERY_REJECTED` / "invalid feature request or
  plan" — the block classification is not at fault.
- The universal `submit_envelope` schema leaves `work_product` unconstrained,
  so the grammar mask guards JSON syntax but not per-handler field sets. A PO
  refine can add extra keys (e.g. preserved_acceptance_criteria) that the
  strict `DisallowUnknownFields` stage decoder rejects, blocking the planning
  task. Recover with retry-planning + a method-changing reason ("build
  work_product as EXACTLY five keys"). Permanent fix options: tighten the
  refine brief, or add a per-handler work_product schema to submit_envelope.
- mlx-serve v26.9.5 (2026-09-21) fixes NEITHER the hot-cache SSM
  checkpoint-inheritance bug NOR the in-context template-tag imitation; both
  remain unfiled and valid. Its prefix-cache commits are GPU-memory-overrun
  fixes, not checkpoint-bounding. Note: next release renames MLX Core.app to
  MLX-Serve.app and resets app settings.

## v204 constrained-schema re-qualification (2026-09-22)

- Making a role bundle's `result.schema.json` canonical (constraining
  `work_product`) changes the bundle digest, which changes the DERIVED
  `model_profile_digest` = sha256(role, bundle_digest, agent_settings)
  (client.go:160). This cascades: team.json PO `model_profile_digest`,
  tekrood.json profile `model_profile_digest` + qualification + corpus,
  and the durable `role_instances` doc's `model_profile_digest`/`bundle_digest`/
  `manifest_digest`/`bundle_version` (rebind in place, revision preserved, or
  `registerRoleState` fails "role is not configured" at boot). The qualification
  binds `model_profile_digest`, so ANY bundle change invalidates the model-profile
  qualification by design — a correct security property, not a bug.
- Qualification chicken-and-egg: refine admission itself requires a
  qualification, so a genuine re-qualification must be an OUT-OF-BAND replay
  (direct `POST /api/conversations` with the role's agent_settings + injected
  `submit_envelope` + `client_tools`, valid client UUIDv7 id, faithful brief
  carrying the NEW result_schema). Author the corpus+qualification with the REAL
  Go digest functions (`QualificationCorpusDefinition.Digest()`,
  `QualificationDigest`) — never hand-rolled. Attest only work kinds genuinely
  replayed (PO = DESIGN via refine, RELEASE via product-acceptance over a real
  candidate workspace).
- `data` in `role_instances` is BSON Binary (JSON bytes), not an embedded doc:
  write back `new Binary(Buffer.from(JSON.stringify(j)), Binary.SUBTYPE_DEFAULT)`.
- Pre-assignment identity guard (feature_stage_result.go): rejects actor FQNs in
  planning text not present in the feature, to stop the model pre-assigning
  routable work. FALSE POSITIVE when a PO names the feature's OWN operator actor
  (teams::operator-1) as subject-matter vocabulary. FIXED 21cc880: seed the
  allowed set with the feature's authoritative OperatorActor/ProductOwnerActor;
  invented worker FQNs still rejected.
- Decoder-defect self-recovery: when a BLOCKED task's blocker is exactly
  `invalidPlanningOutputReason` for that invocation and a redeployed validator
  now accepts the STORED output, the reconcile loop reuses the immutable output
  (no new model call) and the task recovers to RUNNABLE/COMPLETED. This is how
  the blocked refine recovered after the guard fix — no operator retry needed.
- A valid FEATURE_REFINEMENT with non-empty clarification_questions drives the
  feature to CLARIFICATION_REQUIRED (human gate) — the designed outcome, not a
  failure.
- Terminal bulk-input hazard RECONFIRMED: multi-line python/heredoc through the
  terminal corrupts (splice/duplicate at wrap). Write scripts with the file
  editor and run by path; `git commit -F msgfile`, never `-m "$(cat <<EOF)"`.

## Root cause: coder "mental breakdown" after compaction (2026-09-22, run _r3)

Diagnosed the degenerate search loop on implementation task 70c633a6 (coder,
conversation bdc34ae3…). It is NOT a fence false positive and NOT a broken tool.
Evidence from the full 1116-event record:

- Tool histogram: terminal 9, glob 1, file_editor 75, repository_search 267,
  repository_view 0. The model NEVER used repository_view in the entire
  conversation — it never actually read any file. It only ever grepped
  (repository_search) to GUESS symbol names.
- The tool was correct: 267 searches, exactly 1 is_error (the model passed a
  FILE path `adapters/operationalruntime/production.go`; the tool requires a
  directory — a model error, correctly reported). 120 empty (m=0) results were
  ACCURATE: the symbols genuinely do not exist (e.g. `func (host *Host) Team`
  / `Manifest` are absent; only `Roster` exists). Searches with path omitted
  searched the whole workspace correctly — right directory, valid responses.
- 5 condensations fired. The condensation summary FABRICATED its COMPLETED
  section: it claimed the model "read key files (actor_alias.go, production.go,
  host.go, …)" — but repository_view count is 0, so no file was ever read. The
  summary is a lossy reconstruction that hallucinated a reading history.
- The breakdown mechanism: after compaction dropped 122 forgotten events, the
  model — believing (from the fabricated summary) that it had "read host.go" —
  tried to RECALL the method names it thought it knew by brute-forcing regex
  character classes: enumerating the alphabet one letter at a time
  (`[N]`,`[L]`,`[I]`,`[J]`,`[K]`…) and incrementally growing negated classes
  (`[^a-zc]`→`[^a-zct]`→…). When a search returned m=0 it did NOT conclude
  "symbol absent"; it treated the empty result as "my regex was wrong, adjust
  one char and retry" — a self-reinforcing recall loop.
- The REPOSITORY_PROGRESS fence correctly detected exact repeats (max exact
  repeat was only 3; the model evaded it by varying the pattern by one char).
  The fence worked; it cannot catch "semantically identical, syntactically
  varying" enumeration.

Root cause = three stacked causes: (1) PRIMARY: search-only exploration
strategy — the model never grounded itself by reading files, so it had no
ground-truth in context; (2) TRIGGER: condensation dropped the thin evidence and
emitted a fabricated summary claiming files were read; (3) AMPLIFIER: the model
interprets empty search results as "regex wrong, retry" instead of "symbol
absent." Compaction is the trigger, not the sole cause — the search-only
strategy is the underlying defect.

Generalizable fixes to evaluate (not yet implemented):
- Condenser integrity: the summary must not claim actions that did not occur.
  A fabricated "read X" is worse than an honest "searched for X." Investigate
  the condenser prompt/model for action-fabrication.
- Grounding: the implementation brief should force repository_view (read the
  actual file) before symbol-level repository_search; the deployed grounding
  fix (5b3b19a) exempts orientation but cannot force a model that never reads.
- Empty-result discipline: teach the model that m=0 means "absent, change
  approach (read the file / glob the dir)," not "tweak the regex."

## Root cause: PM "specify" search-loop / never-submits (2026-08-13, run _r8)

Diagnosed the FAILED project-manager specify invocation 01a0ce69 (conversation
01a0ce69, task ebbb4105 "specify: software-development"). This is a DISTINCT
failure family from the coder compaction breakdown above — it is NOT a
condenser-fabrication or grounding defect.

Evidence from the full 168-event record:
- Grounding was CORRECT: first content action was repository_view AGENTS.md
  (event 11), then the testdata fixture. No grounding fence fired.
- The single condensation (event 120) summary was FAITHFUL: every "Read X"
  matched a real repository_view action (invocation.go, handler.go,
  kernel/types.go, operatortools/service.go). The condenser faithfulness fix
  (SDK e226bbeb) WORKED — no fabricated reads.
- Tool mix: 33 repository_search, 13 repository_view, 3 glob, 0 terminal.
  Unlike the coder, the PM DID read files (13 views) — it was not search-only.
- The defect is a never-submits search loop: 33 searches, only 5 empty (m=0),
  and the searches were mostly DISTINCT and productive (each returned new
  symbols). The model kept orienting and never emitted the specification
  envelope. One pattern (ProductionService SendMessage) repeated 4x — the
  REPOSITORY_PROGRESS fence fired on that exact repeat (event 154).
- After the REPOSITORY_PROGRESS correction (154) and the CHECKPOINT_COMPLETION
  correction (163, "read allowance exhausted, call submit_envelope exactly
  once"), the model issued ONE more search (event 157) and was interrupted
  (InterruptEvent 161/167) — it NEVER called submit_envelope. Invocation
  terminal outcome FAILED, Retryable:false.
- A separate minor side effect of the repository_search schema change: at
  event 27-28 the model called repository_search with a HALLUCINATED parameter
  `exclude2` (alongside a valid `exclude`); the strict schema correctly
  rejected it (extra_forbidden). The schema change is working as designed; the
  model invented a parameter name. Not the cause of the FAILED outcome.

Root cause = a planning-role "orientation trap": the specify brief asks the PM
to ground a specification in the repository, and the model treats that as a
license to exhaustively map every existing surface (federation alias, operator
identity, roleControl, PrincipalRef, ProductionService, event/audit types,
RoleLibraryEntry, lifecycle funcs) before writing anything. It is a
breadth-first orienter that never reaches the emit step. The fences detected
the loop (exact repeat + exhausted read allowance) but the model could not
recover to submit — it kept searching even after being told to stop reading and
submit. This is the AGENTS.md-documented "planning role loops and never submits
/ no-progress guard" family, independent of compaction.

Distinction from the coder breakdown: the coder failure was search-ONLY (0
reads) + fabricated condensation + alphabet-enumeration after compaction. The
PM failure is read+search (grounded, faithful condensation) but a breadth-first
orientation loop that never reaches submit_envelope. Same "never produces the
deliverable" symptom, different mechanism.

Fixes to evaluate (not yet implemented):
- Emit-first planning brief: the specify/refine/design brief should require the
  role to emit a first-pass result envelope EARLY (a draft specification from
  the admitted refinement + AGENTS.md), then optionally refine it, rather than
  orient exhaustively first. The deliverable must be produced before deep
  codebase mapping.
- Harder submit steering after CHECKPOINT_COMPLETION: the model ignored
  "call submit_envelope exactly once" and searched again. The last-mile
  correction may need to be enforced (e.g., reject further read actions after
  the allowance is exhausted) rather than only instructed.
- Hallucinated-parameter note: adding `exclude` gave the model a name to
  hallucinate (`exclude2`); the strict schema rejected it correctly, but a
  model that guesses parameter names will keep doing so. Not a defect.


## Root cause: `view_range` emitted as a truncated string (2026-09-23, run _r9)

Observed three times across roles (PM `view_range='[1,'`, coder-2
`view_range='[205,'`, and an `_r8` `exclude2` sibling): the model emits a
tool-call argument that should be a list as a *string* holding a truncated
array fragment. The daemon/SDK correctly rejects it (`Input should be a valid
list`), the AgentError is classified `retryable`, and the model recovers on the
next turn. No task was lost to it in _r9.

Investigated to root cause (option 3 first, per directive) and the obvious
hypotheses are all DISPROVEN:
1. NOT generation truncation. The raw `tool_call.arguments` JSON is COMPLETE
   and valid — the envelope closes properly. The model deliberately emitted
   `view_range` as a string value `"[205,"`, not a cut-off stream.
2. NOT MTP. The offending invocation was coder-2 = coder profile, which has
   `enable_mtp:false`. (architect/security/senior-coder still carry
   `enable_mtp:true` and are a separate AGENTS.md-flagged cleanup, but they are
   not this defect.)
3. NOT schema strictness. Decisive per-request experiment on mlx-serve :8800
   (temp 0, identical "view lines 205-230" prompt): the NON-strict
   `to_openai_tool` schema (view_range as `{"type":"array","items":
   {"type":"integer"}}`, no `strict:true`) produced a valid `[205, 230]` list
   5/5; the OpenAI-strict variant produced the same 5/5; strict+MTP-on also 5/5.
   So a strict schema is NOT the lever — the model already emits a correct list
   almost always, and strict mode would not have prevented the rare string slip.

Conclusion: a rare, transient model emission defect (the model reaches for a
stringified array fragment), not a harness, schema, or MTP defect. The existing
retryable classification is the correct and sufficient handling. A recovery
validator cannot fix the observed case (the string is truncated — the second
element is absent, so it cannot be reconstructed). DECISION: document-only, no
code fix; do not add a strict-mode carve-out that the evidence shows is not the
cause. If this ever hard-fails a task (repeated on one invocation), revisit.

## Root cause: coder-1 implementation "orientation trap" (never writes) (2026-09-23, run _r10)

FAILED invocation e3da34fe (task d4928ed5 "cmd/tekroo: alias CLI commands",
coder-1). This is the AGENTS.md-documented planning-role "orientation trap /
never-submits" family, now observed on an IMPLEMENTATION role for the first
time. Evidence from the full 299-event record:

- 0 condensations — NOT a compaction-triggered breakdown (rules out the _r3
  coder family).
- 23 file_editor actions, ALL `view` (reads); 0 writes, 0 commits, 0
  submit_envelope. The model read and searched but NEVER wrote the deliverable.
- 57 searches, 48 distinct — it WAS grounding (unlike the search-only _r3
  coder), but `recipient` was searched 7x in the SAME directory
  (adapters/operationalruntime) as exact repeats.
- 4 REPOSITORY_PROGRESS fences, each a correct exact-repeat detection; the model
  could not recover to the write step and was interrupted (terminal FAILED).
- Terminal commands were all orientation (ls, wc -l); no build/test/commit.

Retry 18f921fd SUCCEEDED in 45 actions: first WRITE at event 48, then
build->test->commit->submit. The retry brief carried a generic recovery
preamble + recovery_directive whose reason was
"automatic glitch recovery: REPEATED_CAPABILITY_MISMATCH_REPOSITORY_NO_PROGRESS
(<the repeated recipient search command>)".

KEY FINDING: the retry succeeded WITHOUT a strong method-changing reason. The
recovery_directive.reason only names the offending repeated command and says
"address it"; it does NOT say "write the file first." The generic recovery
preamble contains only mild method guidance — "Inspect Git status, recent
commits, and the focused diff before reading source broadly" — plus a
misleading hint that "the workspace may already contain a completed
implementation from the failed invocation" (false here: the failed attempt wrote
nothing). The retry still did ~14 orientation actions (git status, git log,
grep, view) before its first write at event 48 — it was NOT a clean
"write-first" recovery. So the cure is weak and unproven: the retry succeeded
on a fresh attempt that happened to reach the write step, with the preamble's
git-first nudge as a possible but unconfirmed contributing factor. This does
NOT validate the automatic glitch-recovery reason as a method-changing cure for
the orientation trap; it restates the symptom command.

Implications:
- The orientation trap is NOT compaction-specific and NOT planning-role-only;
  it hits implementation tasks too. The distinguishing signature is
  read-heavy + never-writes + exact-repeat search loop + interrupt.
- Automatic glitch recovery (REPEATED_CAPABILITY_MISMATCH_REPOSITORY_NO_PROGRESS)
  recovers by fresh-attempt variance, which is unreliable — a same-brief retry
  can re-enter the same trap. A principled fix should make the FIRST attempt
  emit-first (create the deliverable file early, then refine), so the trap is
  prevented rather than re-rolled.
- Candidate fix (not yet implemented, needs a digest-neutrality check): add
  emit-first ordering to editableExecutionGuidance in
  application/operational_execution.go — "create the deliverable file before
  broad repository mapping; do not exhaustively search before your first write."
  Verify brief text is not in model_profile_digest (it is not) so no
  re-qualification cascade.
