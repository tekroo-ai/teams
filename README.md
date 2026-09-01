# Tekroo Teams v4

Tekroo Teams is a local-first autonomous software-engineering organization. It
turns an operator feature request into a finite story/task DAG, assigns exact
role actors and qualified model profiles, executes through OpenHands, uses SMA
as non-authoritative semantic memory, validates independently, and performs a
deterministic release only after explicit operator acceptance.

## Current state

The supported implementation is contract
`tekroo.kernel.contracts/0.10.0`. It includes:

- versioned team manifests and signed role bundles;
- stable actor FQNs with replaceable process/execution identities;
- product-owner, project-manager, architect, coder, senior-coder, tester,
  security, and operator roles;
- feature intake, planning, complexity/risk classification, finite task DAGs,
  exact assignment, bounded work, independent review, repair, acceptance, and
  deterministic Git release;
- MongoDB persistence, projections, change-stream wakeup, leases, recovery,
  redelivery, and dead letters;
- OpenHands execution with the accepted local Qwen model profile and SMA prompt
  hook;
- focused operator HTTP, MCP, and `tekroo` commands;
- human participant questions, responses, and durable notifications; and
- exact revisioned aliases plus Ed25519-signed, replay-safe cross-team routing.

Agent messages carry facts, requests, and evidence. They cannot directly start
another model. Only a current Teams decision backed by a finite DAG, budget,
assignment, and single-use invocation can wake an execution worker. This is the
structural replacement for v3's unbounded conversational loops.

## Build and verify

```sh
go test ./... -count=1
go test -tags=mongo_integration ./... -count=1
go vet ./...
```

Validate the current contract with:

```sh
node CONTRACTS/tekroo.kernel.contracts/0.10.0/runner/validate-package.mjs /tmp/tekroo-contract-structure.json
```

## Local production installation

Install the two product binaries and lifecycle scripts into an isolated root:

```sh
./scripts/tekroo-install-local /absolute/install/root
```

Initialize a fresh deployment with the installed CLI:

```sh
/absolute/install/root/bin/tekroo init-local \
  -root /absolute/deployment/root \
  -source-root /absolute/path/to/teams \
  -repository /absolute/path/to/repository \
  -openhands-key /absolute/path/to/openhands-api-key \
  -sma-hook /absolute/path/to/sma_context_hook.py \
  -tekrood /absolute/install/root/bin/tekrood \
  -branch-prefix tekroo/
```

Initialization fails closed if the deployment root is nonempty, the source has
tracked changes, a role branch already exists, MongoDB or the operator endpoint
is not loopback, Teams and SMA database identities overlap, required accepted
assets are missing, or the generated production configuration does not pass the
same loader used by `tekrood`.

Use a different valid branch prefix when two preserved deployments must share
one repository; initialization never overwrites an existing deployment branch.

The command creates deployment-owned secrets and provenance, copies the signed
starter role library, creates isolated role worktrees, installs the accepted
SMA hook in each workspace, and writes `tekrood.json` plus a disabled-by-default
LaunchAgent definition. It never reads or migrates v3 data.

Start and inspect the service with:

```sh
/absolute/install/root/scripts/tekroo-start \
  --config /absolute/deployment/root/config/tekrood.json \
  --state-dir /absolute/deployment/root/state

/absolute/install/root/bin/tekroo \
  -config /absolute/deployment/root/config/tekrood.json status
```

See [the operator workflow](docs/operations/002-normal-operator-workflow.md) and
[the local deployment runbook](docs/operations/005-local-production-deployment.md).

## Architecture boundary

Teams owns organizational state and process control: teams, roles, features,
stories, tasks, DAGs, routing, assignment, budgets, invocations, evidence,
review, acceptance, release, and operational recovery.

SMA owns semantic memory. Its retrieved context is advisory and cannot mutate
Teams state, grant authority, assign work, reset a budget, or complete a task.
OpenHands owns model/tool execution and returns observations; it does not decide
organizational truth.

MongoDB is the Teams persistence and change-stream wakeup substrate. Local model
and provider choices remain behind exact qualified execution profiles.

## Scope

Tekroo v3 remains read-only historical source material. No v3 data migration is
required. Public-network exposure, wildcard routing, cloud trust brokerage, and
billing are deliberate non-goals rather than incomplete product steps.

Current acceptance records:

- [Phase 6 local-team acceptance](docs/architecture/121-phase-6-integrated-acceptance.md)
- [Phase 7 federated-routing acceptance](docs/architecture/123-phase-7-integrated-acceptance.md)
- [Phase 8 local-production plan](docs/architecture/124-phase-8-local-production-plan.md)
