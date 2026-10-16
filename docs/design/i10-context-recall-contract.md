# I-10：context参照と停止executionのrecall契約

2026-10-11。[I-10計画](../plans/i10-context-recall.md)の操作・保存境界を具体化する。
当初は通常・批判的reviewと限定再reviewの修正確認を完了した実装入力として作成した。
契約reviewの[確認結果](../results/i10-plan-contract-review.md)はその時点の記録である。
その後、`/context`・`/recall`からprepare・admission・参考入力・次requestまで実装した。
I-10時点の実provider確認と留保は[実装結果](../results/i10-context-recall.md)を参照する。

2026-10-16、現行sourceに合わせてrequest記録の説明を更新した。以下の操作・消費境界は維持するが、
I-10当初のschema v3・毎request全文／精密attribution保存は後続変更で置き換えられている。
現在の保存方式は[通常履歴保存](normal-history-storage.md)、DDLは[schema契約](history-schema.md)、
公開DTOは[HTTP API](http-api.md)・[保存payload](history-payloads.md)を正本とする。
既存の[会話契約](i06-conversation-contract.md)、[入力契約](i09-runtime-input-contract.md)と併せて読む。
全体の実装／未実装一覧は[実装状況](../implementation-status.md)を参照する。

## 人間が行う操作

- `/context`：保存された直近の親model request metadata・予算summaryと、現在のrecall準備を読む。最新詳細の保存状態も表示する。実行中にも使える。
- `/recall`または`/recall latest`：同Sessionの終了済み・非採用の停止rootを選び、次の依頼へ準備する。
- `/recall ID`：同じ条件のexecutionを完全IDまたは一意なprefixで選ぶ。明示指定では子も選べる。
- `/recall clear`：次の依頼に向けた準備を解除する。過去の使用関係や元executionは変更しない。

prepare/clearはSessionがidleのときに行う。F1の取消受付だけではidleにならず、終了保存と予約列の
終了を待つ。preparing、running、終了保存中、F2のclaim・予約引継ぎ中も同じSessionの実行所有者がある。
操作できない場合は現在のactiveRunIdと理由を返す。現在runへの追加指示は既存F3を使う。

準備は同じCoreでTUIをdetachしても残る。Core再起動後は準備し直す。
準備時に全文を常駐させず、参照IDと小さいmetadataだけを保持する。
別Sessionでの操作、model/providerの選択変更はこのSessionの準備を消さない。

## 公開APIとDTO

pathは`/api/v2`以下。

| method/path | 動作 |
| --- | --- |
| GET `/sessions/{sessionId}/context?coreId=...` | 保存済みcontextのmetadataと、このCoreのpending参照を返す。 |
| POST `/sessions/{sessionId}/recall` | prepare/clear。新規保存完了は200、同ID再送も200。 |
| GET `/sessions/{sessionId}/recall/operations/{operationId}?coreId=...` | 保存済み操作結果を読む。過去Coreの結果も明示coreIdで読める。 |

POST bodyは`{coreId,operationId,action,selector?}`。actionは`prepare`または`clear`。
prepareのselector省略または`latest`は直近の停止root。clearにはselectorを付けない。
対象Sessionはpathで固定する。coreIdは既存のCore identityと同じ扱いにする。

context responseのI-10基本部分（後続のlatestBudget・snapshotSave等は[通常履歴保存](normal-history-storage.md)と[HTTP API](http-api.md)を参照）：

```text
{
  coreId, sessionId,
  canonical: {stateRevision, turnCount, messageCount, activeModel, agentName},
  activeRunId?, latestRootExecution?: {executionId, lifecycle, outcome},
  latestRequest?: {
    executionId, recordOrdinal, requestOrdinal, modelStep, selection,
    itemCount, messageCount, toolContractCount, instructionCount, wireBytes
  },
  recallRevision, pendingRecall?: {
    generation, operationId, sourceExecutionId, sourceOutcome, preparedAt, claimedByRunId?
  },
  latestRecallAdmission?: {
    sourceExecutionId, targetExecutionId, recordId, createdAt,
    referenceMessageOrdinal?, requestRecordOrdinal?
  }
}
```

latestRequestはroot executionに保存されたschemaVersion=1の小さなmodel_request metadataから読む。
I-10当初のschemaVersion=2、lane=parent、purpose=user_turnとoccurrencesを辿る方式は、
[通常履歴保存](normal-history-storage.md)への変更で置き換えた。
Session内のroot実行順で最後にrequestを保存したrootを選び、そのroot内の最後のrequestを返す。
子やrun_jsの独立model呼出しを親requestと混ぜない。providerのHTTP開始通知とappが保存する
入力構成recordも区別する。最新rootがまだrequestを持たない場合、latestRootExecutionと
latestRequestのexecutionIdが異なることを表示する。requestが一度もなければlatestRequestを省略する。
selectionはそのrequestの実使用modelであり、Session.activeModelやTUIの次回選択で上書きしない。
このrecordはprovider.Generateより前に保存される。表示は「保存された送信準備」とし、providerが
受信・受理したことまで保証しない。latestRecallAdmissionのrelationもadmissionの保存事実である。
referenceMessageOrdinalは参考messageが保存された場合、requestRecordOrdinalはそれを含む親requestが
保存された場合に付け、admissionだけで「モデルへ引継ぎ済み」と表示しない。

itemCountは準備済みのmessage数・tool定義数・instructionCount・wire相当1件の合計。
instructionCountは組立済み指示が空なら0、それ以外は1。messageCount・toolContractCount・wireBytesは
同じ準備済みrequestからmetadataへ保存した件数・WireBodyのbyte数であり、occurrencesを数え直さない。
最新wire全文はexecutionごとにServiceのsnapshot ownerへ保持し、終了確定後に
execution_latest_requestsへ保存する。予算も各回summaryと最新の完全Factを分けて扱う。
詳細取得時は同じrecord IDのsnapshotだけを対応させ、metadata保存を本文のDB保存完了と混同しない。
I-10自体はtoken予算を追加しなかったが、後続では[コンテキスト管理](i14-web-context-contract.md)が
推定値と予算判断を追加した。wireBytesだけからtoken実測値や残量を算出したものではない。
canonicalの件数・選択とlatestRequestは一つの短いSQL読取transactionで取得する。
pending/activeはCoreの短い同期でsnapshotする。保存側とCore側を単一の原子的snapshotとは表示しない。
recallRevisionはこのCore・Sessionのrecall状態の世代であり、I-09の入力revisionとは別系列。

operation response：`{receipt, managedByThisCore}`。
receiptは`{schemaVersion:1, coreId, sessionId, operationId, action, selector?, sourceExecutionId?,
generation, completedAt, result}`。resultは`prepared`、`cleared`、`already_clear`。
これは操作が完了した時点の結果であり、現在も準備があることを保証しない。
現在の準備と使用先はcontext responseを読む。過去Coreのreceiptから準備を復元・自動実行しない。

同じcoreId/operationIdで同じ型付き内容を再送したら元receiptを返し、再適用しない。
fingerprintはSession・action・selectorの省略を含む元指定を比較する。latestを再解決しない。
同じIDの異なる内容は`command_id_conflict`。I-09と同じcommand_receiptsのID空間を使い、
別operationの既存IDをrecallとして再利用しない。通信未確認時は同じIDで照会／再送する。
新しいIDへの自動再送はしない。

source選択は同Sessionのlifecycle=settled、adoption=non_canonical、
outcome=cancelled/failed/interrupted/unknownを対象にする。
latestはparent_execution_idがないrootだけをsettled_at、created_at、execution_idの降順で選ぶ。
明示prefixは対象executionの中で解決し、複数一致なら`recall_ambiguous`、一致なしなら
`recall_not_found`。同Session所属はsession_correlationで確認し、別Sessionの内容を暗黙に取り込まない。
完了した非採用childは今回の停止executionの条件には含めない。

## 準備・claim・消費の境界

所有者は既存corerun.ManagerのSession予約と同じところに置く。
recall操作の処理中登録、pending reference、通常StartRunのclaimを短いManager同期で順序付ける。
別のrun registryやI-09入力queueは作らない。

1. 新規prepare/clearを登録する際、Core・Sessionの実行予約とrecall処理中登録を確認する。
   同IDの処理中再送は同じ完了結果を待つ。異なる新規操作は同Sessionの処理中操作を追い越さない。
   その処理中登録がある間、通常run開始も同Sessionを予約しない。
   保存済み同ID再送は現在のbusy状態に関係なく元receiptを返し、pendingを操作しない。
   同IDのGETも処理中登録があればlock外でその完了を待ち、保存とpending公開の途中を完了扱いしない。
2. lock外でsourceをmetadataだけで解決し、command_receiptsへ小さい操作結果を保存する。
   保存失敗では既存pendingを変更せず、成功したふりをしない。新しいprepareは保存後に以前の準備を置換する。
3. 保存後に短いlockでpending/generationを公開し、処理中登録を解除する。
   POST成功はこの公開まで済んだことを示す。Core停止が先に受付を閉じれば新規登録しない。
   既登録操作は保存・完了をCore.Closeのjoin対象とし、Store.Closeより先に終える。
4. 通常StartRunがSessionを予約するとき、そのSessionのpendingをrunへclaimする。
   本文は移さず、source ID・準備generation・operationIdだけをrun入力へ捕捉する。
   claimedByRunIdをcontextへ表示し、claim中に別prepare/clearは行わない。
   次rootのcandidate execution IDもrun入力へ先に割り当て、app→BeginExecutionへ同じIDを渡す。
   このIDは内部照合キーであり、commit前に保存済みexecutionとして公開しない。
5. app.RunのBeginExecutionが、次rootの開始とrecall relationを同じSQL transactionで保存する。
   このcommitが消費境界である。直後の型付きadmission通知でCoreの同じgenerationのpendingを消費する。
   execution.startedの描画・SSE通知・provider応答を消費の条件にしない。
   I-10着手前のBeginExecutionはcommit後に取消可能ctxでexecutionを再読取していた。
   commit後のF1/Core停止でadmission成功を見失わないよう、実装済みのroot開始経路はreturnに必要な
   execution/recall record metadataをtransaction内で確定し、commit後に再読取せず返す。
   appはそのreturn直後に型付きadmission通知を呼び、VM初期化やprogress通知より前に消費を確定する。
   通知は{executionId,recallRecordId?,recallGeneration?}とし、commit結果とrunへ捕捉したcandidate IDを
   対応させる。照合が必要な場合も同じtarget IDを使い、Sessionの最新executionを代用しない。
6. admission以前のconfig/provider準備失敗や取消では、runのclaimを外しpendingを残す。
   admission後の投影失敗・provider失敗・取消では消費済みのままにし、relationを保存証拠として読む。
   不明な結果をpendingの無条件復元で処理しない。commit済みかはtarget execution/relationで確認する。

HTTP request切断を登録済み操作やrunの取消へ変換しない。
Manager lockをsource読取、SQL保存、投影、JSON化、provider、HTTP/SSE書込みの間保持しない。
既存Storeの1connectionをtransaction中に別Store操作へ戻って待たない。

F2はすでに消費したrecallを改めてclaimしない。通常rootで一度だけ挿入し、そのrootが成功すれば
既存の会話採用経路で後続rootへ引き継ぐ。失敗したtargetを再び参考に使いたければ利用者がprepareする。
I-09のselection固定、予約引継ぎ、F1/Core停止、未開始理由の保存を維持する。

## 参考情報の投影と入力順序

選択したsourceだけの保存済み公開情報を必要時に読み、以下のpayloadを作る。

```text
{
  schemaVersion: 1, sourceExecutionId, sessionId, turn, agentName, selection,
  lifecycle: "settled", outcome, task,
  automaticReplay: false,
  inheritedSources: [{sourceExecutionId, turn, agentName, selection, outcome, task}],
  observations: [
    {kind, sourceLocator, modelStep?, text?, call?, result?, status?}
  ]
}
```

sourceLocatorは既存ExecutionLocatorのexecutionId、messageOrdinal、recordId/ordinal、
itemIndex等で元証拠を指す。kindはsteering/assistant/assistant_partial/tool_exchange。
message・semantic recordの保存位置で時系列を揃え、受理だけで未適用のF3を適用済み指示へ偽装しない。
元taskはtaskに一回置き、同じuser messageをobservationsへ重ねない。

- assistantは公開本文を使い、providerState/replayItems等は一切payloadへ持ち込まない。
  対応するmessage/model resultがあれば同requestの本文を一回だけ使う。
  完了・未完了は保存phaseとfinishReasonから区別し、lengthで終わった本文を完了扱いしない。
  生成中断後はEndExecutionWithConversationが本文をphase=incompleteの
  semantic_records(kind=assistant_message、conversation.entityKind=assistant)へ移しているため、
  その終端recordをassistant_partialの正本として使う。settled後に消えるassistant_text_statesは読取先にしない。
  thinkingの展開は今回追加しない。
- tool_exchangeはモデルが呼んだtool callのargumentsと対応resultを使う。
  callを保存したが結果がなければstatus=incomplete、結果があればそのsuccess/error等の保存事実を示す。
  モデルの複数call、実際に開始したcall、結果まで保存したcallを区別し、未開始を完了扱いしない。
- messagesを優先し、同call/requestのmessage未保存でもsemantic recordに公開結果が保存されていれば補う。
  同じtoolのmessageとmirror recordを二重に展開しない。
- run_js内の内側host callや子Agentの全文を外側tool結果と重ねない。
  外側結果が保存済みならそれを使う。外側が未完了なら、内側callの保存済み実行状態を参照metadataで
  補い、詳細本文を一括展開しない。明示選択した子については、その子だけの公開証拠を投影する。
  内側の状態はtool_exchange.activity=[{sourceLocator,callId,parentCallId?,name,status}]として保存する。
  statusは保存済みstarted/success/error/cancelled等の事実を使い、結果のないstartedを完了へ変換しない。

source自身が過去にrecallを使っていた場合、後述のdurable recall markerで参考messageを識別する。
そのmessageを新しいtask/steeringに分類せず、内包するI-10 payloadを読み、元sourceのmetadataを
inheritedSourcesへ、公開observationsを同じ配列へ平坦に引き継ぐ。入れ子のJSON textを重ねない。
同じ元execution/locatorの証拠は一回だけ置き、元参考の位置→今回sourceの指示・応答の順序を保つ。
元taskはprimaryのtaskまたはinheritedSourcesに一回だけ置く。
この読取は選択sourceの参考messageのcontentsを使い、関連executionの全履歴を再帰的に読まない。
旧Henji serializerや他形式の互換変換は追加しない。

payloadをJSON textとして、provider非依存のuser text messageへ包む。
冒頭で「停止した過去executionの参考情報。未完了の呼び出しを実行待ちとして復元していない」ことを示す。
実tool_call/tool_result messageへ戻さない。したがって旧callの自動再実行は起こさない。
入力順序は`採用済み会話 → recall参考message → 今回task`。
参考messageはtargetのmessagesへ一度だけ保存し、成功時は既存AdoptCanonicalで会話へ含める。
TUI本文へ巨大な参考JSONを自動表示せず、source IDを持つ短いrecall使用表示を出す。
これはtool結果や公開本文そのものを縮める仕様ではない。

参考message保存は既存journal/ExecutionDataOwnerのcommit経路を使うが、通常user messageの
全文semantic mirrorをそのまま使わない。conversation.entityKind=recall、localKey=recall/{relationRecordId}
の軽いtyped factを作り、semantic recordにはmessageOrdinal、messageContentDigest、
recall={sourceExecutionId,relationRecordId,operationId,generation}と短い表示・conversation metadataだけを置く。
これは準備・解除receiptとは別の、実際に保存した参考messageのdurable markerである。
モデル入力のConversation読取は元Message JSONを使い、人間の会話walk/replayはこの軽いmarkerを使う。
Core/TUIの会話viewへ参考JSON全文を展開せず、表示・再recallの判別に同じmarkerを使う。

## 保存・attribution・費用

I-10ではschema v3のDDLを維持してrecall payloadを追加した。現在のGo DBはschema v4であり、
requestごとのcontext_item・occurrences・精密attribution archiveは廃止した。
下表は維持されるrecallの保存境界と、後続変更後のrequest記録を示す。

| 保存先 | 内容と保存境界 |
| --- | --- |
| command_receipts | core_epoch=coreId、command_id=operationId、operation=recall.prepare/recall.clear。signature_digest、result_json=receipt、target_json={sessionId}、completed_at。元本文を保存しない。 |
| semantic_records | target開始transaction内でkind=recall_relation、payload={schemaVersion:1,sourceExecutionId,targetExecutionId,coreId,operationId,generation}。元task/本文を複製しない。 |
| recall_relations | source_execution_id、target_execution_id、上記record_id。同じBeginExecution transactionで保存する。 |
| messages/contents | targetで実際に使う参考messageを既存serializerで一回保存。digest参照を共用する。通常userの全文mirrorを作らず、上記の軽いrecall markerをsemantic_recordsへ保存する。 |
| model_request | 小さなrequest metadataへ、recall参考messageを選択したrequestのrecallRelationRecordIdを付ける。I-10当初のcontext_item/sourceRelationsによる精密出典archiveは生成しない。最新wire全文はメモリ保持し、終了時にexecution_latest_requestsへ保存する。 |
| execution_contexts | externalRelationsに{kind:recall,sourceExecutionId,targetExecutionId,recordId}を含める。credentialやprivate providerStateを含めない。 |

親loopはSQLを知らず、appがroot入力を構築する。childの独立入力へ親のpendingを暗黙挿入しない。
referenced sourceの非採用状態、task、結果、履歴は変更・削除しない。
元証拠の再読取と新しく必要な投影message・provider wireの保存量を区別する。
pending、receipt、Session viewへ参考本文を重ねて常駐させず、使用後の一時投影を解放する。
context読取はSQLで必要なscalar/件数を抜き、Inspection・会話全文・wire body・configurationをGoへ展開しない。
metadataを抜くために既存JSONをSQLiteが読む費用までゼロであるとは扱わない。

## I-10当時の確認範囲と対象外

以下は2026-10-11の契約review・実装確認方針を記録する。実施結果は[I-10結果](../results/i10-context-recall.md)を参照する。

契約reviewでは、旧Henjiのprepare/admission/projectorの現行経路と対比し、Goの既存所有者で
一度だけの消費・全文保持・公開情報のprovider切替が成立することを確認する。
実装後は停止tool結果・生成途中本文・適用steeringを持つsourceから次の実provider requestまで照合する。
準備後のconfig失敗、admission後の取消、F2、同ID再送、detach、Session切替、Core再起動を
それぞれ上記の具体的な操作・寿命の確認として扱う。仮想的なpermission matrixは追加しない。
停止AをrecallしたBが失敗→Bをrecall→次request、という実経路で元証拠が一回だけ平坦に入り、
途中応答と適用指示が欠けず、会話replayには短いrecall表示だけが出ることも確認する。
単発・反復recallのDB本文/参照/request量とGC後保持heap・投影割当を分けて測定する。

I-10では自動compact/要約、token予算、ターン中の切り詰め、HTTP生本文の加工を実装対象外とした。
当時の問題は[後続課題メモ](../notes/http-response-context-overflow.md)に記録した。
その後、[コンテキスト管理](i14-web-context-contract.md)で予算と送信範囲選択、
[JSコンパクション](js-compaction.md)でopt-inの要約拡張を実装した。
これは生`http`本文の全文取得や巨大結果の問題すべてを解消したという意味ではなく、
現在の挙動・制約はそれぞれの現行契約に従う。
