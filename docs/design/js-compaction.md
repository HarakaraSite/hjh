# JS compaction extension

The opt-in [compaction.js](../../examples/hooks/compaction.js) hook selects old
conversation, consolidates continuation summaries when needed, then replaces
the model input. It is not installed as a default hook. Ordinary saved
conversation remains unchanged.

The 2026-10-16 implementation follows the adopted
[compaction strategy and measurement plan](../plans/js-compaction-strategy.md).
See its [implementation and latency measurements](../results/js-compaction-strategy.md).
The earlier append-only summary implementation is recorded in the
[original results](../results/js-compaction.md). The workspace's deployed copy
is separate from this example; changing the example does not deploy it.

## Policy and flow

Policy is an editable object in JS:

| Setting | Initial example | Meaning |
| --- | ---: | --- |
| `fallbackInputTokens` | 800000 | Input budget used only when the model's effective input limit is unknown. Known model/configuration budgets are preserved. |
| `triggerRatio` | 0.9 | Consider summarizing above this fraction of the resolved input limit, or if the candidate does not fit. |
| `triggerTokens` | null | Optional absolute trigger overriding the ratio, including small values for isolated verification. |
| `targetTokens` | null | Optional absolute final-input target. Default: one third of the resolved input limit. |
| `keepRecentTurns` | 2 | Completed turns treated as recent continuation material. Older turns retain only guidance/results needed by the current task. |
| `summaryCharacters` | 1200 | Soft target in the summarization instruction, not a local truncation limit. |

The hook reads the effective input limit from `api.context.get().budget` on
each request, so changing models also changes its compaction budget. The default
input ratio is 0.9 of the context window; when no other constraint governs, the
0.9 trigger ratio places summarization at 81% of the full window. The final
target is `floor(inputLimit / 3)`, including instructions, tools, recall prefixes,
summary text, references and retained raw messages. With the unknown-limit
default, the trigger is above 720,000 estimated tokens and the final target is
266,666. A known limit is not capped at 800,000. These are policy values, not
model capacity claims. Token counts use hjh's existing wire estimate, separately
from provider-reported usage.

History is read newest first through the first overflowing turn. During normal
continuation that turn remains a compaction candidate so its needed results can
be summarized. On restoration it is excluded before summarizing, as described
below. An in-memory floor and covered ranges prevent reviving discarded or
already summarized raw history. Already covered raw messages are not re-summarized.

On each projectRequest:

1. Read Session memory and the original current transcript. Keep recall prefixes.
2. Add the existing continuation note and unsummarized canonical history through
   the first overflowing turn during normal continuation.
   Without compaction, retain the note's text/order and append new conversation.
3. At the trigger, summarize recent completed turns for continuation. For older
   turns, discard unrelated material and retain only guidance/results needed by E,
   the current task. Use the existing selector's complete-exchange rules inside E.
4. Provide the summarizer with E's task, applied steering, recent retained state,
   the existing note and newly removed originals. Use readable sections without
   duplicating the task/steering in recent state or embedding the full transcript
   in another JSON string. Measure this input, including the summary instruction.
5. Call the inherited parent provider/model/effort once and consolidate into one
   continuation note. If the final input exceeds the target, allow one additional
   summarization, adding further removable current exchanges or shortening the note.
   If recent completed history cannot fit one summary input, first summarize its
   older part and retain the deferred part raw in the first candidate, then use
   the second call to merge that raw history with the first note.
6. Compare the measured candidates. Adopt the smaller continuation candidate with
   its matching note, references, coverage and retained boundary. Record target
   attainment and the number of model calls in `lastCompaction`.
7. Apply the exact candidate with replace and stage the extension state. Even raw ranges
   intentionally discarded without summary are covered so they cannot reappear.
   Pending raw history retains its own coverage and is not silently trimmed by
   a final select. Summary preparation failure also retains the original exact
   candidate for the existing input-budget check rather than silently discarding history.

The first compaction normally uses one model call; at most two are made per
compaction. A second response that does not shrink the input does not cause
another retry. If the target is still missed but input fits the effective budget,
continue and record the miss. Protected task/steering/latest exchanges inside E
follow the existing turn-internal behavior. If the effective input budget itself
is exceeded, the existing context-budget result applies. No new target-based
refusal is added. No child execution, agent loop or tools are used for summarizing.

The summary model automatically inherits the parent provider, model and effort.
Selecting a separate summarization provider/model is a separate task and is not
implemented by this extension.

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
standalone CLI process starts without that memory. On its first request with
saved history, it first excludes old whole turns until the candidate fits the
input budget, then compacts remaining history if above the final target rather
than waiting for the normal 90% trigger. Changing the explicit from-turn boundary
resets this example's state; summaries outside that selection are not reused.
The extension uses the `compaction-v2` memory key without converting older state.

No summary table, checkpoint JSON or source archive is added. The summarization
request/response/stream is not appended to normal messages, assistant thinking,
child execution history or latest request snapshots. Its operation stores small
metadata and usage. **The ordinary final model-input snapshot may contain the
summary text**, just as it contains any other text actually sent to the model.
This is not an agent-wide no-DB mode.

See the [one-shot model and memory API](request-context.md#extension-model-and-memory)
and [implementation results](../results/js-compaction.md).

## 改善メモ：直近原文の保持と再開時の要約状態復元

2026-10-15、利用者との検討を記録する。まず取り組む候補は次の二つ。
この節は検討メモであり、現行のメモリのみの保存契約は変更していない。

- 直近の原文をターン数だけでなくトークン量で保持する。巨大な完了ターンも丸ごと要約へ回さず、
  末尾の原文を残せるようにする。ツール呼出し・結果の対応を維持し、具体的な保持量と
  最終回答・追加指示の扱いは設計時に決める。
- Core再起動後も、実行中に作った要約と保持範囲から復元する。同じ原文について、継続中は
  小分けの要約を使い、再開時だけ完了ターン全体を粗く要約し直す差をなくしたい。
  同じCoreへのTUI再接続は既にメモリを維持するため、主な対象はCore再起動・別CLI processでの再開。

### DBへの対応案

直近原文の選択方針だけならJSの変更で扱え、DBへの保存追加は不要。
要約状態の復元には保存・読込み処理が必要だが、新しいtableや列は必須ではない。
調査時の既存schemaには未使用の`sessions.checkpoint_json`があり、Session作成時にNULLを入れる以外の
利用経路はなかった。この欄を使い、要約文・原文参照・要約済み範囲・原文保持境界を保存する案を検討する。

Sessionごとに最新状態1件を上書きし、過去の状態snapshotや原文コピーを積み増さない。
会話の正常採用と要約状態の保存を揃えて確定し、失敗・取消の実行で採用会話と状態がずれない経路を設計する。
現在のpublishは会話採用後のメモリ更新のみなので、その保存境界の変更を含む。
ただし1件上書きでも、内部の要約配列・参照範囲が増え続ければ容量は増える。
要約の保持・統合方針と更新量も合わせて検討し、固定容量になるとは扱わない。

### 参考と別テーマ

ローカルの参照版では、Piは要約とfirstKeptEntryId、Zotは要約と末尾原文のcheckpoint、
OpenCodeは要約と直近原文または保持境界を永続化していた。再開時も保存した入力状態を再構成する方式が参考になる。
旧Henji側の参照版のrevisionは`../../../henji-harness/_refs/README.md`に記載されている。
参照実装の方式をそのままhjhへ採用する決定ではない。

原文の検索・読戻しは別の候補。要約や最終回答にない局所的な質問には、検索で候補を探し、
該当する原文範囲だけ読む経路が必要になり得る。DeepSeek Harnessの任意拡張には
session_event_search/read/traceとSQLite FTS5による具体例があるが、この二つの改善とは切り分ける。
「遡るのは10ターンまで」という案も議論したが、上限の採用・値はまだ確定していない。
