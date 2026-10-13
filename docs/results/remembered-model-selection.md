# 同じworkspaceでのモデル選択の引き継ぎ

## 動作

TUIで選んだprovider・model・effortとauthProfile参照をCoreが
`workspace/.hjh/selection.json`へ保存する。選択時点で保存し、実行前にTUIを
閉じた場合も次の新規Sessionで使う。TUI/Coreの再起動をまたいで保持する。
保存は認証値やroleを含まず、workspaceの`.hjh/`は引き続きGit追跡対象外。

新Sessionの解決順はconfigのmodel → role.model → workspaceの記憶した選択 →
明示指定。CLIも同じ新Sessionの既定選択を使うが、CLIの明示flag自体は記憶した値を
書き換えない。既存Sessionの再開はそのSessionの保存selectionを使い、
子agentは引き続き親の実効selectionと子role/callの指定を使う。

`/new`では切替前Sessionの次runの選択を記憶する。provider変更は既存の規則どおり
移動先providerのmodel・effort・authProfile既定値を使う。未登録のmodel IDも指定できる。
別modelへ切り替えた場合はeffortを`auto`へ戻し、同じmodelの再選択では維持する。
Coreも明示model変更にeffort指定がなければ`auto`を使い、旧modelのeffortを引き継がない。

## 実装と確認

- appが保存fileと解決を所有し、公開APIはGET/PUT `/api/v2/selection`。
  PUTは同じCore確認とscope解決を使い、Session・実行を作らない。
- TUIは選択の保存を順番に送信し、選択直後の新Session・run開始・Core停止・UI終了で
  保存が追い越されないよう既存のjob管理に組み込んだ。成功時に履歴行は追加しない。
  保存失敗は画面に表示する。
- Service/Core再起動後の読込み、別workspaceへの非適用、既存Sessionの選択保持、
  previewと実runのモデル一致、明示provider切替の既定値を確認した。
- 公開HTTP/clientの選択保存 → Core再起動 → 新Session previewを確認した。
- TUIの連続provider/model/effort変更 → `/new` → 即送信とUI終了について、保存順序と
  run開始順序を確認した。外部の有料API呼出しは実施していない。
- TUIの保存・開始順序testをrace detectorでも確認し、変更範囲のtest・vetを通した。
  既存metadata preview testの個人APIキー依存は、埋込みconfigとローカルcatalog応答を
  使う確認へ修正した。
- DeepSeek/maxから別modelへの変更で、明示選択・Session継承の両方についてautoへの
  切替、保存・run入力、effort pickerから旧maxが消えることを確認した。
  同じmodelの再選択と、Coreへの明示effort指定は維持する。
- dist/hjhとdist/hjh-tuiを更新し、実binaryの公開APIで保存 → Core再起動 → 復元を確認した。

既存の履歴・configを書き換えるmigrationや、Session選択を遡って自動登録する処理は
追加していない。既存Sessionから`/new`へ進むか、TUIで選択を確定すると保存する。
このVMのworkspaceについては、直近Sessionの保存済み選択を初期設定として一度保存した。
起動済みCoreへ新しい保存・読込み動作を適用するには、新binaryで再起動する。
