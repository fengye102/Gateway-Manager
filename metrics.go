package main

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func escapeLabel(v string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n")
	return replacer.Replace(v)
}

func (s *Server) metricsAuthorized(r *http.Request) bool {
	cfg := s.store.Config()
	if !cfg.Server.MetricsEnabled {
		return false
	}
	token := strings.TrimSpace(cfg.Server.AdminToken)
	if token == "" {
		return true
	}
	got := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if got == "" {
		if h := r.Header.Get("Authorization"); h != "" {
			if parts := strings.SplitN(h, " ", 2); len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				got = strings.TrimSpace(parts[1])
			}
		}
	}
	if got == "" {
		got = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return got == token
}

type metricWriter struct {
	b strings.Builder
}

func (m *metricWriter) header(name string, help string, kind string) {
	m.b.WriteString("# HELP " + name + " " + help + "\n")
	m.b.WriteString("# TYPE " + name + " " + kind + "\n")
}

func (m *metricWriter) sample(name string, labels [][2]string, value string) {
	if len(labels) == 0 {
		m.b.WriteString(name + " " + value + "\n")
		return
	}
	parts := make([]string, 0, len(labels))
	for _, kv := range labels {
		parts = append(parts, kv[0]+"=\""+escapeLabel(kv[1])+"\"")
	}
	m.b.WriteString(name + "{" + strings.Join(parts, ",") + "} " + value + "\n")
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.store.Config().Server.MetricsEnabled {
		proxyError(w, http.StatusNotFound, "metrics disabled")
		return
	}
	if !s.metricsAuthorized(r) {
		proxyError(w, http.StatusUnauthorized, "A valid admin token is required for /metrics")
		return
	}

	cfg := s.store.Config()
	stats := s.store.StatsSnapshot()
	ups := s.store.SnapshotUpstreams()
	sort.Slice(ups, func(i, j int) bool { return ups[i].ID < ups[j].ID })
	ruleStats := s.store.RuleStatsSnapshot()
	ruleIDs := make([]string, 0, len(ruleStats))
	for id := range ruleStats {
		ruleIDs = append(ruleIDs, id)
	}
	sort.Strings(ruleIDs)
	ruleNames := map[string]string{}
	for _, rule := range cfg.Rules {
		ruleNames[rule.ID] = rule.Name
	}

	m := &metricWriter{}
	now := time.Now()

	m.header("pojiagw_info", "构建与监听信息", "gauge")
	m.sample("pojiagw_info", [][2]string{
		{"version", version},
		{"bind", cfg.Server.Bind},
		{"port", strconv.Itoa(cfg.Server.Port)},
		{"key_strategy", cfg.Server.KeyStrategy},
	}, "1")

	m.header("pojiagw_uptime_seconds", "进程运行时长（秒）", "gauge")
	m.sample("pojiagw_uptime_seconds", nil, formatFloat(now.Sub(time.Unix(stats.StartedAt, 0)).Seconds()))

	m.header("pojiagw_requests_total", "代理请求总数", "counter")
	m.sample("pojiagw_requests_total", nil, strconv.FormatUint(stats.Requests, 10))
	m.header("pojiagw_profile_injections_total", "按策略注入次数", "counter")
	m.sample("pojiagw_profile_injections_total", nil, strconv.FormatUint(stats.Injected, 10))
	m.header("pojiagw_rule_injections_total", "按规则注入次数", "counter")
	m.sample("pojiagw_rule_injections_total", nil, strconv.FormatUint(stats.RuleInject, 10))
	m.header("pojiagw_no_profile_total", "未注入策略的请求数", "counter")
	m.sample("pojiagw_no_profile_total", nil, strconv.FormatUint(stats.NoProfile, 10))
	m.header("pojiagw_inject_conflict_total", "注入结构冲突（400）次数", "counter")
	m.sample("pojiagw_inject_conflict_total", nil, strconv.FormatUint(stats.BadShape, 10))
	m.header("pojiagw_rule_denies_total", "被规则拦截的请求数", "counter")
	m.sample("pojiagw_rule_denies_total", nil, strconv.FormatUint(stats.RuleDeny, 10))
	m.header("pojiagw_auth_failures_total", "代理鉴权失败次数", "counter")
	m.sample("pojiagw_auth_failures_total", nil, strconv.FormatUint(stats.AuthFailures, 10))
	m.header("pojiagw_upstream_errors_total", "上游失败总次数", "counter")
	m.sample("pojiagw_upstream_errors_total", nil, strconv.FormatUint(stats.UpstreamErrors, 10))
	m.header("pojiagw_key_skips_total", "因 key 全部冷却而跳过上游的次数", "counter")
	m.sample("pojiagw_key_skips_total", nil, strconv.FormatUint(stats.KeySkips, 10))

	m.header("pojiagw_upstream_enabled", "上游是否启用（1/0）", "gauge")
	m.header("pojiagw_upstream_healthy", "上游健康状态（1/0）", "gauge")
	m.header("pojiagw_upstream_cooldown_seconds", "上游冷却剩余秒数", "gauge")
	m.header("pojiagw_upstream_requests_total", "上游处理请求数", "counter")
	m.header("pojiagw_upstream_successes_total", "上游成功次数", "counter")
	m.header("pojiagw_upstream_failures_total", "上游失败次数", "counter")
	m.header("pojiagw_upstream_inflight", "上游当前并发数", "gauge")
	m.header("pojiagw_upstream_latency_ms", "上游最近一次延迟（毫秒）", "gauge")
	m.header("pojiagw_upstream_models", "上游已知模型数量", "gauge")
	for _, u := range ups {
		labels := [][2]string{{"upstream", u.ID}, {"name", u.Name}}
		enabled := "0"
		if u.Enabled {
			enabled = "1"
		}
		healthy := "0"
		if u.Healthy {
			healthy = "1"
		}
		cool := 0.0
		if u.CooldownUntil > 0 {
			cool = float64(u.CooldownUntil) - float64(now.Unix())
			if cool < 0 {
				cool = 0
			}
		}
		m.sample("pojiagw_upstream_enabled", labels, enabled)
		m.sample("pojiagw_upstream_healthy", labels, healthy)
		m.sample("pojiagw_upstream_cooldown_seconds", labels, formatFloat(cool))
		m.sample("pojiagw_upstream_requests_total", labels, strconv.FormatUint(u.Requests, 10))
		m.sample("pojiagw_upstream_successes_total", labels, strconv.FormatUint(u.Successes, 10))
		m.sample("pojiagw_upstream_failures_total", labels, strconv.FormatUint(u.Failures, 10))
		m.sample("pojiagw_upstream_inflight", labels, strconv.Itoa(u.Inflight))
		m.sample("pojiagw_upstream_latency_ms", labels, strconv.FormatInt(u.LatencyMS, 10))
		m.sample("pojiagw_upstream_models", labels, strconv.Itoa(len(u.Models)))
	}

	m.header("pojiagw_upstream_circuit", "上游熔断状态（1 表示当前状态）", "gauge")
	for _, u := range ups {
		state := u.Circuit
		if state == "" {
			state = circuitClosed
		}
		for _, candidate := range []string{circuitClosed, circuitHalfOpen, circuitOpen} {
			value := "0"
			if candidate == state {
				value = "1"
			}
			m.sample("pojiagw_upstream_circuit", [][2]string{{"upstream", u.ID}, {"name", u.Name}, {"state", candidate}}, value)
		}
	}

	m.header("pojiagw_experiment_total", "A/B 实验各臂请求数", "counter")
	m.header("pojiagw_experiment_latency_ms_sum", "A/B 实验各臂累计耗时（毫秒）", "counter")
	m.header("pojiagw_experiment_tokens_sum", "A/B 实验各臂累计 token", "counter")
	expStats := s.store.ExperimentStats()
	for _, expID := range sortedKeys(expStats) {
		for _, armID := range sortedKeys(expStats[expID]) {
			st := expStats[expID][armID]
			m.sample("pojiagw_experiment_total", [][2]string{{"experiment", expID}, {"arm", armID}, {"result", "all"}}, formatUint(st.Count))
			m.sample("pojiagw_experiment_total", [][2]string{{"experiment", expID}, {"arm", armID}, {"result", "error"}}, formatUint(st.Errors))
			m.sample("pojiagw_experiment_latency_ms_sum", [][2]string{{"experiment", expID}, {"arm", armID}}, strconv.FormatInt(st.LatencyMS, 10))
			m.sample("pojiagw_experiment_tokens_sum", [][2]string{{"experiment", expID}, {"arm", armID}}, strconv.FormatInt(st.Tokens, 10))
		}
	}

	m.header("pojiagw_key_score", "key 健康评分（0-100）", "gauge")
	m.header("pojiagw_key_successes_total", "key 成功次数", "counter")
	m.header("pojiagw_key_failures_total", "key 失败次数", "counter")
	m.header("pojiagw_key_cooling", "key 是否冷却中（1/0）", "gauge")
	m.header("pojiagw_key_requests_total", "key 被使用次数", "counter")
	for _, u := range ups {
		for _, k := range u.Keys {
			labels := [][2]string{{"upstream", u.ID}, {"key", k.Masked}}
			cooling := "0"
			if k.State == "cooling" {
				cooling = "1"
			}
			m.sample("pojiagw_key_score", labels, formatFloat(k.Score))
			m.sample("pojiagw_key_successes_total", labels, strconv.Itoa(k.Successes))
			m.sample("pojiagw_key_failures_total", labels, strconv.Itoa(k.Failures))
			m.sample("pojiagw_key_cooling", labels, cooling)
			m.sample("pojiagw_key_requests_total", labels, strconv.FormatUint(k.Requests, 10))
		}
	}

	m.header("pojiagw_rule_matched_total", "规则命中次数", "counter")
	m.header("pojiagw_rule_skipped_total", "规则跳过次数", "counter")
	m.header("pojiagw_rule_denied_total", "规则拦截次数", "counter")
	m.header("pojiagw_rule_hit_ratio", "规则命中占比（0-1）", "gauge")
	for _, id := range ruleIDs {
		hit := ruleStats[id]
		labels := [][2]string{{"rule", id}, {"name", ruleNames[id]}}
		ratio := 0.0
		total := hit.Matched + hit.Skipped
		if total > 0 {
			ratio = float64(hit.Matched) / float64(total)
		}
		m.sample("pojiagw_rule_matched_total", labels, strconv.FormatUint(hit.Matched, 10))
		m.sample("pojiagw_rule_skipped_total", labels, strconv.FormatUint(hit.Skipped, 10))
		m.sample("pojiagw_rule_denied_total", labels, strconv.FormatUint(hit.Denied, 10))
		m.sample("pojiagw_rule_hit_ratio", labels, formatFloat(ratio))
	}

	m.histogramFamily("pojiagw_request_duration_seconds", "网关侧请求总耗时（秒）", nil, s.store.RequestHistogram())

	upNames := map[string]string{}
	for _, u := range ups {
		upNames[u.ID] = u.Name
	}
	upHist := s.store.UpstreamHistograms()
	for _, id := range sortedKeys(upHist) {
		labels := [][2]string{{"upstream", id}, {"name", upNames[id]}}
		m.histogramFamily("pojiagw_upstream_duration_seconds", "上游调用耗时（秒）", labels, upHist[id])
	}

	m.header("pojiagw_upstream_responses_total", "上游响应码分布", "counter")
	respCodes := s.store.ResponseCodes()
	for _, id := range sortedKeys(respCodes) {
		for _, code := range sortedKeys(respCodes[id]) {
			labels := [][2]string{{"upstream", id}, {"name", upNames[id]}, {"code", code}}
			m.sample("pojiagw_upstream_responses_total", labels, strconv.FormatUint(respCodes[id][code], 10))
		}
	}

	m.header("pojiagw_requests_by_endpoint_total", "按端点统计的请求数", "counter")
	kindHits := s.store.KindHits()
	for _, kind := range sortedKeys(kindHits) {
		m.sample("pojiagw_requests_by_endpoint_total", [][2]string{{"endpoint", kind}}, strconv.FormatUint(kindHits[kind], 10))
	}

	m.header("pojiagw_replay_entries", "当前保留的拦截回放条数", "gauge")
	m.sample("pojiagw_replay_entries", nil, strconv.Itoa(s.store.ReplayCount()))

	queueSnap, ttfbSnap, relaySnap := s.store.TimingHistograms()
	m.histogramFamily("pojiagw_queue_duration_seconds", "排队耗时（选路 + 许可 + key 选择，秒）", nil, queueSnap)
	m.histogramFamily("pojiagw_upstream_ttfb_seconds", "上游首字节耗时（秒）", nil, ttfbSnap)
	m.histogramFamily("pojiagw_relay_duration_seconds", "转发响应体耗时（秒）", nil, relaySnap)

	m.header("pojiagw_upstream_weight", "上游灰度权重", "gauge")
	m.header("pojiagw_routed_total", "按模型统计的实际分流次数", "counter")
	routeHits := s.store.RouteHits()
	for _, u := range ups {
		m.sample("pojiagw_upstream_weight", [][2]string{{"upstream", u.ID}, {"name", u.Name}}, strconv.Itoa(weightOf(cfg, u.ID)))
	}
	for _, model := range sortedKeys(routeHits) {
		for _, id := range sortedKeys(routeHits[model]) {
			m.sample("pojiagw_routed_total", [][2]string{{"model", model}, {"upstream", id}, {"name", upNames[id]}}, strconv.FormatUint(routeHits[model][id], 10))
		}
	}

	m.header("pojiagw_budget_requests_used", "当日已用请求数（预算）", "gauge")
	m.header("pojiagw_budget_tokens_used", "当日已用估算 token（预算）", "gauge")
	m.header("pojiagw_budget_requests_limit", "当日请求预算上限（0 = 不限）", "gauge")
	m.header("pojiagw_budget_tokens_limit", "当日 token 预算上限（0 = 不限）", "gauge")
	m.header("pojiagw_budget_exhausted", "预算是否用尽（1/0）", "gauge")
	for _, u := range ups {
		if u.Budget == nil {
			continue
		}
		labels := [][2]string{{"upstream", u.ID}, {"name", u.Name}}
		exhausted := "0"
		if u.Budget.Exhausted {
			exhausted = "1"
		}
		m.sample("pojiagw_budget_requests_used", labels, formatUint(u.Budget.Requests))
		m.sample("pojiagw_budget_tokens_used", labels, formatUint(u.Budget.Tokens))
		m.sample("pojiagw_budget_requests_limit", labels, strconv.Itoa(u.Budget.MaxRequests))
		m.sample("pojiagw_budget_tokens_limit", labels, strconv.Itoa(u.Budget.MaxTokens))
		m.sample("pojiagw_budget_exhausted", labels, exhausted)
	}

	m.header("pojiagw_tokens_total", "按上游累计的 token 用量（来自响应 usage）", "counter")
	tokenUsage := s.store.TokenUsage()
	for _, id := range sortedKeys(tokenUsage) {
		m.sample("pojiagw_tokens_total", [][2]string{{"upstream", id}, {"name", upNames[id]}}, formatUint(tokenUsage[id]))
	}

	m.header("pojiagw_mirror_total", "镜像投递结果（ok/fail）", "counter")
	mirrorOK, mirrorFail := s.store.MirrorStats()
	for _, id := range sortedKeys(mirrorOK) {
		m.sample("pojiagw_mirror_total", [][2]string{{"upstream", id}, {"name", upNames[id]}, {"result", "ok"}}, formatUint(mirrorOK[id]))
	}
	for _, id := range sortedKeys(mirrorFail) {
		m.sample("pojiagw_mirror_total", [][2]string{{"upstream", id}, {"name", upNames[id]}, {"result", "fail"}}, formatUint(mirrorFail[id]))
	}

	m.header("pojiagw_config_upstreams", "配置里的上游数量", "gauge")
	m.sample("pojiagw_config_upstreams", nil, strconv.Itoa(len(cfg.Upstreams)))
	m.header("pojiagw_config_profiles", "配置里的策略数量", "gauge")
	m.sample("pojiagw_config_profiles", nil, strconv.Itoa(len(cfg.Profiles)))
	m.header("pojiagw_config_rules", "配置里的规则节点数量", "gauge")
	m.sample("pojiagw_config_rules", nil, strconv.Itoa(len(cfg.Rules)))
	m.header("pojiagw_state_last_save_timestamp", "状态文件最近保存时间（unix 秒，0 表示尚未保存）", "gauge")
	persist := s.store.persistInfo()
	savedAt := int64(0)
	if v, ok := persist["last_save"].(int64); ok {
		savedAt = v
	}
	m.sample("pojiagw_state_last_save_timestamp", nil, strconv.FormatInt(savedAt, 10))
	m.header("pojiagw_snapshots", "配置快照数量", "gauge")
	m.sample("pojiagw_snapshots", nil, strconv.Itoa(len(listSnapshots(cfg.SnapshotDir(s.store.ConfigPath())))))

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, m.b.String())
}

var _ = fmt.Sprintf

func weightOf(cfg *Config, id string) int {
	for _, u := range cfg.Upstreams {
		if u.ID == id {
			return u.Weight
		}
	}
	return 0
}
