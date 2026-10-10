# S-01スパイク結果

実施日：2026-10-10。今回合意した実験範囲では、**生成JS → 外部JSツール → GoのHTTP・OSコマンド → JSへの結果返却は成立した**。
例外伝播、同期・非同期JSの割込み、HTTP・プロセスのキャンセルと終了、外部ソース編集の次Workerへの反映も確認した。
製品のbinding採用、API、プロセス配置を確定する結果ではない。

## 実行条件と証拠

- 環境：Linux/amd64、Go 1.27.2。
- binding：`modernc.org/quickjs v0.25.0`、engine：QuickJS `2026-06-04`。
- 実験コード：[spikes/s01](../../spikes/s01/README.md)。依存のversion・checksumは `go.mod` と `go.sum` に固定した。
- 通常buildの実行：[summary.json](s01-artifacts/summary.json)。2026-10-10 13:18 JSTに実行。
- race検出付きの実行：[summary.json](s01-race-artifacts/summary.json)。同日13:18 JSTに実行。
- 各summaryと同じディレクトリに、シナリオ別の親子ID付きJSONLを保存している。

通常build・race検出付きbuildのいずれも16の実験シナリオが期待する動作を満たし、終了コード0。
race検出の報告なし。`go vet ./...` も終了コード0。
シナリオ数を成功基準としたものではなく、合意した動作に対応する観測を行った。

## 観測結果

| 動作 | 実際に観測したこと | 証拠 |
| --- | --- | --- |
| HTTP結果の加工とコマンドへの連鎖 | HTTPのJSONから配列を取り出し、JSで結合して `tr` へstdinとして渡した。集約結果は `RED\nBLUE`、exit code 0。 | [result_chain](s01-artifacts/result_chain.jsonl) |
| 外部ツールから別の登録ツールを呼ぶ | `pipeline` が `http` と `command` を呼んだ。内部ツールのparentはpipeline、Go処理のparentは各ツールだった。 | [nested_tools](s01-artifacts/nested_tools.jsonl) |
| Promise.all | 2つのHTTP handlerが同時に存在した。両方が到着するまで応答しないサーバーから結果を返せた。 | [parallel_http](s01-artifacts/parallel_http.jsonl) |
| JSと外部ツールの例外 | 生成JSの例外と外部ツールの例外が、メッセージを保持してWorkerの失敗として返った。 | [js_exception](s01-artifacts/js_exception.jsonl)、[tool_exception](s01-artifacts/tool_exception.jsonl) |
| HTTP失敗 | HTTP 503のstatus/bodyと、実際の接続失敗を生成JSのcatchで扱えた。statusの扱いはGo本体ではなく外部JSツールが決める。 | [status](s01-artifacts/http_status_failure.jsonl)、[transport](s01-artifacts/http_transport_failure.jsonl) |
| コマンド失敗 | 終了コード7とstderr `bad` が外部ツール経由で生成JSのcatchへ届いた。 | [command_failure](s01-artifacts/command_failure.jsonl) |
| 途中の成功とその後の失敗 | コマンドがファイルを書いた後、生成JSを失敗させた。ファイル内容 `persisted` を読み戻し、成功したコマンドの記録と親の失敗を別々に読めた。 | [partial_failure](s01-artifacts/partial_failure_evidence.jsonl)、summaryの `sideEffectReadback` |
| 同期JSのキャンセル | ループ開始を観測してからキャンセルし、Workerが終了した。 | [cancel_sync_js](s01-artifacts/cancel_sync_js.jsonl) |
| await後のJSループのキャンセル | HTTP完了後のPromise継続内でループを開始し、割込みでWorkerが終了した。 | [cancel_async_js](s01-artifacts/cancel_async_js.jsonl) |
| HTTP待ちのキャンセル | サーバーで受信を観測してからキャンセルした。Goリクエストの終了とサーバー側request contextの終了を確認した。 | [cancel_http](s01-artifacts/cancel_http.jsonl)、summaryの `serverRequestContextEnded` |
| コマンドと子プロセスのキャンセル | shellとsleepの起動を観測してからprocess groupを停止した。`Wait` が戻り、親子PIDはどちらも `/proc` から消えた。 | [cancel_process_group](s01-artifacts/cancel_process_group.jsonl)、summaryのPID・終了状態 |
| 編集の次Workerへの反映 | 外部ソースの一時コピーを編集し、同じバイナリ内の次Workerで `revision-one` → `revision-two` を観測した。再buildは行っていない。 | [before](s01-artifacts/external_edit_before.jsonl)、[after](s01-artifacts/external_edit_after.jsonl) |
| Worker内のVM共有 | 外部moduleのcounterが同一Worker内で1 → 2になった。 | [vm_shared_state](s01-artifacts/vm_shared_state.jsonl) |
| Worker間のVM分離と同時実行 | 別goroutineの2WorkerがHTTPを同時実行した。各Workerのglobalとmodule counterは独立し、それぞれcounterは1だった。 | [worker-A](s01-artifacts/vm_isolation_worker-A.jsonl)、[worker-B](s01-artifacts/vm_isolation_worker-B.jsonl) |

キャンセルを行うシナリオは、対象処理の開始観測から50ms後にキャンセルを送る。
通常buildでのWorker全体の所要時間は同期JS・非同期JS・HTTPが51ms、コマンドが55msだった。
これは起動や50msの待機を含む実行時間であり、キャンセル要求から終了までのレイテンシだけを測った値ではない。
通常build・race検出付きbuildとも、全Workerで終了後のGo処理数は0だった。

## 履歴の足場

JSONLには実行コードとinput、内部tool・Go処理の開始、引数、結果、失敗・キャンセル、親子IDを保存した。
集約結果はsummaryと親executionの終了記録に置き、内部呼出しの結果は別eventとして残した。
JSのcatch/finallyが割込みによって実行できない場合も、Go側が未終了のツール記録をcancelledへ収束させる。
成功済みの処理と副作用を、親の失敗に合わせて成功記録から消すことはない。

ファイルを閉じた後に読み戻し、親の存在、event順序、開始・終了の対応を確認した。
IDは各JSONL内で有効。SQLite、crash時の耐久性、全使用構成の保存、会話への採用、Session再開は未検証。

## binding調査と今回の選択

| 候補 | 確認した範囲 | 今回の扱い |
| --- | --- | --- |
| [modernc.org/quickjs](https://pkg.go.dev/modernc.org/quickjs@v0.25.0) | pure Go、module loader、Promise capability、pending job処理、非同期Interruptの公開APIと取得ソースを確認。今回の実行経路を実測した。 | 実験用に使用。本採用は未決。 |
| [fastschema/qjs](https://github.com/fastschema/qjs/tree/v0.0.6) | `v0.0.6` のAPI・取得ソースを確認。QuickJS-NG/Wazero、SetAsyncFunc/Await、contextによるWasm終了の接続がある。 | 比較候補。実行・性能・同じシナリオでの成立は未確認。 |

modernc版は、Go側でVM所有・Promise完了・contextを接続する構成を直接検証できるため、今回の実験に選んだ。
他候補より性能や品質が優れていると判断したものではない。

接続時に確認した契約と制約：

- VMはgoroutine間で同時操作できない。Interrupt以外の操作はVM所有goroutineで行い、Go処理の結果だけをchannelで戻す。
- `RegisterFunc` の複数返り値はJSの配列になる。今回の `(Value, error)` は接続用JSで明示的に取り出し、非同期失敗はPromise rejectionへ接続した。
- `Eval`、`Call`、pending job実行は割込みflagを入口でリセットする。今回の監視はキャンセル後に割込みを繰り返し要求し、監視終了を待ってからVMを閉じる。
- JSのPromise jobsは自動では進まない。embedding側でpending job処理を呼ぶ必要がある。
- bindingのpending job処理には、1回のdrainで `1 << 20` jobsの上限がある。今回の負荷はこの上限に達していない。製品での大量job処理やbinding変更の必要性は未評価。
- Go側のHTTP・コマンドにはcontextを別途接続する必要がある。VM割込みだけでGo処理は停止しない。
- プロセス停止はLinuxのprocess groupを用いた。同じgroup内のshell・sleepは終了を確認した。別groupへ離れる子プロセスや他OSの停止方法は未検証。
- VM値・Promise capabilityは明示的な解放が必要。Worker終了時はGo処理を待ってから未解放の値とVMを閉じる。

## S-01〜S-05の到達点

| 項目 | 今回の到達点 | 残る確認 |
| --- | --- | --- |
| S-01 | **成立：今回のLinux・同一プロセス・WorkerごとのVM構成と固定JSでの検証範囲。** | 実Agent生成コード、多種類の実ツール、大量データ、長期運転、別プロセス構成は未確認。 |
| S-02 | 一部成立：親子呼出しと途中の成功・失敗・中断をJSONLで保存・読戻しできた。 | SQLiteでの保存、異常終了時の観測境界・残った副作用の扱い。 |
| S-03 | 一部成立：外部ES moduleの読込み、Go APIへの接続、ソース編集の次Worker反映。 | 実際のJSライブラリ、相対依存・package取得・配布、hookの編集反映。 |
| S-04 | 未確認。 | 会話採用、構成追跡、Session閲覧・再開、履歴増加時の読込み範囲。 |
| S-05 | 未確認。 | 対象OSの合意、QuickJS・SQLiteと外部依存を含む配布物での起動・編集反映。 |

次はS-02の保存経路を具体化し、正常終了・失敗・中断と副作用の証拠をSQLiteへ残して読戻す実験に進める。
bindingと編集可能範囲の本採用は、残るスパイクの結果も使って利用者が判断する。
