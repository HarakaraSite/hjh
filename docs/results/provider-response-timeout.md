# モデル応答待ちタイムアウトの実装・確認結果

2026-10-15。[承認済み計画](../plans/provider-response-timeout.md)。起点`0fd3c1a`。
利用者指示: 「実装テストコードレビューして」。

## 実装

- `protocol.ResponseTimeout`と要求単位の`responseWait`を追加。
  Do直前から最初のBody非空readまで300秒、以後Bodyの各read待ち600秒を既定とする。
  ヘッダで開始時計をリセットしない。コメント/heartbeat/分割tool引数等も受信に数える。
  parser/observer/保存処理中はidle時計を停止し、総生成時間の上限は設けない。
- 要求専用contextの取消、同期Do/read終了、Body closeとtimer callback join後にGenerateを返す。
  自前timeoutは既存`provider_timeout`、外側取消は`turn_cancelled`。
  既知の非2xx statusと実認証失敗、保存失敗はtimeoutへ置換しない。
- `providers.<id>.responseTimeout.{startSeconds,idleSeconds}`をcaptured JSONから共通接続へ渡す。
  省略は300/600秒、0はその時計無効、正値は秒（小数可）。負値はprovider名付き設定エラー。
  親子で共通適用、projectRequest補助生成は現行の一度だけ呼出しを維持。
- OAuth refresh/discoveryの成功HTTP本文readが取消された場合のみ、取消/期限sentinelを保持する。
  原因を破棄してmissing_credentialにしていた箇所を、Do内の自前timeoutへ接続するために変更。
  JSON decode詳細やtoken response bytesをエラーへ展開しない。
- 既存の追加1回・固定2秒のretry、superseded/incomplete保存、TUI、recallを利用。
  完了toolや子全体の再実行、schema変更、新しい取消/再送ownerは追加していない。

## Focused確認

S1の実HTTP testと並行部race確認、S2の親子/設定/認証接続のfocused確認は成功。

- ヘッダ前/ヘッダのみ/本文・thinking・heartbeat後の停滞、ヘッダ受信で開始時計をresetしないこと。
- コメント・分割tool引数を含む受信継続には総時間上限が掛からず、observerの遅延/失敗をtimeoutにしないこと。
- Body closeを意図的に待たせ、Generateがtimer callbackの完了をjoinすること。
- 401/503エラー本文の途絶でもHTTP分類を保持し、credentialを観測errorへ露出しないこと。
- captured JSONの省略/override/0無効と負値のprovider名付き説明。補助生成はtimeoutでも一度だけ。
- 既に完了したtool→次要求timeout→同一wireの再送成功。過去toolは一度だけ、失敗試行の未完toolは非実行。
- 失敗本文/thinkingをsupersededで保持しcanonicalへ混入しない。429の後にtimeoutでも二試行で停止、最後はincomplete。
- 子timeoutの再失敗後に子がfailedでsettleし、collectが戻って親が完了。
- 実connections OAuth binding越しのrefreshヘッダ待ち、refresh/discoveryの200本文途絶がtimeout→一回再送で回復。
  実認証401/外側取消はそれぞれmissing_credential/turn_cancelled、再送なし。

主なコマンド（いずれも成功）:

```sh
go test -race ./internal/provider/protocol -run TestResponseWait -count=1 -timeout=45s
go test ./internal/provider/protocol -run 'Test(Generate|Cancellation|ResponseTimeout)' -count=1 -timeout=30s
go test ./internal/app -run TestResponseTimeout -count=1 -timeout=90s
go test -race ./internal/app -run TestResponseTimeout -count=1 -timeout=90s
```

## 独立Core・実PTY TUIの総合確認

artifact: `/tmp/hjh-response-timeout-s3-WADRDuNz/`。独立Core、分離XDG、使い捨てworkspaceと新規v4 DB、local HTTPのみ使用。
`check.py`、`result.json`、`short/`のANSI出力/DB/provider eventsに記録。

| 操作 | 結果 |
| --- | --- |
| 親start待ち→再送成功 | 設定0.8秒に対し約0.800秒で旧接続終了。同wireを切断後2.006秒で一度再送し完了 |
| 子本文/thinking後途絶→再失敗 | idle 0.6秒に対し約0.602秒、切断後2.005秒で同wire再送。子failed→collect復帰→親completed |
| 親二試行失敗→復元 | FAILED/recall案内。supersededは非表示、最終partialだけ表示 |
| 引数なしrecall→次依頼 | 最後のpartialのみ参考入力に入り、正常完了 |
| 実PTY F1取消 | turn_cancelledで通信終了、再送なし |
| 独立Core shutdown | 取消/終了保存/joinを確認、再送なし |
| 省略既定start 300秒の実時間確認 | 300.000203秒でEOF・provider_timeout。retry notice後F1で二回目送信を抑止 |

短縮suiteのlocal provider要求は11回。保存errorとserver EOFの対応、保存retry→次startの2.001〜2.002秒も照合。
失敗本文/thinkingのsuperseded/incomplete、retryAttempt=1の別物理ID、失敗親子non_canonical、
spawn/collect/run_js各一回をDBで確認した。省略既定の実時間suiteはlocal要求1回。
短縮11回と合わせ計12要求、suite再実行なし、外部provider呼出し0回。
検証用Core/TUI/harnessは全て終了済み。既定600秒idleは実時間では反復せず、S1の既定配線と短縮実HTTPで確認した。

## コードreviewと最後の全体確認

独立review完了、findingsなし。時計、取消/auth分類、callback join、非2xx、設定/親子/retry/補助生成の実経路を静的確認。
reviewerはtest/full gateを実行していない。

coordinating ownerが安定候補に対して下記を一回実行した:

```sh
go test ./internal/... ./cmd/... -count=1
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45 ./internal/... ./cmd/...
git diff --check
```

- staticcheck、diff checkは成功。
- 全体Go testは全`internal/...`と`cmd/hjh-tui`が成功したが、`cmd/hjh`の既存
  `TestConnectedRunSharesActiveSessionAndPreservesCLIOutput/stream=false`が
  `remote_run_test.go:154`の会話進捗待機で失敗。したがって**全体test成功とはしない**。
- 原因切分けで当該testを単独実行すると一回は成功、10回反復では8回失敗。
  今回変更したproduction source 6ファイルをHEAD内容へ差し替えるread-only Go overlayで
  同testを5回実行しても3回失敗し、同じline 154だった。
  overlay: `/tmp/hjh-timeout-baseline-overlay/overlay.json`。workspaceを巻き戻す操作は行っていない。
- 変更前にも再現するCLI接続/会話進捗待機の不安定さとして報告し、今回勝手に修正していない。
  具体的原因と実利用での影響は未確定。全体gateは繰り返さず、上記のfocused比較だけ実施した。

## 続報: CLI接続testの既存失敗修正

2026-10-15、利用者が「全体テストには既存CLI接続テストの失敗が ＞修正しようか」と対応を指示。
前節は当初の全体確認結果として保持する。下記修正後の全体testは成功した。

### 原因と変更

CLI→Core→executiondata/history→conversation→SSE→別attach client observerまでの経路を追い、
`/tmp/hjh-connected-debug/overlay.json`でtest側の受信・破棄イベントを記録した。
失敗回は本文付き`run.update revision=7 text=README `をobserverで受信済みだったが、
容量8のtest channelが満杯で`default`へ入り、そのイベントを破棄していた。
待機側は5msごとに一件しか取り出さず、startupイベントのburstに依存して失敗していた。
この失敗について製品側の本文欠落は確認されなかった。

`cmd/hjh/remote_run_test.go`だけを修正し、callback内でsnapshot/update双方の既存本文条件を判定し、
`sync.Once`とchannel closeで検出を通知する。イベント数に依存する中継queueを廃止した。
待機上限5秒、途中本文の完全一致、同一active Session/Run、stream/nonstream stdout、
終了保存・Session再開の確認は維持。製品コードやprovider応答は変更していない。

### 確認

- 対象testの20反復: 成功。
- `go test -race ./cmd/hjh -run '^TestConnectedRun' -count=1 -timeout=60s`: 成功。
- 独立review: findingsなし。検出条件/出力契約を維持し、raceや新たなdropがないことを静的確認。
- 原因修正後にcoordinating ownerが全体testを一回再実行する具体的理由が成立したため、
  `go test ./internal/... ./cmd/... -count=1`を実行し**全package成功**。
- staticcheckと`git diff --check`も成功。

実provider・常用Core/DB/config/distの操作、commit/push/配置は行っていない。

## コミット・プッシュ・配置

実装/test/review後、利用者が「コミットプッシュ配置して」と指示した。
本機能、CLI接続test修正、関連計画/結果/設定文書/依頼記録をコミットし、origin/mainへpushする。
配置先は従来どおり`dist/hjh`と`dist/hjh-tui`。commit revisionを埋め込んで一時pathへbuildし、
version/hash照合後にrenameで配置する。稼働中Core/TUIは停止せず、Core再起動は利用者が行う。
既存workspace configは書き換えず、省略既定300/600秒を次の新Coreで利用する。

## 範囲・制約

実装・検証段階では実provider呼出し、常用Core/DB/config/dist変更、commit/push/配置を行っていない。
後続指示で上記commit/push/バイナリ配置のみ承認された。実provider呼出し、常用Core停止、DB変更は行わない。
実HTTP/local providerの確認は通信取消・retry・保存・UIの成立を確認するものであり、
実providerで長考を誤停止しないことや待機改善効果の実証ではない。
300/600秒は暫定値。heartbeatのみが続く停滞は終了せず、正常でも10分無受信なら終了し得る。
