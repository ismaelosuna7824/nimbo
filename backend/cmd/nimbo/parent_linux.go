package main

import "golang.org/x/sys/unix"

// setParentDeathSignal asks the kernel to send SIGTERM when the parent dies,
// covering parents that leak the stdin pipe to other processes.
func setParentDeathSignal() {
	unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(unix.SIGTERM), 0, 0, 0)
}
