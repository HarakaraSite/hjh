# 最新送信全文と予算記録の保存改善

> **後続変更の案内（2026-10-16）**：v4専用表・小さな予算summaryは現行でも使用する。以下の毎request UPSERTと専用行からのlocator生成は、[終了時snapshot保存](history-terminal-snapshots.md)で置換済み。最新wire／完全FactはService ownerが保持し、終了確定後に保存する。[現行保存契約](../design/normal-history-storage.md)を参照。

2026-10-14。状態：実装・各スライスのtest/review・総合test・最終批判的レビュー完了。
利用者が[改訂計画](../plans/history-snapshot-overwrite.md)に沿った各スライスの実装・test・reviewと、
最後の総合test・変更全体の批判的コードレビューを指示した。起点は`aae542e`。
常用Core・DB・backup・distは変更していない。

## S-1：送信全文の直接上書き

schema v4の専用2 tablesを追加し、最新wireをexecution専用行へ直接UPSERTする経路へ切り替えた。
通常requestのcontents保存・古いFK解除・毎送信の全DB参照検索とGCを廃止した。
Records/Recordはexecution IDとrecord IDが一致する専用行を同じSQLで読み、metadata/bodyを対応させる。
公開Contextのlocatorは専用行から組み立て、execution_contextsには重ねて保存しない。
会話・tool結果・recallの所有と読取り、Session削除時の共通本文回収は維持する。

focused testで、実HTTPのbytes一致、失敗・取消後の保持、並行置換と読取り、親子別行と削除cascade、
tool本文とrecall・次turn継続を確認した。gofmt・diff checkも通過した。
通常reviewは必須finding・S-1を止める具体的確認不足なし。S-2へ進む状態と判断した。

検証コマンド：

```sh
go test ./internal/history -run 'TestEmbeddedSchemaV4AddsLatestSnapshotTables|TestAppendLatestRequestDirectlyOverwritesExecutionOwnedBody|TestLatestRequestBodiesAreSeparateForParentAndChildAndCascadeOnDelete|TestRecordsConcurrentLatestRequestReplacementKeepsCapturedBody|TestSessionContextReadsRequestMetadataAndSeparatesLatestRoot|TestLatestRequestReplacementPreservesToolMessageBodies' -count=1
go test ./internal/app -run 'TestLatestRequestBodyMatchesHTTPAndPhysicalRequestKeepsMetadataOnly|TestStreamFailureRetainsTextWithoutAdopting|TestLatestRequestBodySurvivesProviderCancellation|TestRunAdmitsRecallBeforeProviderAndAttributesReference|TestDelegationParallelResultsEvidenceAndResume|TestChildRunsParallelCollectAndIndividualCancel' -count=1
```

## S-2：各回summaryと最新予算詳細

各回の予算recordにはsummaryとdetailAvailable:falseを保存し、最新の完全なFactを
execution_latest_budgetsへ1件だけ保持する。両者を同transactionで確定し、
Records/RecordとSessionContextはmetadata・summary・最新Factを同じSELECTで捕捉する。
最新recordだけにfactとdetailAvailable:trueを返し、過去の選択/除外配列・History.Turnsを蓄積しない。

focused testは保存粒度、最新詳細の置換、上書き中の同step対応、初回予算超過、前wireとの独立性、
最新rootと子budgetの選択、current/historyの選択・再開、lifecycle/親子、CLI detail・TUI表示、削除を確認した。
通常reviewは必須finding・S-2を止める具体的確認不足なし。S-3へ進む状態と判断した。
focusedコマンドの記録は一時資料のs2-tests.txtへ保存した。

## S-3：実Core・CLI・TUIと一括確認

使い捨てworkspaceと新規v4 DBで実Core・CLI・PTY上のTUIを起動し、ローカルHTTP providerを使った。
親が二子を並列開始し、中間報告をstatusで読み、子Aへ追加指示を送り、両子の最終回答を回収した。
親の復元・`/context`表示、Core再起動後の同Session継続、HTTP失敗からのrecall、取消も確認した。
APIが受信したbytesとinspectの最新wireは親子それぞれで一致した。

大きなread_file結果を受けたstep 2で予算超過となり、APIへ送信せず、
最新budgetはstep 2、前wireはstep 1として独立して残ることをCoreのcontext APIで確認した。
この試験では入力上限100,000 tokensをconfigへ明示する。初回は上限がなく超過条件を作れず、
検証configを直して再確認した。製品コードの変更は行っていない。

DBの各予算payloadにはsummaryとdetailAvailable:falseだけがあり、最新の公開recordにだけFactが返る。
全Session削除後はexecutions・contents・両専用tableが0行、integrity checkはok、FK違反は0件だった。
ローカルmodel呼出し15件、外部API呼出し0件。これはmodelの役割選択能力を評価する試験ではない。

安定候補で次の一括確認が通過した。

```sh
go test ./internal/... ./cmd/... -count=1
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45 ./internal/... ./cmd/...
git diff --check
```

PATH上のstaticcheck v0.7.0はGo 1.27のexport dataを読めず、解析前に失敗した。
前incrementでも使用したGo 1.27対応の固定版に切り替え、製品sourceを変更せず通過した。
一括testは一度だけ実行し、reviewerはtestやgateを再実行していない。

## 保存容量と実行時間

旧candidateは`aae542e`から、新candidateはS-2完了時のsourceからビルドした。
ローカルHTTP providerが同じ約4 KiBのfileをrun_js/read_fileで繰り返し読み、最後に回答する条件を比較した。
入力上限10,000 tokens、model呼出し300/600回、tool呼出し299/599回、各条件は一度ずつ計測した。
SQLite journal modeはdelete。tmpfs上で使い捨てDBを作成し、外部APIは使っていない。

| model呼出し | DB変更前 | DB変更後 | 削減 | 実行時間変更前 | 実行時間変更後 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 300 | 15.67 MiB | 6.36 MiB | 59.4% | 11.71 s | 5.75 s |
| 600 | 49.19 MiB | 12.43 MiB | 74.7% | 62.06 s | 18.71 s |

| model呼出し | 旧予算payload合計 | 新summary合計 | 最新Fact 1件 | 最新wire 1件 |
| --- | ---: | ---: | ---: | ---: |
| 300 | 9.212 MiB | 0.175 MiB | 63,692 bytes | 26,123 bytes |
| 600 | 36.634 MiB | 0.350 MiB | 127,684 bytes | 26,124 bytes |

新summary＋最新Factは合計0.236→0.472 MiBとなり、300→600回で約2倍だった。
旧方式の約4倍という一覧蓄積を除去できた。最新の除外件数592→1,192はFactへ保持している。
新DBの専用request/budgetは各1行、通常model_requestのcontents FKは0件だった。
各DBのintegrity checkはok、FK違反は0件。会話・toolの他記録の内訳は[測定値](history-snapshot-overwrite-data.json)へ保存した。

これは同じ結果を繰り返し読む条件であり、異なる結果が増える実利用へ削減率をそのまま適用しない。
数時間〜数日の耐久性を保証する試験でもなく、普段の利用で継続確認する。

## Process I/Oと残るコスト

最後のHTTP応答を返す直前に`/proc/PID/io`を採取した。完了採用時の最終書込みは含まない。
rchar/wcharはprocess全体のread/write系呼出しで扱ったbytesであり、SQLiteだけのI/Oではない。

| model呼出し | rchar変更前 | rchar変更後 | wchar変更前 | wchar変更後 |
| --- | ---: | ---: | ---: | ---: |
| 300 | 4,206.61 MiB | 1,116.69 MiB | 415.12 MiB | 405.80 MiB |
| 600 | 24,849.35 MiB | 4,959.86 MiB | 853.60 MiB | 835.42 MiB |

rcharは73.5%/80.0%減ったが、wcharの減少は2.2%/2.1%だった。
tmpfsのためread_bytes/write_bytesは全条件で0であり、実diskの書込み削減量は未計測。
最新Factの生成・毎回全文上書き、wire全文書込み、他の会話・tool記録とtransactionの書込みは残る。
新方式でも300→600回のrcharは約4.4倍、全体時間は約3.3倍であり、長期の処理コストが解消したとは扱わない。
保存内訳と全体時間・process I/Oの測定から、最新詳細保存・projectionの個別の寄与は分離できない。
CPU時間、SQLite/journal別の累積bytes、transaction数、実disk I/Oは分離計測していない。

## 保存履歴の復元・出力

300回DBを別CLIプロセスでopenし、`history --latest`の各viewを3回出力した中央値。
OS cacheを保持したopenから全文出力までの時間で、cold-cacheやTUI単独の計測ではない。

| view | 変更前 | 変更後 |
| --- | ---: | ---: |
| session | 0.129 s | 0.103 s |
| canonical | 0.058 s | 0.056 s |
| detail | 0.536 s | 0.438 s |

session/canonicalの出力bytes数は前後で一致した。detailは過去の予算詳細を廃止したため、
21,546,121→12,133,825 bytesへ減った。会話・結果の本文を失った削減ではない。

## 最終レビューと利用環境の切替

S-1/S-2の通常レビュー、最終候補のコード全体にまたがる批判的レビューは、
必須finding・変更されたproduct動作の具体的確認不足なしで完了した。
最終reviewでは送信前保存、親子と最新rootの選択、予算・wireの独立性、Core/API/CLI/TUI、
会話継続・provider state・tool本文・recall・削除、測定の主張と残余コストを確認した。
常用環境のv3 DBは保持しており、今回の新v4 binaryでそのままopenすることはできない。
旧DBの変換・削除・再初期化、常用Core/distの更新は実施していない。
新DBの使用場所またはbackup後の切替は別途利用者が決める。migration・dual-read/writeは追加していない。

## 検証資料

一時検証資料は`/tmp/codex-agent-context/history-snapshot-implementation/`へ保存する。
起点source・旧binary、slice別diffとfocused test記録、ローカルHTTPの総合操作・計測scriptを分ける。
計測は使い捨てDBだけで行い、外部API・有料modelを使わない。
一時資料がなくても測定の数値を確認できるよう、集計JSONを[リポジトリ内](history-snapshot-overwrite-data.json)にも保存した。
