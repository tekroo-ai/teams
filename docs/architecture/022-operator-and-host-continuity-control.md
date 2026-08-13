# Operator identity and host-continuity control

**Status:** `APPROVED DESIGN — CONTRACT REVISION REQUIRED`

**Design authority:** the principal approved the continuous-operation revision,
the planned suspension fallback, and the explicit preservation of the operator
role on 2026-08-13.

**Contract authority:** `NONE` until the exact successor to accepted contract
`tekroo.kernel.contracts/0.6.0` is generated, verified, and accepted.

**Implementation authority:** `NONE` under this document alone.

**Production authority:** `NONE`

## Purpose

Tekroo v4 already distinguishes a durable actor FQN from an execution process,
provider session, model, workspace, and credential. It also represents command
authority as an exact `PrincipalRef` whose kind is `ACTOR`, `HUMAN`, `SERVICE`,
or `POLICY`.

Two related boundaries remained implicit:

1. v3 exposed a persistent `teams::operator-1` coordination role while also
   synthesizing that identity for some human operator actions. V4 retained the
   operator workflow, CLI, monitoring semantics, authenticated human actions,
   and future dashboard capability, but did not explicitly define the named
   operator actor or separate it from the human principal and control surface.
2. the execution design did not define how a locally hosted team fences work
   before planned host sleep, reconciles remote results after wake or outage, or
   represents a deliberately always-on deployment.

This revision makes both boundaries explicit before starter-role activation or
live provider qualification.

## Evidence calibration

- **OBSERVED:** accepted contract 0.6.0 defines `PrincipalRef.kind` as `ACTOR`,
  `HUMAN`, `SERVICE`, or `POLICY` and preserves exact actor FQN plus execution
  fencing.
- **OBSERVED:** the Phase 1B handoff retains the operator CLI, monitoring
  semantics, role-library reimplementation, authenticated human action, and a
  future operator dashboard.
- **OBSERVED:** neither accepted contract 0.6.0 nor the current v4 role-library
  implementation defines `teams::operator-1` as a named role binding.
- **OBSERVED:** OpenHands and model-provider status are evidence only; local
  OpenHands and tool execution remain necessary for locally hosted work even
  when model inference or a remote job runs elsewhere.
- **INFERRED:** treating the remote model as the remote agent would permit a
  stale provider response to cross a host sleep or outage boundary without an
  exact local execution and power fence.
- **INFERRED:** retaining the name `operator` without separating actor, human,
  and interface identities would recreate the v3 ambiguity and could
  misattribute human authority to an agent.

## Decision OP-001 — three distinct operator concepts

**Status:** `APPROVED BY PRINCIPAL`

V4 defines three non-interchangeable concepts:

1. **Human operator or principal.** A real person authenticated as
   `PrincipalRef{kind: HUMAN, id: ...}`. Human decisions retain the authenticated
   human identity and are never rewritten as an actor action.
2. **Operator/coordinator actor.** An optional durable actor whose conventional
   FQN is `<bus>::operator-<instance>`, including `teams::operator-1`. It has the
   same FQN, execution-ID, fencing, qualification, role-bundle, work-profile,
   evidence, and ownership rules as every other actor.
3. **Operator control surface.** CLI, API, channel, notification surface, or
   future dashboard used by a human or automation principal. A control surface
   is an adapter and does not become an actor or authority merely by presenting
   a command.

Identity fields must not be copied across these concepts. In particular, an
adapter must not synthesize `teams::operator-1` to stand in for a human whose
authenticated principal identity is known.

## Decision OP-002 — durable operator actor continuity

**Status:** `APPROVED BY PRINCIPAL`

The operator actor uses the existing durable-FQN rule. Restarting
`teams::operator-1` creates a new `ExecutionId` and higher fencing epoch but not
a new actor. Durable assigned work, coordination state, and attributable memory
may remain associated with that FQN. Output from the prior execution is stale
and cannot acquire authority after replacement.

Changing the operator role's semantic identity, role definition, or intended
organizational subject requires an explicit role-binding replacement with
lineage. It is not an ordinary process restart.

## Decision OP-003 — operator is not intrinsic authority

**Status:** `APPROVED BY PRINCIPAL`

The string `operator`, an operator prompt, its role bundle, provider, model, or
control surface grants no organizational authority.

An operator actor may submit only commands whose catalogue entry admits
`ACTOR` authority and for which current policy authorizes that exact actor and
execution. It cannot present itself as `HUMAN`, inherit the human escalation
principal, bypass ownership or validation, accept its own work, or turn an
observation into organizational truth.

Human-only and policy-only commands remain human-only and policy-only. Any
delegation to the operator actor is explicit, scoped, versioned, revocable, and
retained as evidence.

## Decision OP-004 — bounded coordination responsibilities

**Status:** `APPROVED BY PRINCIPAL`

The starter operator role may be qualified to:

- inspect organizational projections and execution observations;
- coordinate a directed acyclic work plan;
- propose decomposition, assignment, validation, escalation, and recovery;
- invoke separately authorized application commands;
- surface stalls, orphaned executions, ambiguity, and unresolved external
  outcomes;
- prepare evidence-backed decisions for a human or policy principal; and
- route `HUMAN_REQUIRED` work to its exact authenticated human destination.

It may not:

- mutate MongoDB or another organizational store directly;
- create authority through prompts or role text;
- silently reassign, complete, accept, release, reopen, or expand work;
- resolve its own disputed output as the sole reviewer or adjudicator;
- indefinitely circulate work among roles; or
- treat SMA recollection, provider status, or model confidence as truth.

The role profile and content bundle are immutable, versioned,
content-addressed, signed, and validated before activation.

## Decision OP-005 — human-required delivery

**Status:** `APPROVED BY PRINCIPAL`

`HUMAN_REQUIRED` is an organizational terminal route for the current automated
decision path, not a message to whichever actor happens to be named operator
and not an assumption that one distinguished human is the only human
participant.

The escalation records the exact selected human participant or participant set,
or a separately authorized human-selection policy, decision question, relevant
immutable evidence, deadline or absence of deadline, and permitted response
commands. Selection resolves to exact `HUMAN` principals before delivery.
Delivery may use any qualified operator control surface. Delivery success does
not resolve the escalation; only an authenticated, accepted human command does.

The operator actor may summarize and present the decision but cannot answer as
the human principal.

## Decision HP-001 — durable human participants and scoped roles

**Status:** `APPROVED BY PRINCIPAL`

V4 supports multiple durable human participants. Each person has an exact
`PrincipalRef{kind: HUMAN, id: ...}` and a separately versioned participant
profile. Human IDs do not use actor FQN syntax and are not execution identities.

A human participant may hold one or more scoped role bindings such as SME,
client, end user, approver, principal, or a versioned project-defined role. A
role binding records its project, work, domain, or system scope; advisory
topics; any explicitly permitted command types; policy identity; evidence;
validity interval; and revocation state.

Role labels are descriptive and select eligible recipients. They grant no
authority beyond the exact scoped policy binding. An SME answer is ordinarily
advice or evidence. A client or approver answer has organizational decision
effect only when the current policy grants that exact human the applicable
command authority over the exact subject.

## Decision HP-002 — authentication and delivery bindings

**Status:** `APPROVED BY PRINCIPAL`

A participant profile contains no reusable credential or raw contact address.
It references content-addressed authentication and delivery bindings:

- an authentication binding identifies method, issuer, opaque subject digest,
  assurance class, revision, evidence, activation, and revocation;
- a delivery binding identifies a channel class, opaque endpoint digest,
  qualified adapter profile, confidentiality ceiling, authentication support,
  revision, evidence, activation, and revocation.

Email, web portal, Slack, Teams, SMS, and later channels remain replaceable
adapters. A channel identity is not itself a human principal. The adapter must
authenticate the presenter through an active binding and attach credential,
channel, and delivery provenance to the human command. Authorization is checked
after authentication against current participant and role bindings.

Authentication tokens and one-time response capabilities are verified before
consumption and consumed atomically with accepted response recording. Failure
after presentation cannot silently destroy the human's only retry path.

## Decision HP-003 — typed human interaction aggregate

**Status:** `APPROVED BY PRINCIPAL`

Ordinary consultation is not forced through escalation. V4 defines a durable
human interaction linked to a story, task, escalation, or other approved work
subject. Opening it records:

- the exact originating principal and, when applicable, actor execution;
- immutable canonical question and response-specification digests;
- question revision and causal work path;
- exact selected human recipients and applicable role-binding identities;
- interaction purpose: advisory consultation, evidentiary question,
  requirements clarification, acceptance feedback, or authorized decision;
- declared effect: advisory only, evidence only, or authority only if current
  authorization independently permits it;
- response policy: one exact respondent, any one, all, or a finite quorum;
- deadline and deterministic timeout policy;
- confidentiality class and disclosure-scope digest; and
- evidence, policy, idempotency, and provenance.

An interaction is directed and finite. A corrected or follow-up question
creates a causally linked successor interaction with a new immutable question
identity. It does not mutate the question already presented and does not create
an unbounded conversational or role-handoff cycle.

## Decision HP-004 — presentation and delivery without impersonation

**Status:** `APPROVED BY PRINCIPAL`

The canonical question is the organizational source. An operator actor may
create a human-oriented rendering or summary, but the delivery record retains
both canonical and rendered digests, the rendering actor and execution, exact
recipient, route binding, adapter identity, delivery attempt, and observed
outcome.

The rendering cannot silently change response choices, requested authority,
confidentiality, or acceptance meaning. A materially changed presentation
requires a successor interaction or is rejected. Delivery success is an
observation and does not count as response, agreement, or authorization.

## Decision HP-005 — authenticated response and exact effect

**Status:** `APPROVED BY PRINCIPAL`

A human responds through a qualified channel adapter. The resulting command
uses the respondent's exact `HUMAN` authority, with `actor_fqn` and `execution`
null. Its payload binds the interaction, question revision, recipient, role
binding, delivery attempt, response artifact digest, response-specification
digest, authentication binding, channel provenance, timestamp, and evidence.

The kernel rejects responses from an unselected, inactive, revoked, expired,
wrong-scope, insufficiently authenticated, stale-question, or duplicate
principal. It evaluates the declared response policy from exact accepted
respondents. `ANY_ONE`, `ALL`, and `QUORUM` never collapse distinct humans into
a role name or shared channel identity.

A response is recorded first as human testimony. Its organizational effect is
classified separately:

- `ADVISORY_ONLY` cannot authorize a transition;
- `EVIDENCE_ONLY` can support a later authorized decision;
- `AUTHORITY_IF_AUTHORIZED` has the requested effect only if the catalogue and
  current scoped authorization independently admit that exact human command.

The human's words, the operator's summary, and any later policy interpretation
remain distinct artifacts with lineage.

## Decision HP-006 — closure, timeout, confidentiality, and manual relay

**Status:** `APPROVED BY PRINCIPAL`

The interaction reaches `SATISFIED` only when its exact response policy is met.
It then closes through an authorized, revision-checked transition. Expiry,
decline, revocation, unavailable recipients, conflicting responses, or an
unsatisfied quorum remain explicit terminal or escalated outcomes; silence is
not consent.

Every delivery is bounded by the recipient's disclosure scope and channel
confidentiality ceiling. Responses and evidence retain those classifications in
subsequent projections and SMA intake.

If an operator manually relays what a person allegedly said, the result is
operator-attributed evidence. It is not an authenticated human response unless
the named human separately attests through a qualified binding. In-person
attestation may be added as a qualified authentication method, but cannot be
synthesized from an operator assertion.

## Decision HC-001 — continuous operation is the preferred posture

**Status:** `APPROVED BY PRINCIPAL`

The preferred operating posture is `CONTINUOUS`: the local organizational
kernel, MongoDB change-stream path, execution coordinator, OpenHands tool host,
and applicable SMA services remain awake and healthy while work is admitted.

Closed-lid operation is a host/deployment capability, not a kernel assumption.
Tekroo may report whether required services and host leases are healthy, but it
does not treat Power Nap, wake-for-network-access, `caffeinate`, a model
provider, or a remote inference request as proof that the local agent system is
continuously available.

A supported closed-display configuration or a dedicated always-on host may
satisfy the deployment requirement. The exact platform mechanism remains an
adapter/deployment concern.

## Decision HC-002 — planned suspension state machine

**Status:** `APPROVED BY PRINCIPAL`

When continuous operation cannot be maintained, the team uses this explicit
control state machine:

`ACTIVE -> QUIESCING -> SUSPENDED -> RECONCILING -> ACTIVE`

Requesting quiescence:

- atomically closes new-work admission;
- advances a monotonic `power_epoch`;
- establishes a deadline and exact initiating principal;
- fences local tool calls and organizational effects from the prior epoch; and
- records an approved disposition for every in-flight execution.

`SUSPENDED` is entered only after the bounded quiescence procedure has recorded
each in-flight execution as cancelled, safely detached, terminally captured, or
unknown. Unknown does not mean cancelled.

No direct `SUSPENDED -> ACTIVE` transition exists. Wake always enters
`RECONCILING` first.

## Decision HC-003 — provider disposition policies

**Status:** `APPROVED BY PRINCIPAL`

Every provider-backed execution has one preregistered suspension disposition:

- `CANCEL_ON_SUSPEND`: request bounded cancellation and reconcile ambiguous
  cancellation; never assume external effects rolled back.
- `COMPLETE_AND_QUARANTINE`: permit bounded remote completion, capture the
  result outside organizational acceptance, and quarantine it until wake-time
  identity and effect reconciliation.
- `DETACH_AND_RECONCILE`: permit an explicitly detachable remote job to
  continue, prohibit further local tools, and query the authoritative provider
  state after wake.

The disposition is recorded before dispatch. It cannot be selected
retroactively because the host is about to sleep. A result carrying an earlier
power epoch is late evidence and cannot directly mutate work.

## Decision HC-004 — reconciliation and unexpected outage

**Status:** `APPROVED BY PRINCIPAL`

On wake or process recovery, the execution coordinator enters `RECONCILING`,
keeps admission closed, advances or confirms the durable power epoch, and
reconciles every execution that was running, cancelling, detached,
quarantined, or unknown.

Reconciliation checks provider state, local tool effects, workspace state,
durable organizational events, outbox state, and current execution fencing.
Every ambiguous result remains `UNKNOWN` until authoritative evidence resolves
it or policy terminates it as blocked/human-required. Reconciliation is
idempotent and crash-recoverable.

An unplanned sleep, process loss, network partition, or host outage cannot run
the planned quiescence protocol. Recovery records `UNEXPECTED_OUTAGE`, advances
the power epoch, enters `RECONCILING`, and applies the same fail-closed
admission and late-result rules. Absence of an orderly suspension receipt is
itself retained evidence.

## Decision HC-005 — resumption gate

**Status:** `APPROVED BY PRINCIPAL`

The team may return to `ACTIVE` only when:

- required local services and leases are healthy;
- every pre-boundary execution has a retained reconciliation outcome;
- no unresolved result can still acquire authority under an old execution or
  power fence;
- quarantined results have been accepted as evidence, rejected, superseded, or
  left explicitly blocked;
- durable change-stream/outbox positions are reconciled;
- the exact resuming principal or policy is authorized; and
- the resumption receipt records the current power epoch and evidence set.

Only the accepted resume transition reopens admission. Opening a laptop lid,
restoring network connectivity, receiving a provider response, or observing a
healthy process is not by itself organizational resumption.

## Required contract effects

The successor contract must add, without modifying 0.6.0:

- a content-addressed operator-role binding schema;
- durable human-participant profiles with scoped roles, authentication
  bindings, delivery bindings, confidentiality, activation, and revocation;
- a finite human-interaction aggregate with exact recipients, immutable
  question/response specifications, delivery receipts, authenticated responses,
  response policies, effect classification, expiry, closure, and successor
  lineage;
- a system continuity state with operating posture, control state, monotonic
  power epoch, admission fence, policy digest, and unresolved executions;
- typed commands and events for binding the operator role, registering and
  updating human participants, opening/delivering/responding to/closing or
  expiring human interactions, configuring continuity, requesting quiescence,
  recording suspension, recording an unexpected outage, beginning
  reconciliation, and resuming;
- exact provider-disposition and execution-reconciliation records;
- invariants preventing human/actor/control-surface impersonation;
- invariants preventing role labels, shared endpoints, operator relay, silence,
  delivery, or response text from becoming authorization;
- invariants preventing old-epoch output or direct wake from acquiring
  authority; and
- normative and counterexample fixtures for restart continuity, multiple human
  identities, exact recipient selection, scoped role authority, authentication,
  delivery, response policy, confidentiality, operator presentation and relay,
  stale questions, revocation, silence, planned suspension, late results,
  ambiguous cancellation, unexpected outage, and resumption.

Existing 0.6.0 command and event semantics remain unchanged. Historical replay
continues under the contract version that originally accepted each record.

## Implementation sequence after contract acceptance

1. Add pure operator-binding and continuity evaluators with fake-clock tests.
2. Add persistent system-control state and atomic admission/power fencing.
3. Apply the power fence at assignment, model dispatch, local tool execution,
   provider result intake, and organizational command acceptance.
4. Add provider disposition and idempotent reconciliation ports.
5. Reimplement the starter operator role as a signed content-addressed bundle.
6. Add participant, scoped-role, authentication, and delivery repositories.
7. Add the finite human-interaction coordinator and authenticated human
   control-surface routing without synthetic operator attribution.
8. Qualify multiple-human selection, channel delivery, retry, revocation,
   confidentiality, response policy, and intermediary behavior.
9. Qualify planned suspension, unexpected outage, and continuous-host behavior
   before production operation.

No step authorizes provider execution, production deployment, or a claim that
closed-lid behavior has been empirically qualified.
