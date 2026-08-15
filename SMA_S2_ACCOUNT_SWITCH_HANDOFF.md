# SMA-S2 account-switch handoff

Date: 2026-08-14  
Project: `/Users/paul/work/tekroo-ai/teams`  
Current gate: `SMA-S2 — OpenHands/SMA boundary qualification`  
Current disposition: `NO-GO_FOR_MEASURED_EXECUTION`

## Purpose

This document is the authoritative operational handoff for continuing SMA-S2
after switching accounts. It is intended to be sufficient without the prior
chat history.

The immediate next work is **not** another candidate-7 run. Candidate 7 and its
execution identity are accepted/frozen, its one dress-rehearsal authority is
consumed, and its rehearsal ended `INCONCLUSIVE` because of a verified harness
workspace-isolation defect. The next permissible technical work requires a
separately authorized candidate-8 successor.

## Executive state

**OBSERVED:** Upstream SMA-S1 earned and retained `SMA_SEMANTICS_QUALIFIED`.
Its accepted scientific adjudication identity is:

`0e5ace0acbe6e2441755ae656de36096e38b2364d27fb45ee844f2422abf2f7d`

**OBSERVED:** SMA-S2 has **not** earned `OPENHANDS_SMA_BOUNDARY_QUALIFIED`.

**OBSERVED:** Candidate 7 is accepted/frozen, as is its execution identity.
The single authorized 34-operation, zero-credit dress rehearsal ended
`INCONCLUSIVE` after 10 passing operations and one harness failure during the
case-2 corpus seed.

**OBSERVED:** Candidate-7 cleanup passed. No measured case or repetition was
executed, no measured credit was awarded, and no claim was earned.

**INFERRED:** Candidate 8 should correct workspace capture isolation only. The
accepted scientific cases, predicates, thresholds, fault schedule, product
identity, deterministic model stub, and 34-operation dress plan should remain
unchanged.

## Absolute authority boundaries

At handoff time:

- candidate 7 may not be edited in place;
- the candidate-7 execution identity may not be edited in place;
- the consumed candidate-7 dress rehearsal may not be rerun;
- candidate-8 remediation is **not yet authorized**;
- no live dress rehearsal is authorized;
- no measured SMA-S2 execution is authorized;
- no real-model call is authorized;
- no service restart is authorized;
- no production or historical data access is authorized; and
- `OPENHANDS_SMA_BOUNDARY_QUALIFIED` has not been earned.

Do not infer a new authority from old candidate-6 or candidate-7 statements.
Every future live attempt must have a new candidate-specific, identity-bound,
single-use authorization.

## Frozen candidate-7 package

### Accepted preregistration

- Path:
  `investigations/sma-q1/layered/sma-s2-preregistration-candidate-7.json`
- SHA-256:
  `66530bb10fe7e46f23e8f3f550c83a89203905cc844dba8732d8794dba9575d3`
- Acceptance path:
  `investigations/sma-q1/layered/sma-s2-preregistration-candidate-7-acceptance.json`
- Acceptance SHA-256:
  `c93c8892d58995c86d4ed11cba340192037fc32725db82a2ad66b9e5783b3e0f`

Candidate 7 preserves:

- 20 scientific cases;
- 103 planned measured repetitions;
- 59 predicates;
- 10 required evidence classes;
- the accepted thresholds and deterministic fault schedule;
- the frozen synthetic corpus and oracle matrix; and
- the future claim `OPENHANDS_SMA_BOUNDARY_QUALIFIED` only on a valid measured
  pass.

Candidate 7 added:

- exact success/fault terminal-event requirements;
- two-consecutive-fetch terminal-event stability;
- a candidate-bound deterministic corpus-source constructor;
- sanitized harness-boundary evidence; and
- a mandatory 34-operation, zero-credit live dress rehearsal before any
  measured authority can open.

### Accepted execution identity

- Path:
  `investigations/sma-q1/layered/sma-s2-candidate-7-execution-identity.json`
- SHA-256:
  `40235ba0eda978c497dc17740aab09ebeb8fe3852e961b0f4d522737f3c38f22`
- Acceptance path:
  `investigations/sma-q1/layered/sma-s2-candidate-7-execution-identity-acceptance.json`
- Acceptance SHA-256:
  `cf520fe891c218bfac60500a2625c399211be7be36c64b0c483332fb57e83055`

Bound endpoints:

- deterministic stub: `127.0.0.1:19127`;
- SMA bridge: `127.0.0.1:8130`.

Bound product identity:

- SMA commit:
  `5d58be508c74ae8577d2fd31e3346be23a912ab3`;
- SMA tree:
  `7222410443a6041496a5c313b2549f16eb937458`;
- SMA JAR SHA-256:
  `c0ddb963798d45603c2e2685744892c40d3e841817f9c966ab7fad34f7e48765`;
- OpenHands commit:
  `a338ba9b6cbb529886b755a335bae3dee0004700`;
- OpenHands tree:
  `9e72f1b4ee0b857025d9f170600b9dce539b169a`;
- agent-server executable SHA-256:
  `9f1a91d9a58d13a27bc0e729eac36553bb9c977797c21f5483de3340cfa682bf`;
- Agent Canvas executable SHA-256:
  `133e26fb65434ddac2eee471bfc56cb8144c92452241aacd627a8cbcfde277e6`.

## Candidate-7 dress-rehearsal evidence

### Authorization and preflight

- Single-use authorization:
  `investigations/sma-q1/layered/sma-s2-candidate-7-dress-rehearsal-single-use-authorization.json`
- Authorization SHA-256:
  `c768d5c242e69f6603f2df66703cd2bad852b3905b4aeb3b29409b174f483017`
- Authorization-consumption record:
  `investigations/sma-q1/layered/sma-s2-candidate-7-dress-rehearsal-single-use-authorization-consumption.json`
- Consumption SHA-256:
  `7401dc5b5eb7393bcc184b2297bc0ab29ca1b9bf8487272d03305667d2ffb941`
- Live preflight:
  `OUTPUT/phase-3/sma-s2-candidate-7-dress-rehearsal-live-preflight.json`
- Live-preflight SHA-256:
  `6b13cf050b8a6674233942ed1b09129feaa365dd546bc6b22450bb3eb819985b`

**OBSERVED:** The immediate live preflight passed:

- Agent Canvas health: HTTP 200;
- agent-server health: HTTP 200;
- existing conversation count: 8;
- status counts: 6 finished, 1 paused, 1 error;
- unrelated active conversation count: 0;
- Tekroo Trader remained paused;
- stub port 19127 was available;
- bridge port 8130 was available;
- the candidate service was absent; and
- candidate MongoDB, Qdrant, workspace, dress-output, and measured-output
  namespaces were absent.

### Attempt receipt

- Receipt:
  `OUTPUT/phase-3/sma-s2-candidate-7-dress-rehearsal-attempt-1/dress-rehearsal-receipt.json`
- Receipt SHA-256:
  `82b5db3ec445cd9b22be484f48f26a03758f8f8587e4dc86c93ae588e0702dcf`
- Raw journal:
  `OUTPUT/phase-3/sma-s2-candidate-7-dress-rehearsal-attempt-1/raw-evidence.jsonl`
- Raw-journal SHA-256:
  `7286a8b133e94b545f02eec3ba9d406b48b783c2262f170dd84b9e10b7316185`
- Status: `INCONCLUSIVE`
- Completed dress operations: 10 of 34;
- Measured cases/repetitions: 0/0;
- Measured credit: 0;
- Claim: `null`;
- Cleanup: `PASS`.

**OBSERVED:** All ten repetitions of
`SMA-S2-001-FIRST-PROMPT-EMPTY` passed, including the exact agent terminal
event and all case-1 predicates.

**OBSERVED:** The attempt stopped during corpus seeding for
`SMA-S2-002-SAME-PARTITION-DELIVERY`, repetition 1. The durable error class is
`HARNESS` / `RuntimeError`. Its message digest is:

`42aceb4995e5adee6ccccb7f4abf223522506f5f21ad06b88d909d0c57241844`

## Verified root cause

**COMPUTED:** The failure digest exactly equals SHA-256 of the executable driver
exception:

`memory count exceeded expected 4: 10`

The relevant executable sequence is:

1. `dress_rehearsal_plan()` selects all ten case-1 repetitions first.
2. `case_first_prompt_empty()` writes those prompts into the candidate alpha
   workspace population.
3. Case 2 calls `ensure_seeded()`.
4. `seed_corpus()` creates four candidate-bound source events, then starts SMA
   with capture enabled and alpha/beta workspace allowlisting.
5. `wait_memory_count(4)` rejects any count greater than four.
6. It observed ten and raised the exact retained exception.

**INFERRED:** The case-1 event population contaminated the later exact-four
corpus seed because case 1 and corpus capture shared the allowlisted candidate
workspace population. The failed harness retained the count and message digest,
but not the ten memory documents, so do not claim the exact ten source event
identities.

This is a harness workspace-isolation/order defect. It is not evidence that an
SMA semantic predicate failed.

The authoritative adjudication is:

- Path:
  `OUTPUT/phase-3/sma-s2-candidate-7-dress-rehearsal-adjudication.json`
- SHA-256:
  `3f499ec0bd3f60d036d2c274444b0da94a6ae029de1f64572f9f6ac6f3ebb6da`
- Narrative:
  `docs/architecture/074-sma-s2-candidate-7-dress-rehearsal-inconclusive.md`

## Post-attempt cleanup state

**OBSERVED:** The attempt cleanup deleted all 14 candidate-created
conversations; each delete returned HTTP 200 and each post-delete read returned
HTTP 404.

**OBSERVED:** Cleanup proved:

- candidate service absent;
- bridge listener absent;
- deterministic stub exited;
- candidate MongoDB database absent;
- candidate semantic Qdrant collection absent;
- candidate episodic Qdrant collection absent;
- candidate workspace root absent; and
- both global cleanup oracles `E-009` and `E-010` true.

**OBSERVED:** The independent post-cleanup audit found:

- original conversation count: 8;
- active conversations: 0;
- statuses: 6 finished, 1 paused, 1 error;
- Tekroo Trader status: paused;
- Tekroo Trader created timestamp:
  `2026-08-09T19:52:16.454058Z`;
- Tekroo Trader updated timestamp:
  `2026-08-13T17:38:02.080929Z`;
- ports 19127 and 8130 available; and
- all candidate-7 service/data/workspace resources absent.

Mutable live state must still be rechecked before any future live attempt.

## Recommended candidate-8 correction

The smallest defensible successor is a workspace-isolation correction:

1. Create candidate 8 without modifying any candidate-7 artifact.
2. Add a dedicated empty workspace/partition for all case-1 repetitions, for
   example `<candidate-8-root>/actor-empty`.
3. Keep that empty workspace outside the SMA capture allowlist.
4. Keep the alpha and beta workspaces as the exclusive allowlisted population
   for the four-source corpus and later capture/retrieval cases.
5. Route `case_first_prompt_empty()` to the dedicated empty workspace.
6. Bind the empty workspace identity into candidate-8 configuration, execution
   identity, evidence, and cleanup assertions.
7. Retain the empty-workspace conversation receipts until normal cleanup; do
   not hide the defect by deleting evidence before corpus seeding.
8. Add offline controls proving:
   - case 1 uses the dedicated empty workspace;
   - the empty workspace is not in the capture allowlist;
   - alpha and beta remain in the allowlist;
   - the synthetic corpus still has exactly four source memories;
   - all three workspaces are under the candidate-owned cleanup root;
   - a deliberate shared-workspace mutation is rejected;
   - optimized-Python and default-deny behavior remain intact.
9. Preserve the 20 cases, 103 measured repetitions, 59 predicates, 10 evidence
   requirements, thresholds, fault schedule, corpus, oracle matrix,
   deterministic stub behavior, claim, and product identity.
10. Preserve the 34-operation, zero-credit rehearsal. Do not authorize measured
    execution unless a successor rehearsal passes completely and cleanup is
    `PASS`.

Do **not** solve this by pre-seeding the corpus before case 1 unless the
scientific contract is explicitly revised. Case 1 is defined as a new
conversation with an empty memory partition; pre-seeding alpha would weaken
that condition even if retrieval were temporarily stopped.

## Required gate sequence from here

1. Principal authorizes one bounded candidate-8 workspace-isolation
   remediation and offline qualification only.
2. Coordinator creates the immutable candidate-8 configuration, driver,
   offline harness, preregistration, and review package.
3. Principal accepts/freezes candidate 8 by exact preregistration SHA-256.
4. Principal separately authorizes candidate-8 execution-identity preparation.
5. Coordinator prepares and offline-validates the exact candidate-8 identity.
6. Principal accepts/freezes that identity.
7. Principal separately authorizes one single-use 34-operation, zero-credit
   candidate-8 live dress rehearsal.
8. Coordinator performs immediate live-state, active-work, endpoint,
   namespace, and product-identity checks, then runs at most one attempt.
9. Only if the dress receipt is `PASS`, cleanup is `PASS`, claim is null, and
   measured credit remains zero may the coordinator request a later single-use
   measured authorization.

No step may be collapsed into an earlier authorization by implication.

## Suggested next authorization

If the principal agrees with the recommended correction, the exact next
statement can be:

> Authorize one bounded SMA-S2 candidate-8 workspace-isolation remediation:
> route all first-prompt-empty repetitions through a dedicated empty workspace
> excluded from the alpha/beta corpus capture allowlist; bind that workspace
> into configuration, evidence, identity, and cleanup contracts; add positive
> and mutated offline isolation controls; preserve all accepted SMA-S2 science
> and product identities; and publish candidate 8 for review. This does not
> authorize candidate-8 acceptance, execution-identity creation, live dress
> rehearsal, measured execution, service restart, or real-model calls.

## Restart checklist for the next account

1. Read this file completely.
2. Read `AGENTS.md` and obey its verification and one-command-per-shell-call
   rules.
3. Verify the frozen candidate-7 preregistration and identity SHA-256 values.
4. Verify the dress receipt, raw journal, consumption, and adjudication hashes.
5. Confirm that no new principal authorization was issued after this handoff.
6. Do not run candidate 7 again.
7. Do not edit any candidate-7 package, identity, authority, receipt, or
   adjudication.
8. Do not clean or reset the repository. It contains a large body of existing
   untracked Phase-3 evidence and project artifacts that belong to this work.
9. If candidate 8 is authorized, use new filenames, namespaces, ports/identity,
   receipts, and output roots; preserve predecessor lineage by hash.
10. Report every substantive statement as `OBSERVED`, `COMPUTED`, or
    `INFERRED`, and attach raw receipts for quantitative or mechanistic claims.

## Repository handling notes

The Teams worktree contains extensive pre-existing untracked and modified
Phase-3 work. Do not use `git clean`, `git reset --hard`, or checkout-based
discard operations. Candidate-7 work in this handoff has not been committed or
pushed as part of the final handoff turn. Commit/push authority must be obtained
or confirmed separately.

The following accepted artifacts are immutable even though Git may currently
show them as untracked:

- candidate-7 preregistration and acceptance;
- candidate-7 configuration, driver, and offline harness;
- candidate-7 execution identity and acceptance;
- candidate-7 dress authorization and consumption;
- candidate-7 live preflight, dress receipt, raw journal, adjudication, and
  architecture narratives.

## Compact account-switch prompt

Use this prompt in the replacement account after attaching or pointing it to
this workspace:

> Continue SMA-S2 coordination from
> `/Users/paul/work/tekroo-ai/teams/SMA_S2_ACCOUNT_SWITCH_HANDOFF.md`. Read the
> handoff and `AGENTS.md` completely before acting. Verify the frozen identities
> and evidence hashes. Candidate 7 is immutable, its sole dress authority is
> consumed, and measured execution is unauthorized. Do not rerun candidate 7.
> Await or apply only the principal's next explicit authorization for the
> bounded candidate-8 workspace-isolation successor described in the handoff.
