package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Server struct {
	store  *Store
	mu     sync.RWMutex
	client *http.Client

	loginMu sync.Mutex
	logins  map[string]*loginSession
}

func NewServer(st *Store) *Server {
	s := &Server{store: st, logins: map[string]*loginSession{}}
	s.refreshClient()
	return s
}

func (s *Server) refreshClient() {
	cfg := s.store.Config()
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(cfg.Retry.RequestTimeoutSeconds) * time.Second,
	}
	s.mu.Lock()
	s.client = client
	s.mu.Unlock()
}

func (s *Server) httpClient() *http.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.ServeHTTP) }

func canonicalAPIPath(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	if strings.HasPrefix(p, "/v1/") {
		return p
	}
	return "/v1" + p
}

func requestHeaders(r *http.Request) map[string]string {
	out := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	switch {
	case path == "/ui" || strings.HasPrefix(path, "/ui/"):
		s.handleUI(w, r)
		return
	case strings.HasPrefix(path, "/admin/api/"):
		s.handleAdmin(w, r, strings.TrimPrefix(path, "/admin/api/"))
		return
	}

	switch path {
	case "/":
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "gateway-manager",
			"version": version,
			"endpoints": []string{
				"/v1/models", "/v1/chat/completions", "/v1/responses",
				"/v1/responses/compact", "/v1/messages", "/health", "/metrics", "/ui",
			},
		})
		return
	case "/health":
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": version})
		return
	case "/metrics":
		s.handleMetrics(w, r)
		return
	}

	switch canonicalAPIPath(path) {
	case "/v1/models":
		s.handleModels(w, r)
	case "/v1/chat/completions":
		s.handleProxy(w, r, epChat, "")
	case "/v1/responses":
		s.handleProxy(w, r, epResponses, "")
	case "/v1/responses/compact":
		s.handleProxy(w, r, epResponses, "/v1/responses/compact")
	case "/v1/messages":
		s.handleProxy(w, r, epMessages, "")
	default:
		proxyError(w, http.StatusNotFound, "Unknown endpoint: "+r.URL.Path)
	}
}

func (s *Server) authorized(r *http.Request) bool {
	keys := s.store.Config().Server.AccessKeys
	if len(keys) == 0 {
		return true
	}
	token := ""
	if h := r.Header.Get("Authorization"); h != "" {
		if parts := strings.SplitN(h, " ", 2); len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token = strings.TrimSpace(parts[1])
		}
	}
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-Api-Key"))
	}
	if token == "" {
		return false
	}
	for _, k := range keys {
		if k == token {
			return true
		}
	}
	return false
}

func (s *Server) maxBodyBytes() int64 {
	cfg := s.store.Config()
	mb := cfg.Server.MaxBodyMB
	if mb <= 0 {
		mb = 32
	}
	return int64(mb) * 1024 * 1024
}

func redactedHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "auth") || strings.Contains(lk, "key") || strings.Contains(lk, "token") || strings.Contains(lk, "cookie") || strings.Contains(lk, "secret") {
			out[k] = "***"
			continue
		}
		out[k] = v
	}
	return out
}

func truncateBody(body []byte, limit int) (string, bool) {
	if limit <= 0 {
		limit = 8192
	}
	if len(body) <= limit {
		return string(body), false
	}
	return string(body[:limit]), true
}

func retryableStatus(status int, list []int) bool {
	for _, s := range list {
		if status == s {
			return true
		}
	}
	return status >= 500
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request, kind endpointKind, upstreamPath string) {
	if r.Method != http.MethodPost {
		proxyError(w, http.StatusMethodNotAllowed, "Only POST is supported")
		return
	}
	requestStarted := time.Now()
	s.store.CountRequest()
	if !s.authorized(r) {
		s.store.CountAuthFail()
		s.store.Warn("%s %s 鉴权失败", r.Method, r.URL.Path)
		proxyError(w, http.StatusUnauthorized, "A valid proxy API key is required")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, s.maxBodyBytes()))
	if err != nil {
		proxyError(w, http.StatusBadRequest, "读取请求体失败")
		return
	}
	defer r.Body.Close()

	original := map[string]any{}
	if err := json.Unmarshal(body, &original); err != nil {
		proxyError(w, http.StatusBadRequest, "A JSON object body is required")
		return
	}
	model := jsonStringField(original, "model")
	if strings.TrimSpace(model) == "" {
		proxyError(w, http.StatusBadRequest, "A nonempty model is required")
		return
	}
	wantStream := jsonBoolField(original, "stream")

	cfg := s.store.Config()
	ruleCtx := ruleContext{
		Model:   model,
		Path:    canonicalAPIPath(r.URL.Path),
		Body:    string(body),
		Headers: requestHeaders(r),
	}
	outcome := evaluateRules(cfg.Rules, cfg.Profiles, ruleCtx)
	s.store.RecordRuleHits(outcome.Matched, outcome.Skipped, outcome.Denied)
	if outcome.Denied {
		s.store.CountRuleDeny()
		s.store.Warn("规则拦截 %s：%s", kind, outcome.Message)
		s.store.ObserveRequest(kind.String(), outcome.Status, time.Since(requestStarted))
		bodyText, truncated := truncateBody(body, 8192)
		if id := s.store.AddReplay(ReplayEntry{
			Time:      time.Now().Unix(),
			Method:    r.Method,
			Path:      r.URL.Path,
			Model:     model,
			Status:    outcome.Status,
			Message:   outcome.Message,
			Rules:     outcome.Matched,
			Headers:   redactedHeaders(requestHeaders(r)),
			Body:      bodyText,
			BodySize:  len(body),
			Truncated: truncated,
		}); id != "" {
			s.store.Info("已记录拦截回放 %s（%s %s）", id, r.Method, r.URL.Path)
		}
		proxyError(w, outcome.Status, outcome.Message)
		return
	}
	if strings.TrimSpace(outcome.ModelOverride) != "" {
		s.store.Info("规则改写模型：%s → %s", model, outcome.ModelOverride)
		model = outcome.ModelOverride
	}
	if len(outcome.Matched) > 0 {
		s.store.Info("命中规则：%s", strings.Join(outcome.Matched, ", "))
	}

	exp, arm := s.store.PickExperiment(model)
	candidates := s.store.Candidates(model)
	if arm != nil && arm.UpstreamID != "" {
		filtered := make([]*Upstream, 0, len(candidates))
		for _, c := range candidates {
			if c.ID == arm.UpstreamID {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) > 0 {
			candidates = filtered
		}
	}
	if len(candidates) == 0 {
		s.store.ObserveRequest(kind.String(), http.StatusServiceUnavailable, time.Since(requestStarted))
		proxyError(w, http.StatusServiceUnavailable, "No upstream provides model "+model)
		return
	}

	maxAttempts := cfg.Retry.MaxRetries + 1
	for _, c := range candidates {
		if extra := len(c.APIKeys) - 1; extra > 0 {
			maxAttempts += extra
		}
	}
	if maxAttempts > 8 {
		maxAttempts = 8
	}
	tries := map[string]int{}
	limitFor := func(u *Upstream) int {
		n := len(u.APIKeys)
		if n < 1 {
			n = 1
		}
		return n
	}
	exhaust := func(u *Upstream) { tries[u.ID] = limitFor(u) }

	lastMessage := "No upstream could serve the request"
	lastStatus := http.StatusBadGateway
	skipped := 0

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var up *Upstream
		for _, c := range candidates {
			if tries[c.ID] < limitFor(c) {
				up = c
				break
			}
		}
		if up == nil {
			break
		}
		tries[up.ID]++

		alias, ok := resolveModel(up, model)
		if !ok {
			exhaust(up)
			continue
		}
		preferredProfile := ""
		if arm != nil {
			preferredProfile = arm.ProfileID
		}
		payload, profile, injectRes := s.prepareRequest(original, kind, model, alias, up, outcome, preferredProfile)
		if injectRes == injectBadShape {
			s.store.CountBadShape()
			s.store.ObserveRequest(kind.String(), http.StatusBadRequest, time.Since(requestStarted))
			proxyError(w, http.StatusBadRequest, "Cannot apply a strategy: conflicting field type in request")
			return
		}

		forwardKind := kind
		if kind == epMessages && !upstreamAnthropicNative(cfg, up) {
			// 上游只会讲 OpenAI 方言：把 Anthropic 请求翻译成 chat/completions
			translated, terr := anthropicRequestToOpenAI(payload)
			if terr != nil {
				s.store.ObserveRequest(kind.String(), http.StatusBadRequest, time.Since(requestStarted))
				proxyError(w, http.StatusBadRequest, "Anthropic 请求翻译失败："+terr.Error())
				return
			}
			payload = translated
			forwardKind = epChat
		}

		estTokens := estimateTokens(payload)
		if okBudget, budgetReason := s.store.CheckBudget(up, estTokens); !okBudget {
			exhaust(up)
			skipped++
			s.store.Warn("跳过上游 %s：%s", up.Name, budgetReason)
			lastMessage = fmt.Sprintf("上游 %s 不可用：%s", up.Name, budgetReason)
			continue
		}

		l, reason := s.store.Acquire(up)
		if l == nil {
			skipped++
			s.store.Info("跳过上游 %s：%s", up.Name, reason)
			lastMessage = fmt.Sprintf("上游 %s 不可用：%s", up.Name, reason)
			continue
		}
		queued := time.Since(requestStarted)

		started := time.Now()
		resp, ferr := s.forward(r.Context(), up, forwardKind, upstreamPath, payload, l.keyValue(), outcome.Headers)
		if ferr != nil {
			accountDead := errors.Is(ferr, errAccountUnavailable)
			left := 0
			if accountDead {
				// 账号级不可用（额度耗尽 / 被站点封禁）：只冷却这个账号，
				// 不计入上游级熔断——否则一个账号额度用完会把同渠道其他账号一起熔断。
				coolFor := accountCooldown(ferr)
				if coolFor <= 0 {
					coolFor = time.Duration(cfg.Server.KeyCooldownSeconds) * time.Second
				}
				if coolFor <= 0 {
					coolFor = 15 * time.Minute
				}
				left = s.store.CoolKey(l, coolFor, ferr.Error())
			} else {
				s.store.RecordFailure(l, 0, ferr.Error())
			}
			s.store.ObserveUpstream(up.ID, 0, time.Since(started))
			label := l.keyLabel()
			s.store.Release(l)
			lastMessage = fmt.Sprintf("Upstream %s connection failed: %v", up.Name, ferr)
			lastStatus = http.StatusBadGateway
			if accountDead {
				s.store.Warn("渠道 %s 账号 %s 不可用（%v），剩余可用账号 %d 个", up.Name, label, ferr, left)
				if left > 0 && tries[up.ID] < limitFor(up) {
					continue
				}
				exhaust(up)
				continue
			}
			s.store.Error("上游 %s 连接失败：%v", up.Name, ferr)
			if len(up.APIKeys) <= 1 {
				exhaust(up)
			}
			continue
		}

		if retryableStatus(resp.StatusCode, cfg.Retry.RetryStatus) {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			s.store.RecordFailure(l, resp.StatusCode, fmt.Sprintf("HTTP %d", resp.StatusCode))
			s.store.ObserveUpstream(up.ID, resp.StatusCode, time.Since(started))
			s.store.Release(l)
			lastMessage = fmt.Sprintf("Upstream %s returned HTTP %d", up.Name, resp.StatusCode)
			lastStatus = resp.StatusCode
			keyFailure := resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429
			if keyFailure && len(up.APIKeys) > 1 && tries[up.ID] < limitFor(up) {
				s.store.Warn("上游 %s 返回 HTTP %d（key %s），换 key 重试", up.Name, resp.StatusCode, l.keyLabel())
			} else {
				exhaust(up)
				s.store.Warn("上游 %s 返回 HTTP %d（key %s），切换下一个上游", up.Name, resp.StatusCode, l.keyLabel())
			}
			continue
		}

		ttfb := time.Since(started)
		s.store.ConsumeBudget(up, estTokens)
		s.store.RecordSuccess(l, resp.StatusCode)
		s.store.ObserveUpstream(up.ID, resp.StatusCode, ttfb)
		s.store.ObserveRoute(model, up.ID)
		s.store.Release(l)

		source := "未注入"
		switch {
		case outcome.hasInstruction():
			s.store.CountRuleInject()
			source = "规则注入"
		case profile != nil:
			s.store.CountInjected()
			source = "策略「" + profile.Name + "」"
		default:
			s.store.CountNoProfile()
		}
		s.store.Info("%s → %s HTTP %d · %s · key %s · %.0fms · stream=%v",
			kind, up.Name, resp.StatusCode, source, l.keyLabel(),
			float64(time.Since(started).Milliseconds()), wantStream)
		var tokens int
		var hasUsage bool
		if kind == epMessages && forwardKind == epChat {
			// Anthropic 客户端 ← OpenAI 上游：响应也要翻译回 Anthropic 形状
			tokens, hasUsage = s.relayAnthropicFromOpenAI(w, resp, wantStream)
		} else {
			tokens, hasUsage = s.relay(w, resp, wantStream, kind)
		}
		if hasUsage {
			s.store.AddTokenUsage(up.ID, tokens)
			s.store.ReconcileBudget(up, estTokens, tokens)
		}
		if exp != nil && arm != nil {
			s.store.RecordExperiment(exp.ID, arm.ID, time.Since(requestStarted), tokens, resp.StatusCode < 400)
		}
		total := time.Since(requestStarted)
		relayDur := total - queued - ttfb
		if relayDur < 0 {
			relayDur = 0
		}
		s.store.ObserveTiming(queued, ttfb, relayDur)
		s.store.ObserveRequest(kind.String(), resp.StatusCode, total)
		s.maybeMirror(r.Context(), cfg, up, forwardKind, upstreamPath, payload, outcome, model)
		if s.store.ShouldSample() {
			bodyText, truncated := truncateBody(body, 8192)
			s.store.AddSample(ReplayEntry{
				Time:      time.Now().Unix(),
				Method:    r.Method,
				Path:      r.URL.Path,
				Model:     model,
				Status:    resp.StatusCode,
				Message:   "采样成功请求",
				Rules:     outcome.Matched,
				Headers:   redactedHeaders(requestHeaders(r)),
				Body:      bodyText,
				BodySize:  len(body),
				Truncated: truncated,
				Upstream:  up.ID,
				LatencyMS: total.Milliseconds(),
			})
		}
		return
	}

	if skipped > 0 && skipped == len(candidates) {
		proxyError(w, http.StatusServiceUnavailable,
			fmt.Sprintf("All upstreams for %s are cooling down or rate limited; check upstream URL, key and model list", model))
		return
	}
	if exp != nil && arm != nil {
		s.store.RecordExperiment(exp.ID, arm.ID, time.Since(requestStarted), 0, false)
	}
	proxyError(w, lastStatus, lastMessage)
}

func (s *Server) prepareRequest(original map[string]any, kind endpointKind, requested string, alias string, up *Upstream, outcome ruleOutcome, preferredProfile string) ([]byte, *Profile, injectResult) {
	body := cloneJSONObject(original)
	body["model"] = alias
	for k, v := range outcome.Params {
		body[k] = v
	}

	var profile *Profile
	result := injectSkipped
	if outcome.hasInstruction() {
		result = injectStrategy(body, kind, outcome.Instruction)
	} else {
		cfg := s.store.Config()
		preferred := up.ProfileID
		if preferredProfile != "" {
			preferred = preferredProfile
		}
		profile = selectProfile(cfg.Profiles, preferred, requested)
		if profile != nil {
			result = injectStrategy(body, kind, profile.Instruction)
		}
	}
	if result == injectBadShape {
		return nil, profile, result
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, profile, injectBadShape
	}
	return payload, profile, result
}

func findAdapter(cfg *Config, baseURL string) *Adapter {
	var best *Adapter
	bestLen := -1
	for i := range cfg.Adapters {
		a := &cfg.Adapters[i]
		prefix := strings.TrimRight(strings.TrimSpace(a.MatchBaseURL), "/")
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(baseURL, prefix) && len(prefix) > bestLen {
			best = a
			bestLen = len(prefix)
		}
	}
	return best
}

func (a *Adapter) pathFor(kind endpointKind) string {
	if a == nil {
		return ""
	}
	switch kind {
	case epChat:
		return a.ChatPath
	case epResponses:
		return a.ResponsesPath
	case epMessages:
		return a.MessagesPath
	}
	return ""
}

func (s *Server) forward(ctx context.Context, up *Upstream, kind endpointKind, upstreamPath string, payload []byte, key string, extraHeaders map[string]string) (*http.Response, error) {
	cfg := s.store.Config()
	adapter := findAdapter(cfg, up.BaseURL)
	if upstreamPath == "" {
		upstreamPath = adapter.pathFor(kind)
	}
	if upstreamPath == "" {
		upstreamPath = kind.upstreamPath()
	}
	target := joinURL(up.BaseURL, upstreamPath)

	var channelPreset ChannelPreset
	var channelCfg Channel
	hasChannel := false
	if up.ChannelID != "" {
		if ch, preset, ok := s.store.channelByID(up.ChannelID); ok {
			if fn, has := channelAdapters[preset.ID]; has {
				return fn(s, ctx, ch, preset, payload, key)
			}
		}
	}
	if up.ChannelID != "" {
		if ch, preset, ok := s.store.channelByID(up.ChannelID); ok {
			channelCfg = ch
			channelPreset = preset
			hasChannel = true
			chatPath := preset.ChatPath
			if strings.TrimSpace(up.ChannelPath) != "" {
				chatPath = up.ChannelPath
			}
			if upstreamPath == "" || upstreamPath == kind.upstreamPath() {
				target = channelChatURL(up.BaseURL, chatPath)
			}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "gateway-manager/"+version)
	if hasChannel {
		for k, v := range channelPreset.ExtraHeaders {
			req.Header.Set(k, v)
		}
		for k, v := range channelCfg.Headers {
			req.Header.Set(k, v)
		}
		if key != "" {
			applyChannelAuth(req, channelPreset, key)
		}
	} else if key != "" {
		if adapter != nil {
			req.Header.Set(adapter.AuthHeader, adapter.AuthPrefix+key)
		} else {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	if adapter != nil {
		for k, v := range adapter.ExtraHeaders {
			req.Header.Set(k, v)
		}
	}
	for k, v := range up.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	return s.httpClient().Do(req)
}

func joinURL(base string, path string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.HasSuffix(base, "/v1") && strings.HasPrefix(path, "/v1/") {
		return base + strings.TrimPrefix(path, "/v1")
	}
	return base + path
}

func (s *Server) relay(w http.ResponseWriter, resp *http.Response, wantStream bool, kind endpointKind) (int, bool) {
	defer resp.Body.Close()
	cfg := s.store.Config()
	contentType := resp.Header.Get("Content-Type")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	isSSE := strings.Contains(strings.ToLower(contentType), "text/event-stream")
	if isSSE && !wantStream && cfg.Server.MergeSSE && kind == epChat {
		merged, err := mergeSSEChunks(resp.Body)
		if err != nil {
			proxyError(w, http.StatusBadGateway, "合并 SSE 失败："+err.Error())
			return 0, false
		}
		writeJSON(w, http.StatusOK, merged)
		if total, ok := usageFromMap(merged); ok {
			return total, true
		}
		return 0, false
	}

	if isSSE {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(resp.StatusCode)
		flusher, _ := w.(http.Flusher)
		sniffer := newUsageSniffer()
		buf := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				sniffer.Write(buf[:n])
				if _, werr := w.Write(buf[:n]); werr != nil {
					return sniffer.total()
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if err != nil {
				return sniffer.total()
			}
		}
	}

	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	const bufferCap = 2 << 20
	head := make([]byte, 0, 64*1024)
	buf := make([]byte, 32*1024)
	truncated := false
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if len(head)+n > bufferCap {
				truncated = true
				head = append(head, buf[:n]...)
				break
			}
			head = append(head, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, werr := w.Write(head); werr != nil {
		return 0, false
	}
	if truncated {
		flusher, _ := w.(http.Flusher)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					break
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if err != nil {
				break
			}
		}
		return 0, false
	}
	if total, ok := extractUsageTokens(head); ok {
		return total, true
	}
	return 0, false
}

type toolCallAcc struct {
	ID        string
	Type      string
	Name      string
	Arguments strings.Builder
}

func mergeSSEChunks(body io.Reader) (map[string]any, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var id, model, systemFingerprint string
	var created int64
	var content, reasoning strings.Builder
	finish := ""
	toolCalls := map[int]*toolCallAcc{}
	toolOrder := []int{}
	var usage map[string]any
	seen := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		chunk := map[string]any{}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		seen = true
		if v, ok := chunk["id"].(string); ok && v != "" {
			id = v
		}
		if v, ok := chunk["model"].(string); ok && v != "" {
			model = v
		}
		if v, ok := chunk["system_fingerprint"].(string); ok && v != "" {
			systemFingerprint = v
		}
		if v, ok := chunk["created"].(float64); ok {
			created = int64(v)
		}
		if v, ok := chunk["usage"].(map[string]any); ok && v != nil {
			usage = v
		}
		choices, _ := chunk["choices"].([]any)
		for _, item := range choices {
			ch, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if delta, ok := ch["delta"].(map[string]any); ok {
				if v, ok := delta["content"].(string); ok {
					content.WriteString(v)
				}
				if v, ok := delta["reasoning_content"].(string); ok {
					reasoning.WriteString(v)
				}
				if v, ok := delta["reasoning"].(string); ok {
					reasoning.WriteString(v)
				}
				if list, ok := delta["tool_calls"].([]any); ok {
					for _, raw := range list {
						tc, ok := raw.(map[string]any)
						if !ok {
							continue
						}
						idx := jsonIntField(tc, "index")
						acc, exists := toolCalls[idx]
						if !exists {
							acc = &toolCallAcc{}
							toolCalls[idx] = acc
							toolOrder = append(toolOrder, idx)
						}
						if v, ok := tc["id"].(string); ok && v != "" {
							acc.ID = v
						}
						if v, ok := tc["type"].(string); ok && v != "" {
							acc.Type = v
						}
						if fn, ok := tc["function"].(map[string]any); ok {
							if v, ok := fn["name"].(string); ok && v != "" {
								acc.Name = v
							}
							if v, ok := fn["arguments"].(string); ok {
								acc.Arguments.WriteString(v)
							}
						}
					}
				}
			}
			if v, ok := ch["finish_reason"].(string); ok && v != "" {
				finish = v
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !seen {
		return nil, fmt.Errorf("上游没有返回可解析的 SSE 数据")
	}

	message := map[string]any{
		"role":    "assistant",
		"content": content.String(),
	}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if len(toolOrder) > 0 {
		calls := make([]any, 0, len(toolOrder))
		for _, idx := range toolOrder {
			acc := toolCalls[idx]
			kind := acc.Type
			if kind == "" {
				kind = "function"
			}
			calls = append(calls, map[string]any{
				"id":   acc.ID,
				"type": kind,
				"function": map[string]any{
					"name":      acc.Name,
					"arguments": acc.Arguments.String(),
				},
			})
		}
		message["tool_calls"] = calls
	}

	if finish == "" {
		finish = "stop"
	}
	result := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       message,
				"finish_reason": finish,
			},
		},
	}
	if systemFingerprint != "" {
		result["system_fingerprint"] = systemFingerprint
	}
	if usage != nil {
		result["usage"] = usage
	}
	return result, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	out, err := json.Marshal(payload)
	if err != nil {
		out = []byte("{\"error\":{\"message\":\"encode failed\",\"type\":\"proxy_error\"}}")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(out)
}

func proxyError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "proxy_error",
		},
	})
}

func (s *Server) maybeMirror(ctx context.Context, cfg *Config, primary *Upstream, kind endpointKind, upstreamPath string, payload []byte, outcome ruleOutcome, model string) {
	if !cfg.Server.MirrorEnabled || cfg.Server.MirrorRatio <= 0 {
		return
	}
	if len(cfg.Server.MirrorModels) > 0 {
		matched := false
		for _, pattern := range cfg.Server.MirrorModels {
			if matchModelPattern(pattern, model) {
				matched = true
				break
			}
		}
		if !matched {
			return
		}
	}
	if !s.store.ShouldMirror() {
		return
	}
	target := cfg.Server.MirrorUpstream
	if primary != nil && primary.ID == target {
		return
	}
	var mirror *Upstream
	for _, u := range s.store.effectiveUpstreams() {
		if u.ID == target {
			cp := u
			mirror = &cp
			break
		}
	}
	if mirror == nil {
		s.store.Warn("镜像上游 %s 不存在，跳过镜像", target)
		return
	}
	key := ""
	if len(mirror.APIKeys) > 0 {
		key = mirror.APIKeys[0]
	}
	body := append([]byte(nil), payload...)
	headers := map[string]string{}
	for k, v := range outcome.Headers {
		headers[k] = v
	}
	timeout := time.Duration(cfg.Server.MirrorTimeoutMS) * time.Millisecond
	go func() {
		mctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		resp, err := s.forward(mctx, mirror, kind, upstreamPath, body, key, headers)
		if err != nil {
			s.store.RecordMirror(mirror.ID, false)
			s.store.Warn("镜像投递失败 %s：%v", mirror.Name, err)
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
		resp.Body.Close()
		ok := resp.StatusCode >= 200 && resp.StatusCode < 400
		s.store.RecordMirror(mirror.ID, ok)
		if tokens, has := extractUsageTokens(raw); has {
			s.store.AddTokenUsage(mirror.ID, tokens)
		}
	}()
	_ = ctx
}
