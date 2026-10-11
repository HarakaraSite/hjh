# I-13：TUIから未起動Coreを起動する

2026-10-12。[採用計画](../plans/i13-core-startup.md)に沿って実装・確認した。
利用者のGhostty → SSH → tmuxの環境では、SSH先のVMでTUIだけを起動して使い始められる。

## 実装した操作と責務

`hjh-tui`はendpoint省略時に`http://127.0.0.1:8765`へ接続する。
Coreが未起動なら同じdirectoryの`hjh serve`を起動し、公開Core APIのreadiness後にTUIへ進む。
起動済みCoreは先に再利用するため、起動binaryやlogの参照は行わない。
`--endpoint URL`は接続のみ、明示URLでの起動は`--start-core`、既定先への接続のみは`--start-core=false`を使う。

新規Coreのworkspaceは現在directoryが既定。`--workspace`、`--db`、`--config`、
`--credential-file`、`--credential-state-dir`、`--model-favorites-dir`を起動argvへ渡せる。
`--core-binary`でbinaryを指定できる。既存Coreの起動設定はそのまま使い、
TUIでworkspaceを省略した場合は接続したCoreの既定workspaceを使う。

`internal/corelaunch`が公開HTTP APIの確認、argv、独立process、起動logとreadinessを所有する。
Core実装や履歴DBのownerをTUIへ持ち込んでいない。
CoreはLinuxの新しいsessionで起動し、stdinを端末から分離、stdout／stderrを実効DB隣の`core.log`へ追記する。
TUIや起動待ちcontextの終了によってCoreを止めず、既存のdetach／Session再開／Core停止を使う。
CoreのserveはlistenをDB初期化より先に行い、同時起動の負け候補がDBを開かないようにした。

## 確認したproduct経路

private workspace／DB／port／tmuxで、候補CoreとTUIを使って次を確認した。

- 未起動からTUIが同じdirectoryのCoreを起動し、API readiness後に会話画面へ進む。
  Coreのsession IDが自身のPIDであり、stdinは`/dev/null`、stdout／stderrは起動logだった。
- 明示DBだけが作られ、明示configとcredential stateに保存した登録キーを再利用した。
  二つ目のTUIで同じCore IDとPIDを再利用し、二つ目のdetach後もCoreが続いた。
- TUIから依頼し、active run中にCtrl+Dでdetach。同じSession IDへ再接続し、
  完了した回答`I13_TUI_STARTED_CORE_OK`を表示した。Ctrl+QでCoreを停止できた。
- 同じportへ二つのTUIを起動し、DBのexclusive lockを3秒保持した後でも両TUIが接続した。
  初回に片方のTUIだけが早期終了した実行を保存し、修正後に同じ経路で解消を確認した。
- 最終Linux配布物を用い、既定portが未使用であることを確認して、endpointとstart-coreを省略して起動した。
  private DB／workspaceを明示し、Coreのcold起動後に同じSessionと完了済みexecutionを読み戻した。

実providerには保存済みOpenRouter登録キーを使い、実行前に
[公式catalog](https://openrouter.ai/api/v1/models)の`openrouter/free`でprompt／completion単価がゼロであることを確認した。
新しい認証や有料runは行っていない。
Sessionは`d6339419-e099-4958-9cf1-a39bdbe9d8c3`、
executionは`3ac7318a-bcae-4471-801c-bbf059f63dda`、outcomeはcompleted。
private DB、Core log、pane、公開JSONに登録APIキーの生値がないことを照合した。

証跡はgit管理外の`dist/i13-live/`に保管した。`summary.json`、`executions.json`、
`concurrent-before-summary.json`、`concurrent-summary.json`、`tui-*.txt`、
`secret-exposure-summary.json`に結果を記録した。確認用Coreとtmux画面は終了し、利用者の`main`は変更していない。

## focused確認とreview

`go test -race ./cmd/hjh -run 'TestServe' -count=1`、
`go test -race ./cmd/hjh-tui ./internal/corelaunch`、関連packageの`go vet`、gofmt、`git diff --check`は通過した。
Core再利用、起動argvとpath、readiness、起動失敗のstatus／log案内、起動待ち取消後のprocess継続、
競合相手のreadiness待ちとport消失、port占有時にDBを開かない経路を対象とした。
full gateは実行していない。

process寿命、起動設定のowner、既存detach／quit／保存経路を対象に、一回の限定reviewと限定再確認を行った。

| 採用指摘 | 修正・確認 |
| --- | --- |
| P2：既存Core再利用時に、省略workspaceへTUIのcwdを明示してCore既定値を上書きする | 起動用workspaceとTUIの明示workspaceを分離。既存のCore workspace fallbackを維持する |
| P2：同時起動の負け候補が、勝者のDB初期化中にTUIを終了する | 子のexitを保持し、HTTP応答がなくTCP listenが続く間はreadinessを待つ。非Core HTTP応答またはport消失時は元のexit statusとlog案内を返す |

再確認で両指摘の解消を確認し、追加の採用対象findingはなかった。
reviewerはsource、testの内容、実行記録をread-onlyで確認し、test、network、credential読取りを行っていない。

## 配布物と範囲

最終CoreとTUIはLinux/amd64のstatic ELF。archiveとmanifestの144fileのhash／sizeを照合した。
archive SHA-256は`0447e92325e092d7d264a2cdde04fa4c522b11ff6f9ac4cd3f95c109920bc152`。
`dist/i13-live/package-summary.json`へ記録し、`dist/hjh`と`dist/hjh-tui`をこのbinaryへ更新した。

稼働中TUIがCoreの再起動をまたいで画面復帰する機能と、OS起動時のservice登録は後続である。
今回の再起動確認は、停止したCoreを新しいTUI起動で用意し、保存済みSessionを読み戻す操作である。
