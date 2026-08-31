# Phase 5 — local operational productization completion

## Outcome

Phase 5 is complete for ordinary local use. The supported path is the actual
`teamsd` service and `teamsctl` client assembled from the accepted Phase 4
runtime. It is not a parallel test-only workflow.

Teams v4 starts with a new, explicitly bound MongoDB database. No Tekroo v3
data migration is required or supported by this phase.

## Observed product behavior

The supported product surface was used to:

- create, classify, scope, own, budget, assign, authorize, inspect, complete,
  accept, and cancel work;
- hold authorized work while paused and admit it after resume;
- run two independent OpenHands coding tasks concurrently against the installed
  `ddalcu--Qwen3.8-27B-MLX-Serve-8bit` model on `127.0.0.1:8802`;
- repair two isolated disposable Go repositories and independently pass each
  repository's tests;
- retain terminal output digests and evidence identities;
- preserve task projections and one-use model budgets across a `teamsd`
  restart;
- observe an unavailable OpenHands boundary, recover without a second
  authorization, and avoid a duplicate invocation;
- suspend and resume the active `teamsd` process with `SIGSTOP`/`SIGCONT`;
- cancel the recovered live OpenHands conversation through `teamsctl`; and
- preserve the cancelled projection and budget across restart.

The three live conversations had exact disposable workspace bindings and no
parent conversation. Teams never read the SMA database.

The live receipts are:

- `OUTPUT/phase-5/step-6-live-soak-receipt.json`, SHA-256
  `9fb97ea9b2c4d4ee9efba33101de759fcaa037ebb2c5ab56590886acd42fb0a2`;
- `OUTPUT/phase-5/step-6-live-recovery-cancellation-receipt.json`, SHA-256
  `955ea59116e03bac8d239ca46f7357f65890bb9b92a51665dbadf1f49148f349`.

## Product defects closed

Phase 5 closed two production defects discovered through the supported path:

1. An idle MongoDB change stream was being used with an expiring context. Once
   that context elapsed, the stream remained permanently errored and the worker
   silently stopped accepting later work. The intent feed now uses bounded
   non-blocking change-stream polling, and idle polls are explicitly healthy.
2. Invocation inspection did not expose retryability, terminal evidence IDs,
   output digest, or finish time. The read model and operator endpoint now
   return those durable fields.

The live qualification also caught two harness errors before they could obscure
product behavior: the OpenHands encryption secret had been mistaken for its API
key, and deterministic conversation IDs collided with retained conversations.
The live tests now use `api-key.txt`, generate fresh IDs, require authenticated
absence before execution, and verify the final workspace and lack of parent
conversation.

## Verification

The final implementation passed:

- `go test ./...`;
- `go vet ./...`;
- race-enabled tests for the execution worker, OpenHands client, and operational
  runtime;
- uncached MongoDB and operational-runtime integration tests;
- contract `0.8.0` structure validation: `PASS 3604`, digest
  `fe88ec20d984ee2a547bb010847abd09d7aaea82dd0939921e39e97e2336b958`;
- contract `0.8.0` reference corpus: `PASS 297`, digest
  `0327158c444d5159d8d797378cafa61b5508f6bed68fbd104c52eb6f1aaa3f7a`;
- the two-task live soak in 128.95 seconds; and
- the outage/recovery/suspend/cancellation scenario in 10.43 seconds.

## Explicit limits

The following were not run and are not claimed:

- physical host lid-close sleep;
- model-server shutdown;
- production or historical database access; and
- Tekroo v3 data migration.

Process suspension exercises the Teams-controlled pause in execution time, but
it is not represented as a physical host-sleep observation. Ordinary local
operation is ready; remote hosting, multi-user exposure, and data migration
remain outside Phase 5.
