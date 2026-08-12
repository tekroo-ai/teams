# Phase 3 Step 14A — SMA-Q1 native-path amendment

## Decision

Step 14A prospectively replaces the unexecuted proxy topology in the original
SMA-Q1 preregistration with the completed SMA integration boundary:

```text
OpenHands persisted MessageEvents -> deterministic SMA capture and lifecycle
OpenHands UserPromptSubmit -> loopback SMA context bridge -> additionalContext
OpenHands model call -> configured model provider
```

The pushed Step 14 manifest remains immutable. It is superseded only for future
execution and retains `NOT_STARTED` with no result claim.

## Evidence timing

Before this amendment was authored, Gate 4 was known prior evidence. The SMA
checkout was observed clean on `master` at commit
`d5d49ca18d5845b4ba172cf169d4d7b196c5b38d`, tree
`5f2ccb3734be6ae36d194ddabc91df4b002c90a2`. No `docs/WP5*` file or Gate 5
artifact under `target` was observed. No WP5 measurement is used to choose the
scenario corpus, thresholds, or expected outcomes.

The fixed prospective manifest is
`investigations/sma-q1/preregistration-native-v2.json`. WP5 evidence is eligible
only when its receipt names the exact manifest digest and matches every frozen
scenario field and measurement rule. A generic Gate 5 PASS is not automatically
an SMA-Q1-v2 result.

## WP5 and Step 15 boundary

WP5 may produce valid evidence for first-prompt activation, parent/child
provenance, instruction-like recall, warm hit/no-hit latency, four-channel
concurrency, duplicate prevention, and restart continuity. Any non-matching or
uncovered scenario remains `NOT_RUN`.

Step 15 requires separate principal authorization. It may reuse exact accepted
WP5 receipts and executes only the remaining scenarios before adjudicating the
entire eighteen-scenario corpus. Neither Step 14A nor WP5 alone authorizes an
SMA-Q1 PASS.

## Identity qualification

WP4's canary partition derives from authenticated OpenHands workspace and
profile metadata. Step 14A tests that canary mechanism without claiming it is
the final Teams identity model. A model profile is not durable authorship or a
visibility boundary. Teams actor FQN, author identity, role, semantic scope,
and visibility remain separate adapter contracts.

## Authority boundary

SMA remains semantic memory only. It receives no workflow, process-control,
audit, acceptance, organizational, or outcome-measurement authority. Retrieval
is bounded, partitioned, untrusted, subordinate to current instructions,
fail-open for prompt submission, and fail-closed for protected content. Level 2
training remains disabled.
