# henji-harness(hjh)

[English](README.md)（正本）

**hjh**は**henji-harness**の略です。henji（返事）は、日本語で「reply」を意味します。

ファイル・コマンド・Web情報を使って作業する、ターミナル用のAIエージェントです。
対話画面でもコマンドラインでも利用でき、会話履歴を保存して再開したり、
子agentへ作業を委譲したりできます。

hjhはGoで実装し、QuickJS-NGをPure Goで実装した
[go-quickjs](https://github.com/go-quickjs/go-quickjs)を組み込んでいます。
JavaScriptのツール・hook・`run_js`スクリプトを実行でき、
JavaScriptの実行にNode.jsやDenoのインストールは必要ありません。

このプロジェクトは現在開発途中です。バイナリは6種類を提供しており、
テストでの動作確認はLinux/amd64で行っています。

## 主な機能

- 対話型TUIとCLIによるタスク実行、セッションの再開。
- OpenRouter、OpenCode Go、OpenAI API、ChatGPT OAuthへの接続。
- ファイル・コマンド操作、Webページの読取り、ExaによるWeb検索。
- 子agentの並列実行、途中報告、追加指示。
- 編集可能なJavaScriptツール・hook、workspaceの指示ファイル、skills。

## 使い始める

Go 1.27以降でソースからビルドします。依存関係は[go.mod](go.mod)を参照してください。
本文検索にはripgrep（`rg`）、Git確認にはGitが必要です。

```sh
git clone https://forge.harakara.site/littleisland/hjh.git
cd hjh
go build -o dist/hjh ./cmd/hjh
go build -o dist/hjh-tui ./cmd/hjh-tui
./dist/hjh-tui
```

TUIは必要に応じてローカルのCoreを起動し、起動済みなら再利用します。
`hjh`と`hjh-tui`は同じディレクトリに置いてください。

TUIの`/login`で認証を設定し、`/provider`と`/model`で接続先とモデルを選びます。
既定の接続先はOpenRouterです。依頼を入力し、Enterで送信します。

| コマンド | 操作 |
| --- | --- |
| `/sessions` | 保存済みセッションを開く |
| `/new` | 新しい会話を始める |
| `/effort` | 推論エフォートを選ぶ |
| `/detach` | Coreを動かしたままTUIを閉じる |
| `/quit` | Coreを停止してTUIを閉じる |
| `/help` | コマンドとキー操作を表示する |

## コマンドライン

認証を設定した後、タスクを実行したり履歴を確認したりできます。

```sh
./dist/hjh run "README.mdを要約して" > summary.txt
./dist/hjh sessions
./dist/hjh run --session SESSION_ID "前の作業を続けて"
./dist/hjh history --session SESSION_ID
```

最終回答は標準出力、進捗とセッションIDは標準エラー出力へ出します。
逐次表示には`--stream`を使います。フラグは依頼文より前に指定してください。
セッションIDは一意な短縮形を指定でき、通常は8文字です。

起動済みCoreへ依頼し、同じセッションをTUIで開くこともできます。

```sh
./dist/hjh run --connect http://127.0.0.1:8765 "README.mdを要約して"
./dist/hjh-tui --session SESSION_ID
```

`run`が表示したセッションIDを指定してください。Coreの既定接続先は
`http://127.0.0.1:8765`です。CLIの全コマンドは`./dist/hjh --help`で確認できます。

## 設定

workspaceの設定と編集可能なツールは`.hjh/extensions/`に置きます。
ツール・hook・role設定の変更は次の実行から反映されます。
履歴はローカルの`.hjh/history.sqlite3`へ保存します。

認証情報はLinuxでは`~/.config/hjh/credentials/`、`XDG_CONFIG_HOME`の指定時は
`$XDG_CONFIG_HOME/hjh/credentials/`へ保存します。APIキーはworkspaceの設定や
Git履歴へ含めません。

workspaceの指示は`AGENTS.md`、skillsは`.hjh/skills/<name>/SKILL.md`に置きます。
skillの説明は実行開始時に読み込み、詳しい指示は必要になったときに読みます。
Exa検索には`EXA_API_KEY`を利用できます。

- [provider設定と認証](docs/design/providers.md)（英語）
- [ツール・role・hook](docs/design/extensions.md)
- [開発者向けメモと実装経緯](docs/development.md)

[英語README](README.md)を正本とし、このファイルはその日本語版として更新します。

## ライセンス

[MIT](LICENSE)。依存ライブラリにはそれぞれのライセンスが適用されます。
