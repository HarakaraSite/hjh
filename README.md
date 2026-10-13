# henji-harness(hjh)

[日本語](README.ja.md)

**hjh** is short for **henji-harness**. *Henji* (返事) means "reply" in Japanese.

A terminal AI agent for working with files, commands, and web sources.
Use it interactively or from the command line, keep your session history, and
delegate work to child agents.

Built in Go, hjh embeds [go-quickjs](https://github.com/go-quickjs/go-quickjs),
a pure Go implementation of QuickJS-NG, to run JavaScript tools, hooks, and
`run_js` scripts. No Node.js or Deno installation is needed for JavaScript execution.

This project is still under development. Six binary variants are available;
runtime testing has been performed on Linux/amd64.

## Features

- Interactive TUI and command-line tasks, with resumable sessions.
- OpenRouter, OpenCode Go, OpenAI API, and ChatGPT OAuth connections.
- File and command tools, web fetching, and Exa web search.
- Child agents with parallel execution, progress reports, and follow-up instructions.
- Editable JavaScript tools and hooks, workspace instructions, and skills.

## Get started

Build from source with Go 1.27 or later; dependencies are listed in [go.mod](go.mod).
Content search requires ripgrep (`rg`); Git inspection requires Git.

```sh
git clone https://forge.harakara.site/littleisland/hjh.git
cd hjh
go build -o dist/hjh ./cmd/hjh
go build -o dist/hjh-tui ./cmd/hjh-tui
./dist/hjh-tui
```

The TUI starts a local Core when needed and reuses an existing one.
Keep `hjh` and `hjh-tui` in the same directory.

In the TUI, use `/login` to configure credentials, then `/provider` and `/model`
to choose a connection and model. OpenRouter is the default connection.
Type a request and press Enter to send it.

| Command | Action |
| --- | --- |
| `/sessions` | Open a saved session |
| `/new` | Start a new conversation |
| `/effort` | Choose reasoning effort |
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

To run through an existing Core and open the same session in the TUI:

```sh
./dist/hjh run --connect http://127.0.0.1:8765 "Summarize README.md"
./dist/hjh-tui --session SESSION_ID
```

Use the session ID printed by `run`. The default Core endpoint is
`http://127.0.0.1:8765`. Run `./dist/hjh --help` for the full CLI command list.

## Configuration

Workspace configuration and editable tools live in `.hjh/extensions/`.
Changes to tools, hooks, and role settings take effect on the next run.
History is stored locally in `.hjh/history.sqlite3`.

Credentials are stored in `~/.config/hjh/credentials/` on Linux, or under
`$XDG_CONFIG_HOME/hjh/credentials/` when set. API keys stay outside workspace
configuration and Git history.

Add workspace instructions in `AGENTS.md` and skills in
`.hjh/skills/<name>/SKILL.md`. Skill descriptions are loaded at run start;
their full instructions are read when needed. Exa search can use `EXA_API_KEY`.

- [Provider configuration and authentication](docs/design/providers.md)
- [Tools, roles, and hooks](docs/design/extensions.md) (Japanese)
- [Developer notes and implementation history](docs/development.md) (Japanese)

The English README is the source of truth; [README.ja.md](README.ja.md) is its Japanese translation.

## License

[MIT](LICENSE). Third-party dependencies retain their own licenses.
