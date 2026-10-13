# CLIからのセッション削除

機能比較27番について、利用者が指定した「まずCore停止後のローカルDB削除だけ」を実装した。
TUIの削除操作とCoreの削除APIは後回しとする。

```sh
./dist/hjh sessions
./dist/hjh sessions --json
./dist/hjh core stop --connect http://127.0.0.1:8765
./dist/hjh sessions delete SESSION_ID
./dist/hjh sessions delete --db /path/to/history.sqlite3 SESSION_ID
```

一覧は既存の`hjh sessions`を維持し、`hjh sessions list`も使える。
削除は完全IDまたは一意な短縮IDを受け、成功時は短縮IDを表示する。
通常の削除commandは指定されたSessionを直接削除し、追加の確認promptは挟まない。
対象DBを使う登録済みCoreが応答する場合は、停止commandを案内して削除を中止する。
利用者は同じDBを使うCoreとstandalone runを先に停止する。

## 保存データの扱い

history.Storeが一つのSQLite transactionで、Session・会話・turn・model変更と、
session correlationまたはcanonical attributionに属する実行、その子孫を削除する。
messages・semantic records/relations・診断・context・表示位置などは既存FKのcascadeで削除する。
実行を参照するrecall relation、保存cursor、command/入力receipt、follow-up receiptも除去する。
実行未開始の入力receiptはtargetのsessionIdから特定する。
schema変更・既存DBのmigrationは行わない。

2026-10-13、利用者指摘により、対象Sessionの本文・構成の回収不足を修正した。
削除前に対象の実行・メッセージ・詳細記録・診断・入力receipt・使用構成から本文と構成の候補を集める。
実行群の削除後、残存実行が使わなくなった候補のconfigurationを削除し、他の記録から参照されなくなった
候補のcontentsを削除する。すべて同じtransactionで行う。別Sessionの会話・実行・入力と、それらが
参照する本文・構成は維持する。旧形式のsteering受付JSON内にある本文参照も検査するが、
新形式のsteering受付のcontentDigestはchecksumなのでcontents参照としては扱わない。

回収は対象Sessionから辿れた候補だけに限定し、無関係な未参照データを一括削除しない。
VACUUMは実行しないため、ファイルは自動では縮小しないが、削除領域は再利用できる。
認証情報・workspaceの設定・実際の作業fileは削除対象に含めない。

## 確認

- 一時DBで親・子・孫と別Sessionの会話を保存し、入力receipt・follow-up・cursor・
  双方向のrecall参照を含む対象Sessionの削除を確認した。
- 削除対象のSession/実行と固有本文・構成が取得できなくなり、他Sessionの会話と共有config/本文が
  残ること、失敗した親実行も回収されること、SQLiteのforeign_key_checkが正常であることを確認した。
- 最後のSession削除で共有本文・構成も回収されること、残存follow-upと旧steering受付だけから
  参照する本文が保持されること、新形式の受付本文が独立して読めることを確認した。
- 対象Sessionと無関係な未使用構成は回収対象にしないことを確認した。
- CLIで短縮ID削除、削除後の一覧、対象不在の表示を確認した。Coreが応答する場合の中止と、
  停止後の削除もローカルHTTPを使って確認した。
- 実データの削除は実施していない。
- history/CLIのtest・vetとdiff checkが通り、両binaryを更新した。実binaryでも一時DBの
  一覧 → 短縮ID削除 → 他Sessionが残る一覧を確認した。
- 本文回収の修正後も、更新した`dist/hjh`で一時DBの2セッションと子実行を削除した。
  contentsは12件 → 1セッション削除後5件 → 最後の削除後0件、configurationsは2件 → 1件 → 0件。
  途中では残したSessionの依頼・ツール結果をJOINで読み出せ、foreign_key_checkも正常だった。
