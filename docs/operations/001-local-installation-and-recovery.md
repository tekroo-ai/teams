# Local installation and recovery

## Scope

This is the supported local process path for Tekroo Teams v4. It installs and
runs `tekrood` and `tekroo`; it does not import Tekroo v3 data, enable login
startup, modify OpenHands or SMA, or create organizational authority.

Teams uses a dedicated new MongoDB database. OpenHands executes authorized work
and remains responsible for its configured model and SMA hook. Teams has no
direct SMA, model-server, embedding-server, or SMA-database connection. This is
intentional: SMA absence is fail-open for memory, while a failure of the
OpenHands execution boundary is retained as an execution failure and appears in
Teams status.

## Configure

Copy `config/tekrood.example.json` outside the repository and replace every
placeholder. The configuration path and all runtime paths must be absolute.

The Mongo URI, OpenHands session API key, and operator bearer token live in
separate owner-only files. Set each secret file to mode `0600`. The operator
token must contain at least 32 characters. Do not put credentials in the JSON
configuration or a command line.

The configured Teams database must be new and distinct from both Tekroo v3 and
the configured SMA database identity. Give the deployment a stable, randomly
generated SHA-256 identity in `deployment_identity_digest`. First start binds
that identity only if the database has no organizational data; every restart
must present the same identity. Pointing v4 at a populated unbound database or
changing the identity fails startup. No migration step exists.

## Install without starting

Run:

```text
scripts/tekroo-install-local /absolute/install/root
```

This builds and installs the two binaries and lifecycle scripts. It does not
start a process or configure automatic startup.

## Operate

Every lifecycle command requires the same absolute configuration and a private
absolute state directory:

```text
/absolute/install/root/scripts/tekroo-start \
  --config /absolute/path/tekrood.json \
  --state-dir "/absolute/path/Teams State"

/absolute/install/root/scripts/tekroo-status \
  --config /absolute/path/tekrood.json \
  --state-dir "/absolute/path/Teams State"

/absolute/install/root/scripts/tekroo-restart \
  --config /absolute/path/tekrood.json \
  --state-dir "/absolute/path/Teams State"

/absolute/install/root/scripts/tekroo-stop \
  --config /absolute/path/tekrood.json \
  --state-dir "/absolute/path/Teams State"
```

The scripts refuse to signal a PID unless its exact command matches the
configured `tekrood` binary and configuration. Normal shutdown goes through the
authenticated operator API. A signal is used only when that API is unavailable
and the process identity still matches exactly.

The log is `tekrood.log` in the state directory. Startup refuses an occupied
operator port before any worker starts, which prevents two local instances from
competing under one consumer identity.

## Recovery behavior

Organizational state, outbox intents, leases, projections, and execution
identities are durable in the dedicated Teams database. On every start:

- the intent feed replays pending and previously claimed work;
- expired claims are immediately swept and then checked continuously;
- a returned-to-pending intent is redelivered through the same MongoDB change
  stream used for ordinary work;
- claim epochs prevent a stale process from resolving a replacement claim;
- bounded repeated abandonment becomes a visible dead letter; and
- task/story projection resumes from durable events.

The recovery sweep uses wall-clock lease expiry. If the host sleeps, the next
timer wake performs the overdue sweep; no separate duplicate work path is
created. Clean stop waits for active worker calls to terminate or yield their
lease before the Mongo client is closed.

`tekroo health` reports whether the Teams worker is running or paused.
`tekroo status` includes active/completed work and the last worker failure.
Startup configuration and dependency errors are returned directly and written
without credentials.

## Optional LaunchAgent

`config/com.tekroo.tekrood.plist.example` is a manual template. Its `RunAtLoad`
and `KeepAlive` values are both false. Copying it does nothing by itself. Replace
the three absolute placeholders and validate it with `plutil` before loading it.
Enabling it or changing it to automatic startup is a separate operator action.
