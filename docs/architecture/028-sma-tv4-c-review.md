# SMA TV4-C review

Date: 2026-08-13  
Reviewed SMA worktree: `/Users/paul/work/tekroo-ai/sma-teams-v4-alignment`  
Reviewed commit: `413942373e2b8ff5966284736c72090705536269`  
Reviewed tree: `1e618f593721cc2480673b447bba41adc263fc35`  
Decision: **PASS — ACCEPT/PUSH RECOMMENDED**

## Gate adjudication

**OBSERVED:** TV4-C is one clean local commit above the accepted and pushed TV4-B baseline `975a595bbe0a1474485a6240653131a9357312e6`. The branch is one commit ahead of `origin/teams-v4-alignment`; TV4-C has not been pushed.

**OBSERVED:** The implementation adds an unwired deterministic `TeamsManagedContextAssembler`, typed memory-candidate and trusted-policy inputs, and a content-free audit-sink boundary. Existing runtime services do not consume the assembler.

**OBSERVED:** Request/trusted-state correspondence is validated before candidate processing. Candidate authorization, project/repository/task/story scope, confidentiality and sensitivity, lifecycle/scope/power epoch, execution fence, variant/candidate/artifact/topology, human scope, eligibility, applicability, and supersession checks occur before the ranker observes a candidate. Denied candidates expose no memory identity in the returned denial decision.

**OBSERVED:** Protected context is returned only after the audit sink accepts the disclosure receipt. An audit failure returns empty context and the unchanged retrieval representation. Recalled text is explicitly framed as untrusted historical evidence. The disclosure receipt contains identities, policy/epoch/scope correspondence, memory identities/revisions/authorization reference, latency, and outcome, but no prompt or recalled-memory body.

**OBSERVED:** Source and receipt hashes independently matched `docs/TV4_C_GATE_RECEIPT.json`, including the accepted Teams 0.7.0 manifest SHA-256 `e2b9b5224a860a3eaa07451cf48fb5ac16a440b22b8dd592ff1662c1cff67f16`.

**OBSERVED:** The retained Surefire reports contain 379 root tests in 50 reports and 39 proxy tests in 6 reports, with zero failures, errors, or skips. Independent review reran `mvn -q clean verify`, `mvn -q -DskipTests install`, and proxy `mvn -q clean test`; every command exited zero. `git show --check` passed and the worktree remained clean.

**COMPUTED:** The test and source evidence covers the Gate-C positive same-scope cases and the required negative cross-principal, cross-project, cross-variant, embargo, topology, stale-fence/epoch, confidentiality, human-scope, caller-spoof, audit-failure, and ineligible-memory cases. The assembler is bounded to at most three expanded memories and 4,096 characters.

**INFERRED:** TV4-C satisfies its bounded deterministic Gate C and is fit to become the accepted alignment baseline. This does not qualify live transport authentication, Teams policy derivation, durable audit persistence, live OpenHands/Teams wiring, TV4-D continuity, TV4-E, WP6, or SMA-Q1.

## Residuals retained as later gates

- Teams policy derivation and transport authentication remain outside this pure boundary; the assembler treats its policy input as trusted.
- `maxTokens` is validated as positive but the deterministic package enforces the frozen three-memory/4,096-character bounds without a tokenizer.
- The audit sink is an interface with deterministic fakes; durable storage and runtime integration remain separately authorized.
- Candidate applicability and sensitivity remain supplied evidence. SMA enforces them but does not manufacture them.

These residuals match the authorized TV4-C scope and are not Gate-C failures.

## Recommendation

Accept commit `413942373e2b8ff5966284736c72090705536269`, push `teams-v4-alignment`, and stop before TV4-D unless the principal separately authorizes it.
