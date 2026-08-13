# Phase 3 Step 15 readiness — reconciled after WP5

## Outcome

Step 15 remains `NO_GO`. WP5, the pre-Q1 latency remediation, and its evidence
seal are accepted, but source-level preflight found two direct conflicts with
the frozen Q1 resource bounds. No Q1 scenario has started.

The accepted native-path manifest remains immutable at Teams commit
`e105abb9215358df3470a6d295c1dd6afca6e5bb`, with SHA-256
`c618371d571d5333aebd2bdc83d2db2559115f5ec3e1903b33e8e0ab157edf5d`.

## WP5 evidence adjudication

WP5 and the later remediation established useful prior evidence, but neither
names the frozen manifest digest nor matches the exact scenario prompts,
faults, repetitions, and receipt method. Consequently, none is translated into
Q1 credit. All 18 scenarios and all 96 repetitions remain `NOT_RUN`.

This is not a negative judgment about WP5. It applies the prospectively frozen
evidence rule: similar behavior and a generic Gate 5 PASS are not exact Q1
observations.

## Source-visible blockers

The manifest permits zero unbounded request/response/context/queue paths. It
also caps inbound bridge requests at 1,048,576 bytes, concurrent channels at
four, and queued operations at eight.

The accepted SMA bridge currently reads the complete inbound body with a Java
`Scanner` using a whole-stream delimiter. There is no one-MiB stopping bound.
It also creates cached work and HTTP executors, which do not impose the frozen
four-worker/eight-queue boundary or expose deterministic rejection telemetry.

The bridge's existing three-memory, 4,096-character, and 500 ms bounds are
inside the frozen ceilings and do not require widening.

## OpenHands execution profile

The stored Agent Profile named `local-qwen3.6-35b-a3b-q8` currently has a
dangling LLM profile reference and is excluded from the execution plan. The
candidate plan instead resolves direct `agent_settings` from the existing
`qwen3.6-fast` LLM profile, whose secret-free API projection is pinned. That
method retains workspace-derived partitions with the `default` profile
component and avoids mutating persistent OpenHands profile state.

## Next gate

SMA needs one bounded pre-Q1 correction:

1. reject or fail open on request bodies beyond one MiB without reading them
   into an unbounded buffer;
2. use at most four workers and a queue of at most eight operations;
3. expose accepted, queued, rejected, timeout, and active-work telemetry;
4. prove overload and oversized requests cannot block prompt submission;
5. preserve the one-second hook timeout, 500 ms service budget, partitioning,
   audit-before-disclosure, and existing latency gates; and
6. commit, push, and seal raw evidence without executing `SMAQ1N-*`.

After that correction, Teams must regenerate the SMA commit/tree, JAR, source,
hook, receipt, and configuration digests. Only then can the principal authorize
the first Step 15 scenario.
