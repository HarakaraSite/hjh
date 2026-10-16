# I-11：計画・契約review

> **後続変更の案内（2026-10-16）**：以下の「未実装」は2026-10-11の計画review時点。I-11はその後実装済みであり、[実装・確認結果](i11-model-favorites.md)と[現行契約](../design/i11-model-favorites-contract.md)を参照する。

2026-10-11。利用者が選択したモデルのお気に入り保存について、
[計画](../plans/i11-model-favorites.md)を[保存・API・picker契約](../design/i11-model-favorites-contract.md)へ具体化した。
通常・批判的review、採用指摘の修正、一回の限定再reviewを完了した。
製品codeは変更しておらず、I-11は未実装である。

## 確認した現行経路

- TUIのSelection preview、provider／model picker、非同期jobと世代、indexによる選択確定。
- appのselectionConfig、Modelsと実効認証、Service.Close、credentialのstate directory。
- HTTPのSession／workspace解決、Core ID、shutdown応答とClose、serveの終了順、clientとDTO。
- 旧HenjiのTab操作・保存中の表示、Core modelFavorite、catalogのfavorite保存・整列・account別file。

config path・provider別の共有scope、認証accountに依存しない好み、catalogと独立したmetadata API、
catalog外model ID、別lock fileのflockとatomic renameを確認した。
旧Henjiのcatalog snapshot・effort cache・account別保存・保存中Enter停止は引き継がず、
Goの独立したpreference ownerと既存selection／catalog経路へ接続する。

reviewerはread-onlyで、計画・契約と直接関係するsourceだけを確認した。
一般hardening、permission／failure matrix、context超過対応、I-06〜I-10全体の再reviewは対象外。
test・provider API・私有DB操作・full gateは実行していない。

## 採用・修正した指摘

| review | 指摘と利用者影響 | 契約への反映 |
| --- | --- | --- |
| 通常・批判的／P2 | 切断後も残るPUTを同値再送した場合、flockだけでは古い保存が後の解除を追い越し、お気に入りを復活させ得る。config解決が登録前なので、保存workerだけのFIFOでも不十分。 | scope解決前の受付ticketとCore内favorite GET／PUT専用FIFO。回復GETは先行ticket完了後に読む。受付予約はcaller contextに従い、解決後の保存admissionから切断と独立する。履歴receipt／CASは追加しない。 |
| 批判的／P2 | 初回／回復GETが遅れてfavorite-firstへ整列すると、選択indexが別モデルを指し、Enterで意図しないモデルを確定する。 | PUTだけでなくGET・catalog到着時も、適用直前の選択model IDを捕捉して再構築後に保持する。 |

親が併せて、shutdownの202応答前・Server.Close入口・Service.Close直接呼出しでの受付停止、
同scopeId・provider ID内だけのrevision比較、favorite未取得時のTabは再取得だけを行う操作を明記した。
Serviceには遅延workspace resolverを渡し、HTTPの事前scope解決や二重FIFO登録を避ける境界を示した。

## 限定再reviewと未確認事項

通常reviewは初回P2の解消を確認し、変更箇所に新規Blocker／P1なし。
批判的reviewも初回P2二件の解消を確認し、変更箇所に新規Blocker／P1なし。
再reviewは変更箇所と既存findingだけを一回・15分以内で確認する範囲とした。
親が採否と最終文書整合を確認し、`git diff --check`と関連文書のlocal link確認は通過した。

未確認なのは、実装後のTab登録／解除、実際の通信切断・回復と保存順、複数Coreのfile更新、
Core終了join・再起動、実catalog／runとの分離、Linux配布物での操作である。
文書reviewはこれらの実利用確認を代替しない。

次は計画の順にpreference store、app／HTTP／client、TUIへ接続し、私有設定とCoreで確認する。
今回の依頼範囲は計画・契約reviewまでであり、製品実装は開始していない。
[HTTP生本文・ターン中context予算](../notes/http-response-context-overflow.md)は後続課題のままとする。
