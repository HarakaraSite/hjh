# Henji 機能インベントリと反復型実装ロードマップ

ステータス: **採用済み — roadmapであり、各phaseの実装認可ではない**

作成日: 2026-09-06

実装状況の初回照合基準commit: `29c84cf17063da3565672ce4a37cdbf76334995e`

基盤のsource照合: `a78c2076`（2026-10-04、Increment 182まで）。181のJSON Agent設定・現在tool
folder・新履歴DBへの切替、182の実効ChatGPT登録ID継承を反映する。 追加照合: 2026-10-07、source
`68ab5dd0`。189・191・192・193・197・198・199・201・202・204・206・207・208に
関わるhook、tool、provider、Data処理、TUIの現行記述を照合・反映した。architectureも同じ追加照合範囲とする。
これら以外のincrementの詳細を一括反映済みとはせず、採用範囲・結果は個別increment文書を参照する。
導入時の方式と現行方式を分け、個々の受入・常用配置結果は個別increment文書を参照する。

方針・実装状態の改訂: 2026-10-08、利用者判断。run_typescriptによる自己拡張を自己改訂の部分実装として
反映した。rebuildは実装せず、Agentによるroot model・effort変更は効果が薄いため見送る。
run_typescriptの現行fileアクセス設定は[Increment 213](increments/increment-213.md)へ追加照合した。

## 目的と正本

この文書は、次の正本から導かれる機能を列挙し、現行production経路の実装状況、Host / Worker
architectureとの対応、未実装機能の実装順序、各phaseで必要になる判断を一か所にまとめる。

- 構想: [`concepts/experience-driven-self-revision.md`](concepts/experience-driven-self-revision.md)
- architecture: [`architecture/henji-host-agent-worker.md`](architecture/henji-host-agent-worker.md)
- active sourceとcommand authority: [`../v0/`](../v0/)、[`../deno.v0.json`](../deno.v0.json)

初回照合commitはこの文書を作成したときの照合時点を示す。その後の変更を含む現コードの実装状況はactive
sourceを正本とする。両者に差があればactive
sourceを優先する。`docs/plans/`に残る個別計画と結果は過去の
実装・検証の証拠であり、現在の実装状況やこのroadmapの機能順序を決める正本ではない。
`archive/`の旧roadmap、spike、旧extension管理実装も現在のproduct経路またはroadmapとはみなさない。

## roadmapもループする

Henjiの開発は、一度のroadmapで完全なproductを定義して作り切る工程にしない。一つのloopでは、通常利用へ
戻せる狭い機能増分を選び、実装後の経験を次のloopの入力にする。

```text
構想
  ↓ 必要なproduct動作
architecture
  ↓ 責務・状態・lifetime・commit境界
roadmap
  ↓ 今回の狭い増分と順序
実装・実利用
  ↓ 困難、有用な成功、違和感、利用者の判断
次の構想 / architecture / roadmap
```

実装で行き詰まったときは、現在の設計を前提に機能を狭めて通過しない。目的や必要な動作が違っていたら
構想へ、責務や境界が違っていたらarchitectureへ、順序や増分が違っていたらroadmapへ戻る。局所的な
実装問題なら同じincrement内で直す。戻った結果、このroadmapの後続案は変更または破棄できる。

各loopの完了はHenji全体の完成を意味しない。人間が通常利用の経験から改訂を指示し、Henji自身が一部incrementの
実装を担う運用に加え、run_typescriptで必要な処理を生成・実行する自己拡張は一部実装済みである。
自己改訂を全体として未実装とせず、経験の参照・振り返り、生成した処理の継続利用に不足する具体的な動作から
次の増分を選ぶ。専用の候補管理・採用flowを一式で作ることは必須残件にしない。

現在の基盤はstandalone executableと編集可能な外部tool folderのpackage、現在fileから選ぶJSON
Agent設定とfolder-based tool、native instruction/Skill、 共通Agent Worker
runtime、SQLiteのcanonical/non-canonical履歴と実効configuration snapshotである。 managed Agent
Definition/module closure/transportは32〜34で導入した後、181で廃止した。
これらを今後の必須方式として扱わず、JSON設定・実使用内容を振り返る現在の経路を使う。

instructions、skills、context、tool、delegation、model、agent
loop、runtime、Host/Worker連携、Surfaceへの 拡張は通常利用の具体的な必要から一つずつ選ぶ。native
discoveryはmanaged installへ置き換えない。 durable Instance、managed
Skill、専用candidate管理を自己改訂の開始条件にせず、最初の対象をAgent設定に限定しない。
`/rebuild`は実装しない。Agentによるroot
model・effort変更は効果が薄いため見送り、実装残件に含めない。
人間のmodel・effort選択、subagent起動時のmodel指定、設定/toolを新Workerへ反映する既存経路を使う。

## 実装状況の読み方

| 表記     | 意味                                                                      |
| -------- | ------------------------------------------------------------------------- |
| 実装済み | active sourceに現在のproduct動作がある。未観測の将来variantまでは含めない |
| 部分実装 | 後続機能に使える実装はあるが、列挙したproduct動作全体は成立していない     |
| 未実装   | active production経路にそのproduct動作がない                              |
| 廃止     | 過去に実装した方式を現行経路から撤去済み。将来の必須残件として扱わない    |
| 不採用   | 利用者が実装しないと決めた動作。未実装の残件には含めない                  |
| 見送り   | 現在の効果・必要性に照らして進めない動作。現在の実装残件には含めない      |

## 現在の到達点と残る候補

- 通常利用の基盤: standalone、TUI/CLI、provider/認証、JSON Agent、外部tool、hook、subagent、
  保存Sessionとsemantic履歴の経路は実装済み。採用された個別incrementの結果は各文書を参照する。
- 自己改訂:
  run_typescriptによるcode生成・実行・結果利用の自己拡張と、人間主導の改訂運用は部分実装済み。
  現在の経路で経験の参照・振り返り・継続利用に不足が現れたとき、その一動作を採用する。
- 未採用候補: [通常利用メモ](experience/normal-use-inbox.md)のA3（Context Strategy）、 A29（token
  usage/cache量の保存）等。優先順・実装認可はまだ定めていない。
- 追加機能: WebUI/一般Surface置換、durable Instance等は、具体的な利用目的が生じた場合に採用する。
- 実装対象外: F27のrebuildは不採用。F29のAgentによるroot model・effort変更は見送り。

## 構想とarchitectureから導かれる機能一覧

廃止・不採用・見送りの項目も参照IDを保つために掲載する。これらを現在の実装残件には数えない。

### SurfaceとSessionの通常利用

| ID  | 必要な機能                                                                                                                                                                                     | 構想・architectureとの関係                                                                                                                                                       | 現コードの状態                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| F01 | 人間が外部文書やhelpを先に読まずHenjiへ依頼し、進捗と結果を理解して通常利用を続けられる。配布後はrepositoryや導入済みDenoへの固定参照なしに、任意のworkspaceから同じproduction経路を利用できる | 改訂前後の経験を生む通常利用。SurfaceはHostが所有し、実利用で改善する                                                                                                            | **部分実装（通常利用経路は実装済み）**。standalone binaryの通常起動は新Core・新Sessionを作り、TUIはHTTP/SSEで接続する。ID/URL再接続、detachとCore停止の分離、三行footer、F1 Session一覧、F2予約、F3追加指示、slash picker、Markdown描画、履歴中Esc latest、API key登録とSign in with ChatGPT、Session個別削除を提供する。171でuser行の全幅panelを実装し、193・197で端末ANSIパレットを使う現行配色へ変更した。207で拡張キー要求を追加し、tmux経路のShift+Enter改行を通常利用で確認した。画面の現行動作はarchitectureのSurface節を参照する。grapheme幅、更新合流・行差分・synchronized outputは実装済み。長時間通常利用の入力遅延等の未再現観測を解消済みとはしない。WebUI本体は未実装                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| F02 | 構成済みのsystem instructionを含むprovider/model requestを実行し、複数step、tool call/resultを進めるagent turn                                                                                 | AgentCompositionとturn semanticsはWorkerが所有する。F02はF06で構成済みの入力を各model requestへ渡し、tool call/result loopを実行する                                             | **実装済み**。共通Workerが最大128 model stepsを既定としてprovider/modelとtool loopを実行する。起動時のmax-stepsとrequest単位provider deadline（未指定300,000 ms）をroot/child/compactionへ適用する。同梱四routeとexternal宣言を使い、Coreの現行model一覧・お気に入り・effort metadataからidle時にSession selectionを保存し、turn内で固定する。192で同梱`openai-chat`を廃止し、共有Chat Completions adapterはOpenRouterとexternal宣言用に維持する。ChatGPTは独立auth profileとaccount bindingで共通Responses adapterを使う。effort指定Responses requestは表示用summaryも要求する。read window、bash_output継続取得、外部searchのfile名/本文一致file/一致行/出現数/entry metadata/file集計の六操作、外部git_inspectのstatus/diff/log/show、Exa web_searchとsave_to対応web_fetchがある。201でentriesとgit_inspect、206でstats（LF行数・Unicode空白区切りの単語数・byte数）と専用toolの選択案内を追加した。statsは検索用DB/blob除外を適用しない。modelの自発的な選択改善は通常利用の観測待ちである。searchは共通file列挙を使い、rg不在時だけgrepへfallbackする（186）。countは同じ条件で対象全体の出現数をmatchCountへ合算し、contentの一致行数totalと区別する（187）。202でfiles/content/countをtext検索に限定し（DB/blob除外）、stdout 8 MiB・stderr 64 KiBのcapture上限と超過時の停止、返却JSON 1 MiB・matching textのprefix化、部分結果のtruncated/totalIsExact表示を追加した。Exaは非model検索requestとして親model budgetとは分けて短いphysical request factへ相関し、補助model requestは親budgetを使う。最終回答時にも受付済みsteeringを次model requestへ取り込む（175）。失敗時はprovider API/transport/timeout/解析を区別し、短い例外・stream状態を残す（176） |
| F03 | workspaceの`AGENTS.md`とworkspace/user scopeの`SKILL.md`をnative規約でzero-install discoveryし、実行中に共有するsnapshot/catalogを作る                                                         | 他harnessと共有するfile format、配置、discovery、activationをHenji独自managed resourceで置換しない。未実装のmanaged Skill候補と、現行Henji base instructionのauthorityを分離する | **部分実装**。Worker generationの起動時にworkspace rootのinstructionと、workspace配下およびuser scope（`$ZOT_HOME`または`$XDG_STATE_HOME/zot`、`~/.claude`、`~/.agents`）の`.zot/skills`、`.claude/skills`、`.agents/skills`を一度読み、実行中に共有するimmutableなinstruction snapshotとskill catalogを構築する。tool call時には再読せず、`skill` toolの結果はF02のtranscriptへ入る。Increment 51でnative入力とは別authorityのHenji base instructionを追加した。Increment 103でbuilt-inを最小core（役割identityとcredential/Authorization境界）へ縮小し、外部contentを`$XDG_CONFIG_HOME/henji-harness/instruction.md`の直接読み込みへ変更した。managed Skill revisionと改訂候補の生成・採用は未実装である                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| F04 | canonical会話、non-canonical execution、実効構成とrequest contextを再起動後も区別して使う                                                                                                      | Data Workerがsemantic authorityと保存、Host mainが会話採用判断を所有する                                                                                                         | **実装済み**。workspace共通history.sqlite3（schema 1）へSession、execution、configuration、message、semantic/context/recall/diagnosticを保存する。本文はcontents/messages、採用順はsession_turns/conversation_messages参照で保持し、artifact/exportは読取時に生成する。canonical turnとSession revisionをatomic採用し、child・detached・cancel/failureもnon-canonicalで残す。共有DBのlock/reconciliation、短いrequest factを維持する。199でdescriptor・読取・採用portの処理を必要最小限へ再構成し、意味・順序・revision・所有を維持した。旧DB migration/互換読込/自動削除は行わない                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| F05 | 人間が保存済みSessionのcanonical/non-canonical semantic historyとcontext attribution、短いrequest factを通常利用中に参照する                                                                   | 経験を人間が確認し、診断、`/recall`、後の改訂指示へ使うSurface機能。history rendererとmodel projectionを分ける                                                                   | **部分実装（人間向け参照経路は実装済み）**。接続TUIのPageUp／PageDown・mouse wheel、Session picker/view、`/context`、API Worker経由のHTTP history/context読取と別processのread-only `henji history`（session/canonical/detail）を使う。canonical/non-canonical、thinking、本文とtool、短いrequest factをsourceから投影する。生成中本文はrequest単位の最新durable state、完了時はmodel result、停止時は最後の本文を保持する。Session再開・再接続でもexecution/step/表示identityと順序を維持し、system結果通知を対象executionへ置く。`/recall`は選んだnon-canonical executionを次taskへ投影する。組み込みliteral検索は持たない                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |

### AgentCompositionと現在のHost / Worker runtime

[Increment 191](increments/increment-191.md)で、F06の標準work toolへrun_typescriptをlocal実装し、
compiled TUIと実providerで受入対象機能を確認した。async関数本文、network、実行時std importを提供し、
既存Host所有の呼出し専用Henji子processで同期計算中も取消できる。導入時の全体gateは645
testを含め通過した。
importはstd限定、通常fetchは利用可能である。VM内Agent実行は191の範囲に含めない。
[Increment 204](increments/increment-204.md)で、workspaceと/tmpに加えてconfig rootのread/writeと
`henjiConfigRoot`変数を加え、credential値とChatGPT account fileをstate配下の専用rootへ移した。
[Increment 213](increments/increment-213.md)でfileアクセス設定を共通`tool-paths.json`のtool別allowと
共通denyへ切り替えた。設定済みallowは既定を置換し、旧`run-typescript.json`は読まない。 Deno
permissionによる許可範囲とcode本文照合によるbest-effortのdeny監査は区別する。
191・204・213とも常用配置済み。

この経路は、Agentが作業上必要な処理を生成して実行する自己拡張として、F20〜F24の部分実装にも対応する。
code/inputと結果は既存semantic履歴へ保存し、結果を後続model stepで使う。設定/toolのfile編集は
新Workerの現在file解決から反映する。生成codeの実行と永続toolの登録は別の動作として扱う。

| ID  | 必要な機能                                                                                          | architecture上の責務・境界                                                                                | 現コードの状態                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| --- | --------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F06 | JSON Agent設定をmodel、instruction、tool、hook、child操作と共通AgentCompositionへ組み合わせる       | Hostが現在fileとmetadataを選び、Workerが共通factoryで構成・実行する                                       | **実装済み（現行JSON設定）**。181でname/revision/instruction/tools/agents、agents.jsonのdefault/named選択、tools.jsonの現在folder選択、Worker-local ToolFactoryを実装した。revisionは版名でcontent identityではない。defaultとgenericは同じ同梱JSONを基底とし、named親のroleをgenericへ継承しない。186でsearch/web_search/web_fetchを同じ外部folder供給へ揃え、201で外部git_inspectを加えた。packageのinstallerが四toolを現在configへ登録し、同梱default/genericはgit_inspectも選ぶ。tools/agentsの空配列を尊重し、個別rejectの理由をinstructionとSurfaceへ示す。有効toolの同じ宣言とexecutorを提示・dispatchへ使う。189でhooks.jsonとAgent hooksによる共通default/明示置換/無効化、Worker-local外部TS factoryと六hookを追加した。開始日時定義をpackageへ同梱し、確定後checkpoint更新は元履歴を保ってData ownerへ保存する。子のhookは子自身のcontextへ作用する。compaction用の外部TS定義は未実装。起動済みWorkerは固定snapshotを使い、編集は新Workerから反映する。子は別Worker/Executionのone-shot fork/joinで、model/toolsを指定できる。model省略は親executionのselection・実効ChatGPT登録ID、明示modelは指定providerの認証を使う（182）。再帰child、任意loop/context差替え、reloadは未実装 |
| F07 | executable Agent Definitionのmodule closureをimmutable revisionで固定・選択する旧方式               | 現行は現在Agent JSON/tool folderの選択とconfiguration snapshotへ置換                                      | **廃止（181）**。32〜34のmanaged store、closure install/検証、exact selectorとbuilt-in resource digestによる起動を撤去した。tool local importはDenoで直接読み、revisionは版名、使用内容は独立configuration IDで記録する。旧方式の再導入を残件にしない                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| F08 | 実効構成と各requestに使った情報をexecutionから振り返る                                              | Workerが起動snapshotとrequest attributionを返し、Dataが保存する。版名や説明をpermission authorityにしない | **部分実装**。configuration IDにAgent設定/選択元、最終instruction/components、実提示toolの版名・選択元・contract/rejectを保存し、executionへ完全build、実model/effort、maxStepsを保存する。request context、skill/tool/runtime fact、recallは専用recordから参照する。181でcompiled親子の実build保存を修正した。過去tool source closureや外部状態の完全再現は行わない                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| F09 | 同梱/外部JSON Agentを同じDeno Web Workerと共通runtimeで実行する                                     | Workerはlifecycle/data境界であり別trust tierではない                                                      | **実装済み**。Core対話と独立headless runが共通session factory/bootstrap/構成/runtime/protocol/commitを使う。外部toolは同じWorker内でimportし、functionをHostへ渡さない                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| F10 | UIをWorkerから分離し、Host側の交換可能なSurfaceとして扱う                                           | terminal / Surface I/O、layout、draft、cursor、viewportはHost-side adapter、Agent Workerはheadless        | **部分実装**。Coreのapplication service/read modelとHTTP/SSE contractからTUIを分離し、terminal/draft/viewport/picker/Markdown rendererをclient-localにした。同Coreへの複数TUI接続、detach後の実行継続、非対話CLIのcurated NDJSON/live text出力がある。Increment 179でHTTP/SSEをCore所有のAPI Workerへ、runの引数・stdio・出力drainをheadless Host所有のCLI Workerへ分離した。Data Workerの会話state・保存、mainの操作判断・採用判断を維持し、公式compiled版のTUI完了/cancelとrun出力・保存readbackが成立した。WebUI本体と一般的なSurface選択・load・置換interfaceは未実装                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| F11 | HostがWorker generationを起動、停止、監視、置換する                                                 | Worker generationのlifecycle ownerはHost                                                                  | **実装済み（現行Core/generation経路）**。Core mainはAgent Worker起動・cancel・forced termination・置換・採用判断とchild cleanupを所有し、Data Workerがsettlement・保存を実行する。CoreはAPI/Data Workerも起動・終了まで所有する。独立runではheadless Host mainがCLI/Data/Agent Workerと清算を所有する。Host共通executorはprocess groupをgeneration単位で所有する。通常起動ごとに独立Coreを作り、Core list/status、ID/URL再接続、指定Core shutdownがある。detachはCoreを止めず、Core shutdownは他Coreを止めない。durable AgentInstanceは別の未実装機能                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| F12 | Host / Worker間でdata-only command、event、effect、proposal、ackを交換する                          | 関数は境界を越えず、protocolがapplication semanticsを運ぶ                                                 | **実装済み（現在のcontract）**。Core/headless Host–Agentのcontrol、Agent–Dataのcontext/semantic/proposal、Host–Dataのadmission/prepare/authorize/settleはdata-onlyである。API Worker–Coreのoperation/購読/lifecycle、CLI Worker–headless Hostの選択/実行/event/result/drainもdata-onlyで接続する。Surface–CoreにはHTTP operationとSession snapshot/updateのSSE contractがある。これらは別境界であり、TUIはCore経由で操作する。将来のprotocol negotiation/version migrationは未設計                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| F13 | proposalをHostが検証・永続化し、durable write後だけcommittedとする                                  | Data Workerがcanonical store、Host mainが採用許可を所有する                                               | **実装済み**。Dataがexecution/configuration参照、base revision、transcript/next turnと最終sequenceを検証してprepare tokenを返す。Hostの採用許可後にDataがatomic commitし、保存完了後にackする                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| F14 | Session/execution、configuration ID、generation、base revisionを相関しadmit済みwriterだけを採用する | Host admissionとData revision fencing                                                                     | **実装済み（Session/execution単位）**。共有DBとSession writer lockで別Sessionを並行実行し、同一Sessionの二重writerを防ぐ。Agent/toolの任意revision labelをcommit authorityにしない。Core epochはprocess identityで、durable InstanceのSession横断writer管理は未実装                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| F15 | tool effectとstate commitを区別し、不明なeffectを自動replayしない                                   | Workerはeffect evidenceを返し、Hostはsettlementを記録する                                                 | **実装済み（現在のtool経路）**。tool event、execution artifact、`automaticReplay: false`、`effectCommitRelation: not_transactional`がある                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |

配置と所有は[Host/Worker architecture](architecture/henji-host-agent-worker.md#host--worker-境界)、
API/CLI分離の受入は[179](increments/increment-179.md)、Core重複処理の削除と配置は[180](increments/increment-180.md)、
JSON/tool/新DBと実build保存の修正は[181](increments/increment-181.md)、認証継承と最新配置は[182](increments/increment-182.md)を参照する。
分離や処理削除だけから応答速度・入力遅延の改善を保証しない。B9のbuild
attributionは181で解消済みである。

F02、F03、F06の境界は次のとおりである。F03がWorker generation起動時にnative instruction
snapshotとskill catalogを作り、HostがHenji base instruction（built-in
coreまたは`$XDG_CONFIG_HOME/henji-harness/instruction.md`）を 解決する。F06はWorker-core
finalizerでselected baseをJSON Agentのrole寄与等の先頭へ合成し、native instruction本文とskill
manifestも system instructionへ合成するとともに`skill` toolを registryへ組み込む。F02は、そのsystem
instructionを各model requestへ渡し、modelが`skill` toolを呼んだ
場合は、起動時のcatalogに保存した該当skill本文をtool resultとしてtranscriptへ加え、次のmodel
stepへ渡す。 したがって、skill一覧は最初のmodel
requestから見えるが、skill本文は最初から一括してmodelへ渡されない。 これはnative
discovery経路の責務である。Henji base instructionはこのzero-install入力を置換せず、 一つのcore-owned
`instruction:henji-base` slotとしてその前段へ加わる。managed Skill revisionは未実装であり、
採用時にkind固有のselection authorityと合成順を決める。

### 現在のSurface実装であるproduction TUI

production TUIはF01の通常対話、F05のSession/history/context参照、F10のHost-owned Surfaceを横断する。
現行の所有と操作・表示は
[architectureのSurface節](architecture/henji-host-agent-worker.md#surfaceと現在のtui)を参照する。
TUIの詳細仕様を機能一覧と別の一覧へ重複保存せず、commandは`v0/tui/slash_command.ts`、
実装・検証結果は各incrementを正本とする。

現行sourceの入口は`v0/agent/cli/tui_cli.ts`と`remote_tui_cli.ts`であり、
`v0/tui/remote_session.ts`、`snapshot_presentation.ts`、`tui_renderer.ts`、`assistant_layout.ts`、
`layout.ts`、`terminal.ts`がHTTP read modelから画面と操作を構成する。Core側は
`v0/agent/host/core_service.ts`、`application_service.ts`、`v0/agent/http/server.ts`、`v0/api/`である。

Session
pickerの閲覧/再開/削除、`/context`、`/recall`は実装済み。Agent自身の現在execution・実効構成を
発見する専用入口（F28）は、この人間向け経路と区別して未整備とする。 Agentからのroot
model・effort変更（F29）は効果が薄いため見送り、残件に含めない。

### durable AgentInstanceとrevision transition

F16/F17（durable AgentInstance、Instance単位writer
ownership）は、複数Sessionを共通のidentity・active binding・
writerで束ねる具体的な必要が生じた場合に採用する。自己改訂を始める必須条件にはしない。
保存Sessionを現在設定で継続し、executionへ起動構成を記録するF18の範囲は実装済みである。
76のDefinition切替方式は181でchoice/configuration参照へ置換した。 Instance単位のbinding
transitionはF16/F17を採用する場合の追加範囲とし、candidate採用との接続はF22で扱う。

| ID  | 必要な機能                                                                    | architecture上の責務・境界                                                                          | 現コードの状態                                                                                                                                                                                                                                                  |
| --- | ----------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F16 | Worker generationより長く存続するdurable AgentInstanceを持つ                  | resident Hostがdurable lifecycle ownerとなり、stable identity、metadata、activeな設定参照を所有する | **未実装**。現`instanceCorrelation`は`WorkerHostSession`ごとに生成され、永続化されない                                                                                                                                                                          |
| F17 | durable Instance採用時に対象Sessionの所属とInstance単位writer/inputを管理する | Session単位の現行管理と区別し、Hostが所有する                                                       | **未実装**。現在SessionはworkspaceとAgent choice、executionのconfigurationへ相関するがdurable Instance IDは持たない。現行のSession継続とwriter管理は自己改訂に利用できる                                                                                        |
| F18 | 現在設定で後続実行を続け、過去の使用内容を区別する                            | Hostがchoice解決と新Workerを所有し、Dataが過去/後続configurationを保存する                          | **実装済み（Session継続）**。保存Sessionのchoiceから現在fileを解決し、過去snapshotとの一致を要求せず新configuration IDを使用する。過去turnのattributionは保持する。Instance単位transitionは未実装であり、候補採用に必要な追加操作は具体的な不足が出た場合に選ぶ |

### 経験駆動の改訂ループ

人間主導でHenji自身が一部incrementの実装を担う運用と、run_typescriptによる自己拡張を、自己改訂の
部分実装として扱う。Agentが必要な処理をcodeとして生成・実行し、結果を使い、code/inputと結果をsemantic
履歴から振り返る経路は成立している。以下はその成立範囲と具体的な不足を示し、専用candidate schemaや
一式の候補管理・採用flowの完成を要求するものではない。経験の解釈・候補生成・採用・反映にAgentが
関与した範囲は、個々の実行または採用incrementで記録する。

| ID  | 必要な機能                                                                                                    | 構想・architecture上の責務                                                                                                                    | 現コードの状態                                                                                                                                                                                                                                                                                                                                     |
| --- | ------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F19 | 通常利用の困難、成功、違和感、目的、利用者判断を、後のWorkerが必要に応じてSessionをまたいで読む経験として残す | Hostがdurable semantic historyとreadback、Workerが経験の意味と選択を所有する。durable Instanceや新しいstoreを前提にしない                     | **部分実装**。canonical/non-canonical履歴、checkpoint、`/recall`、context attributionと短いrequest factはある。保存Sessionとrepository上の通常利用メモ・increment文書も現在の改訂運用に使う。Agentが対象経験を発見・選択し、目的・理由やused／missing／unused assessmentへ結び付ける操作は未整備。自身の観測操作はF28で扱う                        |
| F20 | 人間の明示的なアクションまたは指示を契機に、Worker内AIが経験を解釈して対象機能の変更候補を作る                | 経験の解釈と候補生成はWorker。最初の対象をAgent設定に限定せず、人間の契機なしに自発生成しない                                                 | **部分実装（自己拡張・運用）**。run_typescriptでAgentが作業上必要な処理をTypeScriptとして生成し、実行できる。人間の指示でHenjiが一部incrementを実装する運用もある。経験の解釈・改善案形成にAgentが関与した範囲は実行ごとに記録し、必要な追加動作は通常利用から選ぶ                                                                                 |
| F21 | 変更候補を現在の採用・実効状態と区別し、人間が内容と由来をreadbackできる                                      | 対象に応じてsource/diff/data、由来と採用状態を相関する。全resource共通のcandidate schemaは前提にしない                                        | **部分実装（既存readback）**。生成code/inputと結果はsemantic履歴から参照でき、設定/toolの変更はrepositoryのsource/diffと個別increment文書で確認する。経験・候補・採用状態の追加の相関は、現在の参照経路で不足した場合に採用する。専用candidate storeは必須にしない                                                                                 |
| F22 | 人間の採用アクションまたは明示的承認に基づき、対象機能の変更を後続実行へ反映する                              | 人間の目的・改訂範囲・採用境界内でWorkerが生成・実行を要求し、Hostがprocessと設定適用、Dataが履歴を所有する。対象に応じて既存の反映経路を使う | **部分実装（自己拡張・既存反映経路）**。生成codeをtool callで実行し、結果を後続model stepへ渡す。採用済み設定/toolのfile変更は現在fileを読む新Workerから反映する。人間の指示による実装・採用・配置の運用もある。専用採用flow、rebuild、Agentのroot model変更を必須残件にしない                                                                     |
| F23 | 改訂後のHenjiで通常利用へ戻り、そこで得た変化を次の経験にする                                                 | 改訂の循環を育てる動作。統制実験や定量測定は必須ではない                                                                                      | **部分実装（自己拡張・運用）**。run_typescriptで作った処理の結果を同じtaskの後続stepで使い、実行codeと結果を履歴へ残す。改訂後のHenjiを通常利用する運用もある。経験の参照・振り返り・生成した処理の継続利用の不足は、通常利用で具体化する                                                                                                          |
| F24 | Agent設定以外も経験に応じて改訂対象にできる                                                                   | 現在file、native input/state、Worker-local tool、binary platform authorityを区別し、対象ごとのowner/lifetimeを決める                          | **部分実装（run_typescriptによる自己拡張）**。Agentが必要な処理をcodeとして生成・実行し、既存toolの組合せを超えて作業を進められる。user instruction、native AGENTS/Skill、JSON Agent、folder tool等も対象にできる。生成codeの実行と永続toolの登録は区別し、経験に基づく継続利用で不足する動作を選ぶ。共通frameworkや専用候補管理を先行必須にしない |
| F25 | exact managed resource revisionをinstallation間でtransportする旧方式                                          | 現行Agent/toolは現在file/folderを読み、移送専用authorityを持たない                                                                            | **廃止（181）**。34のAgent Definition export/importを撤去した。設定file/folderを人間が配置することはexact revision transportの実装ではない。別kindのtransportは必要が生じた場合に採否を判断する                                                                                                                                                    |

### Durable historyとcontext適用

| ID  | 必要な機能                                                                                  | architecture上の責務・境界                                                                                                                 | 現コードの状態                                                                                                                                                                                                                                                                                                            |
| --- | ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F26 | 人間がsettled non-canonical executionを選び、その保存内容を次の一つのtaskへ明示的に投影する | Hostがsource選択、次taskへの一回のprojection、source/target attributionを所有する。sourceをcanonical化、resume、自動retryしない            | **実装済み**。Increment 38の`/recall`がcurrent Sessionのexecutionを選び、次task内のmodel requestへdata-only contextを渡す。sourceはnon-canonicalのままで、targetだけが通常のatomic commit対象になる。Increment 134で未完了本文を一request一本文へ揃え、元の観測位置とcompleted step・tool情報を保持するreadbackを接続した |
| F27 | resourceから実効構成を再構築する旧rebuild案                                                 | 専用Host operationとrebuild用context generationは導入しない。設定/toolは既存の新Worker起動・Session再開・Core再起動から反映する            | **不採用（実装しない）**。2026-10-08の利用者判断。新Session、`/new`、reopen、再起動の現在設定取込みは維持し、会話やdraftを保持する専用rebuild操作を残件にしない                                                                                                                                                           |
| F28 | Agentが自分の対象execution・実効構成を発見し、必要な履歴を選んで観測・振り返る              | Hostは既存semantic authorityとattributionをreadbackし、Workerは目的に沿って材料を選び解釈する。人間向けrendererとAgent向けreadbackを分ける | **部分実装（材料・間接参照）**。会話・tool resultと、bashによるhistory CLI等の参照は可能。人間向けTUI/historyも充実している。自身のexecution・構成を発見して必要な材料を取得するproduct操作は未整備。raw常設収集、durable Instance、専用experience storeを前提にしない                                                    |
| F29 | Agentが後続root実行のmodel・effortを選ぶ案                                                  | 人間のidle時Session selectionと、子実行のspawn時model指定を使う。Agent向けroot変更operationは見送る                                        | **見送り（効果が薄い）**。2026-10-08の利用者判断。人間の`/model`・`/effort`、generic／名前付きchildのspawn時model指定は実装済み。Agentのroot変更要求を現在の実装残件に含めない                                                                                                                                            |

## architectureで例示する採用未決の将来オプション

以下は、現時点のarchitectureで例示している採用未決の将来オプションであり、将来機能の網羅的な一覧では
ない。現時点の構想から実装を要求されるものでもない。人間が明示的に機能を要求するか、通常利用の経験から
必要性を判断して採用した場合に、architecture上の責務を具体化してroadmapへ追加する。ここにない機能も、
同じ構想・architecture・roadmapのloopで追加できる。

| 例示ID | 将来オプション                                     | 採用した場合のarchitecture                                                                                                 |
| ------ | -------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| C01    | Worker不在時にもInstance宛てinputを保持するmailbox | Hostがdurable queue、Workerがdequeue eventの意味を所有する                                                                 |
| C02    | 非同期または複数Surface間のmessage routing         | HostがmessageをInstance/Sessionへ対応付け、Workerはoutputの意味を生成する                                                  |
| C03    | scheduleによるwake-up                              | Hostがtime/deliveryとenqueue、Workerがschedule intentとevent処理を所有する                                                 |
| C04    | 常にaddress可能なagent operationとdelivery         | 採用するHost lifecycle ownerへ、外部からの常時到達性、delivery、継続稼働とそのservice supervisorを加える。Worker常駐は不要 |
| C05    | 一般的なInstance-wide mutable state                | HostがInstance revision、lock、persistenceを所有し、Sessionとの二重正本を避ける                                            |

## 機能一覧と主なarchitecture領域の対比

一つの機能が複数領域に関わる場合があるため、この表は各F番号の主な対応先を引く索引であり、網羅的な
責務表ではない。

| architecture領域            | 対応する機能                                     |
| --------------------------- | ------------------------------------------------ |
| Surface / human interaction | F01、F05、F10、F20〜F23、F25、F26                |
| HenjiHost lifecycle         | F07、F11、F14、F16〜F18、F25、F26、F28           |
| Host storage                | F04、F13、F15〜F19、F21〜F23、F25、F26、F28      |
| Agent Worker                | F02、F03、F06、F08、F09、F12、F19、F20、F26、F28 |
| commit / revision boundary  | F07、F13〜F15、F18、F21、F22、F25、F26、F28      |

F24のarchitecture領域は固定しない。次のself-revision loopで選んだ改訂対象に応じて、Agent
Worker、Host、 storage、Surface、またはそれらの境界のどこへ対応させるかを決める。

model生成のprovider
HTTPはWorker内で構築され、model一覧取得と認証操作はCoreが所有する。processの物理ownerはHostで、Workerのtoolはdata-only
proxyから
共通executorを利用する。tool結果とRegistry出力storeの意味はWorkerが所有する。このplacementを将来も固定する
決定にはしていない。最初のloopで変更する具体的理由がなければ現配置を保ち、別の境界を設計しない。

## 反復型実装ロードマップ

| 分類                  | 対応機能           | 現在の基盤と次の判断                                                                                                                                                                                                                                                                      |
| --------------------- | ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 通常利用と改善        | F01〜F15、F26      | Core/Data/API/CLI/Agent分離、TUI、履歴、recall、追加指示等を使い、観測した一動作から増分を選ぶ                                                                                                                                                                                            |
| 配布・設定・tool      | F01、F03、F06〜F09 | standaloneと外部search/git_inspect/web toolのpackage/install、JSON Agent、現在folderのtool読込みと構成snapshotは実装済み（186・201）。file集計と選択案内（206）、run_typescriptのconfig root利用（204）と共通fileアクセス設定（213）も実装済み。旧managed Definition/transportは181で廃止 |
| Providerとservice認証 | F02、F06、F08      | data-only route、model一覧・お気に入り・effort、API key/ChatGPT、Exaと共通serviceキー登録を実装済み                                                                                                                                                                                       |
| 自己改訂を支える操作  | F06、F19〜F24、F28 | run_typescriptによる自己拡張と人間主導の改訂運用は部分実装済み。generic/named childも実装済み。経験の参照・振り返り・生成した処理の継続利用の不足から一動作を選ぶ                                                                                                                         |
| durable Instance      | F16〜F18           | 複数Sessionの共通identity/設定/writerが必要になった場合に採用する。自己改訂の前提にしない                                                                                                                                                                                                 |
| 追加オプション        | C01〜C05           | 非網羅的な例示。採用時に正式機能として要件と境界を定める                                                                                                                                                                                                                                  |

### 通常利用で見つかった問題を改善する（F01〜F15、F26）

人間が改善を直接要求するか、通常利用で具体的な問題や改善機会が見つかった場合は、F01〜F15、F26のどの
product動作に関わるかを確認し、通常利用へ戻せる狭いincrementの個別実装計画を
`docs/increments/increment-N.md`として作る。このroadmapへ個々のincrementや作業履歴を追加しない。
契機がなければ実装作業は発生しない。人間が通常利用の経験からHenji自身へ改訂を指示する運用もこのincrement単位で
進められる。Agent自身が経験解釈・改善案形成に関与した範囲は、担当した個別incrementの結果へ記録する。

TUIの改善もこの扱いに含む。入力・表示・操作性は主にF01、Session/history/contextの利用はF05、Surfaceの
分離・load・置換はF10へ対応付ける。既存Fで表せない新しいproduct動作には新しいF番号を付ける。Surface
自体も必要な時点でF24の改訂対象にできる。目的を変える場合は構想、Host / Worker /
Surface境界を変える場合はarchitectureへ先に戻る。

#### 履歴・構成を拡張する場合

現行history.sqlite3、configuration snapshot、request contextとsemantic履歴を使い、利用者が必要とする
readback/構成操作に具体的な不足があればその動作を採用する。旧schemaやfixtureを実仕様として復活させない。
過去executionの通常自動継承はcanonicalに限定し、現在executionのtool
resultや明示recallは別inputとして扱う。

effort、loop、context/compaction等をAgentごとに変える必要が生じた場合は、JSONの表現、共通runtimeとの境界、
構築・適用時点とsnapshotへの記録をその一要素について定める。任意executable
Definitionを前提にしない。
設定/toolの変更は現在fileを新Worker起動時に解決する既存経路から反映する。`/rebuild`は実装しない。

#### Surfaceをload・置換する場合（F10）

現行のData Workerの会話read model、API WorkerのHTTP/SSE、接続TUIとrun CLI Workerを基盤とし、
WebUI等の新Surfaceまたは一般的なSurface置換を採用する 場合に開始する。その時点でSurface
identity、Hostによるload/selection、input actionからCore operationへの 変換、output delivery、active
Sessionとのbinding、置換時のUI-local state引継ぎを決める。

### 導入・置換の記録への入口

詳細な当時計画・contract・受入結果は個別文書に保持し、このroadmapへ重複して保存しない。

| 領域                                 | 導入・置換と現行の参照                                                                                                                                                                                           |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| standalone/native discovery          | [32](history/increments/increment-32.md)。配布基盤を維持する                                                                                                                                                     |
| managed Definition/transport         | [33](history/increments/increment-33.md)、[34](history/increments/increment-34.md)で導入、[181](increments/increment-181.md)で廃止                                                                               |
| durable history                      | [40](history/increments/increment-40.md)〜[44](history/increments/increment-44.md)、[170](increments/increment-170.md)、[181 contract](increments/increment-181-contract.md)。現行はschema 1                     |
| Henji base instruction               | [51](history/increments/increment-51.md)、[103](increments/increment-103.md)。現行はbuilt-in最小core/user file                                                                                                           |
| provider/data-only宣言               | [58](history/increments/increment-58.md)〜[68](history/increments/increment-68.md)、[provider/auth architecture](architecture/multi-provider-routing-and-auth.md)                                                                |
| managed tool/同期subagent            | [69](history/increments/increment-69.md)〜[72](history/increments/increment-72.md)の方式は106/181等で廃止。現行はfolder toolとasync Execution                                                                                    |
| 通常利用の検索・認証・追加指示・診断 | [172](increments/increment-172.md)〜[176](increments/increment-176.md)、[178](increments/increment-178.md)。Exa、download、serviceキー、未登録provider選択、final時steering、短いfailure detail、追加ChatGPT候補 |
| Host配置・重複処理                   | [179](increments/increment-179.md)、[180](increments/increment-180.md)。配置と清算のownerを維持する                                                                                                              |
| JSON/tool/DB切替と認証継承           | [181](increments/increment-181.md)、[182](increments/increment-182.md)。実provider受入・常用配置まで完了                                                                                                         |

### 自己改訂を支える次の増分

人間主導の改訂運用とrun_typescriptによる自己拡張を基盤に、通常利用で不足する観測・振り返り・継続利用を
一動作ずつ選ぶ。以下は既存経路と、具体的な不足が出た場合に判断する候補であり、固定された実装順序や
個別incrementの実装認可ではない。rebuildとAgentによるroot
model・effort変更はこの後続候補に含めない。

| 動作                                         | 使える基盤と不足                                                                                            | 採用incrementで決めること                                                                                                        |
| -------------------------------------------- | ----------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| 生成codeによる自己拡張（F06、F20〜F24）      | run_typescriptの生成・実行・結果利用と履歴保存は実装済み                                                    | 通常利用で継続利用に不足が出た場合、その必要な保存・参照・利用動作だけを追加する                                                 |
| 自身の観測・振り返り（F19、F28）             | semantic履歴・attribution・短いrequest fact、人間向けTUI/history、間接的なhistory参照がある                 | 今の作業に必要な対象Session/execution・実効状態、Agentがそれを発見し取得する入口                                                 |
| 構成を選んだ子実行（F06）                    | genericとspawn時task/model/tools指定はIncrement 131で実装済み                                               | 通常利用の結果から具体的な不足が出た場合に、その一動作だけを追加する                                                             |
| 経験に基づく候補・採用・通常利用（F20〜F24） | run_typescriptによる自己拡張、code/input・結果のsemantic履歴、source/diffと人間の指示・採用による運用がある | 既存経路で経験の参照・振り返り・生成した処理の継続利用に何が足りないか。必要な一動作だけを採用し、専用候補管理を必須残件にしない |

最初の対象をAgent設定に限定しない。instruction、skill、tool、modelの選択と使い方、loop、runtime、Host/Worker連携、
Surface等から実際の目的に必要な対象を選び、観測・構成操作・改訂反映の仕組み自体も改訂する。
現在設定、native input/state、binary platform authorityのどれを変更するかに応じ、既存の保存・採用・
適用経路を利用する。Henji executableはversion/build/source
commit、Agent/toolは版名と起動時の観測内容、native resourceは観測した内容、
modelはselectionとして追い、全体を一つの統一revisionにしない。

native `AGENTS.md`/Skill discovery、user
`instruction.md`の直接読込みを維持する。設定/tool変更は新Worker起動の既存経路で反映し、binary変更は
新buildとして扱う。改訂前後の統制実験・定量測定を完了条件にせず、改訂後の
通常利用で観測した変化を次の経験へ戻す。経験解釈・候補生成・適用にHenjiが担った範囲は個別incrementへ記録する。

durable Instanceは、複数Sessionを同じidentity・active
binding・writerで束ねる具体的な必要が生じた場合にF16/F17として 採用する。新しいexperience
store、専用candidate管理、mailbox等も、それぞれ必要な動作から採否を判断する。 旧Definition＋durable
Instance方式のPhase 1〜5は
[`過去の計画案`](history/self-revision-cycle-1-definition-instance-plan.md)へ保存した。

## 構想・architectureの未決事項と判断する契機

| 未決事項                                          | 判断する契機                           | 現在の扱い                                                                                                               |
| ------------------------------------------------- | -------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| Agent自身が観測する対象とreadback                 | F28の最初のincrement                   | 現在のSession/executionと実効構成から、今の作業に必要な情報を選ぶ。人間向けhistoryの存在だけで成立済みにしない           |
| 経験の具体的な残し方と読み方                      | F19/F20で具体的な不足を扱うとき        | 既存semantic履歴とrepositoryの記録を利用し、Sessionをまたぐ参照もdurable Instanceを前提にしない                          |
| 新しいexperience datumの生成・durable保存の粒度   | 新しい経験recordが必要になったとき     | 通常semantic append/canonical adoptionはIncrement 41/94、短いrequest factは121で実装済み。必要な追加粒度・相関だけ決める |
| 候補生成・内容確認の入口                          | F20/F21の具体的な増分                  | 人間の指示、Agentの観測材料、対象のsource/diff/dataと由来を結ぶ最小経路を決める                                          |
| 人間の採用境界と適用操作                          | F22の具体的な不足を採用するとき        | 人間が定めた範囲内のcode実行・設定/tool変更と、その境界を変える判断を分ける。操作ごとの承認は一律に追加しない            |
| process以外の物理I/O placementの変更              | 採用する機能が変更を必要とするとき     | processはHost共通executor、model生成HTTPはWorker、model一覧と認証操作はCore。対象の責務・lifetimeから判断する            |
| Worker protocolのmessage、handshake、versioning   | 新しいHost操作を公開するとき           | 人間のSurfaceとAgentのtoolでHost operationを共有する方向とし、その動作に必要なmessageだけ決める                          |
| 設定/tool変更の候補と適用経緯                     | 対象設定/toolの改訂で必要になったとき  | 現在file選択とconfiguration snapshotを使い、source closure/managed transportの復活を必須方式にしない                     |
| durable Instanceのidentity・所属・writer・binding | F16/F17を採用するとき                  | 複数Sessionを束ねる具体的な利用目的を先に定める。Session継続と自己改訂の開始条件にはしない                               |
| cross-session memory / Instance-wide state        | 観測・経験参照に具体的な不足が出たとき | 必要な状態とownerを定め、履歴の自動共有や全ての会話の混合を推測で追加しない                                              |
| mailbox、routing、schedule、常時到達性            | 個別機能を採用するとき                 | durable Instanceや自己改訂だけから実装を要求しない                                                                       |
| effectのrecovery、deployment、migration           | 対象effectや実行環境を変更するとき     | 実際の契約・移行対象に必要なsemanticsだけ決める。新しい権限・旧data削除を構想変更から導かない                            |
| 次の改訂対象                                      | 各incrementの通常利用後                | 観測された必要に応じて選び、Agent設定だけの一巡完了を待たない                                                            |
