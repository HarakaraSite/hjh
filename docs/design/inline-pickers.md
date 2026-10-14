# Inline pickers

2026-10-14。Session・provider・model・effortの候補をslash command候補と同じ見た目へ揃える。
利用者の指定により、候補は1項目1行で表示し、Session pickerに削除を追加する。

候補は入力欄の上の一時dockに置く。選択行を青、補足を通常色の薄い表示にし、
画面幅で省略、画面高で表示範囲を移動する。会話のscrollbackには候補を残さない。
現在値・modelのお気に入り・検索、Sessionの短縮IDを表示し、既存の選択と保存経路を使う。
一覧確認と選択を`/provider`・`/model`へ集約し、TUIの`/providers`・`/models`は廃止する。
queryは会話draftと分離し、Escで戻った際もdraftと会話を保持する。
認証画面だけは従来のalternate screenを使う。inline候補の表示中も親本文は通常画面へ出力する。

Session pickerのDeleteは選択対象の短縮IDを示して確認待ちにする。
Enterで削除、Escで確認を取り消す。削除はCore API経由で既存Store.DeleteSessionへ依頼する。
Session・子を含む実行履歴・関連記録・他に参照されない本文/構成を削除する。
Coreの実行予約と削除を直列化し、実行中のSessionは削除せず、先に実行を止める案内を返す。
成功時は候補から除去し、表示中のSessionを削除した場合は新規Session待ちへ戻る。
DBファイルの縮小や、既存ユーザーのSessionを今回の検証で削除することは対象外。

現行経路はhandleKey→既存pickerの選択状態→screenFrame→renderer.DockRows。
見た目を共通のinline rendererに集約し、pickerOpen（操作対象）と認証overlay（画面切替）を分離する。
非同期catalog・favorites・Session読込みの世代管理は既存状態を使用する。
削除経路はTUI→apiclient→HTTP API→Core managerのSession予約→history.Store。

確認は4種類の単行候補、長い名前/狭い画面での選択可視性、model検索/お気に入り、
draft保持、候補表示中の本文進行、削除の確認/取消/成功/実行中拒否を対象とする。
一時Core・DB・PTYで実binaryの操作を確認し、有料モデル生成や常用DB変更は行わない。
