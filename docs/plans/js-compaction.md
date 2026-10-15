# JSによる要約と要求差替え

状態：S1〜S3実装・確認済み。[結果](../results/js-compaction.md)を参照する。

2026-10-15。利用者は親と同じprovider/model/effortへ要約専用instructionで一回問い合わせる方式を採用した。
JSで要約・差替えを行い、原文範囲と完了turnの最終回答への参照を付け、要約はSession単位のメモリで保持する。
agent全体のDB非保存modeは将来検討に分ける。

## 実利用の動作

opt-inの外部hook拡張を作る。毎projectRequestで既存要約と未要約の原文を組み立て、JS内の予算設定で
必要時に要約する。原文はDBへ維持し、モデルへの送信だけを要約＋直近の原文へ差し替える。
過去の要約は再要約せず、新しい範囲だけを追加する。古いDB会話を全て救済する経路は作らない。

標準hookとして自動有効にはしない。編集可能なJSの例として配布し、独立workspaceで動作を確認する。

## 経路と責務

既存CLI/Core→app.Run→execute→projectRequest→context APIを使う。
新しいagent loopやchild executionは作らない。

- `api.model.generate({instruction,messages})`：現在のprovider/model/effortで一回Generateする。
  toolsなし、履歴の自動追加なし、要約hookの再入なし。応答text/usage等をJSへ返す。
  provider encoder・認証・stream解析を既存Providerで共有する。providerが不完了・length等なら失敗し、
  要約の差替えを確定しない。本文・thinkingを親の応答履歴へ流さず、要約入力・応答全文をDBへ追加保存しない。
  bridge記録は操作名とbytes等のみ。モデルの指示とpromptをGoへ埋め込まない。
- `api.context.memory.get/set`：Service/Core所有のSessionメモリを要求hookから使う。
  JSON値のコピーを渡し、execution内で段階更新する。正常canonical採用後にSessionへ引き継ぐ。
  失敗・取消では前のSession状態を維持する。子は独立execution内だけ。削除時に同Sessionのメモリも解放する。
  直接CLIはprocess内だけ、Coreは再接続・次turnでも保持。再起動後は原文から必要時に再要約する。
- JS：発動量・保持範囲・要約instruction・元範囲ID・要約の蓄積・select/replace順序を所有する。
  原文/参照をCoreの別DB表へ保存しない。

通常の最終送信snapshotに要約が含まれることは既存保存契約どおりであり、要約専用の永続stateとは別である。

## JSの初期方針

値はJSの設定へ置く。モデルの有効入力予算を取得し、80%で要約を検討、要約後の原文目標60%とする。
予算不明時だけ入力800,000推定tokensを使う（2026-10-15の追加指示で固定24,000から変更）。
既知の予算は上書きしない。精度や長期利用の保証値ではなく、使用者が調整する方針値である。
既存summary、recall prefix、未要約history、current原文を扱う。
historyは最新から最初の予算超過まで取得し、既に切り離した遠い過去を毎回復活させない。

古い完了turnはturn単位、currentの古いtool交換は既存selectの完全call/result単位で取り出す。
今回のtask/追加指示と最新exchangeを残し、新しく取り出した範囲だけを要約用requestへ送る。
得られた文章へJSが参照を付け、既存要約と一緒に保持する。最後にselect→replaceする。
モデル呼出しが失敗・取消なら今回の新要約とcoverageを更新しない。

参照はSession/turn・execution ID・message範囲を持つ。完了turnは実際の最終assistant messageも示す。
進行中には最終回答参照を作らない。表示は短縮ID、内部は完全ID。
thinking個別参照は付けず、tool個別参照も一律には付けない。原文範囲を既存history/inspectで辿れる形式を記す。
IDをモデルに生成させない。

## スライスと確認

1. S1：副作用を分けた一回model APIとSessionメモリ。親selection継承、本文非保存、cancel、
   次turnへの正常採用時引継ぎ・失敗時維持をfocused確認。
2. S2：JS拡張・参照・coverage。turn内とturn間の差替え、再要約しないこと、原文と参照の対応、
   Session再開・Core再起動時の再生成を独立local HTTP/実QuickJSで確認する。
3. S3：compiled CLI/Coreと許可済みOpenCode Go/DeepSeek V4.1 Flashで短い実要約を確認。
   要約後に元の依頼を継続できること、child executionを作らないこと、DB原文と追加保存境界を照合する。

slice中はfocused test。安定後に関連package test・focused race・対応staticcheck・diff checkを一回実行する。
rootが実経路と保存境界を確認し、結果文書へ記録する。標準有効化・常用DB/稼働Core切替は含めない。
