# I-22 Exa Web検索の移植

> **後続変更の案内（2026-10-16）**：検索は実装済み。以下の自由なcontents・beta指定は[Web defaults整理](../results/web-retrieval-defaults.md)で置換した。現行は検索・本文取得を分離し、検索既定10件・text:false・抜粋1,000文字、JSON解析はGo Hostが所有する。[検索契約](../design/web-search.md)を参照。

2026-10-12。利用者は旧HenjiのExa版web_searchをhjhへ移植し、既存のキー保存先を使うよう指定した。

## 利用動作・根拠

モデルがweb_searchを直接、またはrun_js内のtools.web_searchから呼び、検索結果のURL・タイトル・
抜粋/本文・metadata・任意の合成outputを受け取って回答に使える。子も選択した場合に同じ経路を使う。
既定はtype:auto、contents.highlights:true、stream:false。旧版のdomain/date、各検索mode、
contents、outputSchema等を外部APIへ渡し、未知fieldや追加result metadataを切り捨てない。
dynamic/verbosity highlights指定時の旧版Exa-Beta headerを維持する。

旧正本sourceは `/home/agent/projects/henji-harness/external-tools/web_search/`。
API契約は[Exa Search](https://exa.ai/docs/reference/search)、公式Exa SDK/agent-skillsも確認した。
公式の現在契約に合わせ、userLocationのobject形式とoutputSchemaのtext形式などを説明・schemaへ反映する。
既存file `~/.local/state/henji-harness/v1/credentials/exa-api-key` の存在を確認済み。値は表示しない。

## 実経路・責務

CLI/Core/TUI→app共通execution→capture済み外部JS web-search.js→api.requestProvider→
host dispatch→appのcredential bindingとHTTP→外部JSのJSON結果→通常tool結果/履歴/model次request。

現在のhttp host操作は認証なしGETであり、ExaのPOSTに必要な認証経路はまだない。
hostへcallback seamを追加し、appが既存credential Managerとcapture済みconfig.credentialsを使う。
認証headerはGo側で追加し、keyをJSの引数/結果、model入力、観測へ含めない。
API-key transportは旧版と同じBearer認証を使う。モデル用credential-file上書きを別profileへ流用しない。
HTTPのcontext取消と通常host job join/Worker終了処理を使う。
credential付きtransportのredirectは旧版同様に追わず、keyを別URLへ転送しない。

Exaのdefaults・schema・guidelines・response解析は外部toolが所有し、GoにExa名や検索方針を埋め込まない。
api.requestProviderは外部tool/hookの実装APIに置き、生成JSは選択tool経由で使う。
利用指示はI-21 promptGuidelinesへ載せる。standard/default roleと現在workspaceにweb_searchを追加する。
source capture、通常host/tool観測、snapshot、予算管理は既存経路を使い、旧Henji専用のcounter/evidence ownerは移植しない。

## 確認

focused testは、直接/生成JS/子の検索、queryとoptions/defaults、未知result field保持、guidelines/snapshot、
別profileのキー解決とcredential非露出、HTTP取消・Worker再利用という具体的利用動作を確認する。
実装中は変更packageのtest/format/vet/diff checkを行い、独立reviewで上記経路とcredential露出を確認する。
安定候補で製品packageのrace/vetを一回確認する。
既存キーを使った少数の実Exa検索と無料モデルでCLI/Core/TUIの回答・履歴を照合する。
利用中Core/元Sessionは操作せず、独立workspace/Coreを使う。

TUI包含表示の未commit変更は保持する。専用login UI、一般HTTP toolの拡張、別検索provider、
要約checkpoint、旧DB変換、commit/pushは今回の対象外。

## 実施状態

実装・focused確認・独立review・製品race/vet・実Exaの直接/生成JS/子とCLI/Core/TUI確認を完了した。
[完了結果](../results/i22-exa-web-search.md)へ実requestと保存・表示の照合結果を記録した。
