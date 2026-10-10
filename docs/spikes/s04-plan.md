# S-04：会話採用・使用構成の追跡・Session閲覧と再開

## 目的と実経路

根拠はREADMEと引継ぎ上位計画のS-04。利用者は保存Sessionを閲覧して再開し、
正常完了した会話と、採用されなかった実行の証拠を区別したい。
再開には現在の設定を使い、過去に使った設定・tool・hook・依存も確認したい。
全履歴は保存し、閲覧・再開・モデル入力には指定範囲だけを取り出す。

現行の `jsruntime → 外部tool／hook → host → trace → history` を共有する。
S-03のYAML／semver・HTTP・file処理を実行するSession driverを追加し、実行履歴を複製しない。
今回のSession／turn／会話採用は実験契約であり、製品のschema・UXを確定しない。

## 所有者と採る方式

- `configsnapshot` は現在の設定、tool／hook・vendor・hook設定のfile tree、実行binaryのhashとGo依存情報を捕捉する。
  保存済みsource bytesを別の作業ディレクトリへ展開し、Workerが実際にその構成を使う。
- `history` は構成snapshot、Session、turn、採用markerと既存eventをSQLiteへ保存する。
- `session` は現在の構成の準備、turn開始、共有runtimeの実行、終了結果保存、正常完了後の明示的採用を担当する。
- `sessionexperiment` は実HTTP、成功・失敗・キャンセル、別プロセスでの再開・閲覧、採用COMMIT前の停止、
  履歴を増やした後の範囲指定読込みと計測を担当する。

実行結果と回答draftを保存してから、別transactionで採用markerを保存する。
markerが参照するturnのrequest／responseの組だけを採用済み会話とする。閲覧や再Openでは自動採用しない。
失敗・キャンセル・成功したが未採用のturnは実行証拠として閲覧できる。
会話のコピーを別の正本へ二重保存しない。

モデル名・instruction等は実験用の設定として保存する。API credentialは使わない。
モデルAPIは今回呼ばず、モデル入力候補のmessagesを固定の生成JS役コードへ渡す。
実LLMによる会話品質やAgent loopの受入とは区別する。

## 確認する動作

1. 正常実行が終了した段階と、会話への採用後を別接続から読む。内部ツール成功と集約結果を保持し、
   採用前は会話に含めず、採用後にrequest／responseを同じturnとして復元する。
2. 副作用後のJS失敗と、実HTTP待ちのキャンセルを保存する。会話へは採用せず、Session内の実行として閲覧する。
3. 起動したWorkerプロセスの終了後、設定・tool・hook・hook設定を編集して別プロセスで同じSessionを再開する。
   過去会話を使い、現在の構成で新しい実処理を行い、古い構成とsource bytesをDBから読み戻す。
4. 実行・回答draftが保存された後、採用markerをINSERT済み／COMMIT前に所有するwriterをSIGKILLする。
   終了をWaitで確認し、成功の証拠が残る一方、会話へは未採用であることを独立readerから確認する。
5. 同じSessionで実処理を繰り返して履歴を増やし、最新／過去の会話window、turn一覧のpage、
   指定runのevent pageをSQLで取り出す。全体を読み込んでから切り取らず、全履歴が保持されることを確認する。
   SQL query plan、読み戻したrow数・bytes、入力候補の大きさと読込み時のallocationを記録する。

会話window・pageの件数は呼出し側が指定する実験条件であり、製品の最大件数や固定制限ではない。
SQLの範囲読込みで成立を確認し、長期運転の絶対メモリ上限や性能目標を推測で設けない。

## 成果物と残す範囲

再現用CLI、Session／構成／採用のモジュール、SQLite、別プロセスの読戻し、実HTTP・fileの観測、
構成archive、履歴window・pageの結果、成立範囲を保存する。
共有runtimeの修正は計画しない。S-03で見つけたmodule初期化エラー表示の不具合も今回の修正対象に含めない。
旧Henji DB移行、credential保存、実LLM、TUI、同時編集、全異常終了回復、他OSは今回扱わない。
