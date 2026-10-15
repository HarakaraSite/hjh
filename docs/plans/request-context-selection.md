# JSからの要求予算・履歴選択

状態：S1〜S3実装・確認済み。[結果](../results/request-context-selection.md)を参照する。

2026-10-15。利用者は、コンパクションを通常の履歴除外より先に行う構成と、除外処理のAPI化・
JSによる予算制御を採用した。本計画は既存projectRequest/context APIをその用途へ拡張する。

## 必要な動作と実経路

実プロバイダ観測で、次turnのmeasureは690 tokensなのに過去履歴追加後の送信は3,086 tokensだった。
現在はhook→通常projector→送信の順で、replaceした候補はそのまま予算確認し超過なら失敗する。
JSが履歴を含めて圧迫を検出し、必要な処理後に既存の除外を呼び、最後に送信候補を確定できるようにする。
今回も要約モデルの呼出し・自動発動・Session単位の要約stateは実装しない。

## API契約

- `measure()`の既定はrequest：既存projectorが読む履歴を含める。
  top-levelは除外前候補のwireBytes/estimatedTokens/messageCount/fits/budget。
  `selection`に除外後の同じ数値、omitted、historyStatusを返す。
- 読む履歴は通常projectorと同じ、最新turnから最初の収まらないturnまで。
  除外前候補はその範囲で最も大きかった候補。DB全履歴の総和ではなく、coverageを明記する。
  current原文は過去stepで除外済みでも除外前計測の対象にする。
- `measure({scope:"current"})`はprefix/currentだけを正確に測る。
  `measure({messages, instruction?})`は明示配列だけを正確に測り、履歴は追加しない。
  replace済みの場合も以後のmeasureは差替え候補だけを扱う。
- `select({messages?,instruction?,budget?})`を追加する。省略時は通常projectorと同じ履歴選択、
  明示配列は既存のtask/steering・最新exchange保護と完全なcall/result単位の除外を適用する。
  `{messages,instruction,sources,budget,wireBytes,estimatedTokens,messageCount,fits,omitted,candidate}`を返す。
  保持必須部分が収まらない場合はfits:false。呼出しだけでは候補も通常projectorの状態も変えない。
- measure/select/replaceは`budget`を受ける。既存ValuesのcontextTokens/inputTokens/outputReserve/
  inputRatio/historyTokensを部分上書きし、既存式でlimitを計算する。未指定値は現在の予算を使う。
  JSは設定値から増減できる。新しい上限clampは追加しない。
- measure/selectの予算はその呼出しだけ。`replace({messages,budget})`で今回の送信に適用する。
  `await api.context.replace(await api.context.select({budget:{inputTokens:12000}}))`で
  選択結果と予算を一緒に採用できる。新しい全体設定やDB状態は作らない。
- 最終Go処理は確定候補とその実効予算を計測して送る。明示replaceをさらに黙って除外しない。
  hook未定義・replaceなしの通常経路は維持する。

## 所有・非変更の境界

Goのcontextbudgetが予算式・provider encoder・message grouping・選択を所有し、JSは順序と値を選ぶ。
Previewは通常projectorの除外済みindex/stateをcopyして動かし、元stateは変更しない。
DB履歴readerの既存cacheは共有し、同じturnをAPI/送信で重ねて読まない。
除外前計測のために全DBを読み切らず、新規の選択エンジンや保存表を作らない。
API観測は引き続きbytesだけ。全文や予算選択候補を毎step詳細記録へ複製しない。

## スライス

1. **S1予算とPreview**：既存予算式の再利用、request単位override、非破壊Previewと除外前計測を追加。
   読取り範囲、元projectorの非変更、tool exchangeの選択、JS指定予算の増減をfocused確認する。
2. **S2 API接続**：measureのscope、select、replace予算伝播、bridge、要求ownerを接続。
   再開turnの履歴込み計測、低い予算での選択後に通常送信へ影響しないこと、選択結果を明示適用した
   実送信と予算記録、要約相当message追加後の除外を確認する。
3. **S3 サンプルと利用経路**：計測サンプルを新APIへ合わせる。予算指定→選択→replace例を追加。
   独立workspaceのcompiled CLIで送信・保存を確認し、許可済みOpenCode Go/DeepSeek V4.1 Flashで
   短い2turn観測を再度行う。常用Core/DB/extensionsは操作しない。

sliceごとにfocused test/diff確認。安定後に関連package test・focused race・対応staticcheckを一回実行。
rootが責務と実送信を確認し、結果と残る未実装を文書化する。独立review・全体gateは今回追加しない。
