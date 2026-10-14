// Package corelaunch starts a local Core process for clients that need one.
// It uses only the public Core HTTP API and does not own Core runtime state.
package corelaunch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"hjh/internal/api"
	"hjh/internal/apiclient"
)

const (
	defaultEndpoint = "http://127.0.0.1:8765"
	probeTimeout    = time.Second
	probeInterval   = 100 * time.Millisecond
	tcpProbeTimeout = 200 * time.Millisecond
)

// Options configures the Core process when no Core is available at Endpoint.
// Empty paths keep the defaults owned by hjh serve.
type Options struct {
	Endpoint           string
	Binary             string
	Workspace          string
	DBPath             string
	ConfigPath         string
	CredentialFile     string
	CredentialStateDir string
	ModelFavoritesDir  string
}

// Ensure returns the Core already serving Endpoint or starts the configured
// hjh serve process and waits until its public Core endpoint is ready. A
// started process is independent of ctx and the caller's terminal lifetime.
func Ensure(ctx context.Context, options Options, progress io.Writer) (api.Core, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	client, err := apiclient.New(endpoint)
	if err != nil {
		return api.Core{}, err
	}
	defer client.Close()

	// Existing Core reuse is intentionally the first operation after parsing
	// the endpoint. In that case we do not inspect the binary or touch the
	// filesystem for launch logs.
	if core, _, probeErr := probeCore(ctx, client); probeErr == nil {
		writeProgress(progress, "Using the existing Core.\n")
		return core, nil
	}
	if err := ctx.Err(); err != nil {
		return api.Core{}, err
	}

	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return api.Core{}, fmt.Errorf("parse Core endpoint: %w", err)
	}
	if endpointURL.Scheme != "http" {
		return api.Core{}, fmt.Errorf("cannot start a local Core for non-HTTP endpoint %q", endpoint)
	}
	if endpointURL.Path != "" && endpointURL.Path != "/" || endpointURL.RawQuery != "" || endpointURL.Fragment != "" {
		return api.Core{}, fmt.Errorf("cannot derive a local Core listen address from endpoint %q", endpoint)
	}
	listen, err := listenAddress(endpointURL)
	if err != nil {
		return api.Core{}, err
	}

	workspace := options.Workspace
	if workspace == "" {
		workspace, err = os.Getwd()
		if err != nil {
			return api.Core{}, fmt.Errorf("resolve Core workspace: %w", err)
		}
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return api.Core{}, fmt.Errorf("resolve Core workspace: %w", err)
	}

	dbPath := options.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(workspace, ".hjh", "history.sqlite3")
	}
	effectiveDBPath, err := filepath.Abs(dbPath)
	if err != nil {
		return api.Core{}, fmt.Errorf("resolve Core database path: %w", err)
	}
	logPath := filepath.Join(filepath.Dir(effectiveDBPath), "core.log")

	binary, err := coreBinary(options.Binary)
	if err != nil {
		return api.Core{}, fmt.Errorf("resolve Core binary: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return api.Core{}, fmt.Errorf("create Core log directory for %s: %w", logPath, err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return api.Core{}, fmt.Errorf("open Core log %s: %w", logPath, err)
	}

	args := serveArgs(options, workspace, listen)
	cmd := exec.Command(binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	writeProgress(progress, fmt.Sprintf("Starting Core. Log: %s\n", logPath))
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return api.Core{}, fmt.Errorf("start Core process: %w (Core log: %s)", err, logPath)
	}
	// The child inherited its own descriptors from Start; the UI must not hold
	// the log open for the lifetime of the process.
	_ = logFile.Close()

	processDone := make(chan error, 1)
	go func() { processDone <- cmd.Wait() }()

	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()
	childExited := false
	var childExitErr error
	waitingForListeningCore := false
	for {
		if err := ctx.Err(); err != nil {
			if childExited {
				writeProgress(progress, fmt.Sprintf("Core readiness wait cancelled. Log: %s\n", logPath))
			} else {
				writeProgress(progress, fmt.Sprintf("Core startup wait cancelled; the Core process continues. Log: %s\n", logPath))
			}
			return api.Core{}, fmt.Errorf("wait for Core startup: %w (Core log: %s)", err, logPath)
		}
		core, receivedHTTPResponse, probeErr := probeCore(ctx, client)
		if probeErr == nil {
			writeProgress(progress, "Core is ready.\n")
			return core, nil
		}
		if err := ctx.Err(); err != nil {
			if childExited {
				writeProgress(progress, fmt.Sprintf("Core readiness wait cancelled. Log: %s\n", logPath))
			} else {
				writeProgress(progress, fmt.Sprintf("Core startup wait cancelled; the Core process continues. Log: %s\n", logPath))
			}
			return api.Core{}, fmt.Errorf("wait for Core startup: %w (Core log: %s)", err, logPath)
		}

		if childExited {
			if receivedHTTPResponse {
				writeProgress(progress, fmt.Sprintf("Core exited before readiness. Log: %s\n", logPath))
				return api.Core{}, fmt.Errorf("core process exited before becoming ready (%s); inspect %s", describeExit(childExitErr), logPath)
			}
			listening := tcpListening(ctx, listen)
			if err := ctx.Err(); err != nil {
				writeProgress(progress, fmt.Sprintf("Core readiness wait cancelled. Log: %s\n", logPath))
				return api.Core{}, fmt.Errorf("wait for Core startup: %w (Core log: %s)", err, logPath)
			}
			if !listening {
				writeProgress(progress, fmt.Sprintf("Core exited before readiness. Log: %s\n", logPath))
				return api.Core{}, fmt.Errorf("core process exited before becoming ready (%s); inspect %s", describeExit(childExitErr), logPath)
			}
			if !waitingForListeningCore {
				writeProgress(progress, fmt.Sprintf("Core process exited; waiting for the listening Core to become ready. Log: %s\n", logPath))
				waitingForListeningCore = true
			}
			select {
			case <-ctx.Done():
			case <-ticker.C:
			}
			continue
		}

		select {
		case exitErr := <-processDone:
			// A concurrent startup may own the TCP listener while it still
			// initializes its database. Keep probing until it serves Core or
			// the listening endpoint proves unavailable or non-Core.
			childExited = true
			childExitErr = exitErr
		case <-ctx.Done():
			writeProgress(progress, fmt.Sprintf("Core startup wait cancelled; the Core process continues. Log: %s\n", logPath))
			return api.Core{}, fmt.Errorf("wait for Core startup: %w (Core log: %s)", ctx.Err(), logPath)
		case <-ticker.C:
		}
	}
}

func describeExit(err error) string {
	if err == nil {
		return "exit status 0"
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("exit status %d", exitErr.ExitCode())
	}
	return fmt.Sprintf("wait error: %v", err)
}

func probeCore(ctx context.Context, client *apiclient.Client) (api.Core, bool, error) {
	var receivedHTTPResponse atomic.Bool
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { receivedHTTPResponse.Store(true) }}
	tracedCtx := httptrace.WithClientTrace(ctx, trace)
	probeCtx, cancel := context.WithTimeout(tracedCtx, probeTimeout)
	defer cancel()
	core, err := client.Core(probeCtx)
	return core, receivedHTTPResponse.Load(), err
}

func tcpListening(ctx context.Context, address string) bool {
	dialCtx, cancel := context.WithTimeout(ctx, tcpProbeTimeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func listenAddress(endpoint *url.URL) (string, error) {
	host := endpoint.Hostname()
	if host == "" {
		return "", fmt.Errorf("core endpoint must include a host")
	}
	port := endpoint.Port()
	if port == "" {
		port = "80"
	}
	return net.JoinHostPort(host, port), nil
}

func coreBinary(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(executable), "hjh"), nil
}

func serveArgs(options Options, workspace, listen string) []string {
	args := []string{"serve", "--listen", listen, "--workspace", workspace}
	for _, value := range []struct {
		name  string
		value string
	}{
		{name: "--db", value: options.DBPath},
		{name: "--config", value: options.ConfigPath},
		{name: "--credential-file", value: options.CredentialFile},
		{name: "--credential-state-dir", value: options.CredentialStateDir},
		{name: "--model-favorites-dir", value: options.ModelFavoritesDir},
	} {
		if value.value != "" {
			args = append(args, value.name, value.value)
		}
	}
	return args
}

func writeProgress(progress io.Writer, message string) {
	if progress != nil {
		_, _ = io.WriteString(progress, message)
	}
}
