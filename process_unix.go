//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// sysProcAttrSetpgid 让浏览器进程自成一个进程组，
// 这样退出时能整组杀掉它派生的渲染/GPU 子进程。
func sysProcAttrSetpgid() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree 杀掉整个进程组。
// Chromium 会派生大量子进程，只杀主进程会留下一整组孤儿进程
// 继续吃内存并占着 profile 目录（下次启动会误判「浏览器已在运行」）。
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	// 负号 = 整个进程组
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}

// killProfileProcesses 按 user-data-dir 扫 /proc 清掉残留浏览器进程。
// 必须存在：snap 版 Chromium 由 snapd 以独立 systemd scope 启动，
// crashpad / zygote 等子进程会自成进程组，杀进程组兜不住它们。
// 返回被清理的进程数。
func killProfileProcesses(profileDir string) int {
	base := filepath.Base(profileDir)
	if base == "" || base == "." || base == "/" {
		return 0
	}
	marker := "--user-data-dir="
	self := os.Getpid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	killed := 0
	for _, e := range entries {
		pid, convErr := strconv.Atoi(e.Name())
		if convErr != nil || pid == self {
			continue
		}
		raw, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if readErr != nil {
			continue
		}
		hit := false
		for _, arg := range strings.Split(string(raw), "\x00") {
			if strings.HasPrefix(arg, marker) && strings.Contains(arg, base) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err == nil {
			killed++
		}
	}
	return killed
}
