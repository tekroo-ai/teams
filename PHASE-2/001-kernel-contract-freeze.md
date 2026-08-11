# Tekroo v4 Phase 2 Step 1 — Kernel contract freeze

**Status:** DECISIONS COMPLETE — CONTRACT CORPUS GENERATION IN PROGRESS  
**Authorized by principal:** `Okay, it's authorized.` on 2026-08-11  
**Decision authority:** Principal  
**Coordinator and evidence recorder:** Codex  
**Implementation authority:** NONE

## Purpose

Freeze the provider-neutral contracts that a Tekroo v4 kernel implementation
must satisfy, and define executable acceptance criteria for those contracts.
This step produces specifications, schemas, fixtures, and conformance manifests.
It does not implement a runtime, transplant Tekroo v3 source, run the bounded
SMA or OpenHands investigations, migrate data, or authorize production use.

## Accepted input

- Phase 1B final gate:
  `OUTPUT/adjudication/step-8-final-gate.json`
  (`6c1859e0580175d58225a60f00a1e7c1d016b1937c6abe14245e4fe89e93bebe`)
- Architecture handoff:
  `PHASE-1B/008-final-architecture-handoff.md`
  (`ec0aef90ceab404c6288a0279d47f49ee4c5af1c00577d55b8ffd25882b18fef`)
- Machine-readable handoff:
  `OUTPUT/adjudication/phase-1b-architecture-handoff.json`
  (`137a1399a70cfc96cf36997e8c2154b1847cdde5ee8a50f7a39f7d06e3c83e20`)

These identities are inputs, not editable Phase 2 working files. If one changes,
the Step 1 gate must stop until the change is reconciled explicitly.

## Evidence calibration

- **OBSERVED:** The Phase 1B final gate is `PASS_PRINCIPAL_CONFIRMED` and says
  the next phase is not automatically authorized.
- **PRINCIPAL-AUTHORIZED:** The principal subsequently authorized Phase 2 Step 1.
- **PRINCIPAL-APPROVED INPUT:** The handoff assigns organizational authority to
  the Tekroo kernel and keeps providers, transport, persistence encodings, and
  SMA behind explicit boundaries.
- **COORDINATOR RECOMMENDATION:** The contract freeze should use executable,
  language-neutral schemas and fixtures without making their JSON representation
  the kernel's internal object model or persistence format.
- **UNKNOWN:** No v4 implementation exists in this step, so the specifications
  cannot yet claim implementation conformance, performance, operational safety,
  or production compatibility.

## Decision method

Each decision is presented separately. A decision becomes binding only after a
principal statement is recorded. Approval of one decision does not silently
approve later decisions.

Decision vocabulary:

- `APPROVE`: accept the coordinator recommendation as written.
- `APPROVE_WITH_REVISION`: accept after recording the stated change.
- `DEFER`: leave unresolved and block any dependent freeze item.
- `REJECT`: do not include the proposed contract.

Every approved decision must identify its normative contract, prohibited
interpretations, executable verification, traceability to Phase 1B, and any
remaining unknowns.

## Decision docket

1. Contract boundary, authority, and versioning.
2. Durable actor identity, execution incarnation, and fencing.
3. Command, event, and stable-outcome envelopes.
4. Catalogue, schemas, compatibility, aliases, and unknown messages.
5. Immutable DAG lineage and causal-edge validity.
6. Story, task, ownership, assignment, and reassignment state machines.
7. Authorization, expected revisions, idempotency, and deterministic rejection.
8. Completion request, authoritative completion, reopening, and correction.
9. Evidence, provenance, audit projection, and source/runtime identity.
10. Kernel ports, repository boundaries, and transaction semantics.
11. Executable conformance suites, fixtures, and property invariants.
12. Canonical contract manifest, compatibility gate, and Step 1 closure.

The order is dependency-driven. Later decisions may refine an earlier approved
decision only through an explicit revision presented to the principal.

## Decision 1 — Contract boundary, authority, and versioning

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approve.` on 2026-08-11

### Coordinator recommendation

`APPROVE` the following contract boundary.

1. The normative kernel contract consists of provider-neutral value semantics,
   commands, events, state-transition invariants, authorization rules, stable
   outcomes, and port obligations.
2. Canonical JSON Schemas and versioned JSON fixtures are the executable
   language-neutral conformance representation. They are not the required
   in-memory type system, wire protocol, MongoDB document layout, or public API.
3. An adapter may use a different representation only if it preserves every
   normative value, rejects non-representable input, and passes canonical
   round-trip and behavior fixtures without semantic coercion.
4. MongoDB documents, change-stream records, OpenHands sessions, SMA memory,
   Git-provider payloads, HTTP/MCP envelopes, CLI syntax, credentials, process
   identifiers, and model-provider fields remain projections or adapter data.
   They cannot add, remove, or redefine organizational meaning.
5. The first accepted manifest is identified as
   `tekroo.kernel.contracts/0.1.0`. Once the Step 1 gate passes, its files are
   content-addressed and immutable. A correction creates a new manifest version;
   it never silently edits the accepted baseline.
6. Before a stable `1.0.0` contract, breaking revisions are permitted only with
   an explicit compatibility report, fixture migration, and principal-approved
   gate. Version `0.1.0` therefore means frozen architecture baseline, not a
   production-stability promise.
7. Every generated language binding, API projection, persistence mapping, and
   provider adapter declares the exact contract-manifest identity it implements.

### Prohibited interpretations

- JSON becomes the mandatory kernel implementation language or hot-path encoding.
- MongoDB or a provider payload becomes the source definition of an organizational
  command or event.
- A generated binding can change semantics that are fixed by the canonical
  contract and fixtures.
- `0.1.0` implies a production-ready, backward-compatible public API.
- An accepted file may be edited in place while retaining its version or digest.

### Executable acceptance criteria

The Decision 1 contract is satisfied only when the Step 1 deliverables include:

1. one canonical manifest with semantic version, file digests, dependency order,
   and Phase 1B provenance;
2. machine validation that every listed file exists and matches its digest;
3. positive and negative fixtures for every later frozen contract family;
4. round-trip conformance tests for at least one reference binding, with unknown
   fields/types handled according to the explicitly approved catalogue decision;
5. a compatibility report whenever one manifest supersedes another;
6. a check that provider-, persistence-, transport-, SMA-, Git-, credential-,
   and process-specific fields do not enter the normative kernel schemas unless
   a later principal decision explicitly revises this boundary.

### Phase 1B traceability

- Architecture handoff: organizational kernel authority and adapter separation.
- Negative requirement `NEG-010`: no provider-specific model in the
  organizational domain.
- Catalogue decision: unknown types may not be silently coerced.
- Build/release decision: exact identities and one versioned canonical gate.

### Remaining unknowns

The binding language, repository for the eventual implementation, serialization
libraries, code-generation tools, and performance representation are not selected
by this decision. They are implementation choices constrained by conformance.

### Recorded disposition

`APPROVE`. The seven recommended rules, prohibited interpretations, executable
acceptance criteria, and Phase 1B traceability above are binding for the Step 1
contract freeze.

## Decision 2 — Durable actor identity, execution incarnation, and fencing

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approve.` on 2026-08-11

### Coordinator recommendation

`APPROVE` the following identity and fencing contract.

1. A concrete actor FQN is the durable organizational identity. Its canonical
   string is `<bus>::<role>-<instance>`. `bus` and `role` are lowercase ASCII
   slugs of 1–63 characters, beginning and ending with an alphanumeric character
   and permitting internal hyphens. `instance` is a positive decimal integer of
   at most 20 digits with no leading zero.
2. FQN matching is exact and case-sensitive after canonical parsing. Whitespace,
   empty components, noncanonical leading zeros, wildcard components, control
   characters, and alternate Unicode lookalikes are rejected as actor identity.
3. Role-class and wildcard addresses are routing selectors, not actors. Types
   representing `<bus>::<role>` or `<bus>::<role>-*` cannot be supplied where an
   `ActorFQN` is required and cannot own work, hold authority, or appear as the
   actor responsible for an organizational transition.
4. Restarting, upgrading, reconfiguring, or changing the model, role-library
   revision, worktree, provider session, process, host, container, credential,
   or secret for an FQN does not create a new actor and does not reassign its
   durable work.
5. Each runtime incarnation receives an opaque provider-neutral `ExecutionId`
   and a positive 64-bit `FencingEpoch`. The `ExecutionId` is unique and is never
   reused after that incarnation reaches a terminal state. Provider session IDs
   are optional provenance attached to the execution, not organizational identity.
6. Starting a new authoritative incarnation for an FQN atomically advances its
   fencing epoch. Only the currently registered `(ActorFQN, ExecutionId,
   FencingEpoch)` tuple may commit an execution-attributed organizational command.
   Older or mismatched tuples receive one deterministic stale-execution outcome.
7. Fencing removes authority; it does not assert that an old process stopped or
   that its external effects were rolled back. Late output is retained as
   execution evidence when policy permits, but it cannot silently change
   organizational state.
8. An FQN is never silently reinterpreted as a different actor. Retirement and
   later reactivation preserve its history. A genuinely different actor requires
   a different FQN or a future explicit identity-migration contract.

### Why this is the recommended boundary

- **OBSERVED IN PINNED V3 SOURCE:** `mcp/internal/schema/address.go` at commit
  `7630ca20ccfa0fc2f4102147b10c3705e9ba0158` represents concrete identity with
  `<bus>::<role>-<positive-decimal-instance>`, distinguishes bare-role and
  wildcard addresses, parses hyphenated roles, and compares slots exactly.
- **PRINCIPAL-APPROVED:** Phase 1B records that restarting `teams::coder-1`
  creates a new execution incarnation, not a new actor, and that credential
  rotation does not change organizational identity.
- **PRINCIPAL-APPROVED:** Phase 1B separates durable FQN ownership from
  provider-neutral `ExecutionId`, registry fencing epoch, delivery claims, and
  provider session provenance.
- **COORDINATOR DESIGN RECOMMENDATION:** Tight lowercase ASCII slug and length
  bounds remove ambiguous spellings and Unicode confusables. Those tighter token
  bounds are a v4 contract choice; they are not claimed as observed v3 behavior.

### Prohibited interpretations

- A provider session, process, worktree, model, or credential becomes actor identity.
- Restarting an actor releases or transfers its task ownership.
- A role class, wildcard, or broadcast target owns work or performs a transition.
- Cancellation or fencing proves termination or rollback of external effects.
- Timestamp order, UUID order, or fencing epoch alone establishes causal lineage.
- A retired FQN may be reused for a semantically different actor without an
  explicit identity migration.

### Executable acceptance criteria

1. Parse/render round trips for canonical concrete FQNs, including hyphenated
   roles, and rejects empty, wildcard, role-class, whitespace, leading-zero,
   oversized, mixed-case, control-character, and Unicode-lookalike cases.
2. Type-level or validation-level separation prevents an address selector from
   entering an `ActorFQN` field.
3. Restarting one FQN creates a new `ExecutionId` and higher fencing epoch while
   preserving actor-owned task identity and history.
4. Two concurrent registration attempts for one FQN produce exactly one current
   authoritative tuple; the loser is deterministically fenced.
5. Commands from stale, missing, mismatched, zero, or future fencing tuples are
   rejected without a canonical organizational state transition.
6. Duplicate registration with the same approved idempotency input returns the
   same execution tuple rather than consuming another epoch.
7. Credential rotation and provider-session replacement leave `ActorFQN`
   unchanged while updating execution provenance explicitly.
8. A late result from a fenced execution can be stored as evidence but cannot
   complete, fail, reassign, reopen, or otherwise mutate organizational work.
9. Retirement and reactivation preserve FQN lineage; creating a different actor
   under an existing FQN is rejected.

### Remaining unknowns

The eventual `ExecutionId` generator, registry storage encoding, lease duration,
process supervision, provider-session lifecycle, and credential mechanism are
not selected here. Decision 7 will freeze idempotency and deterministic outcome
envelopes; Decision 10 will freeze transaction and repository obligations.

### Recorded disposition

`APPROVE`. The durable FQN, selector separation, execution-incarnation,
fencing, late-evidence, retirement/reactivation, lexical, and acceptance-test
rules above are binding for the Step 1 contract freeze.

## Decision 3 — Command, event, and stable-outcome envelopes

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approved.` on 2026-08-11

### Coordinator recommendation

`APPROVE` three distinct provider-neutral envelopes: `KernelCommand`,
`DomainEvent`, and `CommandReceipt`. A message-delivery envelope is not any of
these types, even when it transports one of them.

### A. `KernelCommand`

Every command carries:

1. `contract_manifest`: exact accepted contract identity;
2. `command_id`: canonical UUIDv7 generated once by the initiating application
   service and reused for every retry;
3. `command_type` and `command_version`: catalogue-qualified operation identity;
4. `target`: typed organizational aggregate reference;
5. `authority`: authenticated principal reference and authorization context;
6. `actor_fqn`: required when an organizational actor is attributed;
7. `execution`: required `(ExecutionId, FencingEpoch)` when the command relies on
   a running actor incarnation; absent for authorized non-execution operations;
8. `expected_revision`: required whenever the operation is defined against an
   existing aggregate revision; create operations use the explicit no-prior-state
   precondition;
9. `idempotency_key`: operation-scoped key whose semantics are frozen in
   Decision 7;
10. `correlation_id`: groups one organizational intent without defining causality;
11. `causation`: zero or more typed references to existing accepted events;
12. `issued_at`: optional source-supplied evidence timestamp, never commit order;
13. `payload`: command-version-specific validated content; and
14. `evidence_refs`: typed immutable references rather than embedded claims of
    source truth.

The kernel stamps `received_at`; a caller cannot supply or override it. A command
is intent submitted for decision, not evidence that a transition occurred.

### B. `DomainEvent`

Every accepted organizational transition emits one or more immutable events with:

1. `contract_manifest`, `event_id` (kernel-generated canonical UUIDv7),
   `event_type`, and `event_version`;
2. `aggregate` and the resulting positive `aggregate_revision`;
3. the deciding `command_id`;
4. authenticated authority attribution, optional `actor_fqn`, and optional
   execution tuple copied from the accepted command context;
5. typed DAG parent edges, whose validity is frozen in Decision 5;
6. kernel-stamped `committed_at`;
7. event-version-specific immutable `payload`; and
8. provenance and evidence references frozen in Decision 9.

UUIDv7 and timestamps may support indexing and operational inspection. Neither
establishes causal order; only accepted typed DAG edges and aggregate revision do.
Events are domain facts emitted by the kernel, never provider status reports,
model assertions, message-delivery state, or mutable projections.

### C. `CommandReceipt`

Every command that reaches a kernel decision produces an immutable receipt with:

1. `contract_manifest`, `command_id`, `command_type`, and target;
2. `outcome_code` from the closed baseline set:
   `APPLIED`, `NO_CHANGE`, `REJECTED_INVALID`, `REJECTED_UNAUTHORIZED`,
   `REJECTED_NOT_FOUND`, `REJECTED_CONFLICT`, `REJECTED_STALE_EXECUTION`,
   `REJECTED_CLOSED`, or `REJECTED_POLICY`;
3. `state_changed`, resulting aggregate revision when one exists, and ordered
   emitted event IDs;
4. stable machine-readable reason code plus bounded non-authoritative detail;
5. `received_at` and `decided_at`, both kernel-stamped; and
6. the decision provenance required by Decision 9.

An idempotent retry returns the original receipt; it does not manufacture a
second event or change `APPLIED` into a different duplicate outcome. A rejected
command emits no target-aggregate domain event. Its receipt remains auditable.

Failure before a kernel decision—transport timeout, storage unavailability,
cancellation, process loss, or unavailable dependency—is an `InvocationFailure`,
not a `CommandReceipt`. It cannot be represented as an organizational rejection
or success. The caller reconciles by `command_id` before retrying or inferring an
outcome.

### Why this is the recommended boundary

- **OBSERVED IN PINNED V3 SOURCE:** `mcp/internal/schema/message.go` at commit
  `7630ca20ccfa0fc2f4102147b10c3705e9ba0158` combines routing, payload,
  delivery-claim, resolution, dead-letter, lineage, persistence, federation,
  and dispatch-context fields in one bus envelope.
- **PRINCIPAL-APPROVED:** Phase 1B requires organizational commands/events to
  remain separate from transport, persistence, delivery claims, providers, and
  model statements.
- **PRINCIPAL-APPROVED:** `CompletionRequested` is evidence or intent;
  `Completed` is emitted only by the kernel after the authoritative transition.
- **COORDINATOR DESIGN RECOMMENDATION:** One durable receipt per decided command
  makes rejection, retry, reconciliation, and idempotency explicit without
  turning rejected intent into a target-aggregate fact.

### Prohibited interpretations

- Receipt of a command means it was accepted or applied.
- Delivery, provider, tool, shell, model, or process status is a domain event.
- A correlation ID, timestamp, UUID ordering, or adjacency establishes causality.
- A rejection may be reported when the kernel did not durably decide it.
- Retrying a decided command creates new domain events or a different outcome.
- Free-form detail text is a stable machine contract.
- Rejected input mutates the target aggregate merely to record the rejection.

### Executable acceptance criteria

1. Schema validation distinguishes commands, events, receipts, invocation
   failures, and delivery envelopes with no ambiguous union member.
2. Positive fixtures cover all required and conditionally required fields;
   negative fixtures cover caller-stamped commit fields, missing execution
   attribution, invalid expected-revision preconditions, unknown outcome codes,
   and provider/persistence/delivery contamination.
3. One accepted command deterministically yields the same ordered event payloads
   and receipt under a fake clock and deterministic ID source.
4. Retrying the same `command_id` and idempotency input returns the byte-equivalent
   canonical receipt and no additional domain event.
5. Rejection fixtures produce an immutable receipt, zero target-aggregate events,
   and no target revision change.
6. A simulated failure before durable decision yields only `InvocationFailure`;
   reconciliation by `command_id` distinguishes not-decided from decided.
7. Event fixtures prove that UUID and timestamp order are insufficient for DAG
   ancestry and that only valid typed parent edges establish lineage.
8. Unknown command, event, and payload versions follow the explicit Decision 4
   compatibility rules rather than coercion or best-effort decoding.

### Remaining unknowns

Decision 7 will define the authority reference, idempotency scope, expected-
revision conflicts, and reason-code taxonomy in detail. Decision 9 will define
provenance and evidence-reference schemas. Decision 10 will define the atomic
durability boundary between receipt, events, and state.

### Recorded disposition

`APPROVE`. The separate `KernelCommand`, `DomainEvent`, `CommandReceipt`, and
`InvocationFailure` contracts, field obligations, outcome baseline, prohibited
interpretations, and acceptance criteria above are binding for the Step 1 freeze.

## Decision 4 — Catalogue, schemas, compatibility, aliases, and unknown types

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approve.` on 2026-08-11

### Coordinator recommendation

`APPROVE` a closed, content-addressed catalogue in which type identity,
versioned schema, compatibility, authorization surface, and transformation rules
are explicit data rather than naming convention or runtime guesswork.

### Type identity and catalogue entries

1. New v4 kernel type IDs use lowercase ASCII dot-separated names:
   `tekroo.command.<domain>.<operation>` for intent and
   `tekroo.event.<domain>.<fact>` for accepted facts. Command and event IDs are
   disjoint; a type ID never changes kind or meaning.
2. Every catalogue entry declares: type ID; semantic schema version; lifecycle
   state; canonical JSON Schema identity and digest; target aggregate kinds;
   required authority class; whether execution attribution is required; allowed
   routing mode; admissible causal-edge kinds; possible emitted event types or
   accepted command sources; compatibility declarations; and owner.
3. Lifecycle state is one of `ACTIVE`, `DEPRECATED`, or `RETIRED`.
   Deprecation retains meaning and requires an explicit replacement or rationale,
   effective contract range, warning reason, and removal gate. Retired input is
   rejected unless an explicitly authorized offline migration/replay mode handles it.
4. Semantic versions describe schema revisions, but SemVer labels do not create
   compatibility by themselves. Compatibility exists only when the catalogue
   declares the exact source/target range and the required fixtures pass.
5. Payload schemas are closed by default (`additionalProperties: false`). A
   type may define a namespaced `extensions` object whose unknown members are
   preserved but have no canonical organizational effect. Promoting extension
   data into meaning requires a new approved schema version.
6. Observed producer, consumer, recipient, frequency, timestamp, or payload
   similarity is evidence for review, not permission, compatibility, aliasing,
   handling, or causality.

### Unknown types and versions

1. Unknown command types or unsupported command versions are retained losslessly
   at the intake/audit boundary, including original bytes or canonical content
   digest, media type, authenticated source, received time, and transport
   provenance. The kernel returns `REJECTED_INVALID` with a stable
   `UNKNOWN_COMMAND_TYPE` or `UNSUPPORTED_COMMAND_VERSION` reason, emits no
   target-aggregate event, and makes no target state change.
2. An unknown event type/version encountered during authoritative replay stops
   that replay before the event is applied. It is quarantined and preserved;
   it is never skipped, interpreted by field similarity, or treated as a no-op.
3. Projections and transports may retain or relay unknown records, but they may
   not derive canonical organizational truth from them.
4. Unknown extension members follow their declared extension policy; they are
   not equivalent to an unknown type or version and cannot affect canonical state.

### Aliases and transformations

1. There are no implicit aliases. Name similarity, payload overlap, frequency,
   or historical co-occurrence cannot establish one.
2. An approved ingress alias is a version-scoped mapping from one exact source
   type/version to one exact canonical type/version plus a pure deterministic
   transformation identified by content digest.
3. Every alias requires semantic evidence, field-level transformation rules,
   loss analysis, effective contract interval, principal approval, positive and
   negative fixtures, deterministic replay, provenance of the original input,
   and a rollback/removal procedure.
4. Alias chains, cycles, wildcard sources, ambiguous targets, context-dependent
   transforms, and transforms that consult mutable external state are prohibited.
5. Canonical events store the canonical type and payload while provenance retains
   the original type/version and raw-input digest. Egress never silently emits a
   deprecated alias.
6. No alias is adopted for `tekroo-agent-chat`, `tekroo-agent-ping`, or
   `tekroo-agent-pong`. Their historical records remain observed-but-undefined
   evidence, not v4 contract definitions.

### Routing and producer/consumer boundaries

1. Exact directed recipients are the default for transport of actor-addressed
   commands or notifications. Role-class, wildcard, fan-out, or broadcast modes
   must be separately declared per type with deterministic recipient-set,
   ownership, join, timeout, and partial-delivery semantics.
2. Catalogue producer/consumer declarations constrain admissibility and
   conformance; they do not replace Decision 7 authorization or allow consumers
   to create organizational truth outside the kernel.
3. A catalogue entry may describe projections or notifications caused by an
   event, but their delivery status is not the event's organizational outcome.

### Evidence calibration

- **COMPUTED FROM FROZEN PHASE 1A SNAPSHOT:**
  `OUTPUT/evidence/observed-message-catalog.json` records 11,236
  `tekroo-agent-chat`, one `tekroo-agent-ping`, and one `tekroo-agent-pong`
  message as `OBSERVED_BUT_UNDEFINED`.
- **COMPUTED / LOW-CONFIDENCE:** The same artifact records ping/pong mappings as
  provisional candidates based only on historical naming similarity.
- **UNKNOWN:** Those observations do not establish intended semantics,
  authorization, required handling, canonical payloads, or aliases.
- **PRINCIPAL-APPROVED:** Phase 1B requires lossless preservation with no
  canonical effect, adopts no historical alias for these names, and requires
  explicit evidence, transform, approval, replay, and rollback for future aliases.
- **COORDINATOR DESIGN RECOMMENDATION:** Dot-separated kind/domain/action IDs and
  closed payload schemas are new v4 conventions; they are not reported as v3 facts.

### Prohibited interpretations

- Frequency or apparent producer/consumer labels define a contract or permission.
- Unknown input is dropped, coerced, best-effort decoded, or applied as a no-op.
- SemVer labels alone prove compatibility.
- Deprecation changes the historical meaning of an accepted type.
- An alias is inferred from spelling or payload similarity.
- A schema accepts arbitrary top-level fields that might silently acquire meaning.
- Role-wide routing silently establishes task ownership.

### Executable acceptance criteria

1. Every catalogue entry resolves to a schema whose digest is in the accepted
   contract manifest; missing, duplicate, cross-kind, or digest-mismatched entries fail.
2. Type-ID fixtures enforce the v4 namespace and disjoint command/event forms.
3. All active versions have positive, boundary, malformed, additional-property,
   and unsupported-version fixtures.
4. Compatibility is accepted only when declared source-to-target fixtures prove
   deterministic validation or transformation; undeclared pairs fail closed.
5. Unknown-command fixtures preserve the original input identity and provenance,
   return the stable invalid reason, emit no event, and leave aggregate state unchanged.
6. Unknown-event replay fixtures stop at the exact event, preserve it, and leave
   state at the last fully understood revision.
7. Alias fixtures prove one-hop acyclicity, exact version scope, deterministic
   byte-equivalent canonical output, original-input provenance, and rollback.
8. Static checks reject catalogue entries or normative schemas containing
   provider-, persistence-, credential-, process-, SMA-, Git-, or delivery-claim
   semantics outside an explicitly approved adapter projection.
9. Routing fixtures prove exact delivery by default and deterministic bounded
   behavior for every separately approved broader routing mode.

### Remaining unknowns

The complete initial command/event inventory will be generated after Decisions
5 through 10 freeze the relevant domain transitions. Transport-only notification
types and public API compatibility remain separate adapter catalogues unless a
later principal decision joins them explicitly.

### Recorded disposition

`APPROVE`. The catalogue identity, lifecycle, compatibility, closed-schema,
unknown-type, alias, routing, producer/consumer, prohibited-interpretation, and
acceptance-test rules above are binding for the Step 1 contract freeze.

## Decision 5 — Immutable DAG lineage and causal-edge validity

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approved.` on 2026-08-11

### Coordinator recommendation

`APPROVE` one immutable organizational event-lineage DAG. Each new event may
declare typed references to already accepted parent events. The stored logical
edge is `parent -> new child`; the child carries the parent reference so no
existing event is mutated.

### Node and edge contract

1. Every node is an accepted `DomainEvent` identified by its immutable `event_id`.
   Commands, receipts, delivery envelopes, provider events, timestamps, and
   external artifacts are not DAG nodes unless a kernel command accepts their
   organizational meaning and emits a domain event.
2. Each parent reference contains exactly `parent_event_id` and one edge kind:
   `CAUSAL`, `RESPONSE`, `RETRY`, `DERIVATION`, or `SUPERSESSION`.
3. The parent must already exist in the same organizational namespace when the
   child is accepted. A parent may belong to another aggregate, but the child's
   catalogue entry must permit that edge kind and parent event type.
4. Parent references are a mathematical set. Duplicate `(edge_kind,
   parent_event_id)` pairs are invalid. Canonical serialization sorts them by
   edge kind and parent ID; their input order has no meaning.
5. A v4 catalogue entry declares a finite maximum parent count. The initial
   contract-wide hard ceiling is 64 parents per event. Larger joins use explicit
   deterministic intermediate join events rather than an unbounded envelope.
6. An event with no parents is a root only when its catalogue entry permits a
   root transition. Missing, unknown, quarantined, cross-namespace, self, or
   disallowed parents reject the command before any event or target-state change.
7. Since an online child can reference only already accepted immutable events,
   the normal commit path cannot introduce a cycle. Offline import validates the
   entire candidate set with deterministic topological ordering before committing
   any imported event; cycles or unresolved parents quarantine the batch.

### Edge semantics

- `CAUSAL`: the parent is an accepted organizational precondition or decision
  that the child explicitly follows. It asserts approved organizational lineage,
  not an unmeasured physical or psychological cause.
- `RESPONSE`: the child is the accepted response to a parent request or question.
  Mere recipient matching or temporal adjacency is insufficient.
- `RETRY`: the child is a new attempt corresponding to a prior attempt event.
  It carries the operation/target identity, prior outcome, retry ordinal, shared
  finite budget, and changed-condition evidence or authorized override required
  by policy. A restart does not reset this lineage or budget.
- `DERIVATION`: the child creates or revises an organizational subject derived
  from the parent, with an explicit derivation rule or evidence reference.
- `SUPERSESSION`: the child replaces the parent's current applicability without
  deleting, rewriting, reversing, or making the parent historically false.

No edge kind grants authority by itself. The command must independently satisfy
catalogue, authorization, state, expected-revision, and evidence requirements.

### Joins, branching, and conceptual loops

1. Branching creates multiple children with a common parent. Each branch has an
   explicit owner, policy, and bounded terminal outcome.
2. A join is a new event referencing every required terminal branch event. Its
   catalogue entry defines the exact required set or deterministic set-selection
   rule, acceptance predicate, timeout policy, partial-result policy, and stable
   outcome. Arrival order cannot affect the result.
3. Planning revisions, validation rounds, retries, escalations, handoffs,
   completion, reopening, and corrections always create new forward-linked
   events. They may revisit a conceptual workflow state but cannot mutate
   history or create a graph back-edge.
4. A `RETRY` or `RESPONSE` edge does not prove progress. Progress and termination
   are explicit accepted outcomes governed by finite policy, not inferred from
   activity, direction, frequency, or graph shape.
5. `SUPERSESSION` changes current applicability only through the child event's
   authorized transition. Consumers reconstruct current state from accepted
   events and policy; they must not delete superseded nodes.

### Aggregate revision and DAG order

Aggregate revision supplies optimistic-concurrency order within one aggregate.
DAG edges supply declared lineage within and across aggregates. Neither UUIDv7,
timestamp, insertion adjacency, correlation ID, message parent fields, nor
aggregate revision alone creates a DAG edge. If a cross-aggregate relationship
matters organizationally, the accepting command must name the parent explicitly.

### Evidence calibration

- **PRINCIPAL-APPROVED:** Phase 1B makes a directed acyclic organizational work
  graph the target, permits causal, response, retry, derivation, and supersession
  edges, and represents iteration with new immutable forward-linked attempts.
- **PRINCIPAL-APPROVED:** Directed messages are preferred, but direction alone
  does not distinguish progress from loops or wasted cycles.
- **PRINCIPAL-APPROVED:** The negative requirements impose finite retry, review,
  planning, fan-out, handoff, escalation, and reopening boundaries.
- **UNKNOWN:** Phase 1A timestamp, recipient, and parent-reference observations do
  not establish true causality, handling, quality, or progress.
- **COORDINATOR DESIGN RECOMMENDATION:** The 64-parent hard ceiling and explicit
  intermediate joins are new boundedness rules, not measured v3 limits.

### Prohibited interpretations

- Timestamp, UUID order, adjacency, correlation, or intended recipient is a DAG edge.
- A child may name an event that is not already accepted on the online commit path.
- Retry, response, supersession, or directed delivery inherently means progress.
- A superseded or corrected event is deleted or rewritten.
- A restart resets retry lineage, owner, ordinal, or budget.
- A join outcome depends on arrival order or iteration order.
- External evidence is treated as an accepted event without a kernel transition.
- A DAG edge substitutes for authorization, expected revision, or state validity.

### Executable acceptance criteria

1. Property tests generate arbitrary accepted DAGs and prove every admitted new
   edge preserves acyclicity, parent existence, namespace, catalogue constraints,
   uniqueness, canonical ordering, and the 64-parent ceiling.
2. Negative fixtures cover self-reference, missing parent, future parent,
   quarantined parent, duplicate edge, disallowed type/kind, cross-namespace
   parent, over-limit fan-in, and cyclic offline batches.
3. Canonical replay produces the same state and join outcome for every
   permutation of sibling arrival or stored parent order.
4. Retry fixtures prove ordinal and budget continuity across redelivery, process
   restart, execution replacement, concurrent duplicate delivery, and escalation.
5. Retry without changed-condition evidence or authorized override is rejected
   when policy requires it; exhaustion emits the stable blocked/escalated result.
6. Supersession and correction fixtures retain the full prior history while
   changing current applicability only through the new accepted event.
7. Join fixtures cover all-success, defined partial acceptance, rejection,
   timeout, duplicate branch result, missing branch, and deterministic
   intermediate joins above the per-event fan-in ceiling.
8. Replay tests prove timestamps, UUID ordering, message adjacency, and
   correlation IDs do not synthesize missing edges.

### Remaining unknowns

Decision 6 will freeze the story/task dependency and ownership state machines
that emit these events. Decision 7 will freeze retry-budget authority and stable
rejection semantics. The retention, query-index, and graph-projection strategy
remains an implementation concern behind Decision 10's repository contracts.

### Recorded disposition

`APPROVE`. The immutable node, typed edge, online/offline acyclicity, bounded
fan-in, branching/join, retry, supersession, canonical ordering, prohibited-
interpretation, and acceptance-test rules above are binding for Step 1.

## Decision 6 — Story, task, ownership, assignment, and reassignment state machines

**Status:** APPROVED BY PRINCIPAL WITH REVISION  
**Principal statement:** `Agreed.` on 2026-08-11

### Coordinator recommendation

`APPROVE` explicit story and task state machines with lifecycle phase, blocked
condition, ownership, dispatch, and dependency state represented separately.
No single status string should conflate these independent meanings.

### Story lifecycle

`StoryPhase` is one of:

1. `DRAFT` — scope is being formed and is not executable;
2. `READY` — scope is authorized for planning;
3. `PLANNING` — bounded decomposition or revision is active;
4. `ACTIVE` — accepted tasks may execute;
5. `COMPLETED` — the kernel has accepted completion for the current lifecycle
   epoch; organizational acceptance or release may still be pending;
6. `ACCEPTED` — the authorized acceptance policy has approved the completed
   story for the current epoch; or
7. `CLOSED` — work ended without acceptance, carrying exactly one disposition:
   `CANCELLED`, `SUPERSEDED`, or `INVALIDATED`.

The ordinary forward path is `DRAFT -> READY -> PLANNING -> ACTIVE ->
COMPLETED -> ACCEPTED`. A catalogue entry may permit an explicit phase skip only
when its command, authority, prerequisites, emitted events, and stable outcome
are separately defined. `CLOSED` is reached only by an authorized explicit
command. Split, merge, and replacement are events and relations that close
predecessors as `SUPERSEDED`; they are not competing status vocabularies.

### Task lifecycle

`TaskPhase` is one of:

1. `PLANNED` — defined under a story but not dependency-ready;
2. `READY` — dependencies and story policy permit ownership or execution;
3. `ACTIVE` — an exact owner has begun the task;
4. `COMPLETED` — kernel completion was accepted for the current lifecycle epoch;
   or
5. `CLOSED` — task ended with `CANCELLED`, `SUPERSEDED`, or `INVALIDATED`.

The ordinary path is `PLANNED -> READY -> ACTIVE -> COMPLETED`. Reopening a
completed or accepted epoch is governed by Decision 8 and never mutates the old
epoch.

### Completion request and review boundary

`RequestCompletion` is an idempotent command carrying completion evidence, not
a story or task phase. While the kernel evaluates it, the subject remains
`ACTIVE`. Synchronous success atomically emits the authoritative `Completed`
event and enters `COMPLETED`; rejection leaves the subject `ACTIVE` and returns
an auditable receipt with stable, actionable reasons.

When validation is asynchronous, the command creates or joins a separate
`CompletionReview` subaggregate and DAG branch. The story or task remains
`ACTIVE`; projections may display `completion_under_review` from the open review
without making that label primary lifecycle state. Duplicate requests with the
same idempotency input return the original receipt or join the same review and
cannot create parallel completion decisions.

### Blocked condition

`WorkCondition` is orthogonal to phase: `RUNNABLE` or `BLOCKED`. Blocking a
nonterminal story or task records one or more typed blocker references, reason,
accountable owner, review/escalation policy, and the event that established the
condition. It does not erase phase, ownership, dependency state, or retry budget.
Unblocking is an explicit transition tied to evidence that a blocker resolved or
to an authorized override. Terminal phases cannot be blocked.

### Dependencies

1. Story and task dependencies are typed subject references backed by accepted
   DAG events, not bare message adjacency.
2. Dependencies within each graph are acyclic and canonically ordered. Creating
   or changing dependencies uses expected revision and cycle validation.
3. A task becomes `READY` only when every required dependency satisfies the
   exact terminal predicate declared by policy. `COMPLETED`, `CLOSED`, and
   `ACCEPTED` are not treated as interchangeable outcomes.
4. Dependency satisfaction emits an explicit readiness event; readers do not
   infer readiness merely by observing rows independently.
5. A story cannot request completion until every required task has reached the
   policy-approved outcome and all required validation joins are terminal.

### Ownership and dispatch

Every ownable subject carries an `Ownership` value:

- `owner_fqn`: one concrete `ActorFQN` or absent;
- `ownership_version`: a monotonic unsigned 64-bit value, initially zero;
- `assigned_by`: authenticated authority reference for the current version;
- `assigned_event_id` and `assigned_at`; and
- append-only ownership history reconstructed from events.

Rules:

1. Dispatch is routing intent and is recorded separately as an exact FQN or an
   explicitly approved selector. Dispatch never establishes ownership.
2. Ownership is established only by an idempotent compare-and-set transition to
   one exact FQN using expected aggregate and ownership versions. At most one
   current owner exists.
3. An actor restart preserves ownership because FQN is durable. Execution death,
   message-claim expiry, redelivery, lease expiry, missing heartbeat, or provider
   status cannot silently release or transfer it.
4. Acquisition, release, handoff, reassignment, and forced reassignment are
   separate commands and events. Each increments `ownership_version`, records
   prior and new owner, authority, reason, evidence, and DAG lineage.
5. Ordinary reassignment requires the expected current owner/version and either
   owner release/handshake or policy-authorized handoff. Forced reassignment
   requires elevated authority and an explicit reason; the displaced execution
   is fenced before the new owner may commit execution-attributed commands.
6. Role classes, wildcard addresses, provider sessions, `ExecutionId`, worktrees,
   delivery claims, and processes cannot own work.
7. Entering `ACTIVE` requires an exact owner. Blocking normally retains that
   owner. Closing or completing work ends current execution authority but does
   not erase ownership history.
8. Ownership creates accountability, not unlimited authorization. The command
   catalogue and Decision 7 still govern which transitions the owner may request.

### Evidence calibration

- **OBSERVED IN PINNED V3 SOURCE:** `mcp/internal/schema/story.go` and `task.go`
  at commit `7630ca20ccfa0fc2f4102147b10c3705e9ba0158` contain story/task lifecycle
  states, dependency fields, and a deliberate distinction between task
  `Assignee` and `DispatchedTo`.
- **UNKNOWN FROM SOURCE ALONE:** Those fields do not prove that every transition,
  dependency, assignment, completion, or reassignment behaved correctly in
  historical execution.
- **PRINCIPAL-APPROVED:** Phase 1B separates dispatch, durable task ownership,
  message-delivery claims, and execution authority; task ownership is exact FQN
  plus versioned append-only history.
- **PRINCIPAL-APPROVED:** Ownership cannot be silently replaced, ordinary work
  cannot continue under a completed revision, and completion is a kernel decision.
- **COORDINATOR DESIGN RECOMMENDATION:** Orthogonal `WorkCondition`, compact
  lifecycle phases, and typed `CLOSED` dispositions replace the overloaded v3
  status vocabulary; this is a v4 design, not a claim about v3 behavior.

### Prohibited interpretations

- Dispatch, delivery claim, execution lease, heartbeat, or provider status is ownership.
- A role class or wildcard owns a story or task.
- Restart, process death, timeout, claim expiry, or redelivery releases ownership.
- Blocking discards lifecycle phase, owner, dependency state, or retry history.
- `COMPLETED`, `ACCEPTED`, `CANCELLED`, `SUPERSEDED`, and `INVALIDATED` are equivalent.
- Split, merge, replacement, or reopening rewrites the predecessor's history.
- A completion request or open completion review is a story/task lifecycle phase.
- Task readiness is inferred from timestamps or eventually consistent row scans.
- Reassignment is implemented as an unversioned overwrite.
- Ownership grants authority to perform any command.

### Executable acceptance criteria

1. Model/property tests enumerate every legal story/task phase transition and
   prove every unlisted transition returns a stable rejection with no state event.
2. Lifecycle, condition, ownership, dispatch, dependency, and execution fields
   vary independently only within their stated invariants.
3. Competing ownership acquisitions using one expected version yield exactly one
   owner and one ownership-version increment; retries return the original receipt.
4. Role-wide dispatch followed by exact-actor acquisition records both values
   without conflation; wildcard or role-class ownership is rejected.
5. Restart, execution replacement, delivery redelivery, claim expiry, and
   heartbeat loss preserve owner FQN and ownership version.
6. Release, handoff, ordinary reassignment, and forced reassignment fixtures prove
   exact version checks, append-only history, required authority, fencing order,
   and deterministic outcomes.
7. Dependency tests cover arbitrary DAGs, cycles, missing subjects, mixed terminal
   outcomes, dependency revision races, deterministic readiness, and blocked dependencies.
8. Blocking/unblocking tests retain phase/owner and require typed resolution
   evidence or authorized override; terminal blocking is rejected.
9. Story completion requests fail until all required task and validation outcomes
   meet the declared policy; row observation alone cannot make a story ready or complete.
10. Synchronous and asynchronous completion fixtures keep the subject `ACTIVE`
    until acceptance, represent an open asynchronous review separately, and
    prevent duplicate requests from creating parallel decisions.
11. Split, merge, replacement, cancellation, and invalidation retain predecessor
    history and explicit successor relations without status ambiguity.

### Remaining unknowns

Decision 7 will define the principal/authority model, ownership-command
authorization, idempotency, and conflict reasons. Decision 8 will freeze
completion, acceptance, rejection, and reopening epochs. Exact planning,
validation, escalation, and release policies remain catalogue entries built on
these state machines rather than additional ad hoc status values.

### Recorded disposition

`APPROVE_WITH_REVISION`. The principal agreed that request and review are not
primary lifecycle states. `COMPLETION_REQUESTED` is removed from story and task
phases. The idempotent `RequestCompletion` command, authoritative `Completed`
event, unchanged-`ACTIVE` rejection behavior, and separate asynchronous
`CompletionReview` branch above are binding with the remaining Decision 6 rules.

## Decision 7 — Authorization, expected revisions, idempotency, and deterministic rejection

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approved.` on 2026-08-11

### Coordinator recommendation

`APPROVE` authenticated authority as a separate kernel contract, exact
preconditions for every write, durable semantic idempotency, and one deterministic
decision order for all command handlers.

### Principal and authority contract

`PrincipalRef` has one kind and one stable kernel identifier:

- `ACTOR` — a concrete `ActorFQN`; execution-attributed commands also require
  the current execution tuple;
- `HUMAN` — an internal stable human identifier mapped from authenticated adapter
  credentials;
- `SERVICE` — an internal stable service identity; or
- `POLICY` — a content-addressed kernel policy identity used for explicit
  internally initiated commands.

Rules:

1. Authentication adapters establish `PrincipalRef`; untrusted command payloads,
   prompts, model text, headers without verification, role-library content, and
   provider session fields cannot assert it.
2. Authorization is evaluated from the accepted command catalogue, target scope,
   current content-addressed policy/grants, principal, actor/ownership context,
   and required execution fencing. A role name or ownership alone grants only
   capabilities the policy explicitly declares.
3. Every decision records the principal, policy/grant identities and versions,
   delegation chain, decision result, and bounded stable reason. Later grant
   change does not rewrite an earlier decision.
4. Delegation is explicit, scope-narrowing, expiry-bounded, revocable, and
   non-transitive unless each hop explicitly permits further delegation. The
   initial maximum chain depth is four. Cycles and privilege expansion are rejected.
5. An internal `POLICY` principal supplies its policy digest, triggering DAG
   parents, and exact scope. It passes the same catalogue, precondition,
   idempotency, and transition validation as external principals; it is not a
   superuser bypass.
6. Authentication failure occurs before a valid `KernelCommand` decision and is
   an `InvocationFailure` plus security audit evidence. `REJECTED_UNAUTHORIZED`
   means an authenticated principal lacked authority for an otherwise identifiable
   command and target scope.

### Exact preconditions and revisions

1. Every existing-aggregate write supplies an exact positive expected aggregate
   revision. Creation supplies `MUST_NOT_EXIST`; no command means “whatever is
   current.”
2. Commands touching multiple aggregates carry a canonically ordered precondition
   vector of `(aggregate_ref, expected_revision_or_absence)`. Every precondition
   must match in one decision or none of the target transitions occur.
3. Ownership mutations additionally supply exact `ownership_version`. DAG parent,
   lifecycle epoch, policy revision, or catalogue revision preconditions are
   explicit when the command depends on them.
4. A conflict receipt may disclose current revisions only when the authenticated
   principal is authorized to read them. The stable outcome and reason remain
   deterministic without leaking protected state.
5. Adapters may fetch a current revision for user convenience, but the kernel
   never silently refreshes and retries a stale command.

### Durable idempotency

1. `command_id` is globally unique and permanently bound to one canonical
   semantic request fingerprint.
2. The idempotency scope is `(contract_manifest, principal_ref, command_type,
   target, idempotency_key)`. The fingerprint covers type/version, target,
   actor/execution attribution, preconditions, DAG parents, payload, and evidence
   references. It excludes transport metadata, `received_at`, `issued_at`, and
   correlation/tracing fields that do not change semantics.
3. The same scope and fingerprint returns the original immutable receipt and
   events without reauthorization, reevaluation, or additional side effects,
   subject to receipt-read authorization.
4. Reuse of an idempotency scope with a different fingerprint, or reuse of a
   `command_id` with different content, returns `REJECTED_CONFLICT` with
   `IDEMPOTENCY_KEY_REUSE` or `COMMAND_ID_REUSE`, records security/audit evidence,
   and makes no target-state change.
5. Concurrent identical submissions are serialized to one decision. All callers
   receive the same canonical receipt.
6. Decided-command identities and receipts are not expired while the accepted
   retention contract permits a retry or replay. Any future compaction must
   preserve an equivalent permanent deduplication tombstone.

Transport retry of the same undecertain invocation reuses the original command
ID, idempotency key, and fingerprint; it does not consume a domain retry budget.
A new domain attempt has a new command ID and `RETRY` edge and consumes the shared
subject/operation policy budget. Actor or process restart cannot reset that budget.

### Deterministic decision order

Every handler evaluates the same observable order:

1. contract/type/version/schema validity;
2. authentication and principal construction;
3. idempotency/command-ID replay or reuse conflict;
4. catalogue and scoped authorization;
5. actor execution tuple and fencing, when required;
6. target visibility/existence and terminal-state checks;
7. exact aggregate, ownership, epoch, DAG, and policy preconditions;
8. domain invariants, retry budgets, and command-specific policy; then
9. one atomic durable decision.

The first failing category supplies the stable receipt outcome and reason. Within
a category, catalogue-defined reason precedence is canonical. An authenticated
principal that cannot read a target receives the non-disclosing unauthorized
outcome rather than existence information.

Rejected commands emit no target-aggregate domain event and do not advance target
revision. Their receipts and authorization/audit evidence remain immutable.
Retry-budget exhaustion produces a policy-defined blocked or escalated event only
through its own authorized transition; it is not hidden as repeated rejection.

### Baseline reason families

- `REJECTED_INVALID`: malformed schema, unknown type/version, invalid or missing
  required precondition, or invalid identifier;
- `REJECTED_UNAUTHORIZED`: authenticated principal lacks scoped authority;
- `REJECTED_NOT_FOUND`: authorized principal may know the target is absent;
- `REJECTED_CONFLICT`: revision/ownership mismatch, idempotency/command-ID reuse,
  or competing accepted transition;
- `REJECTED_STALE_EXECUTION`: actor execution tuple is no longer authoritative;
- `REJECTED_CLOSED`: operation is prohibited for the current terminal/closed epoch;
- `REJECTED_POLICY`: valid and authorized shape violates a domain policy or
  exhausted bounded policy; and
- `NO_CHANGE`: the authorized command is valid but its explicitly idempotent
  domain semantics require no new event.

Reason codes beneath these families are closed, catalogue-versioned machine
values. Human detail is bounded diagnostic text and never controls behavior.

### Evidence calibration

- **PRINCIPAL-APPROVED:** Phase 1B requires explicit organizational authority,
  expected durable transitions, FQN/execution fencing, deterministic release,
  finite retry, no silent ownership replacement, and observable failures.
- **PRINCIPAL-APPROVED:** Cancellation and timeout do not prove rollback; durable
  idempotency and reconciliation are required.
- **OBSERVED IN PINNED V3 SOURCE:** Mongo claim epochs, held-claim compare-and-set,
  lifecycle transition guards, and author-derived identity provide implementation
  examples of fencing and atomic guards, but they are not the v4 authority model.
- **UNKNOWN:** Phase 1A cannot prove historical authorization correctness or that
  all retries shared one durable budget.
- **COORDINATOR DESIGN RECOMMENDATION:** Four principal kinds, delegation depth
  four, the semantic fingerprint, and the evaluation order are new v4 contract choices.

### Prohibited interpretations

- A prompt, role name, provider session, payload field, or ownership alone grants authority.
- An adapter may silently fetch a new revision and retry changed intent.
- Revocation rewrites the historical authority decision for a committed event.
- An internal policy bypasses normal command validation.
- A timeout, cancellation, or lost response means the command failed.
- The same idempotency key may identify semantically different commands.
- Actor restart, process restart, or message redelivery resets retry budget.
- A rejection mutates target state or advances target revision.
- Free-form text, map iteration, handler ordering accident, or storage error selects an outcome.

### Executable acceptance criteria

1. Authorization matrices cover every principal kind, catalogue command,
   ownership relation, target scope, delegation path, policy version, revocation,
   expiry, and execution-attribution requirement.
2. Negative fixtures prove payload/header/provider spoofing cannot establish principal identity.
3. Delegation property tests prove scope only narrows, depth is bounded, cycles
   fail, expiry/revocation is enforced, and no implicit transitivity exists.
4. Revision tests cover exact match, stale/future/zero/missing revisions,
   `MUST_NOT_EXIST`, ownership versions, multi-aggregate all-or-none behavior,
   and non-disclosing conflicts.
5. Idempotency tests cover sequential/concurrent duplicates, same key with changed
   content, same command ID with changed content, transport metadata differences,
   original receipt replay, crash boundaries, and deduplication tombstones.
6. Every handler passes a shared outcome-precedence conformance suite; insertion,
   map, storage, and sibling-arrival order cannot change the receipt.
7. Failure injection before decision yields `InvocationFailure`; failure after
   an atomic decision is reconciled by command ID to the original receipt.
8. Rejections produce no target event/revision change while preserving bounded
   receipt and audit evidence.
9. Retry tests distinguish transport reconciliation from new domain attempts and
   prove shared budget continuity across actor/process restarts and concurrency.
10. Receipt-read authorization prevents idempotency replay from disclosing prior
    results to an unauthorized principal.

### Remaining unknowns

The authentication provider, credential format, grant administration UI,
policy language, receipt-retention duration, and storage implementation are not
selected here. Decision 10 will freeze the atomic repository obligations that
make these semantics durable.

### Recorded disposition

`APPROVE`. The principal types, authenticated authority boundary, delegation,
exact preconditions, idempotency scope/fingerprint, deterministic decision order,
reason families, retry distinction, prohibited interpretations, and acceptance
criteria above are binding for the Step 1 contract freeze.

## Decision 8 — Completion, acceptance, reopening, successors, and correction

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approved.` on 2026-08-11

### Coordinator recommendation

`APPROVE` completion as a kernel decision about one exact lifecycle epoch,
acceptance as a later and separately authorized decision, and all post-terminal
work as an explicit reopening or successor/correction path.

### Lifecycle epochs

1. Every story and task begins at `lifecycle_epoch = 1`. The epoch is part of
   every lifecycle command precondition and event.
2. `Completed` closes execution of the current epoch. `Accepted` approves a
   completed story epoch under its acceptance policy. Neither deletes ownership,
   evidence, review, release, or prior-epoch history.
3. A command against a completed, accepted, or closed old epoch receives the
   deterministic closed/stale-epoch outcome unless it is an explicitly
   authorized reopening, successor, correction, or evidence-attachment command.
4. Timestamp, message arrival, provider status, process exit, tool success,
   model assertion, PR creation, or apparent inactivity cannot complete or accept
   an epoch.

### Requesting and deciding completion

1. `RequestCompletion` names the exact subject, lifecycle epoch, aggregate
   revision, owner/authority, execution tuple when actor-attributed, acceptance-
   criteria revision, evidence set, produced artifacts/change identities,
   validation results, unresolved exceptions, and idempotency input.
2. The command is admissible only from `ACTIVE`. The kernel validates ownership
   and authority, dependencies, required DAG joins, retry/validation bounds,
   catalogue policy, evidence presence and identity, and exact current revision.
3. Synchronous acceptance emits one authoritative `Completed` event and enters
   `COMPLETED` atomically. Synchronous rejection emits no target event, leaves the
   subject `ACTIVE`, and returns stable actionable reason codes.
4. If review is asynchronous, `RequestCompletion` creates or joins one
   `CompletionReview` subaggregate keyed by subject, epoch, criteria revision,
   and evidence-set digest. The subject remains `ACTIVE`.
5. An asynchronous review result is evidence. Before the kernel emits
   `Completed`, it revalidates the subject's current epoch/revision, required
   dependencies, review set, and policy. A passing stale review cannot complete
   changed work.
6. One open review may have bounded deterministic validation branches. Their join
   rule, owners, deadlines, partial-result policy, and required evidence are
   catalogue-defined. Duplicate or reordered results cannot change the outcome.
7. Review rejection closes that review with stable reasons and leaves the subject
   `ACTIVE`. New evidence starts a new explicitly linked review attempt; it does
   not rewrite the rejected review.

### Story completion and acceptance

1. A story completion decision requires every required task and validation branch
   to have the exact policy-approved terminal outcome. Cancelled, invalidated,
   superseded, completed, and missing work are never treated as interchangeable.
2. `COMPLETED` means the kernel accepted completion evidence for the story epoch.
   It does not by itself mean product acceptance, merge, deployment, billing,
   customer delivery, or production success.
3. `RequestAcceptance` applies only to an exact `COMPLETED` story epoch and names
   the acceptance-policy revision, authorized acceptor, required evidence and
   gates, and expected aggregate revision.
4. Acceptance emits `Accepted` only when every required acceptance predicate is
   verified. When policy requires repository changes, every required merge and
   exact synthesized-tree qualification must be authoritatively reconciled before
   acceptance. Timeout or ambiguous provider state cannot count as acceptance.
5. Rejected acceptance leaves the story `COMPLETED` and records an auditable
   receipt or separate review result. Remedial work requires explicit reopening
   or a successor; rejection does not silently move the story backward.

### Reopening and successor work

1. `ReopenWork` is a distinct elevated-authority command with exact old epoch and
   revision, reason, changed scope/criteria, evidence, ownership disposition,
   policy revision, and idempotency input.
2. An approved same-subject reopen increments `lifecycle_epoch`, creates a new
   forward-linked `Reopened` event, and enters `ACTIVE` under the new epoch. The
   prior completed/accepted epoch remains immutable and addressable.
3. Reopening from `COMPLETED` is permitted when policy authorizes remedial work.
   Reopening an `ACCEPTED` or externally released epoch requires elevated policy
   and explicit impact/reconciliation evidence; the default is a successor or
   correction subject so accepted history remains operationally stable.
4. `CreateSuccessor` creates a new subject identity with `DERIVATION` or
   `SUPERSESSION` lineage and leaves the predecessor terminal. Split and merge
   produce explicit successor sets and deterministic ownership/dependency rules.
5. Reopening never resets retry, validation, audit, release, or ownership history.
   Policy declares whether current owner carries forward; absent an explicit rule,
   the new epoch begins unowned.

### Corrections and late evidence

1. An accepted event is never edited or deleted. `CorrectRecord` emits a new
   correction event with exact target event, corrected fields/meaning, authority,
   reason, evidence, and `SUPERSESSION` lineage. Consumers retain both and derive
   current applicability through policy.
2. Correction cannot change the original event ID, command receipt, original
   payload digest, authority record, or audit provenance.
3. Late output from an old/fenced execution or closed epoch may be retained as
   raw evidence under sensitivity/retention policy. It has no canonical effect
   until an authorized attach, reopen, successor, or correction command accepts
   a specific organizational use.
4. A late `RequestCompletion` cannot be redirected to a newer epoch or successor.
   It is rejected against its named epoch; the caller must submit new intent with
   explicit lineage.

### Evidence calibration

- **PRINCIPAL-APPROVED:** Phase 1B states that `CompletionRequested` is evidence
  from an actor or adapter and `Completed` is emitted only by the kernel after an
  authoritative transition.
- **PRINCIPAL-APPROVED:** Completion closes a revision or lifecycle epoch; later
  work is late evidence, authorized linked reopening, successor/correction, or
  rejection against a closed revision.
- **PRINCIPAL-APPROVED:** Organizational acceptance follows only after required
  merges are authoritatively verified, with deterministic synthesized-tree gates.
- **PRINCIPAL-APPROVED IN DECISION 6:** Completion request/review is not a primary
  story/task phase; asynchronous review is a separate branch.
- **UNKNOWN:** Historical completion messages and snapshot non-completion do not
  prove acceptance, abandonment, quality, merge success, or causal handling.
- **COORDINATOR DESIGN RECOMMENDATION:** Explicit lifecycle epochs, one open
  review key, and default-successor treatment after accepted/released work are new v4 rules.

### Prohibited interpretations

- An actor/provider/model/tool can emit authoritative `Completed` or `Accepted`.
- A passing review against an old revision completes changed work.
- `COMPLETED` implies acceptance, merge, deployment, or customer outcome.
- Acceptance rejection silently returns a story to active work.
- Reopening overwrites or reclassifies the prior epoch.
- Accepted/released work is reopened by default rather than successor/correction.
- Retry, review, ownership, or release history resets on reopening.
- Late evidence is silently attached to a newer epoch.
- Correction mutates the original event or receipt.

### Executable acceptance criteria

1. Completion fixtures cover exact owner/authority, fencing, epoch, revision,
   criteria revision, dependencies, evidence identity, validation joins, and
   unresolved exceptions; every failed precondition leaves the subject `ACTIVE`.
2. Synchronous acceptance emits exactly one `Completed`; duplicate commands
   return the original receipt and emit no additional event.
3. Asynchronous fixtures prove one review per key, bounded deterministic branches,
   order-independent joins, stale-review revalidation, rejection history, and
   new-evidence review lineage.
4. Story completion tests distinguish every task terminal disposition and reject
   missing, ambiguous, or policy-disallowed outcomes.
5. Acceptance fixtures cover authorized acceptor, policy revision, exact tree and
   merge reconciliation when required, partial merge, provider timeout,
   ambiguous result, gate failure, and duplicate acceptance.
6. Acceptance rejection retains `COMPLETED`; remedial execution fails until a
   separate reopening or successor transition succeeds.
7. Reopening tests increment epoch exactly once, preserve all prior history,
   require elevated authority where specified, apply explicit owner carry-forward,
   and reject old-epoch commands afterward.
8. Successor, split, and merge fixtures prove immutable predecessor state,
   explicit DAG relations, deterministic successor sets, and no identity reuse.
9. Correction fixtures retain original bytes/digests and receipts, apply one
   authorized superseding event, and replay to deterministic current applicability.
10. Late/fenced evidence fixtures prove storage without organizational effect and
    prohibit automatic redirection to a newer epoch or successor.

### Remaining unknowns

Exact acceptance policies vary by work type and remain versioned catalogue data.
The Git/provider release protocol is constrained by Phase 1B but will be specified
in a later implementation slice; this decision freezes only what the kernel must
require before it emits organizational acceptance.

### Recorded disposition

`APPROVE`. Lifecycle epochs, completion/review revalidation, story acceptance,
reopening/successor defaults, immutable correction, late-evidence behavior,
prohibited interpretations, and acceptance criteria above are binding for Step 1.

## Decision 9 — Evidence, provenance, audit, and source/runtime identity

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approved.` on 2026-08-11

### Coordinator recommendation

`APPROVE` content-addressed evidence records, explicit claim classification,
complete decision provenance, and a rebuildable audit projection. Evidence is an
input to policy; it is never self-authorizing organizational truth.

### Evidence record

Every registered `EvidenceRecord` contains:

1. `evidence_id`: immutable canonical UUIDv7;
2. `evidence_kind`: closed catalogue value such as source snapshot/diff, test or
   tool result, artifact, provider receipt, model output, human attestation,
   decision record, or external observation;
3. exact raw `sha256`, byte length, media type, and optional canonical-content
   digest when a declared deterministic canonicalizer exists;
4. immutable or version-pinned locator plus availability state; a mutable URL or
   path alone is not evidence identity;
5. authenticated registering principal, producing component and version, source
   timestamp when supplied, kernel ingestion timestamp, and transport provenance;
6. sensitivity class, access partition, retention policy, redaction lineage, and
   legal/authorized deletion state;
7. zero or more source evidence IDs and, for a computed result, exact method,
   code/build identity, parameters/configuration digest, and deterministic/non-
   deterministic declaration; and
8. integrity state separate from semantic assessment. Digest verification proves
   byte identity, not truth, causality, correctness, or admissibility.

Content deduplication may reuse bytes but may not merge provenance: identical
content obtained from two origins remains two evidence registrations referencing
the same content digest. If raw bytes are unavailable, the record says so and no
caller may claim they were verified.

### Claim classification and assessment

Any machine- or human-readable claim used to justify a command or decision has a
`ClaimAssessment` with one class:

- `OBSERVED` — directly present in identified raw evidence;
- `COMPUTED` — produced by an identified method from identified inputs;
- `INFERRED` — interpretation not directly established by raw evidence;
- `DECIDED` — an authorized organizational or principal decision, not an
  empirical observation; or
- `UNKNOWN` — evidence is absent, insufficient, contradictory, or unavailable.

The assessment records the precise claim, supporting and contradicting evidence
IDs, method when applicable, assessor principal/component, scope, and time. It
does not contain a generic confidence score that can silently convert inference
into observation. Strengthening or reversing an assessment creates a new linked
assessment; the old one remains.

Quantitative, causal, mechanistic, and tail-event claims require references to the
specific raw evidence and computation receipt that support them. Aggregate or
derived evidence cannot be silently substituted for an explicitly required raw
source. Unsupported claims remain `INFERRED` or `UNKNOWN`.

### Source, overlay, build, and runtime identity

The following identities are separate and cannot substitute for one another:

1. `SourceIdentity`: repository/namespace, exact commit, source-tree digest, and
   subpath/scope;
2. `OverlayIdentity`: ordered file-level additions/modifications/deletions and
   digest relative to one source identity;
3. `BuildIdentity`: source plus overlay, dependency lock digest, toolchain,
   build definition, generated inputs, and resulting artifact digest;
4. `RuntimeIdentity`: exact build artifact, contract manifest, configuration
   digest, role-library digests, container/host environment identity, enabled
   capabilities, and relevant provider/model identities; and
5. `ExecutionIdentity`: Decision 2 actor FQN, execution ID, fencing epoch, and
   runtime identity actually launched.

A clean commit does not identify a dirty overlay or running process. A source
checkout does not prove what binary ran. A model name does not identify weights,
quantization, provider behavior, prompt/context, or runtime configuration.

### Event and receipt provenance

Every `DomainEvent` and `CommandReceipt` records or content-addresses:

- contract manifest and catalogue entry/version;
- command ID and semantic request digest;
- authenticated principal, applicable delegation, actor FQN, and execution tuple;
- authorization policy/grant identities and decision;
- aggregate/lifecycle/ownership revisions and DAG parents;
- input evidence and claim-assessment IDs;
- deciding kernel component/build/runtime identity;
- kernel received/decided/committed timestamps; and
- canonical payload/receipt digest.

Tracing IDs, logs, message IDs, and provider session IDs may be linked operational
provenance but cannot replace the authoritative fields above or establish causality.

### Audit boundary

1. The immutable command receipts, domain events, evidence registrations,
   authority decisions, and lifecycle/ownership/release records form the audit
   source. Human views and query indexes are deterministic rebuildable projections.
2. Rejected commands and pre-decision authentication/transport failures retain
   appropriately partitioned audit/security evidence without creating target events.
3. Every projection identifies its exact input position, projector build, schema,
   and digest. Projection lag or failure is observable and cannot alter source truth.
4. The bootstrap requires content digests, immutable-storage controls, access
   logging, backup/restore verification, and mutation detection. A stronger
   cryptographic chain or external transparency log remains a later security
   decision and is not claimed here.
5. Redaction creates a derived record with explicit transformation and lineage;
   it never overwrites raw evidence. Authorized/legal deletion leaves a tombstone
   containing identity, digest, provenance, authority, deletion basis, and time,
   while truthfully marking raw content unavailable.

### SMA and provider boundary

SMA capture and recalled context are outside the organizational audit source by
default. An authorized command may register a particular memory record or recall
as evidence with its SMA record identity, source lineage, retrieval parameters,
and non-authoritative classification. Observation by the SMA proxy does not
automatically attach memory to a task, prove a claim, authorize a transition, or
measure organizational outcome.

Provider events, model output, tool output, and shell exit are likewise evidence.
They gain organizational meaning only through the exact kernel command and policy
that accepts a stated use. Current authorized instructions and direct current
source/test/runtime evidence outrank contradictory recollection.

### Evidence calibration

- **PRINCIPAL-APPROVED:** Phase 1B makes evidence/provenance part of every
  organizational decision and keeps provider/SMA output non-authoritative.
- **PRINCIPAL-APPROVED:** SMA is semantic memory, not process control, task audit,
  acceptance, organizational authority, or outcome measurement.
- **OBSERVED IN PHASE 1A:** The accepted archaeology retains separate committed
  SMA source, overlay, and runtime snapshot identities and documents material
  evidence gaps rather than filling them by inference.
- **UNKNOWN:** Historical source attribution, human intervention, review outcome,
  tool results, source diffs, costs, and causal handling are unavailable for many
  events and remain unknown.
- **COORDINATOR DESIGN RECOMMENDATION:** The evidence/assessment schemas and five
  separate source-through-execution identities are new v4 contract structures.

### Prohibited interpretations

- A digest proves semantic truth, correctness, causality, or authorization.
- A mutable locator, filename, commit label, model name, or provider session is
  sufficient evidence identity.
- A computed aggregate substitutes for specifically required raw evidence.
- Confidence or repetition upgrades inference into observation.
- Same bytes from different origins collapse into one provenance record.
- An audit projection becomes the source of organizational truth.
- Redaction silently overwrites raw evidence or deletion pretends bytes remain available.
- SMA observation/recall, provider status, model output, tool output, or shell exit
  automatically enters the organizational record or decides an outcome.
- Current runtime behavior is attributed to source that was not directly identified.

### Executable acceptance criteria

1. Evidence fixtures verify raw and canonical digests, size/media identity,
   immutable locator rules, availability, producer, timestamps, sensitivity,
   retention, and integrity/assessment separation.
2. Mutation, missing-byte, mutable-locator, digest-mismatch, oversized-input,
   access-partition, and unauthorized-registration cases fail observably.
3. Computed-evidence tests require complete input IDs, method/build/config digest,
   determinism declaration, and reproducible receipt; absent raw support cannot be
   classified `OBSERVED`.
4. Same-content/different-origin tests preserve distinct evidence IDs and provenance.
5. Claim-assessment tests retain supporting/contradicting evidence, forbid silent
   class strengthening, and link revisions without overwriting history.
6. Source/overlay/build/runtime/execution fixtures prove none can satisfy a field
   requiring another and that exact launched runtime identity is recoverable.
7. Every event/receipt schema and behavior fixture includes the required
   provenance and produces stable canonical digests.
8. Audit rebuild from the immutable sources is deterministic; projection
   interruption/resume, lag, corruption, and rebuild cannot alter source records.
9. Redaction and deletion fixtures preserve truthful lineage/tombstones, enforce
   access policy, and never report unavailable raw bytes as verified.
10. SMA/provider/model/tool evidence fixtures prove no organizational effect
    occurs without an authorized accepting command and current-policy validation.

### Remaining unknowns

Evidence blob storage, encryption and key management, retention durations,
transparency-log adoption, privacy classifications, and organizational access
roles require later security/operations decisions. This contract fixes the
semantic and integrity obligations those implementations must satisfy.

### Recorded disposition

`APPROVE`. Evidence identity/provenance, claim classification, source-through-
execution identity separation, event/receipt provenance, audit projection,
redaction/deletion truthfulness, SMA/provider boundaries, prohibited
interpretations, and acceptance criteria above are binding for Step 1.

## Decision 10 — Kernel ports, repository boundaries, and transaction semantics

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approve.` on 2026-08-11

### Coordinator recommendation

`APPROVE` a pure deterministic kernel surrounded by domain-specific ports, with
MongoDB as the approved organizational state/event store and change-stream
wakeup substrate. Preserve Mongo's cohesive transaction, outbox, claim, lease,
resume, and recovery mechanics; do not hide them behind generic CRUD or a
replace-any-database abstraction.

### Pure decision boundary

1. The kernel evaluator accepts a validated `KernelCommand`, exact aggregate and
   policy/catalogue snapshots, trusted decision context, and referenced evidence
   metadata. It returns either a proposed atomic `Decision`—new aggregate states,
   ordered events, receipt, authority/audit record, and outbox intents—or a stable
   rejected receipt.
2. The evaluator performs no network, filesystem, process, provider, model,
   database, wall-clock, random-ID, or message-delivery I/O. Clock and ID values
   arrive through explicit trusted decision context and deterministic test ports.
3. Domain policy cannot call MongoDB or an external provider during evaluation.
   Required external observations must already be registered evidence or explicit
   preconditions. External work is scheduled only after the organizational
   decision commits.
4. One input snapshot and deterministic context produce one canonical proposed
   decision. Map iteration, sibling arrival, repository ordering, or retry timing
   cannot alter it.

### Domain-specific ports

The application layer uses narrow semantic ports:

- `KernelDecisionStore` loads exact versioned aggregate/policy/catalogue snapshots,
  looks up commands/idempotency receipts, and atomically commits one proposed decision;
- `EvidenceStore` registers and resolves content-addressed evidence metadata and
  availability without treating blobs as domain objects;
- `CommittedIntentFeed` exposes committed outbox intents and their delivery
  claim/lease lifecycle;
- `ExecutionRegistryStore` performs FQN/execution fencing transitions; and
- deterministic `Clock` and `IdSource` test ports supply trusted context.

These are behavioral contracts, not generic repositories. They expose no generic
query language, table/collection abstraction, arbitrary document writes, or
provider object. Read-optimized Mongo queries and indexes remain adapter-specific
and may be used directly where they preserve the semantic contract.

### Atomic organizational decision

For one command, the MongoDB adapter commits in one majority-acknowledged
transaction:

1. exact aggregate, ownership, lifecycle-epoch, catalogue, policy, execution,
   DAG, and idempotency preconditions;
2. new aggregate snapshot(s) and revisions;
3. immutable ordered domain events;
4. the immutable command receipt and authorization/audit decision;
5. referenced evidence-registration changes when part of the command; and
6. transactional outbox intents for every required post-commit effect.

All listed writes commit or none do. Rejected commands write their receipt and
audit evidence without target state/events. Unique indexes enforce command ID,
idempotency scope, event ID, and `(aggregate, revision)` uniqueness. Multi-
aggregate commands are catalogue-bounded and satisfy all preconditions in the
same transaction; no partial organizational decision is exposed.

If commit acknowledgement is lost or cancellation arrives during commit, the
result is uncertain—not failure. The application reconciles by command ID and
returns the stored receipt if committed. It never repeats external effects based
only on timeout. A storage/infrastructure failure before durable decision is an
`InvocationFailure` and completes observably under a timeout.

Aggregate state, events, receipts, and authority records are mutually
verifiable. A deterministic fold of accepted events must reproduce the semantic
aggregate state or detect corruption. The materialized snapshot is the optimized
current-state representation; it cannot contradict the immutable transition
history. Repair is an explicit verified operation, not silent projection overwrite.

### MongoDB and change-stream delivery

1. MongoDB is the approved v4 organizational state, event, receipt, audit
   metadata, registry, and outbox store. Production topology must support the
   required transactions, majority durability, and change streams and must fail
   startup validation when it does not.
2. Each post-commit intent is an immutable outbox record. Its delivery metadata—
   routing target, claim holder, claim epoch, lease, attempts, resolution,
   readdress history, and dead-letter state—is transport state and never task
   ownership or a domain event.
3. Consumers open the relevant change stream before draining the matching
   committed backlog, then process both paths through one idempotent claim
   primitive. This closes the observation gap without replacing change streams
   with polling.
4. Claim acquisition is an atomic one-winner compare-and-set that increments a
   monotonic claim epoch. Extend, resolve, yield, readdress, sweep, and recovery
   operations pin the observed holder and epoch. Stale holders fail deterministically.
5. Delivery is at least once. Exactly-once organizational effect comes from
   command ID, semantic idempotency, expected revisions, and fencing—not from a
   claim that a message can be delivered exactly once.
6. Resume tokens and consumer checkpoints are durable operational cursors, not
   organizational ordering or causality. Resume failure triggers an explicit
   bounded backlog resynchronization and deduplication by immutable intent ID.
7. The event/intent feed guarantees no global causal total order. Aggregate
   revision and approved DAG edges remain authoritative. Consumers tolerate
   duplicates and permitted cross-aggregate reordering.
8. Sweep, lease, retry, dead-letter, and readdress policies are finite,
   observable, catalogue/version controlled, and cannot alter task ownership.

The transactional outbox is a MongoDB collection committed with the decision,
not adoption of a separate generic queue. Change streams remain the efficient
wakeup path; backlog queries provide recovery and startup completeness.

### Post-commit effects and external systems

1. Effect handlers receive only committed outbox intents and exact idempotency
   identities. They cannot edit the initiating event or receipt.
2. External effects use provider-specific idempotency where available and always
   record attempt, observation, and reconciliation evidence. Cancellation is
   best-effort and never means rollback.
3. An effect success may submit a new kernel command carrying provider evidence;
   it cannot directly mutate organizational state.
4. Ambiguous effects remain pending reconciliation with finite escalation. They
   cannot be silently retried into duplicate merges, launches, notifications, or
   other irreversible operations.
5. Post-commit handler failure is visible and retryable without reversing the
   accepted decision or duplicating its domain events.

### Failure and operational contract

- Every asynchronous store/feed/effect operation completes successfully or
  exceptionally under a deadline.
- Recoverable errors—transient connectivity, resumable stream interruption,
  write conflict, lease expiry—are distinguished from terminal configuration,
  schema/digest mismatch, invariant corruption, authorization, and unsupported-
  topology failures.
- Invalid or contradictory state stops the affected transition and requests
  explicit refresh/reconciliation; it is never carried forward silently.
- Startup verifies contract manifest, catalogue/schema digests, Mongo topology,
  required indexes/unique constraints, write/read concerns, and migration level
  before serving commands or opening delivery.
- Transaction, event, aggregate, and outbox sizes have finite catalogue/manifest
  limits. Exceeding a limit is a stable validation failure, not an unbounded commit.

### Evidence calibration

- **OBSERVED IN PINNED V3 SOURCE:** At commit
  `7630ca20ccfa0fc2f4102147b10c3705e9ba0158`,
  `mcp/internal/transport/subscriber.go` opens the change stream before backlog
  drain; transport code uses atomic claim updates, claim epochs, leases, resume
  tokens, sweep, readdress, and dead-letter mechanics with integration tests.
- **PRINCIPAL OPERATIONAL EVIDENCE:** The MongoDB change-stream wakeup is
  considered efficient, lightweight, and one of the mechanisms that works well.
- **PRINCIPAL-APPROVED:** Phase 1B preserves MongoDB, change-stream wakeup,
  stream-before-backlog ordering, direct optimized access, atomic one-winner
  claims, epochs, leases, bounded resume, sweeps, readdress, and dead letters.
- **PRINCIPAL-APPROVED:** No polling, generic queue, or generic database
  abstraction replaces this path without measured correctness/performance evidence.
- **UNKNOWN:** No v4 implementation or workload measurement yet proves the new
  transaction layout, indexes, latency, throughput, allocation, or recovery behavior.
- **COORDINATOR DESIGN RECOMMENDATION:** Pure evaluation, the named semantic
  ports, atomic decision tuple, deterministic fold check, and Mongo transactional
  outbox are v4 contract choices.

### Prohibited interpretations

- A generic repository/queue abstraction may erase Mongo transaction, change-
  stream, claim, lease, resume, or optimized-query semantics.
- Delivery claim, lease, resume token, or outbox status is task ownership or domain state.
- A change-stream cursor or insertion order defines organizational causality.
- Timeout/cancellation proves commit failure or external rollback.
- An effect handler directly updates organizational state.
- At-least-once delivery is described as exactly-once transport.
- A state snapshot may silently diverge from immutable events/receipts.
- A storage retry silently reevaluates a command against a newer revision.
- Post-commit failure reverses an accepted transition.
- Polling is introduced as the ordinary wakeup path without a separately approved measured case.

### Executable acceptance criteria

1. Pure-kernel tests prove deterministic decisions under fake clock/IDs and
   reject any hidden I/O or ordering dependency.
2. Transaction failure injection at every write/ack boundary proves all-or-none
   state/event/receipt/audit/outbox commits and reconciliation by command ID.
3. Concurrency tests prove unique command/idempotency/event/revision constraints,
   one winner, canonical conflict receipts, and no silent reevaluation.
4. Event-fold verification reconstructs semantic aggregate state and detects
   missing, duplicate, reordered-within-aggregate, corrupted, or mismatched records.
5. Mongo integration tests verify supported topology, majority durability,
   required indexes, startup failure, transaction bounds, and recovery after restart.
6. Change-stream tests cover stream-open-before-backlog, the boundary race,
   duplicate stream/backlog observation, resumable interruption, invalid resume
   token, bounded backlog resync, consumer restart, and cross-aggregate reordering.
7. Claim tests cover concurrent acquisition, epoch fencing, extend/resolve/yield,
   lease expiry, sweep, readdress, dead letter, stale holder, and same-FQN restart.
8. Delivery tests prove claims and failures never acquire, release, or transfer
   task ownership and that duplicate delivery creates at most one domain decision.
9. External-effect tests cover idempotent success, timeout before/after effect,
   ambiguous result, cancellation, partial result, reconciliation, finite retry,
   escalation, and crash after provider success before local observation.
10. Projection/store corruption and post-commit handler failure remain observable,
    repairable, and unable to rewrite accepted source records.

### Remaining unknowns

Exact Mongo collection/index layouts, transaction/record size ceilings, lease and
retry durations, deployment topology, sharding strategy, blob store, backup
product, and performance budgets require implementation design and measurement.
They may not weaken the semantic, durability, wakeup, and recovery contracts above.

### Recorded disposition

`APPROVE`. Pure evaluation, semantic ports, atomic Mongo decisions, state/event
verification, transactional outbox, change-stream wakeup, claim/lease fencing,
external-effect reconciliation, failure semantics, prohibited interpretations,
and acceptance criteria above are binding for Step 1.

## Decision 11 — Executable conformance suites, fixtures, and property invariants

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approved.` on 2026-08-11

### Coordinator recommendation

`APPROVE` a language-neutral, content-addressed conformance corpus as the
executable contract. Implementations qualify against the same fixtures and
invariants; their own unit tests are useful but cannot substitute for this corpus.

### Conformance layers

1. **Schema and canonicalization:** validate every contract document, reject
   malformed/unknown/noncanonical forms, and prove parse-render-parse equality.
2. **Example fixtures:** canonical `given / when / then` scenarios with exact
   inputs, pre-state, command, decision context, expected receipt, ordered events,
   post-state, audit/provenance, and stable canonical digests.
3. **Transition-model tests:** enumerate every legal story, task, ownership,
   lifecycle-epoch, completion, acceptance, review, and closed-state transition;
   every unlisted transition must reject deterministically.
4. **Property/model tests:** generate bounded arbitrary command histories and
   compare the implementation with a deliberately small reference state model.
5. **Repository adapter suite:** run identical decision cases against the
   in-memory reference adapter and MongoDB adapter, including optimistic
   concurrency, atomicity, idempotency, restart, and replay.
6. **Delivery/recovery suite:** exercise Mongo change streams, backlog boundary,
   claims, epochs, leases, resume, redelivery, dead letter, readdress, crash, and
   recovery without conflating transport with ownership.
7. **External-effect simulator:** use deterministic fake providers to exercise
   success, failure, timeout, cancellation, ambiguity, partial effect,
   reconciliation, and escalation. Real paid/provider E2E remains a separate
   explicitly authorized gate.

### Normative fixture contract

Every fixture has:

- immutable fixture ID, contract-manifest identity, catalogue/schema versions,
  source decision IDs, and fixture digest;
- evidence classification: normative example, boundary case, regression receipt,
  generated minimal counterexample, or fault-injection scenario;
- complete deterministic inputs, fake clock/ID sequence, initial state/events,
  command, evidence, authority/policy/catalogue snapshots, and fault schedule;
- exact expected `PASS`, `FAIL`, `NOT_RUN`, or `INCONCLUSIVE` status plus stable
  receipt, events, state, provenance, and observable error/failure boundary; and
- prohibited variation: fields/orderings that may vary are explicitly declared;
  everything else compares canonically.

`PASS` means all required assertions executed and matched. A skipped prerequisite,
unsupported capability, unavailable dependency, flaky retry, or missing receipt
is `NOT_RUN`, `INCONCLUSIVE`, or `FAIL` as specified—never a pass.

Accepted fixtures are immutable. A correction or intended semantic change creates
a new fixture and contract manifest with compatibility analysis; expected output
is never updated merely to make an implementation pass.

### Required invariant families

The property corpus proves at least:

1. aggregate revisions are positive, contiguous per accepted transition, and do
   not advance on rejection;
2. command/idempotency identity yields at most one durable decision and one
   canonical receipt under sequential, concurrent, crash, and replay execution;
3. event DAGs remain acyclic with valid existing parents, canonical parent sets,
   bounded fan-in, and order-independent joins;
4. one exact FQN owns a subject at a time; dispatch, claims, sessions, processes,
   and execution IDs never become ownership;
5. fencing rejects stale executions and late results cannot mutate state;
6. legal lifecycle transitions preserve epochs/history, while completion,
   acceptance, reopening, successor, supersession, and correction obey Decisions 6–8;
7. retry/review/planning/validation/handoff/escalation budgets remain finite and
   survive duplicate delivery and actor/process restart;
8. unknown types/versions and aliases obey lossless-preservation, no-effect,
   compatibility, transformation, and replay rules;
9. every applied state equals the deterministic fold of accepted events and every
   receipt/event carries complete authority/evidence/provenance identities;
10. rejected commands, invocation failures, delivery failures, provider status,
    model/tool output, and SMA recall cannot create organizational facts;
11. transaction/outbox/claim/recovery behavior is atomic or explicitly uncertain
    and reconciled, never silently partial; and
12. canonical results are unchanged by map iteration, sibling arrival, delivery
    duplication, permitted cross-aggregate reorder, wall-clock scheduling, or
    repository query order.

### Determinism and reproducibility

1. Core suites run hermetically with fixed locale/time zone, fake clock,
   deterministic ID stream, pinned dependencies/toolchain, no network, and no
   ambient credentials or home-directory state.
2. Generated tests record generator/version, seed, bounds, execution count, and
   minimal shrunk counterexample. Every discovered counterexample becomes a
   permanent regression fixture before the defect is closed.
3. Concurrency tests use a recorded scheduler/fault trace or systematic bounded
   schedule exploration; a timing-only pass is insufficient.
4. Mongo integration uses a pinned supported topology with declared feature
   compatibility and clean isolated database namespace. Test setup verifies
   indexes/topology and teardown cannot target a broad or unresolved path/database.
5. No automatic retry hides a failing test. An explicit retry/fault scenario is
   itself part of the fixture and its attempts are observable.
6. Each run emits a machine-readable report with source/overlay/build/runtime
   identity, contract manifest, suite/fixture digests, environment, executed/
   skipped counts, seeds/traces, failures, artifacts, and report digest.

### Coverage and qualification

- Every active command/event version has valid, malformed, boundary, unknown-
  field/version, authorization, stale-revision/fence, idempotency, and replay cases.
- Every legal transition and stable outcome/reason family is exercised.
- Every binding negative requirement and every prohibited interpretation in the
  approved decisions maps to at least one named executable test.
- Coverage is requirement/transition/invariant coverage. Source-line percentage
  may be reported but cannot substitute for behavioral coverage.
- The in-memory adapter is the bootstrap reference, not production evidence.
  Mongo passes its own full adapter/recovery suite before use.
- Live OpenHands, SMA, Git, model, or paid-provider tests are excluded from core
  conformance and cannot make a deterministic core gate flaky or unavailable.

### Performance and resource evidence

Semantic conformance does not prove latency, throughput, allocation, storage,
change-stream resource use, or recovery-time objectives. Representative benchmarks
and fault/load tests use the same contract identities and produce separate
`COMPUTED` evidence. Numeric budgets must be approved from measured workloads
before production qualification; this step does not invent them.

### Evidence calibration

- **PRINCIPAL-APPROVED:** Phase 1B requires one canonical versioned gate shared by
  local development, CI, synthesized-merge qualification, and release, with
  hermetic, deterministic-scenario, and paid/provider gates separated.
- **PRINCIPAL-APPROVED:** The deterministic fake execution engine is the bootstrap
  reference; OpenHands and advanced SMA behavior remain separately unqualified.
- **PRINCIPAL-APPROVED:** Merge and release qualification must be deterministic
  and reliable and identify the exact synthesized tree.
- **UNKNOWN:** No v4 implementation or conformance runner exists yet, so no
  implementation has passed these tests.
- **COORDINATOR DESIGN RECOMMENDATION:** The seven suite layers, fixture shape,
  invariant list, report classes, and counterexample promotion rules are new v4 contracts.

### Prohibited interpretations

- Implementation unit tests replace the normative conformance corpus.
- Skipped, flaky, retried-away, unavailable, or unsupported cases count as pass.
- Expected output is changed to agree with implementation without a contract revision.
- Random/property failures without recorded seed and counterexample are dismissed.
- Arrival timing or wall-clock luck proves concurrency correctness.
- In-memory adapter success qualifies MongoDB or production durability.
- Live provider availability controls the hermetic core gate.
- Code coverage percentage substitutes for transition/invariant coverage.
- Semantic conformance proves performance or production readiness.

### Executable acceptance criteria

1. A meta-validator rejects missing/duplicate fixture IDs, unknown decision links,
   schema/digest mismatch, incomplete inputs/expected outputs, undeclared variable
   fields, and invalid result classes.
2. At least one reference runner executes the canonical corpus and emits the
   required machine report; a second deliberately faulty implementation or
   mutation set demonstrates that each invariant family can fail the gate.
3. Fixture-order and JSON-object-order permutations produce identical results.
4. Generated histories replay from recorded seed/trace and shrink to the same
   semantic counterexample.
5. Requirement traceability reports zero approved requirements, transitions,
   negative requirements, or prohibited interpretations without an executable test.
6. In-memory/Mongo differential fixtures yield the same semantic receipt/events/
   state while retaining declared adapter-specific operational evidence.
7. Crash and concurrency suites reproduce every recorded schedule and prove one
   canonical decision or explicit uncertainty followed by reconciliation.
8. Report validation independently recomputes fixture, artifact, and report digests.

### Remaining unknowns

The runner implementation language, property-testing library, model checker,
container/runtime harness, CI platform, benchmark workloads, and numeric
performance budgets remain implementation choices. Decision 12 will freeze the
manifest and gate composition that invokes these suites.

### Recorded disposition

`APPROVE`. The language-neutral corpus, seven conformance layers, normative
fixture/result contract, invariant families, hermetic reproducibility,
counterexample retention, behavioral coverage, performance separation,
prohibited interpretations, and acceptance criteria above are binding for Step 1.

## Decision 12 — Canonical contract manifest, compatibility gate, and Step 1 closure

**Status:** APPROVED BY PRINCIPAL  
**Principal statement:** `Approve.` on 2026-08-11

### Coordinator recommendation

`APPROVE` `tekroo.kernel.contracts/0.1.0` as one immutable, content-addressed
contract release whose manifest is consumed unchanged by local validation, CI,
synthesized-merge qualification, and later release policy.

### Canonical contract package

The accepted package lives under `CONTRACTS/tekroo.kernel.contracts/0.1.0/` and
contains only versioned contract assets:

- `manifest.json` — identity, provenance, dependency order, required profiles,
  and digest inventory;
- `catalogue/` — command/event entries and compatibility declarations;
- `schemas/` — canonical JSON Schemas for values, envelopes, aggregates,
  events, receipts, evidence, authority, and fixture/report formats;
- `fixtures/` — positive, negative, boundary, transition, concurrency,
  fault/recovery, and regression scenarios;
- `invariants/` — machine-readable invariant and property-test declarations;
- `traceability/` — decisions, negative requirements, prohibited
  interpretations, schemas, catalogue entries, and tests mapped bidirectionally;
- `compatibility/` — predecessor/successor reports and migrations, empty for the
  initial release except the explicit no-predecessor record; and
- `runner/` — language-neutral runner protocol and required result/report schema,
  not a production kernel implementation.

All text is UTF-8 without BOM; canonical files use LF endings. JSON objects are
semantically canonicalized using RFC 8785 for comparison/digests, while the
manifest also records each exact file-byte SHA-256 and size. Paths are relative,
normalized, traversal-free, case-distinctness-safe, and sorted by UTF-8 byte order.

`manifest.json` lists every package file except its detached checksum. The
detached `manifest.sha256` contains the exact byte digest of `manifest.json`,
avoiding self-reference. The full contract identity is semantic name/version plus
that manifest digest. Accepted package files are never edited in place.

### Manifest obligations

The manifest records:

1. contract name/version/status, creation decision, and exact Phase 1B/Phase 2
   source gate and decision-register digests;
2. every file path, role, byte size, byte digest, canonical JSON digest where
   applicable, schema identity/version, and dependency list;
3. catalogue/schema/fixture/invariant/traceability counts and cross-reference roots;
4. required gate profiles, suite order, timeout/resource bounds, allowed variable
   outputs, and fail/skip/inconclusive policy;
5. reference clock/ID sequences, canonicalization and digest algorithms, runner
   protocol version, and minimum required report fields; and
6. known limitations, unsupported capabilities, separately authorized profiles,
   and explicit statement that contract acceptance is not implementation or
   production qualification.

The dependency graph among assets is acyclic. A validator resolves every
reference and digest before executing any fixture.

### Gate profiles

One manifest defines distinct profiles without mixing their evidence claims:

1. `contract-structure` — schemas, catalogue, manifest, digests, references,
   traceability, compatibility metadata, and fixture meta-validation;
2. `core-hermetic` — canonicalization, examples, transitions, properties,
   deterministic scenarios, and fake external effects with no network;
3. `mongo-integration` — pinned Mongo topology, transactions, indexes, change
   streams, claims, crash/recovery, and differential semantic results;
4. `synthesized-merge` — all required deterministic profiles against the exact
   candidate Git tree produced by the approved merge plan; and
5. `provider-e2e` — separately authorized live OpenHands/SMA/Git/model/paid
   provider evidence, required only when an explicit later adoption/release policy
   names it.

Local development, CI, merge qualification, and release invoke the same package
and profile definitions. They may supply declared environment bindings but may
not maintain divergent expected results or silently omit required cases.

### Deterministic synthesized-merge qualification

1. A release plan freezes repository identity, exact base commit, ordered head
   commits, merge strategy/tool version, conflict policy, contract manifest, and
   required profiles before synthesis.
2. The coordinator constructs the candidate tree deterministically in the stated
   order. Any unresolved conflict, missing head, base movement, unexpected generated
   change, dirty input, or strategy difference fails synthesis; no agent improvises
   a conflict resolution inside the gate.
3. The gate records base/head commits, ordered plan digest, resulting tree hash,
   build/runtime/toolchain identities, suite reports, artifacts, and final result.
4. Qualification applies only to that exact tree. A rebase, amended head, changed
   base, regenerated artifact, altered merge order, dependency-lock change, or
   conflict resolution creates a new tree and requires a new gate.
5. The release coordinator executes merges in the frozen plan order and verifies
   the authoritative repository's resulting tree equals the qualified tree. Merge
   commit IDs may differ when provider mechanics require, but tree inequality
   blocks organizational acceptance and triggers reconciliation/requalification.
6. Required checks are bound to contract and tree identities, not merely branch
   names, PR labels, timestamps, or provider-reported green status.

### Compatibility and revision

1. Any accepted-file change creates a new contract version and detached manifest
   digest. No alias, fixture edit, schema relaxation, reason-code change, or
   documentation change with normative effect occurs in place.
2. Every successor includes a machine-readable compatibility report covering
   added/removed/changed type IDs, schemas, required fields, invariants, outcomes,
   fixtures, aliases, deprecations, routing/authority, state/replay semantics,
   data migration, rollback, and unresolved risks.
3. SemVer labels describe intent; executable old/new fixtures determine declared
   backward, forward, replay, and adapter compatibility. Undeclared direction is
   incompatible by default.
4. A breaking revision requires explicit principal approval, migration/replay
   fixtures, rollback/default behavior, and qualification of affected stored data,
   adapters, and synthesized trees.
5. A waiver or missing required result cannot be called `PASS`. Any principal-
   approved exception is a separate content-addressed exception record and gate
   result, with scope, owner, risk, expiry, and remediation; production policy
   must explicitly decide whether that non-PASS result is admissible.

### Gate result and evidence

Each profile result is `PASS`, `FAIL`, `NOT_RUN`, or `INCONCLUSIVE`. Overall
`PASS` requires every manifest-required profile and case to pass, every digest and
reference to match, zero uncovered approved requirements, and no unauthorized
exception. Reports carry exact input/output/artifact identities and are themselves
content-addressed. The validator independently verifies the report before it can
be used as organizational evidence.

Step 1 contract acceptance means the package is complete and coherent. It does
not mean a Tekroo implementation, Mongo deployment, OpenHands/SMA adapter, live
provider, migration, or production system has passed conformance.

### Evidence calibration

- **PRINCIPAL-APPROVED:** Phase 1B requires one canonical versioned gate shared
  by local development, CI, synthesized-merge qualification, and release.
- **PRINCIPAL-APPROVED:** Exact base/head commits and resulting tree identity are
  qualified; deterministic merge/release behavior is a priority because v3 merge
  reliability was frustrating.
- **PRINCIPAL-APPROVED:** Hermetic, deterministic scenario/capstone, and separately
  authorized paid/provider E2E evidence remain distinct.
- **UNKNOWN:** No v4 contract package, runner, candidate tree, or implementation
  has yet passed these profiles.
- **COORDINATOR DESIGN RECOMMENDATION:** The package layout, detached manifest,
  RFC 8785 canonicalization, five profiles, tree-verification protocol, and
  exception semantics are new v4 contract choices.

### Prohibited interpretations

- Different environments maintain different expected outputs for one manifest.
- A branch, PR, tag, provider check, or merge commit ID substitutes for tree identity.
- A changed base/head/order/lock/generated artifact retains prior qualification.
- Gate conflict resolution is improvised or unrecorded.
- Skipped/inconclusive/waived work is labeled `PASS`.
- SemVer alone proves compatibility.
- Accepted package files are edited in place.
- Provider E2E availability controls the hermetic core profile.
- Contract-package acceptance claims implementation or production readiness.

### Executable acceptance criteria

1. Package validation independently recomputes manifest/file/canonical digests,
   sizes, normalized paths, schema IDs, dependency DAG, counts, and all references.
2. Traceability is bidirectionally complete: every approved decision requirement,
   negative requirement, prohibited interpretation, schema, catalogue entry,
   fixture, invariant, and gate profile has no orphaned required link.
3. Tampering, BOM/line-ending drift, path traversal/case collision, missing/extra
   file, digest mismatch, cyclic dependency, duplicate ID, unknown result, or
   undeclared variable output fails before fixture execution.
4. Profile composition tests prove local/CI/merge/release resolve the same assets,
   required cases, and expected outputs from one manifest.
5. Compatibility meta-tests reject undeclared direction, missing old/new fixture
   execution, absent migration/rollback for breaking change, and in-place edits.
6. Synthesized-merge fixtures cover head/base movement, merge-order change,
   conflict, generated drift, dependency-lock change, same-tree/different-commit,
   different-tree/provider-green, partial merge, and authoritative reconciliation.
7. Exception tests prove a waiver is separately identified, scoped, expiring,
   non-PASS, and cannot silently satisfy a required profile.
8. Final Step 1 validation emits a content-addressed closure report listing all
   twelve principal decisions, the approved Decision 6 revision, package identity,
   structural/reference/traceability results, limitations, and authority boundary.

### Step 1 closure sequence

After Decision 12 approval, the coordinator will:

1. generate the `0.1.0` catalogue, schemas, fixtures, invariants, traceability,
   runner protocol, and compatibility metadata from the twelve decisions;
2. run the package/meta-validation and resolve every structural or traceability defect;
3. produce the detached manifest digest and machine-readable Step 1 closure report;
4. present the complete package and coordinator gate recommendation to the principal; and
5. stop for explicit principal confirmation. No implementation phase begins automatically.

### Recorded disposition

`APPROVE`. The immutable package layout, detached manifest, canonicalization,
profile composition, synthesized-tree qualification, compatibility/revision,
exception, gate-result, prohibited-interpretation, acceptance, and closure rules
above are binding. All twelve Step 1 decisions now have principal dispositions;
the contract package and closure report remain to be generated and validated.

## Step 1 gate

Step 1 passes only when:

- all twelve decisions have principal dispositions;
- normative schemas and fixtures match those decisions;
- the conformance manifest is complete and content-addressed;
- every approved invariant has positive, negative, and determinism criteria;
- Phase 1B traceability and unresolved unknowns are explicit;
- no implementation, investigation, migration, or production authority has been
  inferred; and
- the principal confirms the complete contract freeze.
