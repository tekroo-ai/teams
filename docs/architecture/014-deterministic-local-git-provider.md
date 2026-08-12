# Phase 3 Step 11 — deterministic local-Git release provider

Status: implementation candidate authorized by the principal statement `Proceed to step 11.` on 2026-08-11 after the accepted Step 10 commit was released and remotely verified.

## Qualification boundary

This step implements one concrete `kernel.ReleaseProvider` for isolated local Git repositories. It is a qualification adapter, not a GitHub adapter and not production release authority.

The provider accepts only:

- an exact contract-derived release request;
- `FF_ONLY_ORDERED` merge strategy;
- `FAIL_NO_IMPROVISATION` conflict policy;
- the exact qualified Git version;
- a bare repository resolved beneath one configured allowed root; and
- a raw local path or local `file://` URI. Every network URI scheme is rejected.

The gate creates all provider targets beneath fresh test-owned temporary directories. It never supplies `tekroo-ai/teams`, `teams-v3`, another user repository, or a pre-existing worktree as a provider target.

## Effect algorithm

For each requested ordered merge, the adapter:

1. resolves symlinks and verifies the repository remains under the configured allowed root;
2. verifies the repository is bare and the running Git version exactly matches the durable plan;
3. resolves the exact base ref, qualified base commit, change ref, and planned head;
4. returns `MISSING` or `FAILED` without mutation when the planned objects or relationships disagree;
5. verifies the qualified base remains an ancestor of the current base and the current base remains an ancestor of the planned head;
6. performs one compare-and-swap `git update-ref <base> <head> <observed-current>`;
7. reads authoritative Git state again using a bounded context detached from caller cancellation; and
8. reports `MERGED`, `ALREADY_MERGED`, `FAILED`, or `UNKNOWN` from that read.

Git is invoked directly without a shell. Hooks are disabled, interactive credentials are disabled, lazy object fetching is disabled, locale and timezone are fixed, and every subprocess has a timeout.

## Ambiguous outcomes

An update process may time out after its ref update succeeds. The adapter never maps that timeout directly to failure. It performs authoritative reconciliation:

- exact head now present: `MERGED`;
- exact head already contained: `ALREADY_MERGED`;
- authoritative eligible but unmerged state: `FAILED`;
- authoritative state unavailable: `UNKNOWN` plus an observable error.

The separate `Reconcile` method performs reads only. It never calls `update-ref`.

## Determinism and concurrency

The base ref update includes the previously observed commit as the old-value compare-and-swap operand. Duplicate and concurrent calls therefore converge on one authoritative ref. Within a provider process, reuse of the same provider idempotency key for a different request is rejected without mutation. Kernel command idempotency remains the durable organizational fence.

Cumulative ordered heads are supported: each later planned head must descend from the current base, while every observation continues to bind the original qualified base and the exact planned head. This matches the Step 10 directed merge DAG.

## Evidence classification

- OBSERVED: ref/object identities and trees returned by Git commands against the isolated bare repository.
- COMPUTED: ancestor relationships, request digests, allowed-root containment, and deterministic outcome classification.
- INFERRED: none. An unavailable authoritative read remains `UNKNOWN`.

## Explicit exclusions

- No GitHub, GitLab, forge, credential, webhook, or network-remote adapter.
- No push, pull-request mutation, branch-protection interaction, or production repository mutation.
- No MongoDB release projection.
- No deployment, migration, rollback, load, latency, or throughput qualification.
- No OpenHands or SMA integration.

The candidate may be pushed only after explicit principal acceptance.
