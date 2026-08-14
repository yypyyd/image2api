//go:build linux

package oreate

import (
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"github.com/chromedp/chromedp"
)

func browserIsolationOptions() []chromedp.ExecAllocatorOption {
	if os.Geteuid() != 0 {
		return nil
	}
	chromeUser, err := user.Lookup("chrome")
	if err != nil {
		return nil
	}
	uid, uidErr := strconv.ParseUint(chromeUser.Uid, 10, 32)
	gid, gidErr := strconv.ParseUint(chromeUser.Gid, 10, 32)
	if uidErr != nil || gidErr != nil {
		return nil
	}
	credential := &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	return []chromedp.ExecAllocatorOption{
		chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
			for _, arg := range cmd.Args {
				if profileDir, ok := strings.CutPrefix(arg, "--user-data-dir="); ok {
					_ = os.Chown(profileDir, int(uid), int(gid))
					break
				}
			}
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Pdeathsig:  syscall.SIGKILL,
				Credential: credential,
			}
		}),
	}
}
