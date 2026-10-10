# hjh S-05 Linux/amd64 spike

技術検証用CLI。LLMは呼ばず、既定の固定JSがHTTPで取得したYAMLをyaml／semverで処理し、
Goのfile APIでreportを保存する。QuickJSとSQLiteは本体に含む。

tar.gzを展開し、書込み可能な場所で以下を実行する。

```sh
./hjh create -session demo
./hjh run -session demo -turn first -url http://YOUR_SERVICE/catalog.yaml
./hjh adopt -session demo -turn first
./hjh inspect -session demo
```

YAMLサービスの入力例は `catalog.yaml`。起動にはGo／Node／npm／curl／sqlite3／qjs、
共有libc、shellを必要としない。展開にはtar.gzを扱う手段が必要。
`./hjh -h` ではなく `./hjh run -h` 等で各operationのflagを表示する。

`settings.json` のinstruction、`tools/modules/catalog.js`、`tools/hooks.js`、
`tools/hook-config.yaml`を編集後、別のturn名でrunする。本体の再ビルドは不要。
各runは現在のsourceをcaptureし、SQLiteへ保存して展開したsnapshotを使う。
以前に採用した会話はrunの入力へ引き継ぐ。runはdraftを保存し、adoptは明示操作。
inspectは会話と指定turnの構成・eventを読む。

stateの既定は本体の隣の `state/`。`-state DIRECTORY` で保存先を変えられる。
設定も本体の隣を使い、`-settings FILE` で変更できる。
`-js FILE -args FILE` で別の固定JS bodyと入力JSONを実行できる。
新しいSession名、turn名は利用者が指定する。製品のUXやschemaを確定するものではない。

HTTPはGoのnet/httpを使う。hostnameの解決には利用環境のhosts／DNS設定、
通常のHTTPSには信頼するCA証明書と正しい時刻が必要。CAは更新可能なOS資源として使う。
OSのproxy設定を使う場合はGoのHTTP実装が参照する環境変数に従う。
本体に特定環境のCA／DNS／proxy設定を固定して配布しない。

追加toolのcommand APIは指定した外部programを実行する。
`command.js` は入力のprogramを呼ぶ例で、実際に指定するprogramのインストールが必要。
S-01のshell例ならsh／tr／sleepが必要だが、HTTP・YAML／semver・file・Session処理には不要。
Node builtinやraw CommonJSはQuickJS互換ではなく、配布vendorはS-03で準備済みのESM。
取得／再準備には別途Node／npm／esbuildを使う。利用時には使わない。

`manifest.json` は配布時のfile hash、Go build情報、JS依存versionを記録する。
Goと使用Go moduleのlicenseは `licenses/`、JS依存のlicenseは `tools/vendor/` にある。
この配布物はLinux/amd64用。他architectureやLinux全distributionでの動作は未確認。
