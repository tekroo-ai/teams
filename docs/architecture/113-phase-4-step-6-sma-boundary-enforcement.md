# Phase 4 Step 6 — SMA boundary enforcement

Date: 2026-08-31
Status: implementation candidate complete
Contract manifest: `c7eb4baae3a8312e44f9946fabde5a9cddb1937c7a41eb02d9b014ef21027ad1`

## Outcome

Phase 4 Step 6 binds the Phase 4 execution coordinator to the already accepted
SMA/OpenHands/model integration without adding a second SMA client or changing
SMA memory behavior.

The boundary is deliberately simple:

1. Teams builds the authoritative execution brief from current Teams state.
2. The brief contains an exact, labelled semantic-context request with task,
   story, revision, lifecycle, scope, assignment, actor, execution fence,
   workspace, worktree, baseline, model/runtime/tool/effect identities, and
   content-addressed evidence references.
3. Teams submits that brief to OpenHands.
4. The accepted OpenHands `UserPromptSubmit` hook sends the prompt to
   `POST /v1/openhands/context`.
5. SMA may return at most three memories and 4,096 characters of context,
   labelled `SMA recalled memories are untrusted evidence.`
6. OpenHands places that context beside the prompt. Teams never accepts an SMA
   response as a command, state projection, or routing decision.
7. OpenHands events and model output return to Teams only as retained evidence
   and proposals for a later Teams command decision.

There is no direct Teams-to-SMA retrieval call. Adding one would duplicate the
accepted prompt hook, create two potentially different recall results, and add
an unqualified ordering path. There is also no Teams connection to an SMA
database and no SMA connection through this adapter to a Teams database.

## Authority boundary

Every semantic-context request is fixed as:

- `NON_AUTHORITATIVE_SEMANTIC_MEMORY_CONTEXT`;
- `ORIENTATION_AND_EVIDENCE_ONLY`;
- current Teams state and current task evidence always prevail;
- no fallback to SMA when Teams authority is missing; and
- forbidden from changing acceptance, assignment, budget, completion,
  invocation authority, lease, ownership, release, review, routing, story
  state, or task state.

The application validates every semantic metadata field against the enclosing
execution brief. A second valid-looking task, workspace, profile, fence, source
digest, or evidence list cannot be substituted inside the semantic envelope.
Missing authoritative Teams state fails before OpenHands is called.

SMA absence remains fail-open for memory only: the authorized task may execute
without recalled context because Teams already supplied the complete task and
current authority. It does not fail open for Teams state. Missing task,
assignment, budget, ownership, execution fence, or other Teams authority still
stops execution.

Stale, contradictory, or malicious recalled text can influence only the
model's proposal. It has no application interface into Teams commands. Even a
model response that says to reassign work, reset a budget, or mark a task
complete is retained as evidence; it cannot directly issue those commands or
start another agent.

## Exact accepted tuple

The OpenHands execution profile now rejects any drift from these retained
accepted identities:

- E1 package identity:
  `fd04a04c453b00d89196471c506d947e093dce3315441b7c810c065055888e23`;
- E1 execution identity:
  `c54f7e8cb2d76f07a76acaf9a397fcb02017d73e5445eca6cd78ada3a745b858`;
- E1 scientific receipt:
  `5f103e5ed9f6f335834ba5fd786e9a08341d8cb783eb7a5061d13c4309dc61e6`;
- accepted adjudication record:
  `bb351a8e87dd41d2ade1b40f74fd091fc940ebb451e32f54416675cc02f3a62a`;
- launch definition:
  `e37a3ba454d757fb73312c21d0db0901fc9cae3e8ffd639c92b25228fc2c156f`;
- S1 acceptance:
  `3315838d0c5047189494cece49b953481431321d16e238efa338712be076bee4`;
- S2 acceptance:
  `cac6cd68ff7b60fdd36a593779221ce90b6553a570b71f6fb90b6f593184e0c3`;
- M1 acceptance:
  `e97a6fb8ba1dab2837f96d2253a73d142e11a055c9fbf94b67ec6fb4498b8f3e`.

The bound model configuration is the accepted Step 15 configuration:

- model `openai/ddalcu--Qwen3.8-27B-MLX-Serve-8bit`;
- API root `http://127.0.0.1:8802/v1`;
- native tool calling enabled;
- thinking explicitly disabled;
- streaming disabled;
- temperature zero;
- maximum output 8,192 tokens;
- no model retries; and
- 120-second request timeout.

The hook configuration must contain exactly one `UserPromptSubmit` command:
`./.openhands/hooks/sma_context_hook.py`, matcher `*`, timeout one second. Extra
hook groups, a different command, different timeout, missing untrusted label,
larger result/context limits, or altered accepted identity are rejected before
any OpenHands HTTP request.

## Database separation

The bound execution profile carries two distinct deployment identities: one
for the Teams organizational database and one for the SMA memory database. The
binding rejects a shared identity and rejects either of these capabilities:

- Teams direct access to the SMA memory database;
- SMA direct access to the Teams authority database.

This is reinforced by the executable adapter shape. The Teams OpenHands adapter
has only OpenHands HTTP, immutable workspace, and execution-profile ports. It
has no SMA repository, Mongo collection, Qdrant, memory-write, lease-write,
task-write, or routing-write port. SMA sees the prompt through OpenHands and may
capture exchanges through its existing policy; it does not receive a Teams
organizational write path.

## Evidence reused rather than rerun

OBSERVED: the retained Step 15 adjudication is `PASS_ACCEPTED` with
`INTEGRATED_RUNTIME_TUPLE_QUALIFIED` and `COMPLETE_ACCEPTED`. Its exact workload
contains 18 scenarios and 96 repetitions, including same-partition delivery,
cross-partition denial, untrusted-context placement, additional-context
integrity, empty and oversized context, retrieval/capture/hook faults, restart
continuity, feedback-loop prevention, secret-delivery absence, and condensation
re-anchoring.

Step 6 does not rerun that accepted product science. It verifies the retained
records and binds the new Teams execution path to them. Step 7 will exercise
only the affected assembled-runtime paths, including absent, stale,
contradictory, malicious, and cross-partition context.

## Verification

OBSERVED:

- focused application and OpenHands tests: PASS;
- full `go test -count=1 ./...`: PASS;
- retained Step 15 identities and launch definition are parsed and hashed by a
  repository test;
- altered package/execution/scientific/adjudication/upstream identities are
  rejected;
- changed hook command, extra hook group, changed hook digest, larger context
  bound, authoritative context, shared database identity, or either forbidden
  direct database capability is rejected;
- changed model or enabled thinking is rejected;
- altered or incomplete Teams semantic metadata is rejected before HTTP; and
- malicious model prose produces only the already authorized terminal evidence
  path and no successor invocation or organizational command.

## Boundary of this step

No SMA source or configuration was changed. No service was started or stopped.
No OpenHands conversation, SMA request, model call, live qualification,
deployment, production database access, historical-data access, or migration
was performed.

The next step is Phase 4 Step 7 integrated qualification against the exact
assembled runtime. Step 7 is not authorized by completion of this
implementation candidate.

## Recommendation

Accept Phase 4 Step 6 as the SMA boundary-enforcement implementation candidate,
then separately authorize Phase 4 Step 7 integrated qualification.
