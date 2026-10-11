# hjh Linux/amd64

Linux/amd64用の静的Goコア`hjh`、独立TUI`hjh-tui`と編集可能なJS拡張。
コアだけでも非対話run・APIを使える。実行にGo・Node・Deno・curl・共有libcは不要。
HTTPSには利用先OSのCA証明書とDNS設定を使う。`command`には呼び出すprogram自体が必要。

archiveを展開し、任意の作業ディレクトリから使う。`/path/to/hjh-linux-amd64` は展開先に置き換える。

```sh
/path/to/hjh-linux-amd64/hjh run --config /path/to/hjh-linux-amd64/extensions/config.json 'input.txtを読み、加工してoutput.txtへ書いて確認して'
/path/to/hjh-linux-amd64/hjh run --stream --config /path/to/hjh-linux-amd64/extensions/config.json '前回の結果を追加編集して確認して'
/path/to/hjh-linux-amd64/hjh roles --config /path/to/hjh-linux-amd64/extensions/config.json
/path/to/hjh-linux-amd64/hjh run --role reviewer --config /path/to/hjh-linux-amd64/extensions/config.json 'input.txtを読んでレビューして'
```

TUIはCoreと別processで起動する。次の2行は別端末で実行する。

```sh
/path/to/hjh-linux-amd64/hjh serve --workspace /path/to/work --config /path/to/hjh-linux-amd64/extensions/config.json --listen 127.0.0.1:8765
/path/to/hjh-linux-amd64/hjh-tui --endpoint http://127.0.0.1:8765
```

TUIで再開するときは`--session SESSION_ID`、次runの選択は`--provider`・`--model`・`--role`等を指定する。
`--max-steps`・`--from-turn`もTUIのrun条件へ送る。省略時はserveの既定値、明示0は無制限/会話先頭。
DB/config/credential-file/credential-state-dirはCoreに指定し、TUIはそれらを直接開かない。
manifest.jsonのbinariesに、各binaryのsize/hash/build情報/依存を記録する。licenseは両binaryの依存を同梱する。

配布設定は外部providers JSONからOpenRouter Responsesを既定選択する。
`--provider ID`・`--model ID`・`--effort VALUE` で切替できる。
APIキーは `/login` の専用入力欄から登録できる。明示 `--credential-file PATH` → 登録キー → env → fileの順に使う。
登録先は実効configとprofileごとの専用state。`--credential-state-dir PATH` はCore／CLIの保存rootを指定する。
標準file参照は `~/.local/state/henji-harness/v1/credentials/` の各APIキー。
配布先では利用先のcredential参照へ設定を編集する。APIキーは設定JSONに書かない。
ChatGPTは `hjh auth --provider openai-chatgpt login` でhjh自身の認証を保存し、
`hjh models --provider openai-chatgpt` から利用可能modelを確認する。

```sh
/path/to/hjh-linux-amd64/hjh sessions
/path/to/hjh-linux-amd64/hjh history --session SESSION_ID
/path/to/hjh-linux-amd64/hjh inspect --execution EXECUTION_ID --from-ordinal 0 --to-ordinal 20
/path/to/hjh-linux-amd64/hjh run --session SESSION_ID --config /path/to/hjh-linux-amd64/extensions/config.json '前回の結果を追加編集して'
```

flagは依頼文より前に指定する。依頼文を省略するとstdinから読む。
既定DBは作業ディレクトリの `.hjh/history.sqlite3`。他の場所から参照するときは `--db PATH` を指定する。
再開時はSession保存済みworkspaceを使う。Ctrl-Cで停止し、途中の証拠も残す。
stdoutは最終回答、stderrは進捗と識別子。成功0、失敗1、取消130。
`--stream` は本文を受信しながらstdoutへ表示する。途中本文とtool進捗を追いながら利用でき、
最終回答の重複出力はしない。失敗・取消でもすでに表示した本文は残る。

配布先 `extensions/index.js`・`hooks.js`・`config.json` を編集すると次のrunから反映される。
`--config` を省略したrunはworkspaceの `.hjh/extensions/` に標準拡張を配置し、そちらを使う。
生成JSでは `await tools.read_file({path:"input.txt"})` のようにtoolを組み合わせる。
configのdefaultRoleとrolesでinstruction・利用toolを定義する。親も子も共通genericへroleを渡す。
既定defaultは作業と委譲、reviewerはrun_js・read_fileを選ぶ。
run_jsもrole.toolsで選択する。run_agentを選んだ生成JSは
`await api.agents.run({role:"reviewer",task:"読む対象と必要な文脈"})` で委譲できる。
成功rootのroleはSessionへ保存され、明示--roleがなければ再開時に現在configから保存roleを解決する。
旧config.agentの自動変換はない。既存configにはdefaultRole／roles、run_agentのregistryを手動で追加し、
不足run-agent.jsを配布例から配置する。既存config・DBは自動上書きしない。

任意拡張用に準備済みYAML／semver ESMを `extensions/vendor/` へ同梱する。
`extensions/modules/catalog.js` を使う場合はconfigの `tools` に次の定義を追加し、
使うroleのtools配列にもcatalogを追加する。

```json
{"name":"catalog","description":"Read a YAML dependency catalog from a URL and select compatible versions.","inputSchema":{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]},"module":"modules/catalog.js","export":"catalog"}
```

TUIは通常画面のscrollbackへ会話を表示する。Enterで依頼、Alt／Shift／Ctrl+EnterまたはCtrl+Jで改行、
Tabでコマンド補完、Escで選択表示close、Ctrl+Cで入力消去、F1で現在runの予約列をキャンセルする。
実行中のEnterはdraftを保持し、受付可能な期間はF2で次runを予約、F3で現在の親runへ指示を追加する。
F2は現在runの正常保存後にCoreが同じSessionで実行する。F3は次の親requestの境界で適用する。
受付応答までdraftを保持し、その間に編集したdraftは消さない。`/inputs`でreceiptと未実行理由を読む。
`/context`で保存済み入力準備と次回選択・引継ぎ状態を確認する。
停止後は`/recall [ID|latest]`で公開情報を次の通常rootへ一度引き継ぎ、`/recall clear`で解除する。
準備はdetach中も残るがCore再起動で失われる。元runと完了済みtoolを自動再実行しない。
`/detach`／Ctrl+DはTUIだけを終了し、Coreとrunは継続する。`--session ID`／`/resume ID`で同runへ再接続する。
`/quit`／Ctrl+QはCoreを停止し、全runの取消・join・保存を待ってTUIも終了する。
serve側のCtrl-C/SIGTERMも終了保存を待つ。F4でSession選択、`/rename 名前`で現在Sessionを命名する。
`/sessions`、`/new`、`/resume ID`、`/history`、`/executions`、`/inspect ID`、`/providers`、`/provider [ID]`、`/models [ID]`、`/model [ID]`、`/effort [VALUE]`、`/login [ID]`、`/roles`、`/role [NAME]` が使える。
引数なしの`/provider`・`/model`・`/effort`はpickerを開く。`/model`はID／表示名を検索できる。
model pickerのTabでお気に入りを登録・解除し、保存確認後に印と先頭への整列を反映する。
同じ実効config path・provider IDで共有し、認証accountの変更やCore再起動後も好みを保持する。
既定保存先は`$XDG_STATE_HOME/hjh/model-favorites/`、未指定なら`~/.local/state/hjh/model-favorites/`。
`hjh serve --model-favorites-dir PATH`でお気に入りの保存先だけを変更できる。
上下で選びEnterで確定、Escで閉じる。直接指定と実行中の次run選択も利用できる。
role選択の変更は次runへ反映し、実行中の構成は維持する。
一覧は上下で選んでEnter、会話の検索・コピーは端末／tmuxのscrollbackを使う。詳細は画面内の `/help` を参照する。

既存DBのmigrationはこの版の対象外。対応確認はLinux/amd64の検証環境に限る。

既存configにはproviders・credentials・object形式のmodelを手動で追加する。
旧OpenRouter ChatのSessionは--provider openrouter-responsesを明示して再開できる。
provider JSONとrole.modelの形式はrepositoryのdocs/design/providers.mdを参照する。
