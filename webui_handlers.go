package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	data, contentType, ok := readWebUIAsset(r.URL.Path)
	if !ok {
		proxyError(w, http.StatusNotFound, "ui asset not found: "+r.URL.Path)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
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

	case "keys":
		current := s.store.Config()
		switch r.Method {
		case http.MethodGet:
			reveal := r.URL.Query().Get("reveal") == "1"
			items := make([]map[string]any, 0, len(current.Server.AccessKeys))
			for i, k := range current.Server.AccessKeys {
				item := map[string]any{"index": i, "masked": maskKey(k), "length": len(k)}
				if reveal {
					item["key"] = k
				}
				items = append(items, item)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"keys":         items,
				"count":        len(items),
				"auth_enabled": len(items) > 0,
			})

		case http.MethodPost:
			var payload struct {
				Key string `json:"key"`
			}
			_ = s.readJSONBody(r, &payload)
			key := strings.TrimSpace(payload.Key)
			if key == "" {
				key = "sk-gw-" + randomAccessKey()
			}
			if len(key) < 8 {
				proxyError(w, http.StatusBadRequest, "密钥长度至少 8 位")
				return
			}
			for _, exist := range current.Server.AccessKeys {
				if exist == key {
					proxyError(w, http.StatusBadRequest, "该密钥已存在")
					return
				}
			}
			next := cloneConfig(current)
			next.Server.AccessKeys = append(append([]string{}, next.Server.AccessKeys...), key)
			s.autoSnapshot("before-access-key-create")
			if err := s.saveConfigAndReload(next); err != nil {
				proxyError(w, http.StatusBadRequest, err.Error())
				return
			}
			s.store.Info("已创建接入密钥：%s（共 %d 把）", maskKey(key), len(next.Server.AccessKeys))
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "key": key, "masked": maskKey(key), "count": len(next.Server.AccessKeys),
			})

		case http.MethodDelete:
			key := strings.TrimSpace(r.URL.Query().Get("key"))
			if key == "" {
				var payload struct {
					Key string `json:"key"`
				}
				_ = s.readJSONBody(r, &payload)
				key = strings.TrimSpace(payload.Key)
			}
			if key == "" {
				proxyError(w, http.StatusBadRequest, "缺少要吊销的 key")
				return
			}
			kept := make([]string, 0, len(current.Server.AccessKeys))
			found := false
			for _, exist := range current.Server.AccessKeys {
				if exist == key {
					found = true
					continue
				}
				kept = append(kept, exist)
			}
			if !found {
				proxyError(w, http.StatusNotFound, "密钥不存在")
				return
			}
			next := cloneConfig(current)
			next.Server.AccessKeys = kept
			s.autoSnapshot("before-access-key-delete")
			if err := s.saveConfigAndReload(next); err != nil {
				proxyError(w, http.StatusBadRequest, err.Error())
				return
			}
			s.store.Info("已吊销接入密钥：%s（剩 %d 把）", maskKey(key), len(kept))
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(kept)})

		default:
			proxyError(w, http.StatusMethodNotAllowed, "GET / POST / DELETE only")
		}

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

	case "channels":
		cfg := s.store.Config()
		presets := builtinChannelPresets()
		if r.Method == http.MethodGet {
			views := make([]map[string]any, 0, len(cfg.Channels))
			for _, ch := range cfg.Channels {
				preset, ok := findPreset(ch.Preset)
				view := map[string]any{
					"id": ch.ID, "preset": ch.Preset, "name": ch.Name,
					"base_url": ch.BaseURL, "models": ch.Models,
					"priority": ch.Priority, "weight": ch.Weight, "enabled": ch.Enabled,
					"token_masked": maskKey(ch.Token),
					"has_token":    strings.TrimSpace(ch.Token) != "",
					"key_count":    len(channelKeys(ch)),
					"protocol":     preset.Protocol,
					"upstream_id":  "ch-" + ch.ID,
					"notes":        preset.Notes,
				}
				if !ok {
					view["protocol"] = "unknown"
				}
				views = append(views, view)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"presets":  presets,
				"channels": views,
			})
			return
		}
		if r.Method == http.MethodDelete {
			id := strings.TrimSpace(r.URL.Query().Get("id"))
			if id == "" {
				var payload struct {
					ID string `json:"id"`
				}
				_ = s.readJSONBody(r, &payload)
				id = strings.TrimSpace(payload.ID)
			}
			if id == "" {
				proxyError(w, http.StatusBadRequest, "缺少渠道 id")
				return
			}
			next := cloneConfig(cfg)
			kept := make([]Channel, 0, len(next.Channels))
			for _, ch := range next.Channels {
				if ch.ID != id {
					kept = append(kept, ch)
				}
			}
			next.Channels = kept
			s.autoSnapshot("before-channel-delete")
			if err := s.saveConfigAndReload(next); err != nil {
				proxyError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		var payload struct {
			ID       string            `json:"id"`
			Preset   string            `json:"preset"`
			Name     string            `json:"name"`
			Token    string            `json:"token"`
			BaseURL  string            `json:"base_url"`
			Models   []string          `json:"models"`
			Headers  map[string]string `json:"headers"`
			Priority int               `json:"priority"`
			Weight   int               `json:"weight"`
			Enabled  *bool             `json:"enabled"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		preset, ok := findPreset(payload.Preset)
		if !ok {
			proxyError(w, http.StatusBadRequest, "未知渠道模板："+payload.Preset)
			return
		}
		id := strings.TrimSpace(payload.ID)
		if id == "" {
			id = payload.Preset
		}
		next := cloneConfig(cfg)
		found := false
		for i := range next.Channels {
			if next.Channels[i].ID == id {
				found = true
				ch := &next.Channels[i]
				ch.Preset = payload.Preset
				if payload.Name != "" {
					ch.Name = payload.Name
				}
				if payload.Token != "" {
					ch.Token = payload.Token
				}
				if payload.BaseURL != "" {
					ch.BaseURL = payload.BaseURL
				}
				if len(payload.Models) > 0 {
					ch.Models = payload.Models
				}
				if len(payload.Headers) > 0 {
					if ch.Headers == nil {
						ch.Headers = map[string]string{}
					}
					for k, v := range payload.Headers {
						ch.Headers[k] = v
					}
				}
				ch.Priority = payload.Priority
				ch.Weight = payload.Weight
				if payload.Enabled != nil {
					ch.Enabled = *payload.Enabled
				}
			}
		}
		if !found {
			name := payload.Name
			if name == "" {
				name = preset.Name
			}
			enabled := true
			if payload.Enabled != nil {
				enabled = *payload.Enabled
			}
			next.Channels = append(next.Channels, Channel{
				ID:       id,
				Preset:   payload.Preset,
				Name:     name,
				Token:    payload.Token,
				BaseURL:  payload.BaseURL,
				Models:   payload.Models,
				Headers:  payload.Headers,
				Priority: payload.Priority,
				Weight:   payload.Weight,
				Enabled:  enabled,
			})
		}
		s.autoSnapshot("before-channel-save")
		if err := s.saveConfigAndReload(next); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.store.Info("渠道已保存：%s（模板 %s）", id, preset.ID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "upstream_id": "ch-" + id})

	case "channels/test":
		var payload struct {
			ID string `json:"id"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		ch, preset, ok := s.store.channelByID(payload.ID)
		if !ok {
			proxyError(w, http.StatusNotFound, "渠道不存在："+payload.ID)
			return
		}
		if strings.TrimSpace(preset.ModelsPath) == "" {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": false, "skipped": true,
				"message": "该渠道模板没有模型探测端点（" + preset.Notes + "）",
			})
			return
		}
		base := ch.BaseURL
		if base == "" {
			base = preset.BaseURL
		}
		target := joinURL(base, preset.ModelsPath)
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		applyChannelAuth(req, preset, ch.Token)
		for k, v := range preset.ExtraHeaders {
			req.Header.Set(k, v)
		}
		for k, v := range ch.Headers {
			req.Header.Set(k, v)
		}
		client := &http.Client{Timeout: 15 * time.Second}
		started := time.Now()
		resp, err := client.Do(req)
		latency := time.Since(started).Milliseconds()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "latency_ms": latency, "message": err.Error()})
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         resp.StatusCode >= 200 && resp.StatusCode < 300,
			"status":     resp.StatusCode,
			"latency_ms": latency,
			"url":        target,
			"body":       truncateForReport(string(body), 400),
		})

	case "channels/login/start":
		var payload struct {
			Preset string `json:"preset"`
			ID     string `json:"id"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		preset, ok := findPreset(payload.Preset)
		if !ok {
			proxyError(w, http.StatusBadRequest, "未知渠道模板："+payload.Preset)
			return
		}
		id := strings.TrimSpace(payload.ID)
		if id == "" {
			id = preset.ID
		}
		profileRoot := filepath.Join(os.TempDir(), "gateway-manager-login")
		session, err := newLoginSession(id, preset, profileRoot)
		if err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.loginMu.Lock()
		if old := s.logins[id]; old != nil {
			old.closeSession()
		}
		s.logins[id] = session
		s.loginMu.Unlock()
		s.store.Info("已打开 %s 的登录页：%s（浏览器 %s）", preset.Name, preset.LoginURL, filepath.Base(session.Browser))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "id": id, "preset": preset.ID,
			"login_url": preset.LoginURL, "login_hint": preset.LoginHint,
			"browser": session.Browser, "port": session.Port,
		})

	case "channels/login/status":
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			proxyError(w, http.StatusBadRequest, "缺少 id")
			return
		}
		s.loginMu.Lock()
		session := s.logins[id]
		s.loginMu.Unlock()
		if session == nil {
			writeJSON(w, http.StatusOK, map[string]any{"active": false})
			return
		}
		preset, _ := findPreset(session.Preset)
		cookies, err := session.cookiesForPreset(preset)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"active": true, "error": err.Error()})
			return
		}
		token := cookiesToToken(cookies)
		names := make([]string, 0, len(cookies))
		for _, c := range cookies {
			names = append(names, c.Name)
		}
		storageKeys := []string{}
		storageCandidate := ""
		storageCandidateKey := ""
		if storage, err := session.storageSnapshot(preset); err == nil {
			for k, v := range storage {
				if strings.TrimSpace(v) != "" {
					storageKeys = append(storageKeys, k)
				}
			}
			sort.Strings(storageKeys)
			storageCandidateKey, storageCandidate = pickTokenFromStorage(storage)
		}
		reveal := r.URL.Query().Get("reveal") == "1"
		var storageDump map[string]string
		if reveal {
			if st, err := session.storageSnapshot(preset); err == nil {
				storageDump = st
			}
		}
		presetForHint, _ := findPreset(session.Preset)
		writeJSON(w, http.StatusOK, map[string]any{
			"active":         true,
			"storage":        storageDump,
			"login_url":      session.LoginURL,
			"login_hint":     presetForHint.LoginHint,
			"browser":        filepath.Base(session.Browser),
			"cookie_count":   len(cookies),
			"cookie_names":   names,
			"token_masked":   maskKey(token),
			"storage_keys":   storageKeys,
			"token_key":      storageCandidateKey,
			"token_key_mask": maskKey(storageCandidate),
			"ready":          len(cookies) > 0 || storageCandidate != "",
		})

	case "channels/login/finish":
		var payload struct {
			ID       string `json:"id"`
			Preset   string `json:"preset"`
			Model    string `json:"model"`
			TokenKey string `json:"token_key"`
			Enabled  *bool  `json:"enabled"`
			Replace  bool   `json:"replace"`
		}
		if err := s.readJSONBody(r, &payload); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		id := strings.TrimSpace(payload.ID)
		s.loginMu.Lock()
		session := s.logins[id]
		s.loginMu.Unlock()
		if session == nil {
			proxyError(w, http.StatusNotFound, "登录会话不存在或已结束："+id)
			return
		}
		preset, ok := findPreset(session.Preset)
		if !ok {
			proxyError(w, http.StatusBadRequest, "未知渠道模板")
			return
		}
		cookies, err := session.cookiesForPreset(preset)
		if err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		cookieValue := cookiesToToken(cookies)
		token := ""
		tokenKind := ""
		if storage, err := session.storageSnapshot(preset); err == nil {
			key := preset.TokenStorageKey
			if key == "" {
				key, _ = pickTokenFromStorage(storage)
			}
			if raw := strings.TrimSpace(storage[key]); raw != "" {
				token = unwrapStorageValue(raw)
				tokenKind = key
			}
		}
		if token == "" {
			token = cookieValue
			tokenKind = "cookie"
		}
		if token == "" {
			proxyError(w, http.StatusBadRequest, "还没抓到 cookie 或 token，请先在打开的浏览器里完成登录")
			return
		}
		cfg := s.store.Config()
		next := cloneConfig(cfg)
		enabled := true
		if payload.Enabled != nil {
			enabled = *payload.Enabled
		}
		if payload.Replace {
			for i := range next.Channels {
				if next.Channels[i].ID == id {
					next.Channels[i].Tokens = nil
				}
			}
		}
		found := false
		appended := false
		for i := range next.Channels {
			if next.Channels[i].ID != id {
				continue
			}
			next.Channels[i].Preset = preset.ID
			next.Channels[i].Enabled = enabled
			found = true
			existing := channelKeys(next.Channels[i])
			duplicated := false
			for _, have := range existing {
				if have == token {
					duplicated = true
					break
				}
			}
			switch {
			case duplicated:
			case strings.TrimSpace(next.Channels[i].Token) == "":
				next.Channels[i].Token = token
				if cookieValue != "" {
					next.Channels[i].Cookie = cookieValue
				}
			default:
				next.Channels[i].Tokens = append(append([]string{}, next.Channels[i].Tokens...), token)
				appended = true
			}
		}
		if !found {
			next.Channels = append(next.Channels, Channel{
				ID: id, Preset: preset.ID, Name: preset.Name, Token: token, Cookie: cookieValue,
				Enabled: enabled, Headers: map[string]string{},
			})
		}
		keyCount := 0
		for i := range next.Channels {
			if next.Channels[i].ID == id {
				keyCount = len(channelKeys(next.Channels[i]))
			}
		}
		s.autoSnapshot("before-channel-login")
		if err := s.saveConfigAndReload(next); err != nil {
			proxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		session.closeSession()
		s.loginMu.Lock()
		delete(s.logins, id)
		s.loginMu.Unlock()
		s.store.Info("网页登录成功：渠道 %s 已保存凭证（%d 个 cookie）", id, len(cookies))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "id": id, "upstream_id": "ch-" + id,
			"cookie_count": len(cookies), "token_masked": maskKey(token),
			"token_kind": tokenKind, "key_count": keyCount, "appended": appended,
		})

	case "channels/login/cancel":
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		s.loginMu.Lock()
		session := s.logins[id]
		delete(s.logins, id)
		s.loginMu.Unlock()
		if session != nil {
			session.closeSession()
		}
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

func randomAccessKey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}
