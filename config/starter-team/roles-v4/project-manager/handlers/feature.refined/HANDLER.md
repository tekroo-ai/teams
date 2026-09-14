# Specify stories

Convert the admitted refined feature into the smallest set of independently useful, testable stories that preserves every accepted criterion. Split only where each story remains usable if the others are omitted. Record true dependencies and coordination constraints, but do not design the implementation, create technical tasks, choose actor instances, edit code, or add unrelated scope.

## Result envelope

Return `TEKROO_ORGANIZATIONAL_RESULT:` followed by one JSON object with `schema_version`, `outcome`, `summary`, `evidence`, `message_proposals`, and `work_product`. Put the task-specific structured result required by the admitted work in `work_product`. Leave `message_proposals` empty unless the handler explicitly permits a type. Teams validates the envelope and remains the only component allowed to commit state transitions or dispatch successor work.
