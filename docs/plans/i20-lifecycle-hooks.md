# I-20 ライフサイクルhook

2026-10-12。状態：S1–S5の実装・test・reviewと総合実利用確認完了。
確認範囲と結果は[実装結果](../results/i20-lifecycle-hooks.md)を参照する。
利用者は[機能比較メモ](../results/henji-feature-comparison.md)のライフサイクルhookを次の作業として指定した。
利用者は要約checkpointをまだ実装しないと明示した。
今回の範囲は六種類のhook、開始時の文脈追加、終了処理と記録保存とする。
checkpointの生成・保存・次turnやSession再開時の再利用は対象外であり、実装時期は未決定。

## 必要な動作と成功条件

利用者が外部ESMのhookを編集すると、次のCLI/Core/TUIのrunと子agentに反映される。
Worker開始・停止、turn前後、tool前後の六つの境界で外部処理をawaitし、
親子の識別と実行結果を保存履歴から確認できる。
開始時とturn開始時に返した文脈は、その実行のモデル入力へ入り、既存の予算管理にも含まれる。

具体例は、外部hookによる開始日時の追加、turn開始時のworkspace情報の読取り、
確定したturnの終了通知、Worker停止時の後処理である。
子の文脈は子だけに適用し、親へ自動挿入しない。
tool引数・結果を変更する既存の二hookと、非同期子操作を維持する。

成功の確認は、独立workspaceの外部hookを実際に編集してcompiled CLIとCore/TUIから使い、
モデル入力・hook記録・保存と採用の順序を照合することとする。
focused testは境界と取消を確認する手段であり、人間が使える実経路の確認を置き換えない。

## 根拠と現行経路

- 明示要件：今回の利用者指定と、比較メモのライフサイクルhook項目。
- hjhの契約：[外部拡張](../design/extensions.md)、[履歴schema](../design/history-schema.md)、
  [予算管理](../design/i14-web-context-contract.md)、[子agent](i19-child-agent-control.md)。
- 旧Henjiの参考source：commit `2eb586316f80ec8297873a96122568d0088971eb`の
  `v0/agent/hook_api.ts`、`v0/agent/worker/worker_runtime.ts`、
  `docs/increments/increment-189.md`、`external-hooks/runtime-start-time/index.ts`。
  旧TSのfactory形式や複数登録、checkpoint、自動要約を一括採用する指定ではない。

直接CLIは`app.Run`、Core接続CLI/TUIは`corerun.Manager → app.Run`へ入り、
rootと子のどちらも`app.execute → agent.Run → jsruntime.Worker`を使う。
`extensions.Capture`がconfigとESM sourceを実行開始時に捕捉し、
`bootstrap.js`の`invokeHook`が現在の`beforeTool`と`afterTool`を呼ぶ。
hookと内側操作はObserverから`app.journal`を通り、意味記録と関係をSQLiteへ保存する。

Workerは現状一つのexecutionに属し、親の次turnは新Workerを作る。
runtimeはCore起動・TUI接続・Session作成を意味しない。
終了経路は現在`Worker.Close → 子のcancel/join → journal.end → rootのAdoptCanonical`である。
旧Henjiのafter_turnは保存・採用確定後であり、単にagent.Runの直後へ置くと意味が異なる。
また、現行`history.AppendOccurrence`はsettled executionへの追記を拒否するため、
終了後hookの記録には専用の保存経路を設計する必要がある。

## 操作契約

既存の`hooks`で選ぶESM moduleへ、camelCaseの関数exportを追加する。
JS関数は既存と同じ`(input, api)`形式で、非同期処理をawaitする。
初回は一moduleの六hookを整備し、複数moduleの登録・管理CLIは増やさない。

| export | 呼出点 | 戻り値と効果 |
| --- | --- | --- |
| `runtimeStart` | capture済みmoduleの評価後、最初のモデル入力前に一回 | `{context: string[]}`を今回Workerの共通指示へ追加 |
| `beforeTurn` | taskのturn処理開始前に一回 | `{context: string[]}`を今回turnの指示へ追加 |
| `afterTurn` | 実行終了保存とrootの採用判断が確定した後に一回 | 終了通知・外部処理。checkpoint効果は実装しない |
| `runtimeStop` | 通常のWorker終了前に一回 | 後処理をawait。停止理由を渡す |
| `beforeTool` | 現行の直接tool・生成JS内toolの実行前 | 現行の返却値を引数とする契約を維持 |
| `afterTool` | 現行のtool成功後 | 現行の返却値を結果とする契約を維持 |

旧Henjiのafter_toolはエラー結果の加工も扱うが、hjhの現行afterToolとは契約が異なる。
エラー加工への拡張は今回の先行案に含めず、必要なら採否と戻り値を別途明確にする。

新hookの入力を次のデータで構成する。JSへGoのlive stateやfunctionを渡さない。

| field | 内容 |
| --- | --- |
| `runtime` | `workspaceRoot`、`agentName`、`role: root/child`、`sessionId`（Session相関）、`executionId`、`parentExecutionId?`、`spawnCallId?`、`turnNumber`、実model/provider/API/effortと認証profile参照 |
| `runtime.startedAt` | Worker開始日時のRFC3339文字列。UTC offsetを含む |
| `runtime.timeZone` | Go実行環境のtimezone表示名。設定されたTZ名、またはGoのzone名を使う。IANA名を推測しない |
| `context` | 呼出時点の`systemInstruction`、`instructionComponents`、有効`tools`、Sessionのturn範囲・件数・execution locator。履歴全文を常駐コピーしない |
| `task` | `beforeTurn`に渡す今回の依頼 |
| `outcome` / `settlement` | `afterTurn`に渡す保存済み実行結果・finalText、採用状態、terminal record ID。保存失敗なら確定不能の理由を別に示す |
| `reason` | `runtimeStop`に渡すcompleted/failed/cancelled等の停止理由 |

初回のcontext viewは上記の指示・tools・会話locatorを扱い、要約用の履歴全文APIは追加しない。
`afterTurn`には最後のprovider responseだけでなく、実行全体の確定結果を渡す。
設定JSON全体や解決済みcredentialをhookへ渡さず、credential値・Authorizationを
入力・記録へ含めない既存境界を維持する。

指示の順序は既存の組立済み指示→runtimeStart寄与→beforeTurn寄与とする。
runtimeStartの適用後のcontextをbeforeTurnへ渡す。未定義hookとundefined/nullの返却は追加寄与なし、
`{context: string[]}`は配列順で結合する。tool hookの既存戻り値契約は変更しない。
動的寄与にはhook名・実行ID・source locator・結果record IDを結び、保存contextと実provider requestで照合する。
追加分を含む指示を既存の予算projectorへ渡し、元の指示や追加文脈を勝手に切り詰めない。
寄与を次turnへ累積せず、既存の完全な会話履歴を保持する。
設定snapshotはcaptureした設定・sourceを表し、動的寄与はhook結果と実行contextとして保存する。
開始日時の標準例は、このGo由来の入力を外部JSが文脈へ返す。QuickJSのIntlやtimerは必要としない。

新四hookは未定義なら呼出しも追加文脈もない。
現在の文字列形式`hooks: "hooks.js"`と単一module指定をそのまま使用する。
保存構成のhandlersは宣言したhookの種類を表し、既定の宣言を六種類へ広げる。
module評価後に実際のfunction export一覧をhook構成の記録として保存し、未定義と未実行を区別する。
JS sourceを独自parserで推測せず、保存済みconfigurationを後から上書きしたり、
旧保存snapshotを変換したりしない。

## 責務と終了順序

- extensionsはsource捕捉と有効hookの構成情報を所有する。
- jsruntimeは同じVM owner上でhookを呼び、Promise・host処理・観測の完了を待つ。
  lifecycle呼出しをmodel toolとして公開したり、toolのbefore/afterを二重適用したりしない。
- appはroot/child共通の呼出順序、文脈の適用、子のcancel/join、保存・採用確定を所有する。
- historyは終了後hookの事実を保存する専用操作を所有する。
  通常のAppendOccurrenceの契約を一律に緩めたり、終了済みexecutionを再開したりしない。
- contextbudgetは追加文脈を含む実requestを既存adapterで計量・選択する。
  TUI/CLIへ文脈の選択責務を移さない。

通常終了は次の順序とする。

1. runtimeStart→beforeTurn→agent loopを実行する。
2. 追加指示の受付を閉じ、未適用結果を保存する。残った非同期子をcancel/joinする。
   既存Call/Hookは返る前にhost work・blocking子・未決着観測をjoin/drainするため、
   この完了を使ってVMのCloseだけを終了hook後へ移す。別のQuiesce APIは増やさない。
3. turnの結果とterminalを保存し、rootではAdoptCanonicalを完了する。
   子はnon_canonicalの確定結果を持つ。
4. journalを終了後記録の経路へ切り替え、afterTurnをawaitする。
5. afterTurnの成否にかかわらずruntimeStopをawaitし、残るhook内操作の終了記録を保存する。
6. VMをCloseしてからapp.Runを返す。Coreのrun完了、次turn開始、子のcollect完了、Core停止完了もこの後になる。

終了後hookからreadFile/writeFile/command/http等の既存host処理と内側toolを使える。
この段階では親executionの子受付は終了している。blocking run_agentも含め、
新しい子を開始する入口を同じ受付ownerへ接続し、確定した親の後に未管理の子を作らない。
終了済み子の状態・結果読取りは維持する。これはI-19の親execution単位の寿命を保つための接続変更である。

### 取消と例外

- 開始hookの例外は開始hookの失敗を保存し、モデルを呼ばずに既存の実行失敗清算へ進む。
  Workerを作成済みならruntimeStopを呼ぶ。beforeTurnへ到達した実行はafterTurnも対象とする。
- afterTurn/runtimeStopのJS例外はhookの失敗として保存する。確定済みのagent.Result、
  outcome・採用状態を変更せず、afterTurnが失敗してもruntimeStopを呼ぶ。
- hookの失敗通知を既存のnoticeへ接続し、直接CLIのstderr、Core接続CLIのstderr、
  TUIの表示履歴と復元履歴に残す。最終回答stdoutや親のモデル入力へ通知を混ぜない。
- 保存失敗は既存のexecutionControlによるstorage errorとして扱い、成功したhookと偽らない。
  確定済みterminal・canonical会話を遡って書き換えない。
  terminal保存自体に失敗した場合はafterTurnを呼ばず、確定不能の理由を報告する。
  保存には成功し採用だけが失敗した場合は、non_canonicalの結果と採用失敗をafterTurnへ渡す。
  runtimeStopはVMが使える限り呼び、terminal未確定なら通常journal、確定済みなら終了後journalで記録する。
- Workerのlifetimeをtask contextの自動取消から分離し、appが明示的にCloseする。
  provider待機中の取消ではVMが生きているため、取消結果を保存後に終了hookを呼ぶ。
- 実JS/host call中の取消では現行のWorker.Callによる停止・joinを維持する。
  その結果VMが閉じた場合は、対象の終了hookをskipped/worker_unavailableとして保存する。
  閉じたVMを別VMで再生成せず、元のcallの続きを次hookとして実行しない。
- 終了hookはtask取消済みcontextを使わず、app所有の清算contextでawaitする。
  正常な外部処理に独自timeoutを追加しない。process crashや強制process終了後のhook実行は保証しない。

### 終了後記録の保存

`history.AppendPostSettlementObservation`を専用操作として追加し、終了後hook・内側操作・失敗通知を保存する。
既存DDLを維持し、semantic_records/relations/contentsを使う。
開始・終了recordのIDと親子関係は現行ObserverのIDを使用する。
各recordはpostSettlementの識別と確定terminalへの参照を持ち、現行と同じtransactionで
ordinal、latest_ordinal、occurrence_count、relationの解決状態を更新する。
terminal_record_id、runtime_outcome_json、outcome、adoption、messages、Session revisionは変更しない。
通常のAppendOccurrenceはactive専用を維持する。

journalはliveの保存sequenceとreplay用conversation metadataを既存executiondata.Ownerで管理する。
hook成功の全入力・結果は詳細履歴に保存し、hook失敗・skippedの短いnoticeだけを通常の会話表示へ出す。
子の詳細を親の通常表示へ出さない現在の表示方針も維持する。
保存terminalはhook中にも読めるが、Coreのrun terminal通知とcollectの終了は清算完了を待つ。

## スライス

| スライス | 実装と利用者が確認する動作 | focused確認 |
| --- | --- | --- |
| S1 六hookの契約とJS呼出し | Worker内で新四hookを呼び、既存二hookと同じmodule state・host APIを使える。handlers保存、Call/Hookの既存清算とVM Closeを分離 | async hookの順序・結果・例外、内側tool、call取消時の停止・join、既存run_js/子操作が成立すること |
| S2 開始hookと文脈 | 親子共通でruntimeStart→beforeTurnを呼び、追加文脈を実モデル入力へ渡す | 保存hook寄与と実requestの一致、親子独立、次turnでの再capture、追加分を含む予算管理 |
| S3 終了後の記録 | 保存・採用確定後にhookの記録を残せる専用history操作を接続する | 確定済みoutcome・terminal・採用会話を変更せず、history/inspectから終了後記録が読めること |
| S4 終了hookと清算 | afterTurn→runtimeStop→Closeを共通終了経路へ接続する | 完了・失敗・取消の実経路、親終了で子清算を待つこと、hook失敗と実行結果の区別、Core停止完了待ち |
| S5 外部定義と通し確認 | 開始日時の外部hook例、設定・契約文書・比較メモ、compiled CLI/Core/TUIで使用する | source編集の次run反映、親子の実入力、TUI再接続と履歴復元、終了処理の保存照合 |

各スライスは実装→対応するfocused確認→限定reviewの順で安定させる。
review対象は明示した動作・保存順序・既存利用経路のregressionとし、一般的hardeningを追加しない。
途中に全体gateを繰り返さず、安定候補でcoordinatorが必要な総合race/vetを一回実行する。
実モデル確認は既存認証の無料モデルと独立Core/DBを使い、有料呼出しや利用中Coreの停止を含めない。

### 変更対象の責務

| 所有者 | 主な対象と変更 |
| --- | --- |
| contracts/extensions | 新hookの入力・結果DTO、handler一覧の捕捉とsnapshot、標準hooks.js例 |
| jsruntime | worker.go/call.go/bootstrap.jsとlifecycle専用module。Hook呼出しを同じowner queueに接続し、既存call清算を使用してVM Closeを遅らせる |
| app | execution.goを共通呼出点としてlifecycle専用moduleを追加。動的instruction、子受付閉鎖、settlement順序、journalの終了後mode |
| history | 専用post-settlement writerと対応する読戻し。既存write.goの共通部分のみ必要に応じて分離 |
| CLI/Core/TUI | 既存noticeの保存・表示、run terminalの完了待ち。独自hook実行や設定UIは追加しない |
| documents | extensions/history-payloadsの採用契約、配布手順、比較メモ、スライス結果 |

### 通し確認

1. 独立workspaceへ編集可能な六hookのESMを配置する。
   runtimeStartは開始日時、beforeTurnはファイルから読んだ識別文字列を返し、
   beforeTool/afterToolは一つのtool引数・結果を変更する。
   afterTurn/runtimeStopは同じmodule stateを使い、確定結果・停止理由をローカルfileへ書く。
2. compiled直接CLIとCore接続runで依頼し、モデルへ追加文脈と加工したtool結果が渡ることを
   実request・保存構成・hook記録・最終回答から確認する。
3. 二子を並行開始し、子ごとに独立した開始情報・文脈・終了通知が保存されることを照合する。
   親のstatus/collectと親終了時の清算も通す。
4. 同Sessionの次turn前にhookの外部sourceを編集し、新Workerで新sourceと新日時が使われることを確認する。
   前turnの動的寄与が累積しないことと、原履歴が残ることを確認する。
5. hookの終了を制御できるprovider/hostを使い、保存・採用確定後にもCoreがhook終了を待ち、
   終了hook失敗で最終回答や採用結果を失わないこと、取消時の実行またはskippedを確認する。
6. TUIで実行・detach・再接続・完了後復元し、履歴・通知・保存contextを照合する。
   使用binaryをbuildし、独立directoryに展開した配布物でも外部hookを呼べることを確認する。

各focused testは上記またはスライス表の具体的動作へ対応付ける。
既存のrun_js、hook内のblocking子、Promise.all、spawn/status/steer/collect、
予算超過の保存・CLI stream・Core追加指示を、影響するスライスで確認する。

## 対象外と次段階

要約checkpoint、複数hook moduleの登録、設定管理CLI、毎モデルrequest前やstreamのhook、
hot reload、toolエラー結果のafterTool加工は今回の対象外とする。
実行中のTUI再接続だけでhookを再実行せず、開始/停止をCoreやSessionの寿命へ変更しない。

checkpointは利用者が後日着手を指定した時点で、既存の`sessions.checkpoint_json`、
turn境界と予算projectorを確認し、全文を残したまま要約と保持turnを選択する計画を別途作る。
今回、checkpointのための先行実装や仮の保存・読取り経路は追加しない。
自動要約モデル・圧縮閾値・自動削除・schema migrationも今回の対象外とする。
