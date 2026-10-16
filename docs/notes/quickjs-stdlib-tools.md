# go-quickjsのstdlib活用とtool実装のJS化

記録日: 2026-10-16。利用者との検討メモ。実装・移行計画はまだ作成していない。

## 検討の方向

利用者の認識は「基礎操作のほとんどはQuickJS側で扱え、Go側に残す接続の中心は認証」というもの。
go-quickjs付属のstdlibを有効にし、toolの処理をJSで組み立てる方向を検討する。
独自Go APIを多数持つ現行構成や、現行と完全に同じ戻り値・取消方式を維持することを
移行の必須条件として扱わない。維持したい製品動作は、計画時に利用者の目的から整理する。

ここでいうQuickJS側は、JS言語エンジンだけでなくgo-quickjsのstdlibまで含む。
基礎I/Oはstdlib内部のGo実装が担当するため、すべてを純粋なJSで再実装する意味ではない。
hjh独自のGo基礎操作APIを、stdlibとJSのtool実装で置き換えられるという整理である。

## 使用中のv0.25.2で確認した代替候補

| 現在のAPI・用途 | stdlibとJSで組める処理 |
| --- | --- |
| `readFile` | `fs`で読取り、JSで行指定・ページング・件数・次位置を生成 |
| `writeFile` | `fs.mkdir`・`writeFile`で親directory作成と保存、`Buffer`等でbytesを算出 |
| `editFile` | `fs`とJSで読取り・置換・件数集計・保存 |
| `listDirectory` | `fs.readdir`・`stat`とJSで一覧・tree・深さ指定・ページング |
| `countFiles` | `fs`とJSで行数・単語数・bytesを集計 |
| `findFiles` | `fs`・`path`とJSで再帰走査・glob・除外・ページング |
| `resolveExecutable` | `process.env`・`fs`・`path`を使ったPATH検索 |
| `command` | `child_process`で起動・stdin・出力取得、JSで結果を加工 |
| `grepCommand` | `child_process`でrg/grepを起動し、JSで出力解析・ページング |
| `gitInspect` | `child_process`でGitを起動し、JSで引数組立て・出力解析・ページング |
| `http` | `fetch`で要求し、JSで応答を処理 |
| `webFetch` | `fetch`・`fs`・streamとJSで取得・HTML可読化・上限・保存・metadataを処理 |
| `webSearch` | `fetch`とJSでExa要求・結果処理。秘密情報を扱う認証接続は分離 |
| 待機・非同期処理 | timer・Promise・stdlibの`Loop`を利用 |
| ハッシュ・圧縮・文字コード変換 | `crypto`・`zlib`・`TextEncoder`等を利用 |

この表はソースから確認した実装可能な構成であり、完成した同等toolがstdlibに付属するという意味ではない。
rg/grep・Gitなどの外部プログラムは引き続き必要だが、その起動もstdlibが提供する。
「起動部分だけhjh独自Go APIが必須」という整理は誤り。
コマンド終了処理やイベントループもstdlibにあり、現行hjhとの方式の差だけを根拠に独自実装を残さない。

## hjh固有の接続と未決事項

- 認証：credentialの解決・更新と認証headerの注入は、APIキーやAuthorizationをJSへ露出させず
  利用する接続としてGo側に残す方向。
- agent・モデル・context・DB：stdlibにはhjh固有の状態を操作する機能がない。
  状態への接続は必要だが、その上の処理・方針までGoに残すと決めたわけではない。
  どこまでJSへ移すかは別途設計する。
- toolの選択・hook・履歴記録：既存実装をそのまま残すことを前提にせず、必要な製品動作と
  JS/Go間の責務を計画時に整理する。
- module読込み：stdlibの導入と、`run_js`からの外部ライブラリ取得・名前解決は別の作業。
  付属`qjs`コマンドにはESM/CommonJSと`node_modules`解決の実装があるが、
  hjhの組込みruntimeに自動で導入されるものではない。
- 性能・互換性：移行後の動作、速度、メモリ、コード削減量は未実測。
  Node用ネイティブ拡張やWASM実行まで、この候補で利用可能になるとは扱わない。

## 参照

- [go-quickjs標準ライブラリ](https://github.com/go-quickjs/go-quickjs/blob/main/docs/stdlib.md)
- [付属qjsのmodule解決](https://github.com/go-quickjs/go-quickjs/blob/v0.25.2/docs/qjs.md)
- [hjhの現在のJS公開API](../../internal/jsruntime/bootstrap.js)
- [hjhの現在のGo基礎操作](../../internal/host/host.go)
- [現行の拡張・Worker契約](../design/extensions.md)

調査にはGo module cache内の`github.com/go-quickjs/go-quickjs@v0.25.2`の
`stdlib`・`docs/stdlib.md`・`docs/qjs.md`も使用した。公開mainの更新だけを根拠にしていない。
