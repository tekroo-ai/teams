# PM-owned executable feature plans (contract 0.13.0)

The product owner owns accepted outcomes. The architect owns technical design and proposes implementation tasks. The project manager owns the final executable DAG: sequencing, readiness, routing constraints, and explicit cross-task handoffs. Teams, not any model, admits and materializes that graph.

## Planning path

`refine -> specify -> design -> finalize-plan -> implementation`. The architect's output is an immutable proposal, identified by its execution-output digest. It cannot dispatch implementation. The PM receives that proposal and emits a final plan bound to the digest. The PM may add justified ordering edges and change critical-path labels, but may not silently alter technical descriptions, acceptance criteria, scope, risks, or the architect's decisions. A needed technical change is a bounded `needs_decision` result, not an improvised PM edit.

Each cross-task capability obligation has a provider task, consumer task, capability key, and short contract. Admission rejects absent endpoints, duplicate consumer/capability bindings, self-dependencies, and providers outside the consumer's transitive predecessor set. Pure scheduling edges do not assert a capability handoff. This check proves graph closure for *declared* obligations, not that the implementation fulfills them; task tests and integrated validation remain necessary. The PM must inspect for undeclared obligations, and a future semantic review can target those, without repeating a complete second-architect review.

## Clarification boundary

If the PM returns `needs_decision`, Teams blocks the finalization task with the PM's stated reason and retains its immutable invocation output. It does **not** treat the response as a malformed execution plan or dispatch implementation. Contract 0.13.0 does not automatically route that question back to the architect. A bounded, forward-only design-revision successor is a separate change, requiring an explicit workflow node and qualification; until then this case needs operator resolution. No free-form agent conversation loop is permitted.

## Compatibility and activation

The 0.12.0 package and 1.x workflow files remain unchanged. Workflow `software-development/2.0.0` opts into PM finalization; the 1.x workflow retains architect-prepared plans. Existing plans remain readable. The current workflow library does not allow two loaded definitions with the same trigger, and feature planning reads the selected definition while reconciling. Therefore switching a live daemon from 1.x to 2.0.0 while 1.x features are in flight is **not supported**; drain or isolate those features before activation. Activation requires a signed PM role bundle that handles `tekroo.message.feature.design-proposed`, an explicitly selected 2.0.0 workflow, and focused qualification before deployment. No active run is migrated in place.
