# I-12：認証保存・account・attempt・HTTP・TUIの契約

2026-10-12。[計画](../plans/i12-authentication-ui.md)の実装入力を具体化する。
通常・批判的計画reviewと、review中の修正確認を完了した。[確認結果](../results/i12-plan-contract-review.md)。
I-12の実装契約である。保存・attempt・HTTP／CLI・TUIと認証bindingを実装し、
登録キー・保存済みChatGPT一accountでの実利用とLinux配布物のcold再開を確認した。
[実装・確認結果](../results/i12-authentication-ui.md)。旧Henjiとの機能対比は計画の参考sourceを使う。

## 1. 解決対象と所有者

公開操作は現行 `/api/v2` に置く。scopeのSession／workspace解決は `httpapi.resolveWorkspace`、
実効configは `app.selectionConfig` を使う。configの正規化済み絶対pathを `configPath` とし、
`scopeId = hex(SHA-256(UTF-8(configPath)))` とする。TUI processのcwdへ解決しない。

認証targetは `providerId`・`authProfile`・`kind` を区別する。providerの実効定義がkindを決め、
明示authProfileがあればそれを使い、なければprovider既定を使う。
profile catalogの掲載をrunのauthProfileのallowlistにしない。
認証画面の入口では次runのSelection previewを使い、Session／role／明示overrideから解決した
providerとauthProfileを初期選択にする。登録しただけでは次runのprovider/modelを変更しない。

APIキーのownerは `configPath + authProfile`。OAuthのownerは現行 `oauthPath(profile)` の
正規化済み絶対path `B`。OAuthの公開owner IDは `hex(SHA-256(UTF-8(B)))`。
同じBを明示する別profile/providerは同じaccount一覧・選択を共有する。
実効configやOAuth保存先が変わった後も、開始済みattemptは開始時のownerへ完了する。

Coreの `credentials.Manager` が保存・attempt・refresh・停止joinを所有する。
appはconfigとtarget解決、run factoryは捕捉した認証binding、TUIは画面と入力だけを所有する。
Serviceと非対話CLIが同じManager操作を使い、別のcredential cacheや保存workerを追加しない。

## 2. APIキーの保存と使用

credential storage root `R` は `$XDG_CONFIG_HOME/hjh`、Linuxで未指定なら `~/.config/hjh`。
共通 `CredentialStateDir` とCLIの `--credential-state-dir` でRを指定できるようにする。
serve・run・models・authは同じoptionをManagerへ渡す。明示 `oauthFile` は従来通りそのpathを使う。

登録キーは `R/credentials/api-keys/<scopeId>/<hex(SHA-256(authProfile))>.json` に保存する。
内容は `{schemaVersion:1, revision:1, mutationId:"<client UUID>", key:"<secret>"}`。
keyは専用credential fileだけに置き、一覧・HTTP response・model input・capture・履歴へ含めない。
同じprofileを使うprovider間で共有し、別configの同名profileと分離する。

解決順序は次の通り：明示 `--credential-file` → このownerの登録キー → 既存env → 既存file。
標準外部fileは `${XDG_CONFIG_HOME}/hjh/credentials/<profile>` を参照する。
このprefixは環境変数未指定時も `os.UserConfigDir()` で解決する。`~/` と明示pathも使える。
登録操作では外部参照fileやconfigを書き換えない。
登録前のmanaged file未作成は未登録という状態であり、壊れた登録fileを未登録として隠さない。
登録画面はAPIキーの削除・旧認証への復帰操作を今回追加しない。

保存は既存 `writePrivateFile` の一時file→close→rename。rename対象と別の `<file>.lock` を使い、
同一／別Coreの更新で同じownerのrevisionを失わない。保存時に最新fileのrevisionを1増やす。
同じmutationIdの再送が現在fileと一致する場合は再保存せず、そのmetadataを返す。
現在fileが別mutationIdなら、新しい明示登録として保存する。同じ値かの照合をGETへ公開しない。
後のclientが別の登録をした場合に古い保存の自動再送で巻き戻さないよう、TUIは結果不明後に一覧を読み直し、
自分のmutationIdが最新なら成功、それ以外なら現在の登録状態と「結果不明」を表示する。
再登録は利用者の再度のEnterによる新mutationIdとする。exactly-onceや永続mutation履歴は設けない。

run factoryは最初に使ったownerのキーを保持する。同じownerの子Agentはそのbindingを共用する。
明示credential-fileは別bindingとして扱い、登録キーを再解決しない。
登録キーのadapter identityはowner＋捕捉revisionで区別し、別登録へ以前のprivate replayを渡さない。
既存外部env/fileのidentityを、この変更だけで新たなcredential監視へ拡張しない。

## 3. OAuth accountの保存

accountはtoken recordの保存slot。Subjectが同じでも別registrationなら別slotとして扱う。
標準slotの `accountId` は `primary`、token recordはB、registrationは現行 `B.registration.json`。
標準slotはI-12でも恒久的な保存先とし、現行recordを変換・複製・別形式でdual-readしない。

追加slotはUUIDのaccountIdとし、token recordは `B.accounts/<accountId>.json`、
registrationはそのpathの `.registration.json`。token/registrationの形式とexchange・refresh処理は現行を使う。
owner metadataは `B.accounts.json`：

```json
{"schemaVersion":1,"revision":2,"selectedAccountId":"primary","accountIds":["primary","<UUID>"]}
```

metadata未作成時は標準slotだけ・選択primary・revision=0というnative初期状態。
一覧はそのslotのrecord存在・表示用状態を読む。metadataが存在する場合はそのaccountIdsを使う。
保存されていないslotも `connected:false` として表示し、Enterで認可開始できる。
追加開始時にslotをmetadataへ登録するため、途中でCoreを再起動しても同じslotへ再認可できる。
未完了slotを期限や失敗で削除せず、既存issued client registrationを同じslotの次attemptで使う。

metadataのlockは `B.accounts.lock`。account recordのlockは現在の `<record>.lock`。
metadata更新は最新snapshotをread-modify-writeし、同じ選択の再送ではrevisionを増やさない。
slot追加と選択変更の保存でrevisionを増やし、他clientのslotを失わない。
accountのlabel・expiresAt等はrecordから投影し、token recordとmetadataへ重複保存しない。
一覧取得やaccount選択でOAuth通信・token refreshを開始しない。

選択は保存済みconnected slotへの `selectedAccountId` の指定。
有効なrefresh認証の再利用は従来のResolveで行い、access期限だけを再認証必須とみなさない。
一覧には `connected`, `expiresAt`, `planUsageEnabled` を返す。未観測のprovider errorを
独自の認証失効matrixへ分類せず、実際のrefresh失敗はその操作の結果として表示する。

追加／未接続slotの認可成功は、そのslotを選択する。接続済みslotの再認証成功は現在選択を維持する。
成功とはtoken recordと必要なmetadataの両方の公開後をいう。rename間の電源断保証は今回追加しない。
record保存に成功して選択保存に失敗した場合は「account保存済み・選択未完了」を返し、一覧から選択をやり直せる。
OAuthネットワーク待ちにはmetadata lockを持たず、最終保存はmetadata lock→account lockの順とする。
refreshはaccount lockだけを使い、逆順でmetadata lockを取らない。

## 4. runとcatalogの認証binding

bindingは秘密を公開しない内部参照で、kind・owner・accountId／key revisionとtoken解決操作を持つ。
factoryは短い同期でownerごとの最初のbinding作成を合流し、同じownerの並列子Agentへ同じ参照を渡す。
異なるownerを初めて使う子Agentは、そのWorkerの現行config captureから準備時にbindingを作る。
全profileのキー読取・OAuth refreshをroot開始時に先回りしない。

OAuthは選択accountを一回捕捉し、そのrecordでtokenをResolveする。
各request前も捕捉accountのrecordだけをrefreshし、現在のselectedAccountIdを引き直さない。
accountの再認証は同じSubject/client registrationを使う現在の経路へ接続する。
provider state identityには現在のdestinationと捕捉した実際のaccount identityを用いる。
公開された会話はaccount変更後も引き継ぎ、別accountのprivate replayを一致扱いしない。

F2予約は次run準備時のaccountを使い、F3は現在のbindingを使う。
保存済みSessionのModelSelectionはprofileを表し、globalなaccount選択IDをSession採用状態へ書き込まない。
履歴schema v3のDDLと現在のmodel payloadを維持する。
catalog requestも開始時にaccountを捕捉する。responseには実際に使ったownerId／accountIdをmetadataとして添え、
TUIで古いaccountの候補が新accountへ混ざらないようにする。既存 `/models` の配列responseを維持するため、
認証metadataはresponse header `Hjh-Auth-Owner-Id`／`Hjh-Auth-Account-Id` とclient内resultへ渡す。

## 5. HTTP DTOと操作

読取scopeは `sessionId?`, `workspace?`。writeはbodyに現在の `coreId` も必須。
同じprofileの別methodを区別するためtargetには `providerId`, `authProfile` を渡し、kindはCoreで解決する。
Core ID・scope・target解決は既存の接続／configの契約を使う。

| method/path（`/api/v2`配下） | 入力と結果 |
| --- | --- |
| GET `/auth/profiles` | scope＋SelectionRequestの選択入力。profile一覧と現在実効targetを返す。 |
| PUT `/auth/api-key` | scope, coreId, providerId, authProfile, mutationId, value。保存後のprofile状態、revision、mutationIdを200で返す。 |
| GET `/auth/accounts` | scope, providerId, authProfile。OAuth ownerのaccount一覧・選択・metadata revisionを返す。 |
| PUT `/auth/account-selection` | scope, coreId, providerId, authProfile, accountId。保存後のaccount一覧を200で返す。 |
| POST `/auth/attempts` | scope, coreId, providerId, authProfile, attemptId, accountId?。ID付きattemptを受付し、202で状態を返す。accountId省略は追加slot。 |
| GET `/auth/attempts/<id>?coreId=...` | 開始時のowner・targetと現在phase。ready時はauthorizationUrl／redirectUriも返す。 |
| POST `/auth/attempts/<id>/complete` | coreId, callbackUrl?。callback省略はローカルcallback待ち。202で現在状態を返す。 |
| POST `/auth/attempts/<id>/cancel` | coreId。starting／ready／waiting_callbackを取消。exchanging／savingでは進行状態を返し、保存済みaccountを取り消さない。 |
| DELETE `/auth/attempts/<id>?coreId=...` | terminal結果を確認したclientがattempt metadataを解放する。account/registration fileは削除しない。 |

profile rowは `{authProfile, methods, providers, apiKey?, chatGPT?}`。
methodsはそのprofileを使うprovider default・roleのmodel指定・入口の実効Selectionから得るkind集合。
一覧はこれらの利用経路を持つprofileを対象とし、未参照のcredential宣言を別の登録機能として追加しない。
同じprofileに両kindがあれば画面でmethodを選べる。kindはcredentialのfieldから推測しない。
roleでproviderが省略される場合は現在のbase selectionを使い、宣言をrun開始前に解決する。
providersは利用providerのID／displayName／kindであり、宣言上の一覧をoverrideの許可一覧にしない。
画面でmethodを選ぶとき、入口の実効providerがそのkindならそのIDを使い、それ以外は同kindの
利用providerをID順に表示して選ぶ。認証操作用providerIdの選択では次runのproviderを変更しない。

apiKey状態は `{registered, source:"override-file|registered|env|file|missing", revision, mutationId?}`。
sourceは現在の実効認証元。presence確認でkeyをresponseへ返さず、登録成功とproviderがそのkeyを受理したかを区別する。
chatGPT状態は `{ownerId, revision, selectedAccountId, accounts:[{accountId,label,connected,expiresAt?,planUsageEnabled}]}`。
未接続slotのlabelは `primary` なら「標準アカウント」、追加ならaccountId。
読めないcredentialは読取errorとして表示し、connected:falseで隠さない。

attempt responseは `{coreId, attemptId, scopeId, providerId, authProfile, ownerId, accountId,
phase, authorizationUrl?, redirectUri?, error?, accountSaved?, selectionSaved?}`。
phaseは `starting|ready|waiting_callback|exchanging|saving|completed|failed|cancelled`。
token・code・callbackURL・PKCE verifierは公開しない。認可に必要なnonce／state／code challengeは
authorizationUrlのqueryとして既存の認可経路通り渡し、独立した秘密fieldを追加しない。
開始時にconnectedだったかで「再認証」か「接続して選択」かを捕捉し、途中の他client選択へ意味をすり替えない。

## 6. attemptの寿命・再確認

attemptIdはclientが生成するUUID。begin受付でCore所有へ移し、target・account slot・開始時の保存先を捕捉する。
同Coreで同じattemptIdを再送すると現在状態を返し、新しいlistener／registrationを重ねない。
別targetの同じIDを同じattemptとして扱わない。初回slot追加の公開前に切断されてもGETで受付を確認できる。
account再認証の別attemptはslotの既存client registrationを使う。
Core内で同slotの進行中attemptがある場合はそのID／状態を返し、TUIは既存操作へ接続できるようにする。

begin後のdiscovery・loopback listener・認可準備、complete後のcallback待ち・exchange・保存はCoreのjobで実行する。
HTTP切断・TUI描画停止はjobを取消さない。短いhandlerは状態を返し、GETで途中経過と結果を確認できる。
callback省略のcomplete受付はwaiting_callbackへ移り、まだ取消可能とする。
waiting_callback中のcallback貼付は同じjobへ入力を渡す。自動callback到着と貼付は同じ受理操作を使い、
有効callbackの検証成功後、exchangingへの遷移をattempt単位で一回だけ行う。
再送は現在の進行中／terminal状態を返し、待機jobやcode exchangeを二重に作らない。
callback入力誤りはreadyへ戻って再入力できる現在の経路を保つ。code失効はfailedとし、同slotで新attemptを開始する。

cancelがexchangingへの遷移より先ならlistenerとcallback待機jobを閉じてcancelled。
exchangingへの遷移が先なら処理を継続し、cancelは現在状態を返す。TUIは「完了処理中」と表示し、会話へ戻れる。
取消後は同slotで新attemptを開始できる。
terminal時にcallback／verifier／tokenのjob保持分とlistenerを解放し、GET用には小さい結果metadataだけを残す。
TUIはterminal結果の確認後DELETEで解放する。画面を閉じて結果未確認なら、同Core再接続で再取得できる。
profile/account読取に進行中・未確認terminalのattempt概要を含め、IDを失ったclientも操作を見つけられるようにする。
Core終了でこのmetadataは消える。再起動後は保存record・選択・未接続slotから現在状態を読む。

複数clientのaccount選択は希望accountIdの保存順で後の値を現在値とする。
OAuth exchange中に別clientが選択しても、再認証ならその選択を保つ。
追加完了なら、その完了時の明示選択として追加accountを選ぶ。TUIは読取revisionで古い一覧を適用しない。

## 7. 受付・保存と終了

Manager内にcredential metadata／mutation専用の短い受付ticket列を持つ。
APIキー・account選択・slot追加・最終account保存・状態読取は、同Coreの受付順でscope解決とmetadata操作を行う。
OAuthネットワークやcallback待ちは列の外へ出し、最終保存だけを新しいticketにする。
file lockは別Coreの同じowner更新とrefreshの同期を担う。Managerのlifecycle mutexは受付と状態だけに使う。
read/metadata操作に会話全文・DB writer・TUI/SSE writeを持ち込まない。

writeはCore IDと受付を照合した後Core所有へ移す。scope解決失敗は操作失敗とし、保存は行わない。
APIキー登録・account選択は受付済み保存のjoin対象となり、HTTP client切断で途中取消しない。
GETは先行受付metadata更新の完了後に読み、未受付writeの完了まで保証しない。
停止開始で新しい受付を閉じる。Core shutdownの202を返す前とServer.Close入口の両方で閉じる。
OAuthのネットワーク・callback待ちは停止時に取消す。最終保存ticket受付済みならrecord／metadataの公開を終える。
閉じた受付への保存移行ができなければ、そのattemptは保存前に終了する。
停止はrun join→認証job／保存ticket join→Manager.Closeを既存ownerへ接続する。
read・保存・OAuth jobのjoin責務をServerとManagerへ二重実装しない。
保存完了は通常Core再起動で読めるfile公開を指す。停止操作はaccountやcredential fileを削除しない。

## 8. TUIと既存入口の整理

`/login`はprofile picker、`/login PROVIDER_ID`はその実効profileのAPIキー入力またはaccount画面。
profile/method/accountは矢印で選択、Enterで確定、Escで戻る／閉じる。
accountは `a`追加、`r`再認証。未接続slotのEnterはそのslotの認可へ進む。
認可画面はURLを表示し、Enterでcallback貼付画面へ、ローカルcallback完了はCore状態から確認できるようにする。
貼付入力は専用editorで伏せ字、Enterでcomplete、Ctrl+Uで消去、Escでcancelして戻る。
自動ローカルcallback経路はready時にCoreがcomplete受付へ進められるよう、callback到着でも同じ一回の状態遷移を使う。

TUIはCore・scope・target・owner・picker generation・attemptIdを捕捉してjob結果を照合する。
account選択完了後は現在pickerの新GETとcatalog世代更新で確認し、古いaccountのcatalog responseを採用しない。
認証画面操作で会話draft、role/provider/model/effortの入力、I-11のfavoriteを変更しない。
同Core・同ownerを共有するSessionへ移った後も、新scopeからの読取で状態を表示する。
送信待ち中のEscとdetachは描画を待たず進め、保存結果未確認を成功表示しない。
starting／ready／waiting_callback画面のEscはcancelを送り、exchanging/saving後の画面closeは処理を進める。
detach時はまだexchangingへ移っていないattemptのcancelを試みる。切断で取消結果が不明なら再接続時に状態を確認する。

キーとcallbackの値をeditorの通常draft・operationResultMsgの表示本文へ流さず、HTTP responseへechoしない。
既存URL全体をslash commandに貼る操作を廃止し、`/login complete`／`/login callback`は新画面入口へ案内する。
旧 `/auth/login`／`/auth/complete` とprofile名キーのpending保存・api.Login DTOをattempt経路へ置き換える。
`/auth [ID]`／CLI `hjh auth ... status` は新status読取の投影として維持する。
CLI `hjh auth ... login`も同Managerのattempt開始／完了操作を使い、Core HTTP接続を必須にしない。
CLI loginの既定targetは実効profileのselectedAccountIdとし、標準初期状態ではprimaryを明示してbeginする。
接続済みなら同slotの再認証、未接続なら同slotの初回接続とする。accountId省略で追加slotを作る入口へ
CLI loginを無条件に接続しない。statusも同じ選択slotを読む。新しいCLI account管理commandは今回必須にしない。
旧callback listener・registration再利用・token refreshを別実装として残さない。

## 9. 確認するproduct動作と未確認事項

- APIキーの登録→実効認証元表示→catalog→run、env-only profileの登録、明示file override、
  同config/profile共有と別config分離、結果不明時のmutation確認、通常再起動後の再利用。
- 標準accountの既存recordから一覧／catalog／run、追加slot→認可→選択、同slot再認証とregistration再利用、
  account選択→実際のcatalog account metadata→次run、選択だけでbrowser認証が起動しないこと。
- runと並列子Agentのbinding、同owner途中切替・再認証／refresh、異なるownerの子Agent準備、F2/F3との境界。
- private tmuxの伏せ字貼付・IME/caret・draft維持、画面close/Session切替後の結果、別clientの選択順、
  Core停止でのOAuth取消と受付済み保存join。HTTP client切断がjobを止めないこと。
- 各動作のfocused testと実経路確認を対応させ、旧Henji対比の通常／批判的review、Linux配布物のcold再開を行う。

実providerの追加・再認証には人のブラウザ認可が必要。利用者は保存済み一accountだけでの実確認を指定した。
追加・再認証・異なる実accountへの切替は今回の実確認範囲に含めない。
実認証で未確認の操作をfixture結果だけで確認済みとしない。
既存の一accountのtokenを二slotへ複製しただけで、異なる実accountの切替確認済みとは扱わない。
providerの新しいscope／model権限／OAuth失効分類を推測で追加しない。
Core自動起動、context超過、色/Glamour、tool詳細はこの契約の対象外。
