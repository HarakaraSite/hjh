# I-03：親子共通generic・外部roleの実装と確認

2026-10-10。[計画](../plans/i03-generic-roles.md)に沿って実装した。
状態：実装・focused test・実provider・PTY・Linux配布確認・通常／批判的reviewと指摘修正を完了。
最終修正の確認はcoordinating ownerが行った。利用者自身によるI-03の操作確認はまだ行っていない。

## 実装した動作

built-in genericの共通Go loopへ、外部configのrole instructionと選択toolを渡す。
同じroleを親／子のどちらにも使える。既定roleはdefaultとreviewer。
rootは明示role、保存Sessionのrole、config.defaultRoleの順、子は明示role、開始時のdefaultRoleの順で解決する。
子は親の絶対config locatorを再captureし、実model／effort・workspace・step設定を継承する。

run_jsも選択対象のbuilt-in。run_agentは外部toolで、生成JSのapi.agents.runは選択したrun_agent／hookへ接続する。
model definition・JS registry・共通instruction・保存snapshotは同じ解決結果を使う。
生成JSの公開apiは選択toolを使い、外部tool／hookは実装用の基礎APIを使う。
roleでtool内部の操作やfile権限を規定せず、JS／OS sandboxを追加していない。

CLIは--roleとDB・認証なしのroles、TUIは/roles・/role [NAME]と実role／次の選択を表示する。
進行中の選択変更は次runへ適用し、子進捗で親のrole／model／step／本文を更新しない。
成功rootの会話・model・roleを同じtransactionで採用する。失敗・取消・子はSession choiceを変更しない。
DDL v3と保存payloadのfield構造を維持し、実roleはagent_nameとsnapshot.agentへ保存する。

既存config・DBは上書き／削除／自動変換しない。入力config.agentは廃止した。
既存workspaceの[手動更新手順](../../README.md#role定義と旧configの手動更新)をREADMEへ記載した。

## 責務とfocused確認

| 所有者 | 変更した責務 |
| --- | --- |
| extensions | defaultRole／roles、capture bytesからのrole解決、tool構成、catalog、instruction、snapshot、既定外部run_agent |
| jsruntime | 選択toolの実登録、生成JS／実装用API、委譲aliasとhook、ownerによる委譲観測の終了 |
| app／history | root／子のrole選択、共通実行、role進捗、成功rootの同時採用、共通catalog操作 |
| CLI／TUI | 構造化RunRequest、role選択・一覧・実行中／次回表示、親子inspection |

Focused確認は、計画の製品動作と既知regressionに対応させた。

- extensions：role解決・catalog、選択toolとdefinition／snapshot、run_jsあり／なし・空tools、使用bytes。
- appの実Go loop・QuickJS・SQLite：rootの保存role・明示選択・現在configでの再開、失敗／取消の非更新、
  指定config編集後のreviewer子のinstruction／tool source、実model／effort継承、並行結果・親子relation。
- runtime：生成JSに選択toolだけを公開、実装APIは基礎操作と選択toolを保持、alias／hookとrole伝達、
  未awaitの正常・例外・取消で子joinとwrapper／active hookの終端、最後のhost完了後のJS継続処理。
  子開始前に止まるbeforeToolと子終了後に止まるafterToolもownerが閉じ、直接run_agentの成功結果を維持する。
- appの未await終了：beforeTool内の委譲を残す実VM／SQLite経路で、次model request前に子・tool・hookを終了。
  元JS例外と子終了保存errorを併せて保持する既知regressionも確認した。
- UI：DB・認証なしのroles、親role選択、進行中の次role変更、子表示と親本文・選択状態の分離。

go test ./internal/app ./cmd/hjhと、app／jsruntime／tui／cmd/hjhのrace確認は成功。
extensionsのtest／vet、historyの採用・子・DDL同一性確認も成功。
review修正後はruntimeの直接委譲・beforeTool／afterTool・未await regressionと、
appのrole・委譲・Service・実行保存のfocused raceを再確認して成功した。
変更packageのvetとformatを確認した。repository全体gateは反復していない。

## 最終候補の実provider確認

OpenRouter deepseek/deepseek-v4.1-flashを、既存credential参照経路から製品transportで使った。
専用workspace／configを使用し、自然文の依頼から実モデルがrole・toolを選んだ。
修正後の記録はgit管理外のdist/i03-live-accepted/へ保存した。
初回候補と中間候補の記録もdist/i03-live/・dist/i03-live-final/に保持した。

Session：a1653ba6-3c75-4fa6-a166-bb8bcad36591。

| 操作 | 実結果 |
| --- | --- |
| run --stream | default親54efe28b-71c1-44c7-bd35-eeca4ff299b7がreviewer二人へ並行委譲。input.txt・usage.txtを子が読み、親がreport.txtを作成・読戻し。親だけcanonical。最初のstdoutは10.655秒、終了11.739秒、exit 0 |
| 同Sessionで--role reviewer | reviewerを親としてreport.txtを実レビューし、Session roleをreviewerへ採用。exit 0 |
| 明示roleなしで別run再開 | 保存reviewerでinput.txtとの照合を完了。snapshot.toolsはrun_js・read_fileだけ。exit 0 |
| run_jsなしの直接委譲 | 親97558b03-0940-4627-ab35-c42eb9f45977のtoolsをrun_agent・read_file・write_fileだけに設定。reviewer子がinput.txtの73を読み、親がdirect-report.txtを作成・読戻し。model_toolとruntime toolの両方でrun_agentのcompleted終端と実子結果を保存。exit 0 |
| CLI SIGINT | default親6ed82d41-9200-4102-85dc-a9121726132f→runner子のcommand開始後に停止。親子cancelled／settled・非採用、子を先に保存、停止0.096秒・exit 130。Session roleはreviewerのまま、shell消滅・late fileなし |

120×40のPTYでは、/roles・/role、default親c1d13cd5-f92f-455b-b1d8-eb1bab5ea978→reviewer子の結果利用、
/executions、親子の/inspectを操作した。
runner子のcommand開始後に/role reviewerを入力し、画面上でcurrent default／next reviewerを確認した。
Escで親子を停止し、次runはreviewerで実ファイルを読んで完了した。
別のdefault親→runner子をCtrl+Cで停止・joinし、exit 0とalternate screen復元を確認した。
停止・終了でSession roleは変更されず、成功reviewerのchoiceを維持した。
PTY decoderの日本語描画には再現の限界があり、画面品質の利用者受入とは区別する。

Linux静的packageから、同梱configのmodelでdefault親139d826e-722a-4416-b16c-a287f5bd8f39→
reviewer子af4f72b5-8782-4684-848e-28c84ca7e56fの実作業と履歴閲覧を完了した。
子はcompleted／non_canonical、親だけcanonical。
manifestの全142 file、build ID、親子のconfig source digestを照合した。
archiveはdist/packages/build-6h5c6p2w/hjh-linux-amd64.tar.gz。
archive SHA-256は604976c565bc578043edbd658d03903ba7d22cd18b155bf029633d2cee0949e1、
静的binary SHA-256は6075cd9f3faecc7ac35914c2d7456f4e82de78ec7b1c1db61f6b22da9c8b3812。
確認scriptの子数・履歴DTO／record種別の誤りはscript側で修正し、取得済みの実行記録を再照合した。
製品動作を変更せず、この再照合によるproviderの再呼出しは行っていない。
最終候補の213確認artifactにcredential値が残っていないことも確認した。
CLI／PTYに使った開発binaryのSHA-256は
e37419ef73f0adc9ae840d8327b35025b7d04d93bb8c801d28b9efbd18686085。

## 実装review

独立した通常reviewは必須findingなし。設定・role選択・保存・Session採用・CLI／TUIを静的確認した。
通常reviewの文書注意に合わせ、設定例と手動更新手順のrun_agent moduleを既定run-agent.jsへ揃えた。

批判的reviewはruntime・委譲・hook・終了記録・保存errorの経路を静的確認した。

1. 初回P2：run_agentのbeforeToolでhost操作をawaitしたまま親JSが正常終了すると、最後のhost解決後の
   JS継続が同call内で処理されず、委譲前のwrapper／hook記録が残り、次callの子scopeへ持ち越され得た。
   最後のhost完了後もJS jobsを処理し、active run_agentをowner closureの起点に含めて修正した。
   子開始前のbeforeToolと子終了後のafterToolが未settledの場合も、親結果と子の実結果を保って閉じる。
2. 一回の変更箇所re-reviewで初回P2の解消を確認したが、修正によるP2を検出：直接model tool callの
   run_agent自身もowner closureでcancelledになり、成功履歴を誤記録していた。
   coordinating ownerが外側callを終端対象から除外し、子孫のscope起点には保持して修正した。
   実VMのTestDirectRunAgentRecordsActualCompletedResult、関連focused race、実providerの
   run_jsなし直接委譲で、completedと実子結果の保存を確認した。追加の独立re-reviewは行っていない。

採用したP2二件は修正・確認済みで、未対応の必須指摘はない。
実装reviewerはtest・API・full gateを実行していない。

コンテナ／OS VM、roleごとのmodel、追加tool filter、permission、変換、他OS／provider、公開は対象外。
