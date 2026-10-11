# HTTP API

`hjh serve`が公開し、独立`hjh-tui`と他clientが使うHTTP＋JSON＋SSE契約。
I-06で`/api/v2`へ切り替えた。v1の互換routeは設けない。
統一会話、固定cut、snapshot／upsert、限定結果読取とCore最終状態の合成は
[I-06契約](i06-conversation-contract.md)を正本とする。
[I-05結果](../results/i05-core-tui-separation.md)は分離時の履歴、
[I-06結果](../results/i06-conversation-tui.md)は今回の実装・検証状況である。
I-09の追加入力route、receipt、nextRunId/inputRevisionの設計は
[追加入力契約](i09-runtime-input-contract.md)を参照する。

I-09ではPOST `/runs/{runId}/inputs`、GET `/runs/{runId}/inputs/{inputId}`、
GET `/sessions/{sessionId}/inputs`、POST `/runs/{runId}/input-chain/cancel`、
GET `/runs/{runId}/input-chain`を公開する。初回の入力保存後202、同じIDの再送は200。
RunStateはnextRunId、inputRevision、inputAcceptanceを持ち、既存SSEとGETで配信する。
通常の `/runs/{runId}/cancel` は当該runの取消のまま維持する。

I-10はGET `/sessions/{sessionId}/context?coreId=...`、POST `/sessions/{sessionId}/recall`、
GET `/sessions/{sessionId}/recall/operations/{operationId}?coreId=...`を追加する。
DTOと消費境界は[入力context・引継ぎ契約](i10-context-recall-contract.md)を参照する。
保存済み操作結果と現在のpendingは別であり、同ID再送や過去結果の参照ではpendingを復元しない。

I-11のお気に入りは[保存・API・picker契約](i11-model-favorites-contract.md)を参照する。
catalogとは別のCore側設定で、実効config path・provider IDごとに共有する。
favorite GET／PUTはscope解決前の専用FIFOへ入り、会話実行・描画の待ちと分離する。

## 識別と公開操作

base pathは`/api/v2`、JSON field名はcamelCase、日時はRFC 3339とする。
`GET /core`は`{coreId, apiVersion, workspace}`を返す。coreIdはCore起動ごとに変わり、
runIdとの組で開始・参照の対象を区別する。API versionは`v2`。
runIdはclientがPOST前に生成するUUID、executionIdは既存履歴のIDとする。
同じCoreのrun管理が開始依頼、context、Session予約、購読、結果を所有する。

```http
POST /api/v2/runs
Content-Type: application/json

{"coreId":"CORE_ID","runId":"CLIENT_UUID","task":"input.txtを読み、結果を説明して","workspace":"/path/to/work","providerId":"openrouter-responses","modelId":"deepseek/deepseek-v4.1-flash","maxSteps":0,"fromTurn":0}
```

新しい依頼を登録したresponseはHTTP 202で、次のrun状態を返す。
準備処理は登録後にCore所有のcontextで開始する。requestの終了は取消経路にしない。

```json
{"coreId":"CORE_ID","runId":"CLIENT_UUID","seq":0,"phase":"preparing"}
```

| 操作 | request | response |
| --- | --- | --- |
| `GET /core/status` | body/query不要 | coreId/apiVersion/workspace/phase/activeRuns。phaseはrunning/stopping、各runはrunId/sessionId/executionId?/phase。準備中も含み、会話本文は読まない |
| `POST /runs` | 必須coreId/runId/task。省略可sessionId/workspace/role/providerId/modelId/effort/authProfile/maxSteps/fromTurn | run状態。新規登録202、同じIDの受理済み依頼200 |
| `GET /runs/{runId}` | queryにcoreId | run状態、判明したsessionId/executionId/actualSelection/actualRole、terminalならresult |
| `GET /runs/{runId}/events` | queryにcoreId | SSE。最初にsnapshot、その後update、終了時terminal |
| `POST /runs/{runId}/cancel` | queryにcoreId。body不要 | 202と現在状態。terminal済みなら200と結果。取消要求の受理は終了完了ではない |
| `GET /sessions`、`GET /sessions/{id}` | ID | Session一覧/単体。activeRunIdは既存Managerの予約から得る |
| `GET /sessions/{id}/active-run` | coreId | Sessionの実行中RunStateまたはnull。本文は取得しない |
| `POST /sessions/{id}/rename` | title | title/updated_atだけを更新しSessionを返す。会話revisionは不変 |
| `POST /core/shutdown` | coreId | `{ok:true}`を202で受付。join/保存完了後にHTTPとprocessを終了 |
| `GET /sessions/{id}/view` | ID | 保存済み／進行中rootと子のConversationSnapshot。response直下にscopeId/viewId/revision/throughSequence/rootCuts/order/entities |
| `GET /sessions/{id}/conversation` | fromTurn/toTurn | 採用会話。省略時0/-1、inclusive範囲は既存操作と同じ |
| `GET /sessions/{id}/executions` | ID | 親子・失敗・取消を含むexecution一覧 |
| `GET /executions/{id}` | fromOrdinal/toOrdinal | execution/children/messages/records/configuration/context。省略時0/-1 |
| `GET /executions/{id}/records/{ordinal}` | 会話detail locatorのexecutionId/ordinal | 指定した保存済みRecordだけ。別message・context・configurationを読み込まない |
| `GET /selection` | sessionId/workspace/role/providerId/modelId/effort/authProfile | 次runの実効`{role, selection}`。実行・source capture・credential取得・保存は行わない |
| `GET /roles`、`GET /providers`、`GET /models` | sessionId/workspace、modelsにはproviderIdと任意authProfile | 選択scopeのcatalog。model候補は指定可能IDの制限にしない。authProfileはpreviewと同じprofileを渡す |
| `GET /model-favorites` | sessionId/workspace/providerId | scopeId/providerId/revision/modelIds。先行favorite受付の完了後に読む。catalogやcredentialを取得しない |
| `PUT /model-favorites` | coreId/providerId/modelId/favorite bool、任意sessionId/workspace | 保存後200。GET fieldsにmodelId/favoriteを加える。falseは解除、同値再送は集合を変えない |
| `GET /auth/status` | sessionId/workspace/providerId | connected/identity/account/expiresAt/planUsageEnabled |
| `GET /auth/profiles` | scope＋SelectionRequest | profile・method・利用provider・実効targetと登録状態 |
| `GET /auth/accounts` | scope/providerId/authProfile | account一覧・選択・owner・revision・attempt概要 |
| `PUT /auth/api-key` | scope/coreId/providerId/authProfile/mutationId/value | 保存後の登録metadata。keyは返さない |
| `PUT /auth/account-selection` | scope/coreId/providerId/authProfile/accountId | 保存後のaccount一覧 |
| `POST /auth/attempts` | scope/coreId/providerId/authProfile/attemptId/accountId? | 202で認可attempt。account省略は追加slot |
| `GET/DELETE /auth/attempts/ID` | coreId | 途中結果・terminal結果の読取／確認済みterminal metadataの解放 |
| `POST /auth/attempts/ID/complete` | coreId/callbackUrl? | 202。専用callback入力またはローカルcallback待ち |
| `POST /auth/attempts/ID/cancel` | coreId | 202。exchange前は取消、exchange・保存中は現在状態 |

query結果は既存Serviceの値を、純粋な公開DTOへ対応付ける。Go実装型をそのまま公開型として
importしない。一覧はJSON配列、rolesは`{defaultRole, roles}`、inspectionは上表のobjectとする。
公開DTOにDB/VMの型を持ち込まない。field一覧は以下と
[純粋なDTO定義](../../internal/api/types.go)で確認できる。wireの値はGo SDKなしでも扱える。

| DTO | JSON fieldと意味 |
| --- | --- |
| Session | id/workspaceRoot/agentChoice/agent/createdAt/updatedAt/title/stateRevision/nextTurn/activeModel/messageCount/modelChangeCount/turnCount/privateStateFromTurn。titleはnull可、agentChoice/activeModelは保存JSONのobject |
| Execution | id/taskId/task/sessionId/sessionCorrelation/parentExecutionId/spawnCallId/turn/baseRevision/baseMessageCount/createdAt/settledAt/agentName/model/build/configurationId/maxSteps/lifecycle/outcome/adoption/terminalRecordId。settledAtはnull可、model/buildは保存JSONのobject |
| Message | executionId/ordinal/turnNumber/json。jsonは保存messageのobjectで、文字列に再encodingしない |
| Record | id/executionId/ordinal/kind/observedAt/payload/content/relations。payloadは保存JSON object、contentはbase64文字列またはnull |
| Relation | relation/targetRecordId/mandatory/resolved |
| RoleInfo | name/description/tools。toolsは名前の配列 |
| ProviderDefinition | id/displayName/api/baseURL/path/auth/defaultModel/defaultEffort/models。authはkind/profile、modelsはModelInfoの配列。transport headers/adapterは一覧DTOに含めない |
| ModelInfo | id/name、任意efforts/effortSource。出所はconfigまたはcatalog、情報なしは省略。候補一覧は明示model ID・effortを制限しない |
| SelectionPreview | role/selection。selectionはprovider/api/modelId/effort/authProfile。取得時点の値であり、次runを予約しない |
| Inspection | execution/children/messages/records/configuration/context。configuration/contextは保存JSON object、未保存contextはnull |

保存JSONの内容は[履歴payload契約](history-payloads.md)を参照する。
selection previewとrun準備は同じrole・model解決を使う。roleは明示値→保存role→defaultRole。
新Sessionは全体model→role.model→明示値、既存Sessionは保存selection→明示値で、
role変更だけでrole.modelを保存selectionへ適用しない。Core起動optionもRunRequestと同じように適用する。
previewはconfig metadataとSession metadataだけを読み、tool／hook本文や会話全体を展開しない。
runは開始時に改めて同じ規則で解決するため、preview後の設定編集を固定しない。

GET runは軽い状態と必要なterminal resultを返し、会話entityを含まない。
保存済み結果はtaskとruntime outcomeの限定読取で取得し、採用error等のCore最終状態を合成する。
GET、同ID POST、terminal cancel、SSE terminalは同じresult resolverを使う。
会話はSSE snapshotまたはSession viewから読む。terminal GETで既表示の本文を消さない。

HTTPの操作失敗は`{"error":{"code":"...","message":"..."}}`で返す。
既知の契約は404 `run_not_found`、409 `core_changed` / `run_id_conflict` /
`session_busy`。session_busyにはactiveRunIdも返す。他の既存操作errorは意味に応じて
対応付ける。新しい入力制限や拒否matrixを追加する目的ではない。
runを受理した後の作業失敗はHTTP成功と区別し、runのterminal結果へ記録する。

## 開始response喪失とSession予約

同じcoreId/runId・同じ開始内容を再送した場合は、準備中/終了後も既存runを返す。
比較はscope解決後の開始DTOの値で行い、JSON field順には依存しない。
Session優先で無効となるworkspace差分は実行内容の差分としない。
同じIDに異なる依頼を割り当てた場合はrun_id_conflictとし、既存runは変えない。
開始IDの登録と同じSessionの予約は準備goroutineを起動する前に行う。

clientはresponse喪失時、保持したcoreId/runIdでGETする。受理未確認の通信切断はrunの終了とせず、
既知IDを保持して照会へ戻る。GETがnot foundでも、同じCoreなら
同じID・同じ内容のPOSTで開始/受理確認を行える。元のPOSTが遅れて到着しても一度だけ始まる。
coreIdが変わった場合は継続の保証が切れたことを表示し、別IDで自動再送しない。
これはCore内のrun識別であり、汎用command queueやDB receipt tableは追加しない。

既存sessionIdのrootは、準備前からterminal確定までCore内で予約する。
別runの開始にはsession_busyとactiveRunIdを返す。終了後は続きのrunを受け付ける。
新規Sessionはrun管理が準備前にIDを生成・予約し、そのIDを共通Go操作の内部optionへ渡して
既存app.Run/Storeが作成する。DBへSessionが公開される時点で予約済みなので、一覧から見つけた
別clientの開始もすり抜けない。作成完了の通知で公開stateにsessionIdを載せる。
この内部optionはwireへ追加せず、既存CLIのSession生成方法は変えない。
異なるSessionまで一つの全体slotで塞がない。
Core外のCLIとの同時更新は既存のrevision競合で検出され、そのerrorをterminalへ反映する。
process間lock、queue、schedulerは今回設けない。

## 実行キャンセル・Core終了・TUI離脱

runのphaseはpreparing/running/cancelling/terminalとする。runningはexecution admissionで確定し、
準備失敗はexecutionIdなしでもterminalになる。cancelはCore所有のroot contextを取り消す。
取消要求と完了が競合しても、確定した結果を取消成功へ書き換えない。

terminalを公開するのは`Service.Run`のreturn後とする。
子Agent/Workerのjoin、execution終了保存、正常rootの会話採用、そのerrorを含む。
resultは`{ok, agentResult, error}`とし、agentResultは既存の
ok/task/outcome/stopReason/finalText/errorを保持する。errorは共通操作が返したerrorのcode/message、
okはそのerrorがなくAgentも正常完了した場合に限りtrue。準備失敗ではagentResultを省略できる。
Agentの正常結果の後に保存/採用が失敗しても、run全体を正常完了とは表示しない。

通常のTUI操作は次の順序で接続する。

- F1：明示input-chain/cancel、terminalと列の保存完了を確認し、TUIを継続。
- Esc：選択表示／補完を閉じる。Ctrl+C：draftを消去する。
- `/detach`／Ctrl+D：保存済みSession admissionを確認後、TUIの購読/通信だけをclose・join。runを継続。
- `/quit`／Ctrl+Q：Core停止を要求し、全runの取消・join・保存後にHTTPの終了を待ってTUIを終了。
- `/new`・別Sessionへの`/resume`：実行中なら明示cancelとterminal確認、その後でSessionを切り替える。
- 現在Sessionの再選択：実行を止めない。実行中Sessionの再接続はactive runをGETし、同runのSSEを購読する。
- 通信切断：runIdを保持し、状態確認/再購読へ戻る。切断だけで「取消完了」と表示しない。

取消requestが届いたか不明でrunが継続している場合、通信復旧後に同じrunへ明示cancelを再送できる。

cancel操作のcontextは購読のcontextと分ける。HTTP clientのCloseやBackend jobの
context取消を、暗黙の実行キャンセルへ変換しない。TUI離脱は購読/通信の終了を指す。
通常のdetachはrunを継続し、TUIの強制終了や購読切断だけでもCoreのrunを取り消さない。
I-07の再接続はactive run IDを先に捕捉し、保存済み会話を得てから同runのSSEへ戻る。
履歴読取中にrunが終了した場合も、そのrunの最終snapshotへ追い付く。再実行のPOSTは送らない。

Core終了は停止APIまたはserveへのSIGINT/SIGTERMで行う。新規runの受理を終え、管理runと認証操作を
取り消してjoinし、Serviceを閉じ、その後HTTP listenerを閉じ、DBを閉じてprocessを終了する。
停止APIの短い受付応答をflushしてからserveへ通知し、応答はrunのjoinや描画を待たない。
Core IDは既存のepoch確認に従う。別Coreへ置換された場合も、元のCoreは停止済みと扱う。

## SSEの状態復元と配信

frameはevent/id/dataと空行を持つ。`run.snapshot`はRunStateと`conversation` snapshot、
`run.update`はRunStateと`conversation` updateを同じfield名で返す。
`run.terminal`は最終RunState/resultだけを返し、会話全文をもう一度複製しない。

| 会話payload | field |
| --- | --- |
| snapshot | scopeId/viewId/revision/throughSequence/rootCuts/order/entities |
| update | scopeId/viewId/baseRevision/revision/throughSequence/upserts |
| entity | id/kind/executionId/parentExecutionId/parentEntityId/position/text/status/name/outcome/error/detail |
| position | rootExecutionOrdinal/firstSequence/itemIndex |

snapshotのentitiesはIDをkeyとするobject、orderはID配列、upsertsはentity配列である。
rootCutsはrootExecutionId/rootExecutionOrdinal/nullable throughSequenceとexecutionごとの
semanticOrdinal/messageOrdinalを持つ。ID・kind・localKeyの対応と旧記録のnull境界はI-06契約に従う。
tool詳細はexecution／record locatorからinspectionで読む。

run seqは実行状態、会話revisionはview内の表示更新の系列で、互いに独立している。
同じrun seqでも会話更新を捨てない。viewIdが同じupdateはbaseRevisionを現在値に対応させて適用する。
別view／再接続は新しいsnapshotから開始する。位置とIDは途中→確定で変わらない。

最初のsnapshotは短い保存cutの復元である。consumer登録とcut取得は共通ownerのgate内で行い、
逐次復元と正規化はgate外で行う。復元中の変更はconsumer側のinboxへ合流し、その後updateで反映する。
初期snapshotのRunStateがterminalでも、その会話cutを最終cutと扱わない。
必要なdurable prefixまでdrainした最終snapshotの後に`run.terminal`を送りstreamを閉じる。
既に終了したrunへの購読にも同じ境界を適用する。clientのWaitは初期terminal phaseで早期完了しない。

producerは短いtyped fact通知を登録し、HTTP write／描画／snapshot加工を待たない。
遅いconsumerはlatest entityから追い付く。全観測を再送する監査streamではなく、保存証拠はhistoryに残る。
同じview/revisionのsnapshotとserializationはconsumerの寿命中に共有する。
再接続はsnapshotから始まり、Last-Event-IDによる全frame replayやCore再起動後のrun復元は提供しない。

終了後はrun ID、fingerprint、最終seq、execution locator、軽い結果状態をCore生存中に保持する。
保存失敗／execution未生成では必要な未保存resultを保持する。
会話viewと本文は最後のconsumer終了後に解放し、次の明示読取で保存事実から復元する。
Session viewの重なるGETも復元を共有し、参照終了で解放する。固定TTL／全文cache上限は設けない。

## workspace・config・startup option

run/catalog/authは同じscope解決を使う。sessionId指定時は保存workspace、次に明示workspace、
最後にserve既定workspaceを使う。pathはCore側で解決し、TUIのcwdやfilesystemを使わない。
明示serve `--config`があればそれを使い、なければ解決workspaceの既存設定準備経路を使う。
TUIは再開Sessionまたは選択workspaceをcatalog/authへ渡す。model/authのproviderIdも明示し、
次runで選択している接続の一覧/認証を読めるようにする。

| 現行option | 分離後の所有者・対応 |
| --- | --- |
| workspace | serve既定、TUIの指定はrun/catalog/authのworkspace。再開Sessionの保存値が優先 |
| db/config/credential-file/credential-state-dir | Core起動option。I-13のTUI起動入口でも新Coreへのargvとして指定できる。TUIの画面はDB/config/credential fileを開かない |
| session/model/provider/role/effort/auth-profile | TUIの次run選択をrun requestへ対応付ける。既存のpartial selection解決を使う |
| max-steps/from-turn | serve既定と、省略可能なrun field maxSteps/fromTurn。TUI起動指定も送る |
| from-turn/to-turn、from-ordinal/to-ordinalの閲覧指定 | 該当queryの範囲。runのfromTurnとは別の操作 |
| stream | 非対話CLIに維持。TUIはSSEから本文/進捗を表示 |

maxSteps/fromTurnの省略時はserve既定、明示0はそれぞれ無制限/会話先頭の既存意味。
GoのRunRequestにも省略を表現できるfieldを設け、0を「未指定」と混同しない。
DBはCoreの一つのStoreが所有する。別workspaceを扱ってもrequestごとに別DBを暗黙に開かない。

## 認証の寿命とcredential境界

既存Serviceとcredential Managerを使い、[I-12契約](i12-authentication-contract.md)の
Core所有attempt・保存ticketへ接続する。HTTP切断は受付済み保存や認可jobを取り消さない。
callback待ちは取消可能で、有効callbackの検証後に一回だけexchangeへ進む。
Core停止開始は新受付を閉じ、OAuth待ちを取り消し、受付済み保存をjoinする。
`/auth/login`・`/auth/complete`はattempt経路へ置換した。

キーはcredential専用state、account tokenは既存形式のslotへ保存する。
APIキー・access/refresh token・Authorization・callbackURLはresponse/SSE/logへ出さず、
一覧は参照名・owner・選択・revision・保存状態だけを公開する。model catalogの配列は維持し、
捕捉認証を`Hjh-Auth-Owner-Id`／`Hjh-Auth-Account-Id` headerで返す。


## 現行Henjiからの参考と差分

対象は`/home/agent/projects/henji-harness`の現行sourceをread-onlyで確認した結果。
旧sourceは参考でありhjhの機能の正本ではない。実動作の再試験は行っていない。

| 参考source | 確認した方式とhjhでの扱い |
| --- | --- |
| [remote_session.ts](../../../henji-harness/v0/tui/remote_session.ts)、[core_service.ts](../../../henji-harness/v0/agent/host/core_service.ts) | clientがcommandIdを先に生成し、response喪失後にcommandRead。同一IDの実行を重複させない。hjhではrunIdに集約し、汎用command管理を移植しない |
| [core_service.ts](../../../henji-harness/v0/agent/host/core_service.ts)、[reducer.ts](../../../henji-harness/v0/api/reducer.ts)、[server.ts](../../../henji-harness/v0/agent/http/server.ts) | snapshot/revision境界、独立配信pump、再snapshot復元。hjhも状態復元を使うが、2MiB等のbuffer数値と全Session control modelは移植しない |
| [chatgpt_auth.ts](../../../henji-harness/v0/agent/provider/chatgpt_auth.ts) | Core所有の認証attempt。pending寿命の参考とし、hjhの既存callback listenerを維持 |
| [task_service.ts](../../../henji-harness/v0/agent/host/task_service.ts) | 準備中からslot予約、completion後にsettlement完了。hjhではSessionごとの予約とService.Run returnを基準にする |
| [HTTP運用契約](../../../henji-harness/docs/operations/http-api.md) | acceptedと完了、detach/cancel/shutdownの区別を参考にする。I-07で旧/quitのCore終了を採用し、/detachは実行継続のTUI離脱へ対応する |
| [api_worker_client.ts](../../../henji-harness/v0/agent/http/api_worker_client.ts)、[api_bootstrap.ts](../../../henji-harness/v0/agent/http/api_bootstrap.ts) | 旧HTTP APIはDeno Workerから内部RPCでCoreを呼ぶ。hjhにAPI/Data Workerを設ける根拠とはしない |

旧Core固定workspace、全体一slot、queue、multi-Core管理を今回引き継がない。
巨大なcoordinatorを写さず、scope解決・run管理・公開DTO・HTTP配信・client・TUIの責務を分ける。
