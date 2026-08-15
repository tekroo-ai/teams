# SMA-S2 candidate 8 host-verification correction

Date: 2026-08-14  
Status: `PASS_SUPERSEDES_SANDBOX_DERIVED_NO_GO`

## Corrected result

**OBSERVED:** The earlier localhost `curl` probes executed inside a restricted
network sandbox and returned connection failures. Those results did not measure
the host network namespace and cannot support the earlier conclusion that the
OpenHands services were down.

**OBSERVED:** Host-level evidence shows the frozen launchd service running, a
Node listener on `127.0.0.1:8000`, Agent Canvas health HTTP 200, and agent-server
health HTTP 200.

**OBSERVED:** The authenticated host-level conversation audit returned eight
conversations: six finished, one errored, and one paused. Active conversations:
zero. Tekroo Trader conversation
`4c2e5f84-ecd3-4971-b375-a0fa9905a058` remains paused.

**COMPUTED:** Controlled starts: 0. Restarts: 0. Conversation mutations: 0.
Dress attempts: 0. Measured attempts: 0. Real-model calls: 0.

**INFERRED:** The earlier preflight `NO_GO` and its consumption record are
invalid for operational adjudication and are retained only as superseded error
evidence. Candidate 8 requires no redesign from this incident.

## Root cause

The verification method crossed the wrong execution boundary: a sandboxed
localhost probe was treated as a host-local service observation. The raw
contradiction—launchd running plus an actual listener, alongside sandboxed
connection refusal—identified the error. Repeating the same checks at the host
boundary returned HTTP 200 for both services.

Future live preflights must perform localhost network checks at the host
boundary. Sandboxed connection failures must not be classified as service
failures without an independent host-level check.

## Authority boundary and next gate

The principal authorized a controlled start only if needed, followed by health
and active-work verification. Because the services were already healthy, no
start was performed. That authority explicitly excluded the candidate-8 dress
rehearsal and measured execution.

The next gate is a new single-use authorization for the candidate-8
34-operation, zero-credit dress rehearsal. It must use host-boundary preflight
checks and cannot reuse the superseded authorization trail.
