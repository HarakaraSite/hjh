# I-17: go-quickjsへの移行結果

> **後続変更の案内（2026-10-16）**：engine移行は実装済み。本文の1 MiB Web上限は当時の値であり、現在は[Web defaults](web-retrieval-defaults.md)で通常64 KiBへ変更済み。engine移行と[stdlibへの移行検討](../notes/quickjs-stdlib-tools.md)は別の作業。

2026-10-12。JS実行基盤を`github.com/go-quickjs/go-quickjs v0.25.2`へ移行した。
[採用計画と確認対象](../plans/i17-go-quickjs.md)を参照する。

## 実装と責務

Workerの公開New／Definitions／Call／Close、Options、JSON入出力、外部tool／hook契約を維持した。
runtimeをWorker入口、VM・ESM接続、call・Promise終了処理、host／Agent bridge、観測へ分け、
一つのVM owner goroutineがRuntimeとValueを操作する。
capture済みsourceとmodule stateは同じWorkerの複数Callで保持する。

Go処理の完了queueと明示Promiseを保ち、engineのrt.Go／Awaitへ置き換えていない。
root callbackで子scopeを取消し、初回join→host／JS jobs drain→遅いAgentRunnerの再joinと
非delivery保存→委譲tool／hook終端closeの順で終了する。
JSのCallContext／RunJobsContextへ取消を接続し、原例外のmessage／stackをVM終了前にGoへ捕捉する。
正常Callのcontextは次Callへ残さない。host・AgentRunner・app・Core・履歴の責務とschemaは変更していない。

root moduleの旧modernc.org/quickjs／libquickjs依存を除去した。SQLite用modernc依存は残る。
過去の別module `spikes/s01` とその実験結果は変更していない。
新engineの任意stdlib、別のfetch／process実装、dual engine、fallbackは導入していない。

## tool互換性と終了処理

移行前HEADを別directoryへ展開し、同じ標準tool／hook sourceと新しい互換確認を実行した。
旧engineと新engineで、直接Callとrun_js経由のファイル読書き・完全一致編集、hookの引数／結果変更、
日本語・絵文字、commandのstdin／stdout／stderr／nonzero exit、HTTP 503の通常結果、
web_fetchの可読化・1MiB・JSON文字列・上限なしsave_toが通った。
JSON object／array／string／number／bool／null、undefined→null、Promise.allと内側run_js、
Go失敗のJS catch、原例外、準備済みYAML／semver ESMも確認した。
実ファイル・HTTP応答に対応する結果、観測の親子IDとcompleted終端を照合した。

既存runtime確認ではVM stateとcapture、選択tool、root記録の重複防止、並行／未await子、
host待機後の遅い子、正常host drain、例外時のcancel／join、JS無限loop・HTTP・process取消と
新Workerでの再開を確認した。app／hostのfocused確認では実AgentRunner・SQLite保存と
Session続行・source再capture・予算経路への接続を確認した。

通常reviewは必須findingなし。批判的reviewのP2を2件採用して修正した。

- host queue確認と処理の間に最後の結果が届くと、JS continuationを未処理で返せるため、
  drain終了判定へengineのpending jobsを含めた。競合はsourceで確認し、限定反復では未再現だった。
- Runtime.Throwが内部Thrownを返し、Promise rejectionのstackを捕捉できないため、
  rejected ValueからVM終了前にmessage／stackとGo causeだけを取り出すようにした。
  旧engineの実Workerでnamed frameのStack保存を確認し、新engineのroot／module例外へ
  同じframeを確認するregression assertionを加えた。

修正後の一回の限定再reviewで2件の解消と追加必須findingなしを確認した。
runtimeのfocused／race、対象vet、format、git diff --checkを確認した。
関連packageの最終race統合確認と配布確認は下記のとおり完了した。

## 実モデル・Core・TUI

private Core／SQLite／workspace／XDG state／tmuxで既存OpenRouter credentialを使った。
実行直前の公式catalogで`poolside/laguna-s-2.1:free`のprompt／completion単価0とtool対応を確認した。
追加認証、有料model、利用者の稼働Core／tmux mainの操作はしていない。

- CLI run --connectからモデルが生成したrun_jsで、input.txtを読みoutput.txtへ保存し、
  alpha→beta編集、読戻しと実shのcatで照合した。raw HTTP 503とHTML web_fetchも実行した。
- Session IDを使ってTUIを途中接続し、detach後もCoreが実行を続けることを確認した。
- hooks.jsを編集して同じSessionを続行し、read_fileのhookRevision変更を確認した。
  再開OKの行を追加し、実reviewer子Agentがfileを読み、I17_CHILD_OKを返して保存された。
- 2turnのSQLite保存、CLI最終回答I17_CORE_OK／I17_RESUME_OK、TUI再接続表示を確認した。

初回openrouter/freeはtoolを実行せず本文を返した。続くcohere/north-mini-code:freeは
Node globalsやobjectへのJSON.parseを誤生成し、修復を繰り返してproviderの429に達した。
これらを成功とは扱わず記録を残した。固定した別の無料modelへ切替え、既存toolの戻り型と
利用可能なJS APIを依頼文へ明記した実行で上記の作業が完了した。
確認用依頼には固定JS codeを与えていない。

## 最終統合・配布

安定候補に対して、計画で指定した統合確認を一回実行し、全対象が成功した。

```sh
go test -race ./internal/jsruntime ./internal/app ./internal/host ./internal/corerun ./internal/httpapi ./cmd/hjh
```

Linux/amd64、CGO_ENABLED=0、GOAMD64=v1のCore／TUIをpackageした。
展開した138fileのsize／SHA-256、両binaryにELF interpreterとdynamic sectionがないこと、
--versionの実hash／source情報とmanifestの一致を確認した。
PATHにGo／Node／Deno等を置かない環境で--versionを実行し、空workspace／XDG stateへfileを作らないことも確認した。
新engineのMIT、go-intlのUnicode license、fdlibmのSun／V8／Arm noticeをmanifestと照合した。
リンク依存に旧JS engineは含まれない。

展開した最終Core／CLI／TUIで上の保存Sessionを3turn目として続行した。
Go／Node／Deno等をPATHから外したCoreで、モデル生成run_jsから配布済みcatalog toolを呼び、
YAMLの読取りとsemverによる候補選択を実行した。保存したtool結果がproject、compatibleの
1.4.2、日本語noteに一致し、既存output.txtも保持された。
CLI最終回答I17_PACKAGE_OK、3turnの保存、配布TUIでの最終回答表示を確認した。
この確認済みペアをdist/hjhとdist/hjh-tuiへ反映した。

| 配布物 | bytes | SHA-256 |
| --- | ---: | --- |
| Core hjh | 48,458,281 | `30758d8d836f6bf4127f2ff26796a5474d31af8fe47996361c2a9f9b0506c047` |
| hjh-tui | 14,596,603 | `fbb8e50a6d4bd37837dbfd8c5594bbcd8f12100fdea61b19bcfaaaa71825bccf` |
| archive | 32,341,944 | `93094d76eca2e3e2c03f8583d835ba92a84e52be3a11b7b1b718e9c790be4ad6` |

archiveは`dist/packages/i17/build-sgexv24e/hjh-linux-amd64.tar.gz`。
source revisionは`a9f1e045bc61e624489757a0fd7958270ee1e9b9`、dirty=true。
I-17のlocal変更を含む未commit buildである。
前回の配布Coreは24,067,125bytesで、今回は24,391,156bytes（約23.3MiB）増加した。
TUIのbyte数は変わらない。Agent全体の速度比較は実施していない。

全provider／account、任意npm package、別OS／architectureの網羅確認は今回の範囲に含めない。
既に稼働している利用者のCoreはそのままで、新engineを使うには新binaryでの再起動が必要。
上記の配布buildと確認はcommit前に実施した。
