# 読取り系toolとファイル操作の整備

2026-10-13。利用者が比較表の3〜10を指定した。先にtoolを整備し、その後に
reviewer / explorer / implementerの標準セットを定義する。

## 必要な動作と現行経路

親は子へ読取り系toolだけを渡して、ファイル探索、本文検索、集計、Git確認を依頼できる。
直接tool callとrun_js内の選択tool callは、外部module→実装API→Go host→通常の
tool結果・履歴・次モデル入力という既存経路を共用する。生成JSへ汎用commandを追加公開しない。
既存role.toolsと呼出し時tools指定を使い、新しい権限機構は追加しない。

## 今回の範囲

- ls：directory entry情報とtree、深さ・結果範囲指定。
- find：globとfile/directoryの種類で探索、結果範囲指定。
- grep：正規表現／固定文字列、大小文字、周辺行、ファイル一覧、結果範囲指定。
- wc：複数fileの改行数・単語数・byte数と合計。
- git_inspect：status / diff / log / show、path・revision・staged・stat・結果範囲指定。
- read_file：1始まりのoffset / limitで行範囲を読む。省略時は全文。続きの位置を返す。
- edit_file：edits配列の完全一致置換を順に適用し、最後に一回保存。
  各置換の件数と合計を返す。旧single oldText/newText入力は置き換える。
- write_file：不足する親directoryを作成する。

本文検索はripgrep、Git確認はGitを実装内部から固定した操作で起動する。
汎用argvやshellをモデル向け引数にしない。ripgrepとGitは利用環境に必要で、
今回のVMでは実在を確認した。ls・find・wc・file読書きはGoのfilesystem操作を使う。
旧Henjiのサイズ上限・permission機構・fallbackは引き継がない。
role定義のmodel/provider/effortは固定せず、既存の親selectionと呼出し上書きを使う。

## 実装と確認

1. hostのdirectory／集計操作とread/write、外部inspect moduleとedit、定義・利用指示を整備。
2. 保存・tool選択は既存capture経路を使い、配布assetsとこのworkspaceの拡張へ反映。
3. 実directory・file・Git repositoryで直接／生成JSの操作、改行を保つ部分読取り、
   複数編集、親directory作成を確認。読取りtoolだけのWorkerでcommand非公開を確認。
4. 新toolでreviewer / explorer / implementerを定義し、親にrole選択と通常tools省略を案内。
5. 変更packageのfocused test・vet・diff check、binary更新。外部有料APIは呼ばない。

未確認事項：モデルが仕事内容から適切なroleを選ぶ安定性は、利用者の実利用で評価する。
今回の実装はtoolの通常公開範囲を分けるもので、実行環境の強制隔離は対象外。
