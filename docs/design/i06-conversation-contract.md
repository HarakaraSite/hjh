# I-06：保存位置・会話ID・同期・結果取得

2026-10-11。利用者の契約確定指示に基づく[I-06計画](../plans/i06-conversation-tui.md)の実装契約。
以下は2026-10-11の設計確定・製品実装前の契約であり、その時点のI-05製品が切り替わった記録ではない。
I-06ではDDL v3を維持する方針だった。保存payloadとHTTPのI-06差分は本書を優先し、I-05の実行証拠は書き換えない。

## 現行状態への案内（2026-10-16 source照合）

I-06の会話保存・安定ID・snapshot／upsert・HTTP v2は実装済み。実装と当時の確認範囲は
[I-06結果](../results/i06-conversation-tui.md)を参照する。以下の実装予定形・確認項目は契約策定時の記録であり、現在の未実装一覧ではない。
後続で保存契約は[schema v4](history-schema.md)と[通常履歴整理](normal-history-storage.md)へ更新され、
file-backed writable Openは[WAL／FULL](../results/history-wal-first-sequence.md)を使う。
「DDL v3維持」「WALを先に決めない」はI-06時点の範囲で、現行Storeの説明ではない。
現在の画面・描画方式は[統合TUI](tui.md)と[Markdown整形](tui-rendering.md)を参照する。
過去の性能値・実provider／端末の確認記録を、現行環境で再確認したという意味にはしない。

## 操作と結果

本文→tool→子Agent→続きの本文をCoreの保存事実から会話順に読む。
生成中の本文は同じ会話entityを更新し、完了・取消・再接続でもIDと位置を変えない。
途中本文の累積版を永久保存しない。正常に保存した本文と、完了したtool結果を失わない。
描画・SSEの停止はcancel受付、別Sessionのadmission、Agentの終了保存を待たせない。

現行入口はprovider/Agent/QuickJS → app.journal → history、
historyとService.Run結果 → corerun → HTTP → apiclient → TUI。
現行journalの保存後progress、delegationの保存外terminal通知、AppendMessageの直接保存も、以下の共通経路へ接続する。

## 状態の所有とGo内部の値

root runごとにexecution data ownerを一つ置き、子executionも同じownerへ事実を渡す。
これは同一processのGo moduleであり、専用process・MessagePort・描画ACKは設けない。
historyはSQLと保存ID、execution data ownerは受付順・request/call対応・保存後の事実公開を所有する。
Core会話ownerはentityと公開revision、run管理は識別・予約・cancel・terminal結果を所有する。

内部の事実は、execution/root/parent ID、request key、call key、kind/phase、text、保存locator、
位置を持つ型付きGo値とする。通常本文をObservation.ResultのJSONに包んでからGo側で再parseしない。
JSONはSQLite、HTTP、QuickJS境界で作る。immutableな文字列・bytesの所有者を決め、
後から変更するbufferだけをその境界でcopyする。private provider stateとtool詳細は必要な保存先へ渡す。

## 四つの番号を分ける

| 番号 | 所有・意味 | 保存／公開 |
| --- | --- | --- |
| acceptedSequence | root ownerがroot/全子の事実の受付を成立させた順。request開始、本文、call、message、終了も同じ系列。 | 表示に関わる保存payloadへ位置の根拠として記録。Core再起動後の受付番号継続は提供しない。 |
| durableSequence | 正常commitした受付事実の連続prefix。 | ownerが完了barrierに使用。本文stateの置換でも、その更新の保存完了を表す。 |
| semantic ordinal | execution内の永久semantic recordの順序。 | 既存semantic_records.ordinal。本文更新ごとには増やさない。 |
| public seq / conversation revision | run状態通知／会話変更の公開番号。 | Core生存中・view生存中の系列。保存番号やSSE受信件数とは区別する。 |

受付順はroot ownerの書込みgateで確定する。壁時計、goroutineのprogress到着順、親子のordinal大小を
全体順の根拠にしない。並行した子の事実はgateに到達した順に直列化し、その順を保存する。
parent_call・child_terminalの因果関係も保持する。保存に失敗した事実をdurableへ進めない。
初期の同期方式ではgate内で次番号を予約し、commit成功時にaccepted/durableを同時に進める。
rollbackした予約番号と候補のID/位置対応は公開・採用せず、受付失敗を既存storage error経路へ返す。
次の保存操作は最後に成功した番号から続けるため、失敗した予約が完了barrierの未充足gapとして残らない。
非同期batchへ変更する際には、先行受付と失敗時sealを含む契約変更が必要である。

## 保存位置とpayload

表示に関わるsemantic payload（開始marker、確定model result/message、tool開始/結果、execution terminal）へ
次の`conversation` metadataを加える。
既存payloadの本文・provider事実・call ID等の意味は保持する。messagesのJSON形とcontentsのbytes/digestは維持する。

```json
{
  "rootExecutionId": "ROOT",
  "sequence": 17,
  "entityKind": "assistant",
  "localKey": "parent/2/answer",
  "firstSequence": 11
}
```

これはpayload内の`conversation` objectの例。sequenceはこの事実、firstSequenceはentityが最初に現れた位置。
公開位置は `(rootExecutionOrdinal, firstSequence, itemIndex)` とする。
rootExecutionOrdinalは既存execution_display_positionsのroot行から読む。
子の表示ordinalはexecutionの参照情報であり、root内会話の並び替えには使わない。
itemIndexは一つの保存事実に複数の表示要素がある場合の配列順で、単一要素は0。
Session内のroot順は保存済みdisplay ordinal、root内の順は上記metadataで再現する。

request keyは `(executionId, lane, requestOrdinal)`。現行Agentではlaneは`parent`、requestOrdinalはmodel stepと
一致するが、physical request IDは別fieldに保持する。request開始時にこの対応を固定する。
子はexecutionIdが違うため同じlane/stepでも衝突しない。JS内callのstepが未設定の場合は親call対応から導出する。

### 途中本文

1. 最初の可視本文で小さいassistant_messageの開始recordを一つ保存する。
   payloadはkind/phase=`started`、request key、conversation metadataだけで、本文全量を入れない。
   そのrecord ordinalをassistant_text_states.first_event_ordinalへ入れる。
   markerと最初のstateは同じtransactionで保存する。
2. 以後の本文は同じstate行を置換する。event_jsonは最新text、observedAt、request key、
   conversation metadataを持つ。sequenceは進め、firstSequenceとfirst_event_ordinalは変えない。
   永久semantic recordやcontentsへ累積本文を追加しない。
3. model resultの保存transactionで対応するstateを終了する。
   resultへ同じlocalKey/firstSequenceを記録し、開始marker→確定resultへ読取元を移す。
   その後のassistant message保存は同じentityへ結び、第二の本文entityを作らない。
4. streamなしのJSON応答でも、resultから同じID規則のentityを作る。
   先行markerがなければresultの位置をfirstSequenceとする。
5. cancel/failure/interruptionでは、残る各requestの最新durable stateを未完了assistant_messageへ移す。
   同じID/firstSequenceを記録し、state終了とexecution terminalを同じtransactionで保存する。
   完了result保存後・message保存前に止まった場合も、resultの確定本文は読める。

完了stepのstateはexecution終了まで残さない。永久record、確定messages、terminalの既存consumerに必要な
内容を残し、本文量が表示更新回数に比例して重複する経路を除く。
stateの全文置換には本文長相当の書込みがある。保存容量削減とWAL・総書込み量削減は別に測る。
最初は一つの論理操作を同期transactionで保存し、返却前にdurableへ進める。
timer付き非同期batchは今回の確定方式に含めない。batchを後から採用する場合は本契約の位置とbarrierを維持する。

event_jsonと未完了本文payloadの形は次とする。開始markerはtextを省略してphase=`started`を加える。
未完了へ移すときはphase=`incomplete`を加え、sequenceを移送事実の番号へ進める。

```json
{
  "kind": "assistant_message",
  "observedAt": "2026-10-11T00:00:00Z",
  "payload": {
    "text": "最新本文",
    "request": {"lane": "parent", "requestOrdinal": 2, "modelStep": 2, "physicalRequestId": "REQUEST"}
  },
  "conversation": {"rootExecutionId": "ROOT", "sequence": 17, "entityKind": "assistant", "localKey": "parent/2/answer", "firstSequence": 11}
}
```

### thinking・tool・親子の確定

公開thinkingはproviderが返したplain text/summaryを独立entityにする。
`thinkingKind`は本文を`reasoning`、要約を`summary`として保存・公開し、TUIはそれぞれ
`thinking`／`thinking summary`と表示する。同じreasoning itemに両方あれば別entityにする。
Responsesのitem keyは`<itemId>/reasoning/<contentIndex>`と`<itemId>/summary/<summaryIndex>`。
delta、done、完了itemを同じkeyへ集約し、再開しても本文と要約を混ぜない。
opaque/encrypted provider stateを本文へ変換しない。adapterの可視item keyをrequest keyへ結び、
複数itemはitemIndexで区別する。最新stateはlane=`thinking/<itemKey>`を使い、通常本文と衝突させない。
開始marker・最新state・確定/未完了への引継ぎは同じ方式で、payloadのentityKindをthinkingとする。
request keyのlaneは`parent`のまま、state行のlaneだけをthinking item用に分ける。
request完了時は可視thinkingの確定assistant_message recordとstate終了をmodel resultと同じtransactionへ含める。
公開thinkingがない応答にthinkingを生成しない。既存provider再送stateの保存は維持する。

toolのentityはcall開始位置を保持し、結果で同じentityを更新する。
providerが宣言したtool callと、その外側JS実行は同じcall IDを使う。
開始・結果の保存はAgent loopが所有し、runtimeはその代理開始・結果を重複保存しない。
外側run_jsがJS内で呼ぶtool/host、run_agentの基礎委譲は別callとしてparentEntityIdで結ぶ。
同名・同引数でも実際に複数回呼んだtoolは別ID・別行を維持する。
子executionの開始/terminalは保存事実から公開し、delegationの保存外通知だけを根拠にしない。
request、usage、source、context等は証拠/状態として保存し、通常会話本文の後ろへ雑多なprogress行として追加しない。

## 会話IDと公開entity

entity IDは `cv:` + SHA-256小文字hexとし、canonical JSONの
`[executionId, entityKind, localKey]` をdigest対象にする。幅・text・phase・revision・runIdを対象にしない。
user taskのlocalKeyは`task`、assistantは`lane/requestOrdinal/answer`、thinkingは
`lane/requestOrdinal/itemKey`、toolは実call ID、execution状態は`execution`。
hashはentity作成時に一度求め、liveと履歴は同じ関数を使う。

entityはid/kind/executionId/parentExecutionId/parentEntityId/position、text、
status（streaming/completed/incomplete）、必要なtool名・outcome・errorとdetail locatorを持つ。
assistantのrequest完了とrun全体のterminalは別状態。途中→確定、再接続、scope変更でentity IDを変えない。
tool詳細はexecution/record/callのlocatorから取得し、snapshotへ全input/resultを複製しない。
tool entityのtextは空とし、previewに短い引数の表示用情報を持つ。call開始と結果が通知合流しても
previewと最初の位置を保つ。previewは保存済みinput/resultを制限・加工しない。
詳細recordは`GET /api/v2/executions/{id}/records/{ordinal}`で指定した保存位置だけを取得する。
通常表示へ結果全文を複製しないため、TUIは先に表示したtool行を再追記せず後続本文へ進める。
表示本文のbyte上限は設けない。user taskもexecution一覧のtaskも必要な全文を読める。

新規payloadには位置metadataを保存する。既存hjh記録は同じnormalizerの既存kind読取で扱い、
execution ordinalとsemantic ordinal、最初の途中観測から位置を導出する。
既存記録に保存されていない並行親子の全体受付順を捏造しない。
同一root内ではroot、子開始位置に結び付く子という保存因果順で決定的に復元し、この既存記録の制約を表示契約に含める。
既存dataの書換えやmigration、別版readerの増設は行わない。

## 読取cutとSQLite

I-06時点ではSQLiteの一つのStore/1connectionを維持し、WAL・reader connection追加は先に決めない方針とした。
現行も1connectionだが、WALは後続で採用済み（冒頭の現行状態案内を参照）。
長い本文復元でread transaction/rowsを保持し続ける方式は採用しない。

1. 短いread transactionで対象root/子のmetadata、各executionのsemantic/message上限、
   active stateの最新値、Sessionのdisplay positionsと採用位置を取得し、cutを固定する。
   active stateは後に置換/終了するため、この時点の本文を取得する。永久fact本文全量は読まない。
2. transactionを終了する。永久record/messages/contentsは追記済みの不変値として、
   捕捉した上限までkeysetで順に取得する。active stateは捕捉済み値を使う。
   後から増えたfact、更新済みexecution状態、後から解決したrelation状態をcutへ混ぜない。
3. 各読取のrowsを閉じ、接続を返してからdecode/normalizer処理を行う。
   一度に読むbatchは内部の処理単位で、閲覧内容の上限にはしない。全本文配列と本文を含むsort配列は作らない。
4. 必要な親子・call関係はcut内の保存locatorから取得する。同じStoreへの入れ子照会をrows保持中に行わない。
   途中return/error/購読取消でもrowsと作業参照を解放する。

初期viewはcutから一度復元する。稼働rootでは書込みgate内で短いcut取得と会話consumer登録を行い、
gate外で本文を復元する。以後の保存公開はconsumerの変更entity集合へ通知し、初期復元の完了を待たない。
集合はentity ID→最新の保存済みsource/位置/状態の対応で、同じIDの途中版を置換する。
active本文はimmutableな最新文字列、確定本文・tool詳細は保存locatorを参照し、全fact payloadや全更新版をqueueに積まない。
集合と別にpendingThroughSequenceを進め、表示entityを変えない保存事実も反映済みprefixへ含める。
これがcut登録後の更新の取りこぼしを防ぐ境界となる。

会話ownerの単一drain処理は、初期snapshotをcutのrevisionで公開し、その後に変更集合を取得して反映する。
取得は短いlock内で集合と境界を交換し、以後の変更を次の集合へ回す。
同じentityの開始→結果が合流しても、最新sourceは最初の位置・parent・確定状態を持ち、同じentityを再現できる。
初期snapshot→それ以後のupsertの順を購読へ提供し、HTTPが止まっても内部drainは進む。
このdrainはviewの生成・更新が必要な期間だけ独立goroutineで実行する。
terminalの公開はdrainを待たず、最終会話snapshotを要求するHTTP操作だけがterminalの保存cutまでの反映を待つ。

metadata照会は必要な列とSQL COUNTを使い、source/context/tool本文を展開しない。
人間向けexecution一覧のtask、明示inspectの全文、Agentのcontext取得は各consumerの必要な内容を保持する。
今回の表示復元改善のためにモデルへ渡す履歴を切り詰めない。

## lock・完了・解放

- manager mutexはrun ID、Session予約、phase、cancel関数、locatorの交換だけに使う。
  SQL、request hash、本文展開、会話normalizer、JSON/SSE writeをこのlock内で行わない。
- rootの書込みgateは受付順→保存→会話への型付き事実通知を直列化する。
  永久factとstateをcommitした後にdurableを進め、会話の変更集合へ渡す。failed commitの本文を保存済みと表示しない。
  通知は最新sourceの置換と1個のnon-blocking wake-upで、会話drain・初期復元・HTTP writeを待たない。
  初回復元以外のnormalizerは対象entityと対応索引だけを更新し、全履歴を再生しない。
- cancel受付はmanagerからcontextを取消してphaseを更新する。root gate/DB/durable待ちを行わない。
  取消後の終了処理は未完了本文とtool結果の保存、子/QuickJS joinを待つ。
- root terminalはService.Run return後に確定する。必要な保存prefix、全子join、終了保存、
  正常rootの会話採用とそのerrorを含む。SSE/表示ACKは完了条件にしない。
- viewは稼働rootまたは会話consumerが使う間保持する。terminalだけを要求するconsumerは会話viewを作らない。
  view consumerとrootの終了後にentity/対応索引/serializationを解放する。
  完了viewの同時復元はownerが一つの作業へまとめ、同じ保持期間の読取は同じviewを使う。
- 購読はrunの重いownerへの参照を持ち続けず、独立した購読handleでCloseできる。
  Core終了はrun/authのcancelとjoinを行い、購読contextを取消し、listener側で接続を閉じてSSE writeを終了させる。
  DBを閉じるのは管理下の読取/保存操作を終えてから。表示の完了通知は待たない。

## 最終結果の参照

run indexはCore生存中、coreId/runId、request fingerprint、最後のseq、Session/execution ID、
軽いphase/selection/roleと最終状態を保持する。TTL/固定cache上限を追加しない。
開始DTOのTask全文やFinalTextは成功保存後にexecution locatorへ移し、常時二重保持しない。

fingerprintはscope解決後の開始DTOをcanonical JSON化してSHA-256で求め、manager lockの外で計算する。
field順に依存しない。nilと明示0は現行契約どおり区別し、Sessionによって解決したworkspaceを使う。
同じcoreId/runId/fingerprintなら既存run、異なれば既存run_id_conflict。
この処理は認証や追加拒否条件ではなく、現行同ID再送の一致判定である。

最終RunResultは二つの部品から組み立てる。

| 部品 | 参照先 |
| --- | --- |
| Agentのtask/outcome/stopReason/finalText/error | 正常に終了保存したexecutionのtask contentとruntime_outcome_jsonから限定読取。全Inspectや会話復元を呼ばない。 |
| run全体のok、error code/message、Agent結果の最終scalar修正 | Service.Run return時のCore terminal summary。保存後の採用失敗等も含む。 |

EndExecution時点のDB結果が成功でも、その後のAdoptCanonical失敗ならrunは失敗として返す。
Core summaryが持つAgent.OK/Errorの最終値も適用し、保存済み結果だけで上書きしない。
execution未生成、終了保存失敗、保存から結果を復元できない場合は必要な最終RunResultをCoreに保持する。
保存成功を確認する前に本文を解放しない。終了結果の読取errorを正常なrun結果や空本文へ変換しない。

GET run、同ID POST再送、terminal済みcancel応答、SSE snapshot/terminalは同じresult resolverを使う。
resolverのSQLはmanager lock外。terminal済みcancelの結果読取は新しい取消や実行を開始しない。
実行中cancel応答とadmissionは本文/resultの限定読取を待たない。
取得した全文は応答作成期間だけ参照し、同時要求は同じ復元作業を共用する。
Core再起動後のrun ID照会は現行どおり保証せず、保存Session/executionから履歴を読める。

## HTTPとSSEのI-06切替

会話順と更新契約を変えるため、実装時にAPI prefixを`/api/v2`、Core.apiVersionを`v2`へ切り替える。
同梱client/TUIも同じ変更で切り替える。v1とのdual routeや旧表示配列の二重配信は作らない。
provider/auth/Session/inspectの操作の意味は維持する。

RunStateのtexts/operations表示配列を統一会話へ置き換える。
GET run・POST/cancelの応答は軽いrun状態と必要なresultを返す。表示はSSEから取得する。
Sessionの既存conversation endpointは採用messageの参照として残し、
`GET /sessions/{id}/view`を人間向け会話snapshotの入口にする。
SSEの会話scopeはそのrunのrootと全子、Session viewは保存済みroot群。entity IDと位置は両scopeで共通。
TUIは既存Session viewと新しいrun scopeをIDで合成し、同じentityを重ねない。

snapshotはrun状態と`conversation:{scopeId,viewId,revision,throughSequence,order,entities}`を持つ。
orderはentity ID配列、entitiesはIDから公開entityへの対応。viewIdはmaterialize期間のUUID。
解放後の再復元では新viewId、同じ保存entity IDを使う。I-06のview更新はupsert/position更新で表し、
Session/scope切替はsnapshotの置換として扱う。
root scopeのthroughSequenceはviewへ反映済みの保存prefix。
Session scopeはroot IDごとのthroughSequenceをcutとして返す。
初期snapshotは捕捉したcut、最終snapshotはterminal確定時のdurableSequenceまでの反映を要求する。
保存番号metadataのない既存rootはthroughSequenceをnullとし、捕捉したsemantic/message上限をcutへ返す。
再実行しない既存rootの固定snapshotに新しい受付番号を付けない。

run.updateはrun状態と`conversation:{scopeId,viewId,baseRevision,revision,throughSequence,upserts}`を持つ。
upsertは最新のentity全体で、本文deltaのネットワーク再送や古い版の履歴logを要求しない。
clientは同じviewId/baseRevisionの更新を適用してposition順にする。
初回/再接続はsnapshot。異なるviewIdやbaseRevisionの欠落ではsnapshotを取り直し、到着順で並べ替えない。
seqはrun状態、revisionは会話変更なので、phaseだけの更新ではconversation変更を省略できる。
clientとTUIはrun状態のseq判定と会話のviewId/baseRevision判定を分ける。
同じrun seqでもconversation revisionが進んだ更新は適用する。
現行apiclientとTUIの同seqによるrun.update破棄を、会話更新へそのまま適用しない。

各購読は最後に取得したrevisionと未配信の変更entity ID集合を持つ。
HTTP write中の更新は同じIDを合流し、古い本文をqueueへ積まない。
次の取得でそのIDの最新immutable値とrevisionをownerから取り出し、取得境界以降の変更は次の集合へ回す。
書込み中は一つのframeだけを保持し、失敗時は購読を閉じて再接続snapshotで復元する。
JSON化はmanager lock外。同じview/revision/変更集合のframeは購読中に共有でき、
frame consumer終了後にbytesを解放する。会話consumer不在時は配信JSONを生成しない。
tool/controlの保存をID集合の合流によって消さない。遅いclientも最新tool状態と完了本文へ追い付く。
全端末へ同じ時点のframeを書き込むことや、過去の表示版の完全replayは要求しない。

終了時は最終会話snapshot→同じ最終RunResultのrun.terminalを配信する。
初期snapshotのrun phaseがterminalでも、捕捉した初期cutが最終保存位置へ到達したとは限らない。
会話を購読するclientのWait完了・TUIの終了表示は、最終snapshotを適用した後のrun.terminalを境界にする。
初期snapshotのterminal phase/resultだけでは、会話の反映完了として購読を終了しない。
GET等による結果取得はCoreの実行完了を表し、会話snapshotの反映完了とは分ける。
購読者の遅さはこのHTTP handlerだけの待ちであり、Coreのterminal確定を遅らせない。
TUIは最終回答を本文entityとして描画し、usageやmodel stepを回答の後ろに追記しない。
非対話`--stream`は既存の本文/進捗操作を維持し、型付き事実からCLI出力へ対応付ける。

## 実装時の確認

同じ最終本文で更新回数を変え、marker/state/resultと保存byte量を比較する。
本文→tool→次本文取消、streamなし応答、公開thinking、並行した子の順序とlive/replay ID一致を確認する。
保存cut後に新しい更新を進めてもそのsnapshotへ混ざらず、次の更新で届くことを確認する。
run seqを固定してconversation revisionだけを進め、apiclientとTUIの双方が会話更新を適用することを確認する。
初期cut取得後・復元中にrunを終了させ、初期snapshotがterminal phaseでも購読を終了せず、
最終durableSequenceまで反映したsnapshotの適用後にrun.terminalで完了することを確認する。
同ID再送、GET、terminal cancel、SSEの結果が一致し、採用失敗の最終errorも保たれることを確認する。
同一Session継続、変更なし再参照、切断/再接続で重いownerと購読参照の寿命、復元/JSON化回数を測る。
DB/WAL量、累計割当、GC後heap、RSS、初期復元中の制御受付を別に記録する。
描画/SSE停止実験を拡張後の経路で再確認し、スパイクの小さいprojectionの結果を代替にしない。

tmux幅縮小時のlive本文重複は描画スパイクに残る。writer/rendererの選択はこのデータ契約と独立に検証する。
本書の確定は実装・性能改善・人間によるTUI受入の完了を意味しない。
前段の計画レビューとは別に、本書も通常レビュー・批判的レビューを実施し、Blocker/P1/P2の指摘はなかった。
両レビューの任意補足として、独立したseq/revisionの適用判定と初期/最終cutの区別を明記した。
補足後の文書整合はownerが確認した。補足に対する独立した再レビューは行っていない。
