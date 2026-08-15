# SMA-S2 candidate 9 working-directory remediation review

Date: 2026-08-14  
Status: `PASS_READY_FOR_PRINCIPAL_REVIEW`

## Recommendation

`GO` for principal acceptance/freeze of candidate 9 only.

This does not authorize execution-identity creation, live-state preflight, a
live dress rehearsal, Maven invocation, or measured execution.

## What changed

**OBSERVED:** Candidate 9 preserves the candidate-8 `actor-empty` isolation and
the exact `actor-alpha`/`actor-beta` capture allowlist.

**OBSERVED:** Maven promotion now passes
`cwd=/Users/paul/work/tekroo-ai/sma-s1-p2m` directly to `subprocess.run`. The
driver does not use `os.chdir`, so the parent process working directory is not
mutated.

**OBSERVED:** Before promotion, the driver validates the exact project and
`pom.xml`. On subprocess failure it records the error type, exit status,
timeout flag, command digest, and stdout/stderr lengths and SHA-256 digests. Raw
stdout and stderr are not persisted.

**OBSERVED:** The execution-identity, dress-authorization, dress-receipt, and
measured-authorization fences require the exact Maven-promotion contract.

## Offline qualification

**COMPUTED:** The final optimized-Python qualification passed 13/13 tests.
Thirty-one path/SHA references were checked with zero mismatches.

Controls cover missing and mutated contracts, missing and non-Maven
directories, explicit subprocess cwd, unchanged parent cwd, successful
promotion receipts, sanitized `CalledProcessError` and timeout evidence,
identity/authorization mutations, preservation of candidate-8 workspace
isolation, exact 34/103 plans, Python optimization, and default deny.

The first offline receipt is retained as 12/13 `FAIL`: its synthetic identity
fixture incorrectly used an empty dependency closure. The production fence
correctly rejected it. The corrected fixture binds a non-empty exact closure;
the final receipt is 13/13 `PASS`.

**OBSERVED:** No Maven process, network call, OpenHands conversation, SMA
service, deterministic stub, live rehearsal, measured attempt, or result credit
occurred.

## Candidate identity and authority boundary

The candidate-9 preregistration SHA-256 is:

`d7680cc0b7b5ed501201e4b9dcc54b089071267c75ca43e3b9fab13b95b251a8`

It preserves 20 cases, 103 measured repetitions, 59 predicates, 10 required
evidence classes, the 34-operation zero-credit rehearsal, all thresholds and
fault schedules, and the eventual claim. Candidate 8 remains immutable and
contributes no imported result credit.

**INFERRED:** Candidate 9 closes the observed candidate-8 Maven working-directory
failure mechanism at the offline contract level. A live result has not been
established.

The next permissible gate is the workspace principal's exact candidate-9
accept/freeze decision.
