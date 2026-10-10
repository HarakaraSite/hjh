# S-04結果：会話採用・構成追跡・Session閲覧と再開

2026-10-10、Linux/amd64、Go 1.27.2で実施。[計画](s04-plan.md)の実験範囲で成立した。
保存Sessionから採用済み会話と未採用の実行を区別して読み、現在の設定で別プロセスから再開できた。
過去の設定・外部ソースはDBに残り、履歴増加後もSQLで指定範囲だけを取り出せた。

[実験コードと再現手順](../../spikes/s01/S04.md)、[通常実行のsummary](s04-artifacts/summary.json)を参照する。
生成JSは固定コード、回答draftは実際のtool結果から作ったもの。モデル名は実験用識別子で、モデルAPIは呼んでいない。
モデル入力候補の構築とJSへの引渡しを確認した結果であり、実LLMによる会話・Agent loopの受入ではない。

## 保存の意味と責務

既存のhistory eventを実行証拠の正本としたまま、Session・turn・構成snapshot・採用markerを追加した。
各turnは実行runと構成IDに関連付く。正常実行の終了後、取得した結果と回答draftをturnへ保存し、
別transactionで採用markerを保存する。

採用済み会話はmarkerが参照するturnのrequest／responseの組から導出する。
会話本文を別の正本へコピーせず、実行証拠も採用時に削除・変更しない。
失敗・キャンセル・成功したが未採用のturnはSession内の実行として閲覧できる。
閲覧・再Openだけでは、成功draftの自動採用や実行の自動再試行は行わない。

`configsnapshot` が設定とsource bytes、`history` が保存と選択読込み、`session` が今回の実行・終了保存・採用の流れ、
`sessionexperiment` が実処理・別プロセス・停止境界・計測を所有する。
共有jsruntime・host・traceとS-03のtool／hookは変更していない。
今回のSession driver・回答生成・schemaはスパイク用であり、最終製品APIではない。

## 実際の観測

| 動作 | 結果 | 記録 |
| --- | --- | --- |
| 実行完了と会話採用の分離 | 正常実行の40eventと回答draftを保存した時点では採用0、会話0。採用後は同じ実行証拠を保持し、1turnのuser／assistant 2messageを復元。 | [採用前](s04-artifacts/before_adoption-inspection.json)、[採用後](s04-artifacts/after_adoption-inspection.json) |
| 失敗・キャンセル・未採用成功 | 内部処理がJSON reportを書いた後に親JSを失敗させ、213bytesの副作用を読み戻した。HTTP待ちはサーバー受信後にキャンセルし、終了結果を保存。成功した未採用turnも保存。4turnのうち会話は採用済み1turnだけ。 | [Session閲覧](s04-artifacts/mixed-inspection.json)、[実行と副作用](s04-artifacts/mixed-evidence.json) |
| 別プロセス再開 | Worker A（PID 354170）の終了後、Worker B（PID 354181）で同じSessionを再開。Bは採用済み2turn・4messageと前の回答を入力として使い、変更後の構成でHTTP・file処理を完了した。 | [A](s04-artifacts/restart_a-worker.json)、[B](s04-artifacts/restart_b-worker.json)、[Session](s04-artifacts/resumed-inspection.json) |
| 過去の構成とソースの保持 | 現在の設定・sourceを編集しても、旧model／instruction・tool・hook・hook設定・semverのbytesをDBから読めた。保存済み旧source treeを使うWorkerは、編集後も旧tool／hookと旧instructionで動作した。 | [構成archive](s04-artifacts/configuration-archive.json)、[旧snapshotからの実行](s04-artifacts/frozen_after_edit-execution.json) |
| 採用COMMIT前の停止 | 実行と回答draftを保存し、採用markerをINSERTしたtransactionのCOMMIT前にwriterをSIGKILL。生存中・停止後とも採用3のまま。対象turnはcompleted／未採用で、成功した40eventと回答draftを読めた。 | [生存中](s04-artifacts/crash_live-inspection.json)、[停止後](s04-artifacts/crash_stopped-inspection.json)、[実行証拠](s04-artifacts/crash_execution_evidence.json) |
| 履歴増加と範囲指定 | 実HTTP・YAML／semver・file処理を120turn追加。全123turn・4,920eventを保持し、最新3turn、過去3turn、turn一覧の3件、最初のrunのevent 3件ずつを取り出した。 | [最新](s04-artifacts/history_large-inspection.json)、[過去](s04-artifacts/history_older-inspection.json)、[turn page](s04-artifacts/history_turn_page-inspection.json)、[event pages](s04-artifacts/history_event_pages.json) |

キャンセル・失敗も共有runtimeが実処理を停止してから戻り、今回のWorker返却後のGo処理数は0だった。
独立readerプロセスが読み取り専用でSessionと会話を閲覧した。採用停止実験では所有するwriterだけを停止し、Wait完了を確認した。
停止したのは採用transactionであり、すでに終了したコマンドやHTTPを中断したという意味ではない。

## 使用構成の追跡

構成には、実験のmodel識別子・instruction・外部treeとhookの選択、外部tree内83fileのbytesとhash、
Go runtime、Go module version、実行binaryのSHA256を保存した。
JS依存のversion・integrityとlicenseは外部vendorに含まれ、snapshotにも保存される。
各turnが内容から導いた構成IDを参照する。同じ構成はturnごとに再保存しない。

| 要素 | Aの構成 | Bで再開した構成 |
| --- | --- | --- |
| model識別子 | spike-model-a | spike-model-b |
| instruction | initial instruction | resumed instruction |
| tool | tool-one | tool-two |
| hook | hook-one | hook-two |
| hookが読む設定 | note: prepared | note: changed |
| 実際のtool結果のnote | prepared:hook-one | changed:hook-two |
| source treeのbytes | 394372 | 394371 |

binaryはA／Bで同一、QuickJS bindingはv0.25.0、SQLiteドライバはv1.60.1。
構成ID・source fileごとのhash・実binary hashは[archive](s04-artifacts/configuration-archive.json)と各Workerの記録にある。
原fileを後から編集してもWorkerの使用構成と記録がずれないよう、captureしたbytesを保存・展開し、そのtreeを読み込ませた。

旧snapshotからの実行は保存bytesを使ったことの確認用で、通常再開の既定動作ではない。
通常の再開Workerは現在のsettingsから新しい構成を準備する。過去の会話に関連する旧構成は閲覧用に保持する。
API credentialは今回使っていない。一般的な利用環境全体や将来の全設定項目をcaptureしたとは判断しない。

## 必要範囲の読込み

会話windowはSession IDとposition cursorを条件に、採用markerのindexから逆順に指定件数を取り出し、
Goで選択範囲だけを古い順に並べる。turn pageはordinal、event pageはrun IDとsequenceを条件にしてSQLで範囲指定する。
先に全payloadを復元してから切り取る方式ではない。

| 通常実行の計測 | 増加前 | 増加後 |
| --- | ---: | ---: |
| 保存turn | 3 | 123 |
| 保存event | 120 | 4920 |
| 会話のSQL row読戻し・decode | 3 | 3 |
| 入力候補のmessage | 6 | 6 |
| messagesのJSON bytes | 388 | 388 |
| window読込み中のGo allocation差分 | 2488 bytes | 2488 bytes |
| window queryの観測時間 | 187 μs | 353 μs |

[増加前](s04-artifacts/history_small-inspection.json)、[増加後](s04-artifacts/history_large-inspection.json)に計測を保存した。
allocationは独立readerプロセスでwindow読込み前後のGo `TotalAlloc` 差分を測ったもので、
SQLite cache・VM・現在のsource展開を含む全プロセスのpeak memory／RSSではない。
絶対メモリ上限や性能SLAをこの結果から定めない。

会話queryはevent本文と構成asset bytesを参照しない。閲覧の全体件数はSQL集計で求め、本文をGoへ全件展開していない。
記録したquery planではadoptionsのSession index、turnsのID indexが使われた。
解釈には[SQLite公式EXPLAIN QUERY PLAN文書](https://www.sqlite.org/eqp.html)を参照した。
planの表示文字列は観測資料であり、特定のindex名を固定の受入条件にはしていない。

長いSessionに保持したHTTP本文には8KiBの観測用文字列を含めた。
会話には実tool結果から作った短い回答draftを採用し、詳細本文・内部hook／Go操作はeventとして残した。
この入力候補の大きさが一定だったのは今回の短い会話と指定3turnによるもので、
長い単一message、実モデルのtoken budget・要約・別projection方式は未確認。

## 検証と残る範囲

- 通常実行：5シナリオ成立。[summary](s04-artifacts/summary.json)、[SQLite](s04-artifacts/history.sqlite)。
- race detector付き実行：同じ5シナリオと120turn追加が成立、race報告なし。[summary](s04-race-artifacts/summary.json)。
- race付きでもwindowは増加前後とも3row・6message・388bytes。Go allocation差分は2600／2648bytesだった。
- `go vet ./...` 成功。両実行でSQLite `integrity_check` が成功した。

共有runtime・既存StoreのOpen／trace保存処理は変更せず、新しいSession用の表と操作を追加した。
S-03で見つけたmodule初期化の原例外が隠れる不具合は、この実験では修正していない。
今回使ったprepared ESMの経路ではblockingは観測しなかった。

未確認は、実LLM／Agent loopによるSession再開、TUIでの閲覧、長いmessageやtoken予算によるprojection、
同時編集・並行採用、全クラッシュ境界からの自動回復、旧DB移行、正式な構成schemaと配布条件。
SIGKILLによる採用境界の観測を、電源断・ディスク故障の実証とはしない。

S-04は今回の保存・閲覧・再開・構成追跡・範囲読込みまで成立した。
次はS-05で対象OSと起動条件を決め、QuickJS・SQLiteと外部tool／hook・依存を含む配布物を検証する。
