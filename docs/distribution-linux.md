# hjh Linux/amd64

Linux/amd64用の静的Goコア`hjh`、独立TUI`hjh-tui`と編集可能なJS拡張。
コアだけでも非対話run・APIを使える。実行にGo・Node・Deno・curl・共有libcは不要。
HTTPSには利用先OSのCA証明書とDNS設定を使う。`command`には呼び出すprogram自体が必要。
本文検索の`grep`にはripgrep（`rg`）またはGNU grep、`git_inspect`にはGitが必要。

archiveを展開し、任意の作業ディレクトリから使う。`/path/to/hjh-linux-amd64` は展開先に置き換える。

```sh
/path/to/hjh-linux-amd64/hjh run --config /path/to/hjh-linux-amd64/extensions/config.json 'input.txtを読み、加工してoutput.txtへ書いて確認して'
/path/to/hjh-linux-amd64/hjh run --stream --config /path/to/hjh-linux-amd64/extensions/config.json '前回の結果を追加編集して確認して'
/path/to/hjh-linux-amd64/hjh roles --config /path/to/hjh-linux-amd64/extensions/config.json
/path/to/hjh-linux-amd64/hjh run --role reviewer --config /path/to/hjh-linux-amd64/extensions/config.json 'input.txtを読んでレビューして'
```

TUIだけを起動すると、同じVMのCoreへ接続し、未起動なら同じ配布directoryの`hjh`を起動する。
CoreとTUIは別processで、TUIをdetachした後もCoreは継続する。

```sh
/path/to/hjh-linux-amd64/hjh-tui --workspace /path/to/work --config /path/to/hjh-linux-amd64/extensions/config.json
```

既定の接続先は`http://127.0.0.1:8765`。起動済みCoreは再利用する。
`--db`・`--config`・`--credential-file`・`--credential-state-dir`・`--model-favorites-dir`は新Coreへの起動option。
既存Coreへの接続ではそのCoreの起動設定を使う。新規Coreのworkspaceは現在directoryが既定で、
既存Coreへの接続ではTUIの`--workspace`省略時にそのCoreの既定workspaceを使う。
起動logは実効DBと同じdirectoryの`core.log`へ追記する。
別配置のCore binaryは`--core-binary PATH`で指定できる。
SSH先のVMでTUIを動かす場合も、このVM上でCoreを起動する。

接続だけを行う場合は`--endpoint URL`を明示する。Coreを別端末で起動する既存の方式も使える。

```sh
/path/to/hjh-linux-amd64/hjh serve --workspace /path/to/work --config /path/to/hjh-linux-amd64/extensions/config.json --listen 127.0.0.1:8765
/path/to/hjh-linux-amd64/hjh-tui --endpoint http://127.0.0.1:8765
```

同じVMの別portで自動起動する場合は`--endpoint http://127.0.0.1:PORT --start-core`。
`--start-core=false`は既定の接続先にも接続だけを行う。

TUIで再開するときは`--session SESSION_ID`、次runの選択は`--provider`・`--model`・`--role`等を指定する。
`--max-steps`・`--from-turn`もTUIのrun条件へ送る。省略時はserveの既定値、明示0は無制限/会話先頭。
DB/config/credential-file/credential-state-dirはCore起動時に渡し、TUIの画面はそれらを直接開かない。
manifest.jsonのbinariesに、各binaryのsize/hash/build情報/依存を記録する。licenseは両binaryの依存を同梱する。
JS実行基盤はPure Goのgo-quickjs v0.25.2。Intl/locale dataもbinaryへ含み、追加downloadは不要。
engineのMIT、go-intlのUnicode license、Math実装のfdlibm noticeを`licenses/`へ同梱する。
既に動いているCoreが新engineを使うには、そのCoreを新binaryで再起動する。

配布設定は外部providers JSONからOpenRouter Responsesを既定選択する。
`--provider ID`・`--model ID`・`--effort VALUE` で切替できる。
APIキーは `/login` の専用入力欄から登録できる。明示 `--credential-file PATH` → 登録キー → env → fileの順に使う。
登録先は実効configとprofileごとの専用state。`--credential-state-dir PATH` はCore／CLIの保存rootを指定する。
既定保存rootは `$XDG_CONFIG_HOME/hjh`、Linuxで未指定なら `~/.config/hjh`。
標準file参照はその `credentials/` の各APIキー。ChatGPT認証情報も同じ配下に保存する。
configの `${XDG_CONFIG_HOME}/` prefixは、未指定時の既定directoryを含めて解決する。
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

`hjh --version`／`hjh-tui --version`は版・source revision/dirty・実バイナリhash・Go/target・
拡張契約を表示する。配布buildはsource情報を両バイナリとmanifestへ埋め込む。
表示だけではCore、DB、拡張設定を作成しない。

`hjh core list [--workspace PATH] [--json]`で同じVM・ユーザーの登録を一覧表示する。
`hjh core status --core ID_OR_PREFIX`、`hjh core stop --core ID_OR_PREFIX`で対象を選ぶ。
URL指定は`--connect URL`。対象省略時は一覧を表示し、停止しない。
stopはCoreの取消・終了保存・HTTP停止を待つ。Coreを新規起動するコマンドではない。
登録はXDG stateの`hjh/cores/`に置き、通常終了時に自身の登録を外す。
異常終了の登録は到達不能として表示する。既存Coreには再起動で反映する。

`hjh history --latest`は既定で統一会話テキスト。`--session`は一意な短縮IDも使える。
Session IDの人向け表示は原則8文字。既知のprefix衝突は区別できるまで延長する。
そのIDをrun／historyの`--session`、TUIの`--session`／`/resume`へ渡せる。
`hjh sessions`は短縮IDの一覧、`hjh sessions --json`は完全IDの構造化一覧。
API／保存履歴／JSON／detail NDJSONは完全IDを保持する。
TUIは製品名・version、保存タイトル・日時、Core／Sessionの短縮ID、workspaceのheaderを
会話を開いた時点で一度表示する。現在のSession IDとタイトルはfooterへ表示する。
新規Sessionは最初の依頼で作成され、その時のID変更やrename・resizeではheaderを追加しない。
`--view canonical`は採用会話Markdown、`--view detail`は保存実行証拠のNDJSON。
detailのrecord contentはbase64で保存bytesを保持する。
`--json`は採用message配列。`--connect URL`はCore側の履歴を読み、未指定ならローカルDBを
read-onlyで読む。範囲は`--from-turn`／`--to-turn`で指定する。
`run`の再開時はSession保存済みworkspaceを使う。Ctrl-Cで停止し、途中の証拠も残す。
`run`のstdoutは最終回答、stderrは進捗と識別子。成功0、失敗1、取消130。
`hjh run --connect URL "READMEを要約して" > summary.txt`で起動済みCoreへ依頼できる。
stderrのSession IDを`hjh-tui --endpoint URL --session ID`で開くと、その実行へ途中から接続できる。
接続runもCtrl-Cは取消と終了保存を待ち、通信切断やCLI強制終了ではCoreの作業が続く。
workspace省略はCore既定、相対pathはCore基準、再開は保存workspaceを使う。
DB/config/credential関連のfile指定はCore起動時に行う。max-steps/from-turn省略はCoreの既定設定を使う。
`--stream` は本文を受信しながらstdoutへ表示する。途中本文とtool進捗を追いながら利用でき、
最終回答の重複出力はしない。失敗・取消でもすでに表示した本文は残る。

配布先 `extensions/index.js`・`hooks.js`・`config.json` を編集すると次のrunから反映される。
`--config` を省略したrunはworkspaceの `.hjh/extensions/` に標準拡張を配置し、そちらを使う。
標準configは`hooks.js`をhook moduleに指定する。開始日時の例はGoが渡す`runtime.startedAt`と
`runtime.timeZone`を使うため、JS側で時計やIntl APIを用意する必要はない。

```js
export async function runtimeStart({ runtime }) {
  return {
    context: [
      "Agent runtime started at: " + runtime.startedAt + " (" + runtime.timeZone + ")",
    ],
  };
}
```

追加するhookは同じ`hooks.js`からfunction exportする。設定snapshotの`handlers`は宣言情報で、
実際のfunction exportが呼出対象を決める。`runtimeStart`と`beforeTurn`の返却文脈は
既存instructionの後に今回のmodel入力へ加わり、通常のcontext budgetと履歴attributionに入る。
この動的文脈は保存済みconfigを書き換えず、次turnへ累積しない。

WorkerはSessionのturnごと、またはchild executionごとに作られる。Core起動やTUIのattach／再接続では
lifecycle hookを再実行しない。module編集は次に作られるWorkerから反映される。
終了時はterminalとrootの採用判断の後に`afterTurn`、`runtimeStop`をawaitしてからWorkerを閉じる。
hookの失敗・skip通知はCLIのstderr、TUIのnoticeへ表示され、詳細記録は
`hjh inspect --execution ID`または`hjh history --view detail`で確認できる。
Webページは`web_fetch`で読み取る。通常は保持する本文64 KiBまでを可読テキストで返し、`save_to`は本文を
上限なくfileへ保存する。コンテキスト予算管理はCoreが行い、ターン内・次ターン・再開・
モデル変更・子Agentに適用する。履歴全文は残し、モデルへ送る内容だけを選ぶ。
TUIの`/context`で有効予算、出典、選択した履歴と保存元を確認できる。
上書きはconfig.jsonの`contextBudget.defaults/providers/models`で指定する。

Web検索はExa版`web_search`。標準はauto・最大10件・highlights最大1,000文字。domain/dateやoutputSchemaも指定できる。
contentsは標準toolが設定するため送らない。
`EXA_API_KEY`または`${XDG_CONFIG_HOME}/hjh/credentials/exa-api-key`を参照する。
キーはGoで認証headerへ付け、JSや履歴へ返さない。[検索契約](design/web-search.md)を参照する。
既存workspaceではconfigのtool/role/credential参照とweb-search.jsを追加し、新binaryでCoreを再起動する。

既存workspaceの拡張は自動置換しない。`web_fetch`を追加するときは配布configのtool definitionを
既存config.toolsへ、使うrole.toolsへ`web_fetch`を追加し、配布index.jsのwebFetch exportを反映する。
容量を公開catalogから解決する標準OpenRouterの`catalog`、標準OpenAIの`modelsDevProviderId`も
配布configを参照する。本体とTUIを更新した後、既存Coreは一度終了して新しい本体で起動する。
生成JSでは `await tools.read_file({path:"input.txt"})` のようにtoolを組み合わせる。
configのdefaultRoleとrolesでinstruction・利用toolを定義する。親も子も共通genericへroleを渡す。
既定defaultは作業と委譲、reviewerはrun_js・read_fileを選ぶ。

外部toolの使い方はconfig.toolsの`promptGuidelines: string[]`へ記述できる。
有効なtoolの指示だけが親・子の共通model instructionへ入り、snapshotにも保存される。
run_jsの基本操作と選択済みJS API、標準子agent操作も同じ仕組みを使う。
既存configの子toolへは配布configの同fieldを追加する。編集は次run・子開始時から反映される。

## Workspaceの指示とskills

AGENTS.mdはユーザー共通の`${XDG_CONFIG_HOME:-$HOME/.config}/hjh/AGENTS.md`、
filesystemの祖先、workspaceの順に適用する。近いscopeの指示を優先する。
各ターン・子Agent開始時に読み、その実行中は固定する。

skillsは次の順にSKILL.mdを再帰探索し、同名は先順位を使う。

1. `WORKSPACE/.hjh/skills/`
2. `WORKSPACE/.agents/skills/`
3. `${XDG_CONFIG_HOME:-$HOME/.config}/hjh/skills/`
4. `$HOME/.agents/skills/`

例えば`.hjh/skills/report/SKILL.md`を次のように配置する。

```markdown
---
name: report
description: 作業結果のレポートを作成するときに使う
---
必要なファイルを読み、結果と確認方法をレポートにまとめてください。
```

名前・説明・pathだけを最初にモデルへ提示し、本文は必要になった時にread_fileで読む。
本文内の相対referenceはSKILL.mdのdirectoryから解決する。
生成JSで読む場合は`return await tools.read_file({path: absoluteSkillPath});`のように本文をモデルへ返す。
標準read_fileを選んだroleで利用できる。`disable-model-invocation: true`は自動選択一覧から除く。

TUIの通常幅headerには基本指示・AGENTS出典・利用可能skillを表示する。
`preview`は起動時候補、`actual`は実runの開始時に固定した情報。
狭幅画面や最新の情報は`/context`で確認でき、Session未作成でも使える。
この機能の反映にはCoreとTUIの両方を新binaryで起動する。

## toolとTUIの操作

run_jsもrole.toolsで選択する。run_agentを選んだ生成JSは
`await api.agents.run({role:"reviewer",task:"読む対象と必要な文脈"})` で委譲できる。
子のmodel/provider/effortは`model:{provider?,modelId?,effort?}`、子のtoolsは`tools:[...]`で指定できる。
tools省略は子roleの一覧、指定は実行用の一覧を置換し、空配列はtoolなし。

親に「二つの子を並行に開始し、私はその間に別作業を進め、途中報告を読んで必要なら
追加指示を送り、最後に回収して」と自然文で依頼できる。
非同期操作は選択済みtoolに対応する生成APIから使う。

```js
// Start an independent review and read its saved reports.
const child = await api.agents.spawn({role:"reviewer",task:"srcの主要な責務を調べ、途中のfindingも通常assistant本文で報告して"});
const status = await api.agents.status({executionId:child.executionId});
return status;
```

次の親stepで`api.agents.status({executionId,after:nextCursor})`から新しい報告を取得し、
`api.agents.steer({executionId,text})`で親から子へ指示を送る。
statusのinstructionsにあるappliedは取込み済みを意味し、内容への従い方は後の報告で確認する。
必要な時点で`api.agents.collect({executionId})`が終了保存を待ち、取消は`api.agents.cancel({executionId})`。
spawnした子は同じ親execution中の後のstepまで続き、親終了では残る子を取消・joinする。
TUI detach中もCoreで親子が続行し、同じSessionへ再接続できる。

既存workspaceへ追加する場合は、配布の`agent-control.js`を配置し、`spawn_agent`、`agent_status`、
`steer_agent`、`collect_agent`、`cancel_agent`のdefinitionをconfig.toolsへ、必要なrole.toolsへ追加する。
既存`run_agent`のschema/説明もmodel/tools指定に合わせて更新する。configとDBは自動変換しない。
本体とTUIを更新した後、既存Coreを新しいbinaryで起動し直して使う。

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
`hjh-tui --continue`／`/resume latest`は同じworkspaceの最新Sessionを再開する。
`/quit`／Ctrl+QはCoreを停止し、全runの取消・join・保存を待ってTUIも終了する。
serve側のCtrl-C/SIGTERMも終了保存を待つ。F4でSession選択、`/rename 名前`で現在Sessionを命名する。
`/sessions`、`/new`、`/resume ID|latest`、`/history`、`/executions`、`/inspect ID`、`/provider [ID]`、`/model [ID]`、`/effort`、`/login [ID]`、`/roles`、`/role [NAME]` が使える。
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

## stdlib移行時の外部拡張更新

この版の標準toolはgo-quickjs v0.25.2 stdlib＋JSへ切り替わる。Go moduleのforkやpatchはない。
**binaryだけ更新して古いindex.js／inspect.jsを残すと、削除済みapi.readFile等を呼んで失敗する。**
既存workspaceの編集済みfileを自動上書きする機構・旧API互換層は提供しない。

1. 利用者が停止対象Coreを選び、進行中作業の終了／取消を確認してから更新する。
   新binaryと同梱extensionsを別directoryに展開し、現在の設定・編集sourceとの差分を確認する。
2. 標準実装を採用するtoolは、新しいindex.js／inspect.js／web-search.jsと、importされる
   inspection.js／glob.js／process.js／grep-parser.js／git-inspect.js／web-fetch.jsを同じ拡張treeへ反映する。
   新規の別treeを使う場合は独自module・hooksも必要分を配置し、configの参照先を明示する。
3. config.json全体を機械的に置換しない。tool definitions、promptGuidelines、helpFileと
   対応する*-help.mdを差分反映し、既存providers／credential参照／roles／独自tool設定を保持する。
   外部tool-usage.mdを差し替えている場合は、選択tools辞書とstdlib公開が別である説明へ合わせる。
4. 独自tool／hookの旧api.readFile／writeFile／editFile／listDirectory／findFiles／countFiles／
   command／resolveExecutable／grepCommand／gitInspect／http／webFetch／webSearch呼出しを更新する。
   選択済みtoolならapi.tools.NAME、基礎操作ならstdlibのfs／path／child_process／fetchを使う。
   認証付き要求はapi.requestProviderを使い、キーやAuthorizationをJSへ渡さない。
5. 新binaryと更新済みconfigでCoreを起動し、新しいSessionまたは既存Sessionの次runで
   help→file読書き→必要なprocess／Web操作を確認する。開始済みWorkerのcaptureは変わらない。

run_jsのasync本文では `await import("node:fs")` 等を使う。
process moduleはdefault exportなので `const {default:proc} = await import("node:process")`、
または公開globalのprocessを利用する。生fsへは絶対pathを渡し、workspace相対pathは
`path.resolve(process.cwd(), relative)`で解決する。Node/npm全体の互換環境ではない。

正常callは開始済みI/O・timerをdrainするためintervalをclearする。
未捕捉例外後は次callで同じcaptureから新VMを作り、global／module stateをリセットする。
取消はWorkerを終了する。開始済み非同期fsは例外／取消後もOS側で完了し得るため実fileを確認する。
commandはstdout／stderr各32 MiB、取消は直接子だけ。子孫を止める必要があればcommandで確認・killする。
詳細はrun_js／commandのhelpと[拡張契約](design/extensions.md)を参照する。
