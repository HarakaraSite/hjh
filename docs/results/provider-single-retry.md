# Provider要求の一回再送の確認結果

2026-10-15。[計画](../plans/provider-single-retry.md)。起点`1b74dd9`。

一つの準備済みモデル要求につき再送は合計1回、待機は固定2秒。
transport/timeout、HTTP429/5xx、終了イベントなし、不完全tool metadata/引数JSONを対象とする。
明示された出力上限、取消、認証・保存失敗は再送しない。二回目も失敗したら既存の停止/recallへ返す。
再送を使い切ったHTTP失敗にも`try /recall`を表示する。

追加したものは共通Generate helper、retryAttempt metadata、失敗試行のsuperseded保存と表示filter、
既存context notice。設定・Retry-After・引数修復・新DB table・raw SSE常時保存は追加していない。
ModelStep/RequestOrdinalは維持する。コンテキストの準備と完了済みtoolはやり直さない。

## 確認

focused testで次の変更動作を確認した。

- 不正tool→正常toolの再生成。同じwireを2秒後に再送し、正しいtoolだけを一度実行。
- HTTP429→不正toolでも二回目で停止し、種類ごとに再送枠を増やさない。
- `finish_reason=length`の不正toolは、[DONE]あり/なしとも再送しない。
- 待機中の取消では二回目を送らず、2秒の待機を中断する。
- 失敗本文/thinkingを詳細履歴に残し、canonical会話には採用しない。
- 再送のentity/receipt/thinking locatorを分離し、通常表示と復元では初回失敗を除外する。
- recallは初回のsupersededではなく、最後のincomplete本文を参照する。

実Core/PTY TUIと使い捨てv4 DB/local HTTPで次を確認した。

| 操作 | 結果 |
| --- | --- |
| 不正tool→正常回答 | 初回と同じwire、再送間隔2.005秒、正常完了 |
| 復元 | 初回の本文/thinkingは非表示。詳細記録には残存 |
| HTTP503→正常回答 | 一回再送して正常完了 |
| 不正toolが二回続く | 二回目で停止。`try /recall`を表示 |
| 引数なし/recall→次の依頼 | 最後の未完了本文をreferenceとして渡し、正常完了 |
| 出力上限 | 呼出し一回のみ。再送なし |
| HTTP503が二回続く | `FAILED · provider request failed · try /recall`。recall後の次実行成功 |

主artifact: `/tmp/hjh-single-retry-final-SxpJGVxh/`（result.json、PTY出力、DB、Core log）。
HTTP再失敗の追加確認: `/tmp/hjh-single-retry-http-recall-VVn1NkYK/`。
外部provider呼出し0回、local provider呼出しは主確認8回・追加確認3回。
主確認のDB integrity_checkはok。常用v3 DB/Core/distは切り替えていない。

独立reviewでlength受信後の終了marker欠落が再送対象に入る問題を修正した。
実PTY確認でrecallがsupersededを完了回答と誤認し最終partialを落とす問題を修正した。
限定再reviewで主経路とrecallの選択を確認し、対応必須findingなし。

安定候補に対する全体確認は一回実行し、成功した。

```sh
go test ./internal/... ./cmd/... -count=1
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45 ./internal/... ./cmd/...
git diff --check
```

端末scrollbackへ既に確定出力した行は消さず、再試行noticeで区切る。
通常の復元表示からはsupersededを除外し、新しい本文は別keyへ描画する。
