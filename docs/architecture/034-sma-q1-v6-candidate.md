# SMA-Q1 V6 single-attempt candidate

Date: 2026-08-13  
State: **PROPOSED — NOT ACCEPTED, NOT AUTHORIZED, NOT EXECUTED**

## Outcome

V6 is ready for one combined principal decision. It carries the unresolved V5
retrieval observation forward unchanged and fixes the process failure that
allowed an identity-schema mismatch to survive until authorized execution.

V6 permits one execution and no corrective rerun.

## What changed

V6 adds an exact offline preflight-contract test. Before acceptance, it loads
the actual candidate manifest, runner, identity, and non-authorizing
authorization template through the same preflight field-access function used
at runtime. The first live-service operation is replaced by a sentinel. PASS
requires reaching that sentinel without any missing field, schema error, hash
mismatch, or earlier exception.

After a final authorization artifact is created, the same test must run again
against that exact artifact before live execution. The runner itself repeats
that offline contract immediately before starting the normal execution path.

This directly covers the `executionFence` field access that stopped the final
V5 attempt.

## Scientific continuity

V6 changes no memory, prompt, scenario, repetition, threshold, stop condition,
or adjudication meaning. It retains V5's deterministic fixture preparation and
telemetry-only qualification:

- exactly three existing qualification retrieval calls;
- no retry, warmup, delay, or added retrieval call;
- per-call timing, trace, identity, hit, and response telemetry; and
- one aggregate bridge snapshot only after all three outcomes have returned.

The V5 first-request miss remains an unresolved observation. V6 does not label
it a cold-start failure or weaken the positive-control criterion.

## Verification

**OBSERVED:** The candidate passed:

- Python compilation;
- inherited source, retained-V3, partition, and fixture-driver checks;
- fixture-driver compilation against the pinned SMA service JAR;
- V6 workspace-partition checks;
- manifest, runner, identity, and template hash bindings; and
- the exact offline preflight contract through
  `OFFLINE_PREFLIGHT_REACHED_FIRST_LIVE_SERVICE_OPERATION` with zero live calls.

No V6 output, service, database, Qdrant collection, conversation, or scenario
was created. The SMA execution worktree remains clean.

Candidate hashes:

- V6 manifest: `56a1573b0a6ea17c52524c2b01f5380b08396237fc33562702bfd5475c48830c`
- V6 runner: `04097708a978508386cc834981ea6d52c16c16e58784463484784058cc9c5f0f`
- V6 identity: `900628a4431c59319d879a6412a632263b2284fc390fb495e33e2cbb4ccb8dc8`
- non-authorizing authorization template: `f85ce09594f3ac0ab31dd2dff91363279be47249a0c27dd879b2b22a353d3d66`

## Single remaining decision

The package is deliberately stopped before acceptance and execution. The next
decision may combine both operator instructions:

`ACCEPT/FREEZE V6 and authorize its single Step 15 execution.`

After that instruction, the package will be resealed, the final authorization
artifact generated, the exact offline preflight contract rerun against the
final hashes, and—only if it passes—the single V6 execution will start.
