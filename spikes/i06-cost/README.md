# I-06 同一Sessionの保持量

外部providerを呼ばず、製品のManager → Service → Agent/QuickJS → history経路から
同じSessionを継続する測定。自分で作成した一時workspace・DBのみ使う。

```sh
go run ./spikes/i06-cost -turns 200 -body-bytes 256
```

最終本文は各turnで256byte、会話購読は行わない。終了済みrunを100回取得し、
結果取得だけで会話viewや全Inspectを生成しない経路を使う。
turnごとのGC後Go heap、累計割当、Linux RSS、goroutine数、DB実サイズを記録する。
本文・保存履歴・モデルへ渡すcontextは切り詰めず、小さいrun索引はCore生存中残る。

DBは正規のmessages、request/source/context事実も含んで増えるため、総容量を一定とは扱わない。
RSSはallocator等も含むprocessの観測値。heap・累計割当とは別に評価し、閾値をtestへ入れない。
owner/index/viewの解放条件はcorerunの具体的動作testと合わせて確認する。

checkpoint終了後、全turnのSession会話を保持viewなしで復元し、時間・割当・entity数を記録する。
さらにsemantic payloadのkind別byte量とcontents総byte量を測定専用connectionから読む。
製品のStore接続方針は変更せず、context/source保存の増加と途中本文重複を分けて説明する。
