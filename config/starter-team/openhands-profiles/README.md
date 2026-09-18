# OpenHands role profiles

Each JSON file is the local OpenHands LLM policy for exactly one Tekroo role
FQRN. `tekroo init-local` loads the file named for the role and rejects a
missing, mismatched, or invalid profile.

The initial OpenHands system prompt is not duplicated here. It is assembled
from the role's authenticated `roles-v4/<role>/ROLE.md` charter and the common
Teams invocation protocol. This makes the signed role package the single source
of truth for both organizational grounding and the model's starting identity.

`senior-architect.json` is intentionally dormant. It prepares the requested
reasoning policy, but no runtime actor or organizational authority exists until
an official signed senior-architect role package is added to the team manifest
and qualified under its resulting model-profile digest.

The 16K response ceiling on editing-oriented senior-coder work retains the
operational safeguard established after a prior 32K generation repeated a
completed implementation plan instead of emitting its next action.

| Role FQRN | Route | Reasoning | Output ceiling | MTP | Condenser events |
| --- | --- | --- | ---: | --- | ---: |
| `architect` | complex | medium | 32,768 | on | 80 |
| `coder` | bounded | low | 8,192 | on | 240 |
| `operator` | bounded | low | 8,192 | on | 80 |
| `product-owner` | bounded | medium | 16,384 | on | 80 |
| `project-manager` | bounded | low | 8,192 | on | 80 |
| `security` | complex | medium | 32,768 | on | 80 |
| `senior-architect` | complex | xhigh | 65,536 | on | 80 |
| `senior-coder` | complex | medium | 16,384 | on | 240 |
| `tester` | bounded | medium | 16,384 | on | 80 |
