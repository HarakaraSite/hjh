# ツール利用ガイドラインの外部化と各toolのhelp：調査・設計・実装計画

2026-10-15。開始時の独立ガイドライン＋各toolのhelpという基本構成を利用者と合意。
その後、利用者の実装・test・review指示により実施した。[実装・確認結果](../results/tool-usage-guidance.md)を参照。
以下は採用した計画と当時の調査事項を残す。常用設定への配置・実provider確認は行っていない。
当初依頼は[tool-usage-guidance-task.md](../notes/tool-usage-guidance-task.md)。その後の対話で、
辞書toolを前提にせずユースケースから見直し、以下の構成へ変更した。
配置名・help資料の登録field・schema表現等の現行契約は[設計文書](../design/tool-usage-guidance.md)を参照。

## 1. ユースケース・採用範囲

### U1：開始・再開時に何をどう選ぶか分かる

モデルの最初の入力に、現在使えるtool一覧、短い用途・利用シーン、利用ガイドライン、引数schema、
正しく呼ぶための最小契約を含める。親の各ターン・再開・子開始も同じ準備経路を使う。
詳細資料を読むことをtool選択の前提にしない。

判断順は以下とする。

1. 合成・独立した並列実行・集約・文字列/JSON加工のためにrun_jsが必要か判断する。
2. 不要な単独操作は目的に合う専用toolを使う。
3. それらで目的を達成できない外部操作はcommandを使う。

run_jsを常に呼ぶ義務ではない。git commit/push、build/test等にcommandは必要だが、
専用read/edit/searchや単純加工の代替として先に選ばない。JSでcommandを包むだけではこの指針を満たさない。

### U2：使おうとしたtoolの使い方が不明なら調べる

選択したtoolの呼び方が自明なら通常呼出しをする。引数・手順・組合せが不明なら、
そのtoolを`{help:true}`で呼び、通常操作を実行せず説明・実例を取得する。

`read_file({help:true})`、`run_js({help:true})`、`spawn_agent({help:true})`が目指す操作。
調べても情報が足りないことはあり得る。helpが空でも調査が解決したとは見なさず、
既知の契約で判断できることと、なお不明なことを区別する。

### U3：第三者が自作toolと説明を一緒に配布できる

インストール先でtool作者が実装・短いpromptGuidelines・help本文を登録する。
中央の辞書やhjh本体の改定、repository作業規約への依存を必要としない。
help分岐は各JS作者へ実装させず、hjhの共通呼出し層が提供する。

### 初期運用での簡素化

- 共通の利用ガイドラインは独立Markdownとして手動編集し、開始・再開時に読み込む。
- tool追加/変更に伴う共通ガイドラインの改訂判断は、残る運用課題として明記する。
  自動生成・変更追従・同期検査・必須の関連付けmatrixは初期版へ入れない。
- 各toolの既存promptGuidelinesは維持する。共通原則を毎toolへコピーせず、個別の短い選択指針と最小契約を持つ。
- 中央tool-help-data.js、独立tool_help、目的検索、検索metadata/DB、runtimeSnapshot/toolUsage APIは今回作らない。
- 前案のConfiguration IDを毎回照合する機構や新しい出典UIは初期必須範囲から外す。

依頼文に記録されたSessionの観測を背景とし、原DB履歴は再調査していない。
Python等の選択理由・実モデルの選択改善は未確認。今回の作業は文書のみで、実provider呼出し、
実行中run操作、常用設定変更、DB変更、Core再起動、commit/push/配置は行わない。
動的schema公開、非保存mode、usage削減、compaction改修、timeout/retry変更も対象外。

## 2. 現行の利用経路と所有者

### 2.1 入口からモデル入力

```text
CLI直接run / Core接続run / TUI
  → app.Service.Run → app.Run
      再開時はSessionのworkspace・roleを解決
      prepareConfig → extensions.Capture
      prepareWorkspaceContext → model解決 → execution-context追記
      使用構成保存・execution開始
  → execute → jsruntime.New(capture済みSnapshot)
      runtimeStart → beforeTurn
      agent.Run
        各step：projectRequest → context budget投影 → provider.Prepare → Generate
        tool呼出し・結果保存 → 次step
```

- 入口：`cmd/hjh/main.go`、`internal/corerun/manager.go`、`internal/app/service.go`。
- 親準備：`internal/app/run.go:60-179`。
- 子準備：`internal/app/delegation.go:43-109`。blocking/spawnの双方が`prepareChild`を使い、
  **親が解決したconfig絶対pathを子開始時に再capture**する。親会話と親role指示はコピーしない。
- 実入力：`internal/app/execution.go:301-385`、`internal/agent/loop.go`。
  hookはinstructionも置換可能なので、captureだけでなくhook適用後のwireまで確認する。
- 通常の複数tool callは`internal/agent/loop.go:186-239`のfor loopで**順次実行**。
  同じ応答に並べるだけでは並列にならない。生成JS内の独立Promiseは別である。

### 2.2 指示の組立順序

現在の文字列組立順は次のとおり。これは全指示種別の新しい優先順位を宣言するものではない。

| 内容 | 所有者・現行の責務 |
| --- | --- |
| base | `extensions/base_instruction.go`。一般的実行姿勢と途中報告。user共通base.mdが組込み全体を置換 |
| active tool guidelines | `extensions.go`。選択済みtool順に`promptGuidelines`を結合 |
| role description/instruction | 外部config。調査/実装/レビュー等の目的と範囲 |
| config systemInstruction | 既存の追加指示。instructionComponents自体は命令入力に解釈しない |
| role catalog | run_agentまたはspawn_agent選択時だけ。名前・説明・configured toolsを提示 |
| AGENTS → skills一覧 | `workspacecontext`が探索、appが追記。AGENTSは近いscope優先、skills本文は必要時read_file |
| execution-context | model解決後にappが実provider/API/model/effort/role/workspaceを追記 |
| runtimeStart → beforeTurn | lifecycle ownerがその実行にだけ追記 |

根拠：`internal/extensions/extensions.go:559-600`、`internal/app/workspace_context.go`、
`internal/workspacecontext/context.go`、`internal/app/runtime_instruction.go`、`internal/app/lifecycle.go`。
base差替えの既存契約とskills探索順はそれぞれ
[base-instructions](../design/base-instructions.md)、[workspace-instructions-skills](../design/workspace-instructions-skills.md)を維持する。

### 2.3 構成・tool選択・capture

- 既定configは`<workspace>/.hjh/extensions/config.json`。`--config`指定時はその絶対path。
  不足する標準ファイルのみ`InstallDefaults`で配置し、編集済みconfig/moduleは置換しない。
- `CaptureOptions.Tools == nil`はrole.tools、非nilは全置換、空配列はtoolなし。
  明示toolsは親toolsとの積集合でもrole標準との積集合でもない。
- `Snapshot.Tools`が選択済み宣言の正本。モデル定義、guidelines、外部module登録、生成JSのtoolsをここから作る。
- `run_js`はbuiltinだが同じTool型を使う。`api.agents.*`も対応toolが選択済みのときだけ生成JSへ公開する。
- config所在directoryを実行用メモリへcaptureし、ESMのrelative importも捕捉済みbytesで解決する。
  disk編集は既存Workerに反映せず、次の親ターン・子開始・再開のcaptureから反映する。

根拠：`internal/app/roles.go`、`internal/extensions/extensions.go:274-380,528-556,650-725`、
`internal/jsruntime/runtime.go:173-263`、`internal/jsruntime/bootstrap.js:74-87,155-158`。

### 2.4 実際の保存・履歴経路

**古い一般説明ではなく現行app経路を前提にする。** `Snapshot.JSON()`はtool定義も生成するが、
`internal/app/journal.go:474-487`の`executionSnapshot()`が保存前に`tools`欄を除く。
実保存は選択名を持つagent.tools・組立済み指示・components等であり、詳細定義を常時archiveしない。
正確なwireと完全budgetは既存SnapshotOwnerがexecutionごとの最新1件を保持し、終了時に保存する。
`context.go`の保存タイミングに関するコメントと実処理には差があり、ここでは実処理を採る。

- 外部baseとAGENTS原文は既存`resource_revision/contents`へ保存。
- extensionのmodule/依存/元configはメモリcaptureのみ。DBへのsource一括archiveはない。
- 通常toolの結果はmessageとして保存し、次stepのtranscriptへ入る。
  tool本文の`textRef`化は保存上の共有で、モデル入力の圧縮ではない。読戻し時に本文を復元する。
- 正常rootのmessageがcanonicalへ採用され、次ターンは最新wireではなくcanonical messagesから投影する。
- 子履歴は独立。子が取得した説明が親に暗黙共有されることはない。

根拠：`internal/app/execution.go:211-257`、`internal/app/context.go`、`internal/app/snapshots.go`、
`internal/history/tool_message_codec.go`、`internal/history/read.go`。
[normal-history-storage](../design/normal-history-storage.md)の現行契約も参照。


## 3. 独立した利用ガイドライン

### 3.1 所有・配置・読込み

短い共通利用方針はbase・role・AGENTS・skillsと別に管理する。以下から一つを選び、全体置換する案。

1. `<workspace>/.hjh/instructions/tool-usage.md`
2. `os.UserConfigDir()/hjh/instructions/tool-usage.md`
3. 配布する独立Markdownの短い既定

通常のuser配置は`~/.config/hjh/instructions/tool-usage.md`、XDG指定時は
`$XDG_CONFIG_HOME/hjh/instructions/tool-usage.md`。未配置は次候補、空fileは方針なし。
他の読取りエラーは既存baseと同様に準備へ返す。自動生成・上書きはしない。
workspaceは実workspaceを明示して解決し、workspace外の--config所在directoryと混同しない。

extensionsが本文・locator・digestを所有し、appの親子準備から実workspaceを渡す。
baseの後、active tool guidelinesの前へ組み込み、`instruction:tool-usage-policy` componentを記録する。
外部方針原文はbase/AGENTSと同じ既存resource_revision保存を使う。help原文全体のarchiveはしない。

親ターン・再開・子開始時に再読し、実行中は固定する。F3/steerや各model stepで再読しない。
既存preview経路と読込み選択を共有するが、新しいheaderや/context表示は初期版の必須機能にしない。
base差替えでもこの独立方針は維持する。記載tool名は能力を追加せず、実際の選択toolだけを使う。

### 3.2 常駐方針の内容

- U1の判断順（run_jsの必要性→専用tool→command）。外部library/programが必要なら適した方法を使う。
- 大きな結果は判断に必要な範囲を取り、JSで絞る際も原文・識別子・根拠を維持する。
- 親も独立作業をするならspawn→親の作業→必要時collect。独立作業がなければ同期/即時collectもよい。
- status/steerは必要時のみ。pollingや追加指示を機械的に義務化しない。
- 通常の複数tool callは順次実行で、同じ応答に並べるだけでは並列にならない。
- 常駐の利用シーンから選び、使い方が自明でなければ対象toolを`{help:true}`で調べる。

個別の長い使用例やAPI一覧は方針へ入れない。利用者が自作toolを増やしても、本計画書を更新する運用にはしない。
共通原則が変わったときの手動改訂は必要であり、この課題を解決済みとはしない。

## 4. 共通helpオプション

### 4.1 呼出し契約と共通処理

- 選択済みtoolの`{help:true}`呼出しは説明を返し、通常のfile/process/HTTP/子起動等を実行しない。
- `help:true`では通常の必須引数path/code/task等を要求しない。ダミー値も要求しない。
- help未指定（またはfalse）の通常呼出しでは元のrequired・引数・戻り値を維持する。
- help結果は`{name, available, text}`。未登録なら`available:false`と詳細なしの説明を返す。
  nameは呼んだtool名。利用可能なtoolがないという意味と詳細説明なしを混同しない。
- 説明取得は登録済みの通常tool callとして観測・保存し、実行開始時に全文を指示へ埋め込まない。
- helpが未登録でもtoolは使える。help取得を必須の成功gateにせず、情報不足を架空の説明で埋めない。
- `run_js`内の`tools.read_file({help:true})`等も直接呼出しと同じ処理を使う。
  モデルへ渡す必要がある結果は外側returnへ含める。

共通help分岐は選択tool解決後、通常のbeforeTool/実装/afterTool分岐の前に置く案。
helpに対して既存の引数加工hookや通常操作を走らせない。外側/内側の通常のtool観測は維持する。
Worker.CallとbootstrapのinvokeToolが別々のhelp ownerにならないよう、両入口が通る共通箇所で処理する。
各外部JS関数にhelp実装を複製せず、専用help toolや別の実行基盤を追加しない。

### 4.2 最初に解消するschema上の未確認事項

現行schemaはread_fileのpath、run_jsのcode等がrequired。
`{help:true}`だけの呼出しと、従来のrequiredを持つ通常呼出しを正しくモデルへ提示する必要がある。
**通常引数を単に全部optionalにして解決したことにはしない。**

S2の冒頭で、現行の各provider adapterがschemaをどうencodeし、どの表現を受け付けるかを確認する。
object内のhelp branchと通常branchの条件を表すschema案を、公式契約・現行sourceとローカルencodeで比較する。
rootのunionを受け付ける、または全providerが同じJSON Schema制約を扱えるとはまだ断定しない。
helpという引数名を共通予約することで自作toolに与える影響も、登録契約として明記する。

現在の対象toolはobject引数だが、独自toolの引数schema一般を無条件に書き換えて成立すると見なさない。
必要なprovider/自作tool契約が成立しない場合は、機能を狭めたり通常requiredを壊す推測をせず、
schema調査結果と最小の代案を報告してから先へ進む。実providerによる受理確認は今回実施しない。

### 4.3 tool作者の資料登録

検索を外したため、keywords/entry ID/index用JSONは不要。最小案はtool宣言の任意Markdown参照とする。

```json
{
  "name": "my_tool",
  "description": "Short purpose.",
  "promptGuidelines": ["Choose this tool for ...; prefer another tool when ... ."],
  "inputSchema": {"type":"object"},
  "module": "my-tool/index.js",
  "export": "myTool",
  "helpFile": "my-tool/help.md"
}
```

pathはextension root相対。実装と説明を同じ配布単位に置き、既存Resourcesから捕捉済みbytesを取得する。
通常のread_fileによる現在diskの再読とは分ける。外部module作者はhelp分岐コードや中央登録を書かない。
複数toolから同じMarkdownを参照してもよい。横断例はrun_jsやspawn_agent等の関連toolのhelp本文に置く。
別catalog・資料検索・機械的なtool間availability matrixは作らず、例に必要なtoolを本文で明記する。
現在の選択tool一覧を能力の正本とし、例で言及されたtoolを自動追加しない。

builtin run_jsは外部toolと二重登録できない。builtinのhelp本文を独立Markdown資産として配布し、
configの`runJSHelpFile`参照から同じhelp取得処理へ渡す案とする。
参照名・builtinへの渡し方は実装前に既存config/resolveToolsへ照合する。新たなJS実装APIは公開しない。

### 4.4 captureと再開

親/子はそれぞれ自分のcaptureのtool定義・実装・helpを使う。実行途中のdisk編集はhelpにも反映しない。
同一executionで既に取得し入力に残っているhelpは再利用できる。新しい親ターン/再開で必要な詳細が不明なら、
過去のhelpを現在の契約と決めつけず対象toolへ再問い合わせする。
短い規則を共通方針に含め、毎回Configuration IDをモデルへ追加・照合する仕組みは初期版に入れない。
これは説明の更新忘れまで自動解決するものではない。tool作者の説明改訂は引き続き必要。

## 5. 常駐とhelpの分担

### 5.1 常駐に残す内容

| 対象 | 常駐 | helpへ置く内容 |
| --- | --- | --- |
| run_js | 合成/並列/集約/加工を選ぶ場面、async body、tools.NAME、await/return、英文目的コメント、timerなし | Promise.all実例、api.agents別名一覧、複数stepのrecipe |
| 子操作 | 同期/起動のみ/待機の違い、独立会話、role/model/tools省略、outcome/error、親終了で残子取消、cursor/receiptの意味 | spawn→親作業→必要時status/steer→collectの実例 |
| read/edit/探索等 | required、offset/limit、edits配列、0件/非0exit、text/nextOffset/backend等 | 応用例・組合せ |
| command | 外部実行が必要な場合に選ぶ、program/args/shell明示、exitCode等 | 必要な外部コマンドの例 |
| web_search | source選択/引用、抜粋と合成の区別、既定/重要戻り値 | 長い調査workflow |

run_jsの短い常駐文案：

> Prefer run_js for composition, independent parallel calls, aggregation, and string/JSON processing; otherwise use the appropriate dedicated tool.
> The code is an async function body. Await selected tools.NAME(args) and return needed results. Begin with a short English // purpose comment. Promise is available; browser timers are not.

helpへの一般導線は共通方針へ一回記載し、全toolへ同じ長文を複製しない。
全toolのschema/短い説明にhelp呼出しを表し、code/path等なしで取得できることを最初から認識できるようにする。

### 5.2 詳細helpへ置く実例

以下は各toolのhelpへ置く内容の例。今回は実行していない。実装スライスで現行Workerを通して確認する。

単独の範囲読取りは`read_file({path:"docs/design/extensions.md",offset:1,limit:40})`を直接使う。
独立した読取りの集約は次のようにする。

```js
// Read independent documents and retain exact source references.
const paths = ["docs/design/extensions.md", "docs/design/base-instructions.md"];
const results = await Promise.all(paths.map(path => tools.read_file({path, offset:1, limit:40})));
return results.map(r => ({path:r.path, offset:r.offset, text:r.text, nextOffset:r.nextOffset}));
```

加工・編集は先に読取りをawaitし、その結果で次の呼出しを作る。依存するread→editをPromise.allにしない。
`edit_file({path,edits:[{oldText,newText}]})`を使い、`edits[].replacements`で変更結果を確認する例を置く。
大きなgrep結果はtool側のpath/glob/context/limitを先に絞り、JSのreturnには根拠行と識別子を残す。
JSON加工だけならJSでparse/map/stringifyできるが、Node/browser APIがあると推測しない。
今回の計測でもQuickJSに`TextEncoder`がないことを観測したため、初期例では使用しない。

子を開始し親も作業する（`api.agents.*`別名は対応tool選択時だけ）：

```js
// Start an explorer and read independent design context.
const child = await api.agents.spawn({role:"explorer",task:"internal/extensionsを読み、tool選択の経路を報告。変更しない。"});
const context = await tools.read_file({path:"docs/design/base-instructions.md"});
return {executionId:child.executionId, state:child.state, context};
```

別のstepで、必要になった場合だけstatusを読む。

```js
// Read the child's saved progress without waiting for completion.
const status = await api.agents.status({executionId:"RETURNED_EXECUTION_ID"});
return {reports:status.reports, nextCursor:status.nextCursor, instructions:status.instructions};
```

追加指示が有用なら`api.agents.steer({executionId,text})`。受付receiptと適用/成果を混同しない。
後続statusには同じ子の実nextCursorをafterとして渡す。最終結果が必要になった時点で
`return await api.agents.collect({executionId})`し、outcome/errorを確認する。
直接toolを使う版も併記し、run_jsがなければ子操作ができないと誤解させない。

親には独立作業がなく子同士だけ並行させたい場合は、`Promise.all`と`api.agents.run`を使える。
これはspawn後に親も作業する例と区別する。polling、不要なsteer、人工的sleepは例に要求しない。


## 6. コンテキスト・保存量

### 6.1 調査時点の実測baseline

2026-10-15、配布`internal/extensions/assets/config.json`と`extensions.go`の文言を読取り、
UTF-8 bytesで集計した。実provider/常用DBを使っていない。tool schemaはJSON compact再serializeで測った。

- 配布外部toolのguidelines本文合計：**8,069 bytes**。
- description合計：**2,097 bytes**。inputSchema合計：**9,868 bytes**。
- 子6操作のguidelines本文合計：**2,972 bytes**。
- `.hjh/extensions/config.json`のguidelines/schemaは同値だったが、description合計は2,298 bytes。
  現物の編集差があるため、配布とworkspaceの計測対象を混同しない。

| role | 現行Active tool guidelines（見出し/各行prefix込み） |
| --- | ---: |
| default | 9,983 bytes |
| reviewer / explorer | 2,163 bytes |
| implementer | 5,922 bytes |


以前のtool_help/検索案の短縮試算は現在案の見積りに使わない。
各schemaへhelp分岐が増えるため、詳細説明を外す量だけでなくschema増分も測る。全roleで減るとは限らない。

### 6.2 比較方法

同じtask・role/tools・adapter・hook条件で、初回wireをpolicy/guidelines/description/schema/catalog別に比較する。
help未取得とhelp1回取得について、追加往復数、結果bytes、次step/次turnのwireへの残留量、
必要時再取得の量を記録する。検索の二段往復はなく、通常helpは1往復である。

help結果は通常の完了tool exchangeとして予算選択・canonical履歴に従う。
後続stepで古くなれば除外され得る。opt-in compactionでは正確なcodeが維持される保証もない。
pin/cache/自動再注入やcompactionの改修は足さない。

一時検証DBで未取得help/moduleの常時archiveがないこと、取得結果だけ通常messageへ残ること、
最新wireが既存ownerから保存されることを照合する。新しいDB/schemaやusage保存削減は扱わない。
推定tokenは現行estimatorによる比較で、実providerのtokenizer値ではない。実モデルの選択改善も別途未確認。

## 7. 実装スライス・確認・review

各sliceは実装後に具体的なproduct動作をfocused確認し、reviewする。実装開始は別途指示を待つ。
途中は必要なfocused test/format/type/lint/diff checkだけを使い、full gateを繰り返さない。

### S1：開始時の独立ガイドライン

U1：独立Markdownの選択・親子準備・instruction component・既存source保存を接続する。
一時HOME/XDG/workspaceで未配置/user/workspace置換/空fileを確認し、hook後の実wireへ
短い利用方針と選択tool一覧/個別guidelinesが最初から入ることを照合する。
base置換で方針が消えないこと、実行中固定・次親ターン/再開/子開始で再読することを確認する。
reviewはworkspaceと--configの混同、既存指示/選択の維持、共通原則と個別契約の重複を確認する。

### S2：schema調査と共通help経路

先に§4.2のprovider schema契約を調査・確定し、成立しなければ報告する。
成立する案で通常requiredとhelpのみの呼出しを表現し、共通help分岐・Markdown参照を接続する。
直接call/生成JS内callでhelpが取得でき、通常実装や引数加工hookの副作用を起こさないことを確認する。
通常呼出しは既存hook/実装経路のまま、helpなし/false時の通常契約を維持する。

一時自作toolを登録し、中央辞書や本体を改変せず説明を取得できること（U3）、help未登録は詳細なしであること、
help取得が通常利用を妨げないことを確認する。親子のrole/tools省略/明示上書き/[]を維持し、
選択toolだけが通常/helpとも呼出し可能であることを照合する。
同Workerで本文固定、次captureで変更反映、旧helpの残る履歴から再開後の再取得を確認する。
reviewはschemaの通常required維持、共通owner、capture/現在diskの混在、不要なAPI/保存の追加を確認する。

### S3：標準help・常駐説明の整理

run_js・子操作中心に標準helpを用意し、その取得経路が動いてから長い例/重複説明を常駐から外す。
個別の利用シーン・最小契約は維持。schemaから分かることをhelp必須へ移さない。
標準help中のcodeそのものを取得して、一時workspaceの実Workerで独立read並列・加工/集約・read→editを確認する。
子の例は一時app/制御可能なローカルproviderでspawn後に親作業が進み、必要時status/steer、collectできることを確認する。
JSモックだけで子lifecycleを確認済みにしない。commit/pushは確認用に行わない。
reviewは初回入力の選択指針と最小契約、実例とAPIの一致、常駐削減とhelp schema増分を確認する。

### S4：総合確認

安定候補へcoordinating ownerが一回、実binary・一時Core/DB・ローカルHTTP providerで
開始wire→対象tool help→実操作/子→最終回答→履歴→編集後再開を通す。
role別の常駐量とhelp取得の追加往復/履歴量、未取得本文の非archiveを結果文書へ記録する。
失敗時は原因に対するfocused確認を行い、総合再実行には変更の具体的理由を添える。

ローカルprovider確認は実モデルの選択改善の証明ではない。実モデルで比較するなら別途了承後、
単独read、複数資料加工、子調査中の親作業、親は待つ独立子2件など少数課題で、
成果・選択tool/順序・help回数・wire/usage/時間を比較する。offline成功だけで改善したと結論しない。

## 8. 変更範囲・残る課題

対象はextensionsの指示組立/短いguidelines/help資料参照/schema生成、appの親子準備/指示出典保存、
jsruntimeの共通呼出し分岐、標準config/help資料と利用者向け登録手順。
agent loopの順次実行、role/tools選択、既存観測/履歴、contextbudget、provider timeout/retryは変えない。
provider wireでhelp schemaを扱う表現は要調査だが、動的tool公開や別の実行基盤は追加しない。

既存config/moduleはInstallDefaultsで上書きしない。help資料参照や短縮文の反映手順を示し、
互換reader/migration/自動rewriteや既存データ削除は行わない。常用.hjhへの反映も配置指示後とする。
前案の独立tool_help、検索、中央data/JSON index、runtimeSnapshot/toolUsage API、
Configuration ID照合、新規出典UIは実装対象から除去した。既に実装済みの機能を削除する話ではない。

残る課題：

- **共通ガイドラインの改訂運用**：初期は独立fileの手動編集。tool変更への自動追従は未解決・対象外。
- **help schemaのprovider適合**：helpのみ/通常requiredの共存は調査前。実装可能と断定しない。
- help予約fieldと自作tool登録契約、builtinの外部help参照の最小表現はS2でsource/公式契約に照合する。
- 標準help実例の実行、正式schema込みのwire差分、実モデル選択改善は未確認。

本計画の作成後に実装開始の指示を受けた。実provider呼出し・常用設定配置はその指示に含めず未実施。
