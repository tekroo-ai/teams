# SMA-S1 final-P2 R1 candidate 2 acceptance

Date: 2026-08-28
Decision: **ACCEPT / FREEZE**
Measured S1 execution: **NOT RUN / NOT AUTHORIZED**

The principal stated `Proceed.` in direct response to the recommended bounded
decision:

> ACCEPT/FREEZE SMA-S1 final-P2 R1 candidate 2 identity
> `2624b25095e99ca20d5d1da936e0276449ac533eca9251ab52828a6dcd972f59`
> and offline qualification
> `cdea4d04eac8c6c9dc4682f441d2bd55405b24c24b72defd9bdbc1a0f0455af9`.
> This does not authorize measured execution, S2, M1, or E1.

The decision accepts and freezes exactly:

1. `investigations/sma-q1/layered/sma-s1-execution-identity-p2final-r1-candidate-2.json`,
   SHA-256
   `2624b25095e99ca20d5d1da936e0276449ac533eca9251ab52828a6dcd972f59`;
2. `OUTPUT/phase-3/sma-s1-p2final-r1-candidate-2-offline-qualification.json`,
   SHA-256
   `cdea4d04eac8c6c9dc4682f441d2bd55405b24c24b72defd9bdbc1a0f0455af9`.

**OBSERVED:** the independent review passed and is retained at SHA-256
`1324a5220d74d259992563f9bd9154ecd4c9cba7814e917fa92f188b754c8f94`.

**COMPUTED:** zero of 71 measured scientific repetitions have executed and no
single-use execution attempt is authorized.

The accepted artifacts are immutable. Any change requires a successor identity
and review. The next gate is a separate principal decision on one single-use
measured S1 execution; this acceptance does not start services or authorize a
live preflight.
