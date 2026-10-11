# モデル接続のSDK採用評価と現行手書き実装の改善候補

status: evaluation（採用方針ではない）
date: 2026-10-11

この文書は、hjhのモデル接続層（`internal/provider/protocol`）について、
「OpenAI系SDKを採用するか、現行の手書きHTTP/JSON/SSE実装を維持するか」を検討した
調査・議論の記録である。ここに記載した内容は評価と改善候補であり、
採用済み方針・実装認可ではない。採否は利用者の明示指示と
[初期製品の方針](initial-product.md)に従って判断する。

前提として、**現在の履歴schemaを維持する**（スキーマは変更しない）ことを置いている。
SQLiteの保存契約と、採用済みの履歴設計は
[履歴schema契約](history-schema.md)を正本とする。

## 調査した現行実装

### モデル接続層

- `internal/provider/protocol/protocol.go`: 共通`contracts.Provider`実装。`api`（`responses` / `chat-completions`）で送信形式を選ぶ。
- `internal/provider/protocol/encode.go`: リクエストbodyを`map[string]any`で手組みし`json.Marshal`で送信。履歴message → wire形式の変換も自前。
- `internal/provider/protocol/stream.go`: `bufio.Reader`による自前`consumeSSE`と、Responses / Chat Completions の各イベントを手解析する`streamAssembly`。
- `internal/provider/protocol/generate.go`: `http.NewRequestWithContext` → `provider.client.Do` → SSE集約 → `ModelResult`組立。
- `internal/provider/protocol/auth_transport.go`: `http.RoundTripper`を実装した`bearerTransport`。endpointと scheme/host が一致するリクエストにのみ`Authorization: Bearer`を付与し、他オリジンでは削除。`secretCache`で最新トークンを保持しエラーからredact。
- `internal/app/connections.go`: 外部`config.json`のprovider定義から`protocol.Config`を組み立て、`protocol.New`でproviderを生成。
- `internal/credentials/`: ChatGPT OAuth（認可URL・callback交換・更新）も`net/http`直叩き。

### 接続先（外部`config.json`で定義）

5種。OpenAI互換だが同一ではない。

| provider | api | auth | 備考 |
| --- | --- | --- | --- |
| `openrouter-responses` | responses | api-key | `adapter.reasoningReplay` |
| `opencode-go-chat` | chat-completions | api-key | `sessionHeader`, カスタムheaders |
| `opencode-go-responses` | responses | api-key | 同上 |
| `openai-responses` | responses | api-key | |
| `openai-chatgpt` | responses | chatgpt-oauth | `adapter.namespaceTools`, `catalog:openai-account` |

### SDKを使っていないことの確認

- `go.mod`の直接依存は quickjs / sqlite / bubbletea / bubbles / lipgloss / ansi / goldmark / uuid のみ。OpenAI/Anthropic等のAI SDKは入っていない。
- 送信は`net/http`＋`encoding/json`、受信は自前SSEパーサ。SDKは不使用。
- テストは`protocol_test.go`（451行）、`worker_test.go`（1262行）等で厚め。

### schemaが要求する「生データ」

現行スキーマは、SDKが隠しがちな生データを不透明JSON/バイトとして保存する：

- 各assistantメッセージに `ProviderState{Provider, Model, Reasoning, ReasoningDetails[]rawJSON, ReplayItems[]rawJSON}`（`internal/contracts/contracts.go`）。Responsesの出力itemをそのまま再送（replay）する。
- `ModelRequest.WireBody`（送信した実バイト列）を`provider_wire_body` occurrence と `model_request` content として `internal/app/context.go` が保存。
- `PhysicalRequestID` も保存。

## SDKを採用する場合のメリット

1. **Responses APIの型付きencode/decode**をSDKに置換できる（最大の利点）。`encode.go`の手組み・`stream.go`のイベント手解析の保守コストとバグを減らせる。
2. **ストリーミングとtool call断片結合**をヘルパに任せられる（`ssestream` / `Accumulate`相当）。ただしhjh固有のthinking/summaryのキー割当（`item_id`+`output_index`+part index）は引き続き自前。
3. **エラー型と自動リトライ**（HTTPステータス・コード・`request-id`・`retry-after`、429/5xxのbackoff）が標準で付く。現行は未実装。
4. **auth・ミドルウェアの標準化**（`WithAPIKey` / `WithBaseURL` / ミドルウェアでカスタムheaders・ログ・redaction）。`bearerTransport`の置換候補。
5. **上流メンテナンスの恩恵**（新reasoning field・新イベント種別・セキュリティ修正の追従）。
6. **テスト容易性**（型付き期待値＋`httptest`）。

## 現行手書き実装を維持する理由（SDK採用の摩擦）

- **schemaの「生state/生バイト保存」との相性**: `ReplayItems` / `ReasoningDetails` / `WireBody` をそのまま保存・再送する契約に対し、SDKは型へ正規化する方向。未知フィールドのround-tripやバイト一致の保証がなく、SDKのraw APIから吸い上げるグルーが別途必要。
- **マルチベンダーでの利得の非対称**: OpenRouter / OpenCode Go はOpenAI非純正。`namespace` toolラッピング、`reasoningReplay`の差、`store`、`sessionHeader`等はSDKの想定外で結局カスタム。利得が大きいのはOpenAI公式ホストのproviderのみ。
- **抽象の二重化**: 現行は`contracts.Provider{Prepare→生バイト, Generate}`の単一抽象。SDK経路を混ぜると`Prepare`（生バイト返却）が作りにくい。
- **依存と配布方針**: 現状は依存最小・pure-Go静的Linux配布。SDKはpure Goで配布とは両立するが、依存グラフと保守対象が増える。

**結論（評価時点）**: 現行手書き実装の維持が妥当。SDK採用はOpenAI公式providerに限ったハイブリッドが現実的だが、その場合も`Prepare`/`Generate`契約への適合と生データ吸い上げの追加作業が必要。

## 現行手書き実装のデメリット・改善候補

優先度は「実利用の安定性に効くか」で整理する。
なお[AGENTS.md](../../AGENTS.md)の方針により、依頼のないhardening・fallbackは足さない。
以下は利用者判断で採用する候補である。

### A. 信頼性・運用（優先度高）

- **A-1 リトライなし**: `generate.go`は`provider.client.Do`を1回呼ぶだけ。429/503・一時的な接続断・ストリーム途中切断でそのままrun失敗。`Retry-After`未使用。
- **A-2 HTTPクライアントのタイムアウトなし**: `connections.go`で`options.HTTPClient`は通常nil → `protocol.New`が`&http.Client{}`を使用。接続・ヘッダ応答がストールするとCtrl-Cまで待ち続ける。Responsesは長時間ストリームなので、全体timeoutより応答ヘッダtimeout/アイドル検知＋ctxキャンセルが適切。
- **A-3 エラーの型情報喪失**: `fail()`が`errors.New(message)`で包み直すため、HTTPステータス・Request-ID・retryable判定を持てない。失敗時のレスポンスヘッダは捨てている（成功時のみ`X-Generation-Id`を拾う）。
- **A-4 非2xx本文を丸ごとmessageへ埋め込み履歴保存**: `provider HTTP %s: %s`にbody全体。大きいbodyや内部実装の露出が履歴に残る。redactはAPIキー置換のみ。

### B. プロトコル頑健性・互換（実害が出たら対処）

- **B-5 ストリーミング固定＋終端マーカー厳格**: 常に`stream:true`。Chat Completionsは`[DONE]`、Responsesは`response.completed`を必須とする（`stream.go`の`result()`）。`[DONE]`を送らない、`stream`を無視して単一JSONを返す互換プロバイダでは必ず失敗する。
- **B-6 indexの数値型に厳格**: `decodeJSONInt`が`int` unmarshal。`output_index` / `tool_calls[].index`がfloatで来る変種でtool call組立が壊れる。
- **B-7 引数なしツールのargumentsに`json.Valid`要求**: `toolCallResults()`が`json.Valid`不合格で「incomplete tool call」として実行全体を失敗させる。0引数ツールで空文字を送る変種で誤爆し得る（現行標準toolの通常経路では踏まない候補レベル）。
- **B-8 リダイレクトはデフォルト挙動**: POSTの301/302/303がGET化する等は未検証。Authorizationのオリジン限定付与は`bearerTransport`が担保。

### C. 機能カバレッジ

- **C-9 マルチモーダル（画像入力）未対応**: `config.json`に`deepseek-v4-flash-vision-exp`がある一方、message送信はテキストのみ。
- **C-10 Responses仕様変更への追従が手作業**: SDKの最大メリットの裏返しで、上流変更時の保守コストが残る。

### D. 保守・テスト

- **D-11 `stream.go`（803行）に2 API分が同居**: 責務は凝集しているが分割余地あり。
- **D-12 encodeが`map[string]any`手組み**: typoがコンパイル時に検出されずテスト依存。
- **D-13 SSEパーサは`data:`のみ処理**（`event:`/`id:`/`retry:`無視）。通常は実害なしだが、巨大1行で無制限にallocし得る。上限を入れるかは方針判断を要する。

### E. 弱点ではない（方針どおりの意図的トレードオフ）

- 履歴の自動切捨て・step上限なし、全履歴保持。
- セキュリティ機構はAPIキー露出防止のみ（過剰なhardening/拒否matrixを足さない）。
- 旧形式の互換reader・dual-read/writeを足さない破壊的変更の既定。

## 改善の優先順位（提案）

1. **A-1 リトライ / A-2 タイムアウト / A-3 型付きエラー** — 実providerでの安定性に直結。SDKに頼らず自前で小さく導入可能（`RoundTripper`ラッパで429/5xx＋`Retry-After`を限定回数リトライ、`protocolError`にstatus/requestID保持）。
2. **B-5 終端マーカー寛容化** — 実機で`[DONE]`欠落を観測した場合のみ。
3. **B-6 / B-7 互換差分** — 該当providerで再現したら修正。
4. **C-9 画像** — 要求が出たら。

## 未確認事項・次の一手

- A-1〜A-3は未実装。導入する場合も`contracts.Provider`とスキーマ（`WireBody`/`ProviderState`保存）を維持したまま実装できる見込み。
- B-5〜B-7は実providerでの再現有無が未確認。観測できれば実害ベースで対処する。
- C-9は実際の画像利用要求の有無が未確認。

## 参考

- [初期製品の方針と範囲](initial-product.md)
- [履歴schema契約](history-schema.md)
- [provider設定と認証](providers.md)
- [AGENTS.md](../../AGENTS.md)（作業ポリシー）
