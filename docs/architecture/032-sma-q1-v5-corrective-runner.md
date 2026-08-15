# SMA-Q1 V5 telemetry-only corrective runner

Date: 2026-08-13  
State: **SEALED — AUTHORIZED, NOT YET EXECUTED**

## Outcome

The single corrective runner permitted by the frozen V5 rerun policy is ready
for principal review. It does not amend the accepted V5 manifest and does not
authorize execution.

## Scope

The corrective runner preserves the exact three qualification retrieval calls,
their order, prompts, conversations, workspaces, and acceptance criteria. It
adds no retrieval call, retry, warmup, delay, or changed threshold.

For each existing call it now retains content-free:

- elapsed time and HTTP status;
- hit count and memory IDs;
- context length and SHA-256;
- trace ID;
- resolved agent, workspace fingerprint, and profile; and
- decision and over-capacity response flags.

After all three existing calls have returned—and therefore after their outcomes
can no longer change—the runner records one aggregate bridge-health snapshot.
This supplies deadline, timeout, failure, readiness, capacity, and disclosure
counters without perturbing the measured qualification calls.

## Preserved evidence

The initial V5 attempt remains `INCONCLUSIVE / NO-GO`; none of its scenarios or
repetitions receives credit. Its receipt, journal, adjudication, and cleanup
evidence remain immutable and content-addressed by the corrective identity.

## Static verification

**OBSERVED:** Python compilation passed. The inherited no-service source,
partition, retained-V3, and fixture-driver checks passed. The deterministic
fixture driver compiled against the pinned SMA service JAR. No live service,
MongoDB database, Qdrant collection, OpenHands conversation, or scenario was
created by this verification.

Sealed hashes:

- frozen V5 manifest: `21030ef4a7004217401b0243e5bb7e94be6bc44ecb9869873a72f112fe676422`
- parent V5 runner: `45b9412e879d76d2beef957efdc23d7a9f7585e4e4820774ba5fb387c599872e`
- telemetry-only corrective runner: `c84e5557c6ca69362e2d57929a5b2536c381e447776f60a9c08da6160bd44494`
- corrective execution identity: `2b157d1431b1e7c0b7f4ec45bf8dad2d800c11d6306ff2954f0f62d0e3d1cd74`
- corrective authorization: `cd284196b70951e6ab9123a2bfab051384005593c2a4384bd37890378a5cce9f`

## Execution fence

Executing this runner would consume the only permitted V5 corrective attempt.
The principal explicitly authorized the sealed corrective runner. The separate
authorization artifact binds the frozen manifest, corrective identity, and
runner hashes and permits this final V5 attempt only.
