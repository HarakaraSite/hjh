# Henji Host / Agent Worker アーキテクチャ

ステータス: 承認済みアーキテクチャ。roadmap、実装計画、Human Gate、実装認可ではない

対応するプロダクト構想は
[`docs/concepts/experience-driven-self-revision.md`](../concepts/experience-driven-self-revision.md)
である。この文書は構想の目的を再定義せず、その実行基盤の構造を定める。

この文書は、Henji HostとヘッドレスなDeno Agent Workerの責務、状態、lifetime、commit境界を定める。

基盤のsource照合: `a78c2076`（2026-10-04、Increment 182まで）。 JSON Agent設定・現在tool
folder・新履歴DBは[Increment 181](../increments/increment-181.md)、
model省略childへの実効認証参照継承は[Increment 182](../increments/increment-182.md)の実装・受入結果を参照する。
追加照合: 2026-10-07、source
`68ab5dd0`。189・191・192・193・197・198・199・201・202・204・206・207・208に
関わるhook、tool、provider、Data処理、TUIの現行記述を照合・反映した。全incrementの結果を本書へ転記するものではなく、
個々の採用範囲・受入・配置結果は個別increment文書を参照する。 導入時のmanaged
Definition/module方式は181で廃止した。過去の設計・受入記録は個別incrementに保持し、現行方式と区別する。

自己拡張と構成操作の方針照合: 2026-10-08。利用者判断に従い、run_typescriptによる自己拡張の責務と
既存の反映経路を明記した。rebuildは実装せず、Agentによるroot
model・effort変更は効果が薄いため見送る。
run_typescriptのfileアクセス設定は[Increment 213](../increments/increment-213.md)の現行経路へ照合した。

複数providerを同一SessionとWorker内で扱うroute、認証profile、model一覧、account binding、provider
stateとevidenceの境界は、 専門設計
[`docs/architecture/multi-provider-routing-and-auth.md`](multi-provider-routing-and-auth.md)
を正本とする。

## プロダクト上の決定

- HenjiはDenoベースのagent harnessである。Agentの挙動はJSON設定で選び、共通runtimeが構成する。
  Agent設定の`name`、`revision`、`instruction`、`tools`、`agents`はdataであり、executable Agent
  Definitionではない。
- 実行primitiveはDeno Web
  Worker（`new Worker`）である。`AgentWorkerGeneration`は一時的なexecutorであり、 durable
  stateの正本でも、別trust tierでもない。外部toolのTypeScriptは同じtrusted-local境界で実行する。
- Hostは現在のAgent JSON、tool
  folderとmetadataを選び、data-onlyな選択結果をWorkerへ渡す。Workerは外部toolを
  importしてfactoryを一度呼び、同じToolの宣言とexecutorをmodel提示・dispatchに使う。functionは境界を渡さない。
- `revision`は任意の版名であり、content digest、exact selector、activation authorityではない。
  configuration
  IDは起動snapshotの独立UUIDで、同じ版名でも内容の違う起動を区別する。snapshotは履歴資料であり、
  source closureを保存・検証・再実行する仕組みではない。
- rootは明示JSON file、明示named Agent、またはconfig
  catalogのdefaultを選ぶ。default指定がない有効catalogでは 同梱defaultを使う。genericは同梱default
  JSONを基底とし、named親のroleを継承しない。
  childは固定roleではなくExecution間の親子関係であり、別Worker・別Executionとして起動する。
- Core/TUI起動とAgent構成成功を分ける。選択Agentやcatalogが使えなくてもCoreの保存履歴・認証・設定操作は使える。
  個別tool/named
  entryの不具合は対象だけを除き、理由をinstructionとSurfaceへ示す。選択済みの不正fileやtoolを
  同梱実装へ暗黙に代替しない。taskの構成拒否はexecution
  admissionより前に返し、TUIはdraftを保持する。
- Henji共通baseはbinary内の最小core、またはuser scopeの`instruction.md`から選ぶ。native
  `AGENTS.md`とSkillは zero-install discoveryを維持し、Agent JSONのrole寄与と共通baseを区別する。
- UIは交換可能なHost-side Surfaceである。Core
  mainは操作判断・実行制御・採用判断・公開revisionと購読、 Data
  WorkerはSession・会話state・履歴と会話payload、API
  WorkerはHTTP/SSE、TUIはterminal/draft/viewportを所有する。 非対話runはheadless Host
  mainとCLI/Data/Agent Workerを使い、HTTP Coreへ接続しない。
- durable historyの保存とturnのcanonical採用は別operationである。Data
  Workerが新しい`history.sqlite3`（schema 1）を
  唯一のproduction履歴正本として所有し、正常完了とHostの採用判断が成立したturnだけをatomicに会話へ採用する。
  cancel/failure/途中本文/tool結果/childもnon-canonical evidenceとして振り返れる。
- 通常履歴はsemantic authority、configuration/build/model/request attribution、短いrequest
  factを保存する。 raw request/response、SSE断片、parser
  transition全文を常設収集せず、credential値とAuthorizationを記録しない。 artifact・human
  exportは正本から生成するread modelであり、全文の別正本を保存しない。
- 保存Sessionの閲覧はWorkerを起動せず、現在設定の解決を必要としない。継続は現在fileから新Workerを構成し、
  過去configurationとの一致を要求しない。過去executionのattributionを書き換えない。
- executableはimmutableな配布artifactとして扱い、writableなconfig/state、workspace
  inputとlifetimeを分離する。 廃止したmanaged store・旧DBのmigration、compatibility
  read、fallback、実data削除は行わない。
- 自己改訂は保存Sessionと履歴、run_typescriptによる自己拡張を基盤に育てる。durable AgentInstanceは
  複数Sessionをstable identityで束ねる追加機能であり、自己改訂の開始条件ではない。Agent自身の観測と
  自己拡張の不足はroadmapで管理する。rebuildは実装せず、Agentによるroot model・effort変更は見送る。

## 用語

| 用語                          | 意味と所有                                                                                                                                                  |
| ----------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `AgentConfiguration`          | name、版名revision、role instruction、tool名、child Agent名を持つdata。Hostが現在fileを選ぶ                                                                 |
| `AgentConfigurationChoice`    | named Agentまたは明示JSON fileの選択。保存Sessionの選択にも使い、過去内容identityとは区別する                                                               |
| `WorkerConfigurationSnapshot` | configuration ID、実効Agent設定、選択元、最終instruction/components、実提示toolの名・版名・選択元・contractとreject理由。Workerが起動時に作りDataが保存する |
| `AgentComposition`            | Worker内のmodel、registry、loop、instruction、context等の実体。起動時に構成し、idle時のSession model selectionを反映する                                    |
| `AgentManifest`               | 実効model、maxSteps、resource等のdata-onlyな説明。configuration snapshotや実際の各request contextと区別する                                                 |
| `HenjiInstructionRevision`    | 共通baseの観測内容・content digest・source attribution。Agent/toolの任意revision labelとは別の内部表現                                                      |
| `AgentWorkerGeneration`       | 一つの起動設定で動くephemeral Deno Worker。Hostが開始・停止・置換を所有する                                                                                 |
| `Task`                        | 一回の依頼としてadmitする入力。recall projectionはこの境界で消費する                                                                                        |
| `Execution`                   | taskの独立したattempt。base Session revision、configuration ID、build、model、親子関係、進捗、outcome、採用状態を相関する                                   |
| `Turn`                        | 正常完了とHost採用判断を経てcanonical Sessionへ一括採用された会話単位。回答の正しさや満足を保証しない                                                       |
| `HistoryLogicalRecord`        | execution内ordinal、semantic contentと関係を持つ観測fact。physical locatorをidentityにしない                                                                |
| `HistoryProjection`           | 保存正本から生成するhuman view、export、artifact、context表示等                                                                                             |
| `AgentInstance`               | 複数Sessionをstable identityと共通設定で束ねる未実装の追加概念                                                                                              |
| `Surface`                     | Host-side interaction adapter。現行TUI、HTTP/API、run CLIはAgent Workerと独立したlifetimeを持つ                                                             |
| `Core`                        | workspace、稼働Session slot、公開revision・購読、Worker/process/childを所有するHost process。epochはprocess identity                                        |
| `Data Worker`                 | Host所有のcanonical Session・semantic history・SQLiteと会話payloadのowner。Agent置換でも正本を保つ                                                          |
| `API Worker`                  | Coreへの非同期operation portを持つHTTP/SSE adapter。Coreが起動・終了を所有する                                                                              |
| `CLI Worker`                  | runの引数・stdin・text/NDJSON/stream・stdio drainを持つadapter。headless Host mainが所有する                                                                |

## externalization taxonomy

外部化できることを、全resourceへ同じloader・immutable revision方式を要求することと同一視しない。

| 分類                      | 現行の対象と境界                                                                                                                                             |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 現在fileから選ぶ設定      | Agent JSON、agents.json、tools.json、tool folder metadata、provider/credential declaration。Hostが選択し、必要なdataをWorkerへ渡す                           |
| native input/state        | workspace file、AGENTS.md/Skill、user instruction、credential、model preference、Session、tool call/result。各ownerと保存scopeを維持する                     |
| Worker-local executable   | 外部toolのentryとlocal import。Workerで直接import・factory実行し、現行sourceをimmutable managed storeへcopyしない                                            |
| binary platform authority | Host coordinator、Worker lifecycle/protocol、共通agent loop、canonical Session・atomic commit、credential resolver、build。変更は新しいHenji buildとして扱う |
| 未採用の追加方式          | managed Skill/resource transport、MCP、Surface loader、loop/context差替え等。具体的な利用目的から採否・owner・lifetimeを決める                               |

managed Agent/tool revision、source closure install、exact selector、module
import/exportは181で廃止した。
将来新しいkindをmanaged化する場合も、それだけを理由に廃止済み方式の復活や全resource共通frameworkを要求しない。

## Host / Worker 境界

現行の配置と所有関係は次のとおりである。

```text
TUI / 外部HTTP client（Surface）
        │ HTTP operation / Session snapshot・update（SSE）
        ▼
API Worker ── data-only operation・購読 ── Core main（Host）
                                            │ control・採用判断・process/child
                  Data Worker ◀── data ──▶ AgentWorkerGeneration
                     ▲   │                  JSON設定 → Composition → turn
     Core mainからcommand│                  provider・tool semantics・context
                         ▼
              workspace共通SQLite（Host-owned semantic authority）

henji runのprocess
CLI Worker ── data-only 選択・実行要求 / event・結果 ── headless Host main
                                                       ├ Data Worker
                                                       └ AgentWorkerGeneration
                                                         ↔ Data直接channel
```

API/Data/CLI WorkerはHost側の実行場所であり、Agent Workerとは責務を区別する。
`henji run`は独立したheadless Hostから同じData/Agent経路と共有履歴DBを使い、HTTP Coreへ接続しない。

この図が示すのは所有関係であり、wire schemaではない。Host–Agentのcontrol contractは
`slice1-data-only-v1`、Host–DataとAPI/CLI adapterのportはそれぞれの内部message schemaを使う。
protocol versionのnegotiationはなく、 現在のmessage
schemaを恒久的な契約として固定しない。将来拡張時のmessage、handshake、error互換性、 version
migrationは未設計である。

### 現行Coreの状態所有と接続

一Coreは一つのcanonical workspaceと稼働Session slotを持つ。複数Coreは同じworkspace・XDGで別Sessionを
並行実行でき、保存先は`${stateRoot}/<workspaceDigest>/history.sqlite3`を共有する。 Session writer
lockは同一Sessionの二重writerを防ぐ。DB初期化はworkspace単位で同期し、write接続は busy
timeoutで待機する。restart reconciliationはSession／execution lockを取得できたactive記録だけを
対象とし、別Coreの生存実行をinterruptedへ変更しない。

Coreのepoch、endpoint、startup／instance lockとboot結果は
`${stateRoot}/cores/<workspaceDigest>/<coreEpoch>/`へ置く。epochはprocessの識別であり、Core終了後に
同じidentityで自動再起動するdurable Instanceではない。configとcredentialは共有XDG
scope（credential値はstate配下の専用root）を使い、Core数を保存先の分割や設定の自動同期へ置き換えない。

`henji core list/status`でCoreを発見・照会し、`core stop --core ID`で一Coreだけを停止する。
各Coreは自分のWorker／child／tool groupを所有する。親cancelは対象親子、Core shutdownはそのCoreだけを
清算し、他Coreを停止しない。同Coreへ複数TUIを接続でき、draftとviewportはclientごとに独立する。
予約follow-upは稼働Coreのpending stateであり、Core再起動後のmailbox復元は提供しない。

`v0/agent/host/core_service.ts`と`application_service.ts`が共通read modelとoperationを提供し、
Dataのencoded会話payloadを小さいcontrol stateと合成する。 `v0/agent/http/api_worker_client.ts`がCore
mainへのportを接続し、API Worker内の
`api_bootstrap.ts`・`server.ts`、`v0/api/`を介してTUIへsnapshot/updateを返す。operation結果の受付とexecutionの
完了は別で、接続が切れた場合も受付済みexecutionは継続する。実装結果と確認は
[Increment 139〜146](../increments/increment-146.md)および
[複数Coreの完了結果](../increments/increment-153.md)を参照する。

### Data Workerの状態所有と保存・公開

Data WorkerはSessionごとのcanonical model data、record組立て・検証、admission/terminal transaction、
semantic履歴、request単位の最新本文stateと共通ConversationStateの唯一のwriterである。 Core
mainは小さいSession/execution/generation index、実行予約、cancel/steering/follow-up、
採用許可とprocess/child制御を持つ。会話stateのCore側複製や別の表示用DBは作らない。

Agent–Dataの直接channelでcontext・semantic data・全文proposalを渡し、Core–Agentのcontrol channelでは
開始・cancel・process/child要求と小さいcorrelation/tokenを渡す。Dataはproposalの最終sequenceまで保存し、
recordを組み立ててprepare tokenを返す。Core/headless Hostは取消状態とchild/process清算を照合して
採用を許可し、Dataが対応するtransactionをCOMMITする。保存完了後にだけcommittedとして公開する。

保存factsの初回再生とliveの保存batchは同じnormalizer/applyFactを使う。
DataはCOMMIT後に会話stateへ一度applyし、変更entityとencoded snapshot/deltaを返す。
Coreはdata/controlの更新へ単一public revisionを付けて合成し、API Workerへ渡す。
Dataの保存版と公開cursorは区別し、通常の更新で過去全履歴の再読取・全会話のparse/再encodeを行わない。
保存Sessionのdescriptor・context・request読取は必要なsemantic dataだけをその時点で導出し、full
recordは明示readで正本から再構成する。会話viewは初回read/watchまでlazyに生成し、無購読の更新では
encode・配信を行わない。生成済みopen Sessionのviewは利用期間中保持し、closed Sessionは実consumerの
利用終了で解放する。canonical採用portは変更分だけを扱い、過去turnのattribution再組立てと二重Session
writeを行わない。処理再構成の採用結果は[Increment 199](../increments/increment-199.md)を参照する。
offline `henji history`はread-only DB adapterと同じ更新器で参照する。
配置・保存と公開の契約は[Increment 170](../increments/increment-170.md)を参照する。

### API/CLI adapterのportとlifetime

API Workerはlisten、route、request decode、response、SSE framing・接続終了を所有する。 Core
mainがlisten成功のURLを受けてendpoint/readyを公開し、Core epoch/PIDはprocess identityを維持する。
操作は非同期request/replyで個別にdispatchし、重いreadのreply待ちでcancel等を直列化しない。
購読identityをsnapshot要求前に登録し、先着update/endを保持してsnapshot→updateの順に渡す。
HTTP切断は該当購読の解除であり、受付済みexecutionやfollow-upをcancelしない。 単独encoded
replyは利用を終えたbufferをtransferし、複数購読で共有するupdateはcloneして元bufferを保つ。
callback、CoreService object、DB handleはWorker境界を渡さず、公開errorもdataで対応させる。

CoreのHTTP shutdown・CLI stop・SIGINT/SIGTERMは共通の終了ownerへ接続する。
新規受付を止め、稼働executionへcancel、購読へ終了通知を出してSSEを閉じる。
SSE終了は保存完了の通知ではない。HTTP shutdownはaccepted responseを返す段階をmainへ通知し、 client
ACKを待たない。API handler/RPCのdrain後、Coreの実行・Data保存とclose、endpoint/所有lockの
清算を終え、listenerをgraceful shutdownしてAPI Workerとmainを終了する。
listener/Workerの予期しない終了もCore closeへ接続し、保存をterminateで省略しない。

runではmainがCLI Workerを起動し、CLIが引数を解釈してHostへAgent設定選択を要求する。
Hostはstdin読取前に現在設定を一度解決して選択結果を保持し、CLIへdata-onlyな選択結果/errorを返す。
CLIがstdin/`--task`と実行optionを渡し、Hostが同じ選択で`runHeadlessWorker`を実行する。
同じportのevent→Host清算後result→CLI出力drain後doneの順を維持し、mainがWorkerを終了してexit
codeを適用する。 CLI Workerは物理stdioを直接扱い、mainにstdio relayを作らない。
runのprocess宛signalに新しいgraceful保存保証は追加しない。
実装・受入と追加配送の測定は[Increment 179](../increments/increment-179.md)を参照する。

### HenjiHost が所有するもの

- Core/headless Host mainによるWorker lifecycle、admission・cancel・採用判断とprocess/child清算。
- TUI/API/CLI adapterのSurface I/O、Data WorkerによるSession、history、会話payloadの保存と公開。
- 現在Agent JSON/catalogとtool folder metadataの選択・読込み。child起動時も現在fileを再解決する。
- generation開始前の共通base instructionの解決と観測内容の固定。
- configuration IDとexecution、build、model、base Session
  revision、semantic履歴、recallの保存・readback。
- Host共通process executor。Worker-local proxyからのdata-only requestを受け、制御端末分離、process
  group所有と清算を行う。

### Agent Worker が所有するもの

標準run_typescriptの呼出しごとに同じHenjiの子processを既存Host ProcessExecutorが所有し、
同期計算中の取消もprocess停止・清算へ接続する。子process内のcode Worker・module取得Workerは
Tool実装が所有する。既存Registry・semantic履歴を使い、async本文、workspace・/tmp・config
rootのread/write、 networkと実行時std取得を提供する。importはstdのみ許可し、通常fetchは維持する。
codeのenv/run/sys/ffiは無効とし、追加Worker作成は許可しない。
codeへ`henjiConfigRoot`を渡す。credential値とChatGPT account fileはstate配下の専用credential
rootへ置き、既定のread/write許可に含めない。現行はconfig rootの`tool-paths.json`でtool別`allow`と
共通`deny`を解決し、tool loaderがWorkerへ固定する。設定済み`allow`は既定への追加ではなく置換である。
run_typescriptは許可範囲をDenoへ渡し、共通denyをcode本文の文字列照合へ使う。旧`run-typescript.json`は読まない。Deno
permissionによる許可範囲と、path合成やsymlink等を強制的に防がない best-effortの監査は区別する。
導入・確認・全体gate通過・常用配置結果は[Increment 191](../increments/increment-191.md)、 config
rootとcredential保存先の変更は[Increment 204](../increments/increment-204.md)、共通fileアクセス設定への
切替は[Increment 213](../increments/increment-213.md)を参照する。

この経路は、Agentが作業上必要な処理を生成・実行する自己拡張を担う。code生成と結果の利用はAgent
Worker、 物理processの起動・取消・清算はHost、code/inputと結果のsemantic履歴保存はData
Workerの責務である。 生成codeは呼出しごとに実行し、tool resultを既存loopの後続model
stepへ渡す。設定やtoolのfileを編集した場合は、
既存の新Worker起動経路から反映する。生成codeの呼出しごとの実行と、永続toolのRegistry登録は別の経路である。

- 共通factoryによるJSON設定、model/effort、実際のTool、native instruction/skill、runtime
  factsのcomposition構築。
- 選択tool moduleのWorker-local
  import、factory実行、提示schemaとexecutorの一体化。toolごとのreject理由を構成へ反映する。
- transcript/context、compaction、tool semantics、turn内stateと各model requestへ渡すcontext。
- selected共通baseを先頭へ一度合成し、構成snapshotとdata-only progress/effect/proposalを返すこと。
- RegistryとBashOutputStoreのlifetime。物理processはHost proxyを使い、Registry終了時にoutput
  storeをcloseする。

Worker内でchildを同期実行せず、Hostへ要求して別Worker・Executionを作る。Workerはterminal/TUIを所有しない。

### 配布artifactと現在設定

HenjiはDeno runtime・production entry・共通Agent設定/構成・tool APIを含むstandalone
executableとして配布する。 任意path・任意workspaceから使え、repository
checkoutや別途導入したDenoをruntime dependencyにしない。配布packageはexecutableと編集可能な
`search`、`git_inspect`、`web_search`、`web_fetch`の外部tool folder、installer、build/file
manifestを含む。installerは
config配下へfolderを配置し、既存のtool選択CLIで登録する。既存folderの編集は保持し、明示的な
`--replace-tools`指定時にpackageのfileを上書きする。

writableなscopeはconfigにpreference・Agent/tool/provider選択、stateにSession・履歴・診断とcredential値（`${stateRoot}/credentials/`）を置く。
workspace fileとnative discoveryはworkspace/userの所定scopeが所有する。binary隣接pathをwritable
stateの正本にしない。 旧managed revision storeの実dataを新方式の一部として読まず、自動削除もしない。

buildはproduct version、build ID、source revision/dirty、Deno version、target、embedded runtime
digest、 Agent JSON schema versionとsupported tool API contractsを持つ。完全なbuild
manifestを親子executionへ保存する。 旧built-in Definition/toolのclosure digestを新しいrevision
identityとして扱わない。

#### Agent JSONの選択と構成寿命

`$XDG_CONFIG_HOME/henji-harness/agents.json`は次のdataを持つ。

```json
{
  "schemaVersion": 1,
  "default": "agents/my-root.json",
  "agents": { "reviewer": "agents/reviewer.json" }
}
```

Agent JSONは`name`と任意の`revision`、`instruction`、`tools`、`agents`を持つ。
revision省略はunversioned、instruction省略は同梱role、空instructionはrole寄与なし。
tools省略は同梱default、`[]`はtoolなし。agents省略はgenericと現在named
catalog、`[]`はchild操作なし。
未知の追加metadataだけでは拒否しない。明示fileとnamed指定は同時に選ばない。

root defaultはcatalogのdefault項目で選ぶ。明示named `default`は通常のnamed entryであり、
`agents.default`をroot defaultの代替にしない。catalog相対pathはconfig
root、明示fileは起動cwdを基準に解決する。 named catalog keyとAgent JSON
nameは一致させる。genericは同梱default JSONを基底にし、catalogで差し替えない。

HostはWorker起動ごとに現在fileを解決する。起動済みWorkerはその時点の設定・import済みtoolを使い、
編集は新Workerから反映する。Session再開は保存choiceに従って現在設定で継続し、過去snapshotを書き換えない。
同一Sessionの再選択だけによるreload/retryとfile
watchは未実装である。`/reload`はCore再起動の運用で足りるため
不採用、`/rebuild`も実装しない。設定/tool変更の反映には既存の新Worker起動・Session再開・Core再起動を使う。

`henji agent list/inspect/activate/deactivate`は現在fileの選択・確認、
`henji tool list/inspect/activate/deactivate`は現在folderの選択・確認を扱う。
登録だけで実行中generationをhot変更しない。module install/import/exportとexact revision
selectorは現行commandではない。

#### tool folderとWorker-local API

`tools.json`は`schemaVersion: 1`と`tools: { "<name>": "<folder>" }`を持つ。
folderのtool.jsonはname、revision label、`apiContract: henji-tool/v1`、entryを持つ。 folderはconfig
root相対または絶対path、entryはfolder相対または絶対pathである。
Agentが宣言したtoolにfolder指定があればそのfolderを使う。指定がなく、同梱実装のないtoolは
未設定としてrejectする。folderを推測したり、外部web/searchを旧同梱実装へ代替したりしない。
metadata/import/factory/返却Toolの失敗は当該toolだけrejectし、選択された不正toolを同梱へ代替しない。

外部moduleは`@henji/tool`をimportし、default
exportの`ToolFactory(input)`からToolまたはPromiseLike<Tool>を返す。 inputはworkspace、skill
catalog、process executor、work-tool seams、Worker共通BashOutputStore、 request/credential presence
seamであり、model/Core/DBのownerをfactoryへ移さない。 local importはDenoがその場で読み、source
closureの列挙・hash照合・永続copyを行わない。

Workerはfactoryを起動時に一度呼び、name/description/inputSchema/executeと任意guideline/terminalを確認する。
構成確認のためexecuteを呼ばない。受理した同じToolをmodel宣言とRegistry
dispatchに使い、function/内部stateをpostMessageしない。
JSONのtoolsが空ならskill/submit_json_resultを自動追加しない。native
skillは実catalogと選択toolがあるとき提示する。

同梱default/genericはread/write/edit/bash/bash_output/run_typescript、search/git_inspect/web_search/web_fetch、
skill/submit_json_resultを選ぶ。search、git_inspectと二つのweb toolはpackageに含む外部sourceを使い、
executableへ実装を埋め込まない。明示toolsを持つnamed Agentは必要な名前を宣言する。 child操作はAgent
JSONのagentsから別に構成し、child Workerには再帰spawn toolを提示しない。 rejectされたtool/named
entryは提示とdispatchの両方から除き、理由を最終instructionとSurfaceへ渡す。
Agent/catalog構成が成立しなければtaskをadmitせず、Core/TUIと履歴閲覧・認証・設定操作は維持する。

#### 外部hookとbuiltinの呼出境界

Agent Workerはruntime_start、runtime_stop、before_turn、after_turn、before_tool、after_toolの
六つのbuiltin呼出点を持つ。共通runnerは登録順のawaitと結果の受渡しを担当し、Agent側が
context・tool値への適用とsemantic記録を担当する。環境情報の挿入等の具体処理は外部TSに置き、
Worker-local HookFactoryへ公開specifier `@henji/hooks`、contract `henji-hooks/v1`を提供する。 Data
Workerのhookはこのincrementでは追加しない。

現在のhooks.jsonがcatalogと共通defaultを選ぶ。Agentのhooks省略はdefault、明示一覧は置換、
空一覧は無効化であり、root/named/genericへ同じ規則を使う。factoryとclosureはWorkerごとに独立し、
編集は新Workerから反映する。外部TSにはroot/child、Agent名、Session/Worker/Executionと
親execution等の識別情報を渡し、子を処理するかは定義側で決める。runtimeはWorkerの寿命であり、
開始hookの寄与をready前の構成snapshotに含める。正常closeは進行中turnと終了hookを待って
process/registry/Data portを清算する。正常hook待機へ既存5秒応答期限を適用せず、
明示取消とWorker喪失は既存終了経路へ接続する。

外部TSへ渡すcontextはlive stateではなくreadonly snapshotであり、canonical turnから
checkpointと最近のturnのviewを導出し、元履歴とprojectionを別々に全量copyしない。
before_turnの寄与はそのturnだけに適用する。before_toolは実効引数だけを変え、モデルの
元callと双方の値・出所を記録する。after_toolは本文だけを変え、outcome/callId/terminal
finalTextを保持する。

after_turnはDataの採用・保存確定後に実行し、確定したterminal
reasonと完成draftを渡す。summaryと保持境界の返却はData ownerの
既存checkpoint保存へ接続し、保存ack後にturn完了と次task/子result公開へ進む。
canonical履歴は残し、子の更新は子自身のcontextへ適用する。確定後のhook効果・例外・ 短いprovider
request factはsealed executionの通常受付とは別の保存経路へ残し、
hook失敗で採用済み結果を再失敗にしない。189ではcompaction用の外部定義の要約処理・閾値は実装しない。

配布packageはruntime-start-time定義を同梱する。これはWorker開始時のDateを一度取得し、 UTC
offsetとtimezone名付きで開始日時を挿入する。installerは初回catalog/defaultを作り、
既存catalogと編集済み定義を保持する。配布定義の置換は明示的な--replace-hooksで行う。

#### 非同期childの操作と清算

`spawn_subagent(agent, task, model?, tools?)`は親が宣言した利用可能Agent名を選び、Hostが現在catalog/fileを
再解決して別Worker・別Executionを起動する。genericは名前付き設定の準備を要求しない。
modelはcatalog検証済みprovider/modelId/effort、toolsは宣言済みtoolを絞る起動時filterであり、追加toolを導入する入力ではない。
model省略は親executionの現在selectionと実効ChatGPT登録IDを使う。明示modelは指定providerの認証を使い、
ChatGPTの場合はspawn時の登録参照を固定する。credential値を親から子へ渡さない。

childは空transcriptから始め、親のconversation/checkpoint/recall/named roleを暗黙継承しない。
結果はcollect_subagentのtool resultとして取得したときだけ親contextへ入る。
子のcompletedもnon-canonicalとして保存し、親Sessionへ直接採用しない。 spawn成功はdurable
admission、collect成功はdurable terminal settlement後に返す。
subagent_statusはlifecycleと最新model/tool作業・更新時刻を示し、短いrequest
factを子executionへ保存する。

status/collect/cancelのaddressabilityはspawn元parent executionに限定する。
parentのsettle/failure/cancel/forced interruption/close/generation置換は未完了childをcancelし、
terminal保存とWorker終了までawaitする。canonical proposalは清算後にgeneration/base
revision等を再照合する。
child清算の失敗は親artifactから参照でき、それだけで有効な親commitをrollbackしない。 V1はone-shot
fork/joinで、recursive spawn、mailbox、restart reattach、follow-up、swarm UIは対象外である。

#### native discoveryとHenji Instruction

workspaceの`AGENTS.md`とworkspace/user scopeの`SKILL.md`は、source-nativeなfile/directory
layoutを保ったまま 起動時に自動発見する。利用にmanaged
installを要求せず、Henji固有のidentity、custody、bindingを本文へ埋め込まない。
source、scope、content digestをSession/evidenceへ記録しても、それはnative inputのsnapshot
attributionであり、 managed revisionのinstallまたはactivationではない。

managed Skill revisionは未実装の追加候補であり、native Skillを置換しない。採用する場合にexact
pinやtransportの必要性を決める。Henji独自のbase instructionはmanaged revisionではなく、user
scopeの`instruction.md`を 直接読み込む別authorityであり、workspace `AGENTS.md`、native
Skill、managed Skillと合成順・provenanceを区別する。 最終的に同じprovider
instructionへ合成されても、identity、selection authority、合成順、provenanceを失わない。

Henji共通baseは、binaryに埋め込む最小core（役割identityとcredential/Authorization境界）と、user
scopeの
`$XDG_CONFIG_HOME/henji-harness/instruction.md`を直接読み込む外部contentからなる。外部ファイルは
`henji-resource.json`、install、activation binding、XDG data
storeを必要としない。Hostはgeneration開始前に ファイルを一度読み、存在すればbuilt-in
coreを置き換え、存在しなければ最小coreを使う。contentはvalidation後も trim、改行変換、Unicode
normalizationを行わずbyte-equivalentに投影し、source identity `user/instruction.md`と content
digestをattributionへ固定する。read failureやinvalid
contentはbuilt-inへ暗黙fallbackせず、turn開始前に 失敗する。workspace
scope、transport、`/rebuild`はこのkindに含めない。

Hostはgeneration開始前にselected built-in/external baseのexact ref、content digest、exact
bytesとbyte-equivalent textを 解決し、Agent JSONのrole寄与とは独立したdata-only
Worker-core入力へ固定する。共通compositionはrole寄与と構成不足の案内、active tool guideline、
workspace instruction、Skill manifest、runtime facts等のbaseを除くinstruction
contributionを返す。Workerのmandatory finalizerはselected
baseを先頭に置き、Henji-ownedな二つのLFだけをcomponent境界として後続contributionへ連結する。 async
childも同じselected baseを再解決せず使う。resolved exact ref/content、final system
instruction内のprojection、 provider requestとの関係はexecution context
attributionへ保存し、完成payloadの別authorityを追加しない。

#### MCP integration

MCPは一つのHenji toolまたはmanaged
moduleではなく、外部serverが提供するtool、resource、prompt等をHenji側clientが
発見・利用するprotocol境界である。HenjiはMCP protocol client/version/transport adapter、server
connection declaration、credential、local server packageまたはremote
service、実行時に発見したcapability projection、 call/result evidenceを別authorityとして扱う。

native MCP connectionにHenji managed installを必須にしない。接続後に発見した個々のMCP
toolは、connection identityと server内tool nameを対応付けたWorker向けtool
projectionとして提示し、MCP `tools/call`へdispatchする。resource、prompt、 server instructionsはtool
revisionへ変換せず、それぞれのMCP protocol operationから得るruntime inputとする。 connection
declarationやserver artifactのexact pin/transportが必要になった場合だけ、後続Integration
resourceとして managed pathを追加する。credentialはportable artifactへ含めず、capability
snapshotとcall/resultはSession/evidenceへ
相関する。client、transport、dispatchをHost、Worker、subprocessまたは別processのどこへ置くかはここでは固定せず、
後続Integration Incrementで実利用経路とauthorityに合わせて決める。

#### Provider設定の外部化

Provider routeはdata-only declarationのprovider ID、API protocol、endpoint、auth profileで表す。
binary同梱は`openrouter-chat`、`openrouter-responses`、`openai-responses`、`openai-chatgpt`の
四routeである。Increment 192で同梱`openai-chat`を廃止した。共有Chat Completions adapterは
OpenRouterと外部provider用に維持する。external `providers/*.json`は新しいprovider
IDを追加し、built-in同名宣言はrouteを維持して catalog/defaultsをoverrideする。既定はHost
configの`default-selection.json`から選び、未設定時は `openrouter-chat`を使う。

model一覧取得とお気に入りはCoreの`LiveModelCatalog`が所有する。通常はproviderのmodel一覧と公開effort
metadataを取得し、一覧掲載とお気に入り登録を選択可否から分ける。provider別JSONをconfigの
`model-catalogs/`へ保存し、お気に入り解除後もmodel別の記憶effortを保つ。metadata取得失敗時は保存候補を
使い、情報源を示す。external宣言の`modelListSource: catalog`は明示された固定一覧を使う経路であり、
OpenCode Goの暫定運用もこれを使う。ChatGPT一覧は選択accountに対応し、account別のcatalogと相関する。

非secret selectionはprovider/API/auth profile/model/effortとしてSessionとexecutionへ保存する。
rootはidle時に変更しturn内で固定する。childはspawn時の明示modelまたは親executionの現在selectionを使い、
Agent名別の同梱model既定は持たない。API keyはrequest時に固定config fileから解決する。ChatGPTは専用の
OAuth登録・選択と共有認証moduleで解決・更新し、root turn／child起動時のregistration参照を固定する。
credential値・Authorization・tokenはselection、Agent設定、Session、通常履歴へ含めない。

protocol adapterはbinary-ownedである。ChatGPTは共通Responses
adapterへ認証、namespace形式のtool宣言、 account別replay
identityを接続する。Responsesは同provider/model、ChatGPTはさらに同registrationのreplayだけを
再送する。effortを指定するResponses requestは`summary: auto`も要求し、読めるreasoning
summaryをthinking 表示・履歴へ供給する。未指定effortの`auto`でreasoning設定を強制しない。

新provider宣言のoptional
headers（`{credential}`／`{sessionId}`）とrequest時の置換、認証の保存・refresh・ account
bindingの詳細は[`multi-provider-routing-and-auth.md`](multi-provider-routing-and-auth.md)を正本とする。
追加protocolやmodel生成HTTPのplacement変更は採用時に決める。

#### 検索・URL取得・process tool

外部searchはpaths（file名一覧）、files（本文が一致するfile）、content（一致行）、count（出現数）、
entries（directory内のentry metadata）、stats（fileごとの行数・単語数・byte数）を提供する。
本文検索は共通列挙したfileを明示引数として渡し、rgを優先して不在時だけgrepへfallbackする。hidden/ignore対象も
同じ範囲へ含める。regexpの方言とゼロ幅一致の扱いはbackendに従う。files/content/countはtext検索であり、
`.db`・`.sqlite`・`.sqlite3`・`.blob`と対応する`-wal`/`-shm`を除外し、その他のfileは64
KiB単位で内容を 読みながらNULをbinaryとして除外する（UTF-16 BOM付きtextは維持し、encoded
NULではなくcode unitの
U+0000を判定する）。paths/files/contentは完全なrecord単位でoffset/limitと続き情報を持ち、contentのtotalは
一致行数である。countは同じ検索条件で選択対象全体の非重複の一致数をmatchCountへ合算し、
offset/limitを適用しない。backend出力のcaptureはstdout合計8 MiB・stderr 64 KiBまでとし、超過時は
processを停止して取得済み範囲を返し、不完全recordを除く。tool返却JSONは1 MiBまでで、超過する
matching textはprefix化する。部分結果は`truncated:true`・`totalIsExact:false`で明示し、
hasMore/nextOffsetは取得済みrecord内のpagingに限る。

entriesはpath直下を既定とし、depthで対象階層を選ぶ。type・fileのbytes・取得できたmodifiedAtを返し、
symlinkはentryとして表示してsymlinked directoryを辿らない。statsはfileまたはdirectory/globを選び、
64 KiB単位の逐次読取でLF数、Unicode White_Spaceで区切った非空列数、全byte数を集計する。
末尾の未改行部分は行数に含めず、検索用のDB/blob除外は適用しない。両modeはrecord単位でpagingする。

外部git_inspectはworkspaceのstatus・diff（staged/rev/path絞込・diffstatを含む）・log・showを、
固定argvで読取専用操作として提供する。通常の出力は行window、logはcommit単位でpagingする。
searchのentriesとgit_inspectの導入は[Increment 201](../increments/increment-201.md)、statsとtool選択の
guidelineは[Increment 206](../increments/increment-206.md)を参照する。modelによる自発的な選択改善は、
機能とguidelineの到達確認だけで実証済みとしない。

外部web_searchはExa APIを使い、非modelの検索requestとしてauthProfile `exa-api-key`を解決する。
親modelのcredentialやmodel request budgetを使わず、tool semantic履歴とprovider=exa/api=exa-searchの
短いphysical request
factを保存する。通常はauto検索とhighlightsで資料を返し、親modelが回答・引用を作る。 Sonar
backendは172で置換済みである。

web_fetchは既知URLをGETし、text/HTML等の本文とHTTP metadataを返す。
任意save_toを指定するとPDF/ZIP/画像等を変換せず元のbyte列としてworkspaceまたは/tmp配下へ保存する。
本文readbackとファイル保存を区別し、詳細な引数・受入結果は[Increment 172](../increments/increment-172.md)を参照する。

外部tool用serviceのAPIキーも/loginの共通登録・request認証経路を使う。 credentials/*.jsonに非secret
ID/表示名/用途/方式/consumerを宣言し、同じauthProfileは一つのcredential fileを共有する。 tool
factoryへcredential値を渡さず、requestProviderがrequest時にBearerまたは宣言した認証headerへ挿入する。
service登録はmodel
catalog/selectionとは分ける。詳細は[provider/auth architecture](multi-provider-routing-and-auth.md)を参照する。

bash/bash_outputはRegistry lifetimeで一つのtemporary output
storeを共有し、切り捨て出力をoutputId/nextOffsetで
継続取得する。storeはprocess/Workerをまたぐdurabilityを持たず、Registry終了でcloseする。 物理process
groupのownerはHostで、foreground return後のbackground processもgeneration清算へjoinする。
WorkerはterminalとSurfaceを所有せず、turnの実行に特定UIを要求しない。

### Surfaceと現在のTUI

Surfaceは、人間のactionをCoreのapplication operationへ変換し、Session snapshotとupdateから会話、
作業状況、結果を提示するadapterである。現在のTUIはCoreと別processのHTTP/SSE clientであり、
terminal、draft、cursor、viewport、入力履歴、picker、表示用cacheを所有する。Core mainは実行受付、
selection操作、Worker lifecycleと公開revision・購読、Data WorkerはSession・semantic履歴と会話read
model、 API WorkerはHTTP/SSEを所有する。UI-local stateをAgent Worker protocolやcanonical Session
stateへ混入させない。

通常の`henji`／`henji tui`は新Core・新Sessionを作る。`--core ID`または`--connect URL`は生存Coreへの
明示再接続で、`--session ID`は新Coreで保存Sessionを再開する。TUIの`/detach`／Ctrl-Dは接続だけを
閉じ、Coreと受付済み実行を維持する。`/quit`／Ctrl-Qは接続先Coreを停止し、そのWorker・child・ tool
processを清算する。Core選択とSession選択は別operationである。

非対話`henji run`はHTTP Core discoveryへ合流せず、同じWorker session
factory、共通factoryによる構成、
composition、proposal／commit／acknowledgementを使う一turnのheadless Host経路である。canonical
Sessionは保存しないが、productionではData Workerがnon-canonical executionとsemantic履歴を共有history
DBへ保存する。 CLI Workerが入力・出力を扱い、headless Host mainが選択解決・実行・清算を扱う。
既定はfinal-only stdoutまたはfailure JSON、`--json`はcurated NDJSON、`--stream`はlive assistant
textを 出す。出力はHost-owned projectionであり、provider-private
replayや内部protocolをそのまま公開しない。

現在の対話画面はconversation log、複数行editor、三行footerで構成する。

| 行 | 現在の役割                                                                                     |
| -- | ---------------------------------------------------------------------------------------------- |
| 1  | 入力・slash picker・履歴に応じた操作案内                                                       |
| 2  | ready／working／cancelling、経過時間、接続・閲覧状態、workspace、Core／Session identity、title |
| 3  | provider、model、effort。項目間は`│`で区切り、provider:/model:ラベルは付けない                 |

経過時間はexecution.createdAtを起点とし、同じ実行への再接続やsnapshot反復で起点を変えない。
workingは送信・準備からsettlementと後処理まで、Coreが新規入力を受け付けられる状態へ戻るまで続く。
credentialの有無は`/login`の一覧で確認し、footerへ常設しない。操作失敗・認証保存・recall準備・
接続断等は本文の`system>`へ表示する。実行結果はCoreの記録、予約は稼働Coreのpending record、
local操作通知はSessionに対応づけたTUI-local stateから表示し、全通知をDBへ保存するものではない。
通知は対象executionの位置へ置き、snapshot反復で重複追加しない。

conversation logは、user入力、thinking、assistant本文、tool、結果の順序とturn境界を表示する。
Responsesの本文とtool callの併存時も本文をtoolより前に保持する。生成中の本文・thinkingはsnapshotで
更新し、thinkingはstepごとに一つに確定する。保存Sessionの表示でも途中本文、tool、step、終了結果を
相関し、stepを作り直すたびにthinkingを重複挿入しない。`spawn_subagent`行には対象agent名を示す。

assistant本文はHost Surface内のMarkdown
rendererで見出し、list、table、quote、bold、emphasis、code等を plain
textと表示spanへ投影する。現行はuser行を灰237の全幅panelと端末既定の本文色で示し、user/assistantラベルは
ANSI 33、toolラベルとtool名・thinking系ラベルは緑＋dim（SGR 32;2）である。通常system通知とlist/quote
markerは端末既定色、失敗語はred、Markdown見出しとreadyはANSI blue（34）、emphasisはcyan（36）、
workingはANSI 33、表罫線・footerはdimである。配色の採用・利用者確認は
[Increment 193](../increments/increment-193.md)と[197](../increments/increment-197.md)を参照する。
terminal styleは最終frameにだけ加え、保存本文・API・model contextへANSIを混入させない。
rendererはgrapheme幅、変更entryの再利用、更新の合流、行差分とsynchronized outputを使う。
会話表示は保存本文の可視rangeを起点に構築し、viewport位置・幅変更・Page/resize・followを同じ経路で
処理する。Core/Dataは表示幅に依存せず、幅と表示操作のownerはTUIである。
長時間通常利用の入力遅延やGhosttyのちらつき等の未再現観測は、これらの実装だけで解消済みとしない。

通常文のEnterはidle時にtaskを送信し、working中はdraftを保持する。F2は成功後の次task予約、F3は現在の
executionへの一回のsteeringである。最終回答を受けた時点でも受付済みの追加指示があれば、同じexecutionの
次model requestへ一度取り込み、元のfinalだけで終了しない。受付可否はCore
operationsから導く。Ctrl-Cは通常入力のclear、
Alt-Enterは改行、区別可能なShift／Ctrl-Enterも改行として扱う。TerminalLifecycleはxterm
modifyOtherKeys mode 1を要求・復元し、CSI-uとxterm形式の修飾Enterを復号する。
tmux経路でのShift+Enter改行の採用・通常利用確認は[Increment 207](../increments/increment-207.md)を参照する。
PageUp／PageDownは実行中も履歴を移動する。mouse
wheelは同じ履歴移動（1イベント＝1ページ）として働き、 TUIは会話表示中にmouse
trackingを要求するため、端末標準のドラッグ選択はShift+ドラッグ（terminal側）または
tmuxのキーボードcopy-modeで行う。履歴中のEscはlatestへ戻り、latestで実行中のEscだけがcancelを
要求する。pickerのEscはその画面を閉じ、cancelへ流さない。入力と過去表示位置はsnapshot更新で保持する。

F1または`/sessions`はSession一覧を開く。Enterは閲覧、R／rは再開、D／dは個別削除確認、
y／Yは削除、n／NまたはEscは取消である。削除はSessionと関連execution・semantic履歴・recall参照を一体で
扱う。`/view ID`は閲覧のみでWorkerを起動せず、`/resume [ID|latest]`は現在のAgent設定で継続する。
`/context`はCoreが保持するcontextを読み取り専用で表示する。

editor先頭の`/`は英語説明・usage・対応keyを持つcommand pickerを開く。↑／↓で選び、EnterまたはTabで
command名を補完し、必要な引数を入力して再度Enterで実行する。workspace pathのTab補完は持たない。
`/help`はcommandとshortcutの対比を表示する。command一覧は`v0/tui/slash_command.ts`を正本とし、
`/login`、`/new`、`/sessions`、`/view`、`/resume`、`/context`、`/rename`、`/provider`、`/model`、
`/effort`、`/recall`、`/detach`、`/quit`を含む。

`/login`はproviderと外部tool用serviceのAPI key登録、Sign in with ChatGPTを認証方式で分ける。API
keyは伏字入力から固定fileへ保存する。
ChatGPTはURL案内・非表示callback入力・account登録／選択／再認証を専用Core操作へ渡す。
account選択のEnterはpickerを閉じ、通常入力へ戻る。認証操作だけで親のprovider/modelは変更しない。
`/model`はCoreが取得した一覧と検索・お気に入りを使い、`/effort`はmodel別のmetadataまたは明示catalogを使う。
ChatGPT未登録でもprovider一覧・選択を使える。selectionはidle時にHostが保存し、admit済みroot
turn内で固定する。詳細なroute・account・replay境界は
[`multi-provider-routing-and-auth.md`](multi-provider-routing-and-auth.md)を参照する。

`henji history`はCore/TUIと別のread-only CLIで、同じDBの単一read
transactionから`session`／`canonical`／ `detail`をstdoutへ出す。TUIはHTTPのhistory/context read
modelを使う。人間向けrendererとAgent向けmodel projectionを分け、`/recall`は選んだnon-canonical
executionを次の一taskにだけ明示投影する。

production TUIと`henji run`の`--provider-timeout-ms`はinvocation stateで、未指定時300,000
msをroot、child、 compaction、補助provider
requestへ適用する。`provider_timeout`をresponse解析失敗と区別する。 terminalはalternate
screenへ隔離し、detach／quit／signal／出力失敗時にHost側Surfaceが復元する。
terminal終了とCore終了は同じlifetimeではない。

具体的なkey、layout、表示量は通常利用に応じて改訂する。WebUI本体と一般的なSurface load／selection／
replacementは未実装であり、HTTP read modelの存在だけで成立済みとしない。

物理process実行はHost共通executor、provider HTTPはWorkerを基本placementとする。Coreのmodel一覧取得・
認証操作はCore側で実行する。境界を渡るのはdata-only
messageであり、JSON設定は共通factoryで構成し、外部toolはWorker内でimportする。 Deno Web
Workerはlifecycle/data境界であり、別trust tierやsubprocessを含むhard sandboxではない。
根拠となるAPIとlocal probeは
[`host-worker-reference-comparison.md`](../research/host-worker-reference-comparison.md)に記録する。

### Durable history、canonical conversation、context projection

Henjiの履歴全体と、以後の通常会話へ既定で引き継ぐconversationを同じ状態として扱わない。

durable historyは、一つのclaimへ一つのownerを置き、次の三層を区別する。

- semantic authority: canonical／non-canonical message、tool call／result／effect、model-visible
  context order、providerから読めたthinkingの実行・model step付き観測（完了／未完了を区別）、Hostの
  admission／outcome／canonical
  decision、使用したconfiguration／build／modelとresource内容、`/recall`の source／target
  relation、provider・model・API経路・論理step・物理request順番・HTTP／error・解析失敗の
  項目と値の形を表す短いrequest fact。
- derived projection: 人間向けhistory view、context manifest、artifact表示、
  summary／compaction、export、later reinterpretation。
- storage mechanism: codec、physical locator、representation digest、index、audit metadata。

通常履歴はsemanticな出来事から、人間入力、Agentへ実際に渡したcontent／revision、tool／providerのsemantic
result、Host判断、明示的な未観測境界へ至る最小説明閉包を持つ。rootとasync childの双方で、観測済みの
request factとfailure
diagnosticを各executionへ相関して保存する。途中のcancelや失敗では確定保存済みの semantic
prefixと最新本文を残す。credential値とAuthorizationは記録しない。semantic authority自体のdurable
write失敗は canonical adoptionを禁止する。async childのcollect結果はstop reason、実request
count、diagnostic id／code などの短い状態を返す。

生成中のassistant本文はexecution・lane・model step・physical requestをkeyとするData
WorkerのHost-owned stateであり、 derived cacheではない。最新eventの本文・観測時刻・Worker
sequenceと初回のevent位置を保持する。 既存journalのflushでstateを置換し、同じrequestのmodel
result保存とstate終了を一transactionで行う。 cancel、failure、forced interruption、restart
reconciliationでは、最後の本文のsemantic追記、state終了、
terminal／outcomeを一transactionで保存する。read-only
detailはstateとsemantic履歴を同じsnapshotから読み、
生成中本文を確定messageやappend済みoccurrenceとして扱わない。停止本文のreadbackは元の観測位置に並べ、
`/recall`へは一request一本文を投影する。未完了本文をcanonical conversationへ自動採用しない。

derived projectionはsemantic authorityにsourceを持ち、sole-owner fieldを
持たない。現行の`henji history`は必要時にsourceから直接view／exportを作り、永続化されたhuman
history行や 更新outboxを使わない。derived documentの生成失敗はsemantic commitを取り消さない。

canonical
conversationは、Hostが正常完了と会話への採用を確定したturnを順序付きで保持するSessionの正本で
ある。正常完了は回答内容の正しさや人間の満足を意味しない。canonical採用はturn全体を単位とし、途中の
assistant outputやtool
interactionだけを部分的に採用しない。turnへ含める具体的なmessageとprojectionは個別schemaで 定める。

executionの状態は少なくとも次の独立した軸で扱う。

- lifecycle: active / settled
- outcome: completed / cancelled / failed / interrupted / unknown等
- conversation adoption: canonical / non-canonical
- effect observation: requested / started / completed / failed / outcome unknown等

non-canonical
executionも、観測済みevidenceをstorageへ物理的にcommitしてreadbackできる。storageへのdurable
writeとcanonical
conversationへの意味上の採用は別operationであり、`uncommitted`という語は後者だけを指す
場面でも誤解を招くため、通常は`non-canonical`を使う。

実行中のHostがforced interruptionを観測してterminalへ収束させたexecutionは、実際の`interrupted`
outcomeを持つ 通常のsettlementとして保存する。restart時にactive prefixを発見し、実際のterminal
outcomeを観測できない場合の reconciliationとは区別し、後者のartifactへ実outcomeを捏造しない。

#### 新DBの保存責務

workspace state rootのhistory.sqlite3（schema 1）を複数Core/headless Hostで共有する。

| 正本/record                           | 責務                                                                                                                  |
| ------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| sessions                              | Sessionのchoice、現在model、revision、next turn、checkpointと会話cut                                                  |
| configurations                        | 独立configuration IDと起動snapshotのcontent参照。同じrevision labelで統合しない                                       |
| executions                            | admissionからsettlementまでの実行、親子相関、configuration参照、完全build、実model/effort、maxSteps、outcome/adoption |
| contents / messages                   | content bytesとexecutionごとの新message本文。canonical/non-canonicalで同じ正本を使う                                  |
| session_turns / conversation_messages | 採用済みexecutionの順とmessage参照。本文/build/model/configの別copyを作らない                                         |
| semantic_records / semantic_relations | tool・control・request fact・runtime outcomeと因果/参照関係                                                           |
| assistant_text_states                 | request単位の最新durable本文。完了messageやsemantic occurrenceとは区別する                                            |
| execution_contexts / recall_relations | 各requestのcontext attributionと明示recall source/target                                                              |
| diagnostics                           | executionに相関した固有failure detail。artifact全文の別copyではない                                                   |

canonical settlementは新message、terminal/outcome、採用turn・会話参照、Session revision/checkpointを
一transactionで保存する。non-canonical
settlementはexecutionとmessage/semanticを保存し、Session会話とrevisionを変更しない。
Session/Execution/artifactは現行schema 1のみを読み、旧history-v7.sqlite3を読込・移行・削除しない。
旧execution_admissions/session_heads/derived_documentsやartifact codec 2〜7を新DBへ持ち込まない。
共有DBの初期化同期、write待機、Session/execution lock、生存実行を避けるrestart
reconciliationを維持する。
詳細column/APIは[181 contract](../increments/increment-181-contract.md#新dbの具体保存責務)、
sourceはsqlite_history_store.ts、sqlite_history_core.ts、history_schema.tsを参照する。

#### History storage不変条件

- logical record／occurrence／decisionのidentityはsegment、offset、page、codec等のphysical
  locatorから独立する。
- immutable contentはalgorithm／version付きcontent digestを持つ。compressed
  representationを保存する場合だけ content identityとrepresentation digestを分離する。
  同じbytesでも別execution／source／occurrenceなら発生factを統合しない。
- append時にcurrent semantic deltaのschema、execution内ordinal、mandatory referenceを検証し、count、
  latest durable ordinal、terminal、unresolved referenceを増分更新する。ordered hash
  rootはsettlementの 必須条件にしない。
- semantic occurrenceのordinal／countと、Worker eventの保存進捗は区別する。本文stateだけのbatchも、
  state更新とevent countを同じtransactionへ保存し、semantic occurrenceを増やさない。
- normal append／settlement／adoptionは過去payloadをapplication
  levelで全scan／decode／rehash／rewriteしない。 同量の新規factを追加する処理量は既存Session
  payloadや当該executionの過去event数を乗数に持たない。
- `settled`はlogical completeness、terminal、mandatory
  referenceの解決を意味し、全過去payloadをsettlement時に
  再scrubしたことを意味しない。materializeするimmutable contentはread時に検証し、全体検証はexplicit
  auditとして 通常pathから分離する。
- crash後に見えるexecution evidenceは最後にatomic commit済みのsemantic ordinal
  prefixと最新本文stateである。
  受信済みでも未commitの本文を保存済みとして返さない。writable起動時は未完了本文をsemantic履歴へ移して
  既存reconciliationを行い、read-only参照はactive状態を変更しない。

将来の自己改訂experienceはstableなsemantic occurrence、history
entry、execution、query／rangeを参照する。 projection再構築によって参照先のsemantic identityを
書き換えない。experience selection、assessment、candidate、human judgmentのdomainとappend ownerは
F19〜F24で定め、history storageが先に固定しない。

人間向けhistory viewは、canonical turnとnon-canonical executionの双方を識別して辿れるようにする。
Markdown、tool
summary/detail、status等のrendererはHost/Surfaceの表示責務であり、保存内容、採用状態、 model
contextを変更しない。
読めるthinking本文と要約は作業単位にまとめて表示し、要約はその旨を明示する。暗号化itemから本文を
作らず、stream断片を一件ずつ通常履歴へ永続化しない。表示するthinkingはprovider-private replay
stateや modelへ渡すsemantic conversationへ混入させない。

model context projectionはhistory
viewとは別責務である。過去executionから既定で引き継ぐconversationは
canonicalに限定する。一方、現在execution内で得たassistant stepやtool
resultはそのexecutionの後続model requestへ
渡すことができ、人間またはAgentが明示的に選んだreferenceも目的と期間を限定して追加できる。
Agent自身の履歴参照を採用するときも、観測材料の選択とcanonical
conversationへの採用は別operationとする。

Increment 38の`/recall`は、settled non-canonical
executionを人間が選び、保存済み内容を次の一つのtaskへ data-only contextとして投影するHost
operationである。sourceをcanonical化、resume、自動retryせず、source identity、
実際のprojection、target
executionを相関する。projectionの選択は次taskのadmissionで消費し、そのtask内の各model
requestで利用できる。targetがcanonical採用されてもsourceはnon-canonicalのままであり、targetが生成した内容は通常の
canonical conversationとして後続へ残り得る。現行はprojection本文をcanonical
turnへ複製せず、source／target relationと 実際のprojectionをsemantic履歴とcontext
attributionへ記録する。canonical表現を変更する場合は別途判断する。

### Execution context attribution

各executionは、使用したAgent側の基底設定、base canonical Session
revision、明示projection、実行中に読み込んだ
resourceや観測情報と相関できるようにする。存在していたresource、discoveryで発見したresource、実際に読み込んだ
resource、modelへ渡した内容を同じ事実として扱わない。

振り返りの対象候補は、Henji共通instruction、agent role、workspace `AGENTS.md`、skill
catalogと実際に読み込んだ skill、Henjiが所有または観測できるsystem
instruction、Agent設定、modelへ提示したtool contract、modelへ 供給したruntime
facts、toolで観測した環境情報である。現在のmutable fileへのpathだけでは当時の内容を振り返れない
resourceは、Henjiが観測した内容または同等のattributionをevidenceへ残す。

`instruction:henji-base`では、実行時にselectedだったbuilt-in/external exact ref、slot、selection
source、content digest、 exact contentを記録し、同じexecutionのfinal system instructionと各provider
requestへ投影されたbyte rangeを相関する。 現在fileと選択、起動snapshot、execution
attributionは別authorityであり、現在設定の変更で過去の attributionを書き換えない。

このattributionは完全再現性を目的にしない。過去Worker、model内部状態、dependency、binary、OS、filesystem、
外部service、tool effectをsnapshotまたは再構築する保証にはしない。

### Agent自身の観測と自己拡張

人間向けTUI/historyの充実を、Agent自身の観測操作が成立したことと同一視しない。Agentが現在の自分の
executionと実効構成を発見し、目的に必要な履歴・attribution・短いrequest
factを選んで読む経路を整える。 Hostは既存のsemantic
authorityをreadbackし、Workerは観測を解釈して次の調査や変更候補の形成へ使う。
新しいInstanceやexperience専用store、raw常設収集を、この観測の前提として追加しない。

`run_typescript`は既存Registryから生成codeを実行し、結果を後続stepへ渡す自己拡張の経路である。
code/inputとtool
resultは通常のsemantic履歴へ保存し、人間とAgentが既存のreadback経路から振り返る材料にする。
専用experience storeや全対象共通candidate管理、採用protocolを、この部分実装の成立条件にしない。
人間が定めた目的・改訂範囲・採用境界の中での実行と、その境界を変える判断を区別する。
操作ごとの承認を一律に要求せず、候補の採用判断をAgentへ自動的に移すこともしない。

現行root selectionはidle時に人間が変更し、admit済みturnで固定する。Agentによるroot
model・effort変更は 効果が薄いため見送る。subagentのtask/model/tools指定は既存のspawn経路を使う。

### 設定・tool変更の反映

`/rebuild`相当の専用operationは実装しない。Hostは新Worker起動時に現在の設定/toolを解決し、Workerはその
起動snapshotからcompositionを構成する。保存Sessionの再開とCore再起動もこの既存経路を使う。
起動済みWorkerの設定・import済みtoolをhot変更せず、過去executionのconfigurationとattributionを維持する。
binary platform authorityの変更は新しいHenji buildとして追う。

`AgentWorkerGeneration`はprocess/isolateのlifetimeを表す。実際のmodel inputはcanonical
conversation、 明示projection、execution中に追加されるskill本文やtool result等とも相関し、generation
IDだけで表さない。 rebuild用の`AgentContextGeneration`は導入しない。

cancel/failed executionのtool
effectとして設定やtoolのfileが変更された場合、その変更は既に外部副作用として
存在し得る。後続Workerが現在fileを読むことは、source
executionのcanonical化や、既に生じた副作用の取消しを 意味しない。candidate採用とtool
effect、canonical会話採用の境界は維持する。

### Configuration と generation の fencing

- Worker generationをSession/execution、configuration ID、generation、base Session
  revisionへ相関する。
- Dataは当該executionの構成参照・base
  revisionとproposalを照合し、Hostは現在admitされたgenerationの採用だけを許可する。
  この境界を履歴閲覧へ適用せず、Agent/toolの任意revision labelをpermission authorityにしない。
- 同じfileからのWorker restartでも新しいconfiguration
  snapshotを作る。設定の現在内容と過去起動内容を分け、
  後続実行の参照だけを変える。保存snapshotとの不一致をSession継続の失敗条件にしない。

## セッションの永続性とコミット

Host/Worker 分割によって、実行中の Worker が durable truth の source になってはならない。

### AgentInstance と Session の関係

現行の継続性は、Hostが保存・再開するSessionとexecution履歴にある。Sessionはtranscript、context、turn
commit等の conversation semantic
stateを所有し、SessionのwriterとgenerationをHostが管理する。独立したdurable Instanceを
Sessionの必須所属先にしない。

durable Instanceを採用する場合は、次の関係とcanonical domainを追加する。

- その機能の対象Sessionは1つの`AgentInstance`に属し、1つの`AgentInstance`は0個以上のSessionを持てる。
- Session は transcript、context、turn commit などの conversation semantic state を所有する。
  `AgentInstance` は stable identity と activeな設定参照 を所有する。
- 同じ `AgentInstance` に admit される writer Worker generation は、一度に 1 つだけとする。 Host は
  admit した input を serialize して、その Instance の generation へ渡す。
- mutable datum は Session または Instance のいずれか 1 つの canonical domain に属する。将来
  Instance-wide mutable state を導入する場合、Host は Instance 単位の revision、lock、persistence
  も所有し、session state と二重の正本にしない。

| 責務                | HenjiHost                                                                                                                  | Agent Worker                                                              |
| ------------------- | -------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| Session identity    | session IDを所有し、durable Instanceを採用する場合はそのassociationも所有する。                                            | 現在の実行でそのidentityを使用する。                                      |
| Concurrency         | lock と session の serialized admission を所有する。                                                                       | ephemeral な turn 中 state だけを持つ。                                   |
| Persistence         | load/store、storage revision、atomic replacement、recovery を所有する。                                                    | commit を提案する。durable state の canonical source にはしない。         |
| Conversation の意味 | 受け入れた canonical state を保存する。                                                                                    | 実行中の transcript/context semantics と compaction decision を所有する。 |
| Turn の settlement  | proposed commit を受け入れ、committed と報告する前に durable に保存する。                                                  | 境界を通じて outcome と proposed state/effect を報告する。                |
| Execution evidence  | canonical採用とは独立して、assistant本文の最新state、semantic fact、effect、outcome、context attributionを相関・保存する。 | 実行中の本文snapshot、semantic eventとsettlementをprotocol経由で返す。    |

概念上の turn の流れは次のとおりである。

1. Host が canonical session state を load し、Worker generation への command を受け入れる。
2. Worker が snapshot を解釈して composition を実行し、turn 中の output と commit proposal を
   生成する。
3. HostのData Workerは観測済みsemantic
   deltaのappendと最新本文stateの更新・終了をatomicに保存する。executionのsettlementは
   未完了本文をsemantic履歴へ引き継ぎ、連続ordinal、count、terminal、mandatory
   referenceを増分処理する。 過去payload全体を再検証しない。
4. canonical採用時、Core/headless Host mainがDataのprepare
   tokenとexecutionの成立条件を照合して許可し、 Data Workerがterminal／settlement、canonical
   turn、Session revisionを一transactionで保存する。 ordered hash rootを成立条件にしない。
5. durable canonical adoptionが成功した後にのみ、HostはSurfaceまたは採用済みoutput
   consumerへturnをcommittedと報告する。

### Effect と commit proposal

Worker から返るものは、概念上、`effect request`、`effect started/completed/unknown evidence`、
`state commit proposal` として区別する。ここではそれらの field や schema を定めない。 durable commit
が存在しないことだけでは、effect が発生していないことや、安全に replay できる
ことの証拠にはならない。したがって、effect が開始された可能性のある turn/event は、明示的な
idempotency/deduplication 契約、または effect が開始されていないことの証拠がない限り、transparent
に再実行しない。これは product correctness の不変条件である。

Persisted Host state が durable truth であり、その中でcanonical conversationとnon-canonical
execution evidenceを 区別する。Worker state は ephemeral である。Worker の
crashまたは置換によって、まだHostが受け取ってdurableに
保存していない作業状態が破棄される可能性がある。turn または canonical commit が失敗した場合に tool
effect が rollback されるとは限らない。local filesystem、subprocess、 network、その他の effect
には、それぞれ将来の semantics が必要である。この文書はそれらの semantics を定義しない。

## 経験から変更と通常利用へ進む改訂ループ

人間主導でHenji自身が一部incrementの実装を担う運用は始まっている。Agent自身の観測・振り返り・改善案の
形成を強めるため、次の関係を狭いincrementでつなぐ。専用candidate管理やdurable
Instanceを先に完成させる 固定工程にはしない。

1. Hostが保存したSession、semantic履歴、実効構成のattribution等を、人間とAgentが目的に沿って観測する。
2. 人間のアクションまたは指示を契機に、Worker内AIが経験を解釈し、対象機能の変更候補を作る。
3. 対象に応じたsource・diff・dataと由来を確認できる形で残し、人間が採用アクションまたは明示的承認を行う。
4. 人間が定めた範囲で、人間またはAgentが変更を後続実行へ反映する操作を要求し、Hostが実効状態を確定する。
5. 改訂後のHenjiを通常利用し、観測された変化を次の経験にする。観測・構成選択・反映の手段も改訂対象にできる。

対象はAgent設定に限定しない。native
instruction、skill、tool実装、modelの選択と使い方、loop、runtime、
Host/Worker連携、Surface等から、実際の経験に必要な対象を選ぶ。経験、candidate、active
resource、実効状態、
後続実行への適用は区別し、そのtargetの保存・採用・適用方式を個別incrementで具体化する。
変更前後の統制実験、改善の定量測定、全実効状態を表す統一revisionは要求しない。

### 現在設定を改訂する場合

Agent JSON、tool folder、native instruction/skill等は、それぞれの現在fileと選択authorityを使う。
採用した変更は新Workerへ取り込み、当時の実効設定・instruction・提示contractをconfiguration
snapshotとして
後続executionへ相関する。現在fileへの編集、変更候補の採用、実効構成の再構築は区別する。

Agent/toolの版名を変えることやgeneric
childを起動できることだけで自己改訂の一巡を実証済みとはしない。
候補の内容・由来と人間の採用境界を維持し、必要な適用操作は対象ごとのincrementで定める。 managed
revision/promotionやdurable Instanceをこのループの必須方式にしない。

## AgentInstanceの継続性とHostの追加機能

durable `AgentInstance`を追加機能として採用する場合、その継続性はWorker
generationを置き換えてもidentity、durable metadata、activeな
設定参照をHostが維持することで成立する。同じInstanceに対するwriter generationは
一度に1つだけとし、Hostがinputをserializeする。この構造だけでは、mailbox、複数Surface間のrouting、
scheduleをproduct機能として採用したことにはならない。

roadmapが各機能を個別に採用した場合は、次の責務分離に従う。

| 追加機能                           | Host の責務                                                                                                                                                      | Worker の責務                                                                            |
| ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Mailbox                            | Worker が不在または置換中である間も含め、`AgentInstance` の input queue を永続化する。                                                                           | dequeue した event を解釈し、agent の response または次の intent を決める。              |
| 非同期または複数Surface間のRouting | inbound message を `AgentInstance` と Session に対応付け、正しい Surface に output を返す。これは tool-name dispatch ではなく、message/Instance routing である。 | 現在の execution context が宛先となる output を生成する。物理channelの選択は所有しない。 |
| Schedule                           | wake-up intent、time、delivery mechanics を永続化し、通常の mailbox event を enqueue する。                                                                      | schedule intent の意味を所有し、結果の event を通常の agent input として処理する。       |

常にaddress可能なagentをproduct機能として採用する場合は、ephemeral Workerだけでは成立せず、
常時稼働するHost、durable storage、service supervisorまたは同等のlifecycle ownerを必要とする。
その場合もWorker自体を常駐させる必要はなく、mailbox、routing、scheduleの採否はそれぞれ別に決める。

## 外部比較の位置付け

Deno、Cloudflare、Pi、Zot、OpenComputer、OpenClawとの詳細な比較は
[`docs/research/host-worker-reference-comparison.md`](../research/host-worker-reference-comparison.md)
に背景調査として分離する。このarchitectureが採用する結論は次に限る。

- Henjiの実行primitiveはDeno Web Workerであり、Cloudflareのデプロイ単位としてのWorkerではない。
- `AgentWorkerGeneration`はephemeralなexecutorであり、durable stateの正本ではない。
- HenjiHostは、採用したlifecycle、routing、storage、supervisionを自身の責務として実装する。
- 外部実装との類似は、Henjiのprotocol、resourceの意味、機能優先順位を決めない。

managed externalization、native discovery、MCP境界の比較根拠は
[`docs/research/externalization-reference-comparison.md`](../research/externalization-reference-comparison.md)に分離する。
参照実装や外部protocolはHenjiの仕様そのものではなく、この文書に明記したtaxonomy、identity、authority、
compatibility境界だけを採用する。

## 未決のアーキテクチャ判断

確定した制約は、それぞれの責務と境界を定める本文に置く。ここには選択肢が残る判断だけを、その理由と
判断する契機とともに記録する。roadmapは契機となるproduct機能を採用するかを決め、個別計画は採用された
機能に必要な判断を具体化する。

| 未決の判断                                                                                                             | 今決めない理由                                                                                                                | 判断する契機                                                           |
| ---------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| Agent自身が参照するexecution・実効構成とreadbackの入口                                                                 | 人間向けhistoryと間接参照はあるが、現在の自分を発見して必要な材料を選ぶ操作は未整備である                                     | 自身の観測・振り返りの最初のincrementを採用するとき                    |
| `/recall`のcanonical表現を変更するか                                                                                   | 現行はprojection本文をcanonical turnへ複製せず、semantic履歴とcontext attributionへ相関する。表現を変更する要件は未採用である | canonical turnのprojection表現を変更するschemaまたは機能を採用するとき |
| process以外のprovider/tool物理I/Oのplacement変更                                                                       | process実行はHost所有として成立した。provider HTTP等はWorker内にあり、将来の配置変更は実際の利用契約から決める                | roadmapが配置変更を必要とするprovider/tool利用経路を選んだとき         |
| Worker protocolのmessage、handshake、error、versioning                                                                 | 必要なmessageとfailure semanticsは、境界を使うproduct機能から決まる                                                           | 新しいHost / Worker間機能を実装するとき                                |
| MCP connection discovery/config format、tool name mapping、capability変更時のgeneration更新、server packageのmanaged化 | MCP protocol compatibilityとHenji固有のselection・durabilityは別contractであり、具体的な利用経路をまだ採用していない          | roadmapがMCP integrationを採用したとき                                 |
| Worker restart、cancel、concurrency、lease、backpressure                                                               | inputの並行性、streaming、effectの有無により必要なsemanticsが変わる                                                           | 複数入力、長時間turn、強制停止のいずれかを扱うとき                     |
| Surface identity、load / selection / replacement、置換時のUI-local state引継ぎ                                         | CoreのHTTP/SSEと接続TUIは成立したが、WebUI本体と一般Surface loaderは未実装である                                              | 新Surfaceまたは一般的な置換operationを採用したとき                     |
| mailbox、非同期または複数Surface間のrouting、schedule、Instance-wide state、cross-session memoryの永続化               | それぞれ独立したproduct機能であり、AgentInstanceの継続性やHost / Worker分割だけからは必要にならない                           | roadmapが対象機能を採用したとき                                        |
| effectのidempotency、deduplication、recovery                                                                           | effect先の契約なしに共通のretryまたはexactly-once semanticsを決められない                                                     | recovery対象となる実tool effectを選んだとき                            |
| deployment profile、service supervision、migration                                                                     | 実行先、可用性、移行元と移行先が決まらなければ必要なmechanismを選べない                                                       | 常時address可能なHost serviceの運用先または移行対象を決めたとき        |
