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

このプロジェクトは現在開発途中です。ビルドで得られる実行ファイルは
`hjh`と`hjh-tui`の2つで、動作確認はLinux/amd64で行っています。

## 主な機能

- 対話型TUIとCLIによるタスク実行、セッションの再開。
- OpenRouter、OpenCode Go、OpenAI API、ChatGPT OAuthへの接続。
- ファイル・コマンド操作、Webページの読取り、ExaによるWeb検索。
- 子agentの並列実行、途中報告、追加指示。
- 編集可能なJavaScriptツール・hook、role、workspaceの指示ファイル、skills。

## 使い始める

Go 1.27以降でソースからビルドします。依存関係は[go.mod](go.mod)を参照してください。
本文検索はripgrep（`rg`）があれば優先し、なければGNU grepを使います。
Git確認にはGitが必要です。

```sh
git clone https://forge.harakara.site/littleisland/hjh.git
cd hjh
go build -o dist/hjh ./cmd/hjh
go build -o dist/hjh-tui ./cmd/hjh-tui
./dist/hjh-tui
```

TUIは必要に応じてローカルのCoreを起動し、起動済みなら再利用します。
`hjh`と`hjh-tui`は同じディレクトリに置いてください。
`./dist/hjh-tui --continue`で同じworkspaceの最新セッションを再開できます。

TUIの`/login`で認証を設定し、`/provider`と`/model`で接続先とモデルを選びます。
既定の接続先はOpenRouterです。依頼を入力し、Enterで送信します。
`/`でコマンド候補を開き、上下で選択・Tabで補完・Enterで確定できます。

| コマンド | 操作 |
| --- | --- |
| `/sessions` `/new` `/rename` | セッションを開く・始める・名前を変える |
| `/resume` | IDまたは`latest`のセッションを再開、実行中ならその実行に追従する |
| `/model` `/provider` `/effort` | 次の実行のモデル・接続先・推論エフォートを選ぶ |
| `/login` `/auth` | 認証を設定する、認証状態を表示する |
| `/roles` `/role` | roleとツール一覧の表示、次のroleの選択 |
| `/history` `/executions` `/inspect` | 会話と保存済み実行を確認する |
| `/inputs` `/tools` `/tool` | 保存済みの追加指示・tool呼び出しを確認する |
| `/context` `/recall` | contextの構成表示、停止した実行を次回実行で再利用する |
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

保存済みセッションをローカルで削除する場合は、CoreとCLI内の実行を先に停止します。

```sh
./dist/hjh core stop --connect http://127.0.0.1:8765
./dist/hjh sessions delete SESSION_ID
```

セッションと実行履歴、他の記録から参照されなくなった本文・構成を削除します。
削除した領域は再利用できます。DBファイル自体の縮小は自動では行いません。

起動済みCoreへ依頼し、同じセッションをTUIで開くこともできます。

```sh
./dist/hjh run --connect http://127.0.0.1:8765 "README.mdを要約して"
./dist/hjh-tui --session SESSION_ID
```

`run`が表示したセッションIDを指定してください。Coreの既定接続先は
`http://127.0.0.1:8765`です。`run`・`history`・`sessions`のほか、`auth`・`models`・
`providers`・`roles`・`inspect`、Core管理の`serve`・`core`があります。
CLIの全コマンドとオプションは`./dist/hjh --help`で確認できます。

## 設定

workspaceの設定と編集可能なツールは`.hjh/extensions/`に置きます。
生成JSからgo-quickjs stdlibのtimer・fetch・組込みmoduleを使え、標準toolの処理も外部JSが所有します。
編集済み拡張は自動置換せず、[stdlib移行時の更新手順](docs/distribution-linux.md#stdlib移行時の外部拡張更新)に従って反映します。
ツール・hook・role設定の変更は次の実行から反映されます。
履歴はローカルの`.hjh/history.sqlite3`へ保存します。
TUIで選んだprovider・モデル・effortは、再起動後も同じworkspaceの新規セッションへ
引き継ぎます。お気に入りのモデルは`~/.local/state/hjh/model-favorites/`
（`XDG_STATE_HOME`指定時は`$XDG_STATE_HOME/hjh/model-favorites/`）に保存します。

認証情報はLinuxでは`~/.config/hjh/credentials/`、`XDG_CONFIG_HOME`の指定時は
`$XDG_CONFIG_HOME/hjh/credentials/`へ保存します。APIキーはworkspaceの設定や
Git履歴へ含めません。

組込み基本指示を差し替えるには、Linuxでは`~/.config/hjh/instructions/base.md`
（XDG_CONFIG_HOME指定時は`$XDG_CONFIG_HOME/hjh/instructions/base.md`）を作成します。
role・tool・workspaceの指示は引き続き組み合わせ、編集は次の実行から反映します。

workspaceの指示は`AGENTS.md`、skillsは`.hjh/skills/<name>/SKILL.md`に置きます。
skillの説明は実行開始時に読み込み、詳しい指示は必要になったときに読みます。
Exa検索には`EXA_API_KEY`を利用できます。

- [provider設定と認証](docs/design/providers.md)（英語）
- [ツール・role・hook](docs/design/extensions.md)
- [ツール利用方針と各toolのhelp](docs/design/tool-usage-guidance.md)：
  `tool-usage.md`の差替え、`{help:true}`、編集済み設定を上書きしない更新手順。
- [開発者向けメモと実装経緯](docs/development.md)

[英語README](README.md)を正本とし、このファイルはその日本語版として更新します。

## ライセンス

[MIT](LICENSE)。依存ライブラリにはそれぞれのライセンスが適用されます。
