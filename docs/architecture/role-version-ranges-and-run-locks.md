# Role version selection and frozen run identity

Version policy is an offline preparation input, not a daemon resolution rule.
Existing signed bundles, exact manifests, qualifications, workflow definitions
and historical run records remain unchanged. No feature-specific selection
rules or new model qualification bypasses are introduced.

## Central policy

Maintain one private `role-version-policy.json` alongside qualified configuration
publications. A shared `default_version_range` avoids repeating a version
constraint; an individual role can override it. `roles` must exactly match the
sorted template roster. Each catalog entry names an existing qualified
configuration and the SHA-256 of its exact bytes. Paths are relative to the
policy file (absolute paths are also supported).

```json
{
  "schema_version": "tekroo.role-version-policy/1.0.0",
  "default_version_range": "[2.1.0,3.0.0)",
  "roles": [{"role": "editor"}, {"role": "reviewer"}],
  "candidates": [
    {
      "configuration_file": "qualified/configuration.json",
      "sha256": "<SHA-256 of that exact published configuration>"
    }
  ]
}
```

The example is illustrative, not a qualified deployment or an executable default.
Versions occur in this policy and in signed publication metadata, not in role
selection code. New releases are added to the catalog explicitly after their
ordinary qualification; the resolver does not scan directories, fetch releases,
change model settings, or generate qualification records.

Supported constraints use the numeric major.minor.patch versions already
accepted by signed role bundles:

| Constraint | Meaning |
| --- | --- |
| `2.1.0` or `[2.1.0]` | Exactly that numeric version |
| `2.*` | Any minor/patch in major 2 |
| `2.1.*` | Any patch in minor 2.1 |
| `[2.1.0,)` | At least 2.1.0 |
| `(,3.0.0)` | Below 3.0.0 |
| `[2.1.0,3.0.0)` | At least 2.1.0, below 3.0.0 |

Brackets include a bound; parentheses exclude it. Open-ended intervals are
supported but bounded intervals are preferred. Unsupported or malformed syntax
fails closed: no bare `*`, unions, qualifiers, implicit latest, soft Maven
requirements, or downgrade fallback outside the configured range. This is the
interval notation, not Maven's full version ordering algorithm.

## Preparation and selection

Use the offline CLI before creating/admitting a run:

```text
tekroo prepare-role-versions --config /absolute/qualified-template.json \
  --version-policy /absolute/role-version-policy.json \
  --output /absolute/new-run-role-directory --run-id new-run
```

The native run preparer now requires the policy and a `tekroo` executable with
this command, after its existing five arguments:

```text
node scripts/prepare_native_feature_run.mjs CANDIDATE REPOSITORY DEPLOYMENT \
  rNUMBER BASELINE_SHA VERSION_POLICY TEKROO_BINARY
```

All paths above must be absolute. The script resolves roles before creating
worktrees; it consumes the private prepared configuration and includes the
lock receipt in the run preparation receipt. It does not activate the run.
Historical explicit configuration paths remain loadable without a lock for
replay; new native runs through this preparer cannot omit range resolution.
`init-local` remains a deployment bootstrap, not a fresh-run selector; use
`prepare-role-versions` on its qualified configuration before admitting a new
run. No historical starter manifest is silently upgraded.

Selection chooses the highest numeric eligible version for each role. It
authenticates every catalog package using the template's trusted publishers,
verifies its resource hashes/schema compilation and exact profile/settings/tool
identity, and requires a valid PASS qualification eligible at preparation time.
Qualification must preserve the template's decision-route minimum and covered
work kinds. Required capabilities, permissions, subscriptions, handler
dispositions, result outcomes and outgoing message types must be retained.
These structural checks are not proof that arbitrary new implementations are
semantically equivalent: qualification and explicit catalog publication remain
necessary. A missing eligible match, invalid source, or conflicting content or
model binding at the same numeric version rejects preparation.

An unqualified manual role can retain its exact existing bundle/profile only;
the range cannot upgrade it without qualification or make it automatic.
Team identity, instance counts, workspace assignments and launch modes come
from the template, not the candidate catalog.

## Frozen output and restart

The output is a new, exclusive directory containing:

- `packages/`: only the selected signed role descriptors and their exact
  hash-bound resources, not an environment copy;
- `team.json`: exact manifest with selected bundle/profile bindings;
- `version-policy.json`: exact policy bytes used for selection;
- `role-lock.json`: run ID, preparation time, policy/manifest digests and each
  role's signed version, publisher, bundle, model profile, qualification and
  source-configuration digest;
- `prepared-config.json`: configuration binding the exact manifest and lock.

The snapshot is re-authenticated before the configuration is published. The
daemon strictly checks the lock digest and compares every entry to the actual
signed bundle/profile during loading. Restart/resume never reads the catalog
or resolves ranges. A later policy/publication only affects a newly prepared
run. Existing runtime qualification/work-kind/capability gates still apply;
freezing a lock does not extend an expired qualification.

No existing output is overwritten. A failed publication may leave a partial
directory, but never publishes `prepared-config.json`; inspect/clean that exact
directory explicitly or use a new output path. Existing runs and deployment
configuration are not edited by preparation.
