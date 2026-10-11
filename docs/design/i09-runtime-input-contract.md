# I-09：追加入力の受付・適用・予約契約

2026-10-11。[計画](../plans/i09-runtime-input.md)の契約詳細化。
利用者は計画案の後、契約を詰める段階への進行を指示した。
この文書をI-09の実装契約とする。確認範囲は[実装結果](../results/i09-runtime-input.md)を参照する。
既存の[会話契約](i06-conversation-contract.md)、[HTTP](http-api.md)、
[保存payload](history-payloads.md)を、以下の操作に限って拡張する。

## 人間に見える動作

| 操作 | 動作 |
| --- | --- |
| F3 steering | 現在の親runへの追加user指示。次の親モデル入力の境界で適用する。生成中のrequestやtoolを強制中断しない。 |
| F2 follow-up | 現在runに続けて、同じSessionで別runを開始する予約。正常終了の保存と会話採用が先。 |
| 複数入力 | Coreが受付順を割り当て、その順で取り込む／実行する。旧1件制限は採用しない。 |
| F1 | 画面のrunに属する予約列を明示cancelする。引継ぎ済みの同列のactive runも止め、未適用steeringと未開始follow-upに理由を残す。既に確定した成功をcancelへ変更しない。 |
| run失敗 | 同じ予約列の未適用steeringと未開始follow-upに理由を残す。別Sessionや独立した通常runを止めない。 |
| detach | 入力・予約はCoreで継続する。別TUIの再接続からも状態を読める。 |
| Session切替 | 送信済み入力の宛先は不変。実行を止めてから切替する既存操作は、予約の次run開始と競合する場合も現在のSessionのactive runを確認して完了させる。 |

F2受付時に次runのrole、provider、model、effort、authProfileを既存のselection resolverで
解決して固定する。maxSteps/fromTurnも省略と明示0を区別し、Core起動既定を適用した値を固定する。
後のpicker変更は後で送る依頼へ適用する。authProfileは参照名のみであり、credential値は含めない。
tool/hook/instructionとprovider定義のsourceは、そのrun開始時に現行configから捕捉する。
接続selectionの固定は、外部config全体のsnapshot固定を意味しない。

F2/F3はSessionとroot executionが保存され、runが追加入力を受け付ける期間に使う。
準備中・終了済みの画面から別のrunへ暗黙に送らない。TUIは利用できるキーを状態に合わせて示す。
本文Enter、F1、F4、Ctrl+C、Ctrl+D、Ctrl+Qの既存動作は維持する。

## 公開API

| method/path | 内容 |
| --- | --- |
| POST `/api/v2/runs/{runId}/inputs` | 型付き追加入力の受付。初回保存後202、同ID再照会相当の再送は200。 |
| GET `/api/v2/runs/{runId}/inputs/{inputId}?coreId=...` | そのCoreで受け付けた入力の最新receiptを取得。過去Coreの保存済みreceiptも読める。 |
| GET `/api/v2/sessions/{sessionId}/inputs?coreId=...&afterRevision=...` | receiptの最新状態を取得。変更revisionで追い付き、返却順は受付順。coreId省略は過去Coreを含む初回読取。本文は展開しない。 |
| POST `/api/v2/runs/{runId}/input-chain/cancel` | bodyは`{coreId}`。当該runに属する予約列の受付・未開始予約・active runを明示cancelする。 |
| GET `/api/v2/runs/{runId}/input-chain?coreId=...` | そのrunに属する列の軽い状態と停止完了を取得。会話や入力本文を含めない。 |

POST bodyは`{coreId,sessionId,inputId,kind,text,next?}`。
kindは`steering`または`follow-up`。nextはF2の`{role,providerId,modelId,effort,authProfile,maxSteps,fromTurn}`
であり、通常のStartRunRequestと同じ省略規則を持つ。F3は現在runのselectionを変更しない。
workspaceは保存Sessionの値を使う。

receiptは`{coreId,sessionId,runId,executionId,inputId,kind,sequence,revision,status,contentDigest,
acceptedAt,updatedAt,next?,startedRunId?,startedExecutionId?,appliedMessageOrdinal?,
appliedSequence?,reason?}`。nextには解決済みrole/selection/maxSteps/fromTurnを載せる。
textはreceiptへ繰り返し複製しない。本文の明示読取はGETの`includeText=true`を使い、保存contentを読む。
Session一覧のresponseは`{coreId,sessionId,revision,activeRunId?,receipts:[...]}`とする。
responseのcoreId/revisionは指定Coreの変更watermark（coreId省略の初回読取では現在Core）で、
各receiptにもその保存元coreId/revisionがある。
初回resumeはcoreIdを省略して過去receiptを発見し、以降は現在coreIdとafterRevisionで取得する。
afterRevisionは明示coreIdの系列を指す。受付sequenceと変更revisionは別であり、
既受付入力のapplied/started更新にも新revisionを付けて増分readへ返す。
watermarkとreceiptは同じSQL読取transactionで固定し、途中の更新を読んだwatermarkだけで飛ばさない。

InputChainStateは`{coreId,sessionId,anchorRunId,phase,activeRunId?,pendingWrites,revision}`。
phaseはopen/cancelling/settled。POST cancelは受付終了とcontext取消を伝えた時点の状態を返し、
join/保存は待たない。GETのsettledは同列の全run/worker joinと入力・未開始理由の保存完了を示す。
anchorRunIdはその列の最初の通常runであり、follow-upで作った各runも同じ列へ属する。
新しい通常StartRunで開始されたrunは、同Sessionでも別の列とする。

状態は次のとおり。

- steering：`accepted → applied`、または`accepted → unapplied`。
  appliedはuser messageが保存されloopの履歴へ取り込まれたことを示す。
  HTTP生成が既に実行されたことや、モデルが指示に従ったことは保証しない。
- follow-up：`queued → starting → started`、または`queued/starting → not_started`。
  startingにはCoreが予約したstartedRunId、startedには保存済みstartedExecutionIdがある。
  started後の実行結果は、そのrun/executionの通常状態から読む。
- reasonは`cancelled`、`run_failed`、`core_stopped`、`start_failed`、`max_steps`などの
  観測した未適用／未開始理由。textと旧receiptは削除しない。

同じcoreId/inputIdの同じ内容のPOSTは、保存済みreceiptを返し、二度目の適用／予約をしない。
fingerprintは操作、宛先、入力text、nextの明示値と省略を型付き値で比較するためのdigestで、
credentialを含めない。同じIDを別内容に再利用した場合だけinput_id_conflictを返す。
通信response喪失時は同じIDでGET／POSTへ戻る。receipt取得前に別IDで自動再送しない。
coreIdが変わったら旧Coreのreceiptを読むが、新Coreへ入力を自動投入しない。

受付不能なら現在のrun状態と理由を返し、draftを保持する。これをacceptedとは表示しない。
保存失敗はHTTP errorとして返し、永続化できていない入力をacceptedと偽らない。
ID不一致と操作の寿命に必要な対応以外の入力制限・permission規則は追加しない。

## 受付の保存境界と短い同期

run/Sessionに付随する入力ownerが受付順と処理中登録を所有する。
Managerの既存run map/Session予約が実行所有者であり、別の実行registryは作らない。
以下の手順を通常受付の実経路とする。

1. 短い入力owner lockで同IDの既存操作と宛先の受付状態を確認し、候補を`processing`として登録する。
   Session内の単調増加sequenceをここで割り当てる。processingは内部状態で、acceptedの公開をしない。
2. lockを離し、F2ならselection/role/起動既定を解決する。receiptと本文参照をSQL transactionで保存する。
   入力操作自身の保存はrun所有の寿命で進め、HTTP切断で一度登録した処理を消さない。
3. commit後に短いlockで候補をdurable/適用可能にする。受付された内容だけをloopへ渡す。
   前のsequenceがprocessingなら後の入力を追い越させない。
4. POSTはdurableな最新receiptを返す。cancelが既に続いた場合はunapplied/not_startedの状態も返せる。
   描画、SSE送信、provider応答、loop境界到達はHTTP responseの条件にしない。

processing登録と、cancel／受付終了の順序を入力ownerで決める。先に登録された処理は保存を完了し、
続くcancel/終了の理由も保存する。登録前に受付が閉じていれば別runへ移さず受付不能として返す。
F3の新規登録を閉じた後でも、同IDの保存済み結果は読み出せる。
保存失敗の候補は適用しない。同IDの通信再試行と永続化失敗後の扱いは分けて表示する。

Manager.muと入力ownerの短いlockを、DB、executiondata gate、journal、VM、provider、
JSON化、HTTP/SSE書込みの間保持しない。processingの保存完了を待つloop/終了処理はlock外で待つ。
cancel受付はcontext取消を先に伝え、SQLの保存完了や処理中入力のjoinを待たない。
receipt保存の直列化が必要なら入力保存専用のgateを使い、そのgateをcancel操作へ共有しない。

## steeringの取り込みと終了境界

最初の親request準備前、tool群の結果保存後、最終回答messageの保存後で未適用入力を確認する。
tool_callsと各tool_resultの間へuser messageを差し込まない。
生成中requestとtool/子Agentは既存contextのまま進む。run_js内の独立model laneも変更しない。

各指示は`{role:"user",content:{kind:"text",text},steering:true}`としてmessagesへ一度追加する。
受付時点で既存serializerによるこのMessageのJSON bytesをcontentsへ保存する。
app/journalの既存message保存経路を拡張し、適用時は受付済みcontent digestをmessages/semantic mirrorへ
参照し、receiptのapplied更新と同じSQL transactionでcommitする。user本文を再encode/再保存しない。
commitは既存executiondata.Ownerのdurable gateを使い、typed factを会話consumerへ通知する。
Agent loopはSQLを知らず、保存済み入力を取り込む型付きinterfaceを呼ぶ。
保存完了後にloop.messagesへ追加し、次requestのattributionで実際のモデル入力を記録する。

会話entityは`kind=steering`、localKey=`input/{inputId}`、
positionは適用messageのdurable sequence。accepted時刻へ本文を遡って挿入しない。
受付表示はreceiptの状態欄で行い、本文の「steering」表示と同じ文章を二重に会話へ追加しない。
正常終了したrootではsteering messageも通常の会話採用に含まれ、次turnへ引き継がれる。
cancel/失敗したrootは既存契約どおり採用せず、指示・適用事実・途中応答はexecutionの保存証拠に残す。

最終回答を保存した後、loopは入力ownerで終端判定を行う。
processingがあればlock外で保存完了を待ち、durable指示があれば取り込んで次stepを実行する。
指示もprocessingもなければ、短いlock内でsteering受付を閉じて正常終了を確定する。
この判定より前の登録を取り残さず、後の新規F3は「このrunの受付終了」としてdraftを残す。
maxSteps到達では未送信指示を勝手に無制限実行せず、未適用理由を保存する。
cancel中は新規受付を閉じ、適用中message transactionをjoinし、残るaccepted入力の理由を保存してから
既存のworker/子join・EndExecution・会話採用判定を完了する。

## follow-upとSession予約の引継ぎ

Sessionごとに未開始follow-upを受付順で所有し、通常StartRunと同じSession実行予約を使う。
F2はsteering受付終了後でも、元runの終了保存中で予約の受付が開いている間は受け付けられる。
親子executionのparent_execution_idと、同Sessionの次turnへの予約関係を混同しない。
follow-upは別root executionであり、子Agentではない。

1. 現在Service.Runのreturnまで待つ。正常result、子/worker join、終了保存、AdoptCanonicalの成功が前提。
2. 入力ownerでF2新規受付を閉じ、先に登録されたprocessingの保存完了をlock外で待つ。
   正常終了なら受付順の先頭をclaimし、異常終了/cancel/Core停止なら未開始予約の理由を保存する。
3. follow-upの次run IDを割り当て、receiptをstartingとして保存する。
   Managerの短いlockでSessionの予約先を元runから次runへ直接引き継ぐ。
   予約解除→他clientの通常run割込み→follow-up開始、という空白を作らない。
4. 元runのterminalを公開する前にnextRunIdを確定し、新runをpreparingとして登録する。
   この時点で元runの保存は済んでおり、元runのSSE購読終了は次run開始の条件にしない。
5. 通常app.Runを使って新rootを開始する。execution admissionでstartedExecutionIdを保存し、startedとする。
   準備/admission失敗はnextRunIdの通常terminal結果とnot_started理由に残す。後続未開始予約も理由を残す。
6. 残る予約は同Sessionの新しい実行所有者へ引き継ぎ、各正常完了後に同じ手順を繰り返す。

claimから登録までの間もSession予約を維持する。DB保存に失敗した場合にfollow-upを実行したふりをせず、
入力操作の保存errorをCore状態へ反映する。元runの既に保存した成功結果を予約開始失敗で書き換えない。
処理中のstarting操作とCore停止は同じ所有者で順序付ける。
停止が先なら新goroutineを起動せずnot_startedを保存し、開始登録が先なら通常のCore join対象に含める。
Core.Closeはrun goroutineだけでなく登録済み入力保存処理と予約状態の保存もjoinしてからStoreを閉じる。

RunStateに任意の`nextRunId`、`inputRevision`、`inputAcceptance:{steering,followUp}`を追加する。
inputAcceptanceは現在の受付可否を表すbooleanで、steering終端判定/F2受付終了/cancel/Core停止で
変化したら既存seqを増分して通知する。phase=runningの終了保存中でもF3不可/F2可を区別できる。
初回snapshot、GET、resumeにも同じ値を載せる。画面からの推測だけで受付可否を決めない。
nextRunIdは正常完了した元runから開始登録済みの次runへのリンクで、各runに高々一つ。
残りの予約の件数や状態はSessionのreceipt読取を使い、RunStateへ本文や全receipt配列を重ねない。
receiptの変化でinputRevisionと既存run seqを更新し、通常のsnapshot/update/terminalにも載せる。
購読側は最新receiptを取得して追い付き、全receiptの全版をSSEで逐一送らない。

TUIは元runの最終会話snapshot/terminal/Waitを確認してからnextRunIdへAttachRunする。
すでに次runがterminalでも通常のterminal復元で追い付く。
resumeは既存activeSessionRunとSession view、receiptを読み、実行中runへattachする。
pendingSessionやCore停止中は自動attachより利用者の切替/停止を優先する。
Session切替のcancel先が既にterminalで後続runが開始していた場合は、
元runの成功を書き換えず、切替前に元runの予約列cancelを要求し、列のsettledを確認する。

### F1と予約run開始の競合

既存のPOST `/runs/{runId}/cancel`は指定runだけのcancelとして維持する。
TUIのF1/実行中Session切替には明示のinput-chain/cancelを使う。
TUIが元runのterminalをまだ受信していなくても、Coreは同じ列の現在active runを取消できる。
元runがterminalならその成功を保持し、同じ列の後続だけを止める。
単にSessionの最新active runをcancelせず、元runに因果的に連なる予約列だけを対象にする。

列ownerはprocessing/queued/claim/starting/登録済みactiveを所有する。
cancelは短いlockで受付と新規開始を閉じ、active contextを取消し、
claim後・run登録前なら登録して実行せずnot_startedを保存する。
登録が先ならそのactive runを通常のcancel/join対象にする。保存完了と各joinはlock外で行う。
TUIのcancel意図は元runのWaitやnextRunIdへのattachで消さない。
停止完了を確認するまでは次runを稼働中表示へ自動attachせず、
必要な最終会話の取得と列状態の確認を行う。Session切替は列settled後に適用する。

## schema v3への対応と本文量

| 保存先 | 保存する内容 |
| --- | --- |
| contents | steeringは適用messageと共用する既存Message JSON bytes。follow-upは既存execution taskと共用するraw UTF-8 bytes。同じ入力の本文を更新ごとに追加しない。receipt.contentDigestはkindに対応する保存形式を指す。 |
| command_receipts | core_epoch=coreId、command_id=inputId。operation=steering/follow-up、signature_digest、target_json={sessionId,runId,executionId,sequence}。result_jsonはschemaVersion=1の最新receipt（textなし）。completed_atは初回受付の保存完了時刻。状態更新は同じ行を更新する。 |
| execution_control_facts | 対象rootの受付終了状態・sequence frontier・未処理件数。全receipt/全文配列を繰り返し格納しない。 |
| core_session_cursors | core_epoch=coreId、session_id=Session ID、public_revision=入力receiptの変更revision。receipt更新と同じtransactionで増分する。この未使用tableを入力変更watermarkとして使い、Session会話のstate_revisionとは区別する。 |
| follow_up_receipts | queue_id=inputId、session_correlation=Session ID、command_id=inputId、parent_execution_id=受付対象root、text_content_digest、status/reason、started_execution_id、作成/更新時刻。次run selection/roleとstartedRunIdはcommand_receipts側のreceiptへ置く。 |
| messages/semantic_records | 適用steeringのuser messageと既存typed mirror。inputId/sequence/steering=true、message ordinal、conversation metadataを保存し、cold replayでも同じentityを得る。 |

DDL、table名、既存payloadのfieldは維持する。新規payloadをこのI-09契約として記載し、
旧Data Workerの実装や旧receipt serializerとの互換変換を追加しない。
同じschemaの未使用tableを使うことと、旧receipt payloadを製品契約へ採用することは別である。
receiptのJSON object key順序を意味としない。

受付・適用・未開始理由の更新はSQL transactionで確定し、GETは一つの整合したreceiptを返す。
follow-upのreceiptとfollow_up_receiptsを同じtransactionで更新する。
次executionのtaskは受付時に保存したraw bytes/digestを再利用し、本文を別形式で保存し直さない。
各receipt transactionはcore_session_cursorsを増分し、その値をreceipt.revisionへ書く。
steeringのapplied receiptとmessage保存も同じtransactionとし、既存helperをtransaction内で再利用する。
DBの1connectionを保持したまま別Store操作へ戻らない。DDL変更やreader connection追加はこの契約の前提にしない。

Coreの重い保持は、processing text、未適用指示、未開始予約に必要な範囲に限る。
保存済みtextはlocatorで扱い、次run開始後と適用後に不要な入力本文を解放する。
完了receiptはDBから必要なときだけ読む。Session receipt一覧はtext/config/source/contextを展開しない。
既存executiondataのsequenceと入力の受付sequenceは別系列であり、両者を同一の値として扱わない。

旧CoreのreceiptはそのcoreIdで読める。再起動前のaccepted/queued/startingが処理未完了なら、
GETで`managedByThisCore=false`を添え、「過去Coreの保存状態」として表示する。
別Core IDだけから別processの生死を断定しない。保存済みstatusを自動書換え・削除せず、
新Coreへ自動実行を移さない。新Coreのactive runと過去receiptの見かけのpendingを混ぜない。

## draftと確認対象

送信操作はtext・draft revision・Session/run/input IDを捕捉する。HTTP処理中も編集できる。
受付確認時にそのrevisionのままならdraftを消す。変化していれば現在draftを消さず、
受付されたtextをreceiptから参照できる。失敗/受付終了/通信未確認ではdraftを保持する。
Session変更後のreceipt responseは元Sessionへ属し、新Sessionのdraftを操作しない。

確認はF2/F3の人間の操作→HTTP→保存→実際のmodel_request/次executionまでを照合する。
最終回答生成中の受付、tool群後の適用、保存後のfollow-up開始、複数入力の順序、
予約時selectionと後のpicker変更、F1/Session切替/Core停止、detach/resume、編集中draftの保持を対象にする。
同ID再送による二重message/次runがないことも、HTTP response喪失の実経路で確認する。
低速SSEやTUI不在時にも適用・予約開始・終了保存が進むことを確認する。
DB増分と保持heapは、同じ本文量の単発/反復入力で重複が増えないことを測る。
実providerの確認は保存済み認証を使うprivate環境で行い、利用者のDB/稼働processを変更しない。
