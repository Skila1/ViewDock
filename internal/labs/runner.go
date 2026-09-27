package labs

import (
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Process is a running broadcaster process.
type Process interface {
	// Stdout carries FFmpeg -progress key=value lines.
	Stdout() io.Reader
	// Stderr carries FFmpeg diagnostics.
	Stderr() io.Reader
	// Wait blocks until the process exits.
	Wait() error
	// Stop asks the process to finish, killing it after grace.
	Stop(grace time.Duration) error
}

// Runner starts processes. ExecRunner is the production implementation.
type Runner interface {
	Start(bin string, args []string) (Process, error)
}

// ExecRunner runs FFmpeg as a separate OS process, detached from any request
// context, in its own process group at reduced scheduling priority where the
// platform supports it.
type ExecRunner struct{}

type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader

	once    sync.Once
	done    chan struct{}
	waitErr error
}

func (ExecRunner) Start(bin string, args []string) (Process, error) {
	cmd := exec.Command(bin, args...)
	isolate(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	lowerPriority(cmd.Process.Pid)
	return &execProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr, done: make(chan struct{})}, nil
}

func (p *execProcess) Stdout() io.Reader { return p.stdout }
func (p *execProcess) Stderr() io.Reader { return p.stderr }

// Wait may be called concurrently; the process is reaped exactly once.
func (p *execProcess) Wait() error {
	p.once.Do(func() {
		p.waitErr = p.cmd.Wait()
		close(p.done)
	})
	<-p.done
	return p.waitErr
}

func (p *execProcess) Stop(grace time.Duration) error {
	select {
	case <-p.done:
		return nil
	default:
	}
	_, _ = io.WriteString(p.stdin, "q\n")
	_ = p.stdin.Close()
	select {
	case <-p.done:
		return nil
	case <-time.After(grace):
	}
	if err := kill(p.cmd); err != nil && !errors.Is(err, errFinished) {
		return err
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		return errors.New("broadcaster process did not exit after kill")
	}
	return nil
}
