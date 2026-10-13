# I-08：接続先・model・effortの選択

2026-10-11。I-07の利用確認後、利用者が次のインクリメントへ進むよう指示した。
[I-06の優先順位](i06-conversation-tui.md)の次段階から、provider／model／effort pickerと検索を具体化する。
この文書はI-08の承認済み計画である。

2026-10-13追記：利用者指示により、TUIのeffort直接指定と候補外の保存値をpickerへ追加する
動作は廃止した。effortはautoまたはmodelの確認済み候補から選ぶ。
以下の初回計画の該当記述より、[現在のTUI契約](../design/tui.md)を優先する。
実装・test・通常／批判的review・実provider／PTY・配布物の確認結果は[結果文書](../results/i08-connection-selection.md)に記録した。

## 利用者の必要な動作

- `/provider`を引数なしで実行すると、外部定義された接続先を一覧から選べる。
  displayName、provider ID、API方式、認証方式、既定modelを読める。
  OpenCode GoのChat CompletionsとResponses、OpenAI APIキーとChatGPT認証を別の接続先として扱う。
- `/model`を引数なしで実行すると、次runに使う接続先のmodel候補を選べる。
  model ID／表示名で検索でき、文字入力・paste・Backspace、上下、PgUp/PgDn、Home/End、Enter、Escを使う。
  現在選択を初期選択し、文字幅と実画面高から選択行を画面内へ保つ。
- `/effort`を引数なしで実行すると、選択modelについて得られるeffort候補を選べる。
  候補の出所を示し、外部定義または確認済みcatalogに情報があるときだけ列挙する。
  不明なmodelを推測で固定enumへ当てはめない。`auto`と直接入力の経路を利用できる。
- `/provider ID`、`/model ID`、`/effort VALUE`の直接指定は利用できる。
  候補一覧はallowlistにせず、一覧にないmodel IDやeffort値をpicker追加によって拒否しない。
- 実行中に選択しても、そのrunは従来の接続先で継続し、変更は次runから使う。
  footerで現在runの実効値と次runの選択を区別する。pickerの検索は会話draftを変更しない。
- loading中もEscで閉じられる。閉じた画面や別Session・別providerへの変更後に、遅い応答を適用しない。
  role／model／effort／auth-profileの変更、run完了による保存selection更新も次run入力の変更として扱う。
  catalog読取・検索・描画はrunや終了保存の待ちへ戻らない。
- 保存済みChatGPT認証をcatalog取得・実行に再利用する。画面を開くたびにログインを開始しない。

`/providers`／`/models [ID]`の明示的な一覧表示は継続する。
お気に入りの永続保存、認証登録・アカウント選択画面、Core自動起動はそれぞれ保存先やprocess寿命を伴うため、
このインクリメントの後に具体化する。F2 follow-up、F3 steering、context／recall、色、Glamour、
`/view`、tool詳細の再設計、Session削除は今回の範囲に含めない。

## 確認済みの経路と旧Henji比較

現行`internal/tui/connections.go`はprovider／effortの直接指定と一覧取得を行い、
`commands.go`はmodelを指定する。`model.go:startRun`が選択値をStartRunRequestへ載せる。
Session／configからの実効値はCoreの`app.Run`→`providers.Registry.Resolve`が決める。
provider変更はmodel／effort／auth overrideをclearし、model変更とeffort変更は現行の直接指定と同じ操作に揃える。
成功rootの採用で選択が保存されるため、picker閲覧だけでSessionの会話revisionや保存selectionを変更しない。

候補取得はTUI→apiclient→HTTP `/providers`／`/models`→Service→外部config／catalogである。
現在のModelInfoはID／nameのみ、effort候補の情報はない。
provider指定なしのService.Modelsは全体configのproviderを使うので、roleやSessionから決まる次runの
providerと常に一致するわけではない。pickerの入口で次runの実効selectionをCoreへ問い合わせる必要がある。
現在のModelsはprovider既定auth profileからcatalogを取る。pickerではpreviewで解決したauthProfileも
`GET /models`へ渡し、Session／明示profileと同じ認証で候補を取得する。別accountの候補を表示しない。

旧Henjiの原典は`/home/agent/projects/henji-harness/v0/tui/remote_catalog_ui.ts`と
`remote_session.ts`の呼出し経路を確認した。引数なしのprovider／model／effort選択、model検索、
現在値の初期選択、loading表示と応答世代、認証再利用を参考にする。
旧10件固定window、実行中selection禁止、modelお気に入りの保存機構をGoへそのまま移さない。
hjhは現在の「実行中の変更は次run」の契約を維持し、実画面高と文字幅で表示範囲を決める。

## CoreとTUIの責務

1. Coreに次runの実効selectionを読む操作を置く。Session ID、workspace、role、provider／model／effort／
   auth-profileの明示値を受け、現在のrun準備と同じrole・model優先順位で解決する。
   roleは明示値→Session保存role→config.defaultRole。新Sessionのmodelは全体config→role.model→明示値、
   既存Sessionのmodelは保存selection→明示値である。既存Sessionでroleを変えても、role.modelを
   保存selectionへ自動適用しない。Serviceが持つ起動時optionの適用も現在のRunRequestと共通にする。
   run開始・VM生成・credential取得・ログイン開始・会話保存をしない。
   `app.Run`とpreviewが別の解決規則を持たないよう、既存の解決部分を共有する。
   metadata操作でtool／hook source全文や会話本文を展開しない。
2. catalogが返すmodel候補へ、確認できるeffort情報の経路を加える。
   静的候補は外部JSONで宣言し、dynamic catalogは現行responseの確認済みfieldだけを対応付ける。
   情報がないことをそのmodelの利用不可として扱わない。TUIにprovider ID別の候補表を持たせない。
   account catalogの認証はpreviewのauthProfileを使用し、credentialの参照とrefreshは既存Core所有者へ任せる。
3. TUIの接続pickerはdraft・検索・選択行・catalog request寿命を所有し、会話entityを複製しない。
   Session pickerと入力・loading取消・文字幅計算を共有できる部分は整理するが、別の責務は混ぜない。
   preview／catalog requestはSession、workspace、role、provider／model／effort／auth-profileの入力と
   次run選択の世代を捕捉する。これらの変更、初回Session ID確定、run完了による保存selection更新時に
   古い要求を取消・無効化し、開いているpickerが必要とするpreview／候補を更新する。
   応答は開いているpickerのjob／世代と最新の次run入力に対応するときだけ採用する。
   検索queryや会話draftはこの入力とは別に保持する。画面を閉じた後の更新は再openしない。
   選択確定は現在のprovider／model／effort操作を呼び、同じ処理を二重に持たない。
4. Coreの実行・会話・保存をpickerの応答や描画ACKに結び付けない。
   HTTPのcredential／Authorizationを画面・API responseへ出さない。

実効selection読取は`GET /api/v2/selection`とし、queryは`sessionId`／`workspace`／`role`／
`providerId`／`modelId`／`effort`／`authProfile`、responseは`{role, selection}`とする。
selectionは既存ModelSelectionのprovider／api／modelId／effort／authProfileを使う。
これは取得時点のpreviewであり、runの予約やconfig snapshotの固定はしない。run開始時に同じ規則で改めて解決する。
静的configのmodel候補と公開ModelInfoへ任意の`efforts: string[]`を加え、公開候補には
`effortSource: "config" | "catalog"`を付ける。情報なしはfieldを省略する。
候補のためにrunの保存selection payloadを増やさない。実装時にHTTP／provider設計へこの契約を明記する。
新しいお気に入り保存先・DB table・selection専用workerはこの段階で追加しない。

## 実装・確認の順序

- 共通selection解決と軽いCore preview、そのHTTP／client経路を作る。
- provider／model pickerと検索を接続し、既存の直接指定と同じ次run操作へ揃える。
- effort候補の出所と自由指定を接続し、footer／helpを更新する。
- 実経路に対応したfocused test、必要なrace／vet／format／diff checkを行う。
  role／Session／明示値の優先順位、API routeの区別、run中の変更が次runだけに適用されること、
  loading中Esc、取得中のrole／次run入力変更、run完了時の保存selection更新、検索とdraftの分離、
  画面内の選択行、候補にない直接指定を確認する。
- private Core／tmuxで実際に候補を選び、既存認証のChatGPT catalogを再取得する。
  OpenRouter、OpenCode Go、OpenAI APIキー／ChatGPTの接続選択と、選択後のrunの実効値・保存・再開を確認する。
  既存認証で成立する確認に再ログインを要求しない。利用者の稼働process・DB・設定は変更しない。
- 通常／批判的reviewで旧Henjiと対比し、既存の次run契約、解決の単一所有、catalogと描画の独立を確認する。
  初回30分、限定再review一回15分。full gateを要求しない。

実装開始時に、保存済みChatGPT認証でcatalogを取得し、
`models[].supported_reasoning_levels[].effort`を実responseで確認した。
他のproviderにないeffort情報は推測で補わない。
確認できない値は「情報なし」として扱い、自由指定を維持する。候補表の網羅性を完了条件にはしない。
結果は`docs/results/i08-connection-selection.md`へ記録する。

## 計画review

通常・批判的reviewを旧Henjiの現行catalog UIと対比して実施した。
通常reviewで、previewのauthProfileが候補取得へ渡らず既定profileへ戻る経路を指摘され、
`GET /models`への実効authProfileの伝達を契約に追加した。
批判的reviewで、role／model／effort変更とrun完了後の保存selection更新に対する応答寿命が不明確と
指摘され、次run入力・世代・jobに対応した取消／再取得／応答適用の条件を追加した。
一般的hardening、候補のallowlist化、旧idle限定は採用していない。
通常・批判的reviewはいずれも更新後の計画を実装へ進める状態と判断し、残る必須findingはない。
