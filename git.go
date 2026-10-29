package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var gitInitMu sync.Mutex

type GitEntry struct {
	Hash    string `json:"hash"`
	Time    string `json:"time"`
	Subject string `json:"subject"`
}

func gitBinary() (string, bool) {
	path, err := exec.LookPath("git")
	if err != nil {
		return "", false
	}
	return path, true
}

func runGit(dir string, args ...string) (string, error) {
	bin, ok := gitBinary()
	if !ok {
		return "", fmt.Errorf("git 不可用")
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=pojiagw",
		"GIT_AUTHOR_EMAIL=pojiagw@localhost",
		"GIT_COMMITTER_NAME=pojiagw",
		"GIT_COMMITTER_EMAIL=pojiagw@localhost",
		"GIT_TERMINAL_PROMPT=0",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func ensureGitRepo(dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return err
	}
	gitInitMu.Lock()
	defer gitInitMu.Unlock()

	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return nil
	}
	if _, err := runGit(dir, "init"); err != nil {
		return err
	}
	_, _ = runGit(dir, "config", "user.name", "pojiagw")
	_, _ = runGit(dir, "config", "user.email", "pojiagw@localhost")
	return nil
}

func gitCommitDir(dir string, message string) error {
	if err := ensureGitRepo(dir); err != nil {
		return err
	}
	if _, err := runGit(dir, "add", "-A"); err != nil {
		return err
	}
	if _, err := runGit(dir, "commit", "-m", message, "--allow-empty"); err != nil {
		return err
	}
	return nil
}

func gitLogDir(dir string, limit int) ([]GitEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if err := ensureGitRepo(dir); err != nil {
		return nil, err
	}
	out, err := runGit(dir, "log", "--pretty=format:%h%x09%ad%x09%s", "--date=iso", "-n", strconv.Itoa(limit))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "does not have any commits") {
			return []GitEntry{}, nil
		}
		return nil, err
	}
	entries := make([]GitEntry, 0, limit)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		entry := GitEntry{Hash: parts[0]}
		if len(parts) > 1 {
			entry.Time = parts[1]
		}
		if len(parts) > 2 {
			entry.Subject = parts[2]
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *Server) gitCommitSnapshot(name string) {
	cfg := s.store.Config()
	if !cfg.Server.SnapshotGit {
		return
	}
	if _, ok := gitBinary(); !ok {
		s.store.Warn("snapshot_git 已开启但找不到 git，跳过提交")
		return
	}
	dir := cfg.SnapshotDir(s.store.ConfigPath())
	go func() {
		if err := gitCommitDir(dir, "snapshot: "+name); err != nil {
			s.store.Warn("快照 git 提交失败：%v", err)
			return
		}
		s.store.Info("快照已提交到本地 git：%s", name)
	}()
}
