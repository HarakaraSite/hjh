# Inline pickerとSession削除

2026-10-14。[操作契約](../design/inline-pickers.md)に沿って実装した。

## 表示と操作

- Session・provider・model・effortをslash候補と同じ描画へ集約。入力欄の上に1項目1行で表示する。
  選択行は青、説明は通常色の薄い表示。長い候補は画面幅で省略し、選択に合わせて表示範囲を移動する。
- Sessionの短縮IDと実行中表示、現在値、model検索・お気に入りを保持する。
  Escで戻っても会話draftを変更しない。inline候補表示中も親本文の確定出力を続ける。
- 一覧だけを履歴へ出すTUIの`/providers`・`/models`を廃止し、一覧確認と選択を`/provider`・`/model`へ集約。
  候補catalog・ヘルプ・command dispatchと使われなくなった一覧処理も整理した。
- 認証画面は従来のalternate screenを使用する。
- Session pickerのDelで対象を確認し、Enterで削除、Escで取消。
  確定後のEscは画面だけを閉じ、削除は完了まで待つ。保存待ちのモデル選択も先に完了させる。
  実行中の場合は停止後に削除する案内を表示し、履歴を保持する。
  表示中のSessionを削除した場合は新規会話へ戻り、draftを保持する。

## Coreと履歴

`DELETE /api/v2/sessions/{id}`→Core Manager→既存Store.DeleteSessionを使う。
Session・子を含む実行履歴・関連記録・参照されなくなった本文/構成を同じtransactionで削除する。
成功204、不存在404、実行予約・保存処理中は409 session_busy。
完了runのCore索引と復元viewも破棄し、古い購読や読込みで削除済み履歴を再表示しない。
実行開始・追加指示保存・recall・Core終了と削除の寿命を既存Managerで管理する。
CLIのCore停止後ローカル削除は従来のまま。DBファイル自体の縮小は行わない。

## 確認

変更されたTUI・apiclient・httpapi・corerun・app、および両CLIのpackage testとgo vetが通過。
HTTP/client経由でSession削除、他Session保持、実行中拒否、完了後削除、古いrun/view購読の終了を確認。
TUIでは単行表示、短縮ID、検索・お気に入り、狭い画面の選択行、draft保持、
本文の一度だけの出力、削除の確認/取消/成功/拒否、確定後にpickerを閉じる動作を確認した。

更新したdist/hjh・dist/hjh-tuiと一時Core・DB・PTYでも、4種類の候補表示、model検索とTabお気に入り、
provider/model/effortの保存、Del確認→Esc取消→Del/Enter削除、表示中Sessionから新規会話への復帰、
draft保持、36×12へのresizeを確認した。候補表示でalternate screenへ切り替わらない。
テスト用の2つのSessionだけを使い、常用DBの変更と外部モデルAPI呼出しは行っていない。

一覧コマンド整理後もTUI・hjh-tuiのpackage test、go vet、diff checkが通過。
実binaryと一時Core・PTYで、単数形のpicker操作・選択保存と、/helpから複数形コマンドが消えたことを確認した。

両distバイナリを更新した。新しい削除APIを使うにはCoreとTUIを再起動する。
