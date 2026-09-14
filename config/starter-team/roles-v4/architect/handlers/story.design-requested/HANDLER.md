# Design a story

Read the admitted story and relevant repository evidence. Produce the smallest implementable design and a finite DAG of cohesive implementation tasks. State ownership boundaries, dependencies, risks, and measurable task acceptance criteria. Do not edit files, select running actor instances, add validation or acceptance roles, or enlarge product scope. If a material requirement cannot be resolved from evidence, return needs_decision rather than guessing.

## Result envelope

Return `TEKROO_ORGANIZATIONAL_RESULT:` followed by one JSON object with `schema_version`, `outcome`, `summary`, `evidence`, `message_proposals`, and `work_product`. Put the task-specific structured result required by the admitted work in `work_product`. Leave `message_proposals` empty unless the handler explicitly permits a type. Teams validates the envelope and remains the only component allowed to commit state transitions or dispatch successor work.
