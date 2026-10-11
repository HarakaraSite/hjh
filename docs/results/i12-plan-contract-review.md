# I-12：計画・契約の通常／批判的review

2026-10-12。利用者が認証・アカウント選択画面を選び、計画の次の契約具体化・reviewへ進むよう指示した。
[計画](../plans/i12-authentication-ui.md)と[契約](../design/i12-authentication-contract.md)をread-onlyの
二人のreviewerが確認した。review中に採用findingを修正し、同じreviewerが更新済み記述を確認した。
現時点の未解消findingはなく、実装へ進める具体性がある。製品実装・実動作の確認は未着手。

## 対象と評価基準

通常reviewは利用者の操作、profile解決、保存owner・優先順位、既存credentialの再利用、
HTTP／TUI／CLI入口の置換を評価した。批判的reviewはroot／子Agentの認証binding、
account切替・refresh、attempt再送・取消・切断、保存順とCore終了の整合を評価した。

双方へrepositoryのproduct-first規則と時間上限30分を指定した。一般hardening、仮想failure matrix、
test件数は評価対象にせず、変更・full gate・credential読取・外部通信を行わない範囲とした。
契約文書と関連sourceを確認し、旧Henjiの現行経路と対比した。

## 採用findingと修正

| 種別 | 具体的な経路と利用者影響 | 修正・確認 |
| --- | --- | --- |
| 通常 P2：CLI loginのslot指定が未定義 | 現行 `cmd/hjh/main.go:loginWithChatGPT` は既存profileの再認証を行う。新beginはaccountId省略を追加slotとするため、単純な置換ではCLI loginのたびにaccountが増える。 | CLI login/statusはselectedAccountId、初期状態ではprimaryを明示する。接続済みslotの再認証と未接続slotの初回接続を区別した。通常・批判的reviewerとも修正確認済み。 |
| 批判的 P2：callback待ちを取消不能にする境界 | 現行 `credentials.CompleteLogin` はcallback待ち→検証→exchange。初期契約はcomplete受付を取消勝敗境界としており、callback省略で未認可のまま待つだけでもEsc/detachで閉じられず、同slotへ再認可できなくなる。 | starting/ready/waiting_callbackは取消可能、有効callback検証→exchangingの一回遷移を境界にした。貼付・自動到着を同じjobへ渡し、取消後の同slot再認可を明記。両reviewerとも修正確認済み。 |

通常reviewはP2一件、批判的reviewはP2一件。Blocker/P1は見つからなかった。
採用には明示操作と現行sourceからの具体的影響を使い、test追加だけで解消扱いにしていない。

## 補足の反映

- profile一覧のmethodsと認証操作用providerIdの対応を具体化した。一覧はprovider既定、roleのmodel指定、
  入口の実効Selectionという利用経路から作る。同kindの実効providerを使うか、同kindの利用providerから選ぶ。
  未参照credential宣言だけの登録機能は今回の利用経路へ追加しない。
- nonce/state/code challengeは認可URLに必要なqueryとして渡すことを明記し、token/code/callbackURL/
  PKCE verifierの非公開と区別した。これはrootが文書の矛盾を訂正したもので、新しい拒否機構ではない。
- private検証で保存済み認証を再利用することと、一accountのtokenを二slotへ複製して異なる実account切替の
  確認とすることを区別した。

## source対比と確認範囲

現行Go：`internal/credentials/{manager,storage,oauth}.go`、`internal/app/{connections,service,selection,run,
delegation}.go`、HTTP/client/TUIの認証・catalog操作、CLI login、provider stateのidentity照合。
runごとのfactoryは `app/run.go`、子Agentのfactory共用は `app/delegation.go` にあり、
契約のowner単位bindingと捕捉account指定refreshをこの寿命へ接続できることを確認した。

旧Henji：`v0/tui/remote_catalog_ui.ts`、`v0/agent/provider/credential_registration.ts`・`chatgpt_auth.ts`、
`v0/agent/worker/worker_runtime.ts`。registrationId指定の再認証／無指定の追加／保存済みaccount選択、
専用の伏せ字入力、accountのrefreshを機能上の参考にした。idle制限・固定port・入力上限は採用していない。

確認した契約にはAPIキーowner・override順・捕捉revision、標準slotと追加slot、metadata／record lock順、
部分保存結果、Core所有attemptと状態照会、scope・画面世代の照合、受付停止とjoin責務が含まれる。
review中にrootが文書を更新し、各reviewerが更新後の該当箇所を確認して初回reviewを閉じた。
別のfull gateや重複した全面再reviewは実施していない。rootの最終 `git diff --check` は成功した。

## 実装後に確認すること

APIキー登録→catalog/run、保存済みChatGPT利用、ブラウザ追加／再認証、異なる実accountの切替、
切替中のroot／並列子Agent、Ghostty→SSH→tmuxでの専用入力、切断・取消・Core停止・cold再開。
複数の実accountが使えるかは未確認。計画reviewの完了をこれらの実動作の確認と置き換えない。
結果はI-12の実装結果文書へ記録する。
