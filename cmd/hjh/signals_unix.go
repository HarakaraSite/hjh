//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func cliSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	signal.Ignore(syscall.SIGPIPE)
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return ctx, func() {
		cancel()
		signal.Reset(syscall.SIGPIPE)
	}
}
