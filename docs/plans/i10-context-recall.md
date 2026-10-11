# I-10：入力contextの確認と停止した実行の引継ぎ

I-09の実装確認後、利用者が次の段階へ進むよう指示した。
[I-06の優先順位](i06-conversation-tui.md)に沿って、`/context`と`/recall`を次の計画案とする。
実装・確認の結果は[実装結果](../results/i10-context-recall.md)へ記録する。
2026-10-11、利用者のI-10再開指示により、[契約](../design/i10-context-recall-contract.md)へ
DTO、操作結果、投影payloadとadmission時の消費境界を具体化した。
[通常・批判的reviewと限定再review](../results/i10-plan-contract-review.md)を完了し、実装入力を確定した。

## 利用者が必要とする動作

1. `/context`で、直近の親model requestの構成と、次の依頼への引継ぎ予定を確認する。
   実際に使用したexecution・model・step・item数と、これから使う選択を混同しない。
2. 停止・失敗した実行を`/recall [ID|latest]`で選び、その指示・途中応答・tool結果を
   次の依頼の参考情報としてモデルへ渡す。
3. `/recall clear`で準備した引継ぎを解除できる。選択した内容は次のroot実行へ一度だけ渡す。
   元executionの結果・会話採用状態を変更せず、toolの実行済み事実と未完了を区別する。
4. model／providerを切り替えても公開された情報を渡せるようにする。
   停止したproviderのprivate replay stateを別のproviderへ持ち込まない。
5. 何を、どのexecutionから、どの次executionへ渡したかを保存し、再開後も確認できる。

最初の操作例は「F1で停止→`/executions`で対象確認→`/recall ID`→`/context`で確認→新しい依頼を送信」。
停止済みの参考情報を新しいrootへ渡す操作であり、旧runの残りのtoolを自動実行する機能ではない。

## 根拠と確認した旧Henjiの経路

利用者は旧Henji TUIの操作要件の踏襲と段階的実装を指定している。
旧sourceは機能の参考とし、Data Workerの構成や旧serializer互換をGo側の必須条件にしない。

- `v0/tui/remote_session.ts`：`/context`はcheckpoint、pending recall、latest requestのmetadataを表示。
  `/recall [ID|latest|clear]`はSessionを捕捉してCoreへ送信し、準備・解除の結果を表示する。
- `v0/agent/host/core_service.ts`：Session宛のrecall.prepare／recall.clearとcontextReadを公開する。
- `v0/agent/data/session_data_owner.ts`：保存済みの停止・失敗executionを選び、pending recallを所有する。
  execution admissionの保存完了後にpendingを消費する。旧Coreのpendingはメモリ上の状態である。
- `v0/agent/worker/recalled_execution_context.ts`：元task、公開応答、途中応答、tool call／result／progress、
  終了状態などを保存証拠から投影し、providerStateを参考情報へ含めない。
- `v0/agent/worker/worker_runtime.ts`：準備済み参考情報を次requestへ組み込み、sourceとの対応を記録する。

旧実装はrecall準備をidle時に行う。hjh向けの具体化と契約reviewを完了し、以下を実装入力にする。
旧実装が使う全artifact読取、旧V1／V2の互換投影、worker間の往復は引き継がない。

## 現行hjhの利用経路と不足

通常runはTUI `submitDraft/startRun`→apiclient→HTTP `/runs`→corerun.Manager→app.Service.Run→
`app.Run`のBeginExecution→`app.execute`→agent.Run→providerへ進む。
`app.execute`が採用済み会話を`Store.Conversation`から読み、実効modelに合わせてprivate providerStateを外す。
失敗・取消のexecutionは保存されるが、通常の採用会話には入らない。

`app/context.go`は実requestのcontext_item、model_requestとexecution_contextsのmanifestを保存する。
現行の`/inspect`はこの証拠も読むが、messages・records・relations・source等を含むinspectionを展開する。
`/context`のmetadata確認をこの全量読取へ接続しない。

TUIは`/executions`と`/inspect`を持つが、`/context`・`/recall`、pending recallの所有者、
次runへの引継ぎ操作は持たない。schema v3に`recall_relations`が既にあり、現在は使っていない。

I-09ではCoreがSession予約とfollow-up列を所有し、F2受付時の実効selectionを保存している。
引継ぎ準備・通常run開始・予約run開始をこの同じ所有者で順序付ける必要がある。
TUIからrecall本文をtaskへ貼り直す構成や、別のrun registryは追加しない。

## 具体化した契約

DTO、投影payload、操作結果と保存境界は
[契約文書](../design/i10-context-recall-contract.md)へ集約する。計画側で別の案を維持しない。

| 項目 | 実装入力 |
| --- | --- |
| `/context` | 保存された直近親requestのmetadata、採用会話の件数、pending referenceとrecall admissionを区別して返す。request準備の保存をproviderの受理と表示しない。 |
| source | 同Sessionの終了保存済み・非採用の停止execution。省略/latestはroot、明示ID/prefixは子も対象。 |
| 準備 | idle時のみ。CoreにIDと小さいmetadataを保持し、detach後も残る。Core再起動後は再準備。 |
| 消費 | 通常rootがclaimし、BeginExecutionとrelationのcommit後に一度だけ消費。commit前の失敗なら保持。candidate IDを先に捕捉し、commit後の取消可能な再読取に消費通知を依存させない。 |
| F2 | 通常rootで消費したreferenceを再挿入しない。成功rootの通常会話採用から引き継ぐ。 |
| 操作結果 | command_receiptsに小さい結果を保存。同IDの再送で再準備しない。現在のpendingは別途contextから確認する。 |
| 参考入力 | 公開task・適用steering・応答・tool call/resultをprovider非依存の参考messageへ投影する。未完了本文は終了保存されたsemantic recordから読む。 |
| 再recall | 既存参考messageのdurable markerを使い、元証拠を入れ子JSONにせず平坦に一回引き継ぐ。 |
| 保存・表示 | 実参考messageはcontentsへ保存し、semantic mirrorと人間の会話viewには軽いrecall markerを使う。attributionには実参考入力と元証拠の対応を残す。 |

契約の通常・批判的reviewでは、上記が現行Goと旧Henjiの実経路に対応するかを確認する。
旧payload互換、一般hardening、context超過対策をこの具体化に追加しない。

## モジュール境界と保存コスト

- **history**：metadataだけのcontext読取、選択したexecutionだけの参考情報読取、recall relationのtransaction操作。
  SQLの読取transactionを保持したまま別Store操作へ戻らない。schema v3のDDLを維持する。
- **app**：共通のroot入力構築で、採用会話・recall投影・今回taskの順序を組み立てる。
  provider切替時の公開情報とprivate replay stateの扱いを既存の入力構築と共有する。
- **corerun**：Session予約と同じ所有者がpending referenceの準備・claim・消費・解除を順序付ける。
  保存・元execution読取・投影・JSON化をManager lock内で待たない。
- **HTTP/client**：Session contextの読取、recall prepare／clear、操作結果参照を公開する。
  他clientからも同じ契約を使えるようにする。
- **TUI**：`/context`・`/recall`と補完・help、準備結果、source IDの表示を追加する。
  Core／Sessionを捕捉し、Session切替後の遅延応答で新Sessionの表示やdraftを変更しない。

元task・tool result・公開本文は既存contents／message／record locatorから必要なときに読む。
参照操作のたびに元execution全体を複製保存せず、provider入力として新しく必要な投影とsource relationを区別する。
モデルへ実際に渡すrequestの必要な保存は維持し、同じ参考情報を準備状態・receipt・会話viewへ重ねて常駐させない。
使用後・解除後には不要な投影本文を解放する。履歴の自動削除やcontextの固定上限でコストを抑えない。

I-09の128 KiB×1／64件の測定は入力受付・取消の管理コストに限る。
I-10では実際にrecallを適用した次requestと、その保存・保持を測定対象へ加える。

## 実装・確認の順序

1. 具体化した契約の通常・批判的reviewと修正確認を完了し、実装入力を確定する。
2. `/context`のmetadata読取をCore→HTTP→client→TUIへ接続する。
3. recallの選択・準備・解除、root入力への一度だけの挿入と保存relationを接続する。
4. I-09の予約・取消・Session切替と接続し、準備の消費先が一意になることを確認する。
5. 変更した具体的動作のfocused test／race、必要なvet／format／diff確認を行う。
   全体gateの反復、仮想的なprovider variantやpermission matrixは追加しない。
6. private Core／DB／tmuxで、実providerのtool実行→停止→recall準備→新しい依頼→保存・再接続までを操作する。
   次の実model_requestに公開参考情報が含まれ、元execution・完了済みtoolの自動再実行がないことを照合する。
   別providerへの切替でも同じ公開情報を使えることを、保存済み認証で確認する。
7. 同じ参考情報量で単発・反復適用し、DB内の本文コピー・relation・request保存を分けて集計する。
   CoreのGC後保持heapと投影中の割当を区別し、metadata読取が会話全文を展開しないことも確認する。
8. 通常／批判的review、採用findingの修正、Linux配布物の実操作・cold再開を確認する。
   reviewを委譲する場合は旧Henjiの現行経路との対比と具体的な評価範囲を指定する。

お気に入り、認証画面、Core自動起動、tool詳細の再設計、色・Glamour、自動compact／要約は今回の対象外とする。
実利用で観測した[HTTP生本文とターン中のcontext予算・送信選択の課題](../notes/http-response-context-overflow.md)は、
利用者の指示により後続課題として保管する。I-10へその修正を追加しない。
結果と実測は`docs/results/i10-context-recall.md`へ記録する。
