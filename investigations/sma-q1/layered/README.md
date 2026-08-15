# Layered Step 15 preregistration drafts

Status: **S1 SCIENTIFIC PASS ACCEPTED/FROZEN; S2, M1, AND E1 NOT RUN**

These four lineages implement the accepted design in
`docs/architecture/041-step-15-layered-qualification-specification.md`. They
replace the future execution design of the terminated monolithic lineage; they
do not revise or regrade V2 through V12.

The S1 scientific preregistration and evidence-v2 scientific PASS are accepted
and frozen. S2, M1, and E1 remain drafts. The S1 single-use measured authority
is consumed and cannot be reused. The accepted S1 claim is narrowly
`SMA_SEMANTICS_QUALIFIED`; it does not qualify the OpenHands boundary, a model
profile, the integrated runtime, or production readiness.

Historical candidate-4 readiness is recorded in
`docs/architecture/044-sma-s1-build-and-harness-readiness.md`. Current P2-M
candidate-7 readiness and exact digests are recorded in
`docs/architecture/046-sma-s1-p2m-preparation-readiness.md`. A fresh 477-test
P2-M build passes, all fourteen reviewed handlers and 32 frozen predicates pass
one non-creditable implementation check, lifecycle controls pass, and all ten
offline harness mechanics tests pass.

## Draft inventory

| Layer | File | Draft SHA-256 |
|---|---|---|
| SMA semantics | `sma-s1-preregistration.json` | `e663f5b732c22248adc5fab2f536dbf02dd72337f7c8428f225937e72d6a7f8c` |
| OpenHands/SMA boundary | `sma-s2-preregistration-draft.json` | `7885f1e5e590d8b0b2bc500bbeab27026a7711dd302cd5e3417d2753a3cd759c` |
| model profile | `sma-m1-profile-preregistration-draft.json` | `1521cbd6704ef5b11d635448b12eea6f48332d679225f464bcfd7698341a1b6c` |
| integrated runtime | `sma-e1-runtime-preregistration-draft.json` | `3647a513d5369b61b17de240f2b7e62db041c6e84196a7014817c2d69fbb39c2` |

The S1 hash identifies an accepted frozen preregistration. The other three
hashes identify drafts only. A revision changes its hash and must be reviewed
as a new artifact; accepted S1 must not be edited in place.

## Review and execution sequence

1. Review and complete the separate S1 execution-identity manifest that binds
   the exact SMA,
   persistence, deterministic fixture, harness, configuration, reviewer, and
   disposable namespace identities.
2. Independently qualify the exact offline harness and bind its receipt to the
   execution identity. Neither identity binding nor harness qualification
   authorizes measured execution.
3. Only after both artifacts are reviewed may the principal issue one
   single-use S1 execution authorization.
4. Repeat the same preregistration, identity, offline-harness, and execution
   sequence for S2, binding the accepted current S1 receipt.
5. Select one concrete model profile for M1 after its scientific design is
   accepted. Its separate execution identity must bind that profile and an
   accepted current S2 request-layout receipt before M1 execution.
6. E1 remains `NOT_RUN` until S1, S2, and that exact M1 profile each have an
   accepted current PASS receipt. Bind the complete runtime tuple and qualify
   the E1 harness before requesting its single-use execution authority.

S1 and M1 implementation preparation may occur independently once their
respective designs are accepted, but M1 fixture freezing depends on the accepted
S2 request layout. E1 depends on all three accepted upstream PASS receipts.

## Accepted S1 result and remaining gates

The accepted S1 adjudication is
`OUTPUT/phase-3/sma-s1-measured-p2m-evidence-v2-sealed-v1-attempt-1/scientific-adjudication.json`,
SHA-256
`0e5ace0acbe6e2441755ae656de36096e38b2364d27fb45ee844f2422abf2f7d`.
Its acceptance record is
`sma-s1-evidence-v2-scientific-adjudication-acceptance.json`.

The next sequential layer is S2. Before any S2 execution, the remaining gates
are:

- revise and review the existing S2 draft so it binds the accepted current S1
  adjudication and exact OpenHands, hook/plugin, bridge, schema, deterministic
  stub, configuration, reviewer, and disposable-namespace identities;
- principal acceptance/freeze of that completed S2 preregistration;
- implementation and independent offline qualification of the exact S2
  deterministic harness;
- principal acceptance/freeze of the exact S2 execution identity and offline
  harness receipt;
- immediate read-only S2 preflight and a separate single-use S2 measured
  execution authorization;
- select and separately qualify a concrete M1 model profile before E1; and
- keep E1 `NOT_RUN` until accepted current PASS receipts exist for S1, S2, and
  the selected M1 profile.

## Separate lineage

Teams event export and SMA live shadow are excluded. Their candidate contract,
client, cursor, deduplication, restart, and live qualification remain a separate
lineage and cannot be inserted into these drafts without a prospectively
accepted amendment.
