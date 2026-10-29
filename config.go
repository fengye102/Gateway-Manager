package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const configVersion = 2

type ServerConfig struct {
	Bind                  string   `json:"bind"`
	Port                  int      `json:"port"`
	AccessKeys            []string `json:"access_keys"`
	AdminToken            string   `json:"admin_token"`
	EnableAdminUI         bool     `json:"enable_admin_ui"`
	MaxBodyMB             int      `json:"max_body_mb"`
	HealthIntervalSeconds int      `json:"health_interval_seconds"`
	LogLimit              int      `json:"log_limit"`
	MergeSSE              bool     `json:"merge_sse"`
	KeyStrategy           string   `json:"key_strategy"`
	KeyCooldownSeconds    int      `json:"key_cooldown_seconds"`
	PersistState          bool     `json:"persist_state"`
	PersistIntervalSecond int      `json:"persist_interval_seconds"`
	MetricsEnabled        bool     `json:"metrics_enabled"`
	SnapshotBeforeSave    bool     `json:"snapshot_before_save"`
	SnapshotLimit         int      `json:"snapshot_limit"`
	SnapshotDir           string   `json:"snapshot_dir,omitempty"`
	ReplayLimit           int      `json:"replay_limit"`
	MirrorEnabled         bool     `json:"mirror_enabled"`
	MirrorUpstream        string   `json:"mirror_upstream,omitempty"`
	MirrorRatio           int      `json:"mirror_ratio"`
	MirrorModels          []string `json:"mirror_models,omitempty"`
	MirrorTimeoutMS       int      `json:"mirror_timeout_ms"`
	SnapshotGit           bool     `json:"snapshot_git"`
	SampleEvery           int      `json:"sample_every"`
	SampleLimit           int      `json:"sample_limit"`
}

type RetryConfig struct {
	MaxRetries            int   `json:"max_retries"`
	FailureThreshold      int   `json:"failure_threshold"`
	CooldownSeconds       int   `json:"cooldown_seconds"`
	RequestTimeoutSeconds int   `json:"request_timeout_seconds"`
	RetryStatus           []int `json:"retry_status"`
	HalfOpenLimit         int   `json:"half_open_limit"`
	CooldownMaxSeconds    int   `json:"cooldown_max_seconds"`
}

type UpstreamProbe struct {
	Path           string `json:"path"`
	Method         string `json:"method,omitempty"`
	ExpectStatus   []int  `json:"expect_status,omitempty"`
	ExpectContains string `json:"expect_contains,omitempty"`
	TimeoutMS      int    `json:"timeout_ms,omitempty"`
}
type UpstreamBudget struct {
	MaxRequestsPerDay int `json:"max_requests_per_day,omitempty"`
	MaxTokensPerDay   int `json:"max_tokens_per_day,omitempty"`
}

type FederationPeer struct {
	ID         string   `json:"id"`
	Name       string   `json:"name,omitempty"`
	BaseURL    string   `json:"base_url"`
	Token      string   `json:"token,omitempty"`
	AsUpstream bool     `json:"as_upstream,omitempty"`
	AccessKey  string   `json:"access_key,omitempty"`
	Models     []string `json:"models,omitempty"`
	Priority   int      `json:"priority,omitempty"`
	Weight     int      `json:"weight,omitempty"`
}

type FederationConfig struct {
	Enabled   bool             `json:"enabled"`
	TimeoutMS int              `json:"timeout_ms,omitempty"`
	Peers     []FederationPeer `json:"peers,omitempty"`
}

type Upstream struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	BaseURL     string            `json:"base_url"`
	APIKeys     []string          `json:"api_keys"`
	Models      []string          `json:"models"`
	Mappings    map[string]string `json:"mappings,omitempty"`
	ProfileID   string            `json:"profile_id,omitempty"`
	Priority    int               `json:"priority"`
	Weight      int               `json:"weight"`
	Probe       *UpstreamProbe    `json:"probe,omitempty"`
	Budget      *UpstreamBudget   `json:"budget,omitempty"`
	ChannelID   string            `json:"channel_id,omitempty"`
	ChannelPath string            `json:"channel_path,omitempty"`
	RPM         int               `json:"rpm"`
	Concurrency int               `json:"concurrency"`
	Enabled     bool              `json:"enabled"`
	Headers     map[string]string `json:"headers,omitempty"`
}

type Profile struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Models      []string `json:"models"`
	Instruction string   `json:"instruction"`
	Enabled     bool     `json:"enabled"`
}

type Adapter struct {
	ID            string            `json:"id"`
	MatchBaseURL  string            `json:"match_base_url"`
	ChatPath      string            `json:"chat_path,omitempty"`
	MessagesPath  string            `json:"messages_path,omitempty"`
	ResponsesPath string            `json:"responses_path,omitempty"`
	ModelsPath    string            `json:"models_path,omitempty"`
	AuthHeader    string            `json:"auth_header,omitempty"`
	AuthPrefix    string            `json:"auth_prefix,omitempty"`
	ExtraHeaders  map[string]string `json:"extra_headers,omitempty"`
}

type ExperimentArm struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Weight     int    `json:"weight,omitempty"`
	ProfileID  string `json:"profile_id,omitempty"`
	UpstreamID string `json:"upstream_id,omitempty"`
}
type Experiment struct {
	ID     string          `json:"id"`
	Name   string          `json:"name,omitempty"`
	Models []string        `json:"models,omitempty"`
	Arms   []ExperimentArm `json:"arms"`
}

type Config struct {
	Version     int               `json:"version"`
	Server      ServerConfig      `json:"server"`
	Retry       RetryConfig       `json:"retry"`
	Upstreams   []Upstream        `json:"upstreams"`
	Profiles    []Profile         `json:"profiles"`
	Rules       []Rule            `json:"rules"`
	Templates   []ProfileTemplate `json:"templates,omitempty"`
	Federation  FederationConfig  `json:"federation"`
	Adapters    []Adapter         `json:"adapters,omitempty"`
	Experiments []Experiment      `json:"experiments,omitempty"`
	Channels    []Channel         `json:"channels,omitempty"`
}

func DefaultConfig() *Config {
	return &Config{
		Version: configVersion,
		Server: ServerConfig{
			Bind:                  "127.0.0.1",
			Port:                  7878,
			AccessKeys:            []string{},
			AdminToken:            "",
			EnableAdminUI:         true,
			MaxBodyMB:             32,
			HealthIntervalSeconds: 30,
			LogLimit:              500,
			MergeSSE:              true,
			KeyStrategy:           "score",
			KeyCooldownSeconds:    60,
			PersistState:          true,
			PersistIntervalSecond: 20,
			MetricsEnabled:        true,
			SnapshotBeforeSave:    true,
			SnapshotLimit:         20,
			ReplayLimit:           30,
			MirrorRatio:           0,
			MirrorTimeoutMS:       10000,
			SampleEvery:           0,
			SampleLimit:           10,
		},
		Retry: RetryConfig{
			MaxRetries:            2,
			FailureThreshold:      2,
			CooldownSeconds:       60,
			RequestTimeoutSeconds: 180,
			RetryStatus:           []int{401, 403, 408, 429},
			HalfOpenLimit:         1,
			CooldownMaxSeconds:    1800,
		},
		Upstreams:   []Upstream{},
		Profiles:    []Profile{},
		Rules:       []Rule{},
		Templates:   []ProfileTemplate{},
		Federation:  FederationConfig{Enabled: false, TimeoutMS: 3000, Peers: []FederationPeer{}},
		Adapters:    []Adapter{},
		Experiments: []Experiment{},
		Channels:    []Channel{},
	}
}

func normalizeConfig(c *Config) {
	def := DefaultConfig()
	if c.Version == 0 {
		c.Version = def.Version
	}
	if strings.TrimSpace(c.Server.Bind) == "" {
		c.Server.Bind = def.Server.Bind
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		c.Server.Port = def.Server.Port
	}
	if c.Server.MaxBodyMB <= 0 {
		c.Server.MaxBodyMB = def.Server.MaxBodyMB
	}
	if c.Server.HealthIntervalSeconds <= 0 {
		c.Server.HealthIntervalSeconds = def.Server.HealthIntervalSeconds
	}
	if c.Server.LogLimit <= 0 {
		c.Server.LogLimit = def.Server.LogLimit
	}
	if c.Server.KeyStrategy != "round_robin" {
		c.Server.KeyStrategy = "score"
	}
	if c.Server.KeyCooldownSeconds <= 0 {
		c.Server.KeyCooldownSeconds = def.Server.KeyCooldownSeconds
	}
	if c.Server.PersistIntervalSecond <= 0 {
		c.Server.PersistIntervalSecond = def.Server.PersistIntervalSecond
	}
	if c.Server.SnapshotLimit <= 0 {
		c.Server.SnapshotLimit = def.Server.SnapshotLimit
	}
	if c.Server.ReplayLimit < 0 {
		c.Server.ReplayLimit = 0
	}
	if c.Server.ReplayLimit == 0 {
		c.Server.ReplayLimit = def.Server.ReplayLimit
	}
	if c.Server.SampleEvery < 0 {
		c.Server.SampleEvery = 0
	}
	if c.Server.SampleLimit <= 0 {
		c.Server.SampleLimit = def.Server.SampleLimit
	}
	if c.Server.MirrorRatio < 0 {
		c.Server.MirrorRatio = 0
	}
	if c.Server.MirrorRatio > 100 {
		c.Server.MirrorRatio = 100
	}
	if c.Server.MirrorTimeoutMS <= 0 {
		c.Server.MirrorTimeoutMS = def.Server.MirrorTimeoutMS
	}
	if c.Retry.RequestTimeoutSeconds <= 0 {
		c.Retry.RequestTimeoutSeconds = def.Retry.RequestTimeoutSeconds
	}
	if c.Retry.MaxRetries < 0 {
		c.Retry.MaxRetries = 0
	}
	if c.Retry.FailureThreshold <= 0 {
		c.Retry.FailureThreshold = def.Retry.FailureThreshold
	}
	if c.Retry.CooldownSeconds < 0 {
		c.Retry.CooldownSeconds = 0
	}
	if len(c.Retry.RetryStatus) == 0 {
		c.Retry.RetryStatus = def.Retry.RetryStatus
	}
	if c.Retry.HalfOpenLimit <= 0 {
		c.Retry.HalfOpenLimit = def.Retry.HalfOpenLimit
	}
	if c.Retry.CooldownMaxSeconds <= 0 {
		c.Retry.CooldownMaxSeconds = def.Retry.CooldownMaxSeconds
	}
	if c.Server.AccessKeys == nil {
		c.Server.AccessKeys = []string{}
	}
	if c.Rules == nil {
		c.Rules = []Rule{}
	}
	if c.Templates == nil {
		c.Templates = []ProfileTemplate{}
	}
	if c.Adapters == nil {
		c.Adapters = []Adapter{}
	}
	if c.Channels == nil {
		c.Channels = []Channel{}
	}
	for i := range c.Upstreams {
		u := &c.Upstreams[i]
		if u.Name == "" {
			u.Name = u.ID
		}
		if u.Mappings == nil {
			u.Mappings = map[string]string{}
		}
		if strings.TrimSpace(u.ProfileID) == "null" {
			u.ProfileID = ""
		}
		if u.APIKeys == nil {
			u.APIKeys = []string{}
		}
	}
	for i := range c.Profiles {
		if c.Profiles[i].Name == "" {
			c.Profiles[i].Name = c.Profiles[i].ID
		}
	}
	for i := range c.Rules {
		r := &c.Rules[i]
		if strings.TrimSpace(r.ID) == "" {
			r.ID = fmt.Sprintf("rule-%d", i+1)
		}
		if r.Name == "" {
			r.Name = r.ID
		}
		if r.Type == "" {
			r.Type = ruleTypeInject
		}
	}
	if c.Federation.TimeoutMS <= 0 {
		c.Federation.TimeoutMS = 3000
	}
	if c.Federation.Peers == nil {
		c.Federation.Peers = []FederationPeer{}
	}
	if c.Adapters == nil {
		c.Adapters = []Adapter{}
	}
	if c.Experiments == nil {
		c.Experiments = []Experiment{}
	}
	if c.Channels == nil {
		c.Channels = []Channel{}
	}
	for i := range c.Channels {
		ch := &c.Channels[i]
		if ch.ID == "" {
			ch.ID = "channel-" + fmt.Sprint(i+1)
		}
		if ch.Name == "" {
			if preset, ok := findPreset(ch.Preset); ok {
				ch.Name = preset.Name
			} else {
				ch.Name = ch.ID
			}
		}
		if ch.Headers == nil {
			ch.Headers = map[string]string{}
		}
		if ch.Weight < 0 {
			ch.Weight = 0
		}
	}
	for i := range c.Experiments {
		e := &c.Experiments[i]
		if e.ID == "" {
			e.ID = "exp-" + fmt.Sprint(i+1)
		}
		for j := range e.Arms {
			arm := &e.Arms[j]
			if arm.ID == "" {
				arm.ID = "arm-" + fmt.Sprint(j+1)
			}
			if arm.Name == "" {
				arm.Name = arm.ID
			}
			if arm.Weight <= 0 {
				arm.Weight = 1
			}
		}
	}
	for i := range c.Adapters {
		a := &c.Adapters[i]
		if a.ID == "" {
			a.ID = "adapter-" + fmt.Sprint(i+1)
		}
		if a.AuthHeader == "" {
			a.AuthHeader = "Authorization"
		}
		if a.AuthPrefix == "" && strings.EqualFold(a.AuthHeader, "Authorization") {
			a.AuthPrefix = "Bearer "
		}
	}
	for i := range c.Templates {
		t := &c.Templates[i]
		if strings.TrimSpace(t.ID) == "" {
			t.ID = fmt.Sprintf("tpl-custom-%d", i+1)
		}
		if t.Name == "" {
			t.Name = t.ID
		}
		if t.Category == "" {
			t.Category = "自定义"
		}
		t.Builtin = false
	}
}

func ValidateConfig(c *Config) error {
	seen := map[string]bool{}
	for i, u := range c.Upstreams {
		if strings.TrimSpace(u.ID) == "" {
			return fmt.Errorf("upstreams[%d]: id 不能为空", i)
		}
		if seen[u.ID] {
			return fmt.Errorf("upstreams[%d]: id %q 重复", i, u.ID)
		}
		seen[u.ID] = true
		if strings.TrimSpace(u.BaseURL) == "" {
			return fmt.Errorf("upstreams[%d] (%s): base_url 不能为空", i, u.ID)
		}
		if !strings.HasPrefix(u.BaseURL, "http://") && !strings.HasPrefix(u.BaseURL, "https://") {
			return fmt.Errorf("upstreams[%d] (%s): base_url 必须以 http:// 或 https:// 开头", i, u.ID)
		}
		if u.Priority < 0 {
			return fmt.Errorf("upstreams[%d] (%s): priority 不能为负", i, u.ID)
		}
		if u.Weight < 0 {
			return fmt.Errorf("upstreams[%d] (%s): weight 不能为负", i, u.ID)
		}
		if u.Budget != nil {
			if u.Budget.MaxRequestsPerDay < 0 || u.Budget.MaxTokensPerDay < 0 {
				return fmt.Errorf("upstreams[%d] (%s): budget 不能为负", i, u.ID)
			}
		}
		if c.Server.MirrorEnabled && strings.TrimSpace(c.Server.MirrorUpstream) == "" {
			return fmt.Errorf("server.mirror_enabled=true 时必须指定 mirror_upstream")
		}
		if u.Probe != nil {
			if strings.TrimSpace(u.Probe.Path) == "" {
				return fmt.Errorf("upstreams[%d] (%s): probe.path 不能为空", i, u.ID)
			}
			switch strings.ToUpper(strings.TrimSpace(u.Probe.Method)) {
			case "", "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
			default:
				return fmt.Errorf("upstreams[%d] (%s): probe.method 不支持 %q", i, u.ID, u.Probe.Method)
			}
			for _, code := range u.Probe.ExpectStatus {
				if code < 100 || code > 599 {
					return fmt.Errorf("upstreams[%d] (%s): probe.expect_status 含非法状态码 %d", i, u.ID, code)
				}
			}
		}
		for _, k := range u.APIKeys {
			if strings.Contains(k, " ") {
				return fmt.Errorf("upstreams[%d] (%s): api key 里不应包含空格", i, u.ID)
			}
		}
	}
	pseen := map[string]bool{}
	for i, p := range c.Profiles {
		if strings.TrimSpace(p.ID) == "" {
			return fmt.Errorf("profiles[%d]: id 不能为空", i)
		}
		if pseen[p.ID] {
			return fmt.Errorf("profiles[%d]: id %q 重复", i, p.ID)
		}
		pseen[p.ID] = true
	}
	profIDs := map[string]bool{}
	for _, p := range c.Profiles {
		profIDs[p.ID] = true
	}
	for i, u := range c.Upstreams {
		if u.ProfileID != "" && !profIDs[u.ProfileID] {
			return fmt.Errorf("upstreams[%d] (%s): profile_id %q 不存在", i, u.ID, u.ProfileID)
		}
	}
	if err := validateRules(c.Rules); err != nil {
		return err
	}
	for i, r := range c.Rules {
		if r.Type == ruleTypeInject && r.ProfileID != "" && !profIDs[r.ProfileID] {
			return fmt.Errorf("rules[%d] (%s): profile_id %q 不存在", i, r.ID, r.ProfileID)
		}
	}
	seenChannel := map[string]bool{}
	for i, ch := range c.Channels {
		if strings.TrimSpace(ch.ID) == "" {
			return fmt.Errorf("channels[%d]: id 不能为空", i)
		}
		if seenChannel[ch.ID] {
			return fmt.Errorf("channels[%d]: id %q 重复", i, ch.ID)
		}
		seenChannel[ch.ID] = true
		if _, ok := findPreset(ch.Preset); !ok {
			return fmt.Errorf("channels[%d] (%s): 未知渠道模板 %q", i, ch.ID, ch.Preset)
		}
	}
	for i, exp := range c.Experiments {
		if len(exp.Arms) < 2 {
			return fmt.Errorf("experiments[%d] (%s): 至少需要 2 个 arm", i, exp.ID)
		}
	}
	for i, adapter := range c.Adapters {
		if strings.TrimSpace(adapter.MatchBaseURL) == "" {
			return fmt.Errorf("adapters[%d] (%s): match_base_url 不能为空", i, adapter.ID)
		}
	}
	for i, peer := range c.Federation.Peers {
		if strings.TrimSpace(peer.ID) == "" {
			return fmt.Errorf("federation.peers[%d]: id 不能为空", i)
		}
		if !strings.HasPrefix(peer.BaseURL, "http://") && !strings.HasPrefix(peer.BaseURL, "https://") {
			return fmt.Errorf("federation.peers[%d] (%s): base_url 必须以 http:// 或 https:// 开头", i, peer.ID)
		}
	}
	if len(c.Server.AccessKeys) > 0 {
		for _, k := range c.Server.AccessKeys {
			if strings.TrimSpace(k) == "" {
				return errors.New("access_keys 里存在空 key")
			}
		}
	}
	return nil
}

func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := DefaultConfig()
	if err := json.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	normalizeConfig(c)
	return c, nil
}

func SaveConfig(path string, c *Config) error {
	normalizeConfig(c)
	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func SampleConfig() *Config {
	c := DefaultConfig()
	c.Upstreams = []Upstream{
		{
			ID:      "local-gw",
			Name:    "本机另一个网关",
			BaseURL: "http://127.0.0.1:8080",
			APIKeys: []string{},
			Models:  []string{},
			Enabled: true,
		},
		{
			ID:          "remote",
			Name:        "远端 OpenAI 兼容服务",
			BaseURL:     "https://api.example.com",
			APIKeys:     []string{"sk-aaaa", "sk-bbbb", "sk-cccc"},
			Models:      []string{"gpt-4o-mini", "deepseek-chat"},
			Mappings:    map[string]string{"fast": "gpt-4o-mini", "coder": "deepseek-chat"},
			Priority:    10,
			Weight:      8,
			Probe:       &UpstreamProbe{Path: "/v1/models", Method: "GET", ExpectStatus: []int{200}, TimeoutMS: 3000},
			RPM:         60,
			Concurrency: 4,
			Enabled:     true,
		},
	}
	c.Profiles = []Profile{
		{
			ID:          "default",
			Name:        "默认策略",
			Instruction: "你是一个严谨的工程助手。回答前先确认事实，不确定就直说不确定。",
			Enabled:     true,
		},
		{
			ID:          "coder",
			Name:        "编码策略",
			Models:      []string{"deepseek-chat", "coder"},
			Instruction: "你是资深工程师。给出可直接运行的完整代码，不要省略。",
			Enabled:     true,
		},
	}
	c.Rules = []Rule{
		{
			ID:          "rule-tag",
			Name:        "给所有请求打标记",
			Type:        ruleTypeHeader,
			Cond:        nil,
			HeaderName:  "X-Gateway",
			HeaderValue: "gateway-manager",
		},
		{
			ID:          "rule-brief-mini",
			Name:        "mini 模型走极简风格",
			Type:        ruleTypeInject,
			Cond:        &RuleCond{Models: []string{"mini"}},
			Instruction: "答案控制在 3 句以内，先给结论。",
		},
		{
			ID:       "rule-deny-empty",
			Name:     "拦截空 messages",
			Type:     ruleTypeDeny,
			Disabled: true,
			Cond:     &RuleCond{BodyContains: "\"messages\":[]"},
			Status:   400,
			Message:  "空 messages 请求已被规则拦截",
		},
	}
	c.Templates = []ProfileTemplate{
		{
			ID:          "tpl-custom-mine",
			Name:        "我自己的口吻",
			Category:    "自定义",
			Instruction: "用第一人称、口语化、简短句子回答；不确定就说不确定。",
			Tags:        []string{"个人"},
		},
	}
	c.Adapters = []Adapter{
		{
			ID:           "anthropic-style",
			MatchBaseURL: "https://api.anthropic.com",
			MessagesPath: "/v1/messages",
			ModelsPath:   "/v1/models",
			AuthHeader:   "x-api-key",
			AuthPrefix:   "",
			ExtraHeaders: map[string]string{"anthropic-version": "2023-06-01"},
		},
	}
	c.Channels = []Channel{
		{
			ID: "zhipu", Preset: "zhipu-autoclaw", Name: "智谱 AutoGLM",
			Token: "", BaseURL: "", Models: []string{"glm-5"}, Headers: map[string]string{},
			Priority: 5, Weight: 0, Enabled: false,
		},
		{
			ID: "zen", Preset: "opencode-zen", Name: "OpenCode Zen 匿名档",
			Token: "public", BaseURL: "", Models: []string{}, Headers: map[string]string{},
			Priority: 10, Weight: 0, Enabled: true,
		},
	}
	return c
}
