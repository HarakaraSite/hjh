# 通常履歴の保存整理計画

2026-10-14。状態：S-1〜S-5の実装・test・review・総合確認・容量測定完了。
残る予算metadataの増加と計測限界は[実装結果](../results/normal-history-storage.md)へ記録した。
計画前に着手した変更は利用者指示でロールバック済み。実装はこの改訂計画から開始する。
通常・批判的レビュー後の改訂案。既存hook観測の保存範囲、本文所有と取得位置、
長期実行時の予算記録・累積書込みの評価を具体化した。
スライスごとの到達点と総合確認は[実装結果](../results/normal-history-storage.md)へ記録する。

## 目的と根拠

数時間〜数日の実行で、通常利用に必要な履歴を保持しながら保存量を減らし、不要な書込みを削減する。
保存容量と累積書込み量は別に評価し、容量が減ったことだけで書込みも減ったとは扱わない。
利用者は、通常利用の記録と開発・調査用の精密な記録を分ける方針を採用した。
不具合時は各実行の最新送信全文から調べ、過去の送信に起因する問題は別途調査する。
計測hookの改善は別テーマとする。

今回合意した変更は、旧Henjiから採用した履歴の精密なcontext archive契約に優先する。
既存の会話・tool・provider契約のうち、今回変更する保存部分と維持する操作を明記してから実装する。

旧DBの読取り専用調査では、44Session・96execution・563回のモデル送信があった。
182.82 MiBから134.54 MiBへの比較は、物理送信記録の二重保存を除いた旧DBのコピーによるもの。
新DBの長期実測ではない。現在のコードでも、呼出しごとの全文と詳細構成記録の蓄積は残っている。

## 完成する利用動作

- CLI/TUIから依頼し、model/toolの繰返しや親子agentを使って回答を得られる。
- 過去Sessionを継続するとき、保存済み会話とprovider固有の継続情報を使える。
- ツール引数・結果、内側の操作、thinking、子の報告、成否・失敗を履歴から確認できる。
- 失敗・取消した実行の情報をrecallで参考にできる。
- 各executionの最新のAPI送信全文を確認できる。親・子は独立して保持する。
- Session削除時に、親子の記録と誰も参照しなくなった本文を回収できる。

毎回の送信全文・ツール定義・構成要素の精密なarchiveは、通常履歴の必須情報から外す。
会話やtool結果を要約・切捨てして容量を減らす変更ではない。

## 現行経路と影響範囲

| 操作・状態 | 生成・保存・参照の現行経路 | 変更で守ること |
| --- | --- | --- |
| モデル呼出し | `agent.Loop` → 予算projection → `provider.Prepare` → `contextAttribution.request` → HTTP/SSE | 実際の送信bytesを、API呼出し前に最新本文として保存する |
| 送信全文 | `context_item`・論理`model_request`・`context_budget`・物理送信記録が同じ`contents`本文を参照 | 所有先を一つにし、古い本文への参照と本文を回収する |
| 継続用会話 | `conversation_messages` → `messages` → `contents` → 新しい入力projection | 会話内容と順序、provider stateを保持する |
| ツール実行 | runtime/host → journal観測 → `semantic_records.payload_json` | 結果全文を本文参照へ移しても、引数・成否・呼出し関係を維持する |
| ツールの会話 | `agent.Loop` → `AppendConversationMessage` → `messages`と表示用semantic記録 | 同じ結果を文字列として再保存せず、読取りで元のMessageへ戻す |
| 詳細・再表示 | `Records`/`Record`、conversation cut/walk、history/inspect、Core/API → TUI | 小さな一覧は小さく読み、詳細では結果全文を読める |
| recall | `RecallEvidence` → 参考情報projection → 次の依頼 | 通常のRecordsとは別のSQL経路にも必要な本文展開を適用する |
| `/context` | `SessionContext`がrequest/budget/recall metadataをSQLで選択 | 直近model・step・件数・bytes・予算・recall適用を表示できる |
| 構成 | runtime用Snapshot → `executionSnapshot` → `configurations` | 実行に渡す定義は維持し、保存時だけ定義一覧を外す |
| 既存hookの入力記録 | `executionLifecycle.context`のツール定義 → runtime観測 → journal → `context_update.payload_json` | hookへ渡す定義は維持し、DB保存時だけ選択したツール名に置き換える |
| 本文回収 | `DeleteSession`が所有executionと本文参照を集めて削除 | 新しい本文参照も所有と削除を追跡できる |

主な対象は`internal/app`、`internal/history`、関連するhistory/API/TUIの確認と保存契約文書。
モデルへの指示・ツール選択・API送信形式・実行結果を変更する計画ではない。

## 保存の境界

| 情報 | 通常の保存方針 |
| --- | --- |
| 会話・provider state・tool引数・結果・thinking・親子関係 | 保持する |
| 開始終了・成否・model・使用量など | 小さな実行情報として保持する |
| context予算・推定量・選択/除外結果 | 保持する。送信全文は添付しない |
| API送信全文 | executionごとに最新1件。1ターン内に20回送信しても1件 |
| 送信の構成要素ごとの本文と詳細出所archive | 廃止する。通常利用に必要な対応だけIDで残す |
| ツール定義の専用保存・構成Snapshot内の定義一覧 | 廃止する。選択したツール名は残す |
| lifecycle hook入力のツール定義一覧 | hookへ渡す入力は維持し、観測の保存時だけツール名の一覧に縮小する |
| 最新API送信全文に含まれるツール定義 | 実際に送った全文の一部として残る |
| 同じtool結果を包み直した会話・詳細JSON | 本文を再埋込みせずIDで参照する |

`contents`は「本文テーブル」と呼ぶ。同一セッションの会話・実行記録が本文をIDで参照する役割を
中心に設計し、セッション間の再利用を目的に新しい仕組みを作らない。

## 本文の所有・保存・読取り契約

以下を実装の契約とし、各スライスの設計文書へ保存JSONの具体例とSQL対応を記載してから実装する。

### 最新送信全文

- 唯一の所有者は、各executionの最新の論理`model_request`recordとする。
  `semantic_records.content_digest`が`contents`の正確な`WireBody` bytesを参照する。
  予算記録と物理送信開始recordは本文を参照しない。
- 最新の意味は「予算projectionとprovider encodingが完了し、API呼出し前に保存した本文」。
  HTTPが成功した本文だけを指すものではない。APIの失敗・取消後も、この準備済み本文が残る。
  projection等で送信準備に至らなかった場合は、それ以前の本文を保持し、失敗は別の実行情報に残す。
- 新しいrequest recordの追加、以前のrequest recordの本文参照解除、未参照の旧本文回収を
  同じtransactionで行う。commitに失敗した場合はAPIを呼び出さず、置換前の本文を保持する。
- 古い論理request recordは小さなmetadataを保持するが、Contentは持たない。
  最新recordの位置だけを`execution_contexts`へ記録し、全requestのmanifest一覧を再生成しない。
  `execution_contexts`は取得位置であり、本文の所有者ではない。
- 取得は既存のexecution detail/inspect APIを使う。`Records`/`Record`およびinspectのrecordsから
  最新論理recordのContentを取得できる。物理観測の`payload.input`を全文へ復元する契約は廃止する。
  live observerへ渡す実際の入力bytesは変更しない。

### ツール結果本文

- 本文の単位は、一つの観測が返したResultのJSON bytesとする。
  call ID・tool名・時刻・親call・成否はrecordのmetadataとして本文から分離する。
  `resultRef`で本文digestを示し、同じrecordの`content_digest`でその本文を所有する。
- 正常なモデル向けtool結果では、現在の`agent.Loop`が`ToolResult.Text = string(value)`とするため、
  観測Resultと同じbytesを参照できる。保存MessageではTextを内部の`textRef`へ置き換え、
  読取り時に元の文字列へ戻す。生の結果JSONと、そのJSONを文字列として持つMessageを二重保存しない。
- error時のTextはエラー文であり、観測Resultと同じとは限らない。異なるTextは別本文として保存する。
  failure/terminal等の情報は保持し、原結果・加工結果・run_jsの集約結果もそれぞれ区別する。
  bytesの異なる結果を似た文章として照合したり、任意のユーザー結果JSON内を再帰的に参照化したりしない。
- 本文は、参照するexecution自身のsemantic recordで必ず所有する。
  正常経路ではMessageより先に保存する結果観測が所有者になる。
  error文等で対応する所有recordがない本文は、会話保存transaction内で本文所有用の小さなrecordを作る。
  Messageや別executionのJSON参照だけを頼りに本文を残さない。
- Message保存後の表示用semantic記録も結果全文を持たず、Message位置・item位置と本文参照を持つ。
  `AppendOccurrence`返却と`Records`/`Record`は公開payloadへ結果を展開し、
  `Messages`/`Conversation`は元のMessage構造・Textへ展開する。
  tool詳細、recall、次のモデル入力に内部参照形式を渡さない。

### 小さなmetadataと回収

- 論理requestにはrequest ordinal、model step、model選択、メッセージ・ツール・指示の件数、
  wire bytes数、適用したrecall relation IDを保存する。構成要素の本文・定義一覧は持たない。
- `context_budget`に予算・推定量・選択/除外結果を保存し、論理requestへ同じ情報を埋め直さない。
  `SessionContext`はこれらのrecordから現行の表示項目とrecall適用位置を取得する。
- TUI walkが使うrecord ID、ordinal、conversation位置、phase、call ID、parent call、name、
  preview、outcome、error等のmetadataを維持する。tool本文は詳細を開いたときに取得する。
- 回収対象は、最新送信の置換またはSession削除で外れた本文参照だけとする。
  `configurations`、executionのtask、`messages`、`semantic_records`、`diagnostics`、
  follow-up等の既存の本文参照が残る場合は削除しない。
  新しいtool本文もrecordのFKから収集できるようにし、親子executionの削除で未参照本文を回収する。
- 既存table/FKを使い、旧形式のmigration、converter、専用compatibility層は追加しない。
  具体化の途中で新しいtableが必要と判明した場合は、理由・DDL・運用影響を計画へ反映し、
  合意前にその実装へ進まない。

## S-1：送信記録を通常履歴へ整理し、全文を最新1件にする

**得られる動作：** 親・子それぞれで、成功/失敗した最新送信を確認できる。
モデル呼出しを重ねても過去の送信全文を蓄積しない。

- `context_item`の毎回の保存を廃止する。
- 論理requestのoccurrences・splices・revisionと、過去request一覧のmanifestを廃止する。
  直近step・model・件数・bytes数などの小さなmetadataへ置き換える。
- 予算記録は計算・選択結果だけを保存し、本文参照とrequest側への二重埋込みを外す。
- 物理送信開始記録は時刻・ID・provider等を保持し、本文を参照しない。
- 最新全文は唯一の所有recordで保存する。置換と未参照本文の回収はtransactionで行い、
  他の会話・実行・設定が参照している本文は維持する。
- 最新本文の確定はAPI呼出し前とし、HTTP成功を保存条件にしない。
  detail/inspectの論理request Contentを取得位置とする。
- `/context`とrecall適用位置の参照を新しい小さなmetadataへ合わせる。
- 不要になったarchive生成・読取り・関係処理を同じスライスで除去する。

**変更範囲：** appのrequest attribution/journal/projector接続、historyの最新本文保存、
SessionContext、recallのrequest対応、保存契約と該当test。

**確認：** 実HTTP経路で複数回送信し、最新本文と受信bytesが一致し、古い送信全文が残らないこと。
モデルが失敗した最後の送信も残ること。親子で互いの本文を上書きしないこと。
会話継続、`/context`とrecallの対応を既存製品経路で確認する。

## S-2：構成Snapshot・既存hook観測からツール定義archiveを外す

**得られる動作：** モデルは同じツールを利用でき、通常DBに定義を重ねて保存しない。

- 実行用Snapshotと保存用の表現を区別する。保存用からツール名・説明・引数schemaの定義一覧を外す。
- role名・選択したtool名などの通常の構成情報は保持する。
- lifecycle hookの観測入力も保存用のコピーを作り、`context.tools`の定義一覧を外して
  選択したツール名を`context.toolNames`へ保存する。現在の生成経路は
  `executionLifecycle.context` → `Worker.Hook` → runtime観測 → journal保存である。
  runtimeStart/beforeTurn/afterTurn/runtimeStopの既存hookへ実際に渡す入力とlive観測は変更しない。
  そのほかのhook入力・戻り値・実行関係はこのスライスで変更しない。
- inspectと保存契約を更新する。設定や実行中のtool定義、tool利用指示は削除しない。
- 専用保存を前提としていたtestを、実際にモデルへ定義が渡る確認と選択したtool名の確認へ変更する。

**変更範囲：** appの構成保存・hook観測の保存用変換、
関連するroles/tool guidelines/lifecycle/inspectの確認と文書。
hookへのイベント追加や実行方法・入力契約の改善ではなく、通常DBの保存範囲の変更として扱う。

**確認：** roleで選んだツールが親・子のAPI送信と実行で利用でき、configurationには定義一覧がないこと。
既存lifecycle hookが従来の定義一覧を受け取り、保存したhook観測にはツール名だけが残ること。
最新送信全文には実際のtool定義が含まれること。

## S-3：ツール実行記録の結果本文をID参照にする

**得られる動作：** 直接toolとrun_js内の操作の結果全文を、詳細・失敗調査・recallで引き続き読める。
実行記録のJSONに結果全文を埋め直さない。

- model-facing toolと内側tool・hostの、現在の結果生成形を対応付ける。
- 結果本文を本文テーブルへ保存し、観測recordが本文を所有する。
  時刻・call ID・親call・成否・error等のmetadataは通常の記録に残す。
- 本文単位・`resultRef`・公開payloadへの展開を、上記の所有契約に従って実装する。
- `AppendOccurrence`返却、`Records`/`Record`、recallの別読取り経路を合わせて変更する。
- tool詳細は参照を解決して完全な結果を返す。通常のTUI一覧は本文を展開しない。
- 参照形式とSession削除を同時に成立させる。書込みだけ先に変更して読取りを未対応にしない。

**変更範囲：** historyの観測保存・本文codec・Records・RecallEvidence・削除、必要なapp/API確認。

**確認：** 実ファイルの読取りとcommand出力を直接tool/run_js経由で保存し、詳細から元の結果を取得できること。
内側操作と集約後の異なる結果が混同されず、TUI再表示の関係・成否、recallの参考情報が保たれること。

## S-4：継続用tool Messageと表示用記録から結果本文の再埋込みを外す

**得られる動作：** 保存形式は参照でも、次のモデル呼出しと過去Sessionの継続に同じtool結果を渡せる。

- `messages`が持つtool Message内の結果本文を、S-3の本文IDへ置き換える。
- 正常結果のTextはS-3と同じbytesを参照し、error文等で異なるTextは独立した本文として所有させる。
- Messageを写した表示用semantic記録にも結果全文を重ねて保存しない。
- `Messages`/`Conversation`の公開読取りでは、元のMessage構造・本文へ展開する。
  並列toolのcall ID・結果の対応・順序、failure/terminal情報は維持する。
- 直接tool、run_jsの集約結果、子agent操作の結果など、同じMessage経路の利用者を確認する。
- 参照した本文が必ずexecution配下のrecordで所有されるようにし、停止とSession削除の回収を保つ。

**変更範囲：** historyの会話保存・semantic mirror・Message codec・読取り・本文回収と利用経路の確認。

**確認：** 別プロセスでDBを開き直し、次ターンの実HTTP送信に保存したtool結果とprovider stateが渡ること。
複数toolの結果・内側callの詳細・子の報告を読めること。
結果が保存された後の停止でも履歴が読め、Session削除で親子の未参照本文が回収されること。

## S-5：総合確認・容量測定・文書整備

**得られる動作：** Core・CLI・TUIを通して日常操作を完了でき、保存量の改善を実測で説明できる。

- 一時workspace/DBとローカルHTTP modelを使い、製品Core・CLI/TUIから
  実ファイル操作、複数model/tool、親子の並列実行・報告・回収、次ターン、再接続を確認する。
- 成功と途中の停止/失敗について、最新送信全文、履歴、detail、recall、Session削除を確認する。
- ローカルmodelで短い実行と長い実行を同じ作業条件で比較する。
  contextが伸びる区間と予算により選択範囲が落ち着いた区間を区別し、実際のtool結果サイズも記録する。
- DBのtable別本文/JSON/索引量、ファイルと空きpage、保存時間・復元時間・メモリを測る。
  SQLiteの未使用pageは「まだ参照されている本文」と区別する。
- 同条件でDB関連の累積書込み量、transaction数、使用するjournal/WALの累積書込みも測る。
  実際のjournal modeを記録し、WALを使用しない場合はその旨を記載する。
  journal/WALの終了時サイズだけを総書込み量として扱わない。
  SQLiteが書き込んだbytesとprocess/OSの観測値は区別し、測れない指標は未測定と明示する。
- 送信全文を毎回書き込み旧本文を回収するコストを、保持容量の改善と別に評価する。
  書込み量の削減を測定できなければ、容量削減だけを結果として主張する。
- 予算記録のselected sources・累積除外一覧のpayload量を、呼出し回数に沿って別集計する。
  現行projectorは除外済み一覧を累積して毎回factへ出すため、長い単一executionでは
  その保存総量が二次的に増える可能性がある。今回の選択/除外記録保持に伴う残る増加要因として評価する。
  支配的な問題と判明した場合は保存粒度の追加設計を提示し、勝手に情報を省略しない。
- 現行版との容量比較は使い捨てDBで行う。常用DBやバックアップを変換しない。
- 不要になったarchive依存とtestを確認し、設計・payload契約・結果文書を最終状態へ更新する。

呼出し数は測定条件として記録し、test件数・削減率の目標を機能の完成条件にしない。
短い実行の容量だけで数日の耐久性を保証しない。残る増加要因と測定上の限界を結果へ記載する。

## 実装・検証・reviewの進め方

計画への合意後、S-1〜S-4は各スライスで「設計の具体化 → 実装 → focused test → review」を行い、
未解決の会話継続・読戻し・本文所有の問題を次スライスへ持ち越さない。
testのないこと自体を問題にせず、上記の具体的操作が成立するかを確認する。

独立reviewを行う場合は、各スライスの変更された利用経路と採用済み保存契約だけを対象とする。
初回30分、再reviewは変更箇所と既存findingの解消確認を15分以内とする。
一般的なhardeningや仮想的な互換・状態matrixを追加しない。
reviewerは全体gateを実行しない。

S-5で安定候補に対し、影響するpackageの一括testとstaticcheck、diff check、総合実経路確認を行う。
一括確認をスライス途中で繰り返さない。新しい失敗があれば該当経路のfocused確認で原因を特定する。
ローカルHTTP確認は製品の保存・通信・UI経路の検証であり、実モデルの判断品質の評価とは区別する。
有料の外部model確認が必要になった場合は、対象と費用発生を示して別途確認する。

## 対象外・運用境界

- hookのイベント追加・実行方法/入力契約の改善、精密な収集プログラム、圧縮、要約checkpoint、
  自動履歴削除、保持数制限。既存hook観測に含まれるツール定義の保存縮小だけはS-2の対象。
- assistant本文・thinking等の保存構造全体の再設計、本文テーブル自体の廃止。
- 旧DBの移行・変換・削除、新しいDBへの再初期化、起動中Coreへの自動反映。
- commit/push/release。

常用環境へ反映する前に、その時点のDB・Core・保存形式の状態を確認する。
既存dataの操作が必要なら、実装・確認済みの結果と具体的な手順を提示してから利用者の指示を得る。
この計画の合意だけで過去の実データを削除・変換しない。
