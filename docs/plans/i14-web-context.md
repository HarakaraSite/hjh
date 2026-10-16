# I-14: Web読取りとCoreのコンテキスト予算管理

> **後続変更の案内（2026-10-16）**：I-14は実装済み。以下の「現在」の経路は着手前の状態。通常Web受信1 MiBは後続で64 KiBへ変更し、毎requestの精密archiveは小metadata／summaryと最新snapshotの終了時保存へ置換した。[現行契約](../design/i14-web-context-contract.md)・[Web defaults](../results/web-retrieval-defaults.md)・[保存契約](../design/normal-history-storage.md)を参照。

利用者はWeb読取りの改善と、ターン内だけでなく全てのコンテキスト予算管理を依頼した。
通常のWeb受信上限は、利用者が旧Henjiと同じ1MiBを明示採用した。
管理の所有者はCoreであり、TUIは管理結果を表示する。

## 利用動作と現行経路

CLIまたはHTTP/TUIからapp.Service / app.Run → app.execute → agent.Run → providerへ進む。
子Agentもapp.executeを使う。現在はadopted履歴全文と現在のターン全文を毎回送信し、
HTTP toolもHTML全文を返す。実際に4ページのHTMLでproviderのcontext上限を超えた
事例は[調査記録](../notes/http-response-context-overflow.md)にある。

## 採用する動作

- Web用のweb_fetchを標準toolに追加する。通常は受信本文1MiB、HTMLを可読テキストへ
  変換、text/JSON/XMLはテキスト、バイナリはメタデータを返す。最終URL、status、type、
  切詰め有無を示す。save_toは元の本文をサイズ上限なくファイルへ保存し本文を会話に戻さない。
  生のHTTPが必要な既存api.http / httpは保持し、ページ読取りにはweb_fetchを案内する。
- Coreが選択モデルのcapacityと明示設定を解決する。context C、input I、実際の出力予約R、
  入力率q(既定0.8)からL=min(floor(C*q), I, C-R)を既知項だけで計算する。
  advertised max_outputを実際のRとして扱わない。モデル別 > provider別 > defaults > metadata。
  capacity不明時は架空の固定上限を作らず、その事実を記録する。
- 送信adapterの実wire全体を測定し、ceil(bytes/3)+16+8*messageCountを推定token数とする。
  正確なtokenizerではないことを記録する。instruction、tools、reasoning/replayを含める。
- 保存全文を変更せず、送信projectionだけを選択する。現在のtask/steering、最新応答、
  最新の完全なtool交換、未完了交換を保護する。assistant応答と全tool call/resultを一単位に
  し、古い完了単位から除外する。削除した現在ターンの単位は同じexecution中に復活させない。
- 最新のadopted turnから履歴を選ぶ。最新turnの全文が入らないときはその保護部分と
  入る古い単位を残し、それより古いturnを追加しない。保護部分も入らなければ履歴を省略する。
  全文が入るときは古いturnを丸ごと新しい順に追加し、送信順は時系列を保つ。
- 次ターン、Session再開、モデル変更で保存全文から改めて選ぶ。--from-turnを守り、
  モデル変更時の既存provider state無効化を保つ。子Agentは独立した履歴・モデル・予算で動く。
- 保護された現在ターンだけで超過したときは送信前にcontext_budget_exceededを記録して終了する。
  予算、根拠、推定cost、選択/除外元、実wireを保存し、/contextから確認できるようにする。
  Coreは削減を通知する。自動要約や別の会話保存先を追加しない。

## 参考sourceと責務

旧sourceは /home/agent/projects/henji-harness/external-tools/web_fetch/main.ts、
v0/agent/session/context_budget.ts、v0/agent/worker/context_projection.ts、
v0/agent/worker/worker_runtime.ts と increment-221を読み取った。
旧Henjiの一般的permission/path制限やprovider wire byte上限は採用しない。

internal/webfetchは取得/変換、internal/contextbudgetは解決/選択を所有する。
appはcaptured config、モデルmetadata、履歴読取り、実行単位のprojector、記録を所有する。
agentは全文の会話を保持して送信時にprojectorを呼ぶ。TUIに予算計算や履歴選択を置かない。

## 確認

実装後、変更箇所に対応するfocused確認を行う。通常のHTML読取り、1MiB切詰め、
save_to、複数tool交換と履歴選択、完全なcall/resultとreplay保持、次ターン/再開/モデル変更、
子Agent、送信前超過の保存を確認する。静的確認だけで完了とせず、production CLI/Core経路で
Web読取りと複数ターンの予算動作を実行する。既存の認証だけを使い、費用を発生させるモデルは使わない。

## 未確認事項

capacityはprovider catalogとmodels.devの現在の応答を調査して接続する。
取得できないroute/modelは不明として記録し、明示設定で解決できるようにする。
容量を推測した固定モデル辞書や別providerの容量の流用をしない。
