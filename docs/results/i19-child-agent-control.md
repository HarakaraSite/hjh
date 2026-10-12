# I-19 子agentの実装と確認

2026-10-12。状態：完了。
利用者は[六スライス計画](../plans/i19-child-agent-control.md)に沿った実装・test・スライスごとのreviewと、
最後のインクリメント全体の総合testを指示した。
[計画review](i19-plan-review.md)は通常・批判的とも必須findingなし。

## スライスの現在地

| スライス | 状態 | 実装と確認とreview |
| --- | --- | --- |
| S1 子の構成指定 | 完了 | model/provider/effort/toolsとJS引数bridge、共通子準備。focused race、通常・批判的review通過 |
| S2 親実行単位の子管理 | 完了 | 子の寿命、開始・状態・回収・取消、終了保存。focused race、取消の読戻し修正と両再review通過 |
| S3 非同期toolとJS接続 | 完了 | 標準toolと生成JS、モデル向け案内 |
| S4 報告と差分取得 | 完了 | 保存済みassistant本文、cursor、親への結果保存 |
| S5 親から子への追加指示 | 完了 | 受付、適用、未適用理由、子の続行 |
| S6 総合確認と仕上げ | 完了 | 全体test、実モデル、Core/TUI再接続、履歴、配布 |

## 作業範囲

開始時のHEADは`1a8e899a1ec963f86df6ca245dc261b55173baeb`。
先行するフッター、Markdown・色表示、run_js英文目的コメント等の未コミット変更は維持する。
I-19のproduct動作の検証には独立workspace・DB・Coreを使い、利用中のCoreとSessionを停止しない。
commitとpushはこの実装指示に含めない。

## 総合確認

完了。S1–S5の安定候補でsource package全体のrace/vetと配布buildが通過。
実モデルとCLI/Core/TUI、配布物の直接run、保存履歴の確認を完了した。
testやreviewの成功だけで実利用が完了したとは扱わない。

## S1の結果

子準備と実行を共通化し、roleのtoolsをcapture時に実行用へ置換した。
省略はroleの一覧、空配列はtoolなし。元のconfig bytes/fileは保持する。
modelは親selection→role→呼出し指定の順に既存resolverで解決する。
実request、子VMの操作、保存snapshotの一致を確認した。

focused確認（各コマンドの通常実行と`-race`実行が成功）:

```sh
go test ./internal/extensions -run 'TestCaptureOptionsReplaceToolsWithoutChangingConfigurationBytes|TestEmptyRoleHasNoModelToolsOrSnapshotTools|TestCaptureResolvesRoleToolsDefinitionsAndCatalog'
go test ./internal/app -run 'TestDelegationUsesSelectedConfigAndFreshChildSource|TestDelegationAppliesChildModelAndToolOverridesToRequestAndSnapshot'
go test ./internal/jsruntime -run 'TestAgentRunReturnsResultAndUsesDedicatedCallID'
go vet ./internal/contracts ./internal/extensions ./internal/app ./internal/jsruntime
```

通常・批判的reviewに必須findingなし。実providerと総合確認はS6で行う。

## S2の結果

親executionのGo ownerが準備中と開始済みの子を所有する。
spawnはadmission後に返り、statusは保存後の進捗を読む。collect/cancelは保存終了を待ち、
collect/cancelの操作から子終端へ関連付ける。親終了では準備中を含む子を取消・joinしてから親終端を保存する。
子結果と終了済み子を保持し、再collectで再実行しない。

```sh
go test -race ./internal/app -run 'TestChildRuns|TestDelegation' -count=1
go test -race ./internal/app -run 'TestChildRuns' -count=1
go vet ./internal/app ./internal/contracts
```

二つの子の並行実行、一方だけの取消、別の子の完了と再回収、親終端より前の子保存、
終了後の開始受付閉鎖を確認した。gofmtと`git diff --check`も成功。
両reviewerのP2は、collect後の終端読取りがcaller取消を共有storage failureへ誤分類する経路。
`context.WithoutCancel`で保存済み終端を読み、限定再reviewで両者とも解消確認。
取消並行testは親control維持を確認するが、旧経路の再現証拠とは扱わない。

## S3の確認済み結果

WorkerのAgentControlから親executionのownerへPromiseを接続し、spawnはadmissionまで待つ。
rawな外部tool用操作と、生成JSの選択tool経由操作を分け、有効なmethodだけを公開する。
標準default roleへspawn/status/collect/cancelを登録し、モデル向けinstruction/catalogとpreviewを揃えた。

```sh
go test -race ./internal/extensions -run 'TestInstallCaptureAndLegacySnapshot|TestSpawnOnlyRoleCapturesRoleCatalogAndSelectedJSOperations|TestCaptureOptionsReplaceToolsWithoutChangingConfigurationBytes|TestEmptyRoleHasNoModelToolsOrSnapshotTools'
go test -race ./internal/jsruntime -run 'TestAgentControlBridgeUsesSelectedToolsAndDedicatedCallIDs|TestAgentRunReturnsResultAndUsesDedicatedCallID|TestUnawaitedAgentIsCancelledAndJoinedOnSettlement|TestAgentRunsCanCompleteConcurrentlyWithPromiseAll|TestSelectedToolsControlGeneratedRuntimeAPI'
go test -race ./internal/toolactivity -run 'TestAgentControlPreviewsShowTaskOrExecutionID|TestRunJSPreviewShowsLeadingPurposeComment'
go test -race ./internal/app -run 'TestAsyncChildrenThroughRunJSAndSavedHistory|TestDelegationParallelResultsEvidenceAndResume|TestDelegationUnawaitedChildSettlesBeforeNextModelStep|TestChildRunsParallelCollectAndIndividualCancel|TestChildRunsCloseJoinsBeforeParentTerminal'
go test -race ./internal/app -run '^TestRoleChoiceAdoptionResumeAndCurrentConfig$' -count=1
go vet ./internal/contracts ./internal/extensions ./internal/app ./internal/jsruntime ./internal/toolactivity
```

二子の並行spawn→親独立read_file→次stepのstatus→片方collect/片方cancelを確認。
開始と終端relation、child live/replay相関も確認。既存run_agent、未await取消、Promise.allも通過。
通常・批判的reviewに必須findingなし。実モデルはS6へ残す。

## S4の確認済み結果

agent_statusへafter、reports、nextCursorを追加。通常assistant本文とtool併記本文を
既存messagesから読取り、thinking/tool/replay/生成中stateは返さない。
本文と上端message ordinalは一つのSQL statementのsnapshotで取得し、Goへtool本文を持ち出さない。
読取りは共有の既読状態を変更しない。

```sh
go test -race ./internal/history -run '^TestAssistantReports' -count=1
go test -race ./internal/app -run '^TestChildReports|^TestChildRuns|^TestAsyncChildren' -count=1
go test -race ./internal/extensions -run '^TestSpawnOnly|^TestEmptyRole|^TestCaptureOptions' -count=1
go vet ./internal/history ./internal/app ./internal/contracts ./internal/extensions
```

本文の選択、保存元ordinal、最終本文、空差分、同cursor再読、並行保存中に飛ばさないことを確認。
実app/JS経路で、status報告が親の通常tool結果と次request、採用履歴へ入ることを確認した。
gofmtとgit diff --check成功。通常・批判的reviewに必須findingなし。

## S6の準備中確認

S5の実装と並行して、既に成立した非同期経路のCore再接続確認を追加した。

```sh
go test -race ./internal/corerun -run '^TestCoreAsyncChildrenSurviveDetachAndJoinOnParentSettlement$' -count=1
```

接続を閉じても二子がactiveで続行し、再接続の保存cutから同じ二子を復元する。
個別取消＋別子回収と、親取消での二子join/終端保存を確認した。
これは制御可能なproviderによるCore経路の確認であり、実モデル/TUI確認と総合gateはまだ未実施。

## S5の確認済み結果

親のsteerから子専用instruction ownerへ受付し、既存semantic_records/contentsへ保存後acceptedを返す。
status.instructionsは最新receiptを返す。子のAdditionalInputが同一transactionでmessageとappliedを保存し、
通常loopへ渡す。未適用理由は子終端前に保存する。Core/F3の受付ownerは継承しない。

```sh
go test -race ./internal/app -run 'TestSteerChildDuringToolAndFinalResponsePersistsOrderedInstructions|TestSteerCancelledChildPersistsUnappliedReasonBeforeSettlement|TestFinalInstructionBoundaryWaitsForInFlightAcceptanceSave|TestSteeringAdapterAppliesReceiptAndReusesAcceptedMessageContent|TestRoleChoiceAdoptionResumeAndCurrentConfig' -count=1
go test -race ./internal/jsruntime -run '^TestAgentControlBridgeUsesSelectedToolsAndDedicatedCallIDs$' -count=1
go test -race ./internal/extensions -run 'TestInstallCaptureAndLegacySnapshot|TestRunJSSelectedOperations' -count=1
go test -race ./internal/toolactivity -run 'TestAgentControlPreviewsShowTaskOrExecutionID' -count=1
go test -race ./internal/agent -run 'TestFinalSteeringContinuesSameRunWithMessageInNextRequest|TestFinalSteeringDoesNotApplyAfterStepBudgetIsExhausted|TestFailureAfterFinalSteeringIsNotReportedAsSuccessful' -count=1
go vet ./internal/app ./internal/agent ./internal/contracts ./internal/extensions ./internal/history ./internal/jsruntime ./internal/toolactivity
```

appの同focused群とagentの同境界群の通常testも成功。
子command中の二指示の順序と次request、final model_result後の三指示目による続行、
取消時unapplied/cancelled理由と保存message/receiptを照合した。
SQLite writer lockで受付登録済み・保存未完了の状態を作り、final境界がlock外で保存を待ち、
解放後のaccepted結果、user message、applied receiptを確認した。
この保存待ちのfocused testは単独通常実行（count=10）も成功。
gofmt、config JSON、git diff --checkも成功。通常・批判的reviewに必須findingなし。

## S6の総合gateと配布build

初回の`go test -race ./...`と`go vet ./...`は、ignored dist内の古い診断断片
`dist/i06-live/user-render-diagnosis/render_diagnosis_test.go`が同moduleとして探索され、
元のtest helperを持たないためcompile失敗した。保存資料は削除せず、go listの`hjh/dist/`だけを対象から除いた。
初回製品testではTUIの二つの旧assertionがANSIを含む本文を連続したplain文字列として比較していた。
出力自体のheader順序と復元の重複防止は正しく、比較へansi.Stripを加えfocused raceで確認した。
production表示コードはこの修正で変更していない。

```sh
go test -race ./internal/tui -run 'TestInitialInputCanRunBeforePreviewAndActualWinsHeaderBeforeBody|TestResumeAttachesExactRunAndCurrentSessionSelectionKeepsIt' -count=1
python3 dist/i19-verification/source_gate.py race
python3 dist/i19-verification/source_gate.py vet
python3 scripts/package-linux.py --out dist/i19-package
```

source_gateは`go list ./...`からdistを除いた全36 source package（cmd/internal/spikes）を構造化引数で実行する。
全体raceとvetは成功。同じpackage集合は次の通常コマンドでも指定でき、go listの集合一致を確認した。

```sh
go test -race ./cmd/... ./internal/... ./spikes/...
go vet ./cmd/... ./internal/... ./spikes/...
```

再実行の理由は上述の探索対象とANSI assertionの具体的な失敗の解消である。
配布buildは`dist/i19-package/build-20zatom8/hjh-linux-amd64.tar.gz`。
archive SHA-256は`b81a137e68fbc6d0313934870ecb82e77f83af673d81032bbd73191c5fbd3984`。
両binaryのELF interpreterなし、153 manifest file hash、agent-control.jsの同梱を確認した。
新binaryをdist/hjhとdist/hjh-tuiへ配置済み。

## S6の実モデル確認経過

無料料金とtool対応は[OpenRouter公式catalog](https://openrouter.ai/api/v1/models)で実行時に確認した。
各試行は独立workspace/DB/Core/XDGを使い、保存済みAPI keyを参照した。

- 第一試行は二子の回収と最終回答まで進んだが、親が子taskから待機条件を省略し、子終了後のsteerが拒否された。
  最終回答はSTEER_APPLIEDを主張したが履歴にaccepted/appliedがなく、成功とは扱わない。
- 第二試行は親も子の待機処理を選び、双方が待機したため検証用Coreを停止した。
  親と実行中子のcancelled終端、既に終了した別子のcompleted終端が保存された。
- 第三試行はinclusionai/ling-3.1-flashの上流shared pool 429で停止した。
  有料接続/新認証は使わず、別の無料tool対応モデルへ切り替えた。
- 次の試行では親taskを短く分け、workspaceの待機shell scriptは子だけが実行する。
  親の検証roleではcommandを選ばず、子へexplicit toolsでcommandを指定する。
  この試行は完了し、受付/適用receiptと実requestの保存照合まで成功した。

各試行の資料はdist/i19-verification/attempt-1〜attempt-3。総合gateと配布hashの資料も同directoryにある。

## S6の実モデル成功結果

Session `15a96b20`、root execution `ef5099e9-cc38-4e0f-8dbc-dbf588b0ca12`。
親はOpenRouter Chatのnvidia/nemotron-3.5-lightning:free、AlphaはOpenRouter Responsesの同model・auto、
BetaはOpenRouter Chatのnvidia/nemotron-3-super-120b-a12b:free・low。
Alphaの実toolsはrun_js/read_file/command、Betaはread_fileのみ。
親roleにはcommandを選ばず、子へ明示toolsで指定する実経路も成立した。
Betaの実wire request二回にreasoning_effort=lowを確認した。

保存済みAPI key一つを使い、親が自然文から二子を開始し、独立してlocal.mdを読んだ。
親モデルは補助確認用のblocking子も一つ追加した。二つの調査と別の補助executionを正当に保存し、
検証側の「executionが厳密に二件」の仮定を除いた。全子はcompleted/non_canonicalとして終了保存された。

Alphaの中間finding（rollback手順なし）をstatusで取得後に親がsteerし、
instruction `7e7d492c-c193-477d-9f69-ecc5628b541d`のaccepted→appliedを確認した。
指示body digestと子message ordinalが一致し、適用後の実model requestに本文が入り、
最終報告へSTEER_APPLIEDが出た。親は両結果を回収してI19_LIVE_OKを回答した。
Betaでは以前のnextCursor=2をafterへ渡し、後から保存された新しい報告を取得した。
Alphaの追加指示後のstatusはafter省略による全報告再取得であり、差分と呼び替えない。
Alphaの同cursor再読と新しいfinal報告の差分はS4のfocused実app/JS確認で成立している。

CLIのCore接続run、TUI attach/detach/同Session再接続、完了後の復元、
別processのhistory/inspectを照合した。live/restored API viewにも各子executionが残る。
最初の空statusや絶対pathのread_fileを正当な応答として扱い、保存済み本文があるstatusを
steer前の中間報告として選び直して検証した。再モデル実行で証拠を作り直していない。
保存照合は `python3 dist/i19-verification/verify_saved.py` で成功。
proof.json、terminal.json、view.json、各inspect、history.txt、TUI captureとtask.txtが証拠。

モデルに手順を伝えるだけで常に同じ操作順が選ばれる保証はない。
終了済み子をsteerで再起動する契約は追加せず、親はstateとreceiptを読んで判断する。
これは先行試行からの具体的な観測であり、tool側の成功/失敗を最終回答だけで置き換えない。

## S6の配布と実利用設定

archiveを独立directoryへ展開し、両binaryのversion/hashと、展開したhjhの直接CLI runで
spawn→親の独立読取り→collect→PACKAGE_I19_OKを実無料モデルで確認した。
検証directoryは`/tmp/hjh-i19-package-y0754u8q`。
package-run-proof.json、package-run.stdout/stderr、各versionが証拠。
Core接続/TUIは同じpackageのbinaryをdistへコピーしたものを使った。

実利用の`.hjh/extensions/config.json`へ新五toolを登録し、run_agentのmodel/tools schemaと説明を更新した。
既存run_agentを選ぶroleへ五toolを追加し、agent-control.jsを配置した。
model/providers/credentialsの設定は保存した値と一致することを確認し、`./dist/hjh roles --config ...`でcaptureを確認した。
変更前configはdist/i19-verification/workspace-config-before.jsonへ保存した。
利用中Coreの停止操作は行っていない。新機能の利用は新binaryでCoreを起動して行う。

S6の通常・批判的reviewは完了し、両者とも必須findingなし。

## 完了判断と確認範囲

S1–S6で実装、対応するfocused確認、通常・批判的reviewを完了した。
S2のP2（caller取消をstorage failureへ誤分類する読戻し）を修正し、両限定再reviewで解消を確認。
全36 source packageのrace/vet、静的配布と展開実run、実モデルの報告/構成/指示/回収と保存照合が成功した。
必須残作業はない。

実モデルTUI captureは同Sessionへの再接続と完了後復元を示すが、最初の再接続は子開始前だった。
子active中のdetach継続と同親子のcut再取得、親取消joinは制御providerを使ったCore経路の確認が根拠である。
実モデルTUI captureだけで「子active中のdetach再接続まで観測済み」とは述べない。
親の正常終了後/次turn/Core再起動後に子を存続させる機構と、人間による子直接操作は今回の対象外。
利用時は更新済みdist/hjhでCoreを起動し、dist/hjh-tuiから接続する。
