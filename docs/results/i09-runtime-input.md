# I-09：実行中の追加入力と次の依頼の予約

2026-10-11。[計画](../plans/i09-runtime-input.md)と
[受付・適用・予約契約](../design/i09-runtime-input-contract.md)に基づき実装した。
実装・focused test・通常／批判的review・実provider／PTY・保存コスト・配布物確認を行った。

## 利用者の操作

- F2は現在runの正常終了・保存・会話採用後に、同じSessionで実行する依頼を予約する。
  複数の予約を受付順に実行し、受付時のrole・接続・model・effort等を保持する。
- F3は現在の親Agentに追加指示を送る。生成やtoolを途中で止めず、最初のrequest準備前、
  tool群の結果保存後、最終回答保存後の境界で取り込む。
- 実行中のEnterはdraftを保持する。受付確認後は送信したdraftだけを消し、応答待ちの間に編集したdraftを残す。
- F1は現在runに属する予約列をcancelする。既に次runへ引き継いでいても同じ列を止め、別の独立したrunを巻き込まない。
  Session切替は保存とjoinの完了を待つ。detachは通信だけを閉じ、Coreの実行・予約は続く。
- `/inputs`で受付・適用・予約・開始・未実行理由を読める。再起動前のreceiptはhistoricalとして参照し、自動再実行しない。

## 所有者と保存

Coreの入力owner、受付・参照、終了・follow-up引継ぎを別モジュールにした。
Managerの既存Session予約を直接引き継ぎ、短いlock内で受付・終了・cancelの順序だけを決める。
DB、provider、VM、SSE、描画の完了をそのlock内で待たない。

HTTPは既存JSON＋SSEを維持し、inputs、receipt一覧、input-chainの参照・cancelを追加した。
旧来のexact run cancelは引き続きそのrunだけを対象とする。
同じinput IDの再送は保存済み受付を返し、通信が切れても受付済み処理はCoreが所有する。
TUIはterminalのnextRunIdへattachし、予約本文を再POSTしない。

履歴はschema v3の既存tableを使い、DDLを変更していない。
受付receiptとSession revisionを同じtransactionで保存する。
steeringの適用messageとreceipt更新も同じtransactionに置く。
steering本文はcontentsの同じdigestを受付・message・semantic recordで共有し、semantic payloadはmetadataだけにした。
follow-up本文のdigestは次executionのtaskにも使い、開始時に同じ本文をDBから再読込しない。
receipt一覧・control factへ本文やqueue全体を複製せず、処理済み入力をCoreのpending mapから外す。
cold会話は保存済みcontentから同じ会話ID・位置・本文を復元する。

## review

通常・批判的reviewは旧HenjiのTUI、Core/task service、Agent loopの実経路と対比した。
旧実装の固定1枠制限やworker構成をそのまま採用せず、hjhの契約とGoの所有者・同期境界で確認した。

| finding | 対応 |
| --- | --- |
| 通常P2：旧Session宛の遅延受付応答が新Sessionの表示へ混ざる | 送信時のCore・Sessionを捕捉し、切替後の応答を表示へ採用しない |
| 通常P2：steering本文がsemantic payloadにも重複保存される | payloadをmetadataに縮め、保存済みcontent digestから復元する |
| 通常P2：receipt状態更新でupdatedAtが変わらない | 状態更新時刻を保存し、受付時刻を維持する |
| 通常P2：terminal後の遅いreceipt pageから最新revisionへ追い付けない | 必要revisionをactive runと独立に保持し、未取得差分を読み続ける |
| 批判的P2：既受付F2の再送で現在configを再解決する | 保存済みreceiptを先に照合し、受付時のselectionを返す |
| 批判的P2：cancel後の最終回答境界でsteeringを取り込む | claim前にcancelを確認し、未適用理由を保存する |
| 批判的P2：execution開始前のcancelをstart_failedと記録する | 観測したcancel・Core停止理由を保存する |

通常reviewの一回の限定再reviewで4件の解消を確認した。
Coreのsettled判定をrun workerの終了後に置き、予約runの登録・terminal通知・joinより先にsettledを返さない。
終了時の小さいcontrol snapshotを保存し、receipt/controlの保存失敗を正常終了やsettledとして扱わず返す。

批判的reviewの一回の限定再reviewで、初回3件のF1/Core停止経路と上記Core終了処理の解消を確認した。
その再reviewに残った「準備中のfollow-upを既存exact-run cancelで止めるとstart_failedになる」点は、
観測済みcancel理由を優先して記録するよう修正し、親agentがsourceとfocused race testで確認した。

保存コスト測定では、複数steeringの取消処理が、走査中のqueueをその場で詰め直して入力を飛ばす不具合を観測した。
取消対象をsnapshotにして走査し、全receiptへunapplied/cancelledを保存するよう修正した。
親agentが8件のfocused race test、64件の再測定、配布candidateの実provider経路で解消を確認した。
Core停止時に未適用steeringの理由がcancelledになる点も実経路のfocused testで観測した。
停止理由を実行contextの取消より先に確定し、sourceのCloseでその理由を使うよう修正した。
Core停止時のsteering／follow-up保存と、通常cancelを巻き込まないことをfocused raceで確認した。
これら最後の局所修正について独立reviewを追加していない。既知の未解消必須findingはない。

## verification

Agent loop、app/historyの保存・適用、Coreの受付・引継ぎ・cancel、HTTP/client、TUIのdraft・receipt差分に対する
focused testとrace確認、関連vet、format、diff確認を行った。full gateは実行していない。

同ID再送、最終回答後の継続、cancel前後のclaim、複数F2のFIFOと同Session履歴、
元run IDから引継ぎ済みrunを止める操作、独立Sessionを巻き込まないこと、
Session切替待ち、遅延receiptの旧Sessionへの混入防止、terminal後のrevision追随を確認した。

## 実provider・PTY

private Core／DB／tmuxで、実際のTUIのF2/F3/F1、detach/resumeを操作した。
利用者の稼働Core／TUI、DB、設定、tmuxは変更していない。既存credential参照を再利用し、ログインを追加要求していない。

- OpenRouter Responses／`deepseek/deepseek-v4.1-flash`の生成中にF3を受付し、
  一度保存した最終回答の後に同じrunで指示を取り込んだ。次の実model_requestと最終本文で確認した。
- F2の予約が同じSessionの次turnで完了した。受付後に`/provider`をOpenAIへ変えても予約済みselectionはOpenRouterのままだった。
  detachしてもCoreが継続し、再接続したTUIがCoreの作ったnextRunIdへattachした。予約taskのTUI再POSTは0。
- F3の受付HTTP応答を一時保留してdraftを編集し、受付後も新しいdraftが残った。
- OpenAI Responses／`gpt-6-luna`の実`command sleep`中にF3を送った。
  tool結果のmessage ordinalがsteeringの適用ordinalより前で、次の実model_requestと最終本文に追加指示が入った。
- 別runの`command sleep`中にF2を予約してF1で取消し、runの取消、未開始予約のnot_started/cancelled、
  列のsettled、Core／TUI終了0を確認した。

この通し操作は複数未適用steeringの取消修正前のcandidateである。
その取消修正後の配布candidateでは、変更された経路に絞ってOpenAIの実tool実行中にF3を8件、F2を1件受付して取消し、
全8件のunapplied/cancelled、予約のnot_started/cancelled、settled、同ID再送200と二重受付なしを確認した。
最終配布物ではOpenAIの実tool実行中に同様の8件と1件を受付してHTTPでCoreを停止し、
全8件のunapplied/core_stopped、予約のnot_started/core_stopped、rootの終了保存、後続executionなし、Core終了0を確認した。
TUIのbinary hashは通し確認時から同じである。

通し確認のSessionは`0a549894-72e8-4b9c-bd96-41b4b51e8019`。
証跡はgit管理外`dist/i09-live/runtime-input/`、`multi-steering-cancel/`、`core-stop/`。
requests、実model_request、pane、保存view、receipt、summaryを保管した。
実DBの照合ではsteeringの受付・message・semantic mirrorのdigestが一致し、
follow-upの受付本文と次executionのtaskのdigestも一致した。
semantic mirrorはmetadata 588／592 bytes、rootのcontrol snapshotは各53 bytesで、本文やqueueを含まなかった。

## 保存と保持heapの実測

`go run ./spikes/i09-cost`で、ローカルproviderの生成を保持したprivate Coreに同じ128 KiBの本文を
1件または64件受け付け、cancelと全receiptの理由保存・settledまでを測定した。
Managerを生かしたままGC後のheapを比較した。入力1件／64件の双方で本文のcontents rowは1個だった。

| 入力 | 件数 | 終了後DB増分 | receipt metadata | 受付中heap増分 | 終了後heap差分 |
| --- | ---: | ---: | ---: | ---: | ---: |
| steering | 1 | 132 KiB | 0.62 KiB | 127.19 KiB | −22.87 KiB |
| steering | 64 | 200 KiB | 39.74 KiB | 308.08 KiB | +0.30 KiB |
| follow-up | 1 | 132 KiB | 0.83 KiB | 119.48 KiB | −56.16 KiB |
| follow-up | 64 | 256 KiB | 53.05 KiB | 158.80 KiB | +0.23 KiB |

DBの増分にはSQLiteのpage割当とreceipt／index／controlを含む。
steeringの共有本文はMessage JSON込み131139 bytes、follow-upはraw本文131072 bytes。
64件でも終了controlは54 bytesだった。heapの差分はGC・cache等による変動を含み、長期運用の上限を意味しない。
測定対象は入力受付と取消の保持であり、成長するモデルcontextやrequest snapshot、会話view全体のコストは含めていない。
sourceは[測定spike](../../spikes/i09-cost/README.md)、証跡は`dist/i09-live/cost/summary.json`。

## Linux配布物と再開

最後のCore停止理由の修正後に`python3 scripts/package-linux.py --out dist/i09-runtime-input-packages`で作成した。
最終archiveは`build-8vw8az7f/hjh-linux-amd64.tar.gz`、SHA256は
`45b3cb3d85c151e3448b9dca2d10e88e147640eedf163369a1d8a5c92e70e20f`。
Linux/amd64 static ELF、archive内144 fileのmanifest size／hashを確認した。

最終配布物で通し確認のprivate DBをcold再開し、3 turn、全entityのID・位置・本文の一致、
過去Coreのreceipt 4件のhistorical表示とmanagedByThisCore=false、追加model_request 0、Core／TUI終了0を確認した。
証跡は`dist/i09-live/cold-replay/`。確認後に`dist/hjh`／`dist/hjh-tui`へ反映した。
適用には利用者がCoreとTUIの両方を再起動する。

| binary | SHA256 |
| --- | --- |
| hjh | f66de8f20157070e80e06b7d4879f06401978be2464ebf4e6f7d0ac0d8bb1166 |
| hjh-tui | 0a11dfbe6ccb37d1e5b9afb8b9b9e037f2342c7caec053aad8ce8434f46b1583 |
