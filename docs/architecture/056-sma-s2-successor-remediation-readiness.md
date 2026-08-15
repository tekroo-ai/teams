# SMA-S2 successor remediation readiness

Date: 2026-08-14  
Status: `REMEDIATION_PASS_READY_FOR_INDEPENDENT_REVIEW`

## Outcome

The two candidate-1 review blockers have been remediated prospectively in
SMA-S2 preregistration candidate 2. Candidate 2 is not accepted or frozen and
does not authorize measured execution.

No measured S2 case ran. No OpenHands conversation or model request was created
for S2, and no measured SMA namespace was created.

## Fault and cancellation controls

**OBSERVED:** The successor configuration binds zero model retries, a one-second
model timeout, five-second delayed-stub behavior, exact case/repetition fault
mappings, expected request counts, and an active cancellation trigger 250 ms
after the append-before-action raw request receipt exists.

Candidate 2 adds:

- `SMA-S2-019-MODEL-STUB-FAULT-MATRIX`: four prospectively ordered repetitions
  for timeout, malformed response, HTTP 503, and unused-port transport failure;
- `SMA-S2-020-ACTIVE-CANCELLATION-AND-SHUTDOWN`: three repetitions requiring
  bounded conversation-future completion, `CLIENT_DISCONNECTED`, owned-process
  termination, resource inventory, and cleanup.

**OBSERVED:** Stub candidate 2 adds a separate terminal journal containing
request identity, mode, status, response digest/length, and
`CLIENT_RECEIVED`/`CLIENT_DISCONNECTED`, without request bodies.

**OBSERVED:** Offline harness candidate 5 passed nine controls. Its cancellation
test durably observed the request, reset the TCP client, and then observed the
stub terminal `CLIENT_DISCONNECTED`. Ruff and Python compilation also passed.

**COMPUTED:** The successor contains 20 cases and 103 planned repetitions,
compared with candidate 1's 18 cases and 96 repetitions.

## Controlled runtime restart

**OBSERVED:** Immediately before restart, the agent server reported zero
running, waiting-for-confirmation, stuck, or deleting conversations. Tekroo
Trader was paused.

Exactly one authorized `launchctl kickstart -k` was performed. The authorization
is consumed with no restart remaining.

**OBSERVED:** The new agent-server process started at
`2026-08-14T09:34:20-06:00`, after bound source commit
`a338ba9b6cbb529886b755a335bae3dee0004700`. It returned health 200 and reports
version 1.40.1. Its executable digest matches the bound artifact, its command
uses the exact editable source path, and that worktree remains clean at the
bound commit/tree.

**OBSERVED:** The new live OpenAPI `MessageEvent` schema now contains:

- `authorship_origin`
- `semantic_purpose`
- `agent_response_finality`

This resolves the candidate-1 source/running-schema mismatch. The server's
`build_git_sha` remains `unknown`; therefore the evidence floor is the
post-commit process start, exact editable-source command path, clean commit/tree,
executable digest, reported version, and matching live semantic schema. A fresh
identity check remains mandatory immediately before any future execution.

## Paused-conversation preservation

**OBSERVED:** Tekroo Trader conversation
`4c2e5f84-ecd3-4971-b375-a0fa9905a058` retained the same title, paused status,
creation timestamp, and update timestamp after restart.

## Verification and gate

**COMPUTED:** All 16 successor semantic checks passed. All six successor
artifact digests declared by the boundary identity matched their files. The
offline receipt contains nine passes and zero failures.

**INFERRED:** Both substantive candidate-1 review blockers are remediated.
Candidate 2 is ready for the designated principal's independent review.

The next decision is review only: accept/freeze candidate 2 or identify a new
prospective correction. Acceptance would still not authorize measured S2.
Execution identity sealing and one single-use measured authorization remain
separate later decisions.

## Principal artifacts

- `investigations/sma-q1/layered/sma-s2-preregistration-candidate-2.json`
- `investigations/sma-q1/layered/sma-s2-boundary-identity-candidate-2.json`
- `investigations/sma-q1/layered/sma-s2-nonsecret-configuration-candidate-2.json`
- `investigations/sma-q1/layered/sma-s2-successor-remediation-authorization-consumption.json`
- `OUTPUT/phase-3/sma-s2-successor-preparation-readiness-candidate-2.json`

The machine-readable readiness receipt is authoritative for exact hashes,
counts, process observations, preservation evidence, and authority fences.
