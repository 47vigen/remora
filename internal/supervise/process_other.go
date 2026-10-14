//go:build !linux

package supervise

import (
	"errors"
	"os"
	"syscall"
)

// sysProcAttr is a no-op outside Linux: the image runs on Linux only.
func sysProcAttr() *syscall.SysProcAttr { return nil }

// signalProcess sends sig to p. A process that is already gone is not an error.
func signalProcess(p *os.Process, sig syscall.Signal) error {
	if err := p.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
