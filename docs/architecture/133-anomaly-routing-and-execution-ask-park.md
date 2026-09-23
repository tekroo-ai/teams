# Anomaly routing and the execution ask-and-park

Status: PROPOSED (design only; no implementation authorized by this document).

## Problem

The operational runtime silently absorbs most anomalies. When a role invocation
behaves pathologically, the current handling is: a fence injects a canned
correction, and if the model does not recover, the invocation is interrupted to
`FAILED` and re-rolled on a fresh attempt. The re-roll's recovery directive
restates the symptom command; it is not method-changing. This was observed in
run `_r10`: coder-1 invocation `e3da34fe` entered an orientation trap (23 file
reads, 0 writes, `recipient` searched 7x in one directory, 4 correct
`REPOSITORY_PROGRESS` corrections ignored), was interrupted, and only the fresh
retry `18f921fd` succeeded — on model variance, not a proven cure. The defect
was invisible to the operator: the task projection read `COMPLETED`.

Two properties of the current design are wrong:

1. **Anomalies are not routed.** They are either silently retried or silently
   absorbed. Nothing carries the model's own state to a decision-maker who could
   resolve it.
2. **The fence talks *at* the model, not *to* it.** A correction is a statement
   the model can passively acknowledge and ignore. It did.

The planning plane already has the right affordance and it works: when the
product owner is uncertain it emits `clarification_questions`, the feature parks
at `CLARIFICATION_REQUIRED`, a human answers, and the flow resumes. This document
proposes the execution-plane analogue, generalized into a single anomaly-routing
policy.

## Decision

Adopt one **anomaly-routing policy** with three arms. The unifying rule: route
every anomaly to the *lowest* decision-maker that can actually resolve it,
carrying the model's own state as *evidence* (never as truth). The
ask-and-park arm is one arm, not the whole policy.

### Arm 1 — Transient / self-recovering anomalies: record, do not ask

Signature: a rare model emission defect that recovers on the next turn without
intervention. Observed instance: `view_range` emitted as a truncated string
(`'[205,'`), rejected by the strict schema, corrected by the model unprompted.

Action: keep the existing retryable classification. **Emit a durable evidence
record** so the anomaly is visible in aggregate instead of vanishing. Do *not*
ask the model — it did not decide to do it, so an "ask" yields a confabulated
answer and spends a turn interrupting a recovery already in progress.

### Arm 2 — Persistent model-cognitive anomalies: ask-and-park-escalate

Signature: the model is stuck in a self-reinforcing loop it cannot exit, and the
missing thing is *direction it does not have*. Observed instance: the coder-1
orientation trap; the AGENTS.md planning-role never-submits loop.

Action — tiered, so autonomy is preserved:

1. **Ask the model.** Replace the canned correction with a question that
   requires an answer ("state the goal you are pursuing and the next concrete
   action you will take that is not a repeat"). Generating the answer places the
   model's own articulated plan into context, which it tends to stay consistent
   with. This is cheap and stays autonomous.
2. **Still stuck → escalate to a senior role** (senior-coder / architect) as an
   autonomous second decision-maker, with the model's stated goal and last
   actions as evidence.
3. **Still stuck → park for the operator.** Reuse the proven
   `CLARIFICATION_REQUIRED` park mechanism as the execution-plane analogue: the
   task parks in a durable waiting state carrying the model's answer and recent
   actions; the operator issues a *targeted* method-changing directive; the task
   resumes. This is the last resort, not the first.

The asking only earns its cost because the answer reaches a decision point. An
"ask" whose answer is then ignored and the task re-rolled anyway is worse than
today (more tokens, same blindness) and is explicitly out of scope.

### Arm 3 — Harness / infrastructure anomalies: alert, do not ask

Signature: no model-cognitive state to interrogate. Observed instances: the
shell-discipline false positive (a fence bug), the daemon wedge
(`LastError: "external execution outcome is unknown"`), MTP envelope
non-determinism.

Action: alert the operator (or auto-recover where a safe mechanical fix exists,
e.g. daemon relaunch). No model ask.

## Why "ask on every anomaly" is rejected

The literal generalization fails on our own evidence. Of the anomalies seen this
session, only the orientation trap is a good fit for an ask. The `view_range`
slip self-recovered (asking interrupts a recovery); the condenser fabrication was
the model *misled by its own memory* (asking cannot fix a poisoned summary — the
fix was condenser faithfulness, SDK `e226bbeb`); the shell-discipline false
positive and the daemon wedge have no model-cognitive state at all. Bolting an
ask onto those arms is noise. The principle that generalizes is *routing to the
right decision-maker*, not *asking the model*.

## Evidence discipline

The model's answer to an ask is **evidence, not truth**. The condenser was
observed fabricating a reading history; a stuck model asked "why?" may
confabulate a confident wrong reason. Every decision-maker in the escalation
chain weighs the answer against durable evidence (event chain, git state), per
the standing doctrine that model assertions are evidence only.

## Interfaces (indicative, to be finalized in a contract change)

- Reuse: `organization` feature clarification park (`CLARIFICATION_REQUIRED`)
  as the durable waiting-state template.
- New: an execution-plane task park state (working name
  `TASK_AWAITING_DIRECTION`) carrying `{model_stated_goal, recent_actions,
  fence_history}`.
- New: an operator directive surface to answer a parked task (analogue of
  `feature-clarify`), resuming the task with a method-changing directive bound
  to the park evidence.
- Changed: `adapters/openhands/client.go` fence handling — after N same-pattern
  `REPOSITORY_PROGRESS` corrections within one invocation, transition to the ask
  rather than continuing canned corrections to a bare interrupt.
- Changed: `adapters/operationalruntime/task_glitch_retry.go` —
  `REPEATED_CAPABILITY_MISMATCH_REPOSITORY_NO_PROGRESS` no longer re-rolls on
  blind variance; it enters the ask-and-park-escalate chain.

## Non-goals

- Not a config flip; this touches the flow layer and the fence→recovery contract
  and needs its own contract/qualification work.
- Not removing the retryable path for Arm 1 anomalies.
- Not making the human operator the first responder (that would end autonomous
  RSI operation); the operator is the last tier.

## Open questions for review

1. Does a method-changing operator directive reliably change behavior, or is
   that the same unproven assumption that just bit the auto-recovery reason?
   (Mitigation: the directive is *targeted* — names the file/symbol the model
   failed to find — unlike the current symptom-restating reason. Still needs a
   measured qualification.)
2. Where does the senior-role escalation tier run — a fresh senior invocation
   over the same workspace, or a fork of the stuck conversation?
3. Does the ask itself risk a new failure mode (model answers the question and
   repeats anyway)? If so, the N-correction budget before tier-2 must be small.
4. Budget accounting: does a parked task hold its budget reservation, and what
   bounds total ask/park latency?
