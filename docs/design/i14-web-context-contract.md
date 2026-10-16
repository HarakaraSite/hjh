# Web読取りとCoreコンテキスト管理

[計画](../plans/i14-web-context.md)に対応する。予算計算、履歴の選択、送信内容の記録は
Coreの責務である。TUIはCoreから受け取った通知と`/context`の結果を表示する。
CLIの直接runも同じCoreの共通実行処理を使う。

I-18で追加したAGENTS.mdとskill manifestはinstructionの一部として同じwire計測へ入る。
利用時に取得したskill本文は通常のtool結果であり、ターン内・ターン間の履歴投影も同じ経路を使う。
詳細は[workspace指示とskills](workspace-instructions-skills.md)を参照する。

## Web読取り

`web_fetch({url, save_to?})`は最終URL、HTTP status、Content-Typeと本文を可読文字列で返す。
通常は返却用に保持する本文を64 KiB（65,536 bytes）までとし、超過を`truncated: true`と`[body truncated]`で示す。
stdlib streamには内部read-aheadがあるため、これはnetwork転送量そのもののbyte上限ではない。
この上限は[Web取得の既定値整理](../plans/web-retrieval-defaults.md)による現行契約で、
初期I-14の1 MiBを置き換える。[確認結果](../results/web-retrieval-defaults.md)も参照する。
上限はHTML変換前の受信bytesに適用し、metadataや切詰めmarkerは上限の外へ付加する。
HTMLはscript/style/comment/tagを除き、block改行とentityを整える。JS描画は行わない。
text/*、JSON、XML(+json/+xml含む)は文字列、その他はmetadataを返す。
GETはredirectを辿り、30秒のdeadlineと実行取消を使う。非2xxはtool errorになる。

`save_to`はworkspace相対またはabsolute pathへ元のresponse bytesを作成/置換する。
通常読取りの64 KiB上限は適用せず、path、byte数、HTTP metadataだけを会話へ戻す。
親directoryは事前に用意する（write_fileの自動作成とは異なる）。中断時の途中fileはそのまま残る。
外部JS moduleはstdlib fetch／stream／fsを使い、生成JSは`tools.web_fetch`を呼ぶ。
raw status/bodyが必要なら登録`http`またはstdlib fetchを使い、旧api.webFetch／api.httpは提供しない。

## 予算設定

captured `config.json`の`contextBudget`で明示設定できる。

```json
{
  "contextBudget": {
    "defaults": { "inputRatio": 0.9 },
    "providers": {
      "my-provider": { "contextTokens": 100000, "outputReserve": 10000 }
    },
    "models": {
      "my-provider/my-model": { "inputTokens": 70000, "historyTokens": 50000 }
    }
  }
}
```

fieldごとにmodels > providers > defaults > capacity metadataの順で解決する。
未指定のinputRatioは0.9。contextTokens=C、inputTokens=I、outputReserve=Rから
`min(floor(C*q), I, C-R)`を既知項だけで計算する。実際に予約していないcatalogの
max outputをRとみなさない。現在adapterはmax_tokens/max_output_tokensを送らないので
Rは未指定なら不明。historyTokensは任意の履歴だけの追加予算で、既定の固定上限はない。

capacityは設定モデル情報、provider catalog、[models.devの公開API](https://models.dev/api.json)の順で
未指定fieldを補う。標準OpenRouterは[公開catalog](https://openrouter.ai/api/v1/models)の
`context_length`を使う。models.devでは設定されたbaseURLに
対応するprovider/modelを選ぶ。重複候補は不明。provider定義の`modelsDevProviderId`で
対応先を明示できる。標準OpenAIのAPI/ChatGPT定義は`openai`を明示する。
同じmodel IDでも異なるrouteの容量を自動流用しない。`providers[].models`に置く
`contextTokens`/`inputTokens`も容量情報として使える。metadata snapshotは親子間で共有し、
各executionは自分のcaptured configとselectionで予算を解決する。catalog snapshotも同じ
構成/接続/認証の親子間で共有する。別runでは新しいmetadataを読む。
metadata取得が失敗または容量不明でも架空の上限を作らず、取得状態と不明fieldを保存する。

実際のadapter wire bytes全体から`ceil(bytes/3)+16+8*messageCount`でtoken数を推定する。
instruction、tools、reasoning、opaque replayも含む。これは正確なtokenizerではなく、
providerの実token数との差は残る。historyTokensにはinstruction/tools/current/recallを含めず、
履歴だけを同じadapterでencodeしたwireとframingから推定する。

## 履歴と送信内容

保存する会話全文は変更しない。Coreが毎requestに送信projectionを作る。
taskとsteering、最新assistant応答、最新の完全なtool交換、未完了交換を保護する。
古い完全なassistant/tool交換を単位ごとに除外し、call/resultやreplayの途中を切らない。
現在turnで除外した単位は同じexecution中に戻さず、除外を開始した後は過去turnも再追加しない。

履歴は最新のadopted turnから読み、予算が満ちるところでDBの本文読取りを止める。
最新turn全文が入らなければ保護部分を残して古い単位を除外し、それより古いturnは追加しない。
保護部分も入らなければ過去履歴を省略し、現在taskで継続する。
最新turn全文が入れば古いturnを丸ごと追加し、実際の送信順は時系列にする。
`--from-turn`で候補の下限を指定できる。次turn、Session再開、model変更では保存全文から
改めて選ぶ。モデル変更時には従来のprovider state無効化を適用する。
子AgentはSessionの採用会話を継承せず、自分のmodelと独立した予算で同じ処理を使う。

保護された現在入力が予算を超えると、送信前に`context_budget_exceeded`で実行を終了する。
依頼と完了済み処理の証拠は保存し、その失敗turnは会話に採用しない。
要約やcompactionで原文を置き換える処理は追加しない。

## 確認できる記録

保存粒度は後続の[通常履歴の保存契約](normal-history-storage.md)に従う。
初期I-14の各requestのwire・精密出典archiveは置換済みで、`context_item.sourceRelations`は生成しない。

- 各回の`context_budget` recordには予算値と設定出所、推定cost、選択/除外件数などのsummaryを保存する。
  元execution/turn/message ordinal、除外index、turn別の選択/除外理由を含む完全なFactは、
  executionごとに最新1件だけ保持する。過去recordは`detailAvailable:false`であり、詳細を復元しない。
- 各回の`model_request`は件数・model・step・wire bytes等の小さなmetadataを保存する。
  正確なwireはexecutionごとに最新の送信準備済み1件だけ保持し、予算recordへ重複保存しない。
  予算超過の未送信候補wireは保存せず、前の準備済みwireがあれば保持する。
- 最新wireと完全なFactは実行中にはServiceのsnapshot ownerがメモリ保持し、
  モデル終了確定後にschema v4の専用tableへ保存する。親子は別execution IDで保持する。
  inspect/Recordは同じrecord IDに対応する最新詳細だけを返し、
  `memory / saved / failed / unavailable`で保存状態を区別する。

詳細は[終了時保存の計画](../plans/history-terminal-snapshots.md)と
[確認結果](../results/history-terminal-snapshots.md)を参照する。

Coreは現在turnの削減と履歴部分選択/省略を種類ごとに一度通知し、再接続でも読み戻せる。
`/context`の`latestBudget`は予算recordを持つ最新rootの選択を示し、送信前超過も確認できる。
`latestRequest`は最後に送信準備まで進んだrequestなので、両者のexecutionが異なる場合がある。
TUIは有効なC/I/R/q/Hとfieldごとの出典を表示し、最新の完全なFactが利用可能な場合は
履歴turnごとの保存元と選択/除外message ordinalも表示する。予算recordとwireのinspection先も示す。履歴全文が予算を満たす場合にそれより古い
turnを読み進め、入らないturn以降の本文は展開しない。
CLIの`inspect`では子を含む各回summaryと、利用可能な最新の完全なFact・実wireを参照できる。
全requestの全文・精密出典が残る契約ではない。
