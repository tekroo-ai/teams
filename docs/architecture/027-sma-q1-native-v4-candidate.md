# SMA-Q1 native v4 acceptance and freeze

Date: 2026-08-13  
State: **ACCEPTED AND FROZEN — NOT AUTHORIZED, NOT EXECUTED**

## Outcome

The principal explicitly accepted and froze the v4 package on 2026-08-13. This decision does not authorize Step 15 execution.

The candidate consists of:

- `investigations/sma-q1/preregistration-native-v4.json`
- `investigations/sma-q1/step-15-execution-identity-v4.json`
- `scripts/smaq1_native_step15_v4.py`

## Evidentiary basis

**OBSERVED:** Both v3 attempts ended before any scenario repetition began. The corrective rerun nevertheless retained four distinct Mongo memories, three semantic points, three episodic points, and point-level receipts for all eligible memories. The raw ineligible memory was absent from both Qdrant collections. The v3 adjudication and every v3 artifact remain unchanged.

**COMPUTED FROM PINNED SOURCE AND RAW RECEIPTS:** The corrective runner used two expectations that disagree with the pinned SMA source:

1. It expected a full 64-character workspace hash, while `OpenHandsEventIntakeService` uses the first 24 characters.
2. It expected both Mongo embedding references, while `ReplayWorker` performs an episodic Qdrant upsert but persists only `semanticRef` through `updateCanonical`.

**INFERRED:** A prospective amendment limited to those two harness corrections is preferable to changing SMA or weakening the scientific criteria. This inference authorizes no result claim.

## Preserved scientific design

V4 retains the v2 scenario and synthetic-memory corpora and every accepted v3 amendment other than the two superseded harness expectations. It preserves exactly 18 scenarios and 96 repetitions, with zero credit from v2 or v3. Thresholds, prompts, faults, evidence rules, stop conditions, and PASS/FAIL/INCONCLUSIVE meanings are unchanged.

## V4 corrections

The runner now computes agent identity as:

`openhands:<first 24 lowercase hex characters of SHA-256(canonical workspace path)>:default`

For an eligible memory it requires:

- exactly one semantic and one episodic Qdrant point;
- Java `UUID.nameUUIDFromBytes(actualMemoryId)` point identity;
- matching actual memory and expected agent identities;
- a non-empty vector in both collections;
- Mongo `semantic_ref` equal to the deterministic point ID; and
- no Mongo `episodic_ref` under the pinned implementation.

The ineligible memory must have neither Qdrant point nor Mongo reference.

## Pre-live expectation self-test

Before any service query or live fixture mutation, the runner must:

- verify the three pinned SMA source hashes and the retained v3 journal hash;
- verify the relevant source expressions still encode the preregistered rules;
- recompute all four v4 workspace partitions;
- re-adjudicate the retained v3 qualification observation under the corrected rules; and
- prove that the retained observation is rejected by both superseded expectations.

The candidate self-test was run without live-service access. It passed with zero corrected-expectation problems, rejected the superseded full-hash rule for all four memories, and rejected the superseded Mongo episodic-reference rule for all three eligible memories. Python compilation also passed. No fixture namespace or scenario was created or run.

## Required decisions

The frozen identities are:

- v4 manifest SHA-256: `2178116c866e9abed9b5e83edd3c9c221f55f81e6f343a168205209f25834d23`
- v4 runner SHA-256: `5175b053f3633744212923290b0ac2614412a516e9279649b166d1ab2957b3e2`
- v4 execution-identity SHA-256: `8c7509e3ef10667594ce117f044e6a242c4e572cbf65636ba39973fc0059b2db`

The next decision is whether to create a separate authorization artifact for one v4 Step 15 execution. That artifact must bind all three frozen identities. Acceptance alone is not execution authority.

Current recommendation: retain the execution fence until the principal separately authorizes one v4 Step 15 execution.
