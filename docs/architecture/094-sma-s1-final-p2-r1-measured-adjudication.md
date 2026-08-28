# SMA-S1 final-P2 R1 measured adjudication

Date: 2026-08-28
Measured attempt: **PASS — 71/71**
Scientific classification: **PASS — FINAL-P2 SMA SEMANTICS QUALIFIED**
Principal acceptance/freeze: **ACCEPTED / FROZEN**

## Authority and identity

The principal authorized exactly one attempt against sealed identity
`2cd705ab40a0aabee0fea5115154920675ebba600a2e2e670a368c51c25b1738`
and post-seal receipt
`0fbc9bf3a671143305173304caf0bf6f0e6b67ecc2ac681f8fe5bd411b73c283`,
under preflight
`0a897a45629e2dea9bf0badaabaf98e6d4fef63e690a2ec250cf40315d02ce6e`.

The authorization was consumed before the first measured action. It permits no
rerun and creates no S2, M1, E1, OpenHands/model, or production/historical-data
authority.

## Result

**OBSERVED:** the frozen harness completed all 71 repetitions with zero
failures, no harness failure, no safety stop, complete minimum-pass evidence,
and cleanup PASS.

**COMPUTED:** all 71 case assertions and all 160 predicate evaluations passed.
The journal contains 169 uniquely numbered contiguous records from sequence 0
through 168. All 14 frozen cases received their declared repetition counts.

**COMPUTED:** warm context-service nearest-rank p95 was 3.313459 ms with hits
and 3.320209 ms without hits, against the 200 ms threshold. Fail-open maximum
latency was 38.502 ms. The maximum canonical request and response sizes were
1,682 and 2,453 bytes.

**OBSERVED:** independent post-run reads found the exact disposable MongoDB
database absent and both exact Qdrant collections absent with HTTP 404.

The execution receipt SHA-256 is
`5379325c60138b129227d7df006b4d104e01638ac37fad9d7db391e4db1b7c55`;
the raw journal SHA-256 is
`2b4a0c9971658f28ac5b4d0d97d45f050702c24fed6d102d304c9b6640e6c331`.

## Adjudication

The scientific adjudication is
`OUTPUT/phase-3/sma-s1-measured-p2final-r1-sealed-v1-attempt-1/scientific-adjudication.json`,
SHA-256
`d4800482ef8204904c182783abb8800770b46d08f572df5f717e5c1a2ed645b5`.

The read-only adjudication validator is
`scripts/validate_sma_s1_p2final_r1_measured_adjudication.py`, SHA-256
`469d5a0903fe526ee43802cb16b9ae7ae611df1c5138cebc2e3768b0553db684`.
It independently recomputes and passes 47/47 content, case-distribution,
predicate, journal, cleanup, and authority checks.

**INFERRED:** the exact bound final-P2 SMA implementation satisfies the frozen
S1 deterministic semantic qualification through the declared service
interfaces and persistence adapters.

This does not qualify OpenHands integration, model behavior, the integrated
runtime, workflow/process control, or production readiness.

## Acceptance

The principal accepted and froze the exact scientific adjudication hash. The
acceptance record is
`investigations/sma-q1/layered/sma-s1-p2final-r1-scientific-adjudication-acceptance.json`.
The consumed authorization cannot be reused, and this acceptance creates no
downstream authority.
