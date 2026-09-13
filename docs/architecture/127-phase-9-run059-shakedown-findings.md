# Phase 9 — Run-059 shakedown findings

Status: run-059 is **retained as evidence and retired**; it is not a qualification
run. Its durable state carries operator-provenance contamination recorded below.

## Findings

### 1. OBSERVED — validator output protocol could not carry raw test receipts

Tester invocation `2cad15b4-360b-71dc-858f-cbdaff4fd569` completed at the model
level with a correct `PASS` bound to candidate
`5e0ef6c2-5ea2-7143-8f9b-37336b3942ea`, receipt
`6a00dcdbee2d8ea26069dee8698a12f38aed6dd126fbf6b5708b02a726b927d8`, and nine
reasons. Teams blocked the task with
`task returned an invalid structured result`.

COMPUTED by replaying the stored `finish.message` bytes through
`parseStructuredValidationResult`: the sole cause was an additional
`test_evidence` array of 14 raw test receipts. The parser sets
`DisallowUnknownFields`, so one unlisted field rejected an otherwise valid
result.

Phase 9 requires the tester to "return raw receipts", but the result protocol
published no field able to carry them. The strictness is correct and remains:
unknown fields still fail closed, so a result cannot smuggle control fields.
`test_evidence` is now a known field validated as a bounded list of flat
trimmed strings and treated as advisory only — Teams decides from `outcome` plus
the candidate binding, never from receipts.

Retained fixture:
`adapters/operationalruntime/testdata/run059-validator-2cad15b4.result.txt`
(exact bytes), with `validation_result_receipts_test.go` asserting both the
accepted shape and the negative boundary cases.

### 2. OBSERVED — restart churn fabricated host suspensions

`run-059`'s daemon was supervised by an ad-hoc `launchctl submit` KeepAlive job
with no plist on disk. An operator `tekroo stop` issued while the job was live,
followed by a manual foreground start, overlapped the supervisor. With
`continuity.suspension_threshold` of 10 s and `StopRuntimeSession` recording a
clean-stop anchor, the gaps were classified as **physical host suspensions**:

| quantity | before | after |
|---|---|---|
| `runtime_suspensions` windows | 38 | 72 |
| feature budget deadline | `2026-09-10T09:34:15.24Z` | `2026-09-10T09:34:22.231Z` |
| budget account revision | 60 | 62 |
| events | 572 | 932 |

Thirty-four windows and seven seconds of execution-deadline allowance are
therefore artifacts of operator process management, not of host sleep. This is
why run-059 cannot serve as the clean run and why a fresh deployment is used.

Note that recording a *clean* stop as a suspension window is correct behavior:
`StopRuntimeSession` stores `SuspendedAt` so a deliberate stop's duration is
excluded from team deadlines exactly once. The defect is not that a stop was
recorded; it is that a *concurrent* supervised process was indistinguishable
from an outage, and that a repeated restart keeps minting new windows.

### 3. OBSERVED — startup suspension reconciliation is unbounded in a fixed budget

`cmd/tekrood/main.go` sets `startupTimeout = 20s`, and
`ProductionService.Start` treats any `reconcileRuntimeSuspensions` error as
fatal. Reconciliation iterates every suspension window against every planned
task, and for windows whose deterministic `work-budget.amend` receipt is absent
it re-submits an amendment plus per-task `ReadAggregateHead` and `LoadDecision`
calls. Fourteen windows were in that state because earlier processes were killed
mid-reconciliation.

A foreground start ran 18.99 s and failed at window 58 of 72, so startup cost is
O(suspension history x planned tasks) with no pagination and no checkpoint. The
failure surfaces as `ErrInvalidFeature` rather than a context error, so the
precise rejecting predicate is not yet isolated; that isolation is a follow-up.

The structural consequence stands regardless of which predicate rejects: once a
deployment accumulates enough suspension history it can never start again, and
each failed boot adds more history.

## Follow-ups, not yet implemented

0. **Pending decision (accepted):** the clean run will be qualified on
   `ddalcu--Qwen3.8-Flash-Next-MLX-Serve-mixed-4-8bit`, not the retired 27B
   model. All §D role-profile canaries must be re-passed against the exact new
   tuples and a new qualification bundle built from those canary packages
   before `init-local` can provision run-060. The exact profile digests
   `init-local` generates are recorded in
   `/Users/paul/.local/share/tekroo/phase9/run-060/flash-next-generated-profiles.txt`.
   Because the model changed, run-059's role-performance evidence (plan review
   PASS, coder/tester canaries inside the run) belongs to the 27B tuple and is
   historical, not qualification of Flash-Next.

1. Bound startup reconciliation with its own budget and make transient storage
   errors non-fatal: keep admission closed (`RECONCILING`) and surface the error
   through status instead of exiting, preserving the accepted gate that
   reconciliation completes before admission reopens.
2. Checkpoint or paginate suspension reconciliation so startup cost does not
   grow with immutable history.
3. Make `runProjector` and the recovery loops retry with backoff rather than
   return fatal errors that terminate the process.
4. Distinguish an operator-initiated stop from an unavailability window in a way
   that survives concurrent supervised processes, without weakening the
   exactly-once deadline allowance.
5. Items 1–4 belong with the still-unwired physical sleep/outage path in
   `docs/operations/007-team-process-qualification-checklist.md` section C.

## Operational rule

`run-059` was supervised by a launchd KeepAlive job. Never stop or start a
supervised `tekrood` with `tekroo stop` plus a manual foreground start: the
overlap fabricates suspensions in the qualification database. Stop the
supervisor first, then the process.
