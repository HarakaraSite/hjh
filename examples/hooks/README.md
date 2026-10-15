# Hook examples

`context-metrics.js` measures each model request, including recent history,
before and after budget selection. It does not summarize, replace messages, or change
instructions. It writes small JSON observations under
`WORKSPACE/.hjh/context-metrics/`, one file per execution/model step. It never
writes message bodies or tool definitions to those files.

To try it, add its `projectRequest` export to the external module selected by
`hooks` in your extension config. Preserve any other exports you already use.
The next execution captures the edited module. Do not install this observer as
a standard hook unless you want an observation file on every model step.

The record includes wire bytes, estimated input tokens, effective budget,
message/tool counts and the model selection. Token counts are hjh's estimate,
not the provider's tokenizer usage.

`messageCount` describes the history-inclusive candidate; `currentMessageCount`
describes the raw current execution. `selection` describes the actual selected
size, including whether anything was omitted. History reads stop at the first
overflowing turn, as in normal preparation. To measure only the current turn,
call `api.context.measure({scope: "current"})` instead.

`context-selection.js` demonstrates JS-owned policy values: it selects with
`inputTokens: 12000` and `historyTokens: 6000`, then explicitly applies both
messages and budget through `replace`. Adjust those example values for your
task. It does not generate a summary. Protected input that still exceeds the
budget produces the usual budget failure.

See [the context API contract](../../docs/design/request-context.md) and
[the real-provider observation](../../docs/results/context-metrics-live.md).
That first observation used the earlier current-only measure contract; the
[selection API result](../../docs/results/request-context-selection.md) records
the history-inclusive extension.

`compaction.js` is the opt-in summarizing version. It uses the parent's current
provider/model/effort once per compaction, retains previous notes and exact
source ranges in Session memory, then selects and replaces the request. Keep
any existing lifecycle exports when adding it to your configured hook module.
Edit its `policy` values to tune the trigger and retained input size.
It follows the selected model's effective input budget. When that budget is
unknown, `fallbackInputTokens` defaults to 800,000; the trigger is 80% and the
retained raw input target is 60% of the effective budget.
It requires the model/memory APIs and does not create child agents.
See [its contract and storage behavior](../../docs/design/js-compaction.md).
