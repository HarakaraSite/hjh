# TUI footerの変更・確認

2026-10-12。[表示契約](../design/tui-footer.md)に従って旧Henjiの3行形式へ変更した。
操作案内、状態・workspace・タイトル・短縮ID、provider / model / effortを表示する。
effortはmodelの直後へ並べ、独立したconnection／role／statusの常設行を廃止した。
旧表示だけに使われたmodel label・footer helper・状態も削除した。

次回の接続は既存GET /selectionで読み、現在runのactual selectionと区別する。
次回が異なるときだけ同じ行に添え、表示幅を双方へ配分する。
時計更新はrunごとに開始・停止し、既存の会話cacheとinline writerを使う。
CoreのstartedAtを通常runとfollow-upに追加し、reattachで開始時刻を保つ。
履歴schema、credential、モデル実行、会話採用の契約は変更していない。

関連packageの確認が成功した。

```sh
go test -race ./internal/tui ./internal/corerun ./internal/api ./internal/apiclient ./internal/httpapi ./cmd/hjh-tui
go vet ./internal/tui ./internal/corerun ./internal/api ./internal/apiclient ./internal/httpapi ./cmd/hjh-tui
git diff --check
```

表示、次回選択変更、現在runの保持、古いpreview応答の無視、経過時間、時計停止、
狭幅のID／effort保持、editor cursor、headerの一度表示、会話の確定・resizeを確認した。
既存testの旧footer行数・旧Session表記を新契約へ更新した。
非同期の表示読取が操作応答より先に返る場合は、testで応答種別を待ち、順序を仮定しない。
Coreの通常run／follow-upとterminal後の購読で開始時刻の保持も確認した。

実確認はprivate workspace／Core／DB／HOME／XDG／tmuxで行った。
利用者DBはread-only接続からprivate DBへbackupし、b71a29a6を復元した。
新規・保存Session、50列の画面、/effort high・auto、model pickerの操作案内を確認した。
公式catalogで入力・出力の単価が0であることを確認した
nvidia/nemotron-3.5-lightning:freeをTUIから実行し、working 00:01とF1/F2/F3表示、
実行中のdetach→reattachで同じCore開始時刻、正常完了・保存・readyへの復帰を確認した。
確認用のproofと端末captureはignoredなdist/footer-checkに残した。
利用者のCore、DB、tmux mainは変更せず、確認用processは終了した。

dist/hjhとdist/hjh-tuiをCGO_ENABLED=0・trimpathで更新した。
反映にはCoreとTUIの再起動が必要。色付けは従来方針に従って後回しとした。

## 2026-10-16：区切り線へ状態・Session・ctxを集約

利用者指定により、操作案内の上の区切り線へ状態を左寄せ、タイトルとSession短縮IDを
中央、ctxを右寄せで表示した。メタデータ間は横線でつなぎ、タイトルは端末cell幅で省略する。
左右の表示へ重なる場合だけ中央位置を調整する。狭幅では状態・短縮IDを優先する。
入力欄の下はworkspace、provider / model / effortの2行とし、移動した情報を重複表示しない。
操作案内・通知、上下の背景付き余白、本文高さ・dock・カーソル計算の行数は維持した。
状態・Session情報・ctxの取得元や更新経路、保存契約は変更していない。

確認：次のfocused testと差分checkが成功した。

```sh
go test ./internal/tui -run 'Test(Footer|Theme|Header|SavedHeader|ContextDetails|ConversationTheme|Editor|RealEditorCursor|JapaneseEditor|SlashMenuDock|InlinePicker|AuthenticationOverlay|PickersShare|ResizeCursor|APIKeyInputIsMasked|ChatGPTCallbackUsesMasked)' -count=1
git diff --check
```

表示幅・中央揃え・長いタイトル・狭幅省略・ctx形式とSession復元・actual/next selection、
生成進行・picker復帰・入力欄の余白とカーソルを確認した。差分レビューで追加findingはなかった。

PTYでは実際のRunをfake Backendと100×24のpyte端末で動かし、ready、workingのspinner・
00:00から00:01への経過時間、完了後のready復帰、中央のタイトル・IDと右端のctx 24%を確認した。
日本語複数行・背景色237・F4→Esc復帰・Alt+Enter・Ctrl+Dも確認した。
証跡は一時領域 `/tmp/hjh-footer-pty-ha1251iv/` に保持する。
fake生成は1件、Core停止呼出しは0件。実プロバイダ、実保存DB、物理端末の目視は未確認。
実装確認時点では稼働Coreの停止・常用バイナリ更新・コミット・プッシュは行っていない。
後続の利用者指示で、本変更のコミットと`dist/hjh`・`dist/hjh-tui`の更新を行う。
今回の表示変更はTUIの再起動で反映され、Coreの再起動は不要。プッシュは行わない。
