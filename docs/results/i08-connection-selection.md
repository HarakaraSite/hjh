# I-08：接続先・model・effortの選択

2026-10-11。実装・focused test・通常／批判的review・実provider／PTY・配布物確認を完了した。

2026-10-13の利用者指示で、TUI effortはpickerからのみ選ぶ方式へ変更した。
以下の初回結果中にあるeffort直接指定の記載は当時の動作であり、現在は
[/effortの現行契約](../design/tui.md)に従う。
[計画](../plans/i08-connection-selection.md)、[TUI操作](../design/tui.md)、
[HTTP契約](../design/http-api.md)、[provider定義](../design/providers.md)を参照する。

## 2026-10-13：effortはpickerから選択する

利用者指示により、`/effort VALUE`は選択を変更せずpicker利用の案内を表示する。
`/effort`ではautoと当該modelに宣言された候補のみを選ぶ。保存値が候補外でも選択肢へ追加しない。
CLI flag、role.model、子agentのmodel.effortの契約は今回変更しない。

標準OpenCode Go Chat 19件・Responses 6件の既存modelを
[公式モデル定義](https://github.com/anomalyco/models.dev/tree/dev/providers/opencode-go/models)と照合し、
各`reasoning_options`のeffort値を配布config・現在のworkspace configの`efforts`へ設定した。
GLM-5.3はlow/high/max、MiMo V2.6 Proはeffort値なしなのでautoのみ。
toggleだけを宣言するmodelにもeffort値を推測で追加しない。model候補の追加やAPI経路の切替は行わない。

[Go公式docs](https://opencode.ai/v2/docs/console/go/#models)はmodel一覧APIを案内している。
現行workspaceの接続先に対応する[`/zen/go/v1/models`](https://opencode.ai/zen/go/v1/models)と
新docsの`/inference/go/v1/models`の実GET応答を確認した。どちらもmodelのID等を返すが、
2026-10-13の応答にeffort metadataはないため、今回はconfigの確認済み候補を使う。

配布configを使うTUI testで、直接指定が選択を変更しないこと、候補外の保存済みxhighを
GLM-5.3の選択肢へ混ぜないこと、highの選択が次のStartRunRequestへ届くこと、MiMoでautoのみになることを確認した。
関連picker tests、TUI vet、diff checkも成功し、両binaryを再buildした。
実PTY・一時Core・workspaceでも直接指定の案内、GLM-5.3の候補表示、選択したhighのCore保存を確認した。
モデル生成は行わず、常用Core・履歴・認証には触れていない。一時processとデータは確認後に片付けた。

## 利用者の動作と責務

- 引数なしの`/provider`・`/model`・`/effort`でpickerを開き、上下・PgUp/PgDn・Home/End、Enter、Escを使う。
  providerは接続ID・表示名・API・認証方式・既定modelを表示する。
- modelはID／表示名を検索する。文字入力・paste・Backspaceは会話draftから分離する。
  effortは選択modelの確認済み候補と出所を表示し、情報なしでもautoと直接指定を使える。
- `/providers`・`/models [ID]`の一覧表示、候補にないmodel／effortの直接指定を維持する。
  実行中の選択は次runから使い、実行中runの接続を変えない。
- 保存済み認証を再利用する。catalogはpreviewで解決したauthProfileと、実runと同じcredential-fileを使う。
  pickerを開くだけではログインを始めない。

Coreは軽い`GET /selection`と既存runの解決を共有し、Session／config metadataだけを読む。
新Sessionは全体model→role.model→明示値、再開は保存selection→明示値。
role変更だけで再開Sessionの保存modelをrole.modelへ置き換えない。
source capture、VM、実行、保存はrun側に残す。新しいDB table、保存先、workerは追加していない。

TUIはpickerのquery・選択行・request寿命を所有する。次run入力、世代、jobを捕捉して応答を採用し、
Session／role／接続変更、初回Session ID、終了採用時に古い要求を無効にする。
開いているpickerだけを更新し、閉じた画面を再openしない。Coreの実行・保存は描画ACKを待たない。

## 確認済みの外部契約

OpenAI Docsの[account catalog契約](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)で、
選択accountのtokenによる`/models`取得、`visibility=list`、server順序、`slug`／`display_name`を確認した。
保存済みChatGPT認証による実応答で`models[].supported_reasoning_levels[].effort`を確認し、
その順序をModelInfoの`efforts`／`effortSource=catalog`へ対応付けた。
他のprovider・modelへ固定enumを推測で当てはめていない。

private Coreの実HTTPで5接続先のpreviewとAPI方式の一致、Session生成0を確認した。
ChatGPTの可視modelは7件で、`gpt-6.1-sol`の候補はlow／medium／high／xhigh／max／ultraだった。
証跡はgit管理外`dist/i08-live/metadata/`と`dist/i08-live/catalog.json`に置く。

## reviewの採用finding

| finding | 対応 |
| --- | --- |
| 通常P2：catalog取得がprepareConfigを通りtool／hook一式を展開する | connectionRegistryもselectionConfigのmetadata読取へ揃え、未初期化workspaceでpreview／Providers／Modelsを読んでも設定やsourceを追加しないことを確認 |
| 批判的P2：run用credential-fileをcatalogが適用しない | credential file適用処理をrun／catalogで共有し、明示profileの候補取得も起動optionと同じkeyを使う |
| 通常P2：保存providerが現在configにないSessionで、provider pickerがpreview失敗を理由に一覧へ到達しない | provider一覧と現在previewの依存を分離し、接続先を選び直す経路と`/models ID`の明示先解決を成立させる |
| 通常／批判的P2：継承中の同値model／effortをEnter確定しても明示選択にならず、後のrole変更で置き換わる | model／effortは同値でも直接指定と同じsetterを呼ぶ。確定→role変更→StartRunのrequestにmodel／effortが残ることを確認 |

通常・批判的reviewは旧Henjiのremote_catalog_ui.ts／remote_session.tsと対比した。
旧idle限定、10件固定window、お気に入り保存をそのまま移植せず、採用済みI-08の次run選択と実画面高を使う。
各reviewerの一回の限定再reviewで解消を確認した。変更範囲に残る必須findingはない。
`/models`の非同期処理はjob開始前に入力を捕捉し、引数なしは過去runのproviderへ固定せず
次runのpreviewで解決することも確認した。

## focused verification

Coreのapp／extensions／providers／httpapi／cmdのfocused package test、
selection／catalogのappとHTTPのfocused race、apiclientのpackage test／race、関連vetは成功。
TUIのpackage test／race、picker・検索・request捕捉・復旧・選択確定のfocused race、
関連vet、format、`git diff --check`も成功。full gateは実行していない。

## 実provider・PTY

private Core／DB／tmuxで接続picker→model検索paste→effort選択→実run→保存を確認した。
利用者の稼働Core／TUI、DB、設定、tmuxは変更していない。既存credential参照と通常refreshを使った。

| 接続 | model／effort | 結果 |
| --- | --- | --- |
| OpenRouter Responses | deepseek/deepseek-v4.1-flash／auto | 完了、actualと保存値一致 |
| OpenAI API Responses | gpt-6-luna／auto | 完了、actualと保存値一致 |
| OpenCode Go Chat Completions | deepseek-v4.1-flash／auto | 完了、actualと保存値一致 |
| OpenCode Go Responses | gpt-6-luna／auto | 完了、actualと保存値一致 |
| ChatGPT Responses | gpt-6.1-sol／medium | 既存認証で完了、actualと保存値一致 |

OpenRouterの実行中に次providerをOpenAIへ選び直し、現在runのactual値が変わらず、
次runでOpenAIへ切り替わった。5runの最終本文を実paneで確認した。
detach／再接続後のmodel／mediumの初期選択、48×15の狭幅でEnd選択行が可視であること、
Core／TUI終了0を確認。login POST、cancel POSTはいずれも0だった。

この5接続確認は同値確定の最後のP2修正前のcandidateである。
修正後の最終配布物では、private configの継承model `deepseek-v4.1-flash`／lowをpickerでそのままEnter確定し、
既定modelが`deepseek-v4-pro`／highのreviewer roleへ変更して実runを行った。
StartRunRequestの明示model／effort、actual、保存値はいずれもFlash／lowを維持し、roleはreviewerで完了した。
根拠のある変更箇所に対する追加確認であり、変更のない5接続全体を繰り返していない。

証跡はgit管理外`dist/i08-live/connection-selection/`、`role-confirmation/`に置く。
5runのSessionは`673a3340-56b2-4965-8009-428cf3dd71d2`。
requests、pane、summary、saved viewから操作と保存値を追える。

## Linux配布物

最後のP2修正後に`python3 scripts/package-linux.py --out dist/i08-connection-packages`で作成した。
最終archiveは`build-smq6izdv/hjh-linux-amd64.tar.gz`、SHA256は
`c691aaf273f60c0e8094b9bc5bf7ec562513f5b024f1dcbe7a4e98278d774215`。
Linux/amd64 static ELF、archive内144 fileのmanifest size／hashを確認した。

配布物そのものでprivate DBのcold再開を確認し、5 turn、全保存entity値、5 assistant本文の各1回表示、
保存model／mediumの初期選択を維持した。追加model呼出し0、Core／TUI終了0。
証跡は`dist/i08-live/package-replay/`。確認後`dist/hjh`／`dist/hjh-tui`へ反映した。
適用には利用者がCoreとTUIの両方を再起動する。

| binary | SHA256 |
| --- | --- |
| hjh | 8daff02e2cc04d980a4632807471d967d2a7564d8c8e60ddd156cca1d7c7d7ec |
| hjh-tui | 03484f6488148f1f7c8aa39cb831af96724a93a528416cba607d02e9f4b4ae26 |
