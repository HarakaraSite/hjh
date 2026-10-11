# I-15: Core管理・版情報・履歴CLIの確認結果

2026-10-12。利用者の指定した`core list/status/stop`、`--version`、`history`を整備した。
操作と対象範囲は[計画](../plans/i15-cli-management-history.md)を参照する。

## 実装

Core登録はserveの寿命に属する。listは同じVM・ユーザーの登録と到達状態を表示し、
workspaceで絞れる。statusは完全ID・一意な短縮ID・URLで選択し、Coreのphase、Session数、
準備中を含む実行中runを読む。stopは既存shutdown APIで取消・終了保存・HTTP停止を待つ。
対象を省略したstatus/stopは一覧を表示し、stopは対象指定の案内を出す。
登録一覧、状態取得、版情報、履歴renderingはそれぞれのmoduleへ分けた。

hjhとhjh-tuiの--versionは実バイナリhash、source revision/dirty、Go/target、契約を表示する。
配布buildはgit metadataをlinkerへ渡し、通常Go buildはVCS build settingsを使う。

historyは省略時にworkspace内の最新Sessionを選び、完全ID・一意な短縮IDも解決する。
sessionは失敗・取消・子・途中の公開thinkingを含む会話、canonicalは採用会話Markdown、
detailはSession/execution、全保存message、全recordのpayload/content/relations、構成/contextを
NDJSONへ出す。contentはInspectと同じbase64で、binaryを含む保存bytesを保持する。
ローカルはread-only DB、--connectは既存公開APIを使う。履歴未作成時にDB・設定を作らない。

## 実利用経路と検証

private workspace・DB・XDG stateと自動割当portを使い、配布archiveから展開したバイナリで
実Coreを2つ起動した。list、workspace絞込み、短縮ID/URLのstatus、対象省略stopで両Coreが
継続すること、URL/短縮IDのstopで選択Coreだけが終了すること、正常終了時の登録解除を確認した。
利用者の稼働Core・tmuxは操作していない。

I-14で保存した実SessionのDBをprivate copyして閲覧した。session/canonical/detailの全出力が
ローカルとCore経由でbyte単位で一致した。detailには保存済み6messageと78recordを全て含めた。
最新・短縮ID・採用message JSON・inclusive turn範囲と、履歴がないworkspaceへfileを作らず
no historyで終了することを確認した。追加認証・モデル実行はしていない。

変更したCLI/TUI入口、buildinfo、corecatalog、historyviewのrace付きpackage test、
Managerの状態取得と既存serve経路のfocused test、api/apiclient/httpapiのpackage test、
対象packageのgo vet、format、git diff --checkを確認した。

独立reviewでdetailの非JSON contentの文字列化によるbinary bytes欠落をP2として採用した。
base64へ統一し、実保存・CLI読取り経路のfixtureで不正UTF-8を含むresource_revisionを保存して、
全recordの復元bytesを元contentと照合した。focused race testと一回の限定再reviewで解消を確認した。
ほかに採用するcorrectness findingはなかった。

## 配布バイナリ

最終sourceからLinux/amd64 packageを作り、展開した144fileのhash/sizeと両バイナリのstatic ELFを
確認した。--versionのhash・revision/dirtyがmanifestと一致し、空のworkspace/XDG stateへ
fileを作らないことを確認した。上段の実Core管理・履歴閲覧もこの展開バイナリで確認し、
dist/hjhとdist/hjh-tuiへ反映した。

archive SHA-256: `8159781235c3353f3f235db7d1e83409cec4a6c5f8b226d8951acd884df8610b`

source revision: `010e9719e676205ae5f9affa9f65143092c0ac08`、dirty: true。
この配布buildはI-13/I-14を含むlocal変更が未commitの状態で作成した。

## 利用上の範囲

I-15以前のCoreは再起動後に登録と新しい状態APIが有効になる。
異常終了の登録は到達不能として残し、自動削除しない。
detailは現在の保存契約を出力し、旧HenjiのNDJSON schemaそのものへの互換は追加していない。
Session削除、旧DB移行、agent/tool管理、診断管理CLIは今回の対象外である。
