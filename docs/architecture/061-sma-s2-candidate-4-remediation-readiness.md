# SMA-S2 candidate 4 remediation readiness

Date: 2026-08-14

## Result

Candidate 4 is **ready for independent review**, not yet accepted and not authorized for live preflight or measured execution.

Candidate SHA-256: `5875ec59ec1cb0e8d8d20f18bc0429e1324a160eed827222dcfb4c0cabc6d0ae`.

## Why this is convergence rather than another measured iteration

Candidate 3 was stopped before preflight because its executor could report PASS without proving every accepted predicate. Candidate 4 changes the control structure:

- all 59 accepted predicates have stable mandatory oracle IDs;
- all 10 required-evidence clauses are independently mapped;
- all 20 handlers contain their assigned oracle bindings;
- each of the 201 applicable oracle positions was tested once as false and once as missing, and every mutation was rejected;
- candidate-4 configuration and driver namespaces are identical;
- cases 11 and 14 now have sealed non-generative stimuli that can actually create delegation and ineligible reasoning/tool/utility events;
- no Python `assert` controls the scientific result;
- live execution remains default-denied behind an exact dependency, preregistration, identity, and single-use-authorization fence.

The offline qualification is 10/10 PASS under `python3 -O` with no network use. It does not claim that the live OpenHands/SMA runtime passes; that remains the purpose of the later preflight and single measured execution.

## Review boundary

The coordinator authored the harness and therefore cannot perform the preregistered independent-reviewer act. The workspace principal should review candidate 4 against accepted candidate 2. Acceptance/freeze, if granted, should name candidate 4 by SHA-256. Only after acceptance should an execution identity and a separate live-preflight authorization be prepared.

No candidate-3 or accepted artifact was modified. No OpenHands conversation, SMA service, live preflight, deterministic-stub network request, measured case, or real model call occurred.
