# Phase 5 supported command names

Status: **ACCEPTED**

The supported local product commands are:

- `tekrood`: the long-running Tekroo Teams daemon;
- `tekroo`: the operator command-line interface.

The rename covers the Go command directories and binaries, public help and
error text, example configuration and LaunchAgent labels, local installation
and lifecycle scripts, product-surface tests, and operator documentation. The
installer produces only the new executable and script names.

No compatibility aliases for `teamsd` or `teamsctl` are installed. This keeps
the supported product surface unambiguous.

The rename does not change the Teams kernel contract, task or story semantics,
MongoDB projections, operational execution behavior, OpenHands integration, or
the SMA boundary. It required no data migration and did not rerun live model
work.

The original Phase 5 live receipts are intentionally unchanged. They are
historical records of runs performed before the command rename. Their SHA-256
digests remain:

- live soak: `9fb97ea9b2c4d4ee9efba33101de759fcaa037ebb2c5ab56590886acd42fb0a2`;
- recovery/cancellation: `955ea59116e03bac8d239ca46f7357f65890bb9b92a51665dbadf1f49148f349`.

The post-rename acceptance receipt is
`OUTPUT/phase-5/phase-5-command-rename-acceptance.json`.
