# SMA-S1 P2-M measured execution adjudication

Date: 2026-08-14  
Measured execution: **HARNESS PASS**  
Scientific S1 gate: **INCONCLUSIVE**  
Product failure established: **NO**  
Authorization: **CONSUMED / NO REUSE**

## Execution result

The principal authorized exactly one measured execution against sealed identity
SHA-256
`f8bf0323680504df1afb14fe6184e87eaaa675170079c005ce827fcfdfe15e0e`
and offline receipt SHA-256
`ca3c5aedebb05fe69fd88d4a2570ff6560c45f49cfe0e8d109591b9c95b7f076`.

**OBSERVED:** the harness accepted the exact five authorization bindings and
created the output root once. It completed all 71 preregistered repetitions,
reported zero failures, encountered no harness failure or safety stop, and
completed cleanup.

**OBSERVED:** the append-only journal contains 149 records with contiguous,
unique sequence numbers. It contains 71 pre-assertion evidence records and 71
matching PASS assertion records. All 32 frozen predicates passed at every
applicable repetition.

**OBSERVED:** the execution receipt is
`OUTPUT/phase-3/sma-s1-measured-p2m-sealed-v1-attempt-1/execution-receipt.json`,
SHA-256
`3f7d16db5ded23f11b3baad1ea6e37faf6fb82890feeb8211c3b433f7a7ed9c2`.
The journal SHA-256 is
`e17eea8db45c89ebc11fa58095c2e26c7ff9221d34869741f6352a2d9cc0f538`.

**OBSERVED:** both the driver's cleanup receipt and direct post-run service
queries show that the measured MongoDB database and both Qdrant collections are
absent.

## Threshold observations

**COMPUTED from retained raw records:** expected same-partition recall at five
was 1.0; cross-partition leaks, raw-memory injections, duplicate captures,
feedback-loop captures, secret-exposure signals, and irrelevant-query
injections were zero. Bounded termination was 1.0. The five fail-open samples
ranged from 28.256083 to 29.827291 milliseconds. The largest returned context
contained one memory and 3,183 characters with no mid-memory truncation. Four
concurrent calls and four queued operations were observed, and the largest
driver response was 2,419 bytes.

## Why the scientific gate is not PASS

The frozen design requires more than predicate truth. Its minimum PASS evidence
also requires raw monotonic durations with nearest-rank latency inputs, exact
source/provenance identities, restart receipts, and bound measurements.

**OBSERVED:** the measured journal lacks:

1. warm hit and warm no-hit context-service latency samples needed to calculate
   the two frozen p95 thresholds;
2. the exact runtime conversation, parent, event, sequence, workspace, profile,
   source, and partition values for case 007—it retains only booleans and
   counts;
3. discrete before/restart/after identity receipts for the three restart cases;
4. canonical inbound request byte lengths—it retains only request hashes.

Whole driver-process durations cannot substitute for context-service latency:
they include a new JVM launch and are not the metric named by the frozen
threshold. Source-code fixture values likewise cannot substitute for the exact
runtime identities that the minimum evidence rule requires.

**COMPUTED:** the retained evidence supports the harness PASS but is insufficient
for the scientific claim `SMA_SEMANTICS_QUALIFIED`.

**INFERRED adjudication:** S1 is **INCONCLUSIVE**, not FAIL. No observed product
predicate failed; the evidence-producing driver and harness omitted required
measurements. Post-observation regrading is frozen out, cleanup removed the
runtime namespaces, and the single-use authority has been consumed. This run
must remain immutable.

## Next boundary

The minimum prospective correction is to make the driver and harness retain the
four missing evidence categories mechanically, add offline positive and
negative controls for them, bind the revised artifacts in a new sealed
identity, and obtain a new single-use authority. The scientific cases,
predicates, thresholds, and P2-M implementation need not change based on this
result.
