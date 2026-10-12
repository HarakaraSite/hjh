# I-18：AGENTS.mdとPi方式skillsの実装・確認

2026-10-12。[採用計画](../plans/i18-workspace-instructions-skills.md)と
[読込み・表示契約](../design/workspace-instructions-skills.md)に沿って実装した。

## 実装

workspacecontextがAGENTS.mdの実bytes・出典・digestとskill metadataを探索する。
共通appが各親ターン・再開・子開始時に読み、指示・一覧とactual metadataを同じ結果から作る。
AGENTS.mdは実行内で固定する。skill本文はカタログに保持せず、利用時のread_fileでdiskから読む。
直接tool結果または外側run_js結果に本文を含めて、モデル会話へ渡す。

既存のconfiguration/context_item/model_request/resource_revision/contentsとtool_resultを使い、
履歴schema、予算管理、QuickJSの実行経路は変更していない。
config directory全体の既存captureとskill配置が重なる場合のsource archiveは維持し、
archive bytesをskill本文のモデル提示へ使わない。

Coreのorientation APIはmetadataだけを返し、Sessionや拡張を作成せず、モデルも呼ばない。
RunState.ActualContextは開始時の実使用情報を保持し、attach/reconnectでも取得できる。
TUI通常幅headerへbase/context/skillsを追加し、previewとactualを区別する。
狭幅は2行を維持し、新規Sessionでも/contextで出典・一覧を確認できる。
agent/runtimeのheader除外、短縮ID、一度header、現在Sessionのfooter表示を維持した。

## 動作確認とreview

focused確認では、AGENTSの順序・exact bytes・sha256 digest、skill優先順・YAML・symlinkと重複、
本文の非保持、readerのないrole、previewの非書込みを確認した。
実Worker/host/SQLiteを通して直接read_fileと生成JSの本文返却、
実行中の指示固定、利用時本文の編集反映、次ターンと子の再capture、旧sourceの保存を確認した。
実wireとbudget factも同じ記録から照合した。

TUIでは初回入力中の遅延preview/actual先着、headerと会話scrollbackの順、
Session切替と古い応答の無視、新規/context、active attachをfocused/raceで確認した。
初回Session ID割当後にprovider準備が失敗すると、古いpreviewを無視したまま待機が残る経路を
実装中に確認し、現在Session向けのpreview再取得とfocused regressionで修正した。

通常実装reviewは探索・app・API・保存・予算を確認し、修正必須findingなし。
批判的実装reviewはTUI境界を重点にP2一件を採用した。
準備中runへのattachでactualが未発行だとpreviewがheaderを先に確定するため、
attached非terminalではactualを待ち、actualなしterminal時だけpreviewへ移るよう修正した。
一回の限定再reviewで解消と追加findingなしを確認した。

安定候補の製品全体確認は以下が成功した。

```sh
go test -race ./cmd/... ./internal/...
```

初回の`go test -race ./...`には、以前のignoredなdist/i06-live/user-render-diagnosisの
単独test artifactが含まれ、helper不在でcompileに失敗した。その実行でも製品packageは成功した。
既存artifactを削除せず、製品sourceのcmd/internalを全体gateの対象として指定し直した。
変更箇所のvet、format、diff checkも成功した。

## 実Core・CLI・TUI・モデル

private workspace/DB/Core/HOME/XDG/tmuxと既存OpenRouter credential参照を使った。
実行直前の公式catalogで無料とtool対応を確認したnvidia/nemotron-3.5-lightning:freeで、
以下が正常完了した。固定JSをモデルへ渡していない。

1. Session未作成のorientationと狭幅/contextで一覧を確認。Sessionを作らないことを照合。
2. TUIから通常のレポート作成を依頼し、モデルがSKILL.mdと相対referenceを読み、
   日本語・絵文字を含むrecipeの行をoutput.txtへ保存し、読戻してI18_ROOT_OKを回答。
3. 保存Sessionを短縮IDでTUIへ開き直し、会話・出典・skill名と一度headerを確認。
4. AGENTS.mdとskill/referenceを編集し、同じSessionへstandalone CLIで依頼。
   新しい指示と本文で成果物を更新し、正常保存。
5. Core接続CLIからreviewer子へ委譲し、子が現在のskill本文とreference・成果物を読み、
   I18_CHILD_OKで検証完了。親の正常終了保存まで確認。

SQLiteの実configuration/model_requestを照合し、初回requestにはskill本文がなく、
読取り後のrequestに本文があること、新しいAGENTSの適用と子の本文取得を確認した。
private Sessionは9b8770aa、親3executionと子1executionを保存した。
詳細はignoredなdist/i18-context/live-proof.json、configurations.json、wire-evidence.json、
terminal capturesに保持する。

初回poolside/laguna-s-2.1:freeは本文/referenceを読んで成果物を作った後、上流の共有pool制限429で
runに失敗した。成功扱いにせずprivate履歴を保持し、別の無料モデルで上記を確認した。
確認scriptの狭幅markerとhistory commandを実際の表示・CLI契約に合わせた。
レポートはrecipeの各行の一致を確認し、モデルが省略した末尾改行を機能の失敗条件にしない。
API credential値とAuthorizationは出力・履歴へ露出させていない。
利用者の稼働Core・DB・tmux mainは操作していない。

## 最終配布物

Linux/amd64・CGO_ENABLED=0・GOAMD64=v1のCore/TUIをpackageした。
140fileのsize/hash、静的ELF、--versionと実binary hash、YAML parserのlicenseを照合した。
Go/Node/Deno等をPATHへ置かず、最終packageでCoreを起動し、保存SessionをTUIとCLI historyから閲覧した。
--versionは空workspace/HOME/XDGへstateを作らないことを確認した。

| 対象 | bytes | SHA-256 |
| --- | --- | --- |
| hjh | 48,877,594 | 0b92adbc4491516eb88ac6b4ae27d3f6a53043fd3983cf37a64780979969ac44 |
| hjh-tui | 14,672,520 | d42c16ccfe2332cb259741245ea62326e4e2c4423d67e0cdaf7cc1b391cfbbc1 |
| archive | 32,609,454 | d984baa6451ab8008702a046caecd85c20b328b169fb5903c7747a78d93a3a4e |

archiveはdist/packages/i18-final/build-wrxawv4a/hjh-linux-amd64.tar.gz。
package-proof.jsonに照合結果を保存し、同じbinaryをdist/hjh・dist/hjh-tuiへ反映した。
この機能はCoreとTUIの両方を更新するため、利用には両processの再起動が必要。
この確認はcommit前の候補で行った。commit/pushの現在状態はGitを参照する。
