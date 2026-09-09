# Local production deployment

## Prerequisites

- loopback MongoDB replica set on `127.0.0.1:27017`;
- OpenHands Agent Canvas on `127.0.0.1:8000` with a nonempty session API key;
- accepted model `ddalcu--Qwen3.8-27B-MLX-Serve-8bit` available through the
  configured OpenHands profile;
- SMA retrieval bridge on `127.0.0.1:8130`; and
- an accepted `tekroo.local-model-profile-qualifications/1.0.0` bundle for the
  exact generated model profiles, routes, role tool surfaces, and work kinds;
- a clean tracked Git repository to be operated by the team.

Untracked files do not block initialization and are not copied into role
worktrees. Tracked modifications do block it because a deployment must bind one
exact source commit and tree.

## Install

```sh
./scripts/tekroo-install-local /absolute/install/root
```

This does not modify Python, Homebrew, Ollama, MLX, OpenHands, or SMA and does
not configure automatic startup.

## Initialize

```sh
/absolute/install/root/bin/tekroo init-local \
  -root /absolute/deployment/root \
  -source-root /absolute/path/to/teams \
  -repository /absolute/path/to/target-repository \
  -openhands-key /absolute/path/to/openhands-api-key \
  -sma-hook /absolute/path/to/sma_context_hook.py \
  -tekrood /absolute/install/root/bin/tekrood \
  -qualification-bundle /absolute/path/to/accepted-model-profile-qualifications.json \
  -mongo-uri mongodb://127.0.0.1:27017 \
  -database tekroo_teams_v4_prod \
  -sma-database sma \
  -operator-address 127.0.0.1:8787 \
  -branch-prefix tekroo/
```

The root must be absent or empty. The command will not merge, overwrite, or
adopt an existing deployment. Use a distinct valid branch prefix to preserve
multiple deployments against the same repository.

Initialization copies the supplied bundle into the deployment, binds each
qualification to the exact generated profile, and rejects unknown, duplicated,
stale, revoked, failed, mismatched, or incomplete operational coverage. The
daemon repeats the operational-coverage check before creating runtime state or
connecting to MongoDB, so an unqualified deployment cannot report healthy and
then fail on its first feature.

## Dependency check

Before starting Teams, verify MongoDB, OpenHands, the exact model, and SMA are
healthy. Teams will fail rather than silently substitute another model or
memory service.

## Lifecycle

```sh
/absolute/install/root/scripts/tekroo-start \
  --config /absolute/deployment/root/config/tekrood.json \
  --state-dir /absolute/deployment/root/state

/absolute/install/root/scripts/tekroo-status \
  --config /absolute/deployment/root/config/tekrood.json \
  --state-dir /absolute/deployment/root/state

/absolute/install/root/scripts/tekroo-restart \
  --config /absolute/deployment/root/config/tekrood.json \
  --state-dir /absolute/deployment/root/state

/absolute/install/root/scripts/tekroo-stop \
  --config /absolute/deployment/root/config/tekrood.json \
  --state-dir /absolute/deployment/root/state
```

The generated `config/com.tekroo.tekrood.plist` is deliberately not installed
or loaded. Copying it to `~/Library/LaunchAgents` is a separate operator choice
after the manual start/restart checks pass.

## Recovery

- An unhealthy process is an error; inspect `state/tekrood.log` and repair the
  dependency or configuration.
- A stale/foreign PID file is never overwritten automatically.
- Restart uses the same configuration, database, role FQNs, workspaces, and
  execution fences.
- `tekrood` records a durable deployment heartbeat. A clean stop/start or a
  heartbeat gap caused by host sleep creates one immutable suspension window.
  Active feature deadlines and unfinished task profiles exclude each window
  exactly once; an already-started OpenHands invocation retains its conversation
  and receives the same suspension allowance when expiry is evaluated.
- After wake-time service, provider, workspace, outbox, and change-stream
  reconciliation passes, execution resumes at the durable task and conversation
  checkpoint. Opening the lid alone does not bypass that gate. Resume does not
  repeat completed roles or completed model invocations. A process killed
  mid-write resumes from the last committed event; a provider that cannot
  preserve an in-flight generation may still require the existing explicit
  retry path for that one invocation.
- Do not repair Teams by editing MongoDB directly. Use `tekroo` diagnostics,
  role lifecycle, dead-letter repair, and bounded feature controls.

## Data boundary

The deployment uses a new Teams database. It does not inspect, import, or
migrate v3 databases. SMA remains a physically separate semantic-memory
database and has no Teams workflow authority.
