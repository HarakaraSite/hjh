# 最新snapshotの終了時保存

2026-10-14。状態：実装・確認完了。起点は`4586934`。
利用者が[計画](../plans/history-terminal-snapshots.md)に沿った実装・各sliceのtest/reviewと、
総合操作・比較測定・最終批判的レビューを指示した。
常用DB/Core/dist・backupは操作せず、使い捨てworkspaceと新規v4 DB、local HTTPだけで検証する。

## 現在地

S-1完了。metadata追加とsnapshot-only保存を分離し、Service共有のexecution別ownerを追加した。
focused history/app test、gofmt、diff checkが成功。通常reviewは必須findingなし。
S-2完了。focused確認・実Core/CLI/PTY確認と通常review/限定re-reviewで完了した。
S-3完了。focused/race確認・実Core/CLI/PTY確認と通常reviewで必須findingなし。
S-4完了。比較/復元測定、最終全体gateと総合的な批判的reviewが完了し、必須findingなし。

## 保存契約の境界

会話・provider state・tool結果・小さなmetadataは逐次保存する。
最新wireと完全なbudget Factはexecution別にメモリへ保持し、モデルの終了確定後に保存する。
モデルの成否とsnapshotだけの保存失敗は分ける。生存中のCoreは未保存本文を保持して明示操作で再保存する。
突然の消失と、DBが最後まで書けない停止時には、未保存snapshotを保持できない。
定期保存・別fileへの退避・migrationを加えず、schema v4の同じ専用tableを使う。

## 検証資料

一時資料は`/tmp/codex-agent-context/history-terminal-implementation/`へ置く。
凍結したbaseline source/binary、slice別diff・focused結果、総合操作script、300/600回の計測・復元を分ける。
実行中と終了後の専用tableの有無を確認し、DB保持容量とprocess累積I/Oを別に報告する。
以下の比較値はprocess I/Oの実測に限り、保持容量から書込み削減率を推定しない。

## 比較用の起点

`4586934`の同条件300/600呼出しを各一回測定した。local providerと約4KiBの同じread_file結果を繰返す。

| model呼出し | 終了後DB | 全体時間 | process wchar（最終HTTP応答前） | 同rchar | RSS peak |
| --- | ---: | ---: | ---: | ---: | ---: |
| 300 | 6.32 MiB | 5.75s | 408.29 MiB | 1107.42 MiB | 47.11 MiB |
| 600 | 12.39 MiB | 18.72s | 840.84 MiB | 4943.33 MiB | 51.16 MiB |

process I/OはSQLite以外も含む累積値であり、最終応答後のsettlement/snapshot保存とcloseを含まない。
この環境のread_bytes/write_bytesは0で、実device I/Oではない。最終保存分を含めた総量とは呼ばない。
復元は新CLI processによるhistoryの全件renderで、OS page cacheを保持。cold-cache/TUI単体起動の測定ではない。
実装後の値と正確な条件・生値は[計測データ](history-terminal-snapshots-data.json)へ追記する。

## S-2の確認

local HTTP 15呼出しの実Core/CLI/PTY TUIで、親・並列二子が応答待ちの間に両専用tableが0行、
Core detailにはexact wireと完全Factがあることを確認した。status/steer/collect、TUI本文復元と/context、
Core再起動後のSession続行、初回budget-only（HTTPなし）、次step予算超過と前wire、
provider失敗recall・取消、Session削除後の全本文回収・DB integrity/FKも成功した。
app focused testはimmutable captureから保存/cache解放をまたぐ読取りと、terminal record失敗でもsnapshot保存を試みる動作を確認。

S-2通常reviewで2件のP2を採用し修正した。最新state判定SQLのouter record相関漏れと、
公開Context locatorがまだsnapshot行に依存した経路である。requestの対象をlogical schemaVersion=1に限定し、
Record自身との対応・最新metadataからのlocator生成へ直した。
focused回帰と実Core/CLI/PTYの再確認で、終了後requestのsaved、過去/物理/non-snapshot記録へのstate不在、
未保存時と保存後のlocator record ID一致を確認した。限定re-reviewで2件の解消を確認した。

## 既存の非Unix全体build制約

起点`4586934`と候補のWindows/amd64 package compileは、未変更のmodelfavorites/credentialsの`syscall.Flock`と
hostの`Setpgid`/`Kill`で失敗する。今回のsignal変更から生じたregressionではなく、計画外の既存build不具合として残す。
新しい`signals_other.go`単体のWindows/amd64・arm64 type-compileは成功。Windows全体や実行可能性を確認済みとはしない。

## S-3の実経路確認

一時SQLite triggerでsnapshot-only保存を失敗させ、Core/CLIがモデル成功とcanonical回答を維持すること、
request/Factのatomic rollback、メモリからのexact detail読取り、並行再保存が同recordに保存しモデルを再実行しないことを確認。
active中の再保存はfreeze/保存せずmemoryを返す。再起動後のsaved entryも再生成なしでsavedを返す。
削除済みSessionの再保存は404で、cacheとDBを再作成しない。Core通常停止の最終retryで本文が保存されるケース、
停止まで書けないケースのexecution ID付きlogと再起動後unavailable、kill -9後のmetadata/tool結果保持・detail不在も確認した。

二子実行を使った16呼出しのscriptを2種類実行した。子Aのsnapshotだけを失敗させてもstatus/collectの
モデル状態はcompleted、最終回答を親へ返す。親実行中に子をretryした後は新status/collectのsnapshot状態がsavedとなる。
別caseでは親完了時のCLI notice/failed child IDsを確認し、その後の子retryで現在のCore結果から失敗IDが外れる。
過去のtool報告やrecord IDは書き換えない。両caseでCore/CLI/PTY復元・context・再起動続行・recall・取消・budget・削除も成功。

local CLI --streamのstdout pipe切断はsignal終了でなくexit 1、SIGHUPはexit 130となり、共通終了でexact wire/Factが保存された。
run --connectの出力pipe切断はCLIだけを終了させ、Coreの実行と終了時保存が続いた。
scriptの初回assertionは子のモデルstate（completed）をsnapshot stateへ混同して失敗したため、
snapshotSaveのdetailAvailableを持つobjectだけへ確認対象を修正。製品codeはこのためには変更していない。

## S-4：同条件の比較測定

起点`4586934`とS-3 frozen candidateを、約4KiBの同一ファイルを299/599回読むlocal HTTP modelで各一回ずつ測定した。
full gateと同時実行せず、一時DB・同じworkspace・inputTokens=10000。実外部providerの時間・実disk IOの評価ではない。

| 呼出し | 変更前DB | 変更後DB | 前→後の全体時間 | 前→後のwchar（MiB） | 同減少率 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 300 | 6.32 MiB | 6.32 MiB | 5.75→5.83s | 408.29→373.00 | 8.64% |
| 600 | 12.39 MiB | 12.39 MiB | 18.72→18.80s | 840.84→749.22 | 10.90% |

DBの保持量はほぼ同じ（600回で+4096 bytes）。以前も最終詳細は1件だけだったため、今回の目的は保持容量の縮小ではなく、
毎回の上書きと完全Fact JSON化を終了時へ集約すること。metadata・会話・tool等の逐次書込みとFact生成は残る。
この一回ずつの計測では全体時間短縮を示していない。

変更後は最終HTTP応答待ち時点でrequest/budgetの専用行がどちらも0、終了後は各1行。
最新wireは両条件27203 bytesで同じ、最新Factは300回63676 bytes・600回127664 bytes、budget各回payload量も前後同じ。
per-call summaryのSources/除外配列保存はない。300/600回ともintegrity ok・FK違反0。

read累積rcharは300回約2.20%、600回約1.27%の減少に留まり、全体readの増加はこの変更で解消していない。
wchar/rcharはSQLite以外も含むprocess累積量で、最終HTTP応答前に採取している。
終了確定後のsnapshot保存・cleanup・closeは集計外であり、終了までの総書込み削減率や処理別IO/CPUとは扱わない。
read_bytes/write_bytesは0で、実disk IO・長期異なるtool結果のworkloadは未計測。
RSS peakは300回47.11→46.06 MiB、600回51.16→52.58 MiB。latest Fact生成とメモリ保持のcostは残る。

## 履歴復元の計測

各viewを新CLI processで3回開き、その中央値。page cacheは温存しており、cold-cacheやTUI単体の復元時間ではない。

| 呼出し | session（前→後） | canonical（前→後） | detail（前→後） |
| --- | ---: | ---: | ---: |
| 300 | 0.101→0.106s | 0.057→0.060s | 0.436→0.482s |
| 600 | 0.197→0.199s | 0.104→0.120s | 0.853→0.964s |

session/canonicalの出力bytesは前後同じ。detailはlatest stateの公開fieldで300回+237 bytes・600回+331 bytes。
この条件ではdetail復元は約0.047/0.111秒増加した。送信中の保存集約とは別に、最新metadataの判定と詳細表示を
含む読取りの負担は残る。処理別の寄与や実外部provider・長期workloadでのlatencyは未分解。

## 最終gateとreview

初回全体gateでは、private child fixtureの共有owner未設定と、TUIのFact付きfixtureのDetailAvailable未設定が検出された。
productionのService/Run/子経路は実Core確認で成立しており、fixtureを現在の契約へ合わせた。
staticcheckで置換後のowner status helper 3件・旧eager保存helper 1件の未使用を検出し、callersなしを確認して除去した。
focused app/child・TUI・historyを確認後、失敗原因の解消を理由に全体gateを再実行した。
この修正は未使用関数除去とfixture調整だけで、測定候補の実行経路を変えていないため、比較計測は繰り返していない。

最終候補の`go test ./internal/... ./cmd/... -count=1`、Go 1.27対応の
`go run honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45 ./internal/... ./cmd/...`、
`git diff --check`がすべて成功。staticcheck出力なし。途中のsnapshot app/httpapi focused raceも成功した。
最終の独立した批判的reviewは必須findingなしで完了。通常slice reviewはS1/S3必須findingなし、S2のP2二件は修正・限定再reviewで解消済み。

source・使い捨てDB/local HTTPでの確認は完了したが、常用v3 DB/Core/distの切替・backup・migration・commit/pushは操作していない。

最終批判的reviewは共通終了保存・metadata/body tuple・cache保持/解放・読取遷移・current child/Core state・
retry/delete/close/signalと検証証拠を独立に照合した。sourceの主要経路とactual script/結果JSON・gate記録が一致し、
完成判断を妨げる追加findingはなかった。実外部provider・実device IO・異なるtool結果を伴う長期workload・
Windows全体buildは未確認/既存制約として区別した。合意した突然消失・停止までDBが書けない場合の限界は維持する。
