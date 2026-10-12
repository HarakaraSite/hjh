# 回答のMarkdown整形と色

2026-10-12。利用者の指定によりGlamourを採用し、旧Henjiを基準に次の配色へ変更する。

assistantの可視本文はcharm.land/glamour/v2 v2.0.1で描画する。
生成中・最終回答・履歴復元は同じ経路を使う。見出し、強調、箇条書き、リンク、表、コードを
端末向けに整形し、利用者の端末幅で折り返す。本文にdocument左右marginを追加しない。
利用者指定で回答本文のテーマをSolarized Darkに固定する。Glamourの組み込みテーマでは
ないため、[公式Solarized palette](https://ethanschoonover.com/solarized/)をcustom styleに設定する。
本文はbase0（#839496）、背景はbase03（#002b36）、見出しはblue（#268bd2）の強調。
リンク・引用・inline codeにも同paletteを使い、code blockはChromaのsolarized-darkを使う。
テーマの選択設定・環境変数は追加しない。
ユーザー入力・thinking・tool・コマンド結果・失敗理由にはMarkdown変換を適用しない。
toolを呼ぶ前の途中のassistant本文も、最終回答と同じMarkdown整形・テーマを使う。
Markdown内の空行は本文と同じ背景色で端末幅まで塗る。末尾改行だけのsentinel行は
新しい空行にしない。原文の改行数・source receipt・会話の区切りを変えない。

| 対象 | 配色 |
| --- | --- |
| ユーザー本文・入力欄 | ANSI 256色237の灰色背景、通常色の本文 |
| ユーザー・assistantのラベル | ANSI黄系 |
| thinkingラベル | ANSI黄系・dim |
| thinking本文 | 通常色・dim |
| tool名・ラベル | ANSI黄系・dim |
| tool previewの引数・パス・ファイル名、/tool・/toolsの本文 | 通常色・dim |
| コマンド結果・システム案内 | ANSI紫系・dim |
| 実行失敗・tool失敗の印 | ANSI赤 |
| ready | ANSI青 |
| working・cancellingなど | ANSI黄系 |
| workspace・Session短縮ID・provider・model・effort | 通常表示 |
| 操作案内 | キー名は通常表示、説明・区切りは通常色・dim |

通知の追加保存や新しい履歴種別は設けない。既存のコマンド結果・失敗entryは会話欄から
端末scrollbackへ流し、読み込み・進捗・選択変更の案内はlive footerへ表示する。
これらのTUI command entryはDBの採用会話には追加しない。

Coreの会話・履歴・モデル入力はMarkdown原文を保持する。
TUIはGFMのsource block境界をreceiptに使い、Glamourの整形後文字数や色制御列で
元のsource offsetを推測しない。確定blockだけをscrollbackへ出力し、未完了末尾をliveで更新する。
Glamourが足すdocument空行は各原文blockの区切りに合わせる。
source blockの描画cacheには幅と本文種別を含め、幅変更では未確定本文を再整形する。
変更のない本文のparse・描画と確定本文の再出力は行わない。

描画rowは色・hyperlinkの状態を次行へ引き継ぎ、行末で閉じる。
live windowの途中行から表示しても色を失わず、editor・footerへ本文の色を持ち越さない。
色・Markdown描画はTUI内だけに置き、Core APIへpresentationの値を追加しない。

最初の描画前に出力terminalの実幅・実高さを取得する。Bubble Teaの後続size通知は
resizeに使う。opening metadata待機中も入力を受け付け、metadataが確定したframeで
headerと保存済み確定本文をまとめて出力する。追加の入力eventを本文表示の条件にしない。

## 将来の改善候補：テーマの差し替え

2026-10-12、利用者要望。Markdown本文のテーマと、Markdown以外の履歴表示のテーマを、
それぞれ独立して差し替えられるようにしたい。

- Markdownテーマ：途中のassistant本文・最終回答のGlamourによる整形と配色。
- Markdown以外の履歴表示テーマ：ユーザー本文、thinking、tool、システム通知、失敗表示の
  ラベル・本文・背景などの配色。

将来の改善候補として記録する。具体的な設定方式・実装時期は未定。
