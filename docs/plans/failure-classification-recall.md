# エラー分類と英語のrecall案内

2026-10-14。利用者が旧Henjiの分類に沿った短い英語表示と、必要な場合の`try /recall`を採用した。
起点`5d27f33`。旧資料は参考とし、parserの追加拒否条件や全文SSE保存は採用していない。

## 必要な動作・根拠

`bc6b68fc`のMiMo応答で不完全なtool callが発生した。
実provider再送では再現しなかったが、実受信SSEのID欠落・名前欠落・引数JSON不正で
同じ停止と未完了本文/thinkingをlocal HTTPで再現した。recall後の次実行成功も確認した。
詳細は残し、通常TUIには次の種類別表示を出す。実行IDを覚える操作を要求しない。

| 分類 | 短い理由 | recall案内 |
| --- | --- | --- |
| response_error | provider response invalid | try /recall |
| transport_error | provider connection failed | try /recall |
| provider_timeout | provider deadline exceeded | try /recall |
| http_error | provider request failed | なし |
| missing_credential | credential unavailable | なし |
| context_budget_exceeded | context budget exceeded | なし |
| commit_error | session save failed | なし |
| turn_cancelled | cancelled | なし |
| model_step_limit | step limit reached | なし |

分類済みの失敗rootは`FAILED · provider response invalid · try /recall`の形式。
recall案内は停止した同Sessionのrootを参照できる場合に出す。子の通知は短い理由のみ。
unknownの詳細表示、CLIの詳細エラーと成否、通常の停止/recall動作は維持する。

## 現行経路

protocolの受信/組立て→error・model_request error観測→agent.Result→appの共通終了→
host_decision/runtime outcome→history walk→conversation entity→TUI。
CoreのRunResult/Errorは別のlive terminal経路。現在はほぼ文字列だけを渡し、TUIは全文を表示する。
root execution entityは通常非表示なので、復元後にも短い失敗通知を表示する経路を追加する。
snapshotだけの保存失敗はモデル結果から分離された既存の別通知であり、この分類に混ぜない。

## Slice

### S1：型付き分類

contractsに小さいdiagnostic（code、parseReason、field、httpStatus）とerror wrapperを置く。
詳細文字列とerrors.Is/Asによる原因を維持する。
protocolの既存HTTP/通信/応答解析経路へ分類を添え、不完全metadataと不正引数JSONを
`incomplete_tool_call` / `invalid_tool_arguments`へ分け、欠落fieldを示す。
受理する応答の条件、timeout設定、retry、引数修復は変えない。credential redaction後も分類を保持する。

### S2：保存・Core・表示

credential解決、context budget、保存失敗、取消、step limitの既存責務で分類する。
観測・終了結果・実行Fact・Core/API・会話entityへ同じdiagnosticを通す。
独立snapshot/公開copyでは小さなdiagnosticもcopyする。
TUIは種類から短い英語表示を作り、保存済みrootの失敗通知は復元後にも表示する。
liveのrun endedと保存済み通知の二重表示を避ける。詳細はDB/inspect/CLIへ残す。
新しいDB table・schema version・migration・raw SSE archiveは追加しない。

### S3：確認

各sliceでfocused testと自己review。実際に変える分類/表示、credential redaction、
取消と保存失敗の区別、live/reopenの一致を確認する。
安定候補で全体Go test/staticcheckを一回実行する。
使い捨てDB/local HTTP/実CoreとPTY TUIで不正tool callの停止、短い通知と詳細保存、
復元後の通知、引数なしrecall→次の正常実行を確認する。
HTTP/通信エラーの案内差も確認する。今回の分類にはAPI料金を必要とする試験を加えない。
独立reviewは保存・live/reopen・取消/保存失敗の分類経路を対象にする。

## 現在地

S1〜S3実装・focused test・実Core/PTY確認・全体test/staticcheck・独立review完了。
review指摘4件を修正し、一回の限定再reviewで解消を確認した。
結果: [failure-classification-recall.md](../results/failure-classification-recall.md)。
常用DB/Core/distの切替は未実施。
