# 文書と現行実装の照合結果

照合日: 2026-10-16。対象revision: `0884313`（`Document QuickJS tooling and decision model exploration`）。
利用者の依頼に基づく調査結果。初回照合ではコード・既存文書を変更せず、本報告だけを追加した。
その後、利用者指示で[docs直下の実装状況一覧](../implementation-status.md)を追加し、元文書の古い現在形と
後続置換の案内を修正した。以下の文書上の不一致・行番号は**修正前の照合時点の記録**であり、
現在もすべての誤記が残っているという意味ではない。現在の状態の入口は実装状況一覧と各設計文書を参照する。

## 対象と判定方法

対象は、調査開始時点の `docs/notes/` **7文書**、`docs/design/` **31文書**、
`docs/plans/` **43文書**、`docs/results/` **70文書**、計 **151 Markdown文書**。
同主題の設計・計画・結果を同じ行にまとめた箇所も、対象ファイル名をすべて列挙する。
本報告自体はこの151件に含めない。関連するコード、標準配布設定、workspace hook、既存結果も参照した。
関連JSONは測定記録の参照先として扱い、個々の数値の再計算・原証跡の全面再検証はしていない。
`docs/references/`・`docs/normal-use-inbox.md`・隣接Henjiの機能を、hjhの必須仕様へ繰り上げていない。

- **実装済み**: 現行sourceで利用者入口→処理→保存／参照／表示の接続を確認できる。
  今回、実providerや常用環境で全機能の通し操作を再実行した、という意味ではない。
- **一部／未実装**: 一つの文書に、実装済みの基礎機能と未実装の改善候補が混在する場合を含む。
- **将来案／検討**: 採用・方式・実装時期が未確定。実装されていないことを製品不具合とはしない。
- **置換済み／廃止済み**: 過去に成立した機能・方式が後続決定で変更されたもの。未実装と区別する。
- **調査／review記録**: その時点の調査・計画確認・測定であり、機能実装や現在の性能保証ではない。
- **未確認**: 実装はあるが、現行binaryでの実provider・物理端末・運用効果などの確認が残る。

過去のresultsに記載されたCLI/Core/PTY・実provider確認は**既存の実行記録**として参照した。
テストの存在や「完了」の文言だけで実装済みと判定せず、現行sourceの呼出し経路を追った。
今回は実provider呼出し、常用Core操作、DB変更、build/test/full gateを実施していない。
sourceの行番号は照合時点のもの。

## 結論

**主要な採用済み機能は現行コードに接続されている。今回の範囲では、採用済み主機能の入口が欠けると断定できる実装漏れは確認しなかった。**
ただし、これは全機能の実運用完了宣言ではない。文書に書かれた将来案まで実装済みでもない。
主な差分は、未実装の改善候補、古い現在形の記述、後続置換前の設計・測定記録である。

### 実装済みとして整理できる主なもの

- CLI／独立Core／独立TUI、HTTP+JSON+SSE、保存会話の再開、Session管理・削除。
- 外部role・tool・hook、生成JS、子agentの同期／非同期実行・報告・追加指示・回収。
- provider／model／effort選択、認証UI、favorites、workspaceの記憶selection。
- AGENTS・skills・base指示・tool利用方針の読込みと各toolの共通help。
- Web読取り・Exa検索、各モデル送信前のcontext予算と履歴／現在turnの送信選択。
- opt-in JSコンパクション、ctx表示、フッター再配置、入力欄上下余白・通知寿命整理。
- schema v4、WAL、通常履歴整理、終了時snapshot、索引整理、usage単一保存、assistant/thinkingの参照化（S-3まで）。

### 未実装／検討中として分離するもの

| 項目 | 現状・境界 | 文書／現行sourceの根拠 |
| --- | --- | --- |
| 意思決定モデル（Jev／Decisions API）のhjh統合 | **未実装・導入方式未決**。委譲・model選択・検索選別等は活用案。隣接Henjiの実装済みCLIとhjh統合は別。 | `docs/notes/decision-model-uses.md:3-5,41-64`。`cmd/hjh/main.go`・`internal/`に専用decision入口／provider実装を確認できない。既存JSやcommandで外部CLIを組み合わせられることは統合済みを意味しない。 |
| QuickJS stdlib有効化とtool実装のJS化 | **未実装・移行計画未作成**。go-quickjsへのengine移行自体は実装済み。stdlib利用への置換とは別。 | `docs/notes/quickjs-stdlib-tools.md:3-13,40-54`。`internal/jsruntime/runtime.go:111-118`は`quickjs.New()`と独自bindingを使用。`internal/jsruntime/bootstrap.js:36-55`→`internal/host/host.go:94-180`が基礎I/Oを担当。 |
| Markdown／非Markdownテーマの独立差替え | **未実装・将来候補**。現在のSolarized Darkと固定配色は実装済み。 | `docs/design/tui-rendering.md:58-67`。`internal/tui/markdown_renderer.go:19-26,99-119`、`internal/tui/theme.go:14-23`は固定スタイル。 |
| コンパクション後の直近原文をtoken量で保持する改善 | **検討段階**。既存selectorの予算処理や`rawTargetRatio`と、巨大な完了turnの末尾原文を残す新方針は別。 | `docs/design/js-compaction.md:95-104`。`examples/hooks/compaction.js:3-7,103-119`は既存turn数と交換単位の選択。 |
| Core再起動／別CLIでも要約状態を永続復元 | **未実装・保存案の検討**。同一Core内の次turn／TUI再接続では維持する。 | `docs/design/js-compaction.md:72-88,105-119`。`internal/app/context_memory.go:8-40,60-71`はService内map。`sessions.checkpoint_json`はSession作成以外の使用経路がない。 |
| 原文検索・読戻し専用の拡張 | **別候補・未採用**。CLI history／inspectでID・範囲を読む既存機能はあるが、FTS等の新経路とは別。 | `docs/design/js-compaction.md:123-131`。10turn上限も未確定。 |
| agent全体のDB非保存mode | **未実装・将来検討**。要約専用の一回生成で送受信全文を保存しない限定経路は既存。 | `docs/design/agent-storage-modes.md:3-12`。`internal/app/run.go:108-145`は通常保存、`internal/app/request_model.go:23-40`は補助生成。 |
| Coreからコンテナ／OS VMへのagent配置 | **未実装・将来構想**。現在は同じGo process内の実行とWorkerごとのJS VM。 | `docs/design/future-agent-environments.md:3-19`、`internal/app/delegation.go:113-116`。 |
| 追加retryを2回へ増加（最大3試行、2秒→4秒） | **未実装・改善案**。現行は追加1回・固定2秒。応答待ちtimeoutは別途実装済み。 | `docs/plans/provider-single-retry.md:61-79`、`internal/agent/retry.go:11,61-63,78-83`。 |
| hook文脈の追加位置指定API | **未実装・方式未決**。標準の日時注入は停止済み。既存runtimeStart／beforeTurnの文脈追加はある。 | `docs/design/extensions.md:328-337`、`internal/app/lifecycle.go:73-104`。 |
| 精密usage collector／調査用記録基盤 | **別テーマ・未実装**。通常保存のattribution除去と集計値保持は実装済み。 | `docs/design/history-schema.md:211-218`、`docs/plans/usage-storage.md:226`以降、`internal/app/usage_storage.go:11-47`。 |
| 共有本文contentsの全面廃止・保存圧縮 | **未実装／未採用**。steering受付の独立保存、不要本文回収、source自動archive停止は実装済み。 | `docs/design/history-schema.md:305-342,449-470`、`internal/history/schema.sql`、`internal/history/session_delete.go:15-138`、`internal/app/execution.go:240-260`。 |

### 古い現在形・後続置換を区別する必要がある箇所

| 文書の記述 | 現在の実装／読み替え |
| --- | --- |
| `docs/design/initial-product.md:121-124`「現在まだ同一process」 | **明確に古い現在形**。独立`hjh-tui`→HTTP client→別processの`hjh serve`。`cmd/hjh-tui/main.go:46-55`、`internal/corelaunch/corelaunch.go:122-128`。 |
| `docs/design/i10-context-recall-contract.md:3-5`「機能はまだ未実装」 | **明確に古い現在形**。`/context`・`/recall`→HTTP→prepare/admission→参考入力→次requestまで実装済み。`internal/tui/commands.go:107-110`、`internal/corerun/recall.go:242-291`、`internal/app/execution.go:263-296`。 |
| `docs/design/tui.md:77`の3行footer／context表示計画のモデル行右端ctx | 現行は**区切り線に状態・Session・ctx**、入力欄下は**workspace＋接続の統合1行**。`docs/design/tui-footer.md:6-22`、`internal/tui/footer.go:70,207,260`。 |
| `docs/plans/i06-conversation-tui.md:50`のI-12「実装未着手」 | 後続認証UI実装済み。`internal/tui/connections.go:43`、`internal/tui/auth.go:25,398,476`。段階表が過去状態。 |
| `docs/notes/http-response-context-overflow.md:44-45`の送信予算・turn内選択「未実装」 | I-14以降で実装済み。`internal/agent/loop.go:104-108`→`internal/app/execution.go:327-380`。生`http`は今も全文取得なので「巨大HTTPが必ず解決」は意味しない。 |
| `docs/design/provider-sdk-evaluation.md`のretry・timeout・型付きerror不足 | **評価当時の調査記録**。分類・追加1回retry・開始300秒／idle600秒timeoutを後続実装。SDK本体は未採用。`internal/contracts/failure.go`、`internal/agent/retry.go`、`internal/provider/protocol/response_wait.go`。 |
| I-14／I-17のweb_fetch 1 MiB、I-22の自由なcontents・応答JSON解析所有者 | 後続defaultsで通常**64 KiB**、検索**10件・抜粋1,000文字・text:false**、本文はweb_fetch。解析はHostへ。`internal/webfetch/webfetch.go:18-20`、`internal/extensions/assets/web-search.js:11-25`、`internal/host/web_search.go:24-63`。 |
| `docs/design/i14-web-context-contract.md:83-98`、I-10等の各request全文／精密出典archive | **保存契約は置換済み**。毎requestの小metadata／予算summaryと、execution最新wire／完全Factの終了時保存に分離。`internal/app/context.go:33-66`、`internal/app/context_budget_record.go:17-49`、`internal/app/snapshots.go`。 |
| schema議論の「本文・構成を回収しない」「source archive停止未実装」 | 同文書末尾の追記で解決済み。`docs/design/history-schema.md:449-470`、`internal/history/session_delete.go:28-57,105-138`。過去の議論は残っているが現在の未実装一覧に含めない。 |
| `docs/results/cli-session-delete.md:3-4`のTUI／Core API後回し | 現在はTUI picker削除→DELETE API→Core→Storeまである。`internal/tui/session_management.go:223-265`、`internal/httpapi/server.go:374-382`。 |
| `docs/results/henji-feature-comparison.md:87`等の標準日時注入 | 標準hookから停止済み。hook機構は維持。`internal/extensions/assets/hooks.js:1-7`、`docs/design/extensions.md:328-337`。 |
| `docs/design/run-api.md:23-27,117`のselection／credential／login操作 | 現在はworkspace記憶selection、ユーザーconfig配下credential、attempt認証API。`internal/app/selection.go:93-121`、`internal/credentials/manager.go:181-217`、`internal/app/authentication.go:192-217`。 |

過去のplan/resultやreviewを一律に最新仕様へ書き換える必要がある、という結論ではない。
特に優先して整理すべきなのは、**現行設計の現在形と実装が食い違う箇所**。
一つの文書内で旧状態と追記が併存するものは、末尾の決定と現行の保存契約を優先する。

## 文書別照合一覧

### notes（7文書）

| 対象文書 | 状態／コード根拠／留保 |
| --- | --- |
| `docs/notes/decision-model-uses.md` | **検討のみ・hjh統合未実装**。隣接Henjiの実装済みという記述とhjh活用候補を明確に分離。専用入口／adapterなし。 |
| `docs/notes/quickjs-stdlib-tools.md` | **検討のみ・移行計画未作成**。engineはgo-quickjs v0.25.2だが、実I/Oは独自binding→Go Host。`internal/jsruntime/runtime.go:111-118`、`internal/jsruntime/bootstrap.js:36-55`。 |
| `docs/notes/http-response-context-overflow.md` | **原因調査記録＋後続で一部対応済み**。web_fetchと送信予算・選択を実装。生httpは`internal/host/host.go:297-298`で全文取得のまま。保護対象の巨大結果が必ず収まる保証や元Sessionの再実行確認はない。 |
| `docs/notes/provider-cache-affinity.md` | **記載のID分離・ヘッダー機構は実装済み**。rootはSession ID、子はexecution ID。`internal/app/run.go:123-124`、`internal/app/delegation.go:78-103`、`internal/app/connections.go:126`。ChatGPTの実測をOpenRouter／DeepSeekの効果へ一般化しない。 |
| `docs/notes/provider-response-timeout-task.md` | **依頼記録・後続実装済み**。開始300秒／read idle600秒、親子共通、既存一回retry接続。`internal/provider/protocol/response_wait.go:50-99`、`internal/app/connections.go:100-128`。元の「まだ実装しない」は当時の作業範囲。 |
| `docs/notes/tool-usage-guidance-task.md` | **依頼記録・後続方式で実装済み**。方針外部化＋各toolの`{help:true}`を採用し、独立した名前／keyword検索辞書は設けない。これは未完了ではなく後続設計の決定。`docs/design/tool-usage-guidance.md:3-5`、`internal/extensions/tool_usage_policy.go:33-94`、`internal/jsruntime/bootstrap.js:125-135`。 |
| `docs/notes/usage-storage-task.md` | **依頼記録・後続実装済み**。usage観測へ単一保存・直下attribution除去、補助生成の保存projectionも接続。`internal/app/observation_storage.go:12-32`、`internal/app/usage_storage.go:11-47`。既存DB変換・精密collectorは対象外。 |


### UI分野（33文書）

同題名のdesign／plan／resultは同じ行へ列挙しています。

| 対象文書 | status／現行コード根拠／留保 |
|---|---|
| `docs/design/i06-conversation-contract.md`、`docs/plans/i06-conversation-tui.md`、`docs/results/i06-conversation-tui.md` | **主要会話経路は実装済み・一部後続置換**。保存`internal/app/journal.go:56`・`:90`、最新state`internal/history/conversation_write.go:630`、stable ID`internal/conversation/view.go:614`、SSE`internal/httpapi/server.go:279`、TUI snapshot／upsert`internal/tui/model.go:992`。実装前・v3・原文描画・段階表は現状説明に使えない。過去性能値の現在性能への適用は未確認。 |
| `docs/design/i11-model-favorites-contract.md`、`docs/plans/i11-model-favorites.md`、`docs/results/i11-model-favorites.md` | **実装済み**。Tab`internal/tui/connection_picker.go:491`→希望値PUT`internal/tui/model_favorites.go:147`→FIFO内scope解決`internal/app/model_favorites.go:35`→flock／JSON`internal/modelfavorites/store.go:193`→成功後の印・選択ID復元`internal/tui/model_favorites.go:272`。過去の実catalog／再起動確認は既存結果記録。 |
| `docs/design/inline-pickers.md`、`docs/results/inline-pickers.md` | **実装済み**。単行共通描画`internal/tui/inline_menu.go:16`→dock`internal/tui/conversation.go:229`、Del確認`internal/tui/session_management.go:223`→API`internal/httpapi/server.go:377`→予約`internal/corerun/management.go:70`→transaction削除`internal/history/session_delete.go:15`。削除成功時の新規会話復帰も同`session_management.go:266`に存在。 |
| `docs/design/slash-command-menu.md`、`docs/results/slash-command-menu.md` | **実装済み・候補数は後続変更**。共通catalog／検索／Tab・Enter・Esc`internal/tui/slash_commands.go:18`・`:53`・`:117`、dispatch`internal/tui/commands.go:18`。結果の24commandは当時の数で、現行catalogは22。候補はDB保存でなくdock表示。 |
| `docs/design/tui-footer.md`、`docs/results/tui-footer.md` | **最新配置は実装済み、初期3行形式は置換済み**。`internal/tui/footer.go:70`・`:207`・`:260`、入力上下余白`internal/tui/conversation.go:184`、下部固定／resize CPR`internal/tui/renderer/writer.go:129`。最新結果のPTYはfake Backendであり実provider・物理端末確認ではない。 |
| `docs/design/tui-header.md`、`docs/results/tui-header.md` | **実装済み**。保存metadata`internal/tui/model.go:537`→header`internal/tui/header.go:20`→一度だけ確定出力`internal/tui/model.go:1311`。初回実端末サイズ取得`internal/tui/tui.go:122`、orientation後の本文同時commit`internal/tui/conversation.go:248`。現在Session情報はfooter区切り線へ後続移動。 |
| `docs/design/tui-rendering.md`、`docs/results/tui-rendering.md` | **採用部分は実装済み／テーマ差替えは将来候補・未実装**。assistantのみGlamour`internal/tui/transcript.go:110`、Solarized／全行背景`internal/tui/markdown_renderer.go:19`・`:56`・`:99`、空assistant省略`internal/tui/child_display.go:29`。将来テーマ設定は実装漏れ扱いしない。 |
| `docs/design/tui.md` | **主要統合契約は実装済み・一部記述更新漏れ**。公開Backend境界`internal/tui/tui.go:22`、入力・実行`internal/tui/model.go:726`・`:893`、merge・描画`internal/tui/conversation.go:15`・`:211`。`:77`の3行footerは旧記述。別詳細browserは将来扱いで、現行`/tool`は末尾出力（`internal/tui/commands.go:147`）。 |
| `docs/plans/context-usage-display.md`、`docs/results/context-usage-display.md` | **計測・配信・復元・表示は実装済み／配置は置換済み**。projection通知`internal/app/execution.go:374`→RunState`internal/corerun/manager.go:249`→TUI`internal/tui/model.go:969`→表示`internal/tui/context_usage.go:13`、Session seed`internal/tui/model.go:1385`。現在ctxは区切り線。推定値でありprovider実測usage表示ではない。 |
| `docs/plans/first-tui.md`、`docs/results/first-tui.md` | **初版の実装・実行記録／方式・操作は後続置換済み**。現在入口`cmd/hjh-tui/main.go:28`→`tui.Run`、HTTP Backend`internal/tui/tui.go:22`。同一process、viewport scroll、Esc停止、Ctrl+C終了を現行仕様と読まない。 |
| `docs/plans/i05-core-tui-separation.md`、`docs/results/i05-core-tui-separation.md` | **分離機能は実装済み／v1・旧配信・旧終了操作等は後続置換**。独立client`cmd/hjh-tui/main.go:47`、DTO Backend`internal/tui/tui.go:22`、v2`internal/httpapi/server.go:20`。バイナリサイズ・依存除外の実測は当時の記録で今回再測定なし。 |
| `docs/results/i06-plan-review.md` | **計画review記録、後続契約・実装あり**。現在のcut＋購読`internal/corerun/conversation.go:42`、保存後公開`internal/executiondata/owner.go:32`へ接続済み。review成功それ自体は製品完了の根拠にしていない。 |
| `docs/plans/i07-session-management.md`、`docs/results/i07-session-management.md` | **実装済み／picker外観・削除は後続拡張**。一覧`internal/tui/session_management.go:79`、rename`同:107`→titleのみ更新`internal/history/session_metadata.go:11`、active捕捉後view読取`internal/tui/model.go:1369`→GET attach`internal/tui/session_management.go:126`、detach／停止`同:166`・`:195`。過去の「削除対象外」は現在未実装を意味しない。 |
| `docs/plans/i08-connection-selection.md`、`docs/results/i08-connection-selection.md` | **実装済み・複数の後続置換あり**。Core preview→実効auth catalog`internal/tui/connection_picker.go:71`・`:134`、世代照合`同:155`、次回setter`同:577`。effort自由指定・複数形一覧・成功runまで保存しない説明・新Session優先順は旧仕様。 |
| `docs/results/i11-plan-contract-review.md` | **計画・契約review記録／「未実装」は後続解消**。現行store FIFO`internal/modelfavorites/store.go:97`、保存`同:193`、TUI確認後反映`internal/tui/model_favorites.go:272`。このreview記録自体は実操作確認ではない。 |
| `docs/plans/tui-notice-lifecycle.md`、`docs/results/tui-notice-lifecycle.md` | **実装済み**。Session取得errorの単一保持・成功clear`internal/tui/runtime_input.go:271`、run表示対象`internal/tui/run_notice.go:11`、追加入力通知`internal/tui/runtime_input.go:164`、取消通知と制御維持`同:439`、footer`internal/tui/footer.go:138`。既存端末確認は制御HTTPであり常用Coreの目視受入ではない。 |
| `docs/results/s06-tui-core.md` | **調査・spike記録／旧方式は後続置換**。累積永久保存・全Inspect復元・標準rendererの記述は現行ではなく、最新state`internal/history/conversation_write.go:630`、cut復元`internal/corerun/conversation.go:42`、独立writer`internal/tui/renderer/writer.go:110`へ置換。過去の未解決resizeを現在の既知不具合とは断定しない。 |
| `docs/results/tui-tool-nesting.md` | **実装済み**。`ParentEntityID`から深さ算出`internal/tui/tool_nesting.go:11`→二文字字下げ・↳`同:26`、通常確定描画`internal/tui/transcript.go:228`と全体描画`internal/tui/conversation.go:62`で共用。子agent詳細省略は別の既存表示方針。 |


### 履歴・保存分野（26文書）

同主題のdesign/plan/resultは同一行に列挙しています。全26文書を含みます。

| 対象文書 | status | 現行コード根拠 | 留保 |
|---|---|---|---|
| `docs/design/agent-storage-modes.md` | **未実装・将来検討** | 通常rootは必ずSession/構成/execution保存へ進む：`internal/app/run.go:108–145`。要約専用呼出しは別経路：`internal/app/request_model.go:22–39` | agent全体の非保存modeは設計未確定。要約送受信全文を保存しない限定実装と混同しない。 |
| `docs/design/history-payloads.md` | **実装済み＋旧契約の調査・履歴記録** | 保存projection：`internal/app/observation_storage.go:12–65`。Message/result復元：`internal/history/read.go:22–130,222–252` | v3・旧wire GC等は冒頭の優先関係と後続節で読み替える。 |
| `docs/design/history-schema.md` | **現行v4実装済み＋旧v3採用・容量調査記録** | version/Open：`internal/history/store.go:26,48–100`。現行DDL：`internal/history/schema.sql:1–247` | 現行DDLは23 tables・9明示索引。v3固定DDLは履歴資料であり、migration未提供は明示方針。圧縮・共有本文全面廃止は採用済み機能に数えない。 |
| `docs/design/normal-history-storage.md`<br>`docs/plans/normal-history-storage.md`<br>`docs/results/normal-history-storage.md` | **実装済み／初期のwire所有・予算粒度は後続置換** | request metadata：`internal/app/context.go:32–65`。定義除去：`internal/app/journal.go:507–518`。tool result/text参照：`internal/history/tool_result_codec.go:15–90`、`internal/history/tool_message_codec.go:15–103` | designが現行契約。初期plan/resultの毎回wire GC・完全予算履歴は現行要件ではない。 |
| `docs/design/session-ids.md` | **実装済み** | 共通短縮・一意prefix：`internal/sessionid/sessionid.go:9–49`。CLI入力：`cmd/hjh/main.go:250–264`。TUI resume：`internal/tui/session_management.go:43–69` | 短縮は人向け。API/DB/保存本文は完全IDを保持。 |
| `docs/plans/assistant-thinking-storage.md`<br>`docs/results/assistant-thinking-storage.md` | **S-1/S-2/S-3実装済み＋調査・測定記録** | mirror/thinking：`internal/history/assistant_mirror_codec.go:11–89`。result所有：`internal/history/conversation_write.go:461–473`。完全request別handoff：`internal/app/journal.go:129–148,444–459`。walk：`internal/history/walk.go:270–317` | 第一段の保留はS-3続報で更新。terminal/runtimeOutcome/afterTurn整理は別候補。実provider・常用有効化は今回未確認。 |
| `docs/plans/grep-paged-results-and-v4-cutover.md`<br>`docs/results/grep-paged-results-and-v4-cutover.md` | **grep実装済み／常用切替は既存の実行記録** | 文字列glob：`internal/extensions/assets/inspect.js:29,41`。Host paging：`internal/host/grep.go:71–110`。v4/WAL：`internal/history/store.go:26,61–84` | backup・バイナリ配置・切替時のCore/TUI確認は結果文書の記録。現在の稼働環境は再照合していない。 |
| `docs/plans/history-redundant-indexes.md`<br>`docs/results/history-redundant-indexes.md` | **実装済み** | 既存writableだけ2索引DROP：`internal/history/store.go:78–84`。制約索引維持：`internal/history/schema.sql:85,137` | read-onlyは削除しない。過去の削減率は同一結果を反復した測定条件に限定。 |
| `docs/plans/history-snapshot-overwrite.md`<br>`docs/results/history-snapshot-overwrite.md` | **専用表・summary粒度は実装済み／毎回保存は後続置換** | 専用表：`internal/history/schema.sql:140–151`。matching record/body JOIN：`internal/history/read.go:167–175`。現行保存：`internal/history/snapshots.go:63–111` | locatorは現在、専用行の存在ではなくlogical metadataから公開する。終了時保存が現在方式。 |
| `docs/plans/history-terminal-snapshots.md`<br>`docs/results/history-terminal-snapshots.md` | **実装済み** | 終了時保存：`internal/app/execution.go:204–228`。owner保存・解放：`internal/app/snapshots.go:259–337`。live合成：`internal/app/snapshot_read.go:10–47`。再保存API：`internal/httpapi/snapshot_save.go:10–28`。Close再試行：`internal/app/service.go:338–345` | 再保存はCore API。新CLI/slash commandは対象外。突然消失時の未保存snapshot喪失は合意済み境界。 |
| `docs/plans/history-wal-first-sequence.md`<br>`docs/results/history-wal-first-sequence.md` | **実装済み** | schema確認後WAL/FULL：`internal/history/store.go:61–75,118–125`。成功後初出記憶：`internal/app/journal.go:74–75`。map受渡し：同`:449–452`。直接StoreのSQL経路：`internal/history/conversation_write.go:209–216` | 独自checkpoint/backup機能の実装ではない。物理device IO・長期改善率は別。 |
| `docs/plans/tool-output-storage.md`<br>`docs/results/tool-output-storage.md` | **実装済み** | Git paging：`internal/host/git_inspect.go:26–65`。編集一括処理：`internal/host/edit_file.go:31–51`。Web返却/観測分離：`internal/host/web_search.go:11–21,24–62`、`internal/jsruntime/host.go:31–43` | 内側toolの明示的結果はrun_jsで絞ってreturnしても保存対象。JS変数のsnapshot保存ではない。 |
| `docs/plans/usage-storage.md`<br>`docs/results/usage-storage.md` | **実装済み／配置・有効化は別判定** | usage直下attribution除去・model_result重複除去：`internal/app/observation_storage.go:12–32`、`internal/app/usage_storage.go:11–47`。元live観測：`internal/app/journal.go:47–83` | 精密collectorは別テーマ。結果末尾は「binary配置済み・Core再起動待ち」：`docs/results/usage-storage.md:220–236`。今回、現在の有効化状態は確認していない。 |
| `docs/results/cli-session-delete.md` | **CLI実装済み／TUI・Core保留は後続実装で更新** | CLI：`cmd/hjh/sessions.go:63–105`。transaction・候補限定回収：`internal/history/session_delete.go:15–138` | TUI/APIも現在存在。VACUUMしない・別Sessionの所有本文を保持する処理は残る。 |
| `docs/results/grep-native-backends.md` | **backend選択実装済み／JS paging責務は置換済み** | rg優先/GNU代替・Worker内保持：`internal/extensions/assets/inspect.js:13–19,50–62`。Go解析：`internal/host/grep.go:94–110` | backend差は現行公開結果にも残る。旧「JSがpaging所有」は現在に適用しない。 |
| `docs/results/short-session-ids.md` | **実装済み＋既存の実利用記録** | 短縮/解決：`internal/sessionid/sessionid.go:9–49`。API入力解決：`internal/apiclient/session_reference.go:13–29`。一覧/JSON分離：`cmd/hjh/sessions.go:85–104` | 記載の実provider・PTY確認は当時の実行記録。今回の短縮ID判定はsource照合による。 |


### Runtime・Provider・Web分野（27文書）

同主題のdesign／plan／resultは同一行に列挙しています。

| 対象文書 | 現在のstatus／コード根拠 | 留保・既存実行記録との区別 |
|---|---|---|
| `docs/design/i14-web-context-contract.md`<br>`docs/plans/i14-web-context.md`<br>`docs/results/i14-web-context.md` | **主要機能実装済み／上限・保存契約は後続置換**。`internal/app/execution.go:302-389`、`internal/contextbudget/projection.go:117-173,289-416`、`internal/webfetch/webfetch.go:18-130`、`internal/tui/context_display.go:19-113`。 | resultのWeb作業・再開・TUI確認は過去記録。毎requestの精密archiveは現行仕様ではない。 |
| `docs/design/js-compaction.md`<br>`docs/plans/js-compaction.md`<br>`docs/results/js-compaction.md` | **opt-in要約は実装済み／改善メモは未実装・検討段階**。`examples/hooks/compaction.js:70-138`、`internal/app/request_model.go:23-40`、`internal/app/context_memory.go:8-80`、`internal/app/execution.go:224-230`。 | resultに実provider・Core再起動後の再要約記録あり。永続復元、トークン量による直近原文保持改善、検索・読戻し新toolは未採用／検討を含む別範囲。 |
| `docs/design/provider-sdk-evaluation.md` | **調査記録・非採用提案**。現在も手書きHTTP／JSON／SSE：`internal/provider/protocol/protocol.go:86-103`、`internal/provider/protocol/generate.go:44-111`、`go.mod:5-17`。 | retry／timeout／分類不足は後続実装で解消した部分あり。SDK、画像、未観測互換variant等を実装漏れとしない。 |
| `docs/design/providers.md` | **主要契約実装済み**。選択解決：`internal/providers/registry.go:137-179`、`internal/app/selection.go:91-122`；接続：`internal/app/connections.go:95-140`；wire：`internal/provider/protocol/encode.go:54-202`。 | 認証・picker等を含む全操作の今回の再実行なし。現在のアカウントcatalogや外部provider応答は未照会。 |
| `docs/design/request-context.md`<br>`docs/plans/request-context-selection.md`<br>`docs/results/request-context-selection.md` | **API実装済み**。`internal/app/execution.go:327-348`、`internal/app/request_context.go:56-160`、`internal/app/request_context_selection.go:35-83`、`internal/contextbudget/preview.go:23-48`。 | 履歴込み計測は全DB総和ではなく最初のoverflowまで。resultにはcompiled CLI/Core・実provider再観測記録あり。要約は別の後続機能。 |
| `docs/design/web-search.md`<br>`docs/plans/i22-exa-web-search.md`<br>`docs/results/i22-exa-web-search.md` | **現行検索実装済み／初期仕様の一部は置換済み**。`internal/extensions/assets/config.json:555-578`、`internal/extensions/assets/web-search.js:7-27`、`internal/host/web_search.go:24-63`、`internal/app/tool_requests.go:15-58`。 | I-22 resultには直接／run_js／子／TUI、任意引数修正後の実provider記録あり。現行固定contentsでの外部Exa再実行は今回なし。 |
| `docs/plans/failure-classification-recall.md`<br>`docs/results/failure-classification-recall.md` | **分類・保存・短い表示は実装済み／HTTP案内条件は後続変更**。`internal/contracts/failure.go:8-75`、`internal/provider/protocol/stream.go:698-727`、`internal/app/execution.go:214-235`、`internal/tui/failure.go:5-55`、`internal/tui/child_display.go:15-82`。 | resultのlocal HTTP／実Core／PTY／recall成功は過去記録。元MiMo障害原因の確定ではない。 |
| `docs/plans/i17-go-quickjs.md`<br>`docs/results/i17-go-quickjs.md` | **engine移行・終了処理は実装済み**。`go.mod:12`、`internal/jsruntime/worker.go:257-296`、`internal/jsruntime/runtime.go:111-133,180-195`、`internal/jsruntime/call.go:38-123,138-240`。 | 実モデル・展開配布物確認はresultの過去記録。現在distのhash／稼働Coreのengineは今回未確認。旧modernc spikeは過去資料であり移行漏れではない。 |
| `docs/plans/provider-response-timeout.md`<br>`docs/results/provider-response-timeout.md` | **実装済み**。設定接続：`internal/app/connections.go:100-128`；時計：`internal/provider/protocol/response_wait.go:50-140`；HTTP接続：`internal/provider/protocol/generate.go:44-90,113-130`。 | resultにlocal HTTP・Core／PTY・実時間300秒確認。実providerでの改善効果・正常長考への影響は未実証。初回credential準備は時計外。 |
| `docs/plans/provider-single-retry.md`<br>`docs/results/provider-single-retry.md` | **追加1回・固定2秒は実装済み／追加2回案は検討記録**。`internal/agent/retry.go:11-105`、`internal/agent/loop.go:107-143`、`internal/app/journal.go:119-159`、`internal/tui/child_display.go:15-20`、`internal/recall/projection.go:228`。 | resultはlocal HTTP／Core／PTYの過去記録。最大3試行案は採用済み仕様と扱わない。timeout不足メモは後続実装で置換。 |
| `docs/plans/web-retrieval-defaults.md`<br>`docs/results/web-retrieval-defaults.md` | **実装済み**。`internal/extensions/assets/web-search.js:19-24`、`internal/webfetch/webfetch.go:18-20,80-104,160-169`、`internal/extensions/assets/config.json:555-578`。 | resultにlocal検索・Core／CLI・全体保存・常用反映記録。検索応答全体のbyte上限を追加したものではない。 |
| `docs/results/context-metrics-live.md` | **調査記録／current-only既定は置換済み**。現在は`internal/app/request_context_selection.go:43-59,67-83`。 | 当時の690対3,086 tokensは旧計測の観測値。現在の不足として転記しない。 |
| `docs/results/direct-cache-patterns.md` | **調査記録＋session header組込み済み**。標準設定：`internal/extensions/assets/config.json:120`；送信：`internal/provider/protocol/generate.go:56-58`；root：`internal/app/run.go:123-124`；子：`internal/app/delegation.go:78-103`。 | 実HTTP／実Core／親子でのcache hitは過去測定。比率の恒常保証、別providerへの一般化は不可。 |
| `docs/results/sdk-cache-probe.md` | **独立probeの調査記録／本体SDK採用ではない**。本体は`go.mod:5-17`、`internal/provider/protocol/generate.go:44-61`；probeは`spikes/cache-probe/`に分離。 | 両経路cache 0は当時の条件での結果。後続`direct-cache-patterns`でsession header条件を確認済み。「SDK不使用が原因」とは結論していない。 |


### Core・拡張・認証分野（58文書）

凡例：**実装済み**＝現行product経路に接続、**置換あり**＝主機能は存続するが旧方式を後続変更、**調査記録**＝実装完了を表さない、**将来案**＝現行実装対象ではない。

#### design：12文書

| 対象文書 | 状態／現行コード根拠／留保 |
|---|---|
| `docs/design/base-instructions.md` | **実装済み**。`internal/extensions/base_instruction.go:23-43`、`internal/app/execution.go:240-246`。親子共通、出典表示は `internal/tui/orientation.go:164-187`。 |
| `docs/design/extensions.md` | **実装済み**。capture／選択は `internal/extensions/extensions.go:257-364`、実行は `internal/jsruntime/bootstrap.js:120-172`。現行追補が重要。 |
| `docs/design/future-agent-environments.md` | **将来案・未実装**。現行は `internal/app/delegation.go:115-116` の同Go process実行。コンテナ／OS VM配置は採用済み漏れではない。 |
| `docs/design/http-api.md` | **実装済み**。`internal/httpapi/server.go:163-254,374-441`、`internal/corerun/manager.go:125-208`。HTTP/SSE・保存完了後terminalに接続。 |
| `docs/design/i09-runtime-input-contract.md` | **実装済み**。`internal/corerun/runtime_input_admission.go:60,200`、`internal/agent/loop.go:101-106,155-170`。本文独立保存の追補は現行と一致。 |
| `docs/design/i10-context-recall-contract.md` | **実装済み・保存方式に置換あり**。`internal/corerun/recall.go:217-291`、`internal/app/execution.go:263-296`。冒頭「未実装」と旧request保存説明が古い。 |
| `docs/design/i12-authentication-contract.md` | **実装済み・実確認は一部**。`internal/credentials/auth.go:105-157,250-284`、`internal/credentials/attempt.go:393-468`。複数実accountは未確認。 |
| `docs/design/initial-product.md` | **初期範囲は実装済み・後続置換あり**。`internal/app/run.go:58-180`、`internal/app/execution.go:335-389`。末尾同一process説明は古い。 |
| `docs/design/latest-session-resume.md` | **実装済み**。`internal/tui/latest_session.go:20-43` → `internal/tui/model.go:1369-1393`。復元／attachであり自動生成開始ではない。 |
| `docs/design/run-api.md` | **実装済み・一部説明が古い**。`cmd/hjh/main.go:114-142,244-270`。selection／認証／login API名は上記1-7参照。 |
| `docs/design/tool-usage-guidance.md` | **実装済み・実provider受理未確認**。`internal/extensions/tool_usage_policy.go:33-94`、`internal/jsruntime/bootstrap.js:125-135`。モデル選択改善は未実証。 |
| `docs/design/workspace-instructions-skills.md` | **実装済み**。`internal/workspacecontext/context.go:75-95,134-195`、`internal/app/workspace_context.go:15-33`。skill本文は利用時read。 |

#### plans：17文書

| 対象文書 | 状態／現行コード根拠／留保 |
|---|---|
| `docs/plans/i02-child-agents.md` | **実装済み・後続拡張**。`internal/app/delegation.go:29-42,98-135`、`internal/jsruntime/call.go:76-89`。taskのみ／親model固定は旧段階。 |
| `docs/plans/i03-generic-roles.md` | **実装済み・置換あり**。`internal/extensions/extensions.go:302-364,511-585`。子model/tools指定、source保存は後続変更。 |
| `docs/plans/i04-provider-model-switching.md` | **実装済み・置換あり**。`internal/providers/registry.go:137`、`internal/app/connections.go:94-136`。旧source archiveと認証先は現行ではない。 |
| `docs/plans/i09-runtime-input.md` | **実装済み**。`internal/corerun/runtime_input_followup.go:17-113,252-328`、`internal/tui/model.go:751-769`。未実装説明は着手時点。 |
| `docs/plans/i10-context-recall.md` | **実装済み・保存方式に置換あり**。`internal/history/store.go:539-595`、`internal/corerun/recall.go:361-388`。pendingはCore内参照。 |
| `docs/plans/i12-authentication-ui.md` | **実装済み・実確認は一部**。`internal/tui/auth_input.go:93-130,164-168` → `internal/httpapi/authentication.go:99-165`。実追加／再認証は未確認。 |
| `docs/plans/i13-core-startup.md` | **実装済み**。`internal/corelaunch/corelaunch.go:48-67,122-156`、`cmd/hjh/main.go:300-324`。Core再起動を跨ぐ画面復帰は対象外。 |
| `docs/plans/i15-cli-management-history.md` | **実装済み**。`cmd/hjh/core.go:68-150`、`cmd/hjh/history.go:215-257,287-344`。旧NDJSON互換ではない。 |
| `docs/plans/i16-cli-core-run.md` | **実装済み**。`cmd/hjh/main.go:114-142`、`cmd/hjh/remote_run.go:45-103`。接続CLI自身はDB／credentialを開かない。 |
| `docs/plans/i18-workspace-instructions-skills.md` | **実装済み・archive／base説明に置換あり**。`internal/app/workspace_context.go:15-33`、`internal/workspacecontext/context.go:154-195`。 |
| `docs/plans/i19-child-agent-control.md` | **実装済み**。`internal/app/child_runs.go:42-195`、`internal/app/child_instructions.go:60-195`。親終了後の子存続は対象外。 |
| `docs/plans/i20-lifecycle-hooks.md` | **実装済み・後続拡張あり**。`internal/app/lifecycle.go:66-168`、`internal/history/post_settlement.go:11-28`。現在はprojectRequestを加え七hook。 |
| `docs/plans/i21-tool-guidelines.md` | **実装済み・説明／保存に置換あり**。`internal/extensions/extensions.go:544-564`。長い例はhelpへ、保存tools欄は除外。 |
| `docs/plans/initial-run.md` | **実装済み・旧方式を後続置換**。`internal/app/execution.go:298-389`、`scripts/package-linux.py:44-50,90-103`。専用OpenRouter Chat／v3は当時の段階。 |
| `docs/plans/project-request-hook.md` | **実装済み・後続拡張あり**。`internal/app/execution.go:337-380`、`internal/app/request_context.go:55-155`。select等の追加評価は親側対象。 |
| `docs/plans/read-only-tools.md` | **実装済み・grep方式更新**。`internal/extensions/assets/inspect.js:1-87`、`internal/host/host.go:243-280`。rg必須はGNU fallbackへ置換。 |
| `docs/plans/tool-usage-guidance.md` | **実装済み計画＋調査記録**。`internal/extensions/tool_help.go:15-72`、`internal/jsruntime/bootstrap.js:125-135`。schema未調査／常用未配置の記述は当時の状態。 |

#### results：29文書

| 対象文書 | 状態／現行コード根拠／留保 |
|---|---|
| `docs/results/base-instructions.md` | **実装済み**。`internal/extensions/base_instruction.go:23-43`、`internal/app/execution.go:240-246`。記録の実確認はローカルHTTP。 |
| `docs/results/credential-config-directory.md` | **実装済み**。`internal/credentials/manager.go:181-217`、`internal/credentials/auth.go:457-463`。このVMでのコピー実施は過去運用記録。 |
| `docs/results/henji-feature-comparison.md` | **調査記録・実装／一部／未実装／非採用が混在**。例：command完了後出力は `internal/host/host.go:314-346`。日時注入行は古い。全未実装項目が要件ではない。 |
| `docs/results/i02-child-agents.md` | **実装済み・旧段階記録**。`internal/app/delegation.go:29-42,98-135`。当時の親model固定・UI停止キー・archiveは後続変更。 |
| `docs/results/i03-generic-roles.md` | **実装済み・旧段階記録**。`internal/extensions/extensions.go:302-364`、`internal/history/write.go:708`。標準roleは現在四種類。 |
| `docs/results/i04-provider-model-switching.md` | **実装済み・後続修正あり**。`internal/app/connections.go:94-136`、`internal/provider/protocol/encode.go:86-95,125-128,151`。後続設定修正の外部再確認は限定。 |
| `docs/results/i09-plan-contract-review.md` | **調査・設計review記録**。現行実装は `internal/corerun/runtime_input_admission.go:60`。本文共有案は `internal/history/runtime_input.go:70-79` へ置換。 |
| `docs/results/i09-runtime-input.md` | **実装済み・本文保存／測定前提に置換あり**。`internal/history/runtime_input.go:70-103`、`internal/history/conversation_write.go:315-353`。旧共有本文の測定値に留保。 |
| `docs/results/i10-context-recall.md` | **実装済み・request保存方式に置換あり**。`internal/app/execution.go:263-296`、`internal/history/request_body.go:19-62`。当時のattribution説明は現行ではない。 |
| `docs/results/i10-plan-contract-review.md` | **調査・設計review記録**。admission改善は `internal/history/store.go:584-595`、消費は `internal/corerun/recall.go:361-375` に実装。 |
| `docs/results/i12-authentication-ui.md` | **実装済み・実確認は一部**。`internal/credentials/attempt.go:393-468`、`internal/tui/auth_input.go:111-130`。追加／再認証／別実account未確認を明記。 |
| `docs/results/i12-plan-contract-review.md` | **調査・設計review記録**。現在は `internal/app/authentication.go:192-217` のattempt経路。review完了を実認可成功とは扱わない。 |
| `docs/results/i13-core-startup.md` | **実装済み**。`internal/corelaunch/corelaunch.go:48-67,122-156`。新TUI起動によるcold復帰と稼働TUIの跨Core復帰は別。 |
| `docs/results/i15-cli-management-history.md` | **実装済み**。`cmd/hjh/core.go:113-150`、`cmd/hjh/history.go:246,333-344`。Session削除対象外はI-15当時の範囲。 |
| `docs/results/i16-cli-core-run.md` | **実装済み**。`cmd/hjh/remote_run.go:74-103`、`cmd/hjh/remote_output.go:127-174`。CLI強制終了を取消に変換しない。 |
| `docs/results/i18-workspace-instructions-skills.md` | **実装済み・archive説明に置換あり**。`internal/workspacecontext/context.go:75-95,154-195`、`internal/app/execution.go:251-259`。 |
| `docs/results/i19-child-agent-control.md` | **実装済み・実確認の限界あり**。`internal/app/child_runs.go:42-195`、`internal/history/assistant_reports.go:14-43`。実モデル子active中detachの観測は限定。 |
| `docs/results/i19-plan-review.md` | **調査・設計review記録**。当時の「bridgeはtask/roleのみ」は `internal/app/delegation.go:52-74` の構成指定へ更新済み。 |
| `docs/results/i20-lifecycle-hooks.md` | **実装済み・標準日時例は停止済み**。`internal/app/lifecycle.go:66-168`、`internal/extensions/assets/hooks.js:1-7`。当時の配布確認記録は保持。 |
| `docs/results/i21-tool-guidelines.md` | **実装済み・保存／長文説明に置換あり**。`internal/extensions/extensions.go:544-564`、`internal/app/journal.go:507-517`。元config archive説明は古い。 |
| `docs/results/initial-run.md` | **実装済み・初期記録＋後続追補**。`internal/app/run.go:58-180`、`internal/app/execution.go:202-229`。初期v3／専用Chat／旧credential先は現行ではない。 |
| `docs/results/latest-session-resume.md` | **実装済み**。`internal/sessionid/latest.go:13-34`、`internal/tui/latest_session.go:30-43`。再開自体でモデルを呼ばない。 |
| `docs/results/project-request-hook.md` | **実装済み・後続拡張あり**。`internal/app/execution.go:347-354`、`internal/jsruntime/bootstrap.js:101-110`。末尾の要約未実装は当時の段階。 |
| `docs/results/read-only-tools.md` | **実装済み**。`internal/extensions/assets/inspect.js:1-87`、`internal/extensions/extensions.go:574-583`。role catalog効果の実モデル再確認は未実施。 |
| `docs/results/remembered-model-selection.md` | **実装済み**。`internal/app/selection_preferences.go:34-71`、`internal/app/selection.go:93-121`、`internal/tui/selection_preferences.go:28-38`。既存Sessionとは別状態。 |
| `docs/results/run-js-purpose.md` | **実装済み・指示配置を後続置換**。`internal/toolactivity/preview.go:66-72`、`internal/extensions/extensions.go:187-199`。description直書きではなくguidelines経由。 |
| `docs/results/run-stream.md` | **実装済み・provider経路を後続置換**。`cmd/hjh/output.go:24-68,128`、`cmd/hjh/remote_output.go:144-174`。OpenRouter専用表示ではない。 |
| `docs/results/tool-definition-guidance.md` | **実装済み・後続定義更新あり**。`internal/extensions/assets/index.js:1-15`、`internal/app/delegation.go:52-74`。read_file全文のみ等は部分読取り／help追加前。 |
| `docs/results/tool-usage-guidance.md` | **実装済み・配置続報あり**。`internal/extensions/tool_usage_policy.go:33-94`、`internal/jsruntime/bootstrap.js:125-135`。`:129`未配置は`:135-162`で更新、実provider受理は未確認。 |


## 実装済みと、実使用での確認済みを分ける留保

- **稼働中Core・dist・外部extensionの現在の一致は未確認**。ソースがあること、過去にbinaryを配置したこと、今動いているprocessに有効なことは別。今回、稼働環境を操作していない。
- **最新help schemaの実provider受理・自然なtool選択改善は未確認**。ローカルHTTP／Worker経路の既存確認を、外部モデルの改善効果へ読み替えない。
- **timeoutの実provider上での改善・正常長考への影響は未実証**。既存結果はローカルHTTP、Core／PTY、開始300秒の実時間確認。
- **OAuth追加account・再認証・別の実accountへの切替は未確認**。実装経路と、一つの保存済み実accountでの既存確認を区別。
- **最新footerの物理端末目視・常用Ghostty→SSH→tmuxでの操作受入は今回未確認**。PTY／fake Backend確認を実provider／物理端末確認と呼ばない。
- **最新assistant/providerState保存方式での実provider→再起動→継続は今回未確認**。既存のローカルSSE／CLI／Core／PTY結果はその条件の証拠。
- **コンパクション長期品質、検索defaults変更後の外部Exa応答、cache比率の現在の再現性、保存最適化の長期I/O効果は今回未測定**。既存の数値を普遍的な削減率・性能保証へ一般化しない。

## 次に扱う際の切り分け

1. **文書整理は2026-10-16の後続指示で実施済み**。初期製品方針末尾・I-10冒頭・統合TUIのfooter・run API等を修正し、過去結果には現行契約への参照と置換状態を追記した。入口は[実装状況一覧](../implementation-status.md)。過去結果は当時の証拠として保持する。
2. 未実装候補から新機能へ進む場合は、利用者が優先順位を選び、必要な動作・採用方式・実経路を具体化する。検討メモだけで実装承認としない。
3. 常用環境で有効かを確認する作業は、ソース照合とは分け、対象binary・Core・workspaceと利用者の操作範囲を定めて実施する。

この報告では新しい機能要件・実装計画・hardeningを採用せず、既存データ・設定・稼働processは変更していない。
