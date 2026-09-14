# Starter team role bundles

These eight signed, provider-neutral bundles are the Phase 6 starter role
library. `team.example.json` demonstrates exact role, model-profile, instance,
launch-policy, and workspace bindings. Copy the manifest to a deployment-owned
location, replace its example profile/workspace bindings with real accepted
identities, compute its SHA-256, and bind that digest plus `publisher.pub` in
`tekrood` configuration.

The bootstrap private signing key was destroyed after those immutable bundles
were produced. Architect bundle `1.1.0` is an immutable successor that adds the
read-only repository access needed for grounded design work; it is signed by
`role-grounding-publisher.pub`. Use `role-bundle-tool` with a deployment-owned
Ed25519 key to publish later amendments; do not edit a signed bundle in place.

`team.v4.example.json` and `roles-v4/` are the successor Teams v4 role-package
format. Each package separates its concise always-on `ROLE.md` charter from the
one message handler selected for an admitted invocation. Every resource is
content-addressed and every bundle is signed by
`message-handler-publisher.pub`. The example is not activated merely by being
present: a deployment must explicitly bind the new manifest digest and trust
that publisher. The private example signing key was never written to disk.

Successor handlers return one `TEKROO_ORGANIZATIONAL_RESULT` JSON envelope.
The envelope contains a terminal outcome, summary, evidence, the handler's
typed `work_product`, and zero or more message proposals. Teams validates the
whole envelope against the selected content-addressed result schema and the
handler's declared authority before accepting the terminal result. The starter
packages currently allow no model-authored message proposals: workflow
progression remains a deterministic Teams operation, so a role cannot create a
reply chain by choosing another message type.

The starter workflow uses durable one-hop task messages selected by configured
work purpose: implementation and repair use `tekroo.message.task.assigned`,
validation uses `tekroo.message.task.review-requested`, security review uses
`tekroo.message.task.security-review-requested`, escalation uses
`tekroo.message.task.escalated`, and promotion uses
`tekroo.message.release.ready`. Adding a role or changing its message handler
uses signed configuration; it does not add a role-name branch to the runtime.

`../editorial-team/` is the non-software example. It adds an `editor` FQRN and
the `tekroo.message.content.requested` handler entirely through a separately
signed team manifest and role package.
