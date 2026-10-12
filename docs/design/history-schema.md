# 履歴SQLite schema：旧henji-harnessと同一

2026-10-10の利用者の明示指定により、初期製品の履歴SQLite schemaは
`/home/agent/projects/henji-harness` の現行schema v3と同じものを採用する。
一般の旧Henji参考資料から、指定されたschemaを明示的に採用したものである。
採用範囲は履歴保存schemaであり、旧機能の全採用・旧実装の配置・旧入力制限を意味しない。

## 固定した取得元とDDL

- 取得元commit：`2eb586316f80ec8297873a96122568d0088971eb`。
- 定義：`v0/agent/history/history_schema.ts` の `HISTORY_SCHEMA_SQL`。
- `HISTORY_SCHEMA_VERSION = 3`。`PRAGMA user_version` と `store_metadata.schema_version` は3。
- hjhで採用したDDL：[henji-history-schema.sql](henji-history-schema.sql)。
- 取得元と関連保存contractのhash：[schema-source.json](schema-source.json)。
- DDL照合と空DBの確認結果：[schema-verification.json](schema-verification.json)。

取得元はHEADと上記commitが一致し、対象sourceに未コミット変更がないことを確認した。
SQLは元のtemplate bodyからversion変数を3へ置換しただけで、他は変更していない。
21 tables、11個の明示indexに加え、元のPRIMARY KEY／UNIQUE由来のindexを保持する。
列名・型・NULL条件・既定値・主キー・UNIQUE・CHECK・外部キー・indexを一致させる。
スパイクのruns／events／turns／adoptions／config_assets等の簡略表を製品schemaへ持ち込まない。

## 保存の意味

| 表 | 対象 |
| --- | --- |
| store_metadata | schemaの識別と作成日時 |
| contents | digestで参照する実bytesとbyte length |
| sessions | workspace、agent・model選択、revision、会話件数、turn、checkpoint |
| configurations | snapshotの内容digestと構成ID |
| executions | task、Session／親実行、使用構成、実行状態・結果・採用状態 |
| execution_display_positions | Session相関内の実行表示順 |
| messages | execution内のmessageの内容参照 |
| session_turns、conversation_messages | 採用したexecution／messageから構成するSession会話 |
| session_model_changes | Sessionのmodel選択変更 |
| semantic_records、semantic_relations | 保存する意味記録、その内容と他記録との関係 |
| assistant_text_states | 完了前requestのassistant textの最新状態 |
| execution_contexts | モデル入力・使用contextのmanifest |
| diagnostics | executionに属する診断内容参照 |
| recall_relations | 実行間のrecall関係 |
| command_receipts、execution_control_facts、follow_up_receipts | Coreの操作結果、実行制御、追加入力の記録 |
| core_session_cursors、data_session_read_cursors | Core／DataによるSession読込み位置 |

execution内のmessageを先に保存し、Sessionへの採用はsession_turns／conversation_messagesの参照と
Session revision・件数、executionの採用状態をtransactionで更新する。
会話はuser／assistantの最終textだけへ縮めず、採用したexecutionのmessageを基礎とする。
途中のmodel／tool処理や失敗・中断の証拠は会話採用と別に残す。
上記は旧保存sourceで確認した意味であり、S-04のrequest／response pairとmarkerだけの構造を移植しない。

## Go実装へ対応付ける契約

DDLの一致だけで、保存JSONやblobの読戻し互換を実証したとは扱わない。
Go側の対応は[保存payload対応表](history-payloads.md)に記録する。
利用者はDeno専用build fieldの `denoVersion` を `goVersion` へ変更する差分を明示承認した。
この差分はJSON内のbuild情報だけであり、採用DDLやschema versionは変更しない。
実装前に以下の現行sourceを確認し、保存fieldの意味とencodingをGoへ対応付ける。
取得commitとhashはsource manifestに固定している。

- `history/sqlite_history_core.ts`：content保存、意味記録、executionとcanonical採用transaction。
- `history/sqlite_history_store.ts`：Sessionの保存・閲覧、構成snapshot、実行の記録と読戻し。
- `history/history_semantic_model.ts`、`history/history_store_contract.ts`：kind、payload、record関係。
- `history/context_attribution.ts`、`history/exact_byte_plan.ts`：context encoding、digest。
- `core/contracts.ts`：保存messageとprovider state。
- `worker/worker_configuration.ts`：使用構成snapshot。
- `session/session_store_contract.ts`、`session/session_record_codec.ts`：Sessionの保存契約。

これらのpathは取得元の `v0/agent/` を基準にする。
現行content digestは `sha256:` prefix付きの実bytesのSHA-256。
スパイクの構成ID／hash表現や独自JSONを、そのまま同じ意味の保存fieldへ流し込まない。
hjhで必要な生成JS・内側tool／hookの証拠、外部source bytesのarchiveを
既存のrecord・relation・contentsへどう対応付けるかは、Goのhistory実装計画で具体化する。

旧Denoのlock・Worker通信・permission check・上限等は、schema採用だけで引き継がない。
schemaに含まれる後続機能の表は保持するが、使われない表へ仮のrecordを作る必要はない。
旧DB移行・converter・dual-read/writeは今回の採用範囲に追加しない。既存実DBは操作していない。

## 今回の確認と残る確認

元DDLと保管DDLをそれぞれ独立したin-memory SQLiteへ適用し、SQL catalogの一致、
21 tables、11 explicit indexes、user_versionとmetadataの3、foreign_key_checkとintegrity_checkを確認した。
検証engineはPythonのSQLiteで、versionは確認JSONに記録した。
これはschema採用資料の確認であり、Go製品storeの実装・保存JSONの互換検証はこれから行う。
既存Henjiの実DB、スパイクのDB、原文の引継ぎ資料は変更していない。

## 将来の改善候補：DB容量の最適化

2026-10-12、利用者要望。現時点では実装を進めず、今後の検討事項として記録する。

Session `72dc9c7b`（subagentテスト）の容量を、子agentの実行履歴も含めて読取り専用で確認した。
当日の保存本文・JSON等は合計20,166,100 bytes（約19.2 MiB）で、主な内訳は
`semantic_records`の文字列データ13,336,562 bytes（約12.7 MiB）、参照先の重複を除いた
`contents`の本文6,592,172 bytes（約6.3 MiB）だった。これは共有データを含み、
索引・SQLiteの管理領域を除く概算であり、Session専用の物理占有量ではない。
全Sessionを含むDBファイルは75,370,496 bytes（約71.9 MiB）だった。

対応時は、まず容量の大きい実行履歴JSONの生成・保存・参照経路を調べ、重複保存の削減や
圧縮を検討する。完全な履歴を保持し、会話・tool結果・子agentの記録を引き続き参照できることを
前提とする。重複が削減可能か、どの圧縮方式を使うかは未確認・未決定である。
保存容量に加え、保存・復元速度、メモリ使用量、WAL・総書込み量も併せて評価する。
自動削除・履歴の切捨てやschema変更を、このメモによって採用・実装認可したとは扱わない。
