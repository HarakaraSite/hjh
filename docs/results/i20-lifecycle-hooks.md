# I-20 ライフサイクルhookの実装と確認

> **後続変更の案内（2026-10-16）**：hook機構は実装済み。以下の配布・確認時には日時を注入したが、標準runtimeStartの日時注入はキャッシュprefix維持のため後続で停止済み。開始時刻情報とhook機構は維持する。[現行契約と停止理由](../design/extensions.md#i-20execution-lifecycle-hooks)を参照。

2026-10-12。状態：S1–S5の実装・test・reviewと総合実利用確認完了。
[五スライス計画](../plans/i20-lifecycle-hooks.md)に沿って実装した。
利用者指定により要約checkpointの生成・保存・再利用は実装していない。
先行する未コミットのTUI表示、実行情報、effort候補、DB容量メモ等の変更を保持している。

## スライスの結果

| スライス | 状態 | 実装・確認 |
| --- | --- | --- |
| S1 JS呼出し | 完了 | Hook/HookHandlers/Available、同じmodule state、async host/内側tool、欠落hook、例外・取消、既存Callの子join/drain。限定review必須findingなし |
| S2 開始文脈 | 完了 | 親子共通runtimeStart→beforeTurn、実requestへの追加、予算への包含、記録参照、source編集の次turn反映。review指摘を修正し限定再確認済み |
| S3 終了後保存 | 完了 | 専用post-settlement writer、terminal参照、ordinal/count更新、採用済み会話と結果の保持。限定review必須findingなし |
| S4 終了処理 | 完了 | 子清算→保存・採用→afterTurn→runtimeStop→Close、取消・hook例外・notice、Core完了/次turn/shutdown待ち。review指摘を修正し限定再確認済み |
| S5 通し確認 | 完了 | 実無料モデルの直接CLI/Core/並列子、TUI active detach/reconnect/次turn/復元、製品race/vet、配布物の展開実runが成功。最終限定review必須findingなし |

## 実装の契約

編集可能な単一ESM moduleから`runtimeStart`、`beforeTurn`、`beforeTool`、`afterTool`、
`afterTurn`、`runtimeStop`をexportする。未定義のhookは呼ばない。
既存tool前後の戻り値契約と、afterToolがtool成功後だけである動作は維持した。
新四hookは同じVM ownerで呼び、既存のcall清算を使うためQuiesce APIは増やしていない。

runtimeはCore/Session/TUI接続の寿命ではなく、各executionのWorkerを指す。
親の新turnと子実行は新Workerであり、開始・停止をそれぞれ実行する。
Go由来のstartedAt/timeZoneと解決済みselection、親子・実行IDを外部hookへ渡す。
credential値・Authorizationを渡すAPIは追加していない。

既存指示→runtimeStart寄与→beforeTurn寄与の順に今回のモデル入力を組み立てる。
capture済み設定snapshotは書き換えず、実export一覧をconfigured記録、追加文脈をhook結果と
model requestのinstruction attributionから辿れるようにした。空の実export一覧も記録する。
追加文脈は通常の予算管理へ入り、次turnへ累積しない。

afterTurnはterminal保存とroot採用判断の後で、保存時点の実行結果を受け取る。
採用だけが失敗した場合はnon_canonicalの結果とadoptionErrorを分けて渡す。
終了後recordはpostSettlement/terminalRecordIdとsettlement relationを持ち、
通常のAppendOccurrenceを一律に緩めず専用writerを使う。
確定terminal、outcome、adoption、messages、Session revisionは書き換えない。

JS hook失敗は詳細recordとnoticeへ保存する。終了hookの失敗は保存・採用済み回答を失敗へ戻さず、
afterTurnが失敗してもruntimeStopを続ける。通知は直接CLI/Core接続CLIのstderrと
TUIの通常履歴・復元に接続した。子の詳細表示を省く既存方針も維持する。
provider待機中の取消は生きているVMで清算する。active JS/host取消でVMが閉じた場合は、
呼べなかった終了hookをworker_unavailableとして保存し、別VMを作り直さない。

## focused確認とreview

S1/S3の限定review後、S2/S4を別の限定review対象として確認した。
指摘3件は採用し、実経路に対応するfocused testと限定re-reviewで解消した。

| 指摘 | 修正と確認 |
| --- | --- |
| 開始hookのJS例外がnoticeへ出ない | runtimeStart/beforeTurnの例外をnoticeへ接続。モデル未呼出し、runtimeStop、replay noticeを確認 |
| 採用失敗後のafterTurn結果が保存terminalと不一致 | 保存時のsettledResultを保持し、採用失敗を別fieldへ。同Sessionの競合producerによる実revision conflictで完全照合 |
| 空moduleの実export一覧が記録されない | module指定時はhandlers: []もconfiguredとして記録 |

主なfocused確認は、`TestLifecycle*`、`TestCoreWaitsForLifecycleCleanupBeforeTerminalAndShutdown`、
`TestAppendPostSettlementObservation*`、JS runtimeのlifecycle testsと既存取消/子/host tests、
既存の予算、非同期子、追加指示、CLI streamの関連tests。
appでは親と二子の文脈・終了順、次turnの編集反映、予算超過の完全保存、
終了hook失敗後の回答保持・live/replay noticeを確認した。
Coreではhook待ちの間にturnがcanonical保存済みでも次turnを受付せず、停止完了を待つことを確認した。

## 実モデルとCLI/Core/TUI

独立workspace `/tmp/hjh-i20-live-5pyqmcms`、独立DB/Core、保存済みAPIキーで確認した。
モデルは`nvidia/nemotron-3.5-lightning:free`。実行前の公開catalogでprompt/completion価格0とtools対応を確認。
有料モデル呼出し、新認証、利用者Coreの停止、利用者Sessionの変更は行っていない。

- 直接CLI execution `3d8d8989-e7d7-4dd4-86b6-7c1dd76f7149`：read_file、追加文脈、tool加工、終了hook、I20_DIRECT_OK。
- Core経由Session `c7363091`、root `932e8ab2-aace-410a-86ad-bc1531c0b5d4`：二子を並行spawnし、親が独立して読取り、両子をcollectしてI20_CORE_OK。
  子`a6aaa284`/`015907d7`の各requestにも子固有の追加文脈が入り、non_canonical保存と終了hookを確認した。
- Coreを再起動し、TUIから同Sessionを復元。hookのsource/contextを編集して次turnを送信した。
  execution `26242973-cda3-47eb-91dc-eaf16fe3aded`がactiveの間にdetachして再接続し、作業は完了した。
  alias-alpha.txtをhookがalpha.txtへ変更して読み、REV_B文脈だけを今回instructionへ入れた。
- このTUI turnではafterTurnに意図的なJS例外を入れた。I20_TUI_OKの回答とcanonical採用を保持し、
  runtimeStopを完了した。I20_NOTICE_FAILUREがlive画面と完了後の復元画面に残った。

保存照合は`dist/i20-verification/proof.json`、`tui-proof.json`。
検証script、CLI stdout/stderr、Core log、TUI captureと独立workspace内のhook出力fileを保存している。
各wire requestのinstruction、hookの順序とterminal ordinal、module state、採用状態を照合した。
TUI再接続で新executionやhookの再実行は起きていない。

## 総合gateと配布

安定候補に対し、製品sourceの`go list ./cmd/... ./internal/...`で列挙した31packageで
`go test -race -count=1`と`go vet`を一回ずつ実行し、成功した。
旧スパイク・ignored dist内の診断断片は製品gateの対象に含めない。
記録は`dist/i20-verification/gate-proof.json`とgate-race/vet.log。
`git diff --check`も成功している。

Linux/amd64配布archiveを生成済み。静的ELF確認、hash/license/ESM sourceを含むmanifestは
`dist/i20-verification/packages/build-zou9mhzy/hjh-linux-amd64/manifest.json`に保存した。
archive/binaryのhashとsizeの概要は`dist/i20-verification/package-build.json`に保存した。
`/tmp/hjh-i20-package-y25d6m4w/hjh-linux-amd64`へ展開し、両binaryのversion/hashをmanifestと照合した。
展開したhjhで直接CLIを実行し、execution `2359ed88-a7b2-4d4f-ab49-0f747b1f58f4`が
外部hookの追加文脈・tool実行・終了hookを通ってI20_PACKAGE_OKを回答し、canonical保存された。
標準runtimeStart例がarchiveへ含まれ、実runの文脈と停止完了を保存結果から確認した。
記録は`dist/i20-verification/package-proof.json`とpackage-run.stdout/stderr。
S5の最終限定reviewは、文書・証拠・検証script、独立DBの保存instruction、
配布二binaryのhash、workspaceの旧template/新標準例の一致を独立して確認し、必須findingなし。
配布物の実runは検証用外部configを指定した確認であり、標準例のarchive収録確認とは区別する。

## 実利用への反映

`dist/hjh`と`dist/hjh-tui`を更新した。
利用workspaceの`.hjh/extensions/hooks.js`は旧同梱の未編集placeholderと完全一致したため、
標準runtimeStartの開始日時例を配置した。変更前sourceは
`dist/i20-verification/workspace-hooks-before.js`へ保存した。利用者の独自hookを上書きする処理は加えていない。
新機構の反映には新binaryでのCore再起動が必要であり、利用中Coreは停止していない。
利用中Core ID `5a97e742-37ca-447e-8175-95035ddd59a6`が稼働継続していることを最後に確認した。
実装・確認時点ではcommit/pushを行わず、後続の利用者指示で先行する改善とまとめてcommit/pushする。
