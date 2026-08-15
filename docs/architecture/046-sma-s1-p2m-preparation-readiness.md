# SMA-S1 P2-M preparation readiness

Date: 2026-08-14  
Scientific preregistration: **ACCEPTED / FROZEN**  
Execution identity: **COMPLETE CANDIDATE / PENDING PRINCIPAL ACCEPTANCE**  
Offline harness: **PASS CANDIDATE / PENDING PRINCIPAL ACCEPTANCE**  
Measured S1 execution: **NOT RUN / NOT AUTHORIZED**

## Result

**OBSERVED:** the remote branch `codex/sma-p2-m-release-mtls` resolves to commit
`5d58be508c74ae8577d2fd31e3346be23a912ab3`. An isolated detached worktree at
that commit remains clean after the build. SMA's advancing P2-N worktree was
not changed or used.

**OBSERVED:** a fresh `mvn clean verify` against the exact P2-M source reported
477 tests, zero failures, zero errors, zero skips, and `BUILD SUCCESS`.

- build receipt:
  `OUTPUT/phase-3/sma-s1-p2m-clean-build-receipt.json`
- build receipt SHA-256:
  `e5d92da8efbba6556deee36071895c999a145c6f4b8c5e60ac5f83dff4a0e5fc`
- service JAR SHA-256:
  `c0ddb963798d45603c2e2685744892c40d3e841817f9c966ab7fad34f7e48765`

## Product-driver preparation

**OBSERVED:** candidate 7 compiles all fourteen handlers against the exact
P2-M service JAR. It binds the supported P1 semantic-event metadata, typed
parent/child provenance, semantic acceptance threshold, whole-frame context
budget, protected raw/safety decision components, and the delivery-manifest
state machine through its real MongoDB adapter.

**OBSERVED:** the non-creditable handler check invoked each handler once. All
fourteen handler mechanics and all 32 frozen predicates returned true. Initial
inventory, preparation, two cleanup calls, and final absence inventory passed.
The run executed zero scientific repetitions and consumed no authorization.

- driver build receipt SHA-256:
  `a63a702707a73b3d5997a5ceca375468a77ea70ed52f53174e73f30e902a10ca`
- handler receipt SHA-256:
  `99c1128af41904e0e361176732c7015df7d008d0fa9123c2463b976ec2781955`
- handler journal SHA-256:
  `0cacfb4d12a95313710f9d354ca940837a741febec0ee773ea926e2fa28fb1fc`
- independent handler/evidence review SHA-256:
  `a3103e7c898583bfbdd62576604ded1c1c3282ddb57fdb193cd5fbb710a2d539`

Candidate 5 is retained as an advance failure receipt. Its only false predicate
was caused by a lowercase comparison against uppercase persisted enum values.
Candidate 6 corrected that evidence reader and passed, but review then found
that case 014 still used an in-memory delivery repository. Candidate 7 replaced
it prospectively with `MongoDeliveryManifestRepository` and passed again. No
prior receipt was rewritten.

## Environment and isolation

**OBSERVED:** the bound environment uses MongoDB 8.3.4 as single-member replica
set `rs0`, Qdrant 1.18.2 commit
`44ad62f8cd69642be5afa6441612525e24a0d063`, Java 21.0.12, and Maven 3.9.16.
The configuration, topology, host capacity, process limits, and pressure
baseline are content-addressed at:

- environment binding:
  `investigations/sma-q1/layered/sma-s1-p2m-environment-binding.json`
- SHA-256:
  `46af2cba3ea00e0852a0ad034450705b7d3a02098925625d6e61e4709b5380c0`

**OBSERVED:** the proposed measured MongoDB database and both proposed measured
Qdrant collections were absent during preparation. Their absence, service
identity, artifact digests, and pressure state must be rechecked immediately
before any separately authorized measured run.

## Exact execution identity

- identity:
  `investigations/sma-q1/layered/sma-s1-execution-identity-p2m-candidate-7.json`
- identity SHA-256:
  `e67d731d4d4ab8936c3ccf8d89c336614f5049ee6294029c5fce94d27d28774f`

The identity binds P2-M rather than the moving SMA P2-N branch. It explicitly
excludes P2-N hard deletion from S1 and makes no claim about live OpenHands
wiring, model behavior, deployment, or production readiness.

## Offline harness qualification

**OBSERVED:** the exact offline harness bound to the candidate-7 identity
passed all ten required mechanics tests. The machine receipt records zero
measured cases, no execution authority, no SMA service start, no MongoDB or
Qdrant mutation, and no OpenHands or model use.

- machine receipt SHA-256:
  `715933557d86c606f12a0fe106157bd4efd5c419dc424b88a882a4f40a3352a5`
- append-only journal SHA-256:
  `f60ea96a2ebc9b6916714b0ef5ca762c18a61997d75d308e2844aa4c9d3b0a21`
- qualification review SHA-256:
  `1174d5743b88be2f22210b448ebbbf80430164654daae4195d201b2d350eabe9`

## Gate recommendation

**COMPUTED:** all preparation blockers identified in the candidate-4 identity
are now resolved prospectively: exact SMA identity, fresh build artifacts,
P1/P2 policy bindings, fourteen-handler review, MongoDB/Qdrant topology and
configuration, resource baseline, non-secret configuration, planned unique
namespaces, and a sealed offline harness receipt.

**INFERRED recommendation:** accept the exact candidate-7 execution identity
and its exact offline harness qualification together. After acceptance, perform
the required immediate drift/absence checks and ask for one single-use measured
S1 authorization. Do not run a measured case before that authorization exists.

