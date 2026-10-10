# 初期製品の採用方針と範囲

2026-10-10に利用者と合意した方針。S-01〜S-05の結果を基にした提案について、
利用者は「スキーマは旧henji-harnessと同じで、それ以外は提案通り」と指定した。
この文書はhjhの採用方針・初期製品範囲の正本とする。
引継ぎ原文、旧Henjiの参考文書、スパイクの簡略schemaやCLIは、この決定を上書きしない。

## 最初に使える動作

非対話runに依頼を渡すと、OpenRouterのモデルが必要なtoolを呼び、生成JSでtoolを組み合わせ、
実作業を完了して回答を返す。そのSessionを後のrunで閲覧・再開できることを完成条件とする。
固定JS・offline fixtureの成功を、実モデルによる作業完了の代替にしない。

最初の一本は、**OpenRouter → 生成JS → 外部toolによるファイル操作 → 最終回答 → 履歴保存 → 別runで再開**。
その実経路を作った後、初期版の他の動作へ広げる。

## 採用する方式

| 項目 | 決定 | 根拠 |
| --- | --- | --- |
| 対象 | Linux。最初の実装・配布検証はLinux/amd64 | 利用者の指定、[S-05](../spikes/s05-results.md) |
| 接続先 | OpenRouter。model IDは設定で選択する | 利用者の指定 |
| 最初の操作画面 | 非対話runから。TUIは後続 | 利用者の最終指定 |
| JS実行基盤 | `modernc.org/quickjs v0.25.0` | [S-01](../spikes/s01-results.md)、[S-03](../spikes/s03-results.md)、[S-05](../spikes/s05-results.md) |
| 実行配置 | Go本体内でQuickJSを実行し、Worker間でVMを共有しない | [S-01](../spikes/s01-results.md) |
| Agent loop | Goがモデル呼出し、tool実行への接続、終了判断を担当 | 採用済み提案。[初期runで実モデル接続を確認](../results/initial-run.md) |
| tool／hook | 外部JS。標準toolの振る舞いも外部定義に置く | [S-03](../spikes/s03-results.md)、[S-05](../spikes/s05-results.md)、採用済み提案 |
| 基礎操作 | HTTP・ファイル・プロセス等の実処理はGoが提供 | [S-01](../spikes/s01-results.md)、[S-03](../spikes/s03-results.md) |
| JS依存 | 準備済みESMを同梱。取得・変換と利用時の実行を分ける | [S-03](../spikes/s03-results.md)、[S-05](../spikes/s05-results.md) |
| 履歴保存 | SQLite、driverは検証済み `modernc.org/sqlite v1.60.1` を出発点とする | [S-02](../spikes/s02-results.md)〜[S-05](../spikes/s05-results.md) |
| 保存schema | 旧henji-harnessの現行履歴SQLite schema v3をそのまま採用 | 利用者の明示指定。[schema契約](history-schema.md) |
| 操作の共通化 | runと将来のTUIから使うGo APIを設ける | 採用済み提案 |

Go APIの共通化と、全操作をHTTPへ通すことは別である。
最初のrunは共通Go操作を呼ぶ。2026-10-10のTUI追加では、利用者の回答により同じprocessから
共通Go操作を呼ぶ方式を採用した。[TUIの計画](../plans/first-tui.md)を参照する。
QuickJS bindingに依存する操作はruntime内にまとめ、Agent loopや履歴へVM値を渡さない。

Workerの構成は開始時に捕捉し、現在のtool／hook・設定を使う。
そのWorkerが実際に使う構成と記録を一致させ、編集は次Workerから反映する。
VMはWorker内の複数モデルstepを通して保持する。公開tool／hookの形式とAPIは
[拡張契約](extensions.md)、実Agent loopでの確認は[初期runの結果](../results/initial-run.md)に記載した。

## 初期版に含める機能

- 非対話run、最終回答と実行進捗の出力、キャンセル。
- ファイルの読取り・作成・編集、コマンド実行、HTTP取得、生成JS実行。
- Sessionの一覧・閲覧・再開、失敗・キャンセル・途中のtool実行の確認。
- tool／hookと設定の編集を、本体再ビルドなしで次Workerへ反映。
- 使用構成の記録、過去の構成の確認、現在の設定によるSession再開。
- 全履歴を保持したまま、閲覧・モデル入力に必要な範囲を取り出す。
- API credentialとAuthorizationをログ・履歴へ露出させない。

実行証拠の保存と正常完了turnの会話採用は別の操作とする。
会話採用を担当するGo操作が正常完了後に保存済みmessageを採用し、失敗・キャンセルの証拠も保持する。
run利用者へ毎turnの手動adopt操作を要求することは、スパイクCLIから引き継がない。

外部JSの編集時に原因を確認できるよう、S-03で観測したmodule初期化の原例外が隠れる問題を
初期版のruntime実装で修正する。ライブラリ関数やhook関数内の既存の例外伝播も維持する。
新しい依存の互換性は必要な実toolで確認し、全npm／Node／Deno／Web標準互換を要求しない。

## 後続の範囲

TUIは非対話runが成立した後に追加する。子Agent、複数provider、Agent loopの外部化、旧DB移行は後続。
schemaに子実行・Core・queue等の表が含まれていても、その機能を初期版へ繰り上げない。
schema採用は既存DBの削除・変換・本番書込みや、release公開の指示を含まない。

その後、利用者が最初のTUIを基盤プロトタイプとして操作確認し、作業単位をインクリメントとした。
初期run・stream・履歴・再開・最初のTUIをI-01とし、I-02「子Agentへの委譲」を実装した。
I-02の範囲・操作契約・受入条件は[計画](../plans/i02-child-agents.md)、
実装・実provider・reviewの確認範囲は[結果](../results/i02-child-agents.md)を参照する。
利用者もI-02の子Agent呼出しを確認し、次のインクリメントへの進行を指定した。
旧Core・queue等の一括採用は行わない。

I-03は、利用者の指定によりbuilt-in genericを親子共通の実行エンジンとし、
role・instruction・利用可能toolを外部定義へ置く方式で実装した。
権限の指定は利用可能toolまでとし、toolの操作はtool内部で規定する。
具体的な選択・保存・操作は[I-03計画](../plans/i03-generic-roles.md)、
実装・確認・reviewの現在地は[結果](../results/i03-generic-roles.md)を参照する。
Coreからコンテナ・OS VMへAgentを配置する構想は[将来検討](future-agent-environments.md)に残す。

## 責務と状態の所有者

| モジュールの責務 | 所有する状態・操作 |
| --- | --- |
| 共通Go操作 | Session操作、root／子の開始・停止・終了保存、root会話採用、進捗購読、閲覧・再開の入口 |
| Agent実行 | 実行中のモデルstep、toolと生成JSへの接続、終了結果・会話採用の要求 |
| Provider | OpenRouterのrequest／response／streamとの接続、model選択、credential参照 |
| 外部拡張・構成 | tool／hookの定義と依存、開始時の構成捕捉、使用sourceの読込み |
| JS runtime | VM、module評価、Promise jobs、Go APIとのbridge、call単位の子取消・join、JSの割込みと値の解放 |
| Go基礎操作 | HTTP・file・processの実処理とcontext。VMを直接操作しない |
| 履歴 | 旧schemaに従うSession・execution・message・内容・構成・意味記録の保存と範囲読込み |
| CLI・TUI | 入出力と親子の進捗表示。Agent loopと保存SQLを所有しない |

これは責務の境界であり、先にファイル数を決めた分割ではない。
モジュール間は公開操作で接続し、共有の内部stateを直接参照しない。
DBは履歴モジュールが所有し、ProviderやUIから直接書き込まない。
巨大な旧storeや旧Workerの配置を、そのままGoへ移すことは要求しない。

## 実装順序と確認

詳細な段階、モジュールの境界、成果と確認方法は[非対話runの実装計画](../plans/initial-run.md)を参照する。

1. 採用DDLと保存payloadの対応を定義し、Goのhistoryを作る。
   旧schemaと同じ空DBを作り、実行・messageの保存、会話採用、別process読戻しを確認する。
2. module初期化例外を直したJS runtime、外部toolのfile処理、OpenRouter adapterとGo Agent loopを接続する。
   実モデルが生成したJSで実ファイルを操作し、結果を次の判断へ使って回答を完了できることを確認する。
3. 同じ実経路でSessionを再開する。設定・source変更の反映、過去の証拠と構成、失敗・キャンセルを確認する。
4. 初期版の他のtool、進捗・一覧・閲覧を整え、非対話runの利用手順を仕上げる。
5. Linux配布物からこの実モデル経路を確認する。その後TUIへ進む。

schema、payloadのcodec、tool／hook API、CLIのflag、model選択、表示形式の詳細は実装計画の各段階で具体化する。
ここで受け入れた機能範囲を、スパイクの簡略contractに合わせて縮めない。
初期runの実モデル接続とLinux/amd64配布環境での作業完了を実施した。
確認した動作、review修正、残る対象外は[初期runの結果](../results/initial-run.md)を参照する。

I-04では利用者指定のOpenCode GoとOpenAI API／ChatGPT認証を追加し、標準providerも外部JSON定義にした。
同じSessionで接続を選ぶ操作・role.model・認証は[provider設計](providers.md)、
実装と実確認の現在地は[I-04結果](../results/i04-provider-model-switching.md)を参照する。

I-05では利用者の指定により、コアからTUIを独立したコンポーネントへ分離する。
通信方式はHTTP＋JSON＋SSEに確定した。将来のWeb UIや独立クライアントも同じ公開APIを使う。
これはI-01で採用した同一process方式を次段階で置き換える方針であり、現在の実装はまだ同一processである。
具体化する操作・状態の所有者・受入条件は[I-05計画案](../plans/i05-core-tui-separation.md)を参照する。
