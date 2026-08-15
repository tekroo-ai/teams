# SMA-S1 advance-gap adjudication and remediation handoff

Date: 2026-08-13  
Scope: prospective remediation before measured SMA-S1 execution  
Measured S1 status: **NOT RUN / NOT AUTHORIZED**  
SMA production mutation by Teams coordinator: **NONE**

## Purpose

The non-creditable candidate-3 handler check exercised each frozen S1 handler
once to validate driver mechanics. It recorded eight false predicates across
six cases. Those observations are not accepted S1 results. This document
adjudicates whether each is presently strong enough to authorize SMA
remediation.

The evidentiary rule is deliberately asymmetric: a product change requires an
accepted requirement plus direct current-source or runtime evidence. An
invented fixture field or an unbound policy cannot justify a code change.

## Governing artifacts

- accepted S1 preregistration:
  `investigations/sma-q1/layered/sma-s1-preregistration.json`, SHA-256
  `e663f5b732c22248adc5fab2f536dbf02dd72337f7c8428f225937e72d6a7f8c`;
- layered qualification design:
  `docs/architecture/041-step-15-layered-qualification-specification.md`,
  SHA-256
  `1700db925d2b8a42cc3df073b9d678dd971dc0d604bb6f01d2b35488350ebfff`;
- bound SMA source commit:
  `e89ff9e0bab192689122d5e7b9d36d16cae5d368`;
- bound SMA service JAR SHA-256:
  `ead8c0c5d9e3fd37b61318d2bdb8a97ef5c4c7481c11ed453777492f39f71042`;
- handler-check journal SHA-256:
  `12588e0ef8b1d00da1f5019313a2f2d039f450fd1bf4b914c37595ab0c045b4b`.

## Adjudication summary

| Case | Adjudication | Action now |
|---|---|---|
| 007 parent-child provenance | **CONFIRMED PRODUCT GAP** | Remediate prospectively |
| 010 feedback-loop prevention | **INCONCLUSIVE — FIXTURE CONTRACT UNBOUND** | Bind an observed supported event discriminator first |
| 011 secret quarantine | **INCONCLUSIVE — POLICY UNBOUND** | Freeze the sensitivity/raw-retention policy first |
| 012 empty result | **CONFIRMED PRODUCT GAP** | Remediate prospectively |
| 013 oversized context | **CONFIRMED PRODUCT GAP** | Remediate prospectively |
| 014 condensation/re-anchor | **CONFIRMED PRODUCT GAP** | Remediate prospectively |

## Confirmed remediation R1 — parent-child provenance

**OBSERVED:** The accepted programming-memory protocol requires capture to
preserve conversation, event, workspace, task, role, parent/child, model,
sequence, and Git provenance when available. The accepted OpenHands integration
plan separately requires parent and child conversations to use their actual
OpenHands IDs.

**OBSERVED:** `OpenHandsEventIntakeService` reads only conversation ID,
workspace, profile, and model into `ConversationMetadata` (source lines
123–151 and 485–490). The resulting memory preserves conversation/event only
inside `origin.sourceRef`, plus workspace/profile in `agentId`; it has no
parent-conversation field (lines 232–285). The bound `Memory` record likewise
has no explicit conversation or parent-child provenance object.

**OBSERVED:** The deterministic parent/child fixture captured both memories and
preserved source, sequence, workspace, and profile identities, but the child
document contained no explicit reference to its actual parent ID.

**REQUIRED REMEDIATION:** Add a typed, persistence-mapped provenance structure
that preserves at least actual conversation ID, parent conversation ID when
present, event ID, workspace identity, profile/role, sequence, and model. Do not
encode the parent relation into free text or infer it from actor identity. Keep
existing deterministic memory IDs and backward-compatible reads of existing
documents. Add mapper round-trip, absent-parent, parent/child, restart, and
duplicate-reconciliation regressions.

## Inconclusive item P1 — feedback-loop event discriminator

**OBSERVED:** The accepted requirements exclude reasoning, system scaffolding,
hook context, tools, and utility traffic. Current intake accepts every textual
`MessageEvent` whose source is `agent` or `user`; after kind/source checks it
does not inspect a semantic event subtype (source lines 213–230).

**OBSERVED:** The candidate handler supplied a synthetic
`semantic_source=replay_context_scaffolding` field. No named design artifact,
bound OpenHands schema, current SMA source, or prior exact capability receipt
establishes that field as a supported OpenHands discriminator.

**INFERRED:** The product needs a trustworthy way to distinguish a final agent
message from non-user scaffolding, but the current handler does not prove what
that way is. Coding against its invented field would repeat the proxy-evidence
failure this qualification redesign was created to prevent.

**PREREQUISITE DECISION:** Inspect an exact supported OpenHands event schema or
raw synthetic event receipt and bind the real discriminator(s), including their
absence behavior. Then amend only the execution identity/fixture mapping—not
the accepted scientific predicate—and implement fail-closed capture
eligibility. If no supported discriminator exists, return a documented
interface gap instead of guessing from text.

## Inconclusive item P2 — secret quarantine policy

**OBSERVED:** The protocol requires credentials and prohibited sensitive
material to be stripped or quarantined before promotion. It also defines a
minimum adapter entry contract containing `sensitivity_label`. The currently
bound raw `Memory` schema has neither sensitivity nor secret-classification
fields.

**OBSERVED:** The handler showed that a synthetic credential was retained only
as `RAW`, with `reasoningEligible=false`, no vector, no retrieval event, and no
operational stderr. This is compatible with a broad interpretation of
“quarantine.” It also showed that no explicit sensitivity classification was
persisted.

**OBSERVED:** The S1 execution identity still leaves the non-secret
configuration and therefore the exact sensitivity/raw-retention policy unbound.
The accepted predicate explicitly conditions raw retention on that frozen
policy.

**INFERRED:** Absence of a separate classification field does not, by itself,
prove failure until the policy says whether `RAW + ineligible` is sufficient
quarantine, whether credential bodies may be retained at all, and what
redaction/classification receipt is mandatory.

**PREREQUISITE DECISION:** Freeze one deterministic policy with exact detection,
retention, redaction, classification, vector, retrieval, event, log, and
deletion behavior. Bind its digest in the S1 identity. Then revise the handler
to measure that policy exactly and implement only demonstrated missing product
behavior. No real credential may be used.

## Confirmed remediation R2 — irrelevant-query rejection

**OBSERVED:** Accepted S1 requires empty or irrelevant retrieval to select no
memory. The deterministic orthogonal query selected
`mem-alpha-adversarial` and `mem-alpha-timeout` with zero semantic relation.

**OBSERVED:** `SmaRetrievalService.prepareSemantic` obtains Qdrant candidates,
filters partition/eligibility/tier/text, adjusts scores, sorts, and takes the
requested limit (source lines 183–229). It applies no minimum raw or adjusted
similarity threshold. `SemanticMemoryConfig.RetrievalConfig` contains only a
co-activation threshold and retention days.

**REQUIRED REMEDIATION:** Add an explicit configurable semantic-retrieval
acceptance threshold with validation and deterministic defaults. Apply it
before audit/result construction and pressure mutation. Retain exact exclusions
and reasons for qualification evidence. Add below/equal/above-threshold,
partition, raw/ineligible, empty, and no-side-effect regressions. The eventual
S1 identity must bind the chosen threshold; do not choose it after observing a
measured S1 result.

## Confirmed remediation R3 — whole-memory context budgeting

**OBSERVED:** Accepted S1 requires truncation or omission only at memory
boundaries. The candidate supplied six 3,000-character memories. The bridge
returned three memories and 4,096 context characters with valid framing, but
each selected canonical body was partially truncated.

**OBSERVED:** `OpenHandsBridgeServer.buildContextBlock` divides remaining
characters across remaining entries and calls `truncate` on each canonical text
(source lines 804–831). The final context is then substring-bounded. This is
explicit mid-memory truncation.

**REQUIRED REMEDIATION:** Build complete framed entries and append a whole entry
only if it fits the remaining count and character budget. Otherwise omit that
entry and retain a content-free omission reason/count. Never emit an empty or
partial memory frame. Preserve the existing stricter 3-result/4,096-character
product bounds unless separately revised. Add exact-fit, one-character-over,
single-entry-too-large, multi-entry, Unicode, stable-order, and framing
regressions.

## Confirmed remediation R4 — delivery manifest and condensation re-anchor

**OBSERVED:** The accepted protocol requires an external per-session delivery
manifest, forbids inferring delivery state from the current prompt, and requires
a new context epoch plus fresh bounded snapshot after condensation. The layered
S1 design explicitly assigns deterministic condensation/re-anchor state to S1,
without an OpenHands process.

**OBSERVED:** Searches of current production source found no session-delivery
manifest or condensation/re-anchor state implementation. The bound `Memory`,
`RetrievalEvent`, and `RetrievalDisclosureEvent` records expose no such state.
The bridge tracks individual disclosure completion but not the session manifest
required to decide snapshot/delta/re-anchor behavior.

**REQUIRED REMEDIATION:** Implement a deterministic delivery-manifest service
and declared persistence adapter independent of the live OpenHands process. It
must preserve session identity, context epoch, scoped principal/project/task
identity where applicable, delivered memory revision/card digest/state,
expanded state, last OpenHands event, last condensation event, and update time.
Condensation must atomically advance the epoch, invalidate prior model-visible
delivery certainty, and require a fresh bounded snapshot on the next request.
Duplicate condensation and restart/replay must be idempotent. Generic summary
text must never establish exact delivery. Add new-session, unchanged delta,
condensation, duplicate condensation, crash/restart, stale epoch, and bounded
snapshot regressions.

## Change and compatibility boundaries

The SMA remediation work must:

- use a new prospective SMA commit; do not amend accepted receipts or rewrite
  historical evidence;
- leave Teams source and accepted contract packages unchanged;
- preserve existing Mongo documents through backward-compatible readers and
  additive initialization/migration where required;
- use no model and start no OpenHands process in its deterministic regressions;
- use disposable MongoDB/Qdrant namespaces for integration tests;
- retain all current clean-build tests plus new counterexample regressions;
- make no measured S1 claim and consume no S1 authorization; and
- return a clean worktree, exact commit/tree/artifact hashes, commands, test
  counts, and content-free logs/receipts.

## Required SMA work-product receipt

The SMA team must return one receipt with four sections:

1. `CONFIRMED_REMEDIATIONS`: R1–R4, exact changed symbols, regressions, and
   before/after mechanical observations;
2. `PREREQUISITE_FINDINGS`: P1–P2, exact primary evidence and a proposed frozen
   contract/policy—no guessed implementation;
3. `COMPATIBILITY`: old-document reads, schema/index changes, rollback, and
   cleanup proof; and
4. `IDENTITY`: commit, tree, branch, clean status, dependency and built-artifact
   digests.

The work product is reviewable evidence only. The Teams coordinator will update
the S1 execution identity and rebuild the driver fixtures prospectively after
review. Measured S1 remains separately authorized.
