# 履歴DBの重複索引2本の削除

2026-10-14。[計画](../plans/history-redundant-indexes.md)の実装・確認完了。
WAL・初出位置改善の候補に追加した。常用DB/Core/dist・backupは未操作。

## 変更

- semantic_records_execution_ordinalを削除し、UNIQUE(execution_id, ordinal)由来の索引を使う。
- execution_display_positions_executionを削除し、execution_id UNIQUE由来の索引を使う。
- 新規DDLは23 tables・9 explicit indexes。schema versionは4、制約と保存内容は維持する。
- 既存v4はwritable Openでschema検証/WAL設定後にこの2本だけDROP INDEX IF EXISTSする。
  read-only Openは削除しない。新規DBには不要なDROPを発行しない。
- DROPで空いた既存DBの領域はSQLiteが再利用する。file縮小用のVACUUMは実行しない。

## 測定根拠

実装前の一時source比較で同じ2本だけを新規DDLから削除した600回local HTTP実行を確認した。
process累積書込みは390.92→342.40 MiB（約12.41%減）、閉じた後DBは12.39→11.85 MiB。
時間は6.56→6.67秒で短縮を観測していない。記録10,199・message1,200、integrity ok・FK違反0。
通常のexecution別ordinal順検索はUNIQUE由来の索引を使用した。
[生値](history-redundant-indexes-data.json)。同一約4KiB結果の反復試験で、物理disk IOや長期利用の削減率ではない。
今回の新規DBのhot pathはこの比較候補と同じ。既存DBのOpen時削除は別のfocused testで確認した。

## 確認

history testで旧v4の索引2本を持つ一時DBを作り、read-only Openでは残ること、
writable Openで削除されること、履歴ID/順序/本文が一致することを確認した。
別record IDで同じexecution/ordinalを入れる操作と、別表示番号で同じexecutionを入れる操作が
UNIQUE制約で拒否され、通常の履歴追加がその後も成功した。
schema照合・既存history package testも成功した。

全体`go test ./internal/... ./cmd/... -count=1`とhistory/app対象staticcheck
（`v0.7.0-0.dev.0.20261009230814-452d5bb86b45`）、diff checkが成功。
新しいDDL/Open変更を理由に、安定候補の全体testを一回実行した。

実binaryとlocal HTTPのCore/CLI/PTY TUIで15回のmodel呼出しを実行し、
並列二子status/steer/collect、最新snapshotのmemory→saved、TUI復元、
Core再起動後のsession続行、取消、失敗recall、budget超過とsession削除/本文回収を確認した。
外部API利用なし。資料は`/tmp/hjh-redundant-index-verification-aduaf3xv`、
gate logは`/tmp/hjh-redundant-index-{full-test,staticcheck}.log`。

自己reviewでは新規/既存/read-onlyのOpen、v4検証前の非変更、削除対象が2本だけであること、
UNIQUE制約と既存の検索caller、保存・復元・削除を確認した。
独立reviewの追加は行っていない。実provider・実device IO・異なる結果の長期利用は未確認。
