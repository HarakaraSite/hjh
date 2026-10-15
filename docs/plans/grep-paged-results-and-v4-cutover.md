# grepのページ処理と常用DBのv4切替

2026-10-15。利用者の「grepの改善、その後WALの設定とv3→v4」に基づく。

## 必要な動作・根拠・現行経路

セッション28224a97で、文字列glob `!*_test.go` が1文字ごとのrg引数となり、
ignore対象のdist配下まで検索された。limit 30でも、JS側でページ処理する前の
api.command結果がeffect_observationとして約5.20 MiB保存されていた。
公開grep → inspect.js → Host → native rg/GNU grep → host観測 → JSページ処理 →
公開tool結果、という順序を、Host内でページ処理してから観測・保存する順序へ変える。
検索全体のmatches/total、nextOffset、context、特殊なファイル名、rg優先/GNU代替を維持する。

常用DBはschema v3・DELETE mode、現行ソースはschema v4・WAL。
利用者が採用した「旧DBをbackupして新規作成」に従い、履歴変換は行わない。
設定・認証・モデル選択を維持し、既存履歴と旧バイナリを退避する。

## S1：grep

- globは単一文字列または文字列配列として扱う。
- rg/GNU共通のgrepCommand host操作がnative出力を解析し、指定ページ・総件数だけを返す。
  JSの旧rg全出力command経路と後段ページ処理は除去する。
- native全出力は一時メモリ内に留め、観測・DBへ渡さない。
  streaming化・独自出力上限・検索打切りは対象外。
- 実rg/GNUの既存検索、context・ページ・filename bytes確認を継続する。
  文字列globと大量matchの公開tool/host観測/保存結果を回帰確認する。

## S2：常用切替

- 安定候補で全体Go test、staticcheck、diff checkを一回実行し、新バイナリを別ディレクトリへbuild。
- Core/run等のDB接続が終了したことを確認し、DBと残存sidecar、旧バイナリ、extensionsを日時付きで退避。
- 常用のinspect.jsとgrep定義だけ更新し、他設定・credentialを変更しない。
- 新バイナリを配置し、同workspace・同endpointでCore起動。新規v4 DBのWAL設定を確認する。
- 旧backupのschema・integrity・件数、新DBのschema/WAL/初期状態、Core API、TUI接続を確認する。
  有料provider呼出しは不要。旧履歴は退避DBに残るが、新DBのsession一覧には出ない。

## 現在地

S1/S2完了。実rg/GNUによる文字列glob・context・ページ・filename bytesと、
app journal経由の大量検索保存を確認。全体Go test、staticcheck、diff check成功。
常用Core/TUIを新バイナリで起動し、v4/WALと空のsession一覧を確認した。
[結果](../results/grep-paged-results-and-v4-cutover.md)を参照。
