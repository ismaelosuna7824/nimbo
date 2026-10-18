package main

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestWithParentExitCancelsOnEOF(t *testing.T) {
	r, w := io.Pipe()
	ctx, cancel := withParentExit(context.Background(), r)
	defer cancel()

	if _, err := w.Write([]byte("ignored")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("context canceled while the pipe is still open")
	case <-time.After(50 * time.Millisecond):
	}

	w.Close()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context not canceled after the pipe closed")
	}
}
