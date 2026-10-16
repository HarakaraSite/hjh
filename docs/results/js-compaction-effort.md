# DeepSeek要約のeffort比較

2026-10-16、OpenCode Go / DeepSeek V4.1 Flashで測定した。
キャッシュヒットなしにそろえた各3回では、要約時間の中央値はautoが18.9秒、maxが33.6秒だった。
中央値の比較では約1.78倍、14.7秒増。入力は約19,400 prompt tokens、要約指示は約1,200文字である。

| effort | 要約時間の中央値 | 最短〜最長 | 各回の時間 |
| --- | --- | --- | --- |
| auto | 18.921秒 | 9.937〜25.366秒 | 9.937 / 25.366 / 18.921秒 |
| max | 33.640秒 | 25.570〜50.116秒 | 33.640 / 50.116 / 25.570秒 |

この入力での小規模な実測値であり、一定の倍率として扱うものではない。
今回の要約本文を確認した範囲では、maxによる品質改善は確認できなかった。

## 前回の設定と測定経路

[前回の課題比較](js-compaction-behavior.md)は親・要約ともautoだった。
hjhのChat Completions実装はautoの場合に`reasoning_effort`を送らず、プロバイダー既定に委ねる。
maxの場合は`reasoning_effort: "max"`を送る。autoをlow固定という意味では扱っていない。
根拠は[エンコーダ](../../internal/provider/protocol/encode.go)の`prepareChatCompletions`内のeffort分岐。
プロバイダー内部でautoがどの強度へ解決されるかは、この測定では確認していない。

前回の圧縮あり1組目で実際に生成された要約要求からinstructionとmessagesを取り出し、
同じ`api.model.generate`経路で再実行した。対象はリリース計画の合成履歴A〜Dと継続タスクE。
要約指示は変更せず、親のeffortだけをauto/maxに切り替えた。
要約生成は[既存実装](../../internal/app/request_model.go)に従い、親の接続・モデル・effortを継承する。
任意の要約モデルを指定する製品機能は追加していない。

Core/CLI・workspace・DB・設定を各実行で分け、同じ実行バイナリを使用した。
順序はauto→max、max→auto、auto→max。各実行の選択モデル・effortはruntimeと保存済み意味記録で確認した。
要約API呼出しの開始から完了までを測り、最後に通常実行を終了する短いDONE応答は計測値から除いた。
履歴探索・圧縮候補の選択・ファイルI/Oを含むコンパクション全体の時間ではない。
JSの経過時間と保存済みHost呼出し時間も照合した。

## キャッシュ条件の扱い

最初に、前回と完全に同じ要約要求を各3回実行した。
この系列では入力が同一であることをfixtureの内容から確認している。

| effort | 中央値 | 各回の時間 | 各回のcached_tokens |
| --- | --- | --- | --- |
| auto | 23.473秒 | 20.403 / 30.797 / 23.473秒 | 0 / 19,328 / 19,328 |
| max | 27.596秒 | 29.905 / 18.253 / 27.596秒 | 0 / 19,328 / 0 |

この系列の中央値差は4.1秒、約1.18倍だった。ただしキャッシュ条件が異なるため、
effortだけの影響を示す値としては採用しなかった。結果は削除せず、データに残している。

続いて、instructionの冒頭へ各実行で異なる測定IDと「要約に含めない」という短い指示を追加し、
入力prefixのキャッシュ再利用を避けた。要約対象のmessagesと元のinstructionは全実行で一致し、
追加した測定ID部分だけが異なる。完全に同一の送信本文ではない。
この追加系列ではusageの`cached_tokens`が全6回とも0であることを確認した。
prompt tokensは19,377〜19,382で、冒頭の表はこの系列の値である。

ネットワーク・provider側の待ち時間・生成量の揺れは分離していない。
各3回という規模から、モデル一般の速度比や他の入力サイズの所要時間へは外挿しない。

## 要約本文の観測

両系列とも、auto/maxの要約は採用済みのschema・channel・region順序・125ms基準・rel-736除外・
rollback先・最新のpercent訂正を保持していた。現在の未完了作業もファイル作成として残していた。
今回は生成された要約本文の確認であり、その要約を使った後続課題の実行比較は行っていない。

| 確認した情報 | 同一要求：auto / max | キャッシュなし：auto / max |
| --- | --- | --- |
| 125ms採用の理由（ピーク時間帯再試験で130ms案にタイムアウト） | 1/3 / 1/3 | 1/3 / 1/3 |
| 採用根拠の試験ID PERF-Q17-125 | 3/3 / 3/3 | 3/3 / 2/3 |

maxでも理由が省かれ、追加系列の1回では根拠試験IDも省かれた。
maxの1回目にはJSONの出力イメージが加わるなど構成の違いもあったが、
必要情報の保持が改善したとは、この例では判断できない。
無関係な完了済み搬入の予約番号・搬入口・担当者は、全12要約で省かれた。

キャッシュなし系列のreasoning tokensはautoが583 / 2,560 / 2,553、maxが5,364 / 5,138 / 2,366。
要約文字数はautoが1,007 / 1,043 / 914、maxが1,286 / 981 / 928だった。
約1,200文字はソフトな指示で、切り詰めは行っていない。
推論量にも揺れがあり、maxが毎回autoより多くのreasoning tokensを使ったわけではない。

## 記録

- [全12回の時間・usage・実際の要約文](js-compaction-effort-data.json)
- [測定スクリプト](../../scripts/measure-compaction-effort.py)
- 同一要求系列：`/tmp/hjh-compaction-effort-live/effort-sqky5_vh/`
- キャッシュなし系列：`/tmp/hjh-compaction-effort-live/effort-05y3r59h/`

再実行例（有料APIを利用）:

```bash
python3 scripts/measure-compaction-effort.py \
  --binary /tmp/hjh-compaction-strategy-wvuVHX3a/hjh \
  --source-reports /tmp/hjh-compaction-behavior-live/behavior-9_ft19_6/reports.json \
  --output-dir /tmp/hjh-compaction-effort-live \
  --repeats 3 \
  --cache-mode fresh-prefix
```
