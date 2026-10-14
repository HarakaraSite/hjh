# 最新送信全文の直接上書きと予算記録の保存粒度

2026-10-14。状態：実装・各スライスのtest/review・総合test・最終批判的レビュー完了。起点は`aae542e`。
利用者が各スライスの実装・test・reviewと、最後の総合test・変更全体の批判的レビューを指示した。
通常レビュー・批判的レビューを実施し、測定で報告できる範囲、summaryの保存粒度、
最新記録の選択条件を明確にした。常用DB・Coreの切替は今回の実装指示に含めない。

## 必要な動作と根拠

利用者との議論では、実行ごとの最新送信全文を新しい全文で上書きする方式を改善の軸にした。
更新のために古い本文IDの利用状況を全DBへ問い合わせる必要のない構造にする。
予算の過去の除外一覧が通常利用に必要か、一覧をID参照にする必要があるかも問い直した。

本計画の予算保存契約は、各回の予算・推定量・件数を保存し、詳細一覧は各executionの最新1件だけを保持する。
過去の全選択/除外一覧は保存しない。利用者が改訂計画に沿った実装を指示して採用した。
実行中の除外済み状態とモデル入力の選択処理は現在のCoreのまま使う。
会話の再開は保存Messageから選び直すので、過去の予算一覧には依存しない。

根拠は[前回の測定](../results/normal-history-storage.md#残る増加要因と次の設計)と現行source。
300回の予算JSONが9.21 MiB、600回では36.62 MiB（DBの約75%）となった。
各回の`Sources`、`OmittedCurrent`、`OmittedCurrentSources`、`History.Turns`を展開保存するため、
長い一実行では過去分を含む一覧のコピーが蓄積する。

最新wireの置換は`AppendLatestRequest`が旧FKを外して、`deleteContentIfUnreferenced`で
会話・実行記録等の全DB参照を調べる。本文を実行専用の場所へ置けば、この処理を廃止できる。

完成する利用動作は次の通り。

- 親・子それぞれの最新準備済み送信全文を、成功・失敗・取消後にinspectで読める。
- 古い送信全文は新しい全文で置き換え、全DBの本文参照を検索しない。
- `/context`で最新の予算・送信選択・除外番号・理由を確認できる。
- 過去の予算記録から、その回の予算・推定量・件数・成否を確認できる。
- 会話継続、tool結果・provider state、recall、親子の報告、Session削除を維持する。

## 現行の生成・保存・読取り

| 情報 | 現行経路 | 置き換える部分 |
| --- | --- | --- |
| 予算判断 | `agent.Loop` → `ProjectRequest` → `contextbudget.Projector` | 実行時の判断・状態は維持する |
| 予算保存 | `app/execution.go` → `AppendOccurrence(context_budget)` → payload_json | 数値の記録と最新の詳細を分ける |
| 最新予算表示 | `SessionContext`がbudget recordを持つ最新rootから最新budgetを読む → Core/API → `formatCoreBudget` | 最新詳細を結合して現行表示へ渡す |
| wire保存 | `provider.Prepare` → `contextAttribution.request` → `AppendLatestRequest` | contentsのFK・旧参照解除・GCを直接上書きへ置換 |
| wire詳細 | `Records/Record` → `app.Service.Inspect` → Core/API・CLI | 最新recordと専用本文を同じSQLで捕捉する |
| context位置 | execution_contextsのlatestRequest locator → `Context` | 専用wire行を唯一の保存位置とし、公開locatorはそこから生成する |
| 会話再開 | conversation_messages → messages → contents → projector | 変更しない |
| Session削除 | executionsの親子削除と本文/構成の回収 | 専用の最新行をexecutionと一緒に削除する |

## 保存先とschema案

wireと予算詳細は異なるタイミングで更新する独立した最新状態なので、別の行へ置く。
各保存処理が更新するのは自分の状態だけとし、他方の大きな内容を持つrowを更新対象にしない。
executions自身のordinal更新も、wireを持つrowと分ける。

既存tableを維持し、次の2tableを追加するschema v4とする。
新tableの理由は、実行専用の可変全文を、汎用のcontentsと分けて直接所有・上書きするためである。

```sql
CREATE TABLE execution_latest_requests (
  execution_id TEXT PRIMARY KEY REFERENCES executions(execution_id) ON DELETE CASCADE,
  record_id TEXT NOT NULL REFERENCES semantic_records(record_id) ON DELETE CASCADE,
  wire_body BLOB NOT NULL
);

CREATE TABLE execution_latest_budgets (
  execution_id TEXT PRIMARY KEY REFERENCES executions(execution_id) ON DELETE CASCADE,
  record_id TEXT NOT NULL REFERENCES semantic_records(record_id) ON DELETE CASCADE,
  fact_json TEXT NOT NULL
);
```

`record_id`は最新状態に対応するsemantic recordを示す。requestのordinal・model・件数は
semantic recordが持ち、tableへ同じ情報を複製しない。execution IDのPKで更新位置を直接引く。
親子は別execution IDなので、互いの最新状態を上書きしない。

`execution_contexts.manifest_json`へlatestRequest locatorを重ねて保存する処理は廃止する。
通常recallのexternalRelationsは既存のmanifestへ保持する。
公開`Context`のlatestRequestは、専用request行とsemantic recordから組み立てる。
最新を管理する場所を増やすだけでなく、旧locatorの保存責務も置き換える。

schema versionは`PRAGMA user_version`とstore_metadataを4へ揃える。
旧Henjiから固定したv3のsource資料は履歴資料として残し、Goの新規DB契約を更新する。
v3→v4のmigration、converter、dual-read/writeは今回含めない。
常用のv3 DBの扱いは実装後の切替手順で利用者と決め、計画合意だけでは削除・変換しない。

## 最新送信全文の契約

1. 準備済みの正確なwire bytesと小さなlogical request metadataを受け取る。
2. 同じtransactionでmetadata recordを追加し、executionの専用wire行をUPSERTする。
3. commit後にAPIへ進む。保存失敗では以前の専用wire行を保持し、APIを送信しない。

以前の送信recordは小さなmetadataだけを残す。
通常モデル送信のrecordはcontentsのcontent_digestを持たず、wireのハッシュ照合・旧FK解除・
全table参照確認・本文回収を行わない。上書きに伴うpage管理はSQLiteへ任せる。
他の本文を保存する汎用OccurrenceInput.Contentや、tool結果のresultRef/textRef契約は維持する。

Records/Recordは専用行のrecord_idと一致するrecordにだけwireをContentとして返す。
metadataとwireを同じSELECTで取得し、並行更新時に別の送信の本文を結び付けない。
一覧や会話walk、SessionContextはwire bytesを読まない。
最新wireをcontentsから取得する旧処理と、使われなくなるrequest専用GC/helper/test期待を整理する。
Session削除時は専用行がexecutionのcascadeで消え、共通本文の回収候補へ混ぜない。

最新の意味は今と同じ「API送信前の保存が完了した準備済み全文」。
HTTP失敗・取消後も保持する。projection失敗や予算超過で送信へ進まない場合は前のwireを保持する。
予算の最新判断とwireの最新送信が別stepになる場合も、各record/stepを明示する。

## 予算記録の契約

### 各回の小さな記録

context_budget semantic recordには次の情報を保存する。

- model step・model選択・予算超過の有無・metadata取得error。
- Budgetの上限・入出力reserve・比率・設定出所、Estimator。
- wire bytes・推定tokens・history分の推定量。
- 選択したmessage件数・currentから除外した件数。
- historyの選択状態と全体の理由。

Sources・OmittedCurrent・OmittedCurrentSourcesの配列と、History.Turns全体は保存しない。
historyは全体の状態・理由と上記の推定量だけを残し、turn別の状態・理由・Source一覧は入れない。
過去の詳細がないことを明示する保存形式にし、件数を0や空一覧へ偽装しない。
各回の件数はその回に計算した値として保存するので、後の状態で置き換わらない。

### 最新の詳細

各executionの最新Factをexecution_latest_budgets.fact_jsonへ1件だけ保存する。
小さなsemantic record追加と専用budget行のUPSERTを同じtransactionにし、record_idを結び付ける。
既存FactのSources・除外一覧・History.Turns等は、最新ではそのまま確認できる。

budget用Store操作を一本に集約し、runtime用のFactと保存用summaryを明確に分ける。
実行中のprojector、通知、live observer、モデルに渡すMessageは従来のFact/判断を使う。
budget記録でwireを参照したり、wire更新がbudget詳細を消したりする処理を作らない。
最初の判断が予算超過だった場合も、budget詳細を保存できる。送信rowの存在を前提にしない。

SessionContextはbudget記録を持つ最新rootの最新budget recordと、同recordを指す専用行から完全なStatusを返す。
LatestRootExecution、最新requestを持つroot、最新budgetを持つrootは現行どおり独立に選ぶ。
新しいrootにまだrequestやbudgetの記録がない間は、それぞれの記録を持つ前のrootの最新情報を返す。
返却するexecution ID・record ordinal・stepは選択した記録のものとし、最新rootの情報へ付け替えない。
Records/Recordの最新budgetも詳細を展開し、過去のbudgetはsummaryとして返す。
番号を使う/context表示は最新詳細を使用する。CLI detail・inspectでは過去と最新の粒度を表示する。
metadataと最新詳細は同じSQLで捕捉する。後続判断による上書き後に別stepの詳細を付けない。
wire上書き・予算上書きとも公開読戻しを同時に切り替える。

## スライスと確認

### S-1：wireの実行専用保存と読戻し

schema v4に上記2tableを定義し、最初にwireの経路を切り替える。
budget tableの利用はS-2で接続する。途中スライスの候補は常用環境へ反映しない。
対象はhistoryのschema/version、AppendLatestRequest、Records/Record・Contextの読取り、
appのrequest接続、保存契約と該当test。旧GCとlocator保存も同じスライスで廃止する。

確認する具体的操作は、既存の実HTTP・親子・失敗/取消の経路を使う。
APIが受け取った最新wireが保存bytesと一致し、親子各1行、以前のwireは上書きされること。
会話・toolの本文が残り、通常送信ではwireをcontentsへ追加せず、全DB参照確認を呼ばないこと。
実行中のdetail読取りと置換でmetadata/bodyが対応し、Session削除で専用rowも消えること。

### S-2：予算のsummaryと最新詳細

対象はappのbudget保存用変換、historyのatomicなbudget保存と公開読取り、
contracts/TUI/historyviewの必要な表示、関連するcontext/recall/lifecycle test。
最新詳細を更新するStore経路を設け、全Factを各回のpayloadへ埋める旧接続を置換する。

予算によるcurrent/history選択・通知・親子・次turnを既存product testで確認する。
除外が増えても過去recordに大きな配列がなく、最新には番号・理由が残ること。
予算超過時の最新budgetと前のwireが独立して残り、/contextとinspectが正しいstepを示すこと。
上書き中の公開読取りが同じstepのsummary/detailを返すこと。
S-1のwire保存が最新budgetを消さず、Session削除で両専用rowが消えること。

### S-3：総合確認と効果の記録

一時workspace/新規v4 DB・ローカルHTTP model・実Core/CLI/TUIで確認する。
既存の親子並列・報告・追加指示・回収、Core再起動後の同Session継続、失敗recall、
取消、/context、detail、Session削除の確認を再利用する。有料APIは使わない。
影響packageの一括test・staticcheck・diff checkを安定候補で行う。

旧candidate（aae542e）と新candidateを、使い捨てDBの同じ300/600回条件で一度比較する。
各回summaryの合計、最新budget詳細1件、最新wire1件、他の会話・tool記録を別集計する。
二次的な一覧蓄積がなくなったことと、毎送信の全DB GC検索がなくなったことを確認する。
実行時間・復元時間・process I/Oを分けて記録し、保持容量から書込み量を推定しない。
この測定で報告するのは本文回収除去後の全体の残存コストであり、最新budget詳細の保存と
projectionの個別の寄与は分離できない。処理別のCPU時間・I/Oを測定したとは扱わない。
削減率・test件数を完成条件にしない。異なる結果の追加benchmarkを必須化せず、普段の利用でも継続確認する。

各スライスは実装→focused test→reviewで進め、保存と読戻しの未解決問題を次へ持ち越さない。
review初回30分、修正後は変更箇所の再確認15分を既定とする。一般的なhardeningや未観測matrixは加えない。
計画採用後の確認方法であり、計画作成・レビュー段階ではコード変更やtestを行わない。

## 効果の範囲・残る点

過去予算の一覧を蓄積しないことで、保持する予算情報は「各回のsummary＋最新詳細1件」になる。
最新詳細の除外一覧自体は実行が伸びると増える。full Factを毎回作って上書きするCPU/I/Oは残る。
この計画はその累積書込みまで一定量になると約束するものではない。
latest詳細の範囲/差分化は、必要性が実測された場合の追加設計とする。
最新wireの全文書込みも残るが、全DBの旧本文参照確認は除去する。

全ての共通本文を直接保存へ変える変更ではない。toolの原結果・会話・指示原文と
Session削除時の共通本文回収は現契約のまま。廃止するのは毎送信のwire用GCだけである。
不要になったGCに索引を足す変更は含めず、残る他用途の索引追加は実測を根拠に別途検討する。

実装後は[前計画の契約](../design/normal-history-storage.md)・payload/schema文書・
新しい結果文書を更新する。schema/version契約変更と、過去budget詳細を残さない変更を明記する。
旧DBの変換・再初期化、常用Coreへの反映、commit/push、hookの精密計測改善、要約checkpointは今回の対象外。

## 実装・確認の到達点

- S-1：専用wire上書き・読戻しを実装し、focused testと通常reviewを完了。
- S-2：各回summary・最新Factを実装し、focused testと通常reviewを完了。
- S-3：実Core/CLI/TUIの総合操作、300/600回の前後比較と復元測定、一括test/staticcheckを完了。
- 最終候補のコード全体にまたがる批判的reviewは必須finding・具体的確認不足なし。

確認方法・数値・残余コストは[結果](../results/history-snapshot-overwrite.md)に記録した。
常用DB・Core/distの切替は行っていない。

## 切替時に決めること・残る未計測

- 常用v3 DBを保持したまま別v4 DBを使うか、backup後に新規v4 DBへ切り替えるか。実データの操作は別途指示を得る。
- 本文回収除去後の全体実行時間・復元時間・process I/OはS-3で測定済み。最新詳細保存とprojectionの個別の寄与、実disk I/Oは未分離・未計測として残す。
