# run --stream の追加と実provider確認

2026-10-10。利用者の「TUIはまだなくてもいいが、--stream相当は実装した方がいい」に従い、
`hjh run --stream` を実装した。[操作契約](../design/run-api.md)と[README](../../README.md)に使い方を追記した。

## 動作と経路

OpenRouterは以前から `stream: true` で接続していたが、CLIはassistant_textを表示せず、
run終了後に最終回答だけを出していた。

今回、既存の `OpenRouter → assistant_text観測 → app journal → CLI observer` へ表示を接続した。
表示状態は `cmd/hjh/output.go` が所有する。累積textの未表示分をstdoutへ書き、
新しいmodel requestで位置をリセットする。tool等の進捗はstderrへ出す。
モデルの途中本文も表示し、完了時の最終回答再出力は行わない。
改行済みの本文に余分な改行を加えず、途中行と進捗の境界には改行を補う。

`--stream` なしは従来通り最終回答だけをstdoutへ出す。
失敗・SIGINTでは表示済み本文を残し、stderrとexit codeで終了状態を示す。
provider、保存payload、会話採用の条件はこの表示flagで変えない。

```sh
./dist/hjh run --stream --model deepseek/deepseek-v4.1-flash --workspace /path/to/work 'input.txtを加工し、結果を説明して'
./dist/hjh run --stream --workspace /path/to/work --session SESSION_ID '前回の結果を追加編集して'
```

## Focused testと確認範囲

具体的な利用動作に対応する確認を行った。

- 非streamの途中text非表示、streamの差分表示、日本語／絵文字、同じprefixを持つ複数model step。
- 最終回答の末尾と改行を保持し、全文の重複表示をしない。
- httptestのSSEを最初の本文chunkで待機させ、実provider adapterとappを通し、run未完了の間に
  CLI表示writerへ本文が届くことを確認。応答を再開すると残りの本文が一度だけ届く。
- `go test ./cmd/hjh -run TestRunOutput -count=1`、`go test -race ./cmd/hjh`、
  `go vet ./cmd/hjh`、format、whitespace、静的binary buildを実施し成功。

変更されたflag解析・CLI表示と、既存providerの累積text・app observerの直列化・終了joinを
親agentが通して確認した。今回の局所変更について追加の独立reviewerは起動していない。
初期製品全体の独立reviewは[初期runの結果](initial-run.md)の範囲で実施済み。

## 実OpenRouter・CLIでの確認

modelは利用者指定の `deepseek/deepseek-v4.1-flash`。
credentialは旧Henjiと同じXDG stateの参照先をCLIから私的に読み、値は記録していない。
Linux/amd64でビルドした製品CLIを `run --stream` で起動し、stdout／stderrの到着を別々に測定した。
stdoutのread回数はOSから読み取れた回数であり、token数やSSE event数ではない。

Session：`b96d9bd5-1731-4bfe-a5eb-40af2313c927`。

| 依頼 | Execution | 実測・結果 |
| --- | --- | --- |
| 生成JSによる3行のfile加工・確認、結果説明 | `857c5c56-303e-4b6d-becb-4e0d57086175` | 最初のstdout受信3.5083秒（改行）、意味のある本文10.1791秒、終了12.3231秒。stdoutを191回読み取り、終了前に本文が逐次到着。exit 0、completed／canonical |
| 同Sessionで長文を生成し、本文受信後にSIGINT | `ba3efc2d-e71b-4bf2-8e7e-922e5b65d97d` | 本文3.0746秒から受信しSIGINT送信、3.5007秒で終了。部分本文962 bytesをstdoutに保持。exit 130、cancelled／non_canonical |

成功runは4 model step・9保存messageで、run_jsとcommandを実行した。
実fileは `stream: alpha`、`stream: beta`、`stream: こんにちは` の3行となり、
read_file／write_file等の開始・終了がstderrへ届いた。
stdout末尾が保存済み最終assistantの本文と一致し、指定した終端「STREAM確認完了」が一回だけ出たことを確認した。
取消runは表示後にモデル接続を取消し、途中本文を実行証拠へ残し、会話へ採用していない。

実ログ・時刻・inspect結果・DBはgit管理外の `dist/product-stream-live/` に保存。
`summary.json`、`success.timing.json`、`cancel.timing.json` に測定と識別子を記録した。
実ログ・DB・配布archiveにcredential値がないことも確認した。

## 更新した配布物

`python3 scripts/package-linux.py` で新しいflagを含むLinux/amd64静的配布物を作成した。
配布binaryのhashは上記実モデル確認で使った `dist/hjh` と一致する。

- archive：`dist/packages/build-56gb4h_e/hjh-linux-amd64.tar.gz`、12,056,539 bytes
- archive SHA-256：`695043571ad3ec47c5f78349fc9e76ae9df6370f5feb0c329dc37a619ded04f7`
- binary：21,209,639 bytes
- binary SHA-256：`a1c3c7386b13ff07ca91d139bc2a190b250789f4c5538242bc1f2b9c30331a63`

更新配布物のmanifest照合とhelpの `stream` flagを確認した。
今回の実provider確認はホストLinux上で行った。開発tool・共有libc・`/proc`のない最小Linuxでの
初期run検証は[前回の記録](initial-run.md)を参照する。
