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
