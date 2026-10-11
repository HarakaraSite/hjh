# I-11：モデルお気に入りの保存・API・picker契約

2026-10-11。[計画](../plans/i11-model-favorites.md)を具体化した契約。
利用者はモデルのお気に入り保存を選択し、この契約の確定と通常・批判的reviewを依頼した。
通常・批判的reviewと修正後の限定再reviewは完了した。[確認結果](../results/i11-plan-contract-review.md)を参照。
その後の実装・focused確認・実provider／TUI・Linux配布物確認も完了した。[実装結果](../results/i11-model-favorites.md)を参照。
旧Henjiは参考sourceであり、この文書の実装方式はGoの現行経路に合わせる。

## 利用範囲と識別

お気に入りはCore側の利用者設定。Sessionの会話、モデルの許可一覧、実行selectionではない。
共有単位は**実効configの絶対path × provider ID**、その中でmodel IDを識別する。
同じconfigを使うSession／workspace／client／Coreで共有し、違うconfigでは分離する。
config内容のhashではなくpathで識別するため、instructionやmodel定義の編集で設定が消えない。
pathは現行 `app.selectionConfig` の結果を使い、新しいconfig探索やsymlinkの別解決規則は追加しない。

providerは既存の外部定義IDを使う。OpenCode Go chat/responses、OpenAI APIキー/ChatGPTの異なるIDは混ざらない。
auth profile／account IDは保存keyへ含めない。認証を切り替えても同じ接続先の好みを共有する。
catalog取得には現在の実効auth profileを使い、favorite操作のためにcredentialを読む処理は追加しない。
旧HenjiのChatGPT account別保存とは異なるが、旧方式を必須化せず接続先の好みとして扱う。

HTTPのSession／workspace解決は現行 `httpapi.resolveWorkspace` と同じである。
Sessionがある場合はその保存workspace、次に明示workspace、最後にCoreのworkspaceを使う。
Serviceの明示 `--config` があれば全scopeにそのconfigを使い、なければ各workspaceの既定config pathを使う。
provider IDはfavorite APIで明示する。TUIは既存Selection previewで解決したIDを使う。
model IDはcatalog内外の直接指定を維持し、favorite保存でcatalogをallowlistへ変更しない。

## 保存

既定directoryは `$XDG_STATE_HOME/hjh/model-favorites/`、未指定なら `~/.local/state/hjh/model-favorites/`。
Core／共通操作には `ModelFavoritesDir` option、serveには `--model-favorites-dir PATH` を設け、
verificationでfavorite設定だけを隔離できる。credentialの既定保存先はこのoptionから変更しない。

`scopeId = hex(SHA-256(UTF-8(config絶対path)))` とし、fileは `<scopeId>.json`。
JSONはfavorite ID集合と軽いrevisionだけを保存する。

```json
{
  "schemaVersion": 1,
  "revision": 3,
  "providers": {
    "openrouter-responses": ["deepseek/deepseek-v4.1-flash"],
    "openai-responses": ["gpt-6-luna"]
  }
}
```

provider内のIDは重複なし・文字列昇順。file未作成はrevision=0、空集合として読む。
実際に集合が変わった保存でrevisionを1増やす。同じ希望値の再送は状態・revisionを変えない。
revisionはfile単位であり、他provider更新でも増える。値は更新順の比較用で、Session revisionとは無関係。

全更新はLinuxの `<scopeId>.lock` への排他flock中に、最新JSONの読取→対象IDへの希望値適用→
一時fileの書込・close→renameを行う。lockはrename対象JSONとは別のfileとし、rename後のinode変化で
更新lockが分かれないようにする。操作ごとに開いたlock descriptorを解放し、lock file自体は削除しない。
同じprocess内・別Core processの双方が同じ規則を使い、全providerのread-modify-writeが更新を失わない。

同Coreのfavorite GET／PUTは、scope解決前の受付順で専用FIFOを通す。
GETは自分より前の受付ticketが完了してから現在のJSONを一回読む。通信結果が不明なPUTも、
このGETより前に受付済みなら保存完了または保存前の取消まで待つ。読取のためにJSONを新規作成しない。
別Coreの同時更新に対してはatomic rename前後のどちらかの完了snapshotを返す。
PUTはrename完了後のmetadataを返す。これはその更新の完了時点の状態であり、別clientの後の更新まで保証しない。
既存JSONを読めない・保存できない場合は失敗を返し、空集合や成功へ置き換えない。
ここでの保存完了は正常Core終了／再起動で読めるfile公開であり、電源断時のfilesystem保証は追加しない。

catalog、名前・effort・capacity、credential、会話本文はこのfileへ保存しない。
履歴schema v3のDDL、provider config、拡張sourceのcapture、モデルrequestへfavoriteを追加しない。
未使用provider／configの設定を自動削除・migrationしない。

## HTTPと共通操作

base pathは現行 `/api/v2`。新しい公開操作は次の二つ。既存 `/models` はcatalog読取のまま維持する。

| 操作 | 契約 |
| --- | --- |
| GET `/model-favorites?sessionId=...&workspace=...&providerId=...` | 実効configとproviderのfavorite集合を読む。Session/workspaceは既存scope解決、providerIdは明示。catalog取得・ログインを行わない。 |
| PUT `/model-favorites` | bodyのscopeとmodel IDへ明示favorite boolを適用し、保存完了後200を返す。 |

GET responseの形は次のとおり。空集合も `modelIds: []` を返す。

```json
{
  "scopeId": "<config-path-sha256>",
  "providerId": "openai-responses",
  "revision": 3,
  "modelIds": ["gpt-6-luna"]
}
```

PUT request：

```json
{
  "coreId": "<connected-core-id>",
  "sessionId": "<optional-session-id>",
  "workspace": "<optional-core-side-workspace>",
  "providerId": "openai-responses",
  "modelId": "gpt-6-luna",
  "favorite": true
}
```

PUT responseはGET fieldsに、今回の `modelId` と `favorite` を加える。
GET/PUTともprovider定義は実効configから解決する。boolは明示必須で、falseを解除として扱う。
未定義provider、未存在Session、壊れたJSON等のerrorは既存APIのerror伝達規則で扱う。
PUTのcoreIdは接続中Coreと照合し、再起動前Core宛の遅延writeを新Coreへ適用しない。
Core終了による登録停止も既存のclosing状態と整合させる。catalog候補の有無やrun busyをwriteの条件にしない。

反転API、operation ID／履歴receipt、CASは追加しない。同じ希望値は何回適用しても同じ集合になる。
結果不明時はGETで先行受付の完了後の値を読み、希望値と違う場合だけ同値PUTを再送する。
再送の結果も不明なら同じ確認を続け、対象について逆の希望値を作らない。
同CoreのGET確認／PUT成功時には先行受付ticketが残っていないため、古いworkerが後から値を戻さない。
HTTP受付前の通信順そのものは保証せず、異なるrequestはCoreが実際に受け付けた順を使う。
異なるclientが同じIDへ逆の希望値を送った場合は保存順の後の値が現在値になる。
異なるID／providerへの更新は最新fileへmergeし、他clientの変更を失わない。
PUTは新しいcatalogを返すAPIではなく、小さいfavorite metadataだけを返す。

共通操作は `Service.ModelFavorites(ctx, resolveWorkspace, providerID)` と
`Service.SetModelFavorite(ctx, resolveWorkspace, providerID, modelID, favorite)`。
`resolveWorkspace`は `func(context.Context) (string, error)` の遅延解決操作とし、受付ticketのslot内で呼ぶ。
HTTPはCore IDを照合し、捕捉したSession／workspaceを現行 `httpapi.resolveWorkspace`へ渡すclosureを送る。
共通Service直接呼出しでは、捕捉したworkspaceを返すclosureを送る。
Serviceが一回だけticketを取り、slot内の既存config解決とpreference storeの保存へ接続する。
HTTPが先にSession scopeを解決したり、store内部でもう一度同じFIFOへ登録したりしない。
TUI Backend／apiclientもこの二操作を提供し、外部HTTP clientを含めて同じ状態を使う。

## 操作の寿命とCore終了

preference storeが受付ticket・専用FIFO・保存登録・終了joinを所有する。
HTTPはDTO捕捉とCore ID照合の直後、Session／workspace／config／provider解決より前に受付ticketを取る。
共通Service直接操作も、scope／config解決より前に同じFIFOへ入る。
まだscopeが確定していないticketも順番に含め、遅いconfig解決が後のGET／PUTに追い越されないようにする。
FIFOを待つのはfavorite操作だけで、Manager lockやrun／auth／描画の操作を待たせない。

受付予約と保存登録は別の境界である。GET、FIFO待ち、scope／config／provider解決までは呼出しcontextに従う。
PUTは自分のslotで解決を終え、取消を確認してから保存登録する。
保存登録後は、捕捉したconfig path、provider/model ID、希望値を使ってclient切断から独立して完了する。
PUT handlerは呼出しcontextが切れれば応答待ちを終了できるが、登録済み保存を取消さない。
取消された受付ticketもFIFOから完了扱いで取り除く。保存登録前の取消では保存を開始しない。

現行停止経路は `httpapi.Server.Close` → runs.Close/join → auth join → Service.Close → HTTP server close。
I-11では停止開始時に新しい受付ticketを閉じ、storeの受付予約と停止を同じ短いlifecycle lockで順序付ける。
HTTP shutdownは既存のCore ID照合／`runs.BeginShutdown`成功後、202応答を返す前に
`Service.BeginModelFavoritesClose`で受付を止める。`Server.Close`も入口で同操作を呼び、SIGINT等の停止を覆う。
`Service.Close`を直接呼ぶ経路も先に受付を止める。登録停止はfile I/O・保存完了を待たない。
停止前の受付ticketは元contextの取消規則を保って処理し、保存登録済みなら保存を完了する。
Service.Closeは受付済みticket全体の完了をjoinしてからstoreを閉じる。通常run・authの既存終了順を変更しない。
StoreのClose／登録停止は複数回呼べる。外側requestの終了とworkerの保存完了を同一視しない。
storeはSession・runの予約を取得せず、保存／flock／JSONをCore run lockの内側で待たない。

## model picker

既存のSelection preview→catalog取得と並行／独立に、previewで解決したproviderのfavorite metadataを読む。
`/model`だけにfavorite操作を追加し、provider／effort pickerのTabへ転用しない。

catalogにあるmodelは名前やeffort情報をそのまま使い、favorite flagは別のmetadata集合から得る。
catalog外のfavoriteはmodel IDだけの追加候補とし、`favorite · not in catalog`と表示する。
既存のcurrent modelがcatalog外のときも一回だけ加える。Effort等を推測して補わない。
同じIDの候補を重複させない。baseのcatalog順を保ってfavorite群→非favorite群へ安定に並べ、
追加IDだけの候補はID順をbase順として扱う。文字列検索はID／表示名に対して両群へ適用する。

Tabは選択IDについて `!確認済みfavorite` を希望値として送る。保存中／結果不明ならその同じ希望値の確認を続け、
未確認値をもう一度反転しない。PUT送信中のTabは既存応答を待つ表示だけで、追加GET／PUTを起動しない。
通信結果不明と判明した後だけ回復GETへ進む。pickerの保存中表示だけを変え、成功前に印や保存成功へ置き換えない。
検索・矢印・Esc・Enterによる選択は保存待ちに巻き込まず、draftも変更しない。
初回／回復GETとPUTのfavorite集合を適用するときは、直前にユーザーが選んでいるmodel IDを捕捉し、
候補の再構築・整列後に同じIDの行へ戻す。catalogの遅延取得で候補を再構築するときも同じ規則を使う。
ユーザーが動かしていなければ保存対象IDのままになる。catalog外favoriteの解除直後も、そのpickerを閉じるまでは
候補を保持して選択行を消さない。後の新規pickerではcatalog／favorite／currentから構築し直す。

送信時のCore ID、Session／workspace、favorite scopeId、provider ID、model ID、希望値、picker世代を捕捉する。
Core／scope／provider変更、picker close/reopen後の遅延応答で新画面・draft・選択を更新しない。
保存待ちの小さい操作metadataとpickerの表示状態を分け、pickerを閉じてもCoreの保存を待ち直せる。
旧pickerの操作が完了した場合、現在pickerのCore・確認済scopeId・providerが一致すれば、
現在のpicker世代・Session／workspaceを捕捉した新GETで再確認する。同configを共有する別Sessionも対象とする。
旧応答自体は現在pickerへ直接適用しない。新GET待ちのTabは古いflagを反転して保存しない。
同じscopeId・provider IDのfavorite snapshotだけでrevisionを比較し、遅い旧snapshotで
新しい確認済み集合を上書きしない。responseは一providerの集合なので、別providerのrevisionが高いことを
理由に、こちらの集合を更新済みと扱わない。
同じpicker内で保存結果を受けても、catalog／credentialを取り直したり次run selectionを変えたりしない。

catalog取得失敗時は従来通りその失敗を表示する。favorite読取だけの失敗なら通常catalogを選べるようにし、
お気に入り情報が未取得であることを表示する。未取得のflagをfalseとみなして反転しない。
未取得または読取error時のTabはfavorite集合の再取得だけを行い、変更しない。
以前の確認済み集合を表示用に残していても、読取error後にその古いflagで反転しない。
取得成功後の次のTabで確認済み値を反転する。
pickerを開き直す場合も再取得する。結果不明の保存がある場合のTabは、その保存の同じ希望値を確認する。
古い未知状態を保存成功扱いしない。
これは外部catalog responseの拒否や匿名の代替catalogではなく、二つの独立した操作の結果を表示する契約である。

## 確認するproduct動作と範囲

- Tabの登録／解除、成功後の印・整列、検索中の選択IDとdraft維持、Enterの次run選択、editorのTab補完。
- 同config・providerの共有、別config／providerの分離、account切替時の好みの共有と現在の認証によるcatalog取得。
- 結果不明からGET確認・同値再送・次の解除までの保存順、複数client／Coreによる異なるID更新の保持、
  metadataだけの保存と履歴／request非混入。
- 保存待ち中のclose／切断／scope変更と遅延応答、Core終了での登録停止・保存join、再起動後の読取。
- catalog外favoriteの表示／解除後の選択維持、読取失敗と未知値の区別。

focused testsは上記変更動作の実経路に対応させる。full gateやpermission／filesystem matrixは追加しない。
private Core・favorite directory・config・tmuxで、Tab→detach→再接続→Core再起動と別HTTP clientを確認する。
実provider catalog／runとの分離は既存認証を使って確認し、Linux配布物でも同じ操作を成立させる。
計画／契約reviewの完了は製品実装・実利用確認の完了を意味しない。
