# TUI通知の残留・遅延上書き：実装と確認

2026-10-15。[対応計画](../plans/tui-notice-lifecycle.md)。利用者が実装・テスト・コードレビューまでの進行を指示した。

## 実装

- receipt取得失敗はSession側の`runtimeReceiptError`一か所に保持し、`m.status`へ複製しない。
  footerでは現在runのreceipt状態と別に、`Session receipt refresh failed · 詳細`を表示する。
  有効な取得成功で解除し、他の操作通知は上書きしない。未復旧の間は同じSessionの次runでも維持する。
- TUI内の`runNoticeTarget`が、表示対象のCore／Session／run IDを保持する。
  通常開始・attachで設定し、初回Session受付で同期する。終了時も確定済み対象を保持し、
  次run開始・attachで置き換え、Session切替でclearする。`activeRun`の寿命は延ばさない。
- 追加入力の成功・失敗・receipt不一致とキャンセル結果は、捕捉済み対象が表示対象runに一致する場合だけ通知する。
  次run終了後も旧対象の応答を再び現在扱いしない。
- 通知以外のreceipt照合・merge・revision追いつき、draft照合、queue進行、キャンセル確定・
  Session切替待機解除は維持する。保存receipt・指示本文・モデルcontext・Core・DB・APIは変更しない。

計画reviewerの指摘：新規Sessionの最初のrunはSession ID未確定で開始するため、開始時に対象を設定するだけでは
現在の操作通知まで非表示になる。`acceptRunEvent`での同一runへのSession割当にも同期を加えた。

## 独立端末確認

実装候補を`go build -o /tmp/hjh-notice-terminal.GawpgM/hjh-tui ./cmd/hjh-tui`でビルドし、
独立tmux・workspaceでcompiled TUI → apiclient → 制御可能なローカルHTTP応答を駆動した。
有料モデル呼出しや常用Core・DB・設定の操作は行っていない。

| 操作・応答 | 観測した端末出力・動作 |
| --- | --- |
| Session未選択からrunを送信、Core受付後のreceipt読取を失敗させる | `Session receipt refresh failed · RECEIPT_TEMP_FAILURE`が表示される |
| draftを入力し、run eventのInputRevision更新から既存の取得を成功させる | 失敗通知が消え、入力中draftが維持される |
| F2の失敗応答・F1のキャンセル確定応答を保留し、親runを終了する | 次runをdraftから開始できる |
| 次runへ進んだ後に、保留した旧追加入力失敗・旧キャンセル確定を返す | 旧失敗・`input chain settled`で次runのfooterが上書きされない |
| 現在runでF3を送信する | 現在runのsteering受付通知が表示される |
| 現在runを終了しCtrl-D | 待機入力へ戻り、detachして終了できる |

証跡：`/tmp/hjh-notice-terminal.GawpgM/verify.py`、`result.json`、
`01-receipt-error.txt`、`02-recovered-draft.txt`、`03-next-run-before-old-results.txt`、
`04-next-run-after-old-results.txt`、`05-current-run-acceptance.txt`、`06-completed.txt`。
実行結果は`ok: true`、2 runs・receipt取得4回・追加入力2回。
試験用tmuxとローカルHTTP serverは終了済み。

ローカルHTTPは通知遷移の制御用であり、実providerや常用Coreの実行証拠ではない。
利用者の常用端末での目視受入は未確認。

## focused test・レビュー

以下を実行して成功した（実装担当agentによる実行）。

```sh
go test ./internal/tui -run 'Test(Notice|SteeringFooter|RuntimeInput|AcceptedReceipt|LateAcceptedReceipt|LostSubmitResponse|FollowUpCapturesSelection|F1AfterTerminal|ResumeLoadsHistoricalReceipts|ReceiptReadCatchesUp|SessionSwitchWaitsForInputChain|Footer)' -count=1 -timeout=60s
go vet ./internal/tui
git diff --check -- internal/tui
```

- `notice_lifecycle_test.go`で、取得復旧・別操作通知維持・未復旧Sessionエラー保持・Session切替を確認。
- 旧追加入力の各応答分岐について、次run実行中と終了後の通知を上書きせず、receipt／draft／queue処理を維持することを確認。
- 新規Sessionの初回受付後の通知を実行中・終了後とも確認。
- キャンセル結果の表示対象判定と、表示抑制時にもsettled／Session切替待機解除が保たれることを確認。
- `run_notice_test.go`で、初回受付前からのキャンセル対象の同期と、終了時に確定したSession情報の保持を確認。
- 既存follow-up接続・Session復元のtestへ、表示対象runの更新の確認を追加した。
- coordinating ownerも最終差分、追加test、整形、`git diff --check`を確認。full gateは実行していない。

独立reviewerが計画・current source・test・端末証跡をレビューし、product correctness・regressionの指摘はなかった。
reviewerはtest・端末確認の再実行を行っていない。レビュー時の未実装表記は本結果と設計文書の更新で解消した。
実装・focused test・独立端末確認・コードレビューまで完了。利用者の常用端末での表示受入は別途未確認。

実装確認時点ではコミット・プッシュ・配置・常用Core再起動を行っていない。
後続指示でコミット・プッシュ・`dist/hjh`／`dist/hjh-tui`への配置が許可された。
稼働中processは停止せず、Core再起動は利用者が行う。
