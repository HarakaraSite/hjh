# S-05結果：Linux配布・起動条件

**指定されたLinuxのうち、この実機のLinux/amd64で成立。** QuickJS・SQLiteを含む静的Go本体と、
編集可能なtool／hook・準備済みYAML／semver依存をtar.gzへまとめ、展開物から実処理を行った。
Go・Node・npm・curl・shell・共有libcがないrootで初回起動し、本体を再ビルドせずに編集を反映してSessionを再開できた。
スパイクの成立であり、LLMを含む製品の完成やLinux全環境への対応を意味しない。

- [計画](s05-plan.md)
- [再現手順](../../spikes/s01/S05.md)
- [実行summary](s05-artifacts/summary.json)
- [配布manifestと依存情報](s05-artifacts/package-manifest.json)
- [保存SQLite](s05-artifacts/history.sqlite)

## 配布したものと起動環境

2026-10-10、Linux kernel `6.12.111+deb13-amd64`、amd64、Go `1.27.2`で検証した。
利用者との対象OSの合意は「Linuxのみ」。architectureはこの実機のamd64で検証した。
`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 go build -trimpath` を使った。
ELF program headerにINTERPがなく、実際にloaderもlibcもないrootで起動した。

本体は20,785,655 bytes、tar.gzは11,833,590 bytes、126 files。
binary SHA-256は `53c5d4273323d175ef97422c345c1a57177c171fa29c509ecc49f4430f866aa5`、
archive SHA-256は `843901de2ef5432aef83ed0e841addfec00a57406e844dcabcfc6554e768c037`。
配布物はローカルの `dist/s05-packages/build-ksb_ygfr/hjh-linux-amd64.tar.gz` にある。
distはGit管理外。binary本体をdocsへ複製せず、manifest／hash／起動記録を保存した。

配布内容は本体、設定JSON、tool／hook・hook設定、vendor、利用メモ、例のJSとYAML、
Go本体と使用Go moduleのlicense、JS依存のlicense。Go依存version／checksumと各fileのhashも記録した。
使用bindingは既存スパイクと同じ `modernc.org/quickjs v0.25.0`、`modernc.org/libquickjs v0.13.1`。
SQLite driverは `modernc.org/sqlite v1.60.1`、実行engineは `3.53.4`。
yaml `2.9.1`のESM treeとsemver `7.8.5`の準備済みbundleを含む。
`_refs/quickjs` の参照sourceを新たに本体へ組み込んだものではない。

新しいrootには最初、配布物の `/app` と書込み用 `/tmp` だけを置いた。
`sudo chroot --userspec=1000:1000` で非root利用者として実行し、procやdevもmountしなかった。
chrootの準備には検証hostのsudoを使うが、通常の配布CLI利用には不要。
実行時は同じhostのkernel・networkを使う。別Linux machineやcontainer imageでの検証ではない。
[初回rootの全file](s05-artifacts/initial-root.json)と[最終root](s05-artifacts/final-root.json)を残した。

## 実際に成立した利用動作

| 動作 | 観測結果 | 証拠 |
| --- | --- | --- |
| 展開後の初回起動 | 外部hookが設定fileを読み、Go HTTPで実YAMLを取得。yaml／semverがcompatibleだけを選び、reportを保存・読戻し。SQLiteへ40 eventsを保存。外部programなし。 | [first](s05-artifacts/first.json) |
| 編集後の次Workerと再開 | 配布copyのtool-one→two、hook-one→two、hook設定とinstructionを編集。別PIDの次runに全編集が反映され、採用済み1 pairを2 messagesとして受け取った。binary hashは不変。 | [second](s05-artifacts/second.json) |
| 旧構成と会話の確認 | 2 turns、2 adopted pairs、80 eventsを別プロセスから閲覧。旧instructionと新instruction、異なる構成IDを維持。 | [再開後inspection](s05-artifacts/resume-inspection.json) |
| CAを使うHTTPS | rootへCA fileとhostsだけ追加し、localhostの実TLSサービスへ接続。証明書検証を無効化せず、同じworkflowが完了。 | [TLS run](s05-artifacts/tls.json) |
| 外部HTTPSとDNS | OSのCAとresolv.confを使い、curlなしで `https://bellard.org/quickjs/` からHTTP 200、4,967文字のbodyを取得。 | [public HTTPS](s05-artifacts/public-https.json) |
| 外部commandを使う追加tool | `/bin/echo` がないと元のfork/exec errorがWorkerとSQLiteへ残った。echoと、そのELFが要求するlibc／loaderを追加するとstdoutを取得。shellは入れていない。 | [未導入](s05-artifacts/command-missing.json)、[導入後](s05-artifacts/command-available.json) |

最終Sessionは6 turns、2 adopted pairs、148 events。
未採用のHTTPS実行、失敗したcommand、導入後の成功したcommandも証拠として残った。
SQLite integrity_checkはok。全runの終了後に残るhost operationは0だった。
[最終inspection](s05-artifacts/final-inspection.json)に構成、会話、turnとeventを保存した。

## 必要な依存と追加ツールの条件

| 場面 | 必要なもの |
| --- | --- |
| 取得・展開 | tar.gzを扱う手段。展開後のCLIは直接実行可能。 |
| HTTP・JS・file・履歴保存 | Linux/amd64 kernel、network、外部sourceとvendor、書込み可能なstate。Go／Node／npm／curl／sqlite3／qjs／共有libc／shellは不要。 |
| hostnameへのHTTP／HTTPS | 使用環境のhosts／DNS設定。一般のHTTPSには信頼するCA証明書と正しい時刻も必要。CAやDNSはOS資源を使う。 |
| echoを呼ぶ今回のtool | 指定したecho executableと、その実行ファイルのruntime依存。今回のDebian版echoはlibcとloaderを要求した。 |
| S-01のshell tool | そのsourceが実際に呼ぶsh／tr／sleep。基本HTTPには不要。 |
| 本体の再ビルド | Goと取得済み／取得可能なGo依存。CGO compilerは今回の配布buildでは使わない。 |
| JS依存の再準備 | S-03のNode／npm／esbuild。利用時のruntime依存ではない。 |
| このchroot検証の再現 | host側のPython、Go、readelf、openssl、ldd、sudo／chroot。配布CLIの必須依存とは分ける。 |

GoのCA参照方式は今回使用したsourceの `crypto/x509/root_linux.go`、
DNS設定は `net/dnsconfig_unix.go` にも対応している。
CA・DNS設定やproxyは配布物に環境依存の値を固定せず、利用環境のものを使う。

## 成立範囲と次の判断

今回の受入動作について不成立はなかった。追加command未導入時の失敗は、必要な外部programを
区別して説明するための観測であり、基本HTTPやSessionのblockingではない。
新CLIのfocused `go vet`、Python構文確認、配布物を使う5シナリオを実施した。
共有runtime／Session処理は変更していない。race buildはこの静的配布物の成立条件に含めていない。

未確認はLinuxの他architecture・distribution・kernel版、企業等の特殊なDNS／proxy環境、
任意の外部commandとJS依存。モデルAPI、TUI、Agent loop、更新／install機構は対象外。
S-03で観測したmodule初期化例外の見え方は共有runtimeの既存課題として残り、今回も変更していない。

S-01〜S-05の中核仮説には実行証拠が揃った。
次はその成立範囲を根拠に、binding、Go公開API、外部拡張境界、履歴契約と初期製品の範囲を選ぶ。
スパイクのschema・CLI・固定JSを、そのまま製品の正本として採用したとは扱わない。
