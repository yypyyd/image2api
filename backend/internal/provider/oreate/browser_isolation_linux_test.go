//go:build linux

package oreate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestConfigureChromiumCommandOwnsProcessGroup(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/true")
	credential := &syscall.Credential{Uid: 123, Gid: 456}
	configureChromiumCommand(cmd, credential)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("Chromium command does not start in its own process group")
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("Pdeathsig = %v, want SIGKILL", cmd.SysProcAttr.Pdeathsig)
	}
	if cmd.SysProcAttr.Credential != credential {
		t.Fatal("Chromium command lost the unprivileged credential")
	}
	if cmd.Cancel == nil {
		t.Fatal("Chromium command has no process-group cancellation hook")
	}
	if !errors.Is(cmd.Cancel(), os.ErrProcessDone) {
		t.Fatal("cancelling an unstarted Chromium command should report process done")
	}
	if cmd.WaitDelay != chromiumShutdownWait {
		t.Fatalf("WaitDelay = %v, want %v", cmd.WaitDelay, chromiumShutdownWait)
	}
}
