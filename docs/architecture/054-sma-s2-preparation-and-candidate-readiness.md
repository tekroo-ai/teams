# SMA-S2 preparation and candidate readiness

Date: 2026-08-14  
Status: `PREPARATION_PASS_CANDIDATE_NOT_ACCEPTED_NOT_AUTHORIZED`

## Outcome

SMA-S2 preparation is complete within the granted authority. The versioned
candidate binds the accepted S1 claim, exact SMA and OpenHands implementation
identities, supported hook assets, event schemas, non-secret configuration,
deterministic model stub, offline harness, disposable namespaces, cases,
thresholds, evidence requirements, and execution fences.

No measured S2 case was run. No OpenHands conversation was created, no model
was invoked, and no SMA measured namespace was created.

The candidate preregistration is
`investigations/sma-q1/layered/sma-s2-preregistration-candidate-1.json`, SHA-256
`fcdfa6a6fc85ea5c8aebe4340e10642007f4cd1df85cea1a0e47f7ef278e62fe`.

## Verified boundary

**OBSERVED:** Agent Canvas 1.12.0 is configured to use the clean OpenHands
1.40.1 custom runtime at commit
`a338ba9b6cbb529886b755a335bae3dee0004700`, tree
`9e72f1b4ee0b857025d9f170600b9dce539b169a`. This is the live source identity;
the nearby `openhands-sma-p1` worktree is not the active runtime and is not used
as a substitute.

**OBSERVED:** The supported message path stores the current user instruction in
`MessageEvent.llm_message`, stores hook context in `extended_content`, and
appends the latter only when producing the LLM message. With list serialization
the model request retains the prompt and context as distinct ordered content
segments.

**OBSERVED:** The bound hook runs at `user_prompt_submit`, uses a 750 ms internal
deadline under a one-second OpenHands timeout, and returns a valid allow/continue
result on exceptions. The bridge and hook enforce the stricter product bounds
of three results and 4,096 context characters.

**OBSERVED:** Eight hook tests and three targeted OpenHands tests passed. Their
JUnit receipts are retained under `OUTPUT/phase-3/` and are bound by the
readiness receipt.

## Prospective corrections

Three historical draft defects were corrected before execution:

1. The old secret case required both byte-identical prompt delivery and absence
   of that prompt's marker from the entire model request. Those requirements are
   mutually exclusive. Candidate 1 permits the synthetic marker only in current
   prompt segment 0 and sealed raw evidence. It remains prohibited from SMA
   context segment 1, operational logs, retrieval events, and projections.
2. The old oversized-context case treated five memories and 10,000 characters
   as the operative product limits. They are scientific ceilings. The actual
   bound product is measured against its stricter three-result and 4,096-character
   limits.
3. The old draft did not freeze the exact model-request prompt field. Candidate
   1 requires list serialization, current prompt at segment 0, extended context
   at segment 1, and classifies string flattening as failure.

These are preregistration corrections, not post-observation regrading.

## Offline harness result

**OBSERVED:** Stub candidate 1 records exact synthetic request bodies to sealed
raw evidence before responding, while its operational journal contains only
identity, length, digest, mode, and timing fields. It supports deterministic
success, streaming success, timeout/client disconnect, malformed response,
HTTP 503, and transport failure.

**OBSERVED:** Offline harness candidates 1 and 2 failed because their own
receipt-decoding assertions searched encoded or JSON-escaped envelopes rather
than reconstructed content. Both failed receipts and exact harness versions are
retained. Candidate 3 corrected those oracles and passed all eight tests.
Candidate 4 prospectively removed two unused imports, passed Ruff, and repeated
the same eight-test PASS, including four-channel concurrency, fault activation,
synthetic-secret surface separation, append-only journal reconstruction, and
cleanup.

The passing offline receipt is
`OUTPUT/phase-3/sma-s2-candidate-4-offline-harness/offline-harness-receipt.json`,
SHA-256
`9ad170a2210ce262e5cff1d637d6a84d2c49b1a74d574f1912f9bb3250086b4d`.

**COMPUTED:** The preregistration contains 18 cases and 96 planned repetitions.
The targeted source suites contain 11 passes and zero failures; the passing
offline harness contains eight passes and zero failures.

## Gate decision

**INFERRED:** Preparation is a PASS. Candidate 1 is ready for independent review
and principal acceptance, but it is not accepted and does not authorize a
measured execution.

The shortest compliant next sequence is:

1. the principal designates an independent harness reviewer (the principal may
   serve if acting independently of the boundary implementation and harness, or
   may name a delegate);
2. that reviewer checks the bound package and issues a review receipt;
3. the principal accepts/freezes the reviewed successor preregistration;
4. immediately before execution, the coordinator seals a successor execution
   identity after clean identity, health, resource, serializer-shape, and
   namespace-absence checks; and
5. the principal separately authorizes one single-use measured S2 execution.

Until all five occur, S2 remains `NOT_RUN` and the claim
`OPENHANDS_SMA_BOUNDARY_QUALIFIED` has not been earned.

## Bound artifacts

- `investigations/sma-q1/layered/sma-s2-boundary-identity-candidate-1.json`
- `investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-1.json`
- `investigations/sma-q1/layered/sma-s2-openhands-openapi-candidate-1.json`
- `investigations/sma-q1/layered/sma-s2-openhands-event-schemas-candidate-1.json`
- `scripts/sma_s2_deterministic_model_stub_candidate_1.py`
- `scripts/sma_s2_offline_harness_candidate_4.py`
- `OUTPUT/phase-3/sma-s2-preparation-readiness-candidate-1.json`

The machine-readable readiness receipt is authoritative for exact paths,
digests, counts, retained failed candidates, and authority fences.
