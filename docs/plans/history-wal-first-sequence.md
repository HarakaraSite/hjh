# 履歴DBのWAL化と初出位置の再検索削減

2026-10-14。利用者の「WAL化と初出検索の改善をやろう」に基づく計画。
起点：`75fa1cd`。実装前に現行操作・保存経路を確認した。

## 必要な動作と根拠

履歴・会話再開・thinkingの表示順を維持しつつ、履歴保存の書込み量と長い実行の保存待ちを減らす。
600回のlocal HTTP呼出しでは約9,000 transaction、終了までprocess書込み750 MiB。
WALだけの一時比較は391 MiB。同期は双方FULL。
確定メッセージの初出位置検索1,198回が約11.92秒で、全体19.14秒の約62%を占めた。
専用索引の試験では検索時間を短縮できたが、書込みとDB容量が増えるため、既存journal情報を先に使う。
数値は同一tool結果を反復する単発試験。実device IO・実provider・異なる結果の長期利用は未確認。

## 現行経路と責務

- CLI run / Core起動 / Core停止後のsession delete → history.Open → writable SQLite。
- CLI history / session list → OpenReadOnly。同じDBをCoreと別processで読む。
- TUI → Core → Service / execution journal → history Store → SQLite。
- journal.observeはprogress/resultのtransaction成功後にfirstSequencesへ初出位置を記憶する。
  親子ごとにjournalが分かれ、mutexでobserveとmessage保存を直列化する。
- SaveAssistantResultは進行状態から初出位置を引き継ぎ、状態を消す。
  AppendConversationMessageでthinking Factを展開した際、この既知情報を渡していないため再検索している。
- SQLite close / 自動checkpointがWALを処理する。製品にDB単体コピーのbackup機能はない。

## Slice

### S1：WAL

file-backed writable接続のDSNへsynchronous(FULL)を設定する。
schema作成/検証に成功してからjournal_mode=WALへ切り替える。
既存の不正/空DBをschema検証前に変更せず、追加接続でも永続したWAL modeを使う。
read-only接続はjournal modeを変更しない。memory DBは従来のmemory journal。
schema v4、table/index、保存transaction・busy timeout・接続数を維持する。
独自checkpoint timer、NORMAL同期への変更は加えない。
新規DB、同schemaの既存DELETE DBの再open、書込み中read-only読取り、close後再openと削除を確認する。
常用DB/Core/distとbackupを操作しない。

### S2：初出位置

AppendConversationMessageに、journalが持つ確定済み初出位置のmapを渡す。
展開されたthinkingを含めてEntityKind/LocalKeyで参照し、mapにない新しいentityは今回のsequenceを使う。
mapはexecution-localな全既知entityの集合で、成功した保存結果だけから更新される。
Storeをjournalなしで直接利用する経路は、従来のDB検索による初出位置解決を維持する。
新しいcache・永続table・索引を増やさず、mapを保存処理内で変更しない。
stream途中→model result→message確定の同じ位置、複数thinking key、最終結果だけの新entity、
liveとDB再読込みの一致を確認する。

### S3：総合確認と測定

各sliceでfocused testとdiff check、変更経路の自己reviewを行う。
安定候補でhistory/app/CLI/Coreを含む全体Go testとstaticcheckを一回実行する。
別processのCLI history、Core経由のrun/再接続・session削除を使い捨てDBで確認する。
local HTTP 300/600回を同条件で測定し、終了までのprocess書込み、初出SQL回数、時間、
閉じた後のDB容量、保持記録/メッセージ件数を比較する。診断instrumentationは一時コピーだけへ置く。
初出位置の表示・WAL読取り/終了に関する独立reviewを行い、具体的regressionがあれば修正する。
実provider料金を使う試験、常用切替、commit/pushはこの作業に含めない。

## WALの運用上の扱い

通常利用の対象は同VMのローカルDB。書込み中は-wal/-shmが存在し、DB本体だけのコピーは最新記録を含まない。
手作業backupはCore/run/history等の全接続を閉じてから、DB本体と残存する-walを
同じbasenameの対応で一緒に保存する。readerが最後に閉じた場合など、正常close後も
未checkpointの-walが残るので、DB本体だけをコピーしてよいとは扱わない。
SQLiteの自動checkpointと最後の接続closeを利用する。
参考：[SQLite WAL](https://www.sqlite.org/wal.html)、[synchronous](https://www.sqlite.org/pragma.html#pragma_synchronous)。

## 現在地

S1/S2実装済み。focused history/app test、対象race test、自己review成功。
S3完了。全体Go testとstaticcheck成功。実Core/CLI/PTY TUIのrun・親子status/steer/collect・
再起動続行・取消・失敗recall・budget・削除確認成功。Core稼働中の別CLI history/listと
停止後CLI短縮ID削除も成功。300/600回の比較を実施し、初出再検索を除去した。
独立reviewでコードへの必須findingなし。readerが最後に閉じる順序の残存WALを再現し、
backup説明のP2を採用して本体と残存-walの同時保管へ修正した。
詳しくは[結果](../results/history-wal-first-sequence.md)を参照する。
