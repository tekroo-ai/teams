# Phase 7 — Federated exact routing

## Status and authority

**Status:** COMPLETE — implemented and qualified on the authorized local
boundary.

**Authority:** the principal authorized Phase 7 after accepting the completed
Phase 6 local-team operating path. This authority covers local source changes,
tests, and disposable localhost integration. It does not authorize public
network exposure, production deployment, historical-data access, or migration.

**Execution checklist:**
`docs/operations/004-phase-7-federated-exact-routing-checklist.md`.

## Scope

Phase 7 closes the three explicit Phase 6 deferrals:

1. `F041` — aliases without identity weakening;
2. `F042` — trusted peers and signed cross-host ingress; and
3. `F043` — exact cross-team routing.

It reuses the useful v3 Ed25519 message-signing mechanism and replaces its
unsafe administration and routing behavior. It does not add a cloud broker,
billing, wildcard federation, or direct remote process launch.

## Sources inspected

- v3 federation design:
  `/Users/paul/work/tekroo-ai/teams-v3/docs/spec-v3/15-federation-architectural-hooks.md`;
- v3 router, receiver, signing, alias, and trust implementations under
  `/Users/paul/work/tekroo-ai/teams-v3/mcp`;
- Phase 1B architecture handoff and execution-boundary decisions under
  `/Users/paul/work/tekroo-ai/teams-v3/tekroo-v4-archaeology`;
- Phase 6 preservation ledger at
  `OUTPUT/phase-6/step-1/feature-preservation-ledger.json`; and
- current v4 organizational message, MongoDB, operator, MCP, CLI, and daemon
  implementations in this repository.

## Threat model and binding decisions

### Identity and aliases

- A durable sender or recipient is always an exact actor FQN.
- An alias is operator convenience only. It resolves before signing to one
  exact actor FQN, deployment identity, and route revision.
- The signed envelope and retained provenance contain both the alias revision
  and the exact resolved identity. The alias is never stored as the message's
  sender or recipient.
- Wildcards, role-wide destinations, and network addresses supplied in a send
  request are rejected.

### Trust and route authority

- A valid signature authenticates bytes; it does not itself authorize a route.
- Acceptance requires an active trust grant and active route matching the exact
  source deployment, destination deployment, source actor, destination actor,
  message type, purpose, key ID, and route revision.
- Trust grants have explicit validity intervals and key epochs. Revoked,
  unknown, premature, or expired keys fail closed.
- Routes are configured through the Teams product configuration and exposed
  through the operator surface. Remote input cannot choose an arbitrary local
  recipient, network endpoint, or permission scope.

### Replay, time, and transport

- Every envelope has a unique replay identity, issued-at time, expiry time, and
  payload digest.
- Future, stale, expired, duplicate, or digest-mismatched envelopes fail before
  message append.
- Replay reservation and message append are one storage transaction in MongoDB.
  A duplicate after an uncertain client result returns the retained delivery
  receipt without appending a second message.
- The production transport requires HTTPS. Plain HTTP is permitted only for an
  explicitly test-only loopback route.
- Federation ingress uses a dedicated listener and authentication path; it does
  not expose MCP or operator tools.

### Organizational safety

- Federation accepts only a valid `OrganizationalMessage`. It cannot invoke a
  model, start a role, create an execution lease, or mutate SMA.
- The imported message retains its existing thread, DAG node, parent, hop,
  maximum hop, budget account, lifecycle epoch, scope revision, and progress
  digest. Federation cannot reset any of them.
- The destination actor must belong to the local team. The source actor must
  belong to the route's remote team.
- A finite federation route trace prevents a message from revisiting a
  deployment and prevents cross-team forwarding loops.
- Remote story/task identities are message evidence and correlation only. They
  do not create local task ownership or execution authority.

### Failure and recovery

- Outbound sends are content-addressed and idempotent. Timeouts are uncertain
  outcomes and are reconciled with the exact delivery identity before retry.
- Inbound verification failures are explicit and do not reserve replay IDs or
  append messages.
- Replay reservation, accepted message, and delivery receipt are durable.
- Key rotation and route replacement use new revisions; existing receipts keep
  their original lineage.

## Engineering sequence

1. Publish this threat model and the executable checklist.
2. Create immutable successor contract `0.10.0` with federation schemas,
   invariants, fixtures, compatibility, and traceability.
3. Implement the pure federation domain: exact aliases, trust grants, routes,
   signed envelopes, canonical signing material, verification, and replay-safe
   ingress.
4. Implement memory and MongoDB stores, including unique replay and delivery
   identities and atomic inbound append.
5. Implement the dedicated HTTP ingress and bounded outbound client.
6. Wire file-backed federation configuration into `tekrood`, then expose safe
   inspection, alias resolution, and federated send through operator HTTP, MCP,
   and `tekroo`.
7. Run unit, Mongo integration, adversarial, and two-disposable-team localhost
   end-to-end tests.
8. Update the Phase 6 ledger and publish a concise Phase 7 acceptance receipt.

Every implementation step must preserve the Phase 6 single-team path. Ordinary
software defects are fixed in production code with regression tests; they do
not create candidate or adjudication cycles.

## Definition of done

Phase 7 is complete when two disposable local Teams deployments can exchange an
exact directed organizational message through the supported daemon surface and
all of the following are demonstrated:

- alias resolution retains exact identity and revision provenance;
- the receiving deployment independently verifies signature, trust, route,
  clock, payload digest, destination, and replay identity;
- one accepted envelope creates exactly one local message, including after a
  duplicate or uncertain sender result;
- unknown key, revoked key, stale/future envelope, replay, payload mutation,
  route mismatch, destination mismatch, wildcard, insecure production endpoint,
  and cross-team loop attempts fail closed;
- federation cannot launch a role/model, reset a budget, alter DAG lineage, or
  mutate SMA;
- operator HTTP, MCP, and CLI can inspect routes/aliases and send without direct
  database access; and
- the complete normal and Mongo integration suites remain green.
