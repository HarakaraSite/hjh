# I-16: CLIからCoreへrunを依頼する

利用者はCLIから依頼を始め、後からTUIで同じSessionへ接続して進捗確認・追加指示・続行できる
動作を採用した。最終回答stdout、進捗と識別子stderr、--streamの逐次表示も維持する。

## 操作と所有者

`hjh run --connect URL "依頼"`は起動済みCoreへ既存POST /api/v2/runsで依頼する。
依頼は引数またはstdin。接続先省略は現在のローカル実行を維持する。
Coreは既存Manager・Service・historyで実行、Session予約、保存、追加入力、取消を所有し、
CLIは既存apiclientでrun SSEを購読する。HTTP schema・DB・TUIの実行経路は増やさない。
CLIがstderrへ出したSession IDをTUIの--sessionまたはSession pickerで開くと既存runへ接続できる。

明示selection/role/session/workspace/max-steps/from-turnは既存StartRunRequestへ渡す。
workspace省略時はCore既定、新規で明示した相対pathはCore既定workspaceを基準に解決する。
Session再開は既存Serviceが保存workspaceを使う。max-steps/from-turn省略はCore起動設定を使い、
明示0もoverrideできるようpointerを保持する。
DB/config/credential file・state/favorites等はCoreの起動設定であり、接続runではCLIから
変更しない。これらのlocal flagを接続runへ明示した場合はCore側の設定であることを案内する。
CLIではDB・拡張・credentialを開かない。

## 出力と終了

Core ID/Run IDと判明したSession/Execution IDをstderrへ表示する。
--streamなしは正常完了の最終回答だけstdoutへ出し、--streamは親assistant本文の差分を出す。
公開thinkingや子本文はstdoutへ混ぜない。tool/子の進捗はstderrへ出す。
snapshotとupsertの同じentityを識別し、再接続snapshotや最終snapshotで本文を二重出力しない。
モデルstepごとに別のassistant entityを扱う。CLIに予算・実行状態ownerを複製しない。

成功exit 0、実行失敗1、取消130。Ctrl-C/SIGTERMは明示Cancelを送り、Coreの終了保存と
最終会話配信後に終了する。取消送信が失敗したときは通信を閉じて原因と識別子を表示する。
通信切断・CLIの強制終了は取消へ変換せず、既存apiclientの再接続とCore継続を使う。
表示writer失敗時はCLI通信を閉じてerror終了し、Coreの作業を自動取消しない。

## 確認

CLIの実HTTP/Manager/Service/provider adapter経路で出力と保存、stdin/selection、stream途中出力、
Ctrl-Cに相当するcontext取消と終了保存待ちをfocused確認する。
表示では再接続snapshot・複数step・子本文の分離を具体的動作として確認する。
private Core/DB/workspace/tmuxと既存認証を使い、CLIから始めた実作業をTUIで途中から開く。
CLI強制終了後もCore/TUIで完了を確認し、保存Sessionを次runで続行する。
API credential/Authorizationは出力しない。追加認証・有料model・利用者の稼働Core操作・公開は行わない。
対象focused test/race/vet/format/diff、独立した限定review、配布バイナリで確認する。
