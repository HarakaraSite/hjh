# 最初のTUI

2026-10-10。非対話runと `--stream` の実provider確認後、利用者の「では次の段階へ」に従い、
[採用方針](../design/initial-product.md)の次段階であるTUIへ進む。

## 必要な動作と根拠

`hjh tui` から日本語の依頼を入力し、OpenRouterの本文・tool進捗を追いながら実ファイル作業を完了する。
続けて依頼でき、保存Sessionを選んで別起動から再開する。停止しても画面が使え、途中の証拠を閲覧できる。
既存の非対話run、保存契約、外部拡張、会話採用を共通操作から使う。
TUIの通信方式は、利用者の回答により共通Go操作を同じprocessから呼ぶ方式に確定した。
UIはbackend操作から分離する。

現行経路は `CLI → app.Run → Agent/OpenRouter/JS Worker → history`。
観測は保存後、直列化されたProgress observerへ届き、run返却前にWorker・watcherをjoinする。
Session・会話・execution・構成・contextのqueryはhistoryの公開操作にある。
CLIのcredential参照はcmd内部にあり、TUIと共用するため専用moduleへ移す。

## 操作と責務

- 入口：`hjh tui [--workspace PATH] [--db PATH] [--session ID] [--model ID] [--config PATH]`。
- 会話と進捗をscrollできる画面、複数行入力。Enterで依頼、Alt-Enter／Ctrl-Jで改行。
- 実行中はEscで停止して画面に留まる。Ctrl-Cで終了する場合も実行停止・保存完了を待つ。
- Session一覧・選択、新規会話、Session IDによる再開。正常会話と失敗・取消の実行証拠を分ける。
- model選択、履歴の完全turn範囲、executionのordinal範囲をUI操作から指定できる。
- tool／hookの編集は既存方式で次のrunへ反映する。

`internal/tui` は入力・表示状態と実行中のcontextを所有し、DBやVMを直接扱わない。
実行は別goroutineで共通操作を呼ぶ。UI event loopだけで画面stateを更新する。
共通app serviceが既存Run・history queryを公開し、CLIとTUIから使う。
終了時は観測を受け取れなくなってもrunをcancel・joinしてからDBを閉じる。

画面基盤は[Bubble Tea v2](https://github.com/charmbracelet/bubbletea/blob/main/README.md)と
Bubblesのtextarea／viewportを使う。旧Henji・Crushは参考とし、旧Core・queue・permission機能を移植しない。
同じ画面で一つのforeground runを扱い、入力draftは実行中も編集できる。
並列Session実行、子Agent、remote接続、TUIの全装飾は別の段階で扱う。

## 確認

先に動作を実装し、その操作に対応するfocused確認を行う。
日本語・複数行入力、本文更新中の画面応答、取消後の再依頼、会話と証拠の復元、
正常採用後の別起動再開を確認する。UI終了時の実行joinは実際の待機HTTP／command経路で確認する。
CLIの非対話・stream出力とcredential参照の共用にもfocused確認を行う。

production binaryをPTYで操作し、利用者指定の `deepseek/deepseek-v4.1-flash` と
旧Henjiのcredential参照先でファイル作業・逐次表示・停止・再開を実施する。
画面state testやfixtureだけでTUIの完了としない。
停止とevent転送の並行処理には独立reviewの具体的価値があるため、安定候補でその接続を確認する。
実行証拠と未確認範囲は結果文書へ記録し、release公開は行わない。

## 完了状態

同一processの共通Go操作による初版TUIを実装した。
実PTY／OpenRouterでfile作業、逐次表示、停止後のdraft送信、保存Sessionの再開・閲覧、
終了時の取消保存、静的配布物からの作業を確認した。
reviewで見つかったworkspaceとmodel優先順の2件、および親の実画面で確認した
進捗JSONによる回答の重複表示を修正し、re-reviewを完了した。
証拠と確認範囲は[結果文書](../results/first-tui.md)を参照する。
