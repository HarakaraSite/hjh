# hjhと旧Henjiの機能比較

2026-10-14更新。初回の比較に、子agent、hook、読取りtool、標準role、認証保存先、基本指示の差替え、
workspaceのモデル選択保存、CLIセッション削除、履歴保存とprovider互換性の後続改善を反映した。
通常の対話、Core、履歴、認証、コンテキスト予算管理はhjhにも揃っている。
残る差は、コマンド途中出力・分割取得、要約checkpoint、
設定管理CLIなどである。未実装の項目すべてを採用する方針ではない。

この比較は未実装機能を検討するための調査結果であり、全機能の移植を要求する要件や実装計画ではない。
採否は利用者の指定とhjhの採用済み設計に従う。

## 比較対象と判定

| 対象 | 比較時点 |
| --- | --- |
| hjh | `/home/agent/projects/hjh`。HEAD `9b99775`と、その上のinline picker・Session削除・最新Session再開の変更 |
| 更新時の未コミット変更 | inline picker・TUI/Core APIのSession削除・最新Session再開と、この比較の更新を含む。slash候補と共通base.mdはコミット済み |
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
| 完了（I-xx） | 後続のincrementで実装・検証・reviewを完了した項目。確認範囲は各結果文書に従う |
| 意図的な方式差 | hjhで採用した方式、または明示的な不要判断による差 |
| 旧側も未完成 | 旧Henjiにも完成したproduct機能がなく、未移植機能とは区別する |

「専用機能なし」は、その作業がhjhでは不可能という意味ではない。
以下はソースと採用文書に基づく比較であり、各項目の外部サービス接続を今回再検証した結果ではない。

後続更新の確認範囲は、各項目の結果文書に従う。providerの最新修正はローカルHTTP・実binaryで確認し、
外部APIの再呼出しは行っていない。

機能一覧の`No.`は、以下の表をまたぐ通し番号である。

## toolの違い

| No. | 機能 | 旧Henji | 現在のhjhと不足している点 |
| ---: | --- | --- | --- |
| 1 | Web検索 | Exaの`web_search`。URL、タイトル、抜粋、本文などを取得 | **完了（I-22）**。Exa版を外部toolへ移植し、直接/生成JS/子から実検索・結果利用・保存を確認。[確認結果](i22-exa-web-search.md) |
| 2 | tool用の認証付きHTTP | 外部toolが認証profileを指定してサービスへrequest | **完了（I-22）**。実装API requestProviderでmethod、headers、JSON body、API-key profileを扱い、GoでBearerを付ける。現行の認証方式はBearer固定。一般http toolはURL指定GET。[確認結果](i22-exa-web-search.md)・[契約](../design/web-search.md) |
| 3 | ディレクトリ一覧 | `ls`。tree表示、entry情報 | **実装済み**。lsのentry情報・tree・depthと結果範囲指定。[確認結果](read-only-tools.md) |
| 4 | ファイル検索 | `find`。glob、種類、ページ指定 | **実装済み**。findのglob・file/directory・exclude・結果範囲指定。隠し・ignored entryを含むGo探索。[確認結果](read-only-tools.md) |
| 5 | 本文検索 | `grep`。正規表現、固定文字列、周辺行、一致ファイル一覧 | **実装済み**。grepのregex/literal・大小文字・周辺行・file一覧・結果範囲指定。rg優先、なければGNU grepを使用。backendとignoreAppliedを結果に含める。[確認結果](grep-native-backends.md) |
| 6 | ファイル集計 | `wc`。行数、単語数、byte数 | **実装済み**。wcで複数fileの改行数・単語数・byte数と合計。[確認結果](read-only-tools.md) |
| 7 | Git確認 | `git_inspect`。status、diff、log、showと範囲指定 | **実装済み**。git_inspectのstatus/diff/log/show、paths・rev・staged・stat・結果範囲指定。Gitが必要。[確認結果](read-only-tools.md) |
| 8 | ファイルの部分読取り | `read`に行単位の`offset`と`limit` | **実装済み**。read_fileの1始まりoffset/limit、改行保持、行数とnextOffset。省略時は全文。[確認結果](read-only-tools.md) |
| 9 | 複数箇所の編集 | `edit`に複数の置換をまとめて指定 | **実装済み**。edit_fileのedits配列で順次置換し、一回保存。各置換件数と合計を返す。[確認結果](read-only-tools.md) |
| 10 | ファイル作成 | `write`が不足する親directoryも作成 | **実装済み**。write_fileが不足する親directoryを作成する。[確認結果](read-only-tools.md) |
| 11 | コマンドの途中出力 | 実行中のstdoutとstderrをtool進捗として通知 | **未実装**。`command`のstdoutとstderrは終了後に返る |
| 12 | 大量のコマンド出力の分割取得 | `bash_output`で保存出力をIDとoffset指定で取得 | **未実装**。現在はコマンド結果をまとめて返す |
| 13 | コマンドごとのtimeout | `bash`に`timeoutMs` | **指定機構なし**。実行取消はある |
| 14 | JSONの最終回答 | `submit_json_result`でJSONを検証し、最終回答として終了 | **専用機構なし**。JSON形式の文章を回答すること自体は可能 |

ファイル探索・本文検索・Git確認は専用toolを追加し、commandなしで子へ渡せるようにした。
findはGoのfilepath.WalkDirを使い、fdやrgの導入を必要としない。
一方、Web検索には検索サービスとの接続が必要で、HTML読取りの改善だけでは補えない。
I-22で外部toolから非モデルサービスを呼ぶ認証付きHTTP共通経路を追加した。
既存のモデルprovider認証とは別の利用経路で、同じcredential ownerを使う。

参照先：

- 旧Henji：[標準tool一覧](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/configuration/default-agent.json)、[外部tool](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/external-tools)、[ファイル操作](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/tools/file_tools.ts)、[bash出力](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/tools/bash_output.ts)、[JSON最終回答](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/tools/tools.ts)。
- hjh：[標準tool定義](../../internal/extensions/assets/config.json)、[tool実装](../../internal/extensions/assets/index.js)、[Go基礎操作](../../internal/host/host.go)。

## agentとhookとインストラクションの違い

| No. | 機能 | 旧Henji | 現在のhjhと不足している点 |
| ---: | --- | --- | --- |
| 15 | 子agentを開始して後で回収 | spawnでIDを返し、status、collect、cancelを別々に呼ぶ | **完了（I-19）**。`spawn_agent`、`agent_status`、`collect_agent`、`cancel_agent`と生成JSの対応APIを実装・確認済み。`run_agent`の同期操作も維持 |
| 16 | 子agentの並列実行 | 複数の子を開始可能 | **完了（I-19）**。複数の子を動かしたまま親が別の作業や次のモデルstepへ進める。既存の`Promise.all`による同期委譲も維持 |
| 17 | 子のmodel、provider、effort指定 | spawn時に個別指定 | **完了（I-19）**。`spawn_agent`と`run_agent`の呼出しごとのmodel/provider/effort指定、実requestと保存構成の一致を確認済み |
| 18 | 子のtool指定 | spawn時にtool一覧を指定 | **完了（I-19）**。呼出しごとのtools指定を実装・確認済み。省略はroleの一覧、空配列はtoolなし |
| 19 | 子の中間報告・最終回答と差分取得 | statusの拡張候補として検討されていた | **完了（I-19）**。保存済みassistant本文を`agent_status`のreportsで返し、after/nextCursorで差分を取得する。取得結果は親のtool結果・履歴・次requestへ残る |
| 20 | 親から実行中の子へ追加指示 | 子への制御操作 | **完了（I-19）**。`steer_agent`と対応JS API、受付・適用・未適用理由の保存、子の続行を実装・確認済み。人間による子直接操作は対象外 |
| 21 | ライフサイクルhook | runtime開始と終了、turn前後、tool前後の六種類 | **完了（I-20）**。親子共通の六hook、保存・採用後のafterTurn、終了記録とnoticeを実装・確認済み。afterToolは現行の成功時だけの契約を維持し、旧側のエラー結果加工は追加していない |
| 22 | hookによる文脈追加 | runtime開始とturn開始時に追加contextを返せる | **完了（I-20）**。runtimeStart/beforeTurnの追加文脈、予算管理、hook結果と実requestのattributionを実装・確認済み。次turnへ累積しない |
| 23 | 要約checkpointの保存と再利用 | `after_turn`から要約と対象turn境界を返し、後続会話へ渡せる | **未実装**。現在は予算内に収まる履歴を選んで送る方式 |
| 24 | tool自身が持つ利用指示 | `promptGuidelines`を有効toolから集めて共通指示へ追加 | **完了（I-21）**。任意外部toolの宣言から親・子の有効tool分だけ収集し、run_js・子操作も同じ経路を使う。snapshot・実requestへ保存。[確認結果](i21-tool-guidelines.md) |
| 25 | 基本指示全体の差替え | ユーザーの`instruction.md`で組込み基本指示を置換 | **実装済み**。`~/.config/hjh/instructions/base.md`（XDG対応）で組込み基本指示を置換。親・子・再開に適用し、role/tool/AGENTS/skillsは別に追加。原文・出典を保存し、headerと/contextに出典を表示。[確認結果](base-instructions.md) |
| 26 | 開始日時の自動注入 | 同梱hookが日時とtimezoneをモデルへ渡す | **完了（I-20）**。標準runtimeStart例がGo由来の開始日時・UTC offset・timezone表示名を文脈へ渡す。編集済みhookを自動上書きしない |

### 子agentの操作範囲

I-19で、子を開始してIDを受け取り、親が別の判断を進め、その後で状態確認・報告の差分取得・
結果回収・取消を行う操作が完了した。呼出しごとのmodel/provider/effort/tools指定と、
親から実行中の子への追加指示も完了している。

標準roleはdefault・reviewer・explorer・implementerを定義済み。
reviewer/explorerは読取りtoolとrun_js、implementerは編集・command・Web操作も使う。
run_js内部にも選択されたtoolだけを公開する。親向けrole catalogには各roleのtools一覧も載せ、
委譲先の選択とtool上書きの判断に使える。[標準roleとcatalogの確認結果](read-only-tools.md)を参照する。

非同期の子の寿命は親executionに属する。外側のJS callが終了しても子は続行し、
親execution終了時には残った子を取消・joinして終了保存を待つ。
既存のblocking `run_agent`はJS call単位の寿命を維持する。
親終了後・次turn・Core再起動後にも子を存続させる機構は対象外である。

参照先：

- 旧Henji：[子agent API](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/tools/async_agents.ts)。
- hjh：[子agent実行](../../internal/app/delegation.go)、[非同期JS接続](../../internal/jsruntime/agent_control.go)、[I-19の実装・確認結果](i19-child-agent-control.md)、[role別model設定](../design/providers.md)。

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
- hjh：[Web読取りとCoreコンテキスト管理](../design/i14-web-context-contract.md)、[六hookの契約](../design/extensions.md#i-20execution-lifecycle-hooks)、[I-20の完了結果](i20-lifecycle-hooks.md)。

インストラクションの比較元は、旧Henjiの[組立処理](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/instructions/compose.ts)と[基本指示の差替え](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/instructions/base_instruction.ts)、hjhの[指示組立](../../internal/extensions/extensions.go)と[workspace指示とskills](../design/workspace-instructions-skills.md)である。

## TUIとセッション操作の違い

| No. | 機能 | 旧Henji | 現在のhjhと不足している点 |
| ---: | --- | --- | --- |
| 27 | セッション削除 | CLI、Session picker、Core操作にある | **実装済み**。CLIのCore停止後ローカル削除に加え、Session pickerのDel→EnterとCore DELETE APIで削除できる。実行中は停止後に削除する。子を含む実行履歴・関連記録、参照されなくなった本文・構成もtransactionで削除する。[CLI確認結果](cli-session-delete.md)・[picker確認結果](inline-pickers.md) |
| 28 | 最新セッションへ簡単に再開 | `--continue`、`/resume latest` | **実装済み**。`hjh-tui --continue`・`/resume latest`で同じworkspaceの最新Sessionを再開する。実行中なら同runへattach。`history --latest`は読取り表示だけ。[確認結果](latest-session-resume.md) |
| 29 | 永続Sessionを作らない対話 | `--no-session` | **未実装**。永続Sessionを作らないモードがない。旧側の指定も全実行証拠の非保存を意味するものではない |
| 30 | 読取り専用の別セッション閲覧 | `/view`、pickerで閲覧と再開を分離 | **意図的に未採用**。以前の利用者指定どおり、hjhには追加していない |
| 31 | TUI内の会話browser | 項目選択、前後ページ、本文chunk表示 | **未実装**。現在は端末やtmuxのscrollbackと`/history`を使う |
| 32 | tool本文の詳細閲覧 | browserから選択して分割閲覧 | **一部対応**。`/tools`と`/tool`はあるが、専用browserや本文の分割閲覧はない |
| 33 | slash command候補 | `/`入力から絞り込みpickerを表示 | **実装済み**。`/`で入力欄の上に名前・引数hint・説明を表示し、文字で絞込・上下で選択。Tabは補完、Enterは実行または必須引数入力、Escはdraftを保持して候補だけ閉じる。Pi/OpenCodeを参考に、会話履歴へ残さないinline表示を採用。[確認結果](slash-command-menu.md) |
| 34 | modelとeffort選択の即時保存 | idle時の選択をSessionと新規Session用の既定へ保存 | **実装済み（workspace単位）**。provider/model/effort/authProfile参照を選択時に`.hjh/selection.json`へ保存し、実行せず終了・TUI/Core再起動後も同じworkspaceの新規Sessionで使う。既存Sessionの保存選択は正常run採用時に更新し、再開時はその保存値を使う。[確認結果](remembered-model-selection.md) |
| 35 | Core IDでTUI接続 | TUIに`--core ID` | **入口なし**。現在は`--endpoint URL`。CLIの`core --core ID`はある |

`/view`は実装漏れではなく、[TUIの採用方針](../design/tui.md)に不要という判断が記録されている。
会話browserの不足も、会話の検索やコピーができないという意味ではない。
両製品とも検索とコピーには端末やtmuxのscrollbackを使う。

また、hjhでは会話全文のviewやturn範囲の読取りはできるが、旧会話browser用の前後ページと
本文chunkの読取りに対応する操作はない。この比較だけでは、実際の長大Sessionでの性能差までは判断しない。

TUIのeffortは`/effort`のpickerからautoとモデルの確認済み候補だけを選ぶ。
直接`/effort VALUE`指定と、候補外の保存値を選択肢へ加える方式は利用者指定で廃止した。
モデルを変更したときはeffortをautoへ戻し、同じモデルの再選択では維持する。
OpenCode Goの登録済み25モデルの候補を公式定義から設定した。GLM-5.3はlow/high/max、
MiMo V2.6 Proはautoのみ。Goのmodel一覧APIの応答にeffort情報はないため、現状はconfigで宣言する。
[effortの確認結果](i08-connection-selection.md#2026-10-13effortはpickerから選択する)を参照する。

参照先：

- 旧Henji：[TUIコマンド](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/tui/slash_command.ts)、[会話browser](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/tui/conversation_browser.ts)、[TUI操作実装](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/tui/remote_session.ts)、[選択の既定保存](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/provider/default_selection.ts)。
- hjh：[TUIコマンド](../../internal/tui/commands.go)、[Session操作](../../internal/tui/session_management.go)、[次runのmodel選択](../../internal/tui/connection_picker.go)、[TUI起動option](../../cmd/hjh-tui/main.go)、[Core HTTP API](../../internal/httpapi/server.go)。

## CLIと診断と配布の違い

| No. | 機能 | 旧Henji | 現在のhjhと不足している点 |
| ---: | --- | --- | --- |
| 36 | runの機械向けイベント出力 | `run --json`でNDJSON | **未実装**。現在は最終回答か`--stream`。sessions/history等の`--json`は実行イベントstreamではない |
| 37 | agent設定の登録と解除 | `agent activate/deactivate`、個別inspect | **未実装**。`roles`の一覧と`--role`選択はあるが、登録と解除のCLIはない |
| 38 | toolの登録と解除とinspect | `tool activate/deactivate/list/inspect` | **未実装**。現在はconfigとJSを編集する |
| 39 | workspace全体の実行一覧CLI | `diagnostics executions list` | **未実装**。TUIの`/executions`やSessionのhistoryから参照する |
| 40 | 失敗診断だけの一覧と最新と表示と削除 | `diagnostics list/latest/show/delete` | **専用入口なし**。失敗や実行記録は`inspect`等で確認可能 |
| 41 | request単位の診断 | request番号から送信内容、HTTP status、parser失敗箇所を取得 | **一部対応**。送信内容、応答結果、usage、失敗記録はあるが、旧方式の短いrequest factと専用CLIはない |
| 42 | runtime情報のまとめ表示 | `diagnostics runtime`でbuildとworkspaceや設定などのpathを表示 | **専用入口なし**。version、Core情報、保存構成などに情報が分かれている |
| 43 | provider requestのtimeout指定 | `--provider-timeout-ms` | **指定機構なし** |
| 44 | インストーラ | binary、外部tool、hookを配置と登録。既存編集を保持する再配置optionもある | **未実装**。配布archiveはあるが、配置と設定登録は手動 |

実行の診断材料そのものがないわけではない。
hjhの`inspect`はexecution、子実行、messages、records、保存構成、contextを返す。
差は、失敗だけを探す操作やrequest単位で整理した参照入口と、診断factの粒度にある。

参照先：

- 旧Henji：[CLI契約](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/cli/cli_help.ts)、[設定管理CLI](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/cli/configuration_cli.ts)、[診断CLI](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/cli/failure_diagnostic_cli.ts)、[provider fact](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/provider/provider_evidence.ts)、[runtime情報CLI](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/v0/agent/cli/henji_cli.ts)、[インストーラ](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/scripts/install_henji.sh)。
- hjh：[CLI契約](../../cmd/hjh/help.go)、[CLI実装](../../cmd/hjh/main.go)、[inspection](../../internal/app/service.go)、[provider実行](../../internal/provider/protocol/generate.go)、[配布生成](../../scripts/package-linux.py)、[導入手順](../distribution-linux.md)。

## hjhにも実装済みの機能

| No. | 分野 | 現在のhjh | 参照先 |
| ---: | --- | --- | --- |
| 45 | 通常実行 | CLIとTUI、stream、公開thinkingとsummary、tool進捗、最終回答。標準接続のthinking抽出・reasoning再送設定を整備 | [runとstream](run-stream.md)、[TUI](../design/tui.md)、[providerの後続修正](i04-provider-model-switching.md) |
| 46 | Core | 未起動時の自動起動、既存Core利用、複数Session、detach、再接続、停止 | [Core起動](i13-core-startup.md)、[Session管理](i07-session-management.md) |
| 47 | CLI管理 | `core list/status/stop`、`--version` | [CLI管理とhistory](i15-cli-management-history.md) |
| 48 | CLIからCoreへ依頼 | `run --connect URL`、後からTUIで接続 | [Core接続run](i16-cli-core-run.md) |
| 49 | セッション | 新規、一覧、短縮ID、再開、rename | [Session管理](i07-session-management.md)、[短縮ID](short-session-ids.md) |
| 50 | 履歴 | session、canonical、detail、ローカルとCore経由、保存構成と送信contextの確認 | [CLI管理とhistory](i15-cli-management-history.md)、[context](i10-context-recall.md) |
| 51 | 実行中の入力 | F1取消、F2予約、F3追加指示、受付結果確認 | [実行中の入力](i09-runtime-input.md) |
| 52 | 停止した作業の引継ぎ | `/recall` | [contextとrecall](i10-context-recall.md) |
| 53 | 接続選択 | provider、model、effort picker、お気に入り、workspaceごとの即時保存と新規Sessionへの引継ぎ、モデル変更時のeffortリセット | [接続選択](i08-connection-selection.md)、[選択保存](remembered-model-selection.md)、[お気に入り](i11-model-favorites.md) |
| 54 | 認証 | APIキー、ChatGPTの追加と再認証と複数accountの選択機構。既定保存先は`~/.config/hjh/credentials/`（XDG_CONFIG_HOME対応）、モデルとtoolの認証は同じManagerを使用 | [認証UI](i12-authentication-ui.md)、[保存先集約](credential-config-directory.md) |
| 55 | 指示とskills | ユーザー共通base.mdによる基本指示差替え、共通・祖先・workspaceのAGENTS.md、role指示、起動・各親子開始時のskill一覧、利用時の本文読取り。`~/.config/hjh/skills/`と`workspace/.hjh/skills/`を探索 | [基本指示](base-instructions.md)、[workspace指示とskills](../design/workspace-instructions-skills.md) |
| 56 | Web読取り | HTML可読化、1MiB上限、`save_to` | [Webと予算管理](i14-web-context.md) |
| 57 | Web検索 | Exa auto/highlights、各option、直接/生成JS/子の利用、認証付きHTTPと保存 | [I-22の完了結果](i22-exa-web-search.md) |
| 58 | コンテキスト予算 | ターン内、ターン間、Session再開、モデル変更、子agent | [予算契約](../design/i14-web-context-contract.md) |
| 59 | 子agent | 非同期開始・並列実行・状態確認・回収・取消、model/provider/effort/tools指定、報告差分、親からの追加指示 | [I-19の完了結果](i19-child-agent-control.md) |
| 60 | 外部拡張 | role、tool、六種類のhook、開始文脈、実行開始時のメモリ上のsource固定、次実行への編集反映。実装ソース一式のDB自動保存は停止 | [外部拡張](../design/extensions.md)、[hookの完了結果](i20-lifecycle-hooks.md)、[ソース保存停止](initial-run.md#2026-10-13追補実装ソースの自動保存を停止) |
| 61 | 表示 | ヘッダー、三行フッター、Markdown、途中のassistant本文、配色、run_js目的コメントと内側のtoolの階層表示。子の詳細は通常の親画面から省き、保存履歴は保持 | [ヘッダー](../design/tui-header.md)、[フッター](../design/tui-footer.md)、[Markdownと色](../design/tui-rendering.md)、[run_js目的表示](run-js-purpose.md) |

認証の複数account機構は実装済みであるが、今回の比較で追加accountのブラウザ認証を
実施したという意味ではない。過去の実確認範囲は認証UIの結果文書に従う。

標準provider定義では、OpenCode Go Chatのreasoning_content抽出と再送、
OpenAI API・OpenCode Go Responsesのreasoning再送の設定漏れを修正した。
OpenAI Responsesのsummaryは従来からautoで要求・受信・表示・保存する。
公開thinkingと、次requestへ戻すreasoning stateは別に扱う。

ツール結果は旧Henji同様、Chat・Responsesとも余分なnameを一律省き、call IDで対応付ける。
GLM-5.3-FlashのGo経路で観測したHTTP 400への修正であり、モデル別の例外設定は設けない。
ツール宣言・呼出し・DB履歴の名前は保持する。[providerの確認結果](i04-provider-model-switching.md)を参照する。

DBは引き続き実行・メッセージ・詳細記録・使用構成とcontentsを持つ。
セッション削除では関連履歴に加え、他の記録から参照されなくなった本文・構成も回収する。
実行中の追加指示は受付本文と適用後のメッセージを独立に保存する。
ツール・hook・依存ファイル・元configのソース一式は自動保存しないが、AGENTS.md原文、
使用指示・ツール定義、モデル送信内容、ツール呼出し・結果、外部base.mdの原文・出典は保存する。
共有本文構造全体の廃止と、DBファイルの縮小は未対応。[保存契約](../design/history-schema.md)を参照する。

## 方式差と旧側も未完成の機能

| No. | 項目 | 比較上の扱い |
| ---: | --- | --- |
| 62 | TypeScriptとDenoの実行環境 | **意図的な方式差**。hjhはQuickJSと準備済みESMを採用した。生成JSでの処理、tool合成、結果利用は可能だが、Deno APIやDeno stdの実行時取得を再現しない |
| 63 | 専用skill toolと旧探索directory | **意図的な方式差**。hjhはPi方式と合意済みの探索場所を採用した。旧側の本文snapshot方式や`.zot`、`.claude`の探索をそのまま移植する要求ではない |
| 64 | TUIの既定Core起動 | **意図的な方式差**。旧側は通常起動ごとに新Core。hjhは既定endpointのCoreを再利用し、必要なら起動する。別portで複数Coreを動かすことも可能 |
| 65 | CLIとTUIの実行ファイル | **意図的な構成差**。旧側は一つのbinaryからTUIを起動。hjhは`hjh`と`hjh-tui`を分離している |
| 66 | Web UI | **旧側も未完成**。旧CLIに予約された名前があるだけで、完成した旧機能の未移植ではない |
| 67 | durable AgentInstanceとInstance単位のSession管理 | **旧側も未完成**。Session継続やCoreの複数Session実行と区別する |
| 68 | agentが自身の実行と構成を発見する専用API | **旧側も未完成**。履歴やCLIによる間接参照と、専用product操作は別 |
| 69 | 専用experienceとcandidate管理 | **旧側も未完成**。既存履歴やrepositoryで改善を進める運用はあるが、完成した一式の機構はない |
| 70 | 標準の自動要約 | **旧側も未完成**。旧側にはcheckpoint基盤があるが、標準の要約処理と閾値は未実装 |
| 71 | managed resourceのexportとimport | **旧側で廃止**。現行の移植残件として扱わない |
| 72 | 専用rebuild | **旧側で不採用**。新実行や再開時の現在設定読込みは両側にある |
| 73 | agentによる後続rootのmodel変更 | **旧側で見送り**。人間の選択や子開始時のmodel指定と区別する |
| 74 | Markdownとそれ以外のテーマ差替え | **hjhの将来候補**。固定テーマは実装済みで、独立した差替え機構はメモ済み。旧機能の移植とは別 |

hjhの方式は[初期製品の採用方針](../design/initial-product.md)、[workspace指示とskills](../design/workspace-instructions-skills.md)、[Core起動](i13-core-startup.md)を参照する。
旧側の未完成、廃止、不採用項目は[旧roadmap](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/docs/roadmap.md)と現在のCLI契約に基づく。
テーマ差替えは[将来の改善候補](../design/tui-rendering.md#将来の改善候補テーマの差し替え)に記録されている。

## 今後の候補を選ぶときの主な差分

作業の完了方法や日常操作への影響が大きい候補は、次のとおりである。実装順序は未決定。

- コマンドの途中出力と、大量出力の分割取得。
- 会話の要約checkpoint保存・再利用（利用者指定により、まだ実装しない）。
- 設定管理CLI、runの機械向けイベント出力。
- 会話browserなどの人間向け操作。Session削除と最新Session再開は実装済み。

専用toolの不足、一般操作では代替できない機構、意図的に採用していない操作を区別し、
実際の利用で必要なものから採否を決める。
