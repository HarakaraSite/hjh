# I-07：Session選択・管理とdetach／再接続

2026-10-11。利用者はtool詳細を現状維持し、`/view`は不要としたうえで、それ以外の次段階を実行するよう指示した。
I-06のSession段階から、選択、rename、detach／再接続、Core停止を今回の範囲とする。
tool詳細・スクリプト閲覧、`/view`、Session削除、色、Glamour、provider pickerは対象外。

## 利用者の操作と結果

- F4／`/sessions`でSessionを選び、Enterで会話を続ける。現在のSessionを初期選択し、
  名前、ID、workspace、model、turn数、実行中の印を読める。選択中もCoreの実行は進む。
- `/rename TEXT`で現在のSessionの名前を保存し、一覧・再起動後も読む。
- `/detach`／Ctrl+DでTUIを終了し、runを継続する。Coreがrunを受け付け、Sessionを保存する前の
  detachは保存済みSession IDが確定するまで待ち（preparing中でも保存済みIDが届いたら切断可能）、再接続先をscrollbackへ残す。
- `--session ID`／`/resume ID`／picker選択で、そのSessionの実行中runがあれば同じrunの
  snapshot＋SSEへ再接続する。新runをPOSTせず、保存済み会話と同IDへmergeする。
  実行が完了していたら履歴を読み、そのSessionで次の依頼を送れる。
- F1はrunのcancel。`/quit`／Ctrl+Qは旧Henjiに合わせCore停止へ切り替える。
  Coreは新run受付を止め、全runと子Agentをcancel・joinし、終了保存後にHTTPとprocessを終了する。
  TUIはCore停止を待って終了する。SIGINT／SIGTERMでのCore停止も既存経路を利用する。
- 別Sessionへの切替中のrun cancel・待機は既存の動作を維持する。同じSessionの選択は実行を止めない。

## 現行経路と所有者

TUIの`/sessions`→apiclient.Sessions→HTTP→Service→history.ListSessionsで一覧を得る。
Sessionのtitleは既存DDLにあり、renameはtitle／updated_atだけを更新する。
会話採用のstate_revisionを変更せず、実行中renameで正常なturn保存を競合させない。

CoreのManager.sessionsがactive runの予約を既に所有する。この対応から
GET `/api/v2/sessions/{id}/active-run?coreId=...`で軽いRunStateまたはnullを返す。
一覧の`activeRunId`もこの対応から得る。新しいrun索引や保存先を追加しない。
title更新はPOST `/api/v2/sessions/{id}/rename`、Core停止はPOST `/api/v2/core/shutdown`。

apiclientは既存runHandleのSSE受信を共有し、AttachRunではGETによる確認から始める。
StartRunのPOST再送とattachのGETを分け、再接続でtaskを再実行しない。
TUIのSession読取はactive runを先に捕捉し、history snapshotを得た後で同runを購読する。
準備中の新Sessionはrunの保存済みadmission後にmetadataと履歴を読み込む。
読取中にrunが終了しても、同runの最終snapshotで保存された完成会話へ追い付く。

detach先のnoticeは終了時の確定出力として会話のmutable本文と分け、scrollbackへ必ず残す。
detach時はClient.CloseでHTTP／SSEだけを終了し、cancel APIを呼ばない。
TUI終了処理はdetachと通常cancelを区別し、通信jobをclose・joinする。
Core停止要求は短い受付応答を返し、serveの寿命管理が既存Manager.CloseとHTTP終了を進める。
停止要求へのHTTP応答を、runのjoinや描画完了へ結び付けない。
Core内部の会話・保存・順序・入力所有者はI-06の契約を維持する。

## 確認と完了条件

変更経路のfocused testで、rename中もturn採用できること、attachがPOSTしないこと、
detachがcancelしないこと、Session切替と同Session選択、picker選択位置、停止保存を確認する。
既存Sessionを再開したprivate tmuxでF4・rename・切断・再接続を操作する。
実providerのrunをprivate Core/TUIから開始し、途中detach後も継続すること、再接続で同runと会話へ
追い付くこと、次の依頼を続けられることを確認する。Core停止は検証用processだけで確認する。
利用者のDB・稼働中Core/TUI・tmux・credential設定は変更しない。

通常・批判的観点の独立reviewは旧Henjiの現行Session選択・rename・detach・Core停止と対比する。
初回30分以内、再reviewは変更箇所とfinding解消を15分以内に限る。full gateを要求しない。
focused race、関連vet、format、diff checkとLinux配布物の実経路確認を行う。
結果は`docs/results/i07-session-management.md`へ記録する。
