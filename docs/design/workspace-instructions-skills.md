# Workspace指示とPi方式のskills

2026-10-12。利用者が[I-18計画](../plans/i18-workspace-instructions-skills.md)の実装を指定し、
同計画の探索場所・優先順を採用した。実装の確認結果は別の結果文書へ記録する。

共通実行層がAGENTS.mdとskill一覧を読み、CLIの直接run、Core接続run、TUI、子Agentに適用する。
TUI/CLIはCoreのfilesystemを独自に探索しない。

## 指示ファイル

ユーザー共通の`${XDG_CONFIG_HOME:-$HOME/.config}/hjh/AGENTS.md`、
filesystemの祖先からworkspaceまでのAGENTS.mdを、この順に組み込む。
近いscopeの指示を優先する旨をモデルへ伝える。workspace配下の全指示ファイルは走査しない。
各親ターン、Session再開、子開始時に再読し、その実行中は本文を固定する。
F3追加入力では組み直さず、予約入力は実行開始時の内容を使う。

既存の組込み共通指示・role指示・systemInstruction・委譲案内へ本文を追加する。
configのinstructionComponentsは保存metadataのままで、新しい命令入力として解釈しない。
独立したbase instruction資産管理やモデルID別の指示は追加しない。

## Skill一覧と本文

次の順にSKILL.mdを探索し、同名skillは先順位を使う。

1. `<workspace>/.hjh/skills/`
2. `<workspace>/.agents/skills/`
3. `${XDG_CONFIG_HOME:-$HOME/.config}/hjh/skills/`
4. `$HOME/.agents/skills/`

再帰配置とsymlinkを利用できる。重複・解析できないsourceは診断に示す。
YAML frontmatterのname、description、disable-model-invocationを扱う。
name省略時は親directory名。descriptionは用途説明として必要。
その他のmetadataからpermissionやtool制限を追加しない。

一覧は名前・説明・絶対path・基準directory・出典だけを持つ。
標準read_fileを選んだroleへ自動invocableなskillの一覧と利用方法を提示する。
本文は直接read_file、またはrun_jsのtools.read_fileで利用時に読む。
生成JSでは取得本文を外側returnへ含め、モデルへのtool結果に入れる。
本文内の相対referenceやscriptはSKILL.mdのdirectoryから解決する。
本文の編集は次のread_fileから反映し、開始時の版へ差し戻さない。

一覧は各親/子開始時に再取得し、実行内では固定する。本文を常駐・自動再注入しない。
専用skill tool、明示/skill command、pin、watcher、plugin/remote providerはこの契約に含まない。

## 保存と予算

指示の実source bytes・locator・digestをresource_revision/contentsへ保存する。
組立済み指示とskill一覧はconfigurationと実model requestへ残す。
読んだ本文は通常のtool結果・messageとして保存し、既存のcontext budgetと履歴投影に従う。
新loaderのsourceはESM resource mapへ加えない。

既存extensions.Captureはconfig所在directory全体をsourceとして捕捉する。
--configのrootとskill配置が重なる場合、未利用本文も従来のextension source archiveへ入る。
そのarchiveをskill本文のモデル入力へ使わず、実利用時はdiskから読む。
SQL schemaは維持する。

## Core APIと表示

GET `/api/v2/orientation` はworkspace/Session/roleに対応した表示metadataを返す。
Modeがpreviewの場合は現在の候補であり、後の実runが同じ内容を使うことを保証しない。
Session作成、credential解決、モデル呼出し、JS評価、履歴書込みを行わない。
実runのRunState.ActualContextは開始時に固定したMode=actualのmetadataを持ち、再接続でも取得できる。

TUI起動・new・resume時に一覧を取得する。通常幅headerには基本指示の出典、
AGENTS出典、モデルへ提示するskill名を一度だけ表示する。agentとruntimeは載せない。
preview待機中も入力を受け、headerが確定するまで会話のscrollback移動を保留する。
実run metadataが先に届いた場合はそれで確定し、遅いpreviewでは上書きしない。
準備中runへattachした場合はactualが届くまでheaderを待つ。
actualなしでterminalになった場合だけ、previewの明示表示へ移る。
狭幅headerは2行を維持する。/contextはSession未作成でも出典・skill一覧を確認できる。
