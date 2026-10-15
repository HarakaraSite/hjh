# Provider要求の最小リトライ

2026-10-15。利用者は「合計1回・2秒待機・出力上限は別扱い・再失敗なら/recall」を採用し、
recallがあるため最小限の実装を指示した。起点`1b74dd9`。

## 必要な動作と現行経路

一時的な通信/provider障害、および不完全tool callで、失敗したモデル要求だけを再送する。
初回を含め最大2回、待機は固定2秒。再送は同じ準備済み要求を使い、過去のtoolを再実行しない。
HTTP 429/5xx、transport/timeout、終了イベントなし、不完全tool metadata/引数JSONを対象とする。
出力上限、取消、認証、履歴保存の失敗は自動再送しない。その他の応答エラーは現行の停止とrecallを使う。
二回目の失敗は既存の終了・分類・recall経路に返す。再送を使い切ったHTTP失敗にも短い理由と`try /recall`を付ける。
取消・認証・保存失敗の通知は既存の分類を維持する。

protocol.Generateは物理要求ごとのstart/error/resultと累積本文/thinkingを観測する。
agent.Runはモデル成功後にtoolを実行する。app.journalは観測をhistoryへ保存し、
ExecutionDataOwner→conversation→Core SSE→TUIへ流す。失敗時のactive本文は終了時に確定される。
現在の本文keyはModelStep/RequestOrdinalで決まるため、再試行を区別しないと本文・thinking・表示receiptが混ざる。

## 実装の分割

### S1：同一要求の一回再送

agentに小さい共通Generate helperを置く。既存の型付きdiagnosticで対象を判断し、
固定2秒の取消可能な待機を行う。設定、Retry-After、引数修復、provider別fallbackは追加しない。
ModelStep/RequestOrdinalは論理要求として維持し、再試行だけretryAttempt=1を添えて本文keyを分ける。
通常は既存keyのまま。
protocolは終了イベントなしのparseReason、および不正toolのfinishReasonを分類へ添える。
受理条件は変更しない。明示された出力上限を再試行対象から除外する。

### S2：失敗試行の保存と表示

初回失敗の本文/thinkingは既存SaveAssistantResultでsupersededとして確定し、active stateを片付ける。
モデル用messagesへは採用しない。詳細履歴にはエラーと本文を残す。raw SSEの常時保存は追加しない。
再送はretryAttemptを含むkeyで別entityにする。成功後のmessage Factもその試行の番号を使う。
通常TUIはsupersededを除外し、既存context noticeで`RETRYING · ... · 1/1 · 2s`を表示する。
既に端末scrollbackへ確定出力した行の消去は行わず、再試行通知で区切る。新しい本文と連結しない。
CLIの進捗もstderrへ出す。子は共通の再送を使い、通常画面の既存表示範囲を維持する。
DB schema/table、送信全文の保存方針、Coreのrun APIは変えない。
recall方式は維持し、superseded記録だけを参考情報の候補から除外する。

### S3：確認

各部分でfocused test。実HTTPの不正tool→正常応答、429→正常応答、再失敗、
出力上限の不正tool、待機中取消を、変更する実経路として確認する。
appの保存/replay・canonical会話・tool回数、TUIの新旧本文とnoticeを確認する。
使い捨てworkspace/schema v4 DBと実Core/PTY TUIで停止回避と、再失敗後の引数なしrecallを確認する。
外部provider料金は必要としない。安定候補で全体Go test/staticcheckを一回行う。

## 範囲・未確認

元MiMo障害の実受信断片はないため、同じ要求を再送すれば必ず成功するという保証はしない。
常用のv3 DB/Core/distは切り替えない。commit/pushは追加指示時に行う。

## 現在地

S1〜S3実装、focused test、実Core/PTY、全体test/staticcheck、独立review・限定再review完了。
結果: [provider-single-retry.md](../results/provider-single-retry.md)。
常用DB/Core/distの切替、commit/pushは未実施。
