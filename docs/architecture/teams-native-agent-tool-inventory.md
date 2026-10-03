# Teams-native agent tool inventory

This is the working inventory for removing OpenHands from Teams execution. It
tracks the **model-facing tools**, separately from the agent runner and its
MongoDB turn journal. It is not a claim that OpenHands can already be removed.

| Tool/function | Teams implementation | Live native-runner exposure | Remaining work |
| --- | --- | --- | --- |
| `read_file`, `list_files`, `find_files`, `search_file_contents` | `agenttools` host and read-only gateway | Available to the new read-only turn adapter | `read_file` now returns a full-file SHA-256 for guarded writes; qualify all tools in the assembled workflow. |
| `git_status`, `git_diff`, `git_log`, `git_show`, `git_check_ignore` | `agenttools` host and read-only gateway | Available to the new read-only turn adapter | Qualify all tools in the assembled workflow. |
| `write_file` | Host implementation plus MongoDB effect-intent ledger | Available through explicit effectful native session and opt-in native lifecycle | Coder exercised it in a disposable real-model canary; qualify the assembled workflow before production selection. |
| `git_stage_files`, `git_commit` | Host implementations plus effect-intent and read-only Git reconciliation | Available only through explicit effectful profile allowlist | Coder committed one disposable candidate in the real-model canary; qualify lost acknowledgment, cancellation, and replay in the assembled workflow. |
| `run_go_tests` | Committed-HEAD snapshot, macOS Seatbelt runner, and durable test-effect receipt implemented | Opt-in native session only; **not deployed** | Tester exercised it in a disposable real-model canary. Local tests cover external-file and network denial, arbitrary executable denial, timeout, a periodic 2 GiB workspace cap, and receipt replay. CGO is disabled. The tester role bundle already grants `test.execute`; native profile allowlisting and assembled workflow qualification remain before production exposure. |
| Editing by patch or targeted replacement | **Not implemented as a distinct native tool** | No | Decide whether SHA-guarded `write_file` suffices for large files; add only if real agent use shows a need. |
| Non-Go build, format, lint, and test operations | **Not implemented** | No | Inventory actual role/task requirements before adding narrow typed tools. |
| Other formerly used shell operations | **Not fully inventoried** | No | Classify actual OpenHands command history; add typed tools only for demonstrated workflows, not a disguised general shell. |

An opt-in read-only native session now binds the admitted brief digest, role,
model profile, and stable workspace authority before model execution and again
on every model/tool call. It also applies the profile's explicit read-tool
allowlist at both model exposure and tool dispatch. Handler-bound sessions
expose their exact result schema through `submit_result` and validate it before
completion. Native validation, review, and promotion sessions also expose a
structured `submit_result`; the adapter converts that object to the unchanged
downstream validation marker. Non-handler handoff and replan can use the same
tool only when the per-invocation configurator supplies an exact JSON Schema
bound to the authoritative task ID and source digest. A server-side resolver
now checks the task's exact stage description, role, and purpose against the
selected immutable workflow definition, then resolves its output-schema name
to a content-addressed document. The adapter checks the schema digest, rejects
external references, validates the returned object, and converts it to the
unchanged organizational marker. Handler-bound work already uses the admitted
role bundle's result schema. It is **not**
yet selected by the daemon. An opt-in `AgentExecutionBoundary` adapter now
adds a MongoDB lease, background execution, cancellation, and restart from the
turn journal; its effectful mode is separately allowlisted. Unit/race tests
cover duplicate starts, cancellation, digest drift, recorded-turn replay, and
an applied write with a lost receipt; MongoDB tests cover lease ownership,
journal persistence, and the effect ledger. An opt-in Go client now mirrors
the accepted local SMA prompt-context bridge, and the first retrieval result
is journaled so restart does not silently change the model's working context.
It still needs production configuration, assembled end-to-end qualification,
context reduction, configured schema documents and per-invocation resolver
wiring before a feature run can use it. A disposable real-model coder/tester
canary passed, but used synthetic invocation bindings rather than the
organizational coordinator and MongoDB stores. No automatic
turn-count limit is imposed when `MaxTurns` is zero; the invocation deadline
remains binding.

Mutation tools in an effectful native session require implementation/repair
purpose, `repository.edit` authority, the admitted effect-policy digest, and
an explicit tool allowlist. Validation sessions may instead be granted the
separate `test.execute` permission and `run_go_tests` allowlist. File writing, Git staging, and committing reserve a
MongoDB intent keyed by invocation and tool-call ID. A host-wide workspace
lock serializes each physical effect with its restart/cancellation inspection.
An ambiguous reservation or receipt stops the model turn; recovery checks the
file hash or Git state without reissuing a model turn. Cancellation reconciles
recorded effects before publishing a terminal outcome. The opt-in native
boundary now accepts the effectful session, but production still selects
OpenHands. Go tests are eligible only in an explicitly authorized opt-in
native session; the deployed tester role grants `test.execute`, but the
production execution path still selects OpenHands.
None of these execution records belongs to SMA's semantic-memory state. Any
future SMA intake should consume an explicit authorized projection, not read
the private turn collection directly.

Sources in this repository: `agenttools/tools.go`, `agenttools/gateway.go`,
`agenttools/turn_adapter.go`, `agentruntime/runtime.go`,
`adapters/nativeagent/session.go`, `adapters/nativeagent/boundary.go`,
`adapters/mongo/run_control.go`, `adapters/mongo/turn_journal.go`,
`agenttools/effects.go`, `agenttools/git_effects.go`, and
`adapters/mongo/tool_effects.go`.
