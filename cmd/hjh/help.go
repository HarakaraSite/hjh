package main

import "net"

const cliUsage = `Usage: hjh COMMAND [options]

  serve       Start the Core HTTP server
  core        List, inspect or stop Cores (list/status/stop)
  run         Execute a task or resume a Session (--connect URL uses a Core)
  history     Read history locally or via --connect
  sessions    List saved Sessions (human IDs; --json writes full IDs)
  inspect     Read one execution's records and configuration
  roles       List roles
  providers   List providers
  models      List models
  auth        Authentication status or ChatGPT login

  --version   Show executable version and build information
  --help      Show this help

Use hjh COMMAND --help for options. Start the TUI with hjh-tui.
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
