# Phase 3 Step 15 readiness — WP5 alignment

Step 15 has advanced to readiness intake but experimental execution has not
started. The accepted native-path manifest is immutable on `origin/main` at
commit `e105abb9215358df3470a6d295c1dd6afca6e5bb` with SHA-256
`c618371d571d5333aebd2bdc83d2db2559115f5ec3e1903b33e8e0ab157edf5d`.

The observed SMA worktree has WP5 active at WP5-A1. Its execution capsule does
not yet name the accepted manifest digest or the `SMAQ1N-*` scenarios, and its
work-package sample counts do not equal the frozen per-scenario repetitions.
Consequently, a future Gate 5 PASS is not automatically Q1 evidence.

Before a WP5 pass intended for Q1 reuse begins, its receipt protocol must:

1. cite the exact accepted manifest digest;
2. name every `SMAQ1N-*` scenario it intends to cover;
3. execute and retain the exact frozen prompts, faults, repetitions, expected
   outcomes, identities, raw receipts, and measurement rules; and
4. label every non-matching scenario `NOT_RUN` rather than translating a
   similar WP5 result into Q1 credit.

After WP5 Gate 5 is accepted and its implementation is a clean immutable commit,
Teams will adjudicate the eight potentially reusable scenarios individually.
The Step 15 execution manifest will then contain only scenarios that remain
`NOT_RUN`, including the ten scenarios already reserved by Step 14A.

This readiness finding does not modify the SMA worktree, interrupt WP5, or
authorize Step 15 experiments.
