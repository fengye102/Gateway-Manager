package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

func (s *Server) RunHealthLoop(ctx context.Context) {
	cfg := s.store.Config()
	interval := time.Duration(cfg.Server.HealthIntervalSeconds) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.checkAllUpstreams(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkAllUpstreams(ctx)
		}
	}
}

func (s *Server) checkAllUpstreams(ctx context.Context) {
	cfg := s.store.Config()
	var wg sync.WaitGroup
	for _, u := range cfg.Upstreams {
		if !u.Enabled {
			continue
		}
		item := u
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.checkUpstream(ctx, &item)
		}()
	}
	wg.Wait()
}

func (s *Server) checkUpstream(ctx context.Context, u *Upstream) {
	timeout := 5 * time.Second
	if u.Probe != nil && u.Probe.TimeoutMS > 0 {
		timeout = time.Duration(u.Probe.TimeoutMS) * time.Millisecond
	}
	client := &http.Client{Timeout: timeout}
	key := ""
	if len(u.APIKeys) > 0 {
		key = u.APIKeys[0]
	}
	modelsPath := ""
	if adapter := findAdapter(s.store.Config(), u.BaseURL); adapter != nil {
		modelsPath = adapter.ModelsPath
	}
	if u.Probe != nil {
		models, err := runProbe(ctx, client, u, key, modelsPath)
		if err != nil {
			s.store.SetHealth(u.ID, false, nil, err.Error())
			s.store.Warn("健康探测失败 %s（%s）：%v", u.Name, u.Probe.Path, err)
			return
		}
		s.store.SetHealth(u.ID, true, models, "")
		s.store.Info("健康探测通过 %s（%s，%d 个模型）", u.Name, u.Probe.Path, len(models))
		return
	}
	models, err := fetchModels(ctx, client, u, key, modelsPath)
	if err != nil {
		s.store.SetHealth(u.ID, false, nil, err.Error())
		s.store.Warn("健康检查失败 %s：%v", u.Name, err)
		return
	}
	s.store.SetHealth(u.ID, true, models, "")
	s.store.Info("健康检查通过 %s（%d 个模型）", u.Name, len(models))
}

func parseModelList(resp *http.Response) ([]string, error) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("响应不是 JSON")
	}
	list, _ := payload["data"].([]any)
	if list == nil {
		list, _ = payload["models"].([]any)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case map[string]any:
			if id, ok := v["id"].(string); ok && id != "" {
				out = append(out, id)
			}
		case string:
			if v != "" {
				out = append(out, v)
			}
		}
	}
	return out, nil
}

func fetchModels(ctx context.Context, client *http.Client, u *Upstream, key string, modelsPath string) ([]string, error) {
	if modelsPath == "" {
		modelsPath = "/v1/models"
	}
	target := joinURL(u.BaseURL, modelsPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range u.Headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("响应不是 JSON")
	}
	list, _ := payload["data"].([]any)
	if list == nil {
		list, _ = payload["models"].([]any)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case map[string]any:
			if id, ok := v["id"].(string); ok && id != "" {
				out = append(out, id)
			}
		case string:
			if v != "" {
				out = append(out, v)
			}
		}
	}
	return out, nil
}

func runProbe(ctx context.Context, client *http.Client, u *Upstream, key string, modelsPath string) ([]string, error) {
	probe := u.Probe
	method := "GET"
	if probe != nil && strings.TrimSpace(probe.Method) != "" {
		method = strings.ToUpper(strings.TrimSpace(probe.Method))
	}
	path := "/v1/models"
	if modelsPath != "" {
		path = modelsPath
	}
	if probe != nil && strings.TrimSpace(probe.Path) != "" {
		path = probe.Path
	}
	target := joinURL(u.BaseURL, path)
	var body io.Reader
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		body = strings.NewReader(`{`)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range u.Headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))

	expect := []int{200}
	if probe != nil && len(probe.ExpectStatus) > 0 {
		expect = probe.ExpectStatus
	}
	ok := false
	for _, code := range expect {
		if resp.StatusCode == code {
			ok = true
			break
		}
	}
	if !ok && probe == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		ok = true
	}
	if !ok {
		return nil, fmt.Errorf("HTTP %d（期望 %v）", resp.StatusCode, expect)
	}
	if probe != nil && probe.ExpectContains != "" && !strings.Contains(string(raw), probe.ExpectContains) {
		return nil, fmt.Errorf("响应体缺少 %q", probe.ExpectContains)
	}
	return modelIDsFromBody(raw), nil
}

func modelIDsFromBody(raw []byte) []string {
	payload := map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	list, _ := payload["data"].([]any)
	if list == nil {
		list, _ = payload["models"].([]any)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case map[string]any:
			if id, ok := v["id"].(string); ok && id != "" {
				out = append(out, id)
			}
		case string:
			if v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}
