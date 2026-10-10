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
