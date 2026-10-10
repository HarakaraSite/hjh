# 保存payloadとGo実装の対応

2026-10-10。取得元は[固定したsource](schema-source.json)、DDLは
[schema v3](history-schema.md)。この対応はI-01・I-02で使う保存経路に限る。
旧Core、queue、permission実装を追加する契約ではない。

## 会話・model

Messageは旧 `core/contracts.ts` のroleとcontentを保持する。
user／最終assistantのcontentは `{kind:"text",text}`。
assistantのtool呼出しは `{kind:"tool_call",callId,name,arguments}` の配列で、
同じ応答の可視textは任意の `text` fieldへ置く。
toolのcontentは `{kind:"tool_result",callId,name,text,outcome}` の配列。
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
agent、source、systemInstruction、instructionComponents、toolPaths、tools、hooks、rejectionsを保持する。
fileAccess／paths等のfieldの存在から旧permission機能の実装を必須にしない。

実際に読み込む各source・依存bytesはcontentsへ保存する。
resource_revisionのpayloadにconfigurationId、logicalIdentity、sourceLocator、contentDigestを記録し、
content_digestでも当該bytesを参照する。使用sourceの場所とbytesをexecutionから辿れるようにする。
独自のasset表・旧snapshotを包む新しい保存形式は作らない。

生成JSの外側callと内側tool／hook／host操作は普通のGo観測値から意味記録へ対応付ける。
tool_call／tool_result、context_update、effect_observation等の旧kindを使い、
各callの開始record、終了record、親call開始recordをrelationで結ぶ。
call IDと親IDをpayloadに残し、未終了callでも開始と実際の副作用を確認できるようにする。
モデルへ返す集約tool結果はmessagesへ保存し、内側の証拠で置き換えない。

assistant_text_statesはまだ完了していないmodel requestの最新textを持つ。
完了後のmessageとは分け、失敗／取消でも観測済みtextを失わないようにする。
model_requestのrequest bytesとcontext manifestを保存し、credential headerは観測へ渡さない。
contextの具体的な生成はprovider／Agent接続時に旧ExecutionContextManifestV2へ対応付ける。

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

子ごとにtask・model・build・構成snapshot・source bytes・messages・context・途中本文・終了recordを保存する。
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
それ以外は空配列。snapshot.toolsはmodel definition・JS registryと同じ選択結果を保存する。
run_jsは選択した場合だけbundledとして保存し、保存時にtool一覧を付け足したり上書きしたりしない。
source.fileは実config絶対locator、resource_revisionは実使用capture bytesを保持する。
未await委譲では、子join後・外側call終了前に残るtool／hook観測をterminalへ閉じ、開始記録だけを残さない。

## I-04：実接続の保存

DDL v3とselectionの既存fieldを維持する。provider・api・authProfile・modelId・effortへ実selectionを保存する。
provider定義とrole.modelは各executionの使用config source bytesに含み、独自snapshot fieldを増やさない。
providerState.replayItemsにResponsesのraw output itemを保存する。providerState.providerは、
送信先・API・credential参照・非秘密account identityを含むdigestによる状態のidentity。
同じ送信先・account・modelだけで再送し、別接続には可視会話とtool call／結果からinputを組み立てる。
context_item.provider_wire_bodyとmodel_request contentはadapter.Prepareの実送信bytesを共用する。
Authorizationはtransportのみに渡す。raw応答の保存を削除・書換えず、既存Session selectionを自動変換しない。
