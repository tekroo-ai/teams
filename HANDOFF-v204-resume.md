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
  - SHELL-DISCIPLINE (14): NOT YET DIAGNOSED - next task.
  - CHECKPOINT families (8): NOT YET DIAGNOSED.
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

## Resume here (virgin session)
1. Diagnose SHELL_DISCIPLINE family (14 firings): each correction's
   first line is TEKROO_SHELL_DISCIPLINE_CORRECTION:<action-id>; look
   up that id among ActionEvents to get the violating command;
   classify fence false-positive vs model misbehavior; permanent fix +
   proven regression test. Run invocation ids: re-query
   work_invocations in tekroo_teams_v4_phase10_prod.
2. Then CHECKPOINT_COMPLETION (2), REPOSITORY_PROGRESS (1),
   PROGRESS_CHECKPOINT (5 - may be informational, check intent).
3. Deploy daemon with 5b3b19a (build, rm+cp+codesign, launchd restarts
   it; re-PAUSE after - keepalive auto-resumes).
4. Re-run a feature to confirm grounding corrections are gone; then
   operator feature-accept the FQN-alias feature to close the RSI test.
5. File both mlx-serve reports (hot-cache inheritance; tag-leak
   imitation) with ddalcu.
