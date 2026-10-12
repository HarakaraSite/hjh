# Glamourと配色の実装・確認

2026-10-12。[Markdown整形・配色契約](../design/tui-rendering.md)を実装した。

Glamour v2.0.1をassistant本文へ導入し、見出し・強調・箇条書き・リンク・表・codeを
生成中／最終回答／保存Sessionの同じ経路で描画する。
ユーザー入力・thinking・tool・通知・失敗理由は原文表示に色を付ける。
workspace・短縮ID・provider・model・effortは通常表示を維持する。
thinkingラベルは黄系dim、本文は通常色dim。tool previewと詳細は通常色dim。
コマンド結果は紫系dim、実行失敗と失敗toolの印は赤。

途中のassistant本文も最終回答と同じGlamour経路・配色になることを確認した。
後続の利用者指定で、Markdown内の空行もSolarized Darkの本文背景で端末幅まで塗るようにした。
改行数と末尾のline terminatorを維持し、source receiptや会話の区切りを変えていない。
既存のMarkdown／streaming／復元のfocused race、vet、diff checkが成功し、
private TUIで保存回答を開いてANSI capture上の空行背景を確認した。

後続の利用者指定でMarkdown本文をSolarized Dark固定へ変更した。
Glamourのcustom styleに公式paletteを設定し、コードはChromaのsolarized-darkを使う。
周囲のUI配色と、生成中・履歴復元・source receiptの経路は共通のまま。
関連するMarkdown・theme・streamingのfocused testとvetが成功した。
保存済み実回答をprivate Core/TUIで開き直し、ANSI captureでbase0の文字色、
base03の背景色、Solarized blueの見出しを確認した。追加のモデル実行は行っていない。

GFM source blockとreceiptの原文byte境界を維持し、cacheに幅・本文種別を加えた。
document余白を追加せず、各source blockの末尾改行と同じ区切りで出力する。
row単位で色・hyperlinkを引き継ぎ、末尾を閉じてeditor・footerへの漏れを防ぐ。
Core・保存schema・モデル入力の変更はない。

実確認で、48列の保存Sessionを開く際に初期80列のframeが端末へ流れる問題と、
opening headerを出すframeでは本文をcommitせず、次のeventまで保存会話の末尾だけが
残る問題を確認した。最初にterminalサイズを取得し、metadataが確定したframeで
headerと保存済み確定本文を続けて出す形に修正した。metadata待機中も送信できる。

関連するTUI／writer／CLI入口のfocused test・race・vetとdiff checkを行った。
source保持、GFM表、幅変更のcache、色、IME用cursor、未完了Markdown、
streamingと最終描画の一致、scrollbackの一度出力、長い保存会話の初回commitを確認した。
旧「Markdown記号をそのまま表示する」assertionは利用者の新契約へ更新した。
taskをstreamingにしていたfixtureは、CoreのentityStatusがcompletedを返す実仕様へ合わせた。

実Core/TUIはprivate workspace・DB・HOME・XDG・tmuxで確認した。
利用者のb71a29a6はread-only backupしたDBから再表示した。
公式catalogで入力・出力単価0を確認したnvidia/nemotron-3.5-lightning:freeへ、
README.mdを実際に読む依頼をTUIから送った。read_fileと公開thinkingに続いて、
見出し・強調・箇条書き・表・Go code・リンクを含む回答が正常完了した。
Coreの保存viewがMarkdown記号を保持することを照合した。
同Sessionを48列で開き直し、header順序・本文全体・整形・幅変更を確認した。
/helpの紫系通知と、未定義providerによる準備失敗のrun ended／理由の赤も確認した。
端末capture、ANSI capture、保存回答原文、proofはignoredなdist/glamour-checkに残した。

確認用Coreとtmuxは終了し、利用者のCore・DB・tmux mainは変更していない。
dist/hjh-tuiをCGO_ENABLED=0・trimpathで更新した。Glamour・配色の反映にはTUIの再起動が必要。
Coreの開始時刻と3行footerの変更は[footer結果](tui-footer.md)を参照する。
