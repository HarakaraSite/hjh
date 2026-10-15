# Child agents: run, spawn, status, steer, collect, cancel

Use a child for an independent task, with enough context in task: the parent's
conversation and role instruction are not copied. Choose a configured role from the
current role catalog. Omit optional settings unless the task needs an override.

- run_agent({task,role?,model?,tools?}) waits for completion.
- spawn_agent({task,role?,model?,tools?}) returns {executionId,state} after startup,
  so the parent can continue independent work. Startup state is not completion.
- agent_status({executionId,after?}) reads state, progress, reports, nextCursor,
  and instructions without waiting for completion.
- steer_agent({executionId,text}) saves useful additional guidance for an active
  child and returns an instruction receipt.
- collect_agent({executionId}) waits for the child's settled result.
- cancel_agent({executionId}) requests cancellation and waits for its settled result.

Use the returned executionId, not a Session ID. run/collect/cancel results contain
{executionId,outcome,finalText?,error?,snapshotSave}. Inspect outcome and error:
failed or cancelled outcomes may arrive as normal tool results. An operation error
may instead reject the call. Collect needed results before the parent finishes;
remaining children are cancelled when the parent execution ends.

## Defaults and explicit overrides

Omitting role selects config.defaultRole, not the parent's role. Model selection
starts from the parent, applies child-role model settings, then explicit model
fields {provider?,modelId?,effort?}. When provider changes, omitted modelId and
effort use that provider's defaults. Omitted tools selects the child's configured
role tools; an explicit list replaces that list, and [] means no tools. It is not
an intersection with the parent's selected tools.

## Runtime availability

Direct child tool calls do not require run_js. Generated JavaScript requires run_js
and each tool it calls to be selected. api.agents.run/spawn/status/steer/collect/cancel
are aliases for tools.run_agent/spawn_agent/agent_status/steer_agent/collect_agent/
cancel_agent, respectively, and exist only for selected operations. The app must
supply the child runner/controller; selecting a name alone does not create a
lifecycle backend. Examples use the standard explorer/reviewer roles and repository
paths; substitute configured roles and real task paths outside this repository.

## Spawn, then do independent parent work

Requires spawn_agent and read_file, plus run_js for this fenced async body. Keep the
returned executionId in the model-visible result for later steps; local JS variables
do not persist between calls.

```js
// Start an explorer and read independent design context in the parent.
const child = await api.agents.spawn({role:"explorer",task:"Read internal/extensions and report how tools are selected. Do not modify files."});
const context = await tools.read_file({path:"docs/design/base-instructions.md"});
return {executionId:child.executionId, state:child.state, context};
```

Equivalent direct calls: spawn_agent with the same role/task, then read_file with
that path, continue parent work, then collect_agent with the returned executionId.
No status request or steering is required merely because a child was started.

## Read saved progress only when useful

Requires agent_status. Replace RETURNED_EXECUTION_ID with the actual value from
spawn; this is a separate run_js call, not a continuation of its local variables.

```js
// Read the child's saved reports without waiting for completion.
const status = await api.agents.status({executionId:"RETURNED_EXECUTION_ID"});
return {executionId:status.executionId, state:status.state, progress:status.progress, reports:status.reports, nextCursor:status.nextCursor, instructions:status.instructions};
```

Reports retain executionId, messageOrdinal, and text. On a useful later read, pass
this same child's actual previous nextCursor as after (omit after on the first
read). Reads do not advance a shared cursor. Do not guess a cursor or poll status
to wait for completion; collect waits for the final result.

## Steer only if additional guidance is useful

Requires steer_agent and an active child. Substitute the real executionId.

```js
// Add a useful focus to the active child's investigation.
return await api.agents.steer({executionId:"RETURNED_EXECUTION_ID",text:"Also identify the owner of the captured tool list and cite source locations."});
```

The receipt's status is accepted, applied, or unapplied. Accepted means saved;
applied means delivered into the child's loop after its current model request and
tools, not proof that the model followed it. agent_status.instructions gives the
latest receipts, including an unapplied reason when present. If the child has
already settled, use its result rather than assuming steering can restart it.

## Collect when the parent needs the final result

Requires collect_agent. Substitute the real executionId.

```js
// Wait for the child and retain both its outcome and diagnostic error.
const result = await api.agents.collect({executionId:"RETURNED_EXECUTION_ID"});
return {executionId:result.executionId, outcome:result.outcome, finalText:result.finalText, error:result.error};
```

Direct equivalents are agent_status({executionId}), steer_agent({executionId,text}),
and collect_agent({executionId}). If work is no longer needed, use
cancel_agent({executionId}); cancellation also returns a settled result to inspect.

## Parallel blocking children when the parent has no separate work

Requires run_agent and run_js, an available runner, and the configured roles below.
These two independent delegations run concurrently; this is distinct from spawning
and doing parent work before collection.

```js
// Run independent investigations concurrently and retain their settled outcomes.
const tasks = [
  {role:"explorer",task:"Read internal/extensions and summarize how the selected tool list is captured. Do not modify files."},
  {role:"reviewer",task:"Read internal/jsruntime/bootstrap.js and review how generated JavaScript receives selected tools. Do not modify files."}
];
return await Promise.all(tasks.map(task => api.agents.run(task)));
```
