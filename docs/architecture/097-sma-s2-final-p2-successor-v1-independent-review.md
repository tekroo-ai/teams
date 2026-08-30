# SMA-S2 final-P2 successor v1 independent review

Date: 2026-08-28
Decision: **NO-GO — reject; do not accept or freeze**

## Review floor

OBSERVED: the published hashes match the reviewed files, the accepted S1 and
S2 identities remain intact, the preserved science has 20 cases, 103
repetitions, 59 predicates, and 10 evidence classes, and candidate 10 remains
untouched with zero imported credit.

Those identity checks pass. The claimed offline qualification does not.

## Decisive blockers

1. **Wrong dress composition.** The runner reaches 34 operations by executing
   cases 1–14 twice and cases 15–20 once. The accepted contract requires case 1
   ten times, cases 2–18 once, all four case-19 fault modes, and all three
   case-20 cancellation repetitions. Matching the total did not preserve the
   accepted experiment.

2. **No frozen live execution path.** The runner hard-codes `/offline` hook,
   journal, runtime, and workspace paths. Its command line rejects live use and
   accepts no authority or execution identity. A later live helper would require
   changing the package after freeze.

3. **Wrong OpenHands event schema.** The fake and observer use nested `input`,
   `output.additionalContext`, and event-level `terminal_status`. The bound
   OpenHands `HookExecutionEvent` uses `hook_input` and `additional_context`;
   terminal execution state is conversation-level. The qualifier never sends a
   real serialized product event through the observer.

4. **SMA boundary bypass.** COMPUTED from the retained measured ledger: calls to
   `/v1/openhands/context` are zero. The fake constructs context internally and
   inserts a private synthetic `delivery_events` record. Final-P2 SMA instead
   reconciles eligible OpenHands events into `memories` with required semantic
   metadata and provenance.

5. **Required workflows are not exercised.** Cases 6, 7, 10, 12, 14, 18, 19,
   and 20 omit or alter decisive restart, outage, reconciliation, event, fault,
   or cancellation mechanics. Most importantly, case 20 waits for the
   synchronous terminal and then calls `/pause`; the bound product states that
   `/pause` waits for the model call, while `/interrupt` cancels in-flight work.

6. **Derived-value mutation.** Resource and cleanup PASS dictionaries are
   inserted literally. The mutation suite edits reconstructed observation
   dictionaries rather than raw HTTP, filesystem, process, MongoDB, or Qdrant
   inputs. It demonstrates Boolean sensitivity, not raw-evidence correctness.

7. **Insufficient retained evidence.** The ledgers omit the observations, raw
   OpenHands event bodies, stub raw/terminal journals, Mongo/Qdrant documents,
   per-oracle outcomes, and per-repetition cleanup inventories. Independent
   regrading would require trusting the runner.

8. **Ungraded timing failure.** The accepted maximum is 120,000 ms per case.
   COMPUTED from retained `PREASSERTION` and `REPETITION_RESULT` timestamps: 53
   of 103 repetitions exceed the threshold; the maximum is 120,342 ms.

The machine review is
`OUTPUT/phase-3/sma-s2-final-p2-successor-v1-independent-review.json`.

## Adjudication

Successor v1 is rejected and must remain unfrozen historical evidence. Its H0
receipt does not establish `OFFLINE_HARNESS_QUALIFIED` or
`OPENHANDS_SMA_BOUNDARY_QUALIFIED`.

No live preflight, service start, dress rehearsal, measured execution, model
call, M1, or E1 is authorized.

## Corrective recommendation

Build one new successor around the actual serialized OpenHands event schema,
`/interrupt`, the real SMA context and event-intake paths, and real memory and
projection provenance. The new harness must use the exact accepted 34-operation
dress selector, retain append-before-action raw receipts sufficient for
independent regrading, grade timing and cleanup from those receipts, and inject
mutations below the raw adapter boundary.
