# I-02：子Agentへの委譲の実装・確認・review

2026-10-10。[計画](../plans/i02-child-agents.md)に沿って実装・focused test・実OpenRouter確認・
通常review・批判的reviewを完了した。Linux/amd64のCLI・TUI・配布物から試せる状態。
利用者も子Agent呼出しを確認し、次のインクリメントへの進行を指定した。

## 成立した製品動作

生成JS・外部tool・hookの `api.agents.run({task})` で、独立した子Agentへ作業を委譲できる。
`await` で結果を受け取り、`Promise.all` で複数の子を並行実行する。
子は実ファイルを読み、親はその結果を使って報告ファイルと最終回答を完成させた。
結果は `{executionId, outcome, finalText?, error?}`。
子の作業失敗は終了証拠を保存した構造化結果として返し、親が判断に使える。

子は独立した会話・Agent loop・QuickJS VM・journalを持つ。
workspace、親の実model／effort、各executionのstep上限を引き継ぎ、必要な文脈はtaskへ明記する。
親が解決した絶対config pathから子開始時に再captureし、親Workerのcaptureは維持する。

CLIの子進捗はID付きでstderrへ出す。streamのstdoutとTUIの親本文へ子本文を混ぜない。
TUIの `/executions` でparent／spawnを確認し、親 `/inspect` のchildrenから子IDを辿れる。
recordのordinal範囲を指定してもchildren一覧を切り落とさない。
子messagesをSessionへ直接採用せず、正常完了した親turnだけを採用する。

## 実装の境界と寿命

| 所有者 | 実装した責務 |
| --- | --- |
| `app.Run` | rootの設定・model・Session・開始処理 |
| `app.execute` | root／子共通のloop・Worker・構成bytes・messages・context・終了保存、rootだけ会話採用 |
| `app.delegation` | 子のconfig再capture、admission、共通実行、保存済み終了結果の返却 |
| `app.journal` | execution別の観測保存・call参照・親子relation。保存lockと共通の進捗転送lockを分ける |
| `jsruntime` | Agent専用call ID、Go操作への接続、外側call単位の子contextとGo job、VM ownerからのPromise処理 |
| `history` | 子の開始・終了・一覧・親子DTO。既存DDLのfieldとrelationを使う |
| CLI／TUI | ID付き子進捗、親本文の表示状態、共通操作による履歴閲覧 |

外側JSの成功・例外・取消で残る子をcancel・joinする。
子Workerの終了・終了保存が済んでから外側callを返し、次の親model stepへ進む。
既存host操作のdrainと子scopeを区別し、未awaitの子もこの境界で終了する。
利用者の停止とTUI終了も同じ経路を使い、親終了・DB closeの前に子を終了する。

履歴保存のerrorは共通Go状態へ保持し、JSのcatchで親成功へ変換できない。
元のJS例外と子の後処理errorが同時に発生した場合は両方を終了errorへ残す。
DDL v3のSHA-256は `6f7cf24c97ca4afe50ba6ab9de25cf8a1e3eb6ece364464af0bb0ae95e2b0eef` のまま。
子はcanonical SessionをNULLとし、root Session相関・親turn・parent／spawn参照を保存する。
保存fieldの詳細は[対応表](../design/history-payloads.md)に記載した。

## Focused testと静的確認

各確認は計画の製品動作に対応させた。

- 実loop・QuickJS・SQLite・ファイルを通した並行委譲、結果利用、親子relation、別inspect、Session再開。
- 指定config locatorの継承、親captureを維持した子の最新source使用、親の実model／effort継承。
- 子のmodule例外を保存し、親が構造化した失敗結果を使えること。
- 子終了の保存errorをSQLite triggerで制御し、JSでcatchしても親成功・会話採用にならないこと。
- 子開始を実commandで確認した後の未await JS成功／例外について、次の親model requestより先に子がsettleすること。
- 親取消時の子join、元の親JS例外と子後処理の保存errorの両方を保持すること。
- runtimeの専用Agent call ID・Promise.all・成功／例外／取消の子scopeと、既存host drainの維持。
- 子admission・readbackがSessionを変えないこと、子の非採用、既存root採用経路。
- CLIの子進捗が親stream本文を変えないこと、TUIの子進捗が親本文・model・stepを変えないこと、親子inspection表示。

`internal/app` の `TestDelegation*` と既存の実tool経路／Service再開test、
`internal/jsruntime` の変更経路、`internal/history` の子・採用経路、
`cmd/hjh`・`internal/tui` の表示・操作testをfocusedに実行し、並行部分はraceでも成功した。
採用したP2修正後も関連app／runtimeのfocused race確認を行った。
変更packageの `go vet`、format、空白確認を行った。全repository gateの反復は行っていない。

## 実OpenRouter・CLI

modelは利用者指定の `deepseek/deepseek-v4.1-flash`。
credentialは旧Henjiと同じXDG stateの参照先を製品transportから使い、値は記録しない。
offline fixtureや固定JSを実モデル確認の代替にせず、製品CLIへ自然文で依頼した。

Session：`8bf3db49-fe16-4a38-9e81-448ba471df44`。

| 操作 | Execution | 実結果 |
| --- | --- | --- |
| `run --stream` でsrc／docsを2子へ並行委譲 | 親 `80e165ae-2cc7-4aa7-9652-294bbffe7fa9`、子 `93b556be-a777-49cb-9475-836f5a2f31a2`・`3fd700d5-936b-4936-b4fb-acc998f153d7` | 実モデルがPromise.allを生成。子がファイルを読み、親が `report.txt` を作成・読戻し。親completed／canonical、両子completed／non_canonical。exit 0 |
| 子のcommand待機中にSIGINT | 親 `77273451-0c74-4813-a49c-6d53dfd97d94`、子 `d89e6017-d359-46c7-aa60-77430ea3ecbc` | ready fileで子開始を確認して停止。子は親より先にcancelled／settled、親もcancelled／non_canonical。exit 130。子shell消滅、sleep後のlate fileなし |
| 同Sessionを別runで再開 | 上記Session | 正常な `report.txt` を読み、`continued-i02.txt` へ同じbytesをコピー・確認。exit 0。取消turnと子会話は採用会話に入らない |

`report.txt` はsrcの `Alpha() → "alpha-value"`・`Beta() → 42` と、
docsの `--input INPUT`・出力 `report.txt` を正しく反映した。
並行委譲runは最初のstdout受信7.213秒、終了7.788秒で、終了前に親本文が到着した。
各子の構成ID・spawn call ID・terminal recordを照合し、別プロセスの親／子inspectから保存証拠を読んだ。
実ログ・時刻・inspect・DBはgit管理外の `dist/i02-live-01/` に保存した。
このCLI確認はレビュー修正前の候補で行い、修正後のbinaryは次のPTYと配布物で実provider確認した。

## 最終binaryの実PTY・TUI

120×40のPTYで同じSessionを再開し、自然文の委譲から実モデルが子を起動した。

- 親 `7c293f7b-5442-47c8-8003-24fe8ba8e233`、子 `052b7f61-5255-41d3-8af4-053731d63089`。
  子がsrcを読み、親が `tui-2-report.txt` を作成・読戻し。親だけcanonical。
- `/executions`、親 `/inspect ID 0 0`、子 `/inspect ID 0 3` を入力し、実行履歴の読出しを確認。
- 子の実commandのready確認後にEsc。親 `89036efe-af7a-402a-bffa-93cbdec30b12` と
  子 `deb20234-3815-4689-a4f4-942affad1147` はcancelled／settled。
  TUIは開いたままで、新しい依頼で `tui-2-after-stop.txt` の作成・読戻しを完了した。
- 別の子command待機中にCtrl+C。親 `6515bcd3-a8b8-45bd-9722-bfde505a304f` と
  子 `e6060ba6-5bea-4fd6-9b9e-8dffd5961726` はcancelled／settled。
  子は親より先に終了保存され、TUIはexit 0、alternate screenを復元して終了した。
  Esc・Ctrl+Cの両方で子shell消滅とlate file非生成を確認した。

PTY生ログ・操作後のterminal decoder記録・SQL照合は `dist/i02-pty-live-02/` に保存した。
decoderの日本語表示には描画再現の限界があるため、画面記録をpixel単位の表示品質確認とは扱わない。
操作結果、ファイル、親子終了順序、表示状態のfocused testを確認済み。
利用者も子Agent呼出しを確認したが、確認時のCLI／TUIの別や画面品質の評価は指定されていない。
使用した `dist/hjh` のSHA-256は `bc26ca16d0886de4a9e625cd3325218d895ae5f2cbf409ade7148ace3acc43a5`。

## Linux配布物

`python3 scripts/package-linux.py` でCGO無効のLinux/amd64静的配布物を作成した。

- archive：`dist/packages/build-m4upcnut/hjh-linux-amd64.tar.gz`、13,134,978 bytes
- archive SHA-256：`dd3325c8e7189f8b310896ed81ef15b6017c66ccafdeb0b8595cb56de4f38467`
- binary SHA-256：`16a20d6faa7da1f20b7169968548ab5aa68fecac628b16b6daaa29dff5bce150`

展開したmanifestの全141 fileを照合した。package用trimpath等のbuild設定により、上記開発binaryとはhashが異なる。
同梱configを `--config` で指定し、そのmodel既定値で実OpenRouterへ接続した。
親 `5c8521fd-b1b0-4fa8-af44-1c89ec00b92d` が子 `ed03a208-a873-4d71-b36d-bd50e4b50939` を起動し、
子が `input.txt` の `package-proof: 37` を読み、親が `package-report.txt` を作成・確認した。exit 0。
親だけcanonicalで、両executionのbuild IDは実配布binaryのhashと一致した。
親子の保存sourceから、指定した同梱config bytesを両方が使用したことも確認した。
実ログ・inspect・summaryは `dist/i02-package-live/` に保存した。

実ログ・DB・展開package・archive内容にcredential値が残っていないことを確認した。
今回のI-02配布実行はホストLinux上の確認。最小Linux環境の初期run検証は
[I-01の記録](initial-run.md)を参照し、I-02の最小環境再検証とは扱わない。

## 実装review

独立した通常reviewと批判的reviewをread-onlyで実施した。
対象は実装sourceと対応testの機能correctness・明示契約・現行製品経路・regression。
reviewerによるAPI実行やfull gateは行っていない。

通常reviewは必須findingなし。批判的reviewはP2を1件採用した。
親JSが例外になり、未await子の終了保存も失敗すると、取消分岐が親の元の例外を捨てる問題だった。
`jsruntime` の取消分岐でcontext・元例外・join errorを併せて保持し、
実VMとSQLite triggerを使うregressionで両原因と親の失敗・非採用を確認した。
一回のre-reviewで解消を確認し、追加findingはなかった。

計画の範囲内の実装・検証・reviewと、利用者の子Agent呼出し確認まで完了。
利用者は「typescriptよりずっと早い気がする」と所感を伝えた。同条件での性能比較は実施していない。
次はI-03の範囲を計画する。専用Agent定義、子ごとのmodel、detach・queue、他OS、公開はI-02の対象外。
