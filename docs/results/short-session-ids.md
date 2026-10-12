# Session IDの人向け表示と指定

2026-10-12。利用者指定により、TUI・CLIの人向けSession IDを原則8文字へ統一した。
[操作契約](../design/session-ids.md)を参照する。

## 変更

共通sessionidが短縮表示と一意prefixの解決を提供する。既知の衝突は区別できるまで延長し、
曖昧な指定では短縮候補を表示する。CLIのlocal入力とapiclientの人向け入力で完全IDへ戻してから
既存のapp／APIへ渡す。run、history、TUI --sessionと/resumeが同じ方式を使う。
TUI --sessionは起動時に解決してからstateへ設定し、/resumeも解決後に現在Sessionと比較する。
自身のSessionを短縮指定した場合は現在runを停止しない。

run進捗、historyの見出し・canonical Markdown、sessions一覧、Core statusのSession表示、
TUIのheader・picker・query見出し・context・detach案内を短縮した。
sessionsは人向け一覧が既定、--jsonは完全IDの従来の構造化一覧。
内部state、API、DB、inspect JSON、history JSON/detail NDJSON、保存本文は変えていない。

短縮ID統一時にはheader全体の枠・項目・出力回数を変更していなかった。
その後の利用者指示による[header改善](tui-header.md)で、agent／runtimeを除外した表示を整備した。

## 確認とreview

CLIの実SQLite／接続Core確認でlocal/connected再開、history、sessionsの人向け表示とJSONを確認。
TUIのfocused確認では短縮指定後の完全ID所有、自Session再指定時のrun維持、表示、detach案内を確認。
clientの実HTTP確認では延長したprefixをcase-insensitiveで解決し、完全IDのpathを使うこと、
曖昧な8文字で別Sessionを選ばないことを確認した。

独立correctness reviewでasync /resumeの順序、lookup中のdraft送信、picker cacheについて
P2を2件、P3を1件採用した。最新lookupだけを適用し、新しい切替要求で古いlookupを無効化する。
lookup待ちはdraftを保持して送信を保留し、成功／失敗／invalidateで解除する。
pickerを閉じても既知のprefix衝突を保持する。
一回の限定再reviewで順序問題の残存経路が見つかったため、最新lookup開始時に旧pending actionも
無効化した。rootが対応sourceと、親runのsettlementを跨ぐfocused regressionを最終確認した。
古い解決結果、/new、解決失敗後の入力復帰、親終了前後の最新要求を実goroutineの遅延resolverで確認した。

Session切替testは、新しいasync解決を処理してから切替を確認するようにした。
同testの既知の不安定なmessage順序仮定も、対象eventまで処理する確認へ修正した。
Sessionの終端会話を待つ製品動作は変更していない。

関連6packageのrace付きtest、対象vet、format、git diff --checkが成功した。
限定再review後の最後の修正についてはTUIの関連focused raceを追加実行し成功した。

## 実CLI・TUI

private Core／DB／workspace／XDG state／tmuxを使い、保存済みSessionの短縮IDから
local runとCore接続runの両方で続行した。既存OpenRouter credentialと、直前の公式catalogで
単価0を確認したpoolside/laguna-s-2.1:freeを使った。
read_fileで既存output.txtを読み、I18_CONNECTED_OK／I18_STANDALONE_OKを返した。
同じ完全IDのSessionへ2turnが追加され、Session件数は増えなかった。

初回の確認用scriptは、再開前のscrollbackの回答markerを拾い、/contextを再開完了前に送った。
再確認の準備で確認用DBを再copyしてしまったため、copyは初回だけに修正し、private履歴と
実モデル続行を再確認した。利用者のDB／稼働Core／tmux mainは変更していない。
画面確認は現在のfooterにあるresumed状態を待ってから次の操作を送る方式へ修正した。

画面確認scriptのroot/child inspectionは、rootの完全Session IDを照合するように修正した。
子executionの独立したSession IDをrootと同一とみなす確認は行わない。

最終の静的CLI／TUIで、保存Sessionへの短縮--session接続、/new→/resume、/context、/sessions、
Ctrl-Dによるdetach後のCore継続を確認した。現在footerのresumed状態を待つ手順で全操作が成功した。
同じroot Sessionの5turn保存、sessions --jsonとAPI inspectionで完全ID保持を照合した。
既存CoreのprotocolやSessionの作成時期は変更していない。

CGO_ENABLED=0、Linux/amd64、GOAMD64=v1でbuildし、両binaryのstatic ELF、--versionの実hashと
source metadata、表示だけでDB／stateを作らないことを確認した。確認済みペアをdist/hjhとdist/hjh-tuiへ反映した。
source revisionはee4cc5cのcommit、dirty=trueのlocal buildである。
commit／pushは未実施。CLI／TUI側の変更のため既存Coreの再起動は不要で、TUIを再起動すると反映される。
