# henji-harness(hjh)

[日本語](README.ja.md)

**hjh** is short for **henji-harness**. *Henji* (返事) means "reply" in Japanese.

A terminal AI agent for working with files, commands, and web sources.
Use it interactively or from the command line, keep your session history, and
delegate work to child agents.

Built in Go, hjh embeds [go-quickjs](https://github.com/go-quickjs/go-quickjs),
a pure Go implementation of QuickJS-NG, to run JavaScript tools, hooks, and
`run_js` scripts. No Node.js or Deno installation is needed for JavaScript execution.

This project is still under development. The build produces two executables,
`hjh` and `hjh-tui`; runtime testing has been performed on Linux/amd64.

## Features

- Interactive TUI and command-line tasks, with resumable sessions.
- OpenRouter, OpenCode Go, OpenAI API, and ChatGPT OAuth connections.
- File and command tools, web fetching, and Exa web search.
- Child agents with parallel execution, progress reports, and follow-up instructions.
- Editable JavaScript tools and hooks, roles, workspace instructions, and skills.

## Get started

Build from source with Go 1.27 or later; dependencies are listed in [go.mod](go.mod).
Content search uses ripgrep (`rg`) when installed, otherwise GNU grep.
Git inspection requires Git.

```sh
git clone https://forge.harakara.site/littleisland/hjh.git
cd hjh
go build -o dist/hjh ./cmd/hjh
go build -o dist/hjh-tui ./cmd/hjh-tui
./dist/hjh-tui
```

The TUI starts a local Core when needed and reuses an existing one.
Keep `hjh` and `hjh-tui` in the same directory.
Use `./dist/hjh-tui --continue` to resume the latest session in the same workspace.

In the TUI, use `/login` to configure credentials, then `/provider` and `/model`
to choose a connection and model. OpenRouter is the default connection.
Type a request and press Enter to send it.
Type `/` to browse commands; use arrows to select, Tab to complete, and Enter to choose.

| Command | Action |
| --- | --- |
| `/sessions` `/new` `/rename` | Open, start, or rename a session |
| `/resume` | Resume a session by ID or `latest`, or attach to its running execution |
| `/model` `/provider` `/effort` | Choose the model, connection, or reasoning effort for the next run |
| `/login` `/auth` | Set up credentials or show authentication status |
| `/roles` `/role` | List roles with their tools or choose the next role |
| `/history` `/executions` `/inspect` | Review the conversation and saved executions |
| `/inputs` `/tools` `/tool` | Review saved follow-up input and tool calls |
| `/context` `/recall` | Show the assembled context or reuse a stopped execution |
| `/detach` | Close the TUI while the Core keeps running |
| `/quit` | Stop the Core and close the TUI |
| `/help` | Show commands and keyboard shortcuts |

## Command line

After configuring credentials, run tasks and inspect their history:

```sh
./dist/hjh run "Summarize README.md" > summary.txt
./dist/hjh sessions
./dist/hjh run --session SESSION_ID "Continue the previous task"
./dist/hjh history --session SESSION_ID
```

The final answer goes to stdout; progress and session IDs go to stderr.
Use `--stream` for incremental output. Flags go before the task text.
Session IDs accept unique prefixes, normally eight characters.

To delete a saved session locally, stop the Core and any standalone runs first:

```sh
./dist/hjh core stop --connect http://127.0.0.1:8765
./dist/hjh sessions delete SESSION_ID
```

This removes the session, its execution history, and content and configuration
records that are no longer referenced. Freed database space is reusable; the
database file is not compacted automatically.

To run through an existing Core and open the same session in the TUI:

```sh
./dist/hjh run --connect http://127.0.0.1:8765 "Summarize README.md"
./dist/hjh-tui --session SESSION_ID
```

Use the session ID printed by `run`. The default Core endpoint is
`http://127.0.0.1:8765`. Beyond `run`, `history`, and `sessions`, the CLI provides
`auth`, `models`, `providers`, `roles`, `inspect`, and Core management with
`serve` and `core`. Run `./dist/hjh --help` for the full command and option list.

## Configuration

Workspace configuration and editable tools live in `.hjh/extensions/`.
Changes to tools, hooks, and role settings take effect on the next run.
History is stored locally in `.hjh/history.sqlite3`.
The TUI remembers your provider, model, and effort for new sessions in the same
workspace, including after a restart. Favorite models are kept in
`~/.local/state/hjh/model-favorites/`, or `$XDG_STATE_HOME/hjh/model-favorites/`
when set.

Credentials are stored in `~/.config/hjh/credentials/` on Linux, or under
`$XDG_CONFIG_HOME/hjh/credentials/` when set. API keys stay outside workspace
configuration and Git history.

To replace the built-in base instructions, create `~/.config/hjh/instructions/base.md`
on Linux (or `$XDG_CONFIG_HOME/hjh/instructions/base.md`). Role, tool, and workspace
instructions are still combined with it. Edits take effect on the next run.

Add workspace instructions in `AGENTS.md` and skills in
`.hjh/skills/<name>/SKILL.md`. Skill descriptions are loaded at run start;
their full instructions are read when needed. Exa search can use `EXA_API_KEY`.

- [Provider configuration and authentication](docs/design/providers.md)
- [Tools, roles, and hooks](docs/design/extensions.md) (Japanese)
- [Tool usage policy and per-tool help](docs/design/tool-usage-guidance.md) (Japanese):
  `tool-usage.md` overrides, `{help:true}`, and updating existing tool configurations without overwriting edits.
- [Developer notes and implementation history](docs/development.md) (Japanese)

The English README is the source of truth; [README.ja.md](README.ja.md) is its Japanese translation.

## License

[MIT](LICENSE). Third-party dependencies retain their own licenses.
