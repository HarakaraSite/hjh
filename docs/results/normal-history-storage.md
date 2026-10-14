# 通常履歴の保存整理：実装結果

2026-10-14。[計画](../plans/normal-history-storage.md)の改訂案に対し、利用者が実装・各スライスのtest/review・
最後の総合testを指示した。常用DB・backup・起動中Coreは変更しない。

| スライス | 状態 | 確認・review |
| --- | --- | --- |
| S-1 最新送信全文と通常request記録 | 完了 | focused test・初回review・P2修正の再review完了 |
| S-2 設定・既存hook観測の定義archive除去 | 完了 | focused test・独立review完了 |
| S-3 tool観測結果の本文参照 | 完了 | focused test・P2修正と再review完了 |
| S-4 継続用tool Messageの本文参照 | 完了 | focused test・P2修正と再review完了 |
| S-5 総合確認・容量/書込み測定 | 完了 | 総合test・測定・独立review完了 |

実装開始時に既存の未commit変更を保った状態を検証用に捕捉した。
比較・testには一時workspace/DBとローカルHTTP modelを使用し、外部有料APIを使わない。

## S-1

各executionの最新準備済みrequestだけが正確なWireBodyを所有する。
予算・物理送信開始はmetadataのみ、context_item・occurrences/splices/revisionと全request manifestは廃止した。
最新locator、本文置換、未参照本文の回収を同じtransactionで確定する。

focused app/history testで実HTTP、複数送信、最後の送信失敗、親子独立、継続とrecall、
他の所有者がある本文の保持と未参照本文の回収を確認した。
独立reviewでRecordsのmetadata取得後の別SQL本文読取りが最新本文GCと競合するP2を採用し、
metadataと本文のLEFT JOIN取得へ修正した。並行read/置換のfocused回帰確認と、変更箇所の再reviewが通過した。
全体gateはまだ行っていない。

## S-2

configuration保存からトップレベルtools定義一覧を外し、agent.toolsの選択名と指示を保持した。
4種類のlifecycle hook観測は保存時だけcontext.toolsをcontext.toolNamesへ置き換える。
runtimeのhook入力とlive観測、beforeTool/afterToolや戻り値は維持する。

`go test ./internal/app -run 'TestLifecycleDefinitionsRemainAvailableButAreNotArchived|TestLifecycleContextParentChildrenAndNextTurn|TestLifecycleCleanupFailurePreservesAnswerAndReplayNotice|TestLifecycleProviderCancellationRunsCleanup|TestLifecycleEmptyHookExportsAreRecorded|TestToolGuidelinesFollowParentAndChildSelectionAndPreserveConfigFile|TestRoleChoiceAdoptionResumeAndCurrentConfig|TestRoleToollessRunUsesNoImplicitRuntimeTool|TestRunPersistsRealToolPathAndResumes' -count=1`
が通過（0.651s）。実JS hook/live/保存の照合と、既存role・親子・継続経路を確認した。
独立reviewで対応が必要なfindingなし。diff checkも通過。

## S-3

tool_resultとeffect_observationのResultを本文FKへ移し、保存payloadはresultRefを持つ。
公開のRecords/Record・AppendOccurrence返却・RecallEvidenceは結果を展開する。
元の結果JSONの内部は変更せず、genericな添付Contentは従来どおり保存する。

独立reviewで、journalの観測JSON化が原Result bytesをHTML escapeしてしまうP2を採用した。
ObservedResultとして原bytesを渡す経路へ修正した。
実run_js/read_file経路で`<p>&`を含む原結果と次のモデル入力のText、本文テーブルのbytes/digestを照合した。
明示要件のないContentとResultの同時指定拒否も撤去し、従来の添付Content fixtureを維持した。
変更箇所の再reviewで指摘解消、追加findingなし。

focused確認は次の通り。全体gateはS-5まで実行しない。

```sh
go test ./internal/history -run 'TestToolObservationResultBodyIsOwnedAndExpandedOnRead|TestAppendPostSettlementObservationPreservesAdoptedExecution|TestRecallEvidenceKeepsPublicMessagesAndSemanticFactsWithoutBlobExpansion|TestDeleteSessionRemovesConversationExecutionTreeAndRelatedReceipts|TestDeleteSessionPreservesRemainingInputBodiesAndLeavesUnrelatedData|TestAppendLatestRequestReclaimsOnlyUnreferencedPreviousBody' -count=1
go test ./internal/app -run 'TestNestedToolResultLiveAndReplayFieldsMatch|TestSteerChildDuringToolAndFinalResponsePersistsOrderedInstructions|TestChildReportsReachParentRequestAndSavedStatusResults|TestRecallKeepsActualStoppedRunJSActivityAsMetadata' -count=1
```

historyは0.032s、appは0.298sで通過。formatとdiff checkも通過した。

## S-4

tool Messageの結果TextはtextRef、表示用semantic mirrorのトップレベルTextはfactTextRefとした。
正常な結果は同executionのS-3所有FKを再利用し、異なるエラーText等はtool_body recordが所有する。
Messages/Conversation・Records/Record・RecallEvidenceで元の公開Message/Textへ戻す。
mirrorへMessage全文を再保存せず、call ID・結果順・成否・failure/terminal・provider stateを保持する。
Messages/Conversationはrowsを閉じてから本文を解決する。

focused確認は次の通り。

```sh
go test ./internal/history -run 'TestToolMessageBodiesShareExecutionOwnerAndExpandAfterReopen|TestHistoryPersistsFullMessagesEvidenceAndCanonicalAdoption|TestDeleteSessionRemovesConversationExecutionTreeAndRelatedReceipts|TestRecallEvidenceKeepsPublicMessagesAndSemanticFactsWithoutBlobExpansion|TestToolObservationResultBodyIsOwnedAndExpandedOnRead' -count=1
go test ./internal/app -run 'TestNestedToolResultLiveAndReplayFieldsMatch|TestToolFailureReturnedToModelBeforeNextStep|TestRunPersistsRealToolPathAndResumes|TestAsyncChildrenThroughRunJSAndSavedHistory|TestChildReportsReachParentRequestAndSavedStatusResults|TestSteerChildDuringToolAndFinalResponsePersistsOrderedInstructions|TestRecallKeepsActualStoppedRunJSActivityAsMetadata' -count=1
```

historyは0.034s、appは0.499sで通過。DBのclose/reopen、正常本文の単一保存と異なるエラー本文、
削除対象の回収・残るSession本文の維持を確認した。
従来fixtureのtool Message比較はobject key順ではなくcanonical JSONの意味で比較し、
必須のtool_body追加で変わる固定ordinalはrecord IDで指定するよう更新した。
独立reviewで、本文の再利用所有者に置換可能なmodel_requestを選んでしまうP2を採用した。
通常appの成功toolではS-3観測が先に保存されるため影響しないが、両保存入口の所有契約では問題になる。
所有者の再利用からmodel_requestを除き、最新wireの置換後もtool_bodyが結果を保持するよう修正した。
両入口で「wire A → Text Aのtool Message → wire B → 公開読取り」の回帰確認が通過（0.016s）。
変更箇所の再reviewでP2解消、追加findingなし。

## S-5：総合確認

一時workspace/DBとローカルHTTP modelで、実Core・接続CLI・PTYのTUIを起動した。
14回の実HTTP送信で以下を完了し、外部有料APIは呼んでいない。

- run_jsから子2件を並列開始し、中間assistant報告をstatusで読む。親から子へ追加指示し、両方の最終報告をcollectする。
- 親子各executionで、保存した最新wire bytesが実HTTP受信と一致し、過去の全文・context_itemが残らない。
- CoreのinspectとSession view、TUIの短縮ID復元と/contextで親の回答・入力準備metadataを確認する。
- Coreを停止し別プロセスで同じ一時DBを開き直し、CLIで同Sessionを続ける。次の送信に保存tool結果・子の回収結果が入る。
- read_file後のモデルHTTP失敗をrecallし、次の送信で実際の読取り内容を参考にする。
- 実行中のHTTPを取消して終了し、その準備済み最新送信を取得する。
- APIで全Sessionを削除し、executions・contentsが0件、SQLite integrityがok、FK違反が0件になる。

`go test ./internal/... ./cmd/...`が通過した。
初回は旧保存形式を期待するapp testが3件失敗し、設定の定義一覧からagent.tools名の確認、
request内予算から独立context_budget factの確認へ期待を更新した。
実requestのツール構成・tool実行結果・指示・workspace継続の確認は維持した。
該当3件のfocused testが通過（0.246s）したため、一括testを再実行して全体の成功を確認した。

Go 1.27対応のstaticcheckを使い、次の全体checkが通過した。
その後のtest期待更新に対してはinternal/appだけを再checkし、通過した。

```sh
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45 ./cmd/... ./internal/...
```

総合確認・測定のcandidateは一時binaryへbuildした。dist、常用Core、常用DB、backupを変更していない。
最終reviewは実操作script・結果artifact・測定JSON・文書と、更新したtest期待を照合し、
対応が必要なfindingなし。各スライスのreview指摘も修正・再確認済みで、全スライスを完了とした。

## 容量と実行時間の比較

変更前は、実装開始時の未commit変更も含めて捕捉したsourceからbuildしたbinaryを使った。
比較は別々に作った使い捨てDBであり、44Sessionの旧常用DBを変換した比較ではない。
同じ約4 KiBのファイルをrun_js → read_fileで繰返し読み、ローカルmodelは最後に回答する。
input予算10,000 tokens、無制限steps、モデル呼出し30/300/600回、tool呼出し29/299/599回。
親子・異なるエラー等の機能確認は上記総合経路とfocused testで別に行った。

最初はtmpfs上の一時DBで測定した。DBはVACUUMせず、そのままのファイルサイズで比較する。

| モデル呼出し | 変更前DB | 変更後DB | 削減 | 変更前→後の実行時間 | 予算JSON（両版同量） |
| --- | ---: | ---: | ---: | ---: | ---: |
| 30 | 3.14 MiB | 0.99 MiB | 68.4% | 0.46 → 0.31 s | 0.11 MiB |
| 300 | 47.01 MiB | 15.46 MiB | 67.1% | 24.04 → 11.68 s | 9.21 MiB |
| 600 | 130.57 MiB | 48.90 MiB | 62.5% | 128.33 → 62.17 s | 36.62 MiB |

600回時のtable・index割当量は次の通り。実本文量は割当量と区別する。

| 保存先 | 変更前 | 変更後 |
| --- | ---: | ---: |
| semantic_recordsの割当 | 106.37 MiB | 44.37 MiB |
| contentsの割当 | 18.70 MiB | 0.62 MiB |
| 全indexの割当（PK/UNIQUEを含む） | 4.38 MiB | 2.86 MiB |
| contentsの実bytes合計 | 18.37 MiB | 0.38 MiB |

同じ結果を読む条件なので、結果本文の再利用が効きやすい測定である。
結果が毎回変わる場合はその原結果bytesを保存する必要があり、この削減率をそのまま適用できない。
SQLite pageは4 KiB。変更後30回の空きpageは3（12 KiB）、300/600回は0。
600回の残り容量は未参照本文の空き領域が支配している状態ではない。
全測定DBでintegrityはok、FK違反は0件だった。

プロセスのVmHWM観測は、300回で49.00 → 49.12 MiB、600回で54.58 → 53.86 MiB。
この条件でメモリの大きな増減は見られない。最終返信を返す直前までの観測であり、
終了処理や履歴全出力中のpeakを測ったものではない。

## 累積書込みと実ディスクの確認

常用repositoryと同じfilesystem上の、別の検証directoryでも30/300回を比較した。
DBサイズと予算JSON量はtmpfs比較と同じ水準だった。

| モデル呼出し | 実行時間（前→後） | process write_bytes（前→後） | process wchar（前→後） |
| --- | ---: | ---: | ---: |
| 30 | 4.30 → 2.81 s | 65.43 → 40.29 MiB | 59.64 → 37.77 MiB |
| 300 | 81.39 → 37.58 s | 741.47 → 442.77 MiB | 682.16 → 418.11 MiB |

300回のprocess write_bytesは40.3%減少し、容量の67.1%削減とは異なる。
最新wireの保存・旧本文回収にも毎回書込みが発生し、終了DBサイズだけからその量を推定しない。
`/proc/PID/io`で最終HTTP返信直前に読み取り、終了・会話採用の書込みは含まない。
write_bytesはprocessへのOS書込み課金量、wcharは書込みsyscallへ渡した文字数であり、
どちらもSQLite専用の値ではない。標準出力・進捗なども含む。
tmpfsではwrite_bytesが0となるため、実ディスクの比較を別に行った。

journal modeは両版ともdeleteで、WALを使わない。
SQLiteだけの書込みbytes、journalの累積書込みbytes、transaction総数は分離計測していない。
このため、今回主張できるのは保存容量と観測したprocess I/Oの削減であり、
SQLite/journalの内訳ごとの削減量ではない。速度はこの一時環境での実測であり一般的な性能保証ではない。

## 保存履歴の復元・出力

300回DBに対し、別CLIプロセスで`history --latest`を3回ずつ実行した中央値。
DBのopenから各viewの全文出力までを含む。OS cacheは維持し、cold-cacheやTUI単独のbenchmarkではない。

| view | 変更前 | 変更後 |
| --- | ---: | ---: |
| session（通常表示） | 0.186 s | 0.130 s |
| canonical（継続用会話） | 0.025 s | 0.058 s |
| detail（全文記録） | 0.870 s | 0.530 s |

session/canonicalの出力bytes数は前後で一致した。
canonicalは結果の本文参照を解決する読取りが増え、約33 ms長くなった。
detailは過去送信全文を廃止したため、出力量自体が92,314,423 → 21,497,012 bytesへ減った。
利用動作を保った結果本文と、意図的に廃止した過去送信全文を区別する。

## 残る増加要因と次の設計

予算記録は今回の採用方針どおり、各呼出しの計算・選択/除外結果を保持した。
300回で9.21 MiB、600回で36.62 MiBとなり、呼出し倍増に対して約4倍に増えた。
除外済みmessage番号と、そのexecution/turn/source対応の一覧を毎回含むためである。
最新factの除外件数は588 → 1,188、payloadは63,859 → 127,847 bytesとなった。
600回では予算payloadだけでDBファイルの約75%を占め、今回の主要な残る増加要因と確認した。

追加設計では、まず除外sourceをexecution/turnとmessage ordinalの範囲へ表せるか調べ、
同じ一覧を各回で展開保存しない方式を検討する。公開の予算・選択/除外結果を復元できる形式と、
通常用として残す保存粒度を定義してから変更する。最新だけにする案は過去の選択記録を失うため、
本実装で自動的に採用しない。計測hookの改善は別テーマとして維持する。

今回の実装は会話・結果の要約や切捨てを行わず、本文・定義・archiveの重複を減らした。
数時間〜数日の耐久性を、この600回の短時間ローカル試験から保証しない。
残る予算一覧の増加と長期実行のCPU/I/Oを、次の保存粒度設計で扱う必要がある。
