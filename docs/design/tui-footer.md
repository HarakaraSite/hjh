# TUI footer

2026-10-12。利用者指定で旧Henjiの3行形式を採用する。
effortは右端へ分離せず、`openrouter / model-name / high`のようにmodelの直後へ並べる。

操作案内・通知の行は区切り線の下、入力欄の上へ表示する。
状態・workspace／Sessionとprovider／model／effortの2行は入力欄の下へ表示する。
これらはlive bandだけに属し、会話へ保存・確定出力しない。
Slash command候補を開いている間は上下・Enter・Tab・Escの案内へ切り替える。
候補は区切り線の上に置き、入力欄と情報footerの下部固定を維持する。
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

## コンテキスト使用率

モデル表示行の右端へ、直近の通常の親モデル入力の使用率を右寄せする。
表示は`ctx 24%`で、分子は指示・tool定義・履歴・現在turnを含む入力のhjh推定tokens、
分母はCoreが解決した有効入力予算Limit。整数%に切り捨て、provider実測usageは混ぜない。
未送信draftや、これから生成される応答の量は含まない。

- Limitがhook policy由来なら`ctx 38%*`。`*`はモデルの容量ではなくhook予算が基準である印。
  `/context`に由来と凡例を表示する。80万の既定予算もこの扱い。
- Limitが不明なら`ctx 3.0k tok`のように推定量だけを表示する。
- 計測がなければ断片を表示しない。狭幅ではselectionの幅を優先して断片を省略する。
- 通常モデル入力の確定時に既存run eventで更新し、待機中も直近の値を維持する。
  予算超過で送信前に停止した場合は、その入力準備時点の値となる。
- Session再開時は既存SessionContext.LatestBudgetで復元し、新規Session・切替時は古い値をclearする。
  モデル変更だけでは値を換算し直さず、次のモデル入力の準備時に更新する。
- 子agentや要約専用呼出しの入力は表示対象にしない。コンパクション後は置換後の親入力を表示する。

既存3行とlive bandの構成を維持し、表示用の保存記録・モデル呼出しは追加しない。
[計画](../plans/context-usage-display.md)と[実装・確認結果](../results/context-usage-display.md)を参照する。

## 改善メモ：前ターンのsteering状態が残る

2026-10-15、利用者報告。TUIの`steering · applied`表示が、次のターンに進んでも残っている。
過去の追加指示の適用状態が現在の状態表示へ残り続けないようにしたい。
対応時は表示元とrun/turnの切替経路を確認し、前ターンの状態を引き継がない表示へ整理する。
適用済み指示の本文や保存receiptは維持し、表示だけの改善として扱う。
現時点は報告のメモのみで、原因の特定・実装修正は行っていない。
