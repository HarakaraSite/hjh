# I-10：計画・契約review

2026-10-11。利用者のI-10再開指示により、
[計画](../plans/i10-context-recall.md)の操作案を[契約](../design/i10-context-recall-contract.md)へ具体化した。
通常・批判的review、採用指摘の修正、一回の限定再reviewを完了した。
この結果は実装前の文書確認であり、I-10機能の実装・実provider確認の完了を意味しない。

## 確認した経路

- Go：ManagerのSession予約・I-09 F2、appのroot準備・admission・入力構築、
  historyの終了保存・message/mirror・context attribution、人間の会話walk/TUI。
- 旧Henji：Data ownerのprepare/clear/admission、Workerの公開情報投影とrequestへの挿入。
  旧方式の全面移植ではなく、Goの既存所有者とschema v3の経路で成立するかを対比した。

一般hardening、仮想的なpermission/failure matrix、context超過対応、表示色は対象外。
reviewerはread-onlyで、test/full gate・provider API・私有DBの操作は行っていない。

## 採用・修正した指摘

| review | 指摘と利用者影響 | 契約への反映 |
| --- | --- | --- |
| 通常・P1 | 停止時にassistant_text_statesは終端recordへ移動・削除される。stateだけを読むと生成途中本文を引き継げない。 | settledのphase=incomplete/entityKind=assistantのsemantic recordを正本にし、length終了も未完了として区別。 |
| 批判的・P1 | BeginExecutionはcommit後に取消可能ctxで再読取する。取消でIDを取得できないと、保存済みrecallを未消費として残し得る。 | candidate target IDを事前捕捉。root admissionの返却metadataをtransaction内で確定し、commit後再読取へ依存せずtyped通知する。 |
| 批判的・P2 | 通常userの保存経路では大きい参考messageがsemantic mirrorにも全文複製される。 | contentsの本文とは別に軽いrecall markerを保存し、会話walk・cold表示・再recallの識別で共用する。 |
| 批判的・P2 | relationはadmission時に保存されるため、reference/requestが未作成でも「モデルへ渡した」と誤表示し得る。 | latestRecallAdmissionへ改名し、参考message保存・request準備保存のlocatorを区別。provider受理まで保証しない表示とする。 |

付随してsystem→instructionCountの対応を明記した。
再recallでは旧参考messageをtask/F3と区別し、元の公開情報を平坦化して一回だけ引き継ぐ方針とした。
計画側の重複する契約案も概要へ置き換え、具体的なDTO/payloadは契約文書へ集約した。

## 限定再reviewと次の段階

通常reviewは初回指摘の解消を確認し、新規Blocker/P1なし。
批判的reviewは初回3指摘の解消を確認し、新規Blocker/P1なし。
いずれも変更箇所と既存findingの解消に範囲を限定した。親が採否と最終文書整合を確認した。
`git diff --check`は通過した。

次は計画の実装順に、metadata読取、recall操作、root admission/入力・保存、HTTP/client/TUIを接続する。
実装後には停止→準備→次の実provider request、F2、再recall、cold読取とDB/heapの実測を確認する。
文書reviewでその実利用確認を代替しない。

HTTP生本文とターン中のcontext予算・送信選択は[後続課題](../notes/http-response-context-overflow.md)のまま。
I-10のrecallで巨大なtool結果を渡す場合のcontext超過は、今回の未解決事項として残す。
