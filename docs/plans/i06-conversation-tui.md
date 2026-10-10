# I-06：旧Henjiに沿った会話画面とCore会話経路

2026-10-11。計画段階。利用者は旧Henji TUIの操作・表示要件の踏襲と段階的実装を指定し、
通常画面の検証をスパイクとして許可した。さらにCore拡張を指定し、旧Data Workerと`_refs`の参照を要求した。
製品実装・受入は未完了。[S-06](../results/s06-tui-core.md)はこの計画の実行証拠である。

## 利用者の必要な動作

Ghostty → SSH → tmuxで日本語の依頼を入力し、進行中の本文・toolの様子・最終応答を会話として読む。
会話は端末scrollbackへ残り、端末・tmuxの検索とコピーを使える。下部に編集中のdraftと状態が表示される。
Alt+Enterは旧Henjiでこの経路から利用できていたので、採用する操作である。
Shift/Ctrl+Enterも旧仕様のとおり識別可能な端末で対応する。
Enter送信、bracketed paste、矢印／Home／Endでの編集、Tab補完、Escで選択表示を閉じる操作も踏襲する。
利用者のtmux設定を勝手に変更しない。

会話や描画が重くなっても、Coreがキャンセルや別Sessionの依頼を受け付ける。
TUIを止めたり遅らせたりしたことで、Agent・終了保存の進行が止まらない。
Core/TUIは独立processのまま、HTTP＋JSON＋SSEを使う。

## 段階と優先順位

| 優先 | 段階 | 採用する動作 |
| --- | --- | --- |
| 1 | I-06 | 通常画面、scrollback、本文・公開thinking・toolの会話表示、Markdown、日本語・複数行編集、ヘッダー／フッター、コマンド補完、F1キャンセル、Ctrl+C入力消去。そのために必要なCore会話・順序・API拡張も同じ段階で扱う。 |
| 2 | 後続 | Session picker、viewとresumeの区別、履歴・tool詳細、rename／delete、detachとCore停止、実行中Sessionへの再接続。 |
| 3 | 後続 | provider／model／effort picker、検索・お気に入り、認証画面、保存済み認証の再利用、Core自動起動／既存Core接続。 |
| 4 | 後続 | F2 follow-up、F3 steering、受付と適用状態、実行中draft保持。 |
| 5 | 後続 | `/context`、`/recall`、停止した実行の内容を次の実行へ渡す操作。 |

後続のincrement番号・詳細な区切りはその計画時に確定する。
旧`/resume`はSession続行、`/recall`は次の実行に使う内容の準備であり、停止executionの再実行とは区別する。
未実装操作を実行できる表示にはしない。

旧スラッシュコマンド14個の配置は、I-06に`/help`、Session段階に`/new`・`/sessions`・`/view`・
`/resume`・`/rename`・`/quit`・`/detach`、選択／認証段階に`/login`・`/provider`・`/model`・`/effort`、
context段階に`/context`・`/recall`とする。F4はSession picker、Ctrl+Dはdetach、Ctrl+QはCore停止へ対応付ける。
`/quit`は旧仕様のCore停止へ対応付け、現行I-05のTUIだけの終了とは切替を明示する。

## 現行経路とCore拡張

現行経路と参照sourceは[S-06結果](../results/s06-tui-core.md)に記録した。
現在のRunStateはTexts／Operationsを別に持ち、TUIも本文の後に操作をまとめて描画する。
本文・tool・thinkingを会話順に表すため、公開会話entityと順序を追加する必要がある。
providerのopaqueなreasoning再送状態は会話のthinking本文と区別し、公開された本文・summaryのみを表示する。

| 所有者 | 担当する状態・処理 |
| --- | --- |
| run管理 | admission、Session予約、run context、cancel、軽い実行状態、join、保存済みterminalの保持 |
| history／保存 | 既存DDL、保存ordinal、journal、transaction、会話採用、保存完了の境界 |
| Core会話module | 保存事実とlive更新を意味的な順序・安定IDのentityへ変換。差分更新、履歴初期復元、公開revisionを所有 |
| HTTP配信 | snapshot／更新のJSON化とSSE書込み。購読ごとに進み、遅い購読は最新状態から追い付く |
| HTTP client | SSE受信・状態適用・接続寿命。描画完了をCoreへ要求しない |
| TUI | draft、入力、選択画面、Markdown・折返し、scrollbackへの確定出力、live tail、footer |

Core会話moduleはUIの色・幅・Markdownを持たず、将来のWeb clientでも同じ会話契約を使える。
UIへ順序の決定や保存payloadの解釈を押し出さない。

## Goでの制御方針

利用者の補足により、旧Data Workerの処理順・Worker配置の再現は要求しない。
参照するのは解決した問題、状態所有と完了境界、実測で効果があった点である。
Goではnet/httpのrequestとrunを独立goroutineで進め、必要な共有状態だけを短いlockで保護できる。
goroutineは同じheapを共有するため、DenoのMessagePort・transfer・描画ACKをGo内部に再現せず、
実際の所有者・競合・処理量からmoduleと同期方法を決める。専用goroutine／processを置くこと自体は完成条件にしない。

1. run registryのlockは識別・予約・context操作に限る。長い会話snapshotの展開・保存・JSON化・履歴復元を
   そのlockと同じ待ちにしない。run別のstate ownerと短いlock、またはimmutable snapshotの公開方式を比較する。
2. live会話は受けた変更から更新する。stable entity IDと意味的positionを定義し、途中本文がfinalへ置き換わっても
   二重追加や順序移動を起こさない。root／childのscopeを保持する。
3. 表示更新は通知を合流できるが、tool結果・制御受付・保存事実を単なる表示通知として捨てない。
   終了結果は購読者への送信成功に依存せず、状態参照と再接続snapshotでも得られる。
4. 保存のbatch化を導入する場合、accepted sequenceとdurable sequenceを区別する。
   terminal・会話採用は必要なdurable prefixとWorker joinを待つ。cancel受付はこのbarrierの外に置く。
   保存barrierと描画／配信ACKを共通にしない。
5. goroutineを追加する場合、各channel送信がどこまで待つかを記述する。
   遅い購読者の待ちがAgent callback、DB writer、制御受付へ戻る経路を作らない。
6. SQLiteの1connectionと累積本文payloadの増加は測定済みの確認点である。
   DDLを維持したbatch transaction・読み出しの分離・変更entityだけのserializationを検討する。
   WAL／reader connectionの追加は実測なしに採用しない。保存payload変更は契約と対応付けて決める。

旧実装のbuffer数・timer・retry・上限は引き継ぐ要件ではない。

## 肥大化と重複処理の削減

利用者が旧HenjiでDBとメモリの肥大化に苦労したことを明示したため、この削減はI-06の必須対象とする。
根拠は[S-06追加計測](../results/s06-tui-core.md)：64KiBの本文に対する約18.16MBのDB、
stream中のGo累計割当約395.63MB、変更のないsnapshot再復元の割当約74.62MB。
累計割当と保持heap・RSSを区別し、一回のrunの数値から長時間利用時のリークを断定しない。

1. 本文のdelta・最新値・確定messageの役割を整理し、更新ごとに同じprefixを保存・hash・parseする量を減らす。
   batch化によるtransaction数の削減と、保存byte量の削減は別々に確認する。DDLは維持する。
2. Go内部の型付き事実と、SQLite payload／HTTP JSON／JS bridgeの形式を区別する。
   同じ本文を何度もJSON化して再parseする箇所を減らす。HTTP＋JSON＋SSEは維持する。
3. 更新時は変更entityだけを処理し、変更のない確定部分のMarkdown・折返し・差分計算を繰り返さない。
   同じ公開revisionを複数clientへ送るときのserializationとsnapshot再構築の重複も減らす。
4. 終了／再接続の会話復元で、inspection用の全record・content・relationを毎回読む経路を置き換える。
   必要な会話情報だけを読む。liveと履歴で変換規則を共有し、毎回全履歴を再生することを共有の条件にしない。
5. run管理の保持結果、保存済み本文、公開snapshot、TUIの会話cacheについて、同じ内容の全量保持を重ねない。
   run識別・terminal結果の取得・Session履歴の利用動作を保ちながら、保存先とlocatorを使う構成も比較する。
   既存dataの削除や会話量の制限によってこの課題を閉じない。
6. 同じ本文量で更新回数が違うケースと、同じSessionを使い続ける経路で、DB増分・累計割当・GC後保持量・
   process RSSを区別して確認する。ユーザーが必要とする継続利用の実経路へ対応する測定とする。

実装前に重複する生成・保存・参照経路を列挙し、置き換え時には不要になる処理と保持先も同じ変更で整理する。
「Goなので軽い」「非同期にしたので軽い」を受入根拠にしない。

## 実装前に確定する契約

- 公開会話entityの種類、ID、execution／request／stepとの対応、意味的position、complete状態。
  現行journalは保存ID／ordinalをprogressへ渡していないため、保存済みfactの識別をnormalizerへ渡す経路も定義する。
  journalのmutexを解放してから呼ばれるprogressの到着順だけでは、並行観測の保存順を保証できない。
- liveと履歴の同じnormalizerへの入力、tool preview／詳細locator、公開thinkingのprovider観測。
- Session過去会話から現在runへの境界と、snapshot／更新／再接続でのrevisionの扱い。
- 保存batch化を行う範囲とpayload保持、失敗時の既存storage error経路、terminal barrierの所有者。
- Core会話・HTTP・clientの状態所有とlock／channelの待ち先。

これらを仕様化してから製品実装へ進む。会話の全履歴復元をイベントごとに追加する実装は避ける。

## 実装順序と確認

1. S-06の通常画面とCore停滞実験を完了し、実測と未確認点を記録する。
   Bubble Tea標準rendererでtmux resize時のlive本文重複が再現したため、旧scrollback writerの
   reflow／live band処理を参考に独立writerを比較する。通常画面を採用する方針は維持し、
   rendererの方式はこの確認後に決める。
   旧現行実装はresize時のsealとsource offsetの出力receiptでも再追記を防いでいる。
   writerだけを移植する案に限定せず、入力処理を含むGo側の最小構成で比較する。
2. 公開会話契約・normalizer・Coreの制御と配信境界を詳細化する。
3. Core会話経路、必要なprovider観測、HTTP／clientを接続し、保存・再接続と順序を確認する。
4. TUIを通常画面の会話と入力に置き換え、使われなくなる現行表示処理を整理する。
5. Linux実利用で送信→stream→tool→最終応答、複数turn、日本語編集、resize、scrollbackの検索・コピー、
   キャンセルを確認する。Ghostty→SSH→tmuxの端末側確認はlocal PTY・private tmuxの結果と分ける。

検証は具体的な製品動作へ対応付ける。描画・SSE writeを止める実験は、利用者が指摘した旧回帰へ対応する。
保存完了・再接続の確認は、表示通知の合流によって本文やterminalが失われないことへ対応する。
長い会話の制御応答と割当量は拡張後にも測定し、現在の小さいprojectionの結果だけで完成扱いにしない。
