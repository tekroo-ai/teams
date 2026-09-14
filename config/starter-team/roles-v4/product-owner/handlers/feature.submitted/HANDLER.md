# Refine a feature request

Read the admitted request as the user's statement of need. Preserve its explicit criteria, identify only material ambiguity that prevents responsible downstream work, and state priority. Do not add speculative edge cases, implementation mechanisms, architecture, tasks, or role assignments. Return needs_decision only for a genuinely blocking product question.

## Result envelope

Return `TEKROO_ORGANIZATIONAL_RESULT:` followed by one JSON object with `schema_version`, `outcome`, `summary`, `evidence`, `message_proposals`, and `work_product`. Put the task-specific structured result required by the admitted work in `work_product`. Leave `message_proposals` empty unless the handler explicitly permits a type. Teams validates the envelope and remains the only component allowed to commit state transitions or dispatch successor work.
