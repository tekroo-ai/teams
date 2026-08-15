# Phase 3 Step 15 — v3 execution adjudication

## Decision

Step 15 v3 is **INCONCLUSIVE / NO-GO**. It is not an SMA-Q1 failure and not an
SMA-Q1 pass. No measured scenario began; all 18 scenarios and 96 repetitions
remain `NOT_RUN`.

## Attempts

**OBSERVED:** The initial attempt passed the complete frozen identity and
authorization preflight, then stopped before creating a conversation because
the qualification workspaces lacked their `.openhands` hook link. Its final
receipt also referenced an uninitialized bookkeeping value. Zero scenario
repetitions started, and cleanup verified every disposable namespace absent.

The exact initial runner and raw journal were retained. The one corrective
rerun permitted by the frozen v3 policy fixed only those two harness defects.

**OBSERVED:** The corrective rerun created four exact source memories and
successfully promoted the three eligible memories. Before cleanup it retained:

- four distinct Mongo memory identities;
- three consistent, reasoning-eligible memories and one raw, ineligible memory;
- exactly three semantic and three episodic Qdrant points;
- one deterministic semantic and episodic point for each eligible memory, with
  matching actual memory and agent identities and 768-dimensional vectors; and
- no semantic or episodic point for the raw ineligible memory.

The gate nevertheless stopped because two runner expectations were wrong.

## Source-grounded classification

**COMPUTED FROM PINNED SOURCE:** OpenHands event intake derives the workspace
fingerprint from the first 24 hexadecimal characters of the canonical workspace
SHA-256. The runner expected the entire 64-character digest. The observed
24-character alpha and beta values exactly match the source algorithm.

**COMPUTED FROM PINNED SOURCE:** Replay writes the episodic vector to Qdrant but
does not persist an `episodic_ref` in the Mongo memory. It persists the semantic
reference during canonical update. The runner incorrectly required both
references in Mongo even though its point-level receipts independently proved
all six eligible projections.

These are harness expectation defects, not observed SMA projection failures.
However, qualification did not advance to its retrieval probes, so the frozen
fixture gate did not complete and no scenario may receive credit.

## Cleanup and boundary

Both attempts cleaned completely. A post-adjudication read found no Step 15
MongoDB database, Qdrant collection, bridge listener, qualification directory,
or measured-run directory. The detached SMA execution worktree is clean.

The sole v3 corrective rerun is consumed. A third v3 execution is prohibited.
The next valid route is a prospective v4 amendment limited to the two
source-grounded expectation corrections, plus a static expectation self-test
that runs without services before any live qualification. V4 would require
separate acceptance and execution authorization and would give the v3 attempts
no scenario credit.

The machine-readable adjudication is
`OUTPUT/phase-3/step-15-execution-adjudication-v3.json`.
