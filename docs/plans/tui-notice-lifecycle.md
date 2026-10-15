# TUI通知の残留・遅延上書きの対応計画

2026-10-15。利用者の「steer以外にも同様のメッセージが残り続ける処理はないか」の調査を受けた対応計画。
状態：利用者の指示に従い、実装・focused test・独立端末確認・コードレビューまで完了。
[実装・確認結果](../results/tui-notice-lifecycle.md)を参照。後続指示でコミット・プッシュ・配置が許可された。
常用Core再起動は利用者が行う。
先行するsteering表示修正はcommit `cdac566`。
表示契約は[TUI footer](../design/tui-footer.md)、作業方針は[AGENTS.md](../../AGENTS.md)に従う。

## 1. 利用者が必要とする動作

- 復旧したreceipt取得を、失敗中として表示し続けない。
- 前runの追加入力・キャンセル操作の結果を、次runの現在通知として表示しない。
- 現在対象の操作結果・未解消の取得エラーは確認できる。表示だけを消すために正常な応答を捨てない。
- 指示本文、receipt履歴、モデルcontext、draft照合、Coreのキャンセル確定・Session切替待機は維持する。

根拠は利用者の調査・計画指示と、current sourceおよび実TUI modelを駆動した調査時の実行証拠。
一般的な通知システムへの置換や時間切れ消去は目的としない。

## 2. 現行のproduct経路と観測

| 経路 | 現行の状態所有と表示までの流れ | 観測した問題 |
| --- | --- | --- |
| receipt取得 | run event／Session復元／revision追いつき → `loadRuntimeInputs` → Backend.Inputs → `acceptRuntimeInputs` → footer | 失敗を`m.status`と`runtimeReceiptError`に二重保持し、成功時は後者だけclearするため、復旧後も前者が残る |
| 追加入力 | F2/F3 → `runtimeInputOperation`へ対象とdraftを捕捉 → SubmitInputと既存照合 → `acceptRuntimeInput` → `m.status` → footer | 成功応答にはactive run照合があるが、error／receipt不一致分岐はSession一致だけ。前runの失敗が次runの通知へ入る |
| キャンセル | F1／Session切替 → 対象Core/runを捕捉 → CancelInputChainと既存poll → `acceptInputChainSettlement` → 通知・Session切替待機の解除 | 旧キャンセル対象と応答の照合はあるが、表示中runとは照合しない。次run開始後に旧結果が通知を上書きする |

対象source：`internal/tui/runtime_input.go`の各accept処理、`model.go`の`startRun`・`finishRun`・
`submitDraft`、`session_management.go`の`attachRun`、`commands.go`の`applySessionAction`、
`footer.go`の`footerNotice`。

`startRun`・`attachRun`は通常の`m.status`を上書きする。`finishRun`は`activeRun`をnilにする。
Session切替ではgeneration更新と`clearRuntimeInputs`がある。
従って、通常の完了通知の保持と、別変数からの再表示・遅延応答による上書きは区別する必要がある。

調査では、一時Go overlay testから実TUI modelのhandlerを駆動して次を確認した。

- receipt取得失敗 → 成功後も`runtime input receipts unavailable …`が表示された。
- 取得失敗後の次run開始時にも、別変数から`receipt refresh failed`が表示された。
- 次run実行中に旧follow-upのerrorを渡すと、`follow-up unconfirmed …`へ上書きされた。
- 次run実行中に旧キャンセル結果を渡すと、`input chain settled …`へ上書きされた。

これはhandler経路の実行証拠であり、稼働Core・実端末での再現証拠ではない。
overlayは調査後に破棄した。その後、本計画の実装指示に従い恒久testとproduct修正を追加した（結果文書参照）。

## 3. 提案する表示契約

### A. receipt取得の状態：Session単位

- 取得失敗の通知は、receipt取得ownerが一か所で保持する。`m.status`への同じ失敗のコピーを廃止する。
- footerはその状態から取得失敗を表示し、現在のreceipt状態とは区別する。現行で通知している失敗の詳細は維持する。
- 同じSessionの有効な取得が成功した時点で解除する。他の通知を`ready`などで上書きしない。
- 未解消の間は同じSessionで次runへ進んでも表示を維持する。run開始だけでは取得の復旧を証明しないためであり、
  「前runの失敗」ではなく「このSessionのreceipt取得が未復旧」であると読める表現にする。
- Session切替・新規Sessionでは既存clear経路で解除する。旧Sessionの応答は既存generation/job照合で適用しない。
- 今回は自動retryやpollの追加を行わない。既存の取得が成功した時点で表示が正しく戻ることを扱う。

### B. 追加入力の結果：対象run単位

- 通知を表示してよい対象を、TUI内のCore ID・Session ID・run IDで識別する。
- error／receipt不一致の場合も、応答receiptではなく送信時に捕捉したoperationの対象で判定する。
- 現在対象のrunと、そのrunが終了して次runへ進む前の待機期間では、受付・未確認などの操作結果を表示する。
- 次runの開始・follow-up接続・別runへの復元、Session切替後は、旧対象の結果で通知を上書きしない。
  次runも終了した後の遅延応答に対して、単なる`activeRun == nil`では表示を再許可しない。
- 通知対象外でも、既存のqueue進行、receipt照合・merge・revision追いつき、draft照合は実行する。
  失敗した応答を成功扱いにしたり、指示を再送する経路は追加しない。

### C. キャンセル結果：対象run単位

- Bと同じ対象判定を、キャンセル成功・失敗通知へ適用する。
- 旧対象の通知を抑える場合も、既存のjob照合、pending解除、settled反映、
  `applyPendingSessionIfSettled`の呼出しは維持する。Session切替のために必要な確定を捨てない。
- 次runの開始を禁止したり、Coreのキャンセル方式・poll契約を変更したりしない。
- キャンセル対象は既存のrunを単位とする。新しいchain識別・保存状態は導入しない。

### 最小の状態境界

receipt取得状態は既存のSession側ownerへ集約する。
B/Cでは、終了後も次run開始まで対象を識別する必要があるため、表示用に最後に表示対象となったrunの
Core／Session／run IDを保持し、`startRun`・`attachRun`で更新、Session切替でclearする小さな共通判定を設ける。
新規会話の初回runはSession ID未確定で始まるため、`acceptRunEvent`でCoreが同じrunへSession IDを
割り当てた時点でも同期する。終了時にも確定済みの対象を保ち、次run開始までは一致判定に使う。
これは計画reviewerのP2指摘を採用したもので、現在runの通知を誤って抑制しないための対応である。
既存`activeRun`は終了時に消えるため、実行制御のownerとして維持し、表示寿命のために延命しない。
この表示対象は永続化せず、TUI再接続時は実際にattachしたrunから設定する。
receipt一覧やoperation queueを表示の都合でclearする処理は追加しない。

## 4. 実装範囲と順序

1. `runtime_input.go`・`footer.go`で取得エラーの重複保持をなくし、復旧時の解除を成立させる。
2. `model.go`・`session_management.go`・`commands.go`の開始／接続／Session切替で表示対象runを更新する。
   状態と判定は小さなTUI内helperへ局所化し、汎用通知queue・event busなどは作らない。
3. 追加入力の全応答分岐とキャンセル結果で、制御・データ反映と通知対象判定を分離する。
4. 下記のfocused確認を追加・実行し、`tui-footer.md`へ採用した表示契約を反映する。
   実行証拠・確認範囲は`docs/results/tui-notice-lifecycle.md`へ記録する。

想定対象：上記productionファイルとTUI内の小さなhelper、`runtime_input_test.go`・`footer_test.go`等の関連test。
変更前に使用箇所を再確認し、不要になった二重保持・判定も同じ変更で除く。

## 5. 確認方法と受入

先にA〜Cのproduct動作を成立させ、その動作に対応する最小限のtestを追加する。

- A：取得失敗 → 成功で古いエラーが消える。復旧中に別の操作通知が出ていてもそれを消さない。
  未復旧での次run開始はSession取得エラーとして表示し、成功で解除する。Session切替では引き継がない。
- B：旧runの遅延errorとreceipt不一致が、次run実行中／次run終了後の通知を上書きしない。
  現在対象の同じ結果は表示できる。既存の受付・適用表示、receipt保持、draft照合、queue進行を維持する。
- C：旧キャンセル結果は次runへ表示せず、現在対象の結果は表示する。
  表示を抑えてもsettled反映・Session切替待機解除が維持される。
- 開始／接続経路：通常start、Core開始follow-upへのattach、実行中Session復元で対象更新を確認する。
  Session未選択からの初回start・Core受付を駆動し、受付後の操作結果が実行中と終了後の待機中に表示されることも確認する。
  既存steering regressionと、終了後receipt追いつき・Session切替待機のfocused testを併用する。

実装中は関連focused testと必要なtype check／format／vet、`git diff --check`に限定し、full gateは要求しない。
安定候補では、独立workspaceのcompiled TUIと制御可能なローカルBackend／HTTP応答で、
取得失敗後の復旧と旧run応答の遅延を起こし、footerが現在の状態へ戻ることを端末出力で確認する。
この操作で人間がdraft入力・次run開始を続けられることも確認する。
有料provider呼出し、常用Core・DB・設定の操作は不要。

受入は「復旧・次run移行後に旧通知を現在状態と取り違えず、現在の操作結果を確認して作業を続けられること」。
handler testの成功だけを実端末での成立の代替としない。端末確認を実施できなければ、その範囲を未確認と報告する。

## 6. 対象外・未確認事項

- 通常の`run completed`・`… loaded`・rename等に対する自動消去、通知全体の再設計。
- `ctx …`の待機中保持、会話内の失敗・retry記録、picker・auth画面の別の表示改善。
- DB・Core・API契約、指示本文、保存receipt、モデルcontext、キャンセル制御・再試行規則の変更。
- 常用Core再起動。実装・テスト・コードレビュー後、別途指示でコミット・プッシュ・配置が許可された。

未確認：稼働環境の端末での発生頻度と、人間による最終表示受入。
未観測の遅延・失敗variantを追加の仕様や網羅matrixにはしない。
新たな別問題が見つかった場合は、この計画へ無断で追加せず根拠と利用者影響を報告する。
