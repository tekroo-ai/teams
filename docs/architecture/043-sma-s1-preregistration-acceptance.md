# SMA-S1 scientific preregistration acceptance

Date: 2026-08-13  
Decision: **ACCEPTED / FROZEN**  
Execution authority: **NONE**

## Accepted artifact

- Path:
  `investigations/sma-q1/layered/sma-s1-preregistration.json`
- SHA-256:
  `e663f5b732c22248adc5fab2f536dbf02dd72337f7c8428f225937e72d6a7f8c`
- Lineage: `SMA-S1`
- Claim under investigation: `SMA_SEMANTICS_QUALIFIED`

The principal approved the S1 scientific preregistration on 2026-08-13. The
accepted artifact differs from the reviewed draft only in administrative
acceptance fields and removal of `-draft` from its filename. The reviewed draft
SHA-256 was
`deea7421a6eac47ef4a5182d3ac393c515481b920defea5bd8aa4782fbc5358a`.
No case, repetition, fixture, predicate, threshold, evidence rule, failure
classification, retry rule, or adjudication rule changed.

## Authority boundary

This acceptance freezes the scientific design. It authorizes preparation of a
separate S1 execution-identity draft. It does not bind an implementation or
environment and does not authorize harness implementation, harness execution,
measured execution, corrective reruns, SMA or OpenHands changes, production
data access, commit, push, deployment, or production use.

Before any measured S1 case can run, the principal must separately review:

1. an exact execution-identity manifest binding every required implementation,
   artifact, dependency, fixture, harness, configuration, reviewer, and
   disposable namespace;
2. an independently sealed offline harness-qualification receipt bound to that
   identity; and
3. a single-use execution authorization naming the accepted preregistration,
   execution identity, and harness receipt digests.

## Historical boundary

V12 remains terminal **FAIL / NO-GO** and contributes zero result credit to
S1. No V13 artifact is permitted. Accepted
`tekroo.kernel.contracts/0.7.0` remains immutable.
