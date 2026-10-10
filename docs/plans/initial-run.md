# 非対話runの初期製品実装計画

作成日：2026-10-10。計画レビュー後、利用者の指示で製品実装・テスト・レビューを実施した。
採用範囲の正本は[初期製品の方針](../design/initial-product.md)、保存契約の正本は
[旧Henjiと同一のschema](../design/history-schema.md)とする。

P-01〜P-05の実装と実経路の確認を完了した。[結果・実行証拠・review修正](../results/initial-run.md)を参照する。
以下の現状・未確認事項は着手時点の計画入力であり、その後の到達点は結果文書へ記録する。

## 目的と完成する操作

Linuxで非対話runに依頼を渡すと、OpenRouterのモデルが外部toolを使い、生成JSで処理を組み合わせ、
実ファイル等に作業を行って最終回答を返す。別プロセスから履歴を閲覧し、そのSessionを再開できる。
tool／hookの編集は次のWorkerへ反映され、過去の実行は当時の構成・途中の処理まで確認できる。

最初の実利用例は、用意した作業ディレクトリのファイルを読み、モデルが生成したJSから外部toolを呼び、
内容を加工して別ファイルへ書き、結果を確認して回答する作業とする。
次のrunでは、その会話を踏まえて追加の編集を依頼する。
固定JSの実行結果を、実モデルによる完了の代わりにしない。

## 根拠・現在の経路・未確認事項

現在の入口は `spikes/s01` の実験CLIである。固定JSを実行し、JS runtimeから外部tool・hook、
Goのfile／HTTP／processへ到達し、簡略historyへ結果を書き込む。
S-04の会話はuser／assistantの文字列ペアで、S-05のrun／adoptも実験用操作である。
製品のモデル接続、Go Agent loop、旧schemaでの保存、Session再開はまだ存在しない。

| 根拠 | この計画で使う確認済み事項 |
| --- | --- |
| 利用者の指定 | Linux、OpenRouter、非対話run先行、旧Henjiと同一のschema。他の範囲は採用済み方針に従う |
| [S-01](../spikes/s01-results.md)・[S-03](../spikes/s03-results.md) | QuickJSのVM所有、Promise jobs、Go bridge、割込み、外部ESM・tool／hookの成立範囲 |
| [S-02](../spikes/s02-results.md)・[S-04](../spikes/s04-results.md) | 内側呼出しの証拠、会話採用との分離、開始時の構成捕捉、範囲読込みの実現性 |
| [S-05](../spikes/s05-results.md) | Linux/amd64の静的配布、Go／Node／curlなしの起動、HTTPSと外部commandの依存 |
| [固定した旧source](../design/schema-source.json) | schema v3、message・構成・Session・意味記録・digestの保存契約 |

OpenRouterの外部契約は以下を使う。

- [Tool calling](https://openrouter.ai/docs/guides/features/tool-calling)：assistantのtool callと対応する
  `tool_call_id` のtool結果を次requestへ渡し、各requestにtool定義を含める。
- [Streaming](https://openrouter.ai/docs/api_reference/streaming)：SSEのコメントと完了通知を扱い、
  分割された応答を組み立てる。HTTP成功後のstream内errorも実行失敗として扱う。
  ローカルで接続を閉じたことと、接続先の処理・課金停止が保証されることは区別する。
- [Reasoning tokens](https://openrouter.ai/docs/guides/best-practices/reasoning-tokens)：返されたreasoningと
  reasoning detailsを保存し、必要な継続情報を保つ。通常の最終回答とは分けて扱う。

未確認事項は、実際に使うmodel IDとそのtool／stream応答、複数モデルstepをまたぐWorkerのVM寿命、
旧payloadのDeno専用build情報とGoの対応である。各段階で下記の通り解消する。
API credentialの値は計画・構成archiveへ含めない。実API確認時には利用するmodelとcredential参照先が必要になる。

## 製品の実経路と状態の所有者

`CLI → 共通Go操作 → 構成捕捉・execution開始 → Go Agent loop → OpenRouter`
から、モデルのtool callを `JS runtime → 外部tool／hook → Go基礎操作` へ接続する。
その結果をモデルへ返して繰り返し、完了時に会話へ採用する。
各段階のmessageと証拠はhistoryへ保存し、閲覧・再開も共通Go操作からhistoryへ到達する。

| 責務 | 所有する状態と公開操作 | 想定配置 |
| --- | --- | --- |
| 共通操作 | Session作成・一覧・閲覧、run開始・停止、進捗、再開と正常turnの採用 | `internal/app` |
| Agent loop | 実行中のmessage、model step、tool結果、終了結果 | `internal/agent` |
| Provider | OpenRouterのHTTP／SSE、選択model、credential参照、provider stateの変換 | `internal/provider/openrouter` |
| 構成・拡張 | 開始時に固定した設定・source bytes・tool定義・hook定義 | `internal/extensions` |
| JS runtime | Worker専用VM、module、Promise、割込み、値の解放 | `internal/jsruntime` |
| 基礎操作 | context付きfile／HTTP／Linux process実行。VMを操作しない | `internal/host` |
| History | DB、旧保存codec、execution・message・証拠、採用transaction、範囲query | `internal/history` |
| 共通値 | message、model選択、tool定義、結果・観測の値。DBやVMを所有しない | `internal/contracts` |
| CLI | flag・stdin／stdout／stderr、signal、表示 | `cmd/hjh` |

配置は上記責務を実装するための出発点であり、空の抽象層を先に量産しない。
依存はCLIからapp、appから実行・構成・historyへ向ける。
Agent loopはproviderとtool実行の操作契約を使い、runtimeやproviderからDBへ直接書き込まない。
VM値を境界の外へ渡さず、普通のGo値・構造化結果・観測で接続する。
historyは生成済みmessageや観測を保存し、モデル呼出し・tool実行を開始しない。

製品用のroot Go moduleを作り、QuickJS・SQLiteは検証済みversionを出発点とする。
実験moduleと簡略DBは実験資料として残す。実験の `internal` packageを製品から直接importせず、
実証した処理を製品の責務へ移す。旧Henjiの巨大store、Worker通信、Core・queue全体を移植しない。

## P-01：旧保存契約とGo historyの最小経路

**利用者が得る動作：** runの全messageと実行証拠を保存し、正常turnだけを会話へ採用できる。
製品から新規作成したDBを別プロセスで読める。

最初に `docs/design/history-payloads.md` を作り、旧sourceから保存field・encoding・Go型・書込み先を
対応付ける。以下を扱い、DDLだけが同じという状態で実装を先へ進めない。

| 対象 | 実装する契約 |
| --- | --- |
| Message | `role`、text content、tool call配列、tool結果配列、callId・name・arguments、outcome、任意のfailure／terminal、assistantの併記text |
| Provider state | `provider`、`model`、reasoningのfield／text、`reasoningDetails`。構造化detailsは未知の内容を削らず保持 |
| Model選択 | `provider: openrouter-chat`、`api: openrouter-chat-completions`、`authProfile`、`modelId`、`effort`。authProfileは参照名でありkey値ではない |
| Session | workspace・agent選択・revision・next turn・件数・model変更。会話本体は採用済みmessageへの参照から復元 |
| 構成 | 旧snapshotのfieldとconfiguration ID。構成IDとcontentsのdigestを混同しない |
| 意味記録 | 旧kind・ordinal・内容参照・relation、終了記録、未完了のassistant text |
| execution開始値 | max_stepsは呼出し元のINTEGERを保存し、history側で上限を決めない。実runの指定／無制限はP-03の操作契約へ分離 |
| Contents | 正確な保存bytes、byte length、`sha256:`付きdigest。読戻したblobを再encodeして同一性を判定しない |
| Encoding | message／構成のJSONと、意味payload／contextのcanonical JSONを区別。Goのmap順序や標準escapeの違いを検討し、必要な箇所で旧encodingへ合わせる |

Goではrole／kindに応じた型とcodecを使い、provider固有の継続情報はJSONとして保持する。
別providerの実行機能は追加しない。単純なuser／assistant文字列ペアへ縮約しない。

初期の保存対応案は次の通り。payloadの具体形・relation名は上記文書で固定する。

- `configurations` に旧構成snapshotを置く。実際に使うsource・依存のbytesは `contents` へ保存し、
  `resource_revision` と構成・実行の関係から辿れるようにする。独自の `config_assets` 表は作らない。
- 生成JSのモデルcall、内側tool、hook、Go操作は既存のtool／context／effect系の意味記録とrelationへ対応付ける。
  内側呼出しを独立した子Agent executionに見立てない。モデルへ返す集約結果と各操作の証拠を区別する。
- Goの実行観測から記録を作り、存在しないDeno Worker通信を生成しない。

旧 `BuildManifestV1` は必須の `denoVersion` を持つ。
利用者は実装開始時にGo用の `goVersion` への変更を明示承認した。
DDLと他のbuild fieldは保持し、このJSON契約の差分を[保存対応表](../design/history-payloads.md)に記録した。
Go versionをDeno versionと偽装せず、旧build codecとの完全同一を主張しない。

その後、root moduleとhistoryを実装する。

1. [採用DDL](../design/henji-history-schema.sql)から新規DBを作り、schema versionを3にする。
   後続機能の表もDDL通り保持するが、初期機能で使わない表へ仮のrecordを作らない。
2. Session／execution開始、content・message・意味記録の追記、正常／失敗／取消の終了を実装する。
3. 正常終了後、保存済みmessageの参照、turn、Session revision・件数、executionの採用状態を同一transactionで更新する。
   利用者向けの手動adoptは設けない。
4. Session一覧、採用会話、executionの証拠・使用構成を読む操作を実装する。

**確認：** Go driverで作ったDBと採用DDLの列・制約・FK・index・versionを照合する。
旧serializerに基づくmessage・構成・contextの具体例でcodecを確認する。
一つのexecutionにuser、assistantのtool call、tool結果、最終assistantを保存し、別プロセスから順序を保って読めること、
会話採用の参照とrevisionが一致すること、失敗・取消の証拠が残り会話へ混ざらないことを確認する。
既存Henjiの実DBや実験DBは書換えない。

## P-02：外部toolと複数stepを支えるWorker runtime

**利用者が得る動作：** 外部JSのfile toolを生成JSから呼び、結果を次の処理へ使える。
外部sourceの編集と初期化例外の原因を確認できる。

- Workerを一回のrunのAgent実行単位とし、専用VMを開始から終了まで所有する方針で実装する。
  複数model stepでも同じ捕捉済みmoduleを使い、別WorkerとはVMを共有しない。
  実験の「生成JS一回につきVM一つ」から延長する部分は、この段階で実行して確認する。
- VM所有goroutineにmodule評価・tool呼出し・生成JS・Promise jobs・解放を集める。
  Goの非同期host処理は完了値を渡し、VMを直接操作しない。
  モデルのHTTP待機とVMのjob処理を混同せず、終了時に割込みwatcher・host処理・VMの寿命を揃える。
- 外部ESMの定義は名前・description・inputSchemaと実行関数を持たせる。
  `run_js` はGoが所有するruntime入口へ接続し、外部標準toolの振る舞いはJSに置く。
  生成JSは `tools` から登録toolを呼び、構造化結果を返す。toolはGo基礎操作と他のtoolを利用できる。
  任意のNode／Deno互換やruntimeでのnpm取得は追加しない。
- beforeTool／afterToolをモデルの直接tool callと生成JS内のtool callへ共通に適用し、呼出し関係を記録する。
  詳細な引数・結果・例外の契約を `docs/design/extensions.md` に記載する。
- 開始時に読み込んだsource bytesを実行・archiveの双方へ使う。
  実行中に再読込みせず、編集は次Workerへ反映する。
- module評価の完了値・rejectionを確認して、S-03で観測した原例外を保持する。
  単にpending jobsを回して成功とみなす処理を移植しない。

**確認：** 同じWorkerで二つの生成JS処理を行い、tool・hook・依存の状態と結果を確認する。
読取り→加工→書込みを実ファイルで確認し、内側callとGo操作が履歴で辿れることを確認する。
module初期化例外が原原因として届くこと、編集前のWorkerと編集後のWorkerで使ったsourceが一致することを確認する。
停止は実証済みのJS割込みとhost contextを接続し、処理・watcherが終了して次Workerが動くことを確認する。

## P-03：OpenRouterと非対話runの最初の実経路

**利用者が得る動作：** モデルへ依頼し、生成JSと外部file toolで作業を完了して回答を受け取る。
この段階で実モデルを接続し、初期版の全機能を待たずに中核経路を確認する。

- OpenRouter adapterは設定したmodelでChat Completionsを呼び、streamのtext・tool call・usage・errorを扱う。
  分割されたtool名・argumentsを完成させてから実行する。SSEのコメントをJSONと誤認しない。
  正常な外部応答をfixtureのfield一覧で拒否せず、opaqueなreasoning detailsを保持する。
- Go Agent loopはuser入力、assistant応答、tool call、tool結果を順に保存し、次のモデル判断へ渡す。
  providerのcall IDを保ち、各requestへtool定義を渡す。tool結果を固定の最終回答へ置き換えない。
  モデルが最終回答を返したらexecutionを終了し、共通操作が正常turnを採用する。
- model選択・入力projection・終了理由・schema必須の `max_steps` の表現を実装前に
  `docs/design/run-api.md` へ記載する。未合意のstep上限や自動履歴切捨てを設けない。
- CLIの基本形は `hjh run [--session ID] [--model ID] [--workspace PATH] [--db PATH] [--config PATH]` とする。
  依頼は引数またはstdin。新規runではSessionを作り、`--session` は採用済み会話から再開する。
  modelはflagまたは設定から選ぶ。credentialは環境変数等の参照として扱う。
  flagの既定pathと入力優先順位はrun API文書で固定する。
- stdoutは最終回答、stderrは進捗とSession／execution IDを基本とする。
  成功・失敗・取消は終了状態と終了codeで判別できるようにする。
  SIGINTを共通停止操作へつなぐ。途中textは最終回答の確定と区別する。
- OpenRouter認証は専用transportで付与し、key値とAuthorization headerを構成・進捗・履歴へ渡さない。
  userの内容全体を加工する一般的なsanitizationは追加しない。

**確認：** 選定した実modelに最初のファイル作業を依頼し、実ファイルの内容、回答、保存message、内側処理を照合する。
同じ製品CLIを別プロセスで再開し、前の作業を踏まえた追加編集まで行う。
model ID・実行ID・使用構成・結果を記録し、credentialは記録しない。
stream組立ては実応答と公式契約に基づくfocused確認、認証の露出防止はtransportから保存・表示へ至る経路で確認する。

## P-04：日常利用に必要なtool・履歴・再開を仕上げる

**利用者が得る動作：** ファイル作成・編集、command、HTTPを含む依頼を行い、進捗・失敗・取消を振り返れる。
tool／hookの編集後も同じSessionを現在の設定で再開できる。

- 外部標準toolをread／write／edit／command／HTTPへ広げる。
  editは編集対象と置換内容を指定する操作、commandはprogram・args・stdinとexitCode・stdout・stderr、
  HTTPは応答statusとbodyを扱う。非zero exitやHTTP statusは結果としてモデルへ返す。
  ファイル編集の具体的な引数・置換規則は実利用例からextensions文書へ記載する。
- `hjh sessions`、`hjh history --session ID`、`hjh inspect --execution ID` を共通Go操作へ接続する。
  会話と実行証拠を区別し、範囲指定でmessage・tool・構成を閲覧できるようにする。
  一覧で全contentsを読まず、範囲queryで必要なblobだけを読む。
- 再開では現在の構成から新Workerを作る。採用済み会話を読取り、現在のprovider／modelに対応する継続情報を渡す。
  過去のmodel選択・構成・sourceは別に参照できるようにする。
- 入力範囲は完全なturnと対応するtool call／resultを保つprojectionとして定義する。
  全保存履歴は保持し、指定範囲でモデル入力を作る。自動要約・古い履歴の削除は追加しない。
- stream途中のtext、model／tool失敗、取消要求と停止結果は会話採用と別に保存・表示する。
  JS無限処理、待機中HTTP、実行中commandそれぞれを同じrun停止操作へつなぐ。
  Linux commandはprocess group停止とWaitまで扱い、残った副作用は履歴から確認できるようにする。

**確認：** 製品runから実ファイルの編集、用意したcommand、実HTTP取得を行う。
外部tool・hookを編集してSessionを再開し、新旧sourceと結果が履歴で一致することを確認する。
実行中のJS・HTTP・commandを停止して途中の記録を閲覧し、その後のrunが利用できることを確認する。
範囲閲覧と入力projectionは、完全なturnが読め、対象外の大きなblobを読まないことに対応する確認を行う。
step間のtool失敗も含め、fixtureの成功だけで日常利用の完了とは扱わない。

## P-05：Linux配布と利用手順

**利用者が得る動作：** 配布ディレクトリから起動し、開発環境なしで同じ実モデルの作業・再開ができる。

- S-05の方式を基に、Linux/amd64向けGo本体、編集可能な標準tool・hook、準備済みESM依存、
  設定例、license／noticeを一つの配布物にする。生成されたarchiveはgit管理外へ置く。
- CGOなしの本体と、配布先のOS CA／DNSでOpenRouter HTTPSへ接続する。
  基本HTTPにcurlを要求しない。外部commandの追加依存は、そのtoolの利用手順に記載する。
- READMEに導入、credential参照、model設定、run、停止、一覧・閲覧・再開、拡張の編集手順を記載する。
  S-05で未確認の他architecture／distributionを対応済みと表示しない。

**確認：** Go／Node／curlがないLinux検証環境で配布物を使い、実OpenRouterによるファイル作業、
履歴閲覧・別プロセス再開、source編集の反映を行う。外部commandは必要なprogramがある環境で確認する。
使用環境と配布物のdigest、実行結果を記録する。公開・releaseはこの計画に含めない。

## 検証・完了報告の扱い

各段階では、上に挙げた具体的動作に対応するfocused test、必要なformat・type／build確認、
`git diff --check` を行う。全体gateの反復や、仮想的なpermission・入力matrixを追加しない。
正常な実service応答とtestが矛盾した場合は実応答・公式契約を確認し、testを見直す。

P-03の中核経路、P-04の日常操作、P-05の配布環境での動作をそれぞれ結果文書へ記録する。
未確認のmodel機能や配布先は未確認として残し、test件数を製品の完成条件にしない。
TUIはこの非対話runの実経路と共通Go操作が成立した後に計画する。

P-01の[保存payload対応表](../design/history-payloads.md)を確定してからhistoryへ進み、
P-05まで実施した。実装・確認の到達点は[結果文書](../results/initial-run.md)に記録した。
