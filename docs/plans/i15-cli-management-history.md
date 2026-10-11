# I-15: Core管理・版情報・履歴CLI

利用者は旧HenjiとのCLI比較後、core list/status/stop、--version、historyの整備を指定した。
旧CLIを参考に、現在のCore HTTP APIと保存履歴へ接続する。旧DBの移行、Session削除、
agent/tool管理CLI、診断管理CLIは今回含めない。

## 操作と責務

- `hjh core list`は同じVM・ユーザーのCore登録を列挙する。ID、URL、workspace、PID、
  起動時刻、到達状態を表示する。明示`--workspace`で絞れる。`--json`も提供する。
- `hjh core status --core ID_OR_PREFIX`または`--connect URL`は公開APIから現在状態を読む。
  Session数と準備中を含む実行中runも表示する。ID選択は一意なprefixを解決する。
- `hjh core stop`は同じ選択で既存shutdown APIを呼び、取消・終了保存後にCoreが停止するまで待つ。
  status/stopの対象省略時は一覧を表示し、stopには対象指定方法を示す。
  問合せによってCoreを起動しない。
- Core登録はserveの寿命に属し、`$XDG_STATE_HOME/hjh/cores`または
  `~/.local/state/hjh/cores`に保存する。Core終了時に自身の登録を外す。
  異常終了の登録は到達不能として表示し、既存dataの自動削除は追加しない。
  稼働中の旧バイナリへ登録を後付けせず、URL指定は登録の有無によらず使用できる。
- `hjh --version`と`hjh-tui --version`は実バイナリの版、build hash、source revision/dirty、
  Go/target、拡張契約を表示する。DB・設定・認証・Coreを開かず終了する。
- `hjh history [--session ID_OR_PREFIX|--latest] [--view session|canonical|detail]`
  を整備する。省略時は最新Session、既定viewはsession。Sessionは更新日時降順、
  同時刻ではID昇順で選ぶ（旧sourceで確認した順序）。
  ローカルはread-only DB、`--connect URL`は既存公開APIで読む。
  sessionは失敗・取消・子を含む会話、canonicalは採用会話のMarkdown、detailは保存実行証拠の
  NDJSON。detailは現在のInspectが公開するSession/execution metadata、保存message全文、
  全recordのpayload/content/relations、captured configuration/contextと、現在の公開会話stateを
  含む。旧のNDJSON schema互換や独立immutable-content exportは要求せず、現在の保存契約で
  `kind/identity/value`のrecordを出す。recordのcontentはInspectと同じbase64で保存bytesを保持する。
  既存turn範囲と明示`--json`の採用会話出力を提供する。
  最新選択は実効workspaceに絞り、明示Sessionは指定DB/Core上のIDから解決する。
  履歴未作成・最新Sessionなしは「no history」で成功し、DBや設定を作成しない。

CLI入口は早期dispatchへ分ける。GET `/api/v2/core/status`は既存Managerのmetadataから
phaseと実行中runのID/Session/実行phaseを返し、会話本文を読まない。
Coreの実行・停止は既存Manager/API、履歴の意味と保存は
既存history/conversationが所有する。登録一覧、版情報、履歴renderingは各責務へ分け、
TUIやCLIに実行状態・予算管理を複製しない。

## 確認

各変更のfocused testとformat/vet/diff確認を行う。private port・workspace・stateで
実serveを複数起動し、一覧、短縮ID選択、状態、停止待ち、登録解除を確認する。
版情報は配布バイナリでも確認し、新しいworkspaceに状態を作らないことを確認する。
既存のprivate実行履歴で3view、最新/短縮ID、Core経由とローカルの対応を確認する。
追加認証・有料モデル実行・利用者の稼働Coreの停止は行わない。
