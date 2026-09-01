# Tekroo v4 engineering instructions

## Governing authority

The latest accepted package is
`CONTRACTS/tekroo.kernel.contracts/0.9.0/`. Authorized Phase 7 implementation
uses the immutable successor candidate
`CONTRACTS/tekroo.kernel.contracts/0.10.0/`; its source-lineage decisions are
binding successor requirements. Packages `0.1.0` through `0.9.0` remain
preserved for historical replay and compatibility analysis. Public-network or
production deployment retains separate authorization. Do not edit an accepted
or released package in place. If code and its governing contract disagree,
stop and report the disagreement; do not weaken fixtures to make code pass.

## Scope boundaries

- Implement v4 from the approved semantics, not by copying the v3 codebase.
- Begin with the pure deterministic kernel and in-memory reference adapters.
- Keep provider, MongoDB, transport, API, CLI, and serialization concerns outside
  the pure domain evaluator.
- Treat provider status, tool output, model assertions, delivery claims, and SMA
  recall as evidence only; none creates organizational truth directly.
- Do not execute OpenHands/SMA investigations, migrate data, deploy production
  systems, or adopt providers without their separate authorization gates.

## Correctness and evidence

- Label substantive findings `OBSERVED`, `COMPUTED`, or `INFERRED`.
- Preserve command, event, receipt, authority, evidence, and provenance identity.
- Make all asynchronous failures observable and bounded by timeouts.
- Preserve deterministic outcomes under replay, duplicate delivery, permitted
  reordering, fake-clock variation, and actor-process restart.
- Every counterexample becomes a retained regression fixture before closure.

## Qualification

Implementation unit tests supplement but never replace the normative contract
corpus. `PASS`, `FAIL`, `NOT_RUN`, and `INCONCLUSIVE` retain their exact meanings;
skipped or unavailable work is never reported as passing.
