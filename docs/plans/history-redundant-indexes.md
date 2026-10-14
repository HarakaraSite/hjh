# 履歴DBの重複索引2本の削除

2026-10-14。利用者の明示指示により、次の2本を削除する。

| 削除する明示索引 | 代わりに使う既存の制約索引 |
| --- | --- |
| semantic_records_execution_ordinal | UNIQUE(execution_id, ordinal) |
| execution_display_positions_execution | execution_id UNIQUE |

## 必要な動作・現行経路

CLI run / Core / 停止後session削除はhistory.Open、新規DBは埋込みDDLを適用する。
既存DBはschema v4を検証する。CLI history/listのOpenReadOnlyはschemaを変更しない。
保存ごとに制約索引と同じ列の明示索引を更新しているため、明示索引だけを外す。
一時新規DBの600回試験では、WAL改善後のprocess書込み391→342 MiB、DB12.39→11.85 MiB。
検索はUNIQUE由来の索引を使い、保存件数・integrityを維持した。時間短縮は観測できなかった。
INDEXED BYでこの2本を指名するproduct callerはない。

## 実装・確認

1. 新規DDLから2本を削除し、explicit indexesを11→9へ変更する。
2. writable Openで検証済みの既存v4 DBも、同じ名前の2本だけDROP INDEX IF EXISTSで削除する。
   新規DBには不要なDROPを発行しない。read-only Openは削除しない。
   version・table・保存データ・UNIQUE制約は維持する。v3変換は追加しない。
3. schema照合、既存v4の読取り→書込み再open、記録の順序読取りと重複拒否、
   既存history/app/CLIのfocused testで保存・会話再開・削除を確認する。
4. 一時Core/CLI/TUIの実経路でrun・復元・続行・削除を確認し、結果へ記録する。

常用DB・dist・backupを直接操作しない。commit/pushは含めない。
未確認：実device IO、異なる結果が増える長期利用。既存の削減率を一般化しない。

## 現在地

実装・確認完了。新規DDLから削除し、既存v4のwritable Openでも削除する。
history focused test、全体Go test、history/app staticcheck、diff check成功。
15呼出しのlocal HTTP実Core/CLI/PTY TUIでrun・親子操作・復元・再起動続行・取消・
失敗recall・budget・削除を確認した。独立reviewは追加せず、変更経路を自己reviewした。
詳しくは[結果](../results/history-redundant-indexes.md)を参照する。
