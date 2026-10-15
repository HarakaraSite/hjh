# Request context API

The external hook module may export `projectRequest({ runtime, modelStep }, api)`.
It runs before budget selection for each logical model request, including the
first request and requests after tool results or steering have been appended.
Parent and child executions use the same path with independent candidates.
Provider retries reuse the already prepared request and do not rerun the hook.

This is an intervention API. The opt-in [JS compaction example](js-compaction.md)
uses it for automatic summarization. No default projection hook or `/compact`
command is installed.

## Operations

All operations return Promises. Await them before using their results.

| Operation | Result / effect |
| --- | --- |
| `api.context.get()` | `{instruction, tools, messages, sources, budget}` for the current candidate. |
| `api.context.history({beforeTurn?})` | One older canonical turn as `{number, entries:[{message, source}]}`, or `null` at the end. Omit `beforeTurn` for the latest completed turn, then pass the returned `number` to read the previous one. Children have no canonical parent history and return `null`. |
| `api.context.measure({scope?, messages?, instruction?, budget?})` | Measure before and after selection, including ordinary history by default. Returns candidate size and a `selection` summary; neither candidate nor normal projector state is modified. |
| `api.context.select({scope?, messages?, instruction?, budget?})` | Return a selected `{messages, instruction, sources, budget, wireBytes, estimatedTokens, messageCount, fits, omitted, historyStatus, candidate, coverage}`. Does not apply it. Uses the existing complete-exchange selection rules. |
| `api.context.replace({messages, instruction?, budget?})` | Stage a complete projection and optional request-local budget. `messages: []` is an explicit empty list. Omitted `instruction` retains the current instruction. Returns `null`. |

Initially `get().messages` contains the complete current execution transcript,
including the initial task, applied steering and completed tool results. A recall
prefix, when present, precedes it. `sources` is aligned with `messages` and identifies
prefix/current execution, turn and message ordinal. The raw messages are available
before the budget projector can omit older exchanges. Completed canonical turns
are read separately with `history()` or included automatically by request-scoped
`measure`/`select`; **get does not implicitly load them**. `budget` contains the
effective limit and its configuration sources.

## Measurement and selection

`measure()` defaults to request scope. It uses a detached copy of the ordinary
projector, including its recent canonical history selection. Top-level
`wireBytes`, `estimatedTokens`, `messageCount`, and `fits` describe the largest
candidate considered before exclusion. `selection` describes the resulting
request with the same size fields plus `omitted` and `historyStatus`. The response
also contains `estimator`, `budget`, and `coverage`.

History reads stop where the ordinary selection stops: at the first overflowing
turn. With no effective cap the normal reader can traverse all available turns.
`coverage: "request-through-first-overflow"` identifies this behavior. The
candidate is not a sum of every old turn in the DB. Full raw current messages
are measured even if the ordinary projector omitted some in an earlier step.

`measure({scope: "current"})` measures the exact prefix/current list without
adding canonical history; `coverage` is `current`. Explicit `messages`, or a
candidate already replaced in this hook, are measured exactly without adding
history; `coverage` is `explicit`. None of these calls applies a selection.

`select()` uses the ordinary request selection. With explicit `messages` it
uses the same current-message grouping: task/steering, latest assistant/exchange,
and incomplete exchanges remain protected; removable old complete exchanges
are removed together. Its top-level size is the selected size, and `candidate`
contains the before-exclusion size. `fits: false` means the protected portion
still exceeds the supplied budget. This is a result the hook can inspect, not
an implicit replacement or provider call.

## Request-local budget policy

`budget` accepts partial `contextTokens`, `inputTokens`, `outputReserve`,
`inputRatio`, and `historyTokens` values. Omitted fields retain the current
budget. The Go resolver reuses its existing formula:

```text
limit = min(floor(contextTokens * inputRatio), inputTokens,
            contextTokens - outputReserve)
```

Only available terms participate. `historyTokens` separately limits history.
It applies to canonical turns selected by request scope. An explicit message
array is treated as current input, without reclassifying individual messages
as history; use request-scoped selection when applying a separate history cap.
JS may increase or decrease configured values; this API adds no extra clamp.
Overridden field sources are recorded as `hook`. These values are request
policy, not a change to the selected provider or model.

Budgets supplied to measure/select apply only to that call. To send with the
selected budget, apply the result explicitly:

```js
const chosen = await api.context.select({ budget: { inputTokens: 12000 } });
await api.context.replace(chosen);
```

The final Go budget check uses the budget passed to replace. No shared config,
other execution, or future turn is modified.

`replace` replaces the whole message list: explicitly include any desired
canonical history, recall prefix, current task and recent exchanges. If retaining
tool calls, retain their matching results and required provider replay state as
well. Message values use the existing structured contract, for example:

```js
{ role: "user", content: { kind: "text", text: "Continue the current task." } }
```

Existing messages carry `providerState` when needed by the selected adapter.
History reads apply the existing provider/model change rules for private state.
The API does not change the selected model or registered tools.

After `replace`, `get` and parameterless `measure` read the staged candidate and budget.
The final explicit projection is measured against the normal input budget. It is
not silently trimmed and does not have old canonical history appended again.
If it exceeds the budget, the execution uses the existing context-budget failure
and sends no provider request. `sources.kind` for replacements is `hook`; its
ordinal addresses the candidate, not a canonical message.
The budget history reason is `project_request_hook`, indicating that the hook
provided the complete projection instead of automatic canonical-history selection.

Without `replace`, the existing budget selection applies unchanged, including
selection of completed canonical turns. Hook return values are ignored. A
rejected hook does not send its staged replacement; its error is reported through
the existing hook notice and execution settlement. Cancellation uses the same
Worker interruption and pending-operation join as other hooks.

## Example: inspect without changing normal selection

Place the export in the configured external `hooks.js`:

```js
export async function projectRequest({ runtime, modelStep }, api) {
  const view = await api.context.get();
  const size = await api.context.measure();
  // size includes history; size.selection describes the selected input.
  // Add a projection policy here when needed; no replace means normal selection.
}
```

To set a policy in JS and apply the existing selection without summarizing:

```js
export async function projectRequest(_, api) {
  const chosen = await api.context.select({
    budget: { inputTokens: 12000, historyTokens: 6000 },
  });
  await api.context.replace(chosen);
}
```

This is an API composition example, not an automatic compaction policy. A
policy can measure, generate a summary, select from the resulting messages,
then replace. If protected input does not fit, the final normal budget failure
is retained rather than silently changing the hook's explicit projection.

## Ownership and storage

The app owns each request candidate; JavaScript receives detached JSON. The
context API is available while `projectRequest` is executing, including its
awaited extension operations. It is not added to model-visible tools or the
generated `run_js` API. It is not a Session or DB editing API.

Replacements do not modify the agent loop's original transcript, the messages
saved to the journal, normal conversation adoption, or the visible history.
The next model step starts from the full original transcript again. Hook module
variables can retain a policy's state within the same Worker, but each new root
turn or child execution has a new Worker. Use the memory API below to carry
extension state across adopted root turns without persistent checkpoints.

Hook invocation records contain runtime/step metadata. Context API records
contain operation names and input/output byte counts, without copying request
messages, history bodies, tool definitions, replacements or hook return values.
An extension explicitly writing a file or executing another tool retains that
operation's usual recording behavior.

The final request and budget use the existing short per-step metadata and latest
per-execution snapshot saved at settlement. This API adds no full-request archive,
summary table, schema change or migration.

## Extension model and memory

These APIs are available to the external implementation API during
`projectRequest`, not as generated run_js methods or model-visible tools.

```js
const result = await api.model.generate({ instruction, messages });
const state = await api.context.memory.get({ key: "my-policy" });
await api.context.memory.set({ key: "my-policy", value: nextState });
```

`api.model.generate` calls the current execution's resolved provider, model and
effort once, using the existing provider encoder, authentication and stream
parser. It adds no ordinary conversation or tools, does not enter hooks again,
and creates no child execution. It returns `{text, finishReason, usage?}`.
Caller-supplied instruction and messages are the complete summarization input.
Provider errors, cancellation and a length-limited incomplete response reject
the call. This API does not use the parent agent loop's automatic retry.

Its request/response body and thinking are not journaled or snapshotted.
Operation names, byte counts, finish reason and returned usage are short
observation metadata. Thus billing evidence can be kept without full input
copies. If an extension later inserts the returned text in a normal request,
that normal latest request snapshot follows its usual persistence contract.

`memory.get` returns a detached JSON value, or null for an absent key.
`memory.set` stages a JSON value in the execution's working copy. Root state is
published to the Session only after normal canonical adoption. A failed or
cancelled execution cannot overwrite the previous published state. Child
memory starts independently and is not published into the parent Session.
Service/Core owns these maps in memory; standalone Run uses a new owner unless
the caller explicitly shares one. Session deletion removes its map. No memory
keys or values are written to SQL by this API.

`runtime.fromTurn` reports the explicit first-turn boundary used by the run, so
an extension can reset its state when that selected history range changes.
