# モデル応答待ちタイムアウトの調査と対応計画

2026-10-15。status: **実装・focused/実Core検証・コードreview・全体test完了**。
依頼: [provider-response-timeout-task.md](../notes/provider-response-timeout-task.md)。
調査sourceはHEAD `0fd3c1a`と通常履歴schema v4。先行するTUI notice/steering対応は
`cdac566`、`0fd3c1a`で反映済みであることをlogで確認した。
調査・提案後、利用者は「実装テストコードレビューして」と指示し、本案の実装・確認・reviewを承認した。
当初の承認は実装・確認まで。後続の「コミットプッシュ配置して」によりcommit/pushとdistバイナリ配置が承認された。
稼働中Coreは停止せず、常用設定/DBの変更・実provider呼出しは行わない。

## 1. 必要な利用者動作・根拠・範囲

利用者は親または子へ作業を依頼した後、providerが応答もエラーも返さず待つ場合に、
手動取消だけに頼らず当該要求の再送または失敗終了へ進める。
正常な長い生成・thinkingは、本文がまだ出ないことや要求の総経過時間だけで打ち切らない。

依頼で確定している条件:

- 親・子の共通経路へ適用する。再送前にタイムアウトした物理要求を終了させる。
- 既存`provider_timeout`分類、同一準備済み要求の追加リトライ1回・固定2秒を維持する。
- 利用者取消・Core停止・保存失敗・認証失敗を自動リトライへ変えない。
- 完了済みtool、子execution全体、context準備/hookをやり直さない。
- 途中本文/thinking、失敗試行表示、再送失敗後の保存・recallは既存契約へ接続する。

根拠は依頼文、下記の保存記録、現行コード、必要箇所の公式契約。
[一回再送計画](provider-single-retry.md)と[結果](../results/provider-single-retry.md)が既存retryの契約。
[SDK評価 A-2](../design/provider-sdk-evaluation.md)は調査の入口であり現行仕様ではない。

最初の調査段階は読取りと本書作成のみ。その後の承認により下記S1〜S3の実装・test・reviewを進める。
使い捨てworkspace/DBと独立Core/local HTTPで確認し、常用run/Core/DB/configは変更しない。
実provider呼出し、commit/push/配置は行わない。AGENTS.mdに従い一般的hardeningや未観測variantの拒否条件は追加しない。

## 2. 保存履歴で分かったこと

### 2.1 問題の要求の続報（事実）

`.hjh/history.sqlite3`をSQLite URI `mode=ro`でSELECTした。生SSE、資格情報、送信全文は調査出力へ展開していない。
対象はSession `64bbc534-6be6-4b26-9b61-afab2c825307`、親execution
`db3a1b99-2b55-482a-9127-14dfe2cdef9f`、子`53dcbedc-679f-4f62-a669-6987155a6a9f`。
以下は保存`observed_at`をJSTへ換算した時刻。

| 時刻 | 保存された事実 |
| --- | --- |
| 17:04:05.575 | 子のmodelStep 4、物理要求`openai-chatgpt.responses-4`開始（ordinal 148） |
| 17:04:59.816 | 親の`collect_agent`開始 |
| 17:19:06.007 | 同要求がHTTP 504 Gateway Timeout、`http_error/httpStatus=504`で失敗（ordinal 149） |
| 17:19:08.032 | 同modelStepの物理要求`responses-5`開始、retryAttempt=1 |
| 17:19:15.869 | 再送が正常終了。その後toolを実行しmodelStep 5も完了 |
| 17:19:34.158 | modelStep 6、物理要求`responses-7`開始 |
| 17:19:36.027 | 取消要求と`turn_cancelled`を保存 |
| 17:19:36.069 | 子がcancelledでsettle。親も同36.082にcancelled |

4回目の待ちは約900.43秒（15分0.43秒）。ordinal 148と149の間に新しい意味記録はない。
依頼文の17:15時点では未回復だったが、後から見ると約4分後に既存retryで回復していた。
取消されたのは4回目ではなく後続要求。したがって、この実例を「永久停止」とは断定しない。
ユーザーが待っていれば子の作業全体も完了した、とは証明していない。
504本文中の外部表示時刻と観測時刻にはずれがあるため、時系列は保存観測時刻に統一した。

現行`Generate`では非2xx HTTP応答をSSE処理へ渡さない。4回目は最終的に非2xx経路だったと分かる。
ただし`model_request/start`は認証・HTTP送信前、errorはエラー本文を読み終えた後なので、
900秒の内訳（OAuth待ち、接続、ヘッダ待ち、エラー本文受信待ち）は記録から分離できない。
HTTPヘッダやコメントを含む受信時刻は保存していない。「本文/thinkingの無記録＝SSE無受信」は一般には成立しない。

### 2.2 正常終了した要求との比較（限定的事実）

今回の調査自身を除くため、`observed_at < 2026-10-15T08:49:00Z`の意味記録を対象とした。
`model_request`の同execution・同物理IDのstart/endが揃う要求だけを集計。
これは**成功例に偏った小さい標本**でありSLAや正常上限ではない。

| provider/model/effort | 正常終了物理要求 | start→end 中央値 / p95 / 最大 |
| --- | ---: | --- |
| opencode-go-chat / mimo-v2.6-pro / auto | 207 | 10.16秒 / 154.58秒 / 414.65秒 |
| openai-chatgpt / gpt-6.1-sol / high | 113 | 11.09秒 / 36.35秒 / 97.00秒 |

p95は昇順配列の`floor((n-1)*0.95)`位置。要求開始はHTTP実送信より前の観測時刻である。
初出本文/thinkingの`assistant_message phase=started`を伴う正常要求はMiMo 207件、Sol 63件。
startからその記録までの最大はMiMo 162.42秒、Sol 29.54秒。
初出記録は最初の受信バイト時刻ではない。tool断片だけの要求もあり、受信間隔の最大値は算出できない。

**推論:** 総時間2〜5分の一律打切りは正常MiMo要求を切る。本文初出30〜120秒の打切りも不適切。
一方5分の応答開始待ち・10分の無受信待ちは今回の正常要求総時間/初出記録より余裕がある。
ただし無出力の正常上限を証明するデータではないため、値は暫定とする。

## 3. 現行product経路と所有者

| 区間・所有者 | 現行経路と期限/取消 |
| --- | --- |
| TUI→Core | `tui/model.go:startRun`→`apiclient`→`httpapi/server.go`→`corerun.Manager.StartRunRequest`。Core―TUIの再接続とprovider再送は別 |
| Core run | `corerun/manager.go`がCore contextとrun contextを`WithCancel`で所有。開始HTTP要求から独立、期限なし。停止/取消はここから伝播 |
| 親execution | `app/run.go`がSession・構成・接続・BeginExecutionを準備。`app/execution.go:execute`が共通実行と終了保存を所有 |
| 子execution | JS agent bridge→`app/child_runs.go:SpawnAgent`→`delegation.prepareChild/runChild`→同じ`execute`。親execution由来の期限なし取消context、親終了時は残子cancel/join |
| モデル選択・認証 | `app/connections.go:provider`がregistryとcaptured bindingをresolve。API keyは参照から取得。ChatGPTは要求ごとに`bearerTransport.RoundTrip`でも`Binding.Resolve`、必要時OAuth refresh |
| 準備・再送・tool | `agent/loop.go`のproject/Prepare→`generateWithRetry`→`protocol.Generate`。成功後にだけtool実行。retryは同一WireBodyを再利用 |
| HTTP/SSE | `protocol/generate.go`でstart観測→NewRequestWithContext→client.Do→非2xx本文またはconsumeSSE→result/error。取得済みBodyはdefer Close |
| 受信組立 | `protocol/stream.go:consumeSSE`はコメントを捨てdataをdispatch。本文/thinkingのみ主な進捗観測。tool引数断片やcreated/in_progress等は表示進捗と一致しない |
| 分類 | `generate.go:classifyProviderTransport`でCanceled→turn_cancelled、DeadlineExceeded/net.Error.Timeout→provider_timeout。それ以外transport。`contracts/failure.go:FailureFor`は取消/保存失敗を優先 |
| 再送枠 | `agent/retry.go`が追加1回・固定2秒。外側ctx終了時と待機中取消は再送しない。種類別の別枠を作らない |
| collect | `child_runs.go:CollectAgent`は子doneか呼出し側ctx終了まで待つ。独自期限なし。子の通常失敗はoutcome/errorを含むtool結果として戻る。親全体を自動失敗にしない |
| 保存 | `app/journal.go`→history commit→ExecutionDataOwnerへ公開。`execute`終了処理が子join、途中状態確定、EndExecution、成功rootのみcanonical採用、最新request/budget snapshot保存を行う。保存ctxはWithoutCancel |
| TUI | ExecutionDataOwner→conversation→Core SSE→TUI。親retry notice、子開始/終了要約、失敗reasonと既存recall導線。表示はtimeout時計の所有者ではない |

通常protocol clientは`&http.Client{}`。未指定transportは`http.DefaultTransport`。
hjhによる要求全体timeout、ResponseHeaderTimeout、Body idle検知はない。
ただしGo標準にはdial 30秒・TLS handshake 10秒等があり、「すべての通信待ちが無制限」ではない。
`IdleConnTimeout=90秒`はpoolの遊休接続用で、生成中の受信途絶timeoutではない。
OAuth managerにも独自通信期限はない。接続準備時の初回ResolveはGenerateより前にあり、本案の応答時計の外側。

TUIの実行取消はF1によるinput-chain取消。Ctrl-Cはdraft消去でありrun取消ではない。
Core停止もchain停止を記録してrunをcancel/joinする。新timeoutでこれらの外側contextを取り消してはいけない。

### 既存の他のGenerate利用者

`app/request_model.go:requestModelGenerator`（projectRequest hookの補助生成）も同じprotocol.Generateを直接呼ぶ。
**受信timeoutはここにも適用するが、「一度だけ生成する」現行契約は変えずAgent retryを新設しない。**
通常の親・子Agentモデル要求はすべて既存retryを通る。補助生成のtimeoutは既存hook失敗経路へ返す。
この差分はS2で確認し、補助生成へretry範囲を拡大する変更は今回含めない。

## 4. 方式比較と推奨

| 方式 | 利点 | 問題・判断 |
| --- | --- | --- |
| 応答ヘッダだけのtimeout | 今回のような遅いHTTP応答を早く終了できる | 200ヘッダ後にBodyが止まる場合を救えない。単独では不採用 |
| 最初の本文/thinking期限＋その更新間隔 | 見た目の進捗と一致 | 非公開reasoning、tool断片、heartbeatを誤停止するため不採用 |
| 最初のSSEイベント期限＋イベント間隔 | 本文以外も考慮できる | コメント/未完の行/大きいイベントの転送を見落とす。パーサより下の受信で測る |
| 応答開始待ち＋受信idle | 無受信を有限化し、受信が続く長い生成を維持 | 正常な無受信reasoningと障害を完全には区別できない。**推奨** |
| 要求全体の固定timeout | heartbeatだけの無限待ちも必ず終了できる | 正常な長い生成も総時間で止める。今回は不採用 |

### 推奨値（両方とも暫定の製品既定値）

- **応答開始待ち: 300秒（5分）**。
- **受信途絶: 600秒（10分）**。
- **要求全体の上限: 設けない**。親子execution全体・collect・tool時間にも上限を新設しない。
- provider別に変更可能。全標準providerに同じ既定値を使い、ID/model/effortを見て推測分岐しない。

5分は正常MiMo初出記録の最大162秒に余裕を置き、15分のHTTPエラー待ちより早く次へ進むための判断。
idleは高effortの非公開reasoningに保守的な余裕を置いて10分とする。5分より短くする受信間隔の証拠はない。
**公式が保証する期限でも、最適値を測定した結論でもない。** 初回無応答なら約5分後に再送へ進み、
両試行とも同じ無応答なら約10分＋2秒で失敗に至る（ローカル保存等の時間を除く）。
ヘッダだけでBodyをまだ受信していない場合は、開始時計300秒の残り時間で終了する。
Bodyを一度以上受信した後の停滞は最大10分の無受信を許すため、全ケースの10分終了保証ではない。

### 開始・進行の厳密な意味

1. start観測保存と要求body準備が済み、`client.Do`へ入る直前から応答開始時計を開始する。
   Do内のcredential Resolve、接続・送信・リダイレクト・ヘッダ待ちもこの時計に含む。
   初回接続準備時のResolve、hook、履歴保存、toolにはこの時計を掛けない。
2. **ヘッダ受信は段階の記録に使うが、開始待ち300秒をリセットしない。**
   ヘッダだけで成功/生成開始とはせず、最初のBody非空readまで開始待ちの残り時間を使う。
   正常なBodyなしEOF等は現行parser/HTTP分類へ返し、無理にtimeoutにしない。
3. **最初のBody `Read`の`n>0`を受信開始とする。** 最初のSSEイベントの完成や本文出力まで待たない。
4. 開始後は、Bodyから新しいバイトを待つreadの無受信時間を600秒まで許す。
   正のreadごとに更新し、コメント・空行・未知イベント・thinking・本文・tool引数のどれも区別しない。
   分割JSON/未完行の受信も進行。HTTPヘッダ、TCP ACKやHTTP/2制御frameはBody受信ではない。
5. parser/observer/DB処理中は受信待ち時計を走らせない。受信read待ちだけを監視し、
   ローカル保存遅延をprovider timeoutに誤変換しない。再readで新たにidle待ちを開始する。
   これは壁時計の厳密なwire無通信計測ではなく、hjhがBodyを待たされる時間の計測である。
6. terminal/EOF/parserエラー/observer失敗で受信時計を終了し、result観測や終了保存へ残さない。
   成功terminalを受けた後のローカル処理にtimeoutを発動させない。

HTTPヘッダ、最初のSSEデータ、heartbeat、本文は異なる段階。
heartbeatは**接続が生きている証拠であって、モデル生成が進む証明ではない**。
heartbeatだけが永久に届く停滞は、本案では自動終了しない。総時間上限や「意味的進捗」の別時計を追加すれば
止められるが、正常な無出力生成と分離できる根拠がないため今回は採用しない。
無受信の正常生成が10分を超えれば誤停止はあり得る。これをゼロにすると無受信の有限化と両立しない。

### provider差・公式根拠（2026-10-15 17:51〜17:56 JST取得）

- [OpenAI streaming guide](https://developers.openai.com/api/docs/guides/streaming-responses.md):
  created/in_progress、本文、function-call等のtyped eventがある。一定秒間隔の受信保証は確認できない。
- [OpenAI reasoning guide](https://developers.openai.com/api/docs/guides/reasoning.md):
  非公開reasoning tokenがあり、本文が出る前/間にもreasoningを行う。summaryはraw reasoningではない。
  本文無更新を生成停止とする根拠にはならない（必要箇所を確認。ページ末尾は取得上限で省略）。
- [ChatGPT OAuth inference契約](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference.md):
  public Responsesへstore=false/stream=true、completedまで受信する。現行ChatGPT経路と一致。
  OAuth経路にもheartbeatの周期/最大無受信時間の保証は確認できない。
- [OpenRouter streaming契約](https://openrouter.ai/docs/api_reference/streaming.md):
  `: OPENROUTER PROCESSING`コメントを時々送る。固定周期とは書かれていない。コメントを進行に含める根拠。
  切断による上流生成/課金停止の対応は接続先に依存すると明記されている。
- OpenCode Go Chatは現行configの`reasoningField=reasoning_content`を読み、本文とは別にthinkingを観測する。
  MiMoの長い正常要求は保存履歴で確認。OpenCode Go Responses、OpenAI API-key、OpenRouterについて、
  このDBに同条件の正常要求標本はない。heartbeat有無/周期や無受信上限を推測で必須化しない。
- Go公式契約はローカル`go doc net/http.NewRequestWithContext`、`net/http.DefaultTransport`で確認。
  outgoing request contextは接続、送信、ヘッダ、Body読取りの寿命を制御する。

### 設定の提案

既存のcaptured provider定義にだけ追加する（新CLI flag/TUI picker/role別設定は不要）。

```json
{
  "providers": {
    "openai-chatgpt": {
      "responseTimeout": { "startSeconds": 300, "idleSeconds": 600 }
    }
  }
}
```

フィールド省略は各既定値、明示0はその時計を無効、正値は秒。省略と0を型で区別する。
負値は設定の誤りとして接続準備時に説明する。恣意的な最短/最長値は設けない。
暫定値に実用上の根拠が弱いため、長考が必要なproviderでは延長/無効化できるようにする。
既存の構成snapshotへ含まれ、次に準備するWorkerから反映する。既存config/DBの移行や自動書換えはしない。

## 5. 既存処理への接続と実装境界

### 5.1 要求の寿命・取消・分類

- protocolに要求単位の小さいwait管理（例`response_wait.go`）を置く。
  状態は要求専用context/cancel cause、開始残時間、受信開始有無、監視中の待ち、timeout理由。
  Provider共有の可変時計、Core側の別watchdog、goroutineからの独立再送を作らない。
- `Generate`のHTTP要求だけに派生contextを使う。Do/readの待ち中だけtimerを動かし、
  発動したら当該派生contextをcancelし、取得済みBodyをCloseしてreadを解除する。
  **同じ呼出しでDo/consumeSSEが戻り、Body closeとtimer callbackの終了を確認してからGenerateを返す。**
  古い通信/readerを残してtimeout値だけ返すgoroutine raceは使わない。
- これはhjh側の当該HTTP要求の終了保証。上流の生成処理/課金まで停止したことは保証しない。
- 外側`ctx.Err()`があれば常にその取消を優先。次にobserver/保存失敗、実際に判明した認証失敗、
  その上で自身のwait期限発動を判定する。認証/保存エラーを単にtimeoutで包んで再送しない。
  **Do内のResolveが自身の時計で中断された場合は、実際の認証失敗ではなくprovider_timeoutとする。**
  現行`connections.go`のResolveTokenラッパは原因を問わずmissing_credentialを付けるので、
  この場合だけは要求contextの自前timeout causeと取消由来のResolve失敗を識別・伝播する。
  protocol側でも認証wrapperを見ただけで判定を確定せず、自前中断の原因を正規化する。
  外側取消、資格情報なし/refresh HTTPエラー等の実認証失敗、自前期限による中断を区別する。
  共有credential manager全体の期限や保存契約は変えない。
- 自身のtimeoutは既存`FailureProviderTimeout`にする。
  **`context.Canceled`をunwrapするerrorにprovider_timeoutを添えるだけでは不可**。
  `FailureFor`が取消を優先するため、自身のtimeoutは`DeadlineExceeded`相当の原因へ正規化する。
  DoエラーとSSE readエラー双方へ適用し、外側run context自体は取り消さない。
- 非2xxのエラー本文readも待ちを有限化する。既に判明したHTTP statusの分類を維持し、
  例えば401/403本文の途絶をprovider_timeoutへ置換して認証エラーを再送しない。
  現行の部分本文/read error付きHTTP失敗へ返し、429/5xxなら既存枠でのみ再送する。
- `provider.fail`を通す既存redactを維持。errorには`response start timeout (300s)`または
  `response read idle timeout (600s)`等の段階と有効値を記載する。
  raw SSE常時保存や新DB列、新しいfailure codeは不要。

### 5.2 再送・保存・表示・recall

- `agent.generateWithRetry`をそのまま枠の所有者にする。timeout→同じWireBodyを2秒後に一度だけ再送。
  最初がHTTP/parse失敗で次がtimeoutでも追加枠は作らない。
- 初回失敗の観測済み本文/thinkingは`model_retry`→`journal`→`SaveAssistantResult`でsuperseded確定、
  active state除去。再送はretryAttempt=1の別key。失敗本文を次のモデルmessagesへ足さない。
- 最後の失敗試行はexecution終了時にincompleteで確定。診断・途中本文/thinkingは詳細履歴へ残す。
  未完tool引数を実行したり、過去のtool結果を巻き戻したりしない。
- 通常TUIはsupersededを除外し、既存`RETRYING · provider deadline exceeded · 1/1 · 2s`。
  既にscrollbackへ出た行は消さず試行を区切る。段階詳細は保存errorで参照し、専用UIは増やさない。
- 親再送失敗は既存`FAILED · provider deadline exceeded · try /recall`へ。
  chain/runがsettleした後の引数なしrecallが停止rootを選び、次の新しい要求へ参考入力を渡す。
- 子再送失敗は子をfailedでsettleしcollectを戻す。親はその結果で次の判断をする。
  子timeoutを理由に親runを強制取消しない。通常子要約の表示範囲は維持し、子自身の参考記録は
  必要なら`/recall 子ID`で選ぶ（引数なしrecallは子を自動選択しない）。
- recall投影は既存どおりsupersededを除外、最後のincomplete本文を参考にし、thinkingは履歴へ残す。
  新しいtimeout用の復旧状態や子全体再実行を設けない。

### 変更範囲

中心は`internal/provider/protocol/{protocol.go,generate.go,新wait helper}`。
受信時計はBody reader境界に置くため、`stream.go`の受理条件/イベント型は原則変更しない。
`internal/providers/registry.go`に設定形、`internal/app/connections.go`に受渡し。
`docs/design/providers.md`へ設定契約を記載する。
retry/保存/TUI/Core/recallは既存経路の接続確認を基本とし、具体的な接続不具合が判明した場合だけ最小変更する。
スキーマ、実DB、実config、credential保存、retry回数、collect期限、TUI操作体系は変更しない。

## 6. 実装スライスと確認方法（承認後）

確認は上記product動作へ対応付ける。人工的な時間短縮は同じ設定/内部wait policyを使い、
外部providerの仕様やparser契約をtest都合で変更しない。実装中はfocused testのみ。

### S1: 共通protocolで待機を終わらせる

動作: ヘッダ前/ヘッダ後最初のBody前/受信途中の停滞を中断し、正常な長い受信を継続する。

- 要求単位wait helper、取消cause/分類、Body読取りラッパを実装。protocol内の既定policyを接続。
- `httptest`の実HTTPでヘッダなし、ヘッダだけ、本文/thinking後途絶を確認する。
  server側request contextの取消とreader終了を確認し、Generateが古い待機を残さず戻ることを確かめる。
- 本文なしのcreated/heartbeat/分割tool引数を送り、総経過が設定値を超えても受信が続けば完了できることを確認。
  heartbeat後に止めればidleで終了する。コメントをJSONへ渡す変更はしない。
- 外側取消が同じ待ちを即解除し、診断がturn_cancelledとなること、観測保存失敗がtimeoutに化けないことを確認。
  非2xx本文途絶でも既知401/5xxの分類を保つことを確認。
- focused: `go test ./internal/provider/protocol -run '<追加したwait/timeout確認名>' -count=1`。
  timer/readの並行部分に限ってrace確認を行う。

### S2: provider設定と親・子の既存retryへ接続

動作: 全通常providerで設定を読み、親子とも同一要求だけを再送し、取消・停止では再送しない。

- registry定義→captured config→connection→protocolへ有効値を渡す。
  省略既定、provider別変更、0無効は実際の接続準備経路で確認。
- Agent loop＋実HTTPで「既に完了したtool→次の要求timeout→同一wire再送成功」を実行。
  過去toolが一度だけ、初回の未完toolは実行ゼロ、再送toolが一度だけであることを確認する。
- appの親とspawn/collect子でtimeout→成功、再失敗→子settle/collect復帰を確認。
  固定2秒と追加1回を維持し、別エラーですでにretry枠を使った場合はtimeoutで三試行目を送らない。
- 通信待ち中のF1相当取消/Core停止とretry待機中取消を確認し、次試行を送らずjoinする。
  projectRequest補助生成は同じtimeoutで戻るが、一度だけ生成の契約を維持することを確認。
- Do内で待つResolveTokenを実際のconnectionsラッパ越しに通し、開始期限による中断は
  provider_timeout→一回再送、実認証失敗はmissing_credentialで再送なし、外側取消は
  turn_cancelledで再送なしとなることをfocusedに確認する。実credential/refreshは呼ばずローカル経路を用いる。
- focused: 変更に応じ`internal/providers`、`internal/agent`、`internal/app`、`internal/corerun`の該当testだけ実行。
  設定文書を更新し、ここで一度経路の限定reviewを行う。

### S3: 保存・画面・recallをproduction入口から総合確認

動作: 待機から実際に再送/失敗へ進み、本文が混ざらず、利用者が失敗後に続行できる。

- 必要なfocused確認を`internal/app`、history/TUIの既存接続testへ追加する。
  timeout前の本文/thinkingのsuperseded/incomplete、retryAttempt key、canonical非混入、
  復元表示とrecall入力を確認する。保存/TUIに実変更不要なら重複testを増やさず既存確認を利用する。
- 使い捨てworkspace・v4 DB・独立Core・実PTY TUI・local HTTP providerで、次を一続きに確認する。
  1. 親の無応答→timeout→再送成功、noticeと本文分離。
  2. 子の受信後停滞→再送失敗→collect復帰、親が結果を受けて完了。
  3. 親の二試行失敗→FAILED/recall案内→引数なしrecall→次依頼が参考記録を受けて完了。
  4. 通信待ち中F1取消、独立Coreの停止で通信/joinが終わり、追加送信なし。
  local serverの受信・取消時刻、試行数、wire一致、tool実行数、保存記録、PTY表示を対応付ける。
- 短い設定で実経路を反復せず確認した後、既定値そのものの配線はS1/S2で確認する。
  最後に安定候補で、実時間の既定start 300秒の無応答一試行を独立Core/local HTTPで確認する。
  成立を確認した後は明示取消で残試行を終了でき、10分待ちの反復は不要。
- 実providerを呼ばないlocal検証は、HTTP取消/再送/保存/UIの成立を示すが、
  実providerの長考を誤停止しないことや待機改善の効果を実証する代替ではない。
  配置後の通常利用でtimeout理由と回復/正常長考の実例を確認し、値の見直し材料にする。
  そのためだけの実provider呼出しや常用環境切替は別途指示なく行わない。
- 最後の総合確認としてcoordinating ownerが安定候補に対して一度だけ
  `go test ./internal/... ./cmd/... -count=1`、既存手順のstaticcheck、`git diff --check`を行う。
  途中のfull gate反復やreview前gateは要求しない。失敗はfocusedで調べ、再実行の理由を残す。

## 7. 未確認事項と実装前の判断

未確認（実装上の選択待ちとは区別）:

- 4回目の15分のうちどの通信段階で待ったか、ヘッダ/Body/heartbeatの実到着時刻。
- provider/model/effort別の最大正常無受信時間。保存本文の間隔では代替できない。
- heartbeatだけが続く永久停滞と、正常な非公開reasoningを意味的に区別する方法。
- requestを閉じた後の各provider上流の生成/課金停止。ローカルHTTP要求の終了とは別。
- protocolより前の初回credential Resolveに期限がない点は残る。認証の別timeout設計へ勝手に広げない。

実装前に利用者が決めることは、**本推奨案（開始5分・受信途絶10分、全体上限なし、provider別設定可能）で
実装へ進めるか**だけ。値は暫定であり、heartbeatだけの待機と10分超の正常無受信を完全に解決しない点も含めた承認とする。
モジュール境界、時計の観測点、取消優先、分類、設定形、実装順序は本書の技術判断で進め、個別確認事項を増やさない。

## 現在地

調査・本書作成後、利用者が実装・test・コードreviewを承認。S1〜S3完了。
[確認結果](../results/provider-response-timeout.md)にfocused/race、実Core/PTY、実時間300秒、独立reviewを記録した。
当初の全体testは既存CLI接続testの進捗待機で失敗し、変更前sourceのGo overlay比較でも再現した。
続いて利用者が対応を指示。test自身の容量8 queueによる本文イベント破棄を特定し、検出通知を修正。
対象20反復・race・独立review後、原因修正を理由とする全体test一回の再実行で全package成功。
staticcheck/diff checkも成功。詳細は結果文書の続報に記載。
依頼メモや既存の未コミット変更はそのまま保持する。常用環境の配置/設定変更は別途指示時に行う。
