# SMA-S1 candidate build, harness, and product-driver readiness

Date: 2026-08-13  
Scientific preregistration: **ACCEPTED / FROZEN**  
SMA clean build: **PASS**  
Harness mechanics self-test: **PASS CANDIDATE**  
Product-driver lifecycle controls: **PASS CANDIDATE / CONTROLS ONLY**  
Product-driver case-handler mechanics: **PASS CANDIDATE / NO PRODUCT CREDIT**  
Execution identity: **INCOMPLETE / NO-GO**  
Measured S1 execution: **NOT RUN / NOT AUTHORIZED**

## Repository and clean build

**OBSERVED:** SMA commit
`e89ff9e0bab192689122d5e7b9d36d16cae5d368` is present on
`origin/master`. The local SMA `master` worktree reported synchronized and
clean before the qualification artifacts were built.

**OBSERVED:** The logged `mvn clean verify` build reported 304 tests, zero
failures, zero errors, zero skips, and `BUILD SUCCESS`.

- Build receipt:
  `OUTPUT/phase-3/sma-s1-clean-build-receipt.json`
  - SHA-256:
    `f6a1c976ca936bad82ad805059583cbb35ac64e9251ee9a2330bbaca2a9c0af3`
- Raw Maven log SHA-256:
  `b96fad6ce18cfec1e45e47e20691e3cc9429045a51de992917d307db610031dd`
- Service JAR SHA-256:
  `ead8c0c5d9e3fd37b61318d2bdb8a97ef5c4c7481c11ed453777492f39f71042`
- Sources JAR SHA-256:
  `61db400f0bdcf24d21f72ed00e62bd4c6310c8a49ce7aa5f04af0c39b6beb139`

The receipt binds the exact final artifacts. It does not claim byte-identical
JARs across independent Maven builds because build-time metadata is present.

## Product-driver candidate

The model-free Java driver is outside the SMA production source tree and is
compiled against the exact clean-built service JAR.

- Java source SHA-256:
  `b22f2e658fad9aa38317e8647f070984a03f7a11a2d1ab0a6a4771243d213345`
- compiled driver JAR SHA-256:
  `e5d0cd194d77968dd18bf2aacd9dbbeb7eea68632b58baabc63681d99881e63a`
- hash-verifying launcher SHA-256:
  `6ce65e203c2de466d20bb6454e16429447fe50da3eccfb43df584c73fb4b8e50`
- build receipt:
  `OUTPUT/phase-3/sma-s1-product-driver-candidate-4/build-receipt.json`
  - SHA-256:
    `1e7310bb69ab5730e958e23c87e758f54a01721788576c005a5e3bd6546b4458`

**OBSERVED:** The driver implements strict request and namespace validation,
read-only inventory, fail-closed disposable preparation, deterministic
model-free corpus/vector seeding, and idempotent cleanup. It starts neither a
model nor OpenHands. The launcher refuses to execute if the SMA JAR, driver
JAR, or driver source digest differs from its bound value.

**OBSERVED:** All fourteen frozen `EXECUTE_CASE` handlers are now implemented
and compiled in candidate 4. They use deterministic loopback metadata/event
fixtures, direct SMA retrieval and persistence adapters, bridge metrics,
bounded concurrency, and mechanical predicate evaluation. Candidate-4 build
receipt SHA-256:
`1e7310bb69ab5730e958e23c87e758f54a01721788576c005a5e3bd6546b4458`.

The implementation has not been independently reviewed and has not run the
frozen scientific repetition schedule. It is therefore not yet a qualified S1
product driver.

## Lifecycle-control qualification

The final candidate-3 qualification executed exactly:

`INVENTORY → PREPARE → INVENTORY → CLEANUP → CLEANUP → INVENTORY`

**OBSERVED:** All six operations passed. The durable pre-assertion records
showed:

- initial MongoDB and both Qdrant namespaces absent;
- four Mongo memories and four points in each Qdrant collection after
  preparation;
- all three namespaces absent after the first cleanup;
- the second cleanup succeeded from the already-absent state; and
- the final inventory again showed all three namespaces absent.

**OBSERVED:** All six candidate-3 invocations had zero-byte stderr, zero
timeouts, exit code zero, a protocol-valid response, and no safety stop.
Candidate 1 had exposed a repeated 186-byte SLF4J initialization warning; the
hash-bound launcher was revised using SLF4J's locally verified internal
verbosity property before candidate 2 ran.

- Control receipt:
  `OUTPUT/phase-3/sma-s1-product-driver-controls-candidate-3/control-receipt.json`
  - SHA-256:
    `58399ed351eac3d8fbe444a0eecb74592d787dd27a2ac94e42d5833f637fbacf`
- Append-only journal:
  `OUTPUT/phase-3/sma-s1-product-driver-controls-candidate-3/control-journal.jsonl`
  - SHA-256:
    `9ab3e7102b958bd3f4ca9827c566b1012521b01d1c5395af435074672e8c03c4`

The receipt records six control operations, two cleanup attempts, zero
`EXECUTE_CASE` operations, zero measured S1 cases, no model or OpenHands use,
and no production or historical namespace permission. It awards no S1 product
result credit.

## Layered harness

- Harness SHA-256:
  `cbb3d1ac8b053817d84f8eb060af6ce28be7bc67abea77cc5a1b3993f29b0d92`
- Driver protocol SHA-256:
  `24d28b526af7d533c1d551b6cdc3d0c15a16f07615945ea570af9dd401374cad`
- self-test-only fixture-driver SHA-256:
  `283a537f6bb8c1e4d59e0c74e16ca97f2c696820ea1793d0f650fd2852579235`

**OBSERVED:** Candidate 5 passed all ten offline mechanics tests: artifact and
fence binding, create-once output, concurrent durable journaling, fault-control
mechanics, pre-assertion persistence, bounded process termination, exact driver
protocol, secret exclusion, cleanup idempotency, and journal reconstruction.

- Receipt:
  `OUTPUT/phase-3/sma-s1-harness-self-test-v5/self-test-receipt.json`
  - SHA-256:
    `d093d6d1b97f4512075c9e281ce2a005f067deec09117cb4bf9213b7c5b7747c`
- Journal:
  `OUTPUT/phase-3/sma-s1-harness-self-test-v5/self-test-journal.jsonl`
  - SHA-256:
    `ed0318d26e77da003dff8d4d11b6bc77a5ff12dd064eef9a97b61a881e948931`

The candidate-5 receipt records ten PASS results, zero measured cases, no SMA service
start, no MongoDB/Qdrant mutation, no OpenHands/model use, and no execution
authority. A negative control using a nonexistent authorization was refused
before output-root creation.

## Case-handler implementation checks

**OBSERVED:** Candidate 3 invoked each of the fourteen handlers exactly once in
a non-creditable implementation-check run. All fourteen returned a valid
protocol response, exact frozen predicate order, activated/observed fault
control, bounded termination, zero stderr, and no safety stop. Initial
inventory, preparation, two cleanup calls, and final absence inventory all
passed.

- Receipt:
  `OUTPUT/phase-3/sma-s1-handler-implementation-check-candidate-3/handler-check-receipt.json`
  - SHA-256:
    `2a65784dd8565f287090aaee58b59790cfb175882515120c109c9c4bdbd2f612`
- Journal:
  `OUTPUT/phase-3/sma-s1-handler-implementation-check-candidate-3/handler-check-journal.jsonl`
  - SHA-256:
    `12588e0ef8b1d00da1f5019313a2f2d039f450fd1bf4b914c37595ab0c045b4b`

The run was explicitly not a measured S1 execution: it used no single-use
authorization, executed zero scientific repetitions, and awards no S1 product
credit.

**OBSERVED:** The implementation checks recorded eight false predicates across
six cases. These are advance observations, not accepted S1 results:

| Case | Direct observation |
|---|---|
| 007 | conversation/source/sequence/workspace/profile identities were present, but explicit parent-child lineage was absent |
| 010 | a replay-scaffolding agent `MessageEvent` was captured as raw memory; no second capture loop occurred |
| 011 | the credential remained raw/ineligible and absent from vectors/retrieval/telemetry, but raw retention carried no explicit frozen sensitivity classification |
| 012 | an orthogonal unrelated query selected two actor-alpha memories because the retrieval service has no observed minimum similarity gate |
| 013 | count/character bounds and framing held, but canonical text was truncated inside memory boundaries |
| 014 | reflection over the bound `Memory`, `RetrievalEvent`, and `RetrievalDisclosureEvent` records found no explicit generic-summary trust or condensation/re-anchor state |

**COMPUTED:** Direct comparison of the six observations with the accepted
requirements and exact bound SMA source supports four product gaps: cases 007,
012, 013, and 014. Case 010 used an invented fixture discriminator not yet
bound to an actual supported OpenHands event schema. Case 011 depends on an
unfrozen sensitivity/raw-retention policy. The full evidence and adjudication
are recorded in
`docs/architecture/045-sma-s1-advance-gap-adjudication-and-remediation.md`.

**INFERRED:** A measured S1 run against the currently bound SMA artifact is
expected to fail the four confirmed predicates. Repeating them at the frozen
counts before remediation would consume the single-use run only to confirm
already exposed gaps. Cases 010 and 011 must be bound to primary interface and
policy evidence before either a product change or a measured result is valid.

## Remaining blockers

The execution-identity candidate remains an unaccepted draft at SHA-256
`fd43e33bed4b2935f81348aa297b4c3e217797b361f4073b9ab52221547e64ba`.
It is not eligible for acceptance because these items remain unresolved:

1. independent review of the fourteen handlers and their evidence mappings;
2. prospective SMA remediation for confirmed cases 007, 012, 013, and 014;
3. binding the actual supported OpenHands event discriminator for case 010;
4. freezing the sensitivity/raw-retention policy for case 011;
5. MongoDB and Qdrant topology/configuration digests;
6. resource-limit and pressure baselines;
7. a complete non-secret configuration digest; and
8. immediate pre-execution proof that the future measured namespaces are
   absent.

**INFERRED:** The driver and harness core are suitable for independent review.
They do not support `SMA_SEMANTICS_QUALIFIED`: the scientific schedule has not
run, the implementation checks expose four confirmed product gaps, and two
additional predicates lack valid interface/policy bindings.

## Recommendation

Send the adjudicated remediation handoff for independent SMA execution. Remediate
the four confirmed behaviors prospectively. For cases 010 and 011, return the
missing primary interface evidence and proposed frozen policy before changing
product behavior. Then review and bind a new SMA commit, revise the handlers
only where the new evidence requires it, and rerun non-creditable build,
control, and handler checks before completing runtime identity bindings. Do not
authorize measured S1 merely to reconfirm known gaps.
