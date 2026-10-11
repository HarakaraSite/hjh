# I-09：実行中の追加入力と次の依頼の予約

2026-10-11。I-08の接続選択とSession継続を利用者が確認し、次段階へ進むよう指示した。
[I-06の優先順位](i06-conversation-tui.md)に従い、F2 follow-up、F3 steering、
受付と適用状態、実行中draft保持を次の範囲とする。
利用者の進行指示に基づき[受付・適用・予約契約](../design/i09-runtime-input-contract.md)を具体化した。
利用者の実装・テスト・review指示に基づき製品経路へ接続した。
確認結果と残件は[実装結果](../results/i09-runtime-input.md)に記録する。

## 利用者の動作

- 実行中にdraftを入力し、F2で「現在の依頼が正常完了した後、同じSessionで実行する依頼」を予約する。
  終了保存と会話採用が完了してから、Coreが次のrunを開始する。TUIをdetachしても予約はCoreに残る。
- F3で「現在の依頼への追加指示」を送る。実行中のHTTP生成やtoolを途中で壊さず、
  次に親Agentがモデル入力を組み立てる境界でuser messageとして取り込む。
  最終回答を生成中に受け付けた指示も、回答の保存後に取り込んで同じrunを続ける。
- 受付済み、適用済み、予約済み、開始済み、未実行となった理由を区別して読める。
  受付されたことを、モデルが既に指示を読んだこととして表示しない。
- 本文のEnterは、idle時は従来どおり送信、実行中はdraftを保持する。F1は現在runに属する予約列のcancel、F4はSession選択。
  追加入力の受付が確認できたら送信したdraftを消し、その間に編集した新しいdraftは保持する。
- Session切替後も受付済み入力の宛先は変わらない。親runへのsteeringを子Agentやrun_js内の
  モデル呼出しへ自動転送しない。受付済み・適用済みの内容と予約の状態は再接続後も確認できる。

## 根拠と現行の利用経路

旧Henjiの参照元を実際の呼出し順で確認した。

- `v0/tui/remote_session.ts`：F2/F3→submitDraft→followUpQueue/steeringSubmit。
  送信対象Session・execution・command ID・draft revisionを捕捉し、受付確認と編集中draftを分ける。
- `v0/agent/host/core_service.ts`：command受付、宛先の解決、task serviceへの伝達、receipt参照。
- `v0/agent/host/task_service.ts`：受付中steering、予約follow-up、正常完了後の開始、cancel/失敗/停止時の状態保存。
- `v0/agent/core/loop.ts`：tool群の完了後と最終回答保存後にsteeringを取り込む。
  生成をcancelして指示を割り込ませる方式ではない。

旧Henjiはsteeringをexecutionごとに1件、follow-upを1枠に制限している。
この数値は参考実装の制約であり、hjhの明示要件として扱わない。
今回の案では複数入力を受付順に処理し、本文やqueue長の固定上限を推測で追加しない。

I-09着手前のhjhは、TUI `submitDraft/startRun`→apiclient→HTTP `/runs`→corerun.Manager→
app.Service/Run→agent.Run→provider/tool→journal/history→会話view→SSE→TUIの経路を持つ。
実行中のdraftは保持するが、追加指示の受付、loopへの伝達、次run予約の操作はまだない。
ManagerがSessionの実行予約とrun寿命を所有し、appが終了保存と会話採用を完了して戻る。
`contracts.Message.Steering`は既にあるが、追加入力の実経路には接続されていない。

`_refs/san/internal/core/run.go`のAppendは入力queueとwake通知を分け、step境界で取り込む。
`_refs/crush/internal/agent/agent.go`は受付・queue・cancelの順序を所有し、
開始されなかった予約にも終了通知を与える。これらを参考に、Goの短いlockと所有者で構成する。
常駐Agent、参考実装のchannel容量やcancel規則をそのまま移植しない。

## 操作契約の案

| 項目 | 提案する動作 |
| --- | --- |
| steeringの宛先 | 送信時のcore ID・Session ID・run IDを固定する。後の画面選択を参照しない。 |
| 適用境界 | 最初のrequest準備前、tool群の結果保存後、最終回答保存後。実行中requestへ後付けできたとは扱わない。 |
| 終了との競合 | loopの終了確定と受付を同じ入力ownerで順序付ける。受け付けた指示は取り込むか、未適用理由を記録する。 |
| follow-upの順序 | 同じSession内で受付順に実行し、先行runの保存・会話採用を次run開始より先にする。 |
| 予約の接続・role | F2受付時に次runの実効selectionとroleを既存Core resolverで解決して保持する。後からの選択変更は、後で送信する入力に適用する。 |
| 拡張source | 予約時にtool/hook全文やVMを捕捉しない。従来どおり、そのrunの開始時に現行sourceを捕捉する。 |
| F1・run失敗 | そのrunに続く未開始予約を実行せず、理由を保存する。F1は引継ぎ済みの同じ列のactive runも止める。予約本文や既存履歴は削除しない。 |
| detach | 通信だけを閉じる。受付済み指示・予約と実行はCoreで継続する。 |
| Core停止 | 新しい開始を止め、現在runをcancel/joinし、未開始予約の状態と終了保存を完了する。 |
| Core再起動 | 過去の受付・適用・予約結果を読めるようにする。旧coreで未開始だった予約の自動再実行は別の機能として扱う。 |

利用者の進行指示後、F1/失敗時の未開始予約は理由を残して実行しないこと、
F2受付時のselection固定を契約へ具体化した。入力受付とrun終了が競合する境界も契約に記載する。
旧実装の存在だけを根拠に、固定1件の制限を採用していない。

HTTPは既存のJSON＋SSEを使う。入口はPOST `/api/v2/runs/{runId}/inputs`、
requestはcoreId・sessionId・inputId・kind（steering/follow-up）・textと、follow-upの次run指定。
receipt参照はGET `/api/v2/runs/{runId}/inputs/{inputId}`、Session内の未処理入力は
GET `/api/v2/sessions/{sessionId}/inputs`で得る。DTO、保存payload、SSEの状態更新は契約文書に定める。
既受付receiptの状態変更はafterRevisionで取得し、初回は過去Coreのreceiptも読める。
F1とSession切替は明示のinput-chain/cancelを使い、列の保存・join完了を確認する。
inputIdは通信応答を失った場合に同じ入力の状態を確認するためのIDとし、同じIDの再送で二重に適用しない。
別クライアントもTUIの内部状態に依存せず同じ操作を使えるようにする。

## 状態の所有者と保存

1. **Core入力管理**：runに属するsteeringの受付順・適用状態と、Sessionのfollow-up予約を所有する。
   Managerの既存Session予約を使い、別の実行registryを作らない。受付・終了・次run予約の移行だけを
   短い同期区間で決め、SQLite、provider、VM、会話構築、HTTP/SSE書込みをそのlock内で待たない。
2. **Agent loop**：型付き操作で未適用入力を取り込み、既存appendMessage経路で保存してからrequestへ渡す。
   receipt管理やSQLをloopへ持たせない。モデル応答とtool結果の既存順序を崩さず、
   accepted指示が最終回答直後に取り残される経路を解消する。
3. **app/history**：schema v3の既存command_receipts、execution_control_facts、follow_up_receiptsと
   contents/messages/semantic_recordsの役割を対応付ける。DDLは維持する。
   受付receipt、適用user message、次runのtaskを区別し、同じ本文を状態更新ごとに全量再保存しない。
   既存journal/executiondataからlive会話と保存済み会話へ同じID・位置を伝える。
4. **TUI/client**：draft revision、送信中操作、宛先、receipt表示、run購読を所有する。
   次runはCoreが開始し、TUIはそのIDへattachする。元runのterminal後に予約状態を取得し、
   detach/resume中に次runが始まっても追い付ける経路を契約に含める。
   TUIから予約taskを再POSTして二重実行しない。

受付通知は保存完了と分けて設計し、APIが何を完了した時点でacceptedを返すかを明記する。
受付操作のresponseを描画やモデルstep完了まで保留しない。
会話本文を入力registry、run terminal、receipt一覧へ全量重複保持せず、保存済みlocatorを使う。
処理済み本文と終了した重い状態は所有者から解放する。入力情報を永続化することを、
receipt全件や全会話をCoreのメモリへ常時載せる理由にしない。

## 進め方と確認

1. 受付→適用→終了、正常完了→予約run開始、cancel/停止、再接続の操作契約を文書化する。
   初回受付の保存境界、次runへのSession予約引継ぎ、draft確認、receiptと会話entityの対応を確定する。
   計画と契約を旧Henjiの実経路と対比して確認する。
2. Core入力管理、history操作、HTTP/clientを接続し、F2の保存後の自動開始を成立させる。
3. loopのsteering適用を接続し、最終回答直後の指示も同じrunで完了させる。
4. TUIのF2/F3、受付・適用・予約の表示、draft保持、次runへのattach、再接続を接続する。
5. 変更経路のfocused testと必要なrace/vet/format/diff確認を行う。
   人間の操作に対応する確認は、生成中とtool実行中のsteering、最終回答直前の受付、
   正常保存後のfollow-up開始、予約後の選択変更、F1/停止、detach/resume、draftの編集中更新とする。
   保存事実とモデルへ送った実requestを照合し、受付表示だけを成功の代わりにしない。
6. private Core/TUI・既存認証の実providerでF2/F3を操作し、入力内容が次requestに含まれること、
   follow-upが同じSessionの次turnへ保存されること、配布バイナリからの再開を確認する。
   利用者のCore/TUI/DB/tmux設定は変更しない。

通常reviewは要件・loop適用・保存・API/client・再開のcorrectnessを確認する。
批判的reviewは入力受付と最終回答/cancel/次run開始の具体的な競合、二重処理、
保存・描画の待ちが制御へ戻る経路を確認する。独立reviewを依頼する場合は旧Henjiの実コードとの対比を指定し、
初回30分、変更箇所とfindingの再確認一回15分を上限にする。full gateや仮想的な状態matrixは要求しない。

receipt/state payload、適用messageとreceiptの同一transaction、RunState.nextRunIdによる引継ぎ、
Core停止時の予約保存順序は契約文書に具体化した。
実装時には既存history helperのtransaction内再利用と、loopへの型付きinterfaceを接続する。
実装結果・実測・残件は`docs/results/i09-runtime-input.md`へ記録する。

お気に入り、認証画面、Core自動起動、context/recall、tool詳細の再設計、色・Glamourは後続とする。

## 契約reviewの結果

旧Henjiの実コードと対比した通常・批判的reviewを行い、通常4件と批判的1件を採用して契約へ反映した。
本文保存の共用、receiptの変更revision、受付可否の公開、過去Coreの結果発見、
元run terminal配信待ちのF1による予約列停止を修正し、限定再reviewで必須findingが残らないことを確認した。
結果は[設計review記録](../results/i09-plan-contract-review.md)に記載する。
この結果は設計段階の判断であり、製品実装・実provider動作・配布物の完了を意味しない。
