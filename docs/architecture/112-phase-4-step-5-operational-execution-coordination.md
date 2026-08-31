# Phase 4 Step 5 — operational execution coordination

Date: 2026-08-31
Status: implementation candidate complete
Starting commit: `6a4a262461e967498e876090079c05a0cbb8ce1d`
Starting tree: `86061116c9eb58e5c55d231d73d121fb174be34f`
Contract manifest: `c7eb4baae3a8312e44f9946fabde5a9cddb1937c7a41eb02d9b014ef21027ad1`

## Outcome

Phase 4 Step 5 implements a separate operational work-invocation coordinator.
The pre-existing actor-process execution coordinator remains responsible for
actor lifecycle and fencing; it was not repurposed as a model dispatcher.

Only an outbox intent whose exact kind is `WORK_INVOCATION_AUTHORIZED` can enter
this execution path. The Mongo backlog query and change stream are both filtered
to that kind. The authorization event resolves the exact durable work invocation;
generic task events, messages, model output, tool output, OpenHands status, and
SMA content cannot start another invocation.

## Exact execution sequence

1. Resolve the authorization event to its exact invocation.
2. Revalidate authoritative Teams state: task revision/lifecycle/scope and
   runnable condition; work profile; qualified assignment; actor and current
   execution fence; model, runtime, tool, and effect policy identities; budget
   debit; deadlines; workspace; task definition; and required evidence.
3. Atomically claim the invocation with current task and budget preconditions.
4. Build a bounded canonical execution brief from the authoritative task-created
   event and Teams records. The brief directs the agent to return evidence and
   proposals to Teams only and forbids addressing or invoking another agent.
5. Resolve the exact immutable workspace/worktree and qualified execution
   profile. Profiles containing OpenHands task, delegate, or workflow tools are
   rejected; `client_tools`, `agent_definitions`, and parent-conversation fields
   are never supplied.
6. Create or reconcile the deterministic OpenHands conversation ID equal to the
   invocation ID. The exact brief digest is bound into conversation metadata and
   key/value tags. An existing conversation must match the workspace,
   invocation, and request digest before it is used.
7. Submit the exact brief at most once. Any ambiguous response is reconciled
   against the complete paginated event stream before another submission is
   possible.
8. Persist the started checkpoint before treating the external execution as
   durable. Restart from `CLAIMED` reconciles; restart from `STARTED` inspects or
   cancels. Neither state blindly starts again.
9. On completion, retain the full relevant event journal, a separate tool and
   artifact journal, the exact provider output, and a structured provider
   receipt in a content-addressed evidence store before recording the terminal
   invocation event.
10. Return only evidence references and the terminal invocation result to Teams.
    Agent prose is evidence and never becomes a command or successor invocation.

## Recovery behavior

- Unknown create or submit outcome leaves the durable invocation claimed and the
  outbox lease unresolved. The next bounded pass reconciles the same exact
  conversation and prompt.
- A known accepted external start records `STARTED` before terminal evidence.
- A terminal evidence-store failure leaves `STARTED`; a later pass inspects the
  same conversation and completes without starting again.
- Cancellation and deadline expiry use the OpenHands interrupt endpoint and
  retain the resulting terminal evidence.
- Host interruption yields the lease. Restart consumes the same outbox intent,
  invocation, claim, conversation ID, and request digest.
- Process replacement changes the actor execution fence, causing pre-provider
  stale-authority rejection rather than continuation under the wrong process.
- Outbox leases are resolved only for terminal invocations or a known stale or
  invalid authority. Unknown external state is never reported as success or
  failure.

## Evidence storage

The filesystem evidence adapter writes immutable content-addressed blobs under
`<root>/<digest-prefix>/<sha256>`. It verifies retained bytes and length before
the evidence-registration command is issued. Registration records exact digest,
length, locator, source time, workspace partition, producing component, and
transport provenance. Terminal work-invocation events reference the registered
evidence and the digest of the exact provider output.

## OpenHands source binding used for implementation review

OBSERVED: the adapter request and response shapes were checked against OpenHands
commit `87b19b1e2418c79c770f2cc6f8a6c5ffe1f99e02` in
`/Users/paul/work/tekroo-ai/openhands-sma-p1`:

- `request.py`: `f1d05f7a7032eaf6aced15e7443a988781b2940072a56476bb77bbf03412f855`;
- `state.py`: `8a9cfc18683afb339b074d8d5a7b03f1fdbe116fc5c8ad18a7d82387199a7722`;
- `types.py`: `d5e955b491e9decc987595d4a61803368c2f7fdf9b6cceded60f753a5b6d34ef`;
- `remote_conversation.py`: `36a1f8be075d5bf2ef73298385939de63418e89a1b321d1cdbb30e06fde8df0f`;
- `conversation_router.py`: `fdce62ede9a09c4b314728f14ec9778ad73dc52463fa31648938a6d7b69846be`;
- `dependencies.py`: `178437e3012e5eb1572b43da37df8fef9ccd3cac10883e868d36096f4ccc606c`.

The checked product surface uses `X-Session-API-Key`, key/value conversation
tags, lowercase execution states, `POST /api/conversations`,
`POST /api/conversations/{id}/events`, fully paginated
`GET /api/conversations/{id}/events/search`, and
`POST /api/conversations/{id}/interrupt`.

## Verification

OBSERVED:

- Frozen contract structure runner: `PASS 3604`.
- Frozen contract reference runner: `PASS 297`.
- `go test -count=1 ./...`: PASS for every package.
- Focused race suite for the application, execution worker, OpenHands,
  filesystem, memory, and Mongo adapters: PASS.
- Real MongoDB integration suite: PASS.
- Real MongoDB operational-context reconstruction and validation: PASS.
- Real MongoDB kind-filtered backlog/change-stream test: PASS.
- `go vet ./...`: PASS.
- `git diff --check`: PASS.

The retained tests demonstrate one admitted invocation from authorization through
terminal evidence, duplicate terminal delivery, ambiguous start reconciliation,
restart from claimed and started checkpoints, cancellation, deadline expiry,
stale execution fencing, evidence-write recovery, exact OpenHands pagination,
workspace/profile drift rejection, subagent-tool rejection, lease preservation,
and exclusion of unrelated outbox traffic.

## Boundary of this step

No live OpenHands conversation, model call, SMA call, service start/restart,
deployment, production database access, historical-data access, or migration was
performed. Live assembly with the qualified SMA boundary remains Phase 4 Step 6;
the preregistered assembled-runtime exercise remains Step 7; the disposable local
operating pilot remains Step 8.

## Recommendation

Accept Phase 4 Step 5 as the operational execution-coordination implementation
candidate, then authorize Phase 4 Step 6 SMA boundary enforcement. Do not infer
live assembled-runtime qualification from this implementation gate.
