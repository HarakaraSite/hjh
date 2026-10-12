# I-21 toolごとの利用指示

2026-10-12。利用者は、任意toolの利用指示を有効toolから集める機構を実装し、
run_js・子agent操作・今後追加するtoolもこの機構へ載せるよう指定した。

## 利用動作と成功条件

外部configのtool定義へ`promptGuidelines: string[]`を書くと、そのtoolを選んだ親・子の
モデル共通指示へ自動で加わる。toolが無効ならその指示は入らない。
次runの編集反映、子のtools上書き、保存snapshotと実requestの一致を確認する。
今後の外部toolは、このfieldを宣言するだけで同じ経路を使える。

既存のrun_jsの英文目的コメント・QuickJSの使い方・利用可能な子API・待機案内と、
子agent各操作の使い方を移す。モデルへ渡す機能説明・引数schemaは継続する。

## 現行経路と責務

CLI/Core/TUI→app.Run/子prepareChild→extensions.CaptureWithOptions→role.tools解決→
assembleInstruction→実行情報/AGENTS/skills/lifecycle追加文脈→budget/実provider request。
現在はRunJSDefinitionForToolsのdescriptionとassembleInstructionのtool名分岐に
利用指示が分散している。任意の外部Toolには利用指示fieldがない。
保存はconfiguration snapshot、config source archiveと実model requestのcontext attributionを使う。

Toolの`promptGuidelines`は宣言データ、collectorは選択済みtoolsだけの指示を集める責務とする。
role.toolsおよび子の明示tools上書きを解決した後、選択順・配列順で収集する。
省略・空配列は指示なし。長さ上限、拒否条件、未知tool名の方針辞書、fallbackを追加しない。

## 実装方針

1. 外部Toolと保存tool snapshotへoptional `promptGuidelines: string[]`を追加する。
   一つの`Active tool guidelines`欄へまとめ、generated instruction componentとして保存する。
   providerのtool definitionはname/description/inputSchemaを維持し、専用fieldをwireへ追加しない。
2. run_jsはbuilt-in tool宣言として基本利用指示を持つ。
   選択済みの子toolに対応するJS APIと、command選択時だけのsleep案内を同じ宣言へ組み込む。
   子のspawn/status/steer/collect/cancel/run操作は外部tool側のguidelinesへ移す。
   assembleInstructionの個別tool利用指示分岐を撤去し、collectorを使う。
3. roleの一般的な作業・途中報告指示と、選択可能な子role catalogは維持する。
   無効なagent_status等を一般指示に含めない。特定toolの利用方針とは分けて扱う。
4. 利用workspaceの標準子toolにもguidelinesを反映する。
   元のrole/provider/model/credential設定や独自tool定義を置き換えない。
   source捕捉は次Workerで行い、実行中Workerへのhot reloadや既存DB変換は行わない。

## 段階と確認

| 段階 | 内容 | 確認する利用動作 |
| --- | --- | --- |
| S1 | field・collector・保存component | 任意toolの指示が選択時だけ入り、順序・source・snapshotを保持する |
| S2 | run_js・子操作の移行 | 有効なJS APIと既存の待機/コメント案内を保持し、無効tool・空toolsの指示を入れない |
| S3 | 共通実行・実利用・review | 親子の実request、tools上書き、次run編集反映、CLI/Core/TUIでの利用と保存照合 |

実装中はextensionsと共通appのfocused test、format/vet/diff checkを使う。
指示の生成・保存・親子選択経路の独立reviewを行い、安定候補で製品packageのrace/vetを一回確認する。
独立workspace/Coreと既存認証の無料モデルで、任意toolの指示と親子操作を実行・照合する。
有料呼出し、利用中Coreの停止、Session/元履歴の変更、commit/pushは今回の実装指示に含めない。

専用hook・skill tool、要約checkpoint、tool管理CLI、旧DB migrationは今回の対象外。

## 実施状態

S1〜S3完了。focused確認、独立reviewと再確認、製品31 packageのrace/vet、
無料モデルのCLI/Core/親子/TUI実利用を確認した。
モデルがJS手順やsuffixを誤った初回試行と、手順具体化後の完了証拠は
[完了結果](../results/i21-tool-guidelines.md)へ記録した。両binary再build済み、未commit。
