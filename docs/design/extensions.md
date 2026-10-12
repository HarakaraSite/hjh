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
     "module":"index.js","export":"readFile"},
    {"name":"run_agent","revision":"1","description":"Delegate to a role.",
     "inputSchema":{"type":"object","properties":{"task":{"type":"string"},"role":{"type":"string"}},"required":["task"]},
     "module":"run-agent.js","export":"runAgent"}
  ],
  "hooks": {"module":"hooks.js","handlers":["runtimeStart","beforeTurn","beforeTool","afterTool","afterTurn","runtimeStop"]},
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

`config.json`にはprovider registry用の`providers`、credential参照の`credentials`、
全体model既定の`model`も置ける。各roleには部分的な`model`設定を加えられる。
これらはtool／instruction定義と同じconfig source bytesからprovider registryが読み取り、
そのrunで使ったsource bytesとして保存される。role modelのfield、provider/API/authの区別、
selection優先順位、credential形式は[provider・model設定](providers.md)を参照する。

`tools` のexportは `(args, api) => result` を実装する。`api.readFile`、`api.writeFile`、
`api.command`、`api.http`、`api.webFetch` はcontext付きGo基礎操作へ接続する。
`api.tools.<name>(args)` は別の登録toolを同じVM内で呼ぶ。
各内側callは親IDを持ち、hookや基礎操作の子callを含めてObserverへ通知される。

外部moduleは相対ESM importを使う。Goはcaptureしたresource bytesをmodule loaderから返すため、
capture後に元ファイルを編集しても現在のWorkerには反映されない。次Workerで新しいcaptureを作ると反映される。
Node／Denoのglobalやnpm取得は提供しない。依存は準備済みESMとして拡張directoryへ配置する。

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

実source bytesはsnapshot JSONへ埋め込まず、`Snapshot.Resources` の個別recordに保持する。
各resourceには相対 `Path`、`LogicalIdentity`、capture時の `SourceLocator`、`sha256:` digest、
正確な `Bytes` がある。history側は旧 `contents`／`resource_revision` の保存契約へ対応付ける。
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
`run_js` の引数は `{ "code": "..." }`。codeはasync JS functionのbodyとして評価され、
外部toolを `tools.read_file(...)` のように呼び出せる。直接のmodel tool callと生成JS内tool callは、
同じ外部module実装とhook処理を使う。

利用者指定によりrun_jsのtool descriptionと共通実行instructionは、codeの先頭へ目的を
短い英文の1行`//`コメントとして書くようモデルへ指示する。親・子roleに共通して適用する。
共通toolactivityが最初の空行以外の行の`//`本文をpreviewにし、実行開始・結果・履歴復元へ
同じ説明を渡す。表示は既存の薄い通常色。コメントなしは説明を付けずtool名だけを表示し、
JSの実行契約やtool引数・保存原文は変更しない。

`beforeTool({name,args}, api)` が返した値をtool引数として使う。
toolが成功した場合だけ `afterTool({name,args,result}, api)` を呼び、その返却値をtool結果とする。
hook自体もcallとしてObserverへ記録し、hook内のGo操作・別toolはhook IDを親に持つ。
toolまたはhookの例外は呼出し元へそのまま伝える。

標準の外部toolは `run_agent`、`spawn_agent`、`agent_status`、`collect_agent`、`cancel_agent`、`steer_agent`、
`read_file`、`write_file`、`edit_file`、`command`、`http`、`web_fetch`。
file pathとcommand `Dir` はSession workspaceから解決し、プロセス全体のworking directoryを変更しない。
`edit_file` は `oldText` の全ての完全一致箇所を `newText` に置換して書き込み、置換数とbyte数を返す。
`command` は `program`、`args`、`stdin` を受け、`exitCode`、`stdout`、`stderr` を返す。
nonzero exitは構造化結果であり、tool失敗にはしない。
`http` はGETを行い、statusとbodyを返す。HTTP statusは結果データである。
ページ読取りは`web_fetch`を使う。HTML可読化、通常1MiB上限、原本文の`save_to`保存は
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
| `beforeTool` | modelまたは生成JSからのtool実行前 | 既存契約どおり、返却値でtool引数を置換 |
| `afterTool` | toolが成功した後だけ | 既存契約どおり、返却値でtool結果を置換 |
| `afterTurn` | terminal保存とrootの採用判断後 | 保存済みoutcome・settlementを使う後処理 |
| `runtimeStop` | `afterTurn`後、Workerを閉じる前 | settlementがあれば参照し、停止理由を使う後処理 |

lifecycle hookの`input`は通常JSON値で、`api`にはhook内host操作・登録tool呼出しと同じ実行APIを渡す。
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
モデルには実行instructionと `run_js` のdescriptionで入力・結果とawait方法を提示する。

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
