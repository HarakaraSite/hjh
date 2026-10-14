# 履歴DBのWAL化と初出位置の再検索削減

2026-10-14。[計画](../plans/history-wal-first-sequence.md)のS1〜S3実装・確認完了。
起点`75fa1cd`。常用DB/Core/dist、既存backupを操作せず、使い捨てDB/local HTTPで確認した。

## 変更

file-backed writable Openはschema作成/検証後にWALへ切り替える。
追加接続にもsynchronous FULLを設定する。read-only Openはjournal modeを変更しない。
memory DB、schema v4、保存transaction、接続数、自動checkpointの設定は維持する。

execution journalが保存成功後に記憶するfirstSequencesを確定message保存へ渡す。
Storeで展開するthinking Factも同じmapを参照し、model resultの後に履歴JSONを再検索しない。
mapは既存のexecution-localな状態をmutex内で参照する。新cache/table/indexは追加しない。
直接Storeを使いmapを渡さない既存の呼出しではDBによる位置解決を維持する。
進行中stateがある場合は、その初出位置を従来どおり優先する。

## 同条件比較

一つの依頼で300/600回のmodel呼出し、299/599回のrun_js→read_file。
約4KiBの同一ファイルを読むlocal HTTP SSE、inputTokens=10000、新規DBで各条件一回ずつ。
計測中にtest等を並行実行せず、変更前後の一時sourceへ同じdriver connector計測を追加した。
SQL実行・rows読取り時間と、終了時のnative connection close後のprocess I/Oを集計する。
生値・条件・前回の分析結果は[データ](history-wal-first-sequence-data.json)に保存した。

| model呼出し | process累積書込み（前→後） | 減少率 | 全体時間（前→後） | 閉じた後DB（前→後） |
| --- | ---: | ---: | ---: | ---: |
| 300 | 373.24→193.90 MiB | 48.05% | 5.96→2.65s | 6.32→6.32 MiB |
| 600 | 749.79→390.92 MiB | 47.86% | 19.24→6.56s | 12.39→12.39 MiB |

600回の時間は約65.90%短縮。DB差は-4096 bytes、300回は差なし。
初出entityを探すSQLは300回で598→0回、600回で1198→0回。
変更前のSQL累積時間は約3.01秒/12.06秒。execution開始位置を読む別のMIN SQLは各一回残る。
WAL/FULL（synchronous=2）とDELETE/FULLを比較し、page sizeは双方4096。

保存内容の件数は前後同じ。300回はsemantic record 5,099、message/採用message各600。
600回はsemantic record 10,199、message/採用message各1,200。
budget summary総bytesも双方同じ。終了前snapshot表0行、終了後の最新wire/Fact保存を維持した。
各DBのintegrityはok、外部キー違反0。

process wcharはSQLite以外のHTTP等も含む累積書込みで、実device I/Oではない。
前回の「最終HTTP応答前」の値と異なり、今回は終了時snapshot/settlement/closeまで含む。
profilingによるCPU/readの増加も全体時間に含む。実providerの通信時間、異なる結果が増える長期利用、
物理disk IOの改善率は未確認であり、この削減率をそのまま当てはめない。

## 利用経路の確認

S1 focused testで新規WAL/FULL、既存同schema DELETE DBの再open時WAL化、
read-only Openによるmode非変更、古いreader snapshotを保持したままwriterがcommitできること、
次のreadで新しい記録を取得できること、close/reopen/session削除を確認した。

S2 app実経路testはstreamなし/ありの回答、複数thinking key、key省略item、最終結果だけのthinkingを使用。
progress→result→messageの全Factで初出位置が一致し、別read-only接続のreplay最終状態とも一致した。
直接Storeを使う既存のprogress/result/message確認も成功。

S3のlocal HTTP実Core/CLI/PTY TUIは15回のmodel呼出しで、並列二子status/steer/collect、
memory→saved、TUI復元、Core再起動後のsession続行、取消、失敗recall、budget超過、
session削除と本文回収を確認した。外部API呼出しなし。
別試験で稼働中Coreと別CLIのhistory/listを同じWAL DBで使い、Core停止後にCLIから短縮IDで削除した。
削除後session/contentsは0、integrity ok、外部キー違反0。

focused test、対象history/app race test、全体`go test ./internal/... ./cmd/... -count=1`、
staticcheck（Go 1.27対応版 `v0.7.0-0.dev.0.20261009230814-452d5bb86b45`）が成功。
安定候補の全体gateは一回だけ実行した。diff checkも成功。
診断source/binary/script/元データとgate logは`/tmp/hjh-wal-sequence-mmgoxbya`および
`/tmp/hjh-wal-sequence-{full-test,staticcheck}.log`に置いた。

## Review

自己review後、独立reviewerがjournalのmutex/保存成功後更新、親子のmap分離、
progress/result/messageのキー、Store直接利用、replay、Core/CLIの接続経路を確認した。
コードへの必須findingなし。

backup説明のP2を採用した。現行modernc/SQLiteでreaderがsnapshotを保持しwriterが先にcloseすると、
readerも正常closeした後に未checkpointの-walが残り、本体だけのコピーは最新行を欠くことを再現した。
計画・設計・開発者文書を、全接続停止後に本体と残存-walを同じbasenameの対応で一緒に保存する説明へ修正した。
製品のcheckpoint機構を増やしていない。[SQLiteのWAL保存契約](https://www.sqlite.org/wal.html#the_wal_file)。
文書修正だけの限定re-reviewでP2解消、追加指摘なしを確認した。
