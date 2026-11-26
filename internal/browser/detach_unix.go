//go:build !windows

package browser

import (
	"os/exec"
	"syscall"
)

// detach starts the browser in its own session so that Ctrl+C in the
// FritzFlow terminal does not also close the browser.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
