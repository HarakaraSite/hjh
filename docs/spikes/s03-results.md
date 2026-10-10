# S-03結果：JSライブラリ・Go API・tool／hook編集と依存配布

2026-10-10、Linux/amd64、Go 1.27.2で実施。[計画](s03-plan.md)の具体的経路で成立した。
実HTTPのYAMLをJSライブラリで解析し、候補バージョンを照合して、Go APIでJSONを保存・読み戻せた。
tool・hookのソース編集は本体再buildなしで次Workerへ反映され、準備済み依存を含む配布コピーも実行できた。

成立したのは、今回の2ライブラリと、準備済みES module方式、外部tool／hookの実験契約まで。
全npm互換、package解決方式の採用、最終Go API・hook契約、S-05の配布条件確定を意味しない。
[再現手順](../../spikes/s01/S03.md)と[実験コード](../../spikes/s01/README.md)を参照する。

## 実処理と実行証拠

| 利用経路 | 観測 | 記録 |
| --- | --- | --- |
| YAML → version照合 → file保存・読戻し | YAMLのanchor／aliasを解析。`^1.2.0` に1.4.2は適合、2.0.0は不適合。prereleaseを含む範囲にも照合し、最終候補はaccepted／prerelease。JSON 286bytesを実ファイルへ保存し、Go経由の読戻しと照合。 | [view](s03-artifacts/normal-view.json)、[HTTP入力](s03-artifacts/manifest.yaml) |
| 内側toolのhook・履歴 | 親 → workflow → catalog → http → Go HTTP、workflow → writeFile／readFile → Go操作を保存。全ツールのbeforeTool／afterToolを記録。catalogのbeforeToolはGo APIでYAML設定を読み、引数へnoteを追加。afterToolは結果へhookRevisionを追加。40event。 | [view](s03-artifacts/normal-view.json) |
| toolの次Worker反映 | 同一プロセス内の次WorkerでtoolRevisionがtool-one → tool-two。hookはhook-oneのまま。 | [編集前](s03-artifacts/edit_before-view.json)、[tool編集後](s03-artifacts/edit_tool-view.json) |
| hookの次Worker反映 | さらにhookを編集し、noteがprepared:hook-two、hookRevisionがhook-twoになった。実行ファイルのSHA256は変更なし。 | [hook編集後](s03-artifacts/edit_hook-view.json) |
| ライブラリの失敗 | 実HTTPから不正YAMLを返し、YAMLParseErrorがcatalog・親まで届いた。HTTP成功は保存され、失敗後のreportは書かれなかった。 | [view](s03-artifacts/yaml_error-view.json) |
| hookの失敗 | catalogのbeforeToolを編集して例外を発生させ、hook・tool・親の失敗を保存。catalogのHTTP処理は実行されなかった。 | [view](s03-artifacts/hook_error-view.json) |
| 配布コピー | 別ディレクトリのbinary・外部JS・vendorだけからWorkerを起動。PATHにNode／npm／curl／OSコマンドを置かず、HTTP・file・SQLiteの実経路が成立。配布コピーのtool／hook編集後も、同じbinaryで次Workerへ反映。 | [編集前](s03-artifacts/distributed_before-view.json)、[編集後](s03-artifacts/distributed_edited-view.json)、[report](s03-artifacts/distributed_edited-report.json) |

親へ返す集約結果には候補名とreport、内部callにはHTTP本文、hookの入力・結果、file操作の結果を保存した。
hookで変更した引数はhook結果として残り、実際のGo呼出しには変更後の値が渡っている。
全Workerで返却後のGo処理数は0。履歴はWorker終了後、新しい読み取り専用接続で読み戻した。
配布Workerは別プロセスで実行し、その履歴とreportを親実験側へ保存した。

## 依存方式で確認できたこと

| 依存 | 準備と実行 | 成立範囲 |
| --- | --- | --- |
| yaml 2.9.1 | published browser ESMの相対importを含む木をvendorへコピー | parse、anchor／alias、実parse error。loaderは相対パスを辿れた。 |
| semver 7.8.5 | CommonJSをesbuild 0.28.2のneutral ESMへ変換 | valid／satisfies、通常versionとprerelease範囲の照合。 |

利用APIはライブラリの[公式YAML文書](https://eemeli.org/yaml/)と
[node-semverの公式README](https://github.com/npm/node-semver)を確認した。
CommonJSの変換方式は[esbuildのbundle・format・platform契約](https://esbuild.github.io/api/)に従った。
Node固有機能やnative addonを、bundleによって自動的にQuickJSへ移せると判断したものではない。

取得・準備時はNode v24.21.0、npm 12.0.2を使用。
[package-lock.json](../../spikes/s01/s03-deps/package-lock.json)にversion・取得URL・integrity、
[vendor manifest](../../spikes/s01/s03-tools/vendor/manifest.json)に78file・380539bytesのhashを保管した。
これには実行用JSのほかlicense、診断用raw CommonJS、bundle metadataも含む。
両JSライブラリのISC licenseと[semver bundleの入力・出力](../../spikes/s01/s03-tools/vendor/semver/build-meta.json)を同梱している。

`npm ci` から準備をやり直し、vendor manifestのSHA256が同一だった。
[取得・再準備の記録](s03-artifacts/dependency-reproduction.json)を参照する。
Node／npmはこの方式では取得・準備用であり、prepared vendorを使うWorkerの実行用ではない。
tool・hookはbundleの外へ置いたため、ソース編集だけで変更できる。
ライブラリのversionやbundle内容を変える際は、lock・vendorの再準備と配布が必要になる。

## 実測したAPI・moduleの境界

| 対象 | 今回の観測 | 目的への影響 |
| --- | --- | --- |
| 相対ESM import | yamlの複数fileとtoolの相対importが動作 | prepared treeを外部fileとして置ける。 |
| bare specifier `yaml` | loaderがローカルの `yaml` fileを探して失敗 | package.json exports、npm package解決を自動では行わない。今回の明示相対パスか、別途resolverが必要。 |
| raw CommonJS | importは失敗。bodyを関数内で直接実行するとrequire未定義 | 今回はESM変換で解消。一般CommonJS対応は未採用。 |
| `node:fs` | moduleを読み込めない | 実ファイル処理はGoのreadFile／writeFileで実現した。Node fsの互換提供とは別。 |
| fetch・URL・TextEncoder・Intl・process・require・Buffer・setTimeout | 全部undefined | これらに依存するコードは、そのままでは動かない。必要に応じたGo APIまたはJS側の対応が要る。 |

[global APIの記録](s03-artifacts/globals-view.json)、[bare import](s03-artifacts/bare_yaml-view.json)、
[raw CommonJS import](s03-artifacts/raw_commonjs-view.json)、[CommonJS body](s03-artifacts/commonjs_body-view.json)、
[node:fs](s03-artifacts/node_fs-view.json)を保管した。
これは今回のVMとloaderの観測であり、ライブラリを将来拒否する仕様ではない。
未観測の依存やpolyfillの組合せについては、互換・非互換を確定しない。

今回追加したGo APIはテキストfile read／writeだけ。HTTPとプロセスはS-01のものを共有した。
`Intl` を使う日時整形、binary・stream、Node固有機能、native addon、Goの新しい専門ライブラリなどは
今回公開していない。実ツールが必要とする場合に、Goの操作追加・対応JS依存・外部プログラム利用のどれが適切かを判断する。

## 発見した未修正の問題

未変換CommonJSのimportでは、保存されたエラーが `require` ではなく
`ReferenceError: '__tools' is not defined` になった。
同じpublished CommonJS bodyをtoolの関数内で実行した診断では、`require` 未定義を直接観測した。

現在の [module起動経路](../../spikes/s01/internal/jsruntime/worker.go) はbootstrapのmodule評価の戻り値を取り出さず、
pending jobs処理後に生成JSを開始する。この経路ではmodule初期化失敗の原例外を保持できず、
ツール定義の初期化が終わっていないことを後続の `__tools` 参照エラーとして報告する場合がある。
利用者は失敗したmoduleの原因を直接確認できない。

修正案はmodule評価の完了・rejectionを確認して原例外を返してから、生成JSの開始へ進むこと。
今回のprepared ESMによる成功経路には影響しなかった。計画外の既存runtime不具合として記録し、修正は行っていない。
ライブラリ関数内の失敗とhook関数内の失敗は、今回の観測では原例外を保持した。

## 検証と残る判断

- 通常実行：6シナリオが成立。[summary](s03-artifacts/summary.json)、[SQLite](s03-artifacts/history.sqlite)。
- race detector付き実行：同じ6シナリオが成立、race報告なし。[summary](s03-race-artifacts/summary.json)。
- 共有runtime変更後のS-01：16シナリオ成立。[summary](s03-regression-artifacts/s01/summary.json)。
- 共有runtime変更後のS-02：7シナリオ成立。[summary](s03-regression-artifacts/s02/summary.json)。
- `go vet ./...` 成功。依存再準備は同一manifestを生成した。

配布コピーの場所とbinary hashは各summaryに記録した。配布binaryは今回の環境のlibcへ動的リンクしている。
[実際のbuild環境・linkの記録](s03-artifacts/build-environment.json)を保管した。
Linux上のコピー実行と依存の同梱は確認したが、他OS、異なるlibc、インストール体験、正式な配布方式は未確認。
S-05の対象OS合意と実環境での配布検証は残る。

S-03の採否判断に使える根拠は、ESM依存の相対tree、変換可能なCommonJS、追加Go API、
bundleへ含めないtool・hookの編集、prepared vendorの同梱が実際に機能したことである。
この2ライブラリだけから、将来必要となる全ライブラリが使えるとは判断しない。
次はS-04の会話採用、使用構成の追跡、Session閲覧・再開、必要範囲の履歴読込みを具体化する。
