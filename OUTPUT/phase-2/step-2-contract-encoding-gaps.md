# Step 2 frozen-contract encoding gaps

Status: **OBSERVED — blocks a full `core-hermetic` PASS**

The receipts below compare the principal-approved requirements in
`PHASE-2/001-kernel-contract-freeze.md` with the immutable contract package at
`CONTRACTS/tekroo.kernel.contracts/0.1.0/`. They do not authorize editing that
package.

## 1. Multi-aggregate and lifecycle/policy preconditions are not encodable

- Decision 7 requires a canonical vector of `(aggregate_ref,
  expected_revision_or_absence)` for commands touching multiple aggregates.
- Decision 8 requires the exact lifecycle epoch on every lifecycle command.
- `schemas/kernel-command.schema.json` has `additionalProperties: false` and its
  properties are limited to `actor_fqn`, `authority`, `causation`, `command_id`,
  `command_type`, `command_version`, `contract_manifest`, `correlation_id`,
  `evidence_refs`, `execution`, `expected_revision`, `idempotency_key`,
  `issued_at`, `payload`, and `target`.
- There is no related-aggregate precondition vector, expected lifecycle epoch,
  expected policy revision, or expected catalogue revision field.

The Go kernel now has an internal exact precondition vector and commit guard,
but a JSON command conforming to the frozen schema cannot supply it.

## 2. Bounded asynchronous review joins are not encodable

- Decision 8 requires bounded named validation branches and an
  order-independent join.
- `tekroo_command_completion_review_record_result_1_0_0` permits only
  `review_id`, `result`, `reasons`, and `evidence_ids`, with
  `additionalProperties: false`.
- It has no branch identity, branch-policy revision, required-branch set, join
  rule, deadline, or partial-result-policy field.

The implementation enforces one open review per semantic key and exact subject
epoch/revision fencing. It cannot truthfully claim the approved branch-join
contract is externally executable.

## 3. Deterministic successor sets are not encodable

- Decision 8 requires split and merge to produce explicit deterministic
  successor sets.
- `tekroo_command_work_create_successor_1_0_0` requires one `successor_id`, one
  `relation`, and one `reason`, with `additionalProperties: false`.
- No successor set, per-successor ownership/dependency rule, or merge input set
  can be supplied.

The implementation validates a single successor and preserves the terminal
predecessor; split/merge set conformance remains unproved.

## 4. Complete evidence registration is not encodable

- Decision 9 requires immutable-locator state, producer/version, source and
  ingestion timestamps, transport provenance, sensitivity, access partition,
  retention, redaction/deletion lineage, source evidence, and computation
  identity.
- `tekroo_command_evidence_register_1_0_0` permits only `evidence_kind`,
  `sha256`, `byte_length`, `media_type`, and `locator`, with
  `additionalProperties: false`.

The pure evidence registry now enforces the richer record, access, redaction,
deletion tombstone, assessment revision, and rebuild semantics. The frozen
evidence command cannot carry the complete record into that registry.

## Gate consequence

These are encoding mismatches in the immutable governing package, not missing
Go validation alone. The Step 2 report must remain `INCONCLUSIVE` until the
principal either:

1. authorizes a new compatible contract-package version that encodes the
   approved requirements; or
2. explicitly narrows the approved requirements and records the adjudication.
