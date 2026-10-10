# S-02結果：内部ツール実行履歴のSQLite保存

2026-10-10、Linux/amd64、Go 1.27.2で実施。
[計画](s02-plan.md)の範囲で成立した。S-01と同じ生成JS → 外部JSツール → Goの実処理を使い、
SQLiteへ保存した親子関係・結果・失敗・キャンセルを別プロセスから読み戻せた。
プロセス途中終了でも保存済み成功は残り、未COMMITの結果は保存された成功と区別できた。

実装は[共有Go module](../../spikes/s01/README.md)内の `cmd/s02`。
[再現手順とモジュール責務](../../spikes/s01/S02.md)を参照する。
今回の成立は技術的実現性の証拠であり、Agent製品としての受入やDB schemaの採用を意味しない。

## 実行経路と保存の責務

`internal/jsruntime` と `internal/host` は変更せず、`internal/trace` に保存先Sinkを設けた。
S-01はJSONL、S-02はSQLiteを使う。親子IDとevent順序はtrace、DB接続・保存・読戻しは
`internal/history`、停止境界と副作用の観測は `internal/historyexperiment` が所有する。

SQLiteにはrunとappend-only eventを保存する。call IDはrun内で有効とし、run IDと組み合わせる。
読み取りviewはeventから導出し、COMMITされた開始しかないcallは `pending` とする。
モデルへ返す集約結果と内部ツールの結果は、それぞれ親executionと子callへ保存する。

各eventを個別transactionでCOMMITし、設定はWAL、`synchronous=FULL`。
別プロセスreaderは読み取り専用接続で保存済みeventを読む。
WALにおける読み手・書き手とCOMMITの関係、FULLの同期については
[SQLite公式WAL文書](https://www.sqlite.org/wal.html)と
[synchronousの説明](https://www.sqlite.org/pragma.html#pragma_synchronous)を参照した。
今回のSIGKILL観測から電源断・ディスク故障時の耐久性まで実証したとは判断しない。

## 観測結果

最新の通常実行は[summary.json](s02-artifacts/summary.json)。すべてのviewは別プロセスで読み戻した。

| 実行 | 観測された動作・保存内容 | 記録 |
| --- | --- | --- |
| 正常な内部合成 | 親 → pipeline → HTTP／commandツール → Go処理の関係を保存。親の集約結果は `{"length":8}`、内部command結果は stdout `RED\nBLUE`・exit 0。12event。 | [view](s02-artifacts/success-view.json) |
| 副作用後の親失敗 | コマンドがファイルへ `persisted` を書いて成功後、親JSが失敗。親はfailed、内部コマンドはcompletedのまま。 | [view](s02-artifacts/failure-view.json) |
| コマンド失敗 | exit 7、stdout `before`、stderr `bad` をGo処理の構造化結果として保存。外部ツールと親はfailed。Go処理のcompletedは終了結果を取得できた意味であり、exit 0を意味しない。 | [view](s02-artifacts/command_failure-view.json) |
| 副作用後のキャンセル | ファイルの `before-cancel` と途中stdout `partial-output` を確認。host・tool・親はcancelled。Worker返却後のGo実処理は0、対象プロセスは `/proc` に存在しなかった。 | [view](s02-artifacts/cancel-view.json) |
| 2Worker並行記録 | 同じDBへ記録し、両runのrootがcall-1でも混ざらず、結果A／Bを別々に読み戻せた。書き込みは共有Storeの1接続で直列化する。 | [A](s02-artifacts/parallel-0-view.json)、[B](s02-artifacts/parallel-1-view.json) |
| 内部成功COMMIT後のSIGKILL | 実際の副作用 `crash-effect` が残り、host・toolのcompletedとstdout `command-finished` を保持。停止直後は親pending、明示的な状態追記後は親interrupted。5 → 6event。 | [停止直後](s02-artifacts/crash_committed-stopped-view.json)、[状態追記後](s02-artifacts/crash_committed-resolved-view.json) |
| host結果COMMIT前のSIGKILL | 実副作用 `crash-effect` は存在。host結果をINSERT済みでも、COMMIT前のため生存中・停止後のreaderには開始3eventしか見えない。状態追記後は内部callがoutcome_unknown、親interrupted。3 → 6event。 | [生存中](s02-artifacts/crash_uncommitted-live-view.json)、[停止直後](s02-artifacts/crash_uncommitted-stopped-view.json)、[状態追記後](s02-artifacts/crash_uncommitted-resolved-view.json) |

副作用は実際のファイルを読み、summaryに内容とファイル名を記録した。
DBが保存するのは呼出し引数と取得できた実行結果であり、外部世界の状態を自動的に収集・確定するものではない。
副作用ファイルと停止境界の `.gate.json` は同じ成果物ディレクトリに保管している。
gateは実験で観測した保存境界を示す補助資料であり、未COMMITの結果を保存履歴へ補完する根拠には使わない。

## 途中終了の意味

閲覧やDB再Openだけでは `pending` を書き換えない。生存中のwriterと、SIGKILL後で状態追記前のwriterについて、
readerは同じ保存済みeventを返した。PIDが保存されていても、readerによる死亡判定や自動復旧は行わない。

実験の所有者がwriterへSIGKILLを送り、`Wait` による終了を確認してから、そのrunだけに状態を追記した。
親executionは `interrupted`、保存済みの終了結果がない内部callは `outcome_unknown` とする。
既存の成功eventは変更せず、状態追記の理由には停止したPID・終了結果・Wait完了を残す。

外部処理の副作用と履歴のCOMMITは別の操作である。開始が記録されていても、結果が保存されていなければ、
履歴だけから成功・失敗・副作用の不在を確定できない。今回の未COMMIT実験は、その差を実際のファイルとDBで確認した。
自動再実行や、Session再開時の回復方針はこの実験では決めていない。

## 実行証拠と依存

- 通常実行：7シナリオすべて成立。[summary](s02-artifacts/summary.json)、[DB](s02-artifacts/history.sqlite)。
- race detector付き実行：同じ7シナリオが成立し、race報告なし。[summary](s02-race-artifacts/summary.json)、[DB](s02-race-artifacts/history.sqlite)。
- 共有traceの変更後、S-01を再buildして16シナリオすべて成立。再確認の出力は `dist/s01-after-s02/`。
- `go vet ./...` 成功。SQLiteの `integrity_check` も両実行で成功した。

SQLiteドライバは [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) v1.60.1、
実行時のSQLiteは3.53.4。PRAGMAの読戻しはjournal `wal`、synchronous `2`（FULL）。
QuickJS bindingは引き続き v0.25.0、共有依存のmodernc.org/libcはv1.77.1。
全Go依存は [go.mod](../../spikes/s01/go.mod) と [go.sum](../../spikes/s01/go.sum) に固定した。

SQLite module checksum：

```text
modernc.org/sqlite v1.60.1 h1:/blz53O951KWFOso4QQvEs/Fq6cDBKLtMVrYNSeJVKw=
modernc.org/sqlite v1.60.1/go.mod h1:1dIoEagfDE72QytD5scH1lxARtaUgKgHC/NuApA27r0=
```

通常実行の成果物DBには先行実行のrunも残っている。最新summaryとviewが指すrunを今回の結果として参照する。
既存runの削除や旧Henji DBの移行は行っていない。

## 成立範囲と次の検証

S-02は今回の実行証拠保存・親子関連・別プロセス読戻し・途中終了時の既知／不明の区別まで成立した。
stdoutはプロセス終了・キャンセル時に取得した内容であり、実行中の継続的なストリーム保存は未確認。
複数Workerの同一Store利用は確認したが、独立した複数writerプロセスの同時書き込み性能は測定していない。

使用構成の全保存、会話への採用、Sessionの閲覧・再開、長期履歴の取り出しはS-04で扱う。
次はS-03で具体的なJSライブラリを使うtool・hookを選び、必要なGo APIと、ソース編集・依存取得・配布の成立範囲を確認する。
