# Normal local operator workflow

This is the supported Phase 5 path. Run `teamsd` as the persistent service and
submit every organizational change with `teamsctl`. Do not write Teams MongoDB
documents directly.

## Product surface

```text
teamsctl health
teamsctl status
teamsctl pause
teamsctl resume
teamsctl submit COMMAND.json
teamsctl task TASK_ID
teamsctl story STORY_ID
teamsctl invocation INVOCATION_ID
teamsctl cancel INVOCATION_ID COMMAND.json
teamsctl stop
```

Every invocation of `teamsctl` also requires `-config /absolute/path/to/teamsd.json`.
The client reads the operator bearer token from the file named by that config.

## Command envelope

Start from [command-envelope.json](../../examples/operator-workflow/command-envelope.json).
Authority, expected revision, lifecycle epoch, evidence references, causal
parents, preconditions, idempotency key, and correlation identity are explicit.
The client validates and submits this envelope; it does not infer or elevate
authority.

The exact accepted payload templates are the `NORMATIVE_EXAMPLE` entries in
[`catalogue-coverage.json`](../../CONTRACTS/tekroo.kernel.contracts/0.8.0/fixtures/catalogue-coverage.json).
Select the entry whose `when.commandType` matches the command below and place
its `when.payload` in the command envelope. This keeps the starter material
identical to contract `0.8.0` instead of maintaining a second copy of each
schema.

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
   `teamsctl invocation`; inspect its task with `teamsctl task`.
5. To cancel active work, read the invocation's current revision and last event,
   place those exact values in a
   `work-invocation.request-cancellation` command, then use `teamsctl cancel`.
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
    identity. Confirm `phase: ACCEPTED` using `teamsctl story`.

The black-box regression
`TestTeamsdAndTeamsctlExecuteNormalTaskThroughSupportedSurface` executes this
sequence against built `teamsd` and `teamsctl` binaries, including a second
invocation that is cancelled through the operator surface.

