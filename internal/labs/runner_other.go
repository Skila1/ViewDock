//go:build !linux && !darwin && !freebsd && !windows

package labs

import (
	"os"
	"os/exec"
)

var errFinished = os.ErrProcessDone

func isolate(*exec.Cmd) {}

func lowerPriority(int) {}

func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errFinished
	}
	return cmd.Process.Kill()
}
