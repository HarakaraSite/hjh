# I-06：旧Henjiに沿った会話画面とCore会話経路

2026-10-11。実装・テスト・通常／批判的reviewを実施した。利用者は旧Henji TUIの操作・表示要件の踏襲と段階的実装を指定し、
通常画面の検証をスパイクとして許可した。さらにCore拡張を指定し、旧Data Workerと`_refs`の参照を要求した。
製品経路の確認とreview指摘の修正後、利用者のGhostty→SSH→tmux確認で最終応答の分断が判明した。
描画を修正し、同じSessionの逐次表示・完成表示の全文比較と保存済みSessionの再開を確認した。
利用者が表示順を確認した。続く指示でtool二重表示と公開thinking／summaryを修正し、
実OpenRouterのthinking途中表示、tool一行、cold再開を確認した。公開summaryの実provider表示は未観測。
[S-06](../results/s06-tui-core.md)はこの計画の実行証拠である。
2026-10-11の追加指示により、旧incrementの肥大化対策を照合し、保存・読取・保持の整理を
Core会話経路の拡張より先に行う計画へ更新した。以下の契約変更はI-06の実装対象である。完成状態と実測・未確認点は
[I-06結果](../results/i06-conversation-tui.md)へ記録する。

## 現行状態への案内（2026-10-16 source照合）

I-06は実装済み。以下の「現行経路」「実装前」「DDL v3維持」「WAL未採用」等は、
2026-10-11の計画・比較対象を記録したもので、現在の未実装や保存方式を表さない。
実装時の証拠は[I-06結果](../results/i06-conversation-tui.md)、現行保存契約は
[schema v4](../design/history-schema.md)・[WAL採用結果](../results/history-wal-first-sequence.md)を参照する。
原文描画・色やGlamourの後回し・旧footer配置も当時の範囲であり、現在は
[Markdown整形](../design/tui-rendering.md)と[統合TUI](../design/tui.md)・[footer](../design/tui-footer.md)に従う。
段階表には後続I-12の実装状態を補記するが、過去計画・実測・確認範囲そのものは書き換えない。

## 利用者の必要な動作

2026-10-11追加指示：旧Henjiの表示を先に目指し、色・Glamourは後回しにする。
確認済みの問題は、IME実cursorが入力先頭に残ること、入れ子toolのREADME結果が通常会話へ3回
展開されること、空assistant行が残ることである。editor実cursor、tool短い行と保存位置による
詳細読取、原文記号・空行を維持する本文描画、入力の下のfooterを今回の修正範囲とする。
`/tools`と`/tool`は結果全文を通常会話から外しても閲覧できる最小の操作として前倒しする。
reviewは旧Henjiの現行HTTP TUIコードと対比し、通常行・詳細・本文順序・IMEcursor・scrollbackの
重複／欠落を確認した。今回の修正に追加の必須findingはなかった。直接model toolのruntime代理呼出が
別entityになり同名toolが二重行になる既存契約差分は、次の利用者指示で修正した。
Agentとruntimeはmodel call IDを共用し、代理開始・結果の重複保存を撤去した。
公開thinking本文・summaryのitem key／種別を受信・保存・履歴・DTO・TUIまで維持する。
確認範囲と残件は結果文書の追加修正に記録した。
色の一致、狭幅tableの再整形、picker等の全面移植は今回の確認範囲に含めない。

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
| 2 | I-07 | Session picker、rename、detachとCore停止、実行中Sessionへの再接続。[計画](i07-session-management.md)。利用者指示で/viewは除外、tool詳細は現状維持。deleteは今回含めない。 |
| 3 | I-08／後続 | provider／model／effort picker、検索、保存済み認証の再利用は[I-08計画](i08-connection-selection.md)。お気に入り保存、認証画面、Core自動起動は後続へ分ける。 |
| 4 | I-09 | F2 follow-up、F3 steering、受付と適用状態、実行中draft保持。[実装結果](../results/i09-runtime-input.md)。利用者確認済み。 |
| 5 | I-10実装・確認完了 | `/context`、`/recall`、停止した実行の内容を次の実行へ渡す操作。[結果](../results/i10-context-recall.md)。 |
| 6 | I-11実装・確認完了 | 利用者が選択したモデルのお気に入り保存。[計画](i11-model-favorites.md)・[結果](../results/i11-model-favorites.md)。 |
| 7 | I-12実装済み（後続更新） | 利用者が選択したTUIの認証・アカウント選択画面。[計画](i12-authentication-ui.md)・[契約](../design/i12-authentication-contract.md)・[当時のreview結果](../results/i12-plan-contract-review.md)・[実装結果と実確認の限界](../results/i12-authentication-ui.md)。この表の旧「計画・契約review完了／実装未着手」は計画当時の状態。 |

後続のincrement番号・詳細な区切りはその計画時に確定する。
旧`/resume`はSession続行、`/recall`は次の実行に使う内容の準備であり、停止executionの再実行とは区別する。
未実装操作を実行できる表示にはしない。

旧スラッシュコマンド14個の配置は、I-06に`/help`、Session段階に`/new`・`/sessions`・
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

## 旧incrementの確認と今回の採用範囲

参照元は `/home/agent/projects/henji-harness/docs/increments/`。記録は当時の実装・測定の証拠であり、
旧文書の完了表示だけをhjhの機能要件や効果の保証にしない。

| 旧記録 | 確認した対策・効果 | I-06への対応 |
| --- | --- | --- |
| 134 | 途中本文は最新stateへ置換し、完了・停止時に引き継ぐ。4/40更新でも保存件数・最終DB容量は同じだが、WAL量は増えた。 | 累積途中本文の永久追記と最新stateの二重保存を撤去。保存容量と書込み量を別に測る。 |
| 194 | Promise.raceの未解決待機が古いframeを保持。event queueで解消。 | Goの購読・goroutine・channel・closureが保持する旧snapshotを寿命表で追う。Promiseの実装は移植しない。 |
| 195/196 | 全factの本文配列化を逐次読取へ変更。metadataは必要なkindとSQL COUNTだけ読む。 | 会話復元・metadata・詳細inspectionを分離し、復元中の一括保持と無関係な本文decodeを減らす。 |
| 198 | 不変prefixと可視範囲の結果を再利用。2,552更新のTUI peak PSSが170.7→112.0MiB。 | 確定出力・live tail・editor/footerの依存を分け、変更のない本文の加工を繰り返さない。 |
| 199/210 | canonical二重所有、旧本文parse、artifact再導出、同じsnapshot再要求、pending再照会、同値JSON比較を整理。 | 一つの保存結果から必要なconsumerのviewだけを作る。metadata操作は会話本文の生成経路を通さない。 |
| 214 | 未使用のJSON/UTF-8サイズ計測を撤去し、モデル入力copyを所有境界へ集約。安定したRSS改善は未確認。 | 型付きGo事実を使い、残すcopyの用途を説明する。I-06表示経路のcopy整理をモデル入力予算の変更へ広げない。 |
| 218/219 | 完了execution索引の解放漏れを同一Sessionの200turnで発見。本文保持整理後にもRSS増加・allocator保持が残り、trimの効果は別測定。 | 本文と索引を両方追い、同一Session継続で確認する。context予算・表示上限・有限cache値・native trimはI-06へ自動採用しない。 |

旧記録の定量値は同条件の比較結果に限る。Go側の削減量はS-06と変更後の同条件測定で確認する。

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

## 保存・読取の具体化

現行 `internal/app/journal.go` はassistant_textごとに累積本文をAppendOccurrenceし、その後に
SetAssistantTextでも最新本文を保存する。`EndExecution`は残るstateをsemantic記録へ移すため、
完了stepのstate終了も接続しないままでは同じ本文をさらに残す。
`internal/corerun/subscription.go`はterminal snapshotごとにInspectを再帰呼出しし、全観測をdecodeして復元する。
これらが最初に置き換える実経路である。

### 途中本文と確定本文

- request／execution／lane／model stepを識別し、生成中は既存assistant_text_statesの最新本文を置換する。
  途中本文の累積snapshotを永久semantic recordへ毎回追記する経路を撤去する。個々の表示更新の時刻・全版を
  永久保存する契約は設けず、live表示では受信した更新から最新本文へ進む。
- 同じrequestの完了model resultを保存するtransactionで対応するstateを終了する。
  完了stepの可視本文・tool call/result・model/provider事実は既存の確定記録とmessagesから読めるようにする。
  model result、message、terminal内の本文にはそれぞれ既存consumerがあるため、必要な確定保存を一律に消さない。
- cancel／failure／interruptionでは未完了requestの最新durable本文を一つの未完了semantic記録へ移し、
  state終了とexecution terminalを同じtransactionで行う。先に完了したstepとtool結果は保持する。
  stateがcommit済みなら正常終了処理が走らなくても読み出せる。process終了時の自動reconciliationは追加しない。
- 最初の本文の意味的位置は、途中本文の永久追記に依存させない。最初の観測位置・request identityを保存し、
  active state、完了result、未完了本文で同じentity IDと前後関係を再現する。既存first_event_ordinalの意味と
  semantic ordinal・受信sequence・durable sequenceの対応を、toolを挟む例で詳細化してから実装する。
- 観測の受信、最新stateの保存完了、会話公開を区別する。batchを採用した場合も、表示の通知合流と保存barrierを
  混同しない。tool・制御・request・source等の意味記録は本文state置換の対象にしない。

DDL v3は維持し、新規書込みのpayload・本文state lifecycleの差分を
`docs/design/history-payloads.md`へ実装と同じ変更で記載する。既存DBを削除・書換えない。
旧hjh途中観測を含む既存データの表示は、会話復元に必要な既存記録の読取として扱う。
別のmigration・converterや形式判別のdual-readerを増設する要求ではない。

### 必要なデータだけを読む

- metadataと実行状態の参照はID・状態・件数・必要なmodel選択だけを読む。会話本文、source bytes、context、
  tool詳細を展開しない。詳細inspectionの既存利用者には必要な全文を返す。
  内部の軽い状態照会と、人間向けexecution一覧・詳細はconsumerを分ける。
  現行execution一覧が表示するtask本文を含め、各画面が必要な返却内容は保持する。
- 会話初期復元は保存時点と意味的順序を固定した読取を使い、本文を含む全fact配列と再sort配列を作らず、
  必要な記録を順にnormalizerへ渡す。active最新state・親子execution・確定messageとの対応も同じcutへ揃える。
  一回の復元内で終了・途中return・error時にreaderを解放する。
  現行SQLite 1connectionをreaderが占有する区間と、normalizerが行う処理を契約詳細化で明記する。
  reader保持中に同じStoreへ追加照会して接続待ちになる構成を避け、必要な保存データはその読取経路へ揃える。
  保存・復元の待ちと制御受付の待ちを区別し、reader connection追加の採否は実測から決める。
- live会話は変更entityから更新し、同じrevisionの会話を再接続や複数consumerのために再構築しない。
  同じrevisionのserializationは必要な購読の寿命中に共有できる構成を選ぶ。
  完了後にviewを解放した場合は次の明示読取で一回復元し、保持期間中の変更なしreadはその結果を使う。
- Agentのcontext取得と人間の会話表示は別consumerである。表示の復元改善のために、次requestへ渡す履歴を
  切り詰めない。context予算・過去閲覧のpage UIはこの段階では変更しない。

## 状態の寿命と解放条件

| 状態 | 所有・終了条件 | 終了後に必要な操作の参照先 |
| --- | --- | --- |
| run context・Session予約・実行中request | run管理。Worker join、終了保存・必要な採用が終わった時点で実行資源と予約を解放。 | 同じCoreでのrun照会・同ID再送の識別情報は残す。 |
| journalの途中本文・call開始対応・親子link | execution owner。必要な保存・relation解決と子joinの完了後に参照を解放。 | 保存fact／最新active state／未完了本文。 |
| 完了runのtask全文・final本文・Agent結果 | 実行中はrun owner。保存成功後はexecution locatorと軽いterminal情報へ移し、本文の常時二重保持を解く。 | 結果要求時の限定読取。保存失敗・execution未生成では必要な結果をCoreに保持する。 |
| run ID・request一致判定・最終seq・execution locator | run管理。現行のCore生存中の照会と再送契約に必要な小さい索引を保持。 | 本文を含むrequest DTOの代わりに一致判定に必要なfingerprintと識別情報を使う案を詳細化する。 |
| 会話view・entity索引・親子索引・serialization | Core会話owner。実行中または会話consumerが参照している間だけ保持し、両方の終了後に解放する。 | 次の明示会話読取で保存済み事実から復元。consumer不在時は配送JSONを生成しない。 |
| SSE待機・取得したsnapshot・旧revision | 購読owner。書込み完了、切断、購読取消で参照とgoroutineを解放。 | 再接続snapshot。run完了・Core制御は購読終了待ちを条件にしない。 |
| TUIの本文加工結果・旧frame・live tail | TUI。変更、確定出力、scope切替、購読終了で不要な版を解放。 | 必要な会話sourceと端末scrollback。現段階で過去本文の表示上限は設けない。 |

小さいrun ID索引は現行契約上Core生存中に増える。この計画で総RAMが定数になるとは主張しない。
本文・snapshot・完了executionの重いownerをその索引へぶら下げないことを確認する。
同ID再送で新しいrunを開始しないこと、terminal取得、既存購読のCloseが成立することを保つ。
現在の`Subscription.Close`はrun map参照に依存しているため、解放条件と同時に所有関係を整理する。
GET terminalがDB読取不要というI-05の実装方式は、結果の限定読取へ変更する場合にHTTP契約へ明記する。
保存errorを持つ結果までDBだけへ押し出して取得不能にしない。
結果のconsumerはGET run、同ID POST再送、terminal済みrunへのcancel応答、SSEのsnapshot／terminalである。
すべて同じ最終RunResultを返す。終了保存後の会話採用失敗ではDB terminalとRunResultが異なり得るため、
採用errorを反映したCoreの結果を保存済みterminalだけで置き換えない。

## 実装前に確定する契約

2026-10-11に以下を[保存・会話・同期・結果取得契約](../design/i06-conversation-contract.md)として確定した。
root/子で共有する受付順、小さい開始markerとlatest state、固定entity ID、短い読取cutと逐次復元、
独立した会話drain、terminal結果resolver、HTTP v2への切替を採用する。
この節の一覧は実装時の照合対象であり、未決事項の一覧ではない。

- 公開会話entityの種類、ID、execution／request／stepとの対応、意味的position、complete状態。
  現行journalは保存ID／ordinalをprogressへ渡していないため、保存済みfactの識別をnormalizerへ渡す経路も定義する。
  journalのmutexを解放してから呼ばれるprogressの到着順だけでは、並行観測の保存順を保証できない。
- liveと履歴の同じnormalizerへの入力、tool preview／詳細locator、公開thinkingのprovider観測。
- Session過去会話から現在runへの境界と、snapshot／更新／再接続でのrevisionの扱い。
- 保存batch化を行う範囲とpayload保持、失敗時の既存storage error経路、terminal barrierの所有者。
- Core会話・HTTP・clientの状態所有とlock／channelの待ち先。
- request一致判定のfingerprint、完了結果の限定読取、execution未生成／保存失敗の保持、run locatorと購読の寿命。
- 最新本文stateの開始位置・完了stepでの終了と、永久途中観測を撤去した後のlive／履歴の対応。

製品実装は上記契約に沿って進む。会話の全履歴復元をイベントごとに追加する実装は避ける。
同期transactionを初期方式とし、timer付き非同期batchはこの契約へ含めない。

## 実装順序と確認

1. S-06の通常画面とCore停滞実験を完了し、実測と未確認点を記録する。
   Bubble Tea標準rendererでtmux resize時のlive本文重複が再現したため、旧scrollback writerの
   reflow／live band処理を参考に独立writerを比較する。通常画面を採用する方針は維持し、
   rendererの方式はこの確認後に決める。
   旧現行実装はresize時のsealとsource offsetの出力receiptでも再追記を防いでいる。
   writerだけを移植する案に限定せず、入力処理を含むGo側の最小構成で比較する。
2. 保存・読取・寿命と公開会話の契約は上記文書で確定済み。実装着手時にconsumer一覧をsourceと照合し、終了result、
   inspect、stream CLI、TUI、子Agentを同じ契約へ接続する。旧実装の対策と今回の対応を照合する。
3. 保存粒度と完了stepのstate終了を変更し、metadata限定読取・会話の逐次復元・完了状態の保持整理を行う。
   途中表示、完了step→tool→次step取消、保存済みactive本文、親子join・終了保存、terminal照会と同ID再送を確認する。
   同じ最終本文を異なる更新回数で生成し、semantic本文件数・payload・DB/WAL量を比較する。
4. 型付き事実からCore会話normalizerを差分更新し、必要なprovider観測を接続する。
   本文→tool→本文、公開thinking、root/child、途中→確定のID・順序がliveと履歴で一致することを確認する。
5. HTTP／clientを接続し、変更revisionだけを配信する。複数consumerと再接続で同じviewを再構築せず、
   遅いSSE writerを止めても別Session受付・キャンセル受付・終了保存が進むことを拡張後の経路で確認する。
6. TUIを通常画面の会話と入力に置き換え、使われなくなる現行表示処理を整理する。
7. Linux実利用で送信→stream→tool→最終応答、複数turn、日本語編集、resize、scrollbackの検索・コピー、
   キャンセルを確認する。Ghostty→SSH→tmuxの端末側確認はlocal PTY・private tmuxの結果と分ける。
8. 同じSessionの継続、terminalの変更なし反復参照、TUI切断・再接続で、保存量、復元／JSON化回数、
   累計割当、GC後保持heap、process RSSとowner・索引・購読数を変更前後で比較する。
   同一本文量の更新回数比較は保存重複、同一Session継続は完了状態の寿命の確認に対応する。
   counterやprofileは一時的な計測として使い、cloneした全文を観測のために常時保持しない。

検証は具体的な製品動作へ対応付ける。描画・SSE writeを止める実験は、利用者が指摘した旧回帰へ対応する。
保存完了・再接続の確認は、表示通知の合流によって本文やterminalが失われないことへ対応する。
長い会話の制御応答と割当量は拡張後にも測定し、現在の小さいprojectionの結果だけで完成扱いにしない。

## 通常レビュー・批判的レビュー

2026-10-11。今回の保存・読取・寿命・実装順序の変更を二人のread-only reviewerが独立に確認した。
両者ともBlocker／P1／P2のfindingなしで、契約詳細化へ進めると判断した。
任意補足の結果consumer全入口、execution一覧のtask表示、逐次readerと1connectionの待ちを上記へ反映した。
製品実装への着手条件は「実装前に確定する契約」の確定であり、reviewは変更後の性能・TUI受入を代替しない。
確認範囲・補足と未確認事項は[レビュー記録](../results/i06-plan-review.md)を参照する。
