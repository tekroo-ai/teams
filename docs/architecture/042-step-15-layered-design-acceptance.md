# Phase 3 Step 15 layered redesign — principal acceptance

Date: 2026-08-13  
Decision: **ACCEPTED FOR PREREGISTRATION DRAFTING**  
Execution authority: **NONE**

## Accepted decisions

The principal authorized the coordinator to proceed after review of
`041-step-15-layered-qualification-specification.md`. The following prospective
design decisions are accepted:

1. replace the terminated monolithic Step 15 lineage with four independently
   qualified layers: `SMA-S1`, `SMA-S2`, `SMA-M1-<profile>`, and
   `SMA-E1-<runtime-tuple>`;
2. preserve the original eighteen scenario intents and 96 repetitions in the
   final E1 integrated soak;
3. grade arbitrary exact-response markers only as model diagnostics unless
   exact formatting is prospectively declared to be a product requirement;
4. preserve independent component credit, component-attributed failures,
   prospective retry and continuation rules, and the prohibition against
   retroactive regrading; and
5. keep Teams event-export and SMA live-shadow qualification in a separate
   lineage.

## Authority granted

This acceptance authorizes creation and local validation of four **draft**
preregistration packages. It does not accept or freeze any package and does not
authorize:

- measured or live execution;
- a V13 artifact or continuation of the V-series lineage;
- a harness implementation or modification of SMA or OpenHands;
- acceptance of the event-export contract;
- SMA client or live-shadow work;
- commit, push, deployment, production data access, or production use.

Each draft package must return to the principal for separate review and
acceptance. After acceptance, each layer still requires an exact execution
identity, an independently sealed offline harness-qualification receipt, and a
single-use execution authorization before any measured case may run.

## Historical preservation

Step 15 V12 remains terminal **FAIL / NO-GO**. V2 through V12 receipts and
adjudications remain immutable and receive no credit in the replacement
lineages. Accepted `tekroo.kernel.contracts/0.7.0` remains immutable.
