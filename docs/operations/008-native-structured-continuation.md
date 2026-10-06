# Native structured-response continuation

Teams can complete a JSON-schema response that exceeds one model output chunk.
This changes transport behavior, not the assigned work or its acceptance criteria.

- On `finish_reason: length`, Teams journals a `model_partial` checkpoint with
  the exact assistant prefix, original request context, and model/schema binding.
  It does not decode or execute any tool from the incomplete document.
- The next request keeps the schema and original messages, appends the exact
  prefix as the final assistant message, and sets `continue_final_message: true`.
  Thinking and API-native tools are disabled on this constrained response path.
  mlx-serve primes its grammar from the prefix and returns only newly generated
  suffix bytes. Teams concatenates those bytes without trimming or reformatting.
- Further caps produce further checkpoints. A process restart resumes the
  recorded context without another condensation or repeated completed tool calls.
  Every request rechecks invocation authority. Completed responses still undergo
  the same JSON Schema validation before any tool is dispatched.
- Empty capped suffixes, changed bindings, malformed responses, size overflow,
  cancellation, and HTTP failures remain explicit errors. The existing invocation
  deadline bounds continuation; partial chunks do not consume working-turn limits.
  One assembled document is bounded at 1 MiB; journal payloads at 8 MiB, below
  MongoDB's document limit. These are document bounds, not model-context limits.

Server contract: `stop` means grammar acceptance; `length` means an incomplete
capped response. An already-complete prefix returns an empty suffix and zero
completion tokens. Completion-token usage counts new tokens only; prefix tokens
are input. Streaming deltas also contain only new suffix bytes.

Qualification:

```
go test ./agentruntime ./adapters/nativeagent ./adapters/mongo
```

The opt-in `TestLiveStructuredContinuation` requires
`TEKROO_CONTINUATION_BASE_URL` and `TEKROO_CONTINUATION_MODEL`; it forces several
caps against a schema-fixed disposable receipt without running organizational work.
Server streaming/non-streaming qualification lives in mlx-serve's
`tests/test_json_continuation.py`.

Deployment must pair this Teams client with mlx-serve's grammar-priming support.
Old failures that discarded their partial text cannot recover that lost text;
recover only their blocked task from the existing durable workspace/checkpoint.
