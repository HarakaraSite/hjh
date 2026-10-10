# S-06：通常画面TUIとCore応答性

2026-10-11。利用者は旧Henji TUIの操作・表示仕様の踏襲、段階的実装、検証用スパイクを指定した。
利用経路はGhostty → SSH → tmux。Alt+Enterは旧Henjiで設定調整後に利用できていたとの明示がある。
Core拡張も対象とし、旧HenjiのData Workerと`_refs`を必ず参照する。

## 現行経路

- Core：`HTTP Start → corerun.Manager → app.Service.Run → Agent / QuickJS / provider → journal → SQLite`。
- 観測：`journal.observe`は保存後に`progressSink → Manager.projection`を同期呼出しする。
- 表示：購読ごとの通知は1個のwake-upに合流し、HTTP handlerがsnapshotを取得してJSON化・SSE writeする。
  HTTP writeはManager lockの外であり、Agentからwrite完了を待たない。
- 制御：run registry、進捗更新、snapshot取得、cancelは現在Managerの共通mutexを使う。
  `Get`はprojectionの全entryをこのlock内でsliceへ展開する。
- 保存：SQLite connectionは1本。本文更新ごとに累積本文の観測保存と最新本文更新を同期実行する。
- 終了：Service.RunがWorker join・終了保存・会話採用を完了して戻った後にManagerがterminalを公開する。
  終了済みのSSE snapshotはInspectから観測を再生して復元する。

## 必須参照：旧Henji Data Worker

参照repo HEAD：`2eb586316f80ec8297873a96122568d0088971eb`。以下は2026-10-11のworking source確認であり、
旧binaryを実行した結果ではない。

1. `v0/agent/host/core_service.ts`は`createDataClient`を呼び、Session会話を`watchConversation`で購読する。
2. `v0/agent/data/client.ts`は`data_bootstrap.ts`をDeno Workerとして起動し、request IDとPromiseで操作を対応付ける。
   会話deltaはbufferをtransferし、listener処理完了後にdelivery ACKを返す。
3. `data_bootstrap.ts`は`createDataService`を作る。`onmessage`は各handleを独立に開始し、保存sequenceを待つrequestが
   seal／terminalを塞がないようにしている。単一FIFOへ全操作を押し込む構造ではない。
4. `session_data_owner.ts`がSession状態・admission・準備・settlementを所有し、`execution_data_journal.ts`がAgent事実を記録する。
   Agent Workerの`worker_bootstrap.ts → agent_data_client.ts`は移譲されたMessagePortで
   Data Workerの`agent_data_endpoint.ts`へ観測を直接送る。表示配信の経路を経由しない。
   通常観測はsequence付きpostで、proposal／failureは最終sequenceを返して保存barrierの基準にする。
   journalは受信sequenceとdurable sequenceを区別し、観測をbatch保存する。
   当時の256件／25msは実装値であり、hjhの採用値ではない。
5. `conversation_writer.ts`はSQLite保存結果からnormalizerを差分更新する。
   batch内のentity upsertを合流してdeltaを作り、Session cutとstore revisionを付ける。
   Session authorityの更新はSQLite commit後に行う。
6. `data_service.ts`は会話の保存・状態更新と、購読先へのdelivery pumpを分ける。
   遅いlistenerを待つのはpump側で、pendingが増えた場合はsnapshotによる再同期へ切り替える。
   ただしinactive Sessionの解放やservice.closeにはdeliveryを待つ箇所もある。
   この待ちをGoのrun完了・Core停止に引き継ぐとは決めない。

旧調査`docs/research/core-responsiveness-and-surface-conversation-review-2026-10-01.md`には、
全履歴の同期再構築を繰り返すとキャンセル受付が約18.85秒遅れた実測がある。
これは当時の調査で、上記の現行Data Worker実装が同じ不具合を持つという証拠ではない。
同資料の重要な区別は、表示の合流と実行制御に必要な事実の合流を同一にしないことである。

## `_refs`の確認

- Crush HEAD `8b1824282eccc78cc62c188c245cf375230a18ce`：`internal/pubsub/broker.go`は途中通知をnon-blocking、
  terminal等をbounded-blockingで配信する。`internal/agent/agent.go`に単一のrun完了通知経路、
  `internal/workspace/client_workspace.go`に切断後の状態再同期がある。
  hjhはterminalを状態と保存先から取得できるため、producerが購読者へterminalを送るまで待つ方式や
  4096件／50msという値をそのまま採用する必要はない。
- San HEAD `baccf83caad7c6b3477fb3606cbaad60cb6ef036`：`internal/app/conv/runtime.go`でready eventをbatch読出しし、
  terminalの境界で区切る。`internal/app/conv/update.go`はscrollback書込みをSequenceで順序付け、
  outbox読出しを並行実行する。
  `internal/core/run.go`のoutbox emitには満杯時の待ちがある。表示の並行化だけでproducerの待ちを
  解消したと判断せず、hjhの実行から描画への依存を作らない。
- 参照実装のpermission・上限・認証retry等を今回の製品要件へ追加しない。

## Coreスパイクの実測

[実験プログラム](../../spikes/s06-core/README.md)は既存製品Core・HTTP handler・QuickJS・SQLiteを使う。
外部LLMは呼ばず、本文だけを注入providerから生成する。SSE writeを意図的に停止する。
別Sessionの依頼受付はHTTP JSON handlerを使う。数値はこのVMでの単発計測で、性能保証ではない。

| ケース | 最新本文 | 保存観測payload合計 | observer時間合計 | 終了snapshot復元 | 別依頼の受付 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 128更新・完了 | 16,384 B | 1,081,463 B | 72.75 ms | 5.87 ms | 0.014 ms |
| 512更新・完了 | 65,536 B | 16,908,746 B | 479.76 ms | 54.91 ms | 0.017 ms |
| 128更新・キャンセル | 16,384 B | 1,081,460 B | 65.10 ms | 5.96 ms | 0.017 ms |

全ケースでSSE writerが止まったまま別Sessionが完了し、元のrunがterminalまで進んだ。
128更新ケースのキャンセル受付は0.055ms、受付から終了保存・terminalまでは1.29msだった。
キャンセルは本文の保存後、providerが終了を待っている段階で行った。保存中のCPU負荷下での受付上限を
示すものではない。
全本文観測が保存され、遅れた購読で最新本文が復元された。writer再開後にterminalも届いた。
`go test -race ./spikes/s06-core`はpass。
追加確認で、SSE writer停止中のCore資源終了（`httpapi.Server.Close`）も完了した。
128更新・キャンセルの追加単発実行では約0.009msだった。listener／net/http.Serverのshutdown全体の計測ではない。
この追加確認でも終了保存・snapshot復元・writer再開後のterminal受信が成立し、race付きfocused testはpass。

累積本文を観測ごとに保存するため、同じ固定サイズのdeltaを増やすとpayload合計は二次的に増える。
この実測でGo Coreの受付停止は再現していない。保存のbatch化だけではpayload量の増加を解消できない。
DDLは維持し、保存payloadの変更が必要なら[保存契約](../design/history-payloads.md)と明示要件を照合して
変更箇所を計画する。黙って観測を間引いたり、表示と保存の完了を同一にしたりしない。

## 描画スパイク

[描画プログラム](../../spikes/s06-render/README.md)で通常画面、確定本文のscrollback送出、
live tailと入力欄、複数行・日本語入力・リサイズを検証する。
個別の確認結果と再現コマンドは同READMEに記録する。
Ghostty → SSH → tmuxの全経路はこのVMの確認結果と区別する。

ローカルPTYとprivate tmux 3.8で日本語stream・確定行／tool marker・Alt+Enter改行・通常Enter送信を確認した。
描画スパイクのfocused test・self-check・vet・buildはpass。最終変更後のrace付きfocused testもpass。
ただしprivate tmuxで80列から44列へ縮小すると、旧幅で描かれていたlive本文がscrollbackへ残り、
新幅で再描画したlive本文と重複した。startup headerを確定出力へ移してmutable frameを小さくしても再現した。
手動のcell幅折返しで本文の欠けは解消したが、scrollback側に残る重複は解消していない。
高さだけの縮小（80×24→80×18）ではlive本文の重複は観測しなかった。
Bubble Tea標準rendererによるこのspike方式は、旧Henjiのresize要件を満たす実装として採用確定できない。
旧`v0/tui/scrollback_writer.ts`はwidth変更後のreflowを計算し、所有するlive bandだけを消す専用writerを持つ。
この方式を参考に独立writerを比較することを、製品適用前の次の確認点とする。
Markdown・可変tool状態・大きな会話はこの小さい描画スパイクの確認範囲に含まれない。

## 設計へ反映する判断

- 会話の並びはCoreの意味的な順序・安定したentity IDで表し、壁時計やTUIへの到着順で決めない。
- live／履歴／再接続で同じnormalizerを使い、更新のたびに全DBを読んで会話を作り直さない。
- 保存完了、会話状態の公開、クライアント受信、描画完了は別の境界にする。
- cancel・新しい依頼の受付は、描画、SSE write、表示ACK、会話の全体復元を待たない。
- 重いJSON化・履歴復元・Markdown・折返しを制御用lockの中へ入れない。
- terminalは保存とWorker joinを待って公開する。TUIの描画ACKをterminalの条件にしない。
- Goの専用goroutineで責務を分ける場合も、共有lock・channelの満杯・DB connectionの待ちを追う。
  workerやgoroutineが存在するだけで分離済みと判断しない。

製品版Core/TUIへの適用は[I-06計画](../plans/i06-conversation-tui.md)で扱う。

## 2026-10-11：利用者の補足と追加確認

利用者は旧Data Workerの処理順や配置をなぞることではなく、Goの特性を踏まえて取り込むことを指定した。
さらに、旧HenjiでDBとメモリの肥大化に苦労したため、重複処理を極力減らすことを要求した。
Goのgoroutineは同じprocessのheapを共有する。DenoのWorker境界・MessagePort・transfer／ACKを再現することは
要件ではなく、状態所有・待ち・保存量・コピー量を現在のGo経路から設計する。

### 旧Henjiのresize重複との区別

旧`docs/increments/increment-212.md`には、ED0による画面clearで直前の画面がtmux履歴へ追加される
現象を観測し、各行のELへ変更した記録がある。また、width変更で端末がlive本文を履歴へ移すことを考慮し、
`tui_renderer.ts:665`が`conversation_flow.ts:178`の`sealVisibleTail`を呼び、表示済みsource offsetを
出力済みとして記録する。writerだけでなく、会話flowの出力receiptが再追記を防いでいる。
したがって旧writerだけを移植して同じ結果が得られるとは判断しない。

同incrementにはtmux上で140×30→90×20→140×30へ変更しても生成中本文の再追記がない実測があり、
`.tools/append-only-body/result.json`と`.tools/tool-thinking-once/result.json`にも長い本文／thinkingの
`resizeDuringStreaming`・各行を一回だけ出す確認のpass記録がある。
現在の旧sourceに対して次を再実行した。旧repoのsource・lockfileは変更せず、provider呼出しはない。

```sh
deno test --no-lock --cached-only /home/agent/projects/henji-harness/tests/v0/increment_212_scrollback_writer_test.ts
deno test --no-lock --cached-only --filter 'resize seals' /home/agent/projects/henji-harness/tests/v0/increment_212_conversation_flow_test.ts
```

writerのfocused testは7件pass、flowのresize確認は1件pass。
これらは旧実装の現行sourceと過去の実機記録を確認した結果であり、今回Ghostty実機で旧TUIを再実行した結果ではない。
今回の未解決点はBubble Tea描画スパイクで観測したもので、旧Henjiの現在の不具合として扱わない。

### DB実サイズとメモリ／反復処理

`-measure-cost`を追加し、別processで128／512更新を各一回計測した。単位はdecimal MB（1,000,000byte）。
既存の累積本文保存と既存の終了snapshot復元の経路を使い、保存内容を間引いていない。

| 本文更新 | 最新本文 | DBファイル全体 | stream中のGo累計割当 | snapshot復元のGo割当 | 同じsnapshotの再復元のGo割当 | process最大RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 128 | 16,384 B | 1.63 MB | 27.25 MB | 5.19 MB | 5.19 MB | 29.86 MB |
| 512 | 65,536 B | 18.16 MB | 395.63 MB | 74.63 MB | 74.62 MB | 74.44 MB |

512更新の同じsnapshotの二回目の復元は約53.43msだった。
GC後のGo heapは512更新ケースでterminal後約0.74MB、snapshot復元後約0.65MB、実験の資源終了後約0.74MB。
累計割当を保持メモリやRSSと混同しない。この一回のrunではGo heapの持続的な肥大化は示していない。
最大RSSは実験process全体（SQLite等と複数回の復元も含む）であり、通常利用の定常値ではない。
stream割当には模擬provider側の文字列連結と別Sessionの実行も含むため、実providerの割当量と同一とは主張しない。

確認したsource上の反復経路は次のとおり。

- `provider/protocol/generate.go`は本文進捗ごとに累積textをJSONへ包み、`app/journal.go`はその観測を保存し、
  textをJSONから取り出して最新本文保存用JSONを作る。Core projectionも観測JSONから同じtextを取り出す。
- `corerun/subscription.go`は終了snapshotごとに`Service.Inspect`を呼び、全観測を取得・JSON解析して再生する。
  同じterminal状態を複数回読んでも同じ経路を反復する。
- `history/read.go`のRecordsは全payloadを読み、その各recordのcontent・relationを追加queryで取得する。
  inspection用の詳細まで会話snapshot復元に持ち込む経路になっている。
- ManagerはCoreの生存中、run開始requestとterminal結果を保持する。長時間・多turn利用時の保持量も
  設計対象とし、全本文を持つ保存形式と全本文を持つmemory cacheを無条件に重ねない。

Go側では累積本文の再保存量とJSON化／parseの反復を減らすこと、変更のないterminalの全観測再生を避けること、
必要な会話情報だけを読むこと、保存済み本文とmemory cacheの重複保持を減らすことを優先する。
単にbatch transactionやgoroutineを追加するだけでDBサイズと割当量が解消したとは扱わない。
計測追加後の`go test -race ./spikes/s06-core`もpass。製品版のCore・DB・TUIは変更していない。
