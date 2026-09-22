# HANDOFF — v204 post-condenser-fix resume

Created 2026-09-22 just before restarting the agent-server to load the
condenser faithfulness fix (SDK commit `e226bbeb`). Restarting the
agent-server (launchd `com.tekroo.openhands-agent-canvas`, the process I run
inside) terminates the operator session; this note is the resume point.

## Root cause proven this session (run _r3, coder conv bdc34ae3)
Coder "mental breakdown" after compaction = three stacked causes:
1. PRIMARY (model): search-only strategy — 267 repository_search, 0
   repository_view across 1116 events; never read a file, only grepped to guess
   symbol names.
2. TRIGGER (condenser): compaction emitted a FABRICATED summary claiming it
   "read host.go, production.go, ..." — false; the SDK summarizing prompt had no
   faithfulness constraint.
3. AMPLIFIER (model): treated empty search (m=0) as "regex wrong, retry" and
   enumerated the alphabet / grew negated regex classes one char at a time.
Tool was CORRECT (1 error in 267 = model passed a file path; 120 accurate
m=0). REPOSITORY_PROGRESS fence fired correctly (evaded by 1-char variation).
Full analysis in AGENTS.md "Root cause: coder mental breakdown after compaction".

## Fixes (all committed)
- #1 SDK condenser faithfulness clause — SDK `e226bbeb`
  (openhands-sdk/.../condenser/prompts/summarizing_prompt.j2). 74 condenser
  tests pass. LOADED ONLY AFTER AGENT-SERVER RESTART (lru_cache'd template).
- #2 grounding + #3 empty-result discipline — teams `effba54`
  (application/operational_execution.go editableExecutionGuidance).
  Daemon binary `effba54` already DEPLOYED to
  /Users/paul/.local/opt/tekroo-teams-v4/bin/tekrood (rm+cp+codesign).

## Resume here (new session)
1. Verify agent-server reloaded the template:
   `python -c` render the summarizing_prompt.j2 and confirm FAITHFULNESS is
   present in the running server's view, OR just trust the restart.
2. Restart the daemon so it re-launches on binary `effba54` (supervisor
   com.tekroo.teams-v4-prod relaunches it; it comes up PAUSED).
   `kill <tekrood pid>`; confirm `go version -m` on the binary = effba54.
3. Re-PAUSE after restart (keepalive auto-resumes to RUNNING — re-PAUSE, then
   resume deliberately).
4. Fresh DB for a clean run: point tekrood.json
   mongo.database + teams_database_identity to
   tekroo_teams_v4_phase10_prod_r4 (DB name is NOT in model_profile_digest, so
   NO digest rebind needed; daemon bootstraps roles on empty DB). Preserve
   _r2/_r3/_prod.
5. Resume team, submit the FQN-alias feature (input preserved in _r3
   feature_requests; new idempotency key). Answer the refine clarification
   (see prior answers in _r3 feature 01a0cad9 refinement) to reach the
   IMPLEMENTATION stage.
6. WATCH the coder implementation task: confirm it now uses repository_view
   (reads files) and does NOT enter an alphabet-enumeration loop. Stop at the
   FIRST anomaly (any TEKROO_* correction, any FAILED invocation, any
   condensation that fabricates).
7. If clean through implementation + validation + acceptance, the RSI test is
   complete; operator feature-accept to close.

## Standing directive
Any anomaly = hard stop, diagnose to root cause, report. No workarounds, no
"known agent-behavior" waivers, no retries past an anomaly. Evidence-based
diagnosis only. Zero tolerance.

## State at restart
- Daemon binary effba54 on disk; daemon process still old (69823) until restart.
- Team PAUSED. DB _r3 has feature 01a0cad9 parked mid-implementation (attempt
  2) — leave it; use a fresh DB for the clean run.
- Agent-server: about to restart (loads #1).
