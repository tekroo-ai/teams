# Phase 3 Step 15 post-P2 baseline reconciliation

Date: 2026-08-28
Status: **ACCEPTED — R1 PREPARATION/OFFLINE QUALIFICATION AUTHORIZED**
Step 15 measured-execution authority created by this record: **NONE**
SMA modification, service-start, deployment, and live authority: **NONE**

## Principal acceptance

**OBSERVED:** after reviewing the reconciliation, the principal stated:

> Accepted. You may proceed.

This accepts the reconciliation and authorizes R1 final-P2 S1 successor
preparation and offline qualification only. It does not authorize the measured
14/71 S1 execution or any later layer.

## Decision floor

Step 15 is the accepted four-layer qualification sequence for the Teams v4
programming-memory integration:

1. `SMA-S1` — deterministic SMA semantics;
2. `SMA-S2` — deterministic OpenHands/SMA boundary;
3. `SMA-M1-<profile>` — one exact model profile; and
4. `SMA-E1-<runtime-tuple>` — the integrated runtime soak.

The accepted order remains S1 → S2 → M1 → E1. Event export and SMA live shadow
remain a separate lineage. This reconciliation does not execute any layer and
does not revise the accepted scientific meaning of any layer.

## Bound canonical identities

**OBSERVED:** canonical Teams `main` and `origin/main` both identify commit
`a3a918d83089fd18799e4cd243d7f3fa33c128b8`, tree
`715d0e89b5321e42139bd9d5067b51dd9c9fc04b`.

**OBSERVED:** published SMA `master` and `origin/master` both identify final P2
commit `fe9903cdf59497aabf12cbe7722e1397a1372068`, tree
`fc2ae06c6333e94f6db34d691e50cb8bb1e5aa36`.

**OBSERVED:** the SMA checkout also contains unrelated tracked and untracked
local work. This review excludes those mutable files and compares immutable Git
objects only. Nothing in that dirty checkout is accepted, qualified, or bound
by this record.

**OBSERVED:** `docs/SMA_S1_P2_AG_CLOSURE_DECISION.json` closes P2, retains P2-AE,
retains P2-AF disabled by default, and classifies P2-AG as a non-requirement.
No P2 implementation package remains.

## Accepted S1 result and identity drift

The accepted S1 evidence-v2 PASS remains immutable. It is bound to:

- SMA commit `5d58be508c74ae8577d2fd31e3346be23a912ab3`;
- SMA tree `7222410443a6041496a5c313b2549f16eb937458`;
- accepted S1 preregistration SHA-256
  `e663f5b732c22248adc5fab2f536dbf02dd72337f7c8428f225937e72d6a7f8c`;
- accepted scientific adjudication SHA-256
  `0e5ace0acbe6e2441755ae656de36096e38b2364d27fb45ee844f2422abf2f7d`.

**COMPUTED:** final P2 is 27 commits beyond that accepted SMA identity. The
immutable commit-to-commit diff contains 191 changed files, 45,613 insertions,
and 66 deletions.

**OBSERVED:** the diff changes boundaries exercised by S1:

| Boundary | Baseline blob | Final-P2 blob | Material change |
|---|---|---|---|
| OpenHands bridge | `5b885689…` | `f8543f01…` | retrieval preparation and a separate pre-disclosure fence check |
| semantic retrieval | `f177edba…` | `5f706a9e…` | deletion-fence checks at retrieval and disclosure |
| replay | `7ce6021e…` | `b8bf08e0…` | replay-entry denial and fenced-context exclusion |
| delivery | `dcffd032…` | `da377208…` | prepare, delivery, and record-time fence checks |
| Mongo collection configuration | `790e4d8f…` | `a316132e…` | protected-raw-lineage collection added with compatibility constructors |

**OBSERVED:** the changed retrieval, replay, and delivery classes retain
backward-compatible constructors using `ProtectedRawDeletionFence.permitAll()`.
The P2-AF receipt says the enabled deletion composition is absent from the
default SMA entrypoint and is disabled by default.

**INFERRED:** those compatibility paths make a no-regression result plausible,
but they do not prove it. The Step 15 specification expressly says that no
result transfers automatically to another SMA commit or material
configuration.

## Adjudication

1. The accepted S1 PASS is **not revoked** and is not regraded. It remains valid
   for its original bound implementation.
2. The accepted S1 PASS is **stale/inapplicable** to final P2 commit `fe9903c`.
3. A successor S1 qualification is required before S2 may claim a current
   final-P2 upstream identity.
4. This is not authority to rerun the old package. The old single-use S1
   authority remains consumed.

## Exact closure route

### R1 — prepare one final-P2 S1 successor

Reuse the accepted S1 scientific preregistration unchanged: 14 cases and 71
repetitions. Do not change its predicates, corpus, thresholds, or adjudication
meanings.

Create a new execution package and identity that bind:

- immutable SMA commit `fe9903c` and tree `fc2ae06…` from a clean checkout;
- newly built service, source, driver, harness, dependency, configuration, and
  environment digests;
- new disposable MongoDB and Qdrant namespaces;
- the exact selected configuration with P2-AF disabled; and
- the original accepted S1 preregistration and historical adjudication as
  lineage, not imported measured credit.

The offline qualification must rerun the accepted harness-mechanics controls
and add exact P2-delta controls proving:

1. the default entrypoint does not activate the deletion runtime;
2. the selected compatibility composition uses the permit-all path and does
   not suppress ordinary S1 retrieval, replay, or delivery;
3. the final bridge's preparation plus disclosure recheck preserves the
   permitted result and its retained audit/provenance evidence; and
4. enabled deletion composition, a pending deletion fence, or any different
   fence configuration fails the identity check and cannot inherit this S1
   result.

R1 is build and offline work only. It starts no OpenHands process, uses no
generative model, touches no production or historical data, and earns no
scientific credit.

### R2 — independent review and freeze

An independent reviewer verifies the complete successor package, final-P2
source bindings, 14/71 plan, P2-delta controls, create-once evidence roots,
fault controls, append-before-assertion behavior, cleanup, and absence of
mutable-worktree inputs. Only a complete PASS-ready package returns for one
principal accept/freeze decision.

### R3 — one measured S1 execution

After R2, request a new principal authorization for exactly one measured S1
execution. It must run the unchanged 14-case/71-repetition corpus through the
deterministic SMA service interfaces and declared real persistence adapters.
No OpenHands process or generative model participates.

### R4 — S1 adjudication

Accept/freeze `SMA_SEMANTICS_QUALIFIED` for final P2 only if all frozen S1
requirements, evidence, and cleanup pass. A failure is recorded as evidence;
the harness is not patched to obtain a PASS.

### R5 — replace, do not continue, the incomplete S2 harness package

Candidate 10 is not an accepted or frozen S2 package. Its three manifest files
say `H0_WORK_PRODUCT_NOT_ACCEPTED`; its publication directory contains only two
integrated walks and two journals, with no H0 qualification receipt or
independent PASS. Under the accepted closure plan, that is `NOT_READY`.

Preserve candidate 10 as historical evidence, but do not execute or incrementally
repair it. After R4, create one new S2 successor lineage that:

- binds the newly accepted final-P2 S1 receipt;
- preserves the accepted S2 scientific questions unless prospectively amended;
- uses actual supported OpenHands request/event surfaces and the current SMA
  bridge, not fake-only routes or semantic commands below the test seam;
- derives evidence from raw HTTP, process, filesystem, MongoDB, and Qdrant
  receipts through one frozen orchestration path shared by offline and live
  adapters; and
- must be complete and independently offline-qualified before any live dress
  authority is requested.

This is a replacement qualification mechanism, not candidate 11 in the failed
iteration sequence.

### R6 — later layers

M1 remains `NOT_RUN` until a current S2 request-layout receipt exists. E1
remains `NOT_RUN` until current accepted S1, S2, and selected M1 PASS receipts
exist. Only accepted E1 evidence can close Step 15.

## Separate event-export lineage

The Teams read-only event-export boundary is accepted on canonical `main`.
Deployment, SMA-client qualification, and live-shadow qualification remain
`NOT_RUN`. That work may be planned separately and may proceed in parallel
under separate authority, but it neither substitutes for nor blocks the Step
15 S1 successor described here.

## Recommendation

**GO** to one bounded final-P2 S1 successor preparation and offline
qualification (R1 only).

**NO-GO** for current S1 scientific execution, any S2 live work, candidate-10
continuation, M1, E1, deployment, or production/historical access.
