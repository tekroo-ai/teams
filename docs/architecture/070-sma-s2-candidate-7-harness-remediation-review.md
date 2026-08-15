# SMA-S2 candidate 7 harness-remediation review

Date: 2026-08-14  
Status: `PASS_READY_FOR_PRINCIPAL_REVIEW`

## Recommendation

`GO` for candidate-7 principal acceptance/freeze only.

This is not authorization to create an execution identity, run the live
no-credit dress rehearsal, or execute the measured SMA-S2 corpus.

## Why candidate 7 exists

**OBSERVED:** Candidate 6's only measured attempt ended `INCONCLUSIVE`. Its
receipt recorded ten substantive failures for the first success case followed
by a harness failure on the first corpus-seeding operation. The attempt retains
zero claim credit and cannot be rerun.

**OBSERVED:** Review of the frozen candidate-6 driver identified two harness
boundaries that were not exercised by its narrower live preflight:

1. the observation loop could grade a conversation after OpenHands reported a
   terminal status but before the expected agent terminal event was visible;
2. corpus seeding called a module-level default conversation constructor rather
   than the candidate-bound deterministic constructor.

Candidate 7 preserves the accepted scientific question and corrects those
harness boundaries prospectively.

## Prospective corrections

**OBSERVED:** Candidate 7 now requires an exact terminal event after the
submitted user prompt: the expected deterministic agent `MessageEvent` for a
success operation, or a `ConversationErrorEvent` for a fault operation. The
complete observation must have the same identity signature on two consecutive
event fetches before grading can begin.

**OBSERVED:** Corpus seeding is routed through the candidate runtime's bounded
conversation constructor with `max_iterations=1`, no default profile, no
default tools, no encrypted-secret configuration, and no real-model execution.
The inherited source factory is rebound only for seeding and restored in a
`finally` block.

**OBSERVED:** HTTP-boundary failures now retain a sanitized phase, error type,
message digest, numeric HTTP status when present, response digest and length,
and hashed server error identity. Response bodies and secrets are not copied
into the evidence record.

**OBSERVED:** A future measured fence cannot open until a separate 34-operation
no-credit dress rehearsal has passed. That rehearsal covers:

- ten repetitions of the fast-success terminal-event path;
- at least one operation from all 20 cases;
- all four case-19 deterministic model fault modes; and
- all three case-20 cancellation/shutdown repetitions.

The dress receipt must bind the accepted candidate-7 execution identity and
exact runtime endpoints, record cleanup `PASS`, record 34 completed operations,
and retain both measured credit and claim as zero/null.

## Offline evidence

**OBSERVED:** The final optimized-Python offline qualification is `PASS`: 11 of
11 controls passed, with no network use, no OpenHands conversations, no SMA or
stub service calls, no dress-rehearsal attempts, and no measured attempts.

The controls exercise:

- the unchanged 20-case/103-repetition science and exact 34-operation dress
  plan;
- exact success and fault terminal-event requirements;
- the fast-terminal event-visibility race;
- the candidate-bound corpus-source constructor and temporary factory binding;
- sanitized HTTP-boundary failure evidence;
- positive and negative identity, dress-receipt, and measured fences;
- mutated dress-receipt rejection;
- acceptance and dependency-closure enforcement; and
- optimized-Python and default-deny behavior.

**OBSERVED:** The first offline qualification remains retained at zero credit.
It passed 10 of 11 controls and failed because the synthetic race fixture
omitted the frozen `literalPrompt` field. The fixture was corrected; no product
code, scientific predicate, or measured result was changed.

## Preserved science and limits

**OBSERVED:** Candidate 7 retains candidate 6's 20 cases, 103 repetitions, 59
predicates, 10 evidence requirements, thresholds, deterministic fault schedule,
claim, corpus, oracle matrix, deterministic-stub behavior, product commits, and
endpoint-binding rules. No Teams, SMA, or OpenHands product code changed.

**COMPUTED:** The candidate changes qualification-harness observation, corpus
construction, failure evidence, and prerequisite coverage only.

**INFERRED:** The two known candidate-6 harness omissions are closed at the
offline contract level. This does not establish that the live integration will
pass. The next empirical step must be the separately authorized no-credit dress
rehearsal; another measured attempt is intentionally still several gates away.

## Bound review artifacts

- Candidate-7 preregistration SHA-256:
  `66530bb10fe7e46f23e8f3f550c83a89203905cc844dba8732d8794dba9575d3`
- Remediation authority SHA-256:
  `324f48993f463ede378b07b74246ee1cada349e177ef64c15027020379fe642b`
- Driver SHA-256:
  `b14d75aff7c43558828b354312961adfe9a7e038daef19971f4bc7b8990eb799`
- Configuration SHA-256:
  `32bc3c3fd03256a7ef6c44c60f5086b36235f95074f48356ece2e223031b9719`
- Offline harness SHA-256:
  `b0a67769223baf859be372859a314275cd9b5a749f53b194545f714ea53287f3`
- Passing offline receipt SHA-256:
  `f1061561784e2fd6730e2370365e90e2057afa1a779afa056ca8f5fe87f942b0`
- Retained failed offline receipt SHA-256:
  `ec2dd48bd5a46e6e6ee2f6f015c28cf32788cf22f64eea3ca0f20217b866b65f`

## Next gate

The principal may accept/freeze candidate 7 by its exact preregistration
identity. Only after that separate decision may the coordinator prepare a
candidate-7 execution identity for review. A later, distinct single-use
authorization is required for the 34-operation live dress rehearsal. Measured
execution remains unauthorized and requires the accepted identity plus a
passing, identity-bound dress-rehearsal receipt.
