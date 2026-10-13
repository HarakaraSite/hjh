# ユーザー共通の基本指示の差替え

2026-10-14。機能比較25について、利用者が実装を指定した。
ユーザー共通の`~/.config/hjh/instructions/base.md`を編集し、組込み基本指示全体を差し替える。
`XDG_CONFIG_HOME`を指定したLinuxでは`$XDG_CONFIG_HOME/hjh/instructions/base.md`を使う。
パスの解決は認証・共通AGENTSと同じ`os.UserConfigDir()`に従う。

## 利用動作

- ファイルがあれば、その本文が組込み基本指示を置き換える。追加ではなく置換である。
- ファイルがなければ、従来の組込み基本指示を使う。空ファイルは基本指示なしの指定として扱う。
- role指示・configのsystemInstruction・有効toolの利用指示・role catalog・実行情報・
  AGENTS.md・skill一覧・hook文脈は従来通り組み合わせる。
- CLI直接run、Core接続run、TUI、子agentが同じ共通実行経路を使う。
- 各親ターン・再開・子開始時に再読し、開始した実行内では固定する。編集中のファイルを
  実行途中のモデルstepへ読み直さない。Session再開は現行ファイルを使う。
- ヘッダーと/contextはpreviewまたはactualの基本指示出典を示す。actualは実行が使った
  path・digestを保持する。本文はヘッダーへ出さない。
- 実行履歴には組立済み指示と基本指示の出典を残し、外部base.mdの原文も保存する。
  ツール実装ソースの自動archiveは復活させない。

今回workspace側のbase差替え、CLIによるinstruction登録管理、モデル別のbase選択、
テンプレート変数展開は追加しない。ファイルは通常のMarkdownテキストである。
読むべきファイルの読取りエラーは実行準備・previewへ返す。

## 現行経路と変更範囲

現在、extensions.CaptureのassembleInstructionが組込み共通文・role・tool・configを組み立て、
appのprepareWorkspaceContextがAGENTS/skills、runtimeとhookが実行情報・開始文脈を追加する。
親・子ともこのcaptureと実行保存を使う。previewはResolveInstructionProfileを使い、本文は送らない。

extensionsに基本指示の選択・読取りを置き、captureとpreviewが同じ選択処理を使う。
capture済み本文・metadataをSnapshotが所有し、appはその原文を既存のresource_revisionへ保存する。
TUIはmetadataを表示するだけで、ユーザーファイルを独自に読まない。
新しいDB table、設定管理owner、ファイル監視は追加しない。

## 確認する動作

通常のHOME既定とXDG指定でのファイル選択、未配置時の組込み指示、配置時の置換と他の指示の維持を確認する。
実モデル入力へ送る指示、preview/actual、保存構成・原文、ターン内固定、編集後の再開・子開始の再読を照合する。
TUIのヘッダーと/contextで出典を確認し、実binary・一時Core/DB・ローカルHTTPで通常経路を通す。
有料外部APIは今回呼ばない。
