# SMA-S2 final-P2 composite acceptance

Status: **ACCEPTED / FROZEN**
Recorded: 2026-08-30T13:15:05Z

## Decision

The principal accepted and froze the final-P2 S2 composite result with the
statement: `ACCEPT/FREEZE and advance.`

The accepted machine record is
`investigations/sma-q1/layered/sma-s2-final-p2-composite-scientific-adjudication-acceptance.json`
with SHA-256
`cac6cd68ff7b60fdd36a593779221ce90b6553a570b71f6fb90b6f593184e0c3`.

## Accepted result

- Accepted S1 receipt: `3315838d0c5047189494cece49b953481431321d16e238efa338712be076bee4`
- Accepted S2 preregistration: `b7315835b6291d44d3d8cb918dbec1f16c1e64f2106fd70212c6a8f7ec678188`
- Composite result: `06614c7fe76acc89a80e3cc8d5e9c2adaa9ab3fc321f222c7ae6ed76748ec497`
- Composite operations: **103 / 103 PASS**
- Accepted claim: `OPENHANDS_SMA_BOUNDARY_QUALIFIED`

The composite preserves the 98 unaffected passing operations from the original
measured execution and replaces only the five case-13 concurrency operations
with the focused passing result. The replacement peaks were 4, 3, 3, 4, and 4
simultaneous in-flight requests.

Final disposable inventory was absent. No real-model calls or
production/historical-data access occurred in S2.

## Advancement

Step 15 advances to **M1 — model-profile qualification**. Advancement authorizes
M1 preparation and offline qualification only. It does not authorize M1 model
calls or E1 execution.

Accepted S1, S2 science, measured evidence, and this acceptance record are
immutable. Any correction requires a successor artifact.
