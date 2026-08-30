# Step 15 SMA-S2 definitive first-pass closure plan

Date: 2026-08-28
Status: `AUTHORIZED_T0_THROUGH_H0_NONLIVE_PREPARATION`
Scope: close the accepted final-P2 `SMA-S2` gate without another published harness iteration

Principal authorization: 2026-08-28. The authorization covers T0 through H0
exactly as bounded below. It does not authorize L0, M0, A0, service start,
OpenHands conversations, live activity, model calls, commit, or push.

## Decision

Do not patch or rerun successor v2. Preserve candidate 10, successor v1, and
successor v2 as historical evidence. Build one mutable, unpublished
`SMA-S2-FINAL-P2-CLOSURE` work product, but do not allow it to become a
candidate until product-surface truth, runner implementation, offline
qualification, and independent readiness review have all passed.

The governing principle is:

> The real OpenHands and SMA implementations generate the offline truth. The
> harness may parse, join, delay, omit, or mutate that truth, but may not invent
> a product schema.

This plan preserves the accepted S2 science unchanged: 20 cases, 103 measured
repetitions, 59 scientific predicates, 10 evidence classes, exact 34-operation
zero-credit selector, deterministic-stub schedules, deadlines, case order, and
zero hidden retries.

## Evidence floor

- **OBSERVED:** accepted S1 final-P2 scientific adjudication establishes
  `SMA_SEMANTICS_QUALIFIED` for SMA commit
  `fe9903cdf59497aabf12cbe7722e1397a1372068`, tree
  `fc2ae06c6333e94f6db34d691e50cb8bb1e5aa36`.
- **OBSERVED:** successor v2's offline runner completed its internally modeled
  34/34 and 103/103 walks and rejected 59/59 plus 10/10 mutations.
- **OBSERVED:** independent review rejected successor v2 because its offline
  MongoDB, Qdrant, and event-intake schemas contradicted the bound final-P2
  product.
- **INFERRED:** the repeated cycle was caused by treating an independently
  reviewed emulator as the last validation step. Product-schema conformance
  must instead be the first executable contract and must mechanically govern
  every later fake receipt.

## Exact product joins that replace the invented v2 model

The closure package must use these source-derived relationships.

### OpenHands events and eligibility

OpenHands event JSON must be produced and validated by the bound OpenHands
Pydantic event classes. User input carries
`conversation_input/task_input/not_applicable`; delegated input may carry
`delegated_agent/task_input/not_applicable`; final agent output carries
`agent_model/agent_response/final`.

The expected eligible-event set for a conversation is derived from the raw
OpenHands events using the exact bound `OpenHandsEventIntakeService`
discriminator. It is not a hand-written expected memory count. This means, in
particular, that case 14 permits captures for eligible user and final-agent
messages while proving exact absence for hook, reasoning, action,
observation, tool, utility, and condensation event identities.

All harness requests to supported OpenHands `/api/*` routes use the product's
`X-Session-API-Key` header. The key value comes only from the exact sealed key
file bound by the later execution identity; browser-cookie fallback is not a
harness authentication mechanism.

### MongoDB capture identity

The stable capture key is:

`(_id, origin.openhands_provenance.conversation_id, origin.openhands_provenance.event_id)`

The live query selector uses the two nested provenance paths. It must retain
`_id`; a projection that removes `_id` is prohibited. Other provenance fields
come from `origin.openhands_provenance`, canonical text comes from
`canonical.text`, and the persisted semantic projection reference comes from
`embeddings.semantic_ref`. The frozen final-P2 `ReplayWorker` intentionally
ignores the episodic upsert return and does not persist
`embeddings.episodic_ref`; an episodic Mongo reference is therefore a
prohibited assumption. Event order is retained by the intake-owned
`event.sequence_position`; `origin.openhands_provenance.sequence` is an
optional upstream field and must not be substituted for the intake position
when the OpenHands event envelope does not supply it.

### Retrieval trace

The hook's retained stdout carries `smaTraceId`. The matching SMA retrieval
record is selected by `task_ref == smaTraceId`. Selected memories are the
`results[].memory_id` values. The retrieval record's `agent_id`, mode, rank,
and scores are retained directly. The runner must not query retrieval events
by a nonexistent conversation field.

### Qdrant projection evidence

Qdrant is queried by `memory_id` and checked against `agent_id`. It is evidence
of projection existence and partition ownership, not the source of OpenHands
conversation provenance or retrieval trace. The semantic point identity is
compared with `memories.embeddings.semantic_ref`. The episodic point is checked
in the configured episodic collection by the deterministic point identity
derived from `memories._id`; no Mongo episodic reference is required. The
harness must never require nonexistent Qdrant `conversation_id` or `trace_id`
payload fields.

### End-to-end evidence join

For a delivered memory, the retained chain is:

`HookExecutionEvent.stdout.smaTraceId`

→ `retrieval_events.task_ref`

→ `retrieval_events.results[].memory_id`

→ `memories._id`

→ `memories.origin.openhands_provenance.*`

→ Qdrant payload `memory_id + agent_id` and Mongo embedding reference.

Every link must be present and equal. Missing evidence fails closed.

## One closure pipeline

### Gate T0 — immutable product-truth pack

Before runner work begins, create one content-addressed product-truth pack that
binds every implementation defining a used boundary—not only the bridge and
hook. At minimum it binds:

- OpenHands conversation and event routers, conversation and event services,
  request and response models, `HookExecutionEvent`, `MessageEvent`, local
  conversation event creation, and response dispatch;
- SMA `OpenHandsBridgeServer`, `OpenHandsEventIntakeService`,
  `MemoryDocumentMapper`, `RetrievalEventDocumentMapper`,
  `QdrantVectorStore`, `SmaRetrievalService`, collection configuration,
  runtime JAR, and service wrapper;
- the deterministic model stub, bridge-fault fixture, hook source, launch
  definitions, accepted S1 receipt, accepted S2 preregistration, corpus, and
  oracle matrix.

The truth pack includes executable contract extractors:

1. bound OpenHands Python probes that construct and serialize actual user,
   delegated, final-agent, hook, error, state-update, action, tool,
   observation, utility, and condensation events through the product classes,
   including the actual hook manager, executor, and event processor path;
2. a bound final-P2 Java probe that runs the actual intake discriminator and
   actual MongoDB mappers over those event fixtures and emits canonical BSON
   Extended JSON;
3. a Qdrant protocol probe that drives the actual `QdrantVectorStore` against
   an in-process recording transport and retains the exact point ID and payload;
4. a bridge/retrieval probe that proves `smaTraceId → task_ref → results[].memory_id`.

No manually authored Mongo document, Qdrant payload, or OpenHands event may be
an authoritative positive fixture.

T0 exits only when an independent source review confirms every bound source,
extractor output, selector, projection, and join. This review occurs before
the S2 runner is implemented.

### Gate T1 — single implementation over typed raw receipts

Implement one runner and one oracle registry. Offline and live adapters return
the same typed raw receipts. The runner owns orchestration, stabilization,
evidence reconstruction, grading, recovery, and cleanup.

Offline adapters may replay product-truth-pack receipts and inject timing,
absence, corruption, ordering, backlog, faults, and cancellation below the
runner. They may not synthesize derived predicates, final observations,
expected counts, provenance, or cleanup success.

Each of the 20 operations declares entry/exit service mode, owned resources,
actual raw event identities, expected eligible and forbidden event sets,
deadlines, stabilization criteria, recovery, and cleanup. Expected captures
are calculated from the actual serialized event population plus the bound
intake discriminator—not copied from predecessor counts.

### Gate T2 — prepublication conformance suite

Before any canonical H0 receipt or candidate identity exists, the mutable work
product must pass all of these controls:

1. every live Mongo selector finds the exact product-mapper document and keeps
   `_id`;
2. every retrieval join uses `task_ref` and exact result memory IDs;
3. every Qdrant query succeeds against the actual recorded product payload and
   asks for no absent fields;
4. every OpenHands event fixture validates through the bound product class;
5. the eligible-event oracle agrees with the actual intake service for all
   eligible and ineligible event kinds, including final agent responses;
6. every hook/context/model-boundary parser consumes exact product output;
7. every live HTTP request matches a bound supported request model and route;
8. every process, store, workspace, authority, and cleanup action is reachable
   through code already present in the package;
9. the exact 34 selector and 103 plan use the same handlers and recovery code;
10. all product-source hashes and generated truth-fixture hashes fail closed
    when missing or changed.

Raw negative controls must include nested Mongo-path mutation, `_id` omission,
retrieval `task_ref` mismatch, result-memory mismatch, Qdrant agent mismatch,
Qdrant phantom provenance fields, incorrect user metadata, missing eligible
final-agent capture, accidental ineligible-event capture, and source-binding
drift.

### Gate T3 — independent readiness review before publication

An independent reviewer receives the complete mutable tree hash, product-truth
pack, traceability matrix, runner, adapters, all 20 workflows, and T2 receipts.
The reviewer repeats source-to-fixture and fixture-to-runner tracing and
specifically attempts to identify a field, route, process, or evidence join
that exists only in the emulator.

Any T3 finding is an ordinary development defect in an unpublished work
product. It does not create a candidate, consume an H0 attempt, or return to
the principal. T3 exits only with `PASS_READY_TO_PUBLISH`; there is no
provisional PASS.

### Gate H0 — one canonical offline qualification and seal

Only after T0–T3 pass may the package run one canonical H0 publication. H0
must retain:

- exact 34/34 and 103/103 integrated walks;
- 20/20 handlers and every lifecycle/recovery branch;
- 59/59 scientific positive and rejecting raw mutations;
- 10/10 evidence positive and missing/mutated rejection controls;
- product-schema conformance and live-reachability controls from T2;
- unrelated backlog and delayed visibility;
- timeout, malformed, 503, bridge-down, unused-port, cancellation,
  disconnect, start/stop, journal, and cleanup faults;
- continuation after safe scientific failure and immediate stop after recovery
  failure;
- normal/optimized equivalence;
- complete retained raw receipts and independently reconstructed grades; and
- explicit zero live/service/model/production activity.

If H0 passes, derive exactly one package identity from the already-reviewed
tree plus canonical receipts. A post-seal check is mechanical: verify that the
manifest binds the T3-reviewed tree and H0 artifacts byte for byte. It is not a
new exploratory review.

### Gate L0 — accept/freeze and one live zero-credit dress

After principal acceptance/freeze, create the execution identity and perform
one separately authorized 34-operation live dress. Preflight stops before
operation 1 on identity drift, unrelated active work, occupied resources,
existing namespaces, unsupported schema, wrong project/workspace, unhealthy
service, or inability to preserve unrelated conversations.

The dress uses the same runner, selectors, joins, oracles, fault fixtures,
recovery, and cleanup as H0. No post-freeze helper or schema adaptation is
permitted.

- Complete scientific counterexample: close S2 as `FAIL_NO_GO_PRODUCT_BOUNDARY`.
- Harness defect: close this approach as `TERMINAL_NO_GO_HARNESS_ARCHITECTURE`.
- PASS: 34/34, all recovery receipts, final cleanup, zero measured credit, and
  zero real-model calls.

All three are closure outcomes. Only PASS advances to measured execution.

### Gate M0 — one measured S2 execution

Under a separate single-use authorization, run exactly 20 cases and 103
repetitions, with zero automatic reruns. Retain the full raw evidence and
cleanup receipts. PASS requires every preregistered threshold and permits the
claim `OPENHANDS_SMA_BOUNDARY_QUALIFIED`.

### Gate A0 — scientific adjudication

Independently reconstruct the measured result from raw receipts, bind it to the
frozen package and accepted S1 identity, and present one principal decision:
accept/freeze PASS or accept the terminal non-PASS disposition. This closes
S2. It does not by itself close all of Step 15; accepted M1 and E1 layers remain
required for `STEP_15_CLOSED`.

## First-pass invariants

The closure work is not permitted to publish merely because its own tests are
green. Publication requires all of the following beforehand:

- product-generated positive fixtures only;
- every used product implementation source bound;
- every live selector proven against actual mapper output;
- every live route proven against the actual request/event model;
- every evidence join explicitly traced end to end;
- no expected capture count detached from actual eligible event identities;
- no emulator-only field, route, store, process action, or predicate;
- no final review deferred until after package publication;
- one package identity, one live dress, and one measured execution; and
- no automatic or hidden rerun.

## Authorization sequence

One authorization can cover T0 through H0 as bounded, non-live preparation.
It permits source-bound contract extractors, the mutable closure work product,
offline qualification, and independent readiness review. It does not permit
service start, OpenHands conversations, live preflight, live dress, measured
execution, real-model calls, M1, E1, production/historical access, commit, or
push.

After H0 PASS, the remaining principal decisions are:

1. accept/freeze the one package and execution identity;
2. authorize the one live zero-credit dress;
3. authorize the one measured execution only after dress PASS; and
4. accept the final S2 adjudication.

No candidate or execution authorization should be requested before T0–T3 and
H0 are complete.
