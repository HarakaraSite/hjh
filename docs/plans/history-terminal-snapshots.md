# 最新送信全文・予算詳細を終了時に保存する

2026-10-14。状態：実装・確認完了。起点は`4586934`。
利用者は「metadata追加と本文保存を分離し、終了時保存へまとめる」方向を指定した。
突然のプロセス消失で未保存の最新送信全文・予算詳細を失うことは許容する。
通常・批判的レビューを終え、利用者が本計画に沿った実装・test・review・総合確認を指示した。
常用DB・Coreの操作とcommit/pushは今回の実装範囲に含めない。

## 必要な利用動作と根拠

会話継続・tool結果・親子の報告を現在と同じ経路で保存しつつ、大きな最新snapshotの毎回の書込みを外す。
最新送信全文の意味は、metadataの保存が成功した準備済みwire bytes。APIの成功を条件にしない。
最新予算詳細は、summaryの保存が成功した判断の完全なFactとする。

- 実行中の`/context`、inspect、detail取得、TUI再接続で最新情報を読める。
- 正常終了・取消・provider失敗・予算超過では、各executionの最新snapshotを終了時に保存する。
- 新しい予算判断だけができた場合は、前の送信全文と新しい予算詳細を独立して保持する。
- snapshot保存失敗はモデル実行結果と区別して通知し、Coreが動いている間は本文を保持する。
- Core経由で保存を再試行できる。Sessionを削除した場合はそのメモリ状態も除く。
- 突然のプロセス消失後は、保存済み会話・tool結果・metadataを読めるが、未保存snapshotは取得不能と明示する。

根拠は[前回の結果](../results/history-snapshot-overwrite.md)。600回の比較でDB容量は74.7%減ったが、
processのwchar減少は約2.1%に留まった。最新wire・最新Factの毎回全文保存は現在も行う。
wcharはprocess全体の量であり、今回どこまで削減できるかは未計測。削減率を先に約束しない。
取消/provider失敗は`execute`の共通終了処理へ戻り、取消と分けた保存contextを使っている。
直前の本文喪失経路レビューでは、SIGPIPE・SIGHUP等の終了迂回、DB保存失敗、terminal後のrecord追加制約を確認した。

## 現行の生成・保存・参照・終了経路

| 情報・操作 | 現行の実経路 | 変更する責務 |
| --- | --- | --- |
| 予算 | Projector → contextBudgetRecord → AppendLatestBudget | summary追加と最新Factの保持を分ける |
| wire | provider.Prepare → contextAttribution.request → AppendLatestRequest → provider.Generate | metadata追加成功後にwireをメモリで保持する |
| 会話・tool結果 | journal → messages/semantic_records/contents | 逐次保存・本文所有を維持する |
| live詳細 | Service.Record/Inspect/SessionContext → history SQL → Core/API/TUI | metadataとメモリの最新snapshotを同じrecord IDで組み合わせる |
| terminal | execute defer → journal.end → EndExecutionWithConversation → canonical採用 → cleanup hook | 実行結果確定後にsnapshotを保存し、最終run結果と保存状態を分ける |
| 子の回収 | runChild → childRun.result/done → collect | 各子がsnapshot保存を試みた後にdoneを閉じる |
| Coreの完了 | Service.Run復帰 → Managerのterminal通知 | snapshot保存試行と状態通知までをRun復帰に含める |
| Core停止 | Manager.Closeでrun join → Service.Close → Store.Close | 未保存snapshotの最終再試行をStore closeより前に行う |
| Session削除 | Managerの既存排他 → Service.DeleteSession → history cascade | 成功時に同Sessionのsnapshotをメモリからも除く |

現在の`executiondata.Owner`はroot＋子のdurable sequenceと購読を所有し、本文cacheを持たない。
この責務へ大きなsnapshotや再保存を混ぜない。snapshot用ownerをapp側に設ける。
現在の終了後hook観測は専用のpost-settlement操作で保存しており、終了後のDB更新自体は禁止されていない。

## 保存操作の分離

historyに次の責務を持つ操作を分ける。名前の確定は実装時にこの責務へ合わせる。

1. request metadataの追加：active中に小さなmodel_requestを追加し、recallのexternalRelationsを保存する。
2. budget summaryの追加：active中に小さなcontext_budgetを追加する。
3. snapshotだけの保存：既存のrequest/budget record IDに対応するwire/Factを専用tableへ保存する。

metadata保存失敗では従来どおりAPIへ進まず、通常の履歴保存失敗として扱う。
成功したmetadataのrecord ID・ordinal・stepと対応する本文をownerへ渡し、その後にAPIへ進む。
metadataが保存できなかった新しい判断やwireを、別stepのrecordへ結び付けない。

snapshot保存はrecordを追加せず、executionのordinal・件数・モデル結果・採用状態を変えない。
参照する既存recordのexecution IDとkindを対応させ、schema v4の両専用tableへ保存する。
渡されたwire/Factを同じtransactionで保存し、どちらかの保存が失敗した場合はrollbackしてメモリを保持する。
requestをまだ作っていない実行はbudgetだけ保存できる。wireの不在を空bytesで代用しない。

この操作はactive/settledの両方で使える。active制約は新しい実行recordの追加に残す。
既存metadataへの本文保存に、新しい会話・tool処理の開始やterminal recordの追加を含めない。
終了時にはsettled後にも保存できるため、現在のAppendLatestRequest/Budgetを単に移動する変更とはしない。

DDLとschema versionは4を維持する。新しい表・本文hash・locatorの二重保存・migrationは追加しない。
既存v4の同じ保存形式はそのまま読める。常用v3 DBの切替は今回も別の操作とする。

## メモリ状態の所有と寿命

app.Serviceがsnapshot ownerを所有し、RunOptions/実行inputを通じてroot・子へ同じownerを渡す。
entryはexecution ID別に持ち、Session IDとroot IDも削除・通知の対応に使う。
CLIも既存のServiceを使い、Coreと同じ保存操作を通す。
直接app.Runを呼ぶ場合は渡されたownerを使い、省略時はそのrunの寿命だけのownerを作る。

各entryが持つのは、最新requestのrecord位置とexact wire、最新budgetのrecord位置と完全なFact、保存状態だけ。
会話・全request一覧・tool結果・過去Factのcacheは追加しない。
requestとbudgetは独立に更新し、budget更新で前のwireを消さない。
Factはprojection完了時の値を保持し、毎回の全Fact JSON化は行わない。JSON化はdetail読取りまたは保存時に行う。
wire/Factを後続処理が変更しない所有方法を確認し、読取りにはrecord ID付きのimmutableなsnapshotを渡す。

agent loopが止まった後にentryを固定する。新たなmodel判断・送信metadataは追加しない。
保存成功後は大きな本文をownerから外し、以降はDBから読む。
保存失敗後は同じCore/Serviceの寿命内で保持し、成功・明示Session削除・Service終了まで再利用できる。
再試行中に本文を更新・破棄せず、並行する再保存は同entryの一つの保存処理に合流する。
DB I/O中はowner全体のmutexやCoreのmutexを保持せず、他Session・子の実行と読取りを止めない。

## 最新情報の公開と整合性

historyの最新request/budget選択は、専用snapshot行の有無から、保存済みlogical metadataの順序へ切り替える。
LatestRootExecution、request metadataを持つ最新root、budget metadataを持つ最新rootは独立に選ぶ。
本文がまだ保存されていなくてもmetadataを隠さず、古いrootの本文を最新のように返さない。
公開ContextのlatestRequest locatorもmetadataから生成する。recallのrelation参照と保存Messageの経路は維持する。

Service.Record/Inspect/SessionContextはownerのsnapshotを捕捉し、DBから読んだrecord IDと一致する本文だけを合成する。
metadataとsnapshotのstepが一致しない場合、別stepの本文で補完しない。
metadata追加とメモリ公開の短い間に読んだ場合も、詳細が未公開であることを返し、次回読取りで最新を返す。
history SQLのrecord/body一致JOINは維持し、memoryからDBへの移行中もcapture済みの本文を読める。
Storeを直接読むoffline CLIはDBにある詳細だけを返す。Core経由のdetailはメモリの最新内容も返す。

詳細の有無と保存状態を分ける。公開の保存状態は、次を区別する小さな値と任意の保存errorとする。

| 保存状態 | 意味 |
| --- | --- |
| memory | 最新詳細をこのCoreが保持している。まだDBへ保存していない |
| saved | 当該metadataに対応する最新詳細がDBへ保存されている |
| failed | 保存を試みて失敗した。生存中のownerには詳細がある |
| unavailable | metadataはあるが対応する詳細がDBにもこのCoreにもない |

`detailAvailable`はmemory/failedでも本文が読めればtrueとする。
budgetの過去recordはsummaryのみで、latestの選択・除外件数や番号を過去へ付け直さない。
API/CLI inspect・TUI `/context`で実行中・保存済み・保存失敗・取得不能を区別する。
突然の消失後はmetadata・summaryを読め、詳細不在を0件や空の全文として表示しない。

## 終了、失敗、再保存

共通終了処理は正常・取消・provider HTTP/stream失敗・予算超過・通常のobserver/hook errorで使う。
子joinと追加入力の終了を既存順序で行い、既存のj.endとcanonical採用でモデル結果を確定する。
その後、固定したsnapshotの保存を取消と独立したcontextで試みる。
terminal保存が失敗した場合もsnapshot保存を試み、既にあるmetadataに対応する本文を捨てない。
`control.failure()`があることだけを理由にsnapshot保存をskipしない。

snapshotを最初に保存する試行はcleanup hookより前に置き、hookの長い待機で本文保存を遅らせない。
`afterTurn/runtimeStop`の従来の実行結果・通知契約は変えない。
モデル処理のterminal factは従来どおりdurable commit後に公開する。
これはモデル処理が終わった記録であり、snapshot保存完了の通知として使わない。
Coreの最終run結果・子のdone/collectはsnapshot保存試行と保存状態の確定後に返す。

会話・metadata・terminal等の通常履歴保存失敗は、従来のhistory_failure経路を維持する。
snapshotだけの保存失敗はモデルのOK/outcome、既に採用した会話、子の最終回答を変更しない。
snapshot保存errorを通常履歴用control.storageへ流さず、保存状態として通知する。
RunResult/Core run結果と子のstatus/collectに小さな保存状態を渡し、TUI通知とCLI stderrで失敗を示す。
rootの完了通知では子のsnapshot保存失敗もexecution ID付きで伝え、子結果を失敗回答へ置き換えない。
CLIのモデル実行結果に基づくexit statusは維持する。診断保存失敗はstdoutの最終回答とは別にstderrへ通知する。

再保存は明示操作とする。Serviceのretry操作をCoreの
`POST /api/v2/executions/{id}/snapshots/save`（bodyにcoreId）へ接続する案とする。
activeでまだ保存を試みていないentryには現在状態だけを返し、途中保存へ切り替えない。
failedで固定済みのentryを再保存し、成功ならcacheを解放する。モデル/APIを再実行せず、metadataも追加しない。
既にsavedなら状態を返すだけにする。消失後のunavailableは再生成せず、取得不能のまま返す。
既存のSession書込み/削除の排他へ接続し、削除されたSessionの本文を再作成しない。
再保存成功後のCore run結果・inspect・contextの保存状態もsavedへ更新し、モデル結果は変更しない。
子のstatus/collectも同じ現在状態を返す。owner/DBから読取り時に合成し、
childRun.status/resultやCore runTerminalに残った値のコピーを保存状態の正本にしない。
既に出した失敗通知は当時の通知として残し、現在状態とは分ける。
親の履歴に保存済みのtool結果も当時の内容として保持し、再保存によって書き換えない。
このincrementではTUIの新slash commandやCLIの新command群は追加せず、Core公開APIとして再保存を扱う。

Core通常停止は既存どおりrunをjoinし、Service.Closeで残るfailed entryを一度ずつ再保存してからStoreを閉じる。
そこでまだDBが書けなければ、保存できなかったexecution IDとerrorを返し、停止時logへ明示する。
無限の再試行や停止拒否は設けない。ローカルCLIもService closeまで同じ最終試行を行う。
CLIの現行のclose error黙殺はこのsnapshot失敗について通知へ接続する。
正常停止でもDBが最後まで書けない場合、メモリ消失後の保持は保証できない。この物理的な限界は隠さない。

## プロセス停止の境界

確認済みのSIGPIPE経路は、Unix CLIで通常のEPIPEとして終了処理へ戻す。
SIGHUPはSIGINT/SIGTERMと同じ取消経路へ接続する。
signalの変更はUnix用の小さな境界へ置き、非Unix配布の既存signal契約を維持する。
`run --connect`やTUIの切断だけでCoreの実行を取消す契約は追加しない。

VM停止、kill -9、SIGQUIT等の通常終了処理を通らない停止、fatal crashは、利用者が許容した消失境界とする。
一般panicのcatch、signal全般の防御、定期保存、別fileへの退避、DB失敗時の別backendは追加しない。
会話・tool結果・metadataはそれまでの逐次保存分を保持する。未保存snapshotの再構築は約束しない。

## スライスと確認

### S-1：historyの責務分離とsnapshot owner

対象はhistoryのmetadata追加/本文保存操作とそのfocused test、app側snapshot ownerと公開statusの基本型。
現行appのeager経路はこの段階では切り替えず、既存のatomicな保存契約を保つ。
新操作はmetadataを追加済みのactive/settled executionへsnapshotだけ保存でき、ordinalや結果を変えないことを確認する。
ownerは親子別の最新状態、wireなしのbudget、immutable capture、保存成功時の解放・失敗時の保持を確認する。

### S-2：実行・終了時保存とlive読戻しを同時に切替

対象はappのrequest/budget接続・Service/RunOptions・共通終了処理、historyの最新metadata読取り、
Core/API/CLI/TUIの詳細合成と保存状態。使われなくなるeager用操作と全Fact毎回JSON化も整理する。
保存タイミングとlive公開を同じsliceで切り替え、途中候補を常用環境へ反映しない。

モデル待ちの間に専用tableが未保存で、Core detailには最新exact wire/Factがあることを実経路で確認する。
終了時にはHTTP受信bytesと一致する最終wireが1件だけ保存され、親子・再接続・次turnを維持すること。
取消/provider失敗、初回予算超過、次stepの予算超過と前wire、上書き/保存/読取りのrecord対応を確認する。
モデルの終了factとsnapshot保存状態を混同せず、子doneとCoreの最終結果は保存試行後になることを確認する。

### S-3：保存失敗・再試行・通常停止・削除・signal

対象はCore再保存APIとclient側操作、既存のSession排他、Service.CloseとCLI/Core通知、Unix signal境界。
snapshotだけのDB保存失敗を一時DBで再現し、モデル結果・会話・子結果を維持し、Coreから本文を読めること。
DBが書けるようになった後に明示再保存し、同じrecordのexact bytesを保存してcacheを解放すること。
再保存後のCore run結果・inspect/context・子status/collectに現在の保存状態を反映することを確認する。
Session削除、Core停止の最終再試行と失敗通知、CLI close通知を確認する。
local CLIの閉じたstdout pipeとSIGHUPで共通終了保存へ戻ること、--connectのCLI切断ではCoreが継続することを確認する。
既存の非Unix配布targetはsignal helperのcompileを確認し、未観測OSの停止挙動をfixtureで仕様化しない。

### S-4：総合確認・比較測定・最終レビュー

使い捨てworkspace/v4 DBとlocal HTTP、実Core/CLI/PTY TUIで、親子並列・報告・steer/collect、
再接続・終了後再起動と継続、失敗recall・取消・予算超過・再保存・削除を確認する。
突然のプロセス消失でsnapshotを失う契約は、local runを送信待ちで停止した一例で確認し、
保存済みmetadata/会話/結果を取得でき、詳細不在を明示することを確認する。停止原因のmatrixは作らない。
影響packageの一括test、Go 1.27対応staticcheck、diff checkは安定候補でrootが一度だけ行う。

4586934と新candidateを、以前と同じ300/600回条件・使い捨てDBで一度ずつ比較する。
wireとbudgetの専用tableが実行中に更新されず、終了後に最新1件を持つことを操作証拠・sourceと合わせて確認する。
DB保持容量、全体時間・復元時間、process rchar/wcharを別に記録する。性能測定中にfull gateを並行実行しない。
最新FactのJSON化を毎回外した効果と、元のprojectionが全Factを作るCPU/メモリの残余を混同しない。
tmpfsの測定で実disk削減を主張せず、処理別I/Oの未分離も明記する。削減率・test件数を完成条件にしない。
外部model/APIの料金は発生させない。保持するtool結果が異なる条件のbenchmarkを必須にせず、日常利用で継続確認する。

各sliceは実装→focused test→通常reviewで進め、最後に全利用経路にまたがる批判的コードreviewを行う。
reviewは初回30分、必要な修正後は変更とfindingの解消を15分で再確認する。
一般hardening、未観測provider/permission matrix、要約checkpoint、精密計測hookは対象外。

## 計画作成・実装時の境界

利用者が詳細計画を採用し、再保存APIと保存状態の公開を含めて実装する。
通常履歴の保存契約・payload/API・開発文書を更新し、結果文書へ保存回数と測定の限界を残す。
常用DB/Core操作、commit/pushは実施しない。sourceで確認した共通終了経路へ新owner・再保存・終了通知を接続する。

## 計画レビュー結果

通常review・批判的reviewは必須findingなしで完了した。
metadata/bodyの対応、モデル結果と診断保存の分離、子doneとCore最終通知、
ownerの責務、再保存/削除/停止の接続を現行sourceと照合した。
批判的reviewの実装上の注意として、再保存後の状態を古い値コピーから返さないことを本書へ明記した。
計画review時点では新しい並行処理と書込み削減率は未確認だった。実装後の確認と到達点は
[結果](../results/history-terminal-snapshots.md)に記録する。
レビュー中にcode、test/gate、実Core/API/DBの操作は行っていない。

## 実装・確認結果

S-1〜S-4の実装、slice別focused testと通常review、実Core/CLI/PTY TUI総合操作、300/600回比較・復元、
最終全体test/staticcheckと総合的な批判的reviewを完了した。S-2通常reviewの2件は修正・限定再reviewで解消。
最終reviewの必須findingはない。[詳細結果と制約](../results/history-terminal-snapshots.md)を参照する。
終了時保存の集約は実現したが、この条件ではDB容量・全体時間はほぼ同じである。
測定のwchar減少8.64%/10.90%は終端flushを除く。常用環境切替・commit/pushは実施範囲外のままとする。
