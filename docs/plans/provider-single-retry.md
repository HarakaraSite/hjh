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

## 改善メモ：追加リトライを2回へ増やす案

2026-10-15、利用者指示で記録。Session `600ce255`の子`fb589e6e`は、6回目の論理モデル要求で
tool引数JSON不正を検出し、約2秒後の自動再送でも引数JSON不正となって失敗終了した。
同Sessionでは親・他の子でも同種エラーがあり、一回の再送だけでは作業が止まる実例になった。
モデル・provider配信・hjhの断片組立てのどこに原因があるかは未確定。

検討案：

- 一つの準備済みモデル要求について、追加リトライを合計2回、初回を含め最大3試行にする。
- 最初の再送まで2秒、次の再送まで4秒待機する。待機中の取消は引き続き可能とする。
- 対象は現在の一時的通信/provider障害、不完全tool callを維持する。
  出力上限は別扱いとし、取消・認証・履歴保存失敗を対象へ追加しない。
- エラー種別ごとに回数を増やさず、同じ論理要求に共通の再送枠を使う。
  親子とも共通Agent loopに適用し、完了済みtoolや子全体をやり直さない。
- 再送を使い切った後は、現行の失敗分類・終了・recall経路へ返す。

回数を増やして実利用で改善するかは未確認。この追記は改善候補のメモであり、
現行の追加1回・固定2秒の実装や常用設定は変更していない。

## 改善メモ：モデル応答待ちのタイムアウト

後続の[対応計画](provider-response-timeout.md)に沿って実装・test・review完了。
[結果](../results/provider-response-timeout.md)を参照。以下は依頼時点の記録として保持する。

2026-10-15、利用者指示で記録。Session `64bbc534`の子`53dcbedc`は
`openai-chatgpt / gpt-6.1-sol / high`で、17:04:05 JSTに4回目のモデル要求を開始した後、
17:15 JSTの確認時点まで新しいthinking・本文・完了・エラーの記録がなかった。
親は17:04:59から`collect_agent`で待機していた。利用者はその後キャンセルして`/recall`した。
providerの生成待ちか通信の停滞かは未確定であり、記録がないことだけでSSEの受信自体が
なかったとは断定しない。

依頼時点では通常のprovider HTTP clientに要求全体のtimeoutを設定せず、Coreの親・子実行も
期限なしの取消可能contextを使っていた。timeoutエラーの分類・リトライはあったが、
応答が終了せずエラーも返らない場合に、hjh自身が待機を打ち切る処理はなかった。
以前の[SDK評価のA-2](../design/provider-sdk-evaluation.md)にも不足を記録していた。

検討方針：

- 長時間の正常な生成を一律の総時間で打ち切るのではなく、応答開始待ちと、
  ストリーム受信が途絶えた時間を対象にする。
- 親・子の共通provider経路に適用し、発動時は当該モデル要求を終了させ、
  既存のprovider timeout分類・共通リトライ枠へ接続する。完了済みtoolを再実行しない。
- 利用者によるキャンセルとは区別し、キャンセルを自動リトライへ変換しない。
- 再送を使い切った場合は既存の失敗終了・`/recall`案内へ返す。
- 待ち時間の具体値、応答開始の定義、SSEのheartbeatや本文以外のイベントを
  受信継続と扱う条件は未決。生成中の無出力期間と停滞をどう区別するかを設計時に確認する。

このメモの作成時点ではtimeoutやリトライ回数、常用設定は変更していなかった。
その後、応答開始待ち300秒／read idle600秒を既定とするtimeoutを実装した。
追加retryは引き続き1回・固定2秒であり、上記の最大3試行案は未実装。詳細は[timeoutの結果](../results/provider-response-timeout.md)を参照する。
