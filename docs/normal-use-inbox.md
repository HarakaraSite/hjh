# 通常利用メモ

Henjiの通常利用で得た観測と、まだ個別Incrementへ採用していない改善候補の入口である。

更新日: 2026-10-10（A37・A38をIncrement 223へ採用・移管、A40: editのsed backend案を追加。2026-10-09:
`/reload`の再記録、search返却量とmode誤用、回答・thinking等の表示省略の追加観測、フッターの入力見積もり使用率表示案、キャッシュusage保存案の追加照合）。直前のsource照合:
2026-10-07、source `68ab5dd0`（S20・B5の現行境界）。基盤の照合: 2026-10-05、source
`a78c2076`・Increment 182まで。S26はIncrement 183、A18はIncrement 185へ採用・移設。B11はIncrement
190へ採用・移設。A23はIncrement 191の計画へ採用・移設。 2026-10-06にA31を追加し、A32・A33はIncrement
200・201へ採用・移設（A32: review用Agentの tool構成とinstruction、A33:
read-onlyのgit調査toolとsearchのentry列挙）。 B12はIncrement 203へ採用・移設（binary更新後のprocess
runner起動と早期終了の原因表示）。
2026-10-06にA19（requestごとの実行状況・日時・地域context）は利用者判断で不採用とし、
候補一覧と本項目を削除した。
2026-10-07にS32・S37は[Increment 211](../increments/increment-211.md)、S36は
[Increment 212](../increments/increment-212.md)へ採用・移設した。順序は211の実表示・操作確認後に212。
同日にS35はS36を進める方針のため取り下げ、候補一覧と本項目を削除した。
同日にA34（再起動後の続行Sessionで新規toolがmodel定義に現れない疑い）は、利用者が対応しないと決定し、
候補一覧と本項目を削除した。追加調査・修正は行わない。
同日にA36（非同期の書記官subagentへメモ・handoff更新を任せる案）を、後で試すためのメモとして追加した。
同日にA35とsubagentの制御・待機に関する相談をA24へ統合した。人間による子の個別キャンセルは当面対象外。
同日にS4（`/reload`によるinstruction・Agent設定・toolの再読込）は、Core再起動で更新を反映する
運用で足りるため利用者判断で不採用とし、候補一覧と本項目を削除した。
同日にA9（Sessionと関連履歴の保存期間・一括整理、旧P7を統合）は利用者判断で不採用とし、
候補一覧と本項目を削除した。Sessionと関連履歴は人間が自発的に個別削除しない限り保持する。
保存期限・自動削除・専用の一括整理機能は設けず、大量整理が必要になった場合は、その都度人間が
AgentへDB操作を依頼する。
同日にA30（tool間の結果連鎖）は現時点では不要との利用者判断で取り下げ、候補一覧・本項目・
関連参照を削除した。
同日にA31（workspace外のファイルアクセス境界）は[Increment 213](../increments/increment-213.md)へ
採用・移設した。共通deny、tool別allow、外部toolへの共通API、bashの対象外扱いを実装する。
2026-10-08にrebuildを実装しない判断と、Agentによるroot model・effort変更は効果が薄いため見送る判断を
構想・architecture・roadmapへ反映し、A2の候補から除いた。run_typescriptによる自己拡張は自己改訂の
部分実装として扱い、R1/R2/R3では既存経路で不足する動作だけを未採用候補として残す。
同日にA39（git書き込み系tool）を追加した。tool
grant粒度の衝突と案A（tool分割）／案B（mode単位grant） を併記する。
2026-10-09に利用者の「`/reload`は改めてメモして」という指示でS4を再記録した。
run_typescriptで生成した処理を永続toolへ登録し、同じ会話の続きで使う用途を再検討の契機とする。
現行の不採用方針を変更する承認ではなく、未採用候補としてのメモである。

- ここへの記載は採用、優先順位、実装認可を意味しない。
- 個別Incrementへ採用した項目はその正本へ移し、この一覧から除く。
- 長い実行証拠、参照実装比較、完了経緯は、increment、research、architecture等の担当正本へ置き、
  ここには候補を選ぶための現在の観測、候補、再検討条件だけを残す。
- 一覧IDは文書内の参照用であり、順序や大小は優先度を表さない。

現在の目的・境界・実装状態は[構想](../concepts/experience-driven-self-revision.md)、
[Host/Worker architecture](../architecture/henji-host-agent-worker.md)、
[provider/auth architecture](../architecture/multi-provider-routing-and-auth.md)、[roadmap](../roadmap.md)を参照する。
採用した要件・原観測・受入結果は[個別increment](../increments/)へ置く。

現行はJSON Agent設定・現在tool folder・configuration snapshotとhistory.sqlite3（schema 1）を使う。
以下の過去観測は観測日当時の事実として残し、「現行境界」は現在sourceへ照合する。 廃止済みのmanaged
Definition/transportを候補の必須前提として復活させず、候補自体の採否は利用者へ戻す。
未解決のB8原失敗原因と再発時調査は[176](../increments/increment-176.md)の担当範囲であり、診断拡充だけで原因特定済みとはしない。

## 候補一覧

| ID  | 領域           | 候補                                                                     | 再検討の主な契機                                                                                                       |
| --- | -------------- | ------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------- |
| S4  | Surface        | `/reload`によるinstruction・Agent設定・toolの再読込                      | 生成・登録・編集したtoolを、Core再起動なしに同じ会話の続きで使いたいとき                                               |
| S20 | Surface        | 巨大表示領域での画面サイズ・frame上限の見直し                            | 大きなディスプレイで履歴の空白・古い行欠落が観測されたとき                                                             |
| S22 | Surface        | 将来のWebUI本体                                                          | browserから通常利用する画面が必要になるとき                                                                            |
| S28 | Surface        | `@`によるコンテキスト注入（旧P5を統合）                                  | 人間がファイル内容等をmodel turnなしでcontextへ入れたいとき。採用は利用者判断                                          |
| S29 | Surface        | `/edit`による外部エディタ起動                                            | 利用者が入力編集の外部エディタ連携を採用するとき                                                                       |
| S30 | Surface        | TUIの`/help`とCLI helpの内容統合                                         | 利用者がヘルプ内容の統合を採用するとき                                                                                 |
| S38 | Surface        | 回答・thinking・assistant noteの一律表示省略で判断に必要な情報が読めない | 通常画面で回答や判断材料が途中で切れる。必要な情報を最後まで読める動作の回復を個別incrementへ採用するとき              |
| S39 | Surface        | フッターに入力トークンの見積もり使用率を表示する                         | 利用者が上限への接近と履歴切り詰め後の使用率を通常画面で把握する機能を採用するとき                                     |
| A2  | Agent実行      | Host操作のmodel向けtool化                                                | AIがSession列挙・詳細取得・選択や自身の実効構成readbackを実taskで必要とするとき                                        |
| A3  | Agent実行      | Context Strategyの外部化                                                 | 長期Sessionのtoken usageとcontext品質を実測で比較できる                                                                |
| A5  | Agent実行      | ambient情報のinstruction化（repository context・実行環境）               | ambient remoteの誤認・repository探索の再発、またはAIが実行環境のambient情報を知らない／instructionだけでは足りない事例 |
| A11 | Agent実行      | instructionの与え方                                                      | 指示の粒度や配置によってtaskの完了挙動が変わるとき                                                                     |
| A21 | Agent実行      | 1ターン内でsteeringを複数回受け付ける                                    | 実行中に追加の指示を続けて送りたいとき                                                                                 |
| A24 | Agent実行      | subagentの委譲と実行中の制御・待機（旧A35を統合）                        | 利用者が委譲・進捗・追加指示・時間管理・待機方法の改善を個別incrementへ採用するとき                                    |
| A27 | Agent実行      | providerの一時的な応答中断に対する自動再試行                             | recallでの手動継続が負担になる、または利用者が自動再試行の検討を再開するとき                                           |
| A29 | Agent実行      | request単位のtoken usage・cache再利用量の保存とreadback                  | token消費の内訳やcontext整理・cache改善の効果を把握したいとき                                                          |
| A36 | Agent実行      | 非同期の書記官subagentによるメモ・handoff更新                            | 利用者が既存subagent経路で試すことを指示するとき。今回はメモのみ                                                       |
| A39 | Agent実行      | git書き込み系tool（`git_inspect`との分割）                               | bashなしsubagentへcommit/pushを委譲する実需が出たとき。案A／案B・mode範囲・名称はその時点で決める                      |
| A40 | Agent実行      | editツールのbackendにsedを使う案                                         | 利用者がeditのbackend見直しを検討するとき                                                                              |
| B5  | 保存履歴       | commit却下時の検証不合格項目を特定できない                               | 却下の再観測、または項目別理由の記録・原因調査を個別incrementへ採用するとき                                            |
| R1  | F24            | 自己改訂対象の重心とagent loop境界                                       | 自己拡張・改訂の経験参照・振り返り・継続利用に具体的な不足が出たとき                                                   |
| R2  | F24            | tool改訂の版・使用内容の記録とMCP                                        | 生成処理やtool変更の継続利用に不足が出る、または具体的なMCP integrationを採用するとき                                  |
| R3  | F24            | tool実行profileとsandboxed Deno program                                  | 既存run_typescriptとは別に、trusted-local以外の実行環境をproduct要件にするとき                                         |
| R4  | F24            | instruction componentのrevision化                                        | instructionを自己改訂candidateとして採用する                                                                           |
| E1  | 配布・外部化   | Agent設定・tool以外のresource外部化                                      | 通常利用で更新・共有・rollback・分離実行が必要になる                                                                   |
| E2  | 配布・外部化   | 追加managed resource kind候補（未採用）                                  | 各kindを通常利用で更新・pin・transport・activationする必要が出る                                                       |
| E3  | 配布・外部化   | Host runtime tunablesの設定ファイル化                                    | provider timeout・tool限界・maxSteps既定などを通常利用で調整したくなるとき                                             |
| E5  | 配布・外部化   | 追加protocol adapter候補（Anthropic Messages／Google／Azure OpenAI）     | 該当providerを通常利用で使う必要が出るとき。Increment 101のauth/header一般化を前提にする                               |
| P3  | 参照実装parity | 手動`/compact`（checkpoint/compactionの人間起動）                        | context圧縮を人間が明示的に行いたくなったとき                                                                          |
| P4  | 参照実装parity | Session export/import                                                    | Sessionを別installationへ移す・再開する必要が出るとき                                                                  |
| P6  | 参照実装parity | model cycling shortcut                                                   | provider横断のmodel切替を頻繁に行うとき                                                                                |
| P8  | 参照実装parity | configurable keybindings                                                 | keybindingを利用者ごとに変えたくなったとき                                                                             |
| P9  | 参照実装parity | 画像入力（`@image`／clipboard paste）                                    | 画像を扱うtaskを通常利用で行うとき。transcriptのimage part・provider別encoding・表示・catalog capabilitiesが必要       |
| P10 | 参照実装parity | 外部agent interface（双方向server/RPC／ACP）                             | editor/IDE統合や別agentからの対話的駆動が必要になるとき。一段目の一方向structured outputはIncrement 104で実装済み      |

## Surface

### S4 — `/reload`によるinstruction・Agent設定・toolの再読込（再検討、未採用、メモのみ）

- 利用者の相談（2026-10-09）: run_typescriptで作ったプログラムをhenjiへtoolとして登録し、永続的に
  使えるかを確認したうえで、`/reload`の必要性を再検討し、改めてメモするよう指示した。
- 現行確認: run_typescriptの生成codeは自動でtool登録されないが、ToolFactory形式のentryとtool.jsonを
  保存し、tools.jsonのfolder登録とAgentのtools一覧への追加で継続利用できる。設定・toolは新Worker
  起動時に読み込み、起動済みWorkerへの即時反映はない。現行は新Worker起動・Session再開・Core再起動を使う。
- 必要な動作の候補: 生成・登録・編集したtoolを、Core全体の再起動で作業を中断せず、同じSessionの
  会話履歴を保って次のturnから使う。人間がidle時に`/reload`を実行し、instruction・Agent設定・toolを
  読み直す案。
- 実装案（未決定）: 同じSessionの履歴を引き継いでWorkerを作り直し、現在の設定とTypeScript entryを
  読み込む。既存Workerの構成をその場で書き換える方式や、Agent向けreload
  toolの追加は今回合意していない。
- 過去判断との関係: 2026-10-07はCore再起動で足りるため不採用とした。生成toolを同じ会話で続けて使う
  用途が、その判断を再検討する具体的な契機となる。今回の記載は採用・実装認可ではない。
- 未確認: 通常利用でのtool生成・更新頻度と再起動の負担。reload時に維持するSession状態と再読込対象の
  詳細は、採用時に現行経路へ照合する。
- 再検討条件: 生成・登録・編集したtoolを、Core再起動なしに同じ会話の続きで使いたいとき。
- 関連: R2、A2（Host操作のmodel向けtool化とは別候補）、
  [現行の設定・tool変更の反映](../architecture/henji-host-agent-worker.md#設定tool変更の反映)、
  [外部toolの登録形式](../../README.ja.md#agent設定とtool)。

### S20 — 巨大表示領域での画面サイズ・frame上限の見直し（F01）

- 原観測（2026-09-26、参照実装調査の実測）: 大きな表示領域で当時のTUIが画面を埋められなかった。
  `HISTORY_WINDOW_ENTRIES` 48／`HISTORY_WINDOW_BYTES` 1 MiBのwindowはentry数・文字量で決まり、
  画面が要求する行数は保証しない。実測で512×200のとき、window30 entry・63 KBで生成行120行に対し
  必要196行（不足分は空行padding）。entryが短い対話ほど起こりやすい。加えて`MAX_ROWS` 200／
  `MAX_COLUMNS` 512でclampされた外側は空白になり、512×200＋CJK本文ではframeが `MAX_FRAME_BYTES` 128
  KiB（実測147 KB、SGR未計上）を超過して`renderFrame`が古いlog行からtruncateする。
- 現行境界（2026-10-07、source照合）: [Increment 198](../increments/increment-198.md)で 48件/byte
  window、全候補行の結合/slice、旧全文layoutを撤去し、本文位置から可視範囲を取得する方式へ
  置換した。旧windowの行量不足を理由とする変更案は残候補にしない。 `MAX_ROWS` 200・`MAX_COLUMNS`
  512・`MAX_FRAME_BYTES` 128 KiBは残っている。
  原観測の512×200条件での空白・行欠落は198後に再測定しておらず、同じ現象が残るとは断定しない。
- 残る候補: 巨大表示領域での画面サイズclampとframe上限を、現行経路での実測に基づき見直す。
- 再検討条件: 大きなディスプレイ利用が日常になり、履歴の空白・行欠落が観測されたとき。
- 関連: `v0/tui/layout.ts`（`MAX_ROWS`／`MAX_COLUMNS`／`MAX_FRAME_BYTES`）、
  `v0/tui/tui_renderer.ts`（frame予算）、[Increment 198](../increments/increment-198.md)、
  [画面表示比較](../research/pi-opencode-henji-screen-display-comparison.md)。

### S22 — 将来のWebUI本体（F01、F10、未採用）

- 候補: 独立HTTPコアと共通のschema・client・操作契約を利用するWebUI本体。
  terminalに固有のeditor・key操作はbrowserのUIへ置き換える。
- 再検討条件: TUI分離後、browserから通常利用する画面を採用するとき。
- 関連: 採用済みの独立HTTPコア・TUI分離・第三者APIは
  [Increment 139](../increments/increment-139.md)と
  [8slice計画](../plans/s22-detailed-design-and-slices.md)を参照する。
  現行は[複数Core](../increments/increment-153.md)で別Sessionを並行実行できる。
  WebUI本体とACPは未実装であり、HTTP/API Workerの存在だけで成立済みとしない。

### S28 — `@`によるコンテキスト注入（未採用、メモのみ）

- 参照実装調査（2026-09-22、旧P5）: `@file`によるファイル内容の注入を候補として記録した。
- 利用者意向（2026-09-29）: 将来`@`によるコンテキスト注入を行うかもしれないが、
  行わない可能性もある。今回はメモのみ。
- 候補: TUI入力で`@`を起点とするコンテキスト注入。具体的な用途として、人間が`@file`で
  ファイル内容をmodel turnなしでcontextへ渡すことを含む。注入対象、操作、contextへの渡し方は未決。
  workspaceパス補完の廃止判断をこの候補で取り消さず、既存処理を将来用に残す要件とも扱わない。
- 再検討条件: 人間がファイル内容等をmodel turnなしでcontextへ入れたい実例があり、
  利用者が必要性を判断してこの候補を個別Incrementへ採用するとき。
- 関連: [Increment 159](../increments/increment-159.md)、
  [Pi／Zot参照実装比較](../research/pi-zot-command-surface-comparison.md)、
  P9（画像入力は別候補）。今回のフッター・ショートカット整理では実装しない。

### S29 — `/edit`による外部エディタ起動（未採用、将来候補）

- 利用者意向（2026-09-29）: 単語・行頭／行末までの削除キーを省く方向に関連して、
  `/edit`でエディタを起動する機能をいつか実装したい。
- 候補: 入力編集に外部エディタを使う。エディタの指定方法、draftの受け渡し、
  編集後のTUI復帰操作等は採用時に決める。今回は記録のみ。
- 再検討条件: 利用者が入力編集の外部エディタ連携を個別Incrementへ採用するとき。
- 関連:
  [Increment 159](../increments/increment-159.md)。今回のフッター・ショートカット整理では実装しない。

### S30 — TUIの`/help`とCLI helpの内容統合（未採用、メモのみ）

- 利用者意向（2026-09-29）: F1 helpの内容をCLIのヘルプと統合するか、別件としてメモする。
- 現行境界: [Increment 159](../increments/increment-159.md)でF1はSession一覧へ変更し、 TUI
  helpは`/help`へ集約した。CLI helpとの内容統合は未採用である。
- 候補: TUI helpとCLI helpの内容を整理・統合する。対象となる内容と統合方法は未決。
- 再検討条件: 利用者がヘルプ内容の統合を個別Incrementへ採用するとき。
- 関連: [Increment 159](../increments/increment-159.md)、TUIの`/help`、CLI help。

### S38 — 回答・thinking・assistant noteの一律表示省略が人間の判断を妨げる（完全な修正は未採用）

- 暫定対応の採用（2026-10-09）: 利用者が表示上限の十倍化を指示したため、2,048→20,480 bytesの
  上限変更とその確認を[Increment 222](../increments/increment-222.md)へ移した。
  完全な取得・保持・表示方式の修正は後回しとする。本項目は暫定上限を超える場合も含む残候補である。

- 原観測（2026-10-09、production TUI）: Session `17c48f73-7d8d-4965-b5e9-2445cbe76c62`の
  第6ターン（Execution `2d457f42-89eb-4d9e-bebd-369f9a0280eb`）は正常完了したが、最終回答 3,187
  UTF-8 bytes／1,757文字が第3項の途中で`… [3187 bytes; open detail]`へ置き換わった。
  後半の要点と限界・未確認事項を通常画面で読めない。モデル結果、runtime
  outcome、保存済み最終messageの 本文は末尾まで一致し、モデル出力や保存履歴の欠落ではない。
- 観測時の経路（source `90696061`）:
  `v0/conversation/history_adapter.ts`の`boundedText`が表示用本文を
  `CONVERSATION_PREVIEW_BYTES = 2_048`で省略し、`v0/tui/keyed_conversation_store.ts`はそのtextを表示する。
  最終回答だけでなく、assistant progress／tool
  callを伴う本文（`assistant note>`）、thinking／summaryにも
  同じ規則が適用される。thinkingの途中省略も実TUIで観測した。assistant noteは確認した実例が短いため
  省略を観測していないが、長い場合に同じ処理を通ることはsourceで確認した。tool引数・結果・進捗にも
  表示用省略がある。モデルへ送るcontextの予算・projectionとは別経路である。
- 導入意図と問題:
  [Increment 218 §11.4](../increments/increment-218.md#114-会話page大きい本文全出力)は
  Data／Core／TUIへの全文の重複保持・更新ごとのコピーを減らし、page／detailで必要部分を取得する設計だった。
  2,048 bytesは実装上の固定値で、確認した設計文書にこの値の指定はない。今回の約3KBの回答を省略する
  必要性を示す実測根拠は未確認であり、メモリ削減が通常利用の読み取り・判断を損なっている。
- 利用者の判断・必要な動作（2026-10-09）: 問題はRAM上の重複保持・コピーであり、大きい本文でも
  人間の判断に必要な情報を省略する理由にはしない。回答、thinking、assistant noteの必要な内容を
  通常利用で最後まで読めることを守る。「全文は保存されている」「別操作でdetailを開ける」だけを
  成功条件の代替にしない。メモリ削減そのものを目的にしてproduct機能を損なわない。
- 本文閲覧UIへの利用者評価（同日）: `/view`で会話ページを開き、項目を選択してEnterで本文を開く
  現行手順は、本文省略の救済策として「使えない」と判断した。通常の回答を読むために別画面で項目を
  探す操作を要求せず、通常表示で必要な内容を最後まで読める動作を回復する。これは`/view`自体の
  廃止・再設計を採用したという意味ではない。
- 改善候補: 重複保持・全文コピーを減らす内部設計と、人間が読める内容を分ける。必要な本文を順次取得・
  表示するなど、通常の読み取りを途中で切らずに成立させる。具体的な取得・保持・表示方式は採用時に決める。
  暫定上限の十倍化を、完全な修正の完了として扱わない。
- 証拠: local観測は`.tools/session-17c48f73-observation-20261009/turn-6-final-answer-display.json`、
  保存本文の読出しは同directoryの`turn-6-final-answer.txt`。生成artifactはgit管理外であり、
  元のExecution／semantic履歴を正本として再照合できる。
- 再検討条件: 利用者が完全な表示・保持方式の修正を個別incrementへ採用するとき。
  十倍化の採用・実装認可を完全な修正の承認へ広げない。

### S39 — フッターに入力トークンの見積もり使用率を表示する（未採用、メモのみ）

- 利用者意向（2026-10-09）: Session `17c48f73`の履歴切り詰めは機能していそうだと判断し、
  フッターに見積もりトークン量の使用率を表示してほしいとメモを指示した。
- 表示案: 左側にprovider／model／effort、右側に`tokens 99%`を表示する。

  ```text
  opencode-go-chat / deepseek-v4.1-flash / effort                                  tokens 99%
  ```

- 使用率の意味: 直近のモデルrequestの入力トークン見積もりを、実効入力上限で割った割合。
  Session全体の累計消費量ではなく、履歴を含むそのrequestが切り詰め判定の上限にどれだけ近いかを示す。
- 観測根拠: 第17ターンの最後は見積もり798,515／上限800,000（約99.8%）、provider実測は 635,155
  tokensだった。第18ターンでは第1ターンが送信対象から外れ、最後の見積もりは735,952
  （約92.0%）へ下がった。利用者が通常画面でこの変化を把握できる表示を候補とする。
- 再検討条件: 利用者が本表示を個別incrementへ採用するとき。今回はメモのみで実装しない。
- 関連:
  [Increment 221](../increments/increment-221.md)、A29（request単位の実測usage保存・readback）。

### 画面表示の参照実装調査で見送ったもの（Pi／OpenCode、2026-09-26）

Pi／OpenCode／Henjiの画面表示比較
（[`pi-opencode-henji-screen-display-comparison.md`](../research/pi-opencode-henji-screen-display-comparison.md)）
から、現時点では取り入れないもの。記録のみで採用・実装を意味しない。

- Piのmain screen履歴（会話をterminal scrollbackへ残す方式）: Henjiはalt screen＋内部window方針で、
  Increment 128／130の履歴閲覧・履歴位置表示と整合しない。描画コストも本質的には変わらない
  （Piもdocument全体を毎frame計算）。 再検討条件:
  履歴閲覧の要求が現行window方式で満たせなくなったとき。
  2026-10-07の利用者による再検討は[Increment 212](../increments/increment-212.md)へ移設した。上記は調査当時の見送り理由である。
- message jump（Pi／OpenCodeのmessage単位移動）: 利用者判断でP2として除外済み（PageUpの方が手軽）。
  調査記録に残す。
- テーマ／256 color／truecolor:
  調査当時は色に関する観測された不満がなく、Surface変更が大きいため見送った。
  配色変更前のsourceは一部に256色指定を使っており、この記録は256色未対応を意味しない。
  2026-10-05の配色変更は[Increment 193](../increments/increment-193.md)へ採用した。
  テーマ切替・truecolor対応の採用は未決。
- Piの`CURSOR_MARKER`（hardware cursor指定によるIME候補位置合わせ）:
  Henjiはframe末尾のcursor位置指定で 入力位置を示しており、目的は現状で満たしている。 再検討条件:
  IME候補位置がずれる観測が得られたとき。
- xterm.js仮想terminal（`@xterm/headless`）のtest基盤:
  単独では採らない。Surface変更を採用するincrementで、
  frameの実挙動検証が要件に直結するときに限って検討する。 再検討条件:
  terminalの実表示・操作確認を補う具体的な必要が出たとき。

## Agent実行

### A2 — Host操作のmodel向けtool化（F02、F06、F10、F28）

- 観測: 人間向けのSession一覧・履歴参照があり、Agentもbash/history等から間接的に参照できる。
  Agentが作業中に必要なSession・execution・実効構成を取得する入口は未整備である。
- 候補: slash command文字列をmodelに擬似入力させず、Host-owned application serviceへ型付きcommand
  handlerとtool
  handlerを接続する。Session列挙・詳細取得・履歴readbackから、実taskで必要な操作を選ぶ。
  Session切替を採用する場合は、read-only取得と呼出元contextへの適用を分け、結果を返す順序を定める。
  reload/rebuildとAgentによるroot model・effort変更は候補に含めない。
- 再検討条件:
  AIがSession列挙・詳細取得・選択、または自身の実効構成のreadbackを実taskで必要とすること。
  UIだけに意味があるcommandや 人間の明示選択が目的のcommandまで一律にtool化しない。

### A3 — Context Strategyの外部化（F02、F06、将来のF24候補）

- 観測: 64 KiBでのtool-result機械的省略とpre-turn automatic semantic compactionの停止はIncrement
  29で 採用済みである。将来のcompactionは容量対策だけでなく、何を覚え、捨て、抽象化するかを決める
  Context Strategyとして扱う必要がある。
- 候補: 発動判断、対象選択、保持予算、semantic summary、model、failure方針、結果説明を交換可能な
  component境界にする。Agent設定から選ぶ場合も、canonical transcript、checkpointの永続化と相関、
  tool call/resultの因果構造、credential、provider
  evidence、strategy結果の採否はHenji-owned境界に残す。
- 利用者との検討（2026-10-05、アイデアのみ）:
  Agentが過去のtool結果を探す`history_search`・`history_read`はcompaction設計と合わせて考える。
  provider error後に、後続実行のAgentが保存済みrequest fact・失敗診断を調べる用途も含む。
  `/recall`による失敗作業の引継ぎは人間が判断し、履歴を読む操作と分ける。
  compaction時には、後から元のtool引数・結果へ戻れるIDをcontextに残す方針を検討する。
  保存記録の`occurrenceId`やexecutionとcallの組を候補とし、検索ではtool名・対象path・本文の語句も使う。
  IDだけで参照対象が分かるか、tool名・対象・短い要点を添えるかは未決。
  利用者は、turn完了後はtool結果本文を順次model contextから外してよいのではないかと提案した。
  元の保存履歴を保持し、必要な結果は履歴toolで再取得する構成を候補とする。対象・タイミング・
  保持内容・再取得経路を一体で設計する。現在の通常処理が自動省略しているという意味ではない。
  履歴tool・compaction方針とも、個別incrementへの採用・実装は未承認。
- 利用者との検討（2026-10-07、調査・議論の整理のみ）:
  Pi・OpenCode・Zotの更新後の機能比較から、context品質を優先し、用件の区切りで詳細なtool結果を
  DBに保持したまま、modelには結論・必要な根拠・参照IDを残して、必要時に履歴toolで再取得する案を検討した。
  IDだけを残すか、tool名・対象・要点を添えるか、整理の契機・投影単位・reasoningとの関係は未決。
  cache hit率を目的に不要なcontextを保持せず、回答品質・重複探索・再取得と費用を合わせて考える。
  常時background要約、固定turn数での省略、履歴tool・compactionの採用・実装は承認していない。
  根拠と比較は[機能比較とcontext整理の検討](../research/2026-10-07-reference-feature-comparison-and-context-strategy.md)を参照する。
- 再検討条件: 長期Sessionの実token usage、provider/model
  context契約、turn中/間checkpointを比較できる 利用証拠が得られること。
- 正本:
  [`increment-29.md`](../history/increments/increment-29.md)は既に停止した挙動と維持するcheckpoint境界を定める。

### A5 — ambient情報のinstruction化（repository context・実行環境）（F02、F06）

- 観測: Increment 37 Human Gateで、local target未指定の外部調査taskに対し、modelがworkspaceのGit
  remoteと handoffを探索し、ambient repositoryから得たForgejo hostをtask
  targetとして扱った。repository情報を 知らないための探索と、repository
  contextを過剰にユーザー指定targetへ結び付ける問題を分ける必要がある。
- 観測（実行環境、2026-09-26、実行証拠と利用者観測）: bash toolは`clearEnv: true`で`PATH`／`LANG`／
  `LC_ALL`のみを設定し、親processの環境を引き継がない（`v0/agent/tools/bash_tool.ts`）。この設計のままでは、
  (1)
  `git push`が`HOME`未提供で認証情報（`credential.helper=store`＋`~/.git-credentials`）に到達できず
  hangした、(2) `git`は`fatal: $HOME not set`でglobal config（user
  identity・credential）を解決できず failする（実測）、(3) buildもよく失敗する（利用者観測）。
- 利用者判断（2026-09-26）: 機構としてambient情報を組み込む（env継承・自動付与・env manifest）のでは
  なく、**instructionとしてambient情報を持つ**方向とする。重要なのはAIがambient情報の**存在を
  知る**こと。環境情報を**機械的に集める**案は範囲が広く難しい（実行環境がdeno／node／bunで違い、
  VCSがgit／jujutsuで違う等）ため採らない。旧候補の「Hostが…配送する」も、Hostが環境情報を集めて
  dataとして渡す意味ではなく、**instructionの配送手段**の話である可能性がある（利用者指摘、
  2026-09-26）。
- 候補（利用者示唆、2026-09-26）: **人間の指示によりリポジトリの環境情報を集め、`ambient.md`等に
  まとめて、それをinstructionとして読み込む**。文書には(1) どんなambient情報が存在するか
  （repository root・VCS種別・非secretなcanonical identity、`HOME`配下のcredential store、git
  identity、 buildに必要なenv等）、(2) その所在、(3) 扱い方（repository contextはtask
  targetではなく、利用者が
  「このrepository」等と結び付けた場合だけsource。実行環境は必要時に明示する）、を記載する。
  credentialやremote URL内の認証情報は含めない。機械的収集・自動更新は行わない。
  `ambient.md`等の文書は**git管理外に置く**（`.gitignore`等）。commit・push・履歴に残さない
  （利用者指示、2026-09-26）。環境情報はworkspace／machine固有のため。
  AGENTS.md「実行環境」はこの方向の先行例。
- 再検討条件: ①外部product名だけのtaskでambient remoteをtargetにする誤認、またはrepository
  identityを
  得るための不要なtool探索が再発する、②AIが実行環境のambient情報の存在を知らない、またはinstruction
  だけでは足りない事例が観測される、のいずれか。採用時にR3（tool実行profile）・E3（Host runtime
  tunables）との分担を決める。
- 再観察（2026-09-22、`henji run --json`、`opencode-go-chat`/`deepseek-v4.1-flash`、workspace=henji
  repo、 task「Giteaの直近10件のPRを教えて」、1 turn）: tool sequenceは`web_fetch`（GitHub
  API）→`web_search`で、 ambient workspace探索（`bash git remote -v`、handoff読み）は0回、ambient
  Forgejo remoteをtargetにする 誤認もなし。credential探索もなし。**この条件では再現せず**。留意: 1
  model/provider・知名度の高いproductで の観測。Increment
  37のmodel/provider（openrouter経由）や知名度の低いproductでは未確認。
- 状態（2026-09-22）: 利用者判断で継続して要観察。trigger未発火のため実装しない。
- 統合記録: 旧A16「bash
  tool実行のambient情報をinstructionで持つ」（2026-09-26記録）はこの項目へ統合。
- 正本:
  [`increment-37.md`](../history/increments/increment-37.md)が観測した実行証拠と完了判断を保持する。
- 関連: A11（instructionの与え方）、R3、E3、`v0/agent/tools/bash_tool.ts`、AGENTS.md「実行環境」。

### A11 — instructionの与え方

- 観測（2026-09-23）: DeepSeekは短いREADME比較依頼ではツール使用を続けたが、対象ファイル、比較項目、
  追加調査の条件、報告する時点を明示した指示では、2回のprovider request（2件の`read`）で回答した。
- 利用者希望:
  instructionの内容・粒度・与える場所（共通instruction、モデル別instruction、個々のtask）を
  検討する。短い依頼から目的に合う作業範囲と終了条件を組み立てられるかを実利用で比較する。
- 再検討条件: 指示の与え方を変えると、同じ目的のtaskの完了挙動が変わること。

### A21 — 1ターン内でsteeringを複数回受け付ける

- 利用者希望（2026-09-27）: 実行中の同じturnへsteeringを複数回送れるようにしたい。
  今回は未採用候補としてメモし、実装は行わない。
- 現行境界（2026-10-03）: busy中の追加指示はF3で送り、受付は1 executionにつき1回。
  `SteeringOwner.admit()`の受付済み状態は指示を消費した後も解除されない。
  [Increment 175](../increments/increment-175.md)でWorkerの受付確認と、tool後に加えてmodelがfinalを
  返した際の取込み・同じexecutionの次requestへの継続を成立させた。複数回の受付は175の対象外であり、
  本候補として残る。これはHenjiの実装上の制約であり、provider APIの制約ではない。
- 利用者方針（2026-10-04、実装は未採用）: 前の指示が適用されたら、同じexecutionで次の指示を
  受け付ける。未適用の指示がある間は次を受け付けない。実装はまだ行わない。
- 候補: 上記の受付方針で、同じturnの実行中にsteeringを複数回受け付け、既存のtool実行後・ 次のmodel
  request前の経路（final後の継続を含む）でモデルへ渡す。
- 対象範囲: 実行中turnへの追加指示。別枠のchatや子Agentへの直接steeringは、この候補には含めない。
- 関連: [Increment 175](../increments/increment-175.md)、`v0/agent/core/steering.ts`、
  `v0/agent/core/loop.ts`、`v0/tui/remote_session.ts`。

### A24 — subagentの委譲と実行中の制御・待機（旧A35を統合、未採用）

- 統合方針（2026-10-07、利用者指示）: A24の起動判断・構成選択、A35の時間上限・自動キャンセルと、
  今回相談したキャンセル・追加指示・進捗把握・非同期の回答待ちを一つのテーマとして扱う。
  人間による子の個別キャンセル操作は当面不要とし、対象外にする。今回は検討メモの統合だけで、
  個別incrementへの採用・実装には着手しない。

#### 観測と利用者の意向

- 起動時の原観測（2026-09-28、workspace `/home/agent`、Session `6e5dbddd`の保存履歴照合）:
  README日英比較で`generic`を3件起動した（run `8739f50c`、`7796828a`、`6a6acd8b`）。
  起動引数は`agent`と`task`だけで、3件とも親の
  `openrouter-responses / xiaomi/mimo-v2.6-flash / auto`を引き継いだ。`tools`未指定により
  `read`、`write`、`edit`、`bash`、`bash_output`、`web_fetch`、`web_search`、`submit_json_result`が
  有効だった。「ファイルを変更しない」はtask内の指示で、toolを制限する指定ではなかった。
  当時、利用者は起動判断と構成選択を今後検討する方針をメモした。
- 時間制限の原意向（2026-10-06、旧A35）: reviewの時間上限を`spawn_subagent`のtaskへ書く方法を
  確認した後、「キャンセル作りたいな、メモしておいて」と指示した。10分などの上限を検討する。
- 今回の利用者観測（2026-10-07）: Henjiでsubagent起動後にすぐstateを参照し、ほんの少し後に
  回答待ちへ入って親がじっと待つ。modelの性質かもしれないが、キャンセル・時間制限・追加指示・
  何をしているかの把握・回答待ちを改善したい。実行履歴による頻度やmodelごとの差は未確認。

#### 現行動作（2026-10-07、source確認）

- 子は別Worker・別executionで動く。`spawn_subagent`は実行完了を待たずrunIdを返すが、
  `collect_subagent`は子の終了まで返らない。runtime/Core/TUIは非同期で動き続けても、親のagent
  loopはtool resultをawaitするため、その間に親modelは別作業や`cancel_subagent`を発行できない。
  statusのtool説明には独立作業を続けてからcollectする案内が既にある。
- `cancel_subagent`は既存。時間上限のspawn引数、collectのtimeout引数、子への追加指示操作はない。
- `subagent_status`はstate・agent名に加え、phase、model step、request順、最後のtool名・実行状態・
  outcome、更新日時を返す。tool引数／対象path、作業内容の要約、経過時間・残り時間は返さない。
- 時間経過がmodelへ自動で供給される仕組みはない。時刻をtoolで取得できれば確認できるが、現行の
  同梱reviewerはread/search/git_inspect/skillだけで、taskへ「10分」と書くだけでは停止を保証しない。
- 子のlifetimeは親executionに紐づき、親の終了時に未完了の子もcleanup・cancel対象になる。

#### 改善候補

- 委譲判断と構成: 何を親が扱い、何を子へ任せるか。Agent・provider・model・effort・toolの選択と、
  親設定の継承／明示指定の使い分け。起動後に親が進める独立作業と、結果が必要になる時点も整理する。
- 親による子のキャンセル: 既存cancelを使える場面と、親がcollect待機中に制御できない問題を扱う。
- 時間管理: 「10分を目安に途中結果をまとめる」指示と「10分でruntimeが停止する」上限を区別する。
  経過時間・期限のmodelへの供給、runtimeからの期限キャンセルを比較する。上限の起算点、停止理由、
  途中結果の返し方は未決で、親が待機中でもruntimeの期限制御は動く案を含める。
- 追加指示: 起動済みの子へ親から指示を送る操作と、その取込み時点・結果への反映を検討する。
- 進捗把握: 現行のphase・最後のtoolに加え、対象や短い作業状況を把握できる情報を検討する。
- 待機方法: collectが一定時間で「実行中」を返す等、親が結果待ちだけに拘束されない方法を検討する。
  親が待つ時間の上限と、子の実行時間上限・キャンセルは別の操作として扱う。
- 追加相談（2026-10-07、メモのみ）: statusで子の中間報告・findingを読む経路と、実行中の子への
  追加指示を優先候補とする。子が通常本文として出した報告は子の実行履歴に残るため、既存履歴を
  statusから取得する案を検討する。親が取得した報告はstatusのtool resultとして親の履歴に残す。
  中間報告の専用送信toolを必須とせず、報告しながら調査を続ける指示と、前回確認後の新しい報告の
  読み方を検討する。追加指示は、statusで状況を読んでから別toolで送る案が素直だが、契約は未決。

#### 外部ツール化の検討順序

- 外部ツール化の検討順序（同日、今回の助言を記録）: status拡張と追加指示を先に成立させ、
  実利用で操作が固まった後に外部化を検討する。現行subagent操作は通常toolと同じRegistryへ入るが、
  専用の組込み生成経路であり、外部tool APIには子を操作する接続口がない。外部化しても中間履歴取得・
  追加指示にはCore側の対応が必要になるため、先行外部化を今回の前提にしない。変更時はCoreの操作と
  toolの説明・引数・結果の整形を分け、既存RPCの分担を活かす。Henji自身が本体の再ビルドなしで
  statusの返し方や待機方法を改善したくなった時点を、外部化の再検討理由とする。採用・実装は未指示。

#### 未決事項と再検討条件

- 未決事項: 上記の具体的な操作契約と、指示・tool説明で改善する範囲／runtimeで支える範囲。
  親の最終回答後も子を継続するlifetime変更は未採用であり、A36の書記官用途と合わせて必要性を判断する。
- 再検討条件: 利用者がこのテーマを個別incrementへ採用するとき。
- 関連: [A36](#a36--非同期の書記官subagentによるメモhandoff更新未採用メモのみ)（書記官用途）、
  [E1](#e1--agent設定tool以外のresource外部化)（外部化全般）、[Increment 131](../increments/increment-131.md)（model・tool指定）、
  `v0/agent/tools/async_agents.ts`、`v0/agent/core/loop.ts`、
  `v0/agent/worker/worker_host_children.ts`、`v0/agent/worker/worker_host_coordinator.ts`、
  `v0/agent/worker_agent_api.ts`、`v0/agent/tool_api.ts`、`v0/agent/worker/worker_tool_loader.ts`。

### A27 — providerの一時的な応答中断に対する自動再試行（未採用）

- 利用者判断（2026-10-01）: 常に停止するわけではないため、当面は`/recall`で対処する。
  自動再試行は将来の候補としてメモに留める。採用・実装は未指示。
- 観測: Session `e71f582a`の4ターン目で、`opencode-go-chat / mimo-v2.6-pro`の回答が途中で
  終了し、HTTP 200のまま`stream_ended_before_done`／`provider response invalid`となった。
  保存入力をproduction adapterで再送する1 requestのprobeでは、約40秒で`finish_reason: stop`と
  `[DONE]`を受信し正常完了した。中断の発生箇所がprovider・gateway・通信経路のどれかは未確定。
  局所probeの証拠は`.tools/provider-stream-eof-probe/report.json`。
- 現行経路と比較: HenjiのChat経路はHTTP 5xxを最大2回再試行するが、今回のHTTP 200後の中断は
  そのままターン失敗となる。Piは途中終了等を分類して既定3回、OpenCodeは再試行可能なエラーを
  最大5回再試行する。参照は10月1日更新のsnapshotにある
  `_refs/pi/packages/ai/src/utils/retry.ts`と`_refs/opencode/packages/opencode/src/session/retry.ts`。
- 現行の対処: [Increment 169](../increments/increment-169.md)で対象のprovider failureに
  `try /recall`を案内し、[Increment 176](../increments/increment-176.md)で失敗の分類と短い診断を拡充した。
  いずれもHTTP 200後の中断への自動再試行を追加しておらず、本候補は未採用のままである。
- トークン量: 今回の再送は入力30,878、出力1,533（推論661を含む）、cached 0だった。
  同規模で3回再試行すれば、キャッシュなしの追加入力だけで約9.3万tokenとなる。
  recallの準備自体はproviderを呼ばないが、次のtaskには通常履歴と参照JSONを送るため、
  recallによる継続1回が再試行1回より少ないtokenで済むとは限らない。
- 候補: 確認された一時的な中断に対し、完了済みtool結果を保持して失敗したmodel requestだけを
  再試行する。途中回答を次のmodel入力へ混ぜない処理、再試行の対象・回数・待ち時間・表示は採用時に
  決める。raw応答の常設保存は候補に含めず、必要な調査時だけ別probeで取得する。
- 再検討条件: recallでの手動継続が通常利用の負担になる、または利用者が検討の再開を指示するとき。

### A29 — request単位のtoken usage・cache再利用量の保存とreadback（未採用、メモのみ）

- 利用者の追加依頼・現行確認（2026-10-09）:
  入力予算上限の引き上げと、キャッシュを含む利用枠消費の釣り合いを判断するため、cache命中量の
  取得・分析方法とDB変更の要否を相談し、メモを指示した。今回は採用・実装の指示ではない。
  現行の`provider_evidence.ts`はinput/output/totalと入力見積もり・差分を保存するが、cache項目は
  抽出していない。Session `17c48f73`の確認した151 requestの保存usageにもcache項目はない。
  未保存をcache未使用または命中0と扱わない。
- 保存・分析の実装候補（同日）:
  API応答から取得可能なcache命中・書込み量等を抽出し、usageの型・検証とJSON保存項目を追加する。
  現行DBの`semantic_records.payload_json`で保存でき、SQLiteテーブル／column変更・schema移行・
  既存履歴の書き換えは不要。追加後のrequestから数値を記録し、read-onlyで集計できる。
  既存DBに保存していない過去のcache量は、このDBからは復元できない。raw応答の常設保存は不要。
- 公式仕様の追加照合（同日）:
  DeepSeekは`usage.prompt_cache_hit_tokens`／`prompt_cache_miss_tokens`を返す。 OpenCode
  Go経由でも同じ項目が返るかは未観測のため、採用時に実応答で確認する。 OpenAI
  Responsesは`usage.input_tokens_details.cached_tokens`、Chat Completionsは
  `usage.prompt_tokens_details.cached_tokens`。OpenRouter Chat Completionsにも取得可能な場合に
  `usage.prompt_tokens_details.cached_tokens`と`cache_write_tokens`があり、現在はstreamの最後のSSEにも
  usageを自動で含める。OpenRouterではActivity詳細やgeneration IDによる後からの確認も可能。
  根拠は[DeepSeek cache仕様](https://api-docs.deepseek.com/guides/kv_cache/)、
  [OpenAI cache実測項目](https://developers.openai.com/api/docs/guides/prompt-caching/diagnostics)、
  [OpenRouter usage仕様](https://openrouter.ai/docs/cookbook/administration/usage-accounting)。
  入力総量だけでなくcache量を使って、切り詰め前後の命中率と利用枠消費を比較する。
- 2026-10-07の追加検討:
  usage保存と三参照のtoken節約策を分けて比較し、cacheは必要な共通contextの再利用に使うと整理した。
  A3の履歴整理・再取得、要約やwarmingの追加費用を含めて評価する材料とする。採用・実装は未承認。
  詳細は[機能比較とcontext整理の検討](../research/2026-10-07-reference-feature-comparison-and-context-strategy.md)を参照する。
- 利用者観測・依頼（2026-10-05）:
  Henjiの`openai-chatgpt / gpt-6.1-sol`でtoken消費が激しく感じられる。
  Codexのcache活用と、完了turnのtool結果をcontextから外すA3案との関係を検討した。
  usage記録の候補をメモするよう指示された。変更・実装の承認は含まない。
- 当時の確認（2026-10-05）:
  `openai_responses_model.ts`は`response.completed`のresponseを受けるが、usageを抽出・保存していない。
  `2bc2699f`の確認した直近実行のsemantic記録にもinput/cache/reasoning token項目がない。
  cache未使用と観測欠落は区別できず、token消費の主因・削減効果は未測定。
- 候補:
  provider・model・API・execution・物理request順に相関して、providerが返したinput/output/total、
  cached input、reasoning、cache write等の取得できるusage数値を短いfactとして保存・readbackする。
  raw responseやcredential/Authorizationを常設記録する必要はない。
  usageまたは詳細項目が返らない場合は未取得として区別し、0へ置き換えたり通常応答を拒否したりしない。
- OpenAI公式仕様との照合（2026-10-05）: Responsesのcached
  inputは`usage.input_tokens_details.cached_tokens`、 Chat
  Completionsは`usage.prompt_tokens_details.cached_tokens`。
  input/outputとreasoningの名前もそれぞれinput/outputとprompt/completionで異なる。 Chat
  Completionsのstreamingは`stream_options.include_usage: true`で最後のusage chunkを要求できる。
  stream中断時はそのchunkを受け取れないことがある。
  これらはOpenAIの仕様であり、同形式の互換providerすべてが同じ詳細項目を返すとは未確認。
  provider別の実対応と保存・公開contractは採用時に確認する。
- 関連:
  A3、[A28の移設先](../increments/increment-199-a28-observations.md)、`v0/agent/provider/openai_responses_model.ts`、
  [OpenAI Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)、
  [Chat Completions公式仕様](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)。

### A36 — 非同期の書記官subagentによるメモ・handoff更新（未採用、メモのみ）

- 利用者の観測・意向（2026-10-07）: handoff変更時の複数段階の手順が体感時間を長くしているため、
  SQLite化や専用tool化を相談した。Henjiでは専用subagentを「書記官」として置き、メモも含めて
  非同期に整理・更新させたい。既存subagent経路を使えば後で試せる案として、今回はメモだけを指示した。
- 候補: 主agentは利用者への回答や作業を続け、書記官subagentが確定した事実・判断を既存記録と照合し、
  通常利用メモ、個別increment、handoff等の担当保存先へ反映する。主agentからは利用者の発言、対象ID、
  判断に必要な短いcontext、根拠への参照を渡し、文章の整理は書記官が担う。
- 役割案: 方針・採用・実装の判断は主agentと利用者が担い、書記官は確定した判断の記録を担う。
  「候補を取り下げる」「複数候補を同時に扱うが未着手」「作業結果と現在地を更新する」等を対象にする。
  正本ごとの役割と変更承認の境界は維持する。
- 体感時間の狙い: 主agentの文書読取・patch作成・保存確認を非同期へ回し、利用者の待ち時間を減らす。
  保存処理自体の高速化とは別で、処理全体の時間やmodel呼出し量が減るとはまだ確認していない。
- 未決事項: 起動契機、渡すcontext、更新の順序、再開時に最新の更新が保存済みかを確認する方法。
  記録依頼と保存完了を区別し、保存前に「記録済み」と扱わない案。専用read/update toolの併用と、
  Markdown／SQLiteの保存方式は別に判断し、試行の前提としてSQLite化や新しいruntimeを要求しない。
- 現行lifetimeの補足（2026-10-07、source確認）: 親executionが終了すると未完了の子も停止する。
  既存経路で試せるのは親の実行中の並行更新であり、最終回答後も更新を続けるにはlifetime変更が必要。
  その採否は[A24](#a24--subagentの委譲と実行中の制御待機旧a35を統合未採用)の制御・待機テーマと合わせて検討する。
- 再検討条件: 利用者がHenjiの既存subagent経路で書記官を試すことを指示するとき。
  今回はagent設定・tool・runtime・作業規則を変更せず、試行も行わない。
- 関連: A24、R4、`v0/agent/tools/async_agents.ts`、`v0/agent/worker/worker_host_children.ts`。

### A39 — git書き込み系tool（`git_inspect`との分割）（未採用、メモのみ）

- 利用者の課題（2026-10-08）: reviewer subagentにgit
  diffを使わせたいが、`git commit`等の書き込み系は
  やらせたくない。diffのためにbashを与えるのも避けたい。read+writeを1つのtool（`git_tools`／`vcs_tools`
  等）にまとめると、tool grantがtool名単位なので衝突する（diffのためにcommit能力まで渡す）。
  衝突の回避策を検討した。
- 現行確認（2026-10-08）:
  agent設定の`tools`とsubagent起動の`tools`はtool名単位のgrant（`run_typescript`も
  listに含めないと使えない）。`git_inspect`はread-only（status/diff/log/show、repository・index・working
  tree に書かない）。reviewerへのdiff提供は`git_inspect`のselective grantでbashなしに既に成立。
  `run_typescript`はsubprocess無効なのでcommit経路にならない。mode単位のgrantは現状ない。
- 候補:
  - 案A（推奨、harness無変更）: `git_inspect`（read）＋write側tool新設の2分割。write側はmode
    `add`／`commit`／`push`（最小）。名称例:
    `git_write`／`vcs_write`／`git_ops`（`git_inspect`と対に なる命名が分かりやすい）。external tool
    folderとして`git_inspect`同family（managed process executor）。
  - 案B（将来の本格対応）: harnessにmode単位のgrant（`tools: ["git_tools:diff"]`的粒度）。agent JSON
    contract・Worker dispatch・tool catalogの変更。product契約・architecture正本の変更承認が要る。
    他toolでも部分grantの実需が出てからで遅くない。
- 認証の前提（既存のまま、新前提なし）: commitはHOME配下のglobal configでidentity解決（HOME未指定は
  `fatal: $HOME not set`、実測）。pushは`credential.helper=store`とHOME配下のcredential
  storeで解決。 credential値はtool
  call・resultに記録しない。認証なし環境ではpush失敗＝bash経由と同じ。
- 機能拡大の規律: scopeは「gitでできること」でなく「bashなしで委譲したいこと」。追加は「通常利用で
  bashなし委譲の実需が出たmodeだけ」に閉じ、非破壊・履歴作成操作（add/commit/push）の範囲を保つ。
  reset/rebase/force/checkout等の書き換え系は対象外（人間とbashがいる場でのみ行う）。拡大はproduct
  要件の変化として管理する。
- 利用者判断待ち: 案A／案B、write側のmode範囲、tool名。採用・実装は未承認。
- 未確認: bashなしsubagentへのcommit/push委譲の実需の頻度。案B採用時のgrant表記の具体形。
- 再検討条件: bashなしsubagentへcommit/pushを委譲する実需が出たとき。
- 関連: A36、[Increment 223](../increments/increment-223.md)、`git_inspect`（external tool
  folder）、AGENTS.mdの実行環境（HOME・credential store）。

### A40 — editツールのbackendにsedを使う案（未採用、メモのみ）

- 利用者案（2026-10-10）: editツールのbackendにsedを使うことを検討候補として記録する。
- 採用可否と具体的な機能範囲は未検討。search分割のIncrement 223とは別候補として扱う。

## F24・自己改訂

### R1 — 自己改訂対象の重心とagent loop境界

- 利用者の仮説: Agent
  Definition、とくにrole定義はmodel能力への依存が大きく、有用なvariationも多くない
  可能性がある。Definition variantの増加自体を自己改訂の中心にしない。tool定義と実装、作業方針、
  instruction、policy、workflowの方が改善余地を観測しやすい。
- 現行境界（2026-10-08）: Agent
  JSONはinstruction/tools/agentsを選び、共通runtimeがmodel/loop/contextと
  compositionを構築する。maxStepsはruntime既定と起動optionで決まり、JSONの任意項目からは選ばない。
  task中の判断規則はinstruction/workflow、tool実行順やdispatch・turn確定条件はruntime/loopの改訂対象である。
  run_typescriptによる必要な処理の生成・実行・結果利用は、自己拡張として一部実装済みである。
- 候補:
  既存の自己拡張と改訂運用で、経験の参照・振り返り・生成した処理の継続利用に不足する動作を選ぶ。
  Agent JSONに限定せず、tool実装、作業方針、instruction/policy/workflow、core loop、Hostから
  実際の経験に必要な対象を選ぶ。現在file、semantic履歴、使用snapshotを使い、全対象共通のrevision
  storeや 専用候補管理・採用flowを必須残件にしない。

- 不変条件: candidateの採用は人間の明示操作・承認に限定する。candidate自身にこの境界を外させない。
- 再検討条件:
  自己拡張・改訂の通常利用で、経験の参照・振り返り・生成した処理の継続利用に具体的な不足が出たとき。
- 調査:
  [`agent-loop-and-durable-state-comparison.md`](../research/agent-loop-and-durable-state-comparison.md)。

### R2 — tool改訂の版・使用内容の記録とMCP component

- 現行境界（2026-10-04）: Agent JSONのtoolsが名前を宣言し、tools.jsonが現在folderを選ぶ。
  Workerが@henji/toolのfactoryを一度呼び、同じToolのcontract/executorを提示・dispatchする。
  revisionは版名であり、実提示contract・選択元・rejectは独立configuration snapshotへ保存する。
  source closureの保存・exact revision pin・transportは現行方式ではない。 2026-10-08の位置付け:
  run_typescriptによる自己拡張は部分実装済みであり、生成code/inputと結果はsemantic
  履歴から振り返れる。生成処理を永続toolとして継続利用する必要があるかは、通常利用で判断する。
- 候補: 生成した処理やtool変更の継続利用で、既存の内容確認・人間の採用・新Workerへの反映経路に不足が
  出た場合、その動作を経験へ結び付ける。 履歴snapshotに加えてtool
  sourceの版やdependencyを固定する必要があるかは、具体的な改訂で振り返る材料から判断する。
  旧案のDefinition lineageへの固定を現行方式の必須追加機能にしない。 一般MCP
  componentは別の将来候補で、Exa MCPの採用は却下済みである。
- 確認する動作:
  schemaや版名の導入だけでF24の完了とせず、必要な候補内容と由来を人間が確認・採用でき、
  後続通常利用へ反映して使用内容を振り返れること。
- 再検討条件: 生成処理・tool改訂の継続利用に具体的な不足が出たとき、または具体的なMCP
  integrationを採用するとき。
- 関連: E1/E2、[181 contract](../increments/increment-181-contract.md)、
  [Host/Worker architecture](../architecture/henji-host-agent-worker.md)。

### R3 — tool実行profileとsandboxed Deno program

- 観測: 現行`bash`はmodelのcommandを`/bin/bash`
  subprocessへ渡す。Deno公式permissionのとおり、subprocessは 親runtimeのfilesystem/network
  permission内に自動で閉じず、OS userの権限で動く。2026-09-07の開発VMには
  `python3`、`apt`、`sudo`とpasswordless
  sudoがあった。あるturnでpackage導入を選ばなかったことは強制境界の 証拠にならない。
- 候補: startupの`trusted-local · no hard sandbox`をtrust、tool capability、permission、human gate、
  isolationの独立軸で扱う。read-only、approval-gated、workspace-sandbox、isolated-runner等を構造化profileで
  表し、Agent設定/tool componentはHost-owned permission ceilingの範囲内だけを選ぶ。
- hard sandboxの条件:
  `deno run --allow-run`やcommand名の禁止ではなく、subprocess自体をbubblewrap、Landlock、
  container、専用VM等へ置き、workspace
  mount、他path/credentialの可視性、network、process範囲をHostが強制する。
- 外書き込みの拒否/承認（Codexの`workspace-write`＋outside承認、OpenCodeのpermission model相当）は
  command解析ではなく**sandbox policy**として実装する。command解析型の承認や、bashの破壊pattern
  （`rm -rf /`等）のdeny-netは**sandboxまでの安全帯**であり境界ではない（変数展開・script・interpreter・
  redirect・`curl|sh`等で回避可能）。frictionの大きいapproval
  gateを常用の前提にしない。安価な大事故低減が 必要になった場合の選択肢としてのみ残す。
- 現行Deno program tool:
  [Increment 191](../increments/increment-191.md)のrun_typescriptは実装・常用配置済み。
  modelがTypeScript本文とdataを渡し、Host所有の呼出し専用Henji子processで実行する。 config
  rootのread/writeは[204](../increments/increment-204.md)、共通allow/deny設定は
  [213](../increments/increment-213.md)を参照する。通常のcode実行を未実装候補に残さない。
- 分類: run_typescriptはF06の標準toolであり、code生成・実行による自己拡張はF24の部分実装でもある。
  executor/contractの改訂flowや強いsandboxの採用は、具体的な必要が出た場合に別途判断する。
- 再検討条件:
  trusted-local以外の実行環境、または既存run_typescriptで足りない実行profile・強いsandboxが
  product要件になること。
- 参照: [Deno permissions](https://docs.deno.com/runtime/reference/permissions/#subprocesses)。

### R4 — instruction componentのrevision化と自己改訂

- 現行境界（2026-10-04）: 共通baseはuser instruction.mdまたはbinary内の最小coreを選び、 Agent
  JSONのrole寄与と構成不足の案内、実tool guideline、workspace AGENTS、skill manifest、runtime
  factsを合成する。 起動snapshotは当時の最終instruction/componentsを保持し、request
  contextとexecutionから参照する。 user
  file/JSONの編集は新Workerから反映し、現在fileの変更で過去snapshotを上書きしない。
- 候補:
  instruction/policy/workflowの候補内容・由来を現在採用状態と区別し、人間の採用・rollback対象にする。
  現在fileとsnapshotで足りるか、独立revision保存や複数component選択が必要かは対象動作から決める。
  合成順・競合・使用内容のattributionを維持し、旧managed
  loader/promotion/bindingを必須方式にしない。
- 再検討条件: instructionまたはworkflowを自己改訂対象として選ぶとき。
- 正本: [構想](../concepts/experience-driven-self-revision.md)、
  [Host/Worker architecture](../architecture/henji-host-agent-worker.md)、[181 contract](../increments/increment-181-contract.md)。
- 関連: E1。

## 配布・外部化

### E1 — Agent設定・tool以外のresource外部化

- 現行境界（2026-10-04）: standalone executable、JSON Agent設定、folder-based tool、native
  AGENTS/Skill、 user instruction.md、data-only
  provider/credential宣言を利用できる。model一覧・お気に入り・effort、
  APIキーとChatGPT認証も既存ownerが持つ。旧managed Agent/tool store、closure install、exact
  selectorとtransportは廃止済み。
- 個別の検討:
  subagent操作toolの外部化は[A24](#a24--subagentの委譲と実行中の制御待機旧a35を統合未採用)へ記録する。
  status拡張・追加指示の実利用を先に確認し、外部化は操作が固まった後の候補として扱う。
- 残る対象: instruction複数component、任意context/loop strategy、MCP、Surfaceのload/置換、
  resource固有のmutable stateや移送等は、具体的な動作を採用するときに境界を定める。
  provider宣言の追加や現在tool folderの読込み自体を未実装候補として重複計上しない。
- 候補: 更新・共有・rollback・分離実行が必要な対象ごとにselection owner、scope、placement、
  lifetime、使用内容の記録を定める。file方式で足りるか、pin/transportが必要かもその利用目的から判断する。
  Agent/instruction/tool/providerを同じloaderへ載せること自体を目標にしない。
- Provider候補: 既存protocolで表せないadapterや認証・一覧取得を外部実装にする必要が出た場合に、
  binary-owned adapter、credential resolver、短いrequest fact、deadlineとの責務を比較する。 raw
  SSEの常設記録は前提にせず、追加確認は別probeを使う。
- 適用候補: candidate生成と人間の採用、現在設定の選択、新Workerへの反映、rollbackを区別する。
  廃止済みmodule install/managed activationを現行の利用入口として扱わない。
- 状態の分離: canonical会話、request
  context、tool/外部moduleの作業stateは、それぞれのownerとlifetimeを保つ。
- 再検討条件:
  instruction/tool/provider/MCP/Surface等の更新・共有・rollback・分離実行が通常利用で必要になること。
- 正本:
  [roadmap](../roadmap.md)、[Host/Worker architecture](../architecture/henji-host-agent-worker.md)、
  [provider/auth architecture](../architecture/multi-provider-routing-and-auth.md)。
- 調査: [外部化の比較記録](../research/externalization-reference-comparison.md)。

### E2 — 追加managed resource kind候補（未採用）

- 原観測（2026-09-18）: 当時のmanaged kind間の重複から一般化を検討した。native Skillの互換形式には
  独立した価値があり、最初のmanaged化対象から外した。
- 現行境界（2026-10-04）: instructionはuser file、AgentはJSON、toolは現在folderを使い、 旧managed
  Agent/tool revision基盤は181で廃止した。追加kindの必要性は、現在fileとsnapshot方式で足りない
  利用目的から改めて判断する。既存managed kindへの単純な追加・共通化を前提にしない。

- 候補（Henjiが単独でownerになれるcontractに限る）。優先順は未定で、通常利用で必要になった時点で個別incrementへ
  採用する。
  - provider declaration revision: data-only宣言をexact
    revision化（pin/transport/activation）。Provider外部化の
    続き。ファイルベースで足りる可能性あり。
  - model profile revision: model/effort/catalog preset。selection presetという別contract。
  - context/compaction strategy revision: generationごとのcontext投影/compaction
    policy（A3）。設計大きめ。
  - instruction component／policy／workflow revision: base
    1つではない複数componentの順序付き合成（R4）。
  - integration declaration（MCP connection）revision: connection/transport/capability（E1）。
  - Surface data/code revision: Host側Surface差し替え（F10/F24）。規模大。
  - Agent loop／runtime policy revision: loop semantics置換。core変更で高リスク。
- framework自体の扱い:
  共通kind基盤は、具体的なkindと必要な動作が決まってから、そのために必要なseamだけ切り出す。仮想的な汎用plugin
  discovery/loaderは現時点で採用しない（architectureの「必要になるまで共通化しない」方針）。
- 再検討条件:
  上記候補のいずれかを通常利用で更新・pin・transport・activationする具体的必要が出ること。
- 正本: [`roadmap.md`](../roadmap.md)
  F24、[`henji-host-agent-worker.md`](../architecture/henji-host-agent-worker.md)。

### E3 — Host runtime tunablesの設定ファイル化（未採用）

- 現行境界（2026-10-04、source照合）: provider deadlineはDEFAULT_PROVIDER_TIMEOUT_MS=300000、
  maxStepsはworker_agent_api.tsのDEFAULT_AGENT_MAX_STEPS=128が既定である。
  TUI/runは--provider-timeout-ms/--max-stepsでHost起動optionを渡し、共通Workerへ適用する。 Agent
  JSONはmaxStepsを宣言しない。runtime.jsonはない。
- 既存configはdefault-selection.json、providers/_.json、model-catalogs、agents.json、tools.json、
  credentials/_.json、user instruction.md等で、それぞれselection/contentのownerを持つ。
- tool定数はweb_fetchのtimeout=30000/本文readback上限1 MiB、bash_outputのwindow既定・上限49152
  bytes等にある。
  web_fetchのdownloadと本文readbackの上限を混同せず、調整したい具体的な動作から対象を選ぶ。
- 候補: CLI引数を毎回指定する負担が残る場合にruntime configで既定を選ぶ。 CLI > config >
  runtime既定の優先関係は案であり、対象設定とscopeを採用時に決める。
  providerTimeoutMsを最初の候補として比較し、他tool限界やmaxStepsを一括外部化しない。
- authority境界: Host起動optionと共通runtime既定の選択経路を使い、廃止済みAgent
  Definitionとの二重authorityを 仮定しない。JSON Agent設定、Session
  selection、executionの実効maxStepsとは役割を分ける。
- 再検討条件: timeout/tool限界/maxSteps既定の継続設定を通常利用で必要とするとき。
- 正本:
  [roadmap](../roadmap.md)、[Host/Worker architecture](../architecture/henji-host-agent-worker.md)。

### E5 — 追加protocol adapter候補（未採用）

- 観測（2026-09-21、official docs調査のみ）: 現行protocolは`openai-chat-completions`と
  `openai-responses`の2つで、adapterはbinary所有である。追加候補を調査した。
  - Anthropic Messages: `POST https://api.anthropic.com/v1/messages`、`GET /v1/models`あり。authは
    `x-api-key`または`Authorization: Bearer`、`anthropic-version: 2023-06-01`必須。wireが別で、
    thinking block＋`signature`のecho必須、`max_tokens`必須。新adapterが必要。
  - Google Gemini (AI Studio): nativeは`/v1beta/models/{model}:generateContent`、
    `:streamGenerateContent?alt=sse`、authは`x-goog-api-key`。OpenAI互換endpoint
    `https://generativelanguage.googleapis.com/v1beta/openai/`があり、Chat Completionsとtoolsを
    既存adapterで再利用できる。nativeは`thoughtSignature`のechoが必要で新adapter。
  - Azure OpenAI: v1 API
    `https://{resource}.openai.azure.com/openai/v1/`はOpenAI形式で`api-key`header またはEntra
    Bearer。classicはdeployment path＋`api-version` queryが必要でURL/query templatingが要る。
  - AWS Bedrock native: SigV4署名はrequestごとの計算が必要で静的header mapでは表現できない。
- 利用者判断（2026-09-21）: 参考調査のみで当面対応しない。Increment 101でauth profileと宣言headerを
  一般化しておく。
- 候補: 必要になったproviderから、binary-owned protocol
  adapterを追加する。GoogleはOpenAI互換endpointの 宣言だけで足りる可能性がある。AzureはURL/query
  templatingの要否を採用時に判断する。
- 再検討条件: 該当providerを通常利用で使う必要が出るとき。
- 正本候補: `docs/architecture/multi-provider-routing-and-auth.md`、`docs/roadmap.md` F02／F24。
- 関連: E1、Increment 101。

## 参照実装parity（Pi／Zot調査、未採用）

- 観測（2026-09-22、snapshot調査）: Pi（`_refs/pi` commit `08dc60bc…`、0.85.1）とZot（`_refs/zot`
  commit `f60e492e…`）のslash commandとCLI
  optionを抽出し、Henji現行surface（`v0/tui/slash_command.ts`、
  `v0/agent/cli/henji_cli.ts`、`v0/agent/cli/tui_cli.ts`）と比較した。詳細と全表は
  [`research/pi-zot-command-surface-comparison.md`](../research/pi-zot-command-surface-comparison.md)。
- 有力候補: 手動`/compact`、Session export/import、model cycling、configurable
  keybindings、画像入力。 P1（`run`の構造化出力）はIncrement 104へ採用済みでこの一覧から除く。
  旧P5（`@file`内容注入）はS28「`@`によるコンテキスト注入」へ統合した。検討事項と再検討条件はS28を参照する。
  旧P7（`sessions prune`）はA9「Sessionと関連履歴の保存・削除」へ統合した。検討事項と再検討条件はA9を参照する。
- 利用者判断（2026-09-22）:
  - P2 `/jump`: 3アクション程度必要でPageUpの方が手軽なため、候補から除外（調査記録には残す）。
  - `!command`はHost実行経路を増やす割にtool loopと重複するため候補から除外（調査記録には残す）。
  - Session tree/fork: 有用性が未確認。採用判断は保留。
  - `/btw` side-chat: Henjiにそぐわないため対象外。
  - `/swarm`: 時期尚早。既存async childのone-shot fork/joinとは別の候補として扱う。
  - extension/package管理: 将来課題（E2／F24）。
  - messaging bridge: 将来Surfaceの拡張で検討する可能性がある（未採用）。
  - sandbox/permission系: 対象外（R3領域）。
- 対象外: self-update、llama.cpp router、project trust。
- 再検討条件:
  各候補の再検討契機は候補一覧の列に従う。通常利用で具体的な不便が観測されたとき個別incrementへ
  採用する。

### P9 — 画像入力（`@image`／clipboard paste、未採用）

- 観測（2026-09-22、参照実装調査）: Piは`ctrl+v`でimage paste、Zotは`ctrl+v` paste（image含む）と
  `inline_images_enabled`を持つ。Henjiのtranscriptはtext-only（`UserMessage.content`は`TextContent`）で、
  画像をtaskへ入れる経路がない。
- 候補: 人間が`@image`／pasteで画像をtaskへ入れる経路。実現にはtranscriptのcontent
  part、provider別encoding （OpenAI `image_url`、Anthropic image block、Gemini `inlineData`）、tool
  result、TUI表示、model catalogの capability（vision）が必要。
- 依存: E5（Anthropic Messages／Google adapter）と、Increment 103で先送りしたcatalog capabilities。
- 再検討条件: 画像を扱うtaskを通常利用で行うとき。
- 関連: E5、A3、[Increment 135（credential登録）](../increments/increment-135.md)。

### P10 — 外部agent interface（双方向server/RPC／ACP、未採用）

- S22で採用した第三者HTTP操作契約は [Increment 139と後続slice](../increments/increment-139.md)、
  [CLI・外部API利用設計](../plans/s22-cli-and-external-api.md)を参照する。
  ACP・editor統合は引き続き本候補の未採用範囲である。
- 観測（2026-09-22、公式docs調査）: Codex app-serverはJSON-RPC 2.0双方向（stdio/WebSocket/unix、
  thread/turn/item）でrich clientを駆動し、OpenCodeは`opencode serve`のHTTP OpenAPI 3.1
  server＋`/event` SSE＋
  `@opencode-ai/sdk`で外部からprogrammaticに制御できる。ACPはeditor/IDE↔agentの標準（JSON-RPC 2.0、
  `session/new|prompt|update|cancel`、`fs/*`・`terminal/*`・permission要求）でZed/JetBrains等が対応。
  詳細は[`research/external-agent-interface-comparison.md`](../research/external-agent-interface-comparison.md)。
- 段階: (1) 一方向structured output（Increment 104で実装済み）、(2) 双方向server/RPC、(3)
  ACP（editor統合）。HenjiはHost/Worker間に
  既にdata-only双方向protocol（F12）を持つため、外部interfaceはHost-owned
  Surface追加（F10）として整理できる。
- 注意:
  ACPはagentがclientのfs/terminal/permissionを使う前提で、Henjiの自前tool・trusted-local方針との写像が
  非自明。R3（sandbox/permission）とF10の判断に接続する。
- 再検討条件: editor/IDE統合、または別agentからの対話的駆動を通常利用で必要とするとき。
- 関連: Increment 104、F10、F12、R3。

## 観測した不具合（未解決の残件）

- 個別incrementへ採用するまでは修正しない。再現条件、実行証拠、利用者影響をここへ残す。

### B5 — `commit proposal invalid`の具体的な検証不合格理由を特定できない

- 原観測（2026-09-19）: Session `54d65ea7`のexecution `e0e9cf4f`は約5分41秒のweb調査後、
  `contract_failure`／`commit proposal invalid`で停止し、成果がcanonical採用されなかった。
  同日の隔離XDG・実provider確認では別原因のprovider timeoutとなり、commit却下は再現しなかった。
  詳細なDB観測、自動復元の由来、再現試行は
  [`increment-85.md`](../history/increments/increment-85.md#b5の原観測と切り分け2026-09-19)へ移した。
- 現行境界（2026-10-07、source照合）: [Increment 170](../increments/increment-170.md)で
  record組立て・保存はAgent
  Data側へ移り、[Increment 199](../increments/increment-199.md)で採用portを 新message
  suffixへ絞った。`session_authority.ts`の`proposalSuffix`はnextTurn、既存会話以上の
  transcript長、新suffixの因果構造と1 turnであることを検証し、不合格なら`undefined`を返す。
  `session_data_owner.ts`の`prepareProposal`はこれを`commit proposal invalid`としてthrowする。
  不合格項目・値の形を返す経路はなく、元の却下原因も未特定である。
- 対応済みの境界: 自動入力復元と停止理由の上書きはIncrement 85／97で解消した。
  却下proposalのtranscriptを保存・readbackする経路は181のhistory.sqlite3（schema
  1）へ接続されており、 原観測時の「却下transcriptをDBから読めない」は現行storeの制約ではない。
  [Increment 176](../increments/increment-176.md)でWorker／Data／commitの例外に取得可能な処理段階・
  例外種類・短いmessage等を残すが、項目別の検証不合格理由は提供していない。
- 残る利用者影響:
  commit却下が起きた場合、保存されたproposalと取得可能な例外情報だけでは具体的な検証不合格箇所を
  直接特定できず、成果がcanonical採用されなかった理由の調査が難しい。
- 対応候補（未採用）:
  commit却下時に検証不合格の項目と値の形を短いfactとして保存・readbackできるようにし、
  実際の却下原因を調べる。再現probeが必要なら隔離XDGで行い、実provider callは別途明示承認を得る。
- 再検討条件:
  commit却下が通常利用で再観測される、または却下理由の記録・原因調査を個別incrementへ採用するとき。
- 関連:
  [`increment-85.md`](../history/increments/increment-85.md)、[`increment-94.md`](../history/increments/increment-94.md)、
  [`increment-97.md`](../history/increments/increment-97.md)、[Increment 170](../increments/increment-170.md)、
  [Increment 176](../increments/increment-176.md)、`v0/agent/data/session_authority.ts`、
  `v0/agent/data/session_data_owner.ts`、`v0/agent/worker/worker_host_coordinator.ts`、
  `v0/agent/history/sqlite_history_store.ts`。
