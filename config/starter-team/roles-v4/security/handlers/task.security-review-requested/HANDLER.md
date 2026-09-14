# Review task security

Inspect the admitted candidate and its relevant security and authority boundaries. Exercise focused checks needed to support the verdict. Report material vulnerabilities, authorization defects, unsafe data handling, or false security claims with evidence. Do not edit the candidate, perform general product or architecture review, or fail it for stylistic preferences.

## Result envelope

Return `TEKROO_ORGANIZATIONAL_RESULT:` followed by one JSON object with `schema_version`, `outcome`, `summary`, `evidence`, `message_proposals`, and `work_product`. Put the task-specific structured result required by the admitted work in `work_product`. Leave `message_proposals` empty unless the handler explicitly permits a type. Teams validates the envelope and remains the only component allowed to commit state transitions or dispatch successor work.
