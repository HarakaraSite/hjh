# 開発者向けメモ

外部利用者向けの入口は[英語README](../README.md)で、日本語版は[README.ja.md](../README.ja.md)。
英語版を正本とし、利用手順・機能説明を更新するときは日本語版にも同じ変更を反映する。

この文書は旧READMEから移した開発経緯、個人の利用環境、運用詳細、prototypeの手動切替と
検証記録への参照をまとめる。incrementの記載は各段階の記録であり、最新の操作契約は
対応する設計文書を参照する。以下のコマンドはrepository rootで実行する。

## 開発と配布

- 作業規則と正本文書への入口：[AGENTS.md](../AGENTS.md)。
- 製品方針・初期範囲：[initial-product](design/initial-product.md)。
- 配布用の既定設定：[config.json](../internal/extensions/assets/config.json)。
  利用中workspaceの`.hjh/extensions/config.json`とは別で、編集済みの利用設定は自動上書きしない。
- 実装計画は`docs/plans/`、完了結果は`docs/results/`、技術実験は`docs/spikes/`。

```sh
go build -o dist/hjh ./cmd/hjh
go build -o dist/hjh-tui ./cmd/hjh-tui
python3 scripts/package-linux.py
```

配布scriptはLinux/amd64静的binary、外部JS、依存license、manifestを`dist/packages/`へ作る。
Python 3とreadelfも使う。[配布物の使い方](distribution-linux.md)を参照する。

変更箇所のfocused test、必要なvet、formatとdiff checkで確認する。外部拡張変更時の例：

```sh
go test ./internal/extensions
go vet ./internal/extensions
git diff --check
```

実provider確認は対応する利用経路で行い、API費用が発生する実行は利用者の承認範囲に従う。
全体test・gateの扱いもAGENTS.mdに従い、途中確認で繰り返さない。

## 直近の実装と検討メモ

- 子agentの開始・並列実行・報告差分・追加指示：[I-19結果](results/i19-child-agent-control.md)。
- ライフサイクルhook：[I-20結果](results/i20-lifecycle-hooks.md)。要約checkpointは未実装。
- tool自身の利用指示：[I-21結果](results/i21-tool-guidelines.md)。
- Exa Web検索とResponses任意引数の修正：[I-22結果](results/i22-exa-web-search.md)。
- 標準toolの引数・省略値・戻り値の説明改善：[確認結果](results/tool-definition-guidance.md)。
- run_jsと内側toolの字下げ：[確認結果](results/tui-tool-nesting.md)。
- Session・接続pickerの1項目1行表示とTUI/CoreのSession削除：[確認結果](results/inline-pickers.md)。
- APIキー・ChatGPT認証情報のユーザー設定配下への集約：[確認結果](results/credential-config-directory.md)。
- 同じworkspaceの新規Sessionへのモデル選択の引き継ぎ：[確認結果](results/remembered-model-selection.md)。
- rg未導入時のGNU grep利用：[確認結果](results/grep-native-backends.md)。
- Core停止後のCLIセッション削除：[確認結果](results/cli-session-delete.md)。
- 通常履歴の保存整理：[保存契約](design/normal-history-storage.md)・[容量と総合確認](results/normal-history-storage.md)。
- 今後の検討： [使い方辞書と遅延読込み](design/extensions.md#将来の改善候補ツールの使い方辞書と遅延読込み)、
  [DB容量](design/history-schema.md#将来の改善候補db容量の最適化)、
  [テーマ差替え](design/tui-rendering.md#将来の改善候補テーマの差し替え)。

以下は旧READMEにあった経緯と詳細。旧prototypeの切替記述は当時の記録であり、
新規利用の手順は現在の英語README、詳細な契約は各design文書に従う。

## 開発経緯と確認済みの範囲

Henjiの構想・機能を引き継ぎ、Goの特性を活かして作る後継Agent Harness。
現行Deno版の完全移植を目指すものではなく、既存コードの再利用にも拘らない。

Go＋QuickJS、複数provider、Linux/amd64向けの非対話runとTUIを実装した。
DeepSeek v4.1 Flashでファイル作業・Session再開・HTTP・command、tool／hook編集を確認した。
[初期製品の方針・範囲・実装順序](design/initial-product.md)と
[旧henji-harnessと同一の履歴schema](design/history-schema.md)を正本とする。
具体的な実装手順と各段階の完成条件は[非対話runの実装計画](plans/initial-run.md)に記載した。
[実装・実モデル・配布・reviewの結果](results/initial-run.md)に確認した環境と実行記録をまとめた。
以後の作業単位はインクリメントとする。I-01は非対話run・stream・履歴・再開・最初のTUIまでの
基盤プロトタイプで、利用者の操作確認を完了した。
[I-02「子Agentへの委譲」](plans/i02-child-agents.md)を実装し、実OpenRouterでCLI・TUI・
Linux配布物の委譲、結果利用、停止、親子履歴を確認した。
[実装・確認・reviewの結果](results/i02-child-agents.md)を参照する。利用者も子Agent呼出しを確認済み。
[I-03計画](plans/i03-generic-roles.md)に沿って、親子共通のbuilt-in genericへ外部roleとtool構成を渡す操作を実装した。
[実装・確認・reviewの結果](results/i03-generic-roles.md)に実provider・PTY・配布物と指摘修正の記録をまとめた。
Coreからコンテナ・OS仮想マシンへAgentを配置する案は[将来構想](design/future-agent-environments.md)として記録した。
I-05は「コアとTUIの分離」。`hjh serve`と独立した`hjh-tui`を実装し、HTTP＋JSON＋SSEで通信する。
[計画](plans/i05-core-tui-separation.md)と[HTTP API契約](design/http-api.md)に対応し、実provider・独立TUI・Linux配布物の確認と通常/批判的reviewを完了した。
[実装・確認・reviewの結果](results/i05-core-tui-separation.md)を参照する。
I-06で通常画面の会話TUIと、それを支えるCore会話経路を実装し、実provider・private tmux・通常／批判的reviewを確認した。
HTTP契約は`/api/v2`へ更新した。初回切替ではCoreとTUIの両方を再起動する。
その後、利用者が確認したMarkdown本文の分断・欠落・重複をTUIで修正した。
追加の表示修正では、色を後回しにして原文記号を保ち、ツールを短い行へ分離し、IMEの実cursorを修正した。
`/tools`と`/tool NUMBER_OR_ENTITY_ID`で保存済み結果の詳細を読める。この修正はCore／TUIの両方を再起動する。
利用者が表示順を確認した後、toolの代理実行による二重表示を修正し、公開本文を`thinking`、
公開要約を`thinking summary`として保存・表示する経路を実装した。Responsesは要約も要求する。
実OpenRouterでthinkingの途中表示とtool一行を確認した。公開summaryは実providerで未観測である。
Ghostty → SSH → tmuxを利用経路とし、[計画](plans/i06-conversation-tui.md)と
[S-06スパイクの結果](results/s06-tui-core.md)に旧Data Worker・参照実装の調査を、
[I-06の結果](results/i06-conversation-tui.md)に保存量・保持メモリ・表示・実利用の確認範囲を記録した。
I-07ではSession選択・命名、detach／再接続、Core停止を実装した。
[結果](results/i07-session-management.md)に実利用経路とreviewを記録した。
I-08ではprovider／model／effort pickerとmodel検索を追加し、5接続先の実run・保存・再開を確認した。
[結果](results/i08-connection-selection.md)に実接続・通常／批判的review・配布物の確認を記録した。
I-09では実行中のF2予約、F3追加入力、予約列のF1停止とreceiptの参照を接続した。
[契約](design/i09-runtime-input-contract.md)と[結果](results/i09-runtime-input.md)を参照する。
I-10では`/context`で入力準備と引継ぎ状態を確認し、`/recall`で停止した実行の公開情報を次の依頼へ渡す。
[契約](design/i10-context-recall-contract.md)と[結果](results/i10-context-recall.md)を参照する。
I-12では`/login`からAPIキー登録とChatGPT account選択へ進める。保存済み認証を再利用し、
登録・選択の変更は次runへ反映する。[契約](design/i12-authentication-contract.md)と
[確認結果](results/i12-authentication-ui.md)を参照する。
I-13ではTUIから同じVMの未起動Coreを起動する。起動済みCoreは再利用し、detach後もCoreは継続する。
[計画](plans/i13-core-startup.md)と[確認結果](results/i13-core-startup.md)を参照する。
I-14ではWeb読取り用`web_fetch`とCore内のコンテキスト予算管理を追加した。
ターン内・次ターン・再開・モデル変更・子Agentに適用し、保存全文と送信内容を分ける。
[契約と設定](design/i14-web-context-contract.md)を参照する。
[実動作と確認範囲](results/i14-web-context.md)を参照する。
I-15では`core list/status/stop`、`--version`、3表示の`history` CLIを整備した。
[操作と確認方針](plans/i15-cli-management-history.md)を参照する。
[実動作と配布物の確認結果](results/i15-cli-management-history.md)を参照する。
I-16では`run --connect URL`でCLIから既存Coreへ依頼し、同じSessionをTUIで開ける。
[操作と確認方針](plans/i16-cli-core-run.md)を参照する。
[実Core/TUIと配布物の確認結果](results/i16-cli-core-run.md)を参照する。
I-17ではJS実行基盤を`github.com/go-quickjs/go-quickjs v0.25.2`へ移行した。
Worker APIと既存の終了保存・非同期処理を維持する。[移行計画](plans/i17-go-quickjs.md)と
[互換性・実作業・配布の確認結果](results/i17-go-quickjs.md)を参照する。
人向けSession IDはTUI・CLIで原則8文字の短縮形を使い、そのまま再開・historyへ指定できる。
`sessions --json`やAPI・保存履歴は完全IDを保つ。[操作契約](design/session-ids.md)と
[実CLI/TUIの確認結果](results/short-session-ids.md)を参照する。
TUI headerは会話を開いた時点の情報を枠で一度表示し、現在SessionのID・タイトルはfooterへ表示する。
[表示契約](design/tui-header.md)と[確認結果](results/tui-header.md)を参照する。

I-18ではAGENTS.mdとPi方式のskillsを共通実行層へ接続した。
指示とskill一覧は各親/子開始時に読み、skill本文は必要になった時にread_fileで取得する。
Coreのorientation APIからTUIのheaderと/contextへ出典・一覧を表示する。
[読込みと表示の契約](design/workspace-instructions-skills.md)を参照する。
[実装・実モデル・配布の確認結果](results/i18-workspace-instructions-skills.md)を参照する。
I-22では旧HenjiのExa版`web_search`を移植した。直接、生成JS、選択した子Agentから検索できる。
既定はauto検索＋抜粋。APIキーは既存のExa credential fileまたはEXA_API_KEYをGo側で解決する。
[検索と認証の契約](design/web-search.md)と[実Exa・CLI/Core/TUIの確認結果](results/i22-exa-web-search.md)を参照する。
S-01〜S-05スパイクは実施済み。[実験コード](../spikes/s01/README.md)、
[S-01の結果](spikes/s01-results.md)、[S-02の結果](spikes/s02-results.md)、
[S-03の結果](spikes/s03-results.md)、[S-04の結果](spikes/s04-results.md)、
[S-05の結果](spikes/s05-results.md)を参照する。

## 日常利用・動作確認用コマンド

ソースからは `go build -o dist/hjh ./cmd/hjh` でコアを、
`go build -o dist/hjh-tui ./cmd/hjh-tui` でTUIをビルドする。
Linux静的配布物は `python3 scripts/package-linux.py` で `dist/packages/` に作成できる。
配布先ではGo・Node・Deno・curlを必要としない。[配布物の使い方](distribution-linux.md)を参照する。

接続は外部 `.hjh/extensions/config.json` の `providers` で指定する。
標準でOpenRouter Responses、OpenCode Go Chat／Responses、OpenAI API Responses、ChatGPT OAuthを用意した。
APIキーは `/login` の専用入力欄から登録できる。解決順は明示 `--credential-file`、登録キー、profileの環境変数、file。
標準のfile参照は旧Henjiと同じ保存先。`--credential-file PATH` は選択profileのfileを明示して使う。
APIキーを設定JSONへ書かない。[provider設定と認証](design/providers.md)に完全な形式とログイン操作を記載した。

```sh
./dist/hjh run --model deepseek/deepseek-v4.1-flash --workspace /path/to/work 'input.txtを読み、加工してoutput.txtへ書いて確認して'
./dist/hjh run --stream --model deepseek/deepseek-v4.1-flash --workspace /path/to/work 'input.txtを読み、結果を説明して'
./dist/hjh sessions --workspace /path/to/work
./dist/hjh history --workspace /path/to/work --session SESSION_ID
./dist/hjh history --workspace /path/to/work --latest --view canonical
./dist/hjh history --connect http://127.0.0.1:8765 --session SESSION_ID_PREFIX --view detail
./dist/hjh inspect --workspace /path/to/work --execution EXECUTION_ID --from-ordinal 0 --to-ordinal 20
./dist/hjh run --workspace /path/to/work --session SESSION_ID '前回のoutput.txtを追加編集して'
./dist/hjh run --connect http://127.0.0.1:8765 'READMEを要約して' > summary.txt
./dist/hjh-tui --endpoint http://127.0.0.1:8765 --session SESSION_ID
```

flagは依頼文より前に指定する。依頼文を省略するとstdinから読む。
`history`は既定で最新Sessionの会話をテキスト表示する。`--view session`は失敗・取消・子を含む
会話、`canonical`は採用会話のMarkdown、`detail`は保存実行証拠のNDJSON。
detailのrecord contentはbase64で、保存されたバイナリも復元できる。
`--session`は完全IDまたは一意な短縮ID。`--json`で採用messageのJSON配列を出す。
最新選択はworkspace内に絞り、`--connect`省略時はローカルDBをread-onlyで読む。
履歴を読むだけではSessionの実行・再開は行わない。
`run`のstdoutは最終回答、stderrは進捗・Session ID・execution ID。Ctrl-Cで停止する。
`--stream` はモデル本文を受信しながらstdoutへ表示し、tool進捗はstderrへ出す。
最終回答を重複表示せず、停止・失敗でも表示済みの途中本文は残す。
成功0、失敗1、取消130。失敗・取消の証拠は残し、正常完了したturnだけを再開会話へ採用する。
`run --connect URL`は起動済みCoreへ依頼し、stderrへCore/Run/Session/Execution IDを出す。
同じCoreのTUIでそのSessionを開くと、実行中なら接続して追加指示・取消、完了後なら続行できる。
接続runもstdout/stderrと`--stream`の使い方は同じ。Ctrl-CはCoreへ取消を送り、終了保存を待つ。
CLIの通信切断や強制終了ではCoreの作業は続く。workspace省略はCore既定、明示相対pathはCore基準。
DB/config/credential-file/credential-state-dir/model-favorites-dirはCoreの起動設定を使う。
`--session`による続行はそのSessionの保存workspaceを使う。max-steps/from-turn省略はCore既定値を使う。

子Agentへ調査を委譲する場合も同じrun・TUIから依頼する。

```sh
./dist/hjh run --stream --model deepseek/deepseek-v4.1-flash --workspace /path/to/work '生成JSのapi.agents.runを使い、srcとdocsの調査を2つの子Agentへ並行委譲して。結果をまとめてreport.txtに書き、読み返して報告して'
```

run_agentを選んだroleでは、生成JSから `await api.agents.run({role: "reviewer", task: "調査内容と必要な文脈"})` を使える。
外部tool／hook実装の同名APIは基礎委譲操作へ接続する。
並行委譲は `Promise.all`。子は独立した会話とVMで実行し、結果にexecution IDと終了状態を返す。
子の進捗はID付きで表示する。TUIでは親子を区別して会話順に示し、CLIの親stdoutへ子本文を混ぜない。子の実行証拠はinspectから閲覧できる。
子の会話をSessionへ直接採用せず、親の正常完了turnだけを採用する。

TUIを起動すると、同じVMのCoreへ接続し、未起動なら独立したCoreを起動する。

```sh
./dist/hjh-tui --workspace /path/to/work
./dist/hjh-tui --session SESSION_ID
./dist/hjh-tui --continue
```

`--continue`は同じworkspaceの最新Sessionを再開する。TUI内では`/resume latest`を使える。
最新は最終更新日時で選び、実行中なら同runへ再接続する。履歴がなければ起動時は新規会話に入る。
`history --latest`は履歴表示だけの操作。[契約](design/latest-session-resume.md)を参照。

既定の接続先は`http://127.0.0.1:8765`。Coreが起動済みなら再利用する。
起動するCoreの`--db`・`--config`・`--credential-file`・`--credential-state-dir`・
`--model-favorites-dir`もTUI起動時に指定できる。新規Coreのworkspaceは現在directoryが既定。
起動済みCoreへの接続では、TUIの`--workspace`省略時にそのCoreの既定workspaceを使う。
Coreの起動logは実効DBと同じdirectoryの`core.log`へ追記する。
配布物の`hjh`と`hjh-tui`を同じdirectoryに置く。別配置では`--core-binary PATH`を指定する。

`--endpoint URL`を明示した場合は接続だけを行う。同じVMの別portでCoreも起動する場合は
`--endpoint http://127.0.0.1:PORT --start-core`を指定する。
Coreを別端末で起動する既存の`hjh serve ...`も使える。

```sh
./dist/hjh --version
./dist/hjh-tui --version
./dist/hjh core list
./dist/hjh core status --core CORE_ID_PREFIX
./dist/hjh core stop --connect http://127.0.0.1:8765
```

`core list`は同じVM・ユーザーの登録を表示し、`--workspace PATH`で絞れる。
`status`と`stop`は`--core ID_OR_PREFIX`または`--connect URL`で選ぶ。
対象省略時は一覧表示し、Coreを停止しない。問合せはCoreを起動しない。
`status`は準備中を含む実行中runを表示し、`stop`は終了保存と停止を待つ。
登録先は`$XDG_STATE_HOME/hjh/cores/`または`~/.local/state/hjh/cores/`。
通常終了時に自身の登録を外す。異常終了の記録は到達不能として残る。
I-15以前から稼働しているCoreは再起動後に登録・新しい状態APIが有効になる。

通常画面に会話を出し、端末／tmuxのscrollbackで検索・コピーする。
Enterで依頼、Alt／Shift／Ctrl+EnterまたはCtrl+Jで改行。
`/`入力でcommand候補を表示し、文字で絞込・上下で選択・Tabで補完・Enterで確定する。
引数必須のcommandは名前補完後に入力を待つ。Escは入力を保持して候補だけ閉じる。
[操作契約](design/slash-command-menu.md)を参照する。
実行中のEnterはdraftを保持する。受付可能な期間はF2で次run予約、F3で現在の親runへ指示を追加する。
F1で現在runの予約列をキャンセルし、Ctrl+Cで入力を消す。`/inputs`で受付・適用・予約の状態を読む。
停止後は`/recall [ID|latest]`で参考情報を準備し、`/context`でsourceと次回選択を確認して新しい依頼を送る。
`/recall clear`で解除する。引継ぎは次の通常rootの開始保存時に一度消費し、完了済みtoolを自動再実行しない。
detach中も準備はCoreに残る。Coreを再起動した場合は再準備する。
`/detach`／Ctrl+DでTUIを閉じても、Coreと実行中runは継続する。切断時に表示されたSession IDを
`--session ID`／`/resume ID`で開くと、その実行中runへ再接続する。
`/quit`／Ctrl+QはCoreを停止し、全runの取消と保存完了を待ってTUIも終了する。
Coreのserve側Ctrl-C／SIGTERMでも同じ終了保存を待つ。
DB/config/credential-file/credential-state-dirはCoreの起動設定で、TUI起動時の指定は新しく起動するCoreへ渡す。
既存Coreはその起動設定を使う。TUIのworkspace指定もCore上のpathを指す。
F4／`/sessions`でSessionを選び、`/rename 名前`で現在Sessionを命名する。
`/new`で新規会話、`/resume ID`で履歴または実行中runを再開する。
`/history [開始turn 終了turn]` は採用会話、`/executions` と
`/inspect ID [開始ordinal 終了ordinal]` は失敗・取消を含む実行証拠を表示する。
`/provider`・`/model`・`/effort`で候補を選び、`/model`ではID／表示名を検索できる。
`/model`のTabでお気に入りを登録・解除する。保存確認後に印が付き、一覧の先頭へ並ぶ。
お気に入りは同じ実効config・providerを使うSession間で共有し、Core再起動後も残る。
保存先は`$XDG_STATE_HOME/hjh/model-favorites/`または`~/.local/state/hjh/model-favorites/`。
Coreの`--model-favorites-dir PATH`で保存先だけを変更でき、credentialの保存先には影響しない。
一覧確認と選択は`/provider`・`/model`のpickerにまとめる。上下で選びEnterで確定、Escで閉じる。
`/provider ID`・`/model ID`の直接指定も使える。effortは`/effort`のpickerから選ぶ。
`/roles` はrole一覧、`/role NAME` は次の実行のrole選択、
`/role` は選択状態を表示する。実行中の選択変更は次runから反映する。成功rootのmodelとroleはSessionへ保存される。
画面内の `/help` と[操作・責務](design/tui.md)に詳細を記載する。

既定DBはworkspace内 `.hjh/history.sqlite3`、外部tool・hookは `.hjh/extensions/`。

履歴DBはWAL、同期はFULL。稼働中は`history.sqlite3-wal`と`history.sqlite3-shm`をSQLiteが管理する。
手作業でDBをバックアップするときはCore/run/history等の全接続を閉じ、
DB本体と残存する`-wal`を同じbasenameの対応で一緒にコピーする。
読取り接続が最後に閉じる場合など、正常終了後でもWALに最新記録が残ることがある。
履歴読取りはDB本体を直接コピーせず、`hjh history`等のSQLite経路を使う。
`index.js`・`hooks.js`・`config.json` を編集すると次のrunから反映される。
設定の `model` は `{"provider":"openrouter-responses","modelId":"deepseek/deepseek-v4.1-flash","effort":"auto"}` のように指定する。
`~/.config/hjh/instructions/base.md`があれば組込み基本指示を置き換える。
XDG_CONFIG_HOME指定時はその配下の`hjh/instructions/base.md`を使う。
role/tool指示・AGENTS.md・skillsは別に組み合わせ、編集は次の親ターン・再開・子開始から反映する。
[基本指示の契約](design/base-instructions.md)を参照する。
別DBや配布設定を使う場合は `--db PATH`・`--config PATH` を指定する。
新規接続は明示選択、role.model、全体modelの順。再開は明示選択、Session保存selectionの順で選ぶ。
roleは明示flag／TUI選択、Session保存role、config.defaultRoleの順。親も子も共通genericへ外部roleを渡す。
role.toolsで使えるtoolを指定し、toolができる操作はtool内部で定義する。

`--from-turn N` はモデル入力を完全なturn単位で選ぶ。
`history` の `--from-turn`／`--to-turn`、`inspect` の `--from-ordinal`／`--to-ordinal` は両端を含む。
保存履歴の自動切捨てやstep上限はない。必要なら `--max-steps N` を指定できる。
[操作契約](design/run-api.md)と[tool／hook API](design/extensions.md)に詳細を記載した。

```sh
./dist/hjh providers --workspace /path/to/work
./dist/hjh models --workspace /path/to/work --provider opencode-go-chat
./dist/hjh run --stream --workspace /path/to/work --session SESSION_ID --provider opencode-go-chat '前の成果物を読み、追加編集して確認して'
./dist/hjh run --stream --workspace /path/to/work --session SESSION_ID --provider openai-responses --model gpt-6-luna --effort low '作業を続けて'
./dist/hjh auth --workspace /path/to/work --provider openai-chatgpt login
```

I-03の既存configには標準例を参考にproviders・credentials・object形式のmodelを手動で追加する。
以前のOpenRouter Chat Sessionは `--provider openrouter-responses` を明示して再開する。
既存config・保存selection・OAuth tokenを自動変換しない。
I-04の実装・テスト・通常／批判的reviewと修正を完了し、5接続での実作業・切替・停止・OAuth更新を確認した。
[I-04計画](plans/i04-provider-model-switching.md)と[確認結果](results/i04-provider-model-switching.md)を参照する。

## role定義と旧configの手動更新（初期prototypeの切替記録）

```sh
./dist/hjh roles --workspace /path/to/work
./dist/hjh run --role reviewer --model deepseek/deepseek-v4.1-flash --workspace /path/to/work 'README.mdを読んでレビューして'
./dist/hjh-tui --role default --model deepseek/deepseek-v4.1-flash --workspace /path/to/work
```

rolesはDB・認証なしで使え、初回は不足する既定拡張fileを配置する。
既定roleはdefault（作業・委譲）、reviewer（レビュー）、explorer（調査）、implementer（実装）。
reviewer/explorerはrun_jsと読取り系tool、implementerは編集・commandも使える。
親は通常roleを選んでtools指定を省略する。同じroleを親と子の両方へ指定できる。
詳細は[標準roleとtool契約](design/extensions.md#標準role)を参照する。
config.jsonのrolesへroleを追加し、instruction・description・revision・toolsを編集する。
run_jsもtools配列で選択するbuilt-inであり、toolsが空なら本文回答だけのAgentになる。
role.modelへ部分的な接続設定を指定できる。子はそのrole.modelを親の実selectionへ適用する。

I-02以前のconfig.agentは廃止した。既存config・DBは自動変換も上書きも行わない。
既存workspaceを使い続ける場合は、config.jsonを手動で次の形へ更新する。

1. config.agentをdefaultRoleとrolesへ置き換える。既定例は[配布config](../internal/extensions/assets/config.json)。
   選ぶtoolをrole.toolsへ列挙し、生成JSを使うroleにはrun_js、委譲するroleにはrun_agentを含める。
2. config.toolsの外部registryへrun_agentのdefinitionを追加する。moduleはrun-agent.js、exportはrunAgent。
   inputSchemaはtaskを必須string、roleを任意stringにする。既存tool／hookの定義は用途に合わせて保持する。
3. run-agent.jsを配布例から追加する（次の内容）。初回roles／runで不足fileとしても配置される。
   既存configのregistryとrole.toolsの変更は手動で行う。

```js
export async function runAgent(args, api) {
  return api.agents.run(args);
}
```

生成JSで直接api.readFile／writeFile／command／httpを使っていた場合は、選択した登録toolを使う形へ変更する。
例：api.readFile({path})はtools.read_file({path})、api.writeFile({path,text})はtools.write_file({path,text})。
commandとhttpもtools.command／tools.httpで呼ぶ。外部tool／hookの実装用apiでは基礎操作を引き続き使える。
過去Sessionの保存role名が現在のcatalogにない場合は、--roleで現在のroleを明示して再開する。


## 背景と目的

Denoの開発体制変更を契機に、Henjiを継続して育てる実行基盤を検討した。
引き継ぎたいのは設計思想であり、特に履歴保存の価値と、ツールを整備してAgentの能力を高める拡張性を重視する。

目指す成果は次のとおり。

- **永続的な拡張**：tool・hookのソースを編集し、本体を再ビルドせずに次のWorkerから使える。
- **その場での拡張**：Agentが生成したJSから登録ツールを組み合わせ、結果を加工し、次の呼出しや判断へ使える。
- **履歴の継承**：成功した会話だけでなく、失敗・中断・途中のツール実行と使用構成も振り返り、保存Sessionを再開できる。
- **基礎機能の提供**：基本HTTPをcurlのインストール状況に依存させず、拡張からライブラリやOSコマンドも利用できる。

主な利用者は本人であり、自分の環境でAgentを使い、外部tool・hookを整備し、保存履歴を根拠にWorkerの振る舞いを改善する。

## 現在の方針

- Go本体がAgent loop、履歴保存、実行管理、HTTP・ファイル・プロセス等の機能を担う。
- tool・hookとAgent生成JSの実行基盤はPure Goの `github.com/go-quickjs/go-quickjs v0.25.2`。
- Go本体内でQuickJSを実行し、Worker間でVMを共有しない。
- モデル接続は外部provider設定で選択する。OpenRouter、OpenCode Go、OpenAI API、ChatGPT OAuthを用意し、非対話runとTUIは共通実行層を使う。
- 履歴SQLiteは旧henji-harnessのschema v3を初期採用し、最新送信全文・予算詳細の専用2 tablesを追加したv4へ変更する。[schema契約](design/history-schema.md)を参照する。
- 生成コードはJSでよい。TypeScript実行、Deno API・Web標準APIの完全互換は必須としない。
- 非同期処理はGoのgoroutineやcontextに合わせて設計する。現行Deno版のWorker配置やイベントループをそのまま再現することは要求しない。
- runや将来のTUIから利用する操作を共通のGo APIへ整理する。共通の操作契約と、HTTP等の呼出し方式は分けて判断する。
- tool・hookは外部JSとし、標準toolの振る舞いも編集可能にする。Agent loopの外部化は後続。
- JS依存は準備済みESMを同梱し、利用時のNode／npm依存を持たせない。

初期版の採用は、全npm／Node／Deno／Web標準互換や、任意のGo packageへの自動接続を意味しない。
tool・hookはJSで始め、必要な専門機能はGo公開APIや外部programで提供する。

## 履歴で引き継ぐ価値

履歴は、実行を振り返り、設定やツールを改善し、会話を再開するための基盤とする。

- 実行の証拠保存と、正常完了したturnの会話への採用を分ける。
- 失敗・中断・途中の出力・ツール結果・子実行も保存対象とする。
- 生成JSの親実行と、その内側で呼んだツール実行を関連付ける。
- モデルへ返す集約結果と、保存する実行証拠を分けて扱う。
- 使用構成を追跡できるようにし、現在の設定でSessionを再開しても過去の構成を確認できるようにする。
- 全履歴を保持しながら、メモリやモデル入力には必要な範囲を取り出す。

利用者の指定により、履歴schemaは旧henji-harnessと同一にする。
既存DBの変換・移行は初期範囲に含めず、保存payloadの契約は旧sourceと対応付けて実装する。

## 最初のスパイク

中核仮説は、Go＋QuickJSで永続的な拡張とその場でのツール合成を成立させ、その過程を履歴へ残せることである。

最小の検証経路は、**編集した外部ツールを生成JSから呼び、Goの機能で実処理を行い、結果と失敗を保存履歴で確認する**流れとする。
この経路を土台に、以下の5点を確認する。S-01〜S-05はそれぞれの実験範囲で成立を確認した。
S-03では2種類のJS依存とtool・hook編集、準備済み依存を含むLinux配布コピーを確認した。
S-04では採用済み会話と実行証拠の分離、構成archive、別プロセス再開、範囲指定読込みを確認した。
S-05では対象をLinuxのみとし、Linux/amd64の静的配布物から、Go・Node・curl・共有libcなしの起動、
編集反映、Session再開、CA／DNSを使うHTTPS、追加commandの依存を確認した。
詳細は各スパイクの結果文書を参照する。

| ID | 確認対象 | 成立の判断基準 |
| --- | --- | --- |
| S-01 | 生成JS → 登録されたJSツール → GoのHTTP・OSコマンド → JSへの結果返却 | 結果を次の呼出しに使える。例外が呼出し元へ届き、キャンセル後の実行と後処理の終了を確認できる。VM共有・分離の成立範囲を説明できる。 |
| S-02 | コード内部のツール実行履歴 | 親実行と内側の呼出しを関連付け、途中で失敗・中断しても、実行した呼出し・結果・残った副作用を振り返れる。集約結果と保存履歴の関係が分かる。 |
| S-03 | JSライブラリとGo公開API、外部tool・hookの編集 | 具体的なツールをGoの基礎機能と実際のJSライブラリで構成できる。ソース編集が本体再ビルドなしで次のWorkerへ反映される。利用できる依存、追加のGo APIが必要な場面、取得・配布の制約を説明できる。 |
| S-04 | 履歴保存・構成追跡・Sessionの閲覧と再開 | 採用済み会話と実行証拠を区別して復元できる。設定変更後の再開でも過去の構成を追跡できる。履歴増加時も必要な範囲を取り出せる。 |
| S-05 | 配布・起動条件 | 合意した対象OSでQuickJS・SQLiteを含む本体、外部tool・hookと依存を配布して起動できる。基本HTTPがcurlなしで動き、配布物でもソース編集と次Workerへの反映が成立する。必要な依存と追加ツールが要求する外部コマンドを説明できる。 |

結果は項目ごとに「成立・不成立・未確認」を区別し、実際の出力、保存記録、起動結果等へ結び付ける。
不成立の場合は、bindingや接続方式の変更で解決する問題か、要求や編集可能範囲の妥協が必要かを整理する。

スパイクの成功は技術的実現性の証拠であり、Agentが日常の作業を完了できる製品としての受入とは分ける。

## 詳細設計と後続

- Worker内のVM寿命、外部tool／hook・ESM依存は[拡張API](design/extensions.md)に記載した。
- 非対話runの公開操作、model選択、入力projection、表示は[操作契約](design/run-api.md)に記載した。
- 旧schemaのJSON／blob契約と観測の保存対応は[保存payload対応表](design/history-payloads.md)に記載した。
- 製品で対応するLinuxのarchitecture・distributionと実際の利用環境（S-05はLinux/amd64で確認済み）。
- TUIの操作は[TUI設計](design/tui.md)、子Agentへの委譲は[I-02計画](plans/i02-child-agents.md)と[確認結果](results/i02-child-agents.md)を参照する。

採用済み方針は[初期製品文書](design/initial-product.md)に従い、
[実装計画](plans/initial-run.md)の各段階で詳細を具体化する。

## 初期検討時の対象外

この段階では、Deno API・TypeScript・Web標準の完全互換、全機能の詳細設計、旧DBの移行方法を確定しない。

利用できるライブラリが狭すぎる、Go側の変更が頻繁に必要になる、実行履歴が欠ける、停止や配布が実際の利用先で成立しない場合は、目的への影響と代替案を比較して方針を見直す。

## 経緯と参照

移設元のREADMEは、2026-10-10時点の次の引継ぎ資料をもとに、このリポジトリの目的と検証方針をまとめた。
引継ぎ資料は `docs/` に原文のまま保管している。資料内の相対リンクは元の文書配置を前提とする。

- [上位計画](README.md)：GoによるHenji後継Harnessの上位計画。
- [2026-10-10の議論メモ](2026-10-10-go-quickjs-discussion-summary.md)：選択肢の比較、現行機能の確認、仮決定と未決事項の議論メモ。
- [現行Henjiの通常利用メモ](normal-use-inbox.md)：`henji-harness` から原文のまま保管した、通常利用での観測と未採用の改善候補。hjhでの採用・実装を意味しない。資料内の相対リンクは元リポジトリの配置を前提とする。
- [現行Henjiの参考資料](references/henji-harness/README.md)：構想・アーキテクチャ・機能インベントリを取得元commitとともに保管している。あくまで参考資料であり、hjhの機能要件・受入条件・設計・実装順序の正本ではない。
- 現行Henjiのソースコードは、ローカルの `henji-harness` リポジトリ（`/home/agent/projects/henji-harness`）にある。[リモートリポジトリ](https://forge.harakara.site/littleisland/henji-harness)とともに、後継開発の参照実装として扱う。引継ぎ資料での参照commitは `2eb586316f80ec8297873a96122568d0088971eb`。現行の全制限や実装方式を後継の必須仕様とするものではない。
- 現行Henji側で保管している参照リポジトリは `/home/agent/projects/henji-harness/_refs` にある。後継開発でも必要に応じて参照する。

初期runの到達点は[実装結果](results/initial-run.md)に記載した。
その後追加した `--stream` の実provider確認は[逐次表示の結果](results/run-stream.md)に記載した。
TUIの追加は[実装計画](plans/first-tui.md)に沿って実施した。
[実端末・実モデル・reviewの結果](results/first-tui.md)に確認範囲を記載した。
