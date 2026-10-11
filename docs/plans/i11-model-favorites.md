# I-11：モデルのお気に入り保存

2026-10-11。I-10完了後、利用者が次の範囲として「モデルのお気に入り保存」を選択した。
[保存・API・picker契約](../design/i11-model-favorites-contract.md)を具体化し、計画・契約の
通常・批判的reviewと修正後の限定再reviewを完了した。[計画確認結果](../results/i11-plan-contract-review.md)。
その後、本文書の実装・focused確認・実装review・実provider／TUI・Linux配布物確認を完了した。
[実装結果](../results/i11-model-favorites.md)を参照する。
[I-08](i08-connection-selection.md)で後続へ分けた操作を、現在の独立Core／TUIへ追加する。

## 利用者が必要とする動作

- `/model`の一覧でTabを押すと、選択したモデルをお気に入りへ登録・解除できる。
  お気に入りには印を付け、一覧の先頭へ並べる。検索はお気に入りと通常候補の両方に作用する。
- favorite集合やcatalogの遅延取得・保存応答後も、その時点で選んでいるモデルを維持し、
  検索文字・会話draft・次runの選択を変更しない。
  Enterはモデル選択、Tabはお気に入り変更とする。通常editorのTab補完は維持する。
- detach／再接続、Core再起動後もお気に入りが残る。別のHTTP clientも同じ操作・保存状態を使える。
- OpenCode GoのChat CompletionsとResponses、OpenAI APIキーとChatGPTは別のprovider IDとして区別する。
  同じmodel IDが別接続先に存在しても、お気に入りを混ぜない。
- run中もお気に入りを変更できる。実行中run・F2予約のselection、Session採用会話やrevisionへ影響させない。
- 保存を確認する前に成功表示しない。通信が切れた場合は対象の現在値を照会し、必要なら同じ希望値を再送する。
  APIは反転操作ではなく `favorite: true/false` を指定する。

## 根拠と現行の実経路

利用者は旧TUI要件の踏襲を指定し、今回お気に入り保存を選択した。
旧sourceの参考経路は `/home/agent/projects/henji-harness/` の次の箇所で確認した。

- `v0/tui/remote_catalog_ui.ts:toggleFavorite`：Tabで変更、検索を再適用し、同じmodel IDの選択を維持する。
- `v0/agent/host/core_service.ts:modelFavorite`：Coreからcatalogの保存操作へ接続する。
- `v0/agent/provider/live_model_catalog.ts:favorite`、`sortedModels`、`#catalogPath`：
  favoritesの永続保存、先頭への整列、ChatGPT account別の保存。

旧catalogはお気に入り以外にeffort等も保存する。hjhでは今回お気に入りの責務だけを追加する。
旧catalog cache・metadata取得・account別保存を、参考sourceだけを根拠に必須化しない。

現行はTUI `openConnectionPicker/loadConnectionPicker` → apiclient Selection／Models →
HTTP `/selection`／`/models` → app PreviewSelection／Models → provider定義または認証済みcatalogである。
一覧と検索・選択行は `internal/tui/connection_picker.go` が所有する。
modelの確定は既存の次run選択へ接続され、保存済みSession selectionの変更は成功rootの採用時に行う。
現在のModels DTOにfavorite情報はなく、UI preferenceの保存ownerもない。

config解決は `app.selectionConfig` を使う。`--config`があればその絶対path、なければSession／workspaceの
`.hjh/extensions/config.json`を使い、未配置時も同じ論理pathから標準構成を解決している。
Session宛HTTPは `resolveWorkspace` で保存workspaceを使うため、この経路をお気に入りでも共用する。

## 具体化した保存と公開操作

お気に入りはCore側の利用者設定として保存する。TUIのローカルfile、Session履歴DB、provider configへ
同じ設定を重ねて書かない。履歴schema v3のDDLを変更しない。

保存先は `XDG_STATE_HOME/hjh/model-favorites/`、未指定なら `~/.local/state/hjh/model-favorites/`。
実効config絶対pathから識別した小さいJSON fileに、provider IDごとのmodel ID集合だけを置く。
config source・拡張module・credential・モデル入力へお気に入りを追加しない。
verification用にprivate state directoryを明示できる構成にする。

共有範囲は同じ実効configとprovider ID。Session別ではなく、同じ構成を使うSessionで共有する。
この案ではauth profile／accountをお気に入りの識別子へ含めず、取得catalogには現在の認証を従来通り使う。
今回のscopeは接続先の好みとして共有する。旧Henjiのaccount別保存は採用せず、catalog認証は従来の実効profileを使う。

| 操作案 | 入出力・用途 |
| --- | --- |
| GET `/model-favorites` | Session／workspace、provider IDを受け、保存されたmodel ID集合を返す。provider catalog取得を行わない。 |
| PUT `/model-favorites` | 同じscope、provider ID、model ID、favorite boolを受け、保存後の対象値を返す。反転しない。 |
| 既存GET `/models` | 現在の認証でcatalogを取得する。favorite metadataとcatalog外favoriteの候補追加はTUIで組み合わせる。 |

catalog候補は従来通りallowlistではない。お気に入り登録・解除にcatalogの再取得やログインを要求しない。
お気に入り保存はモデル実行・認証状態・effortの更新を伴わない。
provider定義が変更された場合も、自動migrationや既存設定の削除は行わない。

## 責務と同期

- preference store：scope解決前の受付ticketとCore内favorite GET／PUT専用FIFO、終了joinを所有する。
  小さいJSONの読取と保存、Linuxの別lock fileへの排他flockでread-modify-writeを直列化。
  一時fileからrenameして保存を公開する。反転ではなく希望値を適用し、同じ値の再送は同じ状態になる。
- app：既存config・provider解決とpreference storeを接続する。catalog取得とpreference保存を別の操作に保つ。
- HTTP/client：scopeと希望値を送り、TUI以外でも同じ契約を使えるようにする。
- TUI：表示・検索・model IDに基づく選択位置の保持。送信時のCore・scope・provider・modelを捕捉し、
  閉じたpickerや切替後の画面へ遅延応答を適用しない。保存状態は小さいmetadataだけを持つ。

既存ManagerのSession/run lockにpreference保存を入れない。
同じCoreではscope解決もfavorite専用FIFO内で行い、file保存完了後に結果を返す。
回復GETは先行受付ticketの完了後に読むため、結果不明の古い保存が次の逆Tab後に復活しない。
現行Core停止は `httpapi.Server.Close` → run join → auth join → `Service.Close` → HTTP server closeの順である。
shutdownの202応答前とServer.Close入口で新しい受付を閉じ、Service.Closeで受付済みticketをjoinする。
受付予約はまだ呼出しcontextへ従い、scope解決後の保存登録からHTTP clientの切断と独立して完了する。
結果を失ったclientは現在値で確認する。
同じ設定fileを複数Coreから更新する場合も、rename対象JSONとは別のlock fileで直列化し、
最新の集合へ希望値をmergeする。具体的なDTO、revision、読取・保存・Closeの境界は契約文書に集約する。

## 実装・確認の順序

1. 保存scope、catalog外favorite、保存中のClose／切断を具体化し、計画・契約をreviewする。
2. preference storeと共通app操作、HTTP/clientへ接続する。catalogやrun ownerへ新たな保存責務を入れない。
3. model pickerのTab、favorite印、安定した整列、検索・選択位置・draft保持を接続する。
4. 変更した実動作のfocused確認を行う：登録・解除、希望値再送、provider別保存、複数clientの更新、
   Session／provider切替・picker close後の遅延応答、保存確認前の表示、再起動後の再読取。
5. private Core／state directory／tmuxでTab→detach→再接続→再起動を操作する。
   実config/catalogで検索・選択・登録・解除を確認し、通信は別clientからも照合する。
   run中の変更と次回選択が独立することは実利用経路で確認する。
6. 通常・批判的実装review、採用findingの修正、Linux配布物で保存・再開を確認する。

成功基準は人間がmodel pickerから登録・解除し、再接続後も目的のモデルを選べること。
追加test件数やfixtureだけの成功を代替にしない。verificationは利用者の設定や稼働Coreから隔離する。

## 今回の対象外

認証登録画面、Core自動起動、お気に入りによるモデルの利用制限、catalog cacheの全面導入、
色・Glamour、tool詳細の再設計は今回の対象外。
[HTTP生本文とターン中context予算](../notes/http-response-context-overflow.md)は引き続き後続課題である。
