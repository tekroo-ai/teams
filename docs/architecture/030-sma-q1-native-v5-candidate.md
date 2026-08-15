# SMA-Q1 native V5 acceptance and execution authorization

Date: 2026-08-13  
State: **ACCEPTED AND FROZEN — EXECUTION AUTHORIZED, NOT YET STARTED**

## Outcome

The principal accepted and froze V5, then separately authorized one V5 Step 15
execution. The accepted package addresses the two
fixture failures observed in the frozen V4 attempts without changing the SMA-Q1
scientific corpus. V5 still contains exactly 18 scenarios and 96 repetitions,
and it assigns zero credit to every prior attempt.

The candidate consists of:

- `investigations/sma-q1/preregistration-native-v5.json`
- `investigations/sma-q1/step-15-execution-identity-v5.json`
- `investigations/sma-q1/fixture-support/DeterministicReplayPromotion.java`
- `scripts/smaq1_native_step15_v5.py`

## Evidentiary basis

**OBSERVED:** V4's initial attempt allowed normal background replay to project
the controlled raw memory before retrieval qualification. The memory remained
`raw` and `reasoning_eligible=false`; retrieval was not run. V4's corrective
attempt isolated background replay, but its second live-model-dependent fixture
promotion failed the retained-marker assertion. Both attempts stopped before
scenario 1, completed cleanup, and yielded no SMA-Q1 result claim.

**COMPUTED FROM PINNED SOURCE:** `SmaServiceMain` starts the replay scheduler
unconditionally. Its canary filter can prevent the scheduler from selecting the
controlled partitions while leaving capture, reconciliation, retrieval,
context assembly, native hooks, and model delivery active. `ReplayWorker`
writes vector projections before its lifecycle transition is finalized, so
projection presence alone does not prove retrieval eligibility.

**INFERRED:** The smallest defensible repair is to control synthetic-fixture
cognition and scheduler timing while leaving production persistence, replay,
lifecycle, embedding, vector storage, and retrieval under test. This does not
qualify autonomous cognition or endogenous replay quality.

## V5 changes

Eligible synthetic fixtures now pass through the production `ReplayWorker`,
Mongo repositories, `LifecyclePolicy`, Ollama embedding service, and Qdrant
vector store. The only substituted component is a deterministic fixture-only
cognitive candidate containing the already frozen fixture text. Fixture text
is supplied through stdin and is absent from the process command line.

All V5 service starts use a nonmatching canary partition. This prevents the
always-on background scheduler from changing controlled fixture state. It does
not disable any measured capture, reconciliation, retrieval, hook, model,
restart, concurrency, or fault boundary.

For a raw, reasoning-ineligible fixture, Mongo embedding references and Qdrant
points are now observational. The decisive test is actual denial at the direct
retrieval boundary and all applicable native-hook, model-context, response, and
retrieval-event boundaries. Any disclosure remains an absolute `FAIL`.

Fixture build and execution subprocesses retain exit code, output lengths, and
output hashes. On nonzero exit or timeout, exact stdout and stderr are copied
to the attempt output and a receipt containing their paths and hashes is
written before namespace cleanup.

## Preserved scientific design

V5 does not change:

- the decisive question;
- any synthetic memory text or expected marker;
- any scenario prompt, fault, expected outcome, or repetition count;
- latency, reliability, safety, provenance, or disclosure thresholds;
- PASS, FAIL, NOT_RUN, or INCONCLUSIVE meanings; or
- the requirement for a completely fresh 18-scenario, 96-repetition run.

## Static verification

**OBSERVED:** Python compilation passed. The no-service self-test verified the
pinned SMA and inherited-runner hashes, the four V5 workspace partitions, and
the fixture driver's use of production replay, persistence, lifecycle,
embedding, and vector components. The driver compiled successfully against the
pinned SMA classes and resolved dependency classpath. No SMA service or V5
namespace was started, queried, or mutated, and no scenario was run.

Candidate identities:

- V5 manifest SHA-256: `21030ef4a7004217401b0243e5bb7e94be6bc44ecb9869873a72f112fe676422`
- V5 runner SHA-256: `45b9412e879d76d2beef957efdc23d7a9f7585e4e4820774ba5fb387c599872e`
- fixture-driver SHA-256: `580f29edefc8c703e68adc3ba6536b58ba1b577a9ed0ca6969ff172eaacb8117`
- V5 execution-identity SHA-256: `8ff6138b1a8a78418f1b19a63068398c22ea2f889ecd252aa87ff8a9b7387f1e`
- V5 execution-authorization SHA-256: `b4ba47abb322621539317cb5b1ea6d8825bebe867d9a2d10a659dacae25c445f`

## Principal decisions

The principal explicitly instructed: `ACCEPT/FREEZE V5 and proceed to step 15
V5 execution.` The acceptance/freeze and execution authority remain represented
as separate records. The execution authorization binds the final manifest,
identity, and runner hashes and permits one initial V5 attempt only.
