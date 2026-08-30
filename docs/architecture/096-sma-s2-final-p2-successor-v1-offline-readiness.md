# SMA-S2 final-P2 successor v1 offline readiness

Date: 2026-08-28
Decision: **PASS — ready for independent review; not accepted or frozen**
Claim available: **OFFLINE_HARNESS_QUALIFIED only**

## Authority and lineage

The principal authorized one bounded final-P2 S2 successor preparation and
offline qualification. The package binds accepted S1 receipt
`3315838d0c5047189494cece49b953481431321d16e238efa338712be076bee4`.
It preserves the accepted 20-case, 103-repetition S2 science without changing
the literal corpus or oracle matrix.

Candidate 10 remains historical evidence. No candidate-10 file was modified,
no candidate-10 driver was imported, and no candidate-10 scientific or
execution credit was carried forward.

## Replacement architecture

The successor uses one runner for the offline dress walk, the offline measured
plan walk, and any future separately authorized live execution. The runner owns
workflow sequencing, bounded polling, evidence reconstruction, grading,
recovery, and cleanup. Its ports expose only raw HTTP, filesystem, process,
MongoDB, Qdrant, and monotonic-clock receipts.

The offline simulator sits below those raw ports. It receives the same
OpenHands request shapes used by the packaged live adapter. It does not expose
fake qualification endpoints or semantic store commands.

The supported product surfaces are source-bound to immutable OpenHands commit
`a338ba9b6cbb529886b755a335bae3dee0004700` and final-P2 SMA commit
`fe9903cdf59497aabf12cbe7722e1397a1372068`. They include actual conversation
creation, event submission/search, pause, condensation, deletion, SMA health,
and SMA context-hook surfaces. Stub evidence is reconstructed from retained
raw and terminal JSONL receipts.

The live adapter is packaged but default-deny. It requires a file-bound,
single-use authority tied to the exact manifest and runner, rejects fabricated,
over-broad, mode-mismatched, or reused authority, and durably consumes authority
before its first raw action.

## Offline result

OBSERVED from the retained qualification receipt:

- 13/13 H0 controls passed.
- The shared runner completed the 34-operation dress plan and the full
  103-repetition measured plan with zero failed evaluations.
- All 59 scientific-oracle mutations and all 10 required-evidence mutations
  were rejected by their target oracle. Evaluator exceptions do not count as
  successful rejection.
- Delayed event and capture visibility consumed four delayed reads at each
  boundary and still reached the bounded stable result.
- Four-channel work used overlapping submissions and raw health telemetry;
  serialized execution is a rejecting mutation.
- Restart, capture-outage reconciliation, duplicate-event reconciliation,
  condensation, fault, cancellation, and per-repetition cleanup paths were
  walked through the common runner.
- Normal and optimized Python produced the same complete-H0 canonical outcome.
- Final disposable conversation, process, workspace, MongoDB, and Qdrant
  inventory was absent.
- Network calls, service starts, Maven invocations, OpenHands conversations,
  and real model calls were zero.

The machine readiness record is
`OUTPUT/phase-3/sma-s2-final-p2-successor-v1-offline-readiness.json`. The H0
receipt is
`OUTPUT/phase-3/sma-s2-final-p2-successor-v1-offline-qualification/offline-qualification-receipt.json`,
SHA-256
`1994b5977c9b71e2dc6d08dce2fad47584031033a3ab45fdc4fa5181815ba345`.

## Adjudication floor

COMPUTED from the passing offline controls: the replacement package is ready
for an independent read-only review.

It does **not** establish `OPENHANDS_SMA_BOUNDARY_QUALIFIED`. No live preflight,
service start, zero-credit live dress, measured S2 execution, model call, M1,
E1, or production/historical access occurred or was authorized.

The next gate is an independent package review followed by an explicit
principal `ACCEPT/FREEZE` or `REJECT` decision. Acceptance would still not
authorize live execution.
