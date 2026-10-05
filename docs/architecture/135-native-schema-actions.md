# Native schema-constrained actions

Native profiles can select `response_mode: "json_schema_actions"`. This is
part of the model-profile digest; existing qualifications cannot silently be
reused for the changed transport. An omitted mode retains the existing
OpenAI-compatible tool-call transport.

The new mode sends each model request with `tool_choice: "none"`, no API
`tools`, and a formal Draft 2020-12 `response_format.json_schema`. Tools are
still executed by Teams under their existing authority and durable receipts.
Only the model-response transport changes.

The response chooses exactly one top-level field:

```json
{"calls":[{"read_file":{"path":"AGENTS.md"}}]}
```

or the final result, using the admitted handler's argument schema:

```json
{"submit_result":{"outcome":"completed","summary":"Done","evidence":[],"message_proposals":[],"work_product":{}}}
```

The final example illustrates transport shape, not a universally valid handler
result. Each handler still determines its required work-product fields. Argument
objects and arrays remain raw JSON values, never JSON documents inside strings.
Multiple ordinary calls, including the same tool twice, remain possible. A
final result cannot accompany ordinary calls. There is no readiness signal or
extra completion request: the agent makes the final choice in that request.

Each response is fully schema-validated before any call is dispatched. The
existing journal and effect-reconciliation routes remain in use; replay does
not repeat completed model requests or tool effects. Truncated responses,
API-native tool calls, unbound schema references, and invalid responses fail
explicitly. The five initial instruction layers remain pinned on every request.

The model-facing schema describes only fields the model supplies. When Teams
assembles an accepted design with a PM dependency delta, it replaces both root
and same-envelope conditional/composed work-product restrictions in that view.
The fully assembled result must then pass the unchanged signed handler schema.

mlx-serve 26.10.1 skips its response JSON Schema grammar when API tools are
available. With API tools absent it enables that grammar. Server support is a
subset of JSON Schema, so server masking never replaces full host validation;
this is not a claim that every provider enforces every schema keyword.

This option does not itself alter production profiles, deploy a daemon, or
establish production model qualification. The live fixture uses isolated
MongoDB and disposable workspaces with synthetic admission; its results must be
reported as such.

## Verification

The isolated greeting-feature run r17 reached `AWAITING_ACCEPTANCE` in 165.19
seconds. Its retained journals contain seven successful invocations (PO intake,
PM specification, architecture, PM plan finalization, implementation, independent
testing, and PO acceptance), each with one final submission and retry ordinal
zero. All submitted work products are objects and evidence fields are arrays;
no tool-error, failed, timed-out, or cancelled journal entries were recorded.
This fixture did not execute every configured role or prove general feature
quality.

Raw receipt: `OUTPUT/native-profile-qualification-20261005-r17/native-evidence.json`.
SHA-256: `849025e4aed7d0221fd89ae6f7055f0e22656be88ae9ff24c08de219037c6464`.
The full Go unit suite, runtime/native-agent race tests, and diff whitespace
check passed. Transport tests verify batched tools followed by a final response
without an additional readiness/correction request and no repeats on replay.
