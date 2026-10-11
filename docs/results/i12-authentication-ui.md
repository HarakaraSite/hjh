# I-12：TUIの認証・アカウント選択

2026-10-12。[計画](../plans/i12-authentication-ui.md)と
[保存・account・attempt契約](../design/i12-authentication-contract.md)に沿って実装した。
認証保存、app、HTTP／client、CLI、TUIを接続し、保存済みChatGPTの一accountと
登録APIキーで、人が画面からmodelを選び依頼を完了する経路を確認した。
利用者は実確認に「保存済みの1アカウントだけ」を指定した。
focused test／race、通常・批判的reviewと一回ずつの限定再review、採用指摘の修正、
最終Linux配布物でのcold確認を完了した。

## 利用者の操作

`/login`は現在の構成で使う認証profileを一覧する。`/login PROVIDER_ID`は対象の
APIキー入力またはChatGPT account一覧へ進む。同じprofileのproviderは一覧の一行にまとめ、
異なる認証方式を共有する場合は方式と操作用providerを選べる。
認証の登録・選択で次runのprovider／modelや会話draftを変更しない。

キーとcallback URLは専用の伏せ字editorで入力し、Enterで送信、Ctrl+Uで消去する。
キーは通常draft、画面、履歴、HTTP responseへ返さない。保存成功はCoreのfile公開後に表示する。
保存結果が不明ならmutationIdをGETで確認し、古い値を自動再送しない。

ChatGPT一覧は保存済みaccountを表示する。接続済み行のEnterは選択だけを保存し、
ブラウザ認可を開始しない。未接続行のEnter、`a`追加、`r`再認証はCore所有のattemptへ進む。
認可URLと専用callback入力、ローカルcallback、自動pollを同じattemptへ接続する。
画面を閉じた後もCoreの保存結果を読める。APIキーとaccount選択は通常のCore再起動後も残る。

## 所有者と保存

credentials.Managerが登録キー、account選択、attempt、token更新と停止joinを所有する。
appがSession／workspaceと実効config／profileを解決し、HTTP／clientが状態と操作を運ぶ。
TUIは表示・入力・応答の世代照合を担当する。履歴schema v3の変更はない。

`--credential-state-dir`はserve／run／models／auth共通の保存先指定である。
登録キーはそのroot下のconfig絶対path＋profileで識別するJSONへ保存し、
解決順は明示credential-file → 登録キー → env → 外部fileとなる。
外部credential参照fileやconfigをキー登録で書き換えない。

ChatGPTの標準recordは既存のOAuth保存先Bをそのまま使い、追加slotは別record／registrationを持つ。
一覧・選択のmetadataはB.accounts.json、lockはB.accounts.lockである。
一覧と選択はOAuth通信を起動せず、保存済みrefresh認証はcatalog／runの通常経路で再利用する。

run factoryはownerごとのキーまたはaccountを一回捕捉し、同ownerの並列子Agentへ共用する。
run途中の登録・選択はそのrunの認証を変えず、次runへ反映する。
OAuth request前のrefreshも捕捉accountへ接続する。
catalogは実際のowner／accountをheaderに返し、古いaccountの候補をTUIで混ぜない。

旧profile名pending、`/auth/login`・`/auth/complete`とapi.Loginをattempt経路へ置き換えた。
CLI loginも同じManagerで選択slotへ認可し、statusは現在の選択slotを投影する。
slash commandへcallback全体を貼る旧操作は専用画面への案内にした。

## 確認結果

credential保存・OAuth lifecycle、app／HTTP-client／CLIの登録とattempt、
root／並列子Agentの認証固定、TUIの専用入力・遅延応答をfocused testで確認した。
登録→実provider catalog→run→再起動はHTTP-client経路でも確認している。
同ownerのAPIキー登録・OAuth選択をrun途中に変えるtestは、子Agentとrootの継続を旧binding、
次の独立runを新bindingで確認する。OAuth側はsyntheticな別Subjectを使った確認であり、
別の実ChatGPT accountの確認ではない。
credentialsのrace、app／HTTP-client／CLIの関連focused raceと、TUIの採用修正に対応する
focused raceを通過した。関連packageのvet、配布build、format、`git diff --check`も通過した。
full gateは計画の完了条件にせず、変更したproduct経路の確認を行った。

private config／credential state／Core／tmuxで次を確認した。

- envだけを参照し実値が未設定のprofileへ、伏せ字editorから既存APIキーを登録。
  登録状態と実catalogを照合し、OpenRouterの公開価格がゼロの`openrouter/free`で依頼を完了した。
- 保存済みChatGPT標準accountの一覧・Enter選択。attemptを作らず、期限切れaccess tokenを
  通常refreshして実catalogを取得し、`gpt-6-luna`で依頼を完了した。
- 専用入力paneに実キーが表示されないこと、登録後に会話へ戻ること、catalog／実行結果の保存。
  private履歴DB、実行一覧JSON、全入力・実行paneにも登録キーの生値がないことを照合した。
- Linux flockを保持してCoreのキー保存を待たせ、client socketを切断してshutdownを202で受理。
  Coreは保存を待ち、lock解放後にmutationIdとrevisionを公開して終了した。

Sessionは`fdfe01c8-50ff-49c6-a9e4-03cc62faa2ee`。
ChatGPT executionは`8fc8d02a-62b9-4671-8e33-efb9c02a9c5a`、
登録キーexecutionは`e7cb9fbb-9add-4057-8434-29344deaf256`で、どちらもcompleted／canonical。
回答はそれぞれ`I12_SAVED_CHATGPT_OK`、`I12_REGISTERED_KEY_OK`。
証跡はgit管理外の`dist/i12-live/`にpane、private DB、公開状態JSON、保存join summaryを保管した。
既存外部APIキーfileを変更せず、ChatGPTは通常のtoken refreshだけを行った。

## reviewと修正

通常reviewは保存受付順、既存OAuth再認証、app／HTTP／CLIを確認し、
批判的reviewはTUI入力・寿命・応答世代、共有owner、catalogとrun bindingを確認した。
一般hardeningと未観測のfailure matrixは対象にしていない。
採用指摘は次の六件である。

| 指摘 | 修正 |
| --- | --- |
| P2：待機中GET取消が先行保存のbarrierを解放し、後続GETが保存を追い越す | callerは速やかに取消結果を返し、barrierとjoinは先行ticket完了までCore所有で保持する |
| P2：同じ明示oauthFileを別state rootから再認証するとhost IDが変わる | 既存recordのhost IDを捕捉し、既存client／registrationで再認証する |
| P2：begin応答待ちとdetachでattempt取消を送り切れない | client IDとownerを先に捕捉し、取消意図を返却IDへ接続。UI context取消とbackend.Closeから制御通信を分け、終了前に有限の通信をjoinする |
| P2：run終了によるselection世代更新で開いている認証画面が応答を受け取れなくなる | 操作所有者と一覧のSession世代を分離。自然なrun完了で操作を維持し、明示Session切替では新scopeへ再読取する |
| P2：同OAuth ownerの別provider aliasから未確認attemptを回復できない | Core／ownerで復元し、開始時のattempt targetと現在pickerのtargetを別に保持する |
| P2：account保存済み・選択失敗時に再認証を案内し、選択を回復できない | 部分保存を表示し、保存済み行を選んでEnterによる選択を案内する |

通常reviewの一回の限定再確認では、二件の解消と全六箇所のmetadata lock pathを確認し、
新規findingはなかった。reviewerはsourceとtestをread-onlyで確認し、live callやfull gateを実行していない。
批判的reviewの限定再確認では、Session load待ちで開いた画面へ旧Sessionの一覧が届く残留経路を採用した。
同じ限定再reviewの時間内に、Session読込後の新scope・新jobへの再読取、完了後の保留再読取、
部分選択回復の元ownerと現在Sessionの読取を修正し、対応regressionとsourceを確認した。
採用六件はすべて解消し、未解消の採用findingはない。

## 最終Linux配布物

Linux/amd64のCoreとTUIをstatic ELFとしてpackageし、archiveと全144fileのmanifest hash／sizeを照合した。
最終binaryを使って同じprivate config／credential state／DBからCoreをcold起動し、TUIでSessionを再開した。
登録キーのsourceとrevision=2、ChatGPTの標準accountと選択primary、完了済み二件の履歴を読み直せた。
実ChatGPT catalogは7件、登録キーによる実OpenRouter catalogは458件で、account headerもprimaryと一致した。
TUIの`/login`は現在providerにかかわらずprofile一覧を開き、target指定ではキー入力・account画面へ進めた。
キー入力に保存値を表示せず、接続済みChatGPTのEnterは新attemptを作らず選択した。

archiveのSHA-256は`4ab8fd04ee9bec0f91d655ef464bcc9de33c8d2637c735cd405535d00314b075`。
`dist/i12-live/package-summary.json`と`cold-summary.json`、`tui-cold-*.txt`に確認結果を保管した。
`dist/hjh`・`dist/hjh-tui`をこの最終binaryへ更新した。今回のAPI切替を使うにはCoreとTUIの両方を再起動する。
追加のmodel runは行わず、既存の実行結果と実catalog・保存状態で配布物を確認した。

## 実確認の限界と後続

追加accountのブラウザ認可、実accountの再認証、異なる実accountへの切替は未確認である。
これらのCore exchange／保存／registration再利用とTUI操作はmockで確認したが、
実providerで成功したとは扱わない。利用者の指定どおり、保存済み一accountを複製して
別accountの実証に使うことも行っていない。

[HTTP生本文とターン中context予算](../notes/http-response-context-overflow.md)、
Core自動起動、色・Glamour、tool詳細の再設計は後続のままとする。
