# SMA-S1 evidence-v2 measured execution adjudication

Date: 2026-08-14  
Scientific gate: **PASS**  
Claim: **`SMA_SEMANTICS_QUALIFIED`**  
Principal acceptance/freeze: **PENDING**

## Exact execution boundary

**OBSERVED:** the principal authorized one measured attempt against sealed
identity SHA-256
`139b38628e049ce2c0b04400014eacb2dd6ba9b4e9306c1347f4f0d3d12d3e36`
and post-seal offline receipt SHA-256
`e57ae8a21a01a7b1955dcf95332fb9129cdf585f903538c4eb3a7301b64565e3`.

**COMPUTED:** the resulting single-use authorization has SHA-256
`ea8d1b69fac1b50d5a9420e155d1159644556185921114a76e77cae8dacbfa92`.
It permitted one attempt, no corrective rerun, disposable namespaces only,
and no model or OpenHands use.

## Raw results

The create-once measured output root is
`OUTPUT/phase-3/sma-s1-measured-p2m-evidence-v2-sealed-v1-attempt-1/`.

**OBSERVED:** the execution receipt reports:

- harness status `PASS`;
- 71 completed repetitions and zero failed repetitions;
- no harness failure and no safety stop;
- complete minimum-PASS evidence;
- cleanup `PASS`.

**COMPUTED:** the execution receipt SHA-256 is
`485152e2fe74b62c426d0c2c35769379f1d05a5da7c94148bdffc5b27fdcadf3`.
The append-only 169-record raw journal SHA-256 is
`334d5e1e1845a7445f623ed88be957c7b433e5e5321c0d086a3c07715f91f5b4`.

**OBSERVED:** reconstruction from that raw journal found:

- all 14 cases passed every prescribed repetition;
- all 71 case assertions were `PASS`, with zero `FAIL` or `INCONCLUSIVE`;
- all 160 evaluations of the 32 distinct predicates passed;
- all nine restart-before assertions passed, and each after-restart receipt
  identified a distinct operating-system process;
- all five parent/child provenance repetitions retained exact conversation,
  parent, event, source, partition, workspace, profile, sequence, and role
  identities;
- no minimum-evidence gap and no safety-stop observation;
- contiguous unique journal sequence numbers 0 through 168.

## Quantitative thresholds

**COMPUTED:** the warm-hit nearest-rank p95 was 2.728375 ms and the warm-no-hit
nearest-rank p95 was 3.443416 ms; both are below the frozen 200 ms thresholds.

**COMPUTED:** the maximum fail-open duration was 30.029625 ms, below the frozen
500 ms hard deadline. Maximum canonical request and response sizes were 1,739
and 2,458 bytes, below their 1,048,576-byte and 2,097,152-byte thresholds.

**COMPUTED:** observed leak, raw-injection, duplicate-capture, feedback-loop,
secret-exposure, and lost-provenance counts were zero; irrelevant-query
injection rate was 0.0; bounded termination rate and same-partition expected
recall-at-five were 1.0. Maximum concurrency and queued work were both four.

## Cleanup and authority

**OBSERVED:** the harness cleanup assertion passed. Independent read-only
post-run queries found the measured MongoDB database absent and both measured
Qdrant collections absent. The post-cleanup verification receipt has SHA-256
`8693073ad3b80034e64c19574dcc8dfe77d058e08c4faa0fbf3346cd96c5f345`.

**OBSERVED:** the single permitted attempt has been consumed. It cannot be
reused and authorizes no corrective or additional run.

## Adjudication

**COMPUTED:** the frozen S1 scientific gate is `PASS`; the prior run's four
minimum-evidence gaps are all prospectively closed in this new evidence-v2 run.
The machine adjudication is
`OUTPUT/phase-3/sma-s1-measured-p2m-evidence-v2-sealed-v1-attempt-1/scientific-adjudication.json`,
SHA-256
`0e5ace0acbe6e2441755ae656de36096e38b2364d27fb45ee844f2422abf2f7d`.

**INFERRED:** the correct next action is to accept and freeze this S1 result,
then advance to the separately authorized downstream integration gate.

This result does **not** qualify OpenHands boundaries, model behavior,
integrated runtime behavior, workflow/process-control authority, or production
readiness.
