# Phase 3 Step 15 — pre-execution identity refresh

## Outcome

Step 15 is `READY_PENDING_EXCLUSIVE_RUNTIME_WINDOW`. The original source-level
blockers are closed, the final separately authorized pre-Q1 closure canary is
`PASS / GO`, and the prospective runtime identity is content-addressed. No
`SMAQ1N-*` scenario has run.

The frozen investigation remains unchanged: 18 scenarios, 96 repetitions, and
manifest SHA-256
`c618371d571d5333aebd2bdc83d2db2559115f5ec3e1903b33e8e0ab157edf5d`.
The prior WP5 adjudication remains binding and supplies zero Q1 scenario credit.

## Frozen SMA lane

The Step 15 SMA checkout is the detached worktree
`/Users/paul/work/tekroo-ai/sma-step15` at commit
`60a966234166ea75f767b25e3cbb7eaabe4064a2`, tree
`7aaa06a68d59dd6f13b0a3a2c6ae3cff845c1121`. It is isolated from the
`teams-v4-alignment` development worktree. Its complete Maven verification
exited zero.

The runtime source correction was tested at commit
`ea70423a9bf9b9ecd01091736b8a0720fbdb4644`. Final production-shaped closure
run `12cd48b4d9204f0f8608c245530f67e1` recorded 31 semantic audits and 31
terminal outcomes with exact bijection. Its maximum applicable external bridge
latency was 353.703125 ms. This is prerequisite evidence, not Q1 execution.

## Closed blockers

Current source buffers no more than 1,048,576 request bytes and probes one
additional byte for overflow. Context work uses four workers and an eight-entry
queue with explicit rejection and fail-open behavior. These close the two
findings that invalidated the first identity candidate.

## Prospective execution identity

`investigations/sma-q1/step-15-execution-identity-v2.json` pins the SMA build,
OpenHands modified runtime, direct `qwen3.6-fast` projection, Agent Canvas,
Ollama binary and model blobs, MongoDB, Qdrant, host, and new disposable
namespaces. Secret values are excluded.

The planned v2 MongoDB database, both Qdrant collections, and both actor
workspaces were absent at preflight. Because services and profiles are mutable,
their identities and absence must be recomputed immediately before scenario 1.

## Parallel work boundary

SMA TV4-A pure schema and validator development may continue independently.
Measured Step 15 execution must wait until that process is idle and no unrelated
local OpenHands/Ollama prompt or runtime-heavy SMA test is active. The identity
refresh therefore does not claim an exclusive measurement window.

## Next gate

Immediately before scenario 1:

1. confirm exclusive local runtime use;
2. recheck all mutable identities and disposable namespace absence;
3. install bounded receipt, cleanup, deadline, queue, memory-pressure, and stop
   observers; and
4. execute the frozen corpus in order, stopping on its first absolute stop
   condition.

No production-readiness, WP6, live Teams integration, or historical migration
claim follows from this readiness result.
