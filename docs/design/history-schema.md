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

## 通常履歴の保存整理（2026-10-14）

利用者は通常利用の履歴と開発・調査用の精密な記録を分ける方針を採用した。
[改訂計画](../plans/normal-history-storage.md)に従い、各スライスの実装・test・reviewと最後の総合testを進める。
保存JSON・本文所有・取得の新しい契約は[通常履歴の保存契約](normal-history-storage.md)、
実装と容量測定の到達点は[結果](../results/normal-history-storage.md)を参照する。
以下の議論・最初の二重保存削減の記録は、それぞれ当時の状況として保持する。

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

## DB構造の見直し：議論メモ

2026-10-13、利用者との議論を記録する。現在の保存構造の説明と、変更要望・未決定事項を
区別する。この節を追加した時点では、保存形式・実DBは変更していない。

### 利用者の変更要望と現在地

- 利用者は「実装ソースの常時保存は外す」「共有本文という概念が不要」と明示した。
  ツール呼び出し・引数・結果の保存は有用だが、毎回のツール実装・依存ファイルの全文保存や、
  内容のハッシュで同一データを共有する仕組みの必要性を疑問視している。
- その後「ちょっと会話しよう」との指示を受け、実装着手を止めて構造を説明した。
  変更要望は記録するが、具体的な新schema、保存対象、既存履歴の扱いは未決定。
- 保存される内容と保存方法を別に検討する。ツール実装ソースの自動archiveと、モデルが作った
  `run_js`スクリプト・ツール引数・実行結果は区別する。後者まで廃止する合意はない。

### 現行構造を具体的に説明した内容

セッションはDBファイルそのものではない。1つのSQLite DBに複数のセッションがあり、
1つのセッションには依頼ごとの実行が複数ある。子agentも独立した実行として保存する。

例として、実行Aが「READMEを要約して」という依頼、`read_file`の呼び出し、READMEの取得結果、
最終回答を0〜3番のメッセージとして保存した場合、現行の読取りは次の経路になる。

1. `conversation_messages`は、セッション内の順序と「実行Aの0〜3番を使う」という対応を持つ。
2. `messages`は実行ID・実行内の番号と、各メッセージの内容を示すdigestを持つ。
3. `contents`は、依頼文・引数・READMEの取得結果・回答などの実際のbytesを持つ。

`contents`への保存時は正確なbytesのSHA-256を求め、同じdigestなら既存の行を使う。
DB内の全文を順に比較する方式ではない。変更のないAGENTS.mdやツールソース、全く同じ依頼文などを
共有できるが、そのために内容参照と削除後の未参照データ管理が必要になる。
現在のCLIセッション削除は`contents`を回収しないため、削除後に誰も参照しないデータも残る。

### `messages`と`conversation_messages`を分ける理由

既存契約に明記された目的は、すべての実行記録を保持しつつ、親の次のターンに引き継ぐ会話を
別に確定すること。正常終了した親実行のメッセージを会話へ採用し、失敗・取消や子実行の記録は
保持しても、そのまま親の会話には採用しない。人間向け履歴表示とモデルへ渡す会話も同一ではない。

実装から読み取れる利点は、実行中は実行内の番号で順に保存でき、正常終了時には本文のコピーや
元メッセージの移動をせず、会話への対応とセッションrevision等を同じtransactionで確定できること。
「実行記録を先に保存し、親の会話への追加は後で決めたい」という意図は、この構造からの推測である。

ただし別テーブルは必須ではない。1つのメッセージテーブルに所属実行、実行内の番号、
セッション内の番号や採用状態を持たせて選択する代案もある。現行方式は責務を分けやすい一方、
読み取りにテーブルの結合が必要になる。hjhでは旧Henjiのschemaを採用しており、統合案と比較して
この分割を選び直したわけではない。

この分割は`contents`による共有と独立している。メッセージの内容を`messages`に直接保存しても、
セッションから実行メッセージへの対応は成立する。説明時には「この分離には実用的な理由があるので
残してよい」という見解を示したが、利用者による構造維持・統合の決定はまだない。

### 共有本文以外に重複を避けている箇所

- 親の会話への採用では、実行Aのメッセージをコピーせず、実行IDと番号の対応だけを保存する。
- 追加指示の表示用記録は受付IDやメッセージ位置を持ち、指示文の全文をそこへ重ねて保存しない。
- ストリーミング中の「こ」→「この」→「このプロジェクトは」等は、最新状態の行を更新する。
  累積文字列を毎回すべて履歴へ追記する方式ではない。
- 受付済み追加指示を適用時に同じdigestで参照する仕組みもあるが、これは共有本文の利用であり、
  共有本文以外の独立した容量削減策には数えない。

一方、通常のメッセージ、表示・調査用の意味記録、モデルへの送信内容には同じ文章やツール結果が
重なる箇所がある。gzip／zstd等の保存圧縮は行っていない。contextの差分形式も、現在は全項目を
置き換える記録であり、実際の差分だけを保存する容量削減にはなっていない。

### 再開時に決めること

まず、日常の再開・履歴確認に必要な保存対象を具体例で整理し、共有本文廃止後の所有先と
削除範囲を決める。実行記録と親の会話の対応を分けるかどうかは、共有本文廃止と別に判断する。
既存履歴の変換・別保管・新DBへの切替の選択は未回答であり、実データの変換・削除は行っていない。

### 続く決定：実行中の追加指示だけ保存データの共有を廃止

同日の議論後、利用者は「追加指示の保存データの共有をやめたい」と選択し、受付メッセージと
実行履歴に入るメッセージを別に持つ意味を確認して、この範囲の実装を指示した。
`A → B → 追加指示`と`A → B → 追加指示 → C`の会話全体を別々に保存する意味ではない。

steering受付Messageを`command_receipts.result_json.acceptedMessage`へ直接保存し、適用時には
通常の実行messageとして独立に保存する。contentDigestは受付Messageのchecksumとして残るが、
受付Messageの読取りはdigestではなく受付IDを使う。適用messageの保存と受付状態の更新は
同じtransactionで確定する。DDLは変更しない。

follow-up予約、子agentへの追加指示、表示用semantic mirror、ストリーミングの最新状態保存は
今回の対象外。共有本文全体の廃止とツール実装ソースarchiveの廃止も未実装のまま残す。
旧steering受付JSONの変換・互換読取りは追加しないため、旧受付記録の本文detail読取りは新形式では
利用できない。既存の適用済み実行メッセージ・会話履歴はそのまま読め、実DBの書換え・削除は行わない。

利用者はこの制約について「はい過去のセッションはいいです」と回答した。旧追加指示受付記録への
互換対応は追加しない。この回答は、過去の実データを削除する指示としては扱っていない。

### DBの役割整理：説明文と表の保存

利用者が保存を指定した説明を以下に残す。これは追加指示の保存変更より前の説明である。
末尾の「保存方式の変更はまだ行っていません」は当時の状態を示し、現在は上記のsteering受付だけ
直接保存へ変更済み。ツールソースarchiveと共有本文全体は、この説明時と同じ構造で残っている。

大きくは、次の役割に分かれています。

| 役割 | 保存するもの |
| --- | --- |
| セッション | 会話のまとまり、workspace、モデル選択、タイトル、ターン数 |
| 実行 | 一回の依頼、開始・終了・成否、モデル、親子agentの関係 |
| メッセージ | ユーザー入力、assistantの発言、tool呼出しと結果、継続に必要なprovider情報 |
| 詳細記録 | toolの開始・終了、内側の操作、thinking、失敗、追加指示など |
| 調査用の記録 | モデルへ送った入力、使用した構成・指示、現在はツールソース一式も含む |
| 本文の置き場 | 上記の本文をハッシュでまとめて保持する contents |

一回の依頼では、まず「実行」を作り、進行中のメッセージと詳細を保存します。正常に完了すると、
その実行のメッセージをセッションの継続用会話へ採用します。失敗や中断の記録も残りますが、
そのまま継続用会話へ採用するわけではありません。

子agentは親とは別の「実行」として保存し、親子関係で辿れます。DBに保存した内容、
人間の画面に表示する内容、親モデルへ渡す内容はそれぞれ別です。

ここで分けて考えたいのは、次の二点です。

- 何を保存するか：会話・呼出し・結果に加え、どこまで調査情報を残すか。
- どう保存するか：各記録が直接本文を持つか、共有本文を参照するか。

ツールソースの常時保存を外すのは前者、共有本文を廃止するのは後者です。会話の再開や
親子agentの履歴を残しながら、両方を整理できます。今は説明と調査だけで、保存方式の変更は
まだ行っていません。

### ツールソースarchiveの廃止による影響と、残る共有本文

DBへのソースarchiveは`app/execution.go`の`resource_revision`保存で行う。
JSの実行には`extensions.Capture`が読み込んだメモリ上の`Snapshot.FrozenResources`を使い、
セッション再開でも現行ファイルを読み直す。DB内の過去ソースを通常実行の復元に使う経路ではない。
従ってDBへのコピーだけを止める変更は局所的にできる。実行時の読み込み・固定は残す必要がある。

影響は、過去実行に使ったツール・hookの実装ファイルや依存ファイルの全文を、後からDBだけで
確認できなくなること。保存契約と、ソースarchiveの存在を前提にしたtestsは更新が必要になる。
同じ保存loopにAGENTS.mdの記録もあるため、実装ソース廃止と指示文保存の廃止を混同しない。
モデルに渡した指示、ツール定義、利用指示、呼び出し引数・結果、生成`run_js`コードとは別に扱う。
この影響確認時点ではソースarchiveの廃止はまだ実装していない。

共有本文はsteering受付以外にも残っている。単に同じ文章を複数回使う場合だけでなく、
一度しか使われないメッセージも通常は`contents`に保存する。

| 残る保存対象 | 具体例 |
| --- | --- |
| 実行の依頼文 | 親への「READMEを要約して」、子への「認証処理を調べて」 |
| 実行メッセージ | ユーザー入力、assistant回答、ツール引数・結果、適用済みの追加指示、provider継続情報 |
| 使用構成snapshot | roleの指示、組立済みsystem instruction、選択されたツール定義・利用指示 |
| モデル入力の調査記録 | system instruction、各メッセージ、ツール定義、APIへ送ったJSON |
| 読み込んだファイルのarchive | ツール・hook・依存ファイル、AGENTS.md |
| follow-upの受付と次の実行 | 予約した次の依頼文と、開始されたexecutionのtaskが同じ保存データを参照 |
| 子agentへの追加指示 | 親が子へ送った指示の受付記録と、子に適用したメッセージ |

診断データの保存tableも`contents`参照を持つ。`semantic_records`のpayload JSONは直接保存するが、
添付データは`contents`を参照する。共有本文全体の廃止では、これらの保存・読取り・削除経路も
整理する必要があり、steering受付だけの変更より影響範囲が広い。

### 過去会話の読取り経路と、キーを辿る本文削除

利用者の指定により、過去会話を読む際の対応表を保存する。ここでいう各保存先は、別々のDBでは
なく、hjhが開いている同じSQLite DBファイル内のテーブルである。

モデルの継続に使う過去会話は、新規セッションを続ける場合も、過去セッションを再開する場合も
同じ経路で読む。新規セッションの最初の依頼には過去会話がなく、読取り対象がない。
TUIの詳細な履歴復元は、失敗した実行やthinking等の別記録も読むため、この経路だけではない。

| 情報 | 保存されるテーブル・列 | 役割 |
| --- | --- | --- |
| セッションID | `sessions.session_id` | セッションを識別する |
| セッション内の会話の順序 | `conversation_messages.message_ordinal` | メッセージを並べる順番 |
| 実行ID＋実行内のメッセージ番号 | `conversation_messages.execution_id`、`execution_message_ordinal` | その位置に使うメッセージを指定する |
| メッセージから本文への対応 | `messages.content_digest` | 実行ID＋メッセージ番号で見つけた行が、本文のハッシュを持つ |
| 実際の本文 | `contents.content_bytes` | `contents.content_digest`をキーに、メッセージのJSONを取り出す |

経路は「セッションID → conversation_messagesの順序 → 実行ID＋メッセージ番号 →
messagesの内容ハッシュ → contentsの実データ」。実装は`history/read.go`の`Conversation`で
SQLのJOINにより取得する。

現在の実行で生まれたツール結果は、メモリ内のメッセージ列にも保持する。その実行中の次のモデル
呼び出しへ渡すたびに、同じ結果をcontentsから取り直しているわけではない。

利用者は「セッション削除の時、このキーを辿れば削除できる」と指摘した。その理解は正しく、
現在のschemaのままでも、削除対象が参照する本文のdigestを集めて、不要になる本文を削除できる。
本文削除の実現に共有本文全体の廃止が必須なわけではない。

ただし、削除対象は採用会話だけに限定しない。失敗・取消の親実行、子実行、使用構成、
モデル入力の記録、追加指示・予約等に属する本文も辿る。削除後に別の記録から参照される本文は保持し、
参照がなくなる本文を削除する。`messages`だけでなく、依頼文・構成snapshot・詳細記録の添付・
診断・follow-up等からの参照も対象にする。使われなくなる構成snapshotの記録も整理する必要がある。

現行`DeleteSession`はセッションと実行群・関連記録を削除するが、本文と構成の回収はまだ行わない。
これは未実装の処理であり、使い手がなくなった本文を残すこと自体に製品上の理由はない。
この追記では削除処理や実データを変更していない。

### セッション削除の本文・構成回収を実装

上記の議論後、利用者の「まずそれをやろう」により、2026-10-13に現行schemaのまま
セッション削除を修正した。削除前に対象Sessionの親子実行・失敗実行・メッセージ・詳細記録・
診断・入力receipt・構成から候補を集め、削除後も使われるものを除いて、本文・構成を同じ
transactionで削除する。対象に無関係な未参照データは回収せず、VACUUMも実行しない。
全セッション共通のcontents構造やソースarchiveは変更しない。
[実装と確認結果](../results/cli-session-delete.md)を参照する。

### ツール実装ソースの自動保存を停止

2026-10-13、利用者の「ではそれもやろう」により、各実行で`Snapshot.Resources`全体を
resource_revision/contentsへコピーする処理を外した。ツール・hook・依存ファイル・元configの
全文をDBへ自動保存しない。Workerの実行時captureと固定したESMの評価は維持する。
AGENTS.mdの原文・出典、使用構成snapshot、モデルへ渡した指示・ツール定義・入力、
呼び出し引数・結果、生成`run_js`コードは引き続き保存する。
通常のread_fileで実装ファイルを明示的に読んだ場合は、その結果も通常のツール結果として保存する。

schemaは変更せず、過去に保存済みのソースは一括削除しない。過去実行に使った実装ファイルの全文を
新しい実行記録からDBだけで調べることはできなくなる。
以前の「source bytesをarchiveする」という初期設計は、この決定により上記範囲で置き換える。
[確認結果](../results/initial-run.md#2026-10-13追補実装ソースの自動保存を停止)を参照する。

### ユーザー共通の基本指示

2026-10-14、[基本指示の差替え](base-instructions.md)により選択したbaseのpath/digestまたは
built-in versionを使用構成snapshot・実行orientationへ保存する。
基本指示本文はinstructionComponentsと組立済み指示に残し、外部base.mdの原文と出典は
AGENTS.md同様、既存のresource_revision/contentsへ保存する。
これは利用者が書く指示の記録であり、ツール実装ソース一式の自動archiveは行わない。
schema変更と既存履歴の変換は追加していない。
