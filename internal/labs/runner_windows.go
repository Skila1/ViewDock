//go:build windows

package labs

import (
	"os"
	"os/exec"
	"syscall"
)

var errFinished = os.ErrProcessDone

const belowNormalPriorityClass = 0x00004000

// isolate starts the process in its own process group at below-normal
// priority so console signals aimed at the server do not reach it.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | belowNormalPriorityClass}
}

func lowerPriority(int) {}

func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errFinished
	}
	return cmd.Process.Kill()
}
