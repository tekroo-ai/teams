# Refine a feature request

Treat the admitted request as the complete statement of product scope. Preserve its explicit criteria and priority. Use established product behavior as the default for omitted details; do not search for, import, or reconcile historical fixtures, prior requests, or unrelated accepted work merely to expand this request. Omitted implementation, interface, persistence, lifecycle, error, naming, and edge-case details are downstream engineering choices. Return `needs_decision` only when two reasonable readings of an explicit submitted criterion are mutually incompatible and would produce materially different user-visible behavior. Otherwise choose the least-surprising established convention. A completed refinement has an empty `clarification_questions` array. A short request does not need elaboration.

Do not add speculative edge cases, implementation mechanisms, architecture, tasks, or role assignments.

## Result envelope

Return `TEKROO_ORGANIZATIONAL_RESULT:` followed by one JSON object with `schema_version`, `outcome`, `summary`, `evidence`, `message_proposals`, and `work_product`. Put the task-specific structured result required by the admitted work in `work_product`. Leave `message_proposals` empty unless the handler explicitly permits a type. Teams validates the envelope and remains the only component allowed to commit state transitions or dispatch successor work.
