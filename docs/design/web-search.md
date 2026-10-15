# Exa Web検索

Exaはmodel providerではなく、外部情報の出典を探す検索toolとして使う。
検索入力は編集可能な`web-search.js`、tool schemaと利用方針は外部configが所有する。
認証HTTP・応答解析はHostが実行する。
[I-22の導入記録](../results/i22-exa-web-search.md)と
[現在の検索/読取り分離](../results/web-retrieval-defaults.md)を参照する。

## 利用と本文読取りの分離

default roleはweb_searchを選択する。別roleや子もtoolsへ選択すると使える。
直接tool callと生成JSの`tools.web_search`は同じ実装・履歴経路を使う。

```js
// Find source URLs and short excerpts before reading selected pages.
return await tools.web_search({
  query: "Exa Search API documentation",
  numResults: 3,
  includeDomains: ["exa.ai"],
});
```

既定は`type:"auto"`・`numResults:10`。明示した件数・検索modeを優先する。
[Exa公式Search契約](https://exa.ai/docs/reference/search)の公開上限は100件で、
検索modeごとの制約もある。hjhに独自の件数拒否・結果件数の切詰めは追加しない。

本文取得の`contents`はモデル向け引数に含めない。内部要求は
`contents:{text:false,highlights:{maxCharacters:1000}}`へ固定する。
contentsが渡されても、この固定設定を上書きしない。transportは`stream:false`。
各結果の短い抜粋をExaへ要求し、全文は要求しない。
既知URLや選んだページの本文はweb_fetchで読む。通常受信上限は64 KiBで、
長いページはsave_toで全体保存し、read_fileの行範囲で読む。
これは件数・抜粋長の要求であり、応答JSON全体へのbyte上限ではない。

検索mode、domain/date、category、additionalQueries、userLocation、outputSchema等は維持する。
検索結果は完全なJSON objectであり、順序、URL、タイトル、抜粋、metadata、任意の合成output、
未知fieldを保持する。contents以外の未知request fieldも従来どおり渡す。
企業契約専用のcomplianceは標準のモデル向け引数一覧に含めない。
任意引数を空文字・null・推測値で埋めないよう利用指示に記載する。

利用方針はpromptGuidelinesに宣言し、有効なtoolだけ親・子の共通instructionへ集める。
明示されたlocal/canonical sourceを先に使い、外部情報の探索には検索を使う。
検索抜粋と合成outputを区別し、回答の根拠へ直接URLを付ける。
[I-21契約](extensions.md#i-21tool自身の利用指示)を参照する。

## 認証・共通HTTP

標準credential参照は次のとおり。キー値はconfigへ書かない。

```json
"exa-api-key": {
  "env": "EXA_API_KEY",
  "file": "${XDG_CONFIG_HOME}/hjh/credentials/exa-api-key"
}
```

検索moduleはapi.webSearchへExa endpoint、POST、公開header、検索JSONとprofile名を渡す。
Hostは既存のcredential付きHTTP dispatcherを内部で使う。
appがcapture済みconfigのcredential参照と既存Manager/Bindingを使い、Go transportでBearerを付ける。
キーはJSへ返さず、認証付きheaderを観測へ記録しない。transport error中の実tokenも省く。
既存のモデル用credential-file overrideを別profileへ流用しない。
登録済みbindingがあれば通常の登録キー→env→file解決経路を使う。

一般の外部tool/hook向けapi.requestProviderは保持する。
戻り値は{status,body}で、bodyは受信文字列。生成JSへは直接公開せず、選択toolを通す。
認証付きrequestはredirectを追わない。context取消・host job join・Worker終了は既存経路を使う。
HTTP status・JSON形式の失敗は通常tool errorへ伝える。Exaのerror/tag/requestIdは
statusとともにtool errorへ含め、次のモデル入力でも失敗理由を読めるようにする。

## 保存・更新

query/option、認証profile名、endpoint/methodはHost開始記録へ保存する。
Host完了記録はstatus/resultCount/responseBytesだけを持ち、HTTP bodyを二重保存しない。
完全な検索結果本文は公開web_searchのtool_resultが所有し、次のモデル入力と予算管理に使う。
run_jsで結果を絞ってreturnした場合でも、内側のweb_search結果は実行証跡として残す。
tool sourceや定義の常時保存は行わない。[通常履歴の保存契約](normal-history-storage.md)を参照する。

配布assetsと通常workspaceへ宣言・moduleを反映する。
他の既存workspaceにはconfig.tools、role.tools、credential参照、web-search.jsを反映する。
Host API変更を含む場合はCoreを新binaryで再起動する。
その後のtool/config編集は次Workerから反映する。
