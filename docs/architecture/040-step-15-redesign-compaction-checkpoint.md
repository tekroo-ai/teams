# Step 15 redesign — compaction checkpoint

Date: 2026-08-13  
Purpose: durable handoff before context compaction  
Execution authorization created by this document: **NONE**

## Governing decisions

1. Step 15 V12 is terminal **FAIL / NO-GO**.
2. No V13 package or execution is authorized.
3. The retained executions do not establish twelve SMA product failures. They
   establish repeated fixture, harness, coordinator, OpenHands, model-lane, and
   evidence-design failures in a monolithic gate.
4. Do not repair the current monolithic runner again. The next work is a design
   task: replace Step 15 with independently qualified layers.
5. The read-only Teams event-export implementation is an independently passing
   **candidate**. It is not accepted, committed, pushed, deployed, or live-shadow
   qualified.
6. Accepted `tekroo.kernel.contracts/0.7.0` remains immutable.

## V12 evidence floor

**OBSERVED:** V12 passed its offline artifact contract and fixture qualification,
then completed 6 scenarios and 41 of 96 repetitions.

**OBSERVED:** In `SMAQ1N-007-RETRIEVAL-OUTAGE` repetition 1, the exact prompt
requested `RETRIEVAL_OUTAGE_OK`; the model returned
`RETRIEVAL_OUTDATED_OK`.

**OBSERVED:** The same repetition recorded zero native/model context, an
unchanged prompt, and a successful hook. Cleanup succeeded.

**INFERRED:** The frozen end-to-end assertion genuinely failed, but this receipt
does not show an SMA retrieval-outage failure. It isolates model exact-response
noncompliance after SMA failed open correctly.

Canonical receipts:

- `OUTPUT/phase-3/sma-q1n-step15-v12/execution-receipt.json`
  - SHA-256: `afb0d9d6033120cc9dbf7a93b9b789f2e3b1be54ca74b7d881daa62ba3fbe2bb`
- `OUTPUT/phase-3/sma-q1n-step15-v12/raw-receipts.jsonl`
  - SHA-256: `8438e9ff85f7d97c8c3f760fc707cba1927fbb62d2a3059991799ad4b1771207`
- `docs/architecture/038-step-15-v12-terminal-adjudication.md`

## Root cause of the iteration cycle

The gate combined fixture construction, MongoDB, Qdrant, SMA capture and
retrieval, OpenHands lifecycle, native-hook timing, model response behavior,
cleanup, and receipt generation under one stop-on-first-error experiment.
Components were discovered serially through full executions instead of being
qualified independently first. Exact arbitrary model markers were also used as
proxies for SMA behavior. This made unrelated failures invalidate the entire
96-repetition corpus and caused amendment cycling.

## Required replacement architecture

Design before executing:

1. **Deterministic SMA semantics** — capture, eligibility, retrieval,
   partition isolation, provenance, duplicate handling, fail-open behavior,
   restart continuity, and bounded recovery. No generative-model assertions.
2. **OpenHands boundary integration** — deterministic stub model; prove hook
   ordering, byte-preserved current prompt, context placement, timeouts,
   cancellation, and lifecycle cleanup.
3. **Model behavior** — separate qualification using semantic predicates.
   Exact text is required only where exact text is itself the product
   requirement. A model-format failure must not erase observed SMA correctness.
4. **End-to-end soak** — runs only after layers 1–3 independently pass. Its
   result reports component attribution rather than one generic gate failure.

Before any new execution, the replacement specification must define:

- ownership and acceptance criteria for every layer;
- deterministic fixtures and offline harness self-tests;
- pre-assertion evidence retention;
- substantive, environment, harness, and model failure classifications;
- bounded retries, if any, declared prospectively;
- the minimum evidence needed to credit a completed layer independently; and
- an explicit rule preventing post-observation regrading.

## Event-export candidate

Candidate contract:
`CONTRACTS/tekroo.event-export.contracts/0.1.0/manifest.json`  
Manifest SHA-256:
`4aa87278b6812f967331bef98b2c7e63585b6742b39d0b64c0ff693632aa9731`

Implementation receipt:
`OUTPUT/phase-3/event-export-boundary-receipt.json`  
Receipt SHA-256:
`011113ce369eda8001df0b9b1b324a76d5f487f89bc5bd0fa99e07a7eb87d147`

**OBSERVED:** contract validation, full Go tests, Go vet, targeted race tests,
and Mongo replica-set integration passed. The tests cover exact event bytes,
live-before-backlog convergence, resume without omission, explicit bounds,
unchanged raw outbox BSON, zero export checkpoints, and a connector that creates
no collections.

Disposition: **GO for principal review of the candidate; NOT_RUN for SMA client
integration and cross-repository live shadow.**

## Repository identities and mutation state

### Teams

- Path: `/Users/paul/work/tekroo-ai/teams`
- Branch: `main`
- HEAD: `e5642704d98702e4ff0b325b7c32a058757c44d9`
- State: substantial uncommitted/untracked Step 15 evidence and event-export
  candidate; `adapters/protocol/import_boundary_test.go` is modified.
- Push/commit authorization: **NONE after this checkpoint**.

### SMA

- Path: `/Users/paul/work/tekroo-ai/sma`
- Branch: `master`, one commit ahead of `origin/master`
- HEAD: `e89ff9e0bab192689122d5e7b9d36d16cae5d368`
- Purpose of local commit: MCP EOF/lifecycle repair.
- Push authorization: **NONE**.

### SMA Step 15 execution worktree

- Path: `/Users/paul/work/tekroo-ai/sma-step15`
- Detached HEAD, clean at the time of checkpoint.
- Pinned execution commit used by V12:
  `60a966234166ea75f767b25e3cbb7eaabe4064a2`.

### OpenHands SDK

- Path:
  `/Users/paul/.local/src/openhands-software-agent-sdk-1.40.1-parallel`
- Branch: `local/parallel-litellm-streams-v1.40.1`
- HEAD: `3e05292b0ba5bccedd6e94f73f48b9f721a7d1dc`
- The commit adds exact default-tool control, vision-tool control, and seed
  forwarding.
- Thirteen pre-existing/unrelated dirty paths remain and must not be cleaned or
  overwritten.
- Push authorization: **NONE**.

### SMA Teams-v4 alignment

- Path: `/Users/paul/work/tekroo-ai/sma-teams-v4-alignment`
- Branch: `teams-v4-alignment`, one commit ahead of origin
- HEAD: `39745bdc34cf7d77f39650f06a76d8cf7143f1b8`
- This commit records the original live-shadow preflight blocker.
- Push authorization: **NONE**.

## First action after compaction

Read this checkpoint and the V12 terminal adjudication. Then draft the
replacement layered Step 15 specification for principal discussion. Do not
execute tests, author V13, accept the event-export contract, commit, push,
deploy, or direct SMA work until separately authorized.
