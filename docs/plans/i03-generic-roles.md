# I-03：親子共通genericと外部role定義

> **後続変更の案内（2026-10-16）**：I-03は実装済み。子のmodel/tools指定は[I-19結果](../results/i19-child-agent-control.md)・[現行拡張契約](../design/extensions.md)に従う。実装sourceの自動archiveと保存snapshotの詳細tools欄は、[通常履歴の保存契約](../design/normal-history-storage.md)で廃止・整理済み。以下の計画はI-03時点の方式を含む。

2026-10-10。状態：**実装・test・実provider／PTY／Linux配布確認・実装reviewと指摘修正完了**。
I-02は[実装・実provider・review・利用者の子Agent呼出し確認](../results/i02-child-agents.md)まで完了した。
この文書は利用者が指定したI-03の方式を具体化する実装計画。
実装・確認の現在地は[結果](../results/i03-generic-roles.md)に記録する。

## 必要な動作と根拠

利用者は、実行エンジンをbuilt-inのgenericに一本化し、どのrole・tool構成で呼ぶかを外部定義にする方針を指定した。
「権限」は利用可能toolの指定までとし、toolができる操作はtool内部で規定する。
デフォルトの親Agentもgenericで実行し、同じroleを親／子のどちらにも使えるようにする。
コンテナ・OS仮想マシンへの配置は[将来構想](../design/future-agent-environments.md)として別途検討する。

完成させる利用動作は、外部JSONにdefault／reviewer等のroleを定義し、親のroleをCLI／TUIで選び、
生成JSからroleを指定して子へ委譲し、選んだinstruction・toolで実作業を完了すること。
定義編集は次の実行から反映し、選択したrole・実tool・使用bytesを履歴で振り返り、Sessionを再開できるようにする。

旧Henjiの[reviewer定義](../../../henji-harness/agents/reviewer.json)と
[構成処理](../../../henji-harness/v0/agent/worker/worker_configuration.ts)では、
Agentのtoolsからロード対象を選んでいた。これは今回の議論の確認根拠であり、旧filterの例外、
path policy、制限、Core・queue等をhjhへ採用する根拠にはしない。
Linux/amd64、OpenRouter、Go loop、独立QuickJS VM、[DDL v3](../design/history-schema.md)、I-02の終了契約を使う。

## 現行の利用経路と変更が必要な点

現行経路は `CLI／TUI → app.Service → app.Run → app.execute → agent.Run／JS Worker → history`。
子は `api.agents.run → jsruntime.AgentRunner → app.delegation → app.execute` を使う。

| 現行の所有者・処理 | 現状 | I-03での扱い |
| --- | --- | --- |
| `extensions.Capture` | 単一config.agentと全config.toolsをSnapshotへ入れる。agent.toolsは実登録の選択に使われない | 同じconfig bytesからroleを選び、選択したtool構成を組み立てる |
| `app.Run` | rootのSession・model・configを準備し、固定instructionを足す | role選択とgeneric共通instructionの組立を共通化する |
| `app.delegation` | 同じ絶対config pathを再captureし、同一Agent定義を子に使う | 子開始時のcaptureから指定roleを解決する |
| `jsruntime.New` | run_jsを全Workerへ無条件で追加する | run_jsもrole.toolsの選択に従う |
| `app.executionSnapshot` | run_jsとtool名を保存JSONへ無条件で追加する | 実使用と同じ組立結果から保存JSONを作る |
| 生成JSのapi | tool呼出し、Go基礎API、直接のagents.runを公開する | 生成JSの公開操作を選択したtoolへ接続する |
| tool／hookのapi | Go基礎APIと委譲操作を渡す | tool／hook実装に必要な基礎APIを引き続き渡す |
| `history.AdoptCanonical` | 成功rootの会話・modelを採用するがAgent choiceは更新しない | 成功rootの実roleも同じtransactionでSessionへ採用する |
| TUI Backend.Run | positional引数でtask／Session／model／workspaceを渡す | roleを含む共通の実行requestへ整理する |

Agent loopと共通実行を別のrole別実装へ分岐させない。
provider・host・historyの責務をgenericやrole loaderへ移さない。

## generic・role・親子関係

- genericはbuilt-inの共通実行エンジン。model step、tool接続、終了結果を扱うGo loopを使う。
- roleは外部データ。名前、revision、説明、instruction、利用可能toolを持つ。
  作業ごとのtask・必要な文脈は呼出し時に渡す。
- root／子は実行時の所属。rootはSession会話を読み成功時に採用する。
  子は独立会話で開始し、呼出し元へ結果を返す。roleに親／子を固定しない。
- 「reviewer」という別エンジンを作らず、genericにreviewer roleを渡す。
  rootにも同じreviewer roleを指定できる。

genericの共通instructionは実際に使える操作に合わせて構成する。
defaultの作業方針とreviewerの作業方針は外部roleへ置き、rootの固定role instructionを子へ足さない。

## 外部定義の形式と選択

既存の編集可能なextensions/config.jsonにrole catalogを置く。
tool registry・module・hook・共通systemInstruction・modelの位置は同じconfigに保持する。
このJSONは入力設定であり、保存用WorkerConfigurationSnapshotとは別である。

次はrole部分の抜粋。toolsの実module定義等は既存のconfig項目と合わせる。

```json
{
  "defaultRole": "default",
  "roles": {
    "default": {
      "revision": "1",
      "description": "依頼を完了する汎用の作業担当",
      "instruction": "依頼された作業を実施し、結果を確認して報告してください。",
      "tools": ["run_js", "run_agent", "read_file", "write_file", "edit_file", "command", "http"]
    },
    "reviewer": {
      "revision": "1",
      "description": "ファイルを読んで目的と実経路に沿ってレビューする担当",
      "instruction": "対象の目的とsourceを確認し、根拠と利用者影響を伴う指摘を報告してください。",
      "tools": ["run_js", "read_file"]
    }
  }
}
```

catalog keyをrole名とする。toolsは使用するtool名の配列であり、空配列はtoolなしの実行を表す。
選んだrole名やtoolが解決できない場合は、対象名と原因を通常の構成準備errorとして報告する。
別roleへの暗黙切替や、tools配列を無視した全tool登録は行わない。

rootのrole選択は、明示 `--role`／TUIの選択、再開Sessionの保存role、config.defaultRoleの順。
子のrole選択は、呼出しのrole、子開始時config.defaultRoleの順。
子に親のrole instructionを暗黙コピーしない。taskへ渡した文脈は従来通り子の入力になる。
model／effortは既存の選択とI-02の実model継承を維持し、roleごとのmodel指定はI-03へ追加しない。

config.agentは入力設定から廃止し、旧形の自動変換・dual-readは作らない。
既存の利用者config・DBは上書き／削除しない。InstallDefaultsは不足fileのみ配置する現在の動作を維持する。
旧configを使っている場合は、新しいdefaultRole／rolesとrun_agentの登録を手動で追加する手順をREADMEへ記載する。
実確認では新形式の専用workspace／configを使い、利用者の既存設定を自動編集しない。

## tool選択と生成JSの接続

tool選択は説明文への反映だけでなく、modelへ提示するdefinitionとWorkerへ登録する実toolの両方へ反映する。
`run_js` はgenericのbuilt-in toolとして名前で選択する。選択しなければ提示・呼出し対象に含めない。
外部toolのregistryと、生成JS／hookの `tools.NAME` はそのWorkerで選択したtoolだけを持つ。

委譲は標準の外部tool `run_agent` を用意し、tool実装からGoのAgentRunnerへ接続する。
role.toolsでrun_agentを選べば、直接model tool callと生成JSの両方で委譲できる。

```js
return await api.agents.run({
  role: "reviewer",
  task: "internal/appを読んで、実行と履歴保存の責務をレビューしてください"
});
```

生成JSの `api.agents.run` は、選択された `run_agent` の呼出しへ接続する便利なAPIとして維持する。
`tools.run_agent` と同じtool・hook処理を通り、実委譲の専用call IDと親子relationはI-02の経路を使う。
run_agent未選択のWorkerには生成JS向けのこのAPIを公開しない。
Promise.all、戻り値 `{executionId,outcome,finalText?,error?}`、取消・保存errorの扱いはI-02と同じ。
tool入力はtaskと任意role。今回、呼出しごとの追加tool filterやmodel指定は導入しない。

生成JSへ渡す公開apiは選択したtoolを使う操作へ整理し、readFile／writeFile／command／httpの
直接のGo基礎APIは、外部tool・hookへ渡す実装用apiに置く。
外部tool／hookは必要なGo APIを使って振る舞いを実装する。roleでcommand内部の動作やfile pathを規定しない。
これは利用可能toolの組立契約であり、JS sandboxやAgent単位のOS・file・permission機構は追加しない。
既存の直接基礎APIを使う生成JSには、登録toolへ書き換える手順を文書化する。

実model definition、JS registry、genericの操作説明、保存snapshotのtool一覧は、一つの解決結果に対応させる。
RunJSDefinitionの説明も同じ構成に合わせ、run_agent未選択時に委譲APIの案内を含めない。
run_jsの無条件追加と保存時のtool名上書きを置き換え、未使用になる組立処理を残さない。

## capture・委譲・履歴・再開

親の開始時に、絶対config locator・capture bytes・選択したrole・tool構成を確定する。
子は同じlocatorを開始時に再captureし、そのbytesだけからroleとtoolを解決する。
同じcaptureの途中でconfigを読み直して選択結果と保存bytesを食い違わせない。
親の既存captureは維持し、role／tool sourceの編集は次の子・次のrootへ反映する。
available roleの名前・説明は、委譲toolを持つWorkerの共通instructionへ提示し、実モデルがroleを選べるようにする。

DDL v3と保存用snapshotのfield構造を維持する。
`executions.agent_name` とsnapshot.agent.nameは実role名を保存し、genericの実装版は既存build情報で辿る。
snapshot.agentには実roleのrevision・instruction・toolsを対応付ける。
snapshot.agent.agentsはrun_agentを選択している場合のcatalog role名、それ以外は空配列とする。
source.fileは使用したconfig locator、resourcesには実config・tool／hook・依存bytesを保存する。
model入力に使った共通instructionとrole説明も実使用のsnapshot・requestへ残す。

Sessionのagent_choice_jsonは旧形の `{name: 実role名}` を使う。
成功rootの採用transactionで、会話・model・role choiceをまとめて更新する。
失敗・取消では前のSession choiceを変更せず、実行時のroleはexecution証拠へ残す。
子はcanonical Sessionを持たず、親のSession choiceを更新しない。
過去のsnapshotは読戻し用証拠であり、再開時には現在configで保存roleを解決する。

I-02の子scope取消・Worker終了・終了保存・join、共通Goの保存error保持、親子relationを使う。
未awaitの委譲を残して親JSが正常終了または例外終了した場合、子取消・join後、外側callの終了前に、
当callに残る委譲tool／hookの観測をVM ownerがterminalへ閉じる。JSのfinally／__endの再開を前提にしない。
これはrun_agent wrapperを追加する経路の終了責務であり、子専用call IDと親子relation、既存host drainを維持する。
観測を二重終了せず、子の実取消結果と整合する終了を保存し、親JSの正常結果・原例外を保持する。
次の親stepを開始する時点で、前callの委譲tool／hookの開始だけが履歴に残らないようにする。
新たなdetach・queue・再帰数・並列数制限は追加しない。

## CLI・TUIの操作

```sh
hjh run --role default --stream --workspace /path/to/work 'reviewerへ調査を委譲し、結果をまとめて'
hjh run --role reviewer --workspace /path/to/work 'README.mdをレビューして'
hjh run --session SESSION_ID --workspace /path/to/work '前回の作業を続けて'
hjh tui --role default --workspace /path/to/work
hjh roles --workspace /path/to/work
```

- `roles` は現在のrole名・説明・tool構成・defaultRoleを表示する。model接続やDBを必要としない。
  config locatorと既定ファイルの準備はrunと共通化する。初回は不足する既定configを配置する。
  CLIではDB初期化より前にroles操作へ分岐する。
- TUI `/roles` は現在workspace／指定configのcatalogを共通Go操作から読み、`/role NAME` は次runの選択を設定する。
  `/role` は選択状態を表示する。実行中の変更は次runから反映し、進行中Workerの構成を変えない。
- TUIは明示選択がある場合はそれを使い、未指定時は共通Go側へSession／configの解決を任せる。
  `/new` は同じworkspaceで新Session、`/resume` は保存workspaceとroleを使う。明示roleは選択の優先順に従う。
- 起動flagのroleはTUIの明示選択へ渡す。Serviceの初期optionsへ固定して、TUIでの選択を無効化しない。
- 実行開始の進捗へ実role名を含め、TUIでは進行中の実roleと次runの明示選択を区別する。
  子roleの表示で親の本文・role・model・stepを更新しない。
- execution一覧・inspectに保存roleを表示し、子IDから子の実tool構成・sourceを閲覧できる。

Service／TUI BackendのRunはroleを含む構造化requestへ整理し、位置引数を増やし続けない。
role catalogの読込みも共通Go操作を使い、UIからSQLや拡張loaderの内部状態を直接操作しない。

## 実装順序と責務

| 段階 | 責務 | 成果 |
| --- | --- | --- |
| 1 | extensionsの入力設定・capture・role解決、共通instruction組立 | default／reviewerを含む外部定義と単一の実tool構成 |
| 2 | jsruntimeの登録・run_js選択・生成JS api、外部run_agent | role.toolsとmodel／JS実呼出しが対応する |
| 3 | appのroot／子の選択・capture・保存接続、history採用 | genericへ実roleを渡し、成功root choiceの保存・再開と子の独立実行が成立する |
| 4 | Service request・catalog操作、CLI／TUI | 親role選択・子role進捗・一覧・inspectionが同じ経路を使う |
| 5 | focused確認・実provider／PTY・Linux配布・実装review | 人間の操作から結果までを確認し、結果と未確認範囲を記録する |

主な境界は `extensions`、`app`、`jsruntime`、`history`、CLI／TUI。role解決をUIやproviderへ分散させない。
実装時には[拡張契約](../design/extensions.md)、[操作契約](../design/run-api.md)、
[保存対応表](../design/history-payloads.md)、[TUI設計](../design/tui.md)とREADMEへ実装した契約を反映する。

## 動作確認・完了条件

| 製品動作 | 確認方法・証拠 |
| --- | --- |
| 親も子も同じgenericでroleを使う | default親からreviewer子へ実モデルが委譲し、子がsourceを読んで報告、親が結果fileを作成・確認する。同じreviewerをrootでも実行する |
| 選択toolが実際に使われる | role.toolsとmodel request・Worker definitions・JSのtools・保存snapshotを照合する。run_jsあり／なし、空toolsは選択契約そのものとしてfocused確認する |
| 生成JSも選択toolを使う | 登録read_fileを生成JSから実行し、生成JSに実装用基礎apiを渡さないこと、委譲aliasが選択run_agentのtool／hook経路を使うことを確認する |
| 定義編集と保存bytesが一致する | 指定configを使い、親capture後にrole instruction／tool sourceを編集して次子へ反映。親は元の構成を維持し、両方の保存bytesを照合する |
| role選択を保存・再開できる | 成功rootのchoice保存、別runでのrole再解決、明示role優先、失敗・取消・子で保存choiceが変わらないことを実SQLite経路で確認する |
| CLI／TUIで選択・閲覧できる | roles／role選択、実行中の次role変更、root／子表示の分離をfocusedに確認。実PTYで選択・委譲・inspect・Esc停止・終了を操作する |
| I-02の寿命契約を維持する | 外部run_agent経由のawait／Promise.allと未await終了、親取消、子後処理保存errorの既知regressionを変更経路で確認する。未awaitの正常・例外終了では実VMとSQLiteで、子join後・外側call終了前に委譲tool／hookの終端記録が揃い、専用call ID・親子relation・実取消結果と整合し、親の正常結果・原例外が維持されることを確認する |
| 配布物でも使える | Linux packageのrole定義・tool・manifestを照合し、展開binaryから同梱configによる実委譲と履歴閲覧を完了する |

実providerは利用者指定のOpenRouter `deepseek/deepseek-v4.1-flash` と既存credential参照経路を使う。
credential／Authorizationをrole、JS、進捗、履歴へ渡さない現在の境界を維持する。
計画段階ではAPI callを行わず、実装が認可された後に製品経路の確認を行う。
実モデルがroleを選ぶこと、toolで実作業を完了することを、offline fixtureの成功で代替しない。

実装中は変更箇所のfocused test・必要なrace／vet／format／空白確認を使う。
review前full gateや全体gateの反復を要求しない。testは表の利用動作・既知regressionに対応させる。
実装と実確認の結果は `docs/results/i03-generic-roles.md` に記録する。
完成条件はCLI／TUIで親roleと子roleを使った実作業・結果利用・停止・履歴閲覧・再開が成立し、利用者が試せること。

## 計画reviewと対象外

通常reviewは設定・選択・Session・DDL／payload・CLI／TUIの整合性と実装可能性を確認する。
批判的reviewは、選択したtoolと実呼出しの食い違い、capture時点、委譲alias・hook・子scope、
会話採用とUIの選択状態の境界に、計画の成立を妨げる具体的な不足がないか確認する。
両者とも一般的なsecurity／hardening、未観測variant、permission matrix、旧機能の必須化は対象外。
初回30分、証拠・tool結果・中間結論が10分なければ中断。一回のre-reviewは変更箇所と既存findingに限り15分以内。

I-03ではコンテナ・OS VM、別processのAgent配置、Agent loopのJS化、roleごとのmodel、呼出しごとのtool filter、
Agent単位のpermission、旧config／DB変換、detach・queue、他provider／OS、TUI全体改修、公開を扱わない。

## 計画review結果

2026-10-10、独立したreviewerへ通常reviewと批判的reviewを依頼した。
設定・capture・共通実行・保存・Session採用・CLI／TUIの現行sourceとこの計画を静的に確認した。
コード変更、test、API callは行っていない。

- 通常review：必須指摘なし。実装段階へ渡せる判定。
  実装時の注意として挙がったRunJSDefinitionの条件付き説明とrolesのDB初期化前分岐を、上段へ明記した。
- 批判的review：P2一件。未awaitの外部run_agent wrapperでは、I-02の子joinだけを維持しても、
  JSへ結果をdeliverしない終了経路で外側tool／hookの__endへ到達せず、開始記録だけが残り得る。
  新しい接続経路の計画不足として採用し、VM ownerの終端責務と、対応する実VM／SQLite確認を補完した。
  現在のI-02委譲に不具合があるという判定ではない。Blocker／P1はなし。
- 変更箇所とこのP2の解消確認だけを、一回・15分以内で再reviewした。
  VM ownerの終了責務と対応する確認条件によりP2は計画上解消し、残存指摘なし、実装へ渡せる判定を受領した。

計画review時点ではI-03の実装・実動作は未確認だった。以後の実装・確認は[結果](../results/i03-generic-roles.md)を参照する。
