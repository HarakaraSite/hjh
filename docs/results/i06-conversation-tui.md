# I-06：会話画面とCore会話経路

> **後続変更の案内（2026-10-16）**：以下はI-06時点からの実装・確認記録。Glamour／色の対象外記述は[後続描画実装](tui-rendering.md)で解消し、保存方式は[現行通常保存](../design/normal-history-storage.md)へ更新済み。当時の実行条件・測定値を現在の未実装状態や性能保証と扱わない。

2026-10-11。実装・テスト・通常／批判的review後、利用者の実端末確認で本文の分断を確認し、TUI描画を再修正した。正本は[計画](../plans/i06-conversation-tui.md)と
[保存・会話・同期・結果取得契約](../design/i06-conversation-contract.md)。

## tool二重表示と公開thinking／summaryの追加修正

利用者が会話の表示順を確認した後、tool二重表示、thinkingとthinking summaryの修正を指示した。
一回の実tool呼出しをAgentのmodel callとruntimeの代理callが別IDで保存していたため、
Agentとruntimeはmodel call IDを共用し、開始・結果の保存をAgentが所有するようにした。
runtimeの代理開始・結果は保存しない。JS内の実tool、hook、host、基礎委譲は別IDと親関係を維持する。
同名・同引数の複数実呼出しを一括dedupしない。既存記録の書換え・削除は行っていない。

Responsesの公開`reasoning_text`のdelta／done／完了item.contentを取り込み、公開summaryと
別item key／`thinkingKind`へ対応付けた。最新state、確定message、未完了への移送、履歴replay、
HTTP entityまで種別と完全なkeyを維持する。TUIのラベルは`thinking`／`thinking summary`。
旧HenjiのResponses request同様に`reasoning.summary: auto`を要求し、指定effortも維持する。
encrypted／opaque再送stateから表示本文を生成しない。

private Core→HTTP/SSE→94列×23行のprivate tmux TUIで実OpenRouter Responsesを確認した。
DeepSeek V4.1 FlashのSession `46855eae-52e0-4486-9b0f-90a2a9e4d801`はREADMEを直接一回読み、
tool entity／通常行が一件だった。公開thinking本文183／231文字はterminal前から同じIDで増加し、
それぞれの完成本文と296文字の最終回答は一度だけ表示された。
CoreとTUIを起動し直したcold再開でもentity ID・kind・本文・順序が一致し、toolは一行だった。

OpenAI API `gpt-6-luna`（auto／high）と既存認証のChatGPT `gpt-6.1-sol`（high）も
README読取→最終回答を実行でき、toolは一行、最終回答は各一度だった。`summary:auto`要求を確認したが、
今回の応答は公開summary／本文を返さなかった。highのreasoning itemはencryptedのみであり、
thinkingを表示しない正常経路として扱った。実providerから公開summaryが返り、PTYに表示される
経路は今回未観測である。summaryの受信・保存・replay・labelは公式契約とfocused testで確認した。
実providerが必ずsummaryを返すという検証scriptの仮定は実応答と矛盾したため撤去した。

旧Henjiとの通常・批判的観点の独立reviewに必須findingはなかった。
旧loop→toolsのcall ID共用、readable_thinkingの公開text／summary、Responses要求・streamと照合した。
旧Henjiのtext優先／summary fallbackに対し、今回は利用者指示に従い両種があれば別entityにする。
変更経路のfocused race（provider/history/conversation/jsruntime/app/httpapi/TUI）、関連vet、
format、diff checkが成功した。full gateは再実行していない。
実行証拠はgit管理外の`dist/i06-live/tools-thinking/`。検証用DB／Core／tmuxを分離し、
利用者のDB・config・実行中Core/TUI・tmux設定は変更していない。

最新Linux候補は`dist/i06-tools-thinking-packages/build-ddopl3ti/hjh-linux-amd64.tar.gz`。
SHA-256は`6cd0c6641597ef38b1f017fac04c935733785b041eae4184acc14f86d18d48f7`。
Core 22,783,715 bytes、TUI 14,048,539 bytes、CGOなしstatic ELF、license同梱。
配布物の全manifest file hashを確認し、配布Core/TUIのprivate tmux再開でも同一entity・tool一行・本文一回を確認した。
`dist/hjh`と`dist/hjh-tui`を更新した。稼働中processは継続し、利用者の両方の再起動後、新しいrunへ反映される。

## 旧Henjiの表示を先に成立させる追加修正

利用者の「旧Henjiのコードを念入りに読む」「Glamourと色は後回し」「表示を成立させる」という指示により、
現行HTTP TUIの受信から会話projection、本文layout、scrollback出力、入力位置までを照合した。
原典は`henji-harness/v0/tui/remote_session.ts`、`snapshot_presentation.ts`、`keyed_conversation_store.ts`、
`conversation_flow.ts`、`layout.ts`、`input_layout.ts`、`tui_renderer.ts`、`scrollback_writer.ts`、`body_document.ts`。

- 本文はMarkdownの原記号・空行を維持してcell幅で折り返す。Glamourの再加工を外し、空assistantを表示しない。
- 通常toolは名前・短い引数preview・状態だけを出す。各callの結果全文は保存を維持し、会話fact/viewへ複製しない。
  `/tools`でcall番号を確認し、`/tool NUMBER_OR_ENTITY_ID`で保存locatorの一つのrecordを取得する。
  このAPIはexecution全体のmessages/context/configurationを読み込まない。
- 旧Henjiのappend-only出力同様、最初のtool状態を一度だけscrollbackへ出す。結果で再追記せず、開き直すと保存済み✓/✗を出す。
  tool結果待ちを後続本文の確定出力のbarrierにしない。異なるnested callを同じ本文という理由で削除・結合しない。
- Bubbles textareaのvirtual cursorを無効化し、実端末cursorをeditorの文字幅・行・viewportと同じ位置へ動かす。
  入力欄を会話の下、その下に短いcontrolsと状態・接続footerを置く。色の追加調整は行っていない。

問題Session `1c1b47b4-4000-45b8-9008-2046c56ca5e2`のDBをread-onlyでprivate copyへbackupし、
新Coreと独立TUIを94列×23行のprivate tmuxから確認した。通常表示は93行、6件のtool callは各一度の短い行、
最終回答全文は一度だけ表示された。94→70→94列でも最終回答の欠落・重複がなく、`/tool`でREADME全文を読めた。
このviewのtool本文37,755文字は通常DTOから外れ、assistant/task本文とtool locatorを維持した。

実tmuxの日本語draft「日本語」はprompt込みでcursor x=8、複数行paste後の「日本語」もx=8だった。
修正前はvirtual caretだけが動き、実端末cursorはx=0に残っていた。GhosttyのIME変換窓自体の確認は利用者側に残る。

同じprivate Core/TUIから実OpenRouter ResponsesのDeepSeek V4.1 FlashでREADME読取→最終回答を確認した。
35行、2つの保存call entityを一度ずつ表示し、937文字の最終回答は保存本文と一致して一度だけ表示された。
利用者のDBと実行中Core/TUIは変更していない。公開thinkingはこのrunでも0件で、既知のreasoning_text adapter対応は今回変更していない。

focused race（TUI/renderer/conversation/app/httpapi/apiclient/corerun/hjh-tui）、関連vet、format、diff checkが成功した。
通常tool／明示detail／空assistant／日本語cursor／原文記号と空行／streamから確定への出力について確認した。
旧Henjiとの対比reviewを完了し、今回の修正に追加の必須findingはなかった。
比較は現行HTTP受信／projection→同entity更新・空assistant、本文layout／conversation flow→原文・receipt・mutable末尾、
tool activity／detail読取→preview・保存locator、editor layout→実cursor・入力下footer、
terminal writer→scrollback・resizeの経路で行った。狭幅tableの再整形、色、詳細browserの全面移植は今回の対象外。
実行証拠はgit管理外の`dist/i06-live/display-stability/`にある。
`/tool`は今回、idle時に保存済み結果を読める最小操作を前倒しした。生成中の明示詳細は本文末尾へ追加され、
本文が終わるまで表示を待つ。旧Henjiの実行中別browse表示は今回移植していない。

reviewで採用した既存差分として、直接`read_file`の外側model callとruntime代理呼出が別UUIDで保存され、
一度の実読取が同名・同previewの2行になる。`agent/loop.go`のmodel_tool開始→`jsruntime/worker.go`の
別UUID開始→`app/journal.go`の別tool localKey→TUIの各entity行という経路で、今回のreceipt変更以前から存在する。
この時点では「provider宣言と実行開始/結果を二重tool行にしない」という採用契約との差分だった。
冒頭の追加修正でcall ID共用と代理保存の撤去により解消した。同名toolや同じ結果本文による一律dedupは行わない。

この表示修正時のLinux候補は`dist/i06-display-packages/build-jjmia7bj/hjh-linux-amd64.tar.gz`。
SHA-256は`176f3fb8674c2e5243d765dcf1014972a2101e355d7c439612cf33410f23471c`。
CoreとTUIはCGOなしのstatic ELF、license同梱で、配布TUIのprivate PTYでも日本語caret x=8を確認した。
Core binaryは22,768,074 bytes、TUIは14,048,722 bytes。未使用Glamour依存を除いた。
`dist/hjh`と`dist/hjh-tui`をこの候補へ更新した。稼働中processは継続し、利用者が両方再起動すると反映される。

## 利用者の端末確認で判明した描画不具合と最初の修正

Core／TUIの双方を再起動した利用者の画面で、READMEの要約が多数の空白を挟んで分断された。
保存済みSession viewの本文は連続しており、Core会話viewにはthinkingが存在しなかった。
`read_file`のcall／resultはviewに存在した。前回のprivate tmux確認は文字の存在を主に判定し、
全文の連続性と欠落・重複を確認できていなかったため、TUIの利用可能性を示すには不十分だった。

保存済み会話を8文字ずつ再描画する再現で、全文を一度描画した場合と比べて余分な空白行、
表の区切り線の重複、リンク付き本文の欠落を確認した。Markdownが生成するURLや表の文字を、
元sourceの文字へ前方検索で対応付けたreceiptが本文位置を飛ばす。また、run終了時に追加された
execution outcomeを過去の会話位置へ挿入し、後続本文より先に確定出力していた。

修正では推定した文字位置での確定を撤去し、不変になったMarkdown blockのsource境界だけを使う。
最初の修正では、未確定本文を全文sourceで保持して可視live windowで更新し、先行tool結果が未着の間は後続出力を
確定しなかった。このtool待ち方式は後述の短いtool行による表示修正で置き換えた。executionの遅れて変わる状態はfooterへ表示する。利用者の指示に合わせ、headerは
通常scrollbackへ流し、更新する状態を下部へ置く。修正後の端末確認は以下の既存確認と区別して記録する。

今回問題が出たSessionを94列×23行のprivate tmuxで再現した。assistant本文を8文字ずつ
追加した出力と完成後の静的出力は、753行・うち空行128行の全文が一致した。
単一Markdown本文、空のtool開始→child→親回答、複数entityの逐次表示と完成表示の改行を
製品writerの確定行で比較するregressionを追加した。
追加reviewで長い未確定段落の先頭だけが表示され、新しい末尾が見えないP2を確認した。
live previewだけを末尾へ追従させ、永久確定の位置とtool barrierは維持した。
子の進捗・結果のlive表示と、tool結果未着中のscrollback確定停止も区別する。
実Coreの同じ保存済みSessionを修正版TUIで読取り、`tool / read_file`の呼出し名・結果と
最終回答を確認した。幅94→70→94、`/quit`後にも、端末reflowの空白を除く本文25,591文字が
同順序で一致した。稼働中の利用者Core／TUIとtmux設定は変更していない。
これは完了本文の保持の確認であり、生成途中の狭幅reflow一般の解決を意味しない。
記録は`dist/i06-live/user-render-diagnosis/`にある。

thinkingの追加診断では、providerStateのreasoning itemに`content[].type=reasoning_text`の
テキストが3応答分存在することを確認した（147／45／83文字）。summaryは空だった。
現行Responses処理はsummaryだけをThinkingItemへ渡すため、この表示形式を拾っていなかった。
これはTUIのreceipt不具合と別のprovider処理で、追加修正の範囲を利用者へ確認している。

## 実装した責務

- `internal/executiondata`：root／子と非対話run／Coreで共通の保存受付順。同期commit後だけ公開する。
- `internal/history`：小さい途中開始marker、latest state、完了時のatomic置換、固定cutと逐次会話読取、限定terminal結果読取。DDL v3を維持する。
- `internal/conversation`：stable entity IDと意味的位置、差分normalizer、短いinboxとconsumer側drain。保存callbackがsnapshot加工を待たない。
- `internal/corerun`：run・Session予約と軽いterminal索引。会話consumerにだけviewを生成し、最後のconsumer終了後に重いviewを解放する。
- HTTP／client：`/api/v2`のsnapshot／upsert。run seqと会話revisionを別に適用し、初期cutと最終snapshot／terminalを区別する。
- TUI：通常画面のscrollback、独立writer、source offsetの出力receipt。Coreへ描画ACKを返さない。

## renderer比較

private tmux 3.8で80×24→44×24→80×24、3秒・14tick、進行中の長い1行Q×1,000を比較した。
画面とscrollbackの合計Q数はBubble Tea標準rendererが63,653、独立writerのみが2,078、
独立writer＋TUIのreceipt／sealが1,000だった。後者を採用した。
比較コード・条件は[render spike](../../spikes/s06-render/README.md)。
旧S-06の短い「最後の行は」条件は今回1回となった。条件の異なる結果を過去記録の訂正や一般的保証には使わない。
Ghostty→SSH→利用者tmuxの実端末確認はprivate tmuxと分ける。

## 保存・読取・停止の実測

Linux/amd64、Go 1.27.2、隔離workspaceで[Core spike](../../spikes/s06-core/README.md)を実行した。
スパイクのproviderが生成する途中本文も、製品のAgent→journal→SQLite→会話→HTTP経路を通る。
過去S-06値はcommit `4c9bb5b` の実装、以下はI-06実装である。

| 64KiB本文・512更新 | S-06 | I-06 |
| --- | ---: | ---: |
| DB file | 約18.16MB | 778,240 byte |
| stream累計Go割当 | 約395.63MB | 200,349,728 byte |
| 変更なしsnapshotの反復割当 | 約74.62MB | 200 byte |
| 重いview解放後のSession冷復元 | 旧全inspection経路 | 167,392 byte、2.65ms |

途中累積本文の永久観測は0件。64KiBを4更新／40更新で生成した場合もDBは778,240 byte、
permanent assistant記録はともに2件だった（小さいfirst-visible markerとAgent会話message。model_resultは別kind）。
semantic payloadは278,150／278,151 byteで、日時等の可変metadata以外に更新回数分の本文増加はない。
最新stateの更新write量とstream割当は更新回数に応じて増える。
各値は一実行の観測であり、配信の合流やschedulingによって割当・時間は変動する。非同期batchやWAL接続は追加していない。

SSE writerを停止したまま別Session開始・実行終了・取消・終了保存を確認した。
writer再開後、稼働中Coreは最終snapshotとterminalを送った。別の購読writerを停止してCoreを終了し、
Core closeは0.008msで返り、writer解放後に購読が終了した。Core停止をterminal配信保証と混同しない。

## 同一Session 200turn

[計測プログラム](../../spikes/i06-cost/README.md)で本文256byteを200turn生成し、最後にterminal GETを100回行った。
外部provider・会話consumerなし、CoreとSQLiteを通す計測で、contextを切り詰めていない。

| checkpoint | GC後heap | process RSS | goroutine | DB |
| --- | ---: | ---: | ---: | ---: |
| 開始前 | 265,176 byte | 18,108,416 byte | 2 | 249,856 byte |
| 200turn＋100 GET | 888,656 byte | 32,481,280 byte | 2 | 78,516,224 byte |

累計Go割当は約1.77GB、実行は12.01秒。軽いrun索引はCore生存中に増えるので、総RAM一定とは主張しない。
本文・viewの保持と累計割当、RSSを同じ指標として扱わない。
重いviewを保持しない状態から200turnの会話600entityを復元すると、2.80秒、累計割当5,428,584 byteだった。
単一turnの冷復元と長いSession全体の復元を同じ値として扱わない。

**長いSessionの保存量はまだ大きい。** 内訳はmodel_request payload約30.60MB（400件）、
context_item約17.01MB（41,800件）、contents約8.37MBが中心である。
`internal/app/context.go`は各requestの入力message／tool／system／wireのsourceと差分manifestを記録し、
`inspect`・context provenanceのconsumerがある。途中本文の重複追記とは別の増加である。
今回の表示最適化を理由にこの証拠や次requestの入力を削っていない。
context source・manifestの保存／重複処理は、長時間利用に向けた次の改善対象として残る。

## 実provider・TUI・review

実OpenRouter ResponsesとOpenCode Go Chatで、生成JSによる入力読取→成果物保存→読み戻し、
reviewer子の呼出し、同じSessionの2turn目の編集が成功した。snapshot/upsertに進行中本文が含まれ、
GET／同ID POST／terminal cancel／SSE terminalの結果も一致した。
修正後はOpenRouterの44entity、OpenCode Goの37entityについて、ID・位置・本文・状態・name・
outcome・error・親execution/entity・detailがライブ表示と保存後復元で一致した。
OpenAI APIの`gpt-6-luna`でも非対話`--stream`から実ファイル作業が成功し、
本文の初回表示3.10秒が最初のmodel_result3.99秒より早く、最終回答の重複がないことを確認した。

| 発見経路 | 採用・修正対象 |
| --- | --- |
| 実provider | pending coalescingでexecution開始のfirstSequenceがterminal値へ動く。最初の位置を保持する。 |
| 実providerのlive／保存後復元比較 | JS内toolの本文・親call・outcome、task locator、assistant locator、子executionの親参照を揃える。 |
| 通常review | 同じ最終prefixを待つ複数consumerへ通知をbroadcastする。 |
| 通常review | model_resultの保存時にlatest stateを終了し、message追記までの間も確定本文を読めるようにする。 |
| 通常review | 旧hjhの実際のstate/message payloadからrequestと意味的位置を復元する。 |
| TUI focused確認 | ANSI装飾と末尾paddingの長さ変化で新本文をreceiptが飲み込む。当初source offsetとstyled位置を対応付けたが、今回の実端末不具合により撤去し、raw block末尾だけを確定境界とした。 |
| CLI source確認 | 公開thinkingだけの更新はstdoutの本文prefixを変えない。空Textによるslice panicを修正し、後続本文の逐次表示を確認した。 |
| TUI source確認 | resize時のsealを会話本文だけにし、古いheader/footer/editorをscrollbackへ確定しない。 |

再レビュー対象には、初期snapshotのResult参照解放、同時terminal取得の読取共有と解放、
子factの親execution情報、length終了の復元、TUI終了時の短い本文のscrollback確定も含めた。
通常再レビューでは、実SSEが会話付き最終snapshot→RunStateだけのterminalで送るのに、
TUIが会話付きterminalを待っていたP1を確認した。実tmuxでも最終回答後に次操作へ進まない状況を再現した。
他に、全確定entityのMarkdown再加工と、旧確定Messageのrequest対応漏れをP2として採用した。
批判的再レビューでは、番号付きlistの生成markerが本文source位置を先飛びするP2を
実Go/Glamour runnerで再現した。未完成inline Markdownの早期sealによるfragment確定もP2として採用した。
旧確定Messageは対応するmodel_resultへ結び付ける修正を行い、旧HEADと同形payloadの2stepで
entityの重複なし・completed・位置の一致を確認した。修正後の実provider保存履歴も44／37entityで差なし。
TUIの最終snapshot／独立terminal、source／block／行cache、生成list markerの位置、
未閉鎖Markdownの確定境界を修正した。commandの旧tailは次run前にsealし、
body・metadata・command entryをcacheで再利用する。全会話sourceの連結はbyte長に置き換え、
全styled本文は明示取得時だけ構築する。不要な旧版cacheはscope／幅／source変更で解放する。
未閉鎖長文は端末内のlive windowへ表示し、resizeでも確定しない。
先行する子のMarkdownが未完成な間に後続する親回答へreceiptを進めないことも確認した。

レビューは初回の通常・批判的確認と、各一回の15分以内の再レビューを行った。
再レビューで見つかった最終指摘の修正は、ownerがsource・focused regression・実経路で確認した。
この最後の修正後に別の独立再レビューは繰り返していない。

実測raw artifactはgit管理外の`dist/i06-live/`に保存した。Core計測は
`s06-512-final.json`、`fixed4-final.json`、`fixed40-final.json`、`cancel-final.json`、
長時間計測は`lifetime-200-final.json`、実providerは`provider-05/`と`go-chat-02/`、
CLIは`cli-01/`。各実provider artifactにはSSE frameと結果比較を含む。

## 初回I-06候補の確認

以下は利用者が表示崩れを報告する前の候補に対する確認である。
描画修正後の候補と混同しない。

- `go test ./...`：最終安定候補に一回実行し、成功。
- focused race：history／app／executiondata／conversation／corerun／HTTP／client／TUIの変更された責務を確認。
  最終TUI修正の全体raceと、その後のreceipt境界修正のfocused raceも成功。
- focused `go vet`、`gofmt`、`go mod tidy -diff`、`git diff --check`：成功。
- private tmux 3.8：日本語、Alt/Shift/Ctrl+Enter、bracketed paste、矢印/Home/End、Tab、Ctrl+C、Esc、
  実Core経由のファイル作業・最終Markdown・44/80列resize・F1取消・次run継続を確認。
  cancel後の遅延書込みはなく、短い最終回答は`/quit`後のscrollbackに残り、Coreは存続した。
  Esc後のpicker status表示も修正した。実TUIのartifactは`dist/i06-live/tui-05/`。
- Linux/amd64配布物：CGOなしのCore/TUI、ELF interpreterなし、依存licenseとmanifest hashを確認。
  CoreにTUI依存がなく、TUIにDB／QuickJS依存がない。配布Coreの起動とv2 API、TUI helpを確認。
  `dist/i06-packages/build-t0qa80_o/hjh-linux-amd64.tar.gz`、確認記録は`dist/i06-live/package-final/`。
- 使用したAPI key三つについて、実行artifact／ログ／DB／端末captureをprivateに照合し、値の混入なし。

初回候補では`dist/hjh`と`dist/hjh-tui`を配布候補から更新した。API v2への切替に合わせ、
試用時はCoreとTUIを両方再起動する。既存利用者processは停止していない。
既存dataやcredential、利用者のtmux設定は変更・削除していない。

未確認は利用者のGhostty→SSH→tmux実端末での操作・表示の受入。
長いSessionのcontext/source保存量と全会話復元コストは前掲のとおり残る。
後続のSession／provider picker等は計画の次段階であり、I-06への実装済み扱いにしない。


## 描画修正後の候補

今回の表示不具合を対象に15分上限の追加reviewを行い、長い未確定本文のlive preview停止P2を
修正して解消確認を受けた。raw block receipt、toolの永久確定barrier、本文の全文出力、
header/footer、改行の確認では追加の必須findingはなかった。
最後に、tool結果待ちでも完了済みの子結果をlive表示へ残す局所調整をowner判断で行い、
表示されることとreceiptが進まないことをfocused regressionで確認した。

修正後は`go test -race ./internal/tui ./internal/tui/renderer ./cmd/hjh-tui`、focused `go vet`、
`gofmt`と`git diff --check`が成功した。前候補の全体gateを繰り返していない。
最新候補でも、実Coreから読んだ同じSessionの94→70→94列と終了後の本文比較が一致した。

新しいLinux配布候補は`dist/i06-render-packages/build-nitc246v/hjh-linux-amd64.tar.gz`。
SHA-256は`21eae131d2703f40004b0b0f4e509bd9c39348891304f3640f105c33ed85c692`。
静的Core/TUIとmanifest内のfile hashを確認した。
この候補のTUIを`dist/hjh-tui`へatomic置換した。稼働中のTUIは旧binaryのままなので、
試用時はTUIを終了して再起動する。Coreのbinary・processは今回変更していない。

この時点で未実装だったOpenRouterの公開`reasoning_text`は、冒頭の追加修正で実装・実provider確認を完了した。
修正版のGhostty→SSH→利用者tmuxでの受入も残る。
