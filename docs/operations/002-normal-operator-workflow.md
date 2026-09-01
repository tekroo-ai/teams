# Normal local operator workflow

This is the supported Phase 7 path. Run `tekrood` as the persistent service and
submit every organizational change with `tekroo`. Do not write Teams MongoDB
documents directly.

## Product surface

```text
tekroo health
tekroo status
tekroo pause
tekroo resume
tekroo submit COMMAND.json
tekroo task TASK_ID
tekroo story STORY_ID
tekroo invocation INVOCATION_ID
tekroo cancel INVOCATION_ID COMMAND.json
tekroo federation
tekroo federation-alias ALIAS
tekroo federation-send ALIAS MESSAGE.json
tekroo stop
```

Every invocation of `tekroo` also requires `-config /absolute/path/to/tekrood.json`.
The client reads the operator bearer token from the file named by that config.

## Command envelope

Start from [command-envelope.json](../../examples/operator-workflow/command-envelope.json).
Authority, expected revision, lifecycle epoch, evidence references, causal
parents, preconditions, idempotency key, and correlation identity are explicit.
The client validates and submits this envelope; it does not infer or elevate
authority.

The exact accepted payload templates are the `NORMATIVE_EXAMPLE` entries in
[`catalogue-coverage.json`](../../CONTRACTS/tekroo.kernel.contracts/0.10.0/fixtures/catalogue-coverage.json).
Select the entry whose `when.commandType` matches the command below and place
its `when.payload` in the command envelope. This keeps the starter material
identical to contract `0.10.0` instead of maintaining a second copy of each
schema.

## Federated exact routing

Federation is disabled when the optional `federation` daemon configuration is
absent. When enabled, aliases, routes, signing identity, and peer trust grants
come only from the validated configuration file. `tekroo federation` inspects
that exact configuration, and `tekroo federation-alias NAME` shows the exact
actor, deployment, and route revision selected by a convenience alias.

`tekroo federation-send NAME MESSAGE.json` accepts an organizational message
whose recipient is either empty or already equals the alias's exact actor. The
daemon resolves the alias, validates the live sender execution, records the
outbound DAG step before network I/O, signs the immutable envelope, and sends it
through the configured route. The remote ingress is a dedicated listener; it
does not expose operator or MCP tools. Plain HTTP routes are accepted only when
explicitly marked test-only and bound to loopback. Production routes require
HTTPS.

## Ordered workflow

Use the event ID and resulting revision returned by each command as the next
command's causal parent and revision fence.

1. Create and activate the story:
   `story.create`, `story.authorize`, `story.begin-planning`, `story.activate`.
2. Create and prepare the task:
   `task.create`, `task.bind-work-profile`, `task.mark-ready`,
   `task.authorize-qualified-assignment`, `task.acquire-ownership`,
   `task.activate`, `work-budget.create`, `task.bind-work-budget`, and
   `task.bind-operational-scope`.
3. Register the exact execution identity and all referenced evidence with
   `execution.register` and `evidence.register`.
4. Authorize work with `work-invocation.authorize`. Inspect it with
   `tekroo invocation`; inspect its task with `tekroo task`.
5. To cancel active work, read the invocation's current revision and last event,
   place those exact values in a
   `work-invocation.request-cancellation` command, then use `tekroo cancel`.
6. For successful work, open one `completion-review` for the task, record each
   required branch result, and finalize the all-pass join. Submit
   `task.request-completion` using the exact finalized-review identity.
7. Repeat the bounded review for the story and submit
   `story.request-completion` with completed-task preconditions.
8. Submit `story.approve-release`.
9. For work with no repository release, create and finalize a release plan with
   `release_mode: NO_RELEASE_REQUIRED` and a nonempty reason. For code release,
   use the separately qualified `CODE` release workflow.
10. Submit `story.request-acceptance` with the exact finalized release-plan
    identity. Confirm `phase: ACCEPTED` using `tekroo story`.

The black-box regression
`TestTekroodAndTekrooExecuteNormalTaskThroughSupportedSurface` executes this
sequence against built `tekrood` and `tekroo` binaries, including a second
invocation that is cancelled through the operator surface.
