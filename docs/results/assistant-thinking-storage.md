# assistant本文・thinking保存整理：実装・確認結果

2026-10-16。利用者が[計画](../plans/assistant-thinking-storage.md)の独立レビュー・訂正後に
実装・test・code reviewを指示した。対象は第一段S-1＋S-2と、その利用確認・比較測定。
第一段ではS-3のproviderState共有は対象外とした。その後、利用者の追加承認によりS-3を実装・確認する。
usage、送信内容、compaction、cache ID/設定は引き続き変更対象外。
常用DB・設定・稼働Core・配置binaryは操作しない。

| 範囲 | 状態 |
| --- | --- |
| S-1 Message由来mirrorのcontent/providerState参照化 | 実装・focused test・独立review完了、指摘なし |
| S-2 thinking本文の先行record参照 | 実装・focused test・独立review完了、指摘なし |
| S-4 CLI/Core/PTY利用確認・同一入力比較測定 | 完了。測定P2を訂正・読取り専用再測定し独立再reviewで解消 |
| 独立code review・総合test | 完了。S-1／S-2指摘なし、S-4測定P2解消・追加指摘なし、総合test全体通過 |

調査時HEAD `96b2625`のtracked sourceを、変更前にtarへ捕捉・隔離展開し、
before CLI/TUIをbuildした。artifact rootは`tmp/assistant-thinking-storage-tZrgMw0i/`。
他作業の`docs/design/extensions.md`と`docs/notes/quickjs-stdlib-tools.md`は本件の変更対象ではない。

以下には実施した確認だけを追記する。ローカル再生と実providerの実行証拠を混同しない。

## S-1・S-2：保存と読戻し

assistant_mirror_codecを追加し、Message由来mirrorのcontent/providerStateを既存Message FKから復元する。
通常thinking mirrorは先行record IDを参照し、公開読取りではtextへ戻してthinkingRecordIdを外す。
journalは成功済みthinking Factのlocatorを完全RequestIdentity＋localKeyで保持し明示handoff、
Message保存成功後に当該requestのmapを解放する。model_resultやMessage本体の所有は変えない。
独立Store入口は渡されたThinkingをinline保存する契約とする。
Records、単体Record、RecallEvidenceの独立JOIN、64件/pageのSQL walkを同時に対応した。

新規history focused testsでFacts有無・先行source有無、Chat/Responsesの複数item、
retry相関、page越しsource、固定cut、close/reopen、公開Payload/Content・recallを確認。
appのTestAssistantStorageLocalSSEParentChildAndReopenはChat/Responsesの実factory＋ローカルSSEで、
親子同step/physical ID、live→save→reopenの本文/kind/identity、providerState全体、
Records/Service.Record、Conversation、AssistantReports、RecallEvidence、walkを確認し成功した。
再開次turnのHTTP入力へChat reasoningとResponsesの全ReplayItems構造・順序が保持されることも確認。
新規app testのrace検出も成功した。
result後Message前停止、初出位置、retry成功/最終失敗、length、親取消の既存focused確認も成功。
初回app新規testはS-2実装途中のraw thinking text重複で失敗し、writer反映後の再実行で成功した。

S-1独立review、S-1＋S-2結合の独立reviewは、writer/Records/RecallEvidence/walk、要求相関、失敗経路と新規testを照合し、いずれもfindingなし。
測定と総合testの結果は後節へ記録する。

## S-4：実CLI・Core・PTY TUI

新規 `scripts/verify-assistant-thinking-storage.py` は、隔離HOME/config/workspace/新規DBで
実CLI/Core/PTY TUIと署名なしローカルHTTPを使う。常用Coreを探索・停止しない。
2026-10-16 01:03 JSTに候補binaryで実行し、passed。

- 親spawn→独立read_file→collect→status、子報告とchild_terminal等の関係を確認。
- 分割reasoning_content SSEを受け、最終回答前のlive thinkingをPTYで確認。
- Coreを停止し別processで再起動、保存Message/providerStateを読戻し、次turn HTTPへのreasoning replayを照合。
- 新Core＋新PTYで保存済み本文/thinkingを再表示。CLI session/canonical/detail（接続・直接DB）、
  Core Inspect/単体Record/親子SessionViewも実際に読む。
- malformed toolによるretry成功、再送最終失敗→recall、stream取消→recallをproduction操作で通した。
  supersededを通常TUI/recallから除き、最後のpartial本文とsourceを維持。
- raw mirror 18件でcontent/providerState省略、thinking参照9件の先行source/物理試行/Text同一性と
  公開Payload/Contentの元field復元を照合した。

初回scriptは非同期StartRun受付のpreparing応答にsessionIdがあると誤って仮定し、KeyErrorで停止した。
APIの既存非同期契約を確認し、scriptだけをrun状態のsession admission待ちへ訂正した。
製品変更は不要。初回到達分を成功完了とは扱わず、訂正後のfresh runで全操作を確認した。

証跡：
- 成功：`tmp/assistant-thinking-storage-tZrgMw0i/acceptance/assistant-thinking-s4-t5_hvjhc/report.json`
- 初回未完了：同acceptance下 `assistant-thinking-s4-3jro_5k9/report.json`
- CLI候補SHA-256：`34fd9cd3ab3a83f796971c974172461b289b18aff86924981d5ccef162b6149a`
- TUI候補SHA-256：`64f144a6f77972d2ab7fe624f634a91bf23e36c2435d0e87b0690c38de379388`

script単体のunverifiedは他focused testや測定の確認と区別する。Responses/stateやresult後Message前停止は
上段のGo focused testで確認。実provider、常用Core再起動、常用の既存Session再開は未実施。


## 同一保存値からのSSE再生：容量・I/O・読戻し時間

2026-10-16 00:54 JSTに、計画§3のroot＋3子だけからmode=ro/query_only/BEGINで
Chat123結果の本文・Thinking・providerStateと相関IDを採取し、固定入力とした。
SHA-256：`cd339048d3a5dc3697c8d206757cdf2052e3b2ac44addc865389fd78872a84fe`。
本文56,265 bytes、Thinking 213,950 bytes、state正規化JSON 237,869 bytes。
usageは入力にもSSEにも含めず、両候補のusage保存0件。

**元の親子/tool実行列・原受信SSEではなく、保存値を独立した123個の最終応答として再生した比較**。
tool calls/引数/結果、元task/親子構成/finish reasonは再現していない。
従って計画の元mirror707,696 bytesの試算と今回のS-1実測量は同じ標本表現ではない。
人間向け親子・失敗/取消/retry操作は前節のacceptanceで別確認している。

実装前source、S-1-only overlay、S-1＋S-2の3候補を同一harnessでbuildし、
固定endpoint/workspace/隔離HOME、ext4、WAL/FULL/default autocheckpointで
各3 fresh DBを順序を回して実行した。下表は中央値。sample範囲・生データはartifactに保持。
独自checkpoint/VACUUMは実施せず、終端・会話採用・snapshot・最後のclose/checkpointを含む
process I/Oを、readback/report出力より前までで採取した。

| 指標（bytes） | Before | S-1-only | S-1＋S-2 | Before→最終の差 |
| --- | ---: | ---: | ---: | ---: |
| semantic payload JSON | 2,309,112 | 1,706,839 | 1,497,529 | −811,583（35.15%） |
| unique contents実bytes | 5,029,004 | 5,029,004 | 5,029,004 | 0 |
| 最新wire | 4,696,386 | 4,696,386 | 4,696,386 | 0 |
| 最新budget | 63,591 | 63,591 | 63,591 | 0 |
| 全close後DB apparent size/dbstat割当 | 14,086,144 | 13,434,880 | 13,172,736 | −913,408（6.48%） |
| process write_bytes | 112,267,264 | 110,841,856 | 110,563,328 | −1,703,936（1.52%） |
| process wchar | 105,837,986 | 104,416,506 | 104,133,834 | −1,704,152（1.61%） |

S-1のsemantic差は602,273 bytes、S-2追加差は209,310 bytes。
微小な時刻等のmetadata差を含むため、正確なfield除去量と全semantic差はわずかに異なる。
最終raw mirrorはcontent/providerStateの埋込み0件、thinking mirror text埋込み0件・先行参照123件。
public readbackは本文/stateに加えrelations/request/locator/sequence等を比較し、
IDを入力順で対応付け時刻等を除いた全8 cross-run比較で一致した。各runでも入力値との完全照合に成功。
123 completed/canonical executions・Session/turn、246 Messages、246 mirrorsを維持した。

| 読戻し（123 executions合計、warm中央値ms） | Before | S-1＋S-2 |
| --- | ---: | ---: |
| Conversation | 6.67 | 6.80 |
| Messages | 6.01 | 10.09 |
| Records | 83.48 | 106.16 |
| Inspect | 116.84 | 132.05 |
| RecallEvidence | 54.38 | 66.55 |
| RecallEvidence→Project | 74.35 | 89.14 |
| Record（thinking、harness内の対象探索・JSON decode込み。単体取得自体の値ではない） | 37.10 | 44.90 |
| AssistantReports | 7.67 | 7.31 |
| SessionView | 220.11 | 173.53 |
| walk | 213.46 | 172.11 |

**容量削減と引換えに詳細/recallの復元時間増加を観測した。全経路の高速化ではない。**
Records +27.17%、Inspect +13.02%、RecallEvidence +22.37%。
Record系の旧測定はtimer内に対象探索・JSON decodeが混入していたため、単体取得自体の増加率として扱わない。
この点は最終reviewのP2指摘を採用し、対象ordinalを計測前に確定する読取り専用測定で訂正した。
SessionView/walkは減った一方、変更していないMessage経路の時間増加もあり、
n=3・OS page cache未制御のため一般的な性能改善/劣化への寄与や統計的有意差を断定しない。
first-open/warm各値と範囲はmeasure/RESULTS.mdに保持。first-openもcold diskとは扱わない。

WAL/SHMはschema作成後、最後のactive/result、terminal＋adoption＋snapshot後、全close後で採取した。
WAL時点サイズを累積書込みへ加算しない。各段のst_blocks、page/freelist、dbstat各table/index、
process I/O・memory補助値もreportに保持。全close後DBは13,168,640–13,176,832 bytes（最終候補）。
process I/Oはapp・同processローカルHTTP・計測query等を含みSQLite専用/SSD物理書込みではない。
最後のprogress時点値は先行122件も含む累積値で、active置換だけの改善とは扱わない。

測定artifact：`tmp/assistant-thinking-storage-tZrgMw0i/measure/` 以下
`README.md / RESULTS.md / comparison.json / summary.json / run-manifest.json`、
`input-evidence.json / build-hashes.txt`、各`*-r*/report.json / readback.json`、共通harnessと実行script。
実provider・Responsesの容量比較・元Session全体の物理削減・CPU/query数/冷ディスク計測は未実施。
既存DBが縮んだという結果ではなく、新規保存DBの比較である。S-3の実装判断へ自動的に進めない。

### 単体Record測定の訂正と再確認

最終reviewのP2は、旧harnessがtimer内で既取得Recordsを探索・JSON decodeしてordinalを選んでいた点。
READMEの「探索はtimer外」という説明を訂正し、旧混合値を保存したまま、新しいreadback-only harnessで
事前manifestに対象record ID/ordinalを確定した。timer内は指定ordinalのService.Record 123呼出しと
typed返却値収集のみ。探索・JSON decode・hash・assert・open/close・report出力は含めない。

既存9 DBをmode=roで同一before/S-1/final harnessから再測定した。各DBでfirst＋warm3回、
各候補3 DBの中央値（123件合計ms）は以下。書込み・容量・I/O測定はやり直していない。

| 指定ordinalの単体Record | Before first | S-1 first | 最終first | Before warm | S-1 warm | 最終warm |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| result | 22.27 | 20.60 | 22.37 | 18.10 | 16.95 | 18.77 |
| assistant mirror | 18.73 | 21.31 | 26.62 | 23.12 | 24.17 | 24.89 |
| thinking mirror | 23.59 | 25.65 | 30.79 | 18.27 | 26.41 | 32.73 |

thinking単体のwarm差は+79.10%（123件合計+14.46ms）、assistantは+7.66%、resultは+3.70%。
これは訂正後の観測値であり、旧混合値の+21.01%を単体取得の増加率として使わない。
OS cacheは未制御、firstもcold diskではなく、対象順固定・n=3の限界は同じ。
容量削減と詳細復元の交換条件を示すが、一般的な性能比率や原因別CPU負荷は断定しない。
返却13,284 recordsが旧公開readbackと完全一致。既存DB本体/WALと旧生report/readback/summary/comparisonの
サイズ・SHA-256が不変であることも確認した。

証跡：measure/readback-only/以下のharness・9 manifests/reports・summary.json・verification.json・保持確認。
総合test後の変更はこの測定artifactと報告文書だけで、製品コードは変更していない。

## 最終focused確認・総合test

親が最終候補でappの新規保存/停止/位置/retry/length/親取消のfocused確認を実行し成功（5.427s）。
historyのmirror/先行thinking/AssistantReports/RecallEvidence/Session削除focused確認も成功（0.114s）。
`go vet ./internal/history ./internal/app`、gofmt、Python構文、diff checkは成功。

計画に基づきcoordinating ownerが安定候補へ一回だけ実行した：

```sh
go test ./internal/... ./cmd/...
```

**全体通過**（internal/app 31.839s、他対象packageも通過）。
証跡：`tmp/assistant-thinking-storage-tZrgMw0i/full-test.log`。
全体testの反復実行、実provider呼出し、常用Core停止/再起動、配置、DB変換/削除は行っていない。

S-4総合reviewは測定scopeのP2一件を指摘し、他に具体的findingなし。
上記の訂正・読取り専用再測定後、別reviewerがtimer境界、9生reportの集計値、
13,284返却値照合、42項目の保持hash証跡と訂正文を独立照合し、P2解消・追加findingなしとした。

## 残る範囲

S-3は追加承認後の続報を下節へ記録する。terminal/runtimeOutcome/afterTurn整理は引き続き別テーマ。
実provider・常用環境での既存Session再開、配置・Core再起動は未実施。
既存DBは保持し、互換読取り・変換・削除を追加していない。commit/pushも行っていない。

## S-3続報：result所有のprovider継続情報をMessageから参照

利用者がS-3の説明を確認し「ああやろうか」と追加実装を承認した。
第一段後のtracked sourceと関連untracked codec/test/scriptを変更前にtarへ捕捉し、隔離before CLI/TUIをbuildした。
S-3 artifact root：`tmp/assistant-provider-state-ycX6mbUm/`。beforeはS-1＋S-2が有効な実装である。

状態：製品実装・focused test・実CLI/Core/PTY受入・比較測定完了。production/受入独立reviewの確認不足P2は実証跡で解消、総合test全体通過、測定・最終文書reviewも追加findingなしで完了。
この続報以下でS-3だけの到達点を記録し、第一段の測定やreviewをS-3済みの証拠としない。

### S-3実装・focused確認

SaveAssistantResult専用codecが保存用result objectをcontentsへ保存し、同transactionのmodel_result.content_digestが所有。
payloadはresultRef、MessageはproviderStateだけをproviderStateRecordIdで先行resultへ参照する。
AssistantResult{Record,Facts}により空本文/ThinkingなしでもIDを返し、完全RequestIdentity別のjournalから明示handoff。
Message.content/text、独立Store入口のinline state、汎用Content添付、resultなしretryは維持した。
Messages/Conversation、mirror Payload/Content、Records/Record、RecallEvidenceの独立経路を復元へ接続し、
walkはtext/finishReasonだけをSQLで読む。result ownerは公開Record.Contentへ追加しない。

一時DBを使うhistory新規testでChat/Responses・empty・単体Record・result-only cut・再open・
compact後の指定digest再put、汎用Content、独立inline入口、Session削除で固有result回収/他Session共有保持を確認。
app新規testはopaque-only Responses（本文/Thinkingなし）のhandoffとreopen→次HTTP、
result後Message前停止、length後の再open・walk・recall継続を確認。
既存親子SSE testもraw owner/reference一致と公開完全state復元を追加し、Chat/Responses双方で成功。
appの追加・変更testのrace検出も成功。

親の最終focused実行：appのS-3/親子/停止/位置/retry/length/親取消は成功（6.256s）、
historyのS-3/mirror/thinking/Reports/recall/削除は成功（0.128s）。
go vet ./internal/history ./internal/app、gofmt、diff checkも成功。
編集中のapp testで一時的な未定義変数compile失敗があり、担当test修正後に再確認成功。製品制約は追加していない。

### S-3実CLI/Core/PTY受入

S-3用raw参照検査を追加したscriptを候補binaryで実行し、2026-10-16 01:32 JSTにpassed。
証跡：`tmp/assistant-provider-state-ycX6mbUm/acceptance/assistant-thinking-s4-4zd8g_yx/report.json`。
CLI SHA-256：`3e6e1e6bf85c1405b20513413b568f0f73deb8ab63e65b824c700edf66aeec6d`。

- 親spawn/read/collect/status、分割thinking live、新Core再起動後の会話・本文/thinking・CLI各view・Core単体Record・PTY replayを維持。
- raw result owners9件、Message state参照9件、canonical assistant6件、mirror18件を照合。
  resultRefと所有FK/digest、Messageの同execution・同physical試行source、公開result/mirror/Contentの完全復元を確認。
- 通常retry成功の先行resultはretryAttempt=1・同logical step・別physical IDで、Messageがこの結果を参照する。
  再起動後のRESUME要求に成功側reasoningが正確に入り、superseded本文/stateは入らない。
- 再送最終失敗とstream取消からのrecallもproduction操作で成功。内部refs/providerStateを参考情報へ漏らさない。

production独立reviewに確定したcorrectness defectはなし。
retry成功stateのdurable再開について確認不足P2が出たが、上記の実受入がその動作を実施済みのため、
別reviewerがscript/source/raw参照/HTTP証跡とCore別process再起動を独立照合し、P2解消・追加findingなしと判定した。
実provider・常用Core再起動/既存Session操作は未実施。



### S-3同一入力比較測定（S-1＋S-2 → S-3）

第一段と同じ固定Chat123 input/hashを再利用し、常用DBの再採取はしなかった。
入力SHA-256：`cd339048d3a5dc3697c8d206757cdf2052e3b2ac44addc865389fd78872a84fe`。
S-1＋S-2を含む今回のbeforeとS-3 afterを同一harnessでbuildし、3 fresh DBずつ交互に比較した。
保存値から123独立final Sessionを再生する条件で、元親子/tool列・原受信SSEの再現ではない。
新規schema/usage変更による効果は混ぜていない（usage0件）。

| 指標（bytes、3標本中央値） | S-1＋S-2 before | S-3 after | 差 |
| --- | ---: | ---: | ---: |
| semantic payload | 1,497,006 | 1,204,273 | −292,733 |
| unique contents | 5,027,774 | 5,099,313 | ＋71,539 |
| payload＋unique contents | 6,524,780 | 6,303,586 | **−221,194（3.39%）** |
| 最新wire/budget含む上記合計 | 11,284,511 | 11,063,317 | −221,194（1.96%） |
| 全close後DB/dbstat割当 | 13,160,448 | 12,967,936 | **−192,512（1.46%）** |
| process write_bytes | 110,542,848 | 111,280,128 | **＋737,280（0.67%）** |
| process wchar | 104,108,780 | 104,875,292 | **＋766,512（0.74%）** |

result offload自体はコピー除去ではない。result contentsは302,360 bytes増え、Message envelopeが230,821 bytes減り、
unique contentsでは差引71,539 bytes増加した。payloadの減少だけで容量削減を報告せず、合計で評価した。
最新wire 4,696,140 bytes、budget 63,591 bytesは各run同量。先行thinkingとmirrorのID/件数も維持する。
結果object123件とMessage state参照123件のraw形、および公開完全値の一致を確認。
公開readbackは全5比較一致、送信requestのpath/長さ/hashも全5比較一致（独立初回要求の一致で、再開送信は前節で確認）。
Record対象ordinalはtimer外で選択し、8,856件の指定ordinal公開値照合と1,476件のmirror Content完全Message一致が成功。

| 読戻し（123件合計、warm中央値ms） | before | after |
| --- | ---: | ---: |
| Conversation | 8.874 | 14.437 |
| Messages | 6.453 | 13.502 |
| Records | 100.809 | 129.852 |
| Inspect | 136.160 | 170.611 |
| RecallEvidence | 68.486 | 97.478 |
| RecallEvidence→Project | 88.208 | 120.203 |
| 単体Record result | 17.448 | 27.260 |
| 単体Record assistant mirror | 22.049 | 30.989 |
| 単体Record thinking mirror | 24.526 | 34.057 |
| AssistantReports | 7.280 | 7.073 |
| SessionView | 178.380 | 178.512 |
| walk | 168.040 | 171.119 |

**S-3はこの条件で保存容量を削減したが、総書込み量の削減・読戻しの高速化にはならなかった。**
Message復元の追加参照などを伴い、Conversation +62.69%、Messages +109.22%、Records +28.81%、
RecallEvidence +42.33%、単体result +56.24%、assistant +40.54%、thinking +38.86%を観測した。
絶対時間と比率を併記し、n=3・OS page cache未制御・fixed route順のため一般的な比率や原因別CPU負荷は断定しない。
firstはfresh Store/Serviceでありcold diskではない。Record timerには対象選択/JSON decode/hash/assert/open/closeを含めない。
process I/Oはschema・app・同process HTTP・計測・terminal/adoption/snapshot・最後のclose/checkpointを含み、
readback/report前で終了する。SQLite専用/SSD物理書込み量ではなく、WAL時点サイズは累積書込みに加算しない。
本測定は既存DBの縮小を示さず、第一段の削減率へ単純加算しない。

artifact：`tmp/assistant-provider-state-ycX6mbUm/measure/`以下のREADME.md/RESULTS.md、
comparison.json/summary.json/supplemental-summary.json、6 raw reports/readbacks、manifest、audit、harness/commands。
source driftなし、保持input/harness/build hashesと公開値一致を保存済み証跡で検証した。

### S-3総合test

coordinating ownerがS-3安定候補で`go test ./internal/... ./cmd/...`を一回実行し、**全体通過**。
internal/app 33.159s、他対象packageも通過。証跡：`tmp/assistant-provider-state-ycX6mbUm/full-test.log`。
常用DB・Core・設定・配置binaryは変更せず、commit/pushも行っていない。

S-3測定・最終文書の独立reviewは、6生reportから主要集計を再計算し、source/input/harnessの比較条件、
公開値/初回送信の5比較、Record timer境界、close後I/O終点、readback退行を含むclaimを照合してfindingなし。
実provider・常用環境の再開操作、配置・Core再起動は未実施のまま。保存容量だけでなく、
今回観測した書込み量・読戻し時間増加も判断材料として残し、自動的な追加最適化には進まない。
