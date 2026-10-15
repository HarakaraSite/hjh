# ツール利用ガイドラインと共通help：実装・確認結果

2026-10-15。[計画](../plans/tool-usage-guidance.md)の最新合意に基づき実装・focused test・コードreview・一時環境の総合確認を実施した。
[利用契約と更新方法](../design/tool-usage-guidance.md)を参照。実providerによる受理・モデル選択改善は未確認。
実装確認後、利用者がcommit/push/配置を指示した。配置については末尾の続報を参照。
利用者は「総コンテキスト量の削減は目的ではなく、ツール利用の最適化が目的」と明確にした。
以下のbytes比較はコストの観測であり、入力削減を受入条件にしない。

## 実装した動作

- 実行開始の最初の入力から、短い共通利用方針と選択済みtoolの定義/個別guidelinesを渡す。
- 独立Markdown方針はworkspace `.hjh/instructions/tool-usage.md` → user config `hjh/instructions/tool-usage.md` → 組込み既定の順で一つを選ぶ。
  XDGはos.UserConfigDirに従い、空fileは方針なし。base差替えとは独立。
- appの共通workspace準備で親ターン・再開・子開始に読む。実行中固定。config directoryと実workspaceを混同しない。
  実装はCaptureOptionsを拡張せず、capture済みSnapshotへApplyToolUsagePolicyで追加する。
- 判断順はrun_jsが必要か（合成/独立並列/集約/文字列JSON加工）→単独専用tool→必要な外部操作にcommand。
  常にJSで包む義務、commandの禁止、status/steerの義務はない。
- 選択済みの各toolは `{help:true}` のみで詳細を返す。結果は `{name,available,text}`。
  beforeTool/通常実装/afterToolを呼ばず、既存のtool観測・message保存は維持する。
- 通常の必須引数は通常branchに維持し、help branchはhelp:trueだけを必須にするobject/anyOf schema。
  引数定義はrootに一度だけ置く。通常schemaは変更せず、model定義時に共通schemaを生成する。
- 外部作者は任意helpFileを通常のtool宣言へ登録する。builtinはrunJSHelpFile。Markdownは既存capture内のbytesを使用する。
  未登録はavailable:false、設定された参照が見つからない場合はhelp取得時にエラー。通常利用を妨げない。
- 標準19toolのhelpを配布し、長い使い方/例を常駐から分離した。role/toolsの一覧・子の明示上書き/空配列は変えない。
- 指示component/外部方針原文の保存には既存経路を使う。help/module/config一括archive、別help tool、検索、DB/cache、出典UIは追加しない。

共通ガイドラインは独立fileの手動改訂。tool変更への自動追従は解決していない。第三者のtool作者は実装とhelpを同じ配布単位で改訂できるが、説明の内容まで自動更新する機能ではない。

## schema根拠と未確認範囲

2026-10-15取得：

- [OpenAI Function calling](https://developers.openai.com/api/docs/guides/function-calling.md)：parametersはJSON Schema。
  Responsesの非strictはstrict:falseを明示し、Chat Completionsは非strictが既定。
- [OpenRouter Client tools](https://openrouter.ai/docs/guides/features/tool-calling.md)：OpenAI互換のJSON spec/parametersで宣言する。
- [OpenCode Go](https://opencode.ai/docs/go/)：OpenAI/OpenAI-compatible endpointを確認したが、条件branch schemaの詳細受理契約までは記載がなかった。

現行adapterは元schemaをparametersへ渡し、Responsesは既にstrict:falseを使う。provider動作は変更していない。
生成schemaはローカルDraft 2020-12 validatorでhelp-onlyと通常required/type/additionalPropertiesの意味を確認した。
Chat/Responses/namespaced Responsesの実encodeとローカルHTTP往復を確認した。
**これは実provider/全modelの受理確認ではない。** 有料/実providerは呼んでいない。

## focused確認

- `go test ./internal/extensions ./internal/jsruntime -count=1`：担当実装者が成功を確認。
  schema生成・非破壊、直接/nested help、hook/実装の非実行、観測owner、capture固定と更新、第三者/未登録help、選択済み/空toolを確認。
- `go test ./internal/extensions -run TestToolUsagePolicySelectionAndComposition -count=1`：成功。
  user/XDG/workspace/空file、baseとの独立・組立順・固定と再準備を確認。
- `go test ./internal/app -run 'TestWorkspaceContextDirectJSResumeAndChild|TestOrientationPreviewDoesNotCreateSessionOrExtensions' -count=1`：成功。
  既存workspace指示とpreview/親子/再開経路のregression確認。
- `go test ./internal/app -run TestToolUsageHelp -count=1 -v`：成功。
  初回方針、親/子/再開、直接/JS/builtin help、canonicalの旧結果と新取得、出典/通常結果保存、未使用sourceの非archiveを確認。
  配布helpから実codeを取得してread並列/加工/依存編集、spawn→親作業→status→steer→collectをapp経路で実行。
- `go test ./internal/provider/protocol -run '^TestToolHelp' -count=1`：成功。
  19toolのschemaをChat/Responses/namespace wireで照合し、ローカルHTTP/SSEでhelp呼出しdecode→結果送信→最終応答を確認。
- 標準helpは19tool全て取得できること、10のfile/inspection/command例、5つの子操作例を担当者が実Worker/appで確認。
  公開networkを使う3例は未実行。
- gofmt、`git diff --check`は成功。full repository gateの反復は行っていない。

## 入力量の前後比較

旧baselineはHEAD `327f55960fba0f1d691e1b64084c59ed09e84b64`の実extensions.go/配布asset。
旧sourceをGo overlayで実行する際は新規field三つをコンパイル用に追加しただけで、旧動作を変えていないことを差分確認した。
新treeと同じrole/tools・model local-help-test・effort auto・task `Read the run_js help and report completion.`を使った。
一時HOME/XDG/workspaceでAGENTS/skills/hook/実行情報を加えず、protocol.Prepareの正確なbytesを比較した。

| role | Chat旧→新 | Responses旧→新 | namespace Responses旧→新 | 増分 |
| --- | ---: | ---: | ---: | ---: |
| default | 25,738→29,381 | 25,765→29,408 | 25,849→29,492 | +3,643 |
| reviewer | 8,754→12,654 | 8,757→12,657 | 8,841→12,741 | +3,900 |
| explorer | 8,759→12,659 | 8,762→12,662 | 8,846→12,746 | +3,900 |
| implementer | 17,068→20,912 | 17,083→20,927 | 17,167→21,011 | +3,844 |

| role | instruction旧→新 | active guidelines旧→新（instructionの内数） | schema旧→新 |
| --- | ---: | ---: | ---: |
| default | 11,695→9,890 | 9,983→6,401 | 9,945→15,417 |
| reviewer | 2,934→4,805 | 2,163→2,257 | 4,233→6,249 |
| explorer | 2,939→4,810 | 2,163→2,257 | 4,233→6,249 |
| implementer | 6,717→6,831 | 5,922→4,259 | 7,292→11,036 |

共通方針1,775 bytes、help schema増分288 bytes/tool。標準外部toolのguidelines本文は8,069→5,453 bytesに減ったが、
初回総wireは全roleで増えた。これは利用コストの観測として残す。
目的はtool選択・利用の最適化であり、総コンテキスト量の削減は成功条件ではない。
開始時の選択指針と各toolへの直接helpの経路は成立した。実モデルの選択改善は別途観測する。

Workerで取得したrun_js helpは本文4,699 bytes、結果JSON4,864 bytes。
その1交換を追加するとChat +5,241、Responses +5,197、namespace +5,233 bytes。
read_file helpは結果1,260 bytes、後続wire増分は各+1,506/+1,462/+1,498 bytes。
helpは通常1回の追加model往復となり、履歴に選ばれる間は後続入力にも残る。実token量/latencyは未計測。

## コードreview

- S1の方針読込み・source保存：reviewer指摘なし。
- 安定したproduction経路（help/schema/親子/登録/capture/方針/観測）：reviewer指摘なし。
- 標準config・help実例・説明・focused testの経路：別reviewer指摘なし。

reviewerはsourceを照合し、full gateやDB/実provider操作は行っていない。
以前の計画reviewにあった「未選択tool_helpを必須要求」の問題は、独立tool_helpを撤回したため現方式では生じない。

## 一時Coreでの総合確認

`python3 scripts/verify-tool-help.py` をcoordinating ownerが実行した。
実binaryを一時directoryへbuildし、隔離HOME/XDG・workspace外config・SQLite・Core catalog・loopback providerを使用。
常用Core/DB/settingsへは接続していない。

最終結果：passed。

- binary SHA-256：`1490fd987c2f28f7e435e323d3e34ce1f2ab203d4200479da526194f4adbc15e`
- Session：`fed5d7ef-fa49-483d-8989-04c65044dac7`
- root実行：`58a6ddd3-f5e4-479a-a6c8-f9fe63df9068`（4モデル要求）
- resume実行：`fb0ed761-fd7a-47fd-bd32-bd50ba3f4b7d`（2モデル要求）
- read_file help（pathなし）、run_js help（codeなし）、取得した並列read例の実行、編集後の方針/helpと旧履歴の共存を確認。
- canonical履歴10,788 bytes、help呼出し3件。最新wireは各11,948/13,659 bytes。
- source locator/digest、終了時最新wireの本文、help通常message、未取得helpの非archiveを照合。
- 子lifecycleは上記focused app確認で扱い、総合harnessで重複実行しなかった。

総合確認はharnessの誤りで3回失敗してから成功した。製品コードの変更は不要だった。

1. --connectに/api/v2付き表示URLを渡した。apiclientがprefixを加えるためoriginに修正。
2. CLI history JSON keyをJSONとした。公開形式jsonへ修正。
3. model_requestの物理送信記録と準備metadataを区別せず最新recordを比較した。wireBytesを持つ準備metadataに絞った。

各失敗はfocused source照合で検証側の原因を特定し、その修正による再実行とした。成功した総合候補のproduct差分はreview後から不変。
一時Coreだけを停止し、一時データを終了時に片付けた。実モデルの使い方改善を証明する結果ではない。

## 未実施・引き続き必要な確認

- 実providerでのhelp schema受理、実モデルによるtool選択の改善。
- 常用workspaceのconfig/help配置。既存configは自動上書きしないため、必要な参照/文言を手動反映する必要がある。
- ガイドラインの改訂運用は独立fileの手動編集のまま。

上記実装・検証段階では常用`.hjh`変更、Core再起動、DB変更、commit/push、release配置は実施していない。
既存の`docs/design/history-schema.md`変更等、今回以前の作業差分は維持した。

## 続報：commit・push・常用配置

利用者の「コミットプッシュ配置して」に従い、本機能に関するsource・test・資料をcommitし、origin/mainへpushする。
既存のhistory-schema変更、handoff、過去の試験fileは含めない。
配置先は従来どおりdist/hjhとdist/hjh-tui。commit revisionを埋め込んで一時pathへbuildし、version/hash確認後renameする。

常用.hjh/extensions/config.jsonはbackupを取り、標準toolのhelpFile、runJSHelpFileと今回変更したpromptGuidelinesのみを反映する。
provider/credential/model/role/tools/hook/独自description等は維持する。標準help Markdownを配置し、
ユーザー共通~/.config/hjh/instructions/tool-usage.md（XDG指定時はそのroot）へ編集可能な標準方針を置く。
既存DBを変換せず、稼働中Core/TUIは停止しない。新binaryのhelp/schema/方針読込みが常用Coreへ有効になるのは再起動後。
配置確認は新binaryのversion、config固有設定の保持、全選択toolのcaptured help、実方針sourceをローカルで照合する。
実providerは呼ばず、既存Coreの稼働とPIDが維持されていることを確認する。
