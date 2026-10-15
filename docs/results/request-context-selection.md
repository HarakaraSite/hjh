# JSからの要求予算・履歴選択

2026-10-15。[計画](../plans/request-context-selection.md)のS1〜S3を実装・確認した。
現行契約は[Request context API](../design/request-context.md)。

## 動作

- `measure()`は履歴込みのrequest scopeを既定にした。除外前候補の量と、選択後の量・除外有無を返す。
  `scope:"current"`で今回executionだけ、明示messagesでその配列だけを測れる。
- `select()`は既存projectorのcopyで選び、メッセージ・instruction・実効予算と数値を返す。
  呼出しだけでは本番projectorも送信候補も変更しない。明示配列でも既存のcall/result単位を使う。
- measure/select/replaceのbudgetへcontextTokens/inputTokens/outputReserve/inputRatio/historyTokensを
  部分指定できる。既存式でlimitを解決し、増減とも可能。適用fieldのsourceはhook。
- `replace(await select(...))`で選択結果と予算を今回の送信へ反映する。
  最終Go側はその候補を計測・送信し、明示候補をさらに黙って除外しない。
- 原文、会話採用、hookなしの通常経路、同一wireでのprovider retry、短いAPI記録を維持する。
  schema・保存表・全体設定・Session要約状態は追加していない。

## スライス確認

S1：予算式を一か所に保ち、overrideと通常解決で共有した。
低予算でPreviewしても元の除外index/stateが変わらないこと、除外前候補が予算超過で
選択後が収まること、最新historyから最初のoverflowで読取りを止めることを確認した。
JS指定に相当する入力上限・比率・出力予約・履歴予算の部分上書きとbase非変更も確認した。

S2：実QuickJSのprojectRequestから履歴込みmeasure、current-only measure、input/history予算でのselectを実行。
試行後に通常の履歴付き要求が変わらないことを確認した。
tool結果二件の後に要約相当のuser messageを挿入し、JS指定予算で古いtool call/resultを一組除外、
replace後の実送信・最終budget保存・DB原文の維持を照合した。
低い設定予算をJSから増やして送信できる経路も確認した。

S3：compiled CLIと独立Coreをlocal HTTPへ接続し、select→replaceの12,000 tokens予算が
最終のlatest budgetに残ること、初回・tool後・別processでのSession再開、同じwireでの再送、
原文維持、hook/API本文複製0を確認した。DB schema4/WAL/integrity ok。
証跡：`/tmp/hjh-context-selection-to9n6dos/verify.py`、`result.json`。

## 実プロバイダの再観測

許可済みOpenCode Go / DeepSeek V4.1 Flash / autoで、前回と同じ短い二つのファイル読取りと
次turnの履歴からの回答を、独立CLI・workspace・DBで再実行した。
サンプルは[context-metrics.js](../../examples/hooks/context-metrics.js)。入力の差替えはしない。

| turn / step | 今回message数 | 履歴込み候補message数 | hook bytes | 送信側bytes | hook/送信側推定tokens | provider入力tokens |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 / 1 | 1 | 1 | 2,221 | 2,221 | 765 | 718 |
| 1 / 2 | 3 | 3 | 4,028 | 4,028 | 1,383 | 1,076 |
| 1 / 3 | 5 | 5 | 8,731 | 8,731 | 2,967 | 2,330 |
| 2 / 1 | 1 | 7 | 9,058 | 9,058 | 3,092 | 2,412 |

**4要求すべてでhookと送信側のwire bytes・推定tokensが一致した。**
前回は次turnを690 tokensと測っていたが、今回は前turnを含む3,092 tokensを測れた。
両turnとも指定の識別子を正しく回答した。provider報告の入力合計6,536 tokens、出力153 tokens。
計測ファイル4件で合計3,666 bytes。本文を含む記録やコンテキスト差替えは追加していない。

証跡：`/tmp/hjh-context-selection-to9n6dos/real-provider/`のobserve.py、result.json、
CLI出力、workspace/.hjh/context-metrics/*.json。
[数値集計](request-context-selection-data.json)に抽出した。
元credential値はCLI出力・実験DBへ露出していない。

## 検証

- sliceごとのfocused test成功。
- 関連8 packageのgo test成功：app/jsruntime/extensions/contextbudget/agent/corerun/httpapi/cmd/hjh。
- app/jsruntime/contextbudgetのProjectRequest・Preview・Override focused race成功。
- 同じ関連package群のGo 1.27対応staticcheck成功
  （`honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45`）。
- 履歴専用予算のfocused確認とgit diff --check成功。
- rootがcopyの所有、Previewの読取り範囲、例外とfits:falseの区別、JS指定budgetの伝播、
  最終wireとDB原文を確認した。独立reviewer・全repository gateは追加していない。
- 常用Core・DB・extensions・dist binaryは変更していない。

## 範囲と次段階

除外前候補は、通常selectorが読む範囲（最初のoverflowまで）での最大候補であり、
DBの全過去turnの合計ではない。予算不明なら通常readerと同様に利用可能範囲を読む。
明示messagesはcurrent扱いなので、historyTokensの個別適用はrequest scopeのcanonical turnが対象。

自動要約の発動条件・要約生成・保持量・Sessionをまたぐ要約state・/compactは今回実装していない。
JSへ機構を公開し、今後その順序や数値を選べるようにした段階である。
