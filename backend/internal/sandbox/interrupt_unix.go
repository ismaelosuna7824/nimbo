//go:build !windows

package sandbox

import "os"

// interrupt asks a process to stop gracefully (SIGINT).
func interrupt(p *os.Process) error { return p.Signal(os.Interrupt) }
