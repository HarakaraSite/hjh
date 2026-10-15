# HTTP直接呼出しでキャッシュがヒットする条件の実測

date: 2026-10-15

## 確認できた動作

**今回のOpenAI ChatGPT OAuth経路では、固定の`session_id`または`session-id`ヘッダーを
付けると、同じ長い入力の2回目・3回目で約98.7%のキャッシュヒットを確認できた。**
SDK・中継サーバーはモデル要求の経路に使っていない。Goの`net/http`から直接送信した。
本文の`prompt_cache_key`は、この測定ではヘッダーを省略する代わりにはならなかった。

## 接続条件

- 接続先: `https://api.openai.com/v1/responses`。
- 認証: hjhで保存済みのChatGPTアカウントを通常のcredential managerで解決。
  必要なaccess tokenの更新も同managerが行った。トークンは子プロセスの環境変数で渡し、
  コマンド引数・成果物・ログには保存していない。
- model / effort: `gpt-6.1-sol / high`、要求・応答service tierは`default`。
- `store:false`、`stream:true`、`reasoning.summary:auto`。
- 約16,000入力トークンの合成データ。最終回答の指示は`OK`だけ。
- 各パターン内で同じ本文バイト列を3回送信。要求完了から次の送信までは2秒。
  別パターンでは新しい固定IDと新しい指示先頭を使う。
- 1実験内で同じ`http.Client` / `http.DefaultTransport`を共有。全23要求がHTTP/2。
- `x-codex-turn-state`は応答にあったが、後続要求へ戻していない。

## パターンの比較

表は共通の認証・Content-Type・Accept以外に加えた項目を示す。

| パターン | 本文のキー | 追加ヘッダー | 1回目 / 2回目 / 3回目のcached_tokens |
| --- | --- | --- | --- |
| key | 固定`prompt_cache_key` | なし | 0 / 0 / 0 |
| underscore | 固定`prompt_cache_key` | `session_id`、`x-client-request-id` | 0 / 15,744 / 15,744 |
| hyphen | 固定`prompt_cache_key` | `session-id`、`thread-id`、`x-client-request-id` | 0 / 15,744 / 15,744 |
| underscore-only | **なし** | **固定`session_id`だけ** | 0 / 15,744 / 15,744 |
| hyphen-only | **なし** | **固定`session-id`だけ** | 0 / 15,744 / 15,744 |
| client-only | なし | 固定`x-client-request-id`だけ | 0 / 0 / 0 |

ヒットした要求の入力は15,950または15,952トークンで、15,744トークンがキャッシュから
読まれた。固定のセッションヘッダーだけでも新しいIDで再現し、本文のキーや追加の
`x-client-request-id`は今回の最小パターンには必要なかった。

## 本文を固定したヘッダー有無の切替

比較パターン間で指示先頭が変わる影響を除くため、最後に**完全に同じ本文**を5回送り、
`session_id`の有無だけを交互に変えた。本文の`prompt_cache_key`も全5回同じ固定値。

| 要求 | 固定session_idヘッダー | 入力 | cached_tokens | 応答のprompt_cache_key |
| --- | --- | ---: | ---: | --- |
| 1 | あり | 15,950 | 0 | 固定IDと一致 |
| 2 | なし | 15,950 | 0 | 別のUUID |
| 3 | あり | 15,950 | 15,744 | 固定IDと一致 |
| 4 | なし | 15,950 | 0 | 要求2とも異なるUUID |
| 5 | あり | 15,950 | 15,744 | 固定IDと一致 |

本文のSHA-256は全5回同一:
`371e3e9dfd885b42dbc6a24cbcc9e6bc3c81be5ffde3df59b8ebbc5b790d8e0f`。
正確なハッシュは[測定JSON](direct-cache-patterns.json)の`toggle_body_sha256`を正本とする。

応答の`prompt_cache_key`は、ヘッダーなしでは本文に送った固定値とは異なる値になった。
ヘッダーありでは固定IDが返り、再送でヒットした。この違いは公開仕様からの推測ではなく、
要求本文・送信ヘッダー・応答メタデータ・usageで観測したもの。
サーバー内部でどのようにキーを生成・格納しているかまでは、この測定では確定しない。

## 再現

[probe](../../spikes/cache-probe/README.md)をビルドし、同じ認証を環境変数に設定済みの状態で実行する。

```sh
cd spikes/cache-probe
go build -o /tmp/hjh-direct-cache-probe .

# 本文キーなし、固定session_idヘッダーだけで3回直接送信する。
/tmp/hjh-direct-cache-probe -direct-patterns underscore-only -out /tmp/cache-session-id-check

# 本文を固定し、session_idのあり・なしだけを交互に変える。
/tmp/hjh-direct-cache-probe -direct-patterns toggle -attempts 5 -out /tmp/cache-header-toggle-check
```

`-direct-patterns`経路はSDK clientを生成する前に分岐し、`http.NewRequestWithContext`と
`http.Client.Do`でモデルを直接呼び出す。本文ハッシュと認証値を除く送信ヘッダーは
プロセス内で観測する。追加のHTTP hopはない。

## 範囲と成果物

- この測定が示すのは、上記認証・接続先・モデルでの動作。API-key認証・OpenRouter・
  別プロバイダーへ同じヘッダーを一律に適用する根拠にはしない。
- 長い静的入力の再送を確認した。履歴が増えるケースとhjh本体への組込みは、この直接試験の時点では未実施（後述の組込み確認で実施）。
- 動くセッションヘッダーのパターンが見つかったため、明示的breakpointや保持時間の
  パラメータを使う実要求は行っていない。
- 全要求で`cache_write_tokens`は0だった。後続で15,744トークンのreadを確認できているので、
  今回の経路ではwrite欄の0だけで「キャッシュが書かれていない」と判断できない。
- 実要求23回、全件HTTP 200・最終本文`OK`。
  累計入力366,874、cached input 157,440、出力115トークン。
- build・`go vet`・通信なしのdry run・実要求の本文同一性とusageの集計を確認した。
  この直接試験ではhjh本体・通常workspaceの設定・DBは変更していない。
- [要求ごとのヘッダー・ハッシュ・応答キー・usage](direct-cache-patterns.json)
- 一時成果物: `/tmp/hjh-direct-cache-patterns-cqg17h3h/`。
  `identity/`、`minimal/`、`toggle/`に実送信本文と結果を保存。

## hjh本体への組込み確認

`openai-chatgpt`の標準設定と通常workspaceの設定に
`"sessionHeader": "session_id"`を追加した。既存の送信機構が、保存済みの完全なhjh
セッションIDをヘッダーに載せる。ターンや再開で同じIDを使う。
この時点では子agentにもrootのIDを渡していたが、後述の変更で子ごとに分離した。
他providerの設定は変更していない。

独立したworkspaceとDBで、実際のCoreから同じ認証・モデルへ直接接続した。
3ファイルを順番に全文読む4回の要求と、同じセッションの次ターン1回を確認した。
中継・SDKの追加はなく、開始時hookも使っていない。

| 要求 | 入力トークン | cached_tokens |
| --- | ---: | ---: |
| ファイル読取り1 | 7,369 | 0 |
| ファイル読取り2 | 12,221 | 7,168 |
| ファイル読取り3 | 17,064 | 12,032 |
| 最終回答 | 21,910 | 16,896 |
| 次ターン | 21,943 | 21,760 |

合計80,507入力トークン中57,856がcached inputで、**71.9%**。
次ターン単体では**99.2%**。各要求は前回のinputを先頭から維持しており、最終回答は
`CACHE-314`、次ターンは`OK`で正常終了した。
以前のヘッダーなしの同様の試験は合計3.5%だった。モデル応答まで完全同一の比較ではないが、
上記の本文固定切替試験と合わせ、通常の履歴増加でも設定が機能することを確認できた。
これはこの短い測定での比率であり、実利用全体で同率になる保証ではない。

既存の設定ファイルは自動インストールで上書きされないため、このworkspaceの設定は明示的に更新した。
Goコード・通常DBの変更やCoreの再起動は行っていない。
[集計JSON](session-header-integration.json)と一時成果物
`/tmp/hjh-session-header-check-ufn6j8z9/`に確認結果を保存した。

## 子agentごとの送信用IDの分離

子のコンテキストは親と独立しているため、子を準備するときに既存の実行IDを先に発行し、
そのIDをprovider接続の送信用セッションIDとして使うよう変更した。
`run_agent`と`spawn_agent`は同じ準備処理を通る。
同じ子のモデル要求・リトライ・追加指示後の継続では同じIDを使う。
履歴のSession相関や保存セッション数は変更しない。
この分離は`sessionHeader`を設定したproviderに適用される。

HTTP送信を観測する統合テストで、同期の子と非同期の子がそれぞれ実行IDを送り、
親と再開後の親は元のSession IDを送ることを確認した。
子の503後のリトライ、ツール実行後、追加指示後でもIDが変わらないことを確認した。
関連する子の並列実行・モデル選択・追加指示・再開の既存テストも通過した。

さらに独立workspaceから実プロバイダーへ接続し、親→explorerの子→親→次ターンを実行した。
子は3ファイルを読み、親へ`CACHE-314`を返し、親の次ターンは`OK`で終了した。

| 要求 | 入力トークン | cached_tokens |
| --- | ---: | ---: |
| 親・子の起動 | 7,387 | 0 |
| 子・読取り1 | 4,560 | 0 |
| 子・読取り2 | 9,412 | 4,480 |
| 子・読取り3 | 14,282 | 9,216 |
| 子・最終回答 | 19,155 | 14,080 |
| 親・子から復帰 | 7,576 | 7,296 |
| 親・次ターン | 7,609 | 7,424 |

子自身の継続でキャッシュがヒットし、親へ戻った要求でも**96.3%**がヒットした。
この試験はID分離後の機能確認であり、親子同一IDとの優劣を比較する試験ではない。
測定結果は[集計JSON](session-header-integration.json)の`child_identity_check`、
一時成果物は`/tmp/hjh-child-session-check-pm__0e8y/`に保存した。
