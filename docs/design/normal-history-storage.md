# 通常利用の履歴保存

2026-10-14。利用者が[改訂計画](../plans/normal-history-storage.md)に沿った実装を指示した。
今回の保存契約は、旧Henji由来の詳細なcontext archive契約に優先する。
実装・検証の到達点は[結果](../results/normal-history-storage.md)を参照する。
続く[最新状態の上書き計画](../plans/history-snapshot-overwrite.md)の採用により、
schema v4ではwireの直接上書きと予算の保存粒度を本書の契約へ反映する。
この追加変更の到達点は[上書き改善の結果](../results/history-snapshot-overwrite.md)を参照する。

## 会話と送信全文の分離

会話の継続は`conversation_messages` → `messages` → `contents`を使う。
APIへ送ったJSON全文を会話の継続用データとして使わない。
会話・provider state・tool引数と結果・thinking・親子関係を保持し、
毎回の送信全文と構成要素の精密なarchiveを通常の保存から外す。
利用者の基本指示fileとAGENTS.mdの原文・出典をexecution開始時に保存するresource_revisionは維持する。
廃止する構成要素archiveは、モデル送信のたびにcontext_itemとして保存していたものを指す。

## S-1：通常のrequest記録

送信準備は`contextbudget.Projector`と`provider.Prepare`で行う。
準備できた正確なWireBodyを、API呼出し前にDBへ保存する。
各executionの最新準備済みwireを、`execution_latest_requests.wire_body`へ直接保存する。
execution IDをPKにして最新1件を上書きし、親・子は独立して保持する。
対応する論理`model_request`の位置は同表の`record_id`で示す。
通常の論理requestは`content_digest`を持たず、wireをcontentsへ保存しない。

小さな論理request payloadは次の形式とする。件数は準備済み入力の件数であり、
tool定義の本体や会話本文をpayloadへ埋めない。

```json
{
  "schemaVersion": 1,
  "requestOrdinal": 2,
  "modelStep": 2,
  "modelSelection": {"provider": "local", "api": "chat-completions", "modelId": "model", "effort": "auto"},
  "itemCount": 7,
  "messageCount": 3,
  "toolContractCount": 2,
  "instructionCount": 1,
  "wireBytes": 4096
}
```

recall参考Messageを選択したrequestは、適用した`recallRelationRecordId`も持つ。
予算判断は`context_budget`の各回summaryと、execution専用行の最新の完全なFactに分けて保存する。
論理requestへ予算recordを埋め直さず、予算recordと物理送信開始recordはWireBodyを参照しない。
live observerへは実際の送信Inputを従来どおり渡すが、物理recordの保存は時刻・ID等のみとする。

新しいrequest metadataの追加と専用wire行のUPSERTを同じtransactionで行う。
transaction失敗ではAPIへ進まず、それ以前の専用wire行を保持する。
古いwireのハッシュ照合・FK解除・全DB参照検索・本文回収は行わない。
他のrecord・Message・構成等のcontents所有は変えず、Session削除時の共通本文回収は維持する。

`execution_contexts`にはschemaVersionと通常recallの外部関係を保存し、最新request位置を重ねて保存しない。
公開`Context`は専用request行とmetadataから次のlocatorを組み立てる。
本文の所有者は専用request行であり、この公開JSONではない。

```json
{
  "schemaVersion": 1,
  "latestRequest": {"recordId": "execution:semantic:12", "recordOrdinal": 12, "requestOrdinal": 2},
  "externalRelations": []
}
```

`context_item`、occurrences、splices、revision digest、全requestのmanifest一覧は生成しない。
最新の意味は「送信準備済み」であり、HTTP成功済みを条件にしない。
HTTP失敗や取消の後も最新本文を取得でき、予算超過等で準備できなかったときは前の本文が残る。

取得には既存のexecution inspect/Record APIを使う。古い論理requestのContentは空、
最新のContentだけが正確な送信bytesである。物理recordのpayload.input全文復元は廃止する。
実行中のinspect/Recordは、record metadataと参照本文を同じSQLで取得する。
専用行のrecord_idが一致するrequestだけにContentを付ける。
metadataだけを先に読んでから別SQLで本文を読むと、次の送信による上書きと競合するためである。
`SessionContext`は小さなpayloadからmodel・step・件数・wire bytesを読み、
予算recordとrecall relation IDを合わせて`/context`を構成する。

## 予算の各回summaryと最新詳細（schema v4）

実行時のcontextbudget.Projector、除外済み状態、モデル入力の選択、通知とlive observerは変更しない。
保存は`AppendLatestBudget`へ集約し、次の2種類を同じtransactionで確定する。

- 各回のcontext_budget payload：modelStep・modelSelection・metadataError・exceeded、summary、detailAvailable:false。
- execution_latest_budgets：execution IDをPKとし、対応record IDと最新の完全なFact JSONを1件だけUPSERT。

summaryはbudgetの上限・入出力reserve・比率・設定出所、estimator、wireBytes・estimatedTokens、
任意のhistoryWireBytes・historyEstimatedTokens、selectedMessageCount・omittedCurrentMessageCount、
historyStatus・historyReasonを持つ。Sources・OmittedCurrent・OmittedCurrentSources・History.Turns全体は保存しない。
過去の除外番号やturn別の理由は残さず、実際の件数をsummaryに保持する。

Records/Recordは同じSELECTでexecution IDとrecord IDが一致する専用行のFactを捕捉する。
その最新recordだけにfactを付け、detailAvailable:trueとして返す。
過去recordはsummaryとdetailAvailable:falseを返し、存在しない詳細を空配列や0件へ偽装しない。
CLI detail・inspectはこの粒度を明示する。通常の会話表示や次turnの継続には影響しない。

SessionContextはbudget記録を持つ最新rootのmetadata・summary・完全なFactを返す。
LatestRootExecution、requestを持つ最新root、budgetを持つ最新rootを独立に選び、各記録のexecution・ordinal・stepを示す。
新rootにまだ記録がなければ、記録を持つ前のrootの情報を返す。
`/context`の選択/除外番号・historyの理由は最新Factから表示する。

予算判断とwire保存は独立して更新する。最初の判断が予算超過でもbudgetを保存でき、
次stepの予算超過なら前stepのwireと新しいbudgetが共存する。
親子は別execution IDで保持し、Session削除では両専用行をexecutionと一緒にcascade削除する。
会話継続とrecallは保存Messageを使い、過去の予算一覧や送信全文には依存しない。

予算の保持量は各回summaryと最新詳細1件になるが、最新Factの生成・全文上書き、wire全文書込みは残る。
容量と全体時間・process I/Oを分けて測定し、処理別のCPU/I/Oを測ったとは扱わない。

## S-2：ツール定義を保存する範囲

実行用Snapshotをそのまま使い、configuration保存用のJSONから`tools`定義一覧だけを除く。
`agent.tools`の選択されたツール名、system instructionと利用指示等は保持する。

runtimeStart/beforeTurn/afterTurn/runtimeStopの観測保存時は、Inputの保存用コピーから
`context.tools`を外し、`context.toolNames`に選択名を入れる。
実際のhookへ渡すcontext.toolsとlive observerのInputは変えない。
beforeTool/afterToolの入力・結果、lifecycle hookの他の入力・戻り値・実行関係も変えない。
最新API送信全文には実際に送ったツール定義を保持する。

## S-3：観測結果の本文参照

tool_resultとhost操作のeffect_observationで、top-level ResultのJSON bytesをcanonical payload生成前に
取り出して本文テーブルへ保存する。観測recordのcontent_digestが所有FKであり、
metadata payloadはresultの代わりにresultRef（同じsha256 digest）を持つ。
結果はobjectだけに限定せず、array・JSON string・number・boolean・nullもそのbytesで保持する。
ユーザーが返したResult JSONの内側は書き換えない。

例として、保存は次の形になり、元のresult bytesはrecordのFKで参照する。

```json
{
  "id": "tool-call-1",
  "kind": "model_tool",
  "name": "read_file",
  "phase": "completed",
  "resultRef": "sha256:..."
}
```

AppendOccurrenceの返却、Records/RecordとRecallEvidenceは、resultRefを元のresultへ展開する。
公開Record.Contentへ同じ結果本文を追加せず、従来の結果payloadを返す。
Recordsはmetadataと本文を同じSQL JOINで捕捉し、RecallEvidenceも別経路のJOINから取得する。
lightweightなTUI walkに必要なname・call関係・preview・成否等はmetadataとして保持する。
結果recordのFKを既存Session削除の本文収集・残存参照確認で扱い、削除後の未参照本文を回収する。

観測ResultはjournalのJSON再encode前の原bytesを保存経路へ渡す。
Resultを含む観測全体のJSONから取り直すと、HTML escape等でbytesが変わり、
正常なToolResult.Textと別本文になるためである。
汎用のOccurrenceInput.Contentで明示された添付本文は従来の用途を維持する。

## S-4：継続用tool Messageの本文参照

tool Messageのcontent配列にあるtool_resultのtextだけを、保存時にtextRefへ置き換える。
通常のassistant/user本文・tool呼出し引数・provider stateは変更しない。
例えば、結果JSON文字列は保存上次の形になる。

```json
{
  "role": "tool",
  "content": [{"kind": "tool_result", "callId": "call-1", "name": "read_file",
               "textRef": "sha256:...", "outcome": "success"}]
}
```

messages.content_digestはこの小さなMessage envelopeを参照する。
textRefが指す本文は同じexecutionのsemantic_records.content_digestで所有する。
正常な結果はS-3の観測recordが先に保存する原Result bytesと同じ本文を参照する。
error文などで同じexecutionに所有recordがない場合は、会話保存transaction内に
本文FKだけを持つ小さなtool_body recordを追加する。別executionの所有だけに依存しない。
通常のmodel_requestのwireはcontentsの所有FKを持たないため、tool本文の所有にはならない。
汎用OccurrenceInput.Contentで明示的な添付本文を持つrecordは、kindによらず永続的な所有として使える。
AppendMessageとAppendConversationMessageの両入口を同じcodecへ接続する。

表示用のtool Message semantic mirrorもtextRefとMessage/item位置を保持し、
結果をcontent配列・トップレベルtextの両方に再埋込みしない。
従来トップレベルtextを持つtool factは、そのTextをfactTextRefで保持する。
mirrorのcontent_digestはNULLとし、Message envelopeの所有はmessages、
結果本文の所有は観測recordまたはtool_bodyに分ける。tool_bodyは公開詳細で本文を二重に返さない。
liveのExecutionFactは元のTextを返す。Messages/Conversation、Records/Record、
RecallEvidenceの公開読取りで内部参照を展開し、元のMessage/Textを返す。
複数tool、run_jsの集約結果、子agent操作の結果も同じ境界で扱う。

Messages/ConversationはSQL rowsを閉じてから本文を解決する。
単一DB connectionのままrowsを開いて別読取りを行う経路を作らない。
会話本文の所有FKはwire専用行とは独立し、Session削除では既存FK収集から回収される。
保存後に停止した実行も、この所有と公開読戻しの経路を使う。

hookのruntime入力・動作の改善、圧縮、要約checkpoint、旧DBの変換は対象外。
