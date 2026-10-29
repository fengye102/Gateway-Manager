package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func startGateway(t *testing.T, cfg *Config) *httptest.Server {
	t.Helper()
	store := NewStore(cfg, "")
	srv := NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postJSON(t *testing.T, url string, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func TestChatStrategyInjectionAndMapping(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("上游路径错误: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","object":"chat.completion","choices":[]}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{
		ID: "up1", Name: "本地上游", BaseURL: upstream.URL,
		Models: []string{"m1"}, Mappings: map[string]string{"fast": "m1"}, Enabled: true,
	}}
	cfg.Profiles = []Profile{{ID: "p1", Name: "默认策略", Instruction: "你是策略文本", Enabled: true}}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions",
		`{"model":"fast","messages":[{"role":"user","content":"hi"}]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200，得到 %d: %s", resp.StatusCode, body)
	}
	if got := jsonStringField(received, "model"); got != "m1" {
		t.Fatalf("模型别名未生效: %q", got)
	}
	msgs, ok := received["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages 结构不对: %#v", received["messages"])
	}
	head, _ := msgs[0].(map[string]any)
	if jsonStringField(head, "role") != "system" || jsonStringField(head, "content") != "你是策略文本" {
		t.Fatalf("策略未注入到头部: %#v", head)
	}
}

func TestResponsesAndMessagesInjection(t *testing.T) {
	var lastBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastBody = map[string]any{}
		_ = json.Unmarshal(body, &lastBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	cfg.Profiles = []Profile{{ID: "p", Name: "P", Instruction: "INS", Enabled: true}}
	gw := startGateway(t, cfg)

	if resp, body := postJSON(t, gw.URL+"/v1/responses", `{"model":"x","input":"hi"}`, nil); resp.StatusCode != 200 {
		t.Fatalf("responses 失败 %d: %s", resp.StatusCode, body)
	}
	if got := jsonStringField(lastBody, "instructions"); got != "INS" {
		t.Fatalf("instructions 未注入: %q", got)
	}

	if resp, body := postJSON(t, gw.URL+"/v1/responses", `{"model":"x","instructions":"原值"}`, nil); resp.StatusCode != 200 {
		t.Fatalf("responses 失败 %d: %s", resp.StatusCode, body)
	}
	if got := jsonStringField(lastBody, "instructions"); got != "INS\n\n原值" {
		t.Fatalf("instructions 拼接错误: %q", got)
	}

	if resp, body := postJSON(t, gw.URL+"/v1/messages", `{"model":"x","system":"原系统","messages":[]}`, nil); resp.StatusCode != 200 {
		t.Fatalf("messages 失败 %d: %s", resp.StatusCode, body)
	}
	if got := jsonStringField(lastBody, "system"); got != "INS\n\n原系统" {
		t.Fatalf("system 拼接错误: %q", got)
	}

	if resp, body := postJSON(t, gw.URL+"/v1/messages", `{"model":"x","system":[{"type":"text","text":"旧"}],"messages":[]}`, nil); resp.StatusCode != 200 {
		t.Fatalf("messages 失败 %d: %s", resp.StatusCode, body)
	}
	arr, ok := lastBody["system"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("system 数组注入失败: %#v", lastBody["system"])
	}
	last, _ := arr[1].(map[string]any)
	if jsonStringField(last, "text") != "旧" {
		t.Fatalf("system 数组原有内容被破坏: %#v", last)
	}
}

func TestFailoverToSecondUpstream(t *testing.T) {
	var hits int
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","choices":[]}`)
	}))
	defer good.Close()

	cfg := DefaultConfig()
	cfg.Retry.MaxRetries = 2
	cfg.Upstreams = []Upstream{
		{ID: "a", Name: "坏上游", BaseURL: bad.URL, Priority: 0, Enabled: true},
		{ID: "b", Name: "好上游", BaseURL: good.URL, Priority: 1, Enabled: true},
	}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"any","messages":[]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("期望 failover 成功，得到 %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "chat.completion") {
		t.Fatalf("响应不是来自好上游: %s", body)
	}
	if hits == 0 {
		t.Fatal("坏上游没有被尝试")
	}
}

func TestAuthRequired(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Server.AccessKeys = []string{"secret"}
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 key 期望 401，得到 %d: %s", resp.StatusCode, body)
	}

	resp, body = postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`,
		map[string]string{"Authorization": "Bearer secret"})
	if resp.StatusCode != 200 {
		t.Fatalf("带 key 期望 200，得到 %d: %s", resp.StatusCode, body)
	}
}

func TestSSEMergedWhenClientNotStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`data: {"id":"c1","model":"m","created":1,"choices":[{"index":0,"delta":{"role":"assistant","content":"你"}}]}`,
			`data: {"id":"c1","model":"m","created":1,"choices":[{"index":0,"delta":{"content":"好"}}]}`,
			`data: {"id":"c1","model":"m","created":1,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			_, _ = io.WriteString(w, c+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Server.MergeSSE = true
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","stream":false,"messages":[]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200，得到 %d: %s", resp.StatusCode, body)
	}
	var merged map[string]any
	if err := json.Unmarshal([]byte(body), &merged); err != nil {
		t.Fatalf("合并结果不是 JSON: %v / %s", err, body)
	}
	choices, _ := merged["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices 数量异常: %s", body)
	}
	first, _ := choices[0].(map[string]any)
	msg, _ := first["message"].(map[string]any)
	if jsonStringField(msg, "content") != "你好" {
		t.Fatalf("SSE 合并内容错误: %s", body)
	}
}

func TestStreamPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","stream":true,"messages":[]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200，得到 %d", resp.StatusCode)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("流式透传内容不对: %s", body)
	}
}

func TestModelsAggregation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{
		ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true,
		Models:   []string{"m1", "m2"},
		Mappings: map[string]string{"alias": "m1"},
	}}
	gw := startGateway(t, cfg)

	resp, err := http.Get(gw.URL + "/v1/models")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var payload struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	ids := map[string]bool{}
	for _, d := range payload.Data {
		ids[d.ID] = true
	}
	for _, want := range []string{"m1", "m2", "alias"} {
		if !ids[want] {
			t.Fatalf("模型列表缺少 %s: %s", want, string(raw))
		}
	}
}

func TestResolveModelAndProfileSelection(t *testing.T) {
	u := &Upstream{ID: "abc", Models: []string{"m1"}, Mappings: map[string]string{"alias": "m1"}}
	if got, ok := resolveModel(u, "alias"); !ok || got != "m1" {
		t.Fatalf("别名解析错误: %q %v", got, ok)
	}
	if got, ok := resolveModel(u, "abc/m1"); !ok || got != "m1" {
		t.Fatalf("前缀路由解析错误: %q %v", got, ok)
	}
	if _, ok := resolveModel(u, "other"); ok {
		t.Fatal("不存在的模型不应命中")
	}
	open := &Upstream{ID: "open"}
	if got, ok := resolveModel(open, "anything"); !ok || got != "anything" {
		t.Fatalf("空模型列表应放行: %q %v", got, ok)
	}

	profiles := []Profile{
		{ID: "default", Instruction: "D", Enabled: true},
		{ID: "coder", Models: []string{"m1"}, Instruction: "C", Enabled: true},
		{ID: "off", Instruction: "O", Enabled: false},
	}
	if p := selectProfile(profiles, "coder", "x"); p == nil || p.ID != "coder" {
		t.Fatalf("优先按 ID 选择失败")
	}
	if p := selectProfile(profiles, "", "m1"); p == nil || p.ID != "coder" {
		t.Fatalf("按模型匹配失败")
	}
	if p := selectProfile(profiles, "", "zzz"); p == nil || p.ID != "default" {
		t.Fatalf("回退默认策略失败")
	}
}

func TestCanonicalPathAndJoinURL(t *testing.T) {
	if got := canonicalAPIPath("/chat/completions"); got != "/v1/chat/completions" {
		t.Fatalf("补齐 /v1 失败: %s", got)
	}
	if got := canonicalAPIPath("/v1/models"); got != "/v1/models" {
		t.Fatalf("已有前缀被改动: %s", got)
	}
	if got := joinURL("http://127.0.0.1:8080", "/v1/models"); got != "http://127.0.0.1:8080/v1/models" {
		t.Fatalf("拼接失败: %s", got)
	}
	if got := joinURL("http://x/v1/", "/v1/chat/completions"); got != "http://x/v1/chat/completions" {
		t.Fatalf("重复 /v1 未消除: %s", got)
	}
}

func TestKeyPoolScoreAndCooldown(t *testing.T) {
	pool := newKeyPool([]string{"key-a", "key-b"}, "score", 10*time.Second)
	if pool.size() != 2 {
		t.Fatalf("key 池大小错误: %d", pool.size())
	}

	first, ok := pool.pick()
	if !ok || first == nil {
		t.Fatal("首次应当取到 key")
	}
	pool.report(first, 200, 12*time.Millisecond, "")
	if first.score <= keyScoreInitial {
		t.Fatalf("成功应当提高评分: %v", first.score)
	}

	pool.report(first, 401, 5*time.Millisecond, "unauthorized")
	pool.report(first, 401, 5*time.Millisecond, "unauthorized")
	if first.score > 30 {
		t.Fatalf("401 应当重罚: %v", first.score)
	}
	if first.cooldownTill.IsZero() || !time.Now().Before(first.cooldownTill) {
		t.Fatal("连续失败应当进入冷却")
	}

	second, ok := pool.pick()
	if !ok || second == nil {
		t.Fatal("另一个 key 应当仍可用")
	}
	if second.key == first.key {
		t.Fatal("冷却中的 key 不应再被选中")
	}

	views := pool.snapshot()
	if len(views) != 2 {
		t.Fatalf("快照数量错误: %d", len(views))
	}
	if views[0].Score < views[1].Score {
		t.Fatalf("快照应按评分降序: %+v", views)
	}
	if !strings.Contains(views[0].Masked, "…") && len(views[0].Masked) == 0 {
		t.Fatalf("mask 失败: %q", views[0].Masked)
	}
}

func TestKeyRotationOnAuthFailure(t *testing.T) {
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		used = append(used, auth)
		if auth == "Bearer bad-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"bad key"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","choices":[]}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{
		ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true,
		APIKeys: []string{"bad-key", "good-key"},
	}}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("期望换 key 后成功，得到 %d: %s", resp.StatusCode, body)
	}
	if len(used) < 2 {
		t.Fatalf("应当尝试过两个 key: %v", used)
	}
	if used[len(used)-1] != "Bearer good-key" {
		t.Fatalf("最后一次应当使用好 key: %v", used)
	}
}

func TestRuleDenyBlocksRequest(t *testing.T) {
	var hit bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	cfg.Rules = []Rule{{
		ID: "r1", Name: "拦截 blocked", Type: ruleTypeDeny,
		Cond: &RuleCond{Models: []string{"blocked"}}, Status: 451, Message: "blocked by rule",
	}}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"blocked","messages":[]}`, nil)
	if resp.StatusCode != 451 {
		t.Fatalf("期望 451，得到 %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "blocked by rule") {
		t.Fatalf("提示信息缺失: %s", body)
	}
	if hit {
		t.Fatal("被拦截的请求不应到达上游")
	}
}

func TestRuleInjectOverridesProfileInjection(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	cfg.Profiles = []Profile{{ID: "p", Name: "P", Instruction: "PROFILE-INS", Enabled: true}}
	cfg.Rules = []Rule{{
		ID: "r1", Name: "规则注入", Type: ruleTypeInject,
		Cond: &RuleCond{Models: []string{"mini"}}, Instruction: "RULE-INS",
	}}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions",
		`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200，得到 %d: %s", resp.StatusCode, body)
	}
	msgs, _ := received["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("规则注入后不应再叠加策略注入: %#v", received["messages"])
	}
	head, _ := msgs[0].(map[string]any)
	if jsonStringField(head, "content") != "RULE-INS" {
		t.Fatalf("规则注入内容错误: %#v", head)
	}
}

func TestRuleOverrideParamsModelAndHeader(t *testing.T) {
	var received map[string]any
	var header string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("X-Test")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	cfg.Rules = []Rule{
		{
			ID: "r1", Name: "改写模型与参数", Type: ruleTypeOverride,
			Cond:      &RuleCond{Models: []string{"cheap"}},
			SetModel:  "renamed-model",
			SetParams: map[string]any{"temperature": 0.2, "max_tokens": float64(64)},
		},
		{
			ID: "r2", Name: "附加请求头", Type: ruleTypeHeader,
			HeaderName: "X-Test", HeaderValue: "1",
		},
	}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"cheap","messages":[]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200，得到 %d: %s", resp.StatusCode, body)
	}
	if got := jsonStringField(received, "model"); got != "renamed-model" {
		t.Fatalf("模型改写未生效: %q", got)
	}
	if got, ok := received["temperature"].(float64); !ok || got != 0.2 {
		t.Fatalf("参数改写未生效: %#v", received["temperature"])
	}
	if header != "1" {
		t.Fatalf("请求头未附加: %q", header)
	}
}

func TestEvaluateRulesConditions(t *testing.T) {
	rules := []Rule{
		{ID: "a", Type: ruleTypeInject, Cond: &RuleCond{Models: []string{"mini"}}, Instruction: "A"},
		{ID: "b", Type: ruleTypeInject, Cond: &RuleCond{Paths: []string{"/v1/messages"}}, Instruction: "B"},
		{ID: "c", Type: ruleTypeInject, Disabled: true, Instruction: "C"},
		{ID: "d", Type: ruleTypeDeny, Cond: &RuleCond{BodyContains: "forbidden"}, Status: 400, Message: "no"},
	}
	out := evaluateRules(rules, nil, ruleContext{Model: "gpt-4o-mini", Path: "/v1/chat/completions", Body: `{"messages":[]}`})
	if strings.Contains(out.Instruction, "B") || strings.Contains(out.Instruction, "C") {
		t.Fatalf("路径不匹配/停用的规则不应命中: %q", out.Instruction)
	}
	if !strings.Contains(out.Instruction, "A") {
		t.Fatalf("模型条件未命中: %q", out.Instruction)
	}
	if out.Denied {
		t.Fatal("不应被拦截")
	}

	out2 := evaluateRules(rules, nil, ruleContext{Model: "x", Path: "/v1/messages", Body: "forbidden text"})
	if !out2.Denied || out2.Status != 400 {
		t.Fatalf("body_contains 拦截未生效: %+v", out2)
	}
	if !strings.Contains(out2.Instruction, "B") {
		t.Fatalf("messages 路径规则未命中: %q", out2.Instruction)
	}
}

func TestTemplatesBuiltinAndUpsert(t *testing.T) {
	builtin := builtinTemplates()
	if len(builtin) < 10 {
		t.Fatalf("内置模板太少: %d", len(builtin))
	}
	for _, tpl := range builtin {
		if !tpl.Builtin || tpl.Instruction == "" || tpl.ID == "" {
			t.Fatalf("内置模板字段不完整: %+v", tpl)
		}
	}

	custom, err := upsertTemplate(nil, ProfileTemplate{ID: "tpl-mine", Name: "我的", Instruction: "指令"})
	if err != nil {
		t.Fatalf("新增自定义模板失败: %v", err)
	}
	if len(custom) != 1 {
		t.Fatalf("自定义模板数量错误: %d", len(custom))
	}
	if _, err := upsertTemplate(nil, ProfileTemplate{ID: "tpl-coder", Instruction: "x"}); err == nil {
		t.Fatal("覆盖内置模板应当报错")
	}
	if _, err := upsertTemplate(nil, ProfileTemplate{ID: "noinst"}); err == nil {
		t.Fatal("空指令应当报错")
	}
	if _, ok := findTemplate(custom, "tpl-rigor"); !ok {
		t.Fatal("应当能查到内置模板")
	}
	custom = removeTemplate(custom, "tpl-mine")
	if len(custom) != 0 {
		t.Fatalf("删除模板失败: %d", len(custom))
	}
}

func TestAdminRulesDryRun(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Profiles = []Profile{{ID: "p", Name: "P", Instruction: "PROFILE", Enabled: true}}
	cfg.Rules = []Rule{
		{ID: "r1", Name: "注入", Type: ruleTypeInject, Cond: &RuleCond{Models: []string{"mini"}}, Instruction: "RULE"},
		{ID: "r2", Name: "拦截", Type: ruleTypeDeny, Cond: &RuleCond{Models: []string{"nope"}}, Status: 403, Message: "denied"},
	}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/admin/api/rules/test", `{"model":"gpt-4o-mini","path":"/v1/chat/completions","body":{"messages":[{"role":"user","content":"hi"}]}}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("试算失败 %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Matched     []string `json:"matched"`
		Denied      bool     `json:"denied"`
		Instruction string   `json:"instruction"`
		PreviewBody string   `json:"preview_body"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("试算响应不是 JSON: %v", err)
	}
	if out.Denied || out.Instruction != "RULE" {
		t.Fatalf("试算结果错误: %s", body)
	}
	if !strings.Contains(out.PreviewBody, "RULE") {
		t.Fatalf("预览体里没有注入内容: %s", out.PreviewBody)
	}

	resp, body = postJSON(t, gw.URL+"/admin/api/rules/test", `{"model":"nope","path":"/v1/chat/completions","body":{}}`, nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "denied") {
		t.Fatalf("拦截规则试算未生效: %s", body)
	}
}

func TestAdminTemplateApplyCreatesProfile(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: "http://127.0.0.1:1", Enabled: true}}
	store := NewStore(cfg, "")
	srv := NewServer(store)
	gw := httptest.NewServer(srv.Handler())
	defer gw.Close()

	resp, body := postJSON(t, gw.URL+"/admin/api/templates/apply",
		`{"template_id":"tpl-coder","mode":"new","profile_id":"from-tpl","name":"来自模板"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("应用模板失败 %d: %s", resp.StatusCode, body)
	}
	profiles := store.Config().Profiles
	if len(profiles) != 1 || profiles[0].ID != "from-tpl" {
		t.Fatalf("策略未创建: %+v", profiles)
	}
	if !strings.Contains(profiles[0].Instruction, "资深工程师") {
		t.Fatalf("策略内容不是模板内容: %q", profiles[0].Instruction)
	}

	resp, body = postJSON(t, gw.URL+"/admin/api/templates/apply",
		`{"template_id":"tpl-coder","mode":"new","profile_id":"from-tpl"}`, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("重复 id 应当 400，得到 %d: %s", resp.StatusCode, body)
	}

	resp, body = postJSON(t, gw.URL+"/admin/api/rules", `{"rules":[{"id":"x","name":"X","type":"header","header_name":"X-A","header_value":"1"}]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("保存规则失败 %d: %s", resp.StatusCode, body)
	}
	if len(store.Config().Rules) != 1 {
		t.Fatalf("规则未落库: %+v", store.Config().Rules)
	}
}

func TestKeyPoolExportRestore(t *testing.T) {
	pool := newKeyPool([]string{"key-one", "key-two"}, "score", 30*time.Second)
	first, ok := pool.pick()
	if !ok || first == nil {
		t.Fatal("应当取到 key")
	}
	pool.report(first, 401, 3*time.Millisecond, "unauthorized")
	pool.report(first, 401, 3*time.Millisecond, "unauthorized")

	exported := pool.export()
	if len(exported) != 2 {
		t.Fatalf("导出数量错误: %d", len(exported))
	}
	var bad keyPersist
	for _, item := range exported {
		if item.FP == keyFingerprint("key-one") || item.FP == keyFingerprint("key-two") {
			if item.Failures == 2 {
				bad = item
			}
		}
	}
	if bad.FP == "" {
		t.Fatalf("没有导出失败状态: %+v", exported)
	}
	if bad.CooldownUntil == 0 {
		t.Fatal("冷却时间未导出")
	}

	fresh := newKeyPool([]string{"key-one", "key-two"}, "score", 30*time.Second)
	if applied := fresh.restore(exported); applied != 2 {
		t.Fatalf("恢复数量错误: %d", applied)
	}
	views := fresh.snapshot()
	lowest := views[len(views)-1]
	if lowest.Score >= keyScoreInitial {
		t.Fatalf("坏 key 评分应当被恢复为降权状态: %+v", views)
	}

	stranger := newKeyPool([]string{"other-key"}, "score", 30*time.Second)
	if applied := stranger.restore(exported); applied != 0 {
		t.Fatalf("指纹不匹配不应恢复: %d", applied)
	}
}

func TestStatePersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gateway-manager.state.json")

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{
		ID: "u", Name: "U", BaseURL: "http://127.0.0.1:1", Enabled: true,
		APIKeys: []string{"sk-alpha", "sk-beta"},
	}}

	store := NewStore(cfg, "")
	store.SetStatePath(statePath)
	pool := store.keyPoolFor(&cfg.Upstreams[0])
	key, ok := pool.pick()
	if !ok || key == nil {
		t.Fatal("应当取到 key")
	}
	pool.report(key, 403, 4*time.Millisecond, "forbidden")
	pool.report(key, 403, 4*time.Millisecond, "forbidden")
	store.RecordRuleHits([]string{"r1"}, []string{"r2"}, true)

	if err := store.SaveState(); err != nil {
		t.Fatalf("保存状态失败: %v", err)
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("状态文件未生成: %v", err)
	}
	if strings.Contains(string(raw), "sk-alpha") || strings.Contains(string(raw), "sk-beta") {
		t.Fatal("状态文件不应包含明文 key")
	}

	reloaded := NewStore(cfg, "")
	reloaded.SetStatePath(statePath)
	if err := reloaded.LoadState(); err != nil {
		t.Fatalf("加载状态失败: %v", err)
	}
	views := reloaded.keyPoolFor(&cfg.Upstreams[0]).snapshot()
	demoted := false
	for _, v := range views {
		if v.Score < keyScoreInitial {
			demoted = true
		}
	}
	if !demoted {
		t.Fatalf("key 评分未恢复: %+v", views)
	}
	stats := reloaded.RuleStatsSnapshot()
	if hit, ok := stats["r1"]; !ok || hit.Matched != 1 || hit.Denied != 1 {
		t.Fatalf("规则统计未恢复: %+v", stats)
	}
	if hit, ok := stats["r2"]; !ok || hit.Skipped != 1 {
		t.Fatalf("跳过统计未恢复: %+v", stats)
	}
}

func TestRuleHitStatsEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	cfg.Rules = []Rule{
		{ID: "r-deny", Name: "拦截", Type: ruleTypeDeny, Cond: &RuleCond{Models: []string{"bad"}}, Status: 403, Message: "no"},
		{ID: "r-any", Name: "总是命中", Type: ruleTypeMatch},
	}
	gw := startGateway(t, cfg)

	if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"bad","messages":[]}`, nil); resp.StatusCode != 403 {
		t.Fatalf("期望拦截 403，得到 %d: %s", resp.StatusCode, body)
	}
	if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"ok","messages":[]}`, nil); resp.StatusCode != 200 {
		t.Fatalf("期望 200，得到 %d: %s", resp.StatusCode, body)
	}

	res, err := http.Get(gw.URL + "/admin/api/rules/stats")
	if err != nil {
		t.Fatalf("读取统计失败: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var payload struct {
		Stats map[string]ruleHitState `json:"stats"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("统计响应不是 JSON: %v / %s", err, string(body))
	}
	if got := payload.Stats["r-deny"]; got.Denied != 1 {
		t.Fatalf("拦截计数错误: %+v (body=%s)", got, string(body))
	}
	if got := payload.Stats["r-any"]; got.Matched != 1 {
		t.Fatalf("命中计数错误: %+v (body=%s)", got, string(body))
	}
	if got := payload.Stats["r-deny"]; got.Matched != 1 || got.Skipped != 1 {
		t.Fatalf("拦截节点的命中的跳过计数错误: %+v (body=%s)", got, string(body))
	}

	req, _ := http.NewRequest(http.MethodDelete, gw.URL+"/admin/api/rules/stats", nil)
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("清零失败: %v", err)
	}
	res2.Body.Close()

	res3, _ := http.Get(gw.URL + "/admin/api/rules/stats")
	body3, _ := io.ReadAll(res3.Body)
	res3.Body.Close()
	if strings.Contains(string(body3), "\"matched\": 2") {
		t.Fatalf("统计未清零: %s", string(body3))
	}
}

func TestMetricsEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{
		ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true,
		APIKeys: []string{"sk-test-1234"},
	}}
	cfg.Rules = []Rule{{ID: "r1", Name: "R1", Type: ruleTypeMatch}}
	gw := startGateway(t, cfg)

	for i := 0; i < 2; i++ {
		if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil); resp.StatusCode != 200 {
			t.Fatalf("请求失败 %d: %s", resp.StatusCode, body)
		}
	}

	res, err := http.Get(gw.URL + "/metrics")
	if err != nil {
		t.Fatalf("读取指标失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	text := string(raw)
	maskedKey := maskKey("sk-test-1234")
	expects := []string{
		"gateway_manager_info{",
		"gateway_manager_key_score{upstream=\"u\",key=\"" + maskedKey + "\"}",
		"gateway_manager_requests_total 2",
		"gateway_manager_upstream_healthy{upstream=\"u\",name=\"U\"} 1",
		"gateway_manager_upstream_successes_total{upstream=\"u\",name=\"U\"} 2",
		"gateway_manager_rule_matched_total{rule=\"r1\",name=\"R1\"} 2",
		"gateway_manager_config_rules 1",
		"gateway_manager_snapshots ",
	}
	for _, want := range expects {
		if !strings.Contains(text, want) {
			t.Fatalf("指标缺少 %q\n%s", want, text[:min(len(text), 1200)])
		}
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("Content-Type 不对: %s", ct)
	}

	cfg2 := DefaultConfig()
	cfg2.Server.AdminToken = "mtok"
	cfg2.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	gw2 := startGateway(t, cfg2)
	unauth, _ := http.Get(gw2.URL + "/metrics")
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("设置了 admin_token 时无 token 应 401，得到 %d", unauth.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, gw2.URL+"/metrics", nil)
	req.Header.Set("X-Admin-Token", "mtok")
	okRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("带 token 请求失败: %v", err)
	}
	okRes.Body.Close()
	if okRes.StatusCode != 200 {
		t.Fatalf("带 token 应 200，得到 %d", okRes.StatusCode)
	}
}

func TestSnapshotCreateAndRollback(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	base := DefaultConfig()
	base.Upstreams = []Upstream{{ID: "old", Name: "旧上游", BaseURL: "http://127.0.0.1:1", Enabled: true}}
	if err := SaveConfig(cfgPath, base); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	store := NewStore(base, cfgPath)
	srv := NewServer(store)
	gw := httptest.NewServer(srv.Handler())
	defer gw.Close()

	resp, body := postJSON(t, gw.URL+"/admin/api/snapshots/create", `{"note":"initial"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("创建快照失败 %d: %s", resp.StatusCode, body)
	}
	var created struct {
		Snapshot SnapshotMeta `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("快照响应解析失败: %v", err)
	}
	if created.Snapshot.Name == "" || !strings.HasSuffix(created.Snapshot.Name, ".json") {
		t.Fatalf("快照名异常: %+v", created.Snapshot)
	}
	if created.Snapshot.Upstreams != 1 {
		t.Fatalf("快照统计错误: %+v", created.Snapshot)
	}

	next := DefaultConfig()
	next.Upstreams = []Upstream{
		{ID: "new1", Name: "新1", BaseURL: "http://127.0.0.1:2", Enabled: true},
		{ID: "new2", Name: "新2", BaseURL: "http://127.0.0.1:3", Enabled: true},
	}
	payload, _ := json.Marshal(next)
	resp, body = postJSON(t, gw.URL+"/admin/api/config", string(payload), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("更新配置失败 %d: %s", resp.StatusCode, body)
	}
	if got := len(store.Config().Upstreams); got != 2 {
		t.Fatalf("配置未更新: %d", got)
	}

	res, err := http.Get(gw.URL + "/admin/api/snapshots")
	if err != nil {
		t.Fatalf("列出快照失败: %v", err)
	}
	listBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var listed struct {
		Snapshots []SnapshotMeta `json:"snapshots"`
	}
	if err := json.Unmarshal(listBody, &listed); err != nil {
		t.Fatalf("快照列表不是 JSON: %v", err)
	}
	if len(listed.Snapshots) < 2 {
		t.Fatalf("自动快照未生成: %+v", listed.Snapshots)
	}

	rollbackBody, _ := json.Marshal(map[string]string{"name": created.Snapshot.Name})
	resp, body = postJSON(t, gw.URL+"/admin/api/snapshots/rollback", string(rollbackBody), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("回滚失败 %d: %s", resp.StatusCode, body)
	}
	restored := store.Config()
	if len(restored.Upstreams) != 1 || restored.Upstreams[0].ID != "old" {
		t.Fatalf("回滚结果不对: %+v", restored.Upstreams)
	}

	badBody, _ := json.Marshal(map[string]string{"name": "../evil.json"})
	resp, body = postJSON(t, gw.URL+"/admin/api/snapshots/rollback", string(badBody), nil)
	if resp.StatusCode != 400 {
		t.Fatalf("非法快照名应被拒绝，得到 %d: %s", resp.StatusCode, body)
	}
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestHistogramAndEndpointMetrics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	gw := startGateway(t, cfg)

	if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil); resp.StatusCode != 200 {
		t.Fatalf("请求失败 %d: %s", resp.StatusCode, body)
	}

	res, err := http.Get(gw.URL + "/metrics")
	if err != nil {
		t.Fatalf("读取指标失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	text := string(raw)

	expects := []string{
		"# TYPE gateway_manager_request_duration_seconds histogram",
		"gateway_manager_request_duration_seconds_bucket{le=\"0.05\"}",
		"gateway_manager_request_duration_seconds_sum ",
		"gateway_manager_request_duration_seconds_count 1",
		"gateway_manager_upstream_duration_seconds_bucket{upstream=\"u\",name=\"U\",le=\"0.05\"}",
		"gateway_manager_upstream_responses_total{upstream=\"u\",name=\"U\",code=\"200\"} 1",
		"gateway_manager_requests_by_endpoint_total{endpoint=\"chat.completions\"} 1",
		"gateway_manager_replay_entries 0",
	}
	for _, want := range expects {
		if !strings.Contains(text, want) {
			t.Fatalf("指标缺少 %q\n%s", want, text[:min(len(text), 1500)])
		}
	}
	if !strings.Contains(text, "gateway_manager_request_duration_seconds_bucket{le=\"+Inf\"} 1") {
		t.Fatalf("+Inf 桶缺失或被计错")
	}
}

func TestReplayRecordsDeniedRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	cfg.Rules = []Rule{{
		ID: "r-deny", Name: "拦截危险模型", Type: ruleTypeDeny,
		Cond: &RuleCond{Models: []string{"danger"}}, Status: 451, Message: "blocked",
	}}
	gw := startGateway(t, cfg)

	if resp, body := postJSON(t, gw.URL+"/v1/chat/completions",
		`{"model":"danger","messages":[{"role":"user","content":"hello"}]}`,
		map[string]string{"Authorization": "Bearer super-secret"}); resp.StatusCode != 451 {
		t.Fatalf("期望 451，得到 %d: %s", resp.StatusCode, body)
	}

	res, err := http.Get(gw.URL + "/admin/api/replay")
	if err != nil {
		t.Fatalf("读取回放失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var listed struct {
		Entries []ReplayEntry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("回放列表不是 JSON: %v / %s", err, string(raw))
	}
	if len(listed.Entries) != 1 {
		t.Fatalf("回放条数错误: %d (%s)", len(listed.Entries), string(raw))
	}
	entry := listed.Entries[0]
	if !strings.Contains(entry.Body, "hello") {
		t.Fatalf("请求体未记录: %q", entry.Body)
	}
	if entry.Model != "danger" || entry.Status != 451 {
		t.Fatalf("回放元信息错误: %+v", entry)
	}
	for name, value := range entry.Headers {
		if strings.EqualFold(name, "Authorization") && value != "***" {
			t.Fatalf("敏感请求头未脱敏: %s=%s", name, value)
		}
	}
	if len(entry.Rules) == 0 || entry.Rules[0] != "r-deny" {
		t.Fatalf("命中规则未记录: %+v", entry.Rules)
	}

	body, _ := json.Marshal(map[string]string{"id": entry.ID})
	resp, replayBody := postJSON(t, gw.URL+"/admin/api/replay/test", string(body), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("复现失败 %d: %s", resp.StatusCode, replayBody)
	}
	var replayOut struct {
		Matched []string `json:"matched"`
		Denied  bool     `json:"denied"`
		Status  int      `json:"status"`
	}
	if err := json.Unmarshal([]byte(replayBody), &replayOut); err != nil {
		t.Fatalf("复现响应不是 JSON: %v", err)
	}
	if !replayOut.Denied || replayOut.Status != 451 {
		t.Fatalf("复现结果错误: %s", replayBody)
	}

	req, _ := http.NewRequest(http.MethodDelete, gw.URL+"/admin/api/replay", nil)
	delRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	delRes.Body.Close()
	res2, _ := http.Get(gw.URL + "/admin/api/replay")
	raw2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if strings.Contains(string(raw2), `\"id\":\"rp-`) {
		t.Fatalf("回放未清空: %s", string(raw2))
	}
}

func TestGrafanaDashboardFileValid(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("deploy", "grafana-dashboard.json"))
	if err != nil {
		t.Fatalf("看板文件缺失: %v", err)
	}
	var dash struct {
		Title  string   `json:"title"`
		UID    string   `json:"uid"`
		Tags   []string `json:"tags"`
		Panels []struct {
			ID      int    `json:"id"`
			Title   string `json:"title"`
			Type    string `json:"type"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
		Templating struct {
			List []struct {
				Name string `json:"name"`
			} `json:"list"`
		} `json:"templating"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatalf("看板 JSON 解析失败: %v", err)
	}
	if dash.UID != "gateway-manager-main" || dash.Title == "" {
		t.Fatalf("看板元信息错误: uid=%q title=%q", dash.UID, dash.Title)
	}
	if len(dash.Panels) < 8 {
		t.Fatalf("面板太少: %d", len(dash.Panels))
	}
	for _, p := range dash.Panels {
		if p.ID == 0 || p.Title == "" || p.Type == "" {
			t.Fatalf("面板字段不完整: %+v", p)
		}
		if len(p.Targets) == 0 || !strings.Contains(p.Targets[0].Expr, "gateway_manager_") {
			t.Fatalf("面板 %s 缺少 gateway_manager_ 查询: %+v", p.Title, p.Targets)
		}
	}
	if len(dash.Templating.List) != 1 || dash.Templating.List[0].Name != "DS" {
		t.Fatalf("数据源变量缺失: %+v", dash.Templating.List)
	}
	if _, err := os.Stat(filepath.Join("deploy", "prometheus.yml")); err != nil {
		t.Fatalf("prometheus 抓取示例缺失: %v", err)
	}
}

func TestWeightedSplitAndRoutingMetrics(t *testing.T) {
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"a"}`)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"b"}`)
	}))
	defer b.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{
		{ID: "canary", Name: "灰度", BaseURL: a.URL, Priority: 1, Weight: 1, Enabled: true},
		{ID: "stable", Name: "稳定", BaseURL: b.URL, Priority: 1, Weight: 9, Enabled: true},
	}
	gw := startGateway(t, cfg)

	const total = 100
	for i := 0; i < total; i++ {
		if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil); resp.StatusCode != 200 {
			t.Fatalf("第 %d 次请求失败 %d: %s", i, resp.StatusCode, body)
		}
	}

	res, err := http.Get(gw.URL + "/metrics")
	if err != nil {
		t.Fatalf("读取指标失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	text := string(raw)

	get := func(prefix string) float64 {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, prefix) {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					var v float64
					_, _ = fmt.Sscanf(fields[len(fields)-1], "%f", &v)
					return v
				}
			}
		}
		return -1
	}
	canary := get(`gateway_manager_routed_total{model="m",upstream="canary"`)
	stable := get(`gateway_manager_routed_total{model="m",upstream="stable"`)
	if canary < 0 || stable < 0 {
		t.Fatalf("分流指标缺失: canary=%v stable=%v", canary, stable)
	}
	if canary+stable != float64(total) {
		t.Fatalf("分流计数总和错误: canary=%v stable=%v", canary, stable)
	}
	ratio := canary / float64(total)
	if ratio < 0.04 || ratio > 0.18 {
		t.Fatalf("灰度比例偏离 10%%: canary=%v/%v = %.2f", canary, total, ratio)
	}
	if !strings.Contains(text, `gateway_manager_upstream_weight{upstream="canary",name="灰度"} 1`) {
		t.Fatalf("权重指标缺失")
	}
}

func TestTimingBreakdownMetrics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	gw := startGateway(t, cfg)

	if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil); resp.StatusCode != 200 {
		t.Fatalf("请求失败 %d: %s", resp.StatusCode, body)
	}
	res, err := http.Get(gw.URL + "/metrics")
	if err != nil {
		t.Fatalf("读取指标失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	text := string(raw)
	for _, want := range []string{
		"# TYPE gateway_manager_queue_duration_seconds histogram",
		"gateway_manager_queue_duration_seconds_count 1",
		"gateway_manager_upstream_ttfb_seconds_count 1",
		"gateway_manager_relay_duration_seconds_count 1",
		"gateway_manager_request_duration_seconds_count 1",
		`gateway_manager_upstream_ttfb_seconds_bucket{le="0.05"} 1`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("缺少 %q", want)
		}
	}
}

func TestAlertRulesFileValid(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("deploy", "prometheus-alerts.yml"))
	if err != nil {
		t.Fatalf("告警规则文件缺失: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "groups:") || strings.Count(text, "- alert:") < 8 {
		t.Fatalf("告警组或条目不足")
	}
	for _, name := range []string{
		"GatewayManagerUpstreamDown", "GatewayManagerAllKeysCooling", "GatewayManagerKeyScoreLow",
		"GatewayManagerUpstreamErrorRatio", "GatewayManagerP95LatencyHigh", "GatewayManagerAuthFailureBurst",
		"GatewayManagerRuleDenySpike", "GatewayManagerInjectConflict", "GatewayManagerStateSaveStale",
	} {
		if !strings.Contains(text, "alert: "+name) {
			t.Fatalf("缺少告警 %s", name)
		}
	}
	exprCount := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "expr:") {
			exprCount++
			if !strings.Contains(trimmed, "gateway_manager_") && !strings.Contains(trimmed, "histogram_quantile") {
				t.Fatalf("表达式未引用指标: %s", trimmed)
			}
		}
	}
	if exprCount < 8 {
		t.Fatalf("expr 数量不足: %d", exprCount)
	}
	if !strings.Contains(text, "severity:") {
		t.Fatalf("缺少 severity 标签")
	}
}

func TestUpstreamProbePath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"healthy","data":[{"id":"probe-model"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{
		ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true,
		Probe: &UpstreamProbe{Path: "/healthz", Method: "GET", ExpectStatus: []int{200}, ExpectContains: "healthy", TimeoutMS: 2000},
	}}
	store := NewStore(cfg, "")
	srv := NewServer(store)
	srv.checkUpstream(context.Background(), &cfg.Upstreams[0])

	state := store.SnapshotUpstreams()[0]
	if !state.Healthy {
		t.Fatalf("自定义探测应通过: %+v", state)
	}
	if len(state.Models) != 1 || state.Models[0] != "probe-model" {
		t.Fatalf("探测应解析出模型: %+v", state.Models)
	}

	cfg2 := DefaultConfig()
	cfg2.Upstreams = []Upstream{{
		ID: "u2", Name: "U2", BaseURL: upstream.URL, Enabled: true,
		Probe: &UpstreamProbe{Path: "/healthz", ExpectContains: "definitely-not-here"},
	}}
	store2 := NewStore(cfg2, "")
	srv2 := NewServer(store2)
	srv2.checkUpstream(context.Background(), &cfg2.Upstreams[0])
	if store2.SnapshotUpstreams()[0].Healthy {
		t.Fatal("expect_contains 不匹配时应判定失败")
	}

	cfg3 := DefaultConfig()
	cfg3.Upstreams = []Upstream{{
		ID: "u3", Name: "U3", BaseURL: upstream.URL, Enabled: true,
		Probe: &UpstreamProbe{Path: "/missing", ExpectStatus: []int{200}},
	}}
	store3 := NewStore(cfg3, "")
	srv3 := NewServer(store3)
	srv3.checkUpstream(context.Background(), &cfg3.Upstreams[0])
	if store3.SnapshotUpstreams()[0].Healthy {
		t.Fatal("路径不存在时应判定失败")
	}
}

func TestSampleArchiveAndReplay(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","choices":[]}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Server.SampleEvery = 1
	cfg.Server.SampleLimit = 5
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	cfg.Rules = []Rule{{ID: "r-tag", Name: "标记", Type: ruleTypeHeader, HeaderName: "X-Gateway", HeaderValue: "gateway-manager"}}
	gw := startGateway(t, cfg)

	for i := 0; i < 2; i++ {
		if resp, body := postJSON(t, gw.URL+"/v1/chat/completions",
			`{"model":"m","messages":[{"role":"user","content":"sample-me"}]}`,
			map[string]string{"Authorization": "Bearer client-secret"}); resp.StatusCode != 200 {
			t.Fatalf("请求失败 %d: %s", resp.StatusCode, body)
		}
	}

	res, err := http.Get(gw.URL + "/admin/api/samples")
	if err != nil {
		t.Fatalf("读取采样失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var listed struct {
		Entries []ReplayEntry `json:"entries"`
		Every   int           `json:"every"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("采样响应不是 JSON: %v / %s", err, string(raw))
	}
	if listed.Every != 1 || len(listed.Entries) != 2 {
		t.Fatalf("采样条数/every 错误: %+v", listed)
	}
	entry := listed.Entries[0]
	if !strings.Contains(entry.Body, "sample-me") {
		t.Fatalf("请求体未采样: %q", entry.Body)
	}
	if entry.Upstream != "u" || entry.Status != 200 {
		t.Fatalf("采样元信息错误: %+v", entry)
	}
	for name, value := range entry.Headers {
		if strings.EqualFold(name, "Authorization") && value != "***" {
			t.Fatalf("采样头未脱敏: %s=%s", name, value)
		}
	}

	body, _ := json.Marshal(map[string]string{"id": entry.ID})
	resp, replayBody := postJSON(t, gw.URL+"/admin/api/samples/test", string(body), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("采样复现失败 %d: %s", resp.StatusCode, replayBody)
	}
	if !strings.Contains(replayBody, "r-tag") {
		t.Fatalf("复现未命中规则: %s", replayBody)
	}

	req, _ := http.NewRequest(http.MethodDelete, gw.URL+"/admin/api/samples", nil)
	delRes, _ := http.DefaultClient.Do(req)
	if delRes != nil {
		delRes.Body.Close()
	}
	res2, _ := http.Get(gw.URL + "/admin/api/samples")
	raw2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if strings.Contains(string(raw2), "sample-me") {
		t.Fatalf("采样未清空: %s", string(raw2))
	}
}

func TestRulesDSLRoundTrip(t *testing.T) {
	rules := []Rule{
		{
			ID: "r-tag", Name: "打标记", Type: ruleTypeHeader,
			HeaderName: "X-Gateway", HeaderValue: "gateway-manager",
		},
		{
			ID: "r-inject", Name: "极简", Type: ruleTypeInject,
			Cond:        &RuleCond{Models: []string{"mini", "micro"}, Paths: []string{"/v1/chat/completions"}, HeaderKey: "X-Client", HeaderValue: "android", BodyContains: "hi"},
			Instruction: "答案控制在 3 句以内",
		},
		{
			ID: "r-ovr", Name: "改写", Type: ruleTypeOverride,
			SetModel: "cheap-model", SetParams: map[string]any{"temperature": 0.2},
		},
		{
			ID: "r-deny", Name: "拦截", Type: ruleTypeDeny, Disabled: true,
			Status: 451, Message: "blocked",
		},
	}
	text := rulesToDSL(rules)
	if !strings.Contains(text, "- id: r-tag") || !strings.Contains(text, "when:") {
		t.Fatalf("序列化结果异常:\n%s", text)
	}

	parsed, err := parseRulesDSL(text)
	if err != nil {
		t.Fatalf("解析失败: %v\n---\n%s", err, text)
	}
	if len(parsed) != len(rules) {
		t.Fatalf("规则数量不一致: %d vs %d", len(parsed), len(rules))
	}
	if parsed[0].Type != ruleTypeHeader || parsed[0].HeaderName != "X-Gateway" || parsed[0].HeaderValue != "gateway-manager" {
		t.Fatalf("header 规则往返错误: %+v", parsed[0])
	}
	if parsed[1].Cond == nil || len(parsed[1].Cond.Models) != 2 || parsed[1].Cond.HeaderKey != "X-Client" ||
		parsed[1].Cond.HeaderValue != "android" || parsed[1].Cond.BodyContains != "hi" || parsed[1].Instruction == "" {
		t.Fatalf("inject 规则往返错误: %+v", parsed[1])
	}
	if parsed[2].SetModel != "cheap-model" || parsed[2].SetParams["temperature"] != 0.2 {
		t.Fatalf("override 规则往返错误: %+v", parsed[2])
	}
	if !parsed[3].Disabled || parsed[3].Status != 451 || parsed[3].Message != "blocked" {
		t.Fatalf("deny 规则往返错误: %+v", parsed[3])
	}

	if _, err := parseRulesDSL("- id: bad\n  when:\n    unknown_field: x"); err == nil {
		t.Fatal("未知字段应当报错")
	} else if !strings.Contains(err.Error(), "行") {
		t.Fatalf("错误应带行号: %v", err)
	}
}

func TestRulesDSLEndpoint(t *testing.T) {
	store := NewStore(DefaultConfig(), "")
	srv := NewServer(store)
	gw := httptest.NewServer(srv.Handler())
	defer gw.Close()

	dsl := "- id: ds-1\n  name: 文本规则\n  type: header\n  action:\n    header_name: X-From\n    header_value: dsl\n"
	payload, _ := json.Marshal(map[string]string{"text": dsl})

	resp, body := postJSON(t, gw.URL+"/admin/api/rules/dsl", string(payload), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("应用 DSL 失败 %d: %s", resp.StatusCode, body)
	}
	rules := store.Config().Rules
	if len(rules) != 1 || rules[0].ID != "ds-1" || rules[0].HeaderName != "X-From" {
		t.Fatalf("DSL 未写入配置: %+v", rules)
	}

	res, err := http.Get(gw.URL + "/admin/api/rules/dsl")
	if err != nil {
		t.Fatalf("读取 DSL 失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(raw), "ds-1") {
		t.Fatalf("GET 未返回规则书: %s", string(raw))
	}

	badPayload, _ := json.Marshal(map[string]string{"text": "- id: x\n  type: unknown-type\n"})
	resp2, body2 := postJSON(t, gw.URL+"/admin/api/rules/dsl", string(badPayload), nil)
	if resp2.StatusCode != 400 {
		t.Fatalf("非法规则类型应 400，得到 %d: %s", resp2.StatusCode, body2)
	}

	previewPayload, _ := json.Marshal(map[string]string{"text": "- id: p1\n  type: match\n"})
	resp3, body3 := postJSON(t, gw.URL+"/admin/api/rules/dsl/preview", string(previewPayload), nil)
	if resp3.StatusCode != 200 || !strings.Contains(body3, "\"ok\":true") {
		t.Fatalf("预览失败 %d: %s", resp3.StatusCode, body3)
	}
}

func TestBudgetExhaustion(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","choices":[]}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{
		ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true,
		Budget: &UpstreamBudget{MaxRequestsPerDay: 2},
	}}
	gw := startGateway(t, cfg)

	for i := 0; i < 2; i++ {
		if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil); resp.StatusCode != 200 {
			t.Fatalf("第 %d 次应成功，得到 %d: %s", i+1, resp.StatusCode, body)
		}
	}
	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil)
	if resp.StatusCode < 500 {
		t.Fatalf("预算用尽应失败，得到 %d: %s", resp.StatusCode, body)
	}

	res, err := http.Get(gw.URL + "/admin/api/status")
	if err != nil {
		t.Fatalf("读取状态失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var status struct {
		Upstreams []struct {
			ID     string      `json:"id"`
			Budget *BudgetView `json:"budget"`
		} `json:"upstreams"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("状态不是 JSON: %v", err)
	}
	if len(status.Upstreams) != 1 || status.Upstreams[0].Budget == nil {
		t.Fatalf("预算视图缺失: %s", string(raw))
	}
	bv := status.Upstreams[0].Budget
	if bv.Requests != 2 || bv.MaxRequests != 2 || !bv.Exhausted {
		t.Fatalf("预算计数错误: %+v", bv)
	}

	met, err := http.Get(gw.URL + "/metrics")
	if err != nil {
		t.Fatalf("读取指标失败: %v", err)
	}
	metRaw, _ := io.ReadAll(met.Body)
	met.Body.Close()
	if !strings.Contains(string(metRaw), `gateway_manager_budget_exhausted{upstream="u",name="U"} 1`) {
		t.Fatalf("预算指标缺失")
	}
	if !strings.Contains(string(metRaw), `gateway_manager_budget_requests_used{upstream="u",name="U"} 2`) {
		t.Fatalf("预算用量指标缺失")
	}
}

func TestFederationFanout(t *testing.T) {
	peerUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer peerUpstream.Close()

	peerCfg := DefaultConfig()
	peerCfg.Upstreams = []Upstream{{ID: "pu", Name: "PeerUp", BaseURL: peerUpstream.URL, Enabled: true}}
	peerStore := NewStore(peerCfg, "")
	peerSrv := NewServer(peerStore)
	peerGW := httptest.NewServer(peerSrv.Handler())
	defer peerGW.Close()

	if resp, body := postJSON(t, peerGW.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil); resp.StatusCode != 200 {
		t.Fatalf("peer 请求失败 %d: %s", resp.StatusCode, body)
	}

	localCfg := DefaultConfig()
	localCfg.Federation = FederationConfig{
		Enabled:   true,
		TimeoutMS: 2000,
		Peers: []FederationPeer{
			{ID: "peer-a", Name: "A 实例", BaseURL: peerGW.URL},
			{ID: "peer-dead", Name: "挂了", BaseURL: "http://127.0.0.1:1"},
		},
	}
	localGW := startGateway(t, localCfg)

	res, err := http.Get(localGW.URL + "/admin/api/federation")
	if err != nil {
		t.Fatalf("联邦查询失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var report struct {
		Enabled      bool          `json:"enabled"`
		PeerCount    int           `json:"peer_count"`
		PeerHealthy  int           `json:"peer_healthy"`
		PeerRequests uint64        `json:"peer_requests"`
		Peers        []peerSummary `json:"peers"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("联邦响应不是 JSON: %v / %s", err, string(raw))
	}
	if !report.Enabled || report.PeerCount != 2 {
		t.Fatalf("联邦元信息错误: %s", string(raw))
	}
	if report.PeerHealthy != 1 {
		t.Fatalf("在线数应为 1: %s", string(raw))
	}
	for _, p := range report.Peers {
		switch p.ID {
		case "peer-a":
			if !p.OK || p.Version == "" || p.Requests != 1 {
				t.Fatalf("peer-a 汇总错误: %+v", p)
			}
		case "peer-dead":
			if p.OK || p.Error == "" {
				t.Fatalf("peer-dead 应标记失败: %+v", p)
			}
		}
	}
	if report.PeerRequests != 1 {
		t.Fatalf("peer 请求总数错误: %d", report.PeerRequests)
	}
}

func TestBatchRegression(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []Rule{
		{ID: "r-deny", Name: "拦截", Type: ruleTypeDeny, Cond: &RuleCond{Models: []string{"danger"}}, Status: 451, Message: "no"},
		{ID: "r-tag", Name: "标记", Type: ruleTypeHeader, HeaderName: "X-A", HeaderValue: "1"},
	}
	gw := startGateway(t, cfg)

	payload := map[string]any{
		"requests": []map[string]any{
			{"model": "danger", "path": "/v1/chat/completions", "body": map[string]any{"messages": []any{}}},
			{"model": "safe", "path": "/v1/chat/completions", "body": map[string]any{"messages": []any{}}},
			{"model": "safe-2", "path": "/v1/messages", "body_text": `{"messages":[]}`},
		},
	}
	raw, _ := json.Marshal(payload)
	resp, body := postJSON(t, gw.URL+"/admin/api/rules/batch", string(raw), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("批量回归失败 %d: %s", resp.StatusCode, body)
	}
	var report struct {
		Total       int            `json:"total"`
		Denied      int            `json:"denied"`
		Allowed     int            `json:"allowed"`
		RuleMatches map[string]int `json:"rule_matches"`
		Items       []struct {
			Model   string   `json:"model"`
			Denied  bool     `json:"denied"`
			Matched []string `json:"matched"`
			Source  string   `json:"source"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		t.Fatalf("回归报告不是 JSON: %v / %s", err, body)
	}
	if report.Total != 3 || report.Denied != 1 || report.Allowed != 2 {
		t.Fatalf("回归统计错误: %s", body)
	}
	// deny 命中后规则链提前返回，r-tag 只会在另外两条上命中
	if report.RuleMatches["r-tag"] != 2 {
		t.Fatalf("规则命中统计错误: %+v", report.RuleMatches)
	}
	if report.RuleMatches["r-deny"] != 1 {
		t.Fatalf("拦截规则统计错误: %+v", report.RuleMatches)
	}
	if len(report.Items) != 3 || report.Items[0].Source != "inline" || !report.Items[0].Denied {
		t.Fatalf("回归明细错误: %+v", report.Items)
	}
}

func TestTokenUsageAndBudgetReconcile(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15},"choices":[]}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{
		ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true,
		Budget: &UpstreamBudget{MaxTokensPerDay: 10000},
	}}
	gw := startGateway(t, cfg)

	if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hello"}]}`, nil); resp.StatusCode != 200 {
		t.Fatalf("请求失败 %d: %s", resp.StatusCode, body)
	}

	res, err := http.Get(gw.URL + "/admin/api/status")
	if err != nil {
		t.Fatalf("读取状态失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var status struct {
		Upstreams []struct {
			Budget *BudgetView `json:"budget"`
		} `json:"upstreams"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("状态不是 JSON: %v", err)
	}
	if len(status.Upstreams) != 1 || status.Upstreams[0].Budget == nil {
		t.Fatalf("预算视图缺失: %s", string(raw))
	}
	if got := status.Upstreams[0].Budget.Tokens; got != 15 {
		t.Fatalf("预算 token 应被纠正为实际值 15，得到 %d", got)
	}

	met, err := http.Get(gw.URL + "/metrics")
	if err != nil {
		t.Fatalf("读取指标失败: %v", err)
	}
	metRaw, _ := io.ReadAll(met.Body)
	met.Body.Close()
	if !strings.Contains(string(metRaw), `gateway_manager_tokens_total{upstream="u",name="U"} 15`) {
		t.Fatalf("token 指标缺失")
	}
}

func TestTokenUsageFromSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"hi"}}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"input_tokens":7,"output_tokens":3}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	gw := startGateway(t, cfg)

	if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","stream":true,"messages":[]}`, nil); resp.StatusCode != 200 {
		t.Fatalf("流式请求失败 %d: %s", resp.StatusCode, body)
	}
	met, err := http.Get(gw.URL + "/metrics")
	if err != nil {
		t.Fatalf("读取指标失败: %v", err)
	}
	metRaw, _ := io.ReadAll(met.Body)
	met.Body.Close()
	if !strings.Contains(string(metRaw), `gateway_manager_tokens_total{upstream="u",name="U"} 10`) {
		t.Fatalf("流式 token 指标缺失")
	}
}

func TestFederationPeerAsUpstream(t *testing.T) {
	peerUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"peer-answer","object":"chat.completion","choices":[]}`)
	}))
	defer peerUpstream.Close()

	peerCfg := DefaultConfig()
	peerCfg.Upstreams = []Upstream{{ID: "pu", Name: "PeerUp", BaseURL: peerUpstream.URL, Enabled: true}}
	peerGW := httptest.NewServer(NewServer(NewStore(peerCfg, "")).Handler())
	defer peerGW.Close()

	localCfg := DefaultConfig()
	localCfg.Federation = FederationConfig{
		Enabled:   true,
		TimeoutMS: 2000,
		Peers: []FederationPeer{
			{ID: "edge", Name: "边缘", BaseURL: peerGW.URL, AsUpstream: true, Priority: 0},
		},
	}
	localGW := startGateway(t, localCfg)

	resp, body := postJSON(t, localGW.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("经 peer 转发失败 %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "peer-answer") {
		t.Fatalf("响应不是来自 peer: %s", body)
	}

	res, err := http.Get(localGW.URL + "/admin/api/status")
	if err != nil {
		t.Fatalf("读取状态失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var status struct {
		Upstreams []struct {
			ID        string `json:"id"`
			Successes uint64 `json:"successes"`
		} `json:"upstreams"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("状态不是 JSON: %v", err)
	}
	found := false
	for _, up := range status.Upstreams {
		if up.ID == "peer-edge" && up.Successes == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("peer 未被当作上游统计: %s", string(raw))
	}
}

func TestAdapterPathAndAuthHeader(t *testing.T) {
	var gotPath, gotAuth, gotVendor string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("x-api-key")
		gotVendor = r.Header.Get("X-Vendor")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer vendor.Close()

	cfg := DefaultConfig()
	cfg.Adapters = []Adapter{{
		ID:           "vendor-x",
		MatchBaseURL: vendor.URL,
		ChatPath:     "/vendor/chat",
		ModelsPath:   "/vendor/models",
		AuthHeader:   "x-api-key",
		AuthPrefix:   "",
		ExtraHeaders: map[string]string{"X-Vendor": "v1"},
	}}
	cfg.Upstreams = []Upstream{{
		ID: "v", Name: "Vendor", BaseURL: vendor.URL, Enabled: true,
		APIKeys: []string{"sk-vendor"},
	}}
	gw := startGateway(t, cfg)

	if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil); resp.StatusCode != 200 {
		t.Fatalf("请求失败 %d: %s", resp.StatusCode, body)
	}
	if gotPath != "/vendor/chat" {
		t.Fatalf("adapter 路径未生效: %q", gotPath)
	}
	if gotAuth != "sk-vendor" {
		t.Fatalf("adapter 认证头未生效: %q", gotAuth)
	}
	if gotVendor != "v1" {
		t.Fatalf("adapter 额外头未生效: %q", gotVendor)
	}
}

func TestExprEvaluation(t *testing.T) {
	ctx := ruleContext{
		Model:   "gpt-4o-mini",
		Path:    "/v1/chat/completions",
		Body:    `{"messages":[{"role":"user","content":"hello"}]}`,
		Headers: map[string]string{"X-Client": "android", "Content-Type": "application/json"},
	}
	cases := []struct {
		expr string
		want bool
	}{
		{`model == "gpt-4o-mini"`, true},
		{`model != "gpt-4o"`, true},
		{`model ~ "^gpt-4o"`, true},
		{`model !~ "claude"`, true},
		{`model ~ "claude"`, false},
		{`path == "/v1/chat/completions" and model ~ "mini"`, true},
		{`path == "/v1/messages" or model ~ "mini"`, true},
		{`path == "/v1/messages" and model ~ "mini"`, false},
		{`body contains "hello"`, true},
		{`header:X-Client == "android"`, true},
		{`header["X-Client"] == "ios"`, false},
		{`!(model ~ "mini")`, false},
		{`!path ~ "messages"`, true},
	}
	for _, c := range cases {
		got, err := evalExpr(c.expr, ctx)
		if err != nil {
			t.Fatalf("表达式 %q 出错: %v", c.expr, err)
		}
		if got != c.want {
			t.Fatalf("表达式 %q 期望 %v，得到 %v", c.expr, c.want, got)
		}
	}

	if _, err := evalExpr(`model ~~ "x"`, ctx); err == nil {
		t.Fatal("非法运算符应当报错")
	}
	if _, err := evalExpr(`unknown == "x"`, ctx); err == nil {
		t.Fatal("未知字段应当报错")
	}
	if _, err := evalExpr(`model ~ "["`, ctx); err == nil {
		t.Fatal("非法正则应当报错")
	}
}

func TestExprInRuleDeny(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []Rule{{
		ID: "r-expr", Name: "正则拦截", Type: ruleTypeDeny,
		Cond:   &RuleCond{Expr: `model ~ "^danger-" and path == "/v1/chat/completions"`},
		Status: 451, Message: "regex blocked",
	}}
	gw := startGateway(t, cfg)

	if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"danger-x","messages":[]}`, nil); resp.StatusCode != 451 {
		t.Fatalf("正则命中应拦截，得到 %d: %s", resp.StatusCode, body)
	}
	if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"safe-x","messages":[]}`, nil); resp.StatusCode == 451 {
		t.Fatalf("未命中不应拦截: %s", body)
	}

	// 非法表达式在保存配置时应当被拒绝
	bad := DefaultConfig()
	bad.Rules = []Rule{{ID: "bad", Type: ruleTypeMatch, Cond: &RuleCond{Expr: `model ~~ "x"`}}}
	if err := ValidateConfig(bad); err == nil {
		t.Fatal("非法表达式应当让配置校验失败")
	}
}

func TestMirrorShadowDelivery(t *testing.T) {
	mirrorHits := make(chan string, 4)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"primary"}`)
	}))
	defer primary.Close()
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case mirrorHits <- string(body):
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"mirror"}`)
	}))
	defer mirror.Close()

	cfg := DefaultConfig()
	cfg.Server.MirrorEnabled = true
	cfg.Server.MirrorUpstream = "shadow"
	cfg.Server.MirrorRatio = 100
	cfg.Server.MirrorModels = []string{"m"}
	cfg.Server.MirrorTimeoutMS = 3000
	cfg.Upstreams = []Upstream{
		{ID: "main", Name: "主", BaseURL: primary.URL, Priority: 0, Enabled: true},
		{ID: "shadow", Name: "影子", BaseURL: mirror.URL, Priority: 100, Enabled: true},
	}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"shadow-me"}]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("主请求失败 %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "primary") {
		t.Fatalf("客户端应拿到主上游响应: %s", body)
	}

	select {
	case got := <-mirrorHits:
		if !strings.Contains(got, "shadow-me") {
			t.Fatalf("镜像请求体不对: %s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("镜像没有被投递")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		res, err := http.Get(gw.URL + "/metrics")
		if err == nil {
			raw, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if strings.Contains(string(raw), `gateway_manager_mirror_total{upstream="shadow",name="影子",result="ok"} 1`) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("镜像指标未记录")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestGitBackedSnapshots(t *testing.T) {
	ensureGitForTest(t)

	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Server.SnapshotGit = true
	cfg.Server.SnapshotDir = dir
	cfg.Server.SnapshotBeforeSave = false
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: "http://127.0.0.1:1", Enabled: true}}

	store := NewStore(cfg, "")
	srv := NewServer(store)
	gw := httptest.NewServer(srv.Handler())
	defer gw.Close()

	resp, body := postJSON(t, gw.URL+"/admin/api/snapshots/create", `{"note":"git"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("创建快照失败 %d: %s", resp.StatusCode, body)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := http.Get(gw.URL + "/admin/api/snapshots/git/log?limit=5")
		if err == nil {
			raw, _ := io.ReadAll(res.Body)
			res.Body.Close()
			var report struct {
				Available bool       `json:"available"`
				Entries   []GitEntry `json:"entries"`
			}
			if err := json.Unmarshal(raw, &report); err != nil {
				t.Fatalf("git 日志不是 JSON: %v / %s", err, string(raw))
			}
			if report.Available && len(report.Entries) > 0 && strings.Contains(report.Entries[0].Subject, "snapshot") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("快照未提交到 git")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func ensureGitForTest(t *testing.T) {
	t.Helper()
	if _, ok := gitBinary(); ok {
		return
	}
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Git", "cmd"),
		filepath.Join(os.Getenv("ProgramW6432"), "Git", "cmd"),
		"D:\\app2\\Git\\cmd",
		"C:\\Program Files\\Git\\cmd",
		"/usr/bin",
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "git.exe")); err == nil {
			_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			break
		}
		if _, err := os.Stat(filepath.Join(dir, "git")); err == nil {
			_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			break
		}
	}
	if _, ok := gitBinary(); !ok {
		t.Skip("本机没有 git，跳过 git 快照用例")
	}
}

func TestCircuitHalfOpenRecovery(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"boom"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","choices":[]}`)
	}))
	defer upstream.Close()

	cfg := DefaultConfig()
	cfg.Retry.MaxRetries = 0
	cfg.Retry.FailureThreshold = 1
	cfg.Retry.CooldownSeconds = 1
	cfg.Retry.HalfOpenLimit = 1
	cfg.Upstreams = []Upstream{{ID: "u", Name: "U", BaseURL: upstream.URL, Enabled: true}}
	gw := startGateway(t, cfg)

	readCircuit := func() (string, bool) {
		res, err := http.Get(gw.URL + "/admin/api/status")
		if err != nil {
			t.Fatalf("读取状态失败: %v", err)
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		var status struct {
			Upstreams []struct {
				Circuit string `json:"circuit"`
				Healthy bool   `json:"healthy"`
			} `json:"upstreams"`
		}
		if err := json.Unmarshal(raw, &status); err != nil || len(status.Upstreams) == 0 {
			t.Fatalf("状态解析失败: %s", string(raw))
		}
		return status.Upstreams[0].Circuit, status.Upstreams[0].Healthy
	}

	if resp, _ := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil); resp.StatusCode < 500 {
		t.Fatalf("首次失败应返回 5xx，得到 %d", resp.StatusCode)
	}
	if circuit, _ := readCircuit(); circuit != "open" {
		t.Fatalf("熔断应打开，得到 %q", circuit)
	}

	failing.Store(false)
	time.Sleep(1200 * time.Millisecond)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("半开探测应成功，得到 %d: %s", resp.StatusCode, body)
	}
	if circuit, healthy := readCircuit(); circuit != "closed" || !healthy {
		t.Fatalf("探测成功后应闭合，得到 %q healthy=%v", circuit, healthy)
	}
}

func TestExperimentArmsSplit(t *testing.T) {
	var aHits, bHits int
	var aSystem string
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		body, _ := io.ReadAll(r.Body)
		payload := map[string]any{}
		_ = json.Unmarshal(body, &payload)
		if msgs, ok := payload["messages"].([]any); ok && len(msgs) > 0 {
			if head, ok := msgs[0].(map[string]any); ok {
				aSystem = jsonStringField(head, "content")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"a","usage":{"total_tokens":4},"object":"chat.completion","choices":[]}`)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"b","usage":{"total_tokens":6},"object":"chat.completion","choices":[]}`)
	}))
	defer b.Close()

	cfg := DefaultConfig()
	cfg.Profiles = []Profile{{ID: "pa", Name: "策略A", Instruction: "PROFILE-A", Enabled: true}}
	cfg.Upstreams = []Upstream{
		{ID: "ua", Name: "上游A", BaseURL: a.URL, Enabled: true},
		{ID: "ub", Name: "上游B", BaseURL: b.URL, Enabled: true},
	}
	cfg.Experiments = []Experiment{{
		ID: "exp-1", Name: "对照", Models: []string{"m"},
		Arms: []ExperimentArm{
			{ID: "arm-a", Weight: 1, ProfileID: "pa", UpstreamID: "ua"},
			{ID: "arm-b", Weight: 1, UpstreamID: "ub"},
		},
	}}
	gw := startGateway(t, cfg)

	for i := 0; i < 4; i++ {
		if resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, nil); resp.StatusCode != 200 {
			t.Fatalf("第 %d 次失败 %d: %s", i, resp.StatusCode, body)
		}
	}
	if aHits != 2 || bHits != 2 {
		t.Fatalf("分臂不均: a=%d b=%d", aHits, bHits)
	}
	if aSystem != "PROFILE-A" {
		t.Fatalf("A 臂未使用指定策略: %q", aSystem)
	}

	res, err := http.Get(gw.URL + "/admin/api/experiments")
	if err != nil {
		t.Fatalf("读取实验失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var report struct {
		Stats map[string]map[string]struct {
			Count  uint64 `json:"count"`
			Tokens int64  `json:"tokens_sum"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("实验响应不是 JSON: %v / %s", err, string(raw))
	}
	arms := report.Stats["exp-1"]
	if arms["arm-a"].Count != 2 || arms["arm-b"].Count != 2 {
		t.Fatalf("实验计数错误: %s", string(raw))
	}
	if arms["arm-a"].Tokens != 8 || arms["arm-b"].Tokens != 12 {
		t.Fatalf("实验 token 统计错误: %s", string(raw))
	}

	met, err := http.Get(gw.URL + "/metrics")
	if err != nil {
		t.Fatalf("读取指标失败: %v", err)
	}
	metRaw, _ := io.ReadAll(met.Body)
	met.Body.Close()
	for _, want := range []string{
		`gateway_manager_experiment_total{experiment="exp-1",arm="arm-a",result="all"} 2`,
		`gateway_manager_upstream_circuit{upstream="ua",name="上游A",state="closed"} 1`,
	} {
		if !strings.Contains(string(metRaw), want) {
			t.Fatalf("指标缺少 %q", want)
		}
	}
}

func TestInstallerScripts(t *testing.T) {
	shRaw, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatalf("install.sh 缺失: %v", err)
	}
	sh := string(shRaw)
	for _, want := range []string{"set -euo pipefail", "systemctl enable --now", "--uninstall", "init -config", "systemctl daemon-reload"} {
		if !strings.Contains(sh, want) {
			t.Fatalf("install.sh 缺少 %q", want)
		}
	}

	psRaw, err := os.ReadFile("install.ps1")
	if err != nil {
		t.Fatalf("install.ps1 缺失: %v", err)
	}
	ps := string(psRaw)
	for _, want := range []string{"[CmdletBinding()]", "sc.exe create", "-Uninstall", "ConvertFrom-Json"} {
		if !strings.Contains(ps, want) {
			t.Fatalf("install.ps1 缺少 %q", want)
		}
	}
	if !strings.HasPrefix(ps, "\uFEFF") {
		t.Fatal("install.ps1 需要 UTF-8 BOM，否则 PowerShell 5.1 会按 ANSI 解析导致中文乱码")
	}
}

func TestChannelPresets(t *testing.T) {
	presets := builtinChannelPresets()
	if len(presets) < 10 {
		t.Fatalf("内置渠道太少: %d", len(presets))
	}
	seen := map[string]bool{}
	openaiLike := 0
	for _, p := range presets {
		if p.ID == "" || p.Name == "" {
			t.Fatalf("渠道字段不完整: %+v", p)
		}
		if seen[p.ID] {
			t.Fatalf("渠道 id 重复: %s", p.ID)
		}
		seen[p.ID] = true
		if !strings.HasPrefix(p.BaseURL, "http") || !strings.HasPrefix(p.ChatPath, "/") {
			t.Fatalf("渠道 %s 的地址/路径不对: %s %s", p.ID, p.BaseURL, p.ChatPath)
		}
		switch p.Protocol {
		case "openai":
			openaiLike++
		case "needs_adapter":
		default:
			t.Fatalf("渠道 %s 协议标记非法: %q", p.ID, p.Protocol)
		}
		if p.Protocol == "openai" && p.Auth.Type == "" {
			t.Fatalf("渠道 %s 缺少认证方式", p.ID)
		}
	}
	if openaiLike < 4 {
		t.Fatalf("可直接用的渠道太少: %d", openaiLike)
	}
	for _, want := range []string{"opencode-zen", "zhipu-autoclaw", "workbuddy", "raccoon", "deepseek-web"} {
		if _, ok := findPreset(want); !ok {
			t.Fatalf("缺少渠道模板 %s", want)
		}
	}
}

func TestChannelRoutingOpenAICompatible(t *testing.T) {
	var gotPath, gotAuth, gotProduct, gotBody string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("X-Authorization")
		gotProduct = r.Header.Get("X-Product")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"zhipu-1","object":"chat.completion","usage":{"total_tokens":9},"choices":[]}`)
	}))
	defer vendor.Close()

	cfg := DefaultConfig()
	cfg.Channels = []Channel{{
		ID: "zhipu", Preset: "zhipu-autoclaw", Token: "tk-zhipu",
		BaseURL: vendor.URL, Enabled: true,
	}}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"glm-5","messages":[{"role":"user","content":"hi"}]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("渠道请求失败 %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "zhipu-1") {
		t.Fatalf("响应不是来自渠道: %s", body)
	}
	if gotPath != "/autoclaw-proxy/proxy/autoclaw/chat/completions" {
		t.Fatalf("渠道路径未生效: %q", gotPath)
	}
	if gotAuth != "Bearer tk-zhipu" {
		t.Fatalf("渠道认证头未生效: %q", gotAuth)
	}
	if gotProduct != "autoclaw" {
		t.Fatalf("渠道额外头未生效: %q", gotProduct)
	}
	if !strings.Contains(gotBody, "glm-5") {
		t.Fatalf("请求体未透传: %q", gotBody)
	}

	st, err := http.Get(gw.URL + "/admin/api/status")
	if err != nil {
		t.Fatalf("读取状态失败: %v", err)
	}
	raw, _ := io.ReadAll(st.Body)
	st.Body.Close()
	if !strings.Contains(string(raw), "\"id\":\"ch-zhipu\"") {
		t.Fatalf("渠道未作为上游出现: %s", string(raw)[:min(len(string(raw)), 600)])
	}
}

func TestChannelQueryAuthAndTest(t *testing.T) {
	var gotToken, gotPath string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.URL.Query().Get("token")
		if r.URL.Path == "/zen/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"grok-code"}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[]}`)
	}))
	defer vendor.Close()

	cfg := DefaultConfig()
	cfg.Channels = []Channel{
		{ID: "xunfei", Preset: "xunfei-loomy", Token: "tk-query", BaseURL: vendor.URL, Enabled: true},
		{ID: "zen", Preset: "opencode-zen", Token: "public", BaseURL: vendor.URL, Enabled: false},
	}
	gw := startGateway(t, cfg)

	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("渠道请求失败 %d: %s", resp.StatusCode, body)
	}
	if gotPath != "/api/v1/chat/completions" || gotToken != "tk-query" {
		t.Fatalf("query 认证未生效: path=%q token=%q", gotPath, gotToken)
	}

	// 探活：opencode 模板有模型端点
	probeBody, _ := json.Marshal(map[string]string{"id": "zen"})
	resp2, body2 := postJSON(t, gw.URL+"/admin/api/channels/test", string(probeBody), nil)
	if resp2.StatusCode != 200 {
		t.Fatalf("探活失败 %d: %s", resp2.StatusCode, body2)
	}
	if !strings.Contains(body2, "\"ok\":true") || !strings.Contains(body2, "/zen/v1/models") {
		t.Fatalf("探活结果不对: %s", body2)
	}
}

func TestChannelCRUD(t *testing.T) {
	store := NewStore(DefaultConfig(), "")
	gw := httptest.NewServer(NewServer(store).Handler())
	defer gw.Close()

	// 预设列表
	res, err := http.Get(gw.URL + "/admin/api/channels")
	if err != nil {
		t.Fatalf("读取渠道失败: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var listed struct {
		Presets  []ChannelPreset  `json:"presets"`
		Channels []map[string]any `json:"channels"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("渠道列表不是 JSON: %v", err)
	}
	if len(listed.Presets) < 10 {
		t.Fatalf("预设数量不对: %d", len(listed.Presets))
	}

	// 新增
	payload, _ := json.Marshal(map[string]any{"id": "zz", "preset": "zhipu-autoclaw", "token": "sk-very-secret-token"})
	resp, body := postJSON(t, gw.URL+"/admin/api/channels", string(payload), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("新增渠道失败 %d: %s", resp.StatusCode, body)
	}
	if len(store.Config().Channels) != 1 || store.Config().Channels[0].Token != "sk-very-secret-token" {
		t.Fatalf("渠道未落库: %+v", store.Config().Channels)
	}

	// 列表里 token 必须被遮罩
	res2, _ := http.Get(gw.URL + "/admin/api/channels")
	raw2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if strings.Contains(string(raw2), "sk-very-secret-token") {
		t.Fatalf("渠道列表泄露了明文 token")
	}
	if !strings.Contains(string(raw2), "sk-v…oken") {
		t.Fatalf("遮罩格式不对: %s", string(raw2)[:min(len(string(raw2)), 400)])
	}

	// 非法模板
	bad, _ := json.Marshal(map[string]any{"preset": "no-such-vendor"})
	resp3, body3 := postJSON(t, gw.URL+"/admin/api/channels", string(bad), nil)
	if resp3.StatusCode != 400 {
		t.Fatalf("非法模板应 400，得到 %d: %s", resp3.StatusCode, body3)
	}

	// 删除
	req, _ := http.NewRequest(http.MethodDelete, gw.URL+"/admin/api/channels?id=zz", nil)
	del, _ := http.DefaultClient.Do(req)
	if del != nil {
		del.Body.Close()
	}
	if len(store.Config().Channels) != 0 {
		t.Fatalf("渠道未删除: %+v", store.Config().Channels)
	}
}

func TestChannelLoginPresetsHaveLoginURL(t *testing.T) {
	for _, p := range builtinChannelPresets() {
		if !strings.HasPrefix(p.LoginURL, "https://") {
			t.Fatalf("渠道 %s 缺少 https 登录页：%q", p.ID, p.LoginURL)
		}
	}
}

func TestWebSocketFramingRoundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		key := ""
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
				key = strings.TrimSpace(line[len("sec-websocket-key:"):])
			}
		}
		h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		accept := base64.StdEncoding.EncodeToString(h[:])
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)

		// 读一帧（客户端带 mask），原样回写（服务端不带 mask）
		head := make([]byte, 2)
		if _, err := io.ReadFull(br, head); err != nil {
			return
		}
		n := int(head[1] & 0x7f)
		if n == 126 {
			ext := make([]byte, 2)
			_, _ = io.ReadFull(br, ext)
			n = int(ext[0])<<8 | int(ext[1])
		}
		mask := make([]byte, 4)
		_, _ = io.ReadFull(br, mask)
		payload := make([]byte, n)
		_, _ = io.ReadFull(br, payload)
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
		frame := []byte{0x81, byte(len(payload))}
		frame = append(frame, payload...)
		_, _ = conn.Write(frame)
	}()

	ws, err := dialWS(fmt.Sprintf("ws://%s/cdp", ln.Addr().String()))
	if err != nil {
		t.Fatalf("WS 连接失败: %v", err)
	}
	defer ws.close()

	msg := `{"id":1,"method":"Browser.getVersion"}`
	if err := ws.writeText([]byte(msg)); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	got, err := ws.readMessage()
	if err != nil {
		t.Fatalf("接收失败: %v", err)
	}
	if string(got) != msg {
		t.Fatalf("回显不一致: %q", string(got))
	}
}

func TestCookieHelpers(t *testing.T) {
	cookies := []cdpCookie{
		{Name: "sessionid", Value: "abc", Domain: ".chat.deepseek.com"},
		{Name: "cf_clearance", Value: "xyz", Domain: "chat.deepseek.com"},
		{Name: "other", Value: "nope", Domain: "example.com"},
	}
	// 域名过滤
	host := hostOf("https://chat.deepseek.com/api/v0/chat/completion")
	if host != "chat.deepseek.com" {
		t.Fatalf("hostOf 解析错误: %q", host)
	}
	kept := make([]cdpCookie, 0, len(cookies))
	for _, c := range cookies {
		d := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
		if strings.HasSuffix(host, d) || strings.HasSuffix(d, host) {
			kept = append(kept, c)
		}
	}
	if len(kept) != 2 {
		t.Fatalf("域名过滤错误: %+v", kept)
	}
	token := cookiesToToken(kept)
	if token != "sessionid=abc; cf_clearance=xyz" {
		t.Fatalf("cookie 拼接错误: %q", token)
	}
	if hostOf("http://www.workbuddy.ai/x") != "workbuddy.ai" {
		t.Fatalf("hostOf 未去掉 www")
	}
}

func TestChannelLoginWithoutBrowser(t *testing.T) {
	orig := findBrowserFunc
	findBrowserFunc = func() (string, bool) { return "", false }
	defer func() { findBrowserFunc = orig }()

	store := NewStore(DefaultConfig(), "")
	gw := httptest.NewServer(NewServer(store).Handler())
	defer gw.Close()

	payload, _ := json.Marshal(map[string]string{"preset": "deepseek-web"})
	resp, body := postJSON(t, gw.URL+"/admin/api/channels/login/start", string(payload), nil)
	if resp.StatusCode != 400 {
		t.Fatalf("没有浏览器时应 400，得到 %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "未找到 Chrome") {
		t.Fatalf("错误提示不友好: %s", body)
	}
}

func TestBuildDeepSeekPrompt(t *testing.T) {
	msgs := []map[string]any{
		{"role": "system", "content": "你是猫娘"},
		{"role": "user", "content": "你好"},
		{"role": "assistant", "content": "喵"},
		{"role": "user", "content": "再说一次"},
	}
	got := buildDeepSeekPrompt(msgs)
	want := "<｜User｜>你好<｜Assistant｜>喵<｜end▁of▁sentence｜><｜User｜>再说一次<｜System｜>你是猫娘<｜Assistant｜>"
	if got != want {
		t.Fatalf("prompt 拼接错误:\n got=%q\nwant=%q", got, want)
	}
	multi := buildDeepSeekPrompt([]map[string]any{{"role": "user", "content": []any{map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "text", "text": "b"}}}})
	if multi != "<｜User｜>ab<｜Assistant｜>" {
		t.Fatalf("数组 content 解析错误: %q", multi)
	}
}

func TestParseDeepSeekStreamToDeltas(t *testing.T) {
	raw := "event: ready\ndata: {\"request_message_id\":1}\n\n" +
		"data: {\"v\":{\"response\":{\"fragments\":[{\"type\":\"RESPONSE\",\"content\":\"你好\"}],\"accumulated_token_usage\":12}}}\n\n" +
		"data: {\"p\":\"response/fragments/-1/content\",\"o\":\"APPEND\",\"v\":\"！\"}\n\n" +
		"data: {\"v\":\"我是\"}\n\n" +
		"data: {\"v\":\"DeepSeek\"}\n\n" +
		"data: {\"p\":\"response\",\"o\":\"BATCH\",\"v\":[{\"p\":\"accumulated_token_usage\",\"v\":68}]}\n\n" +
		"event: close\ndata: {\"click_behavior\":\"none\"}\n\n"
	var sb strings.Builder
	usage := parseDeepSeekStream(strings.NewReader(raw), func(d string) { sb.WriteString(d) })
	if sb.String() != "你好！我是DeepSeek" {
		t.Fatalf("流拼接错误: %q", sb.String())
	}
	if usage != 68 {
		t.Fatalf("用量解析错误: %d", usage)
	}
}

func TestUnwrapStorageValue(t *testing.T) {
	cases := map[string]string{
		`{"value":"enoRKQ8","__version":0}`: "enoRKQ8",
		`plain-token`:                       "plain-token",
		`{"value":123}`:                     `{"value":123}`,
	}
	for in, want := range cases {
		if got := unwrapStorageValue(in); got != want {
			t.Fatalf("unwrap(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDeepSeekChannelRouting(t *testing.T) {
	var gotPath string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path == "/api/v0/chat_session/create" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"biz_data":{"chat_session":{"id":"sess-1"}}}}`)
			return
		}
		if r.URL.Path == "/api/v0/chat/create_pow_challenge" {
			w.Header().Set("Content-Type", "application/json")
			ch := deepseekHash([]byte("s_1_0"))
			_, _ = fmt.Fprintf(w, `{"code":0,"data":{"biz_data":{"challenge":{"algorithm":"DeepSeekHashV1","challenge":"%s","salt":"s","expire_at":1,"difficulty":1,"signature":"sig","target_path":"/api/v0/chat/completion"}}}}`, hex.EncodeToString(ch[:]))
			return
		}
		if r.URL.Path == "/api/v0/chat/completion" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"v\":{\"response\":{\"fragments\":[{\"type\":\"RESPONSE\",\"content\":\"hi\"}],\"accumulated_token_usage\":5}}}\n\n"+
				"data: {\"v\":\" there\"}\n\n")
			return
		}
		w.WriteHeader(404)
	}))
	defer vendor.Close()

	origBase := deepseekBaseOverride
	deepseekBaseOverride = vendor.URL
	defer func() { deepseekBaseOverride = origBase }()

	cfg := DefaultConfig()
	cfg.Channels = []Channel{{
		ID: "ds", Preset: "deepseek-web", Token: "tk", Cookie: "ds_session_id=x", Enabled: true,
	}}
	gw := startGateway(t, cfg)
	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("DeepSeek 渠道请求失败 %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "hi there") {
		t.Fatalf("内容拼接错误: %s", body)
	}
	if gotPath != "/api/v0/chat/completion" {
		t.Fatalf("没有走 DeepSeek 协议: %q", gotPath)
	}
}

func TestBuildRunablePrompt(t *testing.T) {
	msgs := []map[string]any{
		{"role": "system", "content": "你是助手"},
		{"role": "user", "content": "你好"},
		{"role": "assistant", "content": "喵"},
		{"role": "tool", "content": "结果"},
	}
	got := buildRunablePrompt(msgs)
	want := "你是助手\n\n你好\n\n[assistant]: 喵\n\n[tool]: 结果"
	if got != want {
		t.Fatalf("Runable prompt 错误:\n got=%q\nwant=%q", got, want)
	}
}

func TestParseRunableStream(t *testing.T) {
	raw := ": ping\n\n" +
		`data: {"type":"start"}` + "\n\n" +
		`data: {"type":"text-delta","delta":"你好"}` + "\n\n" +
		`data: {"type":"text-delta","textDelta":"世界"}` + "\n\n" +
		`data: {"type":"finish","usage":{"totalTokens":42}}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	var sb strings.Builder
	usage := parseRunableStream(strings.NewReader(raw), func(d string) { sb.WriteString(d) })
	if sb.String() != "你好世界" {
		t.Fatalf("流解析错误: %q", sb.String())
	}
	if usage != 42 {
		t.Fatalf("用量解析错误: %d", usage)
	}
	if got := looseText(`0:"hello"`); got != "hello" {
		t.Fatalf("AI SDK 兜底解析错误: %q", got)
	}
}

func TestRunableChannelRouting(t *testing.T) {
	var gotPath, gotCookie, gotDevice, gotBody string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCookie = r.Header.Get("Cookie")
		gotDevice = r.Header.Get("x-runable-device")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		if r.URL.Path == "/api/auth/get-session" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"session":{"id":"s"},"user":{"email":"a@b.c"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"text-delta","delta":"喵"}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"text-delta","delta":"~"}`+"\n\n")
	}))
	defer vendor.Close()

	orig := runableBaseOverride
	runableBaseOverride = vendor.URL
	defer func() { runableBaseOverride = orig }()

	cfg := DefaultConfig()
	cfg.Channels = []Channel{{
		ID: "rb", Preset: "runable", Cookie: "session=abc", Enabled: true,
	}}
	gw := startGateway(t, cfg)
	resp, body := postJSON(t, gw.URL+"/v1/chat/completions", `{"model":"runable/runner-chat","messages":[{"role":"user","content":"hi"}]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("Runable 渠道失败 %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "喵~") {
		t.Fatalf("内容错误: %s", body)
	}
	if gotPath != "/api/chat/text" {
		t.Fatalf("没有走 Runable 协议: %q", gotPath)
	}
	if gotCookie != "session=abc" {
		t.Fatalf("cookie 未透传: %q", gotCookie)
	}
	if !strings.HasPrefix(gotDevice, "dweb/") {
		t.Fatalf("设备头缺失: %q", gotDevice)
	}
	if !strings.Contains(gotBody, `"trigger":"submit-message"`) {
		t.Fatalf("请求体结构不对: %s", gotBody)
	}
}
