# I-05：コアとTUIの分離・実装結果

2026-10-10、Linux/amd64、Go 1.27.2。通常・批判的な計画再reviewの指摘を反映し、
`hjh serve`と独立した`hjh-tui`をHTTP＋JSON＋SSEで接続する実装を行った。
実装の通常・批判的再reviewはともにready。実利用とLinux配布物の確認を完了した。

## 実装

- CoreはSession/run、provider、credential、QuickJS、履歴DBを所有する。
- `internal/api`は純粋な公開DTO、`internal/corerun`はrunの寿命・予約・表示状態、
  `internal/httpapi`はHTTP対応、`internal/apiclient`は通信を所有する。
- client生成run IDで開始response喪失を回収する。同じIDと内容の再送でexecutionを増やさない。
  同じSessionは準備開始前からterminalまで予約する。
- SSE切断は購読解除。表示状態はsnapshotと累積更新で復元し、HTTP書込みはAgentのobserverを止めない。
- Esc/new/resume/quitは明示cancelとterminal待ち。Core停止はSIGINT/SIGTERMで実行・子・worker・認証をjoinする。
- maxSteps/fromTurnの省略と明示0を区別する。保存schema/payloadの契約は変更しない。
- Linux配布物にCoreとTUIを別binaryで同梱し、個別の依存・size・digestを記録する。

操作とwire形式は[HTTP契約](../design/http-api.md)、範囲は[計画](../plans/i05-core-tui-separation.md)。

## Reviewと修正

| 採用した実装指摘 | 修正と確認 |
| --- | --- |
| P2：POST受理未確認の通信失敗でhandleを失う | 既知ID・request・handleを保持して回収。復旧後の同一run、cancel、client closeのfocused test |
| P2：cancel失敗を保持し、再取消できない | 同時呼出しは同じ試行へjoinし、失敗後の明示操作は新しく送信。focused test |
| P2：取消goroutineとterminal処理のhandle競合 | goroutine開始前にhandle/IDを捕捉。terminal先行のfocused test/race |
| P2：login表示が次runの接続選択を変える | 解決済みlogin scopeだけ保持し、選択を維持。focused test |
| P2：terminal snapshotへ非表示の保存証拠が混ざる | journalのexecution ID付きObservationのみ復元。初回実providerの保存記録で5件の混入を観測し、実Service/Storeの復元testで修正確認 |

通常再reviewは当該4件、批判的再reviewは統合した5件の解消を確認しready。
再reviewは変更箇所と既存findingに限定した一回。追加の必須修正はない。

## 完了した確認

Python標準HTTP clientからOpenRouter Responses、`deepseek/deepseek-v4.1-flash`で実行した。
親はreviewer子にinput.txtを読ませ、output.txtへI05-HTTPと結果を書いて読み戻した。
開始responseを読まず接続を閉じ、既知run IDで回収した。本文受信後にSSEを切断して再購読し、
同じrun IDの再送でもroot executionが増えず、正常結果の会話採用を確認した。
親1件・子2件、240状態frame、採用会話6messages。記録は`dist/i05-live/api-summary.json`。

非対話CLIの`run --stream --session ...`でも同じ成果物を読み、終了0・本文と進捗を確認した。
記録は`dist/i05-live/cli-summary.json`。

focused test、apiclient/TUIとcorerun/httpapiのrace確認、対象packageのvetが成功した。
安定候補に対してrootが`go test ./...`を一度実行し、成功した。
記録は`dist/i05-live/full-test.txt`。

`go list -deps`と配布binaryのbuild metadataで、CoreからBubble Tea/Bubbles/Lip Gloss等、
TUIからapp/history/SQLite/QuickJS/provider/credential/extension実装への依存が外れたことを確認した。
静的配布物は両binaryともELF INTERPなし。manifest内143fileのdigestを照合した。

## 認証確認の扱い

既存の保存済みChatGPT認証は接続済み。HTTP login開始でURLと解決済みscopeを受信し、
request終了後もpendingはCore所有となるsourceを確認した。
利用者は検証のたびにブラウザ認可を繰り返すことを望まないため、今回の新規callback完了は実施しない。
保存済み認証の再利用・自動refreshはI-04で実確認済み。新規callbackとrefreshの既存testも全体testで成功した。

## 配布物・独立TUIの実利用

配布archiveを展開し、CoreとTUIを別process・40行120列の実PTYで動かした。
日本語を含む複数行依頼、draftとcursorの一致、roles/providers/models、保存Sessionの
history/executions/inspect/pickerを確認した。親はreviewer子にinput.txtを読ませ、
tui-result.txtへI05-TUIと結果を書き、読み戻した。

実行中に次回providerをOpenCode Go Chatへ変更しても、実行中のOpenRouter選択は維持された。
次のGo Chat（`deepseek-v4.1-flash`）でrunner子の60秒commandを開始し、ready file生成後にEsc。
親子はcancelled・non_canonicalへsettleし、後続のlate fileは生成されなかった。
その後のGo Chat runでI05-GO-CONTINUEDを追記・読み戻しできた。
同じ子commandの開始を確認してquit/new/resume/Ctrl+Cも実行し、いずれも親子のterminal保存後に
終了またはSession切替へ進んだ。TUI終了後もCoreは同じcore IDで利用でき、端末のalternate screenが復元された。
明示`--max-steps 0 --from-turn 0`も別TUI起動から送り、rootのmaxSteps=0保存を確認した。
fromTurn=0の具体的な会話参照は実Service/Storeのfocused testで確認した。

別workspaceの新規SessionをHTTPから作り、保存済みChatGPT認証でGPT-6-Luna（effort low）を使った。
input.txtを読み、oauth-result.txtへI05-OAUTHを含む結果を書き、読み戻した。
Session優先のrole catalogはalternate、認証状態はconnected、model catalogは7件。
終了後のSSE snapshotに本文と表示Observationが復元され、空のexecution ID/phaseや非表示証拠が混ざらないことも確認した。
CoreへSIGTERMを送り、実行中のGo Chat親子commandの取消・terminal保存・joinと終了0を確認した。

記録は`dist/i05-live/tui-package-summary.json`、`tui-switch-summary.json`、
`fixed-terminal-events.json`、各screen/rawと`package-summary.json`。
APIキーと保存OAuth tokenの実値がDB・受信JSON・PTY・検証ログにないことを、値を表示せず照合した。

| 配布binary | size（bytes） | 起動後のRSS観測（KiB） |
| --- | ---: | ---: |
| Core `hjh` | 22,392,185 | 15,220 |
| Client `hjh-tui` | 12,779,273 | 20,384 |

RSSはCoreのlisten開始後、TUIの初期Session読込み時の単回観測であり、peakや性能比較ではない。
archiveは`dist/i05-packages/build-janwq4od/hjh-linux-amd64.tar.gz`、19,583,859 bytes。
SHA-256は`12d07facad76ef24863c3f9aba2f75fb32178533b85f34f283aaad58dbf428c3`。

Web UI、remote公開運用、別OS、UI操作の再設計は対象外。通信方式自体の性能比較は実施していない。

## 利用者起動先の設定修正

利用者が`/home/agent/projects/hjh`をworkspaceとして起動した際、残っていた初期版の
`.hjh/extensions/config.json`にdefaultRoleがなく、設定読込みが失敗した。
初回の実利用検証は独立した検証workspaceで行っており、案内先の既存設定の確認が不足していた。
既存JSと共通設定が未編集の初期標準であることを確認し、元のJSONを
`.hjh/extensions/config.before-i05-20261010T143952Z.json`へ退避して現行標準設定に更新した。
利用者の稼働中Coreからroles/providersを取得し、default/reviewerと5接続が読めることを確認した。
履歴は変更せず、自動migrationや互換読込みも追加していない。

## TUI表示の利用者確認と次のインクリメント

Session `eb70b5e9-c2dc-4189-9dd2-66664756eb49`で、run completed後に最終回答が見えないとの報告を受けた。
実行は正常完了し、回答も会話へ保存済みだった。最後の本文の後にusage/endが発生している。
現TUIは本文とprogressを別グループで並べ、末尾へスクロールするため、回答が画面外に隠れる。
利用者はイベント表示によるものと理解し、今回の局所的な表示順修正を不要と判断した。
試行したソース修正と回帰testは取り消し、利用者の起動中binaryも変更していない。
次のインクリメントではTUIの使い勝手をまとめて改善し、回答とイベントの見せ方もそこで扱う。
