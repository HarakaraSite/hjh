# 最初のTUIの実装・確認結果

> **後続変更の案内（2026-10-16）**：以下は初版TUIでの実行記録。同一process・Esc停止・Ctrl+C終了・旧viewportは現在の操作仕様ではない。後続の[Core/TUI分離](i05-core-tui-separation.md)・[Session管理](i07-session-management.md)・[現行TUI契約](../design/tui.md)を参照。

2026-10-10、Linux/amd64、Go 1.27.2。
利用者が選んだ「同じprocessで共通Go操作を呼ぶ」方式で `hjh tui` を実装した。
[計画](../plans/first-tui.md)、[操作・責務](../design/tui.md)、
[共通操作](../design/run-api.md)を参照する。

## 成立した動作

日本語・複数行の依頼、model本文の逐次表示、tool／hook／host進捗、scroll、
実行停止、実行中のdraft保持、Session一覧からの選択と再開、新規会話、model選択、
採用会話と実行証拠の範囲閲覧を追加した。
`/history` は完全turn、`/inspect` はordinalの両端を含む範囲を指定できる。
Ctrl-C／`/quit`／外部context終了ではbackendをcancel・joinしてからDBを閉じる。

TUIは入力・表示・実行contextを所有する。DB・provider・VMを直接扱わず、
`app.Service` の実行・queryをCLIと共用する。credentialの参照は専用moduleに移した。
UI表示stateの変更はBubble Teaのevent loopに限定し、非同期結果はjob IDとSession generationで対応する。
保存schemaは変更していない。DDLのSHA256は引き続き
`6f7cf24c97ca4afe50ba6ab9de25cf8a1e3eb6ece364464af0bb0ae95e2b0eef`。

## 実端末とOpenRouter

production binaryを120×40と80×24のPTYで操作した。モデルは利用者指定の
`deepseek/deepseek-v4.1-flash`。credentialは旧Henjiと同じ保存先から読み、値は引数やpromptへ渡していない。
ANSI画面の記録とDB・実ファイル・保存messageを照合した。
記録用のPython terminal decoderはwide文字とCSI scrollの扱いを補正した。

Sessionは `395813b3-f324-486c-9317-f45256c2ea57`。
ローカル証拠は `dist/product-tui-live-accepted/` に保存した。

| 操作 | execution | 確認した結果 |
| --- | --- | --- |
| 日本語の2行依頼、生成JSによるfile加工 | `374014a7-e04d-4e68-a430-0709b92e5811` | 3行に `tui: ` を付けて書き、読み戻し。本文が実行中に表示され、正常会話へ採用 |
| 待機commandのEsc停止 | `43bb3e5b-1b80-490e-a6d0-54f4eed556a0` | ready file作成後のsleepを停止。終了後に作る予定のfileは作られず、取消・非採用として保存 |
| 停止前に編集したdraftを続けて送信 | `6ec27329-2992-4a3f-a0a0-7c9274c98f26` | `継続できた` を末尾へ追加して4行を確認。同じSessionのturn 2へ採用 |
| 別起動から復元、表示中のCtrl-C終了 | `448c49c0-17fc-41fd-ae38-ee3ac166555b` | 保存会話と最後の回答を閲覧。長い回答の途中で終了し、取消・非採用として保存 |
| 静的配布物のTUIから追加のfile確認 | `045f3b0e-9a2f-45bd-8cdd-a176ac584e8a` | 4行・末尾を確認し `package-verified.txt` を作成。turn 3へ採用、保存build hashが配布binaryと一致 |

最初の意味のある本文表示は起動から3.214秒。その時点でexecutionはactiveだった。
Ctrl-C終了の確認では0.036秒でprocessが終了し、取消のsettleと端末のalternate screen解除を確認した。
この値は今回の観測値であり、一般的な速度保証ではない。

実行中のEnterでdraftが消えず、Esc後も同じdraftを送れた。
Session picker、`/history 1 1`、`/executions`、`/inspect ID 0 5`、PgUp／PgDownによる閲覧も確認した。
再開時のviewportは会話先頭から始まり、PgDownで最新回答まで移動できる。
初回2つの正常turnだけが採用会話に入り、2つの取消は実行証拠として保持された。
配布物からの追加作業後は正常turnが3つになった。

## Reviewと回帰確認

独立した通常reviewはTUIの非同期接続、停止・終了、Session切替、model選択、共通操作、認証参照を対象にした。
一般的なsecurity reviewや未変更runtime／providerの再監査は行っていない。

初回reviewのP2を2件修正した。

- 保存Sessionを別workspaceから選び、その後 `/new` すると表示と実作業先が異なる問題。
  現在workspaceをrun開始時に捕捉し、共通操作へ呼出しごとに渡す。
- 保存modelが暗黙のoverrideとなり、現在configより優先される問題。
  表示modelと明示選択を分け、明示しなければ共通のconfig→保存値の順を維持する。

親の実画面確認で、model_resultの全文JSONが回答を重複表示し、画面を埋める問題も修正した。
live進捗は名前・phase・step・errorを表示し、全文payloadは `/inspect` で閲覧する。
修正箇所の一回のre-reviewは追加findingなし。

変更したproduct動作に対応するTUI／CLIのfocused testとrace検査、
実SQLite・provider adapterを通るServiceのfocused race test、credential参照のfocused test、
変更packageの `go vet`、format確認を実施した。既存runの最終回答・stream出力の確認も通過した。
全repository gateは繰り返していない。reviewerはsourceとtestを読み、実APIとPTY確認は親が担当した。

## 配布物と確認範囲

archiveは `dist/packages/build-3pk9413_/hjh-linux-amd64.tar.gz`。

- archive：13,065,867 bytes、SHA256 `ed3d97d71751b9f18fb702077f6f46c30adfc5391eac5b6c56941e8cbc1c4ba7`
- binary：23,586,928 bytes、SHA256 `820b68659717b381d00ddabe489b8ffdd8d231827276f2bda500c2d9adbf38c9`
- `CGO_ENABLED=0`、Linux/amd64、GOAMD64=v1。ELF interpreter不要
- Go依存28 moduleのlicense資料を同梱。manifestに記載した全file hashを照合

配布binary・同梱configでTUIを起動し、実providerによる作業とSession再開・履歴閲覧を確認した。
TUI追加後の最小chroot内での実端末操作は未確認。最小Linux環境での非対話runは
[初期run](initial-run.md)で確認済みである。

TUI証拠・workspace・配布fileに実credentialが含まれないことを確認した。
HTTP独立client、並列Session実行、子Agent、複数provider、旧DB移行、他OS、release公開は今回の対象外。
