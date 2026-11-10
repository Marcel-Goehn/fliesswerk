package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/Marcel-Goehn/kraftwerk/internal/runlog"
)

// watchCtrlC handles a person stopping the Run. The first Ctrl-C closes
// drain, so nothing new starts; the second ends ctx, which kills every
// Step. After that, Ctrl-C has its usual effect again, in case the kill
// itself hangs. stop ends the watching.
func watchCtrlC(log *runlog.Log) (ctx context.Context, drain <-chan struct{}, stop func()) {
	ctx, kill := context.WithCancel(context.Background())
	draining := make(chan struct{})
	presses := make(chan os.Signal, 2)
	signal.Notify(presses, os.Interrupt)
	done := make(chan struct{})
	go func() {
		defer signal.Stop(presses)
		select {
		case <-presses:
		case <-done:
			return
		}
		close(draining)
		log.Write(runlog.Line{Kind: runlog.Warning, Text: "Ctrl-C: the Tickets in flight finish and nothing new starts. Ctrl-C again to kill them."})
		select {
		case <-presses:
		case <-done:
			return
		}
		log.Write(runlog.Line{Kind: runlog.Warning, Text: "Ctrl-C again: killing every Step."})
		kill()
	}()
	return ctx, draining, func() {
		close(done)
		kill()
	}
}
