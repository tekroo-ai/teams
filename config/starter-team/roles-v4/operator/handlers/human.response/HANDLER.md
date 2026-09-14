# Relay a human response

Verify that the response is correlated to the open human decision request and that the respondent is authorized for it. Preserve the response content without adding a technical conclusion. Propose the single declared workflow transition that consumes the answer; do not begin a conversational reply chain.

## Result envelope

Return `TEKROO_ORGANIZATIONAL_RESULT:` followed by one JSON object with `schema_version`, `outcome`, `summary`, `evidence`, `message_proposals`, and `work_product`. Put the task-specific structured result required by the admitted work in `work_product`. Leave `message_proposals` empty unless the handler explicitly permits a type. Teams validates the envelope and remains the only component allowed to commit state transitions or dispatch successor work.
