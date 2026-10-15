package main

import "net"

const cliUsage = `Usage: hjh COMMAND [options]

Commands:
  serve       Start the Core HTTP server
  core        List, inspect or stop Cores (list/status/stop)
  run         Execute a task or resume a Session
  history     Read history
  sessions    List or delete saved Sessions (list/delete)
  inspect     Read one execution's records and configuration
  roles       List roles
  providers   List providers
  models      List models
  auth        Authentication status or ChatGPT login (status/login)

Options for serve, run, inspect, roles, providers, models, auth:
  --workspace PATH             workspace directory (default: current directory)
  --db PATH                    history database (default: WORKSPACE/.hjh/history.sqlite3)
  --config PATH                extension config JSON (default: WORKSPACE/.hjh/extensions/config.json)
  --credential-file PATH       override API key file for the selected credential profile
  --credential-state-dir PATH  credential storage root (default: XDG_CONFIG_HOME/hjh or ~/.config/hjh)
  --model-favorites-dir PATH   favorite state directory (default: XDG_STATE_HOME/hjh/model-favorites)
  --session ID                 Session ID or unique case-insensitive prefix to resume (8 characters by default)
  --model ID                   model ID on the selected connection
  --provider ID                provider ID from external config
  --auth-profile NAME          credential profile reference
  --role NAME                  role for the next run
  --effort LEVEL               reasoning effort (default: auto)
  --max-steps N                model step limit; 0 is unlimited
  --from-turn N                first complete conversation turn (inclusive)
  --stream                     display assistant text on stdout as it arrives (run)
  --connect URL                send the task to an existing Core HTTP endpoint (run)
  --listen ADDR                HTTP API listen address (serve, default: 127.0.0.1:8765)
  --execution ID               execution ID to inspect (inspect)
  --from-ordinal N             first execution record/message ordinal (inclusive) (inspect)
  --to-ordinal N               last execution record/message ordinal (inclusive) (inspect)

Options for core:
  --core ID_OR_PREFIX          Core ID or unique prefix
  --connect URL                Core HTTP endpoint
  --workspace PATH             filter local Core registrations by workspace
  --json                       output JSON

Options for history:
  --workspace PATH             workspace to select latest history from
  --db PATH                    history database (default: WORKSPACE/.hjh/history.sqlite3)
  --connect URL                read history from an existing Core URL
  --session ID                 Session ID or unique case-insensitive prefix (8 characters by default)
  --latest                     select the latest Session
  --view VIEW                  history view: session, canonical or detail
  --from-turn N                first turn to include (inclusive)
  --to-turn N                  last turn to include (inclusive)
  --json                       write adopted messages as JSON

Options for sessions:
  --workspace PATH             workspace directory
  --db PATH                    history database (default: WORKSPACE/.hjh/history.sqlite3)
  --json                       write full Session IDs as JSON (list only)

Global options:
  --version                    Show executable version and build information
  --help                       Show this help

Use hjh COMMAND --help for command usage. Start the TUI with hjh-tui.
`

func coreEndpoint(address net.Addr) string {
	host, port, err := net.SplitHostPort(address.String())
	if err != nil {
		return "http://" + address.String()
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	} else if host == "::" {
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
