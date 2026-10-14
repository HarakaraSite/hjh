# 最新Sessionの再開

2026-10-14。比較表28について利用者が実装を指定した。

`hjh-tui --continue`は同じworkspaceの最新Sessionを再開する。
`/resume latest`はTUI内から同じ操作を行う。保存会話を読み、続きを送れる状態にする。
実行中なら既存のactive runへattachする。操作自体で新しいモデル生成は始めない。
最新の定義は`history --latest`と共有し、workspaceで絞ったupdatedAt降順、同値ならID昇順とする。
workspace省略は接続Coreのworkspace、明示の相対workspaceはCore側での相対path。
TUI内では現在のSessionのworkspaceを使用する。

`--session`と`--continue`は同時指定しない。最新Sessionがない場合、起動時は新規会話へ入り、
`/resume latest`では案内を表示して現在のSession・実行・draftを保持する。
ID指定、短縮ID、Session picker、historyの読取りだけの動作は継続する。

現行の起動経路はCLI flag→TUI.Run→Core/Sessions/ResolveSession→model.Init→
loadSession→SessionViewとActiveSessionRun→attachRun。
TUI内はhandleCommand→resolveResume→sessionResolvedMsg→changeSession→同じloadSessionを使う。
最新の選択だけを既存経路の手前に追加し、会話所有・実行開始・履歴保存・APIを重複実装しない。
最新の順序とworkspace選択はsessionidの本文やDBに依存しないhelperへ集約し、historyも使用する。
起動時は取得済みSessions、TUI内では操作時にSessionsを読んで選ぶ。
既存のresume世代管理で遅延結果と現在Session自身への切替を扱う。

確認は起動flag、workspaceの絞込みと最終更新による選択、既存再開/attachへの接続、
履歴なし時の動作、help・候補・比較表の更新を対象とする。
一時Core/DB、ローカルモデル応答とPTYで、実binaryによる再開と次の依頼を確認する。
常用DBの変更、有料モデルAPI呼出し、Core停止は今回行わない。
