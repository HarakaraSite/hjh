# I-12：TUIの認証・アカウント選択

2026-10-12。I-11完了後、利用者が次の範囲として「TUIの認証・アカウント選択画面」を選択した。
本書と[契約](../design/i12-authentication-contract.md)へ保存・API・認可操作を具体化し、
通常・批判的計画reviewを完了した。review中に採用findingを修正し、両reviewerが更新済み記述を確認した。
[review結果](../results/i12-plan-contract-review.md)。認証保存・attempt・HTTP／CLI・TUIを実装し、
focused test／race、実装reviewと採用指摘の修正、登録キー・保存済みChatGPT一accountでの実利用、
Linux配布物のcold再開を完了した。[実装・確認結果](../results/i12-authentication-ui.md)。
旧Henjiの操作要件を参考に、独立Core／TUIの現在の責務へ接続する。

## 利用者が必要とする動作

- `/login`から、現在の構成で使う認証profileを一覧し、APIキー登録またはChatGPTアカウント画面へ進める。
  providerの表示名・認証方式・登録状態を見て対象を選べる。共有profileは一行とし、利用するproviderを併記する。
- APIキーを専用の入力欄へ貼り付けて保存し、その認証でmodel一覧と次runを使える。
  キーの値は画面・通常の会話draft・scrollback・Session履歴・エラー本文へ出さない。
- ChatGPTの接続済みアカウントを一覧し、Enterで選択、`a`で追加、`r`で選択行の再認証へ進める。
  保存済み認証を通常利用・再接続・Core再起動で再利用する。画面を開くこと自体はログインを開始しない。
- 認可URLを表示し、ローカルcallbackとGhostty → SSH → tmux経由のcallback URL貼付の両方で完了できる。
  callbackは専用の伏せ字入力欄で受け、成功後にアカウント一覧へ戻る。
- 登録・選択の成功はCoreの保存完了後に表示する。Escで画面を閉じても会話draftを保持する。
  実行中runの接続先・認証先を維持し、変更はその後に準備するrunへ反映する。
- HTTP経由の別clientも一覧・登録・選択・認可開始／完了を使える。credentialとtokenの保存・refreshはCoreが所有する。

最初の操作例は「`/login` → OpenRouter APIキー登録 → `/model` → 依頼送信」、
次は「`/login` → ChatGPT既存アカウント選択 → ChatGPT provider/model選択 → 依頼送信」。
認証の登録・選択と、次runのprovider／model選択は別操作とする。

## 根拠と現行経路

利用者は旧Henji TUIの操作要件の踏襲・段階的実装を指定し、今回認証画面を選んだ。
保存済みChatGPT認証の再利用についても、以前の実確認で繰り返す認証操作を避ける意向が示されている。

確認した旧repository `/home/agent/projects/henji-harness` の参考source：

- `v0/tui/remote_catalog_ui.ts`：profile picker、APIキー伏せ字入力、ChatGPTアカウント一覧、
  追加／選択／再認証、認可URLとcallback入力、非同期結果の画面世代照合。
- `v0/agent/provider/credential_registration.ts`：profile単位の登録対象一覧、値を返さない保存操作。
- `v0/agent/provider/chatgpt_auth.ts`：認可attempt、registration／account／選択の分離、account単位のrefresh。
- `v0/agent/worker/worker_runtime.ts`：root接続のChatGPT registrationの捕捉。

旧sourceの固定callback port、idle-only登録、入力上限等は、この計画の採用条件にしない。

計画時点（I-12実装前）のhjhの操作経路：

- `internal/tui/connections.go`の`/auth`／`/login` → apiclient → HTTP `/auth/status`・`/auth/login`・
  `/auth/complete` → app → `credentials.Manager`。画面は文字出力で、profile/account pickerはない。
- appの認証操作はprovider既定のprofileを解決する。一方model preview／catalog／runには
  明示`authProfile`があるため、画面から指定した実効profileを認証操作にも渡す必要がある。
- APIキーは `credentials.Profile.Env` → `File` の順で読む。登録の公開操作とCore所有の登録キー保存先はない。
- ChatGPTは `oauthPath(profile)` の一つのrecordとregistrationを持つ。既存recordのclient IDで再認証し、
  token refreshを既存file lock・atomic renameの経路で保存する。複数accountと選択状態はない。
- `app.connectionFactory.provider` は初回に認証を解決してadapter identityを作り、ChatGPT各request前の
  callbackでも同じprofileを再解決する。account選択を単純に差し替えると、実行中runの途中で認証先が変わる。
  `app/delegation.go`の子Agentも同じrunのfactoryを使うので、rootだけの固定では足りない。
- Coreの認証操作はCore contextで実行し、停止時にauth operationをjoinしてcredential managerを閉じる。
  新操作もこの寿命へ接続し、TUI描画・Session/run lockの内側で保存やOAuth通信を待たない。

## 保存と解決の提案

以下の保存・解決方式は契約に具体化した。既存credential recordの変換、旧Henji credentialの移動、履歴DDLの変更は必要としない。

### APIキー

登録したキーはCore stateのcredential領域へ保存する。実効config絶対path＋profileで識別し、
同じ構成を使うSession／providerから共用する。既存の外部env/file参照は引き続き登録前の認証元となる。
TUIから登録した値はその構成の明示登録として優先する。明示runの`--credential-file`は従来のoverrideとして優先する。
この順序によりenv-only profileも画面で登録でき、旧repositoryを参照するfileへ新キーを上書きする必要がない。
状態表示は実際に使用する認証元と登録の有無を区別し、値を返さない。

### ChatGPT

現在の`oauthPath(profile)`はそのprofileの標準accountの保存先としてそのまま扱う。
接続済み標準accountも一覧へ表示し、再認証は同じrecord・client registrationへ接続する。
追加accountには別のcredential record／registrationを割り当て、account一覧と選択IDは小さいmetadataで保持する。
recordの全文コピーや選択のたびのtoken書換えは行わない。
profileの共有単位は現在の正規化済みOAuth保存先とし、同じ保存先を使うproviderは同じ選択を参照する。
選択IDはCore再起動後も保持し、初期選択は標準accountとする。追加と選択を別操作として明示する。

account追加の成功後は追加したaccountを選択し、再認証だけならそれまでの選択を維持する。
既存accountが使える間は一覧取得・選択に新たなブラウザ認証を要求しない。
access tokenの期限と、保存済みrefresh tokenで継続できるかを混同しない。
再認証が必要な状態の表示は、既存認証経路の実際の結果に基づく。期限だけで接続済みaccountを削除しない。

## 実行中runとの分離

- adapter準備時に、実際に使う認証元／account参照を捕捉する。同じ認証ownerを使うroot・子Agentは
  run factoryの捕捉済み参照を共用する。子Agentが別profileを初めて使う場合は、そのWorkerの準備時に捕捉する。
  既存のWorkerごとのconfig captureを維持し、未使用profileを先回りして認証・refreshしない。
- APIキーは捕捉時の値をrun factoryで保持し、同じ認証ownerを使う子Agentへ登録後の値を後から取り込まない。
  値をexecution captureやmodel inputへ保存しない。
- ChatGPTのtoken更新callbackは、profileの現在選択を解決し直すのでなく、捕捉したaccountへrefreshする。
  同じaccountのtoken更新は継続でき、別account選択は進行中adapterとそのstate identityへ影響しない。
- F2予約は「次に準備するrun」とする。予約時のprovider/model入力と、準備時の実効accountを区別する。
  F3 steeringは現在runの認証参照を変更しない。Session再開の会話とaccount選択を別状態として扱う。
- model catalogはその照会で選択されたaccountを使う。TUIはaccount選択成功後、次runのpreview／catalogを更新する。
  account変更でI-11のお気に入り集合を変更しない。

## 責務と契約確定で具体化すること

| owner | 責務 |
| --- | --- |
| credentials | 登録キー、OAuth account参照・record・選択、attempt、保存・refresh、停止join。公開値は状態metadataのみ。 |
| app | Session/workspaceから実効configとprofileを解決。認証登録・選択を既存model解決とrun factoryへ接続。 |
| HTTP／apiclient | profile一覧、キー登録、account一覧・選択、attempt開始／完了／取消。Coreとscope・attemptの識別を運ぶ。 |
| TUI | picker、専用の伏せ字入力、進捗、会話draft保持、画面世代に基づく遅延応答の適用。 |

既存`/login`の引数なしはprofile picker、provider指定は対象profileの画面へ接続する。
`/auth`は既存statusの確認用途を維持する。新しいattempt操作へ古いprofile名ベースのpending実装を重ねず、
認証操作のownerを一つにする。従来の`/login complete`／`callback`の扱いは契約確定時に整理し、
使われなくなるpending状態・client経路も同じ変更で整理する。

保存path・metadata schema、DTO、account ID／attempt ID、選択・保存の確定点、client切断後の結果取得、
Esc／detach／Core停止時のattempt寿命、複数clientの更新順は[契約](../design/i12-authentication-contract.md)を参照する。
既存file lockと停止ownerを活用し、独立した保存workerや同じcredentialの二重cacheは必要性を確認してから決める。
秘密の入力はHTTP request bodyだけで運び、query・通常ログ・会話記録へ入れない。

## 実装・確認の順序

1. 上記提案を契約へ具体化し、旧Henjiの現行経路との対比を含む通常／批判的計画reviewを行う。
2. credential登録・account一覧／選択・attemptの共通Go操作と保存を実装し、既存OAuth exchange／refreshへ接続する。
3. appのprofile解決、run／子Agentの認証参照固定、HTTP／apiclientを接続する。
4. profile picker、キー入力、ChatGPT account／認可／callback画面を既存TUI描画とjob世代管理へ接続する。
5. 変更したproduct動作のfocused確認を行う。登録後の実効認証、既存認証再利用、追加／選択／再認証、
   実行中の切替と子Agent、遅延応答・draft維持、再接続／再起動の保存結果を対象にする。
6. private config／credential state／Core／tmuxで、人が使う順に画面を操作し、別HTTP clientでも状態を照合する。
   APIキー登録はprivate保存先と既存キーの明示参照を使い、利用者の既存credential fileを変更しない。
   保存済みChatGPTでcatalog／runを確認し、追加・再認証だけはブラウザ操作が必要になる。
   利用者は保存済み一accountだけで進めることを指定したため、追加・再認証・別の実accountへの切替は
   今回の実確認では行わない。mock成功だけで実確認済みとしない。
7. 通常／批判的実装reviewと採用findingの修正後、Linux配布物で登録・選択・cold再開を確認する。

成功基準は、利用者が認証画面から登録・account選択を終え、その接続でmodelを選び依頼を完了できること。
focused test・実確認・reviewの結果は別途結果文書へ記録する。

Core自動起動、色・Glamour、tool詳細の再設計は後続のままとする。
[HTTP生本文とターン中context超過](../notes/http-response-context-overflow.md)も記録済みの後続課題として維持する。
