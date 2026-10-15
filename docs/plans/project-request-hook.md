# 毎モデル要求のhookとコンテキストAPI

状態：S1〜S3実装・確認済み。[結果](../results/project-request-hook.md)を参照する。

## 目的と根拠

2026-10-15、利用者は自動コンパクションの検討に伴い、コンテキストへ介入するAPIと
`ProjectRequest`のhook実装を指定した。今回実装するのは外部JSから送信内容を参照・差替えできる
基盤であり、自動要約の発動条件、要約生成、Sessionをまたぐ要約状態の管理はまだ決めない。

必要な動作は、最初の要求とtool結果を受けた次の要求の両方で外部hookが動き、元の会話を
DBへ残したままモデル入力だけを変えられることである。hook未定義・差替えなしの場合は現行動作を保つ。

## 現行の実経路

CLIとCoreは`app.Run`から共通の`execute`へ入り、rootと子とも`agent.Run`を使う。
loopは追加入力を適用してから毎step `Input.ProjectRequest`をawaitする。
appのcallbackはcurrentメッセージと、DBを1turnずつ読む`budgetHistoryReader`を予算projectorへ渡す。
projectorは送信範囲だけを選び、loop内の原文・journalのmessage保存・正常終了時の会話採用は変更しない。
現行の外部hookは6種類で、開始hookはinstruction追加のみ。要求単位の差替えはない。

## 契約

- 外部export名は`projectRequest({runtime, modelStep}, api)`。Goの`ProjectRequest` callback内で、
  既存予算projectorが原文を外す前に呼ぶ。1回の論理要求につき一回。provider再送では再実行しない。
- `api.context.get()`は現在のinstruction・tools・prefix/currentを結合した原文messages・対応する
  sources・実効budgetを返す。過去のcanonical会話はこの配列へ一括読込みしない。
- `api.context.history({beforeTurn?})`は既存readerで過去の1turnを返す。
  最初は省略可。返ったnumberを次のbeforeTurnに渡して遡る。終端・子の独立実行ではnull。
  provider変更時のprivate state除去は既存readerに従う。
- `api.context.measure({messages?, instruction?})`は候補のwire bytes・既存推定tokens・budget・fitsを返す。
  省略したfieldは現在の候補を使う。モデル呼出しや候補の変更はしない。
- `api.context.replace({messages, instruction?})`は今回の要求全体のmessagesを差し替える。
  instruction省略時は現状維持。modelとtool選択は変えない。過去会話やrecall prefixを含めたい場合も
  hookが明示的に配列へ含める。返却値ではなくこの操作で変更する。
- replace後のget/measureは差替え候補を読む。hookとその非同期操作が正常終了した後だけ候補を採用する。
  次stepにはloopの原文が再度渡る。hookのmodule stateは同一Workerのstep間で保持できるが、
  次turn・Core再起動への保持契約は今回追加しない。
- 明示差替えは既存projectorで全文を計測し、予算超過なら既存のcontext budget errorへ進む。
  差替えた配列へ古い会話を再追加したり、要約を黙って削ったりしない。
- hook例外・API失敗が外へ伝播した場合は要求を送らず、既存hook失敗通知と実行清算を使う。
  キャンセルは既存Workerの中断・非同期joinへ接続する。

## 所有と保存

appの要求単位ownerが候補とhistory readerを所有する。contractsはJS bridge用の操作interfaceだけを置く。
QuickJSは既存owner goroutineからPromiseを処理し、Go値のみを非同期処理へ渡す。
context APIは要求hookの呼出しscopeへ接続し、生成run_jsの公開tool一覧へ追加しない。

DB schema・message・会話採用は変更しない。hook/API記録にはstep、操作名、入出力bytes等の短いmetadataだけを
残し、get/history/replaceの会話全文・tools定義・要約を毎step詳細記録へ複製しない。
実際の最終送信wireとbudgetは既存の最新snapshot保存経路に従う。

## スライスと確認

1. **S1 runtime契約**：handler捕捉、要求scopeのcontext bridge、Promise完了と短い観測記録。
   同一VMのstep間state、awaitしたAPI操作、返却値で会話全文を記録しないことをfocused testで確認する。
2. **S2 app接続**：要求ownerのget/history/measure/replace、親子共通callback、既存budgetと通知へ接続。
   tool後に原文を読んで差し替えられること、DB原文の維持、再開したturnで過去会話を読めること、
   差替えなしの通常履歴選択、失敗・取消でproviderへ送らないことを確認する。
3. **S3利用経路と文書**：契約と編集可能なhook例を文書化し、独立workspaceのcompiled CLIを
   実HTTP provider経路へ接続して、初回・tool後・Session再開の実送信と保存履歴を照合する。
   ローカルHTTPで確認し、外部API料金は発生させない。常用Core・DB・外部extensionsは操作しない。

各sliceで変更箇所のfocused testとdiffを確認する。安定後に関連package test・staticcheck・diff checkを
一回実行し、確認範囲と未確認事項を結果文書へ残す。自動要約・閾値・専用UI・新しい保存経路は対象外。
