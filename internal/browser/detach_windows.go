//go:build windows

package browser

import (
	"os/exec"
	"syscall"
)

// detach starts the browser in its own process group so that Ctrl+C in the
// FritzFlow console does not also close the browser.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
