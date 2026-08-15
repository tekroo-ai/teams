# SMA-S1 evidence-v2 scientific PASS acceptance

Date: 2026-08-14  
Decision: **ACCEPTED / FROZEN**  
Accepted claim: **`SMA_SEMANTICS_QUALIFIED`**  
Downstream execution authority: **NONE**

## Accepted identity

**OBSERVED:** the principal stated:

> ACCEPT/FREEZE SMA-S1 evidence-v2 scientific adjudication
> 0e5ace0acbe6e2441755ae656de36096e38b2364d27fb45ee844f2422abf2f7d.

**COMPUTED:** the supplied SHA-256 exactly matches
`OUTPUT/phase-3/sma-s1-measured-p2m-evidence-v2-sealed-v1-attempt-1/scientific-adjudication.json`.
The adjudication remains unchanged.

The separate machine-readable acceptance record is
`investigations/sma-q1/layered/sma-s1-evidence-v2-scientific-adjudication-acceptance.json`,
SHA-256
`78e766ba6f8fd1fa9958515c9a0bd75174edd8de23bcb0183b66f0ca7340b230`.

## Frozen evidence

**OBSERVED:** before acceptance was recorded, the bound hashes were reverified:

- execution receipt:
  `485152e2fe74b62c426d0c2c35769379f1d05a5da7c94148bdffc5b27fdcadf3`;
- append-only raw journal:
  `334d5e1e1845a7445f623ed88be957c7b433e5e5321c0d086a3c07715f91f5b4`;
- post-cleanup verification:
  `8693073ad3b80034e64c19574dcc8dfe77d058e08c4faa0fbf3346cd96c5f345`;
- consumed-authorization record:
  `771fe27b8c31006e73952d7638d860af54dcdeaf2292b20f1e93e1580b838eea`.

The single measured authority remains consumed and non-reusable.

## Scope and next gate

This acceptance qualifies deterministic SMA semantic behavior through the
declared service interfaces and persistence adapters. It does not qualify the
OpenHands/SMA boundary, generative model behavior, an integrated runtime,
process-control authority, or production readiness.

**INFERRED:** the next sequential gate is S2 preregistration completion and
review. The existing S2 draft must first bind this accepted S1 result and the
exact OpenHands, hook/plugin, bridge, schema, deterministic-stub,
configuration, reviewer, and isolation identities. S2 execution remains
separately gated and unauthorized.
