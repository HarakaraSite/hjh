# TUIコンテキスト使用率表示の実装・確認

2026-10-15。[計画](../plans/context-usage-display.md)。Session `600ce255`の未完了作業を引き継いだ。
スライス1〜3を実装・確認し、利用者の区切り停止指示により一旦停止した。
後続指示でcommit/push・常用配置が許可され、provider実測usage表示は追加しないことを再確認した。

## 現行状態への案内（2026-10-16 source照合）

以下は2026-10-15の初期実装・実測・停止位置の記録。モデル表示行右端・既存3行維持は当時の配置で、
後続変更後のctxは**区切り線の右端**へ移動済み。区切り線の左は状態、中央はタイトル／Session短縮ID、
入力欄下はworkspaceと接続情報の統合1行となる。計測元・配信・復元・表示値の意味は維持する。
現行の配置と狭幅時の省略は[footer契約](../design/tui-footer.md)、後続の確認範囲は
[footer結果](tui-footer.md)を参照する。下記の数値・実provider確認・配置予定を現行環境の再確認へ読み替えない。

## 実装

- Coreが既存のcontext_budgetと同じ計測要約をOnContextBudgetへ渡す。
  RunState.contextBudgetから既存SSEへ配信し、追加のモデル呼出し・DB記録・endpointは作らない。
- childのcallbackは継承せず、要約専用model APIもこの通知を出さない。
- TUIはモデル表示行の右端に`ctx 24%`を表示する。有効入力予算基準のhjh推定値で、
  hook予算では`*`、予算不明では推定tokensだけを示す。待機中も直近の値を保持する。
- Session再開では既存SessionContextからseedし、新規Sessionではclearする。
  モデル変更直後は据置き、次の入力準備で更新する。既存3行・入力欄・会話の配置は維持する。
- `/context`にLimit由来と`*`の凡例を追加した。

引継ぎ時の差分確認で、select→replaceへ解決済みbudgetをそのまま渡すと、未変更項までhook由来へ
変わる問題を修正した。同値のoverrideは元のsourceを維持し、hookがq/Rを変えて決まる
C×q/C−Rもhook由来とする。予算の式・数値は変更していない。

## スライス確認

S1：各stepの通知が保存済みsummaryと一致し、子・要約の要求が親通知へ混入しないこと、
実HTTPのrun eventからclientが要約を受け取れることを確認した。
実compaction.jsでも、既知の予算と不明時のhook予算の由来が維持されることを確認した。

S2：通常%・hook印・予算不明tokens・未計測時非表示、右寄せ・next表示・狭幅、
Session復元・run event更新・newでのclearをfocused確認した。

S3：compiled CLI/Core/TUIと専用tmuxを独立workspaceで動かし、次を確認した。

| 経路 | 観測 | 再接続・復元 |
| --- | --- | --- |
| 実OpenCode Go / DeepSeek V4.1 Flash / auto、入力予算6,000、compaction有効 | 実行中`ctx 58%`、完了時`ctx 68%`。通常モデル要求3回、要約要求1回 | Core/TUI再起動後も`ctx 68%` |
| local HTTP、容量不明、compaction有効 | hook既定80万を使い`ctx 0%*`。`/context`に凡例 | TUI再接続で同値 |
| local HTTP、容量不明、compactionなし | `ctx 296 tok`、%なし | TUI再接続で同値 |

実providerの3回目の入力は、要約前6,348推定tokensから要約後4,119へ減少した。
footerは4,119/6,000の切捨て68%と一致し、要約前や要約専用requestの値を表示しなかった。
最終回答はファイルのAX-314・BY-271を保持した。由来はconfig/defaultsのままで`*`は付かなかった。
初回計測前は断片非表示。通常の`/context`操作とfooterの値も照合した。

試験scriptの最初のSessionContext読取はcoreId未指定で409となった。
scriptを直し、既に完了した実providerの記録を再起動後に読み戻して確認したため、実モデル作業は再実行していない。
常用Core・DB・extensions・distは変更していない。試験用Core・tmuxは終了済み。

証跡：`/tmp/hjh-context-usage-pgi9w8mv/observe.py`、`result.json`、
各scenarioのinitial/completed/context/restored.txt、独立workspaceのDBとmetrics。
数値の抜粋は[集計JSON](context-usage-display-data.json)。

## 最終確認と停止位置

- 関連9 packageのtest成功（contextbudget/app/corerun/httpapi/tui/api/contracts/cmd/hjh/cmd/hjh-tui）。
- app/httpapi/tuiの関連focused race成功。指定filterに該当するcorerun testはなく、
  通常testは上記の関連package確認で実行した。
- 同9 packageのGo 1.27対応staticcheck成功
  （`honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45`）。
- coordinating ownerが差分とCore→TUIの実経路を確認した。独立reviewerは起動していない。
- git diff --check成功。既存の全repository gateは繰り返していない。

利用者のGhostty→SSH→常用tmuxでの表示受入は未確認。日本語のtoken推定誤差そのものは評価対象外。
実装・スライス確認は完了。後続の配置では、この構成からCLI/TUIをビルドし、
実行中runがないことを確認してCoreを切り替える。既存DB・拡張設定は維持する。
別途メモしたsteering状態表示・retry回数・要約状態永続化・詳細記録重複は実装対象に含めていない。
