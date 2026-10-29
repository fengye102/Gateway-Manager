package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) adminAuthorized(w http.ResponseWriter, r *http.Request) bool {
	token := s.store.Config().Server.AdminToken
	if token == "" {
		return true
	}
	got := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if got == "" {
		got = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if got == token {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "admin token required"})
	return false
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Config()
	if !cfg.Server.EnableAdminUI {
		proxyError(w, http.StatusNotFound, "admin ui disabled")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, adminIndexHTML)
}

func (s *Server) readJSONBody(r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, dst)
}

func (s *Server) saveConfigAndReload(next *Config) error {
	if err := s.store.SetConfig(next); err != nil {
		return err
	}
	s.refreshClient()
	return nil
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request, action string) {
	if !s.adminAuthorized(w, r) {
		return
	}
	switch action {
	case "status":
		writeJSON(w, http.StatusOK, s.store.StatusPayload())

	case "logs":
		limit := 200
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"logs": s.store.Logs(limit)})

	case "config":
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, s.store.Config())
			return
		}
		if r.Method != http.MethodPost {
			proxyError(w, http.StatusMethodNotAllowed, "GET or POST only")
			return
		}
		next := DefaultConfig()
		if err := s.readJSONBody(r, next); err != nil {
			proxyError(w, http.StatusBadRequest, "配置不是合法 JSON："+err.Error())
			return
		}
		s.autoSnapshot("before-config-update")
		if err := s.saveConfigAndReload(next); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.store.Info("配置已通过管理接口更新并落盘")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "reload":
		cfg, err := LoadConfig(s.store.ConfigPath())
		if err != nil {
			proxyError(w, http.StatusBadRequest, "重新加载失败："+err.Error())
			return
		}
		if err := s.saveConfigAndReload(cfg); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.store.Info("配置已从磁盘重新加载")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "check":
		go s.checkAllUpstreams(context.Background())
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "rules":
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"rules": s.store.Config().Rules})
			return
		}
		var payload struct {
			Rules []Rule `json:"rules"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, "规则不是合法 JSON："+err.Error())
			return
		}
		s.autoSnapshot("before-rules-update")
		next := cloneConfig(s.store.Config())
		next.Rules = payload.Rules
		if err := s.saveConfigAndReload(next); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.store.Info("规则已更新：共 %d 个节点", len(payload.Rules))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(payload.Rules)})

	case "rules/stats":
		if r.Method == http.MethodDelete {
			s.store.ResetRuleStats()
			s.store.MarkStateDirty()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"stats": s.store.RuleStatsSnapshot()})

	case "rules/dsl":
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"text": rulesToDSL(s.store.Config().Rules)})
			return
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		rules, err := parseRulesDSL(payload.Text)
		if err != nil {
			proxyError(w, http.StatusBadRequest, "解析失败："+err.Error())
			return
		}
		s.autoSnapshot("before-rules-dsl")
		next := cloneConfig(s.store.Config())
		next.Rules = rules
		if err := s.saveConfigAndReload(next); err != nil {
			proxyError(w, http.StatusBadRequest, "应用失败："+err.Error())
			return
		}
		s.store.Info("规则书已应用：共 %d 个节点", len(rules))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(rules)})

	case "rules/dsl/preview":
		var payload struct {
			Text string `json:"text"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		rules, err := parseRulesDSL(payload.Text)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(rules), "rules": rules, "text": rulesToDSL(rules)})

	case "rules/test":
		var payload struct {
			Model string         `json:"model"`
			Path  string         `json:"path"`
			Body  map[string]any `json:"body"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, "试算请求不是合法 JSON："+err.Error())
			return
		}
		cfg := s.store.Config()
		bodyText := ""
		if payload.Body != nil {
			raw, _ := json.Marshal(payload.Body)
			bodyText = string(raw)
		}
		path := payload.Path
		if path == "" {
			path = "/v1/chat/completions"
		}
		kind := epChat
		switch canonicalAPIPath(path) {
		case "/v1/responses", "/v1/responses/compact":
			kind = epResponses
		case "/v1/messages":
			kind = epMessages
		}
		outcome := evaluateRules(cfg.Rules, cfg.Profiles, ruleContext{
			Model: payload.Model, Path: canonicalAPIPath(path), Body: bodyText,
		})
		preview := ""
		profileName := ""
		if payload.Body != nil && !outcome.Denied {
			body := cloneJSONObject(payload.Body)
			for k, v := range outcome.Params {
				body[k] = v
			}
			if outcome.hasInstruction() {
				_ = injectStrategy(body, kind, outcome.Instruction)
			} else if p := selectProfile(cfg.Profiles, "", payload.Model); p != nil {
				profileName = p.Name
				_ = injectStrategy(body, kind, p.Instruction)
			}
			raw, _ := json.MarshalIndent(body, "", "  ")
			preview = string(raw)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"matched":      outcome.Matched,
			"skipped":      outcome.Skipped,
			"denied":       outcome.Denied,
			"status":       outcome.Status,
			"message":      outcome.Message,
			"instruction":  outcome.Instruction,
			"model":        outcome.ModelOverride,
			"params":       outcome.Params,
			"headers":      outcome.Headers,
			"profile":      profileName,
			"preview_body": preview,
		})

	case "templates":
		cfg := s.store.Config()
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"templates": allTemplates(cfg.Templates)})
			return
		}
		var t ProfileTemplate
		if err := s.readJSONBody(r, &t); err != nil {
			proxyError(w, http.StatusBadRequest, "模板不是合法 JSON："+err.Error())
			return
		}
		custom, err := upsertTemplate(cfg.Templates, t)
		if err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		next := cloneConfig(cfg)
		next.Templates = custom
		if err := s.saveConfigAndReload(next); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.store.Info("模板已保存：%s", t.Name)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "templates/delete":
		var payload struct {
			ID string `json:"id"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		cfg := s.store.Config()
		next := cloneConfig(cfg)
		next.Templates = removeTemplate(cfg.Templates, payload.ID)
		if err := s.saveConfigAndReload(next); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "templates/apply":
		var payload struct {
			TemplateID string `json:"template_id"`
			ProfileID  string `json:"profile_id"`
			Mode       string `json:"mode"`
			Name       string `json:"name"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		cfg := s.store.Config()
		s.autoSnapshot("before-template-apply")
		tpl, ok := findTemplate(cfg.Templates, payload.TemplateID)
		if !ok {
			proxyError(w, http.StatusNotFound, "模板不存在："+payload.TemplateID)
			return
		}
		next := cloneConfig(cfg)
		target := -1
		for i := range next.Profiles {
			if next.Profiles[i].ID == payload.ProfileID {
				target = i
				break
			}
		}
		switch payload.Mode {
		case "overwrite":
			if target < 0 {
				proxyError(w, http.StatusBadRequest, "目标策略不存在："+payload.ProfileID)
				return
			}
			next.Profiles[target].Instruction = tpl.Instruction
			if payload.Name != "" {
				next.Profiles[target].Name = payload.Name
			}
		default:
			id := strings.TrimSpace(payload.ProfileID)
			if id == "" {
				id = "p-" + tpl.ID
			}
			for _, p := range next.Profiles {
				if p.ID == id {
					proxyError(w, http.StatusBadRequest, "策略 id 已存在："+id)
					return
				}
			}
			name := payload.Name
			if name == "" {
				name = tpl.Name
			}
			next.Profiles = append(next.Profiles, Profile{
				ID:          id,
				Name:        name,
				Instruction: tpl.Instruction,
				Enabled:     true,
			})
		}
		if err := s.saveConfigAndReload(next); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.store.Info("模板「%s」已应用到策略", tpl.Name)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "experiments":
		cfg := s.store.Config()
		writeJSON(w, http.StatusOK, map[string]any{
			"experiments": cfg.Experiments,
			"stats":       s.store.ExperimentStats(),
		})

	case "federation":
		snapshot := s.federationSnapshot(r.Context())
		writeJSON(w, http.StatusOK, snapshot)

	case "rules/batch":
		var payload struct {
			Requests []struct {
				Model    string         `json:"model"`
				Path     string         `json:"path"`
				Body     map[string]any `json:"body"`
				BodyText string         `json:"body_text"`
			} `json:"requests"`
			From  string `json:"from"`
			Limit int    `json:"limit"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		type batchItem struct {
			Model   string   `json:"model"`
			Path    string   `json:"path"`
			Denied  bool     `json:"denied"`
			Status  int      `json:"status"`
			Matched []string `json:"matched"`
			Skipped []string `json:"skipped"`
			Source  string   `json:"source"`
			Body    string   `json:"body,omitempty"`
		}
		cfg := s.store.Config()
		limit := payload.Limit
		if limit <= 0 || limit > 500 {
			limit = 100
		}

		rawItems := make([]struct {
			Model  string
			Path   string
			Body   string
			Source string
		}, 0, limit)

		from := strings.ToLower(strings.TrimSpace(payload.From))
		if from == "samples" || from == "replay" || from == "both" {
			if from == "samples" || from == "both" {
				for _, e := range s.store.Samples() {
					if len(rawItems) >= limit {
						break
					}
					rawItems = append(rawItems, struct{ Model, Path, Body, Source string }{e.Model, e.Path, e.Body, "sample"})
				}
			}
			if from == "replay" || from == "both" {
				for _, e := range s.store.ReplayEntries() {
					if len(rawItems) >= limit {
						break
					}
					rawItems = append(rawItems, struct{ Model, Path, Body, Source string }{e.Model, e.Path, e.Body, "replay"})
				}
			}
		}
		for _, item := range payload.Requests {
			if len(rawItems) >= limit {
				break
			}
			bodyText := item.BodyText
			if bodyText == "" && item.Body != nil {
				if raw, err := json.Marshal(item.Body); err == nil {
					bodyText = string(raw)
				}
			}
			path := item.Path
			if path == "" {
				path = "/v1/chat/completions"
			}
			rawItems = append(rawItems, struct{ Model, Path, Body, Source string }{item.Model, path, bodyText, "inline"})
		}

		items := make([]batchItem, 0, len(rawItems))
		ruleTally := map[string]int{}
		deniedCount := 0
		for _, raw := range rawItems {
			outcome := evaluateRules(cfg.Rules, cfg.Profiles, ruleContext{
				Model: raw.Model,
				Path:  canonicalAPIPath(raw.Path),
				Body:  raw.Body,
			})
			if outcome.Denied {
				deniedCount++
			}
			for _, id := range outcome.Matched {
				ruleTally[id]++
			}
			items = append(items, batchItem{
				Model:   raw.Model,
				Path:    raw.Path,
				Denied:  outcome.Denied,
				Status:  outcome.Status,
				Matched: outcome.Matched,
				Skipped: outcome.Skipped,
				Source:  raw.Source,
				Body:    truncateForReport(raw.Body, 160),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"total":        len(items),
			"denied":       deniedCount,
			"allowed":      len(items) - deniedCount,
			"rule_matches": ruleTally,
			"items":        items,
		})

	case "samples":
		if r.Method == http.MethodDelete {
			s.store.ClearSamples()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"entries": s.store.Samples(),
			"every":   s.store.Config().Server.SampleEvery,
			"limit":   s.store.Config().Server.SampleLimit,
		})

	case "samples/test":
		var payload struct {
			ID string `json:"id"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		entry, ok := s.store.FindSample(payload.ID)
		if !ok {
			proxyError(w, http.StatusNotFound, "采样记录不存在："+payload.ID)
			return
		}
		writeJSON(w, http.StatusOK, s.dryRunEntry(entry))

	case "replay":
		if r.Method == http.MethodDelete {
			s.store.ClearReplay()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": s.store.ReplayEntries()})

	case "replay/test":
		var payload struct {
			ID string `json:"id"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		entry, ok := s.store.FindReplay(payload.ID)
		if !ok {
			proxyError(w, http.StatusNotFound, "回放记录不存在："+payload.ID)
			return
		}
		writeJSON(w, http.StatusOK, s.dryRunEntry(entry))

	case "snapshots":
		cfg := s.store.Config()
		writeJSON(w, http.StatusOK, map[string]any{
			"dir":       cfg.SnapshotDir(s.store.ConfigPath()),
			"limit":     cfg.Server.SnapshotLimit,
			"snapshots": listSnapshots(cfg.SnapshotDir(s.store.ConfigPath())),
		})

	case "snapshots/git/log":
		cfg := s.store.Config()
		dir := cfg.SnapshotDir(s.store.ConfigPath())
		if _, ok := gitBinary(); !ok {
			writeJSON(w, http.StatusOK, map[string]any{"available": false, "entries": []GitEntry{}})
			return
		}
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		entries, err := gitLogDir(dir, limit)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"available": true, "entries": []GitEntry{}, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"available": true, "dir": dir, "entries": entries})

	case "snapshots/git/commit":
		var payload struct {
			Message string `json:"message"`
		}
		_ = s.readJSONBody(r, &payload)
		if _, ok := gitBinary(); !ok {
			proxyError(w, http.StatusBadRequest, "git 不可用，无法提交")
			return
		}
		cfg := s.store.Config()
		dir := cfg.SnapshotDir(s.store.ConfigPath())
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "manual commit " + time.Now().Format("2006-01-02 15:04:05")
		}
		if err := gitCommitDir(dir, message); err != nil {
			proxyError(w, http.StatusBadRequest, "git 提交失败："+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "snapshots/create":
		var payload struct {
			Note string `json:"note"`
		}
		_ = s.readJSONBody(r, &payload)
		cfg := s.store.Config()
		meta, err := createSnapshot(cfg.SnapshotDir(s.store.ConfigPath()), cfg, payload.Note, cfg.Server.SnapshotLimit)
		if err != nil {
			proxyError(w, http.StatusBadRequest, "创建快照失败："+err.Error())
			return
		}
		s.store.Info("手动创建配置快照：%s", meta.Name)
		s.gitCommitSnapshot(meta.Name)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "snapshot": meta})

	case "snapshots/rollback":
		var payload struct {
			Name string `json:"name"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		cfg := s.store.Config()
		dir := cfg.SnapshotDir(s.store.ConfigPath())
		restored, err := readSnapshot(dir, payload.Name)
		if err != nil {
			proxyError(w, http.StatusBadRequest, "读取快照失败："+err.Error())
			return
		}
		s.autoSnapshot("before-rollback")
		if err := s.saveConfigAndReload(restored); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.store.Info("已回滚到快照 %s", payload.Name)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "snapshots/delete":
		var payload struct {
			Name string `json:"name"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		cfg := s.store.Config()
		if err := deleteSnapshot(cfg.SnapshotDir(s.store.ConfigPath()), payload.Name); err != nil {
			proxyError(w, http.StatusBadRequest, "删除快照失败："+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "snapshots/get":
		name := r.URL.Query().Get("name")
		cfg := s.store.Config()
		snap, err := readSnapshot(cfg.SnapshotDir(s.store.ConfigPath()), name)
		if err != nil {
			proxyError(w, http.StatusBadRequest, "读取快照失败："+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, snap)

	default:
		proxyError(w, http.StatusNotFound, "unknown admin action: "+action)
	}
}

func cloneConfig(c *Config) *Config {
	raw, err := json.Marshal(c)
	if err != nil {
		return DefaultConfig()
	}
	out := DefaultConfig()
	if err := json.Unmarshal(raw, out); err != nil {
		return DefaultConfig()
	}
	return out
}

func (s *Server) dryRunEntry(entry ReplayEntry) map[string]any {
	cfg := s.store.Config()
	kind := epChat
	switch canonicalAPIPath(entry.Path) {
	case "/v1/responses", "/v1/responses/compact":
		kind = epResponses
	case "/v1/messages":
		kind = epMessages
	}
	outcome := evaluateRules(cfg.Rules, cfg.Profiles, ruleContext{
		Model:   entry.Model,
		Path:    canonicalAPIPath(entry.Path),
		Body:    entry.Body,
		Headers: entry.Headers,
	})
	preview := ""
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(entry.Body), &payload); err == nil {
		body := cloneJSONObject(payload)
		if outcome.hasInstruction() {
			_ = injectStrategy(body, kind, outcome.Instruction)
		}
		if raw, err := json.MarshalIndent(body, "", "  "); err == nil {
			preview = string(raw)
		}
	}
	return map[string]any{
		"entry":        entry,
		"matched":      outcome.Matched,
		"skipped":      outcome.Skipped,
		"denied":       outcome.Denied,
		"status":       outcome.Status,
		"message":      outcome.Message,
		"instruction":  outcome.Instruction,
		"preview_body": preview,
	}
}

func truncateForReport(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}
