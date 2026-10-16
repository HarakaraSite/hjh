# QuickJS stdlib利用とtool処理のJS化：移行計画案

2026-10-16。状態：S-1〜S-8実装・test・review完了。S-9総合確認を実施し、外部429等の留保を結果へ記録。
S-9の自動test・private実利用・package・TUI表示まで確認。指定opencode-go modelでlive継続最終回答・Exa・Web取得も成功。残る留保を結果へ明記。[実装記録](../results/quickjs-stdlib-tools.md)を参照。
利用者の「stdlib利用・tool処理のJS化を進めたい」という依頼を受け、
[検討メモ](../notes/quickjs-stdlib-tools.md)を実装段階へ具体化する。
run_jsへのstdlib公開と、stdlib I/Oを旧Hostと同粒度では記録しない方針は利用者が採用した。
call完了境界はA案（正常時にcall内でイベントループをdrain）を利用者が採用した。
現行Go実装との差異は相談してから採否を決める。
以下の未決事項・推奨案を承認済み契約として扱わない。

## 1. 必要な利用動作・根拠

人間がCLI/TUIから依頼し、modelがファイル・コマンド・Web・子agentを組み合わせて作業し、
結果を読んで同じSessionを継続できる。その基礎I/Oをhjh独自Go Hostではなく、
go-quickjsのstdlibと編集可能な外部JS toolで成立させる。

- read/write/edit、directory探索、検索、集計で必要なファイルを読み書きできる。
- commandで外部programを起動し、stdinを渡し、終了結果を作業に利用できる。
  grepはrg／GNU grep、git_inspectはGitを引き続き利用する。
- HTTP本文を読み、Webページを可読化し、原本文をfileへ保存できる。
  Exa検索はcredentialをJSへ渡さず利用できる。
- toolの直接呼出しとrun_js内の合成、tool前後hook、親子agentで結果を利用できる。
- 待機中の作業を既存の取消操作で停止でき、取消後に新しい作業を開始できる。
- 外側・内側tool、hook、子の結果・成否・親子関係を履歴やTUIで確認できる。

根拠は利用者の依頼、検討メモ、[初期製品方針](../design/initial-product.md)、
[拡張契約](../design/extensions.md)、現行sourceである。
I-17のengine移行で求めた旧API・戻り値・取消実装との完全互換を今回へ持ち込まない。
ただし、実際に使う操作を失っていないかは利用経路で確認し、変更した契約はhelp等へ反映する。
一般的hardening、permission matrix、新しい拒否・上限、旧形式converter／fallbackは加えない。
API credentialとAuthorizationをJS・tool結果・観測へ露出させない要件は維持する。

## 2. 現行経路と状態owner

```text
CLIローカル ──────────────────────────────┐
CLI --connect / TUI → HTTP Core → run Manager ─┤
                                           ↓
app.Service.Run → Session/selection解決 → 外部構成capture → execution
  → agent loop → provider tool call → Worker.Call
  → bootstrap dispatch → 外部ESM tool / run_js内tools.NAME
  → __host / __agent / __agentControl / __context
  → Go Host / credential接続 / hjh固有state owner
  → 結果 → modelの次request、journal → history → 会話表示・継続
```

| 責務 | 現行source・移行の接点 |
| --- | --- |
| 入口・execution | `internal/app/run.go`、`execution.go`。standalone／Core接続とも共通appへ入る |
| 構成とsource | `internal/extensions/extensions.go`のcaptureと`InstallDefaults`。編集可能な外部JSを捕捉。既存配置は上書きしない |
| VMとmodule state | `internal/jsruntime/worker.go`、`runtime.go`。Workerごとに一つ、owner goroutineだけがVMを操作。call間でstateを保持 |
| module loader | `runtime.go`の`loadExtensions`。捕捉済みESM sourceを返す。package download／node_modules解決はない |
| tool/hook dispatch | `bootstrap.js`。選択tool、help、before/afterTool、内側tool観測。標準toolの多くはGo APIへのwrapper |
| 基礎I/Oと結果加工 | `internal/host/host.go`、`edit_file.go`、`file_inspection.go`、`file_search.go`、`grep.go`、`git_inspect.go`、`internal/webfetch/` |
| 非同期処理 | `jsruntime/call.go`の`watchPromise`／`drainHost`、`host.go`のpending／completion queue。現在はstdlib Loopを回していない |
| 認証要求 | `internal/app/tool_requests.go`。credential ownerで解決しGo内でAuthorizationを注入。JSは参照と公開requestだけを渡す |
| 固有state | AgentRunner／AgentController、context／model、journal／history。stdlibだけでは置換できない |
| 保存・参照 | `app/journal.go` → historyのsemantic記録・本文参照・会話message。clientは保存stateやVMのownerではない |

source captureは実行のための保持であり、全source treeのDB archiveとは異なる。
現行appは構成source一括archiveを停止済みであり、今回それを復活させない。

## 3. 上流で確認した構成

使用versionは現行依存の **go-quickjs v0.25.2**。latestへ同時更新しない。
module cacheの同tag sourceと下記一次資料を確認した。実Workerへの統合実行は未確認。

- [stdlib契約](https://github.com/go-quickjs/go-quickjs/blob/v0.25.2/docs/stdlib.md)
- [Install / Config](https://github.com/go-quickjs/go-quickjs/blob/v0.25.2/stdlib/stdlib.go)
- [Loop](https://github.com/go-quickjs/go-quickjs/blob/v0.25.2/stdlib/loop.go)
- [fs](https://github.com/go-quickjs/go-quickjs/blob/v0.25.2/stdlib/fs.go)
- [child_process](https://github.com/go-quickjs/go-quickjs/blob/v0.25.2/stdlib/exec.go)
- [native module登録](https://github.com/go-quickjs/go-quickjs/blob/v0.25.2/extend.go)

確認事項：

1. `stdlib.Install`でfs、process、fetch、child_process、timer等を導入できる。
   I/O内部は上流Go実装を使う。純JSによるOS操作の再実装ではない。
2. stdlib moduleは`SetModule`で登録され、独自source loaderより先に解決される。
   捕捉済み外部ESMと`fs`／`node:fs`等を併用できる構成である。
3. `Loop.Post`は完了をVM ownerへ配送し、`Run`／`RunUntil`はtimer・microtaskも処理する。
   現行の`completeWake`待ちへInstallだけを追加しても統合は完了しない。
4. `Loop.Run(ctx)`の終了と`Loop.Context()`の取消は別。
   stdlibのHTTP/process寿命はLoopまたはRuntimeのcloseへ接続される。
   call contextの取消だけで全stdlib I/Oが停止したとは扱わない。
5. FSの空RootはOS filesystemを扱う。非空Rootは見えるpath自体をroot内へ変える。
   workspaceをそのままRootへ設定するのは、単なるcwd指定ではない。
6. child_processの`Allow`未指定は起動不可、Env未指定は空環境、出力上限既定は32 MB。
   `Process.Cwd`、`Run.Dir`、`Run.Env`も明示的に組み立てる必要がある。
   これらの既定をhjhの新しい製品制限とせず、通常作業の成立を確認して設定する。
7. fetchの`MaxBodyBytes`既定は32 MiBで、streamにも適用され、到達後は残りを落として終了する。
   原本文保存を切り詰めないため`Fetch.MaxBodyBytes: -1`を設定する。
   通常読取りの64 KiBはJS側で受信bytesに適用し、`save_to`は全体をstreamで保存する。
8. 外部package解決は別作業。付属qjsのloaderはhjhへ自動導入されない。

## 4. 提案する責務境界と切替

### Goへ残す接続とJSへ移す処理

- Go：VM生成・寿命、stdlibの設定、credential解決・認証注入、hjh固有stateへの接続、
  journal／DBへの保存接続。既存のSessionやexecution ownerを複製しない。
- stdlib：file、process、network、timer等の基礎I/O。
- 外部JS：toolの引数組立て、走査・置換・集計・ページング、検索結果解析、HTML可読化、
  Exaの要求／応答処理。認証済み要求だけGo接続へ渡す。
- bootstrap：tool／hook／helpのdispatchと呼出し関係の記録。
  hjh固有APIの上の方針までGoへ固定するものではないが、agent engineやDBのJS移植は別設計とする。

`api.readFile`等の旧基礎APIをstdlibの上に再建することを目標にしない。
外部toolは標準moduleをimportし、workspace等の実行metadataと固有state接続だけを別に受け取る案とする。
相対pathはexecution workspace基準、絶対pathはOSの絶対pathとして利用する案。
Core全体の`os.Chdir`は使わず、JS側でpathを解決し、processのcwdもWorker単位に設定する。

### 利用者が採用した方針

- **run_jsにもstdlibのtimer・fetch・組込みmoduleを公開する。** 外部tool専用にはしない。
  async関数本文なのでstatic importではなくdynamic importを実Workerで確認する。
  npm取得・CJS／node_modules解決を自動的に追加する意味ではない。
  選択toolは`tools`辞書の構成として維持し、stdlib I/Oの制限には転用しない。
- **tool／hook履歴は残し、個々のstdlib I/Oを旧Hostと同じ粒度では記録しない。**
  tool／hookの結果・成否・親子関係と、残る認証・agent等の必要な観測を保存する。
- **計画を複数の実装スライスへ分ける。現行Go実装との差異が出る部分は利用者へ相談する。**
  完全互換を必須にしないことは、差異を無断で採用してよいという意味ではない。

### 未await I/O・timerの寿命：A案採用

ここでcallは、一回の`Worker.Call`またはlifecycle hook呼出しを指す。
run_js内の`tools.NAME`は同じ外側callに属し、個別にVMやLoopを作る対象ではない。
Workerは一つのexecution内の複数callでVM／module stateを共有するが、Session再開時のVM永続化はない。

`return`でJS本文のPromiseが決着する時点と、開始した非同期処理が終わる時点は別である。
`await`しないfetch／file書込みも開始され得る。`setTimeout`は将来のcallbackを登録し、
`setInterval`はclearするまで繰り返す。単にPromiseを保持しているだけで、実I/Oやtimerがあるとは限らない。

例：以下の`return`は、file書込みやtimerのcallbackを待たない。

```js
const fs = await import("node:fs");
fs.promises.writeFile("/absolute/path/note.txt", "hello");
setTimeout(() => { globalThis.note = "later"; }, 1000);
return "started";
```

現在は`call.go`でJS本文の決着後も開始済みGo Host処理をdrainし、次callへ返す。
timerは未公開なので、timerの待機／持越しは新しい判断である。
stdlibの`Loop.Run`はI/O・jobs・timerがなくなるまで処理する一方、`RunUntil`は指定通知で戻る。
後者が戻るだけでは残るI/Oやtimerは消えない。Loop／RuntimeのcloseはWorker寿命にも関係し、
同じVMを次callで使う構成のままcall単位で任意のI/Oだけを止められると仮定しない。

| 選択肢（A採用、B/Cは比較用の非採用案） | 正常return後 | timer／次callへの影響 |
| --- | --- | --- |
| A. call内でイベントループをdrain | 開始済みの非同期処理やcallbackを進め、単発I/O・timer等がなくなってからcall結果を返す | timeoutの発火も待つ。intervalはJS側でclearしないとcallが完了せず、UI取消で止める。処理を次callへ持ち越さない |
| B. 本文決着で残りを止める | awaitしなかった処理は完了保証せず、残る処理の停止・終了を確認して返す | timerはcallを越えない。開始済み書込み等は取り消して元に戻るとは限らない。stdlibのLoop寿命との差から追加設計が必要 |
| C. Worker内でbackground継続 | 本文の結果を先に返し、残る処理を次callやmodel応答待ちの間にも進める | 前callの処理が後からfile／VM stateを変え得る。call間にもLoopを動かす設計と、遅いtool／hook結果・失敗の所属を決める必要がある |

**利用者はAを採用した。** 正常終了時は開始済みI/O・jobs・timer等をcall内で処理し、
それらがなくなってからcall結果を返す。次callへのbackground持越しは行わない。
intervalはJS側でclearしない限りcall完了を待ち続け、利用者はUI取消で停止できる。
現行の開始済みHost処理を待つ利用動作に近く、
stdlibのLoopが元々持つdrain方式を使えるため、独自のbackground実行管理を増やさずに始められる。
intervalを使うコードでは必要な作業をawaitした後、`finally`等で`clearInterval`する。
awaitしたtimerによる待機・pollingや`Promise.all`での並行処理は、いずれの案でも通常の利用対象である。
前案の「無期限timerをcall完了待ちの条件にしない」はA案採用で取り下げた。

これは採用した正常callの方針であり、取消・JS例外時に全処理の自然終了を待ち続ける方針ではない。
取消・例外時の実I/O停止、子の終了・保存、VM再利用可否はS-2で確認し、現行との差は相談する。
fetchはheaders取得とbody streamの読取りが別なので、Promise決着だけをdownload完了と数えない。
各toolは必要な本文読取り・file保存までawaitして結果を返す。

### 差異が見つかった場合の相談手順

各スライスで現行source・実行証拠と上流契約を照合し、動作差を次の形で提示する。

1. 現行Goでの動作と、新しいstdlib＋JSでの動作。
2. 利用者への影響と、根拠となるsource／実行結果。
3. 揃える場合・差異を採用する場合の実装案、推奨案。
4. 利用者の判断後、採用動作と確認方法を計画・helpへ反映する。

戻り値・path・glob・並び順・文字処理、commandの環境／出力／終了／取消、
HTTP・HTML・stream、call寿命や保存など、現行利用に影響する差異が対象。
内部file分割等だけの違いは逐一相談の対象にしない。既に採用済みの公開面・観測変更は再承認を求めない。
未合意の差異をそのまま製品仕様へ採用せず、その部分の切替を止めて相談する。
旧APIへのfallbackや、機能を狭める推測で回避しない。

外部設定の切替は新規配置と既存配置を分け、差分と更新対象を示して明示的に行う。
編集済みfileの自動上書き、旧APIへのfallback／dual-readは行わない。

## 5. 実装スライス

各スライスは、動作実装（対象toolのinstruction／help更新を含む）→focused test→独立コードreview→
採用finding修正・focused再確認→差異相談／反映→結果記録を一単位にする。
利用者がこの順序で各スライスを進め、最後に総合テストを行うことを指示した。
スライス完了と、全tool移行・配布完了を混同しない。S-1／S-2は基盤接続の順序であり、
採用した寿命・停止処理がまだ接続されていない中間実装を配布可能な製品と扱わない。

| スライス | 完成する利用動作・変更範囲 | 実経路での確認・置換後の整理 |
| --- | --- | --- |
| S-1. stdlib公開とLoop接続 | WorkerにInstall／Loopを接続。外部ESMとrun_jsからtimer、fs／path等の組込みmodule、fetch、child_processを使う | 実Workerでdynamic import、awaitしたtimer、実file／HTTP／processへの到達、同一VMの次callを確認。module loaderと公開説明を整合。公開開始時からcredentialをJSへ渡さない設定を確認 |
| S-2. call寿命・固有bridge・終了 | 採用済みA案のcall内drainを実装し、認証／agent／context bridgeとstdlib完了配送を同じVM owner上へ接続 | call間処理、取消・例外、子の終了・保存、hook、次Workerを確認。不要な独自pump／queueを整理。旧process group等との差があればここで相談 |
| S-3. read/write/edit | read_file／write_file／edit_fileをfs＋JSへ移す | 直接・run_js合成で生成→編集→範囲読戻し。実fileと結果・bytes・件数を確認。対応するHost dispatch／Go処理・旧API専用testを除去 |
| S-4. ls/find/wc | 探索・glob・除外・集計・ページングをfs／path＋JSへ移す | 実directoryで探索→file選択→集計／読戻し。path、順序、glob・文字処理で現行との差を確認・相談。対応するGo実装を除去 |
| S-5. command・PATH | child_process＋JSでcwd、stdin、終了結果を利用 | 実programのstdout／stderr／nonzero終了、環境、取消を確認。出力上限等の差は相談し、対応する独自起動APIを整理 |
| S-6. grep/git_inspect | 既存JSの引数組立てを活かし、起動・出力解析・ページングをJSへ移す | 実rg／GNU grepとGitで検索・履歴／差分のページを読む。使われなくなるGo parser／dispatchを除去 |
| S-7. http/web_fetch | fetch／stream／fs＋JSで本文読取り・HTML可読化・原本文保存 | 実HTTPで通常64 KiB読取りと全体保存を確認。fetchの`MaxBodyBytes: -1`とJS読取り上限を分離。HTML・文字コード・HTTP動作差は相談。置換後`internal/webfetch`等の全参照を追い不要経路を除去 |
| S-8. web_search・認証接続 | Exa要求／応答処理をJSへ移し、credential解決・Authorization注入をGo接続へ分離 | 認証付き検索結果を実経路で利用。credentialがJS・結果・観測へ出ないことを確認。不要な検索専用Go処理を除去 |
| S-9. 実利用・外部設定切替・配布 | 新sourceを使うprivate Core／CLI／TUIで複合作業、履歴閲覧、Session継続、最終packageを確認 | 配置sourceとbinaryを識別。更新手順・help・利用方針・拡張契約を整合。旧基礎Hostの全参照と使われない保存処理を整理し、残るGo接続を明記 |

寿命方針はA案で確定済み。S-1→S-2の順に接続し、S-3以降へ広げる前に基盤の実経路を確認する。
移行途中で未移行tool用Hostが残ることと、切替済みtoolへ互換fallbackを持つことは区別する。
旧DB・利用者file・稼働Coreは削除・変換・停止・置換しない。
## 6. 確認方法・文書の更新対象

testは上表の動作を実装した後、その実Worker経路を確認する最小限を加える。
既存`internal/jsruntime/worker_test.go`、`model_call_test.go`、appのdelegation／tool help確認を参照する。
旧Go APIを直接呼ぶfixtureや旧effect観測数を、新実装の必須仕様へ転用しない。
変更された具体的な利用動作の確認を優先し、全variant／permission matrixは作らない。

実装中はfocused test、必要なtype／format／lintと`git diff --check`を使う。
reviewは公開面・実経路・保存・置換後の不要処理を確認し、full gateを前提にしない。
S-9の安定候補でcoordinating ownerが総合テストを一回実行する。
自動確認は`go test ./...`と関連packageのrace確認
（`go test -race ./internal/jsruntime ./internal/extensions ./internal/host ./internal/app ./internal/corerun ./internal/httpapi ./cmd/hjh`）を行う。
加えてprivate Core／CLI／TUI・実modelの複合作業、履歴・Session継続、最終packageの実経路を確認する。
途中に全体gateを反復しない。失敗時はfocused確認で原因を特定し、再実行理由を記録する。
実modelや実端末で確認できなければ、その動作を未確認と記録し、fixture成功で代替しない。

更新対象は、実装に応じて以下を追う。

- 各スライスで対象toolの`description`、`promptGuidelines`、input schemaとhelpを実装に合わせて点検・修正する。
  対象は`internal/extensions/assets/config.json`、対応する`*-help.md`、
  run_js定義・常駐説明（`internal/extensions/extensions.go`）、共通`tool_usage_policy.md`。
  S-1／S-2では「timerなし」「selected toolsだけ」の説明をstdlib公開・A案へ合わせ、
  S-3〜S-8では各toolの利用例・引数・結果・取消等を採用した実動作へ合わせる。
  変更不要の説明はその判断を結果へ記録し、不必要な文面変更を目的化しない。
  直接helpとrun_js内`tools.NAME({help:true})`がcaptureされた同じ説明を返す経路も確認する。
  編集済み外部設定へは自動上書きせず、S-9で明示的更新手順を示す。
  process取消の説明は利用者が次の分担を採用した。run_js helpには、`child_process`の取消は
  直接起動したprocessのみであることと、詳細はcommand helpを読む旨を短く記載する。
  command helpには、起動したprogramがさらに子processを起動した場合の具体例と、
  残るprocessを確認して必要に応じcommandで明示的にkillする手順を記載する。
  `tools.command`と生の`child_process`の両方が対象であり、timer・fetch・子agentの説明とは分ける。
- `docs/design/extensions.md`、`initial-product.md`、tool利用方針・外部設定更新文書。
- `docs/results/quickjs-stdlib-tools.md`：実装後の実利用証拠、採用した結果形・寿命・観測、残る未確認事項。
- `docs/implementation-status.md`：計画作成と実装済みを区別して更新。

速度・メモリ・binary size・Goコード削減量は移行後に測定し、機能成立の代用にしない。
CGOなしLinux/amd64配布と外部program依存を区別し、最終packageで使うmoduleとlicenseを確認する。

## 計画案のreview

sourceと検討メモに対する独立reviewを実施。fetch既定32 MiBが全体保存を切り詰める指摘を採用し、
固定tag sourceと現行Web契約を確認した上で、第3節・Web移行スライスへ設定と役割分担を追記した。
その他の必須修正指摘はなかった。これは計画の確認であり、移行後の実行確認ではない。

## 7. 着手前に確認した具体的差異

利用者の実装指示後、S-1の公開設定を現行Goと固定tag sourceで照合した。
command出力上限とstdlibの直接子だけの取消は利用者が許容した。生fs相対pathの扱いも採用済みであり、
公開を伴うproduction切替はまだ行っていない。
stdlibの修正にはfork等の依存管理が必要で、上流追随の負担が増すことを説明した。
依存sourceの変更を無断で行わない。

| 相談対象 | 現行Goとstdlibの実装根拠・利用者影響 | 選択肢 |
| --- | --- | --- |
| command出力上限（採用済み） | `internal/host/host.go`のcommandは明示上限なし。v0.25.2 `stdlib/exec.go`の`runner.start`／`cappedBuffer.Write`はstdout／stderrそれぞれ既定32 MiBで、それ以降を通知なく破棄する。負値は無制限ではなく全出力破棄になる | 利用者がstdlib既定上限を許容。stdout／stderrそれぞれ32 MiBを採用してinstruction／helpへ明記する。このためのfork・独自patchは作らない |
| command取消範囲（採用済み） | 現行はLinux process groupを停止してWait。stdlibの`runner.start`は直接の子processのみKillするため、shell等が起動した子孫が残る可能性がある | stdlibの取消範囲を許容する。必要な場合はagent／利用者が残るprocessを確認し、command経由で明示的にkillする。自動的な子孫停止・cleanup、fork・独自patchは追加しない。instruction／helpへこの違いと対処を記載する |
| 生fsの相対path（採用済み） | 現行Hostはworkspace基準。stdlib `fsHost.resolve`は空RootでpathをそのままOSへ渡し、Core cwd基準になる。`Process.Cwd`を設定してもfsは変わらない。非空Rootは絶対pathとworkspace外参照まで変える | 標準toolはJSでworkspace絶対pathへ解決し、生fs利用時は`path.resolve(process.cwd(), relativePath)`等で絶対pathを渡す案を採用。生fs自体の相対pathはCore cwd基準のまま。stdlibに独自patchを追加しない |

HTTPは`Fetch.Client`へ既存の非認証HTTP client（未指定なら`http.DefaultClient`）を渡せるため、
stdlib既定の一律30秒へ変更する必要はない。web_fetchの既存30秒はtool側で扱う。
認証注入用transportを一般fetchへ渡さず、API credentialを環境経由でもJSへ渡さない。
`Run.Env`／`Process.Env`は空環境を無断採用せず、通常作業に必要な環境とcredential除外を明示設定する。
新たな設定だけで現行動作を保てる箇所と、上表のように利用者判断が必要な差異を区別する。

### S-1着手後の相談：未捕捉JS例外後のVM

実装・固定tag probeで、例外後のtimer／I/Oだけを止めて同じVMを再利用するAPIがないことを確認した。
Loop.Closeは恒久的であり、moduleを再登録しても既存importは閉じたLoopを参照する。
そのまま継続するとcallback持越し、drainすると未clear intervalの待ち続けが起きる。
現行は通常のtool error後もmodelが次callで立て直し、hook例外後も終了hookを実行する。
詳細・source-to-impact・実行結果は[実装記録](../results/quickjs-stdlib-tools.md)に記載。
利用者は、未捕捉の外側JS例外でVMを破棄し、次Call／Hookで同じcaptureから新VMを作る案を採用した。
会話・履歴は保持し、global／module stateはリセット、module初期化は再実行する。失敗callを自動再実行しない。
JS内でcatchして正常終了した場合はVMを保持しA案でdrainする。取消は従来どおりWorkerを終了する。
この採用に従いInstallをpublic Newへ接続し、instruction／helpを同時更新する。

### 追加相談：失敗／取消時の非同期fs操作

固定tag sourceとFIFO probeで、stdlibの非同期fsはLoop／VM close後も開始済みOS操作をjoinせず、
後から完了する場合があることを確認した。正常A案はdrainするためこの差はない。
例外後のJS callbackは破棄するが、OS操作は次callと重なる場合がある。
旧HostはOS read/write自体を中断せず、開始済み処理をjoinしていた。
利用者はこの上流挙動を許容しhelpに明記する方針を採用した。
独自join・同期fs化・forkは追加しない。標準toolは必要なfile操作をawaitする。
根拠と実測は[実装記録](../results/quickjs-stdlib-tools.md)を参照。

### 次の一手

生成JS公開面・内部I/O観測・call寿命A案・command出力上限・直接子だけの取消は採用済み。
S-1準備部分のfocused確認・コードreviewは済み。例外後VM再生成と生fs絶対path案の採用に従い、
public stdlib公開・例外後の再生成・instruction／help更新は接続済み。S-1／S-2のfocused race確認と
独立reviewの3件修正・限定再reviewも完了。S-3のfile tool JS化・実例修正・reviewも完了。
S-4〜S-8のJS化・focused確認・独立reviewも完了。S-9総合確認も実施済み。
初回live継続429は保存し、指定opencode-go modelで最終回答・Exa／Web取得を追加確認して解消。
残る留保は既存dist/tmp断片の全体build error、利用者Coreへの未反映と物理端末目視等。結果文書を参照する。
各スライスで実装・instruction／help更新・test・コードreviewを行い、S-9で総合テストを実施する。
外部設定の切替は、実際の更新対象を提示してから行う。
