# 実装状況一覧

更新日: 2026-10-16。`docs/notes/`・`docs/design/`・`docs/plans/`・`docs/results/`の
151文書を現行コードと照合した一覧。詳細なコード根拠は[照合結果](results/documentation-implementation-audit.md)を参照。

**実装済み**は利用者の入口から処理・保存・参照／表示までコードが接続されていることを示す。
最新版の実provider・実端末で全機能を再確認した、という意味ではない。
**未実装・検討中**には未採用の案も含み、実装漏れ・実装承認を意味しない。
計画・結果・review文書の過去の状態と、現在の機能状態を分けている。
この一覧は実装状況の索引であり、各設計契約や利用者の決定を上書きしない。

## 実装済み

| 機能・改善 | 現在の範囲 | 元文書 |
| --- | --- | --- |
| 非対話CLI・stream出力 | 最終回答／進捗の分離、取消、保存会話での継続 | [初期製品方針](design/initial-product.md)・[初期run計画](plans/initial-run.md)・[結果](results/initial-run.md)・[stream](results/run-stream.md) |
| Core／TUI分離・HTTP API | 独立した`hjh serve`と`hjh-tui`、HTTP＋JSON＋SSE | [HTTP契約](design/http-api.md)・[run API](design/run-api.md)・[I-05計画](plans/i05-core-tui-separation.md)・[結果](results/i05-core-tui-separation.md) |
| Core起動・管理CLI・接続run | TUIから起動／再利用、core list/status/stop、CLIからCoreへ依頼 | [I-13](plans/i13-core-startup.md)・[結果](results/i13-core-startup.md)・[I-15](plans/i15-cli-management-history.md)・[結果](results/i15-cli-management-history.md)・[I-16](plans/i16-cli-core-run.md)・[結果](results/i16-cli-core-run.md) |
| 会話TUI・保存履歴表示 | 通常画面への逐次出力、親子・thinking・tool、詳細参照、保存状態から復元 | [TUI](design/tui.md)・[会話契約](design/i06-conversation-contract.md)・[I-06計画](plans/i06-conversation-tui.md)・[結果](results/i06-conversation-tui.md) |
| Session管理・再開・削除 | 一覧・新規・rename・resume・detach、CLI／TUI／Coreの削除、不要本文・構成回収 | [I-07](plans/i07-session-management.md)・[結果](results/i07-session-management.md)・[最新Session再開](design/latest-session-resume.md)・[結果](results/latest-session-resume.md)・[CLI削除](results/cli-session-delete.md)・[picker](results/inline-pickers.md) |
| 短縮Session ID | 人向け表示・一意prefix解決。保存・APIは完全ID | [設計](design/session-ids.md)・[結果](results/short-session-ids.md) |
| 接続・model・effortの選択 | 複数provider、picker、同Sessionでの変更、workspaceの次回選択の記憶 | [provider設計](design/providers.md)・[I-04](plans/i04-provider-model-switching.md)・[結果](results/i04-provider-model-switching.md)・[I-08](plans/i08-connection-selection.md)・[結果](results/i08-connection-selection.md)・[記憶selection](results/remembered-model-selection.md) |
| modelのお気に入り | 接続・認証scopeに応じた保存とpicker表示 | [契約](design/i11-model-favorites-contract.md)・[計画](plans/i11-model-favorites.md)・[結果](results/i11-model-favorites.md) |
| 認証UI・credential保存 | APIキー登録、ChatGPT OAuthの認可・account選択経路、ユーザー設定配下への保存 | [契約](design/i12-authentication-contract.md)・[計画](plans/i12-authentication-ui.md)・[結果](results/i12-authentication-ui.md)・[保存先整理](results/credential-config-directory.md) |
| generic role・子agent委譲 | 親子共通engine、外部role、子ごとのmodel／tools指定、独立会話 | [拡張契約](design/extensions.md)・[I-02](plans/i02-child-agents.md)・[結果](results/i02-child-agents.md)・[I-03](plans/i03-generic-roles.md)・[結果](results/i03-generic-roles.md) |
| 非同期子agent・報告・追加指示 | spawn/status/steer/collect/cancel、親の作業と並行して実行 | [I-19計画](plans/i19-child-agent-control.md)・[結果](results/i19-child-agent-control.md) |
| 実行中の追加入力・次run予約 | F3 steering、F2 follow-up、受付・適用状態、予約列の停止 | [契約](design/i09-runtime-input-contract.md)・[計画](plans/i09-runtime-input.md)・[結果](results/i09-runtime-input.md) |
| context表示・recall | 停止した実行の公開情報を次runへ参考入力として渡す | [契約](design/i10-context-recall-contract.md)・[計画](plans/i10-context-recall.md)・[結果](results/i10-context-recall.md) |
| 外部JS tool・hook・生成JS | 開始時capture、次executionで編集反映、tool合成、内側の実行履歴 | [拡張契約](design/extensions.md)・[run_js目的表示](results/run-js-purpose.md)・[tool字下げ](results/tui-tool-nesting.md) |
| go-quickjsへのengine移行 | v0.25.2、WorkerごとのVM、Go Hostへの接続。stdlib移行とは別 | [I-17計画](plans/i17-go-quickjs.md)・[結果](results/i17-go-quickjs.md) |
| ファイル・コマンド・読取りtool | 行指定read、write/edit、ls/find/grep/wc/git_inspect、rg／GNU grep | [計画](plans/read-only-tools.md)・[結果](results/read-only-tools.md)・[引数説明](results/tool-definition-guidance.md)・[grep backend](results/grep-native-backends.md)・[paging](results/grep-paged-results-and-v4-cutover.md) |
| AGENTS・skills・基本指示 | workspace指示とskill一覧、必要時のskill本文読取り、base指示の差替え | [workspace契約](design/workspace-instructions-skills.md)・[I-18](plans/i18-workspace-instructions-skills.md)・[結果](results/i18-workspace-instructions-skills.md)・[base設計](design/base-instructions.md)・[結果](results/base-instructions.md) |
| lifecycle hook・request hook | 開始／終了・tool前後・各requestの介入。標準日時注入は停止済み | [拡張契約](design/extensions.md)・[I-20](plans/i20-lifecycle-hooks.md)・[結果](results/i20-lifecycle-hooks.md)・[request hook計画](plans/project-request-hook.md)・[結果](results/project-request-hook.md) |
| tool利用方針・個別help | 外部tool-usage.md、短い常駐説明、各toolの`{help:true}`。独立辞書検索は非採用 | [設計](design/tool-usage-guidance.md)・[計画](plans/tool-usage-guidance.md)・[結果](results/tool-usage-guidance.md)・[I-21](results/i21-tool-guidelines.md) |
| Web読取り・Exa検索 | web_fetchは通常64 KiB読取り・HTML可読化／全体保存。検索は既定10件・抜粋1,000文字 | [検索設計](design/web-search.md)・[I-22](plans/i22-exa-web-search.md)・[結果](results/i22-exa-web-search.md)・[取得defaults計画](plans/web-retrieval-defaults.md)・[結果](results/web-retrieval-defaults.md) |
| context予算・送信対象選択 | 各モデル送信前に履歴／現在turnを選択。保存原文とは分離 | [I-14契約](design/i14-web-context-contract.md)・[計画](plans/i14-web-context.md)・[結果](results/i14-web-context.md)・[request context](design/request-context.md)・[選択計画](plans/request-context-selection.md)・[結果](results/request-context-selection.md) |
| JSコンパクションの基本機能 | opt-in hook、要約生成と原文参照、同一Core内のSessionメモリ。標準hookは無効 | [設計](design/js-compaction.md)・[計画](plans/js-compaction.md)・[結果](results/js-compaction.md) |
| failure分類・provider retry | 分類付き失敗保存とrecall案内。追加retryは1回・固定2秒 | [分類計画](plans/failure-classification-recall.md)・[結果](results/failure-classification-recall.md)・[retry計画](plans/provider-single-retry.md)・[結果](results/provider-single-retry.md) |
| モデル応答待ちtimeout | 既定は応答開始300秒／read idle600秒。親子共通、設定可能、総生成時間の上限ではない | [依頼メモ](notes/provider-response-timeout-task.md)・[計画](plans/provider-response-timeout.md)・[結果](results/provider-response-timeout.md) |
| キャッシュ用送信ID | rootはSession ID、子は各execution ID、providerのsessionHeader設定で送信 | [整理メモ](notes/provider-cache-affinity.md)・[実測と組込み結果](results/direct-cache-patterns.md) |
| picker・slash menu・Markdown | inline picker、候補選択、Solarized Darkのassistant Markdown、固定配色 | [picker](design/inline-pickers.md)・[結果](results/inline-pickers.md)・[slash](design/slash-command-menu.md)・[結果](results/slash-command-menu.md)・[描画](design/tui-rendering.md)・[結果](results/tui-rendering.md) |
| header・footer・ctx使用率 | 区切り線に状態／Session／ctx、入力欄下にworkspace／接続統合行、入力上下余白 | [header](design/tui-header.md)・[結果](results/tui-header.md)・[footer](design/tui-footer.md)・[結果](results/tui-footer.md)・[ctx計画](plans/context-usage-display.md)・[結果](results/context-usage-display.md) |
| 通知寿命の整理 | 前runのsteering通知残留・遅延応答の誤表示防止、取得失敗の復旧反映 | [計画](plans/tui-notice-lifecycle.md)・[結果](results/tui-notice-lifecycle.md) |
| 履歴schema v4・通常保存整理 | 本文参照、tool結果重複整理、request小metadata／予算summary | [schema](design/history-schema.md)・[payload](design/history-payloads.md)・[保存契約](design/normal-history-storage.md)・[計画](plans/normal-history-storage.md)・[結果](results/normal-history-storage.md)・[tool出力](plans/tool-output-storage.md)・[結果](results/tool-output-storage.md) |
| snapshot専用表・終了時保存 | 最新wire／完全予算Factをownerに保持し終了後保存。live参照・再保存API | [上書き計画](plans/history-snapshot-overwrite.md)・[結果](results/history-snapshot-overwrite.md)・[終了時保存計画](plans/history-terminal-snapshots.md)・[結果](results/history-terminal-snapshots.md) |
| WAL・初出位置・重複索引整理 | writable DBはWAL/FULL、初出位置の履歴再検索削減、重複索引2本削除 | [WAL計画](plans/history-wal-first-sequence.md)・[結果](results/history-wal-first-sequence.md)・[索引計画](plans/history-redundant-indexes.md)・[結果](results/history-redundant-indexes.md)・[v4切替](results/grep-paged-results-and-v4-cutover.md) |
| usage保存整理 | usage観測に単一保存、直下attributionを通常保存から除去、集計値・live応答は維持 | [依頼メモ](notes/usage-storage-task.md)・[計画](plans/usage-storage.md)・[結果](results/usage-storage.md) |
| assistant／thinking保存整理 | S-1〜S-3実装済み。mirrorとthinkingの参照化、model_resultをproviderStateの所有先に | [計画](plans/assistant-thinking-storage.md)・[現行契約](design/normal-history-storage.md)・[結果](results/assistant-thinking-storage.md) |

## 一部対応済み・未実装部分が残るもの

| 主題 | 実装済みの範囲 | 未実装／未解決の範囲 | 元文書 |
| --- | --- | --- | --- |
| 巨大HTTP結果のcontext超過 | web_fetchの可読化・範囲取得、各requestの予算と送信選択 | 生httpは全文取得のまま。最新の保護対象batchだけで超過する場合もあり、元の事例が必ず完了する保証はない | [原因・後続状態](notes/http-response-context-overflow.md)・[I-14契約](design/i14-web-context-contract.md) |
| コンパクションの継続・再開 | 同一Core内で要約と保持範囲を引継ぎ、TUI再接続でも維持 | Core再起動／別CLIでの要約状態復元、巨大な完了turnの末尾原文をtoken量で保持する改善 | [設計・改善候補](design/js-compaction.md) |
| DB容量最適化 | 通常履歴・usage・assistant重複削減、不要本文回収、source一括自動archive停止 | contents全面廃止・保存圧縮は未採用。精密usage collectorは別テーマ | [schema議論・決定](design/history-schema.md)・[保存契約](design/normal-history-storage.md) |

## 未実装・検討中

| 項目 | 状態・既存機能との違い | 元文書 |
| --- | --- | --- |
| 意思決定モデルのhjh統合 | Jev／Decisions API活用案。導入方式・実装順序は未決。隣接HenjiのCLI実装はhjh統合とは別 | [検討メモ](notes/decision-model-uses.md) |
| QuickJS stdlib利用・tool処理のJS化 | engine移行は済んでいるがstdlib導入・独自Go Host置換は未実装、移行計画未作成 | [検討メモ](notes/quickjs-stdlib-tools.md) |
| Markdown／非Markdownテーマの独立差替え | 現在はSolarized Dark・固定配色。設定方法・時期は未定 | [改善候補](design/tui-rendering.md#将来の改善候補テーマの差し替え) |
| 直近原文のtoken量による保持改善 | 既存の予算選択とは別に、巨大な完了turnの末尾を残す方針を検討 | [改善メモ](design/js-compaction.md#改善メモ直近原文の保持と再開時の要約状態復元) |
| 要約状態の永続保存・復元 | Core再起動・別CLI後も同じ要約／保持範囲を再利用する案。checkpoint_jsonの利用は未実装 | [DB対応案](design/js-compaction.md#dbへの対応案) |
| 過去原文検索・読戻し専用拡張 | CLI history／inspectは既存。FTS等による新しい検索経路は別候補、10turn上限も未確定 | [別テーマ](design/js-compaction.md#参考と別テーマ) |
| agent全体のDB非保存mode | 補助要約の送受信全文を保存しない限定経路とは別。操作契約未定 | [将来検討](design/agent-storage-modes.md) |
| コンテナ／OS VMへのagent配置 | 現在は同じGo process内でagent実行。実行先・通信・配布方式は未設計 | [将来構想](design/future-agent-environments.md) |
| 追加retryを2回へ増加 | 初回含め最大3試行・2秒→4秒案。現行は追加1回・2秒 | [改善メモ](plans/provider-single-retry.md#改善メモ追加リトライを2回へ増やす案) |
| hook文脈の追加位置指定API | 日時などを末尾の会話要素として追加する案。現在の標準日時注入は停止済み | [拡張契約の検討メモ](design/extensions.md#i-20execution-lifecycle-hooks) |
| 精密usage collector | 通常保存から外す詳細内訳を開発・調査用に収集する新基盤は別テーマ | [取得口と未対応範囲](plans/usage-storage.md#5-調査用の取得口と未対応範囲)・[結果](results/usage-storage.md) |
| 共有本文contents全面廃止・保存圧縮 | steering受付の独立保存やSession削除時回収とは別。方式・採用とも未確定 | [schema検討と後続決定](design/history-schema.md) |

## 調査記録・置換済み方式

以下を未実装機能の一覧へ混ぜない。

- [最初のTUI計画](plans/first-tui.md)・[結果](results/first-tui.md)の同一process方式、
  [S-06](results/s06-tui-core.md)の旧描画／保存方式は後続実装で置換済み。
- [SDK採用評価](design/provider-sdk-evaluation.md)は調査。SDKは未採用だが、分類・retry・timeoutは別途実装済み。
- [SDK cache probe](results/sdk-cache-probe.md)・[直接HTTP実測](results/direct-cache-patterns.md)は測定条件付きの記録。
  ChatGPTでのcache hitをOpenRouter等へ一般化しない。
- [context計測の旧調査](results/context-metrics-live.md)のcurrent-only不足は、
  [履歴込み選択](results/request-context-selection.md)で後続対応済み。
- 各`*-plan-review.md`／`*-plan-contract-review.md`は計画reviewの時点記録であり、
  そこでの「未実装」は現在の機能状態ではない。
- [Henji機能比較](results/henji-feature-comparison.md)には未採用項目も含まれる。旧製品の全機能はhjhの必須要件ではない。

## 実装とは別に未確認のもの

- 現在稼働中のCore・配置binary・workspace外部設定が最新sourceと一致しているか。
- 最新help schemaの実provider受理、自然なtool／role選択の改善。
- OAuth追加account・再認証・別の実accountでの通し確認。
- timeoutの実provider上の改善効果、正常な長考への影響。
- 最新footerの物理端末目視、最新保存形式での実provider→Core再起動→継続。
- コンパクションの長期品質、cache比率の現在の再現性、保存最適化の長期I/O効果。

確認済み条件・残る留保は、それぞれの結果文書と[照合結果](results/documentation-implementation-audit.md)に記載する。
