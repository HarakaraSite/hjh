# hjh

Henjiの構想・機能を引き継ぎ、Goの特性を活かして作る後継Agent Harness。
現行Deno版の完全移植を目指すものではなく、既存コードの再利用にも拘らない。

Go＋QuickJS、複数provider、Linux/amd64向けの非対話runとTUIを実装した。
DeepSeek v4.1 Flashでファイル作業・Session再開・HTTP・command、tool／hook編集を確認した。
[初期製品の方針・範囲・実装順序](docs/design/initial-product.md)と
[旧henji-harnessと同一の履歴schema](docs/design/history-schema.md)を正本とする。
具体的な実装手順と各段階の完成条件は[非対話runの実装計画](docs/plans/initial-run.md)に記載した。
[実装・実モデル・配布・reviewの結果](docs/results/initial-run.md)に確認した環境と実行記録をまとめた。
以後の作業単位はインクリメントとする。I-01は非対話run・stream・履歴・再開・最初のTUIまでの
基盤プロトタイプで、利用者の操作確認を完了した。
[I-02「子Agentへの委譲」](docs/plans/i02-child-agents.md)を実装し、実OpenRouterでCLI・TUI・
Linux配布物の委譲、結果利用、停止、親子履歴を確認した。
[実装・確認・reviewの結果](docs/results/i02-child-agents.md)を参照する。利用者も子Agent呼出しを確認済み。
[I-03計画](docs/plans/i03-generic-roles.md)に沿って、親子共通のbuilt-in genericへ外部roleとtool構成を渡す操作を実装した。
[実装・確認・reviewの結果](docs/results/i03-generic-roles.md)に実provider・PTY・配布物と指摘修正の記録をまとめた。
Coreからコンテナ・OS仮想マシンへAgentを配置する案は[将来構想](docs/design/future-agent-environments.md)として記録した。
I-05は「コアとTUIの分離」。`hjh serve`と独立した`hjh-tui`を実装し、HTTP＋JSON＋SSEで通信する。
[計画](docs/plans/i05-core-tui-separation.md)と[HTTP API契約](docs/design/http-api.md)に対応し、実provider・独立TUI・Linux配布物の確認と通常/批判的reviewを完了した。
[実装・確認・reviewの結果](docs/results/i05-core-tui-separation.md)を参照する。
I-06は旧Henjiの操作・表示に沿った会話画面と、それを支えるCore会話経路の拡張を計画中。
Ghostty → SSH → tmuxを利用経路とし、[計画](docs/plans/i06-conversation-tui.md)と
[S-06スパイクの結果](docs/results/s06-tui-core.md)に旧Data Worker・参照実装の調査と確認範囲を記録した。
S-01〜S-05スパイクは実施済み。[実験コード](spikes/s01/README.md)、
[S-01の結果](docs/spikes/s01-results.md)、[S-02の結果](docs/spikes/s02-results.md)、
[S-03の結果](docs/spikes/s03-results.md)、[S-04の結果](docs/spikes/s04-results.md)、
[S-05の結果](docs/spikes/s05-results.md)を参照する。

## 使い始める

ソースからは `go build -o dist/hjh ./cmd/hjh` でコアを、
`go build -o dist/hjh-tui ./cmd/hjh-tui` でTUIをビルドする。
Linux静的配布物は `python3 scripts/package-linux.py` で `dist/packages/` に作成できる。
配布先ではGo・Node・Deno・curlを必要としない。[配布物の使い方](docs/distribution-linux.md)を参照する。

接続は外部 `.hjh/extensions/config.json` の `providers` で指定する。
標準でOpenRouter Responses、OpenCode Go Chat／Responses、OpenAI API Responses、ChatGPT OAuthを用意した。
APIキーはJSONのcredential profileが参照する環境変数、fileの順に読む。
標準のfile参照は旧Henjiと同じ保存先。`--credential-file PATH` は選択profileのfileを明示して使う。
APIキーを設定JSONへ書かない。[provider設定と認証](docs/design/providers.md)に完全な形式とログイン操作を記載した。

```sh
./dist/hjh run --model deepseek/deepseek-v4.1-flash --workspace /path/to/work 'input.txtを読み、加工してoutput.txtへ書いて確認して'
./dist/hjh run --stream --model deepseek/deepseek-v4.1-flash --workspace /path/to/work 'input.txtを読み、結果を説明して'
./dist/hjh sessions --workspace /path/to/work
./dist/hjh history --workspace /path/to/work --session SESSION_ID
./dist/hjh inspect --workspace /path/to/work --execution EXECUTION_ID --from-ordinal 0 --to-ordinal 20
./dist/hjh run --workspace /path/to/work --session SESSION_ID '前回のoutput.txtを追加編集して'
```

flagは依頼文より前に指定する。依頼文を省略するとstdinから読む。
stdoutは最終回答、stderrは進捗・Session ID・execution ID。Ctrl-Cで停止する。
`--stream` はモデル本文を受信しながらstdoutへ表示し、tool進捗はstderrへ出す。
最終回答を重複表示せず、停止・失敗でも表示済みの途中本文は残す。
成功0、失敗1、取消130。失敗・取消の証拠は残し、正常完了したturnだけを再開会話へ採用する。

子Agentへ調査を委譲する場合も同じrun・TUIから依頼する。

```sh
./dist/hjh run --stream --model deepseek/deepseek-v4.1-flash --workspace /path/to/work '生成JSのapi.agents.runを使い、srcとdocsの調査を2つの子Agentへ並行委譲して。結果をまとめてreport.txtに書き、読み返して報告して'
```

run_agentを選んだroleでは、生成JSから `await api.agents.run({role: "reviewer", task: "調査内容と必要な文脈"})` を使える。
外部tool／hook実装の同名APIは基礎委譲操作へ接続する。
並行委譲は `Promise.all`。子は独立した会話とVMで実行し、結果にexecution IDと終了状態を返す。
子の進捗はID付きで表示し、stdoutやTUIの親本文へ混ぜない。子の実行証拠はinspectから閲覧できる。
子の会話をSessionへ直接採用せず、親の正常完了turnだけを採用する。

TUIはCoreを起動してから、別端末で接続する。

```sh
./dist/hjh serve --workspace /path/to/work --listen 127.0.0.1:8765
./dist/hjh-tui --endpoint http://127.0.0.1:8765 --model deepseek/deepseek-v4.1-flash
./dist/hjh-tui --endpoint http://127.0.0.1:8765 --session SESSION_ID
```

Enterで依頼、Alt-EnterまたはCtrl-Jで改行。実行中はEscで停止し、Ctrl-Cで終了する。
通常TUI終了は自分のrunをキャンセルして保存完了まで待ち、Coreは起動したまま残す。
Core自体はserve側のCtrl-C/SIGTERMで終了する。TUIの購読切断だけではrunをキャンセルしない。
DB/config/credential-fileはCore側へ指定する。TUIのworkspace指定もCore上のpathを指す。
`/sessions` でSessionを選び、`/new` で新規会話、`/resume ID` で再開する。
`/history [開始turn 終了turn]` は採用会話、`/executions` と
`/inspect ID [開始ordinal 終了ordinal]` は失敗・取消を含む実行証拠を表示する。
`/providers`・`/models [PROVIDER]` で候補を表示し、`/provider ID`・`/model ID`・`/effort VALUE` で次の接続を選ぶ。`/roles` はrole一覧、`/role NAME` は次の実行のrole選択、
`/role` は選択状態を表示する。実行中の選択変更は次runから反映する。成功rootのmodelとroleはSessionへ保存される。
画面内の `/help` と[操作・責務](docs/design/tui.md)に詳細を記載する。

既定DBはworkspace内 `.hjh/history.sqlite3`、外部tool・hookは `.hjh/extensions/`。
`index.js`・`hooks.js`・`config.json` を編集すると次のrunから反映される。
設定の `model` は `{"provider":"openrouter-responses","modelId":"deepseek/deepseek-v4.1-flash","effort":"auto"}` のように指定する。
別DBや配布設定を使う場合は `--db PATH`・`--config PATH` を指定する。
新規接続は明示選択、role.model、全体modelの順。再開は明示選択、Session保存selectionの順で選ぶ。
roleは明示flag／TUI選択、Session保存role、config.defaultRoleの順。親も子も共通genericへ外部roleを渡す。
role.toolsで使えるtoolを指定し、toolができる操作はtool内部で定義する。

`--from-turn N` はモデル入力を完全なturn単位で選ぶ。
`history` の `--from-turn`／`--to-turn`、`inspect` の `--from-ordinal`／`--to-ordinal` は両端を含む。
保存履歴の自動切捨てやstep上限はない。必要なら `--max-steps N` を指定できる。
[操作契約](docs/design/run-api.md)と[tool／hook API](docs/design/extensions.md)に詳細を記載した。

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
[I-04計画](docs/plans/i04-provider-model-switching.md)と[確認結果](docs/results/i04-provider-model-switching.md)を参照する。

## role定義と旧configの手動更新

```sh
./dist/hjh roles --workspace /path/to/work
./dist/hjh run --role reviewer --model deepseek/deepseek-v4.1-flash --workspace /path/to/work 'README.mdを読んでレビューして'
./dist/hjh tui --role default --model deepseek/deepseek-v4.1-flash --workspace /path/to/work
```

rolesはDB・認証なしで使え、初回は不足する既定拡張fileを配置する。
既定roleはdefault（作業・委譲）とreviewer（run_js・read_file）。同じroleを親と子の両方へ指定できる。
config.jsonのrolesへroleを追加し、instruction・description・revision・toolsを編集する。
run_jsもtools配列で選択するbuilt-inであり、toolsが空なら本文回答だけのAgentになる。
role.modelへ部分的な接続設定を指定できる。子はそのrole.modelを親の実selectionへ適用する。

I-02以前のconfig.agentは廃止した。既存config・DBは自動変換も上書きも行わない。
既存workspaceを使い続ける場合は、config.jsonを手動で次の形へ更新する。

1. config.agentをdefaultRoleとrolesへ置き換える。既定例は[配布config](internal/extensions/assets/config.json)。
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
- QuickJSをtool・hookとAgent生成JSの実行基盤として採用する。bindingは検証した `modernc.org/quickjs v0.25.0`。
- Go本体内でQuickJSを実行し、Worker間でVMを共有しない。
- モデル接続はOpenRouter。非対話runとTUIが同じGo操作を呼ぶ。
- 履歴SQLiteは旧henji-harnessの現行schema v3と同一にする。スパイクの簡略schemaは製品に採用しない。
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

- Worker内のVM寿命、外部tool／hook・ESM依存は[拡張API](docs/design/extensions.md)に記載した。
- 非対話runの公開操作、model選択、入力projection、表示は[操作契約](docs/design/run-api.md)に記載した。
- 旧schemaのJSON／blob契約と観測の保存対応は[保存payload対応表](docs/design/history-payloads.md)に記載した。
- 製品で対応するLinuxのarchitecture・distributionと実際の利用環境（S-05はLinux/amd64で確認済み）。
- TUIの操作は[TUI設計](docs/design/tui.md)、子Agentへの委譲は[I-02計画](docs/plans/i02-child-agents.md)と[確認結果](docs/results/i02-child-agents.md)を参照する。

採用済み方針は[初期製品文書](docs/design/initial-product.md)に従い、
[実装計画](docs/plans/initial-run.md)の各段階で詳細を具体化する。

## 現在の対象外

この段階では、Deno API・TypeScript・Web標準の完全互換、全機能の詳細設計、旧DBの移行方法を確定しない。

利用できるライブラリが狭すぎる、Go側の変更が頻繁に必要になる、実行履歴が欠ける、停止や配布が実際の利用先で成立しない場合は、目的への影響と代替案を比較して方針を見直す。

## 経緯と参照

このREADMEは、2026-10-10時点の次の引継ぎ資料をもとに、このリポジトリの目的と検証方針をまとめた。
引継ぎ資料は `docs/` に原文のまま保管している。資料内の相対リンクは元の文書配置を前提とする。

- [上位計画](docs/README.md)：GoによるHenji後継Harnessの上位計画。
- [2026-10-10の議論メモ](docs/2026-10-10-go-quickjs-discussion-summary.md)：選択肢の比較、現行機能の確認、仮決定と未決事項の議論メモ。
- [現行Henjiの通常利用メモ](docs/normal-use-inbox.md)：`henji-harness` から原文のまま保管した、通常利用での観測と未採用の改善候補。hjhでの採用・実装を意味しない。資料内の相対リンクは元リポジトリの配置を前提とする。
- [現行Henjiの参考資料](docs/references/henji-harness/README.md)：構想・アーキテクチャ・機能インベントリを取得元commitとともに保管している。あくまで参考資料であり、hjhの機能要件・受入条件・設計・実装順序の正本ではない。
- 現行Henjiのソースコードは、ローカルの `henji-harness` リポジトリ（`/home/agent/projects/henji-harness`）にある。[リモートリポジトリ](https://forge.harakara.site/littleisland/henji-harness)とともに、後継開発の参照実装として扱う。引継ぎ資料での参照commitは `2eb586316f80ec8297873a96122568d0088971eb`。現行の全制限や実装方式を後継の必須仕様とするものではない。
- 現行Henji側で保管している参照リポジトリは `/home/agent/projects/henji-harness/_refs` にある。後継開発でも必要に応じて参照する。

初期runの到達点は[実装結果](docs/results/initial-run.md)に記載した。
その後追加した `--stream` の実provider確認は[逐次表示の結果](docs/results/run-stream.md)に記載した。
TUIの追加は[実装計画](docs/plans/first-tui.md)に沿って実施した。
[実端末・実モデル・reviewの結果](docs/results/first-tui.md)に確認範囲を記載した。
