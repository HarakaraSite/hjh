# I-16: CLIからCoreへのrun接続の確認結果

2026-10-12。利用者の採用した「CLIで始め、後からTUIで同じ作業へ接続する」を実装した。
[操作契約と確認方針](../plans/i16-cli-core-run.md)を参照する。

## 実装

`hjh run --connect URL "依頼"`は既存apiclientのStartRunとSSEで起動済みCoreへ依頼する。
接続runの分岐はローカルDB・設定・認証の初期化より先に置いた。
接続先省略は従来のローカル実行。CLIの入力読取りは両経路で共用する。

Core/Run/Session/Execution IDと進捗をstderr、最終回答をstdoutへ出す。
--streamでは親assistant本文をentity IDと表示済みbytesから差分出力し、再接続snapshotと
最終snapshotを二重表示しない。モデルstepの別entityは別本文として表示する。
thinking・子本文はstdoutへ混ぜず、tool・子executionの進捗はstderrへ出す。
CLIには表示metadataだけを持たせ、Coreの実行・保存・予算管理は複製しない。

明示selection/role/session/workspace/max-steps/from-turnを既存requestへ渡す。
workspace省略はCore既定、明示相対pathはCore基準、Session再開は保存workspaceを使う。
max-steps/from-turnは明示0と省略を区別する。DB/config/credential等はCoreの起動設定なので、
接続runに明示した場合はCore起動時に設定する案内を出す。

Ctrl-C/SIGTERMは明示Cancelを送り、保存と最終会話配信の完了まで待つ。
通信切断やCLI強制終了は取消へ変換しない。出力writer失敗時もCoreを自動取消しない。
HTTP API、保存schema、TUIの実行・再接続・追加入力経路は変更していない。

## 実利用経路

private Core/DB/workspace/XDG state/tmuxを用意し、既存OpenRouter credentialを参照した。
実行前の公開catalogでopenrouter/freeのprompt/completion単価0を確認した。
追加認証・有料model・利用者の稼働Coreやtmux mainの操作はしていない。

- CLIからREADME読取り・待機コマンド・日本語要約を依頼した。
  stderrのSession IDを使って別TUIを起動し、進行中の依頼とtoolを表示した。
- 確認用CLIをSIGKILLで強制終了し、Coreが実行を続けることを確認した。
  最終回答I16_CORE_OKがCoreへ保存され、接続済みTUIへ表示された。
  強制終了した非stream CLIのstdoutには未完了の回答を出していなかった。
- CLIから同じSessionへ次の依頼を送り、前のREADME要約を踏まえたI16_CONTINUE_OKを
  stdoutへ出し、2turnの保存を確認した。historyでも両回答を読めた。
- 別の接続runで--streamを使い、I16_STREAM_OKがstdoutへ一回だけ出た。

最初のtmux確認scriptは折返されたSession IDを見落としたため、画面の改行を除いてIDを照合する
確認方法へ修正した。TUI実装の変更は不要だった。

## 検証とreview

CLIのreal HTTP API/Manager/Service/provider adapterを接続したfocused testで、
stdin、明示selection、Core既定workspace、明示0と省略、相対workspace、Session保存workspace、
CLIと別clientの進行中購読、非stream最終回答、stream途中本文、続行と保存、取消保存待ち、
実行準備失敗のexit 1とstdout空を確認した。接続CLIがローカルfileを作らないことも確認した。
表示testではcoalesced upsert、snapshotの重複、複数step、thinkingと子本文の分離を確認した。

変更したCLI packageのrace付きtest、対象go vet、format、git diff --checkを確認した。
独立reviewではcallback/writerの寿命、Cancelと最終SSEのjoin、option転送、表示を確認し、
対応を要するcorrectness findingはなかった。reviewerのstdout=/dev/fullの実CLI確認でも、
exit 1と識別子・原因を表示し、Cancel POSTを送らなかった。

## 配布バイナリ

Linux/amd64 archiveを作成し、展開した144fileのhash/sizeと両バイナリのstatic ELFを確認した。
--versionの実hashとsource metadataがmanifestと一致し、空workspace/XDG stateへfileを作らない。
展開したCore/CLI/TUIのペアで同じ保存Sessionを続行し、I16_PACKAGE_OKをstdoutへ一回表示した。
接続時のworkspace既定値をhelpへ補足した最終buildでもI16_FINAL_PACKAGE_OKをstdoutへ一回表示し、
4turnの保存、TUIに以前の回答と新回答が表示されることを確認した。
この確認済みペアをdist/hjhとdist/hjh-tuiへ反映した。

archive SHA-256: `f8d4bb2fc68420ce1a5da49703d0f928aa120d0a5abb3f9ad3b61bbdd7ec345b`

source revision: `010e9719e676205ae5f9affa9f65143092c0ac08`、dirty: true。
この配布buildはI-13〜I-16を含むlocal変更が未commitの状態で作成した。

接続runは起動済みCoreを指定する。Core自動起動やCLIのdetach操作の追加、
Core起動設定の遠隔変更は今回の範囲に含めていない。
