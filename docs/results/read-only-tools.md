# 読取り系tool・ファイル操作・標準roleの整備結果

2026-10-13。[計画](../plans/read-only-tools.md)に従い、比較表3〜10と標準roleを整備した。

## 使える操作

- ls：entryのpath・種類・byte数・更新時刻、flat treeとdepth、結果範囲指定。
- find：glob・種類・excludeと結果範囲指定。空directoryも探索対象。
- grep：ripgrepのregex／literal、大小文字、周辺行、重複なしfile一覧、結果範囲指定。
- wc：fileごとの改行数・Unicode空白区切りの単語数・byte数と合計。
- git_inspect：status / diff / log / show、paths・rev・staged・stat・contextと結果範囲指定。
- read_file：1始まりのoffset / limit、改行を保持する行範囲、totalLinesとnextOffset。
- edit_file：edits配列の順次置換、各置換件数と合計、一回保存。
- write_file：不足する親directoryの作成。

直接callとrun_js内のcallは同じ外部module・host・hook・履歴経路を使う。
新しい状態ownerや履歴schemaは追加していない。tool自身の利用指示と保存snapshotも既存経路。
当初grepはrg必須だった。後続改善でrg優先・GNU grepへのfallbackに変更した。
Git確認はGitが必要。今回のVMでは実行中CoreのPATHからもrgを解決できることを確認した。
findはGo filesystem探索で、隠し・ignored entryを含む。必要な省略はexcludeで指定する。

reviewer / explorerはrun_js・read_file・ls・find・grep・wc・git_inspect。
implementerはその読取りセットとwrite_file・edit_file・command・http・web_fetch・web_search。
defaultは全標準toolと子制御を維持する。親の標準指示にroleの使い分けと通常tools省略を追加した。
model/provider/effortは固定していない。

## 確認範囲

実file・directoryと実Git repositoryを使い、Workerの直接／生成JS経路で検索・集計・
Git操作、結果の続き取得、改行を保つ部分読取り、依存する複数置換、親directory作成を確認した。
読取りtoolだけのWorkerでは生成JSにcommand・write_fileが公開されないことを確認した。

共通app経路では親→3つのroleへの子委譲→読取りまたは編集→子回答→親の次requestを通し、
実tool結果と保存された子構成・履歴・完了状態を照合した。モデル応答は注入した実行指示を使い、
有料外部APIは呼んでいない。実モデルによるrole選択の安定性は今回未確認。

変更packageのtest・vetとdiff checkを実施した。既存のedit確認を新しいedits入力へ更新した。
予算回帰確認は標準tool定義の増加に合わせ、一つの15KB読取りは保持し、二つは履歴選択が必要に
なる入力予算へ調整した。子の予算確認も親の標準定義を収める設定へ調整した。
製品のコンテキスト予算処理は変更していない。

配布assetsとこのworkspaceの拡張へ反映した。既存Coreは自動停止・再起動していない。
新しいhost操作を使うため、更新binaryでCoreを再起動してから試す。
既存の独自workspace configの自動書換えや旧single edit入力の読替えは行わない。

## 独立reviewと指摘対応

利用者の指定によりreviewerがコード・test・実利用経路をread-onlyで確認した。
製品不具合の指摘は一件：git_inspectでstat:trueとcontextを同時に指定すると、
Gitの--unifiedがpatchも要求してしまい、集計だけを期待する操作に本文が混じっていた。
stat指定時は--unifiedを付けない修正を行い、diff/showの回帰確認と、
reviewerによる実Git出力の再確認が通った。未解決の製品不具合の指摘はない。

具体的な確認不足として、lsの非空subtree・depth打切り・nextOffsetによる継続、
findのexcludeによるsubtree省略と継続を指摘された。
ownerが通常の入れ子directoryを使うWorker testを追加し、全頁の順序・depth・
欠落や重複がないこと、除外directoryの配下が結果に混ざらないことを確認した。
wcの複数file合計とUnicode空白も、実file入力の確認を追加した。
これらの追加確認はownerが実施し、独立review全体は繰り返していない。

実モデルによるrole選択と、更新Coreでの人間の操作確認は未実施のままである。

## 後続改善：親向けrole catalogのツール一覧

実利用の複数モデルでrole選択は成立したが、親が読取りroleへコマンド実行を依頼する例があった。
利用者の指定により、名前・説明だけだった親向け一覧にrole catalogの既存tools一覧を追加した。
各roleの行に`(tools: [...])`を表示し、空一覧は`(tools: [])`。独自roleも設定から取得する。
子の専用指示全文は追加せず、標準4roleの追加テキストは合計450 UTF-8 bytes。
委譲の利用指示、tool選択・上書き、roleの設定自体はこの変更では拡張していない。

run_agentとspawn_agentだけの親、独自role・toolなしrole、保存指示との一致をcapture経路で確認した。
共通appの親子実行ではモデルrequestに各roleの実tools一覧が入り、保存構成の指示と一致することを
確認した。既存role選択・予算管理のfocused test、extensions test、vet、diff checkが通過した。
バイナリを更新した。Coreの自動再起動と、変更後の実モデルでの効果確認は行っていない。
