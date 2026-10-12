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

Receipt presentations contain `access_instructions` and bounded
`source_json_pointers`, not a synthetic source field named `read`.
`read_recorded_result` addresses the original result: source text is `/output`
when that field is present. A nonexistent `/read` is rejected with available
source keys; it is never silently aliased. Exact named records retain
PASS/SKIP/NOT_RUN distinctions. Summaries remain insufficient validation evidence.
Exact matching selects an array, not its containing object; both the tool schema
and receipt view explain that distinction. Wrong-container errors retain the
selected pointer and bounded original object keys. Missing matches in a valid
array remain legitimate empty results, never errors or fabricated evidence.

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

Run these qualification tests individually with build tags
`mongo_integration,native_qualification`, an explicit read-only
`TEKROO_NATIVE_RECEIPT_CONFIG` and a **different new**
`TEKROO_NATIVE_RECEIPT_OUTPUT` directory for each scenario. They do not modify
production configuration, role locks, feature state or historical journals.
An unset configuration produces SKIP, **not qualification PASS**.

The model-server gate is `tests/test_chat_token_count.py`, pointed explicitly at
an already-loaded candidate server/model. It compares counts with generation
usage for template, tools, schema, large text and continuation cases. It does
not start/stop a service or send load/unload requests; normal model resolution
still applies, and it does make qualification generation requests.
Both real-model gates must pass before enabling this policy for a fresh run.

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
