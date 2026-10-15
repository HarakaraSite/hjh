# SDK / direct HTTP cache probe

公式 Go SDK (`openai-go/v3` v3.78.0) と `net/http` による Responses API の
直接呼出しを比較する小さな独立プログラム。hjh 本体への SDK 採用ではない。
ルート module の依存関係・provider 実装は変更しない。

## 比較条件

- 同じ access token、接続先、model、effort、`store:false`、`service_tier:default`。
- 同じ長い合成データを、両経路とも入力メッセージ配列で送信する。
- 同じ `http.Client` と `http.DefaultTransport` を共有する。中継サーバーは使わない。
- SDK の自動リトライは無効。SDK 標準の送信ヘッダーは変更しない。
- 1組目は SDK / HTTP / HTTP / SDK、2組目は HTTP / SDK / SDK / HTTP。
  組ごとに指示の先頭へ別の識別文字列を置き、同じ組では本文を固定する。
- 各呼出し終了から既定2秒待つ。既定の実要求数は8回。

送信直前の `RoundTripper` と `httptrace` で、本文のハッシュ・実際に送った
ヘッダー・接続の再利用・HTTP バージョンを記録する。本文自体は合成データだけ。
`Authorization` 等の認証ヘッダーは値を保存しない。

JSON のキー順序が違っても同一内容か確認できるよう、実バイト列と canonical JSON の
SHA-256 を別々に保存する。内容が同一でなければ比較を停止する。
要求間で会話を増やす試験ではなく、同じ入力の繰返しに対する比較である。

## 実行

```sh
cd spikes/cache-probe
go build -o /tmp/hjh-sdk-cache-probe .

# 通信・認証なしで両経路の encode / decode を確認する。
/tmp/hjh-sdk-cache-probe -dry-run -out /tmp/sdk-cache-dry

# OPENAI_API_KEY に認証情報を設定済みの環境で直接送信する。
# 有効な既存 OAuth access token も同じ変数で渡せる。値をコマンド引数に書かない。
/tmp/hjh-sdk-cache-probe -out /tmp/sdk-cache-live
```

出力先は新しいディレクトリを指定する。`results.json` に usage・送信ヘッダー・
ハッシュ・所要時間を、`request-NN.json` に送った本文を保存する。
SDK / HTTP 別の cache rate は、`cached_tokens` の合計を `input_tokens` の合計で割る。

これは小規模な比較であり、一般的なキャッシュ率や Codex の接続方式を説明する
ものではない。キャッシュの内部状態やサーバー内の振分けは取得できない。

公式 SDK の資料: <https://developers.openai.com/api/reference/go>

## SDKを使わないヒット条件の比較

`-direct-patterns`はSDK client生成前に分岐し、モデル要求を`net/http`だけで直接送る。
同じ認証を環境変数に設定済みの状態で実行する。

```sh
go build -o /tmp/hjh-direct-cache-probe .
/tmp/hjh-direct-cache-probe -direct-patterns underscore-only -out /tmp/cache-session-only
/tmp/hjh-direct-cache-probe -direct-patterns toggle -attempts 5 -out /tmp/cache-session-toggle
```

`underscore-only`は本文のcache keyを送らず、固定`session_id`ヘッダーだけを使う。
`hyphen-only`は固定`session-id`だけを使う。`toggle`は本文を完全に同じに保ち、
`session_id`をあり・なし・あり…と切り替える。
`-attempts`はパターンごとの要求数で、既定3回。

[直接呼出しでの実測結果](../../docs/results/direct-cache-patterns.md)では、このChatGPT OAuth経路の
固定セッションヘッダーで2回目以降に約98.7%ヒットした。最初のSDK比較で両方0だった結果だけから、
必要な送信条件を判断できなかった。別の認証・接続経路については未確認。
