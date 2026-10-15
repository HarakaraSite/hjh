# 外部拡張とWorker runtime

この文書はJSON設定、外部ESM tool／hook、Worker runtimeの境界を定める。
拡張directoryは実行時に編集でき、各Workerは開始時に読んだsource bytesだけを実行する。

## 設定と外部source

拡張directoryには `config.json` と相対import可能なJavaScript moduleを置く。
初期配布の既定ファイルは `internal/extensions/assets/` にあり、
`extensions.InstallDefaults(dir)` が不足ファイルだけを編集可能な形で配置する。
既定配置後にCLIが `extensions.CaptureDir(dir)` を呼ぶか、
`--config` で指定されたJSONを `extensions.Capture(configPath)` で捕捉する。

入力設定はruntimeのための簡潔なJSONである。toolのmoduleは拡張directory内の相対ESM path、
`export` はそのmoduleが公開する関数名を指定する。

```json
{
  "defaultRole": "default",
  "roles": {
    "default": {"revision":"1","description":"作業担当","instruction":"依頼を実施して確認する", "tools":["run_js","run_agent","read_file"]},
    "reviewer": {"revision":"1","description":"レビュー担当","instruction":"sourceを読んでレビューする", "tools":["run_js","read_file"]}
  },
  "systemInstruction": "",
  "instructionComponents": [],
  "toolPaths": ["index.js"],
  "tools": [
    {"name":"read_file","revision":"1","description":"Read a text file.",
     "inputSchema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]},
     "promptGuidelines":["Preserve exact identifiers from the file when reporting."],
     "module":"index.js","export":"readFile"},
    {"name":"run_agent","revision":"1","description":"Delegate to a role.",
     "inputSchema":{"type":"object","properties":{"task":{"type":"string"},"role":{"type":"string"}},"required":["task"]},
     "module":"run-agent.js","export":"runAgent"}
  ],
  "hooks": {"module":"hooks.js","handlers":["runtimeStart","beforeTurn","projectRequest","beforeTool","afterTool","afterTurn","runtimeStop"]},
  "rejections": []
}
```

role catalogのkeyが名前になる。role.toolsは使用するtool名の配列で、空配列はtoolなし。
run_jsは選択できるbuilt-inであり、外部tool registryへのmodule登録は不要。
model向けtool definition、JSのtools、保存snapshotは同じrole解決結果を使う。
run_agentを選んだ場合だけ生成JSへapi.agents.runを公開し、同じrun_agent tool／hook経路を通す。
生成JSの公開apiは選択toolの呼出し用で、Go基礎操作は含めない。
外部tool／hook実装用apiはGo基礎操作とraw AgentRunner／AgentControllerを提供する。
これはtool構成の契約であり、JSやOSのsandboxではない。

### 標準role

標準configには以下のroleを定義する。同じroleを親と子に使える。

| role | 指示とtoolセット |
| --- | --- |
| default | 通常の作業と委譲。標準toolすべてを選択する |
| reviewer | 指定範囲のレビュー。run_js / read_file / ls / find / grep / wc / git_inspect |
| explorer | repositoryの構成・実行経路・根拠の調査。reviewerと同じ読取りtool |
| implementer | 指定範囲の実装・確認。読取りtoolとwrite_file / edit_file / command / http / web_fetch / web_search |

親の標準指示は、レビューにはreviewer、調査にはexplorer、実装にはimplementerを選び、
通常は子のtools指定を省略してroleの標準セットを使うよう案内する。
委譲toolを選択した親向けのrole catalogには、各roleの名前・説明に加えて、設定されたtools一覧を
`(tools: [run_js, read_file, ...])`として同じ行へ載せる。空一覧は`(tools: [])`。
これは子のtools指定を省略した場合のセットで、呼出しごとの明示上書きは既存契約に従う。
独自roleにも同じ設定読取りを使い、子の専用instruction全文は親のcatalogへ追加しない。
run_agentまたはspawn_agentを選択した場合に表示し、保存する指示とモデルへ送る指示は同じ組立結果を使う。
model/provider/effortはroleで固定しない。必要な個別上書きは既存の呼出し引数を使う。
reviewerとexplorerにはcommand・write_file・edit_file・子委譲toolを渡さない。
生成JSも同じ選択toolだけを通常公開する。外部tool自身の実装では必要なGo基礎操作を使う。

既存workspaceの編集済みconfigをInstallDefaultsで自動置換しない。
標準role/toolを追加する場合は既存configを編集する。このrepositoryのworkspaceには反映済み。

`config.json`にはprovider registry用の`providers`、credential参照の`credentials`、
全体model既定の`model`も置ける。各roleには部分的な`model`設定を加えられる。
これらはtool／instruction定義と同じconfig source bytesからprovider registryが読み取る。
元configファイルの全文はDBへ自動保存しない。実際に選んだmodelと使用したtool／instruction定義は
既存のexecution・configurationへ保存する。role modelのfield、provider/API/authの区別、
selection優先順位、credential形式は[provider・model設定](providers.md)を参照する。

`tools` のexportは `(args, api) => result` を実装する。`api.readFile`、`api.writeFile`、
`api.command`、`api.http`、`api.webFetch` はcontext付きGo基礎操作へ接続する。
`api.listDirectory`・`api.findFiles`・`api.countFiles`はdirectory探索・file集計へ接続する。
認証付き外部serviceには`api.requestProvider({authProfile,endpoint,method,headers?,body?})`を使う。
JSON bodyとcredential参照だけを渡し、Go側で認証headerを付ける。戻り値は`{status,body}`。
この実装APIは生成JSへ直接公開せず、選択toolを通す。[Exa Web検索](web-search.md)を参照する。
`api.tools.<name>(args)` は別の登録toolを同じVM内で呼ぶ。
各内側callは親IDを持ち、hookや基礎操作の子callを含めてObserverへ通知される。

外部moduleは相対ESM importを使う。Goはcaptureしたresource bytesをmodule loaderから返すため、
capture後に元ファイルを編集しても現在のWorkerには反映されない。次Workerで新しいcaptureを作ると反映される。
Node／Denoのglobalやnpm取得は提供しない。依存は準備済みESMとして拡張directoryへ配置する。

## tool自身の利用指示とhelp（I-21以降）

外部toolは任意の`promptGuidelines: string[]`に短い用途・選択指針・最小契約を書く。
省略・空配列は指示なし。role.toolsと子の明示tools上書きの解決後、有効tool順・配列順に
`## Active tool guidelines`へ集め、各行を`- tool名: 指示`として共通systemInstructionへ加える。
無効なtoolのguidelinesは加えず、親・子は同じcapture経路を使う。

built-in run_jsの常駐指示は、合成・並列・集約・加工を選ぶ場面、async本文、選択済み
`tools.NAME`、await/return、英文目的コメント、timerなしという最小契約を案内する。
長い例やAPI一覧はhelpで取得する。共通の選択方針はbaseと別のtool-usage.mdで管理し、
[base.mdでの差替え](base-instructions.md)でも維持する。通常の単独操作をJSで包む義務はない。

詳細は[ツール利用方針と各toolのhelp](tool-usage-guidance.md)を参照する。
同文書にworkspace → user → 組込みの方針選択、`{help:true}`、`helpFile`/
`runJSHelpFile`、capture・保存、既存configを上書きしない更新手順を定める。

集約欄は`instruction:active-tool-guidelines` componentへ記録する。
`Snapshot.JSON()`のtool宣言はoptional `promptGuidelines`を持つが、現行appの保存用snapshotは
`tools`欄を除き、組立済み指示等を保存する。元config・module・help本文全体のDB archiveではない。
provider定義はname/description/inputSchemaで、guidelines専用fieldはwireへ送らない。
inputSchemaには通常requiredを維持した共通help分岐を加える。
集約した指示は通常のcontext budgetとmodel request保存・attributionの対象になる。

標準toolのdescription・引数description・guidelinesは実装済みの入力と戻り値を説明する。

| tool | 標準の利用案内 |
| --- | --- |
| read_file | 1始まりのoffset / limitで行範囲を読む。省略時は全文。textと行数、totalLines、続きがあればnextOffsetを返す。 |
| edit_file | edits配列のliteral全一致置換を順に適用して一回保存。各editと合計の置換件数を返す。空oldText・一致なしは0件、空newTextは削除。 |
| ls / find | entry情報とtree、またはglob・種類でpathを探索する。offsetは0始まり、limit省略は100。次頁はnextOffset。 |
| grep | rg優先、未導入ならGNU grepでregex／literal・周辺行・一致file一覧。隠しfileを含む。結果のbackend/ignoreAppliedで実際の探索方式を示す。offset/limitは周辺行込みの出力行またはfile一覧の範囲。 |
| wc | pathsの各fileと合計の改行数・Unicode空白区切りの単語数・byte数を返す。 |
| git_inspect | status / diff / log / showの読取り。paths・rev・staged・stat指定と結果範囲を使う。logはcommit単位、それ以外は出力行単位。 |
| command | programとargsで直接実行し、workspaceをDirにする。shell構文はshellを明示する。args/stdinは省略でき、非0終了も通常結果のexitCode/stderrで判断する。 |
| run_agent / spawn_agent | blocking結果と起動結果を区別する。role省略はconfig.defaultRole。modelは親selection→子role設定→呼出し上書き、provider変更時の省略値は変更先の既定。tools省略はrole一覧、空配列はtoolなし。 |
| agent_status | spawnが返したexecutionIdを使う。初回after省略、以後は同じ子のnextCursorを渡す。完了待ちはcollectを使う。 |

modelへ公開する引数の型・required・省略動作と説明を外部宣言に持たせる。
子は独立会話なのでtaskに必要な文脈を書く。spawnの結果だけでは完了を示さず、
親終了時に残る子は取消されるため、回答に必要な子の結果は親終了前に回収する。

## 子agentの並行実行に関する利用指示の背景

2026-10-15、利用者指示でメモ。Session `600ce255`では、親が「探索子を2つ起動しつつ、
自分は設計文書を読む」と述べたが、同じモデル応答で同期型`run_agent`を2件返した。
実際には最初の子の完了待ちとなり、その間は2件目の起動も親の調査も進まなかった。
最初の子は調査を続けており、確認時点で回収処理が停止している証拠はなかった。

当時のdescription / promptGuidelinesには、run_agentは完了待ち、spawn_agentは開始後に親も
作業を続けられることが明記されていたが、同じ応答の通常tool callが順次実行される点は
明記されていなかった。モデルが並列実行されると思った可能性はあるが、原因は未確定。

現在は独立した共通方針と詳細helpで以下を案内する：

- 通常の複数tool callは順次実行され、run_agentを複数並べるだけでは子は並行しない。
- 子を動かしながら親も作業する場合は、spawn_agentで必要な子を開始し、後でcollect_agentする。
- 子どうしを並行させ、親は結果を待つ場合は、run_js内でPromise.allとapi.agents.runを使える。

利用指示は更新したが、通常tool call自体の順次実行は変更していない。
この観測や指示更新だけから、実モデルの選択が改善したとは結論しない。

## 詳細利用指示の遅延読込み

詳細は各toolの共通helpで取得する。独立した辞書tool・目的検索・検索indexは追加していない。
登録と利用は[ツール利用方針と各toolのhelp](tool-usage-guidance.md)に従う。

## 保存snapshot

`Snapshot.JSON()` は旧 `WorkerConfigurationSnapshot` の保存JSONを生成する。
`schemaVersion`、UUIDの `configurationId`、`agent`、`source`、`systemInstruction`、
`instructionComponents`、`toolPaths`、`tools`、`hooks`、`rejections` を保持する。
snapshot.agentには実role名・revision・instruction・選択toolsを保存し、
agent.agentsはrun_agentまたはspawn_agentを選んだWorkerのcatalog名、それ以外は空配列とする。
source.fileは使用したconfigの絶対locator。組立済みsystemInstructionとmodel requestが実際の操作説明を保持する。
親・子ともmodel解決後に、実際のprovider・API・model・effort・role・workspaceを
`Current execution (resolved values)`としてsystemInstructionへ加える。
同じ本文を`instruction:execution-context` componentとして保存し、configの既定値や
過去の会話から現在値を推測させない。credential・Authorizationは含めない。
run_jsのasync本文ではPromiseと選択toolを使える。setTimeout／setInterval等のtimer APIは
提供していない。子の完了待ちはcollect_agent／api.agents.collectで行い、中間報告は親の
独立作業の後などにstatusで読む。明示的な時間待ちはcommandを選択している場合に限り
既存のsleepコマンドを案内する。
I-18では共通実行層が[workspace指示とskills](workspace-instructions-skills.md)を追加する。
AGENTS.md本文とskill manifestは実指示へ組み込み、生成componentをinstructionComponentsへ記録する。
設定に書かれた既存instructionComponentsを命令入力として解釈する処理は追加しない。
新loaderのAGENTS sourceは実行context側で保存し、JS module loaderのresourceへ追加しない。
run_jsは選択時だけsource bundledとして保存する。
各toolのsnapshot `contract` は `{name,description,inputSchema}` で、`name` にtool名を記録する。
runtimeの外部tool API contract versionは別に `hjh-tool/v1` とする。
hook contractは `hjh-hook/v1`。`fileAccess` は `unmanaged`、
`paths.allow` と `paths.deny` は空配列で、旧permission動作を実装しない。

実source bytesはsnapshot JSONへ埋め込まず、メモリ上の`Snapshot.Resources`に保持する。
各resourceには相対 `Path`、`LogicalIdentity`、capture時の `SourceLocator`、`sha256:` digest、
正確な `Bytes` がある。Workerはこのbytesをmodule loaderへ渡す。
2026-10-13の利用者指定により、ツール・hook・依存・元configをhistory DBへ自動archiveする処理は廃止した。
AGENTS.mdの指示原文と使用構成・モデル入力・呼び出し/結果の保存は維持する。
configurationIdはcaptureごとのUUIDであり、source digestとは別の識別子である。

## Worker APIと寿命

`jsruntime.New(ctx, Options) (*Worker, error)` はWorker専用QuickJS VMを作り、capture済みESMを評価してから返す。
`Options` は `Snapshot`、Session `Workspace`、共通 `contracts.Observer`、
必要なら `ObserverParent` と `host.Host`、委譲操作の `contracts.AgentRunner` と非同期操作の `contracts.AgentController` を受け取る。
`Worker.Call(ctx, name, args)` は `json.RawMessage` の構造化結果を返し、
`Worker.Definitions()` はroleが選んだbuilt-in／外部toolのmodel向けdefinitionを返す。
`Worker.Hook(ctx, name, input)` は同じVM owner上でlifecycle hookを実行し、
`Worker.HookHandlers()` はcapture済みmoduleで実際に見つかったhook function exportsを返す。
`Worker.Available()` はVMがまだ呼出可能かを示す。
`Worker.Close()` はVMと停止中のhost workを閉じる。

JS実行基盤は`github.com/go-quickjs/go-quickjs v0.25.2`。
VMの生成、module評価、JS call、Promise job処理、VM終了は一つのowner goroutineだけが行う。
Go host workは結果をownerへ返し、別goroutineからVM値を操作しない。
一つのWorkerで複数の `Call` を行うと同じVMと外部module stateを使い、別Worker間ではVMを共有しない。
同じWorkerへのlifecycle hookも同じmodule stateを使う。実行前のsource captureを更新しても
既存Workerは変わらず、次のWorkerで新しいcaptureを使う。
context取消はJSのCallContext／RunJobsContext、HTTP request context、Linux process group停止へつながる。
commandはgroup停止後に `Wait` してから戻る。取消されたWorkerを閉じた後は新Workerを作成できる。
host／子AgentのGo完了queueをownerが受け取り、Promiseを明示的にsettleする。
外側CallのPromise決着時にblocking run_agentのcall scopeを取消し、初回join、残るhost／JS jobsのdrain、遅いAgentRunnerの再join、
委譲tool／hookの終端記録を終えてから返す。root決着後の子結果は観測へ保存し、JSを再開させない。
正常終了したcallの取消contextは次Callへ残さない。

## toolとhookの動作

model向けにはrole.toolsで選んだbuilt-in `run_js` と外部toolを定義する。
`run_js` の通常引数は `{ "code": "..." }`。codeはasync JS functionのbodyとして評価され、
外部toolを `tools.read_file(...)` のように呼び出せる。直接のmodel tool callと生成JS内tool callは、
同じ外部module実装とhook処理を使う。選択toolの`{help:true}`は両入口で共通処理し、
通常実装・beforeTool・afterToolを通さず、通常のtool観測と結果保存を残す。

利用者指定によりrun_jsのpromptGuidelinesから組み立てた共通実行instructionは、codeの先頭へ目的を
短い英文の1行`//`コメントとして書くようモデルへ指示する。親・子roleに共通して適用する。
共通toolactivityが最初の空行以外の行の`//`本文をpreviewにし、実行開始・結果・履歴復元へ
同じ説明を渡す。表示は既存の薄い通常色。コメントなしは説明を付けずtool名だけを表示し、
JSの実行契約やtool引数・保存原文は変更しない。

通常操作では`beforeTool({name,args}, api)`が返した値をtool引数として使う。
toolが成功した場合だけ `afterTool({name,args,result}, api)` を呼び、その返却値をtool結果とする。
hook自体もcallとしてObserverへ記録し、hook内のGo操作・別toolはhook IDを親に持つ。
toolまたはhookの例外は呼出し元へそのまま伝える。

標準の外部toolは `run_agent`、`spawn_agent`、`agent_status`、`collect_agent`、`cancel_agent`、`steer_agent`、
`read_file`、`write_file`、`edit_file`、`ls`、`find`、`grep`、`wc`、`git_inspect`、
`command`、`http`、`web_fetch`、`web_search`。
file pathとcommand `Dir` はSession workspaceから解決し、プロセス全体のworking directoryを変更しない。
`read_file`のoffsetは1始まり、limitは行数。各行の改行を保持し、最終行の改行も補わない。
offset/limit省略時は全文、EOF以降のoffsetは空text。textのほかoffset / lines / totalLinesと、
続きがあればnextOffsetを返す。fileの本文行数は、改行なしの最終行も1行として数える。
`edit_file`は`{path,edits:[{oldText,newText},...]}`を受け、各置換を前段の結果へ適用する。
全ての完全一致箇所を置換し、最後に一回保存して`{path,replacements,edits:[{replacements}],bytes}`を返す。
単一置換も配列で指定する。旧single oldText/newText入力を読替える機構は追加しない。
`write_file`は不足する親directoryも作成する。

`ls`は名前順・深さ優先のflat entriesにdepthを付ける。tree:falseでは直下だけ、tree:trueは
既定depth 3。directory symlinkはentryとして表示し、再帰探索しない。
`find`のslashなしglobは各basename、slashありglobは探索root相対pathに一致させ、**はdirectoryを跨ぐ。
file / directory / anyを選べ、excludeでdirectoryに一致すると配下も省く。
隠し・ignored entriesを含み、.gitignoreを自動適用しない。ls/findはGo filesystemで動く。
`grep`はPATH上のrgを優先し、なければGNU grepを使う。初回callで選んだ実行fileをWorker内で保持する。
regexはrgならripgrepの構文、GNUなら拡張正規表現。patternKind:literalは両方とも固定文字列。
rgはignore fileを既定で適用し、includeIgnored:trueで無効化する。GNUは.gitignoreを適用せず、
binary fileをskipする。globはrgのnative path glob、GNUではfilename include/exclude globとして使う。
`!`は除外指定で、GNUでは--excludeへ渡す。globの優先順位などはbackendのnative動作に従う。
結果にbackend:rg/grepとignoreAppliedを付け、モデル向けtool説明も両方式の違いを示す。
lines出力のlineは1始まり、matchは一致行／周辺行を区別し、textは改行を保持する。
UTF-8以外のpath/textは`{base64}`として保持する。output:filesは一致pathを重複なしで返す。
GNUのtextはnative出力の改行を含み、source末尾に改行がなくてもGNUが付ける改行を含む。
0件は通常結果。grepのmatchesは一致行数で、totalはpage対象の行数またはfile数。
`wc`のlinesは改行文字の数で、read_fileのtotalLinesとは定義が異なる。
`git_inspect`にはGitが必要。固定した4操作を既存command基礎操作で実行し、任意argvを公開しない。
statusではoptional index refreshを行わず、diff/showはexternal diff/textconvを実行しない。
ls/find/grepとGitの出力行のoffsetは0始まり、既定limit 100。Git logだけはcommit単位で既定20。
続きがあればnextOffsetを返す。省略したoptionをモデルに埋めさせる必要はない。
`command` は `program`、`args`、`stdin` を受け、`exitCode`、`stdout`、`stderr` を返す。
nonzero exitは構造化結果であり、tool失敗にはしない。
`http` はGETを行い、statusとbodyを返す。HTTP statusは結果データである。
ページ読取りは`web_fetch`を使う。HTML可読化、通常64 KiB上限、原本文の`save_to`保存は
[I-14契約](i14-web-context-contract.md)を参照する。
標準toolの振る舞いは外部JS moduleにあり、host packageはfile、HTTP、Linux processの実処理だけを行う。

Observerには外側tool、生成JS内tool、hook、host操作それぞれの開始・終了を `contracts.Observation`
で通知する。各開始はIDを持ち、親IDは外側runtime callから内側tool、hook、hostへ連鎖する。
Observerのsinkが保存形式を所有し、runtimeとhostはDBへ直接依存しない。

## I-20：execution lifecycle hooks

`hooks`は引き続き単一のESM moduleを指定する。設定snapshotの`handlers`は宣言情報として保存される。
呼出対象は、評価したmoduleがexportする対応functionで決まり、設定配列による追加の絞り込みはしない。
実際に存在するhook function一覧はexecution開始時の`hook/handlers`記録に保存する。
未定義のfunctionは呼ばれず、複数module管理や専用hook CLIはない。

一つのWorkerはrootまたはchildの一つのexecutionを所有する。rootの各Session turnと各child executionは
それぞれ新しいWorkerを作る。Core起動、Session作成、TUI接続・再接続ではhookを起動しない。
編集済みsourceは次に作られるWorkerのcaptureから使われる。

対応exportと呼出順は次のとおり。関数形式はtool hookと同じ`(input, api)`で、非同期処理をawaitする。

| export | 呼出し | 動作 |
| --- | --- | --- |
| `runtimeStart` | Worker作成後、最初のmodel入力前に一回 | 返した文脈をexecutionの指示へ追加 |
| `beforeTurn` | taskのturn処理を始める前に一回 | 返した文脈を今回のturnの指示へ追加 |
| `projectRequest` | 各model要求の予算選択前 | `api.context`で原文を読み、今回の送信内容を差し替える。返却値は使用しない |
| `beforeTool` | modelまたは生成JSからのtool実行前 | 既存契約どおり、返却値でtool引数を置換 |
| `afterTool` | toolが成功した後だけ | 既存契約どおり、返却値でtool結果を置換 |
| `afterTurn` | terminal保存とrootの採用判断後 | 保存済みoutcome・settlementを使う後処理 |
| `runtimeStop` | `afterTurn`後、Workerを閉じる前 | settlementがあれば参照し、停止理由を使う後処理 |

lifecycle hookの`input`は通常JSON値で、`api`にはhook内host操作・登録tool呼出しと同じ実行APIを渡す。
`projectRequest`の入力は`{runtime, modelStep}`である。要求scopeの
`api.context.get/history/measure/select/replace`、保存と次stepの扱いは[コンテキストAPI](request-context.md)を参照する。
`runtime`にはworkspace root、agent名、root/child role、Session/execution/parent/spawnの識別子、
turn番号、解決済みmodel selection、Worker開始時刻`startedAt`と`timeZone`を含む。
開始時刻はGo側で作ったRFC3339Nano値であり、timezoneは設定された`TZ`名、またはGoのzone名を使う。
実model selectionにはcredentialの値やAuthorizationを含めない。

`context`は呼出時点の`systemInstruction`、`instructionComponents`、有効tool definitionsと、
Sessionのturn範囲・件数・execution locatorを渡す。会話全文をhookごとに複製するAPIではない。
`beforeTurn`には今回の`task`を渡す。`afterTurn`には実行全体の`outcome`と、
保存済み`outcome`・`adoption`・`terminalRecordId`・任意の`adoptionError`を含む`settlement`を渡す。
`runtimeStop`には実行結果に対応する`reason`と、確定できている場合の`settlement`を渡す。

`runtimeStart`の後に`beforeTurn`を呼び、既存の組立済み指示へその順で追加する。
両hookは値を返さない、`undefined`／`null`を返す、または`{context: string[]}`を返せる。
文字列は配列順に改行で結合し、hook順に今回の指示へ加える。
その追加文脈とhook結果はcapture済みconfig snapshotを書き換えず、
hook result recordへの参照を持つinstruction componentとして保存・attributionされる。
追加後の指示は通常のcontext budget経路へ進み、次turnへ累積しない。
`afterTurn`と`runtimeStop`の戻り値は後続のmodel入力や結果を変更しない。

終了処理は既存のCall/Hookがhost work、blocking child、終了観測をjoin/drainした後に進む。
子executionの清算後、terminalとrootの採用判断を保存し、`afterTurn`→`runtimeStop`→`Worker.Close`の順にawaitする。
provider待機中の取消ではWorkerを清算に使える。active JS/host call中の取消でWorkerが閉じた場合は
終了hookを`worker_unavailable`でskipし、別VMを作らない。
終了hookのJavaScript例外はnoticeとして保存し、確定済みのanswer・outcome・採用状態を変更しない。
終了後hookの詳細recordは履歴に保存し、通常CLIのstderrとTUI noticeには失敗・skipを表示する。
checkpoint、要約、追加turnへの文脈再利用は行わない。

## I-03：roleを選んで子Agentへ委譲

生成JSの `api.agents.run({role?,task,model?,tools?})` は選択したrun_agentを呼び、Promiseを返す。
外部tool／hookの同名APIは実装用のraw AgentRunnerに接続する。
モデルにはtool definitionと短いtool guidelinesで機能・引数・最小契約を提示し、
詳しい使い方や組合せ例は対象toolの`{help:true}`で取得する。

```js
const results = await Promise.all([
  api.agents.run({role: "reviewer", task: "srcを読んで主要モジュールと責務を報告して"}),
  api.agents.run({role: "reviewer", task: "docsを読んで利用手順を報告して"})
]);
return results;
```

結果は `{executionId, outcome, finalText?, error?}`。子の作業失敗は、終了証拠を保存できた場合に
構造化結果として返す。取消はrejection、履歴保存の失敗は共通Go実行側へ伝える。
親の会話を暗黙に渡さず、必要な文脈はtaskへ書く。
AGENTS.mdとskill一覧は子の開始時にも同じworkspaceから読み直す。
skill本文は子が必要時に標準read_fileを呼んで取得する。
子のroleは明示値、子開始時のconfig.defaultRoleの順で選ぶ。親のrole instructionをコピーしない。
親も同じgenericへ外部roleを渡し、同じroleをroot／子のどちらにも使える。
子は独立した会話・loop・VM・journalを持ち、親のworkspaceと各executionのstep上限を使う。
modelは親の実selection→子role.model→呼出しmodelの順で既存resolverに渡す。providerが変わると
省略したmodel/effort/authは指定先providerの既定を使う。tools省略は子roleの一覧、指定はその実行の
一覧を置換し、空配列はtoolなし。登録済みtoolとrun_jsを指定でき、元configを編集して実現しない。
親が解決したconfigの絶対pathから子開始時に再captureし、親Workerの既存captureは変更しない。

JS runtimeはAgent専用のcall ID・観測と、外側call単位の子context・Go jobを所有する。
AgentRunnerは通常のGo値を受け渡しし、VM値の操作は引き続きVM ownerだけが行う。
外側JS Promiseの成功・例外・取消時に残る子をcancel・joinし、子Worker終了・終了保存が済んでから
外側callを返す。未awaitの子も同じ境界で終了する。既存host操作のdrainとは別のscopeで管理する。
未awaitの委譲tool／hookは子join後、外側call終了前にVM ownerが終端観測を閉じる。
JSのfinally／__endが再開することに依存せず、親の正常結果・原例外を維持する。
親取消は子のモデル接続・JS・HTTP・commandのcontextへ伝播する。
詳細は[I-03計画](../plans/i03-generic-roles.md)を参照する。

## I-19：非同期の子と報告

`spawn_agent`／`api.agents.spawn({task,role?,model?,tools?})`はadmission保存後にexecutionIdとstateを返す。
子の実行は親executionのGo ownerに属し、spawnを呼んだJS Callが返った後も続く。
親は複数子を開始して自分の作業を進め、後のmodel stepでstatus/collect/cancelを呼べる。
各生成APIは対応するtoolが選択済みの場合だけ公開し、外部tool/hook/履歴の通常経路を使う。

`agent_status`／`api.agents.status({executionId,after?})`はstate、role、実model/tools、progress、
保存済みassistant reportsとnextCursorを返す。progressのphaseはstarting/model/tool/between_steps/settled。
after省略は最初から。nextCursorを次回afterへ渡すと新しい報告を取得する。
reportのsourceはexecutionId/messageOrdinal/textで、本文とcursorは同じSQL cutから読む。
報告取得で共有既読状態を変更せず、thinking/tool/replay/stream断片を返さない。
生成JSでは必要な報告を外側returnに含めて次の親モデル入力へ渡す。

`collect_agent`／`api.agents.collect({executionId})`は子の終端保存まで待って構造化結果を返す。
`cancel_agent`／`api.agents.cancel({executionId})`はその子を取消し、同じく保存終了を待つ。
終了済み子への繰返し操作は確定結果を返し、再実行しない。
非同期子の失敗/取消は保存済み結果として判断でき、blocking run_agentのcall単位取消とは区別する。
親終了では準備中を含む残る子を取消/joinし、子の終了保存の後に親終端と採用を確定する。
子の履歴はnon-canonicalで、親へは通常tool結果として返した内容だけが入る。
TUI detachは親子の取消理由にせず、Coreで続行する。

`steer_agent`／`api.agents.steer({executionId,text})`は親から実行中の子へ指示を送る。
受付保存後にinstructionId、送り手parentExecutionId、steerCallId、sequence、contentDigest、
accepted/applied/unappliedのreceiptを返し、statusのinstructionsで最新状態を読む。
指示本文は子のuser/steering messageとして一度保存し、receiptの状態変更は既存semantic記録へ残す。
子messageとapplied receiptを同じtransactionで保存してから、既存AdditionalInput境界でloopへ渡す。
生成中request/toolを中断せず、最初のrequest前、tool群結果の保存後、最終応答後に取り込む。
最終応答後に受付済み指示があれば子は続行し、取消等では未適用理由を終端前に保存する。
人間のF3受付ownerを子へ継承せず、子ごとに受付中の保存と順序・閉鎖を所有する。
appliedは入力への取込みを示し、指示どおりの結果かは後の報告とcollectで判断する。
S5のfocused確認と通常・批判的reviewを完了した。
[I-19計画](../plans/i19-child-agent-control.md)と[実装結果](../results/i19-child-agent-control.md)を参照する。

### Web取得の既定値（2026-10-15）

web_searchはnumResults省略時に10をExaへ送る。明示指定があればその値を使う。
内部要求はtext:false・highlights.maxCharacters:1000へ固定し、モデル向けcontents引数は持たない。
検索で出典を選び、本文はweb_fetchで読む。これは件数・抜粋長の要求であり、
response全体のbyte上限ではない。
web_fetchの通常受信上限は64 KiB。save_toは全文をfileへ保存し、metadataだけを返す。
