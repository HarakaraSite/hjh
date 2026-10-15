# TUI コンテキスト使用率表示

2026-10-15。長めの作業中にコンテキストがどれくらい埋まっているかを人間が把握するため、
TUIのfooterへ定常表示を追加する。利用者がSession `600ce255`で実装を承認した。
スライス1〜3の実装・確認を完了した。利用者の区切り停止後、後続指示でcommit/push・常用配置へ進む。
provider実測usage表示は追加しないことを再確認済み。
[結果](../results/context-usage-display.md)を参照する。

## 目的と利用経路

人間が普段の作業中に、会話・入力欄を妨げず、次のモデル呼出しの入力が
「モデルのコンテキストウィンドウ」と「hjhの有効入力予算」に対しどれくらいかを確認できる。
確認経路はfooterの定常表示1行分の短い断片で、詳細は既存の `/context` に委ねる。
表示のための追加モデル呼出し・送信全文の保存・新規DB記録は行わない。
既存の `context_budget` 計測・予算解決結果とCore→TUI経路だけを使う。

## 表示仕様

### 表示例

footer 3行目(provider / model / effort の行)の**右端へ右寄せ**で追加する。
左のselection表示は既存どおりで、右端の使用率断片を確保した残り幅を使う
(sessionFooterと同じ余白分離の右寄せ方式)。`→ next:` 併記時も断片は右端を維持する。

```
opencode-go-chat / deepseek-v4.1-flash / auto                          ctx 24%
```

- `ctx 24%` = **直近の通常モデル呼出しに送信したモデル入力(推定)÷ hjhの有効入力予算**。
  切り捨て整数%。表示は%だけで足り、分子・分母の内訳は `/context` の詳細で確認する。
- 分子の正確な意味: 選択された会話履歴(コンパクション置換後を含む)+現在のturnの
  メッセージ(user指示・assistant応答・tool call/result)+指示(instructions/system)+
  tool定義を含む実送信入力を、`Estimator = "wire-utf8-bytes/3+framing-estimate"`
  =(wireBytes+2)/3+16+8×messageCount で推定したトークン数。
  wireBytesはadapter `Prepare` 済みリクエストのUTF-8バイト数(実測)だが、
  bytes→tokens換算は経験式で、**表示値は常に推定**。実測usageは表示しない(2026-10-15決定)。
  provider tokenizerとの一致は保証しない。日本語・CJKでの誤差は今回未計測。
- 分母の正確な意味: **hjhの有効入力予算 `Budget.Limit`** = min(floor(C×q), I, C−R)。
  C=モデルのコンテキストウィンドウ(contextTokens)、I=入力上限(inputTokens)、
  R=出力予約(outputReserve)、q=入力比率(既定0.8)。config(models>providers>defaults)>
  capacity metadataの優先順で解決される。hookの既定80万は有効入力予算になり得るが、モデル容量の根拠ではない。
  `ctx` は機能名の略であり、%は「モデルのコンテキストウィンドウ」ではなく
  「hjhの有効入力予算」基準。窓自体の値(C)と窓基準の割合は `/context` の
  Context C / limit / sources で確認できる。

### 表示形態の規則

1. Limitがconfig/capacity由来のとき: `ctx 24%`。
2. Limitがhook policy由来(compaction.jsのfallback 80万等)のとき: `ctx 38%*`。
   `*` = 分母はhook予算でありモデル本来の上限ではない、という凡例(文書と `/context` に明記)。
   **80万をモデル本来の上限と誤認させない**。
   どの項がLimitを決めたかはCore側で算出する `ContextBudgetValues.LimitSource` で判断する
   (本変更で派生値Limitのsourceを追加。TUIでmin式を再実装しない)。
3. Limitが不明(C/I/Rすべて不明)のとき: `ctx 3.0k tok`(%を出さない)。
   分母・%を出さないため**80万が分母になることは無い**。
4. 計測が1件もないとき(新規Sessionの初回送信前): 断片ごと出さない。
5. 狭幅: 断片は `ctx 24%` で7〜10 cell程度と短く、断片幅を先に確保して残りをselectionへ
   割り当てる。極狭では断片を落としてselection優先。
   行への追加で会話・入力欄の行数は変えない(footerは既存3行のlive band、行数不変)。

### 数値の正確な意味

- 表示値は「このSessionのroot executionにおける直近の通常モデル呼出しの準備済み入力」。
  実行中は現在のrunの最新step、待機中は直近に完了したrunの最終stepを表す。
  予算超過で送信前に停止した場合は、その入力準備時点の値となる。
- 要約専用のモデル呼出し(`context.model.generate`、compaction.jsの要約等)は
  通常要求のprojection・`context_budget` 記録・通知を生成せず、表示に混入しない。
  child agentの入力もroot executionの記録のみを対象にしているため混入しない。
- 入力欄に打っている未送信の下書きは含まない。「直近送信時点」の値である。
- 換算は常にhjh推定であり、provider tokenizerの実測ではない。%は桁感の目安として扱う。

## 更新タイミングとシナリオ別の動作

- **実行中**: 各モデルstepのprojection確定時(projectRequest、送信前)に1回更新される。
  直前のtool結果が入力へ加わった直後に値が増える。応答待ち中は不変。
- **待機中**: 最終stepの値を保持したまま据え置き。TUI再起動・セッション再開時は
  既存 `GET /api/v2/sessions/{id}/context` の `LatestBudget` で復元する
  (最新root executionの最新 `context_budget` record + 詳細Fact snapshot)。
- **コンパクション後**: compaction hookの `context.replace` で置換された入力が次のstepの
  projectionになるため、そのstepで値が下がる。Factは `historyReason: "project_request_hook"`、
  `historyStatus: "none"`(超過時 "omitted")。要約呼出しの最中は更新されず、
  コンパクション直前の値が残る(要約の入力・usageは表示されない)。
- **モデル変更**: 予算はrunごとに解決される。次のrunの初stepまで表示は直近呼出しの
  事実(古いモデルの入力・古い予算)を示す。記録は `modelSelection` を持つため、
  現行selectionと不一致であることは検出可能(2026-10-15決定: 不一致中も据え置く)。
  初stepで新モデルの予算へ更新される。
- **セッション再開**: 上記の復元値は「このSessionの直近のモデル入力」を表す。
  Core未接続・記録なしなら断片なし。

## 内部設計

### 既存の値の生成元・所有者・保存先(変更しない)

- 生成: `internal/app/execution.go` の `ProjectRequest`(model step毎)が
  `contextbudget` projectorでprojection/Factを生成。`contextBudgetRecord`
  (internal/app/context_budget_record.go)が要約 `contracts.ContextBudgetSummary` を作る。
- 保存: `history.Store.AppendBudgetSummary`(kind=`context_budget`、step毎にappend)+
  `SnapshotOwner.recordBudget`→実行終了時 `execution_latest_budgets`(詳細Fact、最新1件)。
  読み出しは `history.Store.SessionContext` の `LatestBudget`。
- 実測usage: `internal/provider/protocol/generate.go` が `Observation{Kind:"usage"}` と
  `ModelResult.Usage`(raw JSON)を発行、journalが `model_result` recordへ保存。
  context_budget記録には含まれない。要約呼出しのstreamはno-op observerを使うが、専用APIの短いmetadata・usageは記録する。

### Core→TUI通知経路(既存)と新規状態

既存のpush経路: journal観測 → `app.RunRequest` のコールバック群 →
`corerun.Manager` が `api.RunState` を更新 → `changedLocked` → SSE `run.update` →
`apiclient` → TUI `acceptRunEvent`。`ActualSelection`/`ActualContext` がこの経路の実例。

**新規に必要なものは2つだけ。既存情報で足りない理由は次のとおり。**

1. `api.RunState.ContextBudget *contracts.ContextBudgetStatus`(新規field、詳細Factは載せず要約のみ)。
   既存の `SessionContext.LatestBudget` はpull型で、実行中に毎step取得すると
   step毎のHTTP requestが要る。run状態は既にstep毎の事実(selection等)をpushしているため、
   計測要約も同じ経路に乗せる。contract型は `contracts.ContextBudgetStatus` を再利用し、
   新規type・新規endpoint・新規保存は作らない。
2. `app.RunRequest.OnContextBudget` コールバック(`OnSession`/`OnAdmission` と同じ作法)。
   projection確定後に要約をCoreのrun状態へ渡す。journal観測を経由させないことで
   余分なrecordを残さない。corerunはroot executionのものだけ受理する
   (childのcallbackは渡さない/ExecutionIDで濾す)。

あわせて `contracts.ContextBudgetValues.LimitSource` を追加する
(`contextbudget.resolveLimit` がどの項でLimitが決まったかを記録)。
TUIがmin式を再実装せず分母の由来(hook/unknown/capacity等)を表示するため。
あわせて `contextBudgetRecord` のsummaryにも保存する(復元時に由来が分かるように)。

TUI側は既存endpointの新規呼び出し箇所として、session load/resume時
(`loadSession`)に `SessionContext` を取得し待機中の表示をseedする。API追加ではない。

### 変更モジュールと責務

| モジュール | 変更 | 責務 |
| --- | --- | --- |
| `internal/contextbudget` | `Budget.LimitSource` 追加 | 予算解決の事実にLimitの由来を付す(所有:予算解決) |
| `internal/app` | `RunRequest.OnContextBudget`、`ProjectRequest`後に要約通知 | 計測事実の生成と通知。保存・表示はしない |
| `internal/contracts` | `ContextBudgetValues.LimitSource` 追加 | wire契約 |
| `internal/api` | `RunState.ContextBudget` 追加 | client契約 |
| `internal/corerun` | callback配線、state更新・clone・通知 | run状態の所有。root executionのみ |
| `internal/apiclient` | 原則変更なし(RunState構造体の自動decode) | 事実の転送 |
| `internal/tui` | state保持、`footerModel` への断片描画、loadSession時のseed | 表示のみ。判断・計算はCoreの事実に従う |

表示規則(hook/unknown表示、省略優先順)はTUIの表示責務に置く。
`/context` の既存詳細は維持し、Limitの由来とfooterの`*`の凡例だけを追加する。

## 実装・検証スライス

各testは下記のproduct動作に対応させる。focused確認(`go test` 対象package、
`git diff --check`)を使い、full gateは繰り返さない。

### スライス1: Coreの通知経路

- `LimitSource` 解決 + `RunRequest.OnContextBudget` + `RunState.ContextBudget` 配線。
- test(対応するproduct動作):
  - 実行中、各モデルstepで入力推定・予算・Limit由来がrun状態へ通知される
    (要約値が `context_budget` recordのsummaryと一致する)。
  - compaction hook有効時、通知値は要約呼出しの入力ではなく置換後の親入力を表す
    (既存 `compaction_test.go` の経路でcallback値を確認)。
  - child実行の計測は親のrun状態に載らない。

### スライス2: TUI footer表示

- run event / SessionContextからのstate保持とfooter断片描画。
- `footerModel` は左にselection、右端に使用率断片を余白で分離して1行へ収める。
  断片幅を先に確保し、selection側の省略は残り幅で行う(既存の幅調整に追従)。
- test(対応するproduct動作):
  - 通常形 `ctx 24%` の描画(%=推定入力÷Limit)。
  - Limit不明時 `ctx 3.0k tok`(%なし。80万が分母でない)。
  - hook由来Limit時 `ctx 38%*`(`*` 付きでhook予算基準と分かる。80万をモデル上限と表示しない)。
  - 計測なしで断片が出ない。極狭幅で断片が省略されselectionに幅が戻る。
  - 待機中・セッション再開で復元値が「直近のモデル入力」として表示される。

### スライス3: 文書と実利用確認

- `docs/design/tui-footer.md` への表示規則追記(または `context-usage-display` 設計節)。
  READMEのfooter説明は現状と矛盾する場合のみ直す。
- 実providerでのTUI起動確認(footerの%が `/context` の詳細と一致すること、
  コンパクション前後で値が下がること、`*`/`tok` 表示の出分け)。
  結果は `docs/results/` へ記録。

## 含めないもの

- 表示専用のモデル呼出し・tokenizer呼び出し・送信全文の保存・新規DB table/record。
- compaction.js・予算解決式・履歴schemaの変更(80万fallback自体は
  hook policyとして据え置き、表示側で由来を明示する)。
- 未送信下書きの見積り、閾値警告・色分け・アラート、CLI(非TUI)表示。
- provider実測usageの表示(2026-10-15見送り。表示値は常にhjh推定で足りる。
  将来必要になった場合のみ、hjhのprotocol層が確認済みのusage形状
  (chat-completionsの `prompt_tokens` / responses系の `input_tokens`)に限って
  実測表示を追加検討する。その際は推定/実測の区別表示が必要になる)。

## 未確認事項

- 実測usageは表示しない(推定のみ)。日本語・CJKでの推定誤差は未計測で、
  過大方向に大きく出る可能性がある。実機確認時に `/context` の実測記録と桁感を比べる。
- footer3行目は `→ next:` 併記や長いmodel名と幅を競合する。常用端末幅での
  省略優先の見え方は実装時の実機確認で詰める。
- capacity metadata(models.dev)未取得環境ではC・Limitが不明になりやすい。
  `metadataError` は `/context` に既に出る。表示は `limit unknown` で運用する。
- セッション再開時の `SessionContext` 取得コストは未計測(既存endpoint、1回のみ)。

## 利用者の決定(2026-10-15)

1. 分母: hjhの有効入力予算Limitを%の基準に → **確定**。
   表示はA案 `ctx 24%` の%のみ。窓(win)併記・分子・分母の内訳は `/context` に委ねる。
2. 実測usage表示(旧スライス3)は**見送り**。推定/実測の区別ラベルも不要。
3. モデル変更後・初step前の表示: 直近呼出しの値を据え置く → **確定**。
4. 断片の場所: footer3行目(モデル等の表示行)の**右端** → **確定**。

## 引継ぎ後の由来表示の修正

`api.context.select()`の結果を`replace()`へ渡すと、変更していない解決済み予算までhook由来へ
変わる経路があった。表示上の誤分類を避けるため、Overrideは同値の項の既存sourceを維持する。
またC×qとC−Rは、q/Rをhookが変えた場合もhook由来のLimitとして扱う。
予算の数式・有効値は変えず、通常予算とhook policyの表示を正しく区別する。
