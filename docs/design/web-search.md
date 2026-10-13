# Exa Web検索

I-22は旧Henjiの外部web_searchを移植する。[計画](../plans/i22-exa-web-search.md)。
検索処理は編集可能な`web-search.js`、tool schemaと利用方針は外部configで所有する。
Exaはmodel providerとして登録せず、検索toolとして使う。

## 利用

default roleはweb_searchを選択する。別roleや子もtoolsへ選択すると使える。
モデルの直接tool callと、生成JSの`tools.web_search`は同じtool/hook/host履歴経路を使う。

```js
// Find Exa search documentation and return source excerpts
return await tools.web_search({
  query: "Exa Search API highlights and contents documentation",
  numResults: 3,
  includeDomains: ["exa.ai"],
});
```

既定requestは`type:"auto"`、`contents:{highlights:true}`。明示optionで置き換え、最後に
`stream:false`を付ける。Tool結果は完全なJSON objectであり、結果順、URL、タイトル、
抜粋/本文、metadata、任意outputと未知fieldを保持する。全文の新しい上限や結果件数上限は設けない。

検索mode、domain/date、category、additionalQueries、userLocation、contents、outputSchema等は
[Exa公式Search契約](https://exa.ai/docs/reference/search)に従う。
Go/JS側でoptionの組合せを拒否したり、schemaにないfieldを削除したりしない。
企業契約専用の`compliance`（HIPAA）は標準のモデル向け引数一覧に含めない。
任意引数は必要な場合だけ指定し、空文字・null・推測値で埋めないよう利用指示に記載する。
web_fetchは本文読取り時に`save_to`を省略し、保存時だけ実際のpathを指定する。
userLocationは文字列とobject形式、outputSchemaはtext/object形式の説明に対応する。
dynamic/verbosity highlights指定時は、旧版と同じ
`Exa-Beta: dynamic-highlights-2026-08-28`を追加する。

利用方針は`promptGuidelines`に宣言し、有効なtoolだけ親・子の共通instructionへ集める。
明示されたlocal/canonical sourceを先に使い、外部情報の探索には検索を使う。
検索後の精確な値や変わりやすい情報はcanonical URLをweb_fetchで確認する。
text/highlightsと合成summary/outputを区別し、回答の根拠へ直接URLを付ける。
全文が大きい場合のcontents選択も案内する。[I-21契約](extensions.md#i-21tool自身の利用指示)。

## 認証・共通HTTP

標準credential参照は次のとおり。キー値はconfigへ書かない。

```json
"exa-api-key": {
  "env": "EXA_API_KEY",
  "file": "${XDG_CONFIG_HOME}/hjh/credentials/exa-api-key"
}
```

外部tool/hook実装は`api.requestProvider({authProfile,endpoint,method,headers?,body?})`を使う。
bodyはJSON値、戻り値は`{status,body}`でbodyは受信本文の文字列。
検索toolはExa endpoint、POST、公開header、検索JSONとprofile名だけを渡す。
appがcapture済みconfigのcredential参照と既存Manager/Bindingを使い、Go transportでBearerを付ける。
キーはJSへ返さず、認証付きheaderを観測へ記録しない。transport error中の実tokenも省く。
既存のモデル用credential-file overrideを別profileへ流用しない。
登録済みprofile bindingがあれば通常の登録キー→env→file解決経路を使う。
専用Exa login UIは今回追加しない。

認証付きrequestは旧版同様redirectを追わない。context取消はHTTPへ伝わり、host jobのjoinと
Worker終了は既存経路を使う。HTTP status・JSON形式の失敗は通常tool errorへ伝える。
Exaの失敗応答にある`error`・`tag`・`requestId`はstatusとともにtool errorへ含め、
次のモデル入力でも失敗理由を読めるようにする。
生成JSへrequestProviderを直接公開せず、選択した検索toolを呼ぶ。

## 保存・更新

query/option、認証profile名、endpoint/method、受信status/bodyは通常host observationへ保存する。
解析済み検索結果は通常tool結果・次model入力へ入り、予算管理を使う。
source bytes、tool schema/guidelines、有効instructionは既存のconfiguration/resource保存経路を使う。
旧版の独自auxiliary counter/provider evidence ownerを追加しない。

配布configとsourceには登録済み。このrepositoryの利用workspaceにも宣言とmoduleを反映した。
他の既存workspaceにはconfig.tools、role.tools、credential参照、web-search.jsを反映する。
HTTP実装APIを追加したため、旧Coreは新binaryで再起動する。
その後のtool/config編集は次Workerから反映する。
