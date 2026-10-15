# agentのDB非保存mode：将来検討

2026-10-15、利用者がJSコンパクションの要約モデル呼出しに関連して希望した。
agent全体がDBへ保存しないmodeを今後検討する。現時点では未実装。

対象候補は、一時的な調査・要約・変換等、実行履歴やSession再開が不要なagent。
通常保存modeと非保存modeの選び方、親子の表示・status/collect・取消・失敗確認、
Coreのメモリ寿命、Session継続と参照IDの扱いは、この機能の着手時に決める。

現在のJSコンパクションでは、親selectionを使う一回のmodel要求をagent executionと分け、
要約専用の送受信全文をDBへ追加保存しない範囲だけを扱う。
agent全体の非保存modeの実装認可や保存経路の一括変更とはしない。
