//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// sysProcAttrSetpgid：Windows 上不启用独立进程组（用 Process.Kill 兜底）。
func sysProcAttrSetpgid() *syscall.SysProcAttr {
	return nil
}

// killProcessTree：Windows 上直接结束主进程；
// 浏览器子进程由 Chrome 自身在父进程退出时收尾。
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// killProfileProcesses：Windows 没有 /proc，按 profile 扫进程不可行，
// 交给 killProcessTree 处理（Chrome 退出时会收尾自己的子进程）。
func killProfileProcesses(profileDir string) int {
	return 0
}
