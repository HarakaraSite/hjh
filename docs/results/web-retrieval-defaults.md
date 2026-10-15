# Web取得の上限と検索/読取りの分離

2026-10-15。[計画](../plans/web-retrieval-defaults.md)を完了した。

## 採用した動作

- web_search：省略時numResults:10を明記する。明示された件数は優先する。
- web_searchのモデル向けcontents引数を除去した。内部要求は
  text:false・highlights.maxCharacters:1000へ固定し、全文取得を要求しない。
  引数contentsが渡されても、固定設定は上書きしない。
  任意の検索mode、filter、合成output等の既存機能と未知response fieldは維持する。
- web_fetch：通常受信本文の上限を1 MiBから64 KiB（65,536 bytes）へ変更した。
  超過時はtruncated:trueと[body truncated]を示す。
  上限はHTML変換前の受信bytesに適用し、metadataとmarkerは別途付加する。
  save_toは全体をfileへ保存し、metadataだけ返す。
- 指示は「検索で出典を探す→web_fetchで本文を読む」とした。
  長いページはsave_toで保存し、read_fileの行範囲を使う。

利用者はExaの件数が無制限と想定していたが、[公式API](https://exa.ai/docs/reference/search)では
省略時10件・公開上限100件。100件を既定にする変更は採用せず、従来の件数を維持した。
[公式Contents案内](https://exa.ai/docs/contents/quickstart)は抜粋の固定長指定に
highlights.maxCharactersを用いるとしている。
これはExaへ要求する件数/抜粋長であり、検索応答全体のbyte上限ではない。

## 確認

既存の実HTTP試験で通常本文を正確に65,536 bytesへ切詰めることと、
1 MiB超の元responseをsave_toで全体保存することを確認した。
HTML可読化、非textのmetadata、redirect、30秒deadline、取消も維持した。
直接toolとrun_js経由の標準tool確認に成功した。

Exaはlocal transportで要求を確認し、直接/子の省略値10、生成JSの明示値2を確認した。
直接/run_js/子のいずれもtext:false・highlights.maxCharacters:1000であり、
以前のcontents指定も固定設定を上書きしない。モデルへ渡るschemaにもcontentsはない。
以前の全文/beta指定の試験は、この新しい検索用途に対応する確認へ更新した。
結果のURL/抜粋/未知fieldと合成output、認証非露出、失敗詳細、単一本文保存は維持した。

- focused web_fetch/web_search/標準tool test成功。
- `go test ./internal/app ./internal/extensions ./internal/host ./internal/jsruntime -count=1` 成功。
- 対象app/extensions/host/jsruntime/webfetchのstaticcheck成功。
- `git diff --check` 成功。全体gateは局所変更のため繰り返していない。
- rootが要求の既定値/上書き順、モデル向けschema/指示、通常読取りとsave_toの分岐を確認した。
  独立reviewは実施していない。

実Core＋接続CLIからrun_jsで検索とweb_fetchを実行した。
local検索要求のnumResults:10と固定contentsを確認し、150,017 bytesのtext応答を
通常読取りでは65,536 bytesへ切詰め、save_toでは150,017 bytes全体を保存した。
モデルへの結果・ファイルの完全一致・DB integrity ok/schema4/WALを確認した。
有料API呼出しはない。使い捨てextensionで検索endpointのみlocal URLへ置き換えた。
証跡：`/tmp/hjh-web-discovery-ox5i5g5i/core-result.json`、`check-core.py`。


## 常用反映

新binaryとweb-search.js、web_search/web_fetchの定義・指示を反映した。
Coreにactive runがないことを確認して再起動し、通常TUIのready表示・正常終了を確認した。
Core ID：`64ed2403-212d-4428-ba45-7c3bfe3d08c5`。
v4/WAL DBは保持し、session/execution件数は切替前後同じ。
他のconfigとselection.jsonは保持した。
backup：`/home/agent/backups/hjh/20261015T015717Z-web-discovery/`。
証跡：`/tmp/hjh-web-discovery-ox5i5g5i/cutover.json`、`normal-tui.ansi`。
commit/pushは未実施。
