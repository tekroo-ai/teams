# Phase 8 — Local production operationalization

## Status and authority

**Status:** implementation in progress.

The principal authorized completion of the useful remaining work after Phase 7
and explicitly removed v3 data migration from scope.

## Scope correction

The Phase 7 acceptance record listed several things that were not run. They are
not all unfinished product steps:

- v3 migration is not required;
- public-network exposure is not required for the local-first system;
- cloud trust brokerage, billing, and wildcard routing are explicit non-goals;
- SMA must not acquire Teams workflow authority; and
- access to historical or unrelated production data is unnecessary.

The actual remaining step is a repeatable local production installation of the
already qualified system.

## Deliverables

1. `tekroo init-local` creates a deployment from explicit absolute inputs.
2. It creates a fresh Teams database identity and never reads v3 data.
3. It produces deployment-owned secrets, authorization policy, provenance,
   team manifest, signed role library, role worktrees, SMA hook bindings,
   production configuration, state/evidence directories, and a LaunchAgent
   definition.
4. It fails closed on unsafe paths, non-loopback endpoints, shared Teams/SMA
   database identity, dirty tracked source, pre-existing deployment state,
   branch collision, missing accepted assets, or invalid generated config.
5. The existing isolated installer builds only `tekrood` and `tekroo` and the
   lifecycle scripts start, stop, restart, and inspect the configured service.
6. A fresh local deployment starts against the installed MongoDB, OpenHands,
   accepted local model, and SMA retrieval bridge.
7. The supported operator surface completes a live smoke feature without
   direct MongoDB writes or hand-authored kernel choreography.
8. Current documentation replaces the obsolete bootstrap-era README.

## Acceptance

Phase 8 passes when the generated configuration is accepted by the production
loader, the installed service starts cleanly, health/status/role inspection
works, a fresh operator feature reaches its expected bounded terminal state,
restart preserves the durable state, and normal plus Mongo integration suites
remain green.

Automatic startup remains an explicit operator choice: the generated
LaunchAgent is not installed or loaded by initialization itself.
