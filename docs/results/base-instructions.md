# ユーザー共通の基本指示の差替え

2026-10-14。利用者指定の機能比較25を実装した。
[契約・確認範囲](../design/base-instructions.md)に従う。

`~/.config/hjh/instructions/base.md`を読み、あれば組込み基本指示を本文全体で置換する。
XDG_CONFIG_HOME指定時はその配下を使う。初期ファイルは現在の短い基本指示相当の内容で配置し、
既存ファイルは上書きしない。組込みの役割名入り共通文と途中報告指示が差替え対象であり、
role指示・configの追加指示・tool利用指示・role catalog・実行情報・AGENTS/skills・hookは別に組み合わせる。

extensionsが読取りと選択を所有し、親・子のcaptureとpreviewが同じselectorを使う。
各開始時に再読し、実行内では本文・path/digestを固定する。Session再開でも現行fileを読む。
snapshotへbase metadataと本文componentを保存し、外部base.mdの原文・出典を
既存resource_revisionへ保存する。AGENTSの保存と同じ指示の記録で、実装ソース一式の自動archiveは復活させない。
新DB table・schema変更・旧履歴の変換・watcher・新しいCLI登録操作は追加していない。

## 確認

- HOME既定、XDG指定、未配置時の組込み指示、配置時の置換とrole/tool/config指示の維持を確認した。
- appの親→直接/JS tool→再開→子経路で、親の途中編集が現在の実行に反映されず、
  次turnと子開始では新本文へ切り替わること、AGENTS/skillsを引き続き使うことを確認した。
- HTTPでSession未作成preview、実行actual、file編集後の新preview、再attachしたactualの固定を確認した。
- 保存構成のbase metadataと実入力、外部base原文を照合した。
- TUIのヘッダー・/contextで外部pathを表示し、built-inと誤表示しないことを確認した。
- extensions tests、関連app/HTTP/TUI focused tests、変更packageのvet、diff checkが成功した。
- 両binaryを再buildし、実CLI→explorer子→最終回答→一時Coreへ接続して同Session再開を
  ローカルHTTPで確認した。親はLIVE_BASE_ONE、編集後の子・次turnはLIVE_BASE_TWOを使い、
  3実行の原文と出典をDBで照合した。実PTYでもheaderと/contextのpath表示を確認した。

実確認の一時Core・workspace・DBは確認後に片付けた。外部有料APIと常用Coreの再起動は行っていない。
初回のコード適用にはCore再起動が必要。その後のbase.md編集は再起動せず次の実行開始から反映する。
