# 後続課題：HTTPの生レスポンス全文によるcontext超過

2026-10-11。利用者が実利用で報告した。原因を保存済みrequestとtool結果で確認済み。
利用者の指示により対応は後回しとし、今回は記録のみ。修正方式・実装時期は未決定。

## 観測

- Session：`d5f9aaa7-03e9-4bf8-a29c-ebd2ebc1215a`
- execution：`7b4fe633-124c-4e87-a46a-309d2c748755`、turn 7
- 依頼：「quickjsだが別のgo実装があると聞いた調べられるか？」
- `http`でmodernc.org/quickjsのGo Packagesページと、fastschema/qjs、buke/quickjs-go、dop251/gojaのGitHubページを取得。
  4件ともHTTP 200で、HTMLの生本文は合計2,311,506 bytesだった。
- 4件のtool結果を取り込んだ次requestが455,360 → 3,552,599 bytesに増加。
  OpenRouter経由のTogetherから`context_length_exceeded`が返った。
  providerの報告は上限1,048,576 tokensに対し入力1,097,522 tokens。
- executionはfailed／non_canonicalとして保存され、通常の採用会話には入っていない。

## 原因と経路

外部tool `.hjh/extensions/index.js`の`http`はGo基礎API `api.http()`の
`{status, body}`をそのまま返す。標準例`internal/extensions/assets/index.js`も同じである。
Go処理は`internal/host/host.go`にあり、HTTPの生本文を取得する。
tool結果→Agentのtool message→Responsesのfunction_call_outputへ、HTML全文が渡った。
HTMLの本文抽出や必要な部分の選択はこの経路にない。

I-09で測定した本文のDB共有・入力ownerの保持heapとは別の問題である。
保存内容をdigestで共有しても、実際にモデルへ送るtool結果と会話の情報量は減らない。

## 関連課題：ターン中のcontext予算・送信選択

2026-10-11、利用者の指摘を受け、旧HenjiのIncrement 221と現行sourceを確認した。
利用者の指示により、この差分も後続課題として記録する。hjhへの採用方式・実装時期は未決定。

旧Henjiには以下の処理が実装されている。

- ターン開始時だけでなく、各モデル送信前に実adapterで変換した入力全体の量を推定する。
  選択モデルのcontext容量の既定80%、入力上限、出力予約から入力予算を解決する。
  厳密なtokenizer計数ではなく、wireのUTF-8 bytes等に基づく推定である。
- 現在ターンだけで予算を超える場合、古いassistant response・tool交換から送信対象外にする。
  tool呼び出しと対応結果は一組とし、並列toolのbatchを分割しない。
- task・受理済みsteering・最新の交換などは保持する。
  送信対象から外した内容も保存履歴には全文を残し、保存内容を切り詰めた内容で上書きしない。
- 現在ターンの切り詰めを始めた後は、そのexecution中に外した交換や過去ターンを再投入しない。
  次ターン・Session再開では、保存全文から直近完了ターンの必要部分を選び直す。

現在のhjhの`internal/agent/loop.go`では、蓄積したmessage全体を各requestへ渡しており、
この予算解決・ターン内の送信選択は未実装である。HTTP本文の扱いとは別に検討する必要がある。

旧Henjiでも、保護対象だけで予算を超える場合は`context_budget_exceeded`で停止する。
今回の4ページ取得のように巨大な結果が最新の同一batchに含まれる場合、古い交換を外すだけで
解決できるとは限らない。旧221は自動要約・コンパクションやprovider超過エラー後の自動retryを
実装範囲に含めていない。また、送信対象の削減だけでは、保存全文のDB容量やRAM保持量は減らない。

確認した参考資料（旧repository：`/home/agent/projects/henji-harness`）：

- `docs/increments/increment-221.md`：第3・4節の予算、保持単位、選択順序、全文保存と限界。
- `v0/agent/session/context_budget.ts`：`resolveContextBudget`、`contextCost`。
- `v0/agent/worker/context_projection.ts`：交換単位の構成と保持対象。
- `v0/agent/worker/worker_runtime.ts`：`projectParentRequestWithSources`の送信選択。
- `v0/agent/core/loop.ts`：各モデル送信前に上記projectorを呼ぶ経路。

## 後で検討すること

- 各モデル送信前のcontext予算と、現在ターン・過去会話の送信選択。
  旧Henjiを参考に、Goの既存の状態所有・保存・request構築経路に合わせて検討する。
- Webページ取得時の本文抽出、README等のテキスト取得、JS内で必要部分だけをモデルへ返す経路。
- 生HTTP取得APIと、モデルが調査に使うWeb取得toolの役割分担。
- 保存する取得証拠とモデルへ渡す情報の対応、DB・メモリ・実request量の確認。

自動切捨て、固定文字数上限、自動要約などの方式は採用していない。
対応時に今回の4ページ取得を実例として、調査を完了できる返却内容と実request量を確認する。
