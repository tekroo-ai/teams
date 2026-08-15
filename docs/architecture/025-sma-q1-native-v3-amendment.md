# Phase 3 Step 15 — frozen SMA-Q1 v3 amendment

## Status

This is an **accepted, frozen, not-yet-executed** repair to the Step 15
measurement protocol. It does not change the accepted SMA/OpenHands
architecture and does not claim an SMA-Q1 result.

The binding machine-readable package is
`investigations/sma-q1/preregistration-native-v3.json`. The package was accepted
and frozen separately from the content-addressed Step 15 v3 execution
authorization.

Frozen and authorized content addresses:

- v3 amendment: `6b683faf7437a454395c9691ab29f71cdcb1e221e19679fba471b9671c99ecc9`
- corrected runner: `59ea7d1b3bc5562993e801a361620c1a748be8423f78c579e486b27270e5fef7`
- exact registered child-tool artifact: `89575b6d659666f45b51c3d963b421cd174fd23e84b394714b2580b1a74e2c29`
- sealed execution identity: `a0c6d9a425544591e8ff9e7ba497c296ef19384f20eea5e48210f1072e1cba32`
- separate execution authorization: `af7d933c023b7b9766769b00de6fede2e8f6f2b87c4951faadcb488f5b70db0b`

## What remains unchanged

The v2 decisive question, four-memory synthetic corpus, eighteen scenarios,
exact prompts, faults, 96 repetitions, thresholds, stop conditions, and
PASS/FAIL/INCONCLUSIVE meanings remain binding by reference to the immutable v2
manifest. Neither v2 attempt receives v3 scenario credit.

## Evidence behind the repair

**OBSERVED:** Both v2 attempts completed
`SMAQ1N-001-FIRST-PROMPT-EMPTY` at 10/10. The initial attempt then stopped on a
harness assertion absent from the frozen adversarial text. The single permitted
corrective rerun promoted all three eligible memories with zero-exit
subprocesses, then stopped on `seed vector cardinality mismatch`. It did not
persist the two observed Qdrant counts or individual point receipts. Seventeen
scenarios and 86 repetitions remained `NOT_RUN`.

**OBSERVED:** Both attempts completed cleanup. No safety breach was observed
before the harness terminals. This cannot qualify any unexecuted safety
scenario.

**COMPUTED FROM PINNED SOURCE:** `ReplayWorker` always creates the episodic
projection for a successful eligible replay and creates the semantic projection
on the initial bootstrap replay. `QdrantVectorStore` derives one deterministic
point ID from each actual memory ID and stores both `memory_id` and `agent_id` in
the point payload. The three distinct eligible fixture memories therefore imply
three named points in each collection; the raw ineligible memory implies none.

The defect was not the existence of a `3/3` expectation. The defect was making
that expectation a blanket assertion after the harness had failed to retain the
observations needed to explain a mismatch.

## New setup gate

Before scenario 1, the corrected runner must qualify the exact fixture path in
separate disposable namespaces. It must retain MongoDB identity and lifecycle
receipts plus point-by-point Qdrant identity, payload, vector-presence, and
absence receipts before evaluating totals or cleaning up.

That qualification grants no scenario credit. It is cleaned completely, and
the measured namespaces are reverified empty. Scenario 1 still measures a new
conversation against an empty memory partition. The measured four-memory corpus
is then created and verified with the same point-level receipts before scenario
2.

## Other runner corrections

The v3 runner must also:

- count a scenario as started only after its first durable repetition receipt;
- distinguish one logical repetition from its direct-bridge, native-hook, and
  model-boundary probes;
- require actual delegated-child causation for scenario 11 rather than granting
  a pass for API-created parent/child topology alone;
- explicitly receipt the temporary oversize memories used only by scenario 17;
- retain every latency included in each frozen nearest-rank aggregate; and
- report every unstarted scenario and repetition as `NOT_RUN`.

## Gate

The freeze and separate execution-authorization gates are complete. Execution
may begin only when all recorded digests and the exclusive-runtime preflight
match. A mismatch stops before fixture qualification or scenario 1.
