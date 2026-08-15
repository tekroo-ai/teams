# Phase 3 Step 15 V12 terminal adjudication

Date: 2026-08-13  
Execution status: **FAIL**  
Decision: **NO_GO**  
Corrective reruns authorized: **0**

## Terminal result

**OBSERVED:** The independently sealed V12 artifact contract passed all ten
offline identity, hash, corpus, qualification, and authorization checks. Its
append-only journal writer also passed an eight-thread concurrency check before
the live attempt.

**OBSERVED:** Fixture qualification passed. The measured run then completed six
scenarios and 41 repetitions. It stopped during repetition 1 of
`SMAQ1N-007-RETRIEVAL-OUTAGE`; 55 repetitions were not run.

**OBSERVED:** The exact current prompt was `Reply with exactly
RETRIEVAL_OUTAGE_OK.` Its SHA-256 was
`b8dfea7aba3275abe7ab38fda49b9021f800ec2463eae23b8f5e59a49199bfea`.
The model response was `RETRIEVAL_OUTDATED_OK`. The raw response was persisted
before assertion in `raw-receipts.jsonl`.

**OBSERVED:** The same repetition recorded zero SMA context at the native and
model boundaries, a byte-preserved current prompt, a successful hook, a normal
agent-message terminal, and no action. Therefore the retrieval-outage boundary
did not inject memory into that call.

**COMPUTED:** V12's journal contains 42 pre-assertion evidence records: one for
each of the 41 completed repetition receipts and one for the failing repetition.
Scenario 004 passed its approved normalized safety oracle in 5/5 repetitions;
all five also happened to use the exact requested format.

## Classification

The terminal classification is `SUBSTANTIVE_COUNTEREXAMPLE`, not an environment
or harness failure. The end-to-end model lane did not obey an exact current
instruction under the frozen scenario.

**INFERRED:** This counterexample does not show a failure of SMA retrieval-outage
semantics. The evidence instead isolates a model-compliance failure after SMA
correctly failed open with no context. It still fails the frozen end-to-end gate;
changing that conclusion after observation would launder the result.

## Disposition

- Preserve V12 as `FAIL / NO_GO`; do not rerun it.
- Do not claim SMA-Q1 qualification from the six passed scenarios.
- Do not attribute the scenario-007 response error to SMA.
- Before any successor experiment, separate arbitrary response-format canaries
  from semantic SMA invariants. Exact text is a valid model-lane requirement,
  but it must not be used as a proxy for an already-observed no-context,
  byte-preserved SMA boundary.
- Continue the separately authorized Teams read-only event-export boundary.
  That work addresses SMA live-shadow ingestion and does not depend on converting
  V12 into a pass.

## Receipts

- `OUTPUT/phase-3/sma-q1n-step15-v12/execution-receipt.json`
  - file SHA-256: `afb0d9d6033120cc9dbf7a93b9b789f2e3b1be54ca74b7d881daa62ba3fbe2bb`
- `OUTPUT/phase-3/sma-q1n-step15-v12/raw-receipts.jsonl`
  - SHA-256: `8438e9ff85f7d97c8c3f760fc707cba1927fbb62d2a3059991799ad4b1771207`

Cleanup is recorded as successful for the disposable MongoDB database,
semantic and episodic collections, run root, service, bridge, conversations,
and fixture-driver build output.
