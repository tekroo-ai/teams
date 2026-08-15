# SMA-S2 candidate 6 harness-identity review

Date: 2026-08-14  
Status: `PASS_READY_FOR_PRINCIPAL_REVIEW`

## Recommendation

`GO` for candidate-6 principal acceptance/freeze only.

This is not authorization for execution-identity preparation, live preflight,
or measured S2 execution.

## What candidate 6 fixes

**OBSERVED:** Candidate 5 declared that stub and bridge ports had to be bound,
but its executable fence did not inspect either port and its inherited launcher
asked the operating system to choose stub port `0`.

**OBSERVED:** Candidate 6 makes the endpoint identity executable:

1. The future execution identity must contain exactly `stubHost`, `stubPort`,
   `bridgeHost`, and `bridgePort`.
2. Both hosts must be `127.0.0.1`; both ports must be numeric and in the
   permitted range; the bridge port must be `8130`; and the ports must differ.
3. The live-preflight authorization, live-preflight receipt, and future
   measured authorization must repeat those exact endpoint values.
4. The stub launcher passes the identity-bound host and port explicitly, and
   rejects any ready receipt that reports a different endpoint.
5. Both ports must be unoccupied immediately before the live preflight and
   immediately before measured execution.
6. The measured fence requires a passing live-preflight receipt bound by path,
   SHA-256, execution-identity SHA-256, and exact endpoints.

The driver now contains a separately fenced `--live-preflight` mode. It is
default-deny and cannot run without a later principal authorization record.

## Offline evidence

**OBSERVED:** The final optimized-Python qualification receipt is `PASS`, with
13 of 13 controls passing and no skipped test.

The controls reject:

- every missing endpoint field;
- mutated hosts, non-numeric or out-of-range ports, wrong bridge ports, and
  stub/bridge port reuse;
- endpoint disagreement across identity, preflight authority, preflight
  receipt, and measured authority;
- missing, failing, mutated, identity-misbound, or cleanup-failing preflight
  receipts;
- occupied stub or bridge ports;
- deterministic-stub ready endpoint disagreement;
- mutated acceptance and artifact hashes; and
- optimization-mode or default-deny bypass.

**OBSERVED:** Qualification made zero OpenHands conversations, zero SMA or
service calls, zero deterministic-stub network calls, zero live preflights,
and zero measured attempts. Loopback sockets were bound only to prove the two
occupied-port controls.

Two earlier receipts remain visible at zero credit. The first failed one
self-referential source-safety test. The second passed but was superseded when
the live-preflight runner and its authorization fence were added. Neither is
used to support candidate 6.

## Preserved science and scope

**OBSERVED:** Candidate 6 retains candidate 5's 20 cases, 103 repetitions, 59
predicates, 10 evidence requirements, 25 semantic evidence mutations,
thresholds, fault schedule, claim, corpus, matrix, stub behavior, and bound
product identity. No product code changed.

**COMPUTED:** The remediation changes one domain only: qualification-harness
runtime identity and prerequisite enforcement.

**INFERRED:** The candidate-5 static blocker is closed. This does not establish
that the live environment will pass. That empirical question remains for one
later authorized, identity-bound live preflight.

## Bound review artifacts

- Candidate-6 preregistration SHA-256:
  `06e7a689d46dfc759d07ca2f1099854c42e0c21e4dc84cd1fde0a0f612ae6cee`
- Driver SHA-256:
  `b58c8621997b86347acb29d13b73c9167113cdfdd79c0041599f46b46b92bb1f`
- Configuration SHA-256:
  `c85b83f6707edb86f2f71fafdb4fd404db03f70d0c16529c413b93bccb8e94c2`
- Offline harness SHA-256:
  `665d00001478ee147415514c22635412cc287a8eecec2c6e8acb4e2b266d1e1c`
- Final offline receipt SHA-256:
  `8d43341fd59f447222f629422139e59d2e275ef37e3ae6e0bc9f2856111314e6`

## Next gate

The principal may accept/freeze candidate 6 by exact preregistration identity.
Only after that separate decision may the coordinator prepare an exact numeric
endpoint identity and request a distinct single-use live-preflight authority.
Measured execution remains at least one gate later and must bind a passing
live-preflight receipt.
