//go:build windows

package sandbox

import "os"

// interrupt stops a process; Windows has no SIGINT for child processes
// without a console, so it is killed.
func interrupt(p *os.Process) error { return p.Kill() }
