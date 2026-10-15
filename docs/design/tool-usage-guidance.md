# ツール利用方針と各toolのhelp

短い共通方針と選択済みtoolの用途・引数schema・最小契約は、最初のモデル入力から渡す。
詳細な手順や使用例は、そのtoolを`{help:true}`で呼んで必要時に読む。
独立した辞書tool・検索index・動的tool公開や、新しい設定UIは設けない。

## 常駐する方針

判断順は次のとおり。すべての操作をJavaScriptで包む義務ではない。

1. 合成・独立した並列呼出し・集約・文字列/JSON加工に`run_js`が必要か判断する。
2. 不要な単独操作は、目的に合う専用toolを使う。
3. 選択済みの専用toolとJavaScriptで足りず、外部program/libraryやOS操作が必要なら`command`を使う。
   build/testやgit commit/pushなどはこの例に当たる。単にcommandをJSで包んでも判断は変わらない。

必要な範囲だけ取得し、加工して返すときも原文・識別子・判断の根拠を残す。
親にも独立作業があればspawn → 親の作業 → 必要時collectを使う。
独立作業がなければ同期実行や即時collectでよく、status/steerを機械的に挟まない。
通常tool callを同じモデル応答へ複数並べても順次実行であり、それだけでは並列にならない。

### 配置・置換・読込み

以下の最初に存在するファイル一つを選び、共通方針全体を置き換える。追記・結合ではない。

1. `<workspace>/.hjh/instructions/tool-usage.md`
2. `os.UserConfigDir()/hjh/instructions/tool-usage.md`
3. 組込みの独立Markdown `internal/extensions/tool_usage_policy.md`

Linuxのuser配置は通常`~/.config/hjh/instructions/tool-usage.md`、XDG指定時は
`$XDG_CONFIG_HOME/hjh/instructions/tool-usage.md`。未配置なら次候補を使い、
**空ファイルは方針なし**を明示する。その他の読取りエラーは実行準備へ返す。
workspaceは実行対象のworkspaceであり、`--config`の所在directoryではない。

CLI直接run・Core接続run・TUI・子agentは、共通の`app.prepareWorkspaceContext`から
`extensions.ApplyToolUsagePolicy`を呼ぶ。baseの後・active tool guidelinesの前へ挿入する。
[base.md](base-instructions.md)とは独立しており、base差替えや空baseでもこの方針は残る。
role・個別tool指示・AGENTS・skills・hook文脈も別の責務として維持する。
本文にtool名があっても能力は追加せず、実際に選択されたtoolだけを使える。

各親ターン・再開・子開始で読み直し、開始済みexecution内では固定する。
各model stepやF3/steerで再読しない。子は開始時のファイルを読み、親の固定本文をコピーしない。
方針は手動編集であり、tool変更への自動追従・自動生成・同期検査は行わない。

## 共通help呼出し

選択済みのすべてのtool（builtin `run_js`を含む）は、通常引数なしで呼べる。

```text
read_file({help:true})
run_js({help:true})
spawn_agent({help:true})
```

結果は`{name, available, text}`。`name`は呼んだtool名、`text`は登録したMarkdown本文。
help未登録なら`available:false`と詳細未登録の説明を返す。toolが無効という意味ではなく、
通常利用も可能である。登録したhelpFileをcapture内で見つけられない場合は取得エラーになる。
情報不足は情報不足として扱い、helpの取得を通常操作の必須gateにはしない。

helpは共通の呼出し分岐で処理し、`beforeTool`・通常実装・`afterTool`を実行しない。
ただしtool callの開始・終了・結果は通常どおり観測・保存する。
生成JS内の`tools.NAME({help:true})`も同じ経路を通る。必要な説明は外側のreturnへ含める。

```js
// Retrieve the selected read tool's captured help.
return await tools.read_file({help:true});
```

`help`は共通予約引数。tool作者は独自の業務引数として使わない。
未指定またはfalseなら通常のrequired・引数・戻り値・hook経路を維持する。
通常呼出しではhelpを省略できる。現行runtimeは`help:false`を取り除かず、hookと外部実装へ渡す。

モデル向けschemaはrootをobjectのまま保ち、`anyOf`で次の二つを表す。

- `help:true`：helpだけを必須にし、path/code/task等は要求しない。
- 通常呼出し：元のschema・requiredを維持し、helpはfalseまたは未指定。

外部宣言の通常引数を一律optionalにはしない。provider adapterやstrict設定の挙動は変更しない。
この表現はobject引数のtoolを対象とし、任意の非object schema対応を新たに約束するものではない。

## tool作者の登録

外部tool宣言へ任意の`helpFile`を加える。pathはconfigのあるextension root相対であり、
実装moduleのdirectory相対ではない。実装とMarkdownを同じ配布単位に置ける。

```json
{
  "name": "my_tool",
  "description": "Short purpose.",
  "promptGuidelines": ["Choose this tool for the specific operation."],
  "inputSchema": {"type":"object","properties":{"path":{"type":"string"}},"required":["path"]},
  "module": "my-tool/index.js",
  "export": "myTool",
  "helpFile": "my-tool/help.md"
}
```

通常のtool登録・roleの選択だけでよく、中央のhelp登録や本体改修、JS実装内のhelp分岐は不要。
helpFileを省略してもよい。複数toolが同じMarkdownを参照できる。
短い用途・正しく呼ぶ最小契約はdescription/schema/promptGuidelinesに残し、長い例をhelpへ置く。
例に必要なtoolを本文へ明記しても、そのtoolを自動選択することはない。

builtin `run_js`は外部toolsへ二重登録せず、config最上位の
`"runJSHelpFile": "run-js-help.md"`で指定する（Go fieldは`Config.RunJSHelpFile`）。
これもextension root相対の任意参照であり、同じhelp取得経路を使う。
標準のrun_js helpにはPromise.all、加工、依存するread → edit、子操作の別名と例がある。
子のhelpにはspawn → 親作業 → 必要時status/steer → collect等を置く。

### 新規配置と既存configの更新

標準ファイルは`internal/extensions/assets/`から`extensions.InstallDefaults(dir)`が不足分だけ配置する。
**既存のconfig/module/helpは上書きしない**ため、更新binaryだけでは編集済みconfigへ参照や短縮文を追加しない。

既存環境では、実際に使うextension root（通常`.hjh/extensions/`、`--config`ならその所在）へ、
採用する標準`*-help.md`を不足分だけコピーする。既存の同名ファイルは差分を確認して手動編集する。
そのうえで現在のconfigを編集し、配布configを参照して対象toolの`helpFile`と最上位の
`runJSHelpFile`を加え、採用するdescription/promptGuidelinesの変更だけを反映する。
必要なら共通方針を上記tool-usage.mdへ置く。省略時は組込み方針を使える。
編集済みconfig全体を配布版で置き換えず、role・provider・model等の独自設定を維持する。
新しいhelp用のtool登録やschemaへの手書きhelp分岐は不要で、変更は次executionから反映する。

## capture・保存・再取得

help本文はexecution開始時の`Snapshot.Resources`に捕捉したbytesから取得し、
呼出し時のdiskや履歴DBから読まない。同じexecutionでは定義・実装・説明のcaptureを揃え、
編集は次の親ターン・再開・子開始で再captureして反映する。

`source` locatorやdigestは出典を示すもので、過去のextension sourceを復元するarchiveではない。
config・module・依存・未取得helpの原文全体をDBへ自動archiveしない。
取得したhelp結果は通常tool messageとして履歴・次stepのcontext budget経路へ入り、
pin/cache/自動再注入はしない。子のhelpも親へ暗黙には共有しない。
同executionの入力に残るhelpは再利用できるが、新ターン・再開後に詳細が不確かなら再問い合わせする。
古いhelpが履歴にあるだけで現在の契約と決めつけない。

共通方針は`instruction:tool-usage-policy` componentへ本文・path・digest・builtinVersionを記録する。
外部tool-usage.mdの原文はbase/AGENTSと同じ既存`resource_revision`経路へ保存する。
これはextension source一括archiveとは別で、新しいDB schemaや出典UIは追加しない。
保存全般は[通常履歴保存](normal-history-storage.md)、runtime境界は[拡張契約](extensions.md)を参照する。

ここで定めるのは指示と呼出し・保存の契約であり、実モデルのtool選択改善やtoken削減を示すものではない。
