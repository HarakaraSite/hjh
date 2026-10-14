# 保存payloadとGo実装の対応

2026-10-14の通常履歴整理は[通常履歴の保存契約](normal-history-storage.md)を参照する。
毎回の送信全文・context構成archiveとツール定義保存、tool結果本文の重複を整理する。
新しい契約・実装の到達点は[結果](../results/normal-history-storage.md)に記録する。
同日の追加改善は[上書き計画](../plans/history-snapshot-overwrite.md)を採用し、新規Go DBをschema v4へ変更する。
wireはexecution_latest_requests、最新の完全なbudget Factはexecution_latest_budgetsへ直接保存する。
各回のcontext_budgetはsummaryとdetailAvailable:falseだけを持ち、最新の公開読取りにだけ
factを付けてdetailAvailable:trueとする。今回の到達点は[追加改善の結果](../results/history-snapshot-overwrite.md)を参照する。
以下のv3に関する記述は初期・各increment採用時の契約であり、現在のversionは[schema契約](history-schema.md)に従う。

2026-10-11更新。取得元は[固定したsource](schema-source.json)、DDLは
[schema v3](history-schema.md)。初期の対応に、後続incrementで採用した保存契約を末尾の各節で追加する。
旧Core、queue、permission実装を追加する契約ではない。
I-09で新規に書く入力receipt、予約、steering messageの設計は
[追加入力契約](i09-runtime-input-contract.md)を参照する。schema v3を維持し、
2026-10-13の利用者指定により、steeringの受付Messageは`command_receipts.result_json.acceptedMessage`へ
直接保存し、適用時には実行messageとして独立に保存する。受付と適用messageで保存データを共用しない。
semantic mirrorは本文を含まないmetadataと適用messageのcontent digestを持ち、cold replayで
保存Messageから本文を読む。follow-upの保存方式は変更しない。旧receipt形式の互換変換は含めない。

I-10の[recall契約](i10-context-recall-contract.md)では、操作結果をcommand_receipts、
開始時のsource/target relationをrecall_relationsへ保存する。実参考messageはcontentsで保持し、
semantic mirrorと会話walkは本文のないrecall markerを使う。DDLは変更しない。

## 会話・model

Messageは旧 `core/contracts.ts` のroleとcontentを保持する。
user／最終assistantのcontentは `{kind:"text",text}`。
assistantのtool呼出しは `{kind:"tool_call",callId,name,arguments}` の配列で、
同じ応答の可視textは任意の `text` fieldへ置く。
公開tool Messageのcontentは `{kind:"tool_result",callId,name,text,outcome}` の配列。
保存時はtool結果のtextをtextRefへ置き換え、本文を同executionのrecord FKで所有する。
Messages/Conversationとsemantic詳細の公開読取りでtextへ戻す。結果JSON自体の内部は変更しない。
任意のfailure、terminal、steeringは旧fieldのまま保持する。
providerStateはprovider・model、reasoningのfield／text、reasoningDetailsを保持し、
構造化reasoningとtool argumentsはJSONのまま扱う。

OpenRouter選択は旧fieldのprovider、api、authProfile、modelId、effortを使う。
authProfileは `openrouter-api-key` という非秘密の参照名であり、実keyは保存しない。
SessionのagentChoiceはname／file。採用会話はsession_turnsとconversation_messagesから復元する。
private_state_from_turnは継続情報を利用する開始turnとして保持する。
checkpointは初期runで生成せずNULL。自動要約は追加しない。

## Bytes・JSON

contentsのdigestは `sha256:` + 実bytesのSHA-256小文字hex。
読込みでは保存済みbytesを取得してからdecodeし、再encodeしたbytesで既存digestを置き換えない。
messageと構成snapshotはUTF-8のJSON。field名・union形・値の意味が保存契約であり、
通常JSONのobject field順序を読み手の意味にしない。
新規Go書込みではHTML文字を不要にescapeせず、保存済みのJSONは再書込みしない。

意味payloadとcontextのdigest対象は旧canonical JSONに合わせる。
object keyはJavaScriptと同じUTF-16順、配列は順序を保持し、primitiveはJSON.stringify相当でencodeする。
canonical対象を通常のmessage serializerと混同しない。

## Executionと会話採用

開始時にSessionのstate_revision、next_turn、message_countを捕捉する。
executionにtask bytes、model、build、configuration ID、必須INTEGERのmax_stepsを保存する。
history側は呼出し元が渡すmax_stepsを保存し、上限や既定step数を決めない。
実runの上限指定／無制限の表現はrun APIで決め、test用の値を製品既定へ逆輸入しない。

messageはexecution内で追記し、証拠はsemantic_recordsへ追記する。
終了のhost_decisionを保存してlifecycleをsettled、outcomeをcompleted／failed／cancelled／interruptedへ更新する。
正常完了をSessionへ採用する操作は、保存したmessage全体への参照、turn対応、Session revision・件数、
executionのadoptionを同じtransactionで更新する。
開始時revisionとの一致を確認して、他のturnの会話を上書きしない。
失敗・取消のexecutionは保存した証拠を保持し、会話へ採用しない。

## 構成・source・観測

configurationsのsnapshotは旧WorkerConfigurationSnapshotのfieldを使う。
configurationIdはUUIDであり、内容digestとは別の識別子。
agent、source、systemInstruction、instructionComponents、toolPaths、hooks、rejectionsを保持する。
実行用Snapshotはtoolsを持つが、configuration保存JSONには定義一覧を含めない。
fileAccess／paths等のfieldの存在から旧permission機能の実装を必須にしない。

I-21の有効tool利用指示は、実行用snapshot.toolsのoptional `promptGuidelines`から、
`instruction:active-tool-guidelines` componentとsystemInstructionへ組み込む。
保存configurationはこの集約指示と選択したtool名を保持する。providerのtool definitionには追加fieldを入れない。
未選択toolの指示は含めない。宣言元configは実行時に捕捉するが、ファイル全体のexact bytesを
DBへ自動保存しない。定義本体は各executionの最新API送信全文にだけ残す。

2026-10-13、利用者指定によりツール・hook・依存ファイル・元configの自動source archiveを廃止した。
各Workerは実行に必要なsource bytesをメモリ上へ捕捉して固定し、そこからESMを評価する。
`Snapshot.Resources`を`resource_revision`/`contents`へ自動保存しない。
使用構成snapshot、モデル指示・入力、呼び出し・結果、モデルが生成した`run_js`コードは従来どおり保存する。

I-18のAGENTS.mdと利用者の基本指示fileは、各executionで原bytesと出典をresource_revision/contentsへ記録する。
通常履歴ではmodel送信ごとのcontext_itemによる構成要素archiveを生成しない。
組立済みsystemInstructionにworkspace指示とskill manifestを追加し、生成した指示componentを
instructionComponentsへ保存する。未利用skill本文は新カタログへ保持しない。
利用時のread_file本文は通常のtool結果/messageへ保存し、生成JS経由なら外側returnにも含める。
既存config source捕捉とskill配置が重なる場合の扱いは
[workspace指示・skill契約](workspace-instructions-skills.md)に従う。

生成JSの外側callと内側tool／hook／host操作は普通のGo観測値から意味記録へ対応付ける。
tool_call／tool_result、context_update、effect_observation等の旧kindを使い、
各callの開始record、終了record、親call開始recordをrelationで結ぶ。
call IDと親IDをpayloadに残し、未終了callでも開始と実際の副作用を確認できるようにする。
モデルへ返す集約tool結果はmessagesへ保存し、内側の証拠で置き換えない。

assistant_text_statesはまだ完了していないmodel requestの最新textを持つ。
完了後のmessageとは分け、失敗／取消でも観測済みtextを失わないようにする。
model_requestのrequest bytesは各executionの最新準備済み送信だけを保持する。
論理requestに小さな件数等を保存し、execution_contextsはrecall関係を保持する。
公開の最新位置はexecution_latest_requestsとmetadataから生成する。
context_budgetの各回summaryとexecution_latest_budgetsの最新Factはwire本文を参照しない。
物理送信開始recordはmetadataのみ、live観測は全文を持つ。credential headerは観測へ渡さない。
旧ExecutionContextManifestV2とcontext_item archiveは新規生成しない。

## Build情報：利用者が承認したGo専用差分

旧BuildManifestV1には実Deno versionを意味する必須denoVersionがある。
history APIはbuildをJSONとして受け取り保存するため、この未決定はmessage／storeの実装を止めない。
旧serializerの読戻し確認では実際の旧形を使い、Go実行のbuildとして書き込まない。

2026-10-10に利用者が「Go用のgoVersionに変更する」と承認した。
Go実行ではDDLと他のbuild fieldを保持し、denoVersionだけをgoVersionへ変更する。
Go versionをDeno versionと偽装せず、旧build codecと完全同一という主張もしない。
これは明示承認されたJSON契約の差分であり、Go実行のbuild書込みへ適用する。

## I-02：子execution

DDL v3は変更せず、既存の `parent_execution_id`・`spawn_call_id` に直近の親と委譲callを保存する。
子の `canonical_session_id` はNULL、`session_correlation` はroot Session ID、
`turn_number` は親turn、`base_revision`・`base_message_count` は0。
子は `non_canonical` のまま終了し、Sessionのrevision・turn・message countを直接更新しない。
表示位置は同じSession相関へ追加し、execution一覧から子も閲覧できる。

子ごとにtask・model・build・構成snapshot・messages・context・途中本文・終了recordを保存する。
実装source bytesは親子ともWorkerの実行時捕捉だけに使い、DBへ自動保存しない。
各journalのrecord IDとcall IDは親子で混ぜず、観測payloadへexecution IDとparent execution IDを加える。
親の委譲開始recordと子開始を `parent_call`、親の委譲結果recordと子terminalを `child_terminal` で結ぶ。
同じexecution内のcall開始／終了・内側callのrelationも維持する。

読み出すExecutionはparent・spawn・terminal IDを持ち、親inspectのchildrenは直接の子を返す。
子の正常／作業失敗結果は終了保存後に返す。保存errorが発生した場合は共通Go実行の終了errorへ保持し、
親を正常完了・会話採用にしない。取消時もcancel済みcontextと分けて終了証拠を保存する。

## I-03：roleと実tool構成

DDL v3とJSON field構造は維持する。executions.agent_name、snapshot.agent.nameは実role名。
Sessionのagent_choice_jsonは{name: 実role名}で、成功rootだけが会話・modelと同時に更新する。
子はcanonical Sessionを持たず、choiceを更新しない。genericのコード版は既存build情報で辿る。

snapshot.agentのrevision／instruction／toolsは選択roleに対応し、agentsはrun_agent選択時のcatalog名、
それ以外は空配列。実行用snapshot.toolsはmodel definition・JS registryと同じ選択結果を持つ。
configurationには選択roleのagent.tools名を保存し、定義を付け足さない。
source.fileは実config絶対locator。resource_revisionは利用者の基本指示とworkspace指示のbytesを保持するが、
モデル送信ごとのsource archive・外部拡張の実装ファイル・元config全文は自動保存しない。
未await委譲では、子join後・外側call終了前に残るtool／hook観測をterminalへ閉じ、開始記録だけを残さない。

## I-04：実接続の保存

DDL v3とselectionの既存fieldを維持する。provider・api・authProfile・modelId・effortへ実selectionを保存する。
provider定義とrole.modelは実行時に捕捉したconfigから解決する。元config全文はDBへ自動保存しない。
実際に選んだprovider・api・authProfile・modelId・effortはexecutionのmodelとモデル入力の記録へ残す。
providerState.replayItemsにResponsesのraw output itemを保存する。providerState.providerは、
送信先・API・credential参照・非秘密account identityを含むdigestによる状態のidentity。
同じ送信先・account・modelだけで再送し、別接続には可視会話とtool call／結果からinputを組み立てる。
最新の論理model_request contentはadapter.Prepareの実送信bytesを保持する。
同executionの以前のwire参照は外し、他の所有者がない旧本文を同transactionで回収する。
Authorizationはtransportのみに渡す。raw応答の保存を削除・書換えず、既存Session selectionを自動変換しない。

## I-06：途中本文の保存位置と会話identity

2026-10-11。I-06で実装。[I-06契約](i06-conversation-contract.md)の保存節を参照する。
DDL v3とmessages/contentsの保存形は維持する。
新規書込みでは表示に関わるsemantic payloadへconversation metadataを加え、rootと子に共通の受付順を記録する。
途中本文は小さい開始markerと最新assistant_text_statesへ保存し、累積全文の永久追記を撤去する。
完了resultとstate終了、未完了本文への移送とterminalはそれぞれ同じtransactionで行う。
本文が途中→確定へ変わっても同じentity ID/位置を維持する。公開thinkingも可視text/summaryだけを同じ方式で保存する。
公開thinkingのpayloadは`thinkingKind`（`reasoning`／`summary`）と完全なitem keyを保持し、
本文・要約の開始、最新state、確定・未完了、再開で同じentityへ結び付ける。
modelが宣言したtoolと外側runtime呼出しのcall IDは共用し、Agentが開始・結果を保存する。
runtime代理呼出しの開始・結果を重複保存せず、実nested callは別IDを維持する。
既存データの書換え・削除は行わず、messages、provider再送state、tool/context/sourceの必要な保存を維持する。
性能・実利用・reviewは[I-06結果](../results/i06-conversation-tui.md)へ記録する。

## I-20：lifecycle hookと終了後記録

configuration snapshotの`hooks`にはmodule locatorと設定上の`handlers`宣言を保存し、
実際にcapture・評価するESM source bytesはWorker内に保持し、DBへ自動保存しない。
宣言一覧は呼出可否のfilterではない。Worker開始時の`hook/handlers` observationは、
moduleから実際に見つかったlifecycle/tool hook function一覧を保存する。functionがない場合も空配列を記録する。
後からconfiguration snapshotの宣言一覧を書き換えて実export一覧を表すことはしない。

各hook呼出しは通常のhook observationとしてinput、開始/終了、返却値または例外を保存する。
runtimeStart/beforeTurn/afterTurn/runtimeStopの保存Inputだけはcontext.toolsをtoolNamesへ置き換える。
実hook入力とlive observationは元のツール定義一覧を持つ。
`runtimeStart`／`beforeTurn`の`{context: string[]}`はcapture済みのconfiguration snapshotには加えない。
返した文脈は実executionのinstructionに適用し、hook結果の通常recordと最新の送信全文で確認できる。
構成要素の専用context archiveは作らない。
追加文脈を含むmodel requestは既存のcontext budget記録経路を使う。次turnへ動的文脈を累積しない。

terminal保存後の`afterTurn`、`runtimeStop`とその内側host/tool observationは専用の
`AppendPostSettlementObservation`経路で追記する。追加recordのpayloadには`postSettlement: true`と
`terminalRecordId`を持たせ、確定terminalへの必須`settlement` relationを保存する。
semantic record ordinal、executionのlatest ordinal／occurrence count、relation解決状態は通常どおり進む。
terminal record、runtime outcome、execution outcome/adoption、messages、Session revisionは変更しない。
通常の`AppendOccurrence`はactive execution専用のままである。

hook成功・失敗・skipとhook内処理の詳細は`inspect`および履歴detailから確認できる。
失敗・skipは既存context noticeにも保存され、CLIではstderr、TUIでは表示・復元可能なnoticeとして示す。
noticeは最終回答stdoutや親のmodel inputには混ぜない。
