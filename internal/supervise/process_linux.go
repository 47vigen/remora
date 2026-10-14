package supervise

import (
	"errors"
	"os"
	"syscall"
)

// sysProcAttr puts each engine in its own process group, so signals reach any children it forks, and has the
// kernel kill it if remora dies.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
}

// signalProcess sends sig to the process group led by p. A group that is already gone is not an error.
func signalProcess(p *os.Process, sig syscall.Signal) error {
	if err := syscall.Kill(-p.Pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
