# SMA-S1 evidence-v2 candidate 4 readiness

Date: 2026-08-14  
Scientific design: **UNCHANGED / FROZEN**  
P2-M implementation: **UNCHANGED**  
Evidence-v2 candidate: **OFFLINE PASS / PENDING ACCEPTANCE**  
Measured execution: **NOT RUN / NOT AUTHORIZED**

## Result

**OBSERVED:** evidence-v2 candidate 4 compiles against the exact P2-M service
artifact and changes no SMA product source. Its non-creditable handler check
passed all 14 handlers, all 32 frozen predicates, and all five lifecycle
controls. It executed zero scientific repetitions.

**OBSERVED:** the three restart cases used separate operating-system processes.
Their retained before/after receipts contain distinct PIDs and process start
instants while reading the same disposable persisted state.

**OBSERVED:** same-partition and empty-result handlers now make a warm-up call
and a measured call on the same context-service instance. Each retains its raw
monotonic elapsed nanoseconds and hit class. The harness retains all samples and
computes nearest-rank p95 against the frozen thresholds.

**OBSERVED:** case 007 now retains the exact synthetic runtime memory, partition,
source, conversation, parent, event, workspace, profile, role, sequence, model,
authorship, purpose, finality, and sequence-position values. An absent parent is
represented by a typed `{present:false}` identity rather than omission or a
sentinel string.

**OBSERVED:** every driver request and response now retains both its canonical
SHA-256 and byte length. The measured harness will fail closed against the
frozen inbound-request and receipt-payload limits.

## Controls and lineage

Three zero-credit candidates were retained rather than rewritten:

1. candidate 1 rejected missing event sequence evidence;
2. candidate 2 rejected omission of an intentional null parent identity;
3. candidate 3 demonstrated that embedding `JsonNull` in a Java map was still
   suppressed by the serializer.

Candidate 4 replaced the ambiguous null representation with a typed
presence/value object and passed.

**OBSERVED:** the versioned offline harness passed 12 of 12 mechanics tests,
including new positive and negative controls for nearest-rank p95, request-byte
bounds, exact provenance completeness, and distinct-process restart identity.
It started no SMA service through the fixture driver, mutated no MongoDB or
Qdrant namespace, created no authority, and executed zero measured cases.

**OBSERVED:** the candidate-4 handler check cleaned its disposable MongoDB and
Qdrant namespaces; direct post-check queries confirmed their absence.

## Exact candidate

- identity:
  `investigations/sma-q1/layered/sma-s1-execution-identity-p2m-evidence-v2-candidate-4.json`
- identity SHA-256:
  `d7a388816ccb4ea2c12db365de1116c445f6b8dd18eca8861f118afa7cfb8724`
- offline qualification:
  `OUTPUT/phase-3/sma-s1-p2m-evidence-v2-candidate-4-offline-qualification.json`

**COMPUTED:** all four evidence categories that made the prior measured attempt
inconclusive are now closed prospectively. The prior attempt remains immutable,
inconclusive, and contributes zero measured credit.

**INFERRED recommendation:** accept and freeze the exact candidate-4 identity
and offline qualification together. Acceptance should authorize only creation
of a non-authorizing sealed successor and immediate read-only preflight. A new
measured attempt must still receive a separate single-use authorization.
