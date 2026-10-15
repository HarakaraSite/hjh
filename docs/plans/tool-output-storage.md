# Git・編集・Web検索の内部全文保存を減らす

2026-10-15。利用者の「3つのツールを改善。run_jsは大丈夫？」に基づく。

## 必要な動作・実行証拠

一時環境で公開tool → JS → Host → native/HTTP → host観測 → journal → DB → 次のモデル入力を確認した。
- git_inspectはlimit 2で公開結果153 bytesに対し、ページ前のcommand結果192,356 bytesを保存した。
- edit_fileは186,011 bytesのファイルの1か所置換で、内部read結果189,089 bytesとwrite入力189,045 bytesを保存した。
- web_searchは160,101 bytesの検索結果に対し、HTTP wrapperも含め結果本文320,244 bytesを保存した。
- run_jsはreturn値のみを親モデルへ返すが、内側で明示的に呼んだtoolは別の実行証跡を持つ。
  したがってread_file全文を読んでreturnで一行だけ返しても、元のread_file結果は残る。
  JSローカル変数そのものをsnapshot保存する仕組みはない。

目的は各toolの公開動作・有用な実行記録を維持し、実装内部の全文保存を減らすこと。
精密な開発記録、DB schema変更、過去データの変換、一般的な出力制限は対象外。

## S1：git_inspect / edit_file

- git_inspectの既存Git引数構成はJSに残し、専用Host操作でnative実行・ページ処理を完結する。
  status/diff/showは全件数を維持。logは既存skip/max-countによる継続を維持する。
  pathsの単一文字列を一要素として扱い、公開schemaにも明記する。
- edit_fileは専用Host操作で読取り・順次の文字列全置換・一回保存を完結する。
  観測する入力は既存のpath/edits、出力は置換件数・bytes。編集前後の全文を内部観測へ渡さない。
- 既存の直接呼出し/run_js経由の編集・Git各op、staged/stat/context、ページ継続を確認する。

## S2：web_search

- JSは従来どおりExa入力を構成する。専用Host操作が既存のcredential付きHTTP dispatcherを内部利用し、
  status/JSON/resultsを確認する。未知のレスポンスfieldを含む公開検索結果は維持する。
- HostからJSへの結果とHost観測値を分ける小さなresult型を設ける。
  検索本文は公開tool結果へ保存し、Host観測はstatusとresultCountに留める。
  一般HTTP操作の観測契約は変えない。応答JSONの字句表現差による二重保存にも依存しない。
- 既存のExa error詳細、credential非露出、cancel、直接/run_js/子からの呼出しを確認する。

## S3：保存と実利用の確認

- app経由の一時DBで、ページ外のGit本文・編集前後の全文・HTTP raw wrapperが保存されないことを確認する。
  run_jsで集約/絞込みした時にも元toolの結果を保持する現行動作を確認し、説明へ反映する。
- focused testと差分確認の後、安定候補で全体Go test・staticcheckを一回実行する。
- 通常環境へはDBを維持したまま最新バイナリ・関係するextensionだけを反映する。
  旧バイナリ/extensionをbackupし、Coreに実行中の依頼がない時に再起動してTUI接続を確認する。
  有料provider呼出し・commit/pushは含めない。

## 現在地

S1–S3完了。focused test、全体Go test、staticcheck、自己reviewと差分確認に成功。
一時DB比較・実Core/CLI/local HTTPのrun_js実行で3ツールの保存削減を確認した。
通常バイナリとextensionsを更新し、既存v4/WAL DBを維持してCore/TUIを確認した。
[結果](../results/tool-output-storage.md)を参照。
