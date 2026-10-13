# Slash command候補

2026-10-14。機能比較33について、利用者がPi・OpenCodeを参考にする実装を指定した。

## 利用動作

入力欄の先頭で`/`を入力すると、コマンド名・引数の案内・短い説明を候補として表示する。
文字入力・削除・単行pasteでコマンド名を絞り込み、上下で選ぶ。
完全一致・前方一致を先にし、コマンド名の順序を保つ文字一致でも検索できる。
全候補は画面高に合わせた表示範囲をスクロールして選べる。

Tabは選択候補を`/command `として補完する。Enterは引数なしで使えるコマンドを実行する。
引数必須のresume・rename・tool・inspectは、Enterでも名前だけを補完し、引数入力を待つ。
任意引数のmodel・provider・loginなどで直接指定したいときはTabで補完して引数を入力できる。
Escは候補だけを閉じ、入力を保持する。再編集またはTabで候補を開き直せる。
引数入力中・複数行の依頼・通常の文章ではコマンド候補を出さない。
候補がない文字列は従来の入力として扱い、入力内容を勝手に変えない。

表示は通常画面の入力欄の上に置き、会話のscrollbackには保存しない。
既存のフッターと入力cursorは下に固定し、Session/model/authのpickerでは候補を表示しない。
実行中でもコマンド入力を使え、候補操作でモデル生成や子agentを始めない。

## 参考にした動き

- [Pi editor](https://github.com/badlogic/pi-mono/blob/main/packages/tui/src/components/editor.ts)と
  [autocomplete](https://github.com/badlogic/pi-mono/blob/main/packages/tui/src/autocomplete.ts)：
  自動表示・名前検索・説明/引数hint・上下/Esc、Tab補完、slash選択後のEnter送信。
- [OpenCode prompt autocomplete](https://github.com/anomalyco/opencode/blob/dev/packages/tui/src/component/prompt/autocomplete.tsx)：
  入力付近の候補、画面高に応じた表示、文字検索、組込みcommandの直接実行と引数入力の分離。

旧Henji内`_refs/pi`と`_refs/opencode`の同名sourceも確認した。
OpenCodeのTabによる組込みcommandの直接実行は採用せず、hjhはTabを補完操作に揃える。
skill・file・model名の引数補完、独自slash command登録、履歴検索は今回の対象外。

## 現行経路と責務

現在の入力はmodel.handleKey/PasteMsg→textarea→submitDraft→handleCommandの経路を使う。
commandNamesとhelpTextは別々に定義され、Tabは候補名をstatusに出すだけである。
screenFrameが本文・入力・フッターを描き、rendererのDockRowsが入力位置を維持する。

名前・引数hint・説明・引数必須の宣言を一つのTUI catalogへまとめ、認識・候補・helpで共用する。
候補の選択とEscの非表示状態はTUIが所有し、入力編集時に更新する。
screenFrameは候補を未確定のdock行として描く。候補のためのCore API・DB・別overlayは追加しない。
既存Tab補完の状態と処理はこの候補操作へ置換する。

## 確認範囲

/入力、名前検索・paste・削除、上下、TabとEnter、引数必須操作、Escと再表示、
引数/複数行での非表示を、実入力event→command dispatchの経路で確認する。
候補の表示/非表示・画面幅変更・run進行で、cursorとフッター位置、確定本文の保存を確認する。
実binary・一時Core・PTYで候補→effort picker→保存、引数補完、キャンセルを確認する。
有料モデル生成や常用Coreへの書込みは今回行わない。
