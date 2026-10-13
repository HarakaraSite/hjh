# Terminal UI

`hjh-tui`は`hjh serve`と独立したprocessで、[HTTP API](http-api.md)の
`/api/v2` JSON＋SSEを使う。会話と保存の契約は[I-06](i06-conversation-contract.md)、
実装・検証の状況は[I-06結果](../results/i06-conversation-tui.md)、
[I-07 Session管理](../results/i07-session-management.md)、[I-08 接続選択](../results/i08-connection-selection.md)を参照する。
I-09のF2予約・F3追加入力、受付状態、draft保持と次runへのattachは
[追加入力契約](i09-runtime-input-contract.md)を参照する。実行中のEnterはdraftを保持し、
受付可能な期間はF2で次run予約、F3で親runへ追加入力を送る。F1は同じ予約列を停止する。
I-10の`/context`・`/recall [ID|latest|clear]`は[入力context・引継ぎ契約](i10-context-recall-contract.md)を参照する。
`/context`は保存済み親requestの準備metadataと問い合わせ時点の次回選択を別に表示する。
引継ぎのadmission・参考message保存・request準備を区別し、会話には短いsource markerを表示する。
recallは送信時のCore・Sessionと操作IDを保持し、通信結果不明の再確認でも同じIDを使う。

I-11のmodel pickerはTabでお気に入りを登録／解除する。
Core保存確認後に印と先頭への整列を反映し、遅延したcatalog／favorite読取でも選択中のIDを保つ。
保存待ち中も検索・移動・Enter・Escを使え、次run選択やdraftを保存操作で変更しない。
favorite未取得時のTabは再取得だけ、保存結果不明時は先行完了後のGETと同じ希望値の確認を行う。
詳細は[保存・API・picker契約](i11-model-favorites-contract.md)を参照する。

Coreは実行・DB・provider・credential・JS Workerを所有する。
TUIはdraft、次回の接続／role／Session選択、会話entity、Markdown・折返し、
端末scrollbackへの確定出力と下部live bandを所有する。DBやCore実装packageをimportしない。
Bubble Teaのupdate loopで入力と表示状態を管理し、独立writerで通常画面へ出力する。
pickerと認証の選択画面は一時的なalternate screenへ描く。通常画面の会話とcursorを保持し、
キャンセル・選択完了で元の画面へ戻す。選択画面の内容を会話のscrollbackへ残さない。
picker表示中に確定した親本文は通常画面へ戻ってから一度だけ出力する。
Slash command候補は通常画面の入力欄の上へ一時表示し、alternate screenへ切り替えない。
候補の行は入力欄と同じdockに含め、会話履歴・scrollbackへ保存しない。
端末の検索・コピーは端末／tmuxのscrollbackを使う。

I-13の起動入口は[計画と操作契約](../plans/i13-core-startup.md)を参照する。
endpoint省略時は同VMのCoreへ接続し、未起動なら別processの`hjh serve`を起動してHTTP readinessを待つ。
明示endpointは接続だけを行い、`--start-core`で明示的に同VMのCore起動も選べる。
起動optionとprocess寿命はCLI／corelaunchが扱い、TUIのmodelへCore実装やDB所有を持ち込まない。

Session会話はSession view snapshotで読み、現在runのsnapshot／upsertを同じID／位置へmergeする。
親の本文・公開thinking・toolをCoreの意味的位置に沿って表示する。
子executionは開始・終了・失敗を短縮ID付きの短い行へまとめる。実行中は子ごとに
状態と現在実行中のtool名だけを下部live bandの1行で更新し、進行表示をscrollbackへ追記しない。
子のtask・thinking・tool引数/結果・中間報告・最終回答・追加入力は通常画面から省く。
親が報告や回収結果を材料に書いた本文は通常どおり表示する。
この選択はTUIの表示だけへ適用し、Coreの保存履歴・API view・親のモデル入力を変更しない。
保存済みSessionの再開も同じ簡潔な表示を使い、詳細は既存の/tools・/tool・/inspectから読む。
公開thinkingはproviderが返したtext／summaryだけを使い、private replay stateを本文へ展開しない。
本文は`thinking`、要約は`thinking summary`と表示し、生成中と履歴再開で同じentityを使う。
モデルが宣言したtoolと外側JS実行は同じcallを表示する。実際のJS内toolは親子関係を維持する。
最終応答は進行中と同じentityを確定し、terminal resultをもう一度追記しない。
通常tool行は名前・短い引数preview・状態だけを表示する。結果全文は会話へ展開しない。
内部で呼ばれたtoolは、Coreが渡すParentEntityIDのtool階層に従い、二文字分の字下げと`↳`を付ける。
例えばrun_jsの下にread_file/commandが並び、並列に呼んだtoolは同じ深さへ複数表示する。
直接呼んだtoolは字下げしない。表示順は従来の開始順を維持し、既に出力した親も階層計算へ使う。
`↳`は各callの包含を表し、後続の兄弟が増えても過去の行を書き換えない。
通常の進捗とSession復元は同じ階層表示を使い、子agentの詳細省略は従来どおり維持する。
`/tools`で会話内のcall番号を確認し、`/tool NUMBER_OR_ENTITY_ID`でその保存済みcall/resultを読む。
保存証拠全体は`/inspect`で読める。
今回の詳細操作は会話末尾への明示出力であり、生成中に取得した詳細はその本文の終了後に表示される。
旧Henjiのように実行中でも別browse表示で読む操作は、後続の詳細browserで扱う。

2026-10-12の利用者指示により、assistant本文へGlamourのMarkdown整形を適用する。
生成中・最終回答・履歴復元を同じ描画経路へ通す。本文の原文とsource receiptを保持する。
周囲のUIは旧Henjiを基準とし、thinking・tool・metadataの指定配色は
[Markdown整形・配色契約](tui-rendering.md)に従う。

writerは不変になったMarkdown blockのsource境界をreceiptとして受け取り、表示済み本文を再追記しない。
Markdownが生成したURL・表・装飾文字から元本文の文字位置を推定しない。
確定したblockは端末内に残さず、すぐscrollbackへ出力する。
生成中の末尾blockは、後続blockが現れるか本文が完了するまで全文sourceを保持してlive windowで更新する。
tool行は旧Henji同様、最初に出力した状態をscrollbackへ一度だけ残す。
結果到着で同じtool行を再追記しない。Sessionを開き直すと保存された完了状態を表示する。
結果全文を追記しないため、tool結果待ちは後続の子・親の本文を確定出力する妨げにならない。
live previewは最初の未確定本文blockの末尾を追う。
幅変更・終了時は安全に確定できる未出力本文を取りこぼさずにsealし、未確定部分とeditor/footerを描く。
workspace／Session headerは通常scrollbackへ流す。更新するmodel／connection／実行状態は[3行footer](tui-footer.md)に置き、
遅れて変わるexecution outcomeを過去の本文位置へ挿入しない。footer／draftは会話として確定しない。
変更のないbody・metadata・command entryの加工結果を再利用し、全会話のsource連結を毎更新で保持しない。
HTTPの受信・終了保存は描画ACKを待たない。

日本語・複数行editorの文字数・入力行数に上限を設けない。Enterで送信、Alt／Shift／Ctrl+EnterまたはCtrl+Jで改行。
入力欄は`>` promptだけを表示し、プレースホルダーは置かない。
修飾Enterは端末がkey情報を渡す環境で使える。bracketed paste、矢印／Home／End編集、
Tabのコマンド補完、Escの選択表示／補完closeを扱う。利用者のtmux設定を勝手に変更しない。
editorは実端末cursorを文字幅・改行・表示windowと同じ計算結果で動かし、IMEの変換窓の基準位置にする。
会話の下に区切り線・操作案内と通知・入力欄を置き、その下に状態・接続情報を置く。
F1は現在runの予約列をキャンセルしてCoreのterminalと列の保存完了を待ち、TUIを継続する。Ctrl+Cはdraftを消す。
I-07では`/detach`／Ctrl+DがTUIの購読・通信を閉じ、Coreとrunは継続する。
新Sessionの準備中は保存済みadmissionまで待ち、Session IDとrun IDをscrollbackへ残す。
`/quit`／Ctrl+QはCore停止を要求し、新run受付を止め、全runと子Agentの取消・join・保存を待って終了する。
serveへのSIGINT／SIGTERMも終了保存を待つ。購読切断だけではrunをキャンセルしない。

F4／`/sessions`は名前・ID・workspace・model・turn数・実行中の印を表示し、現在Sessionを初期選択する。
矢印／j/k、PgUp/PgDn、Home/Endで選び、Enterでresume、Escでcloseする。
選択行の位置は本文と同じ文字幅・折返しで計算する。現在Sessionの再選択では実行を止めない。
`/rename TEXT`は既存title metadataだけを更新し、実行中の会話採用revisionは変えない。
`--session ID`／`/resume ID`はactive run IDを先に捕捉し、保存済み会話を読んで同runのSSEへattachする。
AttachではStartRunをPOSTせず、会話entity IDで保存済み本文とstreamをmergeする。

利用者は`/view`を不要とし、tool詳細は現状維持を選んだ。これらをI-07へ追加しない。
I-08で接続pickerを、I-09でfollow-up/steeringとreceipt確認を追加した。

F2の次runはCoreが作り、TUIは元runの最終snapshotとWaitを完了してからnextRunIdへattachする。
F2受付時の実効role/接続/run条件は固定され、後のpicker変更で置き換えない。
F3は生成やtoolを中断せず、親の次request境界へuser messageとして取り込む。
受付、適用、予約、開始、未実行理由をfooterと `/inputs` で確認する。本文は会話へ一度だけ表示する。
受付応答の後にdraftを消すが、その間に編集したdraftや別Sessionのdraftは保持する。
receiptの変更revisionをSession/Core単位で追い、terminal後に遅い読取応答が届いても最新状態へ追い付く。

Commands are:

| Command | Behavior |
| --- | --- |
| `/`入力 | Show command names, argument hints and descriptions. Type to filter, arrows to select, Tab to complete, Enter to choose, Esc to close. Commands requiring arguments leave the completed name in the editor. |
| F4 / `/sessions` | Select a Session with arrows/j/k, PgUp/PgDn, Home/End; Enter resumes and Esc closes. |
| `/new` | Start a new conversation in the current workspace. A Session is created by its first run. |
| `/resume SESSION_ID` | Load saved history and attach to the same active run, if present. |
| `/rename TEXT` | Save the current Session title, including during a run. |
| Ctrl+D / `/detach` | Exit TUI while Core and its run continue. |
| `/history [FROM_TURN TO_TURN]` | Show adopted messages, optionally for an inclusive turn range. |
| `/executions` | List this Session's completed, failed, and cancelled executions. |
| `/tools` | List conversation tool calls with numbers and entity IDs. |
| `/tool NUMBER_OR_ENTITY_ID` | Read the selected saved call/result through its detail locator. |
| `/inspect ID [FROM_ORDINAL TO_ORDINAL]` | Show execution messages, records, relations, captured configuration, and context. |
| `/roles` | Show the current workspace/config role catalog and tool lists. |
| `/role [NAME]` | Show the next-run role selection, or select a role for the next run. |
| `/providers` | List provider definitions from the current external config. |
| `/provider [ID]` | Open the provider picker, or directly select the next provider and clear previous model/effort/auth overrides. |
| `/models [ID]` | List the selected route's model candidates. |
| `/effort` | Choose auto or a declared effort from the model's picker; auto uses the endpoint default. |
| `/auth [ID]` | Show ChatGPT login status without token values. |
| `/login [ID]` | Open authentication profiles, or the selected provider's key/account screen. |
| Account Enter / a / r | Select a connected account, add an account, or reauthorize the selected slot. |
| Authorization Enter | Open the dedicated masked callback editor; local callback completion is observed from Core. |
| Secret-input Ctrl+U / Esc | Clear the dedicated input, or cancel/return without changing the conversation draft. |
| `/model [MODEL_ID]` | Open the searchable model picker, or select an ID directly. A successful run records the selection through the shared Session adoption operation. |
| `/help` | Show command help. |
| Ctrl+Q / `/quit` | Stop Core, wait for cancellation and persistence, then exit TUI. |

Session-changing commands received during a run are applied after that run
settles, so observations and results remain associated with the Session that
started them. Submitting ordinary text while a run is active leaves the draft
in the editor and reports that another run cannot start until the current one
ends.

Workspace paths refer to Core's filesystem. An omitted startup workspace uses
Core's default, rather than the TUI process's current directory.
The selected workspace is passed to each run as a start-time value. Resuming a
Session selects its saved workspace; `/new` then starts the next Session in
that selected workspace. Unless a model was explicitly supplied at startup or
selected with `/model`, the UI leaves model resolution to the shared backend's
configuration and Session rules. It shows the effective model reported by the
execution admission observation without replacing a model selected for a later
run.

## I-02: child executions

Delegation runs within the Core-owned foreground operation. Child observations display their
execution ID and parent ID as progress; child text does not replace the parent's
transcript, effective model, or current model step.
`/executions` shows parent and spawn IDs. `/inspect` shows the selected execution's
parent/spawn metadata and direct children, including when ordinal bounds restrict
the selected records. Inspect a child ID to read that child's evidence separately.

F1 stops the parent and its children and leaves the UI open. `/quit` now stops Core after
child cancellation, worker closure, terminal persistence, and joining. `/detach` leaves Core and
the database open for reattachment.
The earlier same-process PTY verification is
recorded in the [I-02 results](../results/i02-child-agents.md).

## I-03: role selection

Startup --role and /role NAME set the explicit next-run selection. Otherwise the
backend resolves the saved Session role, then config.defaultRole. The execution
admission reports the actual role separately from the explicit selection for a
later run; /role changes during a run do not mutate that Worker. /roles reads
through the scoped HTTP catalog operation. /new retains the selected workspace;
/resume restores saved workspace and role unless an explicit role is selected.
Child progress shows its short ID, state and current tool name without changing the parent's role/model/step.
Execution listings and inspection display saved role names.

## I-04: connection selection

Provider, model, effort, and credential reference are captured when submitting
an execution. Selection commands during a run affect the next run. The footer
shows the running root's actual provider/model/effort. A different next
selection appears beside it; identical values are not repeated. Child progress cannot replace it. Session resume uses the saved
selection unless explicitly overridden; provider changes use the new route's
defaults. JSON and OAuth ownership are described in [providers](providers.md).
Core owns pending OAuth callbacks and their captured workspace/provider/profile.
Showing a login URL does not change the next-run provider selection.

## I-08: connection pickers

2026-10-14、[slash command候補](slash-command-menu.md)を追加した。
名前・引数hint・説明はTUI catalogを認識・候補・helpで共有する。
Tabは補完のみ、Enterは引数なしのcommand実行または引数入力へ進む。
入力に引数・改行がある場合はコマンド名候補を表示しない。

`/provider`、`/model`、`/effort`は引数なしでpickerを開く。providerは外部定義の
ID・表示名・API・認証方式・既定modelを表示し、modelはIDと表示名を検索できる。
effortは選択modelの候補と出所を表示し、情報がなければautoのみを選べる。
effortの直接指定は受け付けず、保存値が候補外でも選択肢に追加しない。
候補にないmodelは従来の直接コマンドで選べる。

矢印、PgUp/PgDn、Home/Endで選択し、Enterで確定、Escで閉じる。
model検索は文字入力・bracketed paste・Backspaceを使い、j/kも検索文字として扱う。
現在値を初期選択し、選択行は実画面高と本文共通の文字幅・折返し計算で画面内へ保つ。
検索queryは会話draftから分離し、pickerを閉じてもdraftを変更しない。
Session pickerとは一つのoverlayを共有し、loading中もEsc、F1、Ctrl+D、Ctrl+Qを扱う。

Coreの`GET /selection`は次run入力を同じrun解決規則で解決する。TUIで優先順位を複製しない。
provider一覧は現在selectionの解決が失敗しても選べるため、保存providerがconfigから消えた場合にも
定義済みproviderへ選び直せる。model／effortの候補取得には有効なselectionを使う。
catalog取得にはそのproviderとauthProfileを使い、既存認証を再利用する。
Session・workspace・role・provider・model・effort・authProfile、初回Session IDの確定、
run完了による保存selection更新で古いrequestを取消し、開いているpickerだけを再取得する。
応答はpicker job・次run世代・入力が一致したときだけ適用する。閉じた画面を再openしない。
catalog読取と描画はCoreのrun・会話保存・終了joinへ待ちを戻さない。

## I-12: authentication screens

The [I-12 contract](i12-authentication-contract.md) defines profile/method/account
pickers, masked API-key and callback editors, authorization attempts, save recovery,
and account-aware catalog generations. Registration and account selection change
authentication for future preparations while preserving the current run binding,
conversation draft, role/model/effort selection, and model favorites. Core owns
credential storage, refresh, attempt cancellation, and shutdown joins.
