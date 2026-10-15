# Git・編集・Web検索の保存改善結果

2026-10-15。[計画](../plans/tool-output-storage.md)のS1–S3完了。

## 変更

- git_inspect：専用Host操作でGitの実行とページ処理を完結する。
  status/diff/showのtotalとnextOffset、logのskip/max-count方式を維持した。
  pathsは単一文字列または配列。文字列の1文字ごとの展開を解消した。
- edit_file：専用Host操作で読取り・順次全置換・一回保存を行う。
  編集前のread結果と編集後のwrite引数として全文を記録する経路を除去した。
  入力edits、各置換件数、合計、書込bytesと公開動作は維持した。
- web_search：JSはExa入力を構成し、Hostが既存の認証dispatcherを使ってHTTPを実行する。
  本文は公開tool結果へ保存し、Hostはstatus/resultCount/responseBytesだけを観測する。
  原HTTP wrapperは保存しない。未知のresponse field、error/tag/requestId、認証経路は維持した。
  JSONのescapeや表現差があってもHost側にもう一本の本文を持たない。

## 保存量の比較

同じ一時ファイル・同じtool入力を、変更前後のapp実行で比較した。
DBのsemantic_recordsが参照するHost/public結果本文は同じIDを一回として数え、
内部Host開始記録のpayload bytesを加えた値。最新model要求本文や会話envelope、
index、SQLite page等を含むDB全体の比較ではない。

| 操作 | 変更前 bytes | 変更後 bytes |
| --- | ---: | ---: |
| git_inspect show、limit 2 | 192,882 | 688 |
| 約186 KBのファイルを1か所edit | 378,724 | 385 |
| 約160 KBのweb_search応答 | 320,706 | 160,610 |

Gitの公開結果は153 bytesのまま。単一文字列pathsでも同じ結果になる。
Web検索の結果本文だけでは320,244 → 160,154 bytes（検索本文160,101＋Host summary53）。
文字・行の長さやデータ内容で数値は変わる。
Gitのnative全文と編集対象全体は処理中のメモリには存在する。

変更前証跡：`/tmp/hjh-tool-output-audit-u2vhw4nn/report.json`。
変更後証跡：`/tmp/hjh-tool-storage-after-jd0v4l3k/report.json`。

## run_js

直接呼出しとrun_js経由でGit/編集を確認した。
Web検索は直接、run_jsで全文return、run_jsでtitleだけreturnの3経路を確認した。
HTML文字・日本語・U+2028を含む応答でも、contents内の検索本文は各executionで一件だった。
検索を絞ってreturnしても内側の検索本文は証跡として残り、親モデルへはtitleだけが渡る。

read_file全文をrun_jsで読んで一行だけreturnする試験も行った。
親モデルに渡るのは一行だけだが、内側のread_file結果は履歴に残る。
JSローカル変数を自動保存しているのではなく、明示的tool呼出しの結果記録による。
最初からread_fileへoffset/limitを指定すると、その範囲だけが記録される。
run_jsの集約結果そのものを変形した場合、各tool結果とは別の本文として保存され得る。

## 検証

- focused app/jsruntime test成功。既存の順次編集・空文字削除・CRLF維持、
  Git各opとstaged/stat/context/log継続、Exa失敗詳細とcredential非露出・子agent呼出しも成功。
- `go test ./internal/... ./cmd/... -count=1` 成功。
- `go run honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45 ./internal/... ./cmd/...` 成功。
- `git diff --check` 成功。
- 独立reviewは未実施。rootがHostの観測/返却値、通常・終了時completion回収、
  JSの公開結果、保存本文の所有、既存呼出し側との整合性を確認した。

実Core＋接続CLI＋local HTTP provider/検索応答でも、run_js内の3操作が完了した。
次のモデル要求に指定Gitページ・編集結果・検索titleが届き、Git/編集対象全文はcontentsに存在せず、
検索本文は一件だった。DB integrity ok、schema4/WAL。外部有料provider呼出しなし。
検索先のみ、使い捨てextensionコピーでExa URLをlocal endpointへ置換した。
証跡：`/tmp/hjh-tool-storage-after-jd0v4l3k/core-result.json`、`check-core.py`。

## 常用反映

旧バイナリ/extension backup：
`/home/agent/backups/hjh/20261015T013018Z-tool-output-storage/`。
Coreにactive runがないことを確認し、停止して新バイナリを配置した。
停止直後の上書きで実行fileがまだbusyだったため、別fileへcopyしてatomic replaceに変更した。
関係するindex.js/inspect.js/web-search.jsとgit_inspect定義だけを更新した。
他のconfigとselection.jsonは維持。DBは作り直していない。
通常TUIの自動Core起動・ready表示・正常終了とAPIを確認した。Coreは稼働状態。
schema4/WAL/integrity ok、切替前後のsession/execution件数は同じ0。
新Core ID：`c05f3648-051d-4530-9a62-61eefe6a40f0`。
証跡：`/tmp/hjh-tool-storage-after-jd0v4l3k/cutover.json`、`normal-tui.ansi`。
commit/pushは未実施。
