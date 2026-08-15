# SMA-S2 candidate 5 execution-identity preparation: NO-GO

Date: 2026-08-14  
Status: `FAIL_NO_GO_BEFORE_EXECUTION_IDENTITY_AND_LIVE_PREFLIGHT`

## Outcome

No candidate-5 execution identity was sealed, and the authorized live preflight
was not consumed. Do not authorize measured S2 execution from candidate 5.

The audit stopped before starting the deterministic stub, creating an
OpenHands conversation, starting SMA, or creating any preflight or measured
namespace.

## Decisive finding

**OBSERVED:** Candidate-5 configuration requires both the ephemeral stub port
and bridge port to be bound in the successor execution identity.

**OBSERVED:** The accepted driver inherits `start_stub`, which does not pass a
`--port` argument. The deterministic stub consequently uses its default port
`0`, and the driver learns the OS-selected numeric port only from the later
ready file.

**OBSERVED:** The accepted execution fence checks hashes, authorization state,
scope, and real-model prohibition, but contains no `stubPort` or `bridgePort`
check. Measured handlers use the post-launch `self.stub_port` value.

**COMPUTED:** Two endpoint bindings are required and zero are enforced.

**INFERRED:** An execution identity could name arbitrary ports, pass the fence,
and then execute against a different OS-selected stub port. It would not be an
honest prospective identity for the measured runtime.

This is a qualification-harness identity defect. It is not evidence of an SMA
or OpenHands product failure.

## Clean read-only result

- Accepted candidate, driver, harness, final offline receipt, configuration,
  and matrix hashes matched.
- OpenHands commit/tree and executable hashes matched the bound identities; its
  worktree was clean.
- Agent server and Agent Canvas listeners were present; agent-server health was
  HTTP 200.
- No SMA bridge listener was present on port 8130.
- Candidate-5 MongoDB database, Qdrant collections, workspace root, and measured
  output root were absent.

The active-conversation audit was not completed because the static identity
defect was already decisive and no live action could follow it.

## Minimal successor

Candidate 5 remains immutable and receives no measured credit. Candidate 6
should change only execution endpoint identity enforcement:

1. Require numeric `stubPort` and `bridgePort` fields in the execution identity
   and single-use authorization.
2. Launch the stub on the bound numeric port and require the ready receipt to
   report that same port.
3. Require identity/authorization equality and `bridgePort == 8130` in the
   execution fence.
4. Offline-test correct binding and missing, mutated, occupied, and ready-port
   mismatch failures.
5. Bind, review, and accept/freeze candidate 6 before issuing new live-preflight
   authority.

The accepted cases, 103 repetitions, 59 predicates, 10 evidence requirements,
thresholds, schedules, product identities, and claim do not change.

