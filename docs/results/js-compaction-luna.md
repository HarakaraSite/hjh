# OpenCode Go GPT-6 Lunaの要約時間

2026-10-16、OpenCode Goの`gpt-6-luna`をauto/maxで各3回測定した。
同じ要約対象では、中央値がauto=6.6秒、max=12.9秒だった。
直前のDeepSeek測定との比較では、要約要求の完了が中央値で約2.6〜2.9倍速かった。

| モデル | effort | 要約時間の中央値 | 最短〜最長 |
| --- | --- | --- | --- |
| DeepSeek V4.1 Flash | auto | 18.921秒 | 9.937〜25.366秒 |
| DeepSeek V4.1 Flash | max | 33.640秒 | 25.570〜50.116秒 |
| GPT-6 Luna | auto | 6.551秒 | 5.980〜6.900秒 |
| GPT-6 Luna | max | 12.940秒 | 7.810〜15.212秒 |

GPT-6 Luna内ではmaxの中央値がautoの約1.98倍、6.4秒増。
今回の要約本文では、auto/maxの全6回で採用理由と根拠試験IDを保持していた。

## 条件と実経路

[DeepSeekのeffort比較](js-compaction-effort.md)と同じ、前回の課題比較で実際に生成された要約要求を使用した。
リリース計画の合成履歴A〜Dと継続タスクEが対象で、要約指示は約1,200文字。
messagesと元のinstructionはDeepSeek側と同一で、キャッシュ再利用を避ける測定IDだけ各実行で変えた。
GPT-6 Lunaのinput tokensは19,388〜19,393。usageで全6回のcached_tokens=0を確認した。
比較表のDeepSeek側も全6回cached_tokens=0の系列を使用している。

GPT側は既存の`opencode-go-responses` / Responses API、DeepSeek側は
`opencode-go-chat` / Chat Completions APIで、それぞれ設定済みの実経路を使った。
親のprovider/model/effortを選択し、要約は`api.model.generate`の既存の継承経路で実行した。
要約専用の任意モデル指定機能を追加した測定ではない。
各実行のruntimeと保存済み意味記録でprovider/model/effortの一致を確認している。

Core/CLI・workspace・DB・設定は各実行で独立し、DeepSeek側と同じバイナリを使用した。
順序はauto→max、max→auto、auto→max。要約APIの呼出し開始から完了までを計り、
最後の短い通常応答DONEは除いた。保存済みHost呼出しの経過時間とも照合した。
履歴探索や圧縮候補の選択を含むコンパクション全体の時間ではない。

| GPT-6 Luna | 1回目 | 2回目 | 3回目 |
| --- | --- | --- | --- |
| auto：要約時間 | 6.551秒 | 5.980秒 | 6.900秒 |
| max：要約時間 | 12.940秒 | 7.810秒 | 15.212秒 |
| auto：reasoning tokens | 157 | 171 | 207 |
| max：reasoning tokens | 893 | 516 | 1,034 |
| auto：要約文字数 | 718 | 656 | 735 |
| max：要約文字数 | 609 | 684 | 746 |

## 要約本文の観測

全6回で、schema・channel・通知先・region順序・125ms基準・rel-736の例外・rollback先・
最新訂正のpercent（osaka=10/tokyo=5）を保持していた。
「ピーク時間帯の再試験で130ms案にタイムアウトが出た」という125ms採用理由と、
根拠試験ID`PERF-Q17-125`も全6回に残った。
無関係な完了済み搬入の予約番号・搬入口・担当者は、採用した削除優先方針に沿って全6回で省かれた。

| 確認した情報 | DeepSeek auto / max | GPT-6 Luna auto / max |
| --- | --- | --- |
| 125ms採用理由の保持 | 1/3 / 1/3 | 3/3 / 3/3 |
| 根拠試験IDの保持 | 3/3 / 2/3 | 3/3 / 3/3 |

GPT側は609〜746文字と、同条件のDeepSeek側914〜1,286文字より短い要約だった。
短い本文でも、今回確認した採用済み事項・判断理由・試験IDは残していた。
maxにするとautoより内容が改善した、とはこの例では確認できない。

これは一種類の入力を各3回要約した観測で、モデル一般の品質差を示すものではない。
本文の保持内容を確認したものであり、その要約を用いた後続課題の実行は今回は比較していない。
同じ文字数の出力を強制しておらず、生成量・API経路・provider側の待ち時間も異なるため、
モデルの純粋な生成速度や他の入力サイズの所要時間へは外挿しない。
DeepSeekとGPTは同時に交互実行した比較ではなく、同日この順に測定した。

## 記録

- [全6回の時間・usage・実際の要約文と比較値](js-compaction-luna-data.json)
- [測定スクリプト](../../scripts/measure-compaction-effort.py)
- 全DB・fixture・意味記録・標準出力：`/tmp/hjh-compaction-luna-live/effort-ivfj2v3w/`

再実行例（有料APIを利用）:

```bash
python3 scripts/measure-compaction-effort.py \
  --binary /tmp/hjh-compaction-strategy-wvuVHX3a/hjh \
  --source-reports /tmp/hjh-compaction-behavior-live/behavior-9_ft19_6/reports.json \
  --output-dir /tmp/hjh-compaction-luna-live \
  --provider opencode-go-responses \
  --model gpt-6-luna \
  --repeats 3 \
  --cache-mode fresh-prefix
```
