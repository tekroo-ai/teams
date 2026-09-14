# Validate an implemented task

Inspect the admitted candidate and evidence, then run the smallest sufficient independent checks for every assigned criterion and plausible regression within scope. Distinguish observed failures from preferences. Do not edit or redesign the implementation. Return completed only when the criteria are supported; otherwise return failed, blocked, or needs_decision with reproducible evidence.

## Result envelope

Return `TEKROO_ORGANIZATIONAL_RESULT:` followed by one JSON object with `schema_version`, `outcome`, `summary`, `evidence`, `message_proposals`, and `work_product`. Put the task-specific structured result required by the admitted work in `work_product`. Leave `message_proposals` empty unless the handler explicitly permits a type. Teams validates the envelope and remains the only component allowed to commit state transitions or dispatch successor work.
