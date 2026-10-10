# Bounded amendment request binding

The frozen 0.15.0 proposal contract remains unchanged. The native request now
specializes that schema with exact `const` values for reservation identity,
source plan version/digests, successor version, current PM execution authority,
and the durable reservation timestamp. The same request reconstructs the same
schema after restart. A new fencing epoch binds a different schema.

The model still proposes the successor task graph. The independent semantic
validator, completed-work protection, single-use assessment and finite budgets
are unchanged. Correct identity is necessary, not sufficient for approval.

## Captured r59 counterexample

OBSERVED: assessment `f99e061d-4581-7e92-871b-99d38bed9245` returned a result;
the durable decision rejected it with `STALE_SOURCE`. Raw output receipt:
`f99e061d-4581-7e92-871b-99d38bed9245:00000000000000000003`, output digest
`2d414313556e5d2115f90831636bf7f5d04ff84569e8a2275c93e49de0f21724`.

COMPUTED from the captured proposal and prior plan, independently of the
validator's first-return behavior:

- Source version was 2 instead of 1 and source digest did not match.
- Successor time was later than the actual decision time.
- Retained definitions were changed, including a completed validator.
- A completed provider was replaced, outside the blocked downstream closure.
- Successor dependencies/validation references still named removed nodes.
- A retained handoff contract was changed beyond endpoint remapping.
- An impact was attributed to a retained task rather than a fresh successor.

These are independent defects, not additional recorded harness decisions.
The input snapshot is `r59-routing-raw-2026-10-10T01-34-22.218Z.json`, SHA-256
`a8ef1f5a48ced57880f825dd4130fa60b34b11e2350976dedfa14f3fb63bb5ef`, retained
in the operator task directory. `r59-full-proposal-audit.json` identifies exact
task IDs, fields and raw receipt references; it also states checks not rerun.

The assessment guidance now makes these existing policy limits explicit.
An architect answer remains evidence, not authority to override the policy.
A repair that cannot satisfy the frozen limits must remain rejected; neither
this change nor a fresh test promises that every design is repairable.

## Verification boundaries

Unit regressions check exact request binding, stable reconstruction and each
captured defect family. Mongo/native tests check retained-context assessment,
independent decision and forward publication through restart. The optional live
grammar delta uses the current PM model/reasoning settings and full bound schema
with captured HTTP receipts. It attests schema compatibility only, not semantic
PM qualification or an intervention-free feature run.
