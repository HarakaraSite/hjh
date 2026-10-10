# I-02：子Agentへの委譲

2026-10-10。利用者はI-01の基盤プロトタイプを実際に操作して了承し、以後の作業単位を
インクリメントとした。次のI-02は子Agentへの委譲とする。
通常review・批判的reviewで採用した計画補完5件を反映して実装した。
状態は**実装・focused test・実provider確認・実装review完了**。
利用者も子Agent呼出しを確認し、次のインクリメントへの進行を指定した。[結果](../results/i02-child-agents.md)を参照する。

## 必要な動作と根拠

親Agentが生成JSから調査を子Agentへ委譲し、子が実ファイルを調査する。
親は返された結果を次の判断に使い、報告ファイルと最終回答を完成させる。
途中の親子の進捗を確認でき、停止と別起動後の履歴閲覧も同じ製品経路で使えることを完成条件とする。

利用者の次インクリメント計画・review依頼と、[採用方針](../design/initial-product.md)に基づく。
Linux、OpenRouter、Go Agent loop、Workerごとの独立QuickJS VM、外部JS tool／hook、
[旧v3のDDL](../design/history-schema.md)を維持する。
旧Henjiの子Agent実装は参考資料であり、旧Core・queue等の採用を意味しない。

I-01は非対話run・stream・履歴・再開・最初のTUIまでの基盤プロトタイプ。
[初期run](../results/initial-run.md)、[stream](../results/run-stream.md)、
[TUI](../results/first-tui.md)の実行記録を既存動作の根拠とする。
I-02の実モデル・並行実行・UI終了時の動作は[実行記録](../results/i02-child-agents.md)で確認した。

## 現行経路と変更の境界

現行経路は `CLI／TUI → app.Service → app.Run → agent.Run／Provider／JS Worker → history`。

- `app.Run` が設定capture、model解決、Session開始・再開、execution admission、journal、
  Worker生成、Agent loop、終了保存、成功時の会話採用を接続する。
- `agent.Run` はSQL・VMを所有せず、モデルとtoolの会話を進める。子にも同じloopを使う。
- `jsruntime` はVM owner、Promise、call contextとGo jobを所有する。Go側からVM値を操作しない。
- `history` はDDL、保存・参照、正常turnの会話採用を所有する。
- CLIのstream状態とTUIの途中本文は単一executionを前提としている。

現行`app.Run`をそのまま子から呼ぶと、子までSessionへ自動採用される。
共通実行処理を取り出し、rootのSession操作・採用と、子の開始・結果返却・停止待ちを分ける。
第二のAgent loop、子用の独自DB、UIからの直接SQLは作らない。

## JS操作契約とモデルへの提示

生成JSと外部tool／hookの`api`から、Promiseを返す `api.agents.run({task})` を呼べるようにする。

```js
const child = await api.agents.run({
  task: "srcを調査して、主要モジュールと責務を報告してください"
});
return child;
```

```js
const children = await Promise.all([
  api.agents.run({task: "srcを調査して主要モジュールを報告してください"}),
  api.agents.run({task: "docsを調査して利用手順を報告してください"})
]);
return children;
```

- 入力は`task`文字列。親が必要な文脈もtaskに明記する。
  親Sessionの会話やprovider継続情報を子へ自動コピーしない。
- 戻り値は`executionId`、`outcome`、任意の`finalText`・`error`。
  `outcome`は保存したexecutionの終了状態に対応させ、Agent内部の`final`等と混同しない。
- 子の実作業が失敗しても、終了証拠の保存まで成功した場合は構造化した失敗結果を返す。
  親はその結果を判断に使える。正常／失敗いずれも子Worker終了・終了保存後にPromiseを解決する。
- 取消はPromiseのrejectionと保存した`cancelled`状態で扱う。
  admission前の設定準備等の失敗は呼出し失敗として記録し、存在しない子execution IDを返さない。
- 履歴保存の失敗は下記の共通Go終了契約に従い、通常の子の作業失敗と区別する。

実モデルがこのAPIを選べるように、共通実行instructionとbuilt-in `run_js`のdescriptionへ
入力・結果・`await`・`Promise.all`の使い方を追加する。
実際のinstructionとtool contractを構成snapshot・モデル入力へ保存し、表示上の説明だけで済ませない。
既存の外部configを自動書換えして新toolを登録する必要はない。

## 子の入力・構成・実行所有者

子は独立した会話・Agent loop・QuickJS VM・journal・モデルstepのカウンターを持つ。
workspace、親の実選択model／effort、`MaxSteps`を引き継ぐ。
step上限は各executionに適用し、既存の0＝無制限を維持する。新しい総量上限を設けない。

親で解決した絶対config locatorを共通実行状態へ保持し、子へ渡す。
`--config`指定をworkspace既定へ戻さず、同じ場所を子開始時に再captureする。
子は新しいcaptureのAgent instruction・tool・hook・依存bytesを使い、その構成を保存する。
model／effortは親の実選択値を使うため、子開始時のconfigのmodelで再選択しない。
親Workerは開始時のcaptureを保持し、子のcaptureや編集によって変化しない。

共通Go実行側がexecution scopeを所有する。root scopeはroot Session ID、config locator、
実model等を持ち、子scopeには直近の親execution IDと委譲呼出しの参照も渡す。
JS runtimeへはcontextと通常のGo値を扱う小さな委譲interfaceを注入する。
file／HTTP／commandを担当する`host.Host`へAgent loopや履歴を移さない。

## 子admissionと保存・閲覧契約

採用DDLは変更せず、子開始のhistory操作をroot開始と区別する。
子の保存fieldを次のように定義する。

| field | 子executionでの意味 |
| --- | --- |
| `canonical_session_id` | `NULL`。Sessionへの直接採用対象ではない |
| `session_correlation` | root Session ID。既存Session一覧の中で親子を閲覧するための相関 |
| `parent_execution_id` | 直近の親execution ID |
| `spawn_call_id` | 個々の`api.agents.run`呼出しのID。外側`run_js`一つのIDで複数の子をまとめない |
| `turn_number` | 親のturn番号。root Session内でその委譲が所属するturn |
| `base_revision`・`base_message_count` | 0。子は空の独立会話から開始し、rootの採用会話を基準にしない |
| `configuration_id`・`model_json`・`build_json`・`max_steps` | 子が実際に使う構成・継承した実model・実Go build・step上限 |
| `adoption` | 完了しても`non_canonical` |

子開始はSessionを新規作成せず、rootのturn・revision・message数を変更しない。
子は専用VMを開始する前にadmissionし、module初期化等の開始後の失敗も終了証拠を残す。
各executionのmessage、resource、モデル入力・途中本文、意味記録、終了記録は自executionへ保存する。
子自身の通常loopは旧payloadの`parent` laneを使い、親子識別のために`child` laneを新設しない。

親の委譲開始recordから子のadmission、親の委譲結果recordから子のterminal recordへ、
明示的な跨execution relationを作る。journal内のcall ID mapだけで自動生成できると仮定しない。
保存済みrecord参照は共通実行側の公開操作で渡し、lockを保持したまま子を待たない。
外側のモデルtool結果は従来通り親messagesへ保存し、子の内部記録で置き換えない。

historyのExecution DTOと読込みへ親execution ID・spawn call IDを公開する。
Sessionのexecution一覧に子も含め、TUIの`/executions`で親子関係を表示する。
`Service.Inspect`には直下の子一覧を追加し、CLIの`inspect --execution 親ID`とTUIの`/inspect 親ID`から
子IDを知り、既存inspectで子の本文・tool・構成・contextを閲覧できるようにする。
子一覧はexecution全体の情報であり、record／messageのordinal範囲指定で隠さない。
rootの採用会話を再開するときは、親messagesの委譲結果を使い、子messages全体を混ぜない。

## JS終了・取消・保存失敗の順序

子の寿命は子を発行した`Worker.Call`に所属する。
VM ownerは委譲Go jobを開始するときにそのcallへ登録し、JSが`await`しなかった子も追跡する。

1. root JS Promiseの正常／例外settlement、またはcall取消を観測する。
2. そのcallに残る子をcancelする。通常の`await`や`Promise.all`で完了した子の結果は保持する。
3. 各子とその配下の処理を終了させ、VM・model通信・commandの停止と終了保存を待つ。
4. 委譲Go jobをjoinし、VM ownerでcompletionと終了観測を処理する。
5. 外側toolの終了を記録し、`Worker.Call`を返す。その後に親の次model stepへ進む。

現行`awaitCall`はJS正常settlement後もpending操作をdrainするため、
子をそのまま同じ待機に入れるだけでは上記2へ到達できない。
子scopeの取消・joinと既存host操作のdrainを区別し、未awaitの子を正常JS終了時に取り消せるようにする。
元のJS例外を子の後処理errorで隠さない。

親の取消は子のcontextへ伝え、全子の停止・保存・join後に親をsettleする。
TUI終了時も同じ順序で、表示先が閉じてもjoinを妨げない進捗転送を使い、DBはその後に閉じる。
DB transaction、journal lock、表示直列化lockを保持したまま子の終了を待たない。

admission・観測・終了保存のerrorは、子の作業結果と別に共通Go実行側へ保持・伝播する。
Promiseをrejectするだけでは、JSのcatchや既存loopのtool error処理で親が続行できるため不十分である。
共通実行側はそのerrorを次の実行処理と親の終了判断へ伝え、残る子を取消・joinする。
親の結果を成功とせず終了errorを返し、正常会話へ採用しない。
利用者の取消中に保存errorが発生した場合も、そのerrorを終了報告から落とさない。
保存できなかった証拠を保存済みと偽らず、既に保存したrecordや実ファイルは保持する。

## 進捗と表示

子の観測は子journalへ保存した後、rootと共通の表示先へ転送する。
表示イベントにexecution ID・親execution IDを付け、call IDやmodel stepだけで親子を識別しない。
子観測を親journalへ投入しない。表示の直列化は各journalの保存状態から分離する。

- CLIのstdout本文は親のstreamを維持し、子の開始・進行・終了とIDをstderrへ表示する。
  子のmodel requestで親の本文差分位置やroot IDを更新しない。
- TUIは親本文と識別付きの子進捗を表示する。子本文の詳細はinspectから確認できる。
  子のmodel stepが親の途中本文entryやmodel表示を上書きしない。
- TUIの表示state更新は既存のevent loopに集める。
  観測が届く間の停止・終了・再依頼も同じ共通Go操作へ接続する。

## 実装順序と担当境界

| 段階 | 責務・主な変更先 | 成果 |
| --- | --- | --- |
| 1 | app実行境界、contracts、historyの開始・読込み | root／子のpolicyを分離し、DDLを変えず子admission・参照を使える |
| 2 | jsruntimeの注入操作・bootstrap・call scope、appの委譲処理 | 生成JSから子を待ち、正常／例外／取消で子を終了保存・joinできる |
| 3 | appの構成・journal・context接続、extensions契約 | 親で解決したconfigの再capture、実model継承、モデルへのAPI提示、親子relationを接続する |
| 4 | app.Service、CLI表示、TUI表示・閲覧 | 保存後の親子進捗を区別し、親inspectから子へ辿れる |
| 5 | focused確認、実provider・PTY確認、review、Linux配布物 | 人間の利用経路を確認し、結果・未確認範囲を記録する |

この表は責務の境界であり、先にfile数を決める分割ではない。
common executionとdelegationを必要な範囲で分け、既存run全体の無関係な再構成はしない。
実装時に[拡張API](../design/extensions.md)、[操作契約](../design/run-api.md)、
[保存payload](../design/history-payloads.md)、[TUI設計](../design/tui.md)へ実装した契約を反映する。

## 動作確認・review・完了条件

機能を実装してから、次の具体的な製品動作に対応するfocused確認を行う。

| 製品動作 | 確認方法・証拠 |
| --- | --- |
| 子の結果を親が使う | 実OpenRouterが生成したJSでファイル調査を委譲し、親が報告ファイルと最終回答を完成させる。実ファイルと親子履歴を照合する |
| 複数の子が混ざらない | 別々の調査を`Promise.all`で委譲し、結果・進捗・親子relation・構成が各executionに対応することを確認する |
| 子の作業失敗を親が使う | 外部JS例外で失敗した子の証拠と構造化結果を確認し、親がそれを使って次の判断を行う |
| JS終了時に子が残らない | 子の開始を確認した後、未awaitのままJSが正常終了する場合と例外になる場合に、子の取消・保存・joinが外側call返却より先になることを確認する |
| 保存失敗が成功へ変換されない | 子の履歴保存errorをfocused確認で制御し、JSのcatchを通っても親の共通終了errorと非採用へ届くことを確認する。filesystem／permission matrixは追加しない |
| 親の停止・TUI終了で子も終了する | 子の通信／command待機中にCLI停止・TUI停止／終了を行い、子の終了保存とjoinを親終了・DB closeより先に確認する |
| 選んだconfigを子も使う | 既定と異なる指定configで実行し、親開始後のsource編集が子の次captureへ反映され、親・子の保存bytesと実使用が一致することを確認する |
| 再起動後も親子を辿れる | 別起動で親inspect・子inspectを読み、採用会話へ子messagesが直接混ざらず親Sessionを再開できることを確認する |
| 既存の利用経路を維持する | 委譲しないrun・stream・TUIと、親子イベントが親の本文を上書きしない表示をfocused確認する |

実確認は利用者指定の `deepseek/deepseek-v4.1-flash`、既存のcredential参照経路を使う。
API key／AuthorizationをJS・設定snapshot・進捗・履歴へ渡さない既存境界を維持する。
offline fixtureや固定JSだけを、実モデルが委譲を生成して結果を使う確認の代替にしない。
安定binaryの`run --stream`とPTYのTUIで進捗・停止・履歴閲覧を確認し、Linux配布物からも実経路を実行する。

変更箇所のfocused test、並行部分のrace確認、format・必要なlint・差分の空白確認を行う。
reviewerはfull gateを実行しない。親子の寿命・会話採用・進捗転送を安定候補で独立reviewし、
採用した指摘を修正する。通常reviewは30分、re-reviewは変更箇所に限定して一回15分を上限とする。
新しい証拠・tool結果・中間結論が10分なければ中断し、確認済み・未確認範囲を返す。

実装・実利用証拠・review結果は `docs/results/i02-child-agents.md` へ記録する。
完了条件は実モデルによる委譲・結果利用・停止・親子履歴閲覧が成立し、利用者がCLI／TUIから試せること。
実利用が未確認の状態をtestやreviewの成功だけで完了にしない。

## 計画reviewの採用結果

通常reviewはDDL・payload・拡張契約とapp／runtime／history／CLI／TUIの関連sourceを、
批判的reviewはPromise待機・取消・保存・採用の境界を読取りで確認した。
両reviewとも方向性は成立すると判断し、次のP2計画補完5件を採用した。
現行製品bugという指摘ではなく、実装・test・API確認はこのreviewで行っていない。

| review | 採用した補完 | この計画の反映先 |
| --- | --- | --- |
| 通常 | 子admissionから履歴閲覧までの契約 | 保存field、親子relation、DTO、TUI一覧、親inspectの子一覧 |
| 通常 | 子がcaptureするconfig locator | 親で解決した場所の継承・再capture、実model選択との分離 |
| 通常 | 実モデルへの委譲API提示 | instruction・run_js説明・保存構成への反映 |
| 批判的 | JS settlement後の子取消・join境界 | call単位の子scope、host drainとの区別、次step前の終了順序 |
| 批判的 | 作業失敗と保存失敗の区別 | Go側のerror保持、親の終了error・結果・会話採用への反映 |

更新版計画を基に実装を行い、実装sourceに対する通常review・批判的reviewを実施した。
採用した実装findingの修正と一回のre-review、実経路の確認は[結果](../results/i02-child-agents.md)に記録した。

## 対象外

専用Agent定義の選択、子ごとのmodel指定、detach・queue、複数provider、TUI全体の改修、
旧DB移行、他OS、release公開はI-02の対象外。
旧Henjiの再帰委譲拒否、permission、任意の並列数・深さの上限を参考資料から引き継がない。
