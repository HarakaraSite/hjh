# I-17: go-quickjsへJS実行基盤を移す

2026-10-12。状態: 実装・実モデル・配布確認・実装review完了。
利用者は`github.com/go-quickjs/go-quickjs`への移行の影響調査、通常・批判的review、
追加spikeの要否確認を経て、計画作成を依頼した。独立した追加spikeは設けず、
本実装の最初の段階でWorkerと実Go操作の接続を確認する。
この文書はI-17の実装・確認計画である。[各段階の結果](../results/i17-go-quickjs.md)を参照する。

## 必要な利用動作と成功条件

現在のCLI/TUI操作を使い、モデルが生成したJSから外部tool・hookを呼び、ファイル・HTTP・
コマンド・子Agentによる作業を完了できる。実行証拠と構成sourceを保存し、同じSessionを
次のrunで続行できることを成功条件とする。

- 一つのWorker内では複数のモデルstepを通じて同じVMとmodule stateを使う。
  tool/hook編集は次Workerのcaptureで反映し、開始済みWorkerのsourceは変えない。
- 親のJSが成功・例外・取消で終了した際、残る子Agentを止め、子のWorker終了・保存まで待つ。
  子の遅い結果によって、終了した親JSを再開させない。
- 正常結果が決まっても、開始済みの未await host処理は完了まで待つ。
  JS例外・取消時は残るHTTP/process等をcancel・joinし、原因と終端観測を残す。
- 無限JS、HTTP待機、コマンド待機を既存の取消操作で停止できる。
  取消されたWorkerを閉じた後、次のWorkerで作業を始められる。
- tool/hook/host/子の観測IDと親子関係を保持する。モデルが宣言した外側toolは
  Agent loopの記録を使い、runtimeで同じ開始・結果を重複記録しない。
- 既存の`run_js`、標準tool、`run_agent`、hookは、同じJS sourceと入力で、
  結果・副作用・保存する観測の意味を保つ。具体的な互換確認は下記に定める。
- `run --connect`から依頼した作業を同じCoreのTUIで開き、進捗と最終回答を読める。
  clientのdetachではCoreの作業が続く。
- Linux/amd64でCGOなしの静的Core/TUIを配布し、実行先でGo・Node・Deno・共有libcを要求しない。

根拠は[初期製品範囲](../design/initial-product.md)、[拡張とWorker契約](../design/extensions.md)、
[履歴schema](../design/history-schema.md)、[Core接続run](i16-cli-core-run.md)と、下記の現行source・
調査・reviewである。API credentialとAuthorizationの露出防止も既存要件として維持する。

## 調査とreviewで確認したこと

移行対象は`github.com/go-quickjs/go-quickjs v0.25.2`。
調査したtagのcommitは`bab6855931194e37afe5355b86f64111189d6a6a`。
このversionで計画・実装し、途中でlatestへ追随しない。
Pure GoのQuickJS-NG再実装で、moduleのGo最低versionは1.24.0。
調査時のhjhはGo 1.27系、`modernc.org/quickjs v0.25.0`を使用していた。

一次資料:
[README](https://github.com/go-quickjs/go-quickjs/tree/v0.25.2)、
[埋込みAPI](https://github.com/go-quickjs/go-quickjs/blob/v0.25.2/docs/embedding.md)、
[非同期host処理](https://github.com/go-quickjs/go-quickjs/blob/v0.25.2/docs/hosting.md)、
[module依存](https://github.com/go-quickjs/go-quickjs/blob/v0.25.2/go.mod)。

repositoryを変更しない確認プログラムで、実際のhjh bootstrap、標準ESM tool、YAML/semverの
準備済みESM、asyncファイル読書き、Go例外のJS catch、module初期化の原例外、VM stateの保持、
無限ループのcontext取消、CGOなしの静的ELFを確認した。
Promise.allとAgent bridgeはmockの子結果で確認しており、実AgentRunnerの終了保存の証拠ではない。

同じ100,000回の算術ループとJSONを評価した小さな比較programでは、中央値が現行4.2229ms、
新engine1.3473ms。これはAgent作業全体の速度を示さない。同じ条件の単体binaryは
現行9,234,100bytes、新engine32,783,377bytesで、hjh実binaryの増加量は未測定。
依存するgo-intlのIntl/locale dataにより配布sizeが増える見込みである。
速度やsizeの目標値を機能の受入条件にせず、移行後の実binaryで記録する。

通常reviewは、直接engine依存が`internal/jsruntime/worker.go`へ集中し、公開Worker APIを
保てばCore/app/provider/history/TUIの境界を保てると判断した。source/JSON保存のため、
DB schema変更も不要と判断した。対応を要するfindingはなかった。

批判的reviewは、次の違いをsourceと限定probeで確認し、移行可能と判断した。

- `Runtime.Eval`は帰還前にjobsをdrainするが、`Value.CallContext`は自動drainしない。
  Worker callは後者を使い、root Promiseへcallbackを付けてからjobsを進める。
- `Await`帰還後に子の取消を始めると、root確定後の別callbackが先に実行される。
  fulfilled/rejected callback内で子scopeを閉じ、現行の終了境界を維持する。
- `rt.Go`はruntimeの寿命contextを渡し、結果到着時にPromiseをsettleする。
  call/agent contextと、保存だけしてJSを再開させない現在の処理への単純置換には使わない。

調査終了時に未確認だったのは、移行したproduction Worker、実子Agentの取消・join・終了保存、実HTTP/processの
停止、モデル作業・Session再開、最終配布物である。調査用probeや上流のconformance結果を
これらの成功の代替にしない。一時probeや委譲packetのpathは計画の正本にしない。

## 現行経路と状態の所有者

standaloneの`hjh run`はCLIから直接app.Service.Runを呼ぶ。
`hjh run --connect URL`とTUIはCore HTTP API→corerun Manager→app.Service.Runへ入る。
どちらもappがSession・selection・外部sourceを捕捉して親/子executionを開始する。
`internal/app/execution.go`がWorkerを作成し、Agent loopがモデルのtool callをWorker.Callへ渡す。
Worker内のbootstrap→選択tool/hook→Go hostまたはAgentRunnerが実処理を行う。
観測とJSON結果はappのjournal・historyへ保存され、会話ownerからclientへ配信される。

| 責務 | 所有者と維持する状態 |
| --- | --- |
| Session/run、モデル選択、予算、保存・会話配信 | 既存Core/app/agent/history。VM値を受け取らない |
| Worker入口と寿命 | jsruntime.Worker。依頼channel、done/cancel、tool definitions |
| VMとmodule | VM owner goroutine。Runtime、capture済みresource、module cache、bootstrap |
| callと非同期bridge | VM owner。call/agent context、pending Promise、Go jobと完了queue、root決着 |
| 観測 | runtimeのcall ID/active観測と既存Observer。保存SQLはjournal/historyが所有 |
| file/HTTP/process、子の実行と保存 | 既存host/AgentRunner。VMを操作せず、Go値で完了を返す |
| client | stdout/stderr/TUI描画。予算・VM・終了保存のownerにならない |

新engineの標準ライブラリでfile/fetch/process/workerを増やさず、現在のGo host経路へ接続する。
engine選択の汎用interface、dual engine、旧engine fallbackは追加しない。

## VM接続と終了処理の設計

公開`New`、`Definitions`、`Call`、`Close`、OptionsとJSON入出力は維持する。
Runtimeはownerが生成・操作・Closeし、Go host/子goroutineからValueを触らない。
現行のGo job、完了queue/wake、pending map、観測処理を活かし、新たな完了ownerを増やさない。

| 現行engine API | 移行先で行う操作 |
| --- | --- |
| NewVM/Close、RegisterHostFunc/RegisterFunc | quickjs.New/Runtime.Close、Setによる既存4bindingの登録 |
| module loaderとnormalize | SetModuleLoaderのsource/resolvedをcapture済みresource mapから返す |
| CallValue/Promise.Then | Getした`__invokeDirect`をValue.CallContextで呼び、thenのreceiverとcallbackを登録 |
| NewPromiseCapabilityとResolve/Reject | NewPromiseをownerのpending mapで保持し、完了queueからownerが明示settle |
| ExecutePendingJobs、別goroutineのInterrupt | RunJobsContextとCallContext。worker寿命の取消もownerのJS実行へ接続 |
| Value.MarshalJSON、Dup/Free | JSがencodeしたJSON文字列をownerでGo値へ取り出す。Goの参照寿命で管理する |

`__start/__end/__host/__agent`の名称とbootstrap/tool/hookの契約を保つ。
JSON object/array/scalar/null、特にweb_fetchのJSON文字列結果を同じ形で返す。
元の例外messageと既存で得られるstack情報はVMが開いている間にGo側へ取り出し、呼出し元へ伝える。
取消のGo errorも識別できるよう保持する。

正常callの順序:

1. call/agent contextを作り、外側toolと内側観測のownerを設定する。
2. Value.CallContextで呼び、返されたroot Promiseのfulfilled/rejected callbackを登録する。
3. RunJobsContextと現行の完了queueを処理する。callback内ではroot決着を記録し、
   子scopeをcancelする。callback内で子のjoinを待たず、ownerの終了処理で待つ。
4. 子の終了・保存をjoinする。root決着後に届いた子結果は観測を保存し、Promiseを再開させない。
   この初回joinでは、まだhost処理を待っている委譲tool/hookの終端観測を閉じない。
5. 残るhost処理とそのJS jobsをdrainする。host完了でbeforeToolが正常終了し、
   root決着後に遅れてAgentRunnerの呼出しへ進む場合も、このcall内で処理する。
6. drain中に追加されたAgentRunnerも再joinする。既に取消されたagent contextを使い、
   遅い結果は非deliveryで観測を保存する。ここで実子Workerが必ず作られるとは仮定しない。
7. その後、未awaitの委譲tool/hookの終端観測をownerが閉じる。
   drain中に正常終了したhookのcompletedはcancelledへ上書きしない。
   JSON結果を返す前に終了記録を揃え、scopeと参照を外し、次のCallで同じVMを使う。

例外時は原例外を保持し、子と残るhost処理をcancel・joinしてから返す。
context取消時はJS実行/jobsも止め、Go処理・観測の終了後にWorkerを閉じる。
Worker.Closeはこの終了を待つ。call正常終了時のscope取消を、次のCallまで残る恒久的な
Runtime abortとして設定しない。再利用するVMと取消されたWorkerの寿命を区別する。
現在の`joinAgents`、`settleAgent(..., false)`、`closeDelegationObservations`、host drainに相当する
責務を保持し、Goの完了queueをengineのAwait/Busyだけで置き換えない。
正常時は「root決着→子scope取消→初回join→host/JS jobs drain→再joinと非delivery保存→
委譲tool/hook終端close→返却」の順序とする。

## 変更範囲と分割

現行経路に基づく中心対象は`internal/jsruntime/worker.go`とその既存test、`go.mod/go.sum`、
`scripts/package-linux.py`、実装に合わせる契約/利用/結果文書である。
Workerの大きい単一fileは、公開入口、VM接続、ESM読込み、call/Promise終了処理、観測の
実際の責務で分ける。具体file名は実装時に決め、既存ownerを分裂させない。
外部に露出する汎用engine abstractionは作らない。

bootstrapや標準外部JSは現行sourceを使う。変更が必要な場合は具体的な実行結果から判断し、
tool APIを狭めたり異なる結果型へ変更してtestを通さない。
Core/CLI/TUI/provider、hostの操作仕様、履歴schema/codec、予算・recallの設計変更は含めない。
これらは既存の実接続を確認する対象であり、移行のために状態ownerを追加する対象ではない。

root moduleでmodernc.org/quickjsと不要になったlibquickjs依存を外す。
SQLiteが引き続き使うmodernc依存は保持する。`spikes/s01`は別moduleの過去の実験記録として
残し、移行や削除をしない。過去のspike結果の採用engineを新しいものへ書き換えない。

## 実装段階

| 段階 | 利用動作と実装 | その段階で確認すること |
| --- | --- | --- |
| 1. Worker差替え | v0.25.2へ依存を切替え、capture済みESM、4binding、JSON codec、明示Promise/queue、root決着callback、context取消を新APIへ接続。既存終了処理を保ち、責務ごとにruntimeを分割する | run_jsと標準toolの直接/生成JS経由、hook、YAML/semver、複数Callのstate、原例外が現行Worker APIで使える |
| 2. 終了と保存の整合 | 子scopeのcancel/join、host drain後の再join、遅い子結果の非delivery保存、委譲終端close、失敗・取消後処理を実接続で確認し、観測された差を修正する | run_agentの実AgentRunner/SQLite経路、未await/並行した子と遅い呼出し、HTTP/process/無限JSの停止、観測ID/親子と重複防止 |
| 3. 実モデルとclient | private Core/DB/workspace/tmuxで生成JSによる作業、保存、続行、CLI接続/TUI表示を実行する | 人間がファイル作業を完了し、同じSessionを続行できる。detach中もCore継続、TUIから履歴と最終結果を読める |
| 4. 配布と仕上げ | 静的packageを作り、依存licenseとmanifestを更新し、実装に合わせた文書/結果を記録する | 展開したCore/CLI/TUIと外部ESMで実作業・再開。実hash/size、source metadata、static ELF、同梱notice、実行時依存を確認 |

段階1/2は一つの移行実装を進める順序である。途中状態を配布可能と扱わず、
小さなprobeだけで段階3/4を省略しない。片側のengineへfallbackする中間製品は作らない。

## tool互換性の確認

比較基準は、現行のbootstrap・外部tool/hook・準備済みESM sourceと、同じ依頼/入力である。
新engineでも呼出し元が同じ結果を利用でき、ファイル・process・HTTP等の副作用、保存する
観測のkind/name/親子関係/終端statusを保つことを確認する。並行処理の完了順、生成UUID、
時刻を固定して出力全文を一致させることは求めない。既存契約と実操作を正本にする。
移行前後の比較は別の実行と保存先で行い、製品にdual engineを組み込まない。

| 対象 | 確認する既存動作と経路 | 確認する結果 |
| --- | --- | --- |
| run_js | Worker.Callの`{code}`をasync function bodyとして実行。await、Promise.all、複数toolの組合せ、選択済みtools.run_jsの内側呼出し、同じWorkerの次Call | JSON object/array/文字列/数値/bool/nullとundefined→null、既存の日本語を含む本文、VM/module state、元の例外が同じ契約で届く |
| read_file/write_file/edit_file | 登録toolの直接callとrun_js内tools経由で同じworkspaceのfileを読み、書き、既存の完全一致置換を行う | file内容・返すpath/text/bytes/置換数。呼出し元と保存結果が実fileに対応する |
| command | 直接callとrun_js経由でprogram/args/stdinを渡す。終了待ちと既存取消経路を使う | exitCode/stdout/stderr、workspace基準の実行。nonzero exitも既存どおり構造化結果で返す |
| http | 直接callとrun_js経由で既存Go HTTPを使う | status/bodyを返し、HTTP status自体をJS例外へ変えない。失敗したGo処理は既存の例外経路へ届く |
| web_fetch | 直接callとrun_js経由で登録wrapperを呼ぶ。HTML可読化、通常1MiB、save_toを使う | JSON文字列として本文・metadataを返し、save_toの原本文fileを保つ。結果型やI-14の挙動を変更しない |
| run_agent | 直接call、run_js内tools.run_agent/api.agents.run、Promise.all、未await、JS例外・親取消、hook待機後の遅い呼出し | role/taskと子結果の契約、実子が開始した場合の終了保存、AgentRunnerのjoin、root決着後の非delivery観測、次Callへ持ち越さないこと |
| beforeTool/afterTool | 現行外部hookが引数・結果を変更し、内側tool/Go操作をawaitする。原例外と遅いbeforeTool/afterToolも既存経路で扱う | 直接/生成JS経由の双方で引数/結果と順序を保つ。正常終了したhookを取消と誤記録せず、委譲終端を最後に閉じる |
| 選択toolとESM | role.toolsで選んだdefinitionsと生成JS API、相対import、YAML/semver、capture後の元source編集、次Workerの再capture | 同じtoolを使え、既存Workerは捕捉sourceを使い続け、次Workerだけに編集が反映される |

段階1/2では実Worker→外部JS→Go host/AgentRunner→保存の既存focused確認を使う。
tool単体のGo関数だけの確認や固定JS probeで、bridge・hook・保存の互換確認を代替しない。
不足する具体動作があれば移行実装後に最小限を追加する。
段階3では実モデルが生成したrun_jsで複数toolによる作業を完了し、保存・Session再開を確認する。
段階4でも展開した最終配布物で同じ外部JS/ESM経路を使う。

## 動作と検証の対応

既存testを新engineに通し、実仕様と矛盾するfixtureは実利用経路・外部契約を確認して修正する。
新規testは、以下の具体動作が既存testで確認できない場合にだけ追加する。

| product動作 | 既存のfocused確認の入口と根拠 |
| --- | --- |
| capture後のsource固定、VM state、module原例外 | `internal/jsruntime/worker_test.go`のWorkerKeepsVMAndCapturedModulesAcrossCalls/WorkerReturnsModuleInitializationError。外部編集と次Worker反映の契約 |
| run_jsと標準toolの互換 | 上のtool互換表に対応する直接/生成JS経由の実Worker確認。既存WorkerKeepsVMAndCapturedModulesAcrossCallsがread/write/editの組合せ、SelectedToolsControlGeneratedRuntimeAPIが内側run_jsを確認。command/http/web_fetchの既存実host経路も使う |
| 選択tool、hookと親子callの観測 | SelectedToolsControlGeneratedRuntimeAPI、`model_call_test.go`、LateDelegation/DelegationAfterTool/UnstartedDelegationの既存確認。roleとroot記録ownerの契約 |
| 子の結果、Promise.all、未await子の終了待ち | DirectRunAgent/AgentRunReturnsResult/AgentRunsCanCompleteConcurrently/UnawaitedAgentの既存確認と、`internal/app/delegation_test.go`の実AgentRunner/SQLite経路。子終了保存の契約 |
| host drain後の遅い子呼出しと最終観測 | TestLateDelegationFromBeforeToolIsClosedBeforeNextCall。beforeToolのcommand待機完了→hook completed→遅いAgentRunnerの完了/観測→委譲終端closeを確認。計画reviewで採用したP2の経路 |
| 正常時の未await host drain、例外時のcancel/join | RootSettlementStillDrainsPendingHostOperations/RootRejectionCancelsAndJoinsPendingHostCall。開始済み操作を残さない現行経路 |
| JS/HTTP/processの取消と次Worker | WorkerCancellationStopsWorkAndAllowsNextWorker。既存context/process group停止・Waitの契約 |
| JSON scalarとWeb tool | `internal/host/webfetch_runtime_test.go`の登録web_fetch wrapper。I-14の文字列結果・save_toを変更しない |
| model/generated JS→保存→再開 | appの親子/role/保存確認とprivate実モデルのfile作業。正常root会話採用と失敗/取消証拠の契約 |
| Core-owned runとclient | corerun/httpapi/接続CLIの既存focused確認、private CLI/TUIで進行中接続・detach・続行。I-16の契約 |
| model入力の予算と保存全文 | appの既存context_budget確認。runtime変更後も親子のtool結果が既存Go予算経路へ届くこと |

実装中は変更したruntimeを中心にfocused test/race、対象vet、format、diff確認を使う。
次にapp/host/AgentRunnerの実接続へ広げる。reviewerはfull gateを実行しない。
安定候補に対する`go test -race`の関連package統合確認はcoordinating ownerが一度実行する。
対象は`./internal/jsruntime ./internal/app ./internal/host ./internal/corerun ./internal/httpapi ./cmd/hjh`。
実装途中やreview前の全体gateにはしない。
失敗時はfocusedで原因を調べ、具体的な変更または未解決事項がある場合だけ再実行する。
全provider/accountや全npm互換の網羅、未知のfailure matrix、上流test262再実行は計画しない。

実モデル確認は既存の認証と明示無料modelを使い、実行前に公式catalogの現在のpricingを確認する。
providerの都合で必要な操作が実行できなかった場合は、fixture成功を実作業完了と扱わず原因を記録する。
有料model・追加認証・別accountの実行は今回の確認へ加えない。
private保存先・process/tmuxを使い、利用者の稼働Coreを停止・置換しない。

既存`scripts/verify-linux-live.py`は有料modelを前提にし、historyの旧出力を読むためそのまま使わない。
I-17の配布確認は現在のCLI契約・無料model設定で行うprivate経路を用意する。
過去scriptの一般的な改修は今回の対象に加えない。

## 配布・文書・review

`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1`を維持する。
commandが呼ぶ外部programの実行時依存は従来通りで、本体の静的配布とは区別する。
新module直下のMIT、go-intl直下のUnicode licenseに加え、
go-quickjsの`internal/fdlibm/LICENSE`を現在のpackageへ収集し、manifestのnotice/file hashにも含める。
上流の既知のnoticeを扱うための変更とし、任意のfilesystemへ一般化した探索処理は追加しない。
Core/TUIの実sizeとarchive sizeを記録し、実行時にIntl/locale dataの追加downloadを要求しない。

実装後に[初期製品のengine記載](../design/initial-product.md)、拡張契約のcontext取消記載、
README/配布文書、必要なarchitecture説明を現行sourceへ合わせる。
`docs/results/i17-go-quickjs.md`へ実装・実利用・review・配布hash/size・未確認事項を記録する。
履歴に記録する実binary digestとpackageの依存metadataでengineを識別し、
この移行だけを理由に履歴schemaやtool/hook contract versionを変更しない。

実装候補では、通常reviewは動作・保存・責務・配布、批判的reviewは新APIのjob順序・
取消・child/hostの終了境界を確認する。非同期実行と保存の順序を独立して追うことに
具体的な価値がある。各初回30分上限、10分新しい根拠なしなら確認範囲と暫定結論を返す。
修正後は採用findingと変更箇所だけを対象に、一回15分以内で再確認する。
一般的security/hardeningのreviewはこの計画に含めない。

## 計画reviewの反映

正式計画の通常reviewは必須findingなし。standalone runとCore接続の入口を二経路へ明記する
低優先の提案を反映した。
批判的reviewのP2は、正常call手順が委譲tool/hook終端をhost drain前に閉じ、drain中に追加された
AgentRunnerの再joinを欠いていた点である。初回join→host/JS drain→再joinと非delivery保存→
委譲終端closeへ修正した。根拠は上記LateDelegationの既存確認と現行awaitCall/callの終了順序。
また利用者指定により、run_js・標準tool・run_agent・hookの互換性を独立した確認表に明記した。
修正後、同じ通常・批判的reviewerが変更箇所を一回ずつ限定再確認し、P2の解消と
入口/tool互換表の整合を確認した。追加の必須findingはなかった。
これらは計画段階での修正であり、その時点では新engineでの実動作確認を行っていなかった。

完了は、上記の実作業・終了保存・続行が新engineを使った最終配布物で成立し、
必要なreview対応と文書が揃った状態とする。commit/push、公開、利用者の稼働Coreへの反映は
別の明示指示に従う。既存dataの削除・変換、自動rollbackは行わない。
