# 人が使うSession ID

2026-10-12。利用者は「TUI・CLIで人間が確認・指定するSession IDは短縮IDで統一」と指定した。

人向けの表示は先頭8文字を使う。一覧で同じprefixを持つSessionがある場合は、
区別できるまで延長する。表示したIDはrun／historyの`--session`、TUIの`--session`と
`/resume`へ渡せる。完全IDの指定も受け付け、case-insensitiveな一意prefixを解決する。
曖昧な指定では別Sessionへ勝手に接続せず、区別できる短縮候補を案内する。

CLIのrun進捗、historyの見出しとcanonical Markdown、sessions一覧、Core statusのSession表示、
TUIのheader・一覧・query見出し・context・detach案内が対象。
`hjh sessions`は人向け表示を既定とし、従来の完全IDを含む構造化一覧は`--json`で読む。
API、内部state、DB、JSON、detail NDJSONと保存本文は完全IDを保持する。
model／保存本文に含まれた任意の文字列を短縮IDへ書き換えない。

短縮処理・prefix解決は`internal/sessionid`が共通に提供する。
CLIのローカル入力とapiclientの人向け入力で完全IDへ解決してから、既存のCore／app経路を使う。
TUIの`/resume`も解決後に現在Sessionとの比較と切替を行う。
同じSession自身を短縮指定した場合は、実行中のrunを取消しない。
Sessionの作成時期、保存schema、Coreの状態所有、Core／Run／Execution IDの表示仕様は変更しない。

headerの枠・項目・出力回数は[header契約](tui-header.md)に定める。
利用者指定によりagentとruntimeをheaderから除外する。
