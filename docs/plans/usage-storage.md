# Provider usageの単一保存と通常／調査記録の分離

2026-10-15。[依頼](../notes/usage-storage-task.md)に基づく計画を利用者が確認し、
独立レビュー後に「実装テストコードレビューに進んで」と指示した。
状態：**S-1〜S-3の実装・focused test・実操作・測定・code review完了。既存test期待2件を利用者承認で修正・独立reviewし、総合testも全体通過。**
調査はprovider/runtime経路と保存・参照経路を独立に照合し、計画の独立reviewでは修正を要する指摘なし。
文書内の相対リンクの存在と追加文書のwhitespaceを確認した。
実装・test・review・測定の到達点は[結果](../results/usage-storage.md)へ記録する。

## 1. 利用者が必要とする動作と範囲

通常の親・子の実行、要約用モデル呼出し、会話の再開・履歴閲覧を維持しながら、
provider usageの集計値を要求ごとに一度保存し、大きな項目別内訳を通常履歴へ保存しない。
入力・出力・合計・キャッシュ・reasoningの値はproviderが返した値を残す。
TUIのコンテキスト表示は従来のhjh推定のままとする。

根拠の優先順：利用者の依頼、現行の製品経路と実受信履歴、採用済み保存契約。
[history-schemaの改善メモ](../design/history-schema.md#2026-10-15追記usage詳細の二重保存と調査用記録への分離)、
[通常履歴の保存契約](../design/normal-history-storage.md)、
[従来改善の結果](../results/normal-history-storage.md)を確認した。
旧Henji資料やtest fixtureを新たなusage仕様の根拠にしない。

対象は今後の保存経路。過去DBの変換・削除・再作成・VACUUMは行わない。
DB非保存mode、コンパクションの方式、tool利用方針、provider timeout・再試行回数、
新collector／hook／デバッグ基盤の実装は対象外。
実provider呼出し、実行中runの操作、常用設定変更、Core再起動、commit/push/配置は行わない。

## 2. 現行の実経路と状態の所有

### 2.1 通常の親・子

```text
CLI / TUI → Core / Service → app.execute（親子共通）→ agent.Run
  → provider.Prepare / Generate → SSE assembly → ModelResult.Usage（raw JSON）
  → Observation(kind=usage, Result=usage)
  → Observation(kind=model_result, Result={text,calls,…,usage})
  → journal（execution・親execution・step・retry・関係を扱う）
      usage: AppendOccurrence
      model_result: SaveAssistantResult（本文/thinking/active stateの確定も行う）
  → semantic_records.payload_json
  → Records / Record / Inspect / RecallEvidence → Core・CLI detail・TUI inspect
```

- Provider protocolが受信usageを所有し、assemblyからModelResultと二つの観測へコピーする。
  Chat Completionsはイベント直下の`usage`、Responsesは`response.completed.response.usage`。
  内部fieldの意味解析や正規化はしない。
- journalが通常履歴の保存表現を決める。現在、両観測のDB上の`kind`は`model_result`。
  区別は**`payload.kind=usage / model_result`**で行う。
- history.StoreがJSONのcanonical encode、record ID・ordinal、transaction、relations、SQL読戻しを所有する。
  usage観測の保存が先に独立して成功し、続いてモデル結果が保存される。
- ModelResultのusageはMessage／provider stateへ移されない。会話採用は
  `conversation_messages → messages → contents`を使い、usage記録とは独立している。
- journalは保存後、**元のObservation**をGo側progress observerへ渡す。
  軽量ExecutionFactを使うCore/TUIの通知と、完全なObservationは別の契約。

主要source（調査時点）：

| 責務 | source |
| --- | --- |
| usage受信・ModelResult生成 | `internal/provider/protocol/stream.go:157–158,290–300,600–609` |
| 二つのusage通知・順序 | `internal/provider/protocol/generate.go:130–169` |
| raw契約・要求識別 | `internal/contracts/contracts.go:122–177` |
| step・retry付与 | `internal/agent/loop.go:122–138`、`internal/agent/retry.go:15–76` |
| 親子共通・progress接続 | `internal/app/execution.go:112–127` |
| 保存後live通知・両保存分岐 | `internal/app/journal.go:43–79,99–167,205–229` |
| モデル結果のtransaction | `internal/history/conversation_write.go:348–442` |
| JSON/record保存 | `internal/history/write.go`の`appendOccurrenceTx` |

### 2.2 要約用呼出し：通常loopとは別

```text
projectRequest hook → api.model.generate → requestContext.Call
 → requestModelGenerator → provider.Generate（provider観測はno-op）
 → JSへ {text, finishReason, usage}
 → Host完了観測(kind=host, name=context.model.generate)
    Result={outputBytes, finishReason, usage}
 → journal → effect_observation → resultRef + contents（既存の本文所有FK）
```

- generatorは解決済みprovider/modelを一度使う。独立execution、通常loopの再試行、
  最新wire/budgetの保存を追加しない。通常のusage/model_result観測は保存されない。
- JSへの返却値とHost観測値は既に別。hookには完全usageを返し、観測metadataにも現在は完全usageがある。
- 要約例`examples/hooks/compaction.js:120–129`は`result.text`のみを使用する。
  `context.model.generate`は汎用API名で、すべての呼出しの用途を「要約」と推定しない。
- 観測のHost call IDと親hook ID、および`parent_call` / `call_start` relationが既存の識別子。
  外側hookの入力にmodelStepはあるが、Host観測に通常loopのRequestIdentity／物理request IDはない。
  providerの物理IDはgeneratorの返却型からも落ちる。この計画で同一キーへ無理に統合しない。

根拠：`internal/app/request_model.go:16–39`、`internal/app/request_context.go:61–75`、
`internal/jsruntime/request_context.go:11–39`、`internal/jsruntime/host.go:167–184`、
`internal/history/tool_result_codec.go:15–45`。

### 2.3 読取り機能とattributionの利用有無

探索範囲：現行`internal/`、`cmd/`、`examples/`、関連設計・計画・結果文書、
workspaceの`.hjh/extensions`内のJS/Markdown。旧`_refs`、任意の外部利用者コード全体は対象にしない。

**Provider usage内の`attribution`を解析・集計・判断に使う製品機能や標準collectorは見つからなかった。**
ただし、次の汎用閲覧経路には現在そのJSONが現れるため、「誰も見られない情報」ではない。

| 参照元 | 現行の利用と変更影響 |
| --- | --- |
| Store Records/Record、Service Inspect | 保存payloadを返す。usageの意味解析なし。今後は集計usageだけを返す |
| Core record DTO | payload/content/relationsを公開する。usageの独立計算なし |
| CLI detail、TUI inspect | JSONとして人間が詳細を読める。今後attribution全文とモデル結果内の二重usageは出なくなる |
| canonical会話・Session再開 | 保存Messageを読む。MessageにUsage fieldはなく影響しない |
| RecallEvidence | usageを含むmodel_result系recordも取得するが、recall projectionは本文・finishReason等を使う。usageへ依存しない |
| TUI footer・/context | contextbudgetの推定・予算・metadataを使う。provider usageへ切り替えない |
| CLIのlive進捗 | kind/name/phase等を表示し、usage内訳を計算しない |

根拠：`internal/history/read.go:116–217`、`internal/app/service.go`の`Inspect`/`Record`、
`internal/httpapi/dto.go:53–71`、`internal/historyview/historyview.go:346–377`、
`internal/tui/commands.go:441–465`、`internal/history/recall.go:341–347,391–395`、
`internal/recall/projection.go:335–361`、`internal/tui/context_usage.go:10–23`。

hjhの`contextAttribution`（`internal/app/context.go`）は送信前metadata・件数・wire bytes・recall関係と
最新wire位置の保存を担当する別物。providerの`usage.attribution`除去の対象ではない。
認証画面の`PlanUsageEnabled`やtool-usage-policyも別概念。

## 3. 実受信履歴の証拠と残す値

### 調査方法・限定

2026-10-15 20:37 JST、`.hjh/history.sqlite3`をPython sqlite3の
`file:…?mode=ro`＋`PRAGMA query_only=ON`、読取りtransactionで参照した。
WALはSQLite経由で読み、本体コピー・書換え・実行中run操作はしていない。
取得対象はusage部分とrecord識別metadataのみで、credentialやAuthorizationを取得・掲載していない。
DB全体の探索件数は稼働中なので変動する。以下のSession単位の固定集計と区別する。

対象Session `64bbc534-6be6-4b26-9b61-afab2c825307`：親8・子4の12実行。
同executionとpayload.parentで照合し、usageとmodel_resultの**113組すべてのusageが一致**した。
依頼メモの片側usage 2,599,487 bytes、attribution 2,577,625 bytesを再現した。
非ASCIIそのまま・空白なしのPython JSON再直列化による値であり、保存中のGo JSON bytesそのものではない。

現行DBで確認できたprovider名／APIと形式：

| 実受信元 | 通常保存に残す確認済みfield |
| --- | --- |
| `opencode-go-chat.chat-completions` | `prompt_tokens`, `completion_tokens`, `total_tokens`, `prompt_tokens_details.cached_tokens`, `completion_tokens_details.reasoning_tokens` |
| `openai-chatgpt.responses` | `input_tokens`, `output_tokens`, `total_tokens`, `input_tokens_details.cached_tokens`, `input_tokens_details.cache_write_tokens`, `output_tokens_details.reasoning_tokens` |

例の所在：Chat Completionsは`85b15e62-e432-4afe-a7aa-3784505330a2:semantic:13`、
Responsesは`db3a1b99-2b55-482a-9127-14dfe2cdef9f:semantic:28`。
後者のattributionはobjectで、`items`（provider item ID別）と`request_fields`（instructions/tools等）を持つ。

### 保存用projectionの提案

- **usage object直下の`attribution`だけを除き、その他のfield・detail objectはその構造と値のまま保持する。**
  上表を固定allowlistにしない。cache・reasoningのdetail object全体を削らない。
- provider固有名を独自のinput/output共通形式へ正規化しない。数値を再計算せず、
  absentを0へ補完しない。providerが返した0は残す。totalへcached/reasoning等を加算しない。
- 除去は確認済みの直下fieldに限定し、未知のnested keyを名前で再帰削除しない。
  新たなprovider形式の拒否・上限・推測のfield matrixを追加しない。
- raw JSON契約は維持する。今回確認したobjectの編集が不要なusageは変換を強制しない。
  この方針は未知形式の全情報が集計値だと認定するものではない。
  未観測の詳細が将来現れた場合の保存粒度は、実受信と利用者要件を確認して別途判断する。

OpenRouterやOpenAI API認証等の別接続について、この現行DBから実受信を確認したとは主張しない。
protocol testの受信例は実装確認には使えるが、未観測providerの製品仕様にはしない。
今回のfield判断は上記実受信に基づき、追加の外部契約調査や実provider呼出しは不要と判断した。

## 4. 採用案

### 4.1 唯一の保存先

| 呼出し | usageの正本 | 保存しないもの |
| --- | --- | --- |
| 通常の親・子 | 既存`semantic_records`、DB kind=`model_result`、payload kind=`usage`の`result` | 同じ要求の`model_result.result.usage`、`usage.attribution` |
| projectRequest内のmodel.generate | 既存`effect_observation`のHost完了Result内`usage`（既存resultRef/contents） | その`usage.attribution`、別のusage専用recordの追加 |

通常のusage観測を正本にするのは、先に保存でき、後続のモデル結果保存が失敗しても集計値が残るため。
DB kind名は変更しない。新table、索引、本文共有機構、usageの別snapshot ownerは追加しない。
要約のcontents利用は既存Host結果codecの利用であり、新しい共有機構ではない。

### 4.2 要求との対応は既存IDを使う

通常のusageとモデル結果は同executionで同じ`payload.parent=physical request ID`を持ち、
それぞれ既存`parent_call` relationから物理送信開始recordへ辿れる。
モデル結果にはRequestIdentity（execution・lane・ordinal・step・physical ID・retry）もある。
usage自身にもmodelStep/retryAttemptがあるがtyped PhysicalRequestIDは現在付かない。
`parent`と保存済み開始recordがあるので、新しい識別子は不要。

- 親子はexecution ID／parent_execution_idで分ける。通常子でもlaneは`parent`なのでlaneで親子を判定しない。
- 同じstepの再試行は新しい物理request IDとretryAttemptで分ける。stepだけでusageを合算・上書きしない。
- provider request IDはprovider instance内連番。必ずexecutionと組み合わせるか、既存record IDを使う。
- 要約はHost call ID＋execution＋親hook／call_start関係を使い、通常要求とは独立して一回ずつ保持する。
  一つのhookで複数回model.generateした場合もHost call IDで分離できる。

**新しい`usageRecordId`・relation種別・自動再埋込みは追加しない。**
現行の利用者にモデル結果単独からusageを一跳びで必要とする経路が見つからず、既存関係で照合できるため。
集計usageはexecutionのInspect/CLI detailで読むと保存契約へ記す。
将来、直接参照が明示要件になったときだけ既存record IDのrelation案を再検討する。

### 4.3 変更境界と読戻し

appの保存専用projectionを、元Observationを変更しないコピーとして作る。
usage編集の小さな共通処理と、観測種別ごとの保存整形を分ける。

1. `Kind=usage`：保存用Resultへprojectionを適用し、既存AppendOccurrenceへ渡す。
2. `Kind=model_result`：保存用Resultの`usage`を外し、SaveAssistantResultへ渡す。
   **この分岐は現在`observationPayload`を通らず`contracts.JSON(o)`を使うため、必ず両分岐を接続する。**
3. `Kind=host && Name=context.model.generate`：保存用metadata内のusageへ同じprojectionを適用する。
   **PayloadとObservedResultを同じ縮小済みResultから作る。** 元ObservedResultが残ると
   tool_result_codecがそれを優先し、attributionをcontentsへ保存してしまう。
4. 他のtool/effectの原Result bytesやJSON内部は変更しない。全Resultを再encodeするような一般化はしない。
5. protocol parser、ModelResult.Usage、元のusage/model_result/Host Observation、
   JSのmodel.generate返却値は変更しない。呼出し元・progress observerへ完全usageを維持する。

読取りは新しい正本の集計値と既存relationsをそのまま公開する。
Records/Record/RecallEvidenceでattributionやmodel_result.usageを再構成しない。
Message・会話採用・provider継続情報・thinkingの生成保存は変更しない。
CLI detailやTUI inspectの汎用表示は原則ソース変更不要だが、表示経路で確認する。
旧の二重保存を期待するtestは、新しい保存契約へ更新し、実行時rawの確認と分ける。

### 4.4 過去履歴・切替条件

DDL、table/index、schema v4は変えない。旧recordを特別に解釈する互換read／migrationは追加しない。
既存の汎用readが過去payloadをそのまま返せる範囲で、過去のraw usage／二重usageは残る。
新旧が混在しても、モデルの継続はMessageを読むため保存切替だけを理由に新DBを要求しない。
DB全体の全期間を一律に「単一保存済み」と扱わず、改善は新規保存分として測る。

将来の配置時はjournalを持つwriter binaryの切替が必要。稼働中旧writerの出力は旧方式のまま。
Coreの停止・再起動・配置の時機は利用者の別指示で決める。この作業では実施しない。
過去のattributionを読めなくするための書換え・削除も行わない。

## 5. 調査用の取得口と未対応範囲

- 通常親子：Go APIの`RunOptions.Progress`／`RunRequest.Progress`には保存後の元Observationが届く。
  開発用Go callerはusage観測だけを選んで通常DB外へ収集できる。既存のcallback接続点を維持する。
- 要約：JSの`await api.model.generate(...)`のusageとGo側Host完了Observationに完全値がある。
  既存hookで値を扱うことはできるが、標準compaction例は収集していない。
- **JSの`api.writeFile`を通常DB外collectorと同等とは扱わない。**
  Host開始観測はwriteFileの入力全文を保存する（`internal/jsruntime/host.go:14–22`）。
  usageをtextへ入れると、調査fileに加えて通常DBにも詳細が再流入する。
  汎用toolの明示的な入出力まで削るのは今回の範囲ではない。
- `examples/hooks/context-metrics.js`は送信前のhjh推定値を記録するもので、provider usage collectorではない。
- 通常モデル応答後のJS専用hook、標準raw-usage collector、そのCore/TUI有効化操作はない。
  lifecycleのafterTurnへusageが一括配信される仕組みもない。
  Coreの軽量SSE factsから完全Observationを収集できるとは主張しない。
- 受信assembly失敗時はusage観測生成前に戻るため、現行経路は全失敗応答の精密収集を保証しない。
  要約generatorは失敗時usageも返さない。この計画で回収範囲・再試行動作を拡張しない。

従って、既存Go Observerを使える開発者には取得口があるが、通常CLI/TUI利用者が後からDBだけで
attributionを取得することは新規保存分ではできなくなる。新収集基盤は別テーマとする。

## 6. 実装スライス

各スライスは実装 → 変更箇所のfocused test → 独立review → 指摘があればfocused修正・再review。
reviewは機能・明示契約・実経路・具体的regressionを対象とし、一般hardeningや仮想matrixを加えない。
reviewerにfull gateは要求しない。以下は確認計画であり、実行済みの範囲は結果文書で区別する。

### S-1：通常親子の単一保存

- appに保存用usage projectionを置き、usage観測とSaveAssistantResult両分岐へ接続する。
  scopeは`internal/app/observation_storage.go`、`journal.go`と必要な小さな保存専用module。
- 同じ物理要求のusageはusage recordだけに保存する。元Observation／ModelResultを変えない。
- 保存契約を`docs/design/normal-history-storage.md`へ承認内容として反映し、
  `history-schema.md`の改善メモから実装計画／結果への案内を実装時に追記する。
- focused確認：実受信2形式のusageをローカルSSEからprotocol→app→Storeへ通す。
  input/output/total/cache_write/cached/reasoningの値と有無、attribution不在、
  同一要求の二重usage不在、close/reopen後Records/Recordの集計値を照合する。
  live Observerの両観測と直接providerのModelResultにはraw attributionが残ることも確認する。
- 親子の同じ番号のphysical IDを別executionで保存し、既存親子関係・parent_callが混線しないことを確認する。
- S-1 review：保存分岐漏れ、rawの破壊、要求照合と会話保存への影響を確認する。

### S-2：要約Hostの保存粒度と調査境界

- `context.model.generate`完了の保存用metadataだけを縮約する。
  PayloadとObservedResultを同じ保存用コピーへ揃え、既存resultRef/contentsを使う。
  jsruntimeの実行時返却値・metadata生成・Promise完了動作は維持する。
- focused確認：実JS projectRequest hook→api.model.generate→ローカルHTTP→Host完了→journalを通す。
  hookが完全usageを読めること、Go observerも完全値を受けること、保存contentsと読戻しは集計だけであること、
  outputBytes/finishReason、Host/hookのID・親関係を保持することを確認する。
- 一つのhookで複数のモデル呼出しを行い、各Host callのusageが一度ずつ独立して読めることを確認する。
  通常rootのwire/budget/ctxを要約の値で上書きしないことは既存app経路で確認する。
- 無関係のtool/effectの原JSON bytesが変わらないことを既存focused確認で維持する。
- S-2 review：ObservedResult経由の詳細再流入、hook返却値の縮小、完了回収経路の漏れを確認する。
  調査用取得の制約も保存契約へ明記する。新collectorは実装しない。

### S-3：要求識別・履歴利用と総合確認／測定

- 通常の一回再試行をローカルHTTPの既存retry経路で発生させる。
  同じlogical step、新physical request、retryAttempt、supersededの保存を照合する。
  失敗試行に返っていないusageを0で生成せず、成功試行のusageだけを該当要求に結ぶ。
  再送回数や2秒delayは変更しない。
- 一時workspace/DB、ローカルHTTP model、候補binaryの独立Coreと接続CLI/TUIで次を人間の操作経路として確認する。
  1. 親から子を実行・回収し、集計usageと親子/要求関係をInspect/CLI detailで読む。
  2. projectRequestによる要約モデル呼出しを行い、そのHost記録と通常要求を区別して読む。
  3. tool結果・親子の最終回答を保存後、一時Coreを終了し、同じDBを別プロセスで開いてSessionを継続する。
     次のHTTP入力に保存Message・tool結果があることを照合する。
  4. 会話/session/canonical表示、thinking、失敗試行の詳細を維持し、TUI ctxと/contextがhjh推定のまま一致することを確認する。
- focused対象は変更したappの保存/要約/親子/retry経路と、history読戻し・recall・TUI既存推定表示。
  新しいtestは上記の具体的動作だけへ対応させ、件数を目標にしない。
- 下記測定と安定候補の最終reviewを行う。最後にcoordinating ownerが
  `go test ./internal/... ./cmd/...`をauthoritativeな総合testとして一回実行する。
  失敗時はfocused切分けを行い、再実行理由を記録する。format/lintは変更箇所、`git diff --check`も確認する。
- 実操作、測定条件と限界、focused/総合確認・reviewを`docs/results/usage-storage.md`へ記録する。
  実providerを呼ばないため、新しい外部service動作の確認や全provider接続の実証とは扱わない。

## 7. 同一入力の削減測定

実装直前のsource（未commit変更を含む）を他の変更を戻さず検証用に捕捉し、before候補を固定する。
afterと同じローカルHTTP応答・usage・tool内容・実行シナリオを、別々の使い捨てDBへ入力する。
常用DBのコピーを最適化して比較する方式ではなく、新規保存経路の比較とする。
providerの真のランダム出力や追加API費用に依存しない。

- 入力A：対象Sessionのusageだけを読取り専用で採取し、同一usageをbefore/afterへ再生する。
  113回分のusageで通常保存量を比較する。本文・toolや秘密情報の複製は不要。
  採取の再取得時刻・record ID範囲・同一入力digestとJSON再直列化条件を記録する。
- 入力B：確認済みChat Completions形式と、要約のHost経路をローカルSSEで再生する。
  要約の実DB記録は今回の現行DBに見つからなかったため、通常で実受信したusageを同protocolへ与える機能確認と位置付ける。
- 親子・retry・会話再開はS-3の実利用確認で別に行い、容量比較の単純入力にすべてを混ぜない。

| 指標 | 方法・解釈 |
| --- | --- |
| 通常保存JSON | 保存中の`semantic_records.payload_json`の実UTF-8 bytes、usage部分、attribution部分、要約結果の参照先contents bytesを分けて集計。public展開JSONをDB保存量と混同しない |
| 論理本文量 | payload、contents（重複digestを一度）、latest wire/budget等を別項目で集計。共有本文・関連recordを二重に足さない |
| 論理DB割当 | dbstatが使える場合table/indexの割当、page_count/freelist_count/page_sizeを記録。UTF-8実bytesとは別に報告 |
| 物理DB/WAL | 同一filesystem・WAL/FULL条件で本体/-wal/-shmを同じ観測点で測る。稼働中、終了保存後接続保持中、全接続close後を区別し、auto-checkpointと最終closeの影響を記す。VACUUM・独自checkpointは行わない |
| 書込み量 | 同じ条件でprocess I/Oのwrite_bytes/wcharを可能な範囲で測る。終端保存・会話採用までを含む終了時値を取得できる方法を選び、取得できない区間は明記。SQLite専用I/OやSSD物理書込み量とは呼ばない |
| 時間・メモリ | 同じシナリオの時間・可能ならpeakを補助測定し、DB容量と混ぜず環境依存の観測値として示す |

物理計測はtmpfsだけで済ませず、常用repositoryと同じfilesystem上の別検証directoryを使う。
稼働WALのサイズは累積書込み量ではない。checkpoint済みDBへWALを単純加算した量を
論理本文量と同一視しない。SQLite専用／累積WAL I/Oを分離できなければ未計測と記す。
検証DBだけでintegrity/foreign keyと保存内容の読戻しを確認する。

### 現時点で期待できる削減と限界

対象113組の再直列化ベースでは：

- beforeのusage二重分：5,198,974 bytes。
- attribution除外後の片側集計usage：20,167 bytes。
- 差分候補：5,178,807 bytes（約4.94 MiB）。大半は二重分のattribution 5,155,250 bytes（約4.92 MiB）。

これはusage object部分の比較であり、JSON key/envelope/relations/page/indexの差分は含まない。
依頼メモの主要データ17,564,930 bytesに対する割合は約29%の規模だが、
全Session／全providerの削減率でも物理DBの縮小保証でもない。
二重保存解消の約2.48 MiBとattribution二重分の約4.92 MiBは重なるので加算しない。
既存DBは小さくならず、usage以外のMessage・thinking・wire等も残る。
大きなraw usageの受信・ModelResult／live観測の運搬コストまでは除去しない。
総書込み量の削減率は変更後の同一入力測定を待つ。

## 8. 未確認事項と利用者の判断

未確認のまま残す事項：外部独自hook/clientのattribution利用、未観測provider形式、
未観測providerの実受信と外部serviceでの新規動作。
本計画の実装後容量・I/O実測とローカル経路の実行結果は[結果](../results/usage-storage.md)で確認済み範囲を示す。
今回の現行DBには`name=context.model.generate`のeffect観測が見つからず、
要約usageの実受信・保存実績はsource/既存test以上には実証していない。
詳細を使う標準機能がないことと、外部利用者がいないことは同義ではない。

利用者が計画確認・独立レビュー後に実装を指示した採用範囲：

1. 通常履歴は**集計値のみ、通常呼出しは既存usage観測に一度保存**する。
2. 新規保存分のattributionはDBから後日取得できなくなり、
   専用collector追加は別テーマのままとする（Goの既存取得口と実行時rawは残す）。
3. 上記スライスと、実providerなし・使い捨てDB／ローカルHTTPでの確認計画で実装する。

旧DBの処理や常用環境の配置は今回の承認に含めず、必要になった時点で別途確認する。
