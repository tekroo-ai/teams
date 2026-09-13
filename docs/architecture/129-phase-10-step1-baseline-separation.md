# Phase 10 Step 1 — Phase 9 baseline separation

**Status:** COMPLETE
**Recorded:** 2026-09-13
**Machine receipt:** `OUTPUT/phase-10/step-1/baseline-separation-receipt.json`

## Result

Phase 9 run-060 is preserved without making its actor-name solution part of the
Phase 10 baseline. The Phase 10 worktree begins at the final committed run-060
infrastructure identity and carries forward the final uncommitted, general
runtime repairs. The complete actor-name implementation is retained in a
separate bare repository that is outside the baseline and outside every future
agent workspace.

The original operator request is preserved as a directly submittable JSON
fixture. It contains the request and its acceptance criteria, but it contains no
Phase 9 architecture, task plan, implementation, patch, candidate tree, agent
output, or validation conclusion.

## Three products

1. **Infrastructure baseline**
   - Worktree: `/Users/paul/.local/share/tekroo/phase10/worktree`
   - Branch: `codex/phase10-hybrid`
   - Committed lineage through `049fec7dc33b27cf677a085d765fa3ce991af022`
   - Final run-060 uncommitted runtime repairs copied byte-for-byte
   - Full `go test ./...`: PASS

2. **Reusable acceptance fixture**
   - `testdata/phase10/actor-name-feature-request.json`
   - SHA-256: `a715c0a895ac014d5c650311cb47d95addc19d528b3a0da037e78f6610416c2e`
   - Parsed as `organization.FeatureRequestInput` and passed `Validate()`
   - The clean Phase 10 baseline identity is bound only when the future run is
     created.

3. **Quarantined reference candidate**
   - `/Users/paul/.local/share/tekroo/phase10/quarantine/actor-name-reference.git`
   - Commit: `673d74054c04439b6eaa25e663fc28ecf8297bb4`
   - Tree: `8c9157dca4434effdcc475341567825166e8b25b`
   - `git fsck --full`: PASS
   - It may be compared with the Phase 10 result only after the new canary is
     terminal.

## Characterized baseline

The preserved test suite characterizes the existing CLI, MCP, HTTP, feature,
story, task, message, evidence, MongoDB, OpenHands, and restart/recovery
surfaces. Step 1 adds a fixture test that guarantees the clean canary request
remains valid input to the public feature-submission path.

Actor-name-specific strings that had leaked into generic OpenHands guard-test
examples were replaced with neutral examples. The guard behavior is unchanged;
the replacement prevents those tests from disclosing the prior solution to a
future canary agent.

## Boundary for subsequent steps

All Phase 10 production and contract work occurs in the Phase 10 worktree. The
canonical checkout and the completed run-060 repository remain untouched. No
Phase 10 code may read from, copy from, or expose the quarantined candidate.
The reference may be opened only after the Step 10 canary reaches a terminal
state.
