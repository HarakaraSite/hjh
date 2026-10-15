# 公式Go SDKとHTTP直接呼出しのキャッシュ比較

date: 2026-10-15

追試: [直接HTTPでヒットするパターン](direct-cache-patterns.md)を確認した。
この文書のSDK / HTTP比較では両方にセッションヘッダーがなく、0という結果だけでは
必要なキャッシュ条件を判断できなかった。追試では固定の`session_id`または`session-id`で
2回目以降に約98.7%のヒットを確認した。

## 結果

今回の小規模試験では、**公式Go SDK・HTTP直叩きの両方で cached_tokens は0だった**。
「SDKを使っていないためhjhのキャッシュ率が低い」という説明を支持する結果にはならなかった。
Codexで観測した80.6%との差の原因は、まだ特定できていない。

| 経路 | 成功した要求 | 累計入力 | キャッシュ済み入力 | 出力 | キャッシュ率 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 公式Go SDK | 4 | 63,768 | 0 | 20 | 0% |
| Go net/http | 4 | 63,768 | 0 | 20 | 0% |

各要求の入力は15,942トークン、出力は5トークンで、8回とも最終本文は `OK`。
返却modelは全件 `gpt-6.1-sol`、返却service tierは全件 `default`。

## 条件

- SDK: `github.com/openai/openai-go/v3` v3.78.0、Go 1.27.2。
- 接続先: `https://api.openai.com/v1/responses`。
- 認証: hjhで保存済みのChatGPT OAuthを通常のcredential managerで解決し、
  **両経路へ同じaccess tokenを渡した**。Codex側の認証との同一性を確認した試験ではない。
- `gpt-6.1-sol / high`、`reasoning.summary:auto`、`store:false`、`stream:true`、
  `service_tier:default`。ツール定義・履歴継続・cache key・明示的breakpointは付けない。
- 長い合成データを入力メッセージ配列で送る。同じ組の要求は同じJSON内容。
  SDKと直叩きではキーの並び順が違うため、本文の実バイト列自体は同一ではない。
- 1組目 SDK / HTTP / HTTP / SDK、2組目 HTTP / SDK / SDK / HTTP。
  各組の指示先頭に別の識別文字列を置き、要求間は完了後2秒待つ。
- 同一プロセス内の同じ `http.Client` と `http.DefaultTransport` を共有。
  **中継サーバー・SDK専用の別接続は使っていない**。
  全件HTTP/2で、2〜8回目は既存接続の再利用を `httptrace` で確認した。
- SDKの自動リトライは無効。SDK由来のヘッダーはそのまま送る。

## 送信内容の確認

`RoundTripper` で本文、`httptrace.WroteHeaderField` で送信ヘッダーを観測した。
ネットワーク経路に別のHTTP hopは加えていない。
`Authorization` などの認証ヘッダーの値は保存していない。

JSONを解析してキー順序を揃えたハッシュは、各組内で全4要求が一致した。
SDKが暗黙にcache keyやbreakpointを足した形跡はない。送信フィールドは両経路とも
`input`、`instructions`、`model`、`reasoning`、`service_tier`、`store`、`stream` の7項目。

送信ヘッダーには以下の差があったが、この試験ではキャッシュ使用量の差には現れなかった。

| ヘッダー | SDK | HTTP直接呼出し |
| --- | --- | --- |
| Accept | application/json | text/event-stream |
| User-Agent | OpenAI/Go 3.78.0 | Go-http-client/2.0 |
| X-Stainless-* | 言語・実行環境・SDKバージョン・retry count | なし |

## 検証範囲と制約

独立moduleのbuild・`go vet`・通信なしのdry runを実行し、実接続で両経路が要求を完了する
ことを確認した。hjh本体、通常workspaceの設定・DB、ルートmoduleの依存関係は変更していない。

SDKと直叩きの比較対象は、同じ静的入力の繰返し。会話やtool結果が伸びる場合の試験でも、
Codexの内部実装との比較でもない。8要求だけから、すべての条件でSDKに差がないとは結論しない。

準備段階では、入力を文字列で送ったSDK要求が3回HTTP 400になった。
受信本文を取得した3回目で `Input must be a list` を確認し、両経路をhjhと同じ入力配列へ揃えた。
その後の成功1回では、probeが本文を `response.completed.output` だけから読んでいたため
表示確認で停止した。この接続先は `response.output_text.delta` で本文を送っており、
probeを修正してから本計測8回を行った。これら準備要求は上の比較集計に含めない。
準備を含むHTTP要求は12回、usageを取得した成功9回の累計入力は143,480、出力は45。

## 成果物

- [probeと実行方法](../../spikes/cache-probe/README.md)
- [集計・各要求のusage・ヘッダー・ハッシュ](sdk-cache-probe.json)
- 一時成果物: `/tmp/hjh-sdk-probe-yk0yp11v/`。`final/request-NN.json` が実送信本文。

SDKの公式資料: <https://developers.openai.com/api/reference/go>
