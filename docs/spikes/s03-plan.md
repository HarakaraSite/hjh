# S-03：実ライブラリ・Go API・外部tool／hookと依存配布

## 利用経路と目的

利用者は外部tool・hookを編集し、生成JSからツールを合成して実処理を行いたい。
READMEのS-03と引継ぎ議論のライブラリ・編集可能性を根拠とし、
HTTPのYAML依存一覧を解析し、候補バージョンを照合してJSONレポートを保存・読み戻す経路で検証する。
Node／Deno互換や任意のnpm packageを無加工で動かすことは要求しない。

現行経路は `cmd/s01・s02 → jsruntime → 外部index.js → api → host`。
VM・moduleの寿命はWorker、親子eventはtrace、SQLiteはhistoryが所有する。
今回もこの経路を共有し、module読込みや実行処理のコピーを作らない。

## 実験で選ぶ方式

- JSライブラリは `yaml 2.9.1` と `semver 7.8.5`。取得versionとintegrityをnpm lockへ固定する。
- yamlのbrowser ESMを相対依存ごと外部vendorへコピーする。
- semverのCommonJSは `esbuild 0.28.2` でESMへ変換する。tool・hookソースはbundleへ含めない。
- npmとNodeは取得・準備時に使用する。prepared vendorを置いたWorkerでは使わない方式を試す。
- toolはYAML解析とsemver判定、Goは既存HTTPと追加のテキストファイルread／writeを担当する。
  ファイルAPIはこの具体的経路に必要な操作だけを公開する。permissionやサイズ上限は新設しない。
- `beforeTool` は呼出し引数、`afterTool` は返却結果を変更する実験契約とし、任意の外部hook moduleを
  Worker起動時に読み込む。内側の登録ツールも同じhook・履歴経路を通す。
- traceはhookをtoolの子callとして記録し、Go操作は呼び出したtool／hookの子として保存する。
- ソース編集は一時コピーで実施し、同じバイナリの次Workerでtool・hookそれぞれの変更を観測する。

この選択はS-03の実験用であり、製品のpackage解決・hook契約・全公開APIを確定しない。

## 確認する動作

1. 実HTTPのYAMLをESMライブラリで解析し、CJS由来のsemverで判定し、Go APIで書いたJSONを読み戻す。
   集約結果、ライブラリ処理の結果、内側toolとhookの親子eventをSQLiteで確認する。
2. toolの振る舞いを変更後、hookを変更し、同じ実行ファイルの次Workerへそれぞれ反映されることを確認する。
3. 実ライブラリのYAML解析失敗とhookの例外が、親JSと履歴へ届くことを観測する。
4. bare npm specifier、raw CommonJS、Node builtin importとWorkerのglobal APIを実測し、
   今回のloaderと実行環境で使える範囲・必要な変換やGo APIを説明する。
   これらは環境の観測であり、将来の拒否仕様や制限の採用ではない。
5. binary・editable tool／hook・prepared vendor・license・依存manifestだけを別ディレクトリへ配置し、
   PATHからNode／npmを外した別プロセスで実行する。外部ソース編集後の次Worker反映も確認する。
   依存取得の再現性とbundleの内容はlock・metadata・hashへ記録する。

## 成立範囲と成果物

ローカルの実HTTP／ファイル操作、2種類のJS依存、toolとhookの編集、準備済み依存を含むLinux配布コピーを対象とする。
モデルAPI、全JSライブラリ互換、実行中ストリーム、全使用構成の追跡、Session再開、他OSは今回の対象外。
配布コピーの実行はS-03の依存方式を確かめるためのもので、S-05の対象OS合意や全配布条件の代替ではない。

計画、再現可能な取得・準備script、実験CLI、固定依存・license、SQLiteと読戻し・副作用・配布観測、
成立範囲・未確認事項を結果文書へ残す。共有runtime変更に対応するS-01／S-02の実経路を最終候補で一度確認する。
