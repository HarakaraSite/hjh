# 非対話runの実装・確認結果

2026-10-10。[承認済み計画](../plans/initial-run.md)のP-01〜P-05を実装し、
利用者指定のOpenRouter `deepseek/deepseek-v4.1-flash` で作業完了を確認した。
認証は旧Henjiの `/home/agent/.local/state/henji-harness/v1/credentials/openrouter-api-key` を参照した。
キー値・Authorizationは本文、構成、履歴、配布物へ含めない。

この文書は初期run完成時点の記録。その後、利用者の指定でCLIの `--stream` を追加した。
逐次表示の実装・実provider確認と更新した配布物は[run streamの結果](run-stream.md)を参照する。

## 実装した経路

`cmd/hjh → app → Agent loop → OpenRouter → JS Worker → 外部tool／hook → Go基礎操作`
を接続した。複数model stepの間、Workerは同じVMと捕捉済みsourceを保持する。
runの正常終了時にhistoryが保存message全体を会話へ採用する。
失敗・取消は証拠を残し、再開時の採用会話へ混ぜない。

| 段階 | 実装と確認 |
| --- | --- |
| P-01 | 旧SQLite schema v3のDDL、message・provider state・構成・source・意味記録・contextの保存。21表・11明示indexを照合。別process読戻し、正常turn採用、revision、失敗取消の非採用を確認 |
| P-02 | Worker専用VM、外部ESM、run_js、内側tool・hook・hostの関係、開始時source捕捉。複数callの状態保持、編集後の新Worker、module初期化の原例外を確認 |
| P-03 | OpenRouter SSE、tool call組立て、reasoning details保存・再送、実モデルのファイル作業と別process再開 |
| P-04 | read/write/edit/command/HTTP、sessions/history/inspect、完全turnの入力projection、範囲閲覧、途中text・失敗・SIGINTの記録。実tool・hook編集と停止後の再開を確認 |
| P-05 | Linux/amd64静的配布、編集可能な拡張、準備済みYAML・semver ESM、license／notice、利用手順。開発環境なしの実モデル作業・再開を確認 |

保存DDLは[固定した旧DDL](../design/henji-history-schema.sql)と同じ。
SHA-256は `6f7cf24c97ca4afe50ba6ab9de25cf8a1e3eb6ece364464af0bb0ae95e2b0eef`。
build_jsonは利用者承認に従い `denoVersion → goVersion` のみ専用fieldを変更した。
保存対応は[対応表](../design/history-payloads.md)、操作は[run API](../design/run-api.md)、
拡張は[tool／hook API](../design/extensions.md)を参照する。

## 実モデルによるファイル・再開・HTTP・command

workspaceは `/tmp/hjh-product-live-20261010`。
Sessionは `1b277fc9-5536-4939-8c47-7c6226b3772e`。

| Execution | 結果 |
| --- | --- |
| `efa13c59-2994-4008-a688-4cc66e463df3` | input.txtの3行を生成JSと外部file toolで加工し、output.txtへ保存・読戻し。モデルがcommandでも照合して最終回答。exit 0、正常採用 |
| `397db258-2e76-4cd3-ae78-976805b968b8` | 再開試行でモデルが `find /` を実行し、約38 MBのtool結果を取得。次requestをOpenRouterが入力8 MB超過で拒否。exit 1、failed／non_canonical。失敗証拠を保持 |
| `44aa53de-c875-401e-ba4e-c8dd86beb043` | 同じSessionを別processで再開し、探索不要の具体的な追加編集を依頼。run_js→edit_file→read_fileで3箇所を置換し、exit 0、正常採用 |
| `560feb6a-5d81-49e6-a4db-85ed27ce638e` | 生成JSからHTTPSでexample.comを取得しstatus 200、外部 `wc -l output.txt` で3行を確認。変更したafterToolの `hookRevision: live-v2` が両結果へ反映。exit 0、正常採用 |

編集後の実ファイルは次の内容となった。

```text
再開済み: alpha
再開済み: beta
再開済み: こんにちは
```

失敗した大きなtool結果を自動削除・切捨てせず、同じSessionの正常会話から再試行できた。
大きなtool出力はproviderの入力上限へ到達しうることを実際に観測した。
この版では出力上限・自動要約・自動切捨てを設けていない。
初回と再開のstdout・stderr、DB、使用sourceは上記workspaceに保存している。

## CLIからの停止

実モデルへ生成JS、待機HTTP、長時間commandを依頼し、それぞれ実処理の開始後にSIGINTを送った。

| 実処理 | Execution | 結果 |
| --- | --- | --- |
| file書込み後のJS無限loop | `a80cff94-7a68-49e6-a9b7-ba3133949e9d` | exit 130、cancelled／non_canonical。書込み済みfileと途中の記録を保持 |
| `sh`からの `sleep` | `d6902cde-0095-4c43-b969-f89cbe5c4be1` | exit 130、cancelled／non_canonical。command PID消失を確認 |
| 待機HTTP | `4ea8ce49-eba0-4a8a-ba03-2bd0f2f8cafe` | exit 130、cancelled／non_canonical。取消要求と内側host終了を保存 |
| stdin入力待ち | execution開始前 | pipe・terminalともEOFを送る前にSIGINTでexit 130 |
| 取消後のSession再開 | `f24473d6-488f-455a-a841-85605d3ef861` | 実モデルのrun_jsでafter-stop.txtへ「再開成功」を書込み、exit 0 |

実証拠は `dist/product-cancel/` に保存。
取消はローカル処理の終了を確認したもので、接続先のモデル処理・課金停止の保証ではない。

## Linux配布環境

ホストはLinux/amd64、Debian系kernel `6.12.111+deb13-amd64`、Go `1.27.2`。
`CGO_ENABLED=0`・`GOAMD64=v1`・`-trimpath`でビルドし、ELFにINTERPがないことを確認した。
sudo chroot内にはGo・Node・Deno・curl・共有libc・`/proc`を配置せず、本体・外部拡張・
OSのCA／resolver／hosts・作業fileのみを置いた。実API credentialはprocess環境から私的に渡した。

最小環境のSessionは `60ea13db-6bec-4a51-9ee4-3fbb7d49189b`。

| Execution | 確認 |
| --- | --- |
| `d65d17ea-86d4-477a-951a-5a392a440d1f` | 実OpenRouter HTTPSでモデルを呼び、生成JSでinput.txt加工・output.txt保存・確認。exit 0 |
| `df798317-96ae-4c6e-a9d7-e6dbbe8632e1` | 別processのSession再開。toolとhookの編集後、追加編集・readback・revision反映。準備済みYAML／semver ESMをHTTP catalog toolで実行し互換version 1.4.2を確認。exit 0 |
| `6b3d2a80-bf38-456a-be42-a022867c65b9` | stdin取消修正を含む最終配布binaryへ置き換え、同じ最小環境でSession再開・追加編集を実モデルで再確認。exit 0 |

sessions、完全turn範囲のhistory、初回／再開のinspectも別processで成功し、
新旧構成・sourceと正常採用を確認した。最終binaryでは最小環境のstdin待ちSIGINTもexit 130。
展開直後の全fileをmanifestのhashと照合し、実ログ・DBにcredential値がないことも確認した。

最終archive：`dist/packages/build-g2v97hsc/hjh-linux-amd64.tar.gz`。

- archive：12,052,428 bytes、SHA-256 `ad77c31703aea70c0a8b3a2e799c9552fc95375658e488c8e17b57a1b77b79fb`
- binary：21,205,120 bytes、SHA-256 `4f28ad7a6e5a01f0543d73a943e12c7a6e447f0fdb1bf60ccba55a509b821e45`
- 最初の配布実確認：`dist/product-live-linux-01/summary.json`
- 最終artifact確認：`dist/product-live-linux-final/summary.json`

再ビルドは `python3 scripts/package-linux.py`。
実APIの配布確認は `python3 scripts/verify-linux-live.py ARCHIVE --out NEW_DIRECTORY`。
後者は実モデルを呼び、API利用料が発生する手動検証である。
検証ログ・DB・archiveはgit管理外に保存し、公開・releaseは行っていない。

## Test・review

各moduleのfocused testで、DDL／旧serializerとの対応、別process保存読戻し、捕捉source・VM寿命、
HTTP／process／JS取消、SSE・tool継続・opaque reasoning、認証の露出防止、
appの実tool経路・再開・失敗非採用を確認した。
step間のtool失敗は、missing fileの原errorとcall IDが次model requestへ返り、
同じWorkerで後続toolがfileを作成して最終回答・正常採用へ進む経路を確認した。
停止処理の変更後はhost／runtime／CLIのrace test、CLIとapp接続にはgo vetと実binary buildを行った。
fixtureだけを完了判定にせず、上記の実CLI・実モデル・実ファイルを結果の根拠とした。

history、runtime／provider、app／Agent／CLIの範囲を独立reviewした。
採用した指摘と修正は次の通り。各reviewerは修正箇所と既存指摘だけを一回再確認し、解消を確認した。

| 指摘 | 修正と確認 |
| --- | --- |
| 相対DB pathをread-onlyで開けない | SQLite URIへ絶対pathを使用。相対pathから別openで読戻し |
| relation解決をcallerの指定値だけで決める | store transaction内でtarget recordの存在から解決。future targetも追跡 |
| `finish_reason=length` を正常採用する | 終了理由をAgentへ渡し、途中／空回答を保存後failed・非採用。両応答形のfocused確認 |
| root JS rejection後も未完了hostを待ち続ける | call contextを取消しhost終了・記録をjoin、原errorを返す。並行HTTP＋missing fileの再現確認 |
| shell終了後のpipe待ち中に取消group killが届かない | commandのWait完了まで独立context watcherを保持し、group停止・join。shell先行終了の再現確認 |
| stdin入力待ち中のSIGINTが効かない | 継承FDをnonblockingでGo pollerへ登録、取消時Close・read join。focused testと実pipe／terminal／最小Linuxで確認 |

親側の接続確認でも、累積stream textを重複連結する箇所を修正し2chunk途中失敗で確認した。
配布実行で観測した `/proc/self/exe` 依存も、S-05のinvocation path解決方式で修正し最小環境で確認した。
OpenRouter reasoning detailsは返却順にopaque JSONとして保持する。観測した同indexのtext断片について
公式docsにmerge規範がないため、旧source同様の保持を維持し、実モデルの継続成功を確認した。

## 残る範囲

TUI、子Agent、複数provider、Agent loop外部化、旧DB migrationは後続。
他architecture／distributionは未確認。任意のNode／Deno互換・npm依存を対応済みとしない。
この結果は初期runの受入範囲であり、全作業のモデル成功や全Linux環境の対応を意味しない。

## 2026-10-13追補：実装ソースの自動保存を停止

利用者指定により、親子共通の実行開始処理からextension treeのresource archiveを外した。
ツール・hook・依存ファイル・元configの全文をDBへ自動保存しない。
Workerのメモリ上へのsource captureと固定したmodule評価は維持する。AGENTS.mdの原文・出典、
使用構成と指示・ツール定義、モデル入力、呼び出し・結果、生成JSは引き続き保存する。
明示的なread_fileの結果は、実装ファイルであっても通常のツール結果として保存する。
schemaと既存履歴は変更せず、過去のsource archiveを一括削除する処理は追加していない。

親子の新しいsource反映、tool/model overrides、利用指示、workspace指示・skill、lifecycleのfocused
testsが成功した。source archiveの存在を要求していたtestsは、自動archiveがないことの確認へ変更した。
appのvetとdiff checkが通り、`dist/hjh`と`dist/hjh-tui`を更新した。

実binaryのCLI runとSession再開を、一時workspace・DBとローカルHTTPのChat Completions応答で確認した。
QuickJSのツールが依存moduleをimportして返した値と、同じ依存moduleを使うruntimeStart hookの指示が
実requestに届いた。ツール・依存・hook・元config・未使用ファイルの全文はcontentsに0件、
AGENTS.mdの原文記録は2件、送信内容を持つmodel_requestは3件だった。
生成JS・ツール結果・構成snapshotも保存され、再開したrequestに過去の結果が含まれることと、
foreign_key_checkが正常なことを確認した。外部の有料モデルAPIは使っていない。
