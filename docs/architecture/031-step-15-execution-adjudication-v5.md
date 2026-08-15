# Step 15 V5 execution adjudication

Date: 2026-08-13  
Frozen manifest: `21030ef4a7004217401b0243e5bb7e94be6bc44ecb9869873a72f112fe676422`  
Status: **INCONCLUSIVE / NO-GO**  
SMA-Q1 result claim: **none**

## Outcome

The authorized V5 attempt stopped during disposable fixture qualification
before scenario 1. Zero scenarios and zero repetitions started. All 18
scenarios and all 96 repetitions remain `NOT_RUN`.

The static expectation self-test and full preflight passed. Frozen identity
bindings passed, no unrelated OpenHands conversation was running, and the SMA
execution worktree matched its pinned commit and tree.

## What V5 fixed

**OBSERVED:** Deterministic promotion passed for all three eligible fixtures,
including the adversarial fixture that stopped V4. The resulting corpus had
four distinct Mongo memories, three consistent reasoning-eligible memories,
three semantic points, and three episodic points. The fourth memory remained
raw and reasoning-ineligible and had no vector projection or Mongo embedding
reference.

This establishes that V5 corrected the V4 fixture-construction failures. It
does not establish SMA-Q1 effectiveness because measured scenarios never ran.

## Qualification terminal

**OBSERVED:** The first same-partition query—`What API timeout applies to
repository alpha?`—returned an empty context and no memory IDs. The expected
eligible timeout memory was therefore missed.

Two subsequent requests in the same service instance produced partition-aware
results:

- the beta request disclosed no alpha memory; and
- the alpha raw-memory request returned two eligible alpha memories but did
  not return the raw, reasoning-ineligible memory.

Thus, raw-ineligible denial and cross-partition denial were observed, but the
same-partition positive control failed and the fixture could not be qualified.

## Causal limit

**COMPUTED FROM PINNED SOURCE:** The bridge can return an empty fail-open result
for several different mechanisms, including failed conversation/workspace
resolution, an empty vector candidate set, or bounded retrieval failure or
deadline. The source does not let us select among those possibilities from an
empty response alone.

**INFERRED, NOT VERIFIED:** Because the first request missed and later requests
retrieved eligible, partition-correct memories, a transient first-request path
such as cold-path latency is plausible.

The qualification journal retained memory IDs and context hashes but omitted
the already-computed direct-call elapsed time, hit count, trace ID, and
before/after bridge counters. Exact cleanup removed runtime logs and the
disposable database, so the mechanism cannot now be proven. It would be
incorrect to label this a cold-start deadline without another evidence-bearing
attempt.

## Cleanup

**OBSERVED:** Post-run checks found no V5 MongoDB database, no V5 Qdrant
collection, no listener on TCP 8130, no V5 run root or fixture-driver build,
and no SMA worktree change. The ordinary non-test Qdrant collections remain.

## Adjudication and next gate

V5 attempt 1 is **INCONCLUSIVE / NO-GO**. It is not a failure finding against
SMA because qualification evidence cannot yet distinguish a transient product
behavior from another bounded empty-result mechanism. It is also not a pass.

The frozen V5 policy permits one corrective rerun for a documented harness
evidence defect that cannot alter an SMA outcome. The valid correction is
telemetry-only: retain each existing qualification call's elapsed time, trace
ID, hit count, and before/after bridge counters without adding retries, warmup
calls, delays, or changed criteria. That correction requires a new runner
identity and separate principal authorization. The current V5 runner must not
be executed again.
