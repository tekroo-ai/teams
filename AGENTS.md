# Tekroo v4 engineering instructions

## Governing authority

The latest accepted package is
`CONTRACTS/tekroo.kernel.contracts/0.11.0/`; its source-lineage decisions are
binding successor requirements. Packages `0.1.0` through `0.10.0` remain
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

## Operational recovery lessons (run-060)

- Task recovery re-materializes the candidate workspace; the operator HTTP
  30-second read timeout killed its own `git` subprocess mid-recovery.
  Recovery runs on a detached context with its own bound.
- An interrupted operator recovery (unblock committed, authorize failed) must be
  resumable from its durable event checkpoint; never re-submit an `unblock`
  that is already in the task's event chain.
- An expired completion review whose successor ID is deterministic over the
  evidence set must be closed on the *same* ID before re-entry; otherwise the
  flow re-selects the stuck review forever.
- A validator stranded ACTIVE while its review target is COMPLETED must be
  re-driven through `completeEvidenceTask` on every pass.
- `POST /v1/conversations` status is `idle|running`; Teams treats `idle` as
  still-active, so a seeded predecessor conversation for an explicit-recovery
  profile must be `paused` before a recovery attempt can build its checkpoint.
- Feature acceptance binds the assembled candidate identity only to the
  whole-feature validator and the promotion; task-local validators may name
  narrower candidates.

## Operating a qualification daemon

Phase 9 run daemons may be supervised by ad-hoc `launchctl submit` KeepAlive jobs
that leave no plist on disk; discover them with `launchctl list | grep tekroo`
before stopping a daemon. Never combine `tekroo stop` with a manual foreground
start: the overlapping processes are recorded as physical host suspensions in the
qualification database, which fabricates execution-deadline allowance and
contaminates the run. Stop the supervisor first. With
`continuity.suspension_threshold` at 10 seconds, run-059 accumulated 34 bogus
suspension windows this way in about ten minutes.

Startup suspension reconciliation cost currently grows with suspension history
inside a fixed 20-second `startupTimeout`, so a crash-looping supervised daemon
degrades until it can no longer boot. See
`docs/architecture/127-phase-9-run059-shakedown-findings.md`.
