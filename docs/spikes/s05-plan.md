# S-05：Linux配布・起動条件

利用者が指定した対象はLinuxのみ。今回の実機はLinux/amd64。
READMEのS-05を根拠に、配布した本体からQuickJS・SQLite・外部tool／hook・準備済みJS依存を使い、
基本HTTP、ソース編集後の次Worker、保存Sessionの再開を成立させる。

現行経路は `session → configsnapshot／jsruntime → 外部tool／hook → host → trace → history`。
S-04の固定JSとS-03のYAML／semver workflowを共有し、配布専用の実装を複製しない。

- `cmd/s05` は配布物からSession作成、単一turn実行、明示的採用、閲覧を行うCLI。
  設定・tool・hookは本体の隣、DBと構成snapshotは指定したstate directoryが所有する。
- `s05/package.py` はCGOなしのLinux/amd64本体、外部source、vendor、設定、利用メモ、
  使用依存のlicense／version／hashをtar.gzへまとめる。Node/npmは依存を作り直すときだけ必要。
- `s05/verify.py` はtar.gzから新しいrootへ展開し、非rootの利用者としてchroot内で配布CLIを起動する。
  rootにGo、Node、curl、shell、SQLite／QuickJSの共有libraryを入れず、実HTTPとSQLiteを使う。
  各起動は別プロセス。編集後の結果、binary hash、採用済み会話、旧構成、実行証拠を保存する。

確認する利用動作は、展開から初回起動、tool／hook／設定編集からSession再開、
curlなしのHTTPとHTTPS、外部commandを必要とする追加toolの利用。
HTTPSは実際のCA検証を使うローカルTLSサービスと外部HTTPSを確認する。
外部サービスの到達性は結果で区別する。DNS／CAはOSの設定であり、配布本体へ固定しない。
追加commandは実際の実行例に必要なものだけ確認し、全OSコマンドの互換性は扱わない。

未知事項は、静的binaryとしての起動、空rootで不足するOS資源、依存の必要範囲。
Linuxの全distribution・architecture・kernel版、LLM接続、製品TUI・Agent loop、release公開は対象外。
chrootを作るためのsudoは検証用であり、通常利用の要件ではない。
