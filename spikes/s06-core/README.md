# S-06 Core：遅い表示クライアントと保存の分離

2026-10-11。利用者が許可したスパイク。製品版Coreは変更しない。

```sh
go run ./spikes/s06-core -updates 128
go run ./spikes/s06-core -updates 512
go run ./spikes/s06-core -updates 128 -cancel
go run ./spikes/s06-core -updates 128 -measure-cost
go run ./spikes/s06-core -updates 512 -measure-cost
go test -race ./spikes/s06-core
```

各実行は一時workspace・SQLiteを作り、終了後に自作の一時ファイルだけを片付ける。
既存Core・TUI・Session・credential・tmux設定には触れない。外部provider呼び出しはない。

## 確認する経路

`corerun.Manager → app.Service → Agent / QuickJS → journal → history.Store`と
`httpapi.Server → Subscription → SSE write`は製品の実装を使う。
LLMは決定的な累積本文を返す注入providerである。
HTTP handlerはhttptestから呼び、SSEのResponseWriter.Writeをchannelで停止する。
これはHTTP handlerの書込み待ちを直接作る実験であり、実TCPの受信バッファやGhosttyの検証ではない。

書込みが停止している間に、別Sessionの依頼をPOST handlerへ投入して完了を待つ。
本文の更新をすべて保存した後、元のrunを完了またはキャンセルし、terminalとSQLiteの終了記録を確認する。
遅れて購読したsnapshotから最終本文を復元できることも確認する。
Coreのrun／Service資源のcloseも、SSE writerが止まったまま完了することを確認する。
ここで確認するのはServer.Closeであり、listenerやnet/http.Server全体のshutdown時間ではない。
最後に書込みを再開し、待っていたSSE handlerがterminalを返すことを確認する。

`observerTotalMs`はGenerateのobserver callbackを呼ぶ時間の合計で、JSON生成を含まない。
callback内の同期保存とCore projection更新を含み、SQLiteだけの時間ではない。
`restoreMs`は終了済みrunのSubscription.Snapshot（DB読込み・観測の再生）である。
`observationPayloadBytes`は保存されたassistant_text観測payloadのbyte合計で、DBファイル全体のサイズではない。
1更新は128byteであり、更新数と本文サイズが同時に増える。更新数だけの比較ではない。

`-measure-cost`はLinuxで一回の実験を新しいprocessから計測する。
`databaseBytes`はSQLiteファイルの実サイズで、他のSessionも含む今回の実験DB全体。
`streamAllocBytes`は本文生成開始からterminalまでのGo累計割当であり、注入providerの文字列連結、
別Session、保存、Core projectionを含む。実providerだけの割当や同時に保持されたメモリではない。
`restoreAllocBytes`と`repeatedRestoreAllocBytes`は同じterminal snapshotを二回読むときのGo累計割当。
同じ状態でもInspectから再生し直す現行経路の反復コストを測る。
`heap*`は指定した段階でGCした後のGo heap、`processPeakRssBytes`はLinux `/proc/self/status`の
VmHWMであり、process全体の最大RSS。Go heapだけではSQLite等の全メモリは説明できない。
最大RSSは二回のsnapshotと最後のSSE再開時の復元も含むprocess全体の値で、段階別のpeakではない。
heapAfterCleanupは実験関数から戻り、Core／Store／一時DBを終了した後のGo heap。
性能testの閾値は設けない。数値の比較は結果文書に記録する。

実測値と旧Data Workerからの設計上の判断は
[結果](../../docs/results/s06-tui-core.md)と[I-06計画](../../docs/plans/i06-conversation-tui.md)を参照する。
