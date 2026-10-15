# 計測だけ行うhookの実プロバイダ観測

2026-10-15。利用者がOpenCode Go / DeepSeek V4.1 Flashの実利用を許可し、
hookを使った単純なコンテキスト量の観測を指定した。

これはcurrent-only計測だった時点の結果である。後続の
[履歴込み計測・選択API](request-context-selection.md)でmeasureの既定を拡張し、
同じ実プロバイダで再確認した。以下の数値と不足の記述は当時の観測として保持する。

## サンプルと実行条件

サンプル：[examples/hooks/context-metrics.js](../../examples/hooks/context-metrics.js)。
`projectRequest`から`api.context.get()`と`measure()`をawaitし、数値と識別情報だけを
`.hjh/context-metrics/<executionId>-<modelStep>.json`へ保存する。
`replace`・要約・instruction変更は行わない。会話本文とtool定義は計測ファイルに書かない。

- provider：`opencode-go-chat`、model：`deepseek-v4.1-flash`、effort：`auto`。
- 現在のsourceをビルドしたCLIから実endpointへ接続。疑似provider・代理応答は使っていない。
- 独立workspace、独立v4 DB、独立configで実施。既存credentialは元のfileから参照し、値を複製しない。
- 観測用roleのtoolはread_file一つ。通常利用の全tool構成での入力量を表すものではない。
- turn 1：1,361 bytesのstart.txtを読み、その中で指定された4,153 bytesのdetails.txtを次に読む。
  最後に識別子二つとFINAL_MARKERだけ回答する。
- turn 2：同じSessionを別CLI processで再開し、toolを使わず会話履歴から同じ値を回答する。

## 観測結果

送信側の数値は通常のcontext_budget記録、provider入力tokensは実responseのusage.prompt_tokensである。
hookと送信側の推定は同じhjh estimatorを使う。providerのtokenizerによる実usageとは区別する。

| turn / step | hookのmessage数 | hook bytes | hook推定tokens | 送信側bytes | 送信側推定tokens | provider入力tokens |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 / 1：最初の読取り前 | 1 | 2,210 | 761 | 2,210 | 761 | 715 |
| 1 / 2：最初の結果追加後 | 3 | 4,023 | 1,381 | 4,023 | 1,381 | 1,074 |
| 1 / 3：二つ目の結果追加後 | 5 | 8,711 | 2,960 | 8,711 | 2,960 | 2,322 |
| 2 / 1：保存会話から再開 | 1 | 1,998 | 690 | 9,040 | 3,086 | 2,405 |

両turnとも識別子とFINAL_MARKERを正しく回答した。実モデル要求は4回、
provider報告の入力合計6,516 tokens・出力合計148 tokens。
turn 1は約5.12秒、turn 2は約2.04秒。いずれもモデル応答・CLI起動・保存を含むため、
hook単体の性能測定ではない。

計測ファイルは4件、合計2,690 bytes。コンテキスト差替え0回。
計測ファイルに元ファイル本文が含まれないこと、既存credential値がCLI出力とDBへ露出しないことを確認した。
DBのintegrity_checkはok。

## 分かったことと次の検討

1. turn内のtool結果追加による増加をhookから観測できる。過去会話がない最初のturnでは
   hookと送信側のbytes・推定tokensが一致した。
2. 現行のget/measureは現在executionの原文だけを対象にする。turn 2は後段の通常予算選択で
   前turnの6 messagesが加わり、hookの1 messageに対して実送信は7 messagesとなった。
   サンプルのscope表記どおりであるが、**この値だけではSession全体の自動コンパクションを判断できない**。
   将来の判断には、過去会話を含む候補を組んでmeasureするか、通常projectorの選択候補を測るAPIが必要。
3. この構成ではbudgetのcontextTokens/inputTokens/limitが不明だった。
   sizeは測れるが、モデル上限に対する使用率や閾値は判断できない。
   自動発動の設計時に、このrouteのcapacity情報または明示設定も検討する。
4. hjhのtokensはwire bytesからの概算で、この観測ではprovider入力より約6〜29%大きかった。
   別の文章・model・tool構成でも同じ誤差とは限らない。

以上は観測結果であり、API・予算設定・自動要約の追加修正は今回行っていない。

## 証跡と検証

- Session：`584bced6-dc45-49a4-95b9-be556b3a436a`（独立DB）。
- `/tmp/hjh-context-metrics-live-j226cmre/observe.py`：実行・集計スクリプト。
- 同directoryの`result.json`、`turn-{1,2}.stdout/stderr`、`history.sqlite3`、
  `workspace/.hjh/context-metrics/*.json`。
- [集計データ](context-metrics-live-data.json)には数値を抽出し、本文は含めていない。
- 実QuickJSでサンプルを実行し、初回・tool後・Session再開の4 hookを確認。
  `git diff --check`成功。Go本体は今回変更しておらず、全体gateは繰り返していない。
- 常用Core・DB・extensions・dist binaryは変更していない。
