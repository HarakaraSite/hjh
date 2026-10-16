# 初期runの操作契約

2026-10-10。[採用範囲](initial-product.md)と[実装計画](../plans/initial-run.md)に基づく。
2026-10-16、現行のselection・credential・認証attempt APIへ記述を更新した。
現在の接続設定は[provider設計](providers.md)、認証操作は[認証契約](i12-authentication-contract.md)、
保存方式は[通常履歴保存](normal-history-storage.md)を正本とする。機能全体の現在地は[実装状況](../implementation-status.md)を参照する。

CLIは共通Go操作を呼び、SQL・VM・モデルloopを所有しない。
`hjh run` に依頼を引数またはstdinで渡す。引数があればstdinを待たない。
stdoutは確定した最終回答、stderrは進捗・Session ID・execution ID・失敗原因。
成功はexit 0、実行失敗は1、SIGINTによる取消は130。

`hjh run --stream` は各model stepの可視本文を受信した段階でstdoutへ逐次出力する。
providerの累積textから未表示の差分を取り出し、stepが変わると表示位置をリセットする。
tool等の進捗はstderrへ出し、本文の途中行と進捗行の境界に改行を補う。
完了時に最終回答全文を再出力しない。失敗・取消では表示済みの途中本文を保持し、
stderrの原因と終了codeで未完了を判別する。`--stream` なしは最終回答だけをstdoutへ出す。
このflagは表示の選択であり、model呼出し・履歴保存・会話採用の条件を変えない。

`--session ID` なしは新規Session、ありはその採用会話から新Workerを作る。
workspaceは新規時 `--workspace` またはcwd、再開時は保存したworkspaceを使う。
`--db` の既定はworkspace内 `.hjh/history.sqlite3`。
閲覧用の操作はread-onlyで開き、存在しないDBを作成しない。

接続は `--provider`・`--model`・`--effort`・`--auth-profile` と外部JSONから解決する。
新規は明示selection、workspaceの記憶selection（`.hjh/selection.json`）、role.model、全体modelの順。
再開は明示selection、Session保存selectionの順とし、workspaceの記憶selectionで保存Sessionを上書きしない。
TUIの接続選択はworkspaceへ即時保存するが、Session保存selectionの更新は正常rootの会話採用時に行う。
providerが変わると未指定fieldは変更先の既定値、同じproviderなら基準から継承する。
明示model変更時にeffortを省略するとautoへ戻し、同じmodelなら継承する。
API種別はprovider定義から取得し、model名から推定しない。
APIキーは明示`--credential-file`、TUIで登録したkey、profileの空でないenv、fileの順に解決する。
OpenRouter、OpenCode Go、OpenAIの標準file参照は`${XDG_CONFIG_HOME}/hjh/credentials/<profile>`。
LinuxでXDG_CONFIG_HOME未指定なら`~/.config/hjh/credentials/`を使う。
I-04当初は旧Henjiと同じfile参照を採用したが、[保存先集約](../results/credential-config-directory.md)で変更した。
登録API keyとChatGPTの認証状態もユーザーconfig配下へ保存し、`--credential-state-dir`で明示変更できる。
ChatGPT認証はhjh自身の発行client・accountを使う。
keyやAuthorization headerはVM・構成snapshot・観測へ渡さない。
[provider設定](providers.md)にJSONとAPI別model候補、ログインの操作を記載する。

`--max-steps N` は利用者が指定したときだけモデルstep上限を設定する。
未指定は0で無制限を表し、同じ値をschemaのmax_stepsへ保存する。
上限到達は完了回答と区別し、実行証拠を保持する。
`--from-turn N` は再開時のmodel入力候補をturn単位で選ぶ。未指定は全採用会話を候補にする。
I-14ではCoreがその候補からmodelの予算に入る履歴を選び、最新turnは部分選択する場合がある。
保存済み履歴を切り捨てず、tool callと対応結果を同じprojectionに含める。
詳しい設定、選択と記録は[コンテキスト管理契約](i14-web-context-contract.md)を参照する。

`hjh sessions` はSession metadataの一覧。
`hjh history [--session ID_OR_PREFIX|--latest] [--view session|canonical|detail]` は
履歴閲覧。既定は最新Sessionの統一会話テキスト、canonicalは採用会話Markdown、detailは
保存実行証拠のNDJSON。`--json`で採用message配列を取得できる。
detailのrecord contentはInspectと同じbase64表現で保存bytesを保持する。
`--from-turn N`／`--to-turn N`でinclusive範囲を指定し、`--connect URL`でCoreの公開APIを使う。
ローカルはread-onlyで読み、履歴未作成時はno historyで終了する。
`hjh inspect --execution ID` は失敗・取消・内側call・使用構成を含む実行証拠。
範囲指定の両端は包含とし、未指定はその側の全範囲。

共通Go操作は実行中のcontextと進捗observerを受け取る。
正常終了後に保存message全体をSessionへ採用する。
model待機／JS／host処理の取消は同じcontextで接続し、処理の終了を待ってexecutionをsettleする。
副作用を巻き戻す処理や失敗turnの会話への自動採用は追加しない。

Go build payloadの差分は[保存対応表](history-payloads.md)に記載した決定に従う。
実確認のmodelは利用者指定の `deepseek/deepseek-v4.1-flash`。
`finish_reason=length` は途中／空回答を保存して失敗終了し、正常turnへ採用しない。
入力待ち中のSIGINTもstdin読込みを解除してexit 130へ接続する。
I-16では`run --connect URL`で既存Coreへ依頼する。[操作契約](../plans/i16-cli-core-run.md)を参照する。
CLI内でDB・設定・認証を開かず、既存StartRunRequestとrun SSEで開始・進捗・終了を読む。
stdoutは従来と同じ最終回答または--streamの親本文、stderrにCore/Run/Session/Execution IDと進捗。
workspace省略時はCore既定、明示相対pathはCore基準、再開は保存Sessionのworkspaceを使う。
DB/config/credential等の起動設定はCore側で管理し、接続runへの明示指定は起動設定の案内で終了する。
max-steps/from-turnは明示時だけpointerで渡し、省略はCore設定、明示0はoverrideとして扱う。
Ctrl-C/SIGTERMはCancel APIと最終保存・会話配信の完了を待つ。通信切断やCLI強制終了は取消へ変換しない。
実行・配布・reviewの結果は[初期runの結果](../results/initial-run.md)を参照する。
逐次表示の追加実装と実provider確認は[run streamの結果](../results/run-stream.md)を参照する。

## 共通操作とTUI

`app.Service` が実行、Session一覧・取得、採用会話、execution一覧、inspectionを公開する。
実行は依頼・Session・明示model・現在workspace・Progress observerを呼出しごとに受け取り、
不変の初期optionsを複製して既存 `app.Run` に渡す。再開時のworkspaceは保存Sessionを使う。
接続を明示しない再開はSession保存selectionを使う。
`inspect` のcontextは構成と同じくJSONとして返す。

I-05以降は独立`hjh-tui`がCoreのHTTP APIを呼ぶ。I-16の接続CLIも同じCoreのrunへ接続する。
認証参照はCoreの専用credentials moduleで扱い、CLI/TUIの表示状態や履歴queryへkeyを渡さない。
Core停止時はbackendをcancel・joinしてからDBを閉じる。clientを閉じるだけではrunを取消さない。
詳しい操作は[TUI設計](tui.md)を参照する。

## I-02：親子実行

CLI・TUIは同じ `app.Run` を使い、rootのSession準備と、root／子の共通実行を分ける。
共通実行がAgent loop・Worker・journal・使用source保存・終了保存を接続する。
委譲操作は親で解決したconfig locatorを再captureし、子role.modelを親の実selectionへ適用し、workspace・step上限を
引き継いで独立した子executionを開始する。新しいSessionは作らず、子の会話はSessionへ採用しない。
正常完了したrootの会話だけを従来の採用操作へ渡す。

親のJS call終了や利用者の停止では、子の取消・Worker終了・終了保存・joinを待ってから親を終了する。
履歴保存のerrorはroot共通状態へ保持し、JSのcatchでroot成功へ変換できない。
元のJS例外と子の後処理errorが同時に発生した場合は、両方を終了errorへ残す。

進捗観測は `executionId`・`parentExecutionId` を持つ。
`run --stream` のstdoutは親本文、子の進捗はID付きでstderrへ出す。
TUIも子本文で親本文・model・stepを更新しない。
親inspectは `children` に直接の子一覧を返す。recordのordinal範囲でこの一覧を切り落とさず、
子のIDを使った別inspectで子の構成・会話・証拠を読む。

## I-03：role選択

built-in genericの共通Go loopへ外部roleのinstruction・選択toolを渡す。
rootのroleは--role／TUI明示選択、保存Sessionのrole、現在config.defaultRoleの順。
成功rootは会話・model・role choiceを同じtransactionで採用し、失敗・取消・子ではrole choiceを更新しない。
再開は現在configから保存roleを解決する。

hjh roles --workspace PATH [--config PATH]はcatalog名・説明・tools・defaultRoleを表示する。
DB・model接続を使わず、runと同じlocator準備と不足既定file配置を使う。
app.Service.RunはRunRequestのTask・SessionID・ModelID・Workspace・Role・Progressを受け取り、
app.Service.Rolesは現在workspace／初期ConfigPathのcatalogを返す。
CLI・TUIは同じ操作を使う。実行開始観測のinput.roleは実role名であり、子観測で親の選択状態を変えない。
executionのagentNameと使用snapshotに実roleを保存し、inspectで親子それぞれの構成を読める。

## I-04：接続選択・catalog・認証

Service.RunRequestのProviderID・ModelID・Effort・AuthProfileは実行ごとのpartial selection。
Service.Providers／ModelsをCLIとTUIで共有する。I-04当初のBeginLogin／CompleteLogin方式は、
I-12でCore所有の認証attempt方式へ置き換えた。現行操作はRegisterAPIKey、AuthAccounts／SelectAuthAccount、
BeginAuthAttempt／AuthAttempt／CompleteAuthAttempt／CancelAuthAttempt／DeleteAuthAttemptである。
CLIはServiceを使い、独立TUIは[HTTP API](http-api.md)経由で同じ認証ownerを使う。
送信requestはadapter.Prepareで一度生成する。同じWireBodyをtransportと最新snapshotへ渡し、
model_requestにはAPI呼出し前に小さなmetadataを保存する。全文はexecutionごとの最新1件をメモリ保持し、
終了確定後に専用tableへ保存する。毎request全文のarchiveは行わない。[保存契約](normal-history-storage.md)を参照する。
成功rootだけが会話と実selectionを同じ採用transactionで更新する。
取消・失敗・子は成功済みSession selectionを更新しない。
