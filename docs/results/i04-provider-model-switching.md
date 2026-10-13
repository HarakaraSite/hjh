# I-04：provider・model切替の実装と確認

2026-10-10。実装・テスト・通常／批判的reviewと修正、5接続の実provider確認を完了。
正本は[計画](../plans/i04-provider-model-switching.md)、[接続設定](../design/providers.md)、[操作契約](../design/run-api.md)。

## 実装

外部config.jsonのproviders・credentials・model・role.modelから実selectionを解決する。
標準の5接続も同じJSON経路を使い、provider IDのGo固定登録はない。
registry、credentials、protocol、appの接続生成、CLI／TUIの選択を責務ごとに分けた。
旧OpenRouter専用Chat adapterとcredential helperを廃止した。

親子共通loopはadapter.Prepareのbytesをcontext・model_request・HTTP送信で共有する。
Responses raw outputを同じ送信先・account・modelのときだけ再送し、切替後は可視会話とtool call／結果を維持する。
成功rootだけが実selectionと会話をSessionへ採用する。子・失敗・取消は採用しない。DDL v3は変更していない。

## 実APIキー接続

旧Henjiの指定済みfile参照で、dist/hjh run --streamを別processから実行した。
同じSession `ab0167ab-1de8-4a60-a56d-756635392a3a` を5回継続し、result.txtへmarkerを追記して読み返した。
実証拠はgit管理外のdist/i04-live/summary.jsonと同workspaceのSQLiteにある。

| 順序 | 接続／model | 確認動作 |
| --- | --- | --- |
| 1 | OpenRouter Responses／DeepSeek V4.1 Flash | sample.txtを読んでresult.txtを作成・確認、途中本文とtool進捗 |
| 2 | OpenCode Go Chat／DeepSeek V4.1 Flash | 前の会話と成果物を引継ぎ、marker追記・確認 |
| 3 | OpenCode Go Responses／GPT-6-Luna | APIを切替えて同じ会話・ファイルを継続 |
| 4 | OpenAI API Responses／GPT-6-Luna・effort low | 追加作業・読み戻し、以前のmarkerを回答 |
| 5 | OpenRouter Responsesへ復帰 | reviewer子をGo Chatで実行し、返したexecution IDをファイルへ追記・確認 |

5つのrootはcompleted・canonical。reviewer子2件はGo Chatを使いcompleted・non_canonical、
親Sessionの接続はOpenRouterのまま。親modelの判断で同じ調査を2回委譲しており、子の件数を固定した動作ではない。
最初のsample.txtは実行開始直後に作成したため初回readが失敗した。modelは後続requestで再読し作業を完了した。
これは確認準備のtimingであり、provider／tool実装の不具合として扱わない。

## テストとreview

focused testはfactory経由のResponses親→Chat子→親への結果、Session切替と可視会話維持、
incomplete rootの非採用、HTTP受信bytesと保存wire bodyの一致を確認した。
TUIはprovider変更で前接続のmodel／effort／auth overrideをclearし、次runへ新selectionを渡す動作を確認した。
旧tool・履歴・Session再開・停止・stream regressionは共通adapterへ移して維持した。
認証testは公式登録・署名付きID token・token rotation・process間直列化、
更新tokenでのrequest、Authorizationの別origin非転送とerror非露出を確認した。

通常／批判的実装reviewで次を採用・修正し、変更箇所の限定再reviewで解消を確認した。

| 指摘・実観測 | 修正 |
| --- | --- |
| 長いrunの後続requestでOAuth tokenが初回値のまま | 各HTTP requestでcredential resolverを呼ぶ |
| 初回code交換失敗でissued clientを失う | tokenと別のregistrationを保持し、新しいstate・nonce・PKCEで再認可 |
| 未完了ログインがUI終了へjoinされない | context取消とManager／Service Closeを結線し、listener・Serve・watcherをjoin |
| TUI header追加でcursorが1行上へずれる | layout・cursor補正を6行へ揃え、実PTYで入力位置を照合 |
| 実認証server時計がVMより約15秒先で正常ID tokenを拒否 | OIDCのsmall clock skew契約に沿い、5秒から60秒へ許容を変更。署名・issuer・audience・nonce検証を維持 |

最終候補で`go test ./...`を一回実行し、成功。変更範囲の`go vet`も成功。
app／TUIのrace確認、credentials／protocolの担当範囲race確認も成功した。
reviewerはsourceと対応test・実証記録を確認し、full gate・実API・credential読取りは実行していない。


## ChatGPTとLinux配布

利用者がブラウザで認証し、hjh自身のhost／issued client／accountを登録した。
初回は実時計差によるID token拒否が起きたため修正し、同じissued clientで再認可して成功。
旧HenjiのOAuth tokenをimportしていない。
`models`はaccountの7つの表示可能modelを取得し、選択したGPT-6-Lunaで
同じSessionのresult.txtへI04-CHATGPTを追記・読み返した。

Linux/amd64の静的配布物を生成し、manifestの全file digestとINTERPなしを確認した。
別processの配布binaryで同じOAuth profileとSessionを再利用し、I04-PACKAGE-CHATGPTを追記・読み返した。
実証はdist/i04-live/package-summary.json、archiveはdist/i04-packages/build-e7u00juj/hjh-linux-amd64.tar.gz。
配布物のSHA-256は`0fb350249b8558c2aee61c430d8282d17c2133f5c05058ee24de54446f5195d5`。

実refreshも成功。通常の1時間経過を待つ代わりに、hjh自身のlocal expiry metadataだけを過去へ変更し、
配布binaryのproduction resolverで更新branchを通した。
access token変更・refresh token rotation・client／host維持と、更新後のtool作業・I04-REFRESH追記を確認。
token値を旧値へ戻す操作は行わない。dist/i04-live/refresh-summary.jsonに結果を保存した。

## TUI・停止

実PTYでproviders／Go Chat models、JSON追加接続custom-responsesからGo Chat reviewer子、
実行中の次回provider変更と現在接続の維持、Esc停止・その後のGo Chat継続、終了時の端末復元を確認。
追加接続はGo再登録なしで実OpenAIへ接続した。dist/i04-live/tui-summary.jsonに記録した。

実CLIではOpenRouter Responses親とGo Chat runner子を動かし、子の60秒command開始後にSIGINT。
親子ともcancelled・non_canonicalへsettleしてexit130、commandの後続fileは生成されなかった。
dist/i04-cancel/summary.jsonに記録した。

OpenRouter Responsesの保存stateにはreasoning・function_call・messageのraw itemがあり、
次requestの継続も実確認した。APIキーはDB／contentsに存在しないことを実値の非表示比較で確認した。
OAuthの更新前・更新後tokenもDB・ログ・PTYに存在しないことを非表示比較で確認した。

## 確認の範囲

対象はLinux/amd64と指定した実model。GoのMessages API、別OS、旧config／DB／tokenの自動変換、公開は対象外。
初回登録のexpired-code回復と複数process refresh直列化は公式契約に対応するtestで確認した。
実ブラウザで期限切れcodeを意図的に生成する確認や、実時間で1時間を跨ぐrunは実施していない。

## 2026-10-13追補：OpenCode Go Chatのthinking設定漏れ

利用者指定のSession `b881e477`はOpenCode Go Chat／DeepSeek V4.1 Flash／maxで9回生成し、
usageのreasoning tokensは合計18,089だったが、thinking本文は保存されていなかった。
標準・workspaceのprovider定義で`adapter.reasoningField`が未設定だったため、共通Chat受信処理が
thinkingを読み取らない状態だった。Git履歴では最初のcommit `4c9bb5b`から未指定であり、
今回のソース保存停止やセッション削除変更によるregressionではない。
以前のOpenCode Go Chat Session `9ad5278e`も本文0件で、OpenRouter ResponsesのDeepSeekでは
Session `c008fb3a`等にthinking本文が保存されていた。

配布configと利用者のworkspace configへ`reasoningField: "reasoning_content"`、
`reasoningReplay: true`を設定した。公開thinkingの受信・保存と、同じ接続・account・modelへの
assistant reasoning stateの再送を有効にする。モデル固有のハードコードやフィールド推測は追加しない。
DeepSeekだけの形式ではなく、公式モデル定義のMiMo V2.6 ProとGLM 5.3も同じfieldを指定している。
[設定契約と出典](../design/providers.md)を参照する。

標準配布configを使う新しいapp testで、HTTPストリームのthinkingがlive通知・保存履歴へ届き、
read_file実行後の入力へ返されることを確認した。既存のprotocol focused tests、appのvet、diff checkも成功。
更新した実binaryと一時DB・ローカルHTTPでも、thinking本文2つの保存・CLI history表示、
ツール後の再送を確認した。実際の外部providerへのAPI呼出しは行っていない。
既存Sessionの受信ストリーム自体は保存されていないので、失われた本文の復元は行わない。
workspaceの修正は次の実行でcaptureされる。編集済みの他workspaceのconfigを自動変換する処理は追加しない。

## 2026-10-13追補：標準provider設定の横断確認

標準5接続についてconfig→registry→connection factory→wire生成・受信→履歴の経路を確認した。
OpenAI API ResponsesとOpenCode Go Responsesでは`adapter.reasoningReplay`が未指定でfalseとなり、
保存したreasoning outputを次のrequestへ返していなかった。配布configと現在のworkspace configへ
`store: false`、`reasoningReplay: true`を設定した。OpenRouter ResponsesとChatGPT Responsesは既に有効だった。

OpenAIのthinking summaryは以前から`reasoning.summary: "auto"`で要求し、受信・保存・TUI表示する。
今回の再送設定とは独立しており、要約を受信しても次requestのreasoning stateを省く状態があり得た。
現行の[OpenAI公式契約](https://developers.openai.com/api/docs/guides/reasoning)では`store: false`で
encrypted reasoningが既定で返るため、旧Henjiが指定したlegacy include項目は追加しない。

標準configの4つのResponses接続をそのままfactoryへ渡すapp testで、ローカルHTTPからのsummaryを
live通知・復元履歴で確認し、read_file後にreasoning・function_call・結果が順序通り次requestへ届くことを確認した。
OpenCode Goのsession headerとUser-Agentも同じ実経路で確認した。テスト用のendpoint・auth・capacity metadataだけを
置き換え、adapterは配布設定を使用した。Chatのthinking testも含めたfocused testsとvet、diff checkが成功した。
両binaryを再buildし、一時workspace・DBで実CLIのOpenAI API ResponsesとOpenCode Go Responsesを
ローカルHTTPへ接続した。summaryの要求、reasoning stateのツール後再送、最終回答のstdout、
`hjh history`へのsummary表示を確認した。一時データは確認後に削除した。
外部APIによる実確認は行っていない。接続先、認証profile参照、model/APIの分離、既存namespace指定については、
現行実装・旧Henjiの実行証拠・公式契約と突き合わせ、今回追加修正を要する設定漏れは確認しなかった。

## 2026-10-13追補：ツール結果の余分なnameを省略

Session `60dd8278`／OpenCode Go Chat／GLM-5.3-Flash／maxは、最初の生成とfind実行が成功した後、
2回目の生成が`messages[3]: "name" is not supported by this endpoint`というHTTP 400で失敗した。
送信wireのrole=toolに`name: "find"`が付いていた。GLM-5.3のSession `01f6755d`と
DeepSeek V4.1 Flashの`b881e477`では同じname付きの結果を送り正常完了しており、
Goのモデル別API経路で受け付け方に差がある。GLM全体の制限とは位置付けない。
OpenRouter経由のGLM-5.3-Flashの実挙動は未確認であり、同じ失敗を推測で当てはめない。

旧HenjiはChatとResponsesのどちらでも結果にnameを付けていなかった。
利用者の採用に従い、hjhも両wire encoderから結果のname付与を除いた。
call ID・結果本文・既存namespaceは保持し、tool宣言・呼出し・DB履歴のnameは変更しない。
モデル別例外、設定項目、エラー後の再試行は追加していない。

既存protocol testsで結果にnameを出さずcall ID・本文・namespaceを保持し、呼出し側のnameは保持することを確認した。
標準configのChat app testでは、name付き結果をHTTP 400で拒否するローカル応答に対してread_file後の
継続・thinking・ツール名の復元履歴を確認した。4つのResponses app testsでも結果のname省略と呼出し名の保持を確認した。
focused tests、protocol/app vet、diff checkが成功し、両binaryを再buildした。
実CLI・一時workspace・DBでOpenCode Go Chat／GLM-5.3-FlashとOpenAI ResponsesをローカルHTTPへ接続し、
find→結果返却→最終回答・履歴表示が完了した。常用履歴の修正や有料API呼出しは行っていない。
起動中Coreは旧コードを使うため、このwire変更を常用TUIへ適用するにはCoreの再起動が必要。
