# I-13：TUIから未起動Coreを起動する

2026-10-12。利用者は「Coreが未起動でもTUIから使い始めること」を先に進めると指定した。
本書を今回の実装入力とする。Core再起動をまたぐ画面復帰とOS起動時のservice登録は後続である。

実装とfocused確認、実Core／TUI、Linux配布物、限定reviewを完了した。
[確認結果](../results/i13-core-startup.md)を参照する。

## 利用者が得る動作

- SSH先のVMで`hjh-tui`を起動すると、同じVMの`http://127.0.0.1:8765`へ接続する。
  Coreが未起動なら、配布物の同じdirectoryにある`hjh serve`を独立processとして起動し、
  HTTPの`GET /api/v2/core`が利用可能になってからTUIへ進む。
- 起動済みCoreは再利用する。二つ目のTUIも同じCoreへ接続できる。
- `--endpoint URL`は従来どおり明示接続先へ接続する。明示URLでも同じVMのCoreを起動したい場合は
  `--start-core`を指定する。`--start-core=false`で既定の接続先にも接続だけを行える。
- 新規Coreの`--workspace`は現在directoryを既定とする。`--db`、`--config`、
  `--credential-file`、`--credential-state-dir`、`--model-favorites-dir`を起動するCoreへ渡せる。
  `--core-binary`で起動するbinaryを指定できる。値はargvで渡し、shellを介さない。
  既存Coreへの接続では、そのCoreの既存の起動設定を使う。
  TUIで`--workspace`を省略した場合は接続したCoreの既定workspaceを使い、明示した場合は次の新規runに使う。
- Coreは端末・TUIの寿命から独立する。`/detach`／Ctrl+Dの後もCoreとrunは継続し、
  TUIを再度起動して`--session ID`やSession pickerから既存の再開経路を使える。
  `/quit`／Ctrl+Qは従来のCore停止・保存joinのままとする。
- 起動中の状態と、失敗時に参照するCore logのpathを表示する。
  logは実効DBと同じdirectoryの`core.log`へ追記する。起動待ちはCtrl+Cで終了できる。
  起動したCoreをTUIのcontext取消で停止しない。

## 根拠と現在のproduct経路

利用者の現環境はGhostty → SSH → tmuxである。TUIとCoreをVM上で動かす今回の入口に接続する。
現状`cmd/hjh-tui`はendpoint必須でapiclientを作り、`tui.Run`がCore情報を取得する。
Coreが未起動ならここで終了する。Coreの起動は別端末の`hjh serve`で行う。

CoreのDB、credential、実行、保存、HTTPは`cmd/hjh` → app／corerun／httpapiが既に所有する。
TUIは公開APIのみを使う。I-07のdetach・Session再開、I-12の認証保存を置き換えず、
起動前にCoreが用意されるようにする。

現行serveはDBを開いてからlistenする。複数TUIが未起動Coreを見て同時に起動すると、
portを得られない候補もDBを開くため、listenをDB初期化より先へ移す。
これは起動候補の所有者を実際のlistenで決め、負けた候補に保存処理を始めさせないための変更である。
PID file、別のSession／credential保存先、OS serviceは追加しない。

## 責務と実装範囲

| owner | 責務 |
| --- | --- |
| corelaunch | Core availability確認、起動argv、独立process、起動logとHTTP readiness待ち。Core実装をimportしない |
| cmd/hjh-tui | endpoint／起動optionを解釈し、必要な場合だけcorelaunchを呼び、従来のTUIへ接続する |
| cmd/hjh | serveのlisten所有をDB初期化より先に確定する。実行・保存・停止経路は既存ownerを使う |
| TUI／apiclient | 起動後の表示・実行・detach／Session再開。今回新しい再接続stateを加えない |

## 確認

Core未起動からの起動、起動済みCoreの再利用、渡したpathによる実効config／DB／credential state、
独立processとしてのdetach後の継続、Core停止後の同じDBからの再起動・Session読戻しを確認する。
起動に失敗した場合のlog案内と、起動待ち取消も確認する。

実装後にこれらに対応するfocused test／race、関連vet、format、diff checkを行う。
private workspace／port／DB／state／tmuxで、人がTUIから起動・model選択・依頼・detach・再開を行う。
実providerは保存済み認証を再利用し、追加ログインと有料runは行わない。
Linux配布物から同じ入口を確認し、結果を`docs/results/i13-core-startup.md`へ記録する。
processの独立性、起動optionの所有と既存detach／停止のregressionを対象に、一回のbounded reviewを行う。

Coreが落ちた後のTUI内の画面復帰、未確認taskの新Coreへの再送、OS service、remote SSH commandの起動、
HTTP本文／context予算、tool詳細browser、色・Markdown装飾は今回の範囲に含めない。
