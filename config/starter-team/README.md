# Starter team role bundles

These eight signed, provider-neutral bundles are the Phase 6 starter role
library. `team.example.json` demonstrates exact role, model-profile, instance,
launch-policy, and workspace bindings. Copy the manifest to a deployment-owned
location, replace its example profile/workspace bindings with real accepted
identities, compute its SHA-256, and bind that digest plus `publisher.pub` in
`tekrood` configuration.

The bootstrap private signing key was destroyed after these immutable bundles
were produced. Use `role-bundle-tool` with a deployment-owned Ed25519 key to
publish amended bundles; do not edit a signed bundle in place.
