package main

import (
	"context"
	"io"
)

// withParentExit returns a context that is canceled once r reaches EOF or
// fails. The desktop app spawns `nimbo serve` with a piped stdin it never
// closes, so EOF means the parent process died and the daemon must not
// outlive it.
func withParentExit(ctx context.Context, r io.Reader) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		io.Copy(io.Discard, r)
		cancel()
	}()
	return ctx, cancel
}
