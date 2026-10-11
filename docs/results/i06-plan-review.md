# I-06計画：通常レビュー・批判的レビュー

2026-10-11。利用者の「反映して通常レビューと批判的レビューさせて」に基づく。
対象は[I-06計画](../plans/i06-conversation-tui.md)の保存粒度、限定読取、状態寿命、実装順序の追加変更。
基準となるrepository HEADは`4c9bb5b`。旧Henjiのincrement記録は参考証拠であり、新製品要件の正本にはしない。

## 結果

通常レビューと批判的レビューはともにBlocker／P1／P2のfindingなし。
契約詳細化へ進める計画と判断した。製品実装は契約確定後に進める。
通常reviewは機能correctnessと直接consumerの整合、批判的reviewは重複の別層への移動、
Goの同期・所有・寿命、同一Session継続の前提を重点に確認した。

両reviewerはread-only、初回上限30分・新証拠や中間結論が10分間なければ中断の範囲で作業した。
製品編集、provider呼出、ユーザーDB・稼働Core/TUI操作、full gateは行っていない。

## 確認した経路

- journalの累積途中本文追記と最新state、完了stepのstate終了、cancel／failureの未完了本文保存。
- historyの終了保存・会話採用、DDLとpayload、active stateと確定記録の意味的位置。
- Agent・子Agentのjoin、stream CLI、HTTP／client／TUIの直接consumer。
- Core run管理・projection・購読・terminal復元、同ID再送、保存error時の結果保持。
- 全Inspect再生の置換、逐次復元と固定cut、会話viewの差分更新、serialization共有と解放条件。
- S-06の実測と、旧134・194〜199・210・214・218・219の関連節。

通常レビューは完了result→次step取消、保存失敗・execution未生成、同revision共有・購読寿命を確認した。
批判的レビューは、current sourceが計画に挙げた二重保存・全Inspect再生・重いrun結果保持に対応すること、
保存順とprogress到着順を同一にしていないこと、小さいrun索引の増加と本文保持を区別していることを確認した。

## 任意補足の採否と反映

coordinating ownerは以下を採用し、計画へ文書補足した。必須findingの修正ではない。

| 提案元 | 具体的な補足 | 計画への反映 |
| --- | --- | --- |
| 通常 | 完了結果をGET以外にも返す経路がある。 | GET run、同ID POST、terminal済みcancel、SSEを結果consumerへ列挙。 |
| 批判的 | metadata取得の軽量化を画面の必要情報削減へ広げない。現行execution一覧はtask本文を表示する。 | 内部照会と人間向け一覧を分け、taskを含む必要な内容を保持すると明記。 |
| 批判的 | 固定cutのreaderと現行SQLite 1connectionの占有・再照会を具体化する。 | reader保持区間とnormalizer処理範囲を詳細化し、同じStoreの接続待ちを追う条件を追記。 |
| 批判的の確認からowner補足 | 終了保存後の採用失敗では、DB terminalと最終RunResultが異なり得る。 | 採用errorを反映したCore結果を保持し、すべての結果consumerで同じ最終結果を返すと明記。 |

この補足後にownerが文書の整合と`git diff --check`を確認した。独立した再レビューは行っていない。

## 未確認と次の段階

固定cutの同期方法、entity ID／position、state開始位置、完了結果の限定読取、request fingerprintは
計画が指定する次の契約詳細化で確定する。変更後の性能改善量や製品TUI動作は未確認。
S-06の累積割当は重複処理の証拠であり、長時間のメモリリークの証明にはしない。
Ghostty→SSH→tmux全経路の人間の操作確認と、描画スパイクの幅縮小時重複の解消も残る。

## 後続の契約確定

同日の利用者指示で[詳細契約](../design/i06-conversation-contract.md)を作成した。
上記の未確定項目は同文書で確定した。これはレビュー後の設計作業であり、今回の二人のreviewerが
詳細契約も確認したという記録ではない。変更後の性能と実機TUI動作は引き続き未確認である。
