# run_jsの英文目的コメント

2026-10-12。旧Henjiのrun_typescriptを参考に、利用者指定でrun_jsへ実装した。

モデル向けbuilt-in tool descriptionと、run_jsが選択されたroleの共通instructionへ、
先頭に目的を短い英文の1行`//`コメントで書く指示を追加した。
toolactivityが最初の空行以外の行からコメント本文を抽出し、既存のpreviewへ渡す。
TUIのtool行では名前の後へ薄い通常色で表示する。
JSの評価、引数・保存原文、tool definition schemaは変更していない。
コメントがなければ説明なしのtool名を表示する。既存保存データは変更していない。

focused／raceでは、英文コメント表示、コードや途中コメントを表示しない動作、
default・reviewerのモデル向け指示、実Worker・journal・SQLiteを通したlive/replay一致を確認した。
関連moduleのvetとgit diff --checkも成功した。

実確認はprivate Core/TUI・workspace・DB・HOME・XDG・tmuxで行った。
公式catalogで入力・出力単価0を確認したnvidia/nemotron-3.5-lightning:freeへ、
run_jsでREADME.mdとsettings.jsonを読んでJSONにまとめる依頼を送った。
モデルが生成した先頭コメントは次の英文で、実行中・保存Sessionの再表示で同じ説明が出た。

```text
Read README.md and settings.json, combine into a JSON object
```

両ファイルの実read_fileと正常終了を確認し、保存model_requestにも英文コメントの指示が
含まれることを照合した。capture・ANSI capture・proofはignoredなdist/run-js-commentに残した。
確認用Coreとtmuxは終了した。利用者のCore・DB・tmux mainは変更していない。

dist/hjhをCGO_ENABLED=0・trimpathで更新した。
新しいモデル向け指示とpreviewの反映にはCoreの再起動が必要。
