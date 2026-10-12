# TUI headerの改善結果

2026-10-12。利用者指定のheader改善を実装した。[操作契約](../design/tui-header.md)を参照する。

## 変更

製品名・version、Sessionタイトル・保存作成日時、Core／Session短縮ID、workspaceの枠付きheaderを追加した。
agent／runtimeは表示しない。狭い画面では2行に縮め、日本語・絵文字・省略をterminal cell幅で扱う。
表示責務はinternal/tui/header.goにまとめた。保存・Coreの状態所有は増やしていない。

headerは開いた会話の情報を一度だけ出し、Session作成・rename・resizeでは追加しない。
現在のSession IDとタイトルはfooterへ出す。長いタイトルでも短縮IDを先に表示する。
保存Sessionは読込み後にheaderを出し、/newと別Sessionへの切替では新しいheaderを一度出す。
新規の未作成状態はnewと表示し、Sessionの作成時期は最初の依頼のまま維持した。

## 確認

focused testで新規opening→Session確定、rename、resizeのheader重複防止とfooter更新、/newでの
別opening、保存title／日時／短縮ID、除外項目、日本語・絵文字の幅、compact表示、長いtitleでも
IDが見えることを確認した。既存TUI test、TUI／TUI CLIのrace、対象vet、format、diff確認も成功した。

private Core／DB／workspace／XDG／tmuxで保存Sessionを短縮IDから開き、保存日時・Core ID・Session IDを確認した。
名前変更・resizeでheaderが増えず、footerに現在の名前とIDが出ることを確認した。
/new後の実モデルによるread_fileとHEADER_OK、Sessionの作成とfooterのID更新も確認した。
既存OpenRouter credentialと、直前の公式catalogで単価0を確認したpoolside/laguna-s-2.1:freeを使った。
利用者のDB／Core／tmux mainは操作していない。

実端末でcompact workspaceの末尾が失われる問題を観測した。TruncateLeftの引数は残す幅ではなく
削除する幅であるため、実幅の差分を渡すよう修正し、観測した長いpathをfocused確認へ追加した。
確認scriptは回答本文の到着だけではCore終了保存を保証しないため、保存turnとactive run終了を待つ手順にした。
また短い会話で末尾に空行が残る端末captureは空行を除いて現在footerを照合するよう修正した。

修正後の最終静的TUIで、保存された新Sessionの作成日時とHEADER_OK、rename済みSessionの
最新タイトル、compactのworkspace末尾、/newの一度だけのopeningを再確認した。
headerが会話ごとに一度、Core／Sessionが短縮表示、除外項目なし、現在情報はfooterという動作が成立した。
実Sessionの作成時期と、保存title／CreatedAtはCore側の既存契約を使っている。

最終TUIのstatic ELF、--versionの実hash一致、表示だけでDB／stateを作らないことを確認し、
dist/hjh-tuiへ反映した。headerはTUI側の変更なので、Ctrl-Dで抜けてTUIを起動し直すと反映される。
header単独改修では既存Coreの再起動は不要。この確認時点では短縮IDとheaderの変更は未commitだった。
後続I-18の機能を使う場合はCoreとTUIの両方を更新する。
