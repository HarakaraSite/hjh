# JSコンパクション拡張

2026-10-15。[計画](../plans/js-compaction.md)のS1〜S3を実装・確認した。
サンプルは[compaction.js](../../examples/hooks/compaction.js)、契約は[JS compaction](../design/js-compaction.md)。
標準hookとしては有効化していない。

## 実装

- `api.model.generate`が親と同じprovider/model/effortと既存の認証・encoder・stream parserを使い、
  toolなしで一回問い合わせる。子execution・hook再入・agent loopを作らない。
- 要約instruction、発動量、原文保持量、要約の追加と差替えはJSへ置いた。
  過去の要約を再要約せず、未要約の新しい原文範囲だけを送る。
- 原文のsourceからJSが参照を付ける。Session/turn、完全execution ID、正確なmessage rangesを保持。
  完了turnは実際の最終assistant messageを参照し、進行中は存在しない最終回答を捏造しない。
- coverageと要約文はService/CoreのSessionメモリへ保持する。execution内のworking copyを使い、
  正常canonical採用後だけ次turnへ引き継ぐ。失敗・取消で前のSession状態を上書きしない。
  子は独立、Session削除時に解放、Core再起動後は空になる。
- from-turnを変更するとサンプルのstateをリセットする。元の会話選択範囲外の要約を再利用しない。
- 要約用の送受信全文・thinkingはjournal/child/messages/latest requestへ追加保存しない。
  model APIの短いoperation metadataとusageは残す。
  **通常の最終送信snapshotには、モデルへ渡した要約文が含まれ得る。**
  要約専用stateの永続保存や、agent全体のDB非保存modeとは別である。

agent全体の非保存modeの希望は[将来検討](../design/agent-storage-modes.md)へ記録した。

## スライス確認

S1：実QuickJSと共通Agent経路から一回model APIを使った。
親selectionの継承、toolなし、子を作らないこと、入力・出力・thinkingの非保存とusage metadataを確認した。
成功turnの引継ぎ、失敗turnの非公開、取消時のpending operation join、長さ上限での未完了要約の不採用、
Session削除・Service再作成によるメモリ解放を確認した。

S2：サンプルJSを実Workerで実行し、tool結果二件後の要約・差替えを確認した。
次turnには新しい残り範囲だけを要約し、三つ目のturnで以前の範囲を再要約しないことを確認した。
最初の進行中参照にfinalなし、完了turnの追加参照にfinal message 5が付き、そのIDでDB原文へ戻れた。
raw tool結果と会話を保存し続け、要約文をcanonical会話へ混ぜないことも確認した。
親子memory独立と明示from-turn変更時のリセットも確認した。

## 実プロバイダ・Coreでの観測

許可済みOpenCode Go / DeepSeek V4.1 Flash / autoを独立Core＋接続CLIで使用した。
観測用JS値は入力6,000 tokens、発動50%、原文目標40%、要約目標600文字へ調整した。
roleのtoolはread_fileだけ。標準値の最適性を示す実験ではない。

1. start.txtとNEXT_FILE先のdetails.txtを順に読み、識別子二つを回答。
2. 同じCore/Sessionでthird.txtを読み、前の二つを含む三つの識別子を回答。
3. Coreを停止・再起動し、同じSessionを再開。ファイルを再読取りせず、原文から再要約して三つを回答。

全turnが成功し、AX-314・BY-271・CZ-161を正しく引き継いだ。

| turn / step | 要約済みnote数 | そのexecutionの要約model要求数 | 要約後の推定入力tokens |
| --- | ---: | ---: | ---: |
| 1 / 1 | 0 | 0 | 689 |
| 1 / 2 | 0 | 0 | 2,777 |
| 1 / 3 | 1 | 1 | 3,239 |
| 2 / 1 | 2 | 1 | 1,174 |
| 2 / 2 | 2 | 1 | 3,301 |
| 3 / 1（Core再起動後） | 1 | 1 | 933 |

親の要求6回、要約要求3回、child execution 0。
要約文は333・363・310文字だった。要約要求だけのprovider報告usageは入力8,552 tokens、
出力2,432 tokens（reasoningを含む）。要約にもAPI料金と待ち時間が発生する。
turn経過は約9.50・8.12・8.59秒で、親model・要約・CLI/保存を含む。

全source rangeとfinal message参照がDBへ解決した。CLI canonical読戻しで元の三ファイルの本文を確認し、
Continuation summaryがcanonical会話へ採用されていないことを確認した。
要約instruction/入力全文の追加archiveなし、DB schema4/integrity ok、credential非露出。
Sessionは`070c2bd7-031b-4494-8165-4a4be59c334c`（独立DB）。

証跡：`/tmp/hjh-js-compaction-9wvxzfho/observe.py`、`result.json`、`canonical.json`、
turn-{1,2,3}.stdout/stderr、workspace/.hjh/observations/。
数値と参照metadataの抜粋は[集計JSON](js-compaction-data.json)。
常用Core・DB・extensions・distは変更していない。

## 検証と残る範囲

- 各sliceのfocused test成功。
- app/jsruntime/extensions/contextbudget/agent/corerun/httpapi/cmd/hjhの関連8 package test成功。
- app/jsruntimeのRequestModel・ContextMemory・JSCompaction・ProjectRequest focused race成功。
- 同package群のGo 1.27対応staticcheck成功
  （`honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45`）。
- rootが要求順序、coverageの正確な範囲、正常採用前後の公開、保存境界、最終message参照を確認した。
  子で親メモリを不要にcopyしないよう整理し、該当focused testで確認した。
  独立reviewerは起動していない。全repository gateは繰り返していない。
- git diff --check成功。

これはopt-inの試作拡張で、短い実経路を確認した段階。数時間・数日の継続や閾値の調整は未確認。
自動の分割要約、永続checkpoint、/compact UI、参照を読む新tool、agent全体の非保存modeは追加していない。
