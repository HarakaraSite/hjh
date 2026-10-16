# 後続課題：HTTPの生レスポンス全文によるcontext超過

2026-10-11。利用者が実利用で報告した。原因を保存済みrequestとtool結果で確認済み。
当時は利用者の指示により対応を後回しとし、記録のみとした。以下の観測と旧Henji比較は当時の記録を保持する。

## 現在の状態（2026-10-16 source照合）

- 各モデル送信前のcontext予算解決と、現在turn・過去履歴の送信選択はI-14以降で実装済み。
  `internal/agent/loop.go`のProjectRequestから`internal/app/execution.go`の
  `contextbudget.Projector`へ接続される。
  [計画](../plans/i14-web-context.md)・[現行契約](../design/i14-web-context-contract.md)・
  [結果](../results/i14-web-context.md)を参照する。
- Web調査にはHTML本文を可読化する`web_fetch`を実装済み。
  通常読取りは現在64 KiBまでで、`save_to`では元response bytesをfileへ保存し本文を会話へ返さない。
  [既定値整理の計画](../plans/web-retrieval-defaults.md)・[結果](../results/web-retrieval-defaults.md)を参照する。
- 一方、生`http`の`api.http()`は現在も`internal/host/host.go`の`io.ReadAll`で本文を全文取得する。
  生HTTP全文をそのままtool結果へ返す経路の巨大入力リスクは残る。
  巨大結果が最新の保護対象交換に含まれる場合、古い交換の除外だけで収まるとは限らず、
  保護入力も予算を超えれば送信前の`context_budget_exceeded`になる。

予算・Web読取りの実装済みと、巨大な生HTTP結果の問題が全面解決したことは区別する。
元Sessionの4ページ取得を再実行して調査完了を確認したものではない。

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

## 観測当時の原因と経路（2026-10-11）

外部tool `.hjh/extensions/index.js`の`http`はGo基礎API `api.http()`の
`{status, body}`をそのまま返す。標準例`internal/extensions/assets/index.js`も同じである。
Go処理は`internal/host/host.go`にあり、HTTPの生本文を取得する。
tool結果→Agentのtool message→Responsesのfunction_call_outputへ、HTML全文が渡った。
HTMLの本文抽出や必要な部分の選択はこの経路にない。

I-09で測定した本文のDB共有・入力ownerの保持heapとは別の問題である。
保存内容をdigestで共有しても、実際にモデルへ送るtool結果と会話の情報量は減らない。

## 当時の関連課題：ターン中のcontext予算・送信選択（2026-10-11）

2026-10-11、利用者の指摘を受け、旧HenjiのIncrement 221と現行sourceを確認した。
当時の利用者の指示により、この差分も後続課題として記録した。この時点ではhjhへの採用方式・実装時期は未決定だった。

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

調査当時のhjhの`internal/agent/loop.go`では、蓄積したmessage全体を各requestへ渡しており、
予算解決・ターン内の送信選択は未実装だった。この差分は後続I-14で対応済みである。
生HTTP本文の全文取得が残ることとは別に扱う。

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

## 当時の検討候補（2026-10-11の記録）

以下は当時挙げた候補である。送信予算・選択とWeb読取りは後続実装済みで、
現在の未実装一覧ではない。生HTTPの扱いと元事例の再確認の留保は冒頭を参照する。

- 各モデル送信前のcontext予算と、現在ターン・過去会話の送信選択。
  旧Henjiを参考に、Goの既存の状態所有・保存・request構築経路に合わせて検討する。
- Webページ取得時の本文抽出、README等のテキスト取得、JS内で必要部分だけをモデルへ返す経路。
- 生HTTP取得APIと、モデルが調査に使うWeb取得toolの役割分担。
- 保存する取得証拠とモデルへ渡す情報の対応、DB・メモリ・実request量の確認。

この記録時点では、自動切捨て、固定文字数上限、自動要約などの方式は採用していなかった。
後続の送信projection・Web受信上限は前述の契約、opt-in要約は
[JSコンパクション](../design/js-compaction.md)を参照する。
対応時に今回の4ページ取得を実例として、調査を完了できる返却内容と実request量を確認する。
