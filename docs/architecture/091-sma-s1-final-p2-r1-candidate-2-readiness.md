# SMA-S1 final-P2 R1 candidate 2 readiness

Date: 2026-08-28
Status: **PASS — ACCEPTED / FROZEN**
Identity acceptance: **ACCEPTED / FROZEN**
Measured S1 execution: **NOT_RUN**

## Scope

This record implements R1 from the accepted post-P2 Step 15 reconciliation. It
prepares and offline-qualifies a successor S1 package against final SMA P2. It
does not accept or freeze the identity and creates no measured, OpenHands,
model, S2, M1, E1, deployment, or production authority.

## Bound identity

**OBSERVED:** the isolated SMA worktree is clean at commit
`fe9903cdf59497aabf12cbe7722e1397a1372068`, tree
`fc2ae06c6333e94f6db34d691e50cb8bb1e5aa36`.

**OBSERVED:** `mvn clean verify` passed 671 tests with zero failures, errors, or
skips. The shaded service artifact SHA-256 is
`1561d18739cc53e744a0014ce562c8a7b2adfc988494a984728d680e887fbb8b`;
the shaded sources artifact SHA-256 is
`a2a521819ae1fcc3164d235823408b987e1b73471bd2d861ec7fb2df9ff91184`.

**OBSERVED:** the accepted candidate-4 product-driver source remains unchanged
at SHA-256
`184fa7bc9bc468b55a9c94d134841161938b9572a7e359265620d84358ed7667`
and compiles against final P2. Its new compiled artifact SHA-256 is
`00ac8098562215b9c021b2512776457d6b123a731c650cc9224d5f8bcff67e8f`.

Candidate 2 identity:

- path:
  `investigations/sma-q1/layered/sma-s1-execution-identity-p2final-r1-candidate-2.json`;
- SHA-256:
  `2624b25095e99ca20d5d1da936e0276449ac533eca9251ab52828a6dcd972f59`;
- status: `CANDIDATE_NOT_ACCEPTED_NOT_AUTHORIZING_NOT_STARTED`.

The accepted scientific preregistration remains byte-identical at SHA-256
`e663f5b732c22248adc5fab2f536dbf02dd72337f7c8428f225937e72d6a7f8c`:
14 cases and 71 repetitions. No prior measured credit or authority is imported.

## Candidate 1 negative control

**OBSERVED:** candidate 1's lifecycle requests were rejected by the inherited
driver before `PREPARE`. Its proposed namespaces were disposable but did not
match the frozen driver's exact `sma_s1_(driver_fixture|measured)_…` and actor
partition grammars. Consequently:

- all lifecycle calls returned driver exit 2;
- zero handlers and zero scientific repetitions ran;
- MongoDB contained no candidate-1 database;
- no candidate-1 Qdrant collection existed; and
- the failed receipt and journal were retained unchanged.

This is a harness-configuration failure, not SMA evidence. Candidate 2 changes
only the new disposable identities so that they remain unique and satisfy the
already-frozen grammar. Product code, driver science, predicates, thresholds,
and corpus remain unchanged.

## Candidate 2 offline results

| Gate | Result |
|---|---|
| Clean final-P2 build | PASS — 671/671 |
| Accepted base-harness mechanics | PASS — 12/12 |
| P2-delta controls | PASS — 10/10 |
| Focused P2 fence/bridge tests | PASS — 32/32 |
| Product-driver handlers | PASS — 14/14 |
| Handler predicates | PASS — 32/32 |
| Lifecycle controls | PASS — 5/5 |
| Measured scientific repetitions | NOT_RUN — 0/71 |

**OBSERVED:** the P2-delta controls bind and check:

1. final-P2 commit, tree, clean worktree, shaded artifacts, and exact source
   blobs;
2. absence of `ProtectedRawDeletionRuntime` activation from the default
   `SmaServiceMain` entrypoint;
3. explicit permit-all compatibility constructors plus the three exact fence
   identity checks;
4. bridge `prepareSemantic` followed by `filterForDisclosure`, preserving the
   prepared audit, agent, and task references;
5. rejection of enabled P2-AF, a journal-fence mode, or a different entrypoint
   as a transferable identity;
6. alignment with the accepted P2-AF disabled-by-default receipt; and
7. the unchanged 14/71 S1 preregistration.

**OBSERVED:** the candidate-2 handler run used only:

- MongoDB database `sma_s1_driver_fixture_p2final_r1_candidate_2`;
- Qdrant collections
  `sma_s1_driver_fixture_semantic_p2final_r1_candidate_2` and
  `sma_s1_driver_fixture_episodic_p2final_r1_candidate_2`; and
- the two new fixture actor partitions bound in the candidate-2 environment.

Both cleanup calls and the final inventory passed. A separate post-run read
found the exact MongoDB database absent and both exact Qdrant collection reads
returned 404.

## Evidence package

The aggregate machine receipt is
`OUTPUT/phase-3/sma-s1-p2final-r1-candidate-2-offline-qualification.json`.
Its SHA-256 is
`cdea4d04eac8c6c9dc4682f441d2bd55405b24c24b72defd9bdbc1a0f0455af9`.
It binds the build receipt, identity, environment, scripts, compiled driver,
three PASS receipts, three append-only journals, candidate-1 negative control,
and cleanup observations. The read-only package validator is
`scripts/validate_sma_s1_p2final_r1_candidate_2.py`, SHA-256
`45813fc2810a6099c7315292aeadaf58456e86f5f249c8f88b489e779682ef3b`;
it passes 44/44 checks and records independent review as `NOT_RUN`.

## Adjudication

**COMPUTED:** candidate 2 executed zero measured S1 repetitions and consumed no
single-use authority.

**INFERRED:** candidate 2 is ready for an independent package review. Its
offline PASS makes a final-P2 S1 no-regression result plausible but does not
qualify it. Only an accepted/frozen identity followed by one separately
authorized 14-case/71-repetition measured run can produce a current
`SMA_SEMANTICS_QUALIFIED` claim.

## Independent review

**OBSERVED:** an independent, read-only review passed with no blocker. The
reviewer independently verified all bound hashes, final-P2 identity and clean
state, the unchanged 14-case/71-repetition plan, journals, namespace grammar,
candidate-1 zero-credit path, candidate-2 handler evidence, cleanup inventory,
and authority fences.

The independent review receipt is
`OUTPUT/phase-3/sma-s1-p2final-r1-candidate-2-independent-review.json`,
SHA-256
`1324a5220d74d259992563f9bd9154ecd4c9cba7814e917fa92f188b754c8f94`.
It content-addresses the unchanged candidate identity and offline qualification
receipt. The offline qualification receipt itself retains its pre-review
`independentReview: NOT_RUN` value because changing it after review would
change the reviewed subject.

The reviewer retained two nonblocking precision notes:

1. the separately observed Qdrant HTTP 404 has no distinct content-addressed
   HTTP receipt, while the bound final-inventory journal independently proves
   both collections absent; and
2. candidate 2 changes no product or scientific behavior, but its handler
   script also adds a `handlerCheckSHA256` evidence field in addition to the
   disposable-identity correction.

## Next gate

The principal accepted and froze the candidate-2 identity and offline
qualification. The acceptance receipt is
`investigations/sma-q1/layered/sma-s1-p2final-r1-candidate-2-acceptance.json`,
SHA-256
`dabc276f083b4fd9d23cbf19e3bf5e55a49150625041834f3edf1d6d91256a7f`.

The next gate is a separate principal decision on one single-use measured S1
execution. Do not begin the measured run, S2, M1, or E1 without that authority.
