# Go＋QuickJSスパイク（S-01〜S-05）

固定の生成JS役コードから、外部ES moduleのツールを呼び、GoのHTTP・OSコマンドへ接続する実験。
実験用bindingは `modernc.org/quickjs v0.25.0`。製品での採用やAPI契約は未決。

- [計画](../../docs/spikes/s01-plan.md)
- [結果と成立範囲](../../docs/spikes/s01-results.md)
- [初回の観測記録](../../docs/spikes/s01-artifacts/summary.json)

S-02はこのGo moduleの実行経路を共有し、SQLite保存先を追加している。
[S-02の再現手順](S02.md)と[結果](../../docs/spikes/s02-results.md)を参照する。
S-03ではJSライブラリ、GoのファイルAPI、外部hookと依存配布を追加した。
[S-03の再現手順](S03.md)と[結果](../../docs/spikes/s03-results.md)を参照する。
S-04ではSession、会話採用、構成snapshot、別プロセス再開と範囲読込みを追加した。
[S-04の再現手順](S04.md)と[結果](../../docs/spikes/s04-results.md)を参照する。
S-05ではLinux/amd64の静的本体と外部sourceを配布し、空rootから起動・編集反映・Session再開を確認した。
[S-05の再現手順](S05.md)と[結果](../../docs/spikes/s05-results.md)を参照する。

## S-01の再現

このディレクトリで実行する。Go 1.27以降とLinuxが必要。
実験のコマンド呼出しには `sh`、`tr`、`sleep` を使う。HTTPはGo標準ライブラリで実行する。
初回buildではGo moduleの取得にネットワーク接続が必要。シナリオのHTTP通信はlocalhostで完結する。
モデルAPIは呼び出さない。

```sh
go build -o ../../dist/s01 ./cmd/s01
../../dist/s01 -out ../../dist/s01-observations
```

`-tools` で外部JS moduleのディレクトリを指定できる。既定値は `tools`。
外部編集のシナリオは一時コピーを編集し、一つの実行ファイルから新しいWorkerを起動して変更を確認する。
リポジトリ内の `tools/index.js` を書き換えるものではない。

各シナリオは実際の結果を観測し、期待する動作と照合する。一つでも不成立なら終了コードは非ゼロ。
生成したJSONLと `summary.json` は `-out` で指定した場所に保存する。
JSONLのIDは各ファイル内で有効であり、製品全体のidentity方式を定義しない。

並行処理の確認に使ったコマンド：

```sh
go vet ./...
go build -race -o ../../dist/s01-race ./cmd/s01
../../dist/s01-race -out ../../dist/s01-race-observations
```

## 構成

| 場所 | 責務 |
| --- | --- |
| `internal/trace` | 親子関係、開始・終了、保存先Sink、JSONL保存・読戻し |
| `internal/host` | HTTP、コマンド実行、contextによる中断と終了待ち |
| `internal/jsruntime` | VM所有、module読込み、登録ツール呼出し、非同期Promiseの接続 |
| `internal/experiment` | HTTPサーバー、動作の観測と照合、成果物の生成 |
| `tools/index.js` | ソース編集できる外部ツール |
| `cmd/s01` | 実験を起動するCLI |

S-03のhook選択は `jsruntime.RunWithOptions` に渡す。S-01／S-02はhookなしで同じruntimeを使う。

VMと外部tool・生成JSは同一Worker内で共有する。Worker間は別VMで、同時実行できる。
HTTP・プロセス処理はgoroutineで進め、Promiseの完了処理はVM所有goroutineで行う。
Workerはcontextを停止し、Go処理と割込み監視の終了を待ち、VMを閉じてから戻る。

実験の5秒watchdogと2秒の終了観測窓は、未終了を実験失敗として検出するためのもの。
製品の実行時間やキャンセル時間の契約ではない。
