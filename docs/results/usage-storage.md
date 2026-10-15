# Provider usage保存整理：実装・確認結果

2026-10-15。利用者が[計画](../plans/usage-storage.md)の独立レビュー後に実装・test・code reviewを指示した。
常用DB・設定・稼働中Core・backupは変更しない。外部providerは呼ばず、一時DBとローカルHTTPを使う。

| スライス | 状態 |
| --- | --- |
| S-1 通常親子の単一保存 | 実装・focused test・独立review／指摘修正の再review完了 |
| S-2 要約Hostの保存粒度 | 実装・focused test・独立review完了、指摘なし |
| S-3 要求識別・履歴利用・総合確認／測定 | 完了。既存test期待2件を利用者承認で修正し、独立review・総合test通過 |

実装前sourceは未commit変更を含むtracked filesをtarへ捕捉し、before binaryをbuildした。
検証artifactは`tmp/usage-storage-PylRnNae/`へ置き、常用のdistへ配置しない。
実行した確認と測定結果は、完了した範囲だけを以下へ追記する。

## S-1：通常親子

`usageStorageJSON`はRawMessageのobject直下fieldだけを除去し、編集不要なJSONはそのまま返す。
usage観測の保存用Resultからattribution、model_resultの保存用Resultからusageを外す。
SaveAssistantResult分岐も保存用projectionを通し、本文・thinking・要求・relationsの確定は維持した。
元Observation・protocol・ModelResultの生成は変更していない。

ローカルSSEのChat（attributionなし）／Responses（あり）で親→子→親回答を通し、
close/reopen後のRecordsとService.Record、同番号physical requestの親子分離とparent_call、
集計detail・0・field有無、モデル結果の他field、live両観測と直接ModelResultのrawを照合した。
独立reviewでChat fixtureへ未観測attributionを追加していた確認不足P3を採用し、
実受信形状へ修正した。focused再確認と独立再reviewで解消、製品コードの追加指摘なし。

最終のS-1 focused確認：

```sh
go test ./internal/app -run '^(TestUsageStorage.*|TestLifecycleDefinitionsRemainAvailableButAreNotArchived|TestNestedToolResultLiveAndReplayFieldsMatch|TestModelResultIsReadableWhenGenerateStopsBeforeAppendingMessage|TestJournalKeepsAssistantAndThinkingPositionsThroughMessageFinalization|TestLatestRequestBodyMatchesHTTPAndPhysicalRequestKeepsMetadataOnly|TestStandardOpenCodeChatSavesPublishesAndReplaysThinking)$' -count=1
```

通過（0.912s）。P3修正後の`go test ./internal/app -run TestUsageStorage -count=1`も通過。
初回test作成中のAPI名誤りによるcompile失敗は現行APIへ直した。製品の契約変更は不要だった。
既存のhistory会話保存・RecallEvidence・tool原Result、TUI footer推定表示のfocused確認も通過。
同時に指定したrecall packageには該当名のtestがなく、recall packageの実行確認とは数えていない。
gofmtとdiff checkは通過。全体testはまだ行っていない。

## S-2：要約Host

保存用Observationの整形をjournal.persistの値コピーへまとめ、PayloadとObservedResultが
同じ縮約済みResultを使うようにした。対象はhost/context.model.generateのusage直下だけ。
要約のJS返却値・元Host観測・protocolは変更せず、無関係のtool/effectの原Result bytesも維持する。
実JS projectRequestで同じhook内に2回呼出し、hook/live raw、contents集計値、
outputBytes/finishReason、Host/hookとcall_start/parent_call、close/reopen後Records/Record、
通常rootのwire/budget/context推定を確認した。独立reviewは指摘なし。
通常完了と終了時回収が同じcompletion.observationValue→Observer→journalへ合流することもsourceで確認した。
終了時回収だけを発生させるattribution付き成功結果の専用testは追加していない。

親による最終focused確認：

```sh
go test ./internal/app -run '^(TestUsageSummaryStorage.*|TestUsageStorage.*|TestNestedToolResultLiveAndReplayFieldsMatch|TestRequestModelUsesParentSelectionWithoutPersistingItsTranscript|TestLifecycleDefinitionsRemainAvailableButAreNotArchived)$' -count=1
```

通過（1.153s）。既存retry・root通知・子予算分離・projectRequest/再開のfocused確認は2.247sで通過、
recallの保存tool証拠とsuperseded除外のfocused確認も通過（0.003s）。

## S-3：実CLI／Core／PTY TUI

新規script `scripts/verify-usage-storage.py`
を一回実行し成功した（2026-10-15 21:15 JST）。
scriptは実CLI/TUIを一時binaryへbuildし、隔離HOME/XDG/workspace/configと新規DBで実Coreを起動する。
設定providerは署名なしローカルHTTPだけ。常用Coreを探索・停止せず、script所有のPopenのみ終了する。

```sh
python3 scripts/verify-usage-storage.py --output-dir /home/agent/projects/hjh/tmp/usage-storage-PylRnNae/acceptance
```

- 親run_jsから独立read_fileと子spawnを行い、collectして最終回答を返した。
- 同一projectRequest hookからmodel.generateを2回呼び、JSで完全usageを検証した。
- HTTP 500で既存の一回再試行を行い、同じlogical step・別physical request・retryAttempt・supersededを照合した。
  失敗試行に架空のusageは作らず、成功通常6要求とHost2呼出しのusageが各一回保存された。
- script所有の一時Coreを停止し、別プロセスで同じDBを開いてSessionを再開。
  次のHTTP入力へ保存したtool・子結果・親回答が入ることを確認した。
- 接続CLIのsession/canonical/detail、Core Inspect/Recordを実際に読み、本文・thinking・関係と縮約usageを照合した。
- PTY TUIから次turnを実行し、footer `ctx 7%`と/context `Estimated input · 6047 tokens`を確認した。
  予算80,000、wireBytes 17,875、推定式(wireBytes+2)/3切捨て+16+8×9=6,047と一致する。
  providerの約700,000 input tokensではなくhjh推定のまま。

証跡：
- `tmp/usage-storage-PylRnNae/acceptance/usage-storage-s3-_i32tege/report.json`
- 同directoryのHTTP入力／応答、CLI出力、Core API JSON、Coreログ、tui.raw/tui.txt、検証DB。
- 候補CLI SHA-256: `d51b549937c5d02fa6b2685f75dfae292881fa7cc6e5d7499470d5bc08d7ca3e`

scriptのreport.unverifiedはscript単体の対象外を表す。Responses・Go Observer/rawはS-1/S-2と測定harness、
容量/I/Oは次節で別に確認しており、実providerは全経路で未実施のまま。
script内のChatに付けたattributionは保存/live分離を明確に観測するための合成拡張であり、
Chatでそのfieldを実受信したという根拠にはしない。実受信Chat形状はS-1と測定Chatで別に確認した。

## 同一usage再生の保存容量・書込み量

常用DBはusageと識別metadataだけをmode=ro/query_only/BEGINで採取した。
2026-10-15 20:57 JST取得、全source record IDsと時刻はmeasure/input-evidence.json。
113件ResponsesとChat1件を同じ固定入力としてbefore/afterへ再生し、
入力SHA-256 `92900c44da484e3ac8f1a5fb69b030e0d7dc283ae2e3eaf712aeb9cfcb702030`
が全reportで一致した。

元Sessionの会話全文ではなく、固定合成task/answerを使った113個別Sessionの新規保存比較である。
要約は実JS HostでResponses usageを2回再生した確認で、実要約の受信実績とは扱わない。
before/afterとも同じharness、endpoint、workspace path、ext4 filesystem、WAL/FULL/default autocheckpoint。
独自checkpoint/VACUUMなし。ランダムID・時刻等の微小な差をusage改善の効果として断定しない。

### Responses 113件

| 指標（bytes） | Before | After | 削減 |
| --- | ---: | ---: | ---: |
| 通常usage object合計（二重→単一） | 5,198,974 | 20,167 | 99.61% |
| semantic_recordsの保存payload JSON | 10,240,586 | 5,060,755 | 50.58% |
| unique contents実bytes | 4,346,938 | 4,346,938 | 0% |
| payload+unique contents+latest wire/budget | 18,965,134 | 13,785,297 | 27.31% |
| dbstat全table/index割当／close後DB | 20,905,984 | 15,609,856 | 25.33% |
| process write_bytes（開始sample〜全close） | 136,011,776 | 118,444,032 | 12.92% |
| process wchar（同区間） | 133,222,598 | 115,679,493 | 13.17% |

集計usageの20167 bytesはそのまま保持し、attributionは保存0、model_resultのusage重複も0。
113件すべてcompleted/canonical、226 messages、同じ件数のRecord読戻しを維持した。
両DBのfreelist_countは0、page_size 4096、page_count 5104→3811。
semantic_records割当は10,866,688→5,558,272 bytes。
logical合計は選んだ保存本文項目だけで、DB全列の論理量やdbstat payload量ではない。

| 物理観測点 | Before DB / WAL / SHM | After DB / WAL / SHM |
| --- | --- | --- |
| 最後のmodel_result後・終端前 | 20,668,416 / 4,198,312 / 32,768 | 15,122,432 / 4,165,352 / 32,768 |
| 終端保存・会話採用後、接続保持中 | 20,668,416 / 4,198,312 / 32,768 | 15,122,432 / 4,165,352 / 32,768 |
| 全接続close後 | 20,905,984 / 不在 / 不在 | 15,609,856 / 不在 / 不在 |

上表はapparent size。st_blocksによる割当もreportで別記録した。
WALは再利用可能なその時点の容量であり、累積書込みや実行中最大値ではない。
後のread-only再openやverifierで0 bytesのWAL等が再生成される場合と、close直後の不在を区別する。

process I/Oはアプリ・ローカルHTTP・schema・計測queryを含むプロセス全体。
終端保存・会話採用・snapshot保存と最後のclose/checkpointを含み、readback再open・report出力前にsampleした。
SQLite専用・累積WAL・SSD物理書込み量を分離測定したとは扱わない。
単回のrun時間は9.207→8.921秒、VmHWMは61,524→55,092 KiBだったが、
時間・メモリは補助観測であり、統計的な性能改善や長期runの保証はしない。

### Chatと要約Host

- 実受信Chat一件のusageは161 bytes×二重から161 bytes×一回へ。
  payloadは45,002→44,833 bytes、物理DB 397,312 bytesとprocess write_bytes 1,458,176 bytesは同量だった。
  小さいJSON削減が必ずpage・I/O削減になるわけではない。
- 要約Host2呼出しは同じ結果を既存contentsの1 digestで所有していた。
  完了Resultのunique実bytesは1,590→230、**1,360 bytes削減**。
  2参照分の2,720 bytesをunique削減へ加算しない。
  通常root要求も含むこのscenario全体のDBは368,640→364,544 bytes、
  process write_bytesは1,503,232→1,490,944 bytes。
- before/afterの全検証DBでintegrity ok、FK違反0、close/reopen後Records/Service.Record、
  集計値・Host outputBytes/finishReasonの保持とlive rawを確認した。

測定artifact： `tmp/usage-storage-PylRnNae/measure/comparison.json`
、同directoryのREADME、harness/main.go、
input-evidence.json、before/after各report.json、before-verification.json、after-verification.json。
二重保存解消とattribution除去の削減は重なるため加算しない。
本結果は新しい保存経路だけの比較で、過去DBの縮小・実Session全体の削減率・全providerへの保証ではない。

## 最終review・総合test

S-3の独立reviewはproduction/test/script、CLI/Core/PTY証跡、before source・同一harness/input、
各測定reportと文書claimを照合し、指摘なし。S-1のP3も修正・独立再review済み。
S-2で不要になったpayload-only wrapperは残さず削除し、単体testを本番projectionの直接呼出しへ整理した。
この整理後のfocused 2 testsは通過（0.003s）。`go vet ./internal/app`、gofmt、Python AST、diff checkも通過。

coordinating ownerが計画の総合testを一回実行した：

```sh
go test ./internal/... ./cmd/...
```

**初回の全体testは未通過だった。** internal/appの次の既存2件が失敗し、他packageは通過した。

| 失敗test | 原因として観測した期待 | beforeでの確認 |
| --- | --- | --- |
| TestRoleToollessRunUsesNoImplicitRuntimeTool | tool定義0件は成立しているが、共通tool利用方針内にrun_jsという語があるため「instructionにrun_jsを含まない」期待に失敗 | 同じ失敗を再現 |
| TestWebSearchProviderErrorReachesFollowingModelRequest | 入力指示に旧文言「Omit unused fields」「omit save_to」が同時にある期待に失敗 | 同じ失敗を再現 |

変更前sourceの`before/`でこの2件だけをfocused実行し、同じ内容で失敗した（0.081s）。
今回のusage保存差分によるregressionとは判定せず、この時点では対象外のtest／tool利用方針／helpを変更しなかった。
誤ったtest期待は無断で修正せず、全体未通過を報告してfull gateの再実行を止めた。
証跡は`tmp/usage-storage-PylRnNae/full-test.log`と`baseline-failures.log`。
この補足も独立reviewで現行／beforeのtest sourceと両ログを照合し、報告は整合・追加指摘なしと確認した。

### 利用者承認による既存2件の修正と総合test再確認

その後、利用者が「ついでに直そうか」と既存2件の修正を指示した。
現行のtool利用方針・help契約と照合し、productionを変えずtest期待だけを修正した。

- tool-less：共通方針のrun_jsという単語を暗黙tool追加とみなす期待を撤去。
  req.Toolsと保存Agent.Tools／Toolsが空、text-only role指示と最終回答を確認する。
- web search：詳細guidanceの旧固定文言が常駐instructionにある期待だけを撤去。
  403・reason・tag・requestIdの次モデルへの伝達、compliance非公開、2step完了は維持する。

修正2件のfocused testと近接するrole/help/web searchの既存4件が通過。
gofmt・diff check通過、独立reviewも指摘なし。
前回の失敗原因を利用者承認で修正したため、coordinating ownerが同じ総合testを一回再実行した。
**`go test ./internal/... ./cmd/...`は全体通過**（internal/app 28.439s、他test対象packageも通過）。
証跡は`tmp/usage-storage-PylRnNae/full-test-after-existing-expectations.log`。

## 残る範囲

外部独自hook/clientの利用と未観測provider形式、実providerでの新規動作は未確認。
精密collector・通常モデル応答後JS hookの追加、過去DBの変換・削除・VACUUMは行っていない。
常用環境で有効化するwriterの配置・Core再起動は実装確認とは別に扱う。下記の配置続報を参照する。

## 続報：コミット・配置

利用者の「コミット配置をして」に従い、本件のsource・test・依頼／計画／結果／保存契約をコミットする。
別作業のextensions/hooks.js・extensions_test.go・extensions設計、handoff、一時検証artifactは含めない。
配置binaryはコミット済みsourceを隔離展開してbuildし、未commitの別作業変更を混入させない。
配置先は従来どおり`dist/hjh`と`dist/hjh-tui`、既存binaryはbackupする。

配置確認時の常用CoreはPID `426882`、endpoint `http://127.0.0.1:8765`。
実行中runがあるため、停止・再起動は行わない。既存DB・config・selectionは変更しない。
新usage保存経路が常用Coreへ有効になるのは、run終了後に新binaryで再起動した後。
配置ではversion/hash、DB/config/selection維持、稼働Coreの同一PIDを確認し、実providerは呼ばない。
pushは今回の指示に含まれないため行わない。
