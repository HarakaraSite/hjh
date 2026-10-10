# I-04：Session内のprovider・model切替

2026-10-10。状態：**実装・テスト・通常／批判的reviewと修正・実provider確認完了**。
I-03は[実装・実provider・reviewと修正](../results/i03-generic-roles.md)まで完了した。
利用者は次のインクリメントへの進行と、旧Henjiと同様の接続先を指定した。
この文書は指定された利用動作と、現行経路からの実装案を分けて記録する。
利用者は本計画の実装・テスト・reviewへの進行を指示した。具体的なJSON・操作契約は[provider設計](../design/providers.md)へ確定し、進捗と実確認は[結果](../results/i04-provider-model-switching.md)へ記録する。

## 必要な利用動作と根拠

同じSessionの会話を引き継ぎ、次の実行でprovider・modelを自由に選ぶ。
CLIとTUIから同じGo操作を使い、roleのmodel設定は既定値として扱い、利用者の明示切替を優先する。
OpenRouterの利用を継続し、利用者が指定したOpenCode Go・OpenAI APIキー・Sign in with ChatGPTを追加する。
OpenAIはAPIキー・ChatGPT認証ともにResponses APIのみを使う。
OpenRouterもResponses APIへの一本化を目標とし、hjhの実利用動作が成立することを確認して切り替える。
OpenCode GoはChat CompletionsとResponsesを独立した接続経路として選び、それぞれの利用可能modelを表示する。
OpenAI・OpenRouter・OpenCode Goの接続定義は、初めから外部JSONで指定する。
将来の互換接続先も、既存protocol・認証実装で扱える場合はJSONの追加だけで選択できるようにする。

| 接続経路 | 認証 | 実装案 |
| --- | --- | --- |
| OpenRouter / `openrouter-responses` | APIキー | Responsesへ一本化。現行modelでtool・stream・会話継続を実確認 |
| OpenCode Go / `opencode-go-chat` | APIキー | Chat Completions。旧実利用のDeepSeekから確認 |
| OpenCode Go / `opencode-go-responses` | APIキー | Responses。Chat経路とはmodel一覧を分ける |
| OpenAI API / `openai-responses` | APIキー | Responsesのみ |
| OpenAI / `openai-chatgpt` | OAuth | Responsesのみ。独立したログイン・credential解決・更新を接続 |

旧HenjiではOpenAI APIキーを`openai-responses`、ChatGPT認証を`openai-chatgpt`として分けていた。
[model selection](../../../henji-harness/v0/agent/provider/model_selection.ts)と
[Responses adapter](../../../henji-harness/v0/agent/provider/openai_responses_model.ts)で確認した。
OpenCode Goは`opencode-go-chat`／`deepseek-v4.1-flash`を実利用していた。
[実測記録](../../../henji-harness/docs/research/increment-218-real-provider-memory-2026-10-09.md)は今回の調査根拠であり、
旧実装の制限、Core・queue、model catalog、budget設定の一括採用を意味しない。
OpenRouter Responsesは旧HenjiでもDeepSeek V4.1 Flashによる複数request・tool実行が成功している
（[Increment 86の実測](../../../henji-harness/docs/history/increments/increment-86.md)）。
これは一本化の実現可能性の根拠であり、hjhの動作確認を代替しない。

## 外部JSONのprovider定義

利用者が外部定義を指定した。既存の外部`config.json`へ`providers`を追加する構成案とし、
roleの定義と同じ使用config bytesからprovider registryを作る。
protocolの処理、認証処理、selection解決はGoの各moduleに置き、接続先の個別設定をJSONから渡す。
標準の5経路も同じJSON形式で配布し、Go内に標準providerだけを固定登録する別経路を作らない。

| 外部定義の項目 | 用途 |
| --- | --- |
| provider ID・表示名 | CLI／TUIとroleのselectionから接続経路を選ぶ |
| API種別・baseURL・endpoint path | 共通Chat Completions／Responses adapterと送信先を指定 |
| auth.kind・auth.profile | 認証処理の種類と、秘密値を含まないcredential参照を指定 |
| 既定model・effort | provider変更時に使うselectionの基準 |
| model一覧または取得設定 | 接続経路ごとの選択候補を提供。ChatGPTではaccountのmodel一覧を取得 |
| 追加header・adapter設定 | 非秘密のheaderと、確認済み外部契約のrequest差分を指定 |

OpenAIのAPIキーとSign in with ChatGPTは、JSONの認証種別で区別する。
両方ともResponsesを使うが、接続ID・認証参照・accountに対応するmodel一覧を独立して扱う。
次は認証指定の形を示す抜粋であり、既定modelやcatalog等を含む完全な設定ではない。

```json
{
  "providers": {
    "openai-responses": {
      "api": "responses",
      "auth": {"kind": "api-key", "profile": "openai-api-key"}
    },
    "openai-chatgpt": {
      "api": "responses",
      "auth": {"kind": "chatgpt-oauth", "profile": "hjh-chatgpt"}
    }
  }
}
```

registryはJSONの`auth.profile`から実selectionの`authProfile`を解決し、credentialsへ`auth.kind`と参照を渡す。
APIキー参照は環境変数／fileの参照先、OAuth参照はhjhが所有する登録・account・保存credentialへ対応付ける。
APIキー、access token、refresh token、実Authorization値をprovider JSONへ書かない。
`chatgpt-oauth`はGoのChatGPT認証実装を選ぶ指定であり、ログイン・登録・token更新の手順をJSONで実装するものではない。
OAuthの動的な登録情報とtokenの所有者はcredentialsである。

OpenRouterのstateless指定、SIWCのtool namespace、OpenCode Goの会話header等、
確認済みの接続差分はadapter／transportの操作として実装し、外部定義から必要な操作を選ぶ。
Go側の接続ID比較で特定providerだけを特別扱いする構成を避ける。
新しいwire protocolや認証手順が必要な接続先は、対応するGo実装を追加してJSONから選べるようにする。

親・子それぞれの使用provider定義は、そのexecutionでcaptureしたconfig bytesから取得する。
使用bytesを既存のcontents／resource_revisionへ保存し、config編集は現在のexecutionへ反映しない。
既存snapshotへ独自のprovider保存fieldを追加せず、実selectionとsource bytesで実接続の根拠を辿る。

## 確認した外部契約

確認日は2026-10-10。sourceは公式文書、旧sourceは参考とする。計画段階の推論requestは送っていない。

- [OpenRouter Responses](https://openrouter.ai/docs/api_reference/responses/overview)は
  `/api/v1/responses`を提供し、APIキー認証、function calling、reasoningに対応する。
  stateless方式のため毎requestに会話履歴を含め、`store:true`や非nullの`previous_response_id`を使わない。
  全modelの対応を一律には仮定せず、現行の`deepseek/deepseek-v4.1-flash`をhjhで確認する。
- [OpenCode Go](https://docs.opencode.ai/docs/go/)はAPIキーで利用し、modelにより
  `/chat/completions`・`/responses`・`/messages`を使う。
  hjhのUser-Agentと会話ごとの安定した`x-opencode-session`を送る。
  旧実利用のDeepSeek V4.1 Flash／MiMoはChat Completions経路である。
  今回指定されたDeepSeek／GPTはChat／Responsesで確認し、Messages経路は対象外とした。
- [OpenAI Responses](https://developers.openai.com/api/docs/guides/migrate-to-responses)と
  [function calling](https://developers.openai.com/api/docs/guides/function-calling)を、
  共通message・tool call・結果・可視本文・provider固有状態の対応付けの根拠にする。
- [Sign in with ChatGPTの推論](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)は
  OAuth access tokenを使う公開Responses APIで、`store:false`・`stream:true`を指定する。
  accountのmodel一覧と完了eventを使い、HTTP 200や本文deltaだけで完了扱いにしない。
- [SIWCの入力・tool契約](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)に合わせ、
  instruction、会話input、function toolのnamespaceとtool結果をencodeする。
  履歴はhjh側で保持し、別providerへの切替もその採用会話から再構成する。
- [SIWC登録](https://developers.openai.com/siwc/token-sharing-open-source/sign-in)と
  [token更新](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions)を、
  host identity・発行client・accountに対応するcredentialの所有責務へ反映する。
  これは利用者が指定した認証機能の外部契約であり、一般的なhardening追加ではない。

## 実装前に確認したproduct経路

`CLI／TUI → app.Service.Run → app.Run → app.execute → agent.Run → provider.Generate → history`。
子は`api.agents.run → app.delegation → app.execute`で同じloopを使う。

| 現行の所有者 | 確認した状態 | 必要な変更 |
| --- | --- | --- |
| CLI | credentialを読み、OpenRouterを生成してServiceへ固定注入 | 接続選択を渡し、選んだ接続の認証だけを実行準備で解決 |
| Service | provider一つと起動optionsを保持 | provider解決の依存を保持。実runに対応する接続を決定 |
| app.Run | 明示model→config.model→保存model。model指定はOpenRouter selectionを生成 | provider・model・effortを共通resolverで一度解決 |
| extensions | 使用config bytesとrole・toolをcapture | 同じ使用bytesから外部provider定義・roleのmodel既定値を得る |
| app.delegation | 子のroleを再captureし、親の実modelとproviderを継承 | 子roleの既定値を共通resolverで解決。接続を変える場合は変更先の既定値を使い、同じ接続なら未指定項目を親から継承 |
| provider | OpenRouter専用request／SSE／認証 | 共通Chat CompletionsとResponses、接続固有の差分を分離 |
| app.contextAttribution | model呼出し前にOpenRouter Chat encoderでwire bodyを作り、context・model_requestへ保存 | 実adapterが準備した送信bytesを受け取り、再encodeせず保存 |
| history | model_json／active_model_json／session_model_changesを保存 | 実selectionを同じ契約で保存。採用処理とmessage保存の責務を維持 |
| TUI | `/model`と起動時固定credential／provider | 次回のprovider・model・effort選択、実接続と次選択の表示 |

起動時に生成したOpenRouterだけを保持する経路は、新しい解決経路へ置き換える。
OpenRouter Responsesの実利用確認後、OpenRouter専用Chat経路と固定selection生成を置き換える。
Chat Completionsの共通adapterはOpenCode Goで使う。
同じ選択・認証処理をCLI、TUI、親、子へ重複実装しない。
QuickJSのtool呼出しとGo host操作のbridgeは今回の接続切替の対象ではない。

## selectionと保存の契約

`provider`・`api`・`authProfile`・`modelId`・`effort`を実行開始時の値として解決する。
providerは上表の接続経路のidentity、apiはwire protocol、authProfileは秘密値を持たない認証参照。
OpenCode GoのChat／Responsesはprovider・apiとmodel一覧を明確に分け、model名からAPIを暗黙推定しない。
OpenAI APIキーとChatGPTログインは同じResponses protocolを使っても独立した接続として選ぶ。
TUI操作やconfig編集は次runに適用し、実行中のroot／子の接続を途中で変更しない。

rootの優先順位は**明示選択 → 再開Sessionの保存selection → roleの既定値 → 全体既定値**。
保存値を優先することで、利用者が切り替えた接続を次回再開でも使う。
実装前のconfig→Sessionの順を変更し、操作契約とREADMEへ明示した。

子は**選んだroleのmodel既定値 → 親の実selection**を基準にする。
taskとroleは既存のrun_agent入力を維持し、呼出しごとのmodel指定追加はI-04には含めない。
親の明示設定はrootへ適用し、別modelを定義した子roleはその既定値で動く。
同じ会話のrootを自由に切り替える機能と、子roleの既定modelはこのように分ける。

### root・子共通の部分selection解決

上記の優先順位で下位のselectionを基準とし、上位の指定を適用する。
provider未指定は基準の接続を使う。providerが同じ場合は、未指定のauthProfile・modelId・effortを基準から継承する。
providerが変わる場合は、変更先の接続定義を新しい基準とし、元の接続のfieldを持ち越さない。

| field | 解決規則 |
| --- | --- |
| provider | 上位の指定を優先し、未指定なら基準の接続を使う |
| api | 選ばれたproviderの接続定義から取得する。別接続のprotocolを継承しない |
| authProfile | 明示指定を優先。同じ接続なら基準を継承、変更時は変更先の既定認証参照を使う |
| modelId | 明示指定を優先。同じ接続なら基準を継承、変更時は変更先の既定modelを使う |
| effort | 明示指定を優先。同じ接続なら基準を継承、変更時は変更先の既定effortを使う |

たとえばOpenRouter親から、providerだけを`openai-chatgpt`へ変える子roleを呼ぶ場合、
子はChatGPT接続のResponses protocol・OAuth認証参照・既定model・既定effortを使う。
親のOpenRouter認証参照やmodel・effortは残さない。
子roleがmodelIdだけ指定した場合は、親の接続・認証参照・effortを継承してmodelだけを変更する。
CLI／TUIのprovider変更にも同じ規則を使う。
接続ごとの既定model・effortの具体値と認証参照名は、実装設計で使用config・model一覧・credential参照先に対応付ける。

DDL v3を維持し、実selectionをexecutionとmodel_requestへ保存する。
成功rootの会話・role・selectionを現在の採用transactionで反映する。
失敗・取消・子executionはSessionの成功済みselectionを更新しない。
切替時も採用会話・tool call／結果を保持し、provider固有のopaque stateは送信先とmodelに合うものを使う。
原記録は削除せず、requestへのprojectionと保存証拠を分ける。
OpenRouterの変更後は実selectionにResponses経路を記録し、APIキーの参照先を維持する。
旧Chat selectionを保存したSession／configは自動変換せず、利用者が新経路を明示選択して会話を継続する。
共通messageからResponses inputへのencodeはadapterの責務であり、旧保存payloadの書換えではない。
ChatGPT accountの実行時参照とopaque state identityは、旧payloadとの対応を設計時に確認する。

## module境界と認証

- appのselection resolver：利用者の指定・保存selection・使用configから実selectionを決める。
- provider registry／factory：capture済み外部JSONを読み、接続ごとのprotocol・既定認証参照・model・effortを提供する。実selectionに対応する共通adapter・認証実装・transportを接続する。
- credentials：APIキー参照とChatGPT登録・token更新を扱う。値をVM・config snapshot・進捗・履歴へ渡さない。
- protocol adapter：Go共通messageからrequestを作り、本文・tool・usage・opaque state・終了結果へ変換する。
- app.contextAttribution：共通messageと準備済みwire bytesからcontext・model_requestを保存する。protocol固有encoderへ依存しない。
- CLI／TUI：選択と表示。SQL、OAuth処理、request parserを所有しない。

### requestの準備・保存・送信

現行は[contextAttribution](../../internal/app/context.go)と
旧OpenRouter専用transport（I-04で廃止）がそれぞれOpenRouter Chat requestをencodeしている。
I-04では各model requestを、**adapterで準備 → context・model_request保存 → transport送信**の順で扱う。
adapterのprepare操作は、実selectionと共通requestからcredentialを含まないwire body bytesを一度生成する。
準備済みrequestを共通境界で渡し、contextAttribution、providerのrequest観測、HTTP transportが同じbytesを使う。
保存後にtransport側で再encodeしたり、接続固有のbody fieldを追加したりしない。
Authorization等のcredential headerはtransportの責務に留め、準備済みbody・context・観測へ渡さない。
context itemの`provider_wire_body`、`model_request`のcontent、context digestをこの実送信bytesから作る。
親と子の両方で同じ準備・保存・送信経路を使い、app側のOpenRouter encoder直接依存を廃止する。

APIキーはfile／環境変数参照で指定する。現行OpenRouterの利用者指定済み参照先を維持する。
OpenCode Go／OpenAIの実APIキー参照先も利用者の「同じ場所」指定で確認した。
旧Henjiの実credential fileを、値を出力せず参照して使うか、新しいhjh保存先を使うかは実装確認前に決める。
ChatGPTはhjh自身の登録・host identityとcredential所有責務を持つ。
旧Henjiのtokenを自動移行したり、旧runtimeと同じrefresh tokenを無断で共同更新したりしない。

CLIのログイン・状態確認を共通Go操作へ接続し、TUIからも同じ操作を呼ぶ。
LinuxのVM利用ではcallbackがブラウザを動かすhostへ届く点を扱う。
[公式VM案内](https://developers.openai.com/siwc/token-sharing-open-source/self-hosted-vms)と旧Henjiの手動callback実測を照合し、
利用環境で成立するログイン操作を設計する。callback URLやtokenを通常の会話・履歴へ入れない。
登録、再起動後の利用、token更新が実経路で完了することを確認する。

## CLI・TUIの操作

CLIは`--provider`・`--model`・`--effort`で次runを指定し、同じ`--session`を再開する。
接続一覧とmodel一覧は実行操作とは分ける。名前入力と一覧からの選択を共通selectionへつなぐ。
model catalogは選択の補助とし、固定fixtureや旧catalogを実serviceの仕様へ置き換えない。
OpenCode Goのmodel一覧はChat／Responsesの選択経路ごとに表示する。
ChatGPTでは選択accountの公式model catalogを利用する。

TUIへ`/provider`・`/model`・`/effort`、接続・model一覧、ChatGPTログイン操作を追加した。
実行中の選択は次runに適用し、headerの実接続と次回選択を分ける。
子のmodel／provider進捗でrootの実設定・選択・本文を変更しない。
具体的な引数とログイン操作は[provider設計](../design/providers.md)に記載した。

## 実装の段階と完了条件

一つのI-04の中で段階を分け、OpenAI APIキーとChatGPTログインを両方完成対象に置く。

| 段階 | 完成させる動作 |
| --- | --- |
| 1 | 外部JSONによる標準5経路の定義、使用config・role既定値・Session・明示指定のselection解決、provider生成を共通化。root・子で部分指定の共通規則を使う |
| 2 | 共通Responses adapterとrequestの準備・保存・送信境界を作る。OpenRouterの現行実modelでtool・stream・会話継続を確認し、成立後にChat経路とappの専用encoder依存を置き換える |
| 3 | OpenCode GoのChat／Responsesを別経路として完成させ、OpenAI APIキーのResponses接続を完了 |
| 4 | hjhのChatGPT登録・ログイン・credential保存・更新・Responses実行を完了 |
| 5 | CLI／TUIから同一Sessionを複数接続で継続し、role別の子modelと実設定を閲覧 |
| 6 | focused確認、実provider／PTY／Linux配布確認、実装reviewと必要な修正を記録 |

| 製品動作 | 確認方法 |
| --- | --- |
| 外部provider定義 | 標準5経路を外部JSONから生成し、API種別・送信先・認証方式・既定値・model一覧を実経路へ反映。既存adapterを使う追加接続IDもGoの登録変更なしで選び、使用source bytesをinspectで辿る |
| OpenRouter Responsesへの一本化 | 現行実modelでrun_js・外部tool、本文stream・最終回答、tool結果を使う次request、Session再開・停止を確認。返されたreasoning状態の引継ぎも確認 |
| OpenCode GoのAPI別model選択 | Chat／Responsesそれぞれの一覧から対応modelを選び、同じSessionで両経路の実tool作業を継続 |
| 同じSessionで切替・復帰 | ある接続で実file作業、別接続で前会話に基づく追加作業、元接続で継続。Session ID・成果物・採用会話・実model履歴を照合 |
| roleは既定値、明示選択は優先 | role既定modelを持つ新規root、明示切替、保存selectionでの別process再開を実Go操作で確認 |
| 部分指定で接続を変更 | providerだけを指定したroot／子で変更先のapi・authProfile・model・effortを使い、modelだけの指定では元の接続を継承することを実selectionとrequestで確認 |
| 子は実roleの接続を使う | 親と別model／providerのreviewer子が実fileを読んで返し、親が結果を使う。子がSession選択を更新しないことも確認 |
| Chat／Responses toolとstream | 実モデルによるrun_js・外部tool呼出し、次model判断、途中本文、完了回答を製品transportで確認 |
| ChatGPTの日常認証 | 利用者ログイン後の実tool作業・別process利用・公式契約に従う更新を確認。refresh更新は同じcredential所有者で直列化 |
| 停止と終了契約 | 変更された接続のmodel待機と親子実行をCLI SIGINT／TUI Esc・終了から停止し、join・終了保存・非採用を確認 |
| 保存とcredentialの境界 | 実selection・opaque state・source bytesをinspectで確認。APIキー／OAuth token／Authorizationを保存・表示しない |
| 実requestと保存bytesの一致 | Chat／Responsesの実adapter・transportを使い、親／子の送信bodyと保存済みprovider_wire_body・model_request content・digestを照合する。接続固有bodyを含め、保存後に再encodeしない経路を確認 |
| Linux配布 | 同梱設定と外部credential参照から切替・会話継続を行い、manifestと実buildを照合 |

focused testは上記の具体的動作と、変更に伴う確認済みregressionへ対応させる。
架空のprovider variantやpermission matrixを追加しない。実adapterと外部契約・実応答を先に確認する。
実装中はfocused test・必要なrace／vet／format／空白確認を使い、全体gateを反復しない。
実provider確認には実modelとcredential参照先を決め、ログインは利用者のブラウザ操作と接続する。

## 計画reviewの結果と反映

2026-10-10に通常reviewと批判的reviewを実施した。重複を統合し、計画上の不足としてP2の2件を採用した。

| 指摘 | 反映内容 |
| --- | --- |
| 実request保存経路にOpenRouter Chat encoder依存が残る | 現行経路とmodule境界へcontextAttributionを追加。準備済みwire bytesを保存・観測・送信で共有し、一致を確認する |
| provider変更時に親の認証参照等が残り得る | root・子共通の部分selection規則を定義。変更時は変更先のapi・既定認証参照・model・effortから解決する |

外部JSON追加後の通常／批判的再reviewでも必須修正findingなし。raw outputは同じ送信先・account・modelのときだけ再送し、異なる接続は可視会話とtool call／結果から組み立てる方式へ具体化した。実装reviewは結果文書へ記録する。

## 実装・確認の現在地と対象外

- 外部JSON、接続切替、親子role.model、Chat／Responses adapter、CLI／TUI操作を実装した。
  JSON fieldと優先順位・認証保存先は[provider設計](../design/providers.md)を参照する。
- APIキーは利用者指定の旧Henjiと同じ場所。OpenRouter DeepSeek V4.1 Flash、
  OpenCode Go Chat DeepSeek V4.1 Flash、Go Responses GPT-6-Luna、OpenAI API GPT-6-Lunaで実tool作業とSession往復を確認した。
- OpenRouter Responsesの現行実modelでtool／stream／会話継続を確認し、旧専用Chat adapterを廃止した。
- ChatGPTは利用者ブラウザ認証を完了し、account model一覧・tool推論・別process再利用・実refreshが成功。
  refreshはlocal expiry metadataを過去へ変更してproduction branchを通した。実時間で1時間を跨ぐrunは未実施。
- OpenCode GoのMessages経路は、今回指定されたDeepSeek／GPTの利用動作に不要で対象外。
- 一般的hardening、JS／OS sandbox、コンテナ配置、Core・queue、旧DB／config／tokenの自動変換、公開は対象外。
