# I-07：Session選択・管理とdetach／再接続

2026-10-11。実装・focused test・通常／批判的review・実provider／PTY確認を完了した。
利用者から「確認しました」との利用確認を受け、次のインクリメントへ進む。
[計画](../plans/i07-session-management.md)、[TUI操作](../design/tui.md)、[HTTP契約](../design/http-api.md)を参照する。
利用者指示に従い、`/view`は追加せず、`/tool`の詳細取得は現状維持した。

## 利用者ができること

- F4／`/sessions`で現在Sessionを初期選択し、名前・ID・workspace・model・turn数・実行中の印を読む。
  矢印／j/k、PgUp/PgDn、Home/Endで選び、Enterでresume、Escでcloseする。
  一覧の読取中でもEscで閉じ、遅い応答で再び開かない。
- `/rename TEXT`で現在Sessionを命名する。実行中でもtitle／updated_atだけを保存し、
  正常なturn採用のstate_revisionを変更しない。旧DDLと既存保存先を使う。
- `/detach`／Ctrl+DでTUIを離れる。新runはCoreで受理され、保存済みSession IDが届くまで待つ。
  保存済みIDがあればpreparing中でも離れられる。cancel APIは送らない。
  Session／run IDを、未確定のthinking／assistant本文とは別に終了時の確定行として端末へ残す。
- `--session ID`／`/resume ID`／一覧選択で、Coreの既存Manager.sessionsから同じactive runを捕捉する。
  保存済み会話を読み、同runのGET＋SSEへattachする。新しいStartRun POSTは送らず、同entity IDをmergeする。
  別Sessionへの切替は既存のcancel・終了待機を維持し、同じSessionの再選択ではrunを止めない。
- F1は現在runのcancel。`/quit`／Ctrl+QはCore停止に切り替わった。
  停止APIは短い202応答をflushし、新規受付を止め、Core側で全runと子Agentをcancel・joinし、
  保存とService終了後にHTTPを閉じる。TUIはCoreのHTTP終了を待つ。
  SIGINT／SIGTERMも同じ保存・join経路を使う。

Coreのactive run索引、会話の順序・差分・保存所有者は既存のものを使い、新しい保存先やworkerは追加していない。
Session metadata更新、実行管理、通信、TUIの操作をそれぞれのmoduleへ置いた。

## 確認

Core側はhistory／corerun／httpapiのfocused確認とserve停止testを実行した。
rename中のturn採用、active run取得と一覧／単体metadata、終端後のnull、停止受付がjoinを待たないこと、
cancel終端のDB保存を確認した。対象のrace確認、vet、format、diff checkは成功。

クライアントとTUIは`go test -race ./internal/apiclient ./internal/tui`で確認した。
GET attach、Core終了待機、保存済みID通知でのdetach、cancel非送信、同Session選択、
履歴とlive entityのmerge、長い一覧の選択位置、rename、loading中Esc、streaming中の終了通知を確認した。
関連vetと`git diff --check`も成功。full gateは実行していない。

実確認は検証専用Core／DB／tmuxと、OpenRouterの`deepseek/deepseek-v4.1-flash`で行った。
利用者のDB・稼働中Core／TUI・tmux・設定・credentialは変更していない。

| 実経路 | 確認した結果 |
| --- | --- |
| 実行中rename、F4で同Session選択 | 名前が保存され、runをcancelしない |
| 実行中detach | 切断前後ともrunning、TUI終了0、cancel POSTは0 |
| 新TUIで同Sessionへ再接続 | 同runへGET／SSE。attach区間の新規run POSTは0 |
| 完了・次の依頼 | 最終本文は1回、同Sessionで次runが完了し採用turn数2 |
| 実行中Ctrl+Q | Core／TUI終了0。DBの3 executionはcompleted、completed、cancelledとしてsettled |
| thinking生成途中のdetach | 未確定本文があってもSession／run IDの終了通知が1回残る。cancel POSTは0 |
| 配布物のcold Core／TUI再開 | 名前と2 turnを保持。初回runのentity値は一致し、保存された2つのassistant本文を各1回表示 |

証跡はgit管理外の`dist/i07-live/session-management/`、`streaming-detach/`、`package-replay/`に保存した。
実Sessionは`69692eac-430f-4869-80dd-dc87804aeedb`、初回runは`190578a7-5811-4f5b-b090-98e90ae01a39`。
HTTP操作記録、pane、saved view、summaryで上記結果を追える。

検証ハーネスには、末尾DB集計の列名誤り、run IDの正常な端末折返し、ユーザーの引用とassistant本文を
同じ文字列として数える誤ったassertがあった。保存済み証跡／既存DDLと役割ラベルで集計を修正した。
追加streaming検証は終了通知の確認後にハーネスfinallyがprivate CoreをSIGINTで停止したため、
その実行では後続attachを行っていない。detach／attach／継続の一連経路は先の実確認で成立している。

## review

通常reviewと批判的reviewを別reviewerで実施し、旧Henjiの`remote_session.ts`、API client、
Core service、HTTP shutdown workerの現行経路と対比した。

| 採用finding | 修正・再確認 |
| --- | --- |
| P2：保存済みSessionでもpreparingを除外してdetachを遅らせる | Session IDの保存通知で離脱可能にし、preparingのfocused testで確認 |
| P2：streaming本文の後ろに置いた終了通知がflushから漏れる | 終了通知を会話entityから分離し、確定出力へ追加。writer testと実PTYで確認 |
| P2：loading中Esc後の応答でpickerが開く | loading時にpickerを開き、Escで既存jobを取消・IDを破棄。遅延応答を無効化 |

各reviewerの一回の限定再reviewで解消を確認した。変更範囲に残る必須findingはない。
通常reviewの限定focused testと批判的reviewのfocused race確認も成功した。

実装中に、Coreのshutdown開始後に直接Manager.Getでterminal結果を読むと、cancel済みmanager contextで
取得に失敗する既存経路も確認した。今回の停止契約ではDB保存後にHTTPを閉じ、TUIはCore終了を待つため、
これを追加修正していない。停止途中の結果取得を別途整える場合の確認点として残す。

## Linux配布物

`python3 scripts/package-linux.py --out dist/i07-session-packages`で作成した。
`build-78ebsxg7/hjh-linux-amd64.tar.gz`のSHA256は
`e8d3de02778badc6156d7f1d98877cde7734d2fef9c596b17f83834469198d7a`。
144 fileのmanifest hash／sizeと、Linux/amd64のstatic ELFを確認した。
配布CoreとTUIそのものでcold replay、F4、Ctrl+Qを確認した後、`dist/hjh`／`dist/hjh-tui`へ反映した。

| binary | SHA256 |
| --- | --- |
| hjh | 93d476a141d88b46be7315eb1ebcf0c705e7ecb4d93d812b4ee3f7f852587d23 |
| hjh-tui | c0ae916c35cc248d7cef00514a24d99824c4973558bc06ee1be14c7b56cfb3cd |

稼働済みprocessは更新されないため、新しい操作の確認にはCoreとTUIを再起動する。
