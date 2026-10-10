# Bounded verification concurrency and regression preparation

Implementation note, 2026-10-10. This does not amend an accepted interface contract,
model profile, verification selection, recovery budget, or feature acceptance.

## Scheduling and execution

OBSERVED (source: `internal/verification/queue.go`, `execution.go`): the shared
daemon-process scheduler admits at most two verifications: two focused checks,
or one whole-module check plus one focused check. A second whole-module check
waits. At most two focused grants bypass an eligible whole-module waiter; FIFO
within each class, cancellation and duplicate-release safety remain covered.

OBSERVED (source: `agenttools/isolated_go_tests.go`): stable workspace paths now
include the acquired lane. Concurrent consumers do not share writable checkout
contents. Each operation still snapshots sources and removes its own workspace.
Default Go package concurrency is two, in-package parallelism is two, and
GOMAXPROCS is capped at two. Explicit lower limits are preserved. Package
selectors, tags, timeouts, assertions and test-binary arguments are not reduced.

## Compiler reuse, not verdict reuse

OBSERVED (source: `agenttools/compilation_cache.go`): preparation hints retain
16 recent exact input keys per namespace, with at most 32 namespaces. Alternating
selectors/lanes no longer replace the only retained key. An empty/recreated
namespace is re-primed. Hints can cause safe cache misses, never permit skipping
requested execution; actual Go tests still use `-count=1`.

OBSERVED (source: `agenttools/verification_fixture_test.go`,
`compilation_pipeline_test.go`): the historical incompatible-flags/double-link
counterexample now starts with reusable compiler artifacts from the suite's
compiler-only cache. Its distinct fixture sources still compile; all link-count,
source-path and real-execution assertions remain. Dedicated cold/no-cache,
complete eviction, changed-source failure and failed-compilation cases remain.

## Qualification and limits

OBSERVED: final qualification ran all module packages at unchanged base commit
`dc6ddcfc535e623d92a72b3656e82c1b4affb6ee`, overlaying only this change's source
and tests. Unrelated pre-existing dirty files were excluded, not overwritten.

OBSERVED: the final full run exited zero in 132.878 seconds, with 2,047 passing
test/subtest results, zero failures and nine pre-existing opt-in skips. New real
test binaries meet at a barrier to prove execution overlaps; a focused real Go
tool also completes while a broad scheduler lease remains held. An alternating
selector fixture deliberately fails on all three executions, proving preparation
reuse is not a cached PASS. Scheduler race tests passed 50 repetitions.

COMPUTED: all 216 baseline agent-tool terminal results remain present with
unchanged outcomes; five results were added. Package time was 150.294 seconds
before and 111.047 seconds after, a 26.1% reduction. The baseline ran this package
alone at package concurrency one; the final measurement ran the entire module at
concurrency two. This is a local package comparison, not a controlled end-to-end
r64 speedup claim. The full regression remains measured in minutes, not seconds.

OBSERVED: the real warm focused-tool benchmark (`./adapters/operatorhttp`) passed
three measured executions, taking 4.813, 4.682 and 4.711 seconds for the whole
tool, including snapshot, execution and cleanup. Each warm receipt has zero
compiler-primer time; actual execution takes about 3.6 seconds. Its separately
reported cold warm-up took 16.533 seconds. These are representative focused-tool
measurements, not a promise that every selector finishes in five seconds or a
before/after speedup comparison. Raw phases are in
`verification-warm-focused-2026-10-10T23-00-37.222Z.jsonl`.

Raw qualification artifacts are retained under
`/Users/paul/Documents/Codex/2026-09-01/it-is-stopped-you-can-preceed/`:

- `verification-source-20zsa0-2026-10-10T22-58-14.293Z.json`: exact overlay hashes.
- `verification-final-qualified-2026-10-10T22-58-18.001Z.jsonl`: raw final events.
- `verification-final-qualified-2026-10-10T22-58-18.001Z.receipt.json`: command,
  environment, raw-output hashes, times and outcomes.
- `verification-baseline-agenttools-2026-10-10T22-42-52.372Z.jsonl`: raw baseline.
- `verification-comparison-2026-10-10T23-00-37.127Z.json`: exact result-set comparison.
- `verification-race-scheduler-2026-10-10T23-01-22.351Z.jsonl`: 50 race-checked
  repetitions, 450 test terminal results, zero failures.
- `verification-race-real-overlap-2026-10-10T23-01-43.671Z.jsonl`: the four new
  real overlap/selector/workspace tests also pass under the race detector.

An intermediate full run failed the old `-p=1` command expectation in
`TestComponentBuildDoesNotRequireUnfinishedConsumerButIntegrationDoes`. The
expectation now matches `-p=2`; its checks that broken integration fails and fixed
integration passes were preserved. That failed run is retained as
`verification-final-full-2026-10-10T22-54-21.320Z.jsonl`.

OBSERVED: no commit, deployment, run restart, feature acceptance or timer change
was performed for this implementation request. End-to-end performance under the
deployed daemon still requires a subsequent authorized deployment and run.
