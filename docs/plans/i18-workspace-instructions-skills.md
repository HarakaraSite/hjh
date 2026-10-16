# I-18：AGENTS.mdとPi方式のskills

> **後続変更の案内（2026-10-16）**：AGENTS／skills読込みは実装済み。以下のextension treeや元configの自動source archiveは後続で停止済み。AGENTS等の指示原文・出典は維持する。[読込み契約](../design/workspace-instructions-skills.md)・[保存契約](../design/normal-history-storage.md)が現行契約。

2026-10-12。状態：実装・test・実装review・実モデル・最終配布確認が完了。
結果は[実装・確認記録](../results/i18-workspace-instructions-skills.md)を参照する。
通常・批判的計画reviewの指摘反映と限定再確認は完了している。
利用者はAGENTS.mdとskillの読込み機構を必要とし、skillsについてPi方式を選択した。
利用者の進行指定により、計画案の探索場所・優先順を今回の実装契約として採用する。

## 必要な利用動作と成功条件

利用者がworkspaceへAGENTS.mdとskillを配置して、通常のCLI/TUIから依頼すると、
agentがworkspaceの指示に従い、必要なskillの本文を読んで作業を完了できること。
同じ動作をSession再開とsubagentでも使えること。TUIでは指示の出典と利用可能skillが分かること。

モデルへの入力は、既存の共通指示・role指示・tool定義・会話に、
AGENTS.mdの本文とskillの一覧を加える。skill本文は一覧へ埋め込まず、
利用時に既存のread_fileを通して取得し、通常のtool結果として会話へ入れる。
この会話で利用者が「モデル別の専用インストラクション」と呼んだものは、
既存のroles.<名前>.instructionを指す。モデルID別の新しい指示機構は追加しない。

実モデルが指示を使って作業を完了した証拠、実際の送信内容、保存履歴、TUI操作を完成判断に使う。
探索単体testや固定responseだけで完了扱いにしない。

## 根拠と参照範囲

| 根拠 | この計画で使う内容 |
| --- | --- |
| 利用者の明示指定 | AGENTS.mdとskillsの機構、Pi方式の本文取得、headerへの指示・skill情報、agentとruntimeのheader除外 |
| hjhの[初期製品方針](../design/initial-product.md)・[拡張契約](../design/extensions.md) | 親子共通の実行層、開始時の構成捕捉、外部role/tool、現在の設定による再開 |
| [履歴schema](../design/history-schema.md)・[payload契約](../design/history-payloads.md) | 実使用bytesと出典の保存、既存schemaの維持 |
| [I-14](../design/i14-web-context-contract.md) | 指示・tool・会話を含む実送信内容を既存の予算管理へ渡す |
| [header契約](../design/tui-header.md) | 会話を開いた時点で一度表示し、実行やresizeでheaderを重ねない |

参照したローカルsourceは以下。上流全体の機能を採用するものではない。

- Pi：`/home/agent/projects/henji-harness/_refs/pi`、commit
  `b30a6dd779340f7bc2f3ffa60f4c0a5f914ba9ae`。
  `packages/coding-agent/src/core/skills.ts`のformatSkillsForPrompt、
  `system-prompt.ts`のreaderに応じた一覧提示、`agent-session.ts`の明示skill展開。
  名前・説明・絶対pathを一覧へ入れ、通常のreadで本文を得る方式を採用する。
- Crush：`_refs/crush`、commit `8b1824282eccc78cc62c188c245cf375230a18ce`。
  `internal/skills/skills.go`のToPromptXML、
  `internal/agent/templates/coder.md.tpl`のView案内、`internal/agent/tools/view.go`。
  通常readerと一覧を接続する比較根拠。Crushは探索時にも本文を保持するので、その保持方式は採用しない。
- San：`_refs/san`、commit `baccf83caad7c6b3477fb3606cbaad60cb6ef036`。
  `internal/skill/loader.go`・`types.go`のmetadataと利用時本文取得の分離。
  専用Skill tool、状態管理、承認処理は今回の採用対象にしない。
- Zot・OpenCode・DeepSeek Harness・旧Henjiの専用skill tool方式も比較済み。
  今回はPi方式を選ぶため、それらのregistry、watcher、pin、専用toolは追加しない。

## 現行のproduct経路

standaloneのhjh runはapp.Service.Runを直接呼ぶ。
run --connectとTUIはHTTP API→corerun.Manager→同じapp.Service.Runへ入る。
app.RunがSessionのworkspace・roleを解決し、extensions.Captureで現在の設定を捕捉する。
子はapp/delegation.goから同じconfig pathを再captureし、選んだroleでexecuteへ入る。

extensionsが組み立てたSystemInstructionとWorker.Definitionsをapp/execution.goが
agent.Runへ渡し、各モデルstepで会話と一緒にcontextbudgetの投影・providerのprepareへ進める。
app/context.goが実送信内容を記録し、journal/historyがtool呼出し・結果・会話・resourceを保存する。

標準read_fileは外部index.js→Go hostへ進み、workspace相対pathと絶対pathを読める。
skill用の新しい読取り経路は不要。現在はAGENTS.mdの探索、skillカタログ、
それらのAPI metadataがない。設定のinstructionComponentsは現在保存metadataだけで、指示を組み立てない。

TUIはCoreのmetadataを使ってheaderを一度描画する。新規Sessionは初回runで作成する。
I-18着手時点のheader/短縮Session IDには未コミットの変更があったため、その内容を前提に接続した。

## 採用した読込み契約

### AGENTS.md

- ユーザー共通指示は`${XDG_CONFIG_HOME:-$HOME/.config}/hjh/AGENTS.md`。
  workspace指示はfilesystemの祖先からworkspaceまでにあるAGENTS.mdを読む。
  適用順はユーザー共通→遠い祖先→workspace。近いscopeの指示を優先する旨をモデルへ伝える。
- 名前はAGENTS.mdを対象とする。CLAUDE.md等の別形式や独自override形式は今回追加しない。
  指定workspace配下の全サブディレクトリを走査して指示へ追加することもしない。
- 親の各ターン、再開ターン、子の開始時に読み直し、その実行中の本文は固定する。
  子も同じworkspaceの指示を適用する。親のrole指示と会話を暗黙にコピーしない既存契約は維持する。
- 正確なsource bytes、絶対locator、digestと適用順を保持し、その同じ読取り結果から指示を組み立てる。
  存在しない候補は通常の空結果。実在する指示を読めない場合は出典付きの問題として報告する。
  表示・保存できた指示と実際に適用する指示を一致させる。

### skills

既定の探索場所と同名skillの優先順は以下を採用した。Pi方式の採用は、Pi固有の配置や
すべての互換scopeをそのまま引き継ぐ意味ではない。

1. `<workspace>/.hjh/skills/`
2. `<workspace>/.agents/skills/`
3. `${XDG_CONFIG_HOME:-$HOME/.config}/hjh/skills/`
4. `$HOME/.agents/skills/`

- 各root配下のSKILL.mdを再帰探索し、同名は上記の先順位を使う。
  重複は勝者・出典が分かる診断にし、同じ実fileへの別経路は重複提示しない。
  symlinkで配置したskillも利用できるようにする。循環するリンクは探索を繰り返さない。
- YAML frontmatterからname・description・disable-model-invocationを扱う。
  name省略時は親directory名を使う。descriptionは用途をモデルへ伝えるために必要。
  通常のYAML parserを使い、旧Henjiの独自parserや名前・件数・byte数制限は持ち込まない。
  その他のmetadataからtool権限や操作制限を新設しない。
- カタログに保持するのは名前・説明・絶対SKILL.md path・基準directory・出典。
  discoveryでファイルを解析しても、新カタログには本文を保持せず、モデルへ先送りしない。
  既存extension source捕捉との重なりは後述の保存契約に従う。
- 親/子の実行開始時に一覧を固定し、モデルへ名前・説明・pathと利用方法を提示する。
  明示名または用途が一致するskillは、作業前にread_fileで本文を読み、
  本文にある相対参照はSKILL.mdのdirectoryから解決するよう案内する。
  直接のread_file呼出しでは返却本文がモデルへのtool結果になる。
  run_js内のtools.read_fileで取得する場合は、読み取った本文を外側returnへ含めるよう明記する。
  例えば`return await tools.read_file({path: absoluteSkillPath});`とする。
  内側toolの観測だけでは本文はモデル会話に入らず、`return "loaded"`を本文提示の証拠にはしない。
- 本文はread_fileを呼んだ時点のファイル内容。その後の読取りでもその時点の内容を返す。
  実行開始時の一覧と利用時本文の版が異なる場合も、実際に返したtool結果を履歴へ残す。
  本文を開始時カタログの版へ差し戻す処理は追加しない。
- roleが標準read_fileを選択している場合に、この読取り案内をモデルへ提示する。
  toolなしroleへreaderを暗黙追加しない。カタログmetadataでは発見済みskillとモデルへ提示したskillを区別する。
- disable-model-invocationがtrueのskillは自動選択用一覧へ出さない。
  `/skill:<名前>`による明示展開はこの計画の範囲に含めないため、今回の実装では自動利用対象外となる。
- 解析できないskillは出典付き診断で示し、他の有効なskillは利用可能とする。
  通常skillの内容を理由にrun全体を開始不能にする入力制限は設けない。

### モデル入力・編集・予算

既存の共通指示、role説明・指示、systemInstruction、委譲先一覧の組立を維持し、
workspace指示とskill manifestを追加する。共通の構成処理で親・子・previewを接続する。
外部設定のinstructionComponentsを、この変更で新たな命令入力として解釈しない。

AGENTS.mdと一覧の変更は次の親/子開始から反映する。予約入力は現在のselection契約を維持し、
指示と一覧は実行開始時に読む。F3追加入力では実行中の指示・一覧を組み直さない。
skill本文の編集は次のread_fileから反映する。

AGENTS本文・一覧・tool定義を含む実wireが既存の予算計測を通る。
取得したskill本文は、直接read_file結果または本文を含む外側run_js結果として
モデル会話へ入れ、ターン内・ターン間の通常の履歴投影に従う。
skill専用のtoken予算や常駐領域、本文の自動再注入は追加しない。

## 責務・保存・表示

| 所有者 | 担当する内容 |
| --- | --- |
| 新しい`internal/workspacecontext` | AGENTS探索と実bytes、skill metadata探索、一覧、出典・診断。DB・HTTP・TUIへ依存しない |
| extensionsとappの構成処理 | 選択role/toolとworkspace contextから指示を組み立て、開始時の実使用構成を固定する |
| app.Run / delegation | 同じ準備処理を使って親/子を開始。子のworkspaceとrole、次ターンの再読を所有する |
| app/history | AGENTSの実source、skill manifest、実指示・モデルrequest、通常read_file結果を既存schemaへ保存する |
| httpapi / corerun / apiclient | 読込みmetadataのpreviewと、実runが固定したmetadataをJSONで返す |
| TUI | Coreのmetadataを表示する。Core側のpathをclientのfilesystemで探索しない |

新loaderのAGENTS sourceは既存resource_revision/contentsへ、実指示・skill一覧は
既存のconfiguration snapshotとcontext_item/model_requestへ保存する。
新loaderが取得したworkspace sourceは、実行context側で扱い、ESM resource mapへ追加しない。
新カタログのために未利用skill本文を一括archiveせず、実際に読んだ本文は
既存のtool_result/messageから参照できるようにする。生成JS内の読取り観測と、
モデルへ返す外側結果は区別し、実requestで本文が届いたことを確認する。

ただし既存extensions.Captureはconfig所在directory全体をsourceとして捕捉する。
`--config <workspace>/config.json`などでcapture rootとskill配置が重なると、
未利用skill本文も従来のextension Resources・ESM resource map・resource_revisionへ入る。
これは新カタログの本文保持とは別であり、この計画で既存Captureを全面変更したり配置を拒否したりしない。
そのarchive bytesをskill本文のモデル入力へ使わず、利用時のread_fileは現在のdiskを読む。
Pi方式の契約は「本文を利用時にモデルへ渡す」であり、「他のsource保存経路でも本文を一切読まない」ではない。
新しいSQL tableや二重のtool実行記録は作らない。

表示用queryは既存selection queryとは分け、`GET /api/v2/orientation`を候補とする。
workspace/Session/roleを受け、既存のrole解決とcontext探索からmetadataだけを返す。
新Session作成、credential解決、モデル呼出し、JS module評価、履歴書込みは行わない。
初回run前にextensionsが未配置なら、既存selectionConfigと同じ組込み設定でroleを解決する。

実行開始の既存観測とRunStateには実使用contextの小さいmetadataを接続する。
attach/reconnectでは、そのrunの固定済みmetadataを取得できるようにする。
previewの読取り結果を、後のrunでも同じ内容を使ったという証拠にはしない。

通常幅headerには以下を追加する。

- 基本指示：現在の組込み共通指示とconfig.systemInstructionの出典。
  独立したbase instruction file/version管理を追加する意味ではない。
- context：読んだAGENTS.mdの出典。未検出と読込み未完了を区別する。
- skills：モデルへ提示する利用可能skill名。本文を既に読んだskill名という意味にはしない。

TUI起動時と新しい会話を開く時に、表示用一覧のpreviewを取得する。
モデル用一覧は各親ターン/子の開始時に再取得して固定する。Core起動時の一覧を永続cacheしない。
新規/非実行Sessionでは起動時previewと表示し、実行中Sessionへの接続では実runのmetadataを使う。
準備中runへの接続もactualを待ち、actualがないままterminalになった時だけpreviewへ移る。
起動時previewを待っている間も初回入力を送信できる。
header未確定の間は会話をlive領域へ表示し、scrollbackへの会話sealはheader確定まで保留する。
実run metadataが先に届けば、それを使ってheaderを一度確定する。
preview成功/失敗で先に確定した場合を含め、確定後の遅いpreviewや実行更新でheaderを上書き・追加しない。
現在の実run情報はrun stateと/contextから確認できるようにする。
表示queryの失敗はheaderに未確認と示し、通常の作業入力は維持する。
狭い端末では既存の2行headerを維持し、`/context`にCoreが返す現在の出典・一覧を表示する。
新規起動や/new直後のSession未作成時にも、/contextはworkspace/roleのorientationを呼んで表示する。
保存会話・予算などSessionContextに属する部分だけをSession未作成として省略する。
Session切替には既存sessionGenerationの管理を使い、workspace/roleの要求との対応も確認して、
古いquery結果を新Sessionや別roleの表示へ適用しない。
agentとruntimeをheaderへ戻さない。短縮ID・日時・footerの現行動作を維持する。

## 実装順序

1. 本計画の探索scope・優先順・header表記を確定し、読込みと表示の契約を設計文書へ置く。
2. workspacecontextのAGENTS/skill探索とmetadataを実装する。
3. 親・子の実行準備と指示組立、source保存へ接続する。予算管理は既存経路を使う。
4. orientation queryと実行metadataをAPIへ接続し、TUI headerと/contextへ表示する。
5. 動作に対応するfocused確認、実Core/TUI/CLIと実モデル確認、自己review、配布binary確認を行う。
6. README、拡張契約、header契約、history payload説明、結果文書を最終実装に合わせる。

計画作成の依頼だけでコード実装、commit、pushは行わない。
独立reviewを行う場合は通常のcorrectness・要件・実経路を対象とし、
global/repositoryのbounded review規約に従う。一般的hardening reviewは追加しない。

## 動作と確認の対応

| 必要な動作 | 確認する実経路・証拠 |
| --- | --- |
| workspaceの指示が適用される | 異なるscopeのAGENTSを用意し、実requestの順序・本文・source bytesと作業結果を照合 |
| 未利用skill本文を送らない | 本文だけに固有markerを置き、最初のrequestには名前・説明・pathだけがあることを確認 |
| 必要なskillで作業できる | 実モデルがread_fileでSKILL.mdと相対referenceを読み、本文の手順に従い成果物を作る。直接呼出しと、本文をreturnするrun_js経由でモデルrequestに本文が届くことを確認 |
| 利用時の内容を読む | 一覧確定後に本文を編集し、通常read_fileの返却内容と履歴が編集後の内容になることを確認 |
| 次ターン・再開で更新される | AGENTS/descriptionを編集し、既存Sessionの次runで新しい指示・一覧になることを確認 |
| 子にも同じ機能がある | run_agentで別roleへ依頼し、子のAGENTS・一覧・本文取得・成果物・保存を確認 |
| tool構成と案内が一致する | 既存toolなしroleを使い、readerを追加せず、呼べないread_fileを案内しないことを確認 |
| 既存の予算管理へ入る | 指示・一覧・取得本文を含むwireとbudget factを照合。本文が通常tool結果として履歴投影を通ることを確認 |
| headerと再接続が正しい | preview待機中の初回入力と実metadata先着を確認し、headerより前に会話をsealせず、遅いpreviewで重ねない。実run attachとSession切替でも出典と表示先を照合 |
| 狭幅でも依頼前に確認できる | 新規起動と/new直後に/contextで指示・一覧を表示し、Session作成を要求しないことを確認 |
| 過去の証拠が保持される | 元file編集後も旧executionの指示・返却本文をinspectから確認。schemaを変更しない |
| 保存経路の重なりを説明できる | config rootがworkspaceにある経路で、既存archiveに含まれる本文とモデルへ渡した本文を区別。未利用本文を指示へ組み込まないことと、利用時の編集がread_fileへ反映されることを確認 |

実装中は関係packageのfocused test、format、vet、diff checkを行う。
全体testは安定候補でrootが一回実施し、新しい修正・具体的な失敗理由がある場合だけ再実行する。
ユーザーの稼働Core・DB・tmuxは操作せず、private workspace/DB/Coreで実動作を確認する。
credential値とAuthorizationは出力・履歴へ出さない。費用を伴うモデル確認は既存の明示許可範囲に従う。

## 対象外・残る判断

専用skill tool、skillのpin、/skill明示展開、/skills picker、watcher、plugin/remote skill provider、
skillの自動編集、独立base instruction資産管理、モデルID別指示、別形式のinstruction file、
tool権限機構、旧履歴の変換は対象外。

探索scopeと優先順、ユーザー共通pathは実装進行の指定によって採用した。
実モデルによるskill選択、読取り、相対resource使用、既存予算下での作業完了、
最終binaryのTUI表示を確認した。具体的な確認範囲と試行の限界は結果文書へ記録する。

## 計画reviewの反映

通常reviewはP2一件、批判的reviewはP2三件。以下を採用して計画へ反映した。

| 指摘 | 反映内容 |
| --- | --- |
| 既存Captureと未利用本文archiveの境界 | 新カタログの責務と既存extension source捕捉を分離。--configでrootが重なる経路を明記し、本文のモデル提示は利用時とする |
| 生成JSで取得した本文がモデルへ届く条件 | 外側run_js結果へ本文をreturnする案内と確認を追加。内側観測だけをモデル入力証拠にしない |
| previewと初回入力・header確定の順序 | 入力を許可し、header前の会話sealを保留。実metadata先着時に一度確定し、遅いpreviewで上書きしない |
| 狭幅・Session未作成時の確認 | /contextからworkspace/roleのorientationを表示し、保存会話・予算部分だけを省略 |

headerへのrole指示表示は要件ではないことを会話と照合した。
通常reviewは一覧取得のタイミングと現行の構成捕捉契約の整合を確認した。
両reviewerが変更箇所と既存findingに限定した再確認を一回行い、全四件の解消を確認した。
実装・実動作の成立は、この計画reviewの完了で代替せず、実装後に確認する。
