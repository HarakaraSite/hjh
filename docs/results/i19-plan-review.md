# I-19 子agent計画の通常reviewと批判的review

2026-10-12。利用者の明示依頼により、独立した二人のreviewerが
[六スライス計画](../plans/i19-child-agent-control.md)と直接関係する現行sourceを確認した。
通常review、批判的reviewともに対応必須のfindingはなく、計画から実装へ進めるという判定である。
I-19の製品コード変更、test、実モデル確認はこのreviewでは行っていない。

## 対象と範囲

- 対象計画：`docs/plans/i19-child-agent-control.md`。
- 計画SHA-256：`5c001ad66787d14255e057ef53a0db8559d929a8efafda4e2123267aafb7fbec`。
- hjh HEAD：`1a8e899a1ec963f86df6ca245dc261b55173baeb`。現行sourceはその上の作業ツリーを含む。
- 通常review：明示要件、現行product経路、モジュール責務、スライスの依存と確認方法。
- 批判的review：子の寿命、VMとGoの境界、admissionと終了保存、報告cursor、追加指示の成立条件。
- 先行するTUI等の変更の再review、一般的なsecurity/hardening、testとfull gate、
  provider呼出し、Core操作、外部書込みは対象外。
- 初回は各20分上限。必須findingがなく計画修正をしていないため、re-reviewは行っていない。

## 通常review

対応必須のfindingはない。以下の契約と現行sourceの整合を確認した。

| 確認した内容 | 根拠 |
| --- | --- |
| JS call単位の子取消と、親execution単位のspawnの寿命を分けている | [現行call](../../internal/jsruntime/call.go)、[子管理](../../internal/jsruntime/host.go) |
| tool指定を適用してからworkspace指示とskill reader案内を組み立てる | [capture](../../internal/extensions/extensions.go)、[delegation](../../internal/app/delegation.go)、[workspace準備](../../internal/app/workspace_context.go) |
| tool callに併記した本文を含め、保存済みassistant messageを報告取得に使える | [Message型](../../internal/contracts/contracts.go)、[message読取り](../../internal/history/read.go) |
| 本文とcursorの同一SQL cutと、共有既読位置を持たない方針が追記履歴に対応する | [保存](../../internal/history/conversation_write.go)、[読取り](../../internal/history/read.go) |
| 子専用入力ownerとrequest境界での適用、最終応答後の続行を分離できる | [AdditionalInput境界](../../internal/agent/loop.go)、[app入力adapter](../../internal/app/runtime_input.go) |
| 六スライスの依存と確認方法が具体化されている | 構成指定、Goの子管理、公開操作、報告、追加指示、実利用の順 |

子指示のsemantic payloadとtransaction型は、計画がS5へ委ねている詳細化事項として扱う。
現在のF3 InputReceiptをそのまま子へ流用する計画ではない。

## 批判的review

対応必須のfindingはない。以下の成立条件を確認し、後段で前段を作り直す必然的な欠落は見つからなかった。

- spawnの子完了goroutineをVMのcall drain対象へ残さず、Go側で親executionの寿命を管理する。
- 子をjoinし、処理中操作と指示の保存を完了してから、親の終了保存と会話採用へ進む。
- message ordinalと同一SQL cutを使い、新しい報告をcursorで飛ばさない。
- 子の指示受付終了、処理中保存の待機、messageと適用receiptの同時確定を、
  既存AdditionalInput境界とhistoryの責務へ接続する。

任意の明確化として、次の二点が挙がった。

| 項目 | sourceと実装時の扱い |
| --- | --- |
| S1のJS引数bridge | [host.go](../../internal/jsruntime/host.go)は現在taskとroleだけを読み取る。S1の構成指定を完成させるには、modelとtoolsのGo requestへの受渡しもS1へ含める。計画の「主な変更範囲」の補足であり、契約欠陥とは判定されていない |
| S5の子専用receipt | [SaveRuntimeInput](../../internal/history/runtime_input.go)はCoreIDとRunIDを必須とする人間入力用codec。これをそのまま子へ流用せず、計画どおり既存semantic記録とcontentsを使う子instruction型とtransactionを具体化する |

## ownerの整理と実装時の確認事項

両reviewの進行可能という判定を受ける。
S1の引数bridgeはS1の実装対象として扱い、S5の子専用receiptは同スライスで具体化する。
今回のreviewによる計画本文の変更はない。

実モデルが中間報告を行う頻度、自然文からstatusとsteerを選ぶ動作、
detachと再接続、実際の保存とrace確認は実装後の検証対象であり、review済みを実動作確認済みと扱わない。
これらは計画の各スライスとS6に記載した経路で確認する。
