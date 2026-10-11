# I-11：モデルのお気に入り保存

2026-10-11。[計画](../plans/i11-model-favorites.md)と
[保存・API・picker契約](../design/i11-model-favorites-contract.md)に沿って実装した。
focused test／race、通常・批判的reviewと一回の限定再review、採用指摘の修正、
private Core／tmux／実provider、最終Linux配布物でのcold再開確認を完了した。

## 利用者の操作と保存

`/model`のTabで選択モデルを登録／解除する。保存確認後に印と先頭への整列を反映し、
検索・選択model ID・会話draft・次runのselectionを保存操作で変更しない。
保存待ち中も矢印・検索・Enter・Escを使える。catalog外のお気に入りはIDの候補として表示する。

お気に入りは実効config絶対path・provider IDの設定で、Session・認証accountとは別である。
Coreが小さいJSONを保存し、TUIや会話DBに同じ設定を書かない。履歴schema v3は変更しない。
catalog名・effort・credential・会話本文・拡張source・provider requestへ混入しない。
既定保存先はXDG_STATE_HOME/hjh/model-favorites、未指定時は~/.local/state/hjh/model-favorites。
`--model-favorites-dir`はこの保存先だけを変更する。

## 所有者と公開経路

modelfavoritesはCore内favorite専用FIFO、受付ticket、JSONと別lock fileのflock、Close joinを所有する。
scope解決前の受付順でGET／PUTを進め、回復GETは先行ticketの完了後に読む。
受付予約と保存登録を分け、caller取消は保存登録前だけに適用する。
保存登録後はclient切断から独立して完了し、Core停止は受付を止めて登録済みticketをjoinする。
Managerのrun／Session lock、auth、描画の待ちへfile処理を入れない。

appは既存の遅延workspace/config/provider解決を接続し、HTTP/clientは
GET／PUT `/api/v2/model-favorites`を公開する。既存 `/models`はcatalogのままである。
PUTは明示true/falseで同値再送可能、responseは保存後の小さいmetadataだけを返す。
同CoreのFIFOと別Coreのflockで異なるIDの更新を保持する。

TUIはcatalogとfavoriteを別に取得し、初回／回復GET・PUT・catalog到着の整列でも選択IDを保持する。
保存中の小さい操作metadataをpickerの寿命と分け、閉じたpickerや切替先へ遅い応答を適用しない。
未取得のTabは再読取のみ、結果不明のTabはGET確認と同じ希望値の再送に使う。

## 確認結果

store／app／HTTP-client／TUIのfavoriteと関連picker・Tabに対するfocused test／raceは通過した。
関連packageのvetとbuild、format、`git diff --check`も通過した。
採用指摘の修正後は、その回復・Tab経路のfocused raceを再確認した。
full gate、一般hardening、利用者の私有設定／DB／稼働processの変更は行っていない。

private Core／config／favorite directory／tmuxで次を確認した。

- 実OpenRouter `/models` catalogからTab登録／解除、detach／再接続、catalog外favoriteの解除後行保持。
- 同じconfigの別Coreから異なるIDを更新して双方を保持、provider／config分離、同値再送のrevision不変。
- Linux flockを検証processで保持し、Coreが保存登録後にlock待ちとなったことを/procのdescriptorで確認。
  clientを切断してCore shutdownを受理してもprocessは終了せず、lock解放後に保存・join・終了した。
- 実OpenRouter Responses／DeepSeek V4.1 Flashのrun中にTabで解除し、保存済み実効modelは変わらなかった。
  run_jsからcommand sleepを実行し、最終回答 `I11_FAVORITES_RUN_OK` と正常終了保存を確認した。

Sessionは`a18cd666-7dec-45d2-b951-8f6fef0fd6f5`、executionは`c1ab2e50-cae7-4a61-b023-248a46d91b11`。
git管理外の`dist/i11-live/verified/`にpane・private DB・JSON設定・summaryを保管した。
既存credential参照を再利用し、追加ログインを要求していない。
保存schemaにfavorite tableがないこと、保存済み拡張capture・provider requestへfavorite metadataが
入っていないこともこの実runで確認した。

## 最終Linux配布物

archiveと両binaryのSHA-256、static ELF、展開物の整合を確認し、最終配布物で次を操作した。

- 458件の実OpenRouter catalogから目的モデルを検索し、Tabで登録する。file lock待ち中のTab連打は保存待ち表示、印は未確認のまま。
- 保存待ちでもEsc・detachを行える。Core停止は受付後も保存を待ち、lock解放後に正常終了する。
- 同じconfig path・favorite directoryでCoreを再起動し、Sessionをcold再開して印と保存済み集合を確認する。
- Tab解除後の印、Enterによるモデル確定、通常editorの`/ren`→Tab補完を確認する。
- favorite directory optionを省略したCoreでも、私有XDG_STATE_HOME下へ保存し、GETだけではdirectoryを作らない。

証跡は`dist/i11-live/cold/`と`default-state/`、最終hashは`dist/i11-live/package-summary.json`。
`dist/hjh`と`dist/hjh-tui`を同じ最終binaryへ更新した。
package確認では新しい有料runを繰り返さず、実catalogと保存・cold操作を確認した。

## reviewと修正

通常・批判的reviewは旧Henjiのremote_catalog_uiとlive_model_catalogの操作を対比し、
GoのFIFO・保存admission・Close、HTTP/client、pickerの遅延応答と共有scopeを確認した。
一般hardeningや仮想的matrixは対象にしていない。採用した指摘は次の二件で、未解消の採用findingはない。

| 指摘 | 修正 |
| --- | --- |
| P2：保存中TabがGET／同値再送を並列起動し、初回PUTの受付前に追い越し得る | putInFlight中は保存確認中の表示だけにし、通信結果不明と判明した後だけ回復GETへ進む |
| P2：開き直したpicker／共有configの別Sessionで旧操作を回復すると印が古く、次Tabが解除にならない | 回復完了後、同Core・確認済scopeId・providerの現在pickerで新GET。現在の世代・Session／workspaceを捕捉し、新GET待ちの旧flag反転も止める |

批判的reviewの限定再確認では二件の解消を確認し、新規Blocker／P1なし。
通常reviewの限定再確認では、二件目についてfresh GET失敗後に旧flagを反転する残留経路が指摘された。
親が読取error時のTabも再取得だけとする修正を行い、同じregressionで
回復→fresh GET失敗→Tab再読取のみ→読取成功後の次Tab解除までfocused raceを通した。
追加の委譲reviewは行わず、親が最終source・配布物確認を担当した。
各reviewerの確認はsourceとtest内容のread-only確認であり、live call・full gateは実行していない。

## 後続課題

[HTTP生本文とターン中context予算](../notes/http-response-context-overflow.md)、認証登録画面、
Core自動起動、色・Glamour、tool詳細の再設計は今回変更しない。
