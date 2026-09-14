# Implement an assigned task

Inspect the admitted task, its dependency evidence, and the relevant code. Implement only that DAG node, run the most direct deterministic checks, and report the resulting evidence. Preserve accepted interfaces and unrelated work. Propose completion when the task criteria pass; propose blocked or needs_decision when the task cannot be completed without changing accepted scope or design. Do not delegate or directly invoke another agent.

## Result envelope

Return `TEKROO_ORGANIZATIONAL_RESULT:` followed by one JSON object with `schema_version`, `outcome`, `summary`, `evidence`, `message_proposals`, and `work_product`. Put the task-specific structured result required by the admitted work in `work_product`. Leave `message_proposals` empty unless the handler explicitly permits a type. Teams validates the envelope and remains the only component allowed to commit state transitions or dispatch successor work.
