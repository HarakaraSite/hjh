# 親子agentのproviderとキャッシュ用ID

記録日: 2026-10-16

## 現在の整理

親がChatGPT OAuth、子がOpenRouter経由のDeepSeekなど、親子で異なるprovider・modelを使える。
子のprovider・model選択は既存の委譲処理が担当するため、親子の全組み合わせごとに
キャッシュ用IDの処理を実装する必要はない。

送信用セッションIDは共通の処理で決め、どの名前のヘッダーで送るかは、選択したproviderの
`sessionHeader`設定で決める。親は保存済みのSession ID、子はそれぞれの実行IDを使う。
同じ子のモデル呼出し・リトライ・追加指示後の継続では同じIDを使う。
子を保存用の独立Sessionにする変更ではなく、履歴は引き続きroot Sessionへ関連付ける。

| provider | 現在の送信用ヘッダー | 確認範囲 |
| --- | --- | --- |
| `openai-chatgpt` | `session_id` | ChatGPT OAuth / GPT-6.1-solでキャッシュヒットを実測 |
| `opencode-go-chat`・`opencode-go-responses` | `x-opencode-session` | 既存の設定。今回、このヘッダーによるキャッシュ効果は実測していない |
| `openrouter-responses` | 未設定 | 今回の送信用IDは送らない。OpenRouter経由のDeepSeekでの効果は未確認 |

## 今後確認するときの境界

ヘッダー名だけが異なる経路なら、既存の`sessionHeader`設定で対応できる。
ただし、すべてのproviderのキャッシュがヘッダー名の変更だけで最適化できる、と確認したわけではない。
認証方式・API経路・モデルによって本文パラメータやキャッシュ指定が必要になる可能性があり、
その部分はproviderごとに公式契約と実測を確認する。

ChatGPT OAuthでの測定結果を、OpenRouterやその先のDeepSeekへそのまま適用しない。
親子で異なるIDを使う設計と、各providerでキャッシュを有効にする設定は別に扱う。
また、親子同一IDとのキャッシュ性能比較は未実施であり、ID分離の優位性を実測済みとはしない。

関連:

- [provider設定と送信契約](../design/providers.md)
- [直接HTTP試験とhjh経由の親子実測](../results/direct-cache-patterns.md)
- [集計データ](../results/session-header-integration.json)
