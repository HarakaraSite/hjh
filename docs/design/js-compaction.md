# JS compaction extension

The opt-in [compaction.js](../../examples/hooks/compaction.js) hook composes
existing summaries and unsummarized conversation, summarizes a new range when
needed, then selects and replaces the model input. It is not installed as a
default hook. Ordinary saved conversation remains unchanged.

## Policy and flow

Policy is an editable object in JS:

| Setting | Initial example | Meaning |
| --- | ---: | --- |
| `inputTokens` | 24000 | Input policy passed to the context API. Other configured budget terms still participate in its existing minimum formula. |
| `triggerRatio` | 0.8 | Consider summarizing above this fraction of the resolved input limit, or if the candidate does not fit. |
| `rawTargetRatio` | 0.6 | Select retained raw input toward this fraction before adding the new summary. |
| `keepRecentTurns` | 1 | Prefer retaining this many completed turns; summarize them too if needed to reach the raw target. |
| `summaryCharacters` | 1200 | Soft target in the summarization instruction, not a local truncation limit. |

These are prototype policy values, not model capacity claims. Token counts use
hjh's existing estimate. The extension reads recent history through the first
overflowing turn. Its in-memory floor prevents reviving earlier history on
every call. It does not attempt to summarize all remote past turns.

On each projectRequest:

1. Read Session memory and the original current transcript. Keep recall prefixes.
2. Add previous summaries and recent, unsummarized canonical history.
3. Measure with the chosen policy. If needed, take old completed turns and use
   the existing selector to identify old complete current tool exchanges.
4. Call the parent provider/model/effort once with a summarization instruction
   and the newly removed original messages. No agent loop or tools are used.
5. Attach source references in JS and mark exactly those message ranges covered.
6. Select the final request and apply it with replace. Stage the extension state.

Previous summaries are retained in memory and are not themselves summarized.
They are assistant notes in the candidate, so ordinary selection may omit old
notes from the request if necessary. The original summaries remain in memory.
The current task, applied user instructions and latest complete exchanges use
the existing selector's protection. If retained input still does not fit, the
usual context-budget failure is returned; the extension does not truncate it.

## References

JS derives references from actual `sources`, not model-generated identifiers.
Each note retains the full Session/execution ID, turn, and exact message ranges.
For a completed canonical turn it also points to the last ordinary assistant
message. An in-progress range has no invented final-answer reference.
Thinking and individual tools do not get separate mandatory references.

The model input contains the continuation note and original locators. The
reference metadata retains full IDs; user-facing tooling may display short IDs.
To read a referenced completed turn, use existing CLI history with its Session
and turn, for example:

```sh
hjh history --session SESSION --view canonical --from-turn 12 --to-turn 12 --json
```

Execution inspection returns the referenced message ordinal and other evidence:

```sh
hjh inspect --execution EXECUTION
```

These are locators for existing history operations, not a new model-visible
history tool or clickable URI protocol.

## Memory and persistence

The Core's Service owns `ContextMemory`. Every root execution gets a detached
working copy; step-to-step updates stay in that execution. Only successful
canonical adoption publishes it for the Session's next turn. Failure/cancel
leaves the previous Session memory intact. Children use independent execution
memory. Deleting a Session releases its extension memory.

TUI reconnection and later runs against the same Core retain it. A new Core or
standalone CLI process starts without that memory and may summarize originals
again. Changing the explicit from-turn boundary resets this example's state;
summaries outside that history selection are not reused.

No summary table, checkpoint JSON or source archive is added. The summarization
request/response/stream is not appended to normal messages, assistant thinking,
child execution history or latest request snapshots. Its operation stores small
metadata and usage. **The ordinary final model-input snapshot may contain the
summary text**, just as it contains any other text actually sent to the model.
This is not an agent-wide no-DB mode.

See the [one-shot model and memory API](request-context.md#extension-model-and-memory)
and [implementation results](../results/js-compaction.md).
