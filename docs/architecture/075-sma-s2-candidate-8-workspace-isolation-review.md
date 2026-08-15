# SMA-S2 candidate 8 workspace-isolation review

Date: 2026-08-14  
Status: `PASS_READY_FOR_PRINCIPAL_REVIEW`

## Recommendation

`GO` for candidate-8 principal acceptance/freeze only.

This is not authorization to prepare an execution identity, perform a live
preflight or dress rehearsal, or execute the measured SMA-S2 corpus.

## What candidate 8 changes

**OBSERVED:** Candidate 7's zero-credit rehearsal proved the ten fast-terminal
case-1 repetitions, then stopped when the later four-source corpus seed observed
ten memories. The retained error digest exactly matched
`memory count exceeded expected 4: 10`.

**OBSERVED:** Candidate 8 separates the two populations:

- every `SMA-S2-001-FIRST-PROMPT-EMPTY` repetition uses
  `/tmp/tekroo-sma-s2-successor-candidate-8/actor-empty`;
- the SMA capture allowlist contains exactly `actor-alpha` and `actor-beta`;
- `actor-empty` is rejected if it enters that allowlist;
- case 1 is rejected if the candidate SMA service or bridge is active;
- the case-1 workspace role and hashed allowlist/workspace identities are
  retained in the raw evidence journal; and
- cleanup adds an explicit empty-workspace absence oracle.

The alpha and beta workspaces remain reserved for the exact four-source corpus
and the later capture/retrieval cases. Candidate-8 conversations are retained
until normal cleanup; the correction does not hide the problem by deleting
case-1 evidence before corpus seeding.

## Offline evidence

**OBSERVED:** The final optimized-Python qualification receipt is `PASS`: 13 of
13 controls passed with no skipped tests.

The controls reject:

- case 1 sharing actor-alpha;
- actor-empty entering the capture allowlist;
- alpha disappearing from the allowlist;
- an empty workspace outside the candidate-owned root;
- a synthetic corpus source count other than four;
- runtime allowlist leakage;
- case 1 running while the candidate bridge/service is active;
- the historical ten-case-1 plus four-seed shared-workspace population;
- workspace-isolation mutations in the future identity, dress authorization,
  or dress receipt; and
- optimization-mode or default-deny bypass.

**COMPUTED:** The isolation simulation produces four capture-eligible seed
events with candidate 8 and reconstructs fourteen eligible events when the ten
case-1 events are deliberately moved back into actor-alpha.

**OBSERVED:** Qualification made zero network calls, zero OpenHands
conversations, zero SMA or deterministic-stub calls, zero live dress attempts,
and zero measured attempts.

The first 12/12 offline receipt remains retained at zero credit. It was
superseded only to add the explicit historical 10-plus-4 contamination
simulation and mutation control; the final receipt passes 13/13.

## Preserved science and scope

**OBSERVED:** Candidate 8 retains the accepted 20 cases, 103 measured
repetitions, 59 predicates, 10 evidence requirements, thresholds, fault
schedule, exact corpus, oracle matrix, deterministic-stub behavior, terminal
observation rules, bound corpus-source constructor, 34-operation dress plan,
product commits, and future claim. No Teams, SMA, or OpenHands product code
changed.

**COMPUTED:** The successor changes qualification workspace routing, evidence,
identity fences, and cleanup only.

**INFERRED:** The candidate-7 workspace-contamination mechanism is closed at
the offline contract level. Live success is not established. A future live
rehearsal still requires candidate acceptance, a separately prepared and
accepted execution identity, immediate mutable-state checks, and a new
single-use authorization.

## Bound review artifacts

- Candidate-8 preregistration SHA-256:
  `d81f6878a0b06462b792e50d7bd3e8708e971ce888d22a95751127ccbaa35231`
- Remediation authority SHA-256:
  `b25ce461723310bfa4fa4604356c229efffacf188f24d6fd6a3a179d2ef833da`
- Driver SHA-256:
  `717d29b96107c7cc86c1aeceeeb9b32ab9aa8776bf6b3ec18496dd86a0305d38`
- Configuration SHA-256:
  `3cc553c6a618b2e4ca6151906c700805303012ef0cb49d270c33291edd517f44`
- Offline harness SHA-256:
  `2365421604e45518826dbed4b5b03fccd445943997873e9363d5c8c2db55cd62`
- Final offline receipt SHA-256:
  `c2ae00d8e23944f9e7fb62ba706bc3b2afe9c24929021904b5d466e8bb990175`
- Retained superseded receipt SHA-256:
  `036bfbdbd4ab4e56b9f09c7fe2558c6d9962c5e548dce288855732a9a6c37149`

All 29 path-and-SHA references in the candidate preregistration matched their
current bytes at review time.

## Next gate

The principal may accept/freeze candidate 8 by its exact preregistration
identity. Only after that separate decision may the coordinator request
authority to prepare a candidate-8 execution identity. All live and measured
gates remain closed.
