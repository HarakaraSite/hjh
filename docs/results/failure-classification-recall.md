# エラー分類とrecall案内の確認結果

2026-10-14。計画: [failure-classification-recall.md](../plans/failure-classification-recall.md)。

## 結果

エラーへ小さいdiagnosticを付け、model_requestの失敗記録、agent結果、終了記録、
Core結果、会話entityまで伝えるようにした。既存の詳細エラーは保持する。
通常TUIは種類別の短い英語を表示する。停止したrootの応答・通信・timeout失敗には
`try /recall`を付ける。HTTP・credential・budget・保存・取消・step limitには付けない。
子の通知には短い理由だけを付ける。unknownは従来の詳細表示を使う。

不完全なtool callは`response_error`として、ID/名前欠落を`incomplete_tool_call`、
不正な引数JSONを`invalid_tool_arguments`へ分け、fieldを記録する。
parserの受理条件、retry、timeout設定、recall方式は変更していない。
raw SSE全文の常時保存は追加していない。

## 実操作

使い捨てworkspace・schema v4 DB・local HTTP provider・実Core・PTY TUIを使用。
元のMiMo成功SSEから引数JSONを不正にし、歴史上の未完了本文を混ぜた再現応答を使った。
これはprovider側faultを仮定した再現であり、元セッションの原因が確定したという意味ではない。

| 操作 | 結果 |
| --- | --- |
| TUIから依頼して不正tool callで停止 | `FAILED · provider response invalid · try /recall` |
| 同じsessionを開き直す | 同じ短い通知を表示 |
| 引数なし`/recall` | 最新の停止rootをprepare |
| 次の依頼 | 未完了本文をreferenceとして渡し、正常完了・canonical採用 |
| 不正な構造化tool call | 自動replay・実行なし |
| HTTP 429 | `FAILED · provider request failed`。recall案内なし |
| 接続先停止・connection refused | `FAILED · provider connection failed · try /recall` |
| DB検査 | integrity_check = ok |

外部provider呼出し0回、local provider呼出し3回。
調査artifact: `/tmp/hjh-classified-failure-final-3v0idCvz/`。
`result.json`、PTY出力`failed-live.ansi` / `reloaded-recall-success.ansi` /
`http-failure.ansi` / `transport-failure.ansi`、使い捨てDBとCore logを保存した。
初回の調査scriptはreference内のJSON escapeを文字列のまま照合して失敗した。
DBに残ったreferenceを確認して内側のJSONをdecodeする照合に直し、新しい使い捨て環境で確認を完了した。

## Testとreview

S1はcontracts/protocol、S2はapp/history/Core/TUIのfocused testで確認した。
不正引数の詳細とdiagnosticがlive terminal・保存・replayで一致し、toolが実行されないことを確認。
短い通知のlive/reopen一致、二重表示防止、childにroot recall案内を付けないことも確認した。

独立reviewで次の4件を修正し、一回の限定再reviewで解消を確認した。

- OAuth credential再解決の失敗がtransport扱いになる経路。
- terminal保存失敗時、空runtime outcomeをdecodeしてCore結果の取得自体が失敗する経路。
- 終了時のinput control・receipt更新の保存失敗がprovider診断に隠れる経路。
- HTTP error bodyを読む途中の取消がHTTP分類になり、cancel identityを失う経路。

対応する実経路のregression testを追加した。取消は`errors.Is(context.Canceled)`を保持し、
実行履歴の保存失敗は`commit_error`を優先する。独立snapshot保存状態は既存の別通知を維持する。
再reviewで対応必須findingなし。

安定候補で次の全体確認を一回実行し、成功した。

```sh
go test ./internal/... ./cmd/... -count=1
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45 ./internal/... ./cmd/...
git diff --check
```

## 範囲

既存DB schemaの変更・migrationはない。以前のdiagnosticがない記録をエラー文字列から
推測分類しない。今回の修正で元の歴史上の受信断片が復元できるわけではない。
常用のschema v3 DB、起動済みCore、distは切り替えていない。
