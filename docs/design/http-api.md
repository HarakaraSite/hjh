# I-05 HTTP API

2026-10-10。`hjh serve`が公開し、`hjh-tui`と独立clientが利用する契約。
利用者が確定した方式はHTTP＋JSON＋SSE。
[I-05計画](../plans/i05-core-tui-separation.md)のreviewと現行Henji調査を反映して実装した。
実利用確認と通常・批判的実装reviewは完了した。[結果](../results/i05-core-tui-separation.md)を参照する。
保存schema、provider契約、Agent loopを変更しない。

## 識別と公開操作

base pathは`/api/v1`、JSON field名はcamelCase、日時はRFC 3339とする。
`GET /core`は`{coreId, apiVersion, workspace}`を返す。coreIdはCore起動ごとに変わり、
runIdとの組で開始・参照の対象を区別する。API versionは`v1`。
runIdはclientがPOST前に生成するUUID、executionIdは既存履歴のIDとする。
同じCoreのrun管理が開始依頼、context、Session予約、購読、結果を所有する。

```http
POST /api/v1/runs
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
| `POST /runs` | 必須coreId/runId/task。省略可sessionId/workspace/role/providerId/modelId/effort/authProfile/maxSteps/fromTurn | run状態。新規登録202、同じIDの受理済み依頼200 |
| `GET /runs/{runId}` | queryにcoreId | run状態、判明したsessionId/executionId/actualSelection/actualRole、terminalならresult |
| `GET /runs/{runId}/events` | queryにcoreId | SSE。最初にsnapshot、その後update、終了時terminal |
| `POST /runs/{runId}/cancel` | queryにcoreId。body不要 | 202と現在状態。terminal済みなら200と結果。取消要求の受理は終了完了ではない |
| `GET /sessions`、`GET /sessions/{id}` | ID | Session一覧/単体 |
| `GET /sessions/{id}/conversation` | fromTurn/toTurn | 採用会話。省略時0/-1、inclusive範囲は既存操作と同じ |
| `GET /sessions/{id}/executions` | ID | 親子・失敗・取消を含むexecution一覧 |
| `GET /executions/{id}` | fromOrdinal/toOrdinal | execution/children/messages/records/configuration/context。省略時0/-1 |
| `GET /roles`、`GET /providers`、`GET /models` | sessionId/workspace、modelsにはproviderId | 選択scopeのcatalog。model候補は指定可能IDの制限にしない |
| `GET /auth/status` | sessionId/workspace/providerId | connected/identity/account/expiresAt/planUsageEnabled |
| `POST /auth/login` | sessionId/workspace/providerId | authorizationUrl/redirectUriと解決済みworkspace/providerId/authProfile |
| `POST /auth/complete` | loginで返ったworkspace/providerId、任意callbackUrl | 認証状態。callbackUrl省略なら既存listenerの自動callbackを待つ |

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
| ProviderDefinition | id/displayName/api/baseURL/path/auth/defaultModel/defaultEffort/models。authはkind/profile、modelsはid/nameの配列。transport headers/adapterは一覧DTOに含めない |
| ModelInfo | id/name。候補一覧は明示model IDを制限しない |
| Inspection | execution/children/messages/records/configuration/context。configuration/contextは保存JSON object、未保存contextはnull |

保存JSONの内容は[履歴payload契約](history-payloads.md)を参照する。
GET runのterminal状態は保持したmetadata/resultを返し、表示配列は空になる。
完了済みの全文・操作表示はSSE snapshotから復元する。terminal GETで既表示の本文を消さない。

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

- Esc：明示cancel、terminal確認、TUIを継続。
- Ctrl+C・`/quit`：明示cancel、terminal確認、TUIの購読/通信をjoinして終了。
- `/new`・`/resume`：実行中なら明示cancelとterminal確認、その後でSessionを切り替える。
- 通信切断：runIdを保持し、状態確認/再購読へ戻る。切断だけで「取消完了」と表示しない。

取消requestが届いたか不明でrunが継続している場合、通信復旧後に同じrunへ明示cancelを再送できる。

cancel操作のcontextは購読のcontextと分ける。HTTP clientのCloseやBackend jobの
context取消を、暗黙の実行キャンセルへ変換しない。TUI離脱は購読/通信の終了を指す。
通常終了では上記のcancelも行うが、TUIの強制終了や購読切断だけでCoreのrunを取り消さない。
専用`/detach`やCore終了のTUIコマンドは今回の追加対象にしない。

Core終了はserveへのSIGINT/SIGTERMで行う。新規runの受理を終え、管理runと認証操作を
取り消してjoinし、Service、DBの順に閉じる。通常TUI終了はこのCore終了を行わない。

## SSEの状態復元と配信

SSEの`Content-Type`は`text/event-stream`、各frameはevent/id/dataと空行を持つ。
cursorはCoreのrun管理が観測を表示状態へ反映するたびに進めるseq。
Observation.IDはoperationのstart/endで共用され、親子の保存ordinalも別系列なのでcursorに使わない。

```text
event: run.snapshot
id: CORE_ID/CLIENT_UUID/12
data: {"coreId":"CORE_ID","runId":"CLIENT_UUID","seq":12,"phase":"running","sessionId":"SESSION_ID","executionId":"ROOT_ID","texts":[{"executionId":"ROOT_ID","modelStep":1,"text":"途中の本文"}],"operations":[]}

```

snapshotはrun状態、actualSelection/actualRole、親子executionの関係と状態、
executionId/modelStepごとの累積texts、operation IDごとの最新表示状態、terminal結果を含む。
operationsは表示に使うid/executionId/parentExecutionId/kind/name/phase/time/modelStep/errorを持つ。
toolの詳細input/resultと全観測payloadの参照先はinspectとし、表示snapshotへ複製しない。
admissionの実selection/roleは専用fieldへ対応付ける。

snapshot取得と購読登録のseq境界を一つのrun管理操作で確定し、その後に通知された更新を読む。
`run.update`はseqと変更された状態のupsertを送る。seqは単調増加だが連番配信を保証しない。
textは同じexecutionId/modelStepの累積値を置き換え、operationはexecutionId/idで置き換える。
clientは同じcoreId/runIdの古いseqを再適用せず、snapshotで表示状態を置き換える。
親子の本文を同じentryへ追加せず、再接続時も既受信本文を二重追加しない。

observerはrun表示状態を更新して購読へ通知し、HTTP writeや受信者のpullを待たない。
購読側は独立した配信処理で最新状態を読み取る。配信中に進んだ更新はまとめて通知でき、
購読ごとに全eventの送信待ちqueueを積み続けない。追い付きは新しいsnapshotでも行える。
seqが飛ぶことは本文の欠落を意味せず、各累積値/状態から復元する。
全start/end recordを順番どおり再送する監査streamではない。保存証拠はhistoryから読める。
この意味をTUI側の表示adapterにも適用し、受信したevent数を履歴件数と扱わない。

終了時は最終表示状態のsnapshotを配信してから、terminal状態とresultを含む
`run.terminal`を送りstreamを閉じる。既に終了したrunを購読した場合も同じ順序とする。
再接続は常にsnapshotから始める。Last-Event-IDによる全frame replayや再起動後の実行復元は提供しない。

開始DTO、最後のseq、Session/execution ID、terminal結果はCoreの生存中保持する。
完了後はlive表示projectionを解放し、必要なsnapshotを既存historyと保持した結果から作る。
準備失敗のように保存executionがないrunも、結果参照を失わない。
固定TTLや旧Henjiのbuffer数値は今回指定しない。APIのための履歴payloadの別永続化もしない。

## workspace・config・startup option

run/catalog/authは同じscope解決を使う。sessionId指定時は保存workspace、次に明示workspace、
最後にserve既定workspaceを使う。pathはCore側で解決し、TUIのcwdやfilesystemを使わない。
明示serve `--config`があればそれを使い、なければ解決workspaceの既存設定準備経路を使う。
TUIは再開Sessionまたは選択workspaceをcatalog/authへ渡す。model/authのproviderIdも明示し、
次runで選択している接続の一覧/認証を読めるようにする。

| 現行option | 分離後の所有者・対応 |
| --- | --- |
| workspace | serve既定、TUIの指定はrun/catalog/authのworkspace。再開Sessionの保存値が優先 |
| db/config/credential-file | Core起動option。TUIはDB/config/credential fileを開かない |
| session/model/provider/role/effort/auth-profile | TUIの次run選択をrun requestへ対応付ける。既存のpartial selection解決を使う |
| max-steps/from-turn | serve既定と、省略可能なrun field maxSteps/fromTurn。TUI起動指定も送る |
| from-turn/to-turn、from-ordinal/to-ordinalの閲覧指定 | 該当queryの範囲。runのfromTurnとは別の操作 |
| stream | 非対話CLIに維持。TUIはSSEから本文/進捗を表示 |

maxSteps/fromTurnの省略時はserve既定、明示0はそれぞれ無制限/会話先頭の既存意味。
GoのRunRequestにも省略を表現できるfieldを設け、0を「未指定」と混同しない。
DBはCoreの一つのStoreが所有する。別workspaceを扱ってもrequestごとに別DBを暗黙に開かない。

## 認証の寿命とcredential境界

既存Serviceとcredential Managerを使う。BeginLoginへ渡すcontextはCore所有とし、
開始HTTP response終了後もpending loginとcallback listenerを維持する。
completeもCore所有の操作として追跡し、HTTP切断だけでtoken交換を取り消さない。
Core終了では操作contextの取消とjoin、pending listenerのclose/joinを済ませる。
既存OAuth登録・clock対応・refresh・保存経路を別実装へ置き換えない。

clientはloginで返ったscope/providerをcompleteにも使う。ブラウザへauthorizationUrlを表示し、
callbackUrlを送る場合は認証入力として扱う。認証入力を進捗/logへ複製しない。
APIキー、access/refresh token、Authorizationはresponse/SSE/logへ出さず、
authProfile等の参照名と既存の非secretな認証状態だけを公開する。
今回一般的hardeningや認証方式の追加は行わない。

## 現行Henjiからの参考と差分

対象は`/home/agent/projects/henji-harness`の現行sourceをread-onlyで確認した結果。
旧sourceは参考でありhjhの機能の正本ではない。実動作の再試験は行っていない。

| 参考source | 確認した方式とhjhでの扱い |
| --- | --- |
| [remote_session.ts](../../../henji-harness/v0/tui/remote_session.ts)、[core_service.ts](../../../henji-harness/v0/agent/host/core_service.ts) | clientがcommandIdを先に生成し、response喪失後にcommandRead。同一IDの実行を重複させない。hjhではrunIdに集約し、汎用command管理を移植しない |
| [core_service.ts](../../../henji-harness/v0/agent/host/core_service.ts)、[reducer.ts](../../../henji-harness/v0/api/reducer.ts)、[server.ts](../../../henji-harness/v0/agent/http/server.ts) | snapshot/revision境界、独立配信pump、再snapshot復元。hjhも状態復元を使うが、2MiB等のbuffer数値と全Session control modelは移植しない |
| [chatgpt_auth.ts](../../../henji-harness/v0/agent/provider/chatgpt_auth.ts) | Core所有の認証attempt。pending寿命の参考とし、hjhの既存callback listenerを維持 |
| [task_service.ts](../../../henji-harness/v0/agent/host/task_service.ts) | 準備中からslot予約、completion後にsettlement完了。hjhではSessionごとの予約とService.Run returnを基準にする |
| [HTTP運用契約](../../../henji-harness/docs/operations/http-api.md) | acceptedと完了、detach/cancel/shutdownの区別を参考にする。旧/quitはCore終了だがhjhの通常quitは実行キャンセル後のTUI終了を維持 |
| [api_worker_client.ts](../../../henji-harness/v0/agent/http/api_worker_client.ts)、[api_bootstrap.ts](../../../henji-harness/v0/agent/http/api_bootstrap.ts) | 旧HTTP APIはDeno Workerから内部RPCでCoreを呼ぶ。hjhにAPI/Data Workerを設ける根拠とはしない |

旧Core固定workspace、全体一slot、queue、multi-Core管理を今回引き継がない。
巨大なcoordinatorを写さず、scope解決・run管理・公開DTO・HTTP配信・client・TUIの責務を分ける。
