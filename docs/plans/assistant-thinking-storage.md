# assistant本文・thinking・provider継続情報の重複保存整理

2026-10-16。状態：**第一段S-1＋S-2の実装・focused test・独立code review、実CLI/Core/PTY確認・比較測定・総合test完了。S-4証跡reviewの測定P2も訂正・再測定・独立再reviewで解消。追加承認のS-3も実装・focused/総合test・実操作・比較測定・独立review完了。**
調査対象source：`96b2625`（調査開始時のHEAD）。指定資料を読み、生成・保存と読取り利用者を
独立したexplorer 2件へspawnし、親が保存codec・実DBを別調査してから回収・突合した。
利用者は独立レビュー後に「修正して　ユーザー判断は全て推奨通り」と指示した。
その後「では実装テストコードレビューまで進んで」と指示した。
第一段の実装・確認・比較測定を進め、到達点は[結果](../results/assistant-thinking-storage.md)に記録する。
調査時点のsource経路・容量値は以下に保持し、実装後の値と区別する。

## 1. 目的・根拠・対象外

必要な利用動作は、通常の会話再開、可読thinkingのlive/replay表示、親子の履歴調査、
失敗・取消後のrecall。provider継続情報を保持し、同じ本文を表示・調査用記録へ再埋込みする量を減らす。

根拠：今回の明示依頼、[history-schemaの観測](../design/history-schema.md#2026-10-15追記assistantthinkingの詳細記録の重複)、
[通常履歴の保存契約](../design/normal-history-storage.md)、[usage実装結果](../results/usage-storage.md)、
本書の現行source調査と読取り専用の実DB観測。旧Henji参考資料やfixtureだけから仕様を追加しない。

- usage単一保存・attribution削減は実装済みで対象外。過去DBのusage値を今回の効果へ含めない。
- モデルへの送信内容・入力選択・コンパクション・cache用ID・送信設定は変更しない。
  provider decoder/encoder、再試行回数、hook動作の変更も計画しない。
- 本文の切捨て、thinking非保存、provider stateの可読textへの置換、圧縮、履歴自動削除は採用しない。
- 今後の新規保存経路を対象とする。既存DBの変換・削除・VACUUM・新DBへの切替は含めない。
  migration、converter、旧形式用dual read/writeや推測fallbackを追加しない。
- 第一段の製品source・test・検証script・文書を変更する。常用DB・設定・稼働Core・配置binaryは変更しない。

## 2. 現行の生成・保存・再読込み経路

### 2.1 操作から保存まで

```text
CLI / TUI → Core / Service → app.execute（親・子共通）→ agent.Run
  → provider.Generate → SSE assembly
      本文 / 可読Thinking[] / ProviderState（継続用）の生成
  → assistant_text Observation（累積typed Text/Thinking）
      journal → SaveAssistantProgress
      started marker + assistant_text_states最新行の置換
      保存後に軽量FactをCoreへ公開 → conversation.View → TUI
  → model_result Observation（JSON result + typed Text/Thinking）
      journal → SaveAssistantResult transaction
      model_result + thinking記録、対応するactive行削除
  → providerのrequest end通知 → agentがModelResultを受取る
  → Assistant / AssistantCalls Message + ExecutionFact
      journal.appendMessage → AppendConversationMessage transaction
      Message envelope → contents ← messages
      本文mirror + Thinkingごとのmirror → semantic_records
  → root正常終了だけ会話採用
      conversation_messages / session_turnsが既存Messageを参照（本文コピーなし）
```

所有：provider assemblyは受信値とメモリ上のModelResult、agent loopは現在のMessage列、
journalはexecution・request相関と保存後のlive公開、history.Storeはtransaction・digest・record ID、
conversation.Viewは軽量表示entityを所有する。最新wire/budgetは別のsnapshot ownerで、本件から触らない。
現在実行中の次要求はメモリ上のMessageを使い、毎回DBから再読込みするわけではない。

主要source（行番号は調査時）：

| 境界 | source・確認内容 |
| --- | --- |
| Chat受信 | `internal/provider/protocol/stream.go:181–218,752–770`。同じ文字列reasoning deltaをchatReasoningとThinkingへ蓄積。配列reasoningDetailsは別のraw情報 |
| Responses受信 | 同`:332–360,440–539,752–756`。可読本文/summaryをThinkingへ、output item全体をReplayItemsへ |
| 結果と通知順 | `internal/provider/protocol/generate.go:93–112,144–196`。model_result通知の後にもrequest end通知がある |
| Message生成 | `internal/agent/loop.go:122–185,251–257`、`internal/contracts/contracts.go:33–87,122–196` |
| 保存とlive | `internal/app/journal.go:43–146,270–280,392–435`。元Observationは保存用コピーと別、保存成功後に公開 |
| progress | `internal/history/conversation_write.go:583–709`。初出markerのみ追加し累積本文は置換 |
| 結果の先行確定 | 同`:348–485`。Message未到達でも再生できる確定点 |
| Message/mirror | 同`:97–136,221–273,513–542`。各mirrorは元Message全体をコピーしtextをFact.Textで上書き |
| 終了時の未完了本文 | `internal/history/write.go:419–561`。active行をincomplete記録へ移しterminalと確定 |

### 2.2 どこへ何回保存されるか

以下は、本文あり・Thinking 1件・providerStateあり・Message保存まで成功した通常の1物理応答。
Thinkingがt件ならMessage mirrorは原則1+t件。空本文、配列reasoning、Responses等で形は変わる。
「コピー数」は文字列/JSON fieldの出現数であり、SQLiteページ数、毎deltaの書込み回数ではない。

| 保存先 | assistant本文 | 可読thinking | provider継続情報 | 読者・所有／寿命 |
| --- | --- | --- | --- | --- |
| `assistant_text_states.event_json` | 最新累積1行 | item別最新累積1行 | 全stateはない | active表示cut。完了/terminal時削除。deltaごと置換の書込みは残る |
| started semantic marker | なし | なし | なし | 初出順序・phase・request。本文削減対象ではない |
| `model_result.payload.result` | text 1回、callsも保持 | state内に重なる場合あり | 1回 | Records、walk、recall。Message保存前から存在 |
| SaveAssistantResultのthinking record | なし | itemごとtext 1回 | なし | live/replay/detail。incomplete/supersededもMessageなしで成立 |
| `messages.content_digest → contents`のMessage envelope | content.textまたは付随text 1回 | state内に重なる場合あり | 1回 | Conversation、Messages、AssistantReports。会話継続の基礎 |
| Message由来assistant mirror | 表示text 1回＋元content（回答型なら本文も1回） | state内に重なる場合あり | 元Messageの全state 1回 | Records、walk、recall。既にenvelopeと同じcontent_digestとMessage locatorを持つ |
| Message由来thinking mirror | 元content（回答型なら本文1回）。tool呼出し型では付随textがthinkingへ上書きされる | 表示text 1回＋state内の可読部分 | 元Messageの全state 1回 | 同上。thinking表示に不要な回答/calls/stateまでコピー |
| canonical採用行 | 参照のみ | 追加コピーなし | 追加コピーなし | `conversation_messages → messages` |
| terminal `host_decision` payload | 最終回答finalText 1回（ある場合） | なし | なし | 実行の終了結果。子collectはExecutionからterminal IDを取得し、child_terminal relationの参照先にする（payloadは読まない） |
| `executions.runtime_outcome_json` | 同じ終了結果のfinalText 1回 | なし | なし | ExecutionTerminal→Coreの結果再読込み。terminal_record_idも別に所有 |
| afterTurn hookの開始観測Input | hookがある場合outcome.finalText 1回 | なし | なし | cleanup hookに渡した結果の証拠。hook未定義では発生しない |
| 最新wire専用行 | 選択済み会話として含まれ得る | encoderが送る範囲 | encoderが送る範囲 | 最新送信の調査用。必要な別表現として今回変更しない |

したがってこの通常例のproviderState全体は4箇所（result、Message、2 mirrors）。
回答型の可視本文は最大5 field（result、Message、本文mirrorのcontent/text、thinking mirrorのcontent）に現れる。
Chat文字列reasoningは、上記4 stateのreasoning.textと2 thinking記録のtextに最大6回現れる。
最終回答だけはさらにterminal/runtimeOutcomeへ各1回、afterTurn定義時はその入力へも現れる。
これは型を限定した説明であり、全provider・全responseの固定回数ではない。
`semantic_records.content_digest`がenvelopeを参照すること自体は追加本文コピーではなく、payloadへ埋めるJSONが重複である。

### 2.3 読者と公開contract

| 利用動作 | 実際の読取り経路 | 計画上の維持条件 |
| --- | --- | --- |
| 次turn/再起動後の継続 | `app/context_budget.go:12–45` → turn単位`Conversation` → Message decode → encoder | 本文・calls・providerStateが同じ型/値で復元。private-state境界・model変更時の既存処理は変更しない |
| provider継続 | `protocol/encode.go:54–97,150–204` | Chatは一致provider/modelのreasoning、ResponsesはReplayItems等を使用。Thinkingから再生成しない |
| execution / canonical履歴 | `history/read.go:20–120`のMessages / Conversation | 公開Messageに内部参照を漏らさず、現行Messageを復元 |
| 詳細/単体record | `history/read.go:123–258`、`app/service.go:263–332` | Record単体取得が全execution読込みを要求しない。PayloadとContentの現行意味を維持 |
| 軽量replay、親子SessionView | `history/conversation.go:150–247`、`history/walk.go:21–139,268–342` | **Recordsを通らずSQLでtext直接抽出**。64件/page、cut、初出位置を維持。provider全文のGoへの転送を増やさない |
| CLI history | `cmd/hjh/history.go:45–141,302–393`、`historyview/historyview.go:164–221,256–419` | session/canonical/detail、local/Core接続両方。`--json`はConversation経路。CLI sessionのsuperseded扱いをTUIに合わせて勝手に変更しない |
| Core API | `httpapi/server.go:96–118,477–550`、`dto.go:53–72` | conversation/view/inspect/recordの既存DTO、identity・relations・本文を維持 |
| TUI live/replay/detail | `corerun/conversation.go:41–68,231–267` → `conversation.View` → `tui/model.go:997–1024,1374–1394`、`commands.go:61–88,140–206` | entity.Textの直接描画、thinking種別、安定ID、detail locator、capture→subscribe接続を維持 |
| 子status / collect | `app/child_runs.go:109–178`、`history/assistant_reports.go:11–50`、`app/delegation.go:78–136` | AssistantReportsはSQLで可視本文だけと全Message watermarkを取得。親へは報告/最終結果、継続stateは渡さない |
| 失敗後recall | `history/recall.go:171–230,315–445` → `recall/projection.go:225–230,318–380` → `app/execution.go:263–297` | Messagesなしでもresult/partial本文を参照可能。providerStateを除去、supersededを除外、source locatorを保持。独立JOIN経路も対応 |
| 終了結果の再読込み | `history/conversation.go:261–278` → `corerun/manager.go:455`、`app/child_runs.go:149–178` | CoreはruntimeOutcomeを読む。子結果はexecuteのFinalTextから生成され、collectはメモリ上の結果を返してExecutionのterminal IDへrelationを付ける。finalTextの所有変更ではdurable読戻しと子結果生成・collect相関をそれぞれ確認する |
| cleanup hook | `app/lifecycle.go:124–169`、`app/observation_storage.go:41–65` | afterTurnへ完全outcomeを渡し、その開始観測を保存。hook動作・実入力は本案で変更しない |
| Session削除 | `history/session_delete.go:100–139` | 新しい所有も既存FK収集/残存参照確認に乗せる。本件で既存データを削除しない |

親子は別execution。親の正常完了で子のMessage全体を親canonicalへコピーしない。
子の最終回答を親tool結果として受け取るのは別の利用情報で、その再表現は今回の削減対象にしない。
公開詳細で同じ値が複数のrecordに再出現しても、**永続コピー削減と公開JSON削減は別**。

## 3. 実DB観測：既存123件と現行sourceの照合

2026-10-16 00:14–00:26 JST、`.hjh/history.sqlite3`をPython sqlite3の
URI `mode=ro`、`PRAGMA query_only=ON`、`BEGIN`内でSELECTしrollback/closeした。
SQLite経由でWALを読み、immutable指定、本体だけのコピー、journal変更、checkpoint、変換は行っていない。
本文原文やcredentialは出力せず、件数・bytes・shape・相関IDを集計した。
追加でterminal/runtimeOutcomeの最終回答経路も読取り専用で照合した。

Session ID：`600ce255-e67d-4464-8ef0-f5f877cd23b2`。現在は11 executionsになっているため、
Session全体を過去の4 executionsの母集団と混同しない。
過去集計を再現した対象は次のrootと再帰子孫だけ：

- root `2cc93d34-2ed7-41f4-a84e-9a729ec78071`（completed）
- child `82879278-7480-48d9-8472-90e0071f12fb`（completed）
- child `fb589e6e-b785-4394-ac3a-039bcba9c71e`（failed）
- child `2f3b4167-b8eb-461c-a9e8-7a084ebe83d2`（completed）

作成開始〜最終settleは2026-10-15 05:10:50–05:55:56 UTC。
124 logical requests、123 completed model_result、349 Messages（assistant 123）。
assistant_message 597件 / 1,528,323 bytes、model_result 251件 / 542,321 bytes、
最新wire 4件 / 1,423,231 bytes、最新budget 4件 / 35,571 bytesで、指定資料の集計と一致した。
過去データにはusage整理前の重複があるが、今回の削減量には含めない。

### 3.1 確認できた重複

| 観測 | 件数・量 | 解釈 |
| --- | --- | --- |
| thinking mirrorのtextと同recordのproviderState.reasoning.textの完全一致 | 123件、片側UTF-8 **213,950 bytes** | 指定観測を再現。全件Chatのreasoning_content。原文量であってDB削減量ではない |
| Message由来mirrorのcontentと参照先Message.contentの値一致 | 246/246件 | 本文mirror123＋thinking mirror123。calls等も含む同じcontent |
| 同mirrorのproviderStateとMessage.providerState一致 | 246/246件 | 2 copiesの正規化JSON値合計475,738 bytes（キー等を除く） |
| Message.providerStateと同じ物理要求のresult.providerState一致 | 123/123件 | 片側正規化JSON値237,869 bytes。ただしMessage未到達も別に考慮が必要 |
| thinking mirrorと先行thinking recordのtext・thinkingKind一致 | 123/123件 | execution＋physicalRequestId＋localKeyで結合。先行record参照候補 |
| terminalとruntimeOutcomeのfinalText一致 | 4/4実行（非空3件、片側UTF-8 47,518 bytes） | 非空最終回答はterminal結果とruntimeOutcomeへ重なる。schemaのterminal_record_idで既に相互に辿れる |

最初の一致recordは`2cc93d34-2ed7-41f4-a84e-9a729ec78071:semantic:34`。
Message ordinal 1、itemIndex 0、requestOrdinal/modelStep 1、physicalRequestId
`opencode-go-chat.chat-completions-1`を持つ。最終側の一致recordは
`fb589e6e-b785-4394-ac3a-039bcba9c71e:semantic:190`。

**原因は確認できた。** Chat decoderが同じreasoningから可読Thinkingと継続stateを作り、
`AppendConversationMessage`がThinking Factを展開後、`messageFactPayload`が元Messageの
content/providerStateをコピーしてtextをThinkingに上書きする。この用途の違いは残すが、
同じ表示recordへ元Message全体を再保存する必要はない。既にそのrecordのFKとlocatorが元Messageを指している。

### 3.2 量の区別（削減実績ではない）

246 mirrorsの元payload合計は1,149,353 bytes。
Pythonでキーsort・空白なし・非ASCIIそのままのJSONへ再直列化し、**全246件で元bytesとの一致も確認**した。
その上でcontentとproviderStateの2 fieldだけをメモリ上のobjectから除くと441,657 bytes。
差 **707,696 bytes（約0.675 MiB）** は、この標本で既存参照から復元可能と確認した重複fieldの符号化量である。
既存content_digestを使うS-1には新しい参照fieldの追加が不要だが、実装後の正確な保存差は再測定する。

追加のメモリ上試算では、S-1後のthinking mirrorからtextを除き
`thinkingRecordId: <先行record ID>`を加えると、123件でさらに209,169 bytes少ない。
これは**提案encodingの見込み**であり、採用実装・物理DBでの測定ではない。
S-1＋この試算は916,865 bytes（約0.874 MiB）だが、確定した製品削減量として扱わない。
S-3のstate共有は最大でも既にS-1で除いたmirror側量を再加算せず、残るMessage/result間から評価する。

707,696 bytes、213,950 bytes、state値の集計は重なるので足し合わせない。
SQLiteページ・索引・WAL・総書込み・速度・メモリの改善量は**未測定**。
本調査はDBを縮小しておらず、Sessionの物理占有量・全provider共通の削減率を算出していない。

再集計手順：rootから`executions.parent_execution_id`の再帰CTEで4 IDsを固定し、
`semantic_records`と`messages JOIN contents`を取得。mirrorはkind=assistant_messageかつcontent_digestあり、
thinkingはconversation.entityKindで分ける。physicalRequestIdはexecution内のキーとして使う。
本文bytesは`length(CAST(payload_json AS BLOB))`、UTF-8長で集計し、JSON値比較と直列化bytes比較を分ける。

## 4. 必要な別表現と参照化候補

### 維持する別表現

- 表示Thinkingと継続stateは用途が違う。Reasoning.Field、ReasoningDetails、ReplayItemsのopaque情報、
  itemの構造・順序をThinking.Textから再生成しない。Responsesの全stateをthinkingと同一視しない。
- model_resultはMessage確定前の独立した証拠。Messageができなかった結果や、retry前のsuperseded、
  失敗時partialに、未来のMessage参照を要求しない。
- model resultとMessageのschemaは別。calls→content、本文→content.text/付随textの対応を保つ。
- 最新wireは送信bytes、canonicalは継続Message、軽量Factは表示用。用途の違いをなくして統合しない。

### 推奨の所有と参照

1. **S-1：Messageは現状のまま、mirrorsのcontent/providerStateを既存content_digestから復元。**
   raw payloadには表示text・phase・request・conversation・locator・その他metadataを残す。
   公開Records/Recordでは参照envelopeのcontent/providerStateだけを合成し、mirrorのtextを上書きしない。
   `Record.Content`は従来どおり公開Message envelope。新規body table、hash、cache IDは作らない。
2. **S-2：thinking mirrorのtextを先行thinking record IDで参照。**
   先行recordは既存の`SaveAssistantResult`が所有するtextを維持。Message mirrorのID・順序は残す。
   phase/request/conversationは参照元自身の値を保持し、sourceから上書きしない。
   参照は同じexecution・物理試行・localKeyの先行recordに向ける。単なるstep一致では結ばない。
3. **S-3（別承認推奨）：model_resultを継続情報の先行所有者にする。**
   `result` object（本文/calls/finishReason/providerState、既存usage整理済み）をcontentsへ一度保存し、
   model_resultの既存content_digestで所有。payloadはresultRefを持ち、公開readで元resultへ展開する。
   Messageはcontent/textを維持し、providerStateだけを元resultのrecord ID経由で参照する内部表現にする。
   これなら完了結果は後続Messageに依存しない。Message再開時に完全stateを復元する。
   result objectのoffload自体はコピー削減ではなく、Message側state省略が効果である。
   appはSaveAssistantResultの返値に結果record IDも受け取り、Message保存入力へ明示的に渡す。
   本文が空・Thinkingなしの結果でもIDを受け取れる必要がある。表示Factのlocatorを意味変更して流用しない。

S-3は現行FKを再利用する案で、schema v4のtable/indexを増やさない方向。
汎用OccurrenceInput.Contentの添付と衝突しないよう対象を通常のSaveAssistantResult経路に限定する。
`Store.AppendMessage`等、元model_resultを持たない独立Message保存入口には、現行のinline stateを保持する
明示的な入口契約を残す（旧形式fallbackではない）。app通常経路は元result IDの明示handoffで統一し、
全DB検索・step推測でそれらしいresultを探す方式にはしない。
新規内部形式のversion/識別は実装時に設計契約へ明記する。古いDB用の変換や互換分岐は足さない。

通常assistant本文のresult↔Message間の残る重複は、S-1〜S-3後にも残る。
今回はMessageの可視本文・callsまで参照化するスライスは推奨しない。
AssistantReportsのSQL、全Message入口、継続読取りまで影響を広げる割に、標本のresult.textの
正規化JSON値は56,989 bytesであり、先に大きく局所的なmirror重複を解く。
terminal `host_decision`と`executions.runtime_outcome_json`の最終回答も重複候補。
`journal.end`（`journal.go:451–462`）→`write.go:521–548`は同じ結果を両方へ保存している。
既存terminal_record_idから結果を読む案は可能性があるが、Coreのdurable結果再読込み、
terminal metadataの除去、子結果生成・collectのterminal ID相関をそれぞれ確認する必要がある。
子collectはterminal payloadから回答を復元する経路ではない。今回の採用slice外の後続候補として残す。
afterTurnのoutcomeも最終回答の再表現であり、観測の参照化候補だが、hook入出力保存の変更は本案に混ぜない。
この47,518 bytesは原文片側量で、S-1〜S-3の削減見込みには加算しない。
同一record内でproviderState.reasoning.textだけをThinking参照へ分解する案も、
provider固有構造への依存が増えるため今回の推奨から外す。捨てずにowner全体を共有する方を選ぶ。

## 5. 正常・失敗・取消・retryの相関

| 経路 | 現行の確定点 | 改善後に保つ対応／確認 |
| --- | --- | --- |
| 正常（回答/tool呼出し） | resultとThinking確定後にMessage、その後root採用 | result→Message→mirrorsの参照が同じ物理要求。再起動後の次要求に同じ本文/state/calls |
| result確定後、Message前で停止 | result transactionのみ成功し得る | MessageなしでRecord、walk、recallが本文を読める。先行ownerを消さない |
| stream途中の最終失敗 | active累積stateをterminal時incomplete化 | partial本文/Thinkingとrequestが残り、canonicalへ採用せずrecallのassistant_partialに使用 |
| 取消 | 同上。journal contextは取消から独立 | retryせず、取消前の本文/thinking、親子outcome、recall sourceを維持 |
| length終了 | result/Messageをincompleteとして保持し失敗 | 部分tool引数を実行しない既存動作を維持。partial Messageのstateも復元 |
| 1回再送 | 最初の試行をsuperseded確定＋active削除後、retryAttempt=1 | logical stepは同じ、physical IDとlocalKeyは別。最初の本文を再送結果へ結ばない |
| 再送の最終失敗 | 最後の試行だけincompleteへ | supersededはdetailに残すが通常TUI/recallから除く。最後のpartialはrecall可能 |
| 親と子の同step | 別execution | execution IDを必ずキーに含める。親child_terminal/spawn/parent_call関係を維持 |

RequestIdentityはexecution ID、lane、requestOrdinal、modelStep、physicalRequestId、retryAttempt。
`AssistantKey`はretry時`/retry/1/`を含む（`contracts/request_identity.go:5–12`）。
active表はretry専用列を持たず、先行試行の確定・削除で分離するため、列を増やす計画ではなく
この順序を維持する。firstSequence/sequence/entityKind/localKey/itemIndexとrecord/message ordinalも維持する。
参照先はimmutableな先行record/Messageに限定し、上書きされるactive最新行を永続mirrorの参照先にしない。
書込み失敗・強制終了の永続化保証を新たに増やす計画ではない。

## 6. 実装・確認可能なスライス

各sliceはwriterだけで終わらず、その形式の全読者まで同じsliceで成立させる。
実装後に具体的なproduct動作へ対応するfocused testを追加し、独立reviewを行う。
全体testは途中で反復せず、最後の安定候補にcoordinating ownerが一回実行する。

### S-1：Message由来mirrorsの元Messageコピーを参照化【第一推奨】

- 変更境界：historyの`messageFactPayload`/AppendConversationMessage、Records/Recordのhydration、
  RecallEvidenceの独立JOIN。専用assistant mirror codecとしてtool codecやprovider層と責務を分ける。
- raw mirrorからcontent/providerStateのみを外す。既存record FKが持つMessage envelopeから公開JSONへ戻す。
  public Record.Contentも元の値を維持する。RecallEvidenceでは展開後に既存のproviderState除去を行うか、
  providerStateを合成しないpublic用projectionで同じ結果を作る。参照追加でrecallへprivate stateを戻さない。
- walkが本文以外にcontent由来name等を読む箇所も照合し、必要fieldだけSQLで参照する。
  visible textは残すためlive/replayの本文取得経路は不変。無関係なtool本文の展開を増やさない。
- 維持する利用：通常再開、thinking表示、単体detail、親子SessionView、recall。
- 確認：実受信Chat標本を使う同一SSE入力で親→子報告/collect→親回答。保存前liveとclose/reopen後の
  Messages/Conversation、Records/RecordのPayload・Content・relations、SessionView、CLI history三view、
  Core単体record、TUI本文/thinkingを比較。raw SQLではmirrorの重複fieldがなくFK先が元Messageであることを確認。
  result後Message前の停止とstream失敗は既存動作が維持されることをfocusedに確認する。
- 完了単位：S-1だけで利用可能。707,696 bytes標本相当の差を再生測定できるが、まだ物理量は断定しない。

### S-2：thinking本文を先行recordへ集約【S-1後の推奨】

- 変更境界：SaveAssistantResultの返却情報、journalのexecution別request相関、ConversationMessageInput、
  assistant mirror codec、Records/RecallEvidence、walkのSQLまたはpage単位hydration。
- journalは既に成功したthinking Factのrecord locatorをrequest/localKey別に持ち、Message保存へ明示handoffする。
  firstSequencesは表示位置専用のままにする。内部mapの所有は当該journalに閉じ、永続cacheを追加しない。
- 元結果を持たない独立Store呼出しはその入口が渡すThinkingを直接保存する契約とする。
  app通常経路のsource選択を曖昧なDB検索や本文一致による推定にしない。
- mirrorはthinkingRecordIdを持ち、sourceのtextだけを復元。既存source recordとmirrorの両IDを残し、
  既に発行されたdetail locatorと会話entity順序を変更しない。
- walkはRecordsを経由しないため必須変更。64件/pageと固定cut、同一executionの先行sourceを使い、
  SQLでtextのみ抽出またはpage内必要IDをまとめて読む。全provider replayをロードしない。
- 維持する利用：複数thinking itemとkind/順序、初出位置、live→replay接続、retry別表示、単体detail、recall。
- 確認：Chat標本で先行sourceとmirrorが同じTextになること、Responsesの可読item/summaryとopaque stateが
  独立に保持されること。物理試行が異なる同step retry、Message未到達、取消後incomplete、
  superseded除外を実際のapp経路で確認し、Core SessionViewとPTY TUIの再読込みを照合する。
  既存`TestModelResultIsReadableWhenGenerateStopsBeforeAppendingMessage`、
  `TestJournalKeepsAssistantAndThinkingPositionsThroughMessageFinalization`の対応動作を維持する。

### S-3：result所有のproviderStateをMessageから参照【追加承認、実装・確認・review完了】

- 変更境界：SaveAssistantResultのresult object codec/所有FK、戻り値、journal→Message入力の元record ID、
  Message保存/復元codec、Records/RecallEvidence/walkのresult読取り、Session削除の所有収集確認。
  provider protocolやencoder、contracts.Messageの公開構造は変えず、内部保存形式だけを変える。
- result本文の所有と参照を同transactionで保存。Messageは既に確定した同物理試行のresult IDを受け取る。
  後続Messageができなくてもresult+thinkingの独立読取りが成立する。
- Message.content/textはinlineのまま。AssistantReportsの既存本文抽出とcursorを維持し、
  provider stateをGoへ転送しない性質も維持する。
- 公開Record.Contentへ新しいresult blobを追加返却せず、従来payload.resultを復元する。
  mirrorのRecord.Contentは完全Messageへ戻す。resultRefの所有とMessageの間接参照は同execution内。
- 維持する利用：再起動後のChat reasoning / Responses replay、resultだけの失敗recall、子報告、Session削除時の残存参照。
- 確認：同じ受信SSEを使い、close/reopen→次turnのHTTP入力をbefore/afterで比較し、stateの値/構造/有無・
  opaque item順序が同じことを確認。state削除や可読textからの再生成で通るtestにしない。
  親子同step、retry、result直後停止、length、取消の既存production経路を使う。
  一時Sessionを削除して対象ownerだけ回収され、他Sessionの共有contentsを維持する既存利用動作も確認する。
- 実装時にdirect Store入口と通常app入口を含めたcodecのencodingを確定し、追加JOIN/read時間とstate shareの物理効果をS-1＋S-2をbeforeとして測定する。
  S-1/S-2で観測した詳細復元負荷と同様に、保存量だけで速度改善を断定しない。

### S-4：同一入力の利用確認・比較測定と結果文書

- 最終候補のCLI/Core/PTY TUIを隔離workspace・HOME・config・新規DBで実行する。
  通常会話→親子→Core停止/再起動→次turn、失敗/取消→recall、retry→成功/最終失敗を通す。
- `scripts/verify-usage-storage.py`と過去結果のharness構成は参考にするが、usage削減を今回の効果にしない。
  本件用の確認スクリプトと結果は実装後に作る。fixture成功を実provider利用成功と同一視しない。
- ownerが`go test ./internal/... ./cmd/...`を安定候補に一回実行する。失敗時はfocused確認で原因を絞り、
  再実行理由を記録。常用Core再起動や実provider呼出しは別途利用者と調整する。
- docs/design/normal-history-storage.mdとhistory-payloads.mdへ採用した内部形式と公開復元契約、
  docs/results/へ実測・未確認範囲を記録する。今回それらを先に採用済みへ書き換えない。

## 7. 同じ入力による測定案

### 固定入力と比較条件

1. 実装前のsource revision/dirty差分を捕捉しbefore binaryをbuildする。after/S-1/S-2/S-3候補を同じtoolchainでbuild。
   beforeはusage整理済みの現行sourceを使い、古い実DBと新DBの直接比較を改善実績にしない。
2. 読取り専用で採取したChat123標本の本文/可読thinking/state/要求対応を固定し、入力manifestのSHA-256を記録。
   原受信SSEが残っていなければ「保存値からの再生」であることを明記し、実受信イベント列の再現とは呼ばない。
   Responsesは利用可能な既存実受信証拠の形を確認して選び、架空variantを追加しない。
3. 同じtask・tool結果・親子構成・応答分割・要求数・失敗/取消位置をローカルHTTPで再生する。
   ID/時刻のランダム差は別記し、公開JSON比較は対応するIDをmappingして意味値を比較、本文/stateは正確な値を比較。
   動的IDがwireへ現れる場合はそのfieldを明示し、送信設定変更で一致させない。
4. 同一filesystem、WAL/FULL/default autocheckpoint、同じDB接続・close順、同じ負荷で新規DBを使う。
   圧縮・VACUUM・独自checkpointは行わず、常用DBを測定対象へ書込み使用しない。

### 指標・採取点

| 指標 | 方法・解釈 |
| --- | --- |
| 保存論理量 | raw semantic payloadのUTF-8長、参照先unique contents bytes、latest wire/budgetを別列で集計。digest重複を除く。公開復元JSONを保存量として数えない |
| 対象別削減 | 本文mirror / thinking mirror / 先行thinking / result / Message envelopeを分け、slice累積とincrementalの両方を出す。stateとthinkingの重なる値を二重加算しない |
| 物理容量 | dbstat table/index割当、page_size/page_count/freelist、DB/WAL/SHMのapparent sizeとst_blocks。schema作成後・最終result直後/terminal前・会話採用/snapshot後・全接続close直後に記録 |
| 書込み量 | `/proc/<pid>/io`のwrite_bytes/wchar等をrun前からterminal・snapshot・全close/checkpointまで差分採取。Coreと外部HTTP/harnessのprocess範囲を固定して明記。readback/report書出しは区間外 |
| active中書込み | 同じdelta分割の入力でprogress期間も採取。activeの累積置換は本案で減らないため、終端JSON削減と区別 |
| 読戻し時間 | close/reopen後にConversation(turn)、Messages(execution)、Record単体（本文/thinking/result）、Inspect、SessionView/walk、AssistantReports(after)、RecallEvidence→Projectを個別に測る |
| 人間向け読取り | 実CLI historyのsession/canonical/detail、Core API、PTY TUIのSession再選択/初回表示。API転送bytesとserver時間/CLI全体時間を分ける |
| 補助 | process CPU/wall time、VmHWM、SQL query数/読込bytes。JSON再構成による負荷が増えた箇所を特定し、保存量だけで採用判定しない |

WALファイルサイズは再利用領域を含む時点値で、累積書込み量ではない。
process I/OはSQLite専用・SSD物理書込みと断定しない。最後のclose/checkpointを除く測定と混ぜない。
read-only再openでsidecarが見える場合をclose直後の観測と区別する。

各候補は同一入力のfresh DB生成を交互順で反復し、sample数・中央値・範囲と生データを残す。
読取りはfirst-open（OS page cacheは未制御と明記）とwarm反復を分け、同じ範囲・返却件数を使う。
公開本文/state/相関・outcome・canonical採用・recall projectionの一致確認を伴わない高速化は成果にしない。

## 8. 利用者が採用した方針と実施順

独立レビュー後、利用者は判断事項をすべて推奨どおりとした。
**第一段はS-1を先行してS-2までとし、実装・測定・reviewを完了した。その後、利用者がS-3の説明を確認し「ああやろうか」と追加実装を承認した。**
S-1は現行Message/継続経路を変えず、既存FKだけで大きなmirrorコピーを除ける。
S-2は別記録を削除せず、先行thinking本文を一度所有して表示recordから参照できる。
S-3は追加効果が見込めるが再開codecまで変えるため、先に軽い2段の実測を得る。

確定した判断：

1. 第一段S-1＋S-2とS-4の確認・比較測定は完了。追加承認されたS-3も実装・focused test・再開確認・比較測定・独立reviewを行う。
2. 公開Records/RecordのPayload・Contentは従来形へ透過復元する。
   raw SQL保存形式は変わるが、CLI/Core/TUIの利用情報を削らない。
3. 過去DBのdetail互換用の読取り・変換は追加しない。既存データは保持する。
   この判断は既存DBの削除・変換・新DB作成を許可するものではなく、それらは別の明示判断とする。
4. 先に隔離環境のローカル再生・CLI/Core/PTY確認を行い、実provider・常用環境の再開操作は
   別途時期・範囲を調整して行う。ローカル再生と実providerの確認結果は分ける。
   今回の方針採用だけで常用Coreの停止・再起動や実provider呼出しを開始しない。

本調査で確認できたのは重複と参照元の同一性、符号化量、実経路上の維持条件である。
調査・計画段階では改善後の製品動作・物理容量・I/O・読戻し性能は未確認だった。
今回の実装指示後に実施した確認だけを[結果](../results/assistant-thinking-storage.md)へ記録する。

## 9. 独立レビューと訂正

利用者指示による独立reviewerは指定資料・本書全体と現行sourceを照合し、
S-1〜S-3の設計上の阻害事項なし、経路説明のP3訂正1件と報告した。
子collectがterminal payloadを読むという説明は、`app/child_runs.go:149–178`と
`app/delegation.go:114–127`を親も確認し、上記の表・後続候補の説明を訂正した。
Coreのdurable読戻しと子のメモリ上の結果返却・terminal ID相関を区別する。
レビューではDB集計値の再検証、実装後の動作・容量・性能測定はしていない。
今回の追記・訂正でも製品source・test・DBは変更していない。
