# I-19 子agentの非同期実行と報告取得と追加指示

2026-10-12。状態：実装・確認・review完了。スライスの確認結果は[実装結果](../results/i19-child-agent-control.md)を参照。
利用者は子の開始と後からの回収、並列実行、model・provider・effort・tool指定を求めた。
続いて、中間報告とfindingの取得、前回以降の差分取得、親から子への追加指示を同じ範囲へ追加した。
追加指示の送り手は親agentであり、人間による子の直接操作は想定しない。
利用者指定により、実装と確認を六スライスに分ける。

## 必要な利用動作と成功条件

親agentが複数の子を開始してexecution IDを受け取り、子と並行して自分の作業を進める。
親は子の状態と保存済みの中間報告を読み、必要なら子へ追加指示を送る。
子はその指示を次のモデル入力へ取り込んで作業を続け、親は必要な時点で結果を回収して回答を完成させる。
子ごとにmodel、provider、effort、toolを選べること。

通しの成功経路は次のとおり。

1. 通常のCLIまたはTUIから自然文で依頼する。
2. 親が異なる構成の二つの子を開始する。
3. 子が実行中の間に親も独立した作業を行う。
4. 親が子の中間報告を取得し、次の取得では前回以降の新しい報告を読む。
5. 親が報告を踏まえて子へ追加指示を送る。
6. 子が指示を取り込んで作業を続け、親が終了結果を回収する。
7. 親が最終回答を返し、保存履歴から報告、指示、実際の子modelとtool、親子の結果を照合できる。

固定responseだけで完成扱いにせず、実モデルが通常のtool経路を選んで作業を完了したことを確認する。
各スライスのfocused確認は、その段階の動作を切り分けるために使う。

## 根拠と比較対象

| 根拠 | この計画で扱う内容 |
| --- | --- |
| 今回の利用者指定 | 非同期の開始と回収、並列実行、子の構成指定、中間報告と差分取得、親から子への追加指示、複数スライス |
| [初期製品方針](../design/initial-product.md)と[拡張契約](../design/extensions.md) | 親子共通のGo実行層、外部tool、独立VM、開始時の構成捕捉 |
| [provider設定](../design/providers.md) | providerとmodelと認証の分離、roleのmodel設定、実selectionとcredential binding |
| [履歴schema](../design/history-schema.md)と[保存payload](../design/history-payloads.md) | 子のnon-canonical保存、親子関係、実使用構成、既存schemaの維持 |
| [I-09の追加指示](../design/i09-runtime-input-contract.md) | request境界での取込み、受付と適用の分離、終了直前の受付処理、保存後の通知 |
| [I-14の予算管理](../design/i14-web-context-contract.md)と[I-18のworkspace指示](../design/workspace-instructions-skills.md) | 子ごとの予算、AGENTS.md、skill一覧と本文読取り |

旧Henjiの参考sourceはcommit `2eb586316f80ec8297873a96122568d0088971eb`。
[比較結果](../results/henji-feature-comparison.md)を入口として、
`v0/agent/tools/async_agents.ts`と`v0/agent/worker/worker_host_children.ts`を確認した。
statusの中間報告と追加指示の案は[通常利用メモA24](../normal-use-inbox.md)に記録されている。
参照位置は同文書のA24節、特に2026-10-07の追加相談である。
旧側でも中間報告の取得と追加指示は未実装であり、旧実装の単純移植とは扱わない。

旧Henjiの[Increment 138](https://forge.harakara.site/littleisland/henji-harness/src/commit/2eb586316f80ec8297873a96122568d0088971eb/docs/increments/increment-138.md)にある、
lifecycleとmodel/tool進捗を分ける考え方を参考にする。
旧側の上限、tool名、設定形式、Worker配置、外部化の順序を一括採用するものではない。

## 現行の操作と保存の経路

入口は直接CLIの`app.Service.Run`、またはCore接続CLI/TUIからの
`HTTP API → corerun.Manager → app.Service.Run`。
以降は共通の`app.Run → execute → agent.Run → JS Worker／provider → journal／history`へ進む。

子は`run_agent → api.agents.run → jsruntimeのAgentRunner → app/delegation.go → execute`を通る。
delegationは同じconfig pathを子開始時に再captureし、子roleを選ぶ。
modelは親の実selectionへ子roleのmodel設定を適用する。子は独立した会話、VM、journalを持ち、
Sessionには直接採用されない。親子は同じexecutiondata.Ownerで保存順を管理する。

現在のJS runtimeは外側tool call単位に子を管理し、外側Promiseの終了時に残る子を取消して待つ。
この寿命のままでは、spawnした子を次の親model stepから操作できない。
非同期の子は共通Go実行層で親execution単位に管理し、VMは操作のrequest/resultを渡す責務にする。

中間本文は既存のmessagesとassistant表示用stateへ保存される。
差分取得には確定したmessagesを使い、生成途中の累積stream stateを別の報告列として複製しない。
人間のF3はCoreのinput-chainで受付し、Agent loopのAdditionalInput境界で適用する。
現在、app側のsteering接続はroot限定なので、子には子専用の入力ownerを接続する必要がある。

## 操作契約の計画案

| 標準tool | 生成JSの操作 | 意味 |
| --- | --- | --- |
| `run_agent` | `api.agents.run` | 子を開始して終了結果を待つ既存の便利な操作。構成指定を追加する |
| `spawn_agent` | `api.agents.spawn` | 子を開始し、admission保存後にexecution IDを返す |
| `agent_status` | `api.agents.status` | 状態、構成、直近進捗、報告の差分、追加指示の状態を取得する |
| `collect_agent` | `api.agents.collect` | 子の終了と保存を待って結果を取得する |
| `cancel_agent` | `api.agents.cancel` | 指定した子へ取消を伝え、終了と保存を待って結果を返す |
| `steer_agent` | `api.agents.steer` | 親から実行中の子へ追加指示を送る |

これらは外部JS toolとして構成し、生成JSのapiも選択済みtoolと同じhook・履歴経路を通す。
rawなGo操作は外部tool実装側へ渡し、生成JSへは選択した操作を公開する。
新しいchild専用HTTP APIや人間向け操作UIは設けない。

### 子の構成

runとspawnは`{task, role?, model?, tools?}`を受け取る。
modelは`{provider?, modelId?, effort?}`の部分指定で、認証値を引数へ含めない。

- role省略時は、子開始時のconfig.defaultRoleを使う。親のrole指示は暗黙継承しない。
- modelは親の実selection、子roleのmodel設定、呼出し時のmodel指定の順で解決する。
- providerが変わると、省略されたmodel、effort、auth profileは指定先providerの既定を使う。
  providerが同じなら既存resolverの継承規則を使う。
- tools省略時は子roleの一覧を使う。指定時はその実行の一覧を置換し、空配列はtoolなしとする。
  選択できるのは同じconfigに登録済みのtoolとbuilt-in run_js。
  親や子roleの既定一覧に含まれるものだけへ新しく制限することはしない。
- 指定したtool一覧から、モデル提示、生成JSのtools/api、実指示、skill reader案内、保存構成を組み立てる。
  元のconfig fileを書き換えて一時指定を実現しない。
- 子開始時にconfigとsourceを捕捉し、そのexecutionの選択を固定する。
  workspace、root Sessionの相関、各executionのstep上限、認証bindingの共通経路を維持する。
- 会話とprovider replay、recallは子へ暗黙継承しない。
  AGENTS.mdとskill一覧は現在の共通処理で子開始時に読む。

### 非同期の寿命と状態

子の操作IDには保存済みのexecutionIdを使い、別の永続run IDを追加しない。
状態はstarting、runningと、保存済みのcompleted／failed／cancelled等の終端outcomeを区別する。
statusのprogressはstarting／model／tool／between_steps／settled、更新日時、step、直近toolの情報を持つ。
statusを読むだけでは進捗の更新日時を変えず、完了率やfindingを推測しない。

- spawnした子は親executionに属し、run_jsが返った後も継続する。
  同じ親の後のmodel stepからstatus、steer、collect、cancelを呼べる。
- 複数の子を同時に開始し、IDごとに操作できる。生成JSのPromise.allも利用できる。
- run_agentは結果を待つ操作として維持する。
  現在の未await run_agentのcall終了時取消は、明示的なspawnの寿命延長とは区別する。
  子の準備と実行、結果、保存は同じ共通処理を使う。
- 親の正常終了、失敗、取消では、新規の子開始と追加指示の受付を閉じ、準備中を含む残る子を取消して待つ。
  子のWorker、処理中操作、指示、終了保存が完了してから親の終了保存と会話採用を確定する。
- TUIのdetachは親子の取消理由にしない。Core上で処理を続け、再接続では同じ保存と進捗を読む。
- 子の失敗や明示取消は、保存済みの構造化結果として親が判断できる。
  保存失敗は現在のexecutionControlで伝播させ、JSでcatchして成功と偽らない。
- 終了済み子のcollectやcancelを繰り返しても子を再実行せず、確定した結果を返す。
  操作対象は、その親executionが開始した子とする。
- collectは終了まで親toolの結果を待つ。待機中に親が別のmodel requestを出す方式は追加しない。
  子を制御する判断はcollect前のstatusとsteerで行う。
- 子の会話はnon-canonicalとして保存する。親の入力には通常のtool結果として選んで返した内容だけが入る。

## 中間報告と差分取得

`agent_status({executionId, after?})`は、状態とprogressに加えて`reports`と`nextCursor`を返す。
after省略は最初からの取得。nextCursorを次回のafterへ渡すと、以降の新しい報告を取得する。

- reportsは子のmessagesへ確定保存された通常assistant本文を返す。
  tool callと併せて出した本文も対象とし、空本文、thinking、tool引数・結果、暗号化replayを混ぜない。
- findingを読むための専用送信toolや自動要約は設けない。
  子が報告しながら調査を続ける使い方をtool説明と共通指示に含める。
- 最終のassistant本文が既に保存されていれば報告取得の対象になるが、
  成否と終了保存の確定した構造化結果はcollectで取得する。
- cursorは子executionに属するmessage ordinalを基準とする。
  報告には保存元execution IDとmessage ordinal、本文を持たせ、親の取得結果から元履歴を追えるようにする。
- 本文と上端ordinalは同じSQL読取cutで取得する。
  cutより後に保存された報告を返していないcursorで飛ばさない。
  全ての子tool出力を読み込んでから捨てる方式を避け、assistant本文を範囲指定で読む。
- 共有の既読位置を持たず、status読取りでcursorを進めない。
  同じafterから再取得でき、複数回のstatusや並列操作で読取り済み扱いの報告を失わない。
- 生成途中のstream断片はこの差分取得に含めない。
  既存の生成中表示と、messageとして保存済みの報告取得を分ける。
- 親が読んだ報告はstatusの通常tool結果へ入り、その履歴に残る。
  run_jsから取得した場合は、必要なstatus結果を外側returnへ含めて親モデルへ渡す。
- 状態と報告の取得で子を停止せず、最終回答やtoolを自動再実行しない。
  親のモデル入力は既存のcontextbudgetを通し、別の報告専用予算や固定byte上限は追加しない。

## 親から子への追加指示

`steer_agent({executionId, text})`は、受付保存後にinstruction IDとreceiptを返す。
状態はaccepted、applied、unapplied。agent_statusからその子のreceiptの最新状態を確認する。
送り手は親agentであり、人間のF3やTUIから子を指定する操作は追加しない。

- 子ごとの入力ownerが受付順、処理中の保存、未適用指示、受付終了を所有する。
  親に属するF3 input-chainを子へ継承・コピーしない。
- 受付内容と親子・tool callの対応を既存semantic履歴へ保存してからacceptedを返す。
  指示本文のsourceと、受付、適用、未適用理由を後から読めるようにする。
- 指示は子の会話へ追加するuser messageとして扱う。
  出所は親executionとsteer操作で記録し、人間が入力したものと取り違えない。
- 生成中requestとtoolは中断せず、最初のrequest前、tool群の結果保存後、最終応答後の
  既存AdditionalInput境界から取り込む。tool callとresultの間へ差し込まない。
- 子messageの保存と適用receiptの更新を同じtransactionで確定し、その後にloopへ渡す。
  同じ受け付けた指示を次の境界で再適用しない。
- 指示は受付順に取り込む。複数回の追加指示を扱い、旧側の一回制限を追加しない。
- 正常終了直前は、処理中の受付保存をlock外で待つ。
  保存済み指示があれば取り込んで続行し、処理中も未適用指示もなければ受付を閉じて終了する。
- 取消、失敗、step上限等で適用できなかった場合は、その理由をunappliedへ残す。
  終了済み子をsteerで再起動しない。
- appliedは保存とloopへの取込みを意味する。モデルが指示どおりに作業したかは後の報告と結果で確認する。
- 追加指示ではmodel、tool、AGENTS.md、skill一覧を組み直さない。
  変更した構成が必要なら別の子を開始する。

F3から共通化するのは、Agent loopの取込み境界と保存後の適用処理である。
Core固有のrun map、follow-up予約列、HTTP受付や人間向けUIまで子の実装へ移さない。
指示の本文とreceiptは既存contentsとsemantic記録を使い、DDL変更や新しい子専用DBを設けない。

## 責務と変更範囲

| 所有者 | 変更内容と責務 |
| --- | --- |
| `internal/contracts` | model/tool指定、非同期操作、status/report/cursor、子指示の型付き値。VMやSQLを所有しない |
| `internal/extensions` | 子tool指定を適用したsnapshotと指示の組立。標準外部toolと選択toolに対応する案内 |
| `internal/app`のdelegationと子管理 | 子の準備と開始、親execution単位の寿命、IDと状態、collect/cancel、子指示owner。共通executeを利用する |
| `internal/jsruntime` | 各操作のPromiseとGo値の接続。call単位のblocking委譲と、親execution単位のspawnを区別する |
| `internal/agent`とappの入力adapter | 子でも既存request境界で追加指示を取り込む。Agent loopは保存SQLを知らない |
| `internal/history`とjournal | 報告の範囲読取り、receiptとmessageの保存、親子・開始・回収・指示の関連付け。既存schemaを使う |
| `internal/toolactivity`とCLI/TUI | 新toolの短いpreviewと既存子進捗・履歴への接続。実行管理や直接SQLは追加しない |

非同期spawnの開始toolは子の完了より先に閉じる。
子の終端を開始toolの終了resultへ後から書く設計にはせず、既存の子execution終端と
collect/cancelの記録から保存済み関係を結ぶ。
共通executiondata.Ownerとjournalの保存後通知を使い、親子のlive表示と履歴復元を一致させる。

## 六スライスの実装順序

| スライス | 完了する動作 | 主な変更範囲 | 段階の確認 |
| --- | --- | --- | --- |
| S1 子の構成指定 | 既存run_agentで子のmodel、provider、effort、toolを指定できる | contracts、extensions、delegation、標準run_agent | 実際の子requestと保存snapshotが指定内容になり、省略時の選択も現在のresolverと一致する |
| S2 親実行単位の子管理 | 共通Go操作で子を開始し、親が続行し、IDから状態・結果・取消を扱える | appの子管理とexecuteの終了処理、journal | 子を動かしたまま親を進められ、準備中と実行中の子を親終了時にjoinして保存できる |
| S3 非同期toolとJS接続 | spawn/status/collect/cancelをモデルと生成JSから使い、複数子と独立作業を組み合わせる | 標準外部JS、config、jsruntime、toolactivity、案内 | 二つの子を開始し、別の親stepでstatusとcollectを呼ぶ。既存run_agentとPromise.allも成立する |
| S4 報告と差分取得 | statusで中間報告・findingを読み、nextCursor以降を取得する | historyの報告読取り、appのstatus、tool schemaと指示 | 最初の報告、新しい報告、同じcursorからの再読、空差分を保存元と照合する。親tool結果にも本文が残る |
| S5 親から子への追加指示 | steerを受け付け、子が境界で適用し、親が状態と続行結果を確認する | 子入力owner、app入力adapter、agentの境界接続、history/journal、標準steer/status | 子tool実行中の受付と後の適用、複数指示の順序、終了直前の受付、取消による未適用理由を確認する |
| S6 通しの実利用と仕上げ | CLI/Core/TUIから一連の依頼を完了し、再接続と保存履歴を確認できる | 設定配置、build、利用案内、結果文書、採用したreview修正 | 実モデルによる通し操作、Core接続run、TUI detach/再接続、別processのhistory/inspectで照合する |

順序はS1 → S2 → S3 → S4 → S5 → S6。
S2の子管理はS3の公開操作を成立させるための共通実行層であり、その内部確認だけでI-19完成とはしない。
S1からS5までは変更した経路のfocused確認を行い、S6で通常のproduct経路をまとめて確認する。
スライス完了ごとに変更範囲と確認済み動作を結果文書へ記録する。commitやpushは別の利用者指示に従う。

### S1 子の構成指定

現行の準備処理を独立した責務として整理し、blockingとspawnの両方から利用できる形にする。
子toolの置換はcapture時の構成入力として扱い、実指示やworkspaceのreader判定より先に解決する。
configに保存した元roleの一覧、実際に選択したtool、VMへ公開する操作が食い違わないようにする。

model指定は既存provider resolverを使い、同じ接続先のbinding共有と、別providerの認証解決を維持する。
新しい認証方式やaccount選択UIは追加しない。

### S2 親実行単位の子管理

子の準備、admission、実行、終了保存を一つのGo ownerへまとめる。
既存delegationに別の起動実装を併設せず、準備とexecuteを共有する。
長いconfig読取り、認証、provider呼出し、SQL、joinは子一覧のmutexを保持して行わない。
親の受付終了と準備中操作を同じownerで追い、親終了後に遅れて子が起動する経路を残さない。

JS callの寿命と子executionの寿命を分けるため、spawnのPromiseはadmissionまでを待つ。
子の結果を待つgoroutineをVMのhost drainへ残さない。
run_agentの未await処理には現在のcall単位取消を適用し、明示spawnだけを親executionまで延長する。

### S3 非同期toolとJS接続

標準configのdefault roleに非同期操作を登録し、run_jsの説明と生成apiを有効toolに合わせる。
外部tool実装のapiと、生成JSの選択tool経由apiの両方を確認する。
親がすぐcollectへ入ることだけを案内せず、独立作業、必要時のstatus、結果が必要になった時点のcollectを説明する。

標準の子進捗と終了記録はCLI/TUIの既存経路へ流す。
親のmodelや本文を子の観測で置き換えず、spawn行と子execution、collect行と終端を追えるようにする。

### S4 報告と差分取得

既存messagesのordinalを使ったreadbackを用意し、報告用の別storeや自動要約を作らない。
本文とcursorの読取りcutを揃え、子が新しい本文を保存している間の取得でも報告を飛ばさない。
親が必要な本文をrun_jsのreturnへ含めた場合に、次の親requestと親の履歴に同じ内容が入ることを確認する。

### S5 親から子への追加指示

子入力ownerとAgent loopのadapterを接続する。
現在のroot限定条件を外すだけで親のsteering sourceを子へ渡すことはしない。
受付中の保存、指示messageと適用記録のtransaction、終端判定を接続してから標準steerを使えるようにする。

子の通常応答後に受け付けた指示で続行する経路も確認する。
receiptのappliedを「モデルが従った」という表示にせず、後の報告を親が判断できるようにする。

### S6 通しの実利用と仕上げ

最初に検証用の独立workspace、DB、Core、設定を使い、利用中のCoreやSessionを検証のために停止しない。
既存configは自動migrationせず、検証用configと明示的に更新する実利用configに必要なtool定義を登録する。
既定配置のInstallDefaultsは既存fileを保持するため、assets更新だけで現在のworkspaceへ反映した扱いにしない。
更新対象と変更内容を結果へ記録する。

利用可能な無料モデルと保存済み認証を実装時点で確認して通しの実モデル確認に使う。
有料接続や新しいブラウザ認証が必要になる場合は、実行前に利用者の指定を確認する。
自然文の依頼へ必要な報告と独立作業の目的を含め、固定JSを実モデルの操作選択の代替にしない。

実モデルの確認を補う制御可能な経路で、detach中の子実行、TUIからの同親子への再接続、
単独子取消と親取消、履歴の保存と読戻しを確認する。
標準配布の両binaryを更新して起動経路を確認し、結果と残る具体的な未確認動作を記録する。

## 確認とreview

| 確認する動作 | 根拠と確認方法 |
| --- | --- |
| 子ごとの構成が使われる | 利用者のmodel/tool指定。実provider requestのselectionとtool定義、子configurationを照合する |
| 子実行中に親の次stepへ進む | 非同期開始の明示要件。子完了を制御して、親の独立toolと次requestが先に実行されることを確認する |
| 複数子と取消が別々に扱われる | 並列実行と個別制御。二つの子を同時に開始し、一方を取消して他方の回収を確認する |
| 親終了前に子の保存が完了する | 現行の親子終了保存契約。実行中と準備中の子について、親終端より前に子終端を確認する |
| 新しい報告を飛ばさない | 差分取得要件。保存前後のcutとcursorを固定し、保存元、返却本文、次回範囲を照合する |
| 親の入力へ取得内容が入る | 中間報告の利用要件。statusの親tool結果と次の実model requestを照合する |
| 子が追加指示で続行する | 親から子への指示要件。子の実行中toolを完了させ、次requestに指示が入り後続結果へ反映されることを確認する |
| 受付と終了が整合する | 指示を取り残さない要件。最終応答後に受付済み指示があれば続行し、取消等では未適用理由を保存する |
| liveと復元を同じ親子として読める | 現行のCore/TUI/履歴契約。SSEと別processのhistory/inspectで同じexecutionと本文と指示を照合する |

実装中はfocused test、必要なformatとvet、git diff --checkを使う。
並行処理を変更するappとJS runtime、入力と保存の接続はrace付きのfocused確認を行う。
安定した全スライスの候補で、変更したpackageと直接の利用者の回帰確認をまとめて一回行う。
全体testや配布buildを各スライスの途中で繰り返さない。

reviewは通常の機能correctness、明示要件、親子の寿命と保存、report cursor、
追加指示の受付と適用、既存のrun_agentとF3へのregressionを対象にする。
実装結果に対応しないtest件数や未観測variant、一般的なhardeningを完了条件にしない。
独立reviewerへ委譲する場合は、利用者の指定または適用される委譲規則に従い、対象スライスと評価基準を限定する。

## Goと現在の実行基盤による条件

今回の操作はGoで実現できる。
QuickJSの値は所有goroutineだけが操作し、子やGo jobとの通信にはGo値とJSONを使う。
親がmodel request中でも、子の観測と指示処理はGo側で進める。

Goのgoroutineを外から強制終了する機構はないため、取消は既存context、JS割込み、
HTTP取消、Linux commandの停止を通して行い、子の終了と保存を待つ。
cancel要求を出した時点と終了完了を区別する。
子ごとに独立VMを持つため、並列数に応じてCPUとメモリ使用量は増える。
この計画で固定の同時実行上限や別のstep総量上限は追加しない。

## 今回の対象外と未確認事項

- 人間から子への追加指示や個別操作UI、子専用の公開HTTP API。
- collectのtimeoutやyield、子の実行時間上限、自動期限取消、定期報告とpush通知。
- 親の最終回答後や次turnへの子の存続、Core再起動後の自動再実行、durable AgentInstance。
- 新しい報告生成モデル、findingの自動抽出、thinkingやstream断片を報告として公開する機構。
- 新しい認証方式、schema変更、データ削除、旧形式のmigrationやfallback。

実モデルの中間報告とstatus/collect/steerの選択はS6で観測した。頻度と操作順はmodelとtaskに依存し、
終了後のsteer拒否、親の待機手順の誤選択、成功した受付/適用と回収を実行証拠として記録した。
報告の専用toolや自動制御は追加せず、詳しくは[実装結果](../results/i19-child-agent-control.md)を参照する。
標準configと実利用configには新toolを登録し、実利用のmodel/providers/credentialsを維持した。
子指示は型付きChildInstructionReceiptと既存semantic_records/contentsを使い、
message/appliedの同一transactionを既存保存codecへ接続した。採用DDLの変更は不要だった。
