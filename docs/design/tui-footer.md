# TUI footer

2026-10-12。利用者指定で旧Henjiの3行形式を採用する。
effortは右端へ分離せず、`openrouter / model-name / high`のようにmodelの直後へ並べる。

操作案内・通知の行は区切り線の下、入力欄の上へ表示する。
状態・workspace／Sessionとprovider／model／effortの2行は入力欄の下へ表示する。
これらはlive bandだけに属し、会話へ保存・確定出力しない。
入力欄とfooterは画面下部へ固定する。生成中本文の伸長、確定、次の生成開始によって
表示位置を上下させない。上の空き行はwriterが調整し、本文のreceiptやscrollbackへ加えない。
確定会話は通常画面のscrollbackへ一度だけ出力する。
resizeでは端末のcursor position reportから既存live bandの位置を確認して描き直し、
応答待ちの間は表示済みreceiptを進めない。clear・移動・描画はsynchronized outputで囲む。

1. 現在使えるキー操作と通知。待機中はEnter send、実行中はF1 cancel、受付可能な期間は
   F2 queue／F3 steer。Session・接続・認証の選択画面では操作案内を切り替える。
2. 左に状態・workspace、右にタイトル・Session短縮ID。実行中はスピナーと経過時間。
   狭幅ではタイトル・workspaceを省略し、状態とIDを優先する。
3. provider / model / effortを左から連続表示する。長いmodelはeffortの幅を確保して省略する。

次回の実効接続は既存CoreのGET /selectionで取得する。TUIに設定解決規則を複製しない。
初期表示を待って入力を止めず、取得中はresolving selection、失敗時はselection unavailable。
Session・workspace・role・provider・model・effort・authの変更では再取得し、古い応答を適用しない。
実行中は親runのactual selectionを表示し、次回のprovider／model／effortが違う場合だけ
同じ行へ`→ next: ...`を添える。子runの情報で置き換えない。
roleの常時表示は廃止し、既存の/role・/rolesで確認・指定する。

経過時間はCoreのRunState.startedAtから計算し、再接続でも同じ開始時刻を使う。
TUIの時計更新は表示だけを更新し、モデル呼出し・会話保存・history生成へ処理を戻さない。
操作・情報の行はterminal cell幅で1行へ収め、狭幅でも折返しで会話領域を消費しない。
配色は[Markdown整形・色契約](tui-rendering.md)に従う。workspace・短縮ID・provider・effortは通常表示。
