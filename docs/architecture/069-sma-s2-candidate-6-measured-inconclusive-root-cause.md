# SMA-S2 candidate 6 measured attempt: INCONCLUSIVE

Date: 2026-08-14  
Status: `INCONCLUSIVE`

## Decision

Candidate 6 did not earn `OPENHANDS_SMA_BOUNDARY_QUALIFIED`. Its sole measured
authorization is consumed, and no rerun is authorized.

This is not an SMA-S2 product failure. The attempt stopped on qualification
harness defects before it could evaluate the corpus.

## What happened

**OBSERVED:** The driver completed ten repetitions of
`SMA-S2-001-FIRST-PROMPT-EMPTY`. In every repetition, OpenHands accepted the
prompt, the hook succeeded, the deterministic stub recorded exactly one
request and terminal response, the response write outcome was
`CLIENT_RECEIVED`, and the conversation status was `finished`.

The event snapshot used for grading nevertheless lacked the agent terminal
message in all ten repetitions. The resulting failure digest matches
`deterministic success terminal mismatch`.

The first repetition of `SMA-S2-002-SAME-PARTITION-DELIVERY` then stopped with
`prompt submission failed: HTTP 500`. Agent Canvas recorded server error ID
`a99d9dd5186649b7a7a4eb84096a9b20`; its log did not retain the underlying
exception, so that deeper server exception is unverified.

## Root cause 1: event-visibility race

**OBSERVED:** `wait_observation` exits as soon as it sees the user event, hook,
stub request, stub terminal record, and a terminal conversation status. It does
not require the agent `MessageEvent` that `one_prompt` grades immediately after
the loop.

**INFERRED:** With the local deterministic stub, status became terminal before
the next event fetch exposed the agent event. The harness therefore converted
a successfully delivered response into ten false failures.

## Root cause 2: corpus seeding bypasses the bound constructor

**OBSERVED:** Corpus seeding calls the inherited module-global
`create_raw_source`, which calls the inherited module-global
`create_conversation`. That constructor uses default OpenHands settings,
`secrets_encrypted: true`, three iterations, and the default tool/profile set.
It does not use `Candidate6Runtime.create_conversation`, its deterministic-stub
settings, or its one-iteration limit.

The server log confirms that this path loaded three tools plus the local Qwen
profiles immediately before the source-prompt POST returned HTTP 500.

**INFERRED:** The unbound seed-conversation path is the harness-level cause of
the case-2 stop. The internal Agent Server exception remains unknown because
the server log retained only its error ID.

## Cleanup

**OBSERVED:** Cleanup passed. All eleven attempt conversations were deleted,
both endpoint listeners exited, all candidate MongoDB and Qdrant namespaces
were absent, the candidate service was absent, and the workspace root was
removed. The original eight-conversation population returned with zero active
work. Tekroo Trader remains paused with unchanged timestamps.

## Required successor gate

Candidate 6 remains immutable. A candidate-7 harness successor should be
authorized only to:

1. wait for and stabilize the exact terminal event population before grading;
2. route all corpus-seed conversations through bound deterministic settings;
3. durably retain sanitized HTTP error identity and boundary phase;
4. add repeated fast-terminal and complete corpus-seeding controls; and
5. pass a broader, no-credit dress rehearsal of every distinct harness
   primitive before another 103-repetition attempt is considered.

No candidate-7 work or measured rerun is authorized by this adjudication.
