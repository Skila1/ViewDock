//go:build linux || darwin || freebsd

package labs

import (
	"os"
	"os/exec"
	"syscall"
)

var errFinished = os.ErrProcessDone

// isolate puts the process in its own process group so signals aimed at the
// server (for example Ctrl+C in a terminal) do not reach it and a kill takes
// down any children with it.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// lowerPriority runs the broadcaster below normal playback priority.
func lowerPriority(pid int) {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, 10)
}

func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errFinished
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
