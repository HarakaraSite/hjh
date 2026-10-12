# hjhと旧Henjiの機能比較

> 後続更新：子agentの非同期開始・並列実行・構成指定・報告差分・親からの追加指示はI-19で実装・確認済み。以下は調査時点の比較であり、現在の範囲と結果は[実装結果](i19-child-agent-control.md)を参照する。
2026-10-12時点。通常の対話、Core、履歴、認証、コンテキスト予算管理はhjhにも揃っている。
旧Henjiとの差が大きいのは、専用tool、子agentの非同期操作、拡張hook、設定管理CLIである。

この比較は未実装機能を検討するための調査結果であり、全機能の移植を要求する要件や実装計画ではない。
採否は利用者の指定とhjhの採用済み設計に従う。

## 比較対象と判定

| 対象 | 比較時点 |
| --- | --- |
| hjh | `/home/agent/projects/hjh`。HEAD `1a8e899a1ec963f86df6ca245dc261b55173baeb`と、その上の作業ツリー |
| hjhの未コミット変更 | フッター、Glamour、Solarized Dark、履歴の色表示、Markdown空行の背景、run_jsの英文目的コメントなどを含む |
| 旧Henji | `/home/agent/projects/henji-harness`。HEAD `2eb586316f80ec8297873a96122568d0088971eb`の現行実装 |

旧Henjiの実行ファイルもCLI上では`hjh`と表示するが、本書の「旧Henji」はDeno版の
henji-harnessリポジトリを指す。旧roadmapには古いキー割当やschema表記が残っているため、
現在のCLI、TUI、tool、Core、保存処理を基準に比較する。

| 判定 | 意味 |
| --- | --- |
| 未実装 | 旧Henjiにある該当の操作や機構がhjhにはない |
| 専用機能なし | 同名・同等の専用操作はないが、commandや生成JSなどで作業自体は代替できる |
| 一部対応 | 基本機能はあるが、指定方法や利用できる範囲に差がある |
| 実装済み | 名称や構成が異なっても、比較対象の利用目的を満たす経路がある |
| 意図的な方式差 | hjhで採用した方式、または明示的な不要判断による差 |
| 旧側も未完成 | 旧Henjiにも完成したproduct機能がなく、未移植機能とは区別する |

「専用機能なし」は、その作業がhjhでは不可能という意味ではない。
以下はソースと採用文書に基づく比較であり、各項目の外部サービス接続を今回再検証した結果ではない。

## toolの違い

| 機能 | 旧Henji | 現在のhjhと不足している点 |
| --- | --- | --- |
| Web検索 | Exaの`web_search`。URL、タイトル、抜粋、本文などを取得 | **未実装**。`web_fetch`は指定URLの読取りであり、検索機能とは別 |
| tool用の認証付きHTTP | 外部toolが認証profileを指定してサービスへrequest | **未実装**。現在のtool用HTTPはURL指定のGET。POST、headers、body、認証profileを扱う共通入口がない |
| ディレクトリ一覧 | `ls`。tree表示、entry情報 | **専用toolなし**。`command`で代替可能 |
| ファイル検索 | `find`。glob、種類、ページ指定 | **専用toolなし**。`command`で代替可能 |
| 本文検索 | `grep`。正規表現、固定文字列、周辺行、一致ファイル一覧 | **専用toolなし**。`command`で代替可能 |
| ファイル集計 | `wc`。行数、単語数、byte数 | **専用toolなし**。commandやJSで代替可能 |
| Git確認 | `git_inspect`。status、diff、log、showと範囲指定 | **専用toolなし**。`command`でGitを呼べる |
| ファイルの部分読取り | `read`に行単位の`offset`と`limit` | **一部対応**。`read_file`は全文読取り。必要な行だけ読む標準操作がない |
| 複数箇所の編集 | `edit`に複数の置換をまとめて指定 | **一部対応**。`edit_file`は一組の`oldText/newText`で、一致箇所をすべて置換する方式 |
| ファイル作成 | `write`が不足する親directoryも作成 | **一部対応**。`write_file`では親directoryを先に用意する必要がある |
| コマンドの途中出力 | 実行中のstdoutとstderrをtool進捗として通知 | **未実装**。`command`のstdoutとstderrは終了後に返る |
| 大量のコマンド出力の分割取得 | `bash_output`で保存出力をIDとoffset指定で取得 | **未実装**。現在はコマンド結果をまとめて返す |
| コマンドごとのtimeout | `bash`に`timeoutMs` | **指定機構なし**。実行取消はある |
| JSONの最終回答 | `submit_json_result`でJSONを検証し、最終回答として終了 | **専用機構なし**。JSON形式の文章を回答すること自体は可能 |

ファイル検索やGit確認は、専用toolを持つかどうかの差である。
一方、Web検索には検索サービスとの接続が必要で、HTML読取りの改善だけでは補えない。
認証付きHTTPの不足も、既存のモデルprovider認証が未実装という意味ではなく、
外部toolから非モデルサービスを呼ぶ共通経路の不足である。

参照先：

- 旧Henji：[標準tool一覧](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/configuration/default-agent.json)、[外部tool](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/external-tools)、[ファイル操作](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/tools/file_tools.ts)、[bash出力](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/tools/bash_output.ts)、[JSON最終回答](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/tools/tools.ts)。
- hjh：[標準tool定義](../../internal/extensions/assets/config.json)、[tool実装](../../internal/extensions/assets/index.js)、[Go基礎操作](../../internal/host/host.go)。

## agentとhookとインストラクションの違い

| 機能 | 旧Henji | 現在のhjhと不足している点 |
| --- | --- | --- |
| 子agentを開始して後で回収 | spawnでIDを返し、status、collect、cancelを別々に呼ぶ | **未実装**。`run_agent`は結果を待つ操作 |
| 子agentの並列実行 | 複数の子を開始可能 | **実装済み**。`run_js`の`Promise.all`で可能。ただし子を動かしたまま親が次のモデルstepへ進む方式ではない |
| 子のmodel、provider、effort指定 | spawn時に個別指定 | **一部対応**。roleのmodel設定は使えるが、呼出しごとの指定はない |
| 子のtool指定 | spawn時にtool一覧を指定 | **一部対応**。roleごとのtool一覧はあるが、呼出しごとの変更はない |
| ライフサイクルhook | runtime開始と終了、turn前後、tool前後の六種類 | **一部対応**。現在は`beforeTool`と`afterTool`の二種類 |
| hookによる文脈追加 | runtime開始とturn開始時に追加contextを返せる | **未実装** |
| 要約checkpointの保存と再利用 | `after_turn`から要約と対象turn境界を返し、後続会話へ渡せる | **未実装**。現在は予算内に収まる履歴を選んで送る方式 |
| tool自身が持つ利用指示 | `promptGuidelines`を有効toolから集めて共通指示へ追加 | **専用機構なし**。現在はtoolのdescriptionやrole指示へ書く |
| 基本指示全体の差替え | ユーザーの`instruction.md`で組込み基本指示を置換 | **未実装**。現在の`systemInstruction`、role指示、AGENTS.mdは追加する方式 |
| 開始日時の自動注入 | 同梱hookが日時とtimezoneをモデルへ渡す | **未実装** |

### 子agentの操作範囲

hjhでも複数の子を並列に実行し、その結果をまとめて親へ返せる。
ただし子の寿命は外側のJS callに属し、残った子を取消して終了保存を待ってから、
次の親model stepへ進む。

旧Henjiには、子を開始してIDを受け取り、親が別の判断を進め、その後で状態確認や結果回収を
行う操作がある。この非同期操作と、呼出しごとのmodelやtool指定が差分である。

参照先：

- 旧Henji：[子agent API](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/tools/async_agents.ts)。
- hjh：[子agent実行](../../internal/app/delegation.go)、[JSからの開始](../../internal/jsruntime/host.go)、[子agentの実装結果](i02-child-agents.md)、[role別model設定](../design/providers.md)。

### コンテキスト予算と要約checkpoint

**ターン内、ターン間、Session再開時、モデル変更時、子agentのコンテキスト予算管理はhjhに実装済みである。**
保存した原文を維持し、送信する履歴を予算に応じて選ぶ。
最新部分やtool交換のまとまりを保ち、選択した内容と理由を記録する。

不足しているのは、過去の会話を要約してcheckpointとして保存し、その要約を後続会話へ入れる方式である。
ただし旧Henjiも、標準の自動要約処理が完成しているわけではない。
`after_turn`とcheckpointの保存、後続contextへの反映までがあり、
実際に要約モデルを呼ぶ外部定義や圧縮閾値は未実装である。

参照先：

- 旧Henji：[六種類のhook契約](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/hook_api.ts)、[turn終了後のcheckpoint処理](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/worker/worker_runtime.ts)、[Increment 189の完成範囲](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/docs/increments/increment-189.md)。
- hjh：[Web読取りとCoreコンテキスト管理](../design/i14-web-context-contract.md)、[tool前後のhook](../../internal/jsruntime/bootstrap.js)。

インストラクションの比較元は、旧Henjiの[組立処理](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/instructions/compose.ts)と[基本指示の差替え](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/instructions/base_instruction.ts)、hjhの[指示組立](../../internal/extensions/extensions.go)と[workspace指示とskills](../design/workspace-instructions-skills.md)である。

## TUIとセッション操作の違い

| 機能 | 旧Henji | 現在のhjhと不足している点 |
| --- | --- | --- |
| セッション削除 | CLI、Session picker、Core操作にある | **未実装**。CLI、TUI、Core APIに削除操作がない |
| 最新セッションへ簡単に再開 | `--continue`、`/resume latest` | **未実装**。現在の再開にはID指定かSession pickerを使う。`history --latest`は実装済み |
| 永続Sessionを作らない対話 | `--no-session` | **未実装**。永続Sessionを作らないモードがない。旧側の指定も全実行証拠の非保存を意味するものではない |
| 読取り専用の別セッション閲覧 | `/view`、pickerで閲覧と再開を分離 | **意図的に未採用**。以前の利用者指定どおり、hjhには追加していない |
| TUI内の会話browser | 項目選択、前後ページ、本文chunk表示 | **未実装**。現在は端末やtmuxのscrollbackと`/history`を使う |
| tool本文の詳細閲覧 | browserから選択して分割閲覧 | **一部対応**。`/tools`と`/tool`はあるが、専用browserや本文の分割閲覧はない |
| slash command候補 | `/`入力から絞り込みpickerを表示 | **一部対応**。現在はTab補完 |
| modelとeffort選択の即時保存 | idle時の選択をSessionと新規Session用の既定へ保存 | **一部対応**。現在は次run用の選択で、正常runの採用時にSessionへ保存。実行せず終了した場合の保存や、新規Session用既定の記憶はない |
| Core IDでTUI接続 | TUIに`--core ID` | **入口なし**。現在は`--endpoint URL`。CLIの`core --core ID`はある |

`/view`は実装漏れではなく、[TUIの採用方針](../design/tui.md)に不要という判断が記録されている。
会話browserの不足も、会話の検索やコピーができないという意味ではない。
両製品とも検索とコピーには端末やtmuxのscrollbackを使う。

また、hjhでは会話全文のviewやturn範囲の読取りはできるが、旧会話browser用の前後ページと
本文chunkの読取りに対応する操作はない。この比較だけでは、実際の長大Sessionでの性能差までは判断しない。

参照先：

- 旧Henji：[TUIコマンド](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/tui/slash_command.ts)、[会話browser](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/tui/conversation_browser.ts)、[TUI操作実装](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/tui/remote_session.ts)、[選択の既定保存](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/provider/default_selection.ts)。
- hjh：[TUIコマンド](../../internal/tui/commands.go)、[Session操作](../../internal/tui/session_management.go)、[次runのmodel選択](../../internal/tui/connection_picker.go)、[TUI起動option](../../cmd/hjh-tui/main.go)、[Core HTTP API](../../internal/httpapi/server.go)。

## CLIと診断と配布の違い

| 機能 | 旧Henji | 現在のhjhと不足している点 |
| --- | --- | --- |
| runの機械向けイベント出力 | `run --json`でNDJSON | **未実装**。現在は最終回答か`--stream`。`--json`はsessions用 |
| agent設定の登録と解除 | `agent activate/deactivate`、個別inspect | **未実装**。`roles`の一覧と`--role`選択はあるが、登録と解除のCLIはない |
| toolの登録と解除とinspect | `tool activate/deactivate/list/inspect` | **未実装**。現在はconfigとJSを編集する |
| workspace全体の実行一覧CLI | `diagnostics executions list` | **未実装**。TUIの`/executions`やSessionのhistoryから参照する |
| 失敗診断だけの一覧と最新と表示と削除 | `diagnostics list/latest/show/delete` | **専用入口なし**。失敗や実行記録は`inspect`等で確認可能 |
| request単位の診断 | request番号から送信内容、HTTP status、parser失敗箇所を取得 | **一部対応**。送信内容、応答結果、usage、失敗記録はあるが、旧方式の短いrequest factと専用CLIはない |
| runtime情報のまとめ表示 | `diagnostics runtime`でbuildとworkspaceや設定などのpathを表示 | **専用入口なし**。version、Core情報、保存構成などに情報が分かれている |
| provider requestのtimeout指定 | `--provider-timeout-ms` | **指定機構なし** |
| インストーラ | binary、外部tool、hookを配置と登録。既存編集を保持する再配置optionもある | **未実装**。配布archiveはあるが、配置と設定登録は手動 |

実行の診断材料そのものがないわけではない。
hjhの`inspect`はexecution、子実行、messages、records、保存構成、contextを返す。
差は、失敗だけを探す操作やrequest単位で整理した参照入口と、診断factの粒度にある。

参照先：

- 旧Henji：[CLI契約](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/cli/cli_help.ts)、[設定管理CLI](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/cli/configuration_cli.ts)、[診断CLI](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/cli/failure_diagnostic_cli.ts)、[provider fact](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/provider/provider_evidence.ts)、[runtime情報CLI](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/cli/henji_cli.ts)、[インストーラ](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/scripts/install_henji.sh)。
- hjh：[CLI契約](../../cmd/hjh/help.go)、[CLI実装](../../cmd/hjh/main.go)、[inspection](../../internal/app/service.go)、[provider実行](../../internal/provider/protocol/generate.go)、[配布生成](../../scripts/package-linux.py)、[導入手順](../distribution-linux.md)。

## hjhにも実装済みの機能

| 分野 | 現在のhjh | 参照先 |
| --- | --- | --- |
| 通常実行 | CLIとTUI、stream、thinking、tool進捗、最終回答 | [runとstream](run-stream.md)、[TUI](../design/tui.md) |
| Core | 未起動時の自動起動、既存Core利用、複数Session、detach、再接続、停止 | [Core起動](i13-core-startup.md)、[Session管理](i07-session-management.md) |
| CLI管理 | `core list/status/stop`、`--version` | [CLI管理とhistory](i15-cli-management-history.md) |
| CLIからCoreへ依頼 | `run --connect URL`、後からTUIで接続 | [Core接続run](i16-cli-core-run.md) |
| セッション | 新規、一覧、短縮ID、再開、rename | [Session管理](i07-session-management.md)、[短縮ID](short-session-ids.md) |
| 履歴 | session、canonical、detail、ローカルとCore経由、保存構成と送信contextの確認 | [CLI管理とhistory](i15-cli-management-history.md)、[context](i10-context-recall.md) |
| 実行中の入力 | F1取消、F2予約、F3追加指示、受付結果確認 | [実行中の入力](i09-runtime-input.md) |
| 停止した作業の引継ぎ | `/recall` | [contextとrecall](i10-context-recall.md) |
| 接続選択 | provider、model、effort選択、お気に入り | [接続選択](i08-connection-selection.md)、[お気に入り](i11-model-favorites.md) |
| 認証 | APIキー、ChatGPTの追加と再認証と複数accountの選択機構 | [認証UI](i12-authentication-ui.md) |
| 指示とskills | AGENTS.md、role指示、起動時のskill一覧、利用時の本文読取り | [workspace指示とskills](../design/workspace-instructions-skills.md) |
| Web読取り | HTML可読化、1MiB上限、`save_to` | [Webと予算管理](i14-web-context.md) |
| コンテキスト予算 | ターン内、ターン間、Session再開、モデル変更、子agent | [予算契約](../design/i14-web-context-contract.md) |
| 外部拡張 | role、tool、tool前後のhook、開始時のsource捕捉、次実行への編集反映 | [外部拡張](../design/extensions.md) |
| 表示 | ヘッダー、三行フッター、Markdown、途中のassistant本文、配色、run_js目的コメント | [ヘッダー](../design/tui-header.md)、[フッター](../design/tui-footer.md)、[Markdownと色](../design/tui-rendering.md)、[run_js目的表示](run-js-purpose.md) |

認証の複数account機構は実装済みであるが、今回の比較で追加accountのブラウザ認証を
実施したという意味ではない。過去の実確認範囲は認証UIの結果文書に従う。

## 方式差と旧側も未完成の機能

| 項目 | 比較上の扱い |
| --- | --- |
| TypeScriptとDenoの実行環境 | **意図的な方式差**。hjhはQuickJSと準備済みESMを採用した。生成JSでの処理、tool合成、結果利用は可能だが、Deno APIやDeno stdの実行時取得を再現しない |
| 専用skill toolと旧探索directory | **意図的な方式差**。hjhはPi方式と合意済みの探索場所を採用した。旧側の本文snapshot方式や`.zot`、`.claude`の探索をそのまま移植する要求ではない |
| TUIの既定Core起動 | **意図的な方式差**。旧側は通常起動ごとに新Core。hjhは既定endpointのCoreを再利用し、必要なら起動する。別portで複数Coreを動かすことも可能 |
| CLIとTUIの実行ファイル | **意図的な構成差**。旧側は一つのbinaryからTUIを起動。hjhは`hjh`と`hjh-tui`を分離している |
| Web UI | **旧側も未完成**。旧CLIに予約された名前があるだけで、完成した旧機能の未移植ではない |
| durable AgentInstanceとInstance単位のSession管理 | **旧側も未完成**。Session継続やCoreの複数Session実行と区別する |
| agentが自身の実行と構成を発見する専用API | **旧側も未完成**。履歴やCLIによる間接参照と、専用product操作は別 |
| 専用experienceとcandidate管理 | **旧側も未完成**。既存履歴やrepositoryで改善を進める運用はあるが、完成した一式の機構はない |
| 標準の自動要約 | **旧側も未完成**。旧側にはcheckpoint基盤があるが、標準の要約処理と閾値は未実装 |
| managed resourceのexportとimport | **旧側で廃止**。現行の移植残件として扱わない |
| 専用rebuild | **旧側で不採用**。新実行や再開時の現在設定読込みは両側にある |
| agentによる後続rootのmodel変更 | **旧側で見送り**。人間の選択や子開始時のmodel指定と区別する |
| Markdownとそれ以外のテーマ差替え | **hjhの将来候補**。固定テーマは実装済みで、独立した差替え機構はメモ済み。旧機能の移植とは別 |

hjhの方式は[初期製品の採用方針](../design/initial-product.md)、[workspace指示とskills](../design/workspace-instructions-skills.md)、[Core起動](i13-core-startup.md)を参照する。
旧側の未完成、廃止、不採用項目は[旧roadmap](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/docs/roadmap.md)と現在のCLI契約に基づく。
テーマ差替えは[将来の改善候補](../design/tui-rendering.md#将来の改善候補テーマの差し替え)に記録されている。

## 今後の候補を選ぶときの主な差分

作業の完了方法や日常操作への影響が大きい候補は、次のとおりである。実装順序は未決定。

- Web検索と、非モデルサービスを呼ぶ認証付きHTTP。
- 部分読取り、複数編集、ファイル検索など、ファイル操作toolの充実。
- コマンドの途中出力と、大量出力の分割取得。
- 子agentの非同期操作と、呼出しごとのmodelやtool指定。
- ライフサイクルhookと、文脈追加やcheckpoint更新。
- 設定管理CLIと、runの機械向けイベント出力。
- Session削除や最新Session再開などの人間向け操作。

専用toolの不足、一般操作では代替できない機構、意図的に採用していない操作を区別し、
実際の利用で必要なものから採否を決める。
