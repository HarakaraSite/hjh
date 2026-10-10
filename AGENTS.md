# AGENTS.md

## Product-first development policy

This section is the highest-priority project policy for planning, implementation, testing, review,
diagnostics, and acceptance. It applies to the coordinating agent and every subagent. When another
repository document or prior practice conflicts with it, this section wins.

- Testはproduct機能と人間の利用可能性に従属する。testを通すために機能を狭め、正常な外部service
  responseを拒否し、fixtureを実仕様として扱わない。競合時は実利用経路、実行証拠、公式外部契約を
  先に確認し、誤ったtestを修正または削除する。
- 安全機構は機能が実環境で安定した後に別途扱う。機能安定前に考慮する安全性は、ユーザーが明示した
  API credentialとAuthorizationの露出防止だけである。一般的なhardening、fail-closed拒否、permission・
  入力・状態matrixを、依頼なしに設計、実装、test、reviewへ追加しない。
- 成功基準は、人間がproduction経路で目的の機能を完了できることである。offline fixture、test件数、
  review結果、sanitized failure、機械的gate成功をその代替にしない。
- 利用者から明確な指示がない修正は、後方互換を要求しない破壊的変更として扱う。旧形式のmigration、
  converter、compatibility read、dual-read/write、fallbackを推測で追加しない。この既定はcontractと実装の
  切替方針であり、既存dataやfileを削除する許可は含まない。実データの削除は利用者の明示指示を必要とする。

## 参考資料の位置付け

- [`docs/references/henji-harness/`](docs/references/henji-harness/README.md) と
  [`docs/normal-use-inbox.md`](docs/normal-use-inbox.md) は旧Henjiの参考資料であり、hjhの機能要件・
  受入条件・設計・実装順序の正本ではない。文書内の「採用済み」「正本」「実装済み」「不採用」等は
  旧Henjiにおける位置付けであり、hjhでの採用や実装認可を意味しない。
- hjhの機能や方式の採否は、利用者の明示指示と、このリポジトリで採用した要件・設計に従って判断する。
  参考資料の記載だけを根拠に、機能を必須化したり、制限や実装方式を引き継いだりしない。
- 採用済み方針と初期製品範囲は [`docs/design/initial-product.md`](docs/design/initial-product.md)、
  利用者が明示採用した履歴schemaの契約は [`docs/design/history-schema.md`](docs/design/history-schema.md) を参照する。
- 非対話runの実装段階・モジュール境界・確認方法は [`docs/plans/initial-run.md`](docs/plans/initial-run.md) を参照する。

## Planning

- 機能の追加・変更・廃止では、対象ファイルを決める前に、利用者の操作から結果までの現行経路を確認する。
  関係する入口、状態の所有者、データの生成・保存・参照先を追い、変更の影響範囲を決める。
- 新しい状態や処理を増やすときは、既存の責務や経路と重複しないか、現行の利用者がいるかを確認する。
  経路を置き換えたり廃止したりするときは、使われなくなる処理と保存先も同じ変更の対象に含める。
- 計画は最初に、利用者が必要とする動作、根拠となる明示要件・実行証拠・公式外部契約、実際のproduct
  経路、未確認事項を定義する。test一覧、test件数、拒否条件、hardeningから計画を組み立てない。
- 各testは、先に定義された具体的なproduct動作または確認済みregressionへ対応させる。対応先と根拠を
  示せないtestは計画しない。test件数は成果の記録であり、目標、上限、完了条件にしない。
- 未観測のprovider variant、仮想的failure、境界値やfilesystem/permission permutationを推測で仕様化しない。
  外部挙動が不明なら公式文書または実行証拠を確認し、それでも不明なら未確認として残す。

## Implementation

- 旧Henjiでは巨大なコードと密結合によって変更・検証に苦労したため、実装当初からモジュール化を意識する。
  責務ごとに境界を設け、各モジュールが所有する状態と公開する操作を明確にする。依存関係の方向を整理し、
  他モジュールの内部状態や実装詳細への直接依存を避ける。単にファイルを分割するだけでなく、変更の影響を
  局所化し、個別に理解・検証できる構成にする。分割や抽象化は実際の責務と利用経路に基づいて行う。
- 実装agentは承認されたproduct動作を最短の実経路で成立させる。明示されていない安全機構、permission
  check、入力制限、上限、拒否条件、sanitization、fallback、cleanupを「念のため」に追加しない。
- 実装詳細に不確実性がある場合も、機能を狭める推測を選ばない。公式契約、現行source、実行証拠で
  解消できなければ、変更せずcoordinating ownerへ返す。
- Testは機能実装後、そのproduct動作が実経路で成立することを確認する最小限を追加する。test helperの
  都合をproduction interfaceやparser contractへ逆輸入しない。
- 計画外のproduct bugは、原因、観測証拠、利用者影響、修正案を報告し、勝手に修正しない。

## Review

- 通常reviewは、機能correctness、明示要件、公式外部契約、実利用経路、変更によるregression、具体的な
  test不足を確認する。一般的な安全性reviewは行わない。安全性reviewはユーザーが別途明示した場合だけ、
  指定範囲で実施する。
- Test不足は、変更された具体的product動作が未確認であるsource-to-impactを示せる場合だけfindingにする。
  testがないこと自体、未観測variant、将来の仮想的failure、網羅matrix不足をfindingにしない。
- 製品不具合のfindingを採用するには、(1) 明示要件・実行証拠・公式契約の根拠、
  (2) current sourceから利用者影響までの経路、(3) test追加だけではないproduct上のcorrectness問題、
  の三つを必要とする。一つでも欠ければ
  coordinating ownerは採用しない。
- Test不足のfindingの採用は、上段の具体的な未確認product動作とsource-to-impactの条件で判断し、
  製品不具合の存在を追加条件にしない。
- Evidence gapという独立severityやclosure queueを使わない。必要な機能確認が不足している場合は、どの
  product動作が未確認かを通常のreview本文へ記録し、仮想的case追加で閉じない。

## Verification efficiency

- 実装中は変更箇所のfocused test、必要なtype check、format、lint、`git diff --check`だけを使う。
  全体testやgateを途中確認に繰り返し使わない。
- Review前のfull gateは要求しない。reviewerもfull gateを実行しない。
- 承認済み計画が要求する場合、安定候補に対するauthoritative gateはcoordinating ownerが一回だけ
  実行する。失敗時はfocused確認で原因を特定し、再実行には具体的理由を必要とする。

## Continuation

- 利用者がセッションの終了を宣言したときに限り、次回再開できるよう、作業の現在地と次の一手を
  `.handoff/handoff.md` に記録する。現在地には完了済み・未完了の作業と未解決事項、次の一手には
  再開時に最初に行う具体的な作業を記載し、必要な正本文書への参照を添える。
  終了の宣言がない通常の作業中や応答終了時には、この記録を作成・更新しない。
- Keep product concepts, architecture decisions, roadmaps, implementation plans, and results in their
  respective documents. Use this file for repository-wide working rules and pointers to authorities.
