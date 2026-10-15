# 毎モデル要求のhookとコンテキストAPI

2026-10-15。[計画](../plans/project-request-hook.md)のS1〜S3を完了した。
公開契約と使用例は[Request context API](../design/request-context.md)を参照する。
measureの履歴込み計測・select・JS指定予算は後続の[選択API結果](request-context-selection.md)で拡張した。

## 実装した動作

- 親子共通の送信前経路から`projectRequest({runtime, modelStep}, api)`を呼ぶ。
  初回、tool後、追加入力適用後の各論理要求が対象。provider再送では再実行しない。
- `api.context.get/history/measure/replace`を追加。appの要求ownerが候補と履歴readerを所有し、
  QuickJSは既存Promise/host完了経路でGo値を受け渡す。
- raw current transcriptを予算選択前に読める。過去canonical turnは一件ずつ読み、
  既存のprovider/model変更によるprivate state除去を使う。
- replaceは今回のmessages全体と任意のinstructionを変更する。modelとtoolsは維持する。
  正常終了後だけ候補を適用し、既存budgetで測る。超過は既存context-budget失敗。
  明示差替えへ古い会話を再追加したり、要約を黙って削ったりしない。
- 原文のloop、message保存、canonical採用、会話表示を変更しない。
  差替えなしでは通常の予算選択を維持する。
- hook/APIの自動記録へ原文・候補・tools定義・hook返却値を複製しない。
  APIの観測は操作名と入出力bytes、hookはruntime/stepと成否のみ。
  最終wireとbudgetは既存の最新snapshot経路に従う。schema/保存表は追加していない。

## スライス確認

S1：実QuickJSでawaitしたcontext操作と同じmodule stateのstep間継続を確認。
hookが全文を返しても記録しないこと、取消時に待機中のcontext操作をjoinしてWorkerが終了することを確認した。
宣言handlersと実export捕捉へprojectRequestを追加し、既存lifecycle focused testも通過した。

S2：実Agent loopからread_fileを呼び、次要求のhookで原文を取得して要約相当の入力へ差し替えた。
次turnは新Workerから前turnの原文を読め、DB会話には差替え文が含まれないことを確認した。
readonly hookで通常履歴が入ること、親子の候補が独立すること、hook例外で送らないこと、
measureと最終budgetが予算超過を扱うことを確認した。予算Factのkindはhook、history理由は
project_request_hookとし、canonical message ordinalと区別した。

S3：ビルドしたCLIを独立workspace・local HTTP Chat providerへ接続した。
最初のHTTPを503にして同じwireが再送され、hookのcounterが増えないことを確認。
read_file後の要求差替え、別processのCoreへ同じSessionを再開して過去原文を読んだ上での差替え、
Core接続CLIのcanonical履歴読戻しを照合した。HTTP4回（1回の再送を含む）、有料外部API呼出し0回。
hook/API記録の本文複製0、DB schema4/WAL/integrity ok。

証跡：`/tmp/hjh-request-context-m2gwuv4h/`の`verify.py`、`result.json`、
`wire-{1,2,3,4}.json`、`canonical.json`、CLI stdout/stderr。
常用Core・DB・外部extensions・dist binaryは変更していない。

## 検証と確認範囲

- 各sliceのapp/jsruntime/extensions focused test成功。
- `go test -race ./internal/app ./internal/jsruntime -run TestProjectRequest -count=1`成功。
- 関連packageの`go test ./internal/app ./internal/jsruntime ./internal/extensions ./internal/contextbudget ./internal/agent ./internal/corerun ./internal/httpapi ./cmd/hjh -count=1`成功。
- 同じpackage群へGo 1.27対応のstaticcheck
  `go run honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45 ...`成功。
  PATH上のv0.7.0はtoolchainのexport dataを読めないため、既存の検証文書と同じ対応版を使用した。
- `git diff --check`成功。
- rootが実行順、候補の所有、取消のjoin、DB保存境界、retryの位置と実送信を確認した。
  独立reviewerは起動していない。全repository gateは繰り返していない。

## 次段階

自動コンパクション・要約モデル呼出し・発動閾値・要約の長さ・保持範囲・Core/Sessionでの
要約state管理・手動/compactは未実装。今回のhookはその方針を実装できる介入点である。
hookのmodule変数は同一execution内だけ保持され、次turnやCore再起動へは引き継がない。
