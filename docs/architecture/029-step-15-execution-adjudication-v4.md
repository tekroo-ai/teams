# Step 15 V4 execution adjudication

Date: 2026-08-13  
Frozen manifest: `2178116c866e9abed9b5e83edd3c9c221f55f81e6f343a168205209f25834d23`  
Status: **INCONCLUSIVE / NO-GO**  
SMA-Q1 result claim: **none**

## Outcome

Both authorized V4 attempts stopped during disposable fixture qualification before scenario 1. Zero scenarios and zero repetitions started. All 18 scenarios and all 96 repetitions are `NOT_RUN`.

The static expectation self-test and full preflight passed in both attempts. Both attempts completed exact cleanup. No production or historical SMA memory was used or modified.

## Initial attempt

**OBSERVED:** Four distinct captured memories existed. The three intended eligible memories and the intended raw memory all had one semantic and one episodic point. The raw memory remained `raw`, remained `reasoning_eligible=false`, and had document version 6, but it also had a semantic reference and 768-value vectors in both collections. The frozen readiness rule therefore stopped the run.

**OBSERVED IN PINNED SOURCE:** `SmaServiceMain` starts `ReplayQueueScheduler` unconditionally. The scheduler selects first-touch raw memories. `ReplayWorker` writes episodic and initial semantic projections before its lifecycle transition determines the final state and reasoning eligibility.

**COMPUTED:** The qualification harness failed to isolate controlled explicit promotion from the normal background replay scheduler. The raw memory's projection state is consistent with one background replay. It is not evidence that the memory was disclosed; retrieval qualification never ran.

Receipt identities:

- execution receipt: `4f1f33d90f2d358d59ac8d1b161fc01904546248112e2846261d41ce5faaa492`
- raw journal: `7fa6a99f7106203a332d8ed548f0b06437738504a42b5b4d48c05d71465ca868`

## Corrective rerun

The frozen V4 rerun policy allowed one corrective attempt because no scenario had started. A separately content-addressed runner used a nonmatching replay canary only during controlled corpus construction, preventing the background scheduler from racing the explicit promotions.

**OBSERVED:** The first explicit promotion passed. The second, for `mem-alpha-adversarial`, returned a nonzero Maven exit. The fresh Surefire report records one test, one failure, and the exact assertion: `canonical output must retain the codename`. Cleanup ran immediately.

The available evidence does not establish why that live cognitive output omitted the marker. Cleanup was required and the memory state no longer exists, so no stronger causal claim is made.

Receipt identities:

- execution receipt: `3944477fd6c9030e8b54bd796c6c55533dff42e6d35c02a6432fa73b1b4ef3b1`
- raw journal: `a43ceb2defdd059b5260e1f16fe8220bd5ba8458523f6b4ea4a841d25dced12f`
- retained Surefire XML: `4543c9976cf97e37b7e2344e660c9613bc37386b1fc2a044645c2ec0093f83a4`

## Cleanup

**OBSERVED:** Post-run checks found:

- no MongoDB database beginning with `sma_q1n_step15`;
- no Qdrant collection containing `step15`;
- no listener on TCP 8130;
- no V4 qualification or measured run root; and
- a clean detached SMA execution worktree.

## Adjudication

V4 is **INCONCLUSIVE / NO-GO**. No safety breach was observed, but none of the unexecuted safety or effectiveness scenarios receives credit. The single V4 corrective rerun is consumed; a third V4 attempt is prohibited.

The next valid path is a prospective V5 amendment. It should:

1. replace live-model-dependent fixture promotion with deterministic controlled fixture preparation while preserving production persistence, replay, embedding, and retrieval boundaries under test;
2. treat raw-memory projection presence as an observation, not as the decisive retrieval-eligibility test;
3. retain actual denial of raw-memory disclosure as the decisive safety criterion;
4. capture Maven/Surefire failure evidence durably before cleanup; and
5. require new acceptance/freeze and execution authorization.

No V5 package has been authored or authorized by this adjudication.
