# I-10：入力contextの確認と停止した実行の引継ぎ

> **後続変更の案内（2026-10-16）**：recallは実装済み。以下の各request全文・精密context attribution保存は後続で置換した。現在はrequest小metadata／予算summaryとexecution最新wire／完全Factの終了時保存に分かれる。[保存契約](../design/normal-history-storage.md)・[recall契約](../design/i10-context-recall-contract.md)を参照。

2026-10-11。[計画](../plans/i10-context-recall.md)と
[入力context・引継ぎ契約](../design/i10-context-recall-contract.md)に沿って実装した。
focused test／race、通常・批判的reviewと限定再review、実provider／TUI、保存コスト、Linux配布物のcold再開を確認した。

## 利用者の操作

- `/context`で採用会話、直近の保存済み親requestの準備metadata、pending recall、参考入力の保存位置を確認する。
  保存済みrequestの実効modelと問い合わせ時点の次回選択は別に表示する。providerの受理を保証する表示ではない。
- 停止・失敗したrunの保存完了後、`/recall [ID|latest]`で公開情報を次の通常rootへ引き継ぐ。
  省略/latestは同Sessionの停止root、明示ID／一意prefixは停止した子も選べる。
- `/recall clear`で解除する。準備はdetach中も残るが、Core再起動後は再準備する。
- 元executionの取消・失敗、非採用状態は変えない。以前のtool callを実行待ちへ復元しない。
  成功した引継ぎ先の参考messageは通常会話に採用され、その後の依頼でも利用できる。

## 所有者・保存・入力

historyはmetadata読取、選択した終了executionの証拠、操作receipt、admission relationを所有する。
rootのBeginExecutionとrecall relationを同じtransactionで保存し、commit後の取消可能な再読取を経由せず
execution ID／relation IDを返す。schema v3のDDLを変更していない。

Coreは既存のSession予約と同じownerでprepare／clear／claim／consumeを順序付ける。
通常rootだけがclaimし、appのtyped admission通知で一度消費する。admission前の失敗はclaimを解除して準備を残し、
admission後の取消では復元しない。F2の後続runはpendingをclaimしない。
操作の保存・公開はCoreのjoin対象であり、HTTP呼出しの切断で取消さない。
SQL、投影、JSON、provider、VM、SSE、描画をManager lock内で待たない。

appと独立したrecall projectionは元task、適用steering、公開応答・停止途中の本文、toolのcall/result/statusを
provider非依存の参考user messageにする。停止本文は終了時に保存したsemantic recordから読む。
private providerStateとthinkingは展開しない。内側callは外側未完了時だけ軽いactivity metadataで示し、
保存済み外側resultを優先する。再recallは元locatorを保った平坦な情報にし、既存参考JSONを入れ子にしない。
参考messageは採用会話の後・今回taskの前へ一度保存・挿入し、独立した子Agentへ暗黙に渡さない。

本文はcontents、操作結果はcommand_receipts、source/targetはrecall_relationsへ保存する。
semantic mirrorと人間の会話viewは短いrecall markerにし、本文を重複保存・表示しない。
requestのsourceRelationsとexecution contextのexternalRelationsに元証拠とrelationのlocatorを残す。
contextはadmission、参考message保存、実親request準備の三段階を区別する。

HTTP/clientはSession context、recall操作、操作結果の参照を公開する。
同じ操作IDの再送・照会は保存・公開の完了を待ち、過去receiptからpendingを復元しない。
TUIは送信時のCore・Session・操作IDを捕捉し、遅い応答を別Sessionの画面やdraftへ適用しない。
通信結果が不明な場合は同じ操作IDで確認する。

## reviewと修正

通常・批判的reviewは旧Henjiのremote_session、core_service、session_data_owner、
recalled_execution_context、worker_runtimeと対比した。旧worker構成やserializer互換は引き継がず、
Goの状態所有者と採用済み契約を基準にした。

| 採用finding | 修正 |
| --- | --- |
| P1：遅延した同ID POSTが消費済みprepareを復活させる | 登録後にもdurable receiptを確認し、既存結果ではpendingを公開しない |
| P1：recall付きrootからの子Agentが空のrelation IDで失敗する | 子入力へ親のrecallをコピーしない |
| P2：実Observationのid/parentを投影が読まず内側activityが欠落する | 現行保存形式を認識し、親chainの内側callをmetadataだけで投影する |
| P2：複数tool callへ同じassistant本文を複製する | 通常経路とmessage fallbackの双方で本文を一回だけ載せる |
| P2：contextがsemantic ordinalをmessage番号として表示する | 保存locatorのmessageOrdinalを使い、0も保持する |
| P2：操作結果GETがpending公開前に完了を返す | durable読取後にも処理中operationへjoinする |

各reviewerによる一回の限定再reviewで6件の解消を確認した。未解消の採用findingはない。
reviewerはsourceと関連testを確認し、live call・full gateを実行していない。実経路の検証は親agentが行った。

## verification

history、projection、app、Core、HTTP/client、TUIの変更動作に対するfocused testとrace確認、
関連packageのvet、format、`git diff --check`を通した。full gateは実行していない。

admission前失敗で準備が残ること、admission後の取消で消費されること、同ID再送と公開join、
F2の非claim、Core Closeの保存join、子Agentの独立入力、Session切替後の遅延応答とdraft保持を確認した。
実Run／VM／journalで保存したid/parent形式も投影へ渡し、外側result優先と、未完了外側snapshotの
内側metadata・本文非展開を確認した。再recallの平坦化、本文の一回化、軽いcold会話もfocused確認に含む。

## 実provider・TUI・Linux配布物

利用者のCore／TUI、DB、設定、tmuxへ変更を加えず、private Core／DB／tmuxを使用した。
保存済みcredential参照を再利用し、追加ログインを要求していない。

1. OpenRouter Responses／`deepseek/deepseek-v4.1-flash`でread_file、command sleepを実行した。
   適用済みsteeringと次のtool開始を確認後、実TUIのF1で取消し、終了保存した。
2. TUIの`/recall latest`・`/context`でprepareし、detach後もpendingが残ることをHTTPで確認した。
   再接続して`/provider openai-responses`へ切り替えた。保存済みOpenRouter入力と次回OpenAI選択は別表示だった。
3. OpenAI Responses／`gpt-6-luna`へ新しい依頼を送り、元tool結果とsteeringの二つのmarkerを含む最終回答を得た。
   保存した実wireにも参考情報が入り、参考message内では各marker一回、targetのtool callは0だった。
   sourceはcancelled／non_canonicalのままで、recall relationは1件だった。
4. 最終Linux配布物のarchive／binary hashとstatic ELFを確認し、同じprivate履歴のcopyをcold再開した。
   人間用viewは参考本文を展開せず、同じrecall entityを表示し、contextのmessage番号は0だった。
   TUIのprepare／clear、Core停止・再起動、historical receiptの非復元を確認した。
5. 再起動後、配布TUIから採用会話を継続し、実OpenAIが同じ二つのmarkerを再回答した。
   保存wireに採用済み参考情報が入り、追加relationとtool callは0だった。

Sessionは`39a3be4e-d694-4520-ac35-3324365c05ab`。
証跡はgit管理外の`dist/i10-live/provider/`、`cold/`、`packages/`にsummary、wire、pane、DBを保管した。
通し確認後にreview修正を施し、最終配布物でcold view・clear・再起動・実provider継続を再確認した。
最終配布物のSHA-256は`dist/i10-live/package-summary.json`に記録している。

## 保存と保持heapの実測

[測定spike](../../spikes/i10-cost/README.md)はローカルproviderを使い、実Core→app→VM→request準備の経路を通す。
128 KiBのread_file結果を持つ停止sourceを1回／16回引き継ぎ、各targetの入力に本文が一回だけ入ること、
pendingの消費とrequest locatorを確認してからtargetを取消した。採用会話の増加は測定に混ぜていない。

最終候補の測定値（bytes）は次のとおり。

| 項目 | 1回 | 16回 |
| --- | ---: | ---: |
| 元本文 | 131,072 | 131,072 |
| 保存された参考本文のunique bytes／digest数 | 132,713／1 | 132,713／1 |
| 保存されたrequest wireのunique bytes／digest数 | 135,783／1 | 135,783／1 |
| recall semantic mirror合計 | 922 | 14,769 |
| 操作metadata合計 | 405 | 6,497 |
| relation件数 | 1 | 16 |
| DB適用前→全target終了後 | 978,944→1,290,240 | 978,944→1,982,464 |
| GC後heap：適用前→全target終了後 | 644,640→694,376 | 758,760→812,936 |
| admission→requestの累積割当 | 5,838,032 | 92,348,960 |
| metadata context読取64回の累積割当 | 591,560 | 552,056 |

同じ本文と同じrequestはcontentsで共有され、各適用のrelation・context metadata・terminal record等は増える。
参考本文を準備状態やviewに常駐させていない一方、入力構築・VM・encoding・保存を含む累積割当は
1適用あたり約5.8 MBだった。これは投影だけの割当でもGC後の保持量でもない。
metadata読取の値もSQLと小さいJSON decodeを含み、会話・wire本文をGoへ展開しない経路を測った。
同じ内容を反復したローカル測定であり、異なる会話やrequestが増える実利用全体のDB成長率を保証しない。
結果は`dist/i10-live/cost-final-summary.json`に保管した。

## 後続課題

[HTTP生本文とターン中context予算・送信選択](../notes/http-response-context-overflow.md)は今回修正していない。
I-10は公開参考入力の選択・保存・引継ぎであり、contextを自動削除・切詰め・要約する処理は追加していない。
