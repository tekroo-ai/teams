# Local production deployment

## Prerequisites

- loopback MongoDB replica set on `127.0.0.1:27017`;
- OpenHands Agent Canvas on `127.0.0.1:8000` with a nonempty session API key;
- accepted model `ddalcu--Qwen3.8-27B-MLX-Serve-8bit` available through the
  configured OpenHands profile;
- SMA retrieval bridge on `127.0.0.1:8130`; and
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
  -mongo-uri mongodb://127.0.0.1:27017 \
  -database tekroo_teams_v4_prod \
  -sma-database sma \
  -operator-address 127.0.0.1:8787 \
  -branch-prefix tekroo/
```

The root must be absent or empty. The command will not merge, overwrite, or
adopt an existing deployment. Use a distinct valid branch prefix to preserve
multiple deployments against the same repository.

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
- Do not repair Teams by editing MongoDB directly. Use `tekroo` diagnostics,
  role lifecycle, dead-letter repair, and bounded feature controls.

## Data boundary

The deployment uses a new Teams database. It does not inspect, import, or
migrate v3 databases. SMA remains a physically separate semantic-memory
database and has no Teams workflow authority.
