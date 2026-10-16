# I-22 Exa Web検索：完了結果

> **後続変更の案内（2026-10-16）**：以下はI-22移植時点の確認記録。検索の自由なcontents／beta指定は[Web defaults](web-retrieval-defaults.md)で、JSによるJSON解析とHost全文観測は[tool出力保存整理](tool-output-storage.md)で置換済み。[現行検索契約](../design/web-search.md)では検索要求をJSが組み立て、Hostが応答解析を担当する。

2026-10-12。[計画](../plans/i22-exa-web-search.md)に従い、旧HenjiのExa版web_searchを移植した。
利用者指定の既存キー参照を使い、親の直接tool call、run_js、選択した子から検索結果を利用できる。

## 実装

外部web-search.jsがExaのdefaults、公開option、beta header、JSON解析を所有する。
既定はauto/highlights、streamはfalse。ドメイン・日付、全文・抜粋・合成outputなどを指定でき、
未知request field・result metadataも保持する。Go側にExaの検索方針やbody上限を追加していない。
結果は通常のhjh JSON objectとして返す。

外部configのpromptGuidelinesへ旧版のsource選択、出典と合成結果の区別、URL引用、contents選択を移した。
標準default roleとこのrepositoryのworkspaceへweb_search、module、credential参照を反映した。
schemaは現在の[公式Exa契約](https://exa.ai/docs/reference/search)に合わせて
userLocationのobject形式やoutputSchemaのtext形式も説明する。

実装API requestProviderを外部tool/hookへ追加した。
appはcapture済みconfig、既存credential Manager/Binding、共通connection ownerを使い、
Go transportでBearerを付ける。モデル用credential-fileを別profileへ流用しない。
JS・model input・host観測へkeyや認証付きheaderを渡さず、transport errorの実tokenも省く。
HTTP context取消と既存host job joinを使い、認証付きredirectは旧版同様追わない。
一般のHTTP tool、認証UI、旧counter/evidence ownerは拡張していない。

公開requestとstatus/bodyは通常host observation、解析済み結果はtool結果・次model入力へ保存する。
config/JS source、guidelines/snapshot、予算・context attributionは既存経路を使う。
TUIのtool行には検索queryの短いpreviewを表示する。

## 確認

- 共通app test：直接/生成JS/子の検索、defaults/options/beta、完全JSONの次requestと履歴、別profile認証、key非露出。
- HTTP取消とtransport error：取消原因を保持し、実tokenをerror表示から除くことを確認。
- 変更packageの既存test/vetと独立review：対応必須findingなし。
- 安定候補の製品31 packageでrace testとvetをそれぞれ一回実施し、通過。
- git diff --check通過。Core/TUIのbinaryを更新した。

既存テストの標準tool一覧へweb_searchを追加した。
予算testはtool schemaの増加に合わせ、15KBの読取り1件を保持して2件は選択するという元の利用動作が
成立する入力量設定へ更新した。製品の予算処理や制限は変えていない。

## 実利用

独立workspace `/tmp/hjh-i22-live-d4x2d1qn`と独立Coreを使い、利用中Core・元Sessionは操作していない。
Exaは指定の既存credential参照、モデルは無料の`nvidia/nemotron-3.5-lightning:free`を使った。
モデルのprompt/completion価格0を公開catalogで確認してから実行した。

| 経路 | execution | 実確認 |
| --- | --- | --- |
| CLI直接検索 | `93e034fe` | auto＋highlights、domain filter、2結果とsource URLを使った最終回答 |
| Core/run_js | `4c57dbec` | fast検索、全文maxCharacters・highlights・text合成output、子の回収と回答統合 |
| 選択した子 | `f32f90f5` | web_searchだけの子、dynamic/verbosity highlights、旧Exa-Beta header、URL付き報告 |

三つの実Exa requestはHTTP 200で、各executionのmodel input・snapshot・guidelines・
host request/response・完全tool結果を照合した。
APIが返したcostDollars.totalの合計はUSD 0.021。これはAPI応答の費用表示である。
受信結果のrequestId、resolvedSearchType、results、searchTime、costDollars、任意outputを保持した。

Session `b2c3fff4`へTUIを実行中attachし、run_js内の検索行の字下げ・query preview、
子の簡潔な表示、URL付き最終回答、detach/再接続後の履歴表示を確認した。
保存contentsとsemantic payloadを実際のExa/model credential値で照合し、非露出を確認した。
検証script・proof・TUI capture・gate logはignored `dist/i22-verification/`に置いた。

通常workspaceで使うには新binaryでCoreを再起動する。tool/config編集はその後の次Workerから反映する。
専用Exa login UI、checkpoint、別検索provider、DB移行、commit/pushは今回の対象外。

## 2026-10-13：GPT系の直接tool callで任意引数を埋める問題の修正

利用者のSession `086cf62d`（Luna）、`d4cc3321`（Sol/high）、
`d099deaa`（Sol/medium）、`fb126c98`（Sol/max）で、不要な任意引数を
埋めた直接検索が失敗していた。Exaの403応答は企業契約専用のHIPAA指定に対する
`FEATURE_DISABLED`だったが、モデルにはstatusだけが届き、無関係な検索条件で再試行していた。
web_fetchも`save_to:""`で失敗し、run_jsから引数を省略して回復していた。
同じ改善前のschemaでも、確認したAnthropic等のSessionでは任意引数を省略できていた。

利用者の指示に従い、企業用`compliance`を標準のモデル向け引数一覧から除いた。
Responsesの通常・namespace内functionへ`strict:false`を明示し、外部toolが宣言する
required/optionalを保持する。schemaをnullable/全必須へ書き換えない。
[OpenAI公式契約](https://developers.openai.com/api/docs/guides/function-calling#strict-mode)では、
strict省略時にResponses側がschemaを正規化する場合があるため、この指定で明示的に回避する。
OpenRouter内部での変換過程を直接観測したわけではない。

Exaの`error`・`tag`・`requestId`をstatusとともにtool errorへ返すようにした。
tool自身のguidelinesに任意引数を必要な場合だけ指定する案内と、
本文読取り時はsave_toを省略する案内を追加した。配布assetsとrepositoryのworkspaceへ反映済み。

送信wireの通常/namespace形、外部schemaの保持、観測済み403理由の次モデル入力への伝達、
企業引数の除外、guidelinesの選択をfocused testで確認した。
既存のprovider/extension test、関係するapp経路とweb_fetch test、vet、diff checkが通過した。
Core/CLI・TUIのbinaryを再ビルドした。全体gateはこの局所修正では繰り返していない。

利用者の費用承認を受け、独立workspace `/tmp/hjh-web-tool-fix-v0is330n`で
同一の利用者プロンプトを`openai/gpt-6.1-sol / high`に一回だけ実行した。
Session `5e3105f3`、execution `32b6c791`は約18.2秒で完了し、モデル呼び出しは3回。
web_searchを直接1回（4結果）、web_fetchを直接1回呼び出し、いずれもHTTP 200。
検索の不要な任意引数やsave_toは省略され、失敗・再試行・run_js経由の回復はなかった。
公式URL付きの最終回答まで実経路で確認した。この一回の結果を速度保証やeffort比較には使わない。

実送信wireのstrict指定・schema・guidelines、引数、host response、次モデル入力を照合し、
保存内容への実credential値の非露出も確認した。
Exa応答のcostDollars.totalはUSD 0.007。証跡はignored
`dist/web-tool-fix-verification/`へ保存した。
利用中のCore・元Sessionは停止・変更していない。通常利用には新binaryでCoreを再起動する。
