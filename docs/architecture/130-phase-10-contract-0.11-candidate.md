# Phase 10 contract 0.11 candidate

## Outcome

The consolidated `tekroo.kernel.contracts/0.11.0` candidate is structurally
valid, its complete reference corpus passes, its generation is reproducible,
and predecessor `0.10.0` remains byte-for-byte unchanged.

The candidate was subsequently accepted and frozen at the manifest identity
recorded below.

## Contract additions

The additive successor defines:

- versioned workflow definitions and durable workflow instances;
- message-to-work proposals and deterministic admission results;
- commands and events for workflow creation, admission, stage results, and
  checkpoints;
- node/DAG identity independent of roles, actors, message types, and threads;
- bounded repair with changed-condition evidence and preserved root budgets;
- dependency-ready parallel admission;
- checkpoint resume without replaying completed predecessors;
- configurable role reuse and non-software workflow loading; and
- validation evidence reuse plus the design-and-implementation elapsed-time
  ceiling.

These are contracts and executable reference cases, not role-specific runtime
code or task-specific prompting.

## Verification

- Contract identity: `tekroo.kernel.contracts/0.11.0`
- Manifest SHA-256: `85306c8edc703e85df502280642ef30161e16b8e82ff48f568c5a5c6d421f12d`
- Structural validation: `PASS`, 3,730 checks, zero failures
- Reference corpus: `PASS`, 313 cases, zero failures
- Generation: repeated twice with the identical manifest identity
- Predecessor manifest SHA-256:
  `2752b876d5a71bb1367a088b9f8cc0ad5df6343b0833906c49aae5a404b8db98`
- Predecessor tracked diff: none
- Predecessor untracked additions: none

The machine receipt is
`OUTPUT/phase-10/step-2/contract-candidate-receipt.json`. The raw validator
reports are retained beside it.

## Boundary

No Phase 10 production implementation was started by Step 2. Nothing was
committed, pushed, deployed, or applied to a live system.
