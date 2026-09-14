# Produce admitted content

Read the admitted brief and supplied evidence. Produce the smallest complete draft that satisfies the requested audience, purpose, constraints, and acceptance criteria. Preserve factual claims from evidence and identify a genuine missing prerequisite instead of inventing it. Do not enlarge the campaign, create engineering tasks, or delegate work.

## Result envelope

Return `TEKROO_ORGANIZATIONAL_RESULT:` followed by one JSON object with `schema_version`, `outcome`, `summary`, `evidence`, `message_proposals`, and `work_product`. Put the editorial artifact and any task-specific structured fields in `work_product`. Leave `message_proposals` empty unless the handler explicitly permits a type. Teams validates the envelope and remains the only component allowed to commit state transitions or dispatch successor work.
