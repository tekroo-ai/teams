# Phase 3 Step 10 — deterministic-release contract sufficiency

## Result

`REVISION_REQUIRED`

The released `tekroo.kernel.contracts/0.4.0` package is sufficient to represent
the final story transition from completed to accepted, but it is not sufficient
to represent the deterministic release process that must qualify that
transition. Implementing release now would require inventing non-normative state
and wire semantics.

## Accepted requirement

The controlling Phase 1B handoff requires a deterministic release coordinator
that:

- persists an ordered release plan before external execution;
- binds exact repository, base, ordered PR/head, policy, idempotency, merge
  strategy, contract, and required-profile identities;
- qualifies one exact synthesized tree;
- records partial and unknown external outcomes explicitly;
- reconciles ambiguous results against authoritative provider state;
- bounds retries and serializes conflicting operations; and
- accepts the story only after author approval, every required merge is
  authoritatively verified, and the provider tree equals the qualified tree.

The principal's accepted release adjudication also retains explicit non-code
story handling and records reliable deterministic merging as a material v4
requirement.

## Observed 0.4.0 surface

The released aggregate-kind vocabulary is:

`completion-review`, `escalation`, `evidence`, `execution`, `story`, `system`,
and `task`.

There is no `release-plan` aggregate. The catalogue contains no release-plan
command or event. Its only organizational acceptance command is
`tekroo.command.story.request-acceptance`, whose payload contains:

- `lifecycle_epoch`;
- `acceptance_policy_revision`;
- `evidence_ids`; and
- optional `qualified_tree_digest`.

Those fields cannot encode or replay a frozen release plan, ordered required
merges, provider attempts, partial results, uncertainty, reconciliation, or a
terminal release decision. Evidence references can support such facts, but
cannot silently replace the missing authoritative domain record.

`INV-013-MERGE-TREE-QUALIFIED` states the correct high-level invariant, but its
only named fixture is the generic idempotency fixture. The corpus contains no
release transition model or scenarios for partial merge, already merged,
conflict, authentication failure, changed head/base, ambiguous timeout,
reconciliation, concurrent workers, provider unavailability, or non-code
stories.

## Minimum successor contract

Create immutable `tekroo.kernel.contracts/0.5.0` with wire version `1.4.0` and:

- aggregate kind `release-plan`;
- `release-plan.create` / `release-plan.created`;
- `release-plan.record-qualification` /
  `release-plan.qualification-recorded`;
- `release-plan.request-execution` /
  `release-plan.execution-requested`;
- `release-plan.record-result` / `release-plan.result-recorded`;
- `release-plan.record-reconciliation` /
  `release-plan.reconciliation-recorded`;
- `release-plan.finalize` / `release-plan.finalized`; and
- a revised story-acceptance payload that binds the exact release plan,
  revision, finalization event, and qualified tree for code stories, with an
  explicit finalized `NO_RELEASE_REQUIRED` path for non-code stories.

The normative model must cover the frozen plan key, exact qualification,
durable effect intent, per-PR attempts, `MERGED`, `ALREADY_MERGED`, `FAILED`,
and `UNKNOWN` outcomes, authoritative reconciliation, bounded retries,
single-active-worker fencing, partial joins, terminality, and acceptance gating.

## Evidence receipts

- `OUTPUT/adjudication/phase-1b-architecture-handoff.json`, SHA-256
  `137a1399a70cfc96cf36997e8c2154b1847cdde5ee8a50f7a39f7d06e3c83e20`;
- `PHASE-1B/008-final-architecture-handoff.md`, SHA-256
  `ec0aef90ceab404c6288a0279d47f49ee4c5af1c00577d55b8ffd25882b18fef`;
- the original Phase 1B kernel decision register at
  `/Users/paul/work/tekroo-ai/teams-v3/tekroo-v4-archaeology/OUTPUT/adjudication/kernel-decision-proposals.json`,
  SHA-256 `598acef9cca08f80258a8117174bd50eb5394b6c4055efae142ac17fb92e014e`;
- `CONTRACTS/tekroo.kernel.contracts/0.4.0/manifest.json`, SHA-256
  `5ff83483ce43ace2e06f2cc2f57dd552342553fc2389f3cc775b761c0c6d6d7c`;
- released catalogue SHA-256
  `aa1b080895dc885c7638b9d6eeda03c56e2384a43039fa316ab3e5957dd1e983`;
- released core schema SHA-256
  `a4a9f8172f23075d18e807e8f6d587914ec8632fd8c807a866594d8abd196ea2`;
- released payload schema SHA-256
  `7781a4f98c7600dc8cf55a5dd0f210c326a324b4ba678a7d7f090886db2476f1`;
  and
- released invariant register SHA-256
  `e5891fbba8fe1baae67129512212a260df6cb50dc401669e793bb0fcc0986237`.

## Gate recommendation

Do not implement deterministic release or organizational acceptance against
contract `0.4.0`. Obtain explicit principal approval for the `0.5.0` successor
contract, qualify that package independently, then implement the coordinator
and provider adapter as separate later gates.
