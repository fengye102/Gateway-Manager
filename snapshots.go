package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type SnapshotMeta struct {
	Name      string `json:"name"`
	Time      int64  `json:"time"`
	Size      int64  `json:"size"`
	Note      string `json:"note,omitempty"`
	Upstreams int    `json:"upstreams"`
	Profiles  int    `json:"profiles"`
	Rules     int    `json:"rules"`
}

var snapshotNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+.json$`)

func (c *Config) SnapshotDir(cfgPath string) string {
	if strings.TrimSpace(c.Server.SnapshotDir) != "" {
		return c.Server.SnapshotDir
	}
	if cfgPath == "" {
		return "snapshots"
	}
	return filepath.Join(filepath.Dir(cfgPath), "snapshots")
}

func sanitizeNote(note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return ""
	}
	out := make([]rune, 0, 24)
	for _, r := range note {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-' || r == '_':
			out = append(out, r)
		default:
			if len(out) > 0 && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
		}
		if len(out) >= 24 {
			break
		}
	}
	cleaned := strings.Trim(string(out), "-")
	if cleaned == "" {
		return ""
	}
	return cleaned
}

func listSnapshots(dir string) []SnapshotMeta {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]SnapshotMeta, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !snapshotNamePattern.MatchString(entry.Name()) {
			continue
		}
		full := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		meta := SnapshotMeta{Name: entry.Name(), Size: info.Size(), Time: info.ModTime().Unix()}
		if raw, err := os.ReadFile(full); err == nil {
			cfg := DefaultConfig()
			if json.Unmarshal(raw, cfg) == nil {
				meta.Upstreams = len(cfg.Upstreams)
				meta.Profiles = len(cfg.Profiles)
				meta.Rules = len(cfg.Rules)
			}
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time > out[j].Time
		}
		return out[i].Name > out[j].Name
	})
	return out
}

func createSnapshot(dir string, cfg *Config, note string, limit int) (SnapshotMeta, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SnapshotMeta{}, err
	}
	normalizeConfig(cfg)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return SnapshotMeta{}, err
	}
	out = append(out, '\n')

	base := time.Now().Format("20060102-150405")
	if suffix := sanitizeNote(note); suffix != "" {
		base += "-" + suffix
	}
	name := base + ".json"
	full := filepath.Join(dir, name)
	for i := 2; ; i++ {
		if _, statErr := os.Stat(full); os.IsNotExist(statErr) {
			break
		}
		name = fmt.Sprintf("%s-%d.json", base, i)
		full = filepath.Join(dir, name)
	}
	temp := full + ".tmp"
	if err := os.WriteFile(temp, out, 0o644); err != nil {
		return SnapshotMeta{}, err
	}
	if err := os.Rename(temp, full); err != nil {
		return SnapshotMeta{}, err
	}
	pruneSnapshots(dir, limit)
	return SnapshotMeta{
		Name:      name,
		Time:      time.Now().Unix(),
		Size:      int64(len(out)),
		Note:      strings.TrimSpace(note),
		Upstreams: len(cfg.Upstreams),
		Profiles:  len(cfg.Profiles),
		Rules:     len(cfg.Rules),
	}, nil
}

func pruneSnapshots(dir string, limit int) {
	if limit <= 0 {
		limit = 20
	}
	list := listSnapshots(dir)
	if len(list) <= limit {
		return
	}
	for _, meta := range list[limit:] {
		_ = os.Remove(filepath.Join(dir, meta.Name))
	}
}

func readSnapshot(dir string, name string) (*Config, error) {
	if !snapshotNamePattern.MatchString(name) {
		return nil, fmt.Errorf("非法的快照名")
	}
	return LoadConfig(filepath.Join(dir, name))
}

func deleteSnapshot(dir string, name string) error {
	if !snapshotNamePattern.MatchString(name) {
		return fmt.Errorf("非法的快照名")
	}
	return os.Remove(filepath.Join(dir, name))
}

func (s *Server) autoSnapshot(note string) {
	cfg := s.store.Config()
	if !cfg.Server.SnapshotBeforeSave {
		return
	}
	if s.store.ConfigPath() == "" {
		return
	}
	dir := cfg.SnapshotDir(s.store.ConfigPath())
	meta, err := createSnapshot(dir, cfg, note, cfg.Server.SnapshotLimit)
	if err != nil {
		s.store.Warn("创建配置快照失败：%v", err)
		return
	}
	s.store.Info("已创建配置快照：%s", meta.Name)
	s.gitCommitSnapshot(meta.Name)
}
