# 通常利用の履歴保存

2026-10-14。利用者が[改訂計画](../plans/normal-history-storage.md)に沿った実装を指示した。
今回の保存契約は、旧Henji由来の詳細なcontext archive契約に優先する。
実装・検証の到達点は[結果](../results/normal-history-storage.md)を参照する。

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
各executionで最新の論理`model_request`だけが`content_digest`を持ち、本文テーブルを参照する。
親・子それぞれで最新1件を保持する。

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
予算の選択/除外結果は`context_budget`recordに一度だけ保存する。
論理requestへ予算recordを埋め直さず、予算recordと物理送信開始recordはWireBodyを参照しない。
live observerへは実際の送信Inputを従来どおり渡すが、物理recordの保存は時刻・ID等のみとする。

新しいrequestと本文の保存、古いrequestの本文参照解除、未参照の古い本文回収、
`execution_contexts`の最新locator更新を同じtransactionで行う。
transaction失敗ではAPIへ進まず、それ以前の本文・locatorを保持する。
他のrecord・Message・構成等が使う本文は回収しない。

`execution_contexts`は、次のように最新request位置と通常recallの外部関係だけを持つ。
本文の所有者はrecordのFKであり、このmanifest JSONではない。

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
metadataだけを先に読んでから別SQLで本文を読むと、次の送信による旧本文回収と競合するためである。
`SessionContext`は小さなpayloadからmodel・step・件数・wire bytesを読み、
予算recordとrecall relation IDを合わせて`/context`を構成する。

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
再利用する所有recordからmodel_requestを除く。そのFKは次の送信で外れるため、
同bytesの最新wireがあるだけでは会話本文の永続的な所有にはならない。
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
会話本文の所有FKは最新wire置換のGCから保持され、Session削除では既存FK収集から回収される。
保存後に停止した実行も、この所有と公開読戻しの経路を使う。

hookのruntime入力・動作の改善、圧縮、要約checkpoint、旧DBの変換は対象外。
