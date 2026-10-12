# I-21 toolごとの利用指示：完了結果

2026-10-12。[計画](../plans/i21-tool-guidelines.md)に従い、任意toolの利用指示を宣言して
有効toolから共通model instructionへ集める機構を実装した。

## 実装

外部config.toolsのoptional `promptGuidelines: string[]`を、role.toolsと子のtools上書きを
解決した後にtool順・配列順で収集する。省略・空配列は指示なし。
親・子は同じcaptureと共通実行経路を使い、toolなしの子には集約欄を加えない。
今後の外部toolも同fieldの宣言だけで利用できる。

run_jsのasync本文、英文目的コメント、QuickJS待機、選択したJS bridge APIの案内と、
六つの子agent操作の利用指示を同じ経路へ移した。run_agentのawait/Promise.all、
statusのcursor・報告return・receipt、collectによる完了待ちも維持する。
一般的な途中報告指示とrole catalogは別の責務として残す。

`Active tool guidelines`欄をsystemInstructionへ組み込み、同じ本文を
`instruction:active-tool-guidelines` componentへ保存する。
snapshot.toolsには各toolのguidelinesを保存し、元config bytesは既存のresource archiveへ保存する。
provider tool definitionはname/description/inputSchemaのままで、追加fieldを送らない。
実instructionは通常の予算管理・model request保存・context attributionを使う。

標準配布configとこのrepositoryのworkspaceの六つの子toolへ宣言を反映した。
workspaceのprovider・credential・role・独自tool設定は保持した。
現在のWorkerは開始時captureのまま、編集は次turn・子開始時から反映する。
要約checkpoint、専用管理CLI、旧DB変換は追加していない。

## 確認

- extensions focused test：任意toolの宣言、選択順、空・省略、override、snapshot/component、元source bytes。
- 共通app test：親、guide_probeだけの子、toolなしの子、実requestと保存構成、同Session次turnの編集反映。
- 独立reviewと変更箇所の再確認：対応必須の製品不具合なし。statusの報告returnとrun_agentの並列await案内を補足した。
- 安定候補の製品31 package：`go test -race -count=1`と`go vet`をそれぞれ一回実施し、通過。
- `git diff --check`通過。`dist/hjh`と`dist/hjh-tui`を現sourceで再buildした。

実利用は独立workspace `/tmp/hjh-i21-live-qml51_ur`と独立Coreで行った。
保存済みOpenRouter credential参照と`nvidia/nemotron-3.5-lightning:free`を使用し、
呼出し前の公開catalogでprompt/completion価格0を確認した。利用中Core・利用者Sessionは操作していない。

| 経路 | 確認結果 |
| --- | --- |
| CLI直接run `9da5cdae` | 任意guide_probeを実行し、taskには書いていない宣言側suffix `GUIDED_V1`を最終回答へ反映 |
| Core親 `9a35e7a6` | run_jsが英文目的コメント付きで二つの子を開始し、自分のfile読取り後に回収。親子結果を最終回答へ統合 |
| 子 `88d70397` | guide_probeだけを明示選択し、`BETA=29 GUIDED_V1`を回答。run_js/子操作のguidelinesなし |
| 子 `b0abfc44` | tools空配列、集約欄なし、`EMPTY_CHILD_OK`を回答 |
| TUI次turn `9f4fd493` | configをV2へ編集し、実行中detach/reattach、完了後の再表示を確認。回答は`BETA=29 GUIDED_V2` |

各実requestの有効tool、集約指示、snapshot/componentを照合した。
次turnのinstructionsにはV1が残らずV2を含み、過去会話への保存は維持した。
検証script、wire照合結果、TUI capture、gate logはignored `dist/i21-verification/`に置いた。

モデルの指示遵守には限界がある。初回の広い依頼では無料モデルがAPI名・console・returnを誤り、
step上限に達した。別の単純runでもsuffixを省いた。
tool固有の報告指示への言及とJS手順を具体化した確認では上記の動作が完了した。
guidelinesが渡る機構の正しさと、モデルが毎回その指示を守ることは区別している。

既存Coreで新しい収集処理を使うには、新binaryでのCore再起動が必要。
commit/pushはこの実装依頼に含めていない。
