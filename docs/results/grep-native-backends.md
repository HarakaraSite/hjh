# rg未導入時のgrep

利用者の指摘に従い、標準grepをrg優先・GNU grepへのfallbackに変更した。
PATHにrgがあればrgを使い、なければgrepを使う。実行fileを初回callで解決し、
Worker内では保持する。自動install・downloadは行わない。

## 現行状態への案内（2026-10-16 source照合）

backend選択とWorker内での保持は現在も外部inspect.jsが担当するが、pagingの所有は後続でHostへ移した。
現在はrg／GNU grepとも`grepCommand`（`internal/host/grep.go`）内で出力を解析し、
指定offset／limitのページとtotal／nextOffsetを作ってからHost観測へ返す。JSは引数を組み立て、
ページ済み結果へbackend／ignoreApplied等を付して返す。
[後続のページ処理・v4切替結果](grep-paged-results-and-v4-cutover.md)を参照する。
以下はbackend対応時の実装・確認記録であり、当時のJS paging責務を現行仕様へ適用しない。

## backend対応時の実装

当初は外部inspect.jsがbackend選択、引数とpagingを所有した。既存host.commandのprocess group・
キャンセル・stdout/stderr収集を使う。GNU出力はNUL区切りfilenameと一致／周辺行の形式を
Go host側でJSON化前に解析し、改行入り・非UTF-8のfilenameを保持する。
新しいhost primitiveはresolveExecutableとgrepCommandで、公開toolは増やしていない。

結果は既存lines/files/matches/offset/total/nextOffsetを維持し、backendとignoreAppliedを追加した。
rgはnative ignore規則とregex/glob、GNUは拡張正規表現・filename include/exclude globを使う。
GNU側は.gitignoreを適用せず、binary fileをskipする。両方式の差異はtool説明へ記載した。
GNUはsource末尾が改行なしでも出力に改行を付けるため、textはnative出力どおりに返す。

## 確認

rgとGNU grepが両方あるPATH、およびGNU grepだけのPATHで、実fileをWorkerから検索した。
直接callとrun_js内callについて、backend選択、ignore差分、隠しfile、一致・周辺行、
literal/大小文字、filename glob、paging・0件・非UTF-8のfilenameを確認した。
rgの明示globはignore fileの指定を上書きするnative動作に従う。

findのfilepath.WalkDirは変更していない。外部processを起動せず、exclude directoryの配下を
走査しない。一方で正確なtotalのため全体探索し、pagingの再callでも再走査する。
fdとの速度比較は実施していないため、fdより速いとは評価していない。

host・JS runtime・extensionsと、appのrole委譲・context予算に関するtest、変更範囲のvetと
diff checkが通った。このworkspaceの標準grep moduleと定義を更新し、バイナリも更新した。
新host primitiveを使うため、稼働中Coreへ反映するには新binaryでの再起動が必要。
