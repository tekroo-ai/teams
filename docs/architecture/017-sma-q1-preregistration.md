# Phase 3 Step 14 — SMA-Q1 preregistration

## Decision

Step 14 freezes the evaluation design for
`SMA-Q1-CORE-PROXY-VERTICAL-SLICE` before executing any scenario. It is a
preregistration gate, not an SMA result gate.

The accepted architecture makes SMA semantic memory only. Applicable OpenHands
model roles use an OpenAI-compatible SMA proxy which delegates to Ollama. SMA
may add bounded, partitioned, explicitly untrusted semantic context and capture
raw exchanges asynchronously. It gains no workflow, task, audit, acceptance,
process-control, or organizational authority.

## Why preregistration is separate

The approved investigation requires semantic-relevance, latency, reliability,
contamination, partition, and failure thresholds to be declared before results
are observed. Combining threshold selection and execution in one mutable work
package would allow the evaluation to be fitted to its outcome.

The fixed manifest is
`investigations/sma-q1/preregistration.json`. It contains the exact synthetic
memory corpus, sixteen scenarios, repetitions, prompts, injected faults,
expected outcomes, absolute stop conditions, quantitative thresholds,
measurement rules, and adjudication policy.

## Reconciliation with current SMA work

The SMA repository's `TASK_STATUS.md` and Gate 4 receipt report a successful
native OpenHands capture/replay/retrieval/`additionalContext` canary. That is
useful prior evidence, but it does not exercise the later principal-approved
automatic proxy path or the Step 14 corpus. It is therefore not counted as an
SMA-Q1 observation.

The older integration plan explicitly says the proxy is not placed between
OpenHands and Ollama. The later accepted Phase 1B handoff selects that proxy as
the automatic path. The current principal-approved architecture wins; the
conflict is preserved rather than silently blending the two designs.

## Execution prerequisite

The observed SMA checkout is a shared dirty worktree at commit
`1df53a2b57f98bdf092841148b3e644afbc7a62a`. It is not an immutable execution
identity. Step 14 does not modify, clean, commit, stash, or operate that
checkout. Before SMA-Q1 begins, its intended implementation must be reviewed
and frozen as a clean commit with exact build, dependency, model, prompt,
schema, configuration, MongoDB, Qdrant, OpenHands, Ollama, and artifact
identities.

## Qualification boundary

A Step 14 PASS means only that the preregistration is structurally complete,
internally consistent, content-addressed, and frozen before execution. It does
not mean the SMA proxy is correct, safe, useful, performant, deployable, or
production-ready. It does not begin SMA WP5, OpenHands-Q1, or SMA-Q2.
