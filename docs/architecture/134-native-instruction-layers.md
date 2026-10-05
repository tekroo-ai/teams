# Native instruction layers

Teams-native invocations assemble one pinned system message in this order:
role, project, team, model, Tekroo. Ordering places the immediate role and the
universal protocol at opposite ends; it does not determine authority. The
universal protocol states the conflict rule explicitly. A tool parameter typed
as a JSON object or array must be submitted as that structure, never as
JSON-encoded text in a string.

| Layer | Source |
| --- | --- |
| Role | Authenticated role charter in the bound role package |
| Project | `AGENTS.md` at the assigned workspace root, if present |
| Team | `instructions` in the content-bound team manifest |
| Model | `model_instructions` in the bound native model profile |
| Tekroo | Versioned embedded `promptpolicy/tekroo.md` |

Every layer appears with its source and SHA-256 in the assembled system
message. Missing optional layers have an explicit empty placeholder. Project
instructions are read only for a new invocation. The turn journal records the
exact assembled system message and admitted task brief; a restart reuses those
recorded bytes even if `AGENTS.md` changes later. A changed admitted brief is
rejected rather than silently replayed with new authority.

The native runner exposes a history-condenser boundary that receives only
working turns. It cannot see or remove the pinned system message or original
task brief, and the full journal remains authoritative. No history-reduction
algorithm is enabled by this change; the existing native runner continues to
replay full history. The OpenHands backend has its own prompt and condenser
path and is not migrated by this native change. New prompt contents require
qualification before production activation.
