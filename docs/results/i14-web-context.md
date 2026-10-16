# I-14: Web読取り・Coreコンテキスト管理の結果

> **後続変更の案内（2026-10-16）**：以下はI-14実装当時の実行記録。通常受信1 MiBは[64 KiB](web-retrieval-defaults.md)へ、requestごとの精密wire／出典保存は[通常保存](../design/normal-history-storage.md)と[終了時snapshot](history-terminal-snapshots.md)へ後続変更済み。実施時点の測定・確認結果は保持する。

2026-10-12。[計画](../plans/i14-web-context.md)と[契約](../design/i14-web-context-contract.md)に対応する。
管理はCoreの共通実行処理が所有し、TUIには計算・履歴選択を置かない。

## 実装

web_fetchを登録tool、生成JS、Hostへ接続した。HTML可読化、通常のraw受信1MiB上限、
text/JSON/XMLの本文、バイナリmetadata、30秒deadline、原文save_toを実装した。
raw HTTPも使える。現在workspaceの拡張にも新toolとcapacity metadata設定を追加し、
利用者の選択model、既存role/instructionを保持した。既存workspaceの自動置換は行わない。

Coreはcaptured configと選択モデルのmetadataから予算を解決し、実adapter wireを測定する。
現在turnと過去turnの送信projectionを選択し、保存全文を変更しない。次turn・DB再開・
model変更は全文から改めて選び、子は独立したmodel/予算を使う。protected入力超過は
provider送信前に保存してcontext_budget_exceededで終了する。
予算fact、実wire、元messageのidentityを保存し、TUI /contextに出典・選択元・inspect先を表示する。

## 実利用経路

既存OpenRouter credential参照を使い、公開catalogでprompt/completion単価0を確認した
openrouter/freeを使った。追加ログイン、APIキー登録、費用のあるmodel実行はしていない。
private workspace/DB/port 18814/tmuxを使い、利用者のtmux mainは操作していない。

- 通常予算のCLIで、以前raw HTMLによりcontext超過した4ページ
  (pkg.go.dev modernc.org/quickjs、GitHub fastschema/qjs、buke/quickjs-go、dop251/goja)を
  web_fetchで全てHTTP200で読み取った。最終requestは159,623bytes、推定53,328tokens、
  入力予算160,000。回答のI14_WEB_OKまで完了した。
  providerが追加で生成したGitHubの誤ったREADME URLは404として記録された。
  最終回答のライブラリ比較の事実正確性は、このtool/予算管理の検証対象にはしていない。
- 小さい明示予算24,000を使った別runで、current exchangeを段階的に除外し、
  送信wireを予算内に保持した。除外した本文も保存した。モデルが省略済みページを再取得したため、
  このrunは回答品質・依頼完遂の証拠にしない。通常予算の上段をWeb作業の確認とする。
- Coreを起動し直し、上段SessionをTUIで開いて次turnを実行した。
  明示入力24,000/履歴16,000では、元turnの14messageから8を選択、6を省略した。
  元turnは14message全文のまま残り、回答I14_RESUME_OKを保存した。
- 実Core/QuickJS/登録web_fetchからprivate HTTP serverの1,200,000bytes本文を取得した。
  通常結果はtruncated:trueと[body truncated]、save_toのfileは正確に1,200,000bytes。
  生成JSはmetadataだけを返し、回答I14_BOUND_OKまで完了した。

## 機能確認とreview

focused確認では、実adapter PrepareとVM/file toolを通したcurrent削減、全文保存、DB再開、
大きいモデルへの変更による全文再選択、子の独立予算、送信前超過の保存を確認した。
予算moduleでは複数tool call/result batch、opaque replay、除外の単調性、
最新履歴の部分選択と古いturn省略、未知容量、override優先順位を確認した。
WebのHost/registered tool経路、HTML変換、1MiB、save_toを確認した。

変更されたapp/providers/contextbudget/history/agent/host/extensions/jsruntimeのrace付きtest、
corerun/httpapi/CLIのfocused package test、TUI /contextのrace付きfocused test、
対象packageのgo vet、format、git diff --checkを確認した。
TUI既存のTestSessionSwitchWaitsForFinalRunConversationはタイミングにより失敗した。
未変更HEADを別directoryへ展開してrace実行しても同じSession切替の待機で失敗した。
この既存testの安定化は今回の範囲に入れていない。

独立reviewで、catalogだけに載るmodelのcapacityが実行予算へ届かない点と、
/contextの根拠・保存元表示不足をP2として採用した。catalog読取りを共通化し、
capacityを親子共有snapshotから解決し、表示を補った。
実TUIでは通知Phase=selectedがstreaming扱いとなり後続描画を妨げたので、
Coreの通知をcompletedへ直し、保存replayもcompletedになることを確認した。
修正後の実Core/TUIでは新しいSessionでファイル読取りI14_SEED_OK、次turnの履歴省略通知、
回答I14_NOTICE_OKが順に表示された。/contextの有効予算・出典・保存元も実表示で確認した。
一回のtargeted re-reviewで両P2の解消と通知状態の整合を確認し、追加の重大regressionはなかった。

## 配布バイナリ

最終sourceからLinux/amd64 packageを生成し、archiveから展開した144fileのhash/sizeと、
hjh/hjh-tuiがstatic ELFであることを確認した。展開したペアでCore未起動からTUIを起動し、
既存Sessionの2turn、完了済み通知、回答I14_NOTICE_OK、/contextの保存予算と出典が
再表示されることを確認した。そのバイナリをdist/hjhとdist/hjh-tuiへ反映した。
利用者の既存Coreへは新バイナリの再起動で反映する。

archive SHA-256: `4f2fd9cddedb88224870b69c16ff696b8a811148acffd6be6934d8d7b56e8725`

## 限界

token数はwireからの推定であり、provider tokenizerと一致する保証はない。
capacityが公開されず、明示設定もないmodelは予算不明と記録する。
自動要約・compactionで保存全文を書き換える処理は追加していない。
ChatGPTの追加・再認証や別account切替は実施していない。
