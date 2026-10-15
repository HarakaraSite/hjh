# Web取得の既定件数と本文上限

2026-10-15。利用者はWeb検索の既定上限を明示し、web_fetchの通常本文上限を
64 KiBへ下げることを選択した。

## 動作と現行経路

web_search → web-search.jsの既定値/明示引数 → webSearch Host → 認証HTTP → Exa。
現状numResultsは未指定でExa既定に委ねている。公式APIは既定10・最大100件を案内する。
利用者は「Exaは無制限と思っていた」と説明した。件数を増やさず、
hjhも省略時10を明示して従来の取得件数を維持する。
明示されたnumResultsは維持し、hjh独自の拒否・結果の切詰めは追加しない。
参照：https://exa.ai/docs/reference/search 、https://exa.ai/docs/search/quickstart 。

web_fetch → index.js → webFetch Host → webfetch.Fetch。
通常はraw受信本文を1 MiBまで読み、HTML変換等の後に返却・保存する。
save_toは別分岐でresponse全体をfileへ書き、metadataだけを返す。

## 変更

- 通常本文のread上限を64 KiB（65,536 bytes）へ変更する。
  既存のtruncated表示・30秒deadline・save_to全体保存を維持する。
  文字変換後の文字数上限や、metadataを含む返却JSON全体の上限は追加しない。
- モデル向けdescription/利用案内と現行design/機能比較を更新する。
- Exa既定件数10をJSに設定し、モデル向け説明・実HTTP要求の確認を揃える。
- 利用者の「web searchをfetchがわりに使われたくない」に従い、モデル向けcontents引数を除去する。
  内部要求はtext:false・highlights.maxCharacters:1000へ固定する。既知URLの本文読取りは
  web_fetchへ案内する。検索モード・filter・合成outputなど他の既存機能は維持する。
  contentsが渡されても、内部の固定設定を上書きさせない。独自の入力拒否は加えない。
- 既存の実HTTP上限試験を64 KiBへ更新し、save_toとrun_jsからの公開経路を確認する。
  Exaはlocal応答を使い、既定値と明示指定の保持を確認する。外部有料APIは呼ばない。
- 局所変更のためfocused testとdiff checkを使用する。確認後、既存v4/WAL DBを維持して
  常用binaryと対象extension定義を更新する。稼働中のrunは中断しない。

## 現在地

完了。web_fetchは64 KiB、検索は既定10件・本文要求なし・抜粋1,000文字へ変更。
関係4packageのtestと対象staticcheck、実Core/CLI/local HTTP、通常TUI接続を確認した。
通常binary/extensionへ反映し、v4/WAL DBと設定を維持した。
[結果](../results/web-retrieval-defaults.md)を参照。
