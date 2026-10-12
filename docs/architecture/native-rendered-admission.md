# Rendered request admission and combined recovery qualification

Historical runs retain their existing digest-bound byte policy. Do not change
that policy or rebind a failed historical invocation to a new configuration.
Rendered admission is an explicit policy for a qualified fresh run:

```json
{
  "context_tokens": 262144,
  "reserve_tokens": 8192,
  "request_bytes": 2097152,
  "result_bytes": 8192,
  "history_bytes": 65536,
  "page_bytes": 4096,
  "admission": "rendered_tokens",
  "repair_reserve_tokens": 32768
}
```

These are host-owned configuration bounds, not a token estimate. The provider's
context limit may lower the configured limit. Each exact serialized generation
request is first sent to `/v1/chat/token-count`; the provider renders it using its
ordinary chat template, tools, schema and continuation controls and tokenizes it
using the loaded model's tokenizer. Counting returns before GPU generation. A
nonresident model follows normal provider loading rules; use an already-loaded
candidate for qualification, without changing a historical run's server.
For each count, a
missing endpoint, failed count, changed model or mismatched request SHA fails
closed. The generation body must be identical to the counted body.

Admission requires both the independent transport cap and:

`prompt_tokens + output_tokens + reserve_tokens + repair_reserve_tokens <= min(configured_context, provider_context)`

The correction reserve is finite and mandatory in rendered mode. It provides
headroom, not a guarantee that arbitrarily growing drafts can always recover.
Admission never drops authority, decisions or raw evidence to force a fit.
Successful counts and their exact request SHA are retained in model diagnostics.
Provider `finish_details` are retained too. A `repetition_loop` cutoff is a
terminal invalid response regardless of `finish_reason`; it is never mistaken
for output-budget exhaustion or continued automatically.

Receipt presentations contain compact identity and bounded
`source_json_pointers`, not a synthetic source field named `read`. Shared access
instructions live once in the reader definition, not in each receipt. Historical
`access_instructions`/`read` fields remain decode-compatible but are not emitted
by new projections. No historical journal is rewritten.
`read_recorded_result` addresses the original result: source text is `/output`
when that field is present. A nonexistent `/read` is rejected with available
source keys; it is never silently aliased. Exact named records retain
PASS/SKIP/NOT_RUN distinctions. Summaries remain insufficient validation evidence.
Exact matching selects an array, not its containing object; both the tool schema
explain that distinction. Wrong-container errors retain the
selected pointer and bounded original object keys. Missing matches in a valid
array remain legitimate empty results, never errors or fabricated evidence.

Condensed reader responses retain two identities: outer `reference` addresses
the page-response journal entry; `page.source` addresses the underlying result
and exact JSON-pointer/record selection. `page` also records the byte range,
completion flag and exact decoded-page digest. To continue source paging, use
`page.source` with `page.next_byte_offset`, not the outer page receipt plus a
guessed pointer. Reader responses retain record-match fields so compaction does
not lose the selected named record. Legacy pages without those fields are never
given an invented filter.

Duplicate presentation is indexed by source identity, selection, byte range and
content digest. Older identical pages may link to a later exact page still in
the visible history; calls and raw outputs stay in the journal. Changed sources,
filters, ranges or bytes are distinct. If budgeting replaces the later content,
the in-context link is removed; durable source/receipt references remain. This
reduces presentation only: no tool is silently skipped, result accepted, pointer
aliased, context authority removed, or invocation continued after a true limit.
The configured history/result/page/transport/token reserves are unchanged.

Generation schemas forbid authored `validates` arrays only when **all** admitted
purpose routes forbid them. Mixed policies retain per-task semantic checks.
The original signed schema is unchanged. Graph rejection names `depends_on` or
`validates` separately, and the semantic validator remains independent.

## Qualification gates

The deterministic `TestCombinedLargeDraftLinkRejectionAndPointerCorrectionRetainsContext`
seeds a large rejected draft, retains its exact original journal, exercises a
bad pointer then exact source/named-record access, and completes in the same
bounded invocation. Its token count is an explicit mock fixture, not evidence
that a real tokenizer counted those tokens.

The opt-in `TestQualifyNativeCombinedReceiptRecovery` uses each configured signed
role with the candidate rendered policy. It deliberately seeds a synthetic
large draft with forbidden links and a nonexistent pointer, then requires the
real model to correct both, read exact evidence, preserve the prior decision,
and submit without promoting SKIP/NOT_RUN. Full journals and exact HTTP bodies
are retained. The existing receipt-only qualification remains separate.
The live draft padding uses distinct synthetic records and remains larger than
the historical 208616-byte failed request. Its optional padding is retained
input, not required output. Exact statuses, source access, preservation and
correction checks remain mandatory; a token-limit rejection or a provider loop
is a qualification failure, not a skipped check or a passing correction.
The corrected contract also requires the exact marker from the original source
page. It is not exposed as a schema enum, summary or task-answer hint; the seeded
draft contains a wrong marker. This ties the required source read to an answer
that actually depends on that read. Both the actual read receipt and exact value
remain required, and no extra tool errors or corrections are allowed.

`TestSustainedPagingPreservesHistoryUnderUnchangedBudget` covers a synthetic
113-observation mix with overlapping pages and immutable decisions, checkpoint
idempotency, unchanged raw journal and exact PASS/SKIP/NOT_RUN lookup. Separate
regressions cover page/source pointer rejection and correction, named-selection
provenance, legacy receipt identities, and removal of dangling delivery links.
Existing true-overflow, malformed/unauthorized access and frozen-ancestor tests
remain mandatory.

The opt-in `TestQualifyNativeSustainedPagingRecovery` seeds 113 successful
synthetic observations, an invalid draft and an invalid page/source pointer in a
single finite invocation. The real signed role must then read a source marker
outside the seeded page ranges, preserve its original decision, correct the
draft, and read all three exact named statuses. The large retained draft keeps
the rendered-admission challenge. Seeding is not live model behavior or evidence
that tests actually executed. Its original journal prefix and both error receipts
are preserved, and qualification does not dispatch or alter a historical run.

Run these qualification tests individually with build tags
`mongo_integration,native_qualification`, an explicit read-only
`TEKROO_NATIVE_RECEIPT_CONFIG` and a **different new**
`TEKROO_NATIVE_RECEIPT_OUTPUT` directory for each scenario. They do not modify
production configuration, role locks, feature state or historical journals.
An unset configuration produces SKIP, **not qualification PASS**.
The seven-role sustained batch needs an explicit outer Go test timeout (for
example `-timeout=80m`); Go's default aggregate ten-minute limit is not the
per-role qualification deadline. Each role retains its existing ten-minute
context/HTTP bound and unchanged finite model-turn/submission allowances.

The model-server gate is `tests/test_chat_token_count.py`, pointed explicitly at
an already-loaded candidate server/model. It compares counts with generation
usage for template, tools, schema, large text and continuation cases. It does
not start/stop a service or send load/unload requests; normal model resolution
still applies, and it does make qualification generation requests.
Both real-model gates must pass before enabling this policy for a fresh run.

## Completed-submission history delivery

A successful structured submission terminates at `Finished`, not `ToolDone`.
Restoration closes its historical tool syntax with the existing exact object
`{accepted:true,executed_in_child:false,historical_result:<original arguments>}`.
That acknowledgement now has a derived reference to the **actual frozen
Finished sequence** and a digest of those exact acknowledgement bytes. It is
not a new tool execution, a fabricated journal entry, or feature acceptance.

Replay, restoration and `read_recorded_result` use one observation decoder.
Finished-backed reads require an authenticated frozen ancestor, its exact
terminal sequence and unchanged journal digest, and a valid replayed final
submission/result chain. The current invocation, arbitrary terminal entries,
failed/cancelled/timed-out attempts and unrecorded effects cannot masquerade as
successful completion. Exact `/historical_result` selections and byte pages
use the existing pointer, hash and paging checks; terminal pages explicitly
identify `source_entry_kind=finished`.

This repair is shared by all native roles. Existing result/history/page,
transport, token and correction limits are unchanged. Only duplicate tool
presentation is bounded; original assistant text, arguments, authority and raw
journals are retained. No historical source journal or stored checkpoint is
rewritten. For older mixed-reference checkpoints, the runtime reconstructs the
frozen source on both first entry and restart. Compatibility permits adding
missing derived receipt metadata only; existing metadata, source identity,
message content and call identity must remain identical.

`TestCompletedSubmissionReceiptHandbackBoundaries` covers the unchanged result
limit minus one, equal, plus one, and the 17992-byte failed-boundary fixture.
Additional tests cover legacy mixed checkpoints, same-invocation pointer
failure/correction across restart, nested frozen ancestry, Unicode exact pages,
unauthorized/hash/pointer/source rejection and truthful closure of unrecorded
effects from unsuccessful attempts. Unbacked oversized results still fail.

`TestArchitectPMDecisionReturnWithLargeFinishedReceipt` exercises real signed
handler validation, PM `needs_decision` disposition, workflow revision
selection, structured feedback and native same-owner history binding, using
explicitly scripted model fixtures. The opt-in
`TestQualifyNativeArchitectPMCompletedDecisionReturn` instead runs the real
configured PM and returning architect against that synthetic completed parent.
`TestQualifyNativeCompletedSubmissionHandback` checks each currently qualified
signed role with a synthetic completed parent, a deliberately rejected pointer,
and live correction within the same finite child invocation. Exact original
decision/marker reads, unchanged parent/error history and completed-child
restart are mandatory. Neither gate dispatches a feature, modifies a historical
run, or qualifies an unqualified operator profile.

Run each live gate separately with `mongo_integration,native_qualification`, an
explicit read-only `TEKROO_NATIVE_TERMINAL_CONFIG`, and a different new
`TEKROO_NATIVE_TERMINAL_OUTPUT` directory. Teams must be stopped and the existing
model idle before entry. HTTP bodies and invocation-bound raw journals are
retained; unset configuration is SKIP, never qualification PASS. The seven-role
gate needs an outer test timeout exceeding its per-role ten-minute bounds.

## Architect-to-PM dependency boundary

An accepted architectural task may omit `depends_on`: its signed schema makes
the field optional. The native finalization binder treats absence as an empty
prerequisite set when applying a PM addition or empty confirmation. The latter
serializes as `[]`, never `null`. Present source values retain their existing
typed decoder behavior; signed source-schema validation remains authoritative.
The PM delta's own `depends_on` array remains
required; missing/null arrays, duplicate/self/out-of-range edges and invalid
handoff paths are not repaired or accepted. The source output and digest are
never rewritten. Existing prerequisites and immutable technical content remain
protected by the same production admission predicate.

`TestArchitectToPMDependencyFailureAndCorrection` carries an accepted three-task
architect output with omitted dependencies on two tasks and an existing edge
on the third into the real native PM binder. It deliberately rejects a self
edge, then a handoff missing its prerequisite, and accepts a valid correction
within the original three-submission invocation. It asserts retained drafts
and errors, preserved technical content and original edges, and finite budgets.
Separate negative controls retain rejection of changed technical work, removed
accepted edges, incorrect source identity, cycles and incomplete handoffs.

The opt-in `TestQualifyNativeArchitectToPMDependencyRecovery` runs the configured
signed architect and PM on a synthetic provider/consumer fixture, not a historical
feature. The architect must actually return the omitted-field source case. Two
deliberately invalid PM drafts are then seeded through the real runner and local
validators; the real PM must correct them in that same invocation using the
remaining submission allowance. Journals and exact HTTP bodies are retained;
no role qualifications, configuration, role locks or historical runs are changed.

Run it with build tags `mongo_integration,native_qualification`, an explicit
read-only `TEKROO_NATIVE_DEPENDENCY_CONFIG`, and a new absolute
`TEKROO_NATIVE_DEPENDENCY_OUTPUT` directory. Teams must remain stopped and the
existing model server must be idle before starting. Missing authorization paths
produce SKIP, not PASS. This is a scoped boundary qualification, not a claim
that a feature was implemented, tested or accepted.
