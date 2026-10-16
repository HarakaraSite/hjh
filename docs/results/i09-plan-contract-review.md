# I-09：計画・追加入力契約のreview

> **後続変更の案内（2026-10-16）**：以下は実装前の契約review記録。機能はその後[I-09結果](i09-runtime-input.md)のとおり実装済み。本文共有案はsteering受付の独立保存へ置換済みで、[現行入力契約](../design/i09-runtime-input-contract.md)に従う。

2026-10-11。[計画](../plans/i09-runtime-input.md)と
[契約](../design/i09-runtime-input-contract.md)を具体化し、通常・批判的review、
指摘反映、変更箇所に限定した再reviewを完了した。製品実装・実provider確認は未実施。

## 確認範囲

通常reviewは人間のF2/F3操作、HTTP/client、保存、draft、selection固定、再接続を確認した。
批判的reviewはprocessing登録からdurable受付・適用・終端まで、cancelとSQL待ちの独立、
終了保存後のSession予約引継ぎ、Core停止とstarting、SSEから次runへのattachを確認した。
各初回30分以内、再review一回15分以内の範囲で行い、reviewerは書込み・test/full gateを行っていない。

旧Henji `v0/tui/remote_session.ts`、`v0/agent/host/task_service.ts`、
`v0/agent/core/loop.ts`の現行呼出し経路と、hjhのManager、Agent loop、journal/history、
executiondata、TUI/apiclientを対比した。旧固定1件の制限はhjhへ採用していない。
`_refs/san/internal/core/run.go`の入力queueと境界取込、
`_refs/crush/internal/agent/agent.go`の受付順とqueue/cancelも設計時に参照した。

## 採用した指摘

| review / 指摘 | 契約への反映 |
| --- | --- |
| 通常P2：受付raw textと適用Message JSONが二重保存される | steering受付で既存Message JSONを保存し、適用時に同じdigestをmessages/semantic mirrorから参照する。follow-upは受付raw UTF-8を次executionのtaskに共用する。 |
| 通常P2：受付sequence cursorではaccepted→applied等の更新を読めない | receipt変更revisionとafterRevisionを定義。core_session_cursorsをreceipt transactionで増分し、読取watermarkとreceiptを同じSQL transactionで固定する。 |
| 通常P2：F3終了/F2継続の期間をclientが区別できない | RunState.inputAcceptanceで両受付可否を公開し、変更でseq/通知を更新する。GET/snapshot/resumeを同じ値に揃える。 |
| 通常P2：再起動後に旧coreIdを発見できず過去予約を読めない | Session inputsの初回coreId省略で過去Coreを含めて読み、各receiptにcoreIdを返す。以降は現在Coreのrevisionで追従する。過去状態はmanagedByThisCore=falseとして区別する。 |
| 批判的P1：次run登録後、元runのterminal SSE待ち中のF1が次runを止めない | exact-run CancelRunを維持し、明示の予約列cancelと列状態GETを追加。列ownerがprocessing/claim/starting/activeを順序付け、TUIは停止意図を引継ぎ後も保持し、列の保存・join完了を確認する。独立した通常runへ停止を広げない。 |

通常reviewの補足に従い、明示coreIdの増分readはそのCoreのwatermarkを返し、
初回全epoch readだけ現在Coreのwatermarkを返すことを明記した。
別Core IDであることだけからprocessの終了を断定せず、保存状態と現在の管理状態を分ける。

両reviewerは初回指摘の解消を確認し、限定再reviewの範囲に残る必須findingはないと判断した。
この判断は契約の整合性に関するものであり、同期・保存・実際のモデル入力・人間のPTY操作の成功を代替しない。

## 次の実装で確認すること

- historyの既存message/task helperをtransaction内で再利用し、receipt更新と本文参照を同時commitする。
- 入力ownerとAgent loopを型付き操作で接続し、最終回答直前の指示受付とmaxSteps/cancelを照合する。
- 次run登録と予約列cancelを同じ所有者で順序付け、元runの成功保存を維持する。
- HTTP/client/TUIを接続し、受付表示・draft・選択固定・detach/resumeを人間の操作で確認する。
- private Core、既存認証、実providerでモデル入力と保存された次turnを照合し、DB増分・保持heapも測る。

今回は文書変更のみ。`git diff --check`と新規文書の参照先を確認した。
製品コード、利用者の設定・DB・稼働processは変更していない。新しい有料モデル呼出しも行っていない。
