# Slash command候補

2026-10-14。利用者指定の機能比較33を実装した。
[操作・参考source・責務](../design/slash-command-menu.md)に従う。

`/`入力で入力欄の上に候補を自動表示する。名前・引数hint・説明を持ち、
完全一致・前方一致を先に、名前の順序を保つ文字一致で絞り込む。
上下とPgUp/PgDnで選び、Tabは補完のみ、Enterは既存commandの実行または必須引数の入力へ進む。
Escは入力を保って候補を閉じ、編集またはTabで再表示する。
引数や改行を入力したときはコマンド名候補を出さない。

PiのTab補完・slashのEnter実行と、OpenCodeの名前/説明候補・入力上部配置・高さ追従を参考にした。
OpenCodeのTabによる組込みcommand直接実行は採用していない。
既存24 commandの名前・説明・引数hintを一つのcatalogへ集約し、認識・候補・helpで共有する。
旧Tab補完の専用状態・候補一覧status出力は置換した。
TUI内の状態とdock描画だけで実現し、Core API・DB・別screen overlayは追加していない。

## 確認

- キー入力とpasteからの自動表示・名前絞込・完全一致優先・上下・Tab・Enterを確認した。
- Tabがcommandを実行しないこと、引数必須のrenameを入力欄へ残すこと、
  Esc後のdraft保持・再表示、引数/複数行/候補なし入力の保持を確認した。
- 実行中に候補からeffort pickerへ進み、元runを保持して戻れることを確認した。
- 表示範囲をスクロールして末尾commandを選べること、入力cursorの下部位置、
  候補の非保存、表示中の親本文の伸長・確定・一回出力、狭幅resizeを確認した。
- TUI packageのtests、変更範囲のvetとdiff checkが成功した。
  旧「候補なしTab補完」とfooter案内のtestsは新しい実入力eventと選択案内へ更新した。
- 更新したdist/hjh-tui・一時Core・PTYで、`/ef`絞込→Tab補完→Enterでeffort picker→high保存、
  必須引数の補完、Esc/Tabによる再表示、50列×14行resize、候補から/help実行を確認した。
  これらの操作でSession・モデル生成が始まらないことも実Coreで確認した。

常用Core・workspaceには触れず、一時process・fileは確認後に片付けた。有料APIは呼んでいない。
TUI binaryの更新のみなので、TUIを起動し直すと適用される。
