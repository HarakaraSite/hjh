# 認証情報の保存先の集約

## 採用した動作

APIキーとChatGPT認証情報の既定保存先を、ユーザー設定directoryの
`hjh/credentials/`へ集約した。Linuxでは`~/.config/hjh/credentials/`、
`XDG_CONFIG_HOME`指定時は`$XDG_CONFIG_HOME/hjh/credentials/`となる。
workspaceの`.hjh/`は開発中のGit追跡対象外を維持する。

標準providerとExaの外部APIキー参照は
`${XDG_CONFIG_HOME}/hjh/credentials/<profile>`。
この明示prefixは`os.UserConfigDir()`で解決し、環境変数未指定時も使える。
認証情報はextensionsのmodule archiveへ入れず、Goの認証Managerだけが読む。

モデル接続・ツールのrequestProvider・CLI・Coreの`/login`は同じManagerを使う。
登録キーは`credentials/api-keys/<config-scope>/<profile-digest>.json`、
ChatGPTのtoken・registration・account一覧・host IDは`credentials/chatgpt/`へ保存する。

明示`--credential-file` → 登録キー → env → fileの順序、登録キーのconfig/profile単位の
owner、実行開始時のbindingは変更しない。`--credential-state-dir PATH`は
`PATH/credentials/`へ保存する明示指定として使える。明示`oauthFile`も指定先を使う。
Core登録やmodel favoritesのXDG_STATE_HOMEによる保存先は変更していない。

## このVMの切替

旧Henjiの外部APIキー4件と、hjhのChatGPT token・registration・host IDの3件を
新directoryへコピーし、元fileとの内容一致を確認した。元fileは削除していない。
利用中workspaceのconfigは外部APIキーの参照pathだけ更新した。
旧Henji専用のOAuth形式はコピーしていない。

productionに旧保存先の自動読込み・変換は追加していない。
別環境の既存fileは明示的にコピーするか、env・credential-file・profile参照で指定する。
起動済みCoreは起動時のManagerを持つため、新しい既定保存先の適用には再起動が必要。

## 確認

- HOME既定とXDG_CONFIG_HOME指定の両方で、外部file読込み、実行binding、登録・新Managerでの再利用、
  OAuth保存pathとhost IDの保存を確認した。
- CLIのmodelsが明示rootと新しい既定rootの登録キーを使うことを、ローカルHTTP接続で確認した。
- Coreの公開登録API → catalog → 実行 → Core再起動後の再利用を、新しい既定rootで確認した。
- 標準Exa参照を使う直接tool・run_js内・子agentの認証を確認し、JS・モデル入力・履歴への
  credential露出がないことを既存の実行経路testで確認した。
- 変更範囲のtest・vet・diff checkを通し、dist/hjhとdist/hjh-tuiを更新した。

外部APIの有料呼出しは実施していない。基本指示の差替えとユーザー共通extensionsの
読込み契約は、この認証情報の変更には含めていない。
