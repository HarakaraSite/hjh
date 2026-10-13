# TUIのtool包含表示

2026-10-12。利用者がrun_jsと内部toolの行を二重実行と見分けにくいと指摘し、
字下げによる包含表示を選択した。

Coreが既に渡しているParentEntityIDを使い、内部toolへ二文字分の字下げと`↳`を加えた。
直接呼んだtoolは従来の行、同じrun_jsから並列に呼んだtoolは同じ深さで複数表示する。
全表示entityから深さを一度計算し、既にscrollbackへ出した親も参照する。
通常進捗と履歴再表示の二つの描画入口は同じ計算・行生成を使う。
開始順、tool状態の既存表示、結果全文の省略、子agent詳細の省略、モデル入力、履歴保存は維持する。

```text
tool / read_file · alpha.txt
tool / run_js · Read both files concurrently
  ↳ tool / read_file · alpha.txt
  ↳ tool / read_file · beta.txt
```

既存TUI package test、`go vet ./internal/tui`、`git diff --check`を確認した。
`dist/hjh-tui`を再buildし、実TUIを独立tmuxと独立Coreで確認した。

- 利用者Session `b5e9515a`はread-only SQLite backupで複製し、run_js→commandと
  run_js→read_fileの包含表示と幅変更を確認した。元DB・利用中Coreは変更していない。
- 無料モデル`nvidia/nemotron-3.5-lightning:free`で直接read_fileとrun_js内の
  Promise.allによる二つのread_fileを実行した。直接行の字下げなし、並列二行の字下げ、
  通常進捗の重複なし、再接続時の完了状態と階層、Core viewのParentEntityIDを照合した。
- 独立Session `01b2819d`、execution `2ae0e27d`。
  証拠・capture・検証scriptはignored `dist/tool-nesting-verification/`。

変更はTUI表示側だけであり、既存Coreの再起動は不要。新TUIを起動して反映する。
