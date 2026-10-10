# S06 normal-screen rendering spike

Run it from the repository root:

```sh
go run ./spikes/s06-render
```

The sample emits Japanese assistant text in timed chunks. Completed newline-delimited assistant
lines and a tool marker go through Bubble Tea v2.1.0 `tea.Println`, which inserts them above the
managed view in normal-screen mode. The startup title and key help are also printed once above the
view. The view keeps only the unfinished assistant tail and the draft/footer; completed lines
leave it when their `Println` command is queued. The live tail is hard-wrapped by terminal cell
width so Japanese stays visible after a width change. The footer uses Bubbles v2.2.1 `textarea`
with dynamic height and width; plain Enter submits, while Alt+Enter and Ctrl+J insert newlines.
The model handles `WindowSizeMsg` by resizing the editor. The program does not request Bubble
Tea's alternate screen.

Run the scripted check without opening a terminal, or run the focused Go tests:

```sh
go run ./spikes/s06-render --self-check
go test ./spikes/s06-render
```

The checks drive the same model updates as the app and verify that a Japanese chunk appears in the
live tail, completed text and a tool marker are emitted once and in order, the final unfinished
tail remains live and rewraps to the resized cell width, the view stays on the normal screen,
Japanese input reflows when resized, and the submit/newline keys have distinct behavior.

## Investigation notes

- The adopted TUI plan is [`docs/plans/first-tui.md`](../../docs/plans/first-tui.md). It selects
  Bubble Tea v2 and Bubbles textarea/viewport and calls for Enter submit plus Alt+Enter/Ctrl+J
  newline input.
- Bubble Tea v2.1.0 at
  `/home/agent/go/pkg/mod/charm.land/bubbletea/v2@v2.1.0/` defines `tea.Println` as unmanaged
  output above the program; its event loop routes the message to `renderer.insertAbove`.
  `cursed_renderer.go` resizes its screen buffer and marks it for redraw; normal-screen frame
  height is derived from the current view content. Bubbles v2.2.1 textarea defaults Enter to
  newline, so the spike overrides that binding and handles plain Enter in the app model.
- The former Henji renderer in
  `/home/agent/projects/henji-harness/v0/tui/scrollback_writer.ts` separates `committedRows` from
  `liveRows` and writes the live band on the normal screen. Its renderer also measures Japanese
  terminal cell width and recomputes the live band after resize. This spike tests the same
  committed/live split with Bubble Tea's `Println` path rather than porting the terminal writer.
- The San reference in `_refs/san/internal/app/model_scrollback.go` moves completed assistant
  blocks to `tea.Println` while retaining the active tail in the view. The sample uses a smaller,
  line-based model of that boundary; it does not reproduce San's Markdown, queue, or frame geometry
  handling.
- The old Henji acceptance record at
  `/home/agent/projects/henji-harness/docs/increments/increment-207.md` says the user accepted
  Shift+Enter in Ghostty → SSH → Debian → tmux on 2026-10-07 after the old TUI requested extended
  keys and the user's tmux server advertised `extkeys`. That is real acceptance evidence for the
  former Henji path. It does not verify this Bubble Tea spike or establish that the user's terminal
  configuration should be changed for hjh.

## Limits

Focused verification performed in this VM:

- `go test ./spikes/s06-render`, `go run ./spikes/s06-render --self-check`,
  `go vet ./spikes/s06-render`, and `go build -o /tmp/hjh-s06-render ./spikes/s06-render` pass.
- A Linux PTY smoke sent Kitty CSI-u Alt+Enter (`CSI 13;3u`), Ctrl+J (`CSI 106;5u`), and plain
  Enter (`CSI 13u`). The first two inserted separate draft lines and plain Enter submitted the
  multiline draft. A private tmux 3.8 server with an empty config advertised extended keys and
  passed the same multiline editing smoke; its pane reported extended-key mode. The test server
  was isolated under a private socket and then removed.
- A private tmux 3.8 normal-screen capture at 80x24 followed by a width-only resize to 44x24
  confirms that the live tail rewraps, but also shows the old-width tail retained in scrollback
  while the redrawn tail remains in the mutable view. This leaves a duplicate tail after resize.
  Moving startup text out of the view and removing status rows avoids stale copies of those rows,
  but does not solve the tail duplication. A height-only shrink from 80x24 to 80x18 promoted
  startup text to history without duplicating the live tail; shrinking to 80x4 moved older
  committed lines into history to fit the short screen.

The resize evidence is a reproduction, not a complete account of terminal behavior. Bubble Tea's
stock normal-screen renderer exposes no spike-level operation here that reconciles an old inline
frame with its new wrapped geometry in scrollback. The former Henji `scrollback_writer.ts` has a
separate owned-row clearing path and explicitly accounts for width reflow. Before I06 production
implementation, compare that independent-writer approach with Bubble Tea's normal renderer and
decide whether it is needed to keep a live tail from appearing twice after width resize.

This spike only covers plain line-based assistant text, one fixed tool marker, and a small sample;
variable tool state, Markdown, and large conversations are untested. The VM cannot verify a human
typing Alt+Enter through Ghostty → SSH → tmux with the user's actual setup. The old Henji
acceptance record above is real evidence for that former TUI path, not for Bubble Tea in hjh. No
user tmux configuration, environment, or process was changed.

To reproduce the private-tmux resize capture from the repository root (the unique socket is
removed at the end):

```sh
spike_socket=s06render-resize-check
tmux -L "$spike_socket" -f /dev/null new-session -d -s spike -x 80 -y 24 \
  'TERM=xterm-256color go run ./spikes/s06-render'
sleep 4
tmux -L "$spike_socket" capture-pane -p -t spike
tmux -L "$spike_socket" resize-window -t spike -x 44 -y 24
tmux -L "$spike_socket" capture-pane -p -S - -t spike
tmux -L "$spike_socket" kill-server
```
