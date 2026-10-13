# 標準toolの定義・利用指示改善

2026-10-13。利用者が採用した定義見直しに従い、read_file、edit_file、command、
run_agent、spawn_agent、agent_statusのdescription、引数description、promptGuidelinesを更新した。

現行経路は外部config→選択role/toolsのcapture→有効toolの共通instructionとprovider tool定義。
実装の戻り値・省略値はhost、外部index.js、子のdelegation/resolver/status経路で確認した。

- read_file：`{path,text}`の全文結果と、run_jsが利用可能な場合の必要部分の抽出。
- edit_file：literalの全一致、空newTextで削除、0件も通常結果なのでreplacementsを確認する。
- command：program/argsで直接実行、shell構文はshellを明示、args/stdinの省略、exitCode/stderrの確認。
- run_agent / spawn_agent：完了待ちと起動のみの違い、独立会話、role省略時のconfig.defaultRole、
  親selection→子role→明示modelの順、provider変更時の既定、tools省略と空配列の区別。
- spawn_agent / agent_status：spawnのexecutionIdを使い、初回after省略、同じ子のnextCursorで差分を読む。
  回答に必要な結果は親終了前にcollectし、statusを完了待ちのpollに使わない。

配布configとrepositoryの`.hjh/extensions/config.json`に対象六つの宣言を反映した。
入力schemaのdescriptionを除いた比較で、型・required・公開propertyの構造は従来と同一。
runtime実装・既定値・保存形式を変更していない。

既存のextension capture/guideline保存・選択test、appの親子指示選択、model/tools上書き、
失敗した子の結果、報告差分、非同期子、role、context予算のfocused testを実施して通過した。
commandの非0終了結果を扱う既存testとdiff checkも通過した。
「指示なし」のextension testは、read_fileに指示が加わったため専用fixtureへ置き換え、
標準toolから指示を削ってtestを通すことはしていない。
低影響の宣言変更なので新しい文言照合testや全体gateは追加していない。
課金providerでの追加比較実行は今回行っていない。

Core/CLI・TUIのbinaryも配布既定値を含めて再ビルドした。
repositoryでは外部configを次Workerでcaptureするため、次の依頼から反映する。
契約説明は[extensions](../design/extensions.md#i-21tool自身の利用指示)を参照する。
