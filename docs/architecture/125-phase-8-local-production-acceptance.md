# Phase 8 local-production acceptance

Status: **ACCEPTED**

Phase 8 converts the accepted Teams v4 implementation into a repeatable local
deployment and proves the real operator-to-team path. It does not migrate v3
data.

## Production correction

The first smoke run found that a product-owner refinement could copy its own
Git branch into the feature acceptance criteria. That mixed a runtime assignment
with the product definition before Teams assigned the implementation task.

The production correction is structural:

- product-owner and project-manager instructions forbid actor, branch,
  worktree, workspace-path, model-profile, execution, and fencing identities;
- strict result parsing rejects those identities if a model emits them anyway;
  and
- local bootstrap accepts an isolated Git branch namespace so a clean
  deployment can be qualified without destroying the diagnostic deployment.

The original diagnostic database and deployment were preserved. The passing
deployment used a new database and branch namespace.

## Accepted deployment

- source commit: `2b3f6b9a820e0a79be6b4af54afce48b6901c767`
- source tree: `929a5cff88b464003d68b30296cd9414bfb62c08`
- deployment root: `/Users/paul/.local/share/tekroo/teams-v4-final`
- Teams database: `tekroo_teams_v4_prod_final`
- SMA database: `sma` (separate; no Teams workflow authority)
- operator endpoint: `127.0.0.1:8788`
- Git branch namespace: `tekroo-prod/`
- isolated role workspaces: 13
- model: `ddalcu--Qwen3.8-27B-MLX-Serve-8bit`
- OpenHands: `127.0.0.1:8000`
- SMA retrieval bridge: `127.0.0.1:8130`

MongoDB reported replica-set status `1`; OpenHands reported `{"status":"ok"}`;
the model endpoint reported the exact model `ready`; and a Java process was
listening on loopback port 8130.

## Operating proof

Feature `01a05ce6-94e5-73a8-b452-805107167589` completed the supported path:

1. operator submission;
2. product-owner refinement;
3. project-manager specification;
4. architect DAG planning;
5. coder implementation;
6. independent tester validation;
7. read-only product-owner acceptance; and
8. human acceptance.

The persisted refinement and specification contain no pre-assignment runtime
identity. Teams subsequently assigned the implementation to
`teams::coder-1` on `tekroo-prod/coder-1`, validation to `teams::tester-1`, and
product acceptance to `teams::product-owner-1`. The three tasks formed a finite
acyclic dependency chain and each model invocation ran once.

The feature reached `ACCEPTED` at revision 6. The implementation commit is
`8f8f45f0cf1e78e28d9c6444422804376b0e0e80`; independent validation and product
acceptance both recorded `PASS`.

`tekrood` then stopped with exit code 0, restarted using the generated
LaunchAgent definition, returned `RUNNING` with zero active work, and recovered
the same accepted feature at revision 6. `RunAtLoad` remains disabled, so
automatic login startup was not configured.

## Regression results

- `go test ./... -count=1`: PASS
- `go test -tags=mongo_integration ./... -count=1`: PASS
- `go test -race ./adapters/operationalruntime ./organization ./application -count=1`: PASS
- `go vet ./...`: PASS
- contract `0.10.0` structure: `PASS 3642`, result
  `8a40edfddb4a8bb82cb31d45f8db367ee352c56b641dc38cdfa3060b7a83e35f`
- contract `0.10.0` reference corpus: `PASS 297`, result
  `16c3f5e42d0e27890f5a213a3824bad89d2662649177c3dec1dfa19ae6dcc662`

## Scope

No v3 data was read, imported, transformed, or migrated. Public-network
deployment, cloud trust brokerage, billing, wildcard routing, SMA modification,
and automatic login startup remain outside this phase.
