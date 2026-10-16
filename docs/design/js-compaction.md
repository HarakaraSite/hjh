# JS compaction extension

The opt-in [compaction.js](../../examples/hooks/compaction.js) hook composes
existing summaries and unsummarized conversation, summarizes a new range when
needed, then selects and replaces the model input. It is not installed as a
default hook. Ordinary saved conversation remains unchanged.

## Policy and flow

Policy is an editable object in JS:

| Setting | Initial example | Meaning |
| --- | ---: | --- |
| `fallbackInputTokens` | 800000 | Input budget used only when the model's effective input limit is unknown. Known model/configuration budgets are preserved. |
| `triggerRatio` | 0.8 | Consider summarizing above this fraction of the resolved input limit, or if the candidate does not fit. |
| `rawTargetRatio` | 0.6 | Select retained raw input toward this fraction before adding the new summary. |
| `keepRecentTurns` | 1 | Prefer retaining this many completed turns; summarize them too if needed to reach the raw target. |
| `summaryCharacters` | 1200 | Soft target in the summarization instruction, not a local truncation limit. |

The hook reads the effective input limit from `api.context.get().budget` on
each request, so changing models also changes its compaction budget. The default
input ratio is 0.9 of the context window; when no other constraint governs, the
0.8 trigger ratio places summarization at 72% of the full window. The
`triggerRatio` and `rawTargetRatio` remain 0.8 and 0.6. With the
unknown-limit default, summarization is considered above 640,000 estimated
tokens and the retained raw input target is 480,000. A known limit is not
capped at 800,000. These are policy values, not model capacity claims. Token counts use
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
