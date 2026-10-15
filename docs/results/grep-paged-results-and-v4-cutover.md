# grepのページ処理と常用DBのv4切替結果

2026-10-15。[計画](../plans/grep-paged-results-and-v4-cutover.md)のS1/S2完了。

## grep

文字列globを単一patternとして扱い、配列も維持した。公開schemaに両形式を明記した。
rgもGNU grepも、GoのgrepCommand内で解析・ページ処理してからhost観測へ返す。
以前のrg api.command全文観測とJSでの後段ページ処理を除去した。
総match数・全体件数・次ページ位置は検索全体から計算する。
native全出力は一時メモリに残るため、今回の削減対象は履歴への保存量である。

実rg/GNUを利用したapp実行で、12,001件hit・30行pageを確認した。
直接呼出しとrun_js内からの次ページ、files出力の2ページも確認した。
4ページの保存済みhost結果はrg 4,947 bytes、GNU 4,948 bytes。
ページ外の末尾markerと除外した_test.goの内容は、モデル入力にも記録にも残らない。
従来のcontext、ignore動作、特殊文字・非UTF-8ファイル名確認も成功。
これは今回の試験条件での結果であり、任意の行長・結果内容の保存量を保証する数字ではない。

## 常用環境

旧Coreは切替前に停止済み。fuserでDBを開いているprocessがないことを確認した。
バックアップ：`/home/agent/backups/hjh/20261015T011412Z-v3-to-v4/`。
DB本体と存在するsidecar、旧hjh/hjh-tui、extensions、selection.json、core.logを退避した。
コピーのhash・quick_check・件数を確認した後、元DBもoriginal-dbへ移動して保管した。

| 項目 | 旧DB（保持） | 新しい常用DB |
| --- | --- | --- |
| schema | 3 | 4 |
| journal | delete | wal |
| quick_check | ok | ok |
| session | 3 | 0（切替直後） |
| execution | 7 | 0（切替直後） |
| DB本体 | 28,397,568 bytes | 初期状態。WALと合わせて増加する |

旧履歴の変換は行っていない。旧sessionはbackupに保持し、新しい一覧には表示しない。
新Core IDは`c9f85cee-5e53-4518-b28a-4185f31322ac`、endpointは従来と同じ
`http://127.0.0.1:8765`、workspaceは`/home/agent/projects/hjh`。
通常のdist/hjh-tuiから自動Core起動し、ready表示・空session一覧を確認した後TUIを正常終了した。
Coreは利用可能な状態で残した。

常用inspect.jsはsourceと一致。設定のgrep以外とselection.jsonは退避前と同一。
API credentialやprovider設定は変更していない。
新バイナリには、直前に完成していた一回・2秒のprovider retryも含む。
証跡：`/tmp/hjh-grep-v4-cutover-z9_evgh5/cutover.json`、`normal-tui.ansi`。

## 検証

- `go test ./internal/app ./internal/jsruntime -run 'TestGrep' -count=1 -v` 成功。
- `go test ./internal/... ./cmd/... -count=1` 成功。
- `go run honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45 ./internal/... ./cmd/...` 成功。
- `git diff --check` 成功。
- 実Core API、通常TUIのPTY起動・終了、schema/WAL/integrity確認成功。有料provider呼出しなし。

独立reviewは今回実施せず、rootがJS→Host→観測→journalの経路とページ計算、
filename bytes、常用設定差分・退避順序を確認した。commit/pushは未実施。
